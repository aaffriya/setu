package atomberg

import (
	"context"
	"fmt"
	"net"
	"sync"
	"syscall"
	"time"

	"setu/internal/resolver"
)

// Discoverer is the brand's ear on the network: one UDP socket bound to the
// beacon port, shared by every fan and by the device scan.
//
// This differs from the other brands on purpose. WiZ asks a question and reads
// the answers, so it opens a socket per exchange. An Atomberg fan is not asked
// anything — it broadcasts, once a second, to a fixed port — so the port can
// only be bound once, and a per-device socket would mean every fan competing
// for the same datagrams. One listener, shared, is both simpler and correct.
//
// It fills both address seams from that single stream:
//
//	Lookup — resolver.Resolver: the source address of a fan's beacon is its IP,
//	         so a fan is located without ARP and a DHCP change corrects itself
//	         within a second.
//	Scan   — resolver.Scanner: every MAC heard recently, for the UI's device
//	         scan.
//
// It also pushes: a fan registers a callback, and because the hardware
// broadcasts its state after any change — including one made with the physical
// remote or the vendor app — those changes reach the UI without waiting for a
// poll.
type Discoverer struct {
	addr *net.UDPAddr // nil = the real beacon port (tests point it at loopback)

	start sync.Once
	err   error

	mu       sync.Mutex
	fans     map[string]*sighting // MAC (colon form) → what was last heard
	watch    map[string]watcher   // MAC → the fan's push callback
	watchSeq uint64               // distinguishes successive registrations for one MAC
}

// watcher is one device's push callback. seq identifies the registration, so a
// replaced device cannot unregister the instance that succeeded it.
type watcher struct {
	seq    uint64
	notify func(Snapshot)
}

// sighting is what the listener remembers about one fan on the segment.
type sighting struct {
	ip        net.IP
	model     string
	seen      time.Time
	messageID string
	state     fanState
	// rev counts state broadcasts actually adopted (retransmits excluded). A
	// poll watches it rather than message_id: it cannot be empty, cannot repeat,
	// and needs no assumption about how the vendor fills that field.
	rev uint64
}

// Snapshot is one decoded state broadcast handed to a fan's callback.
type Snapshot struct {
	State     fanState
	MessageID string
}

const (
	// beaconPort is where fans broadcast beacons and state.
	beaconPort = 5625
	// commandPort is where a fan listens for commands.
	commandPort = 5600
	// freshness bounds how long a fan is considered present after its last
	// beacon. Beacons arrive every second, so this tolerates a long run of
	// dropped datagrams before a fan is called gone.
	freshness = 15 * time.Second
	// beaconWait is how long Lookup waits for a fan that is not in the table
	// yet. Nothing can be asked of the network here — the fan speaks on its own
	// schedule — so the only options are to wait one beacon interval or to fail
	// a command that would have worked a moment later. Without this the first
	// command after a restart fails, because the table is still cold.
	beaconWait = 3 * time.Second
	// maxFans caps the sighting table. The segment feeds this map, not Setu's
	// configuration, so it needs a ceiling of its own on router hardware.
	maxFans = 64
	// readBuffer is generously above the largest observed state datagram.
	readBuffer = 4096
)

var (
	_ resolver.Resolver = (*Discoverer)(nil)
	_ resolver.Scanner  = (*Discoverer)(nil)
)

// shared is the process-wide listener. The beacon port can only be bound once,
// so every fan and the scanner must use the same one — which also keeps
// registration in the composition root to the usual single line.
var (
	sharedOnce sync.Once
	sharedInst *Discoverer
)

// NewDiscoverer returns the shared listener, starting it on first use.
func NewDiscoverer() *Discoverer {
	sharedOnce.Do(func() { sharedInst = newDiscoverer(nil) })
	return sharedInst
}

// newDiscoverer builds an unstarted listener. Tests use it directly to get an
// isolated instance on a loopback port.
func newDiscoverer(addr *net.UDPAddr) *Discoverer {
	return &Discoverer{
		addr:  addr,
		fans:  make(map[string]*sighting),
		watch: make(map[string]watcher),
	}
}

// ensure starts the receive loop once. A failure to bind is remembered and
// returned to every caller: without the beacon port there is no discovery, no
// resolution and no state, so it must surface rather than look like silence.
func (d *Discoverer) ensure() error {
	d.start.Do(func() {
		addr := d.addr
		if addr == nil {
			addr = &net.UDPAddr{IP: net.IPv4zero, Port: beaconPort}
		}
		conn, err := listenShared(addr)
		if err != nil {
			d.err = fmt.Errorf("atomberg discovery: listen on :%d: %w", addr.Port, err)
			return
		}
		if d.addr != nil {
			// Tests need the port the kernel actually chose.
			d.addr = conn.LocalAddr().(*net.UDPAddr)
		}
		go d.receive(conn)
	})
	return d.err
}

// receive is the single reader. It runs for the life of the process: the
// listener is shared by every fan, so there is no per-device lifetime to tie it
// to, and a fan added later must find the table already warm.
func (d *Discoverer) receive(conn *net.UDPConn) {
	defer conn.Close()
	buf := make([]byte, readBuffer)
	for {
		n, addr, err := conn.ReadFromUDP(buf)
		if err != nil {
			return // the socket is closed; nothing left to read
		}
		d.absorb(addr.IP, buf[:n])
	}
}

// absorb classifies and records one datagram. Anything unrecognised is dropped
// silently: this is a broadcast port on a home network, so foreign traffic is
// expected and is not an error.
func (d *Discoverer) absorb(src net.IP, datagram []byte) {
	payload := stripProxy(datagram)
	if len(payload) == 0 {
		return
	}

	if msg, isState, err := parseState(payload); isState {
		if err != nil {
			return
		}
		mac, err := resolver.NormalizeMAC(msg.DeviceID)
		if err != nil {
			return
		}
		state, err := decodeStateString(msg.StateString)
		if err != nil {
			return
		}
		d.recordState(resolver.FormatMAC(mac), src, msg.MessageID, state)
		return
	}

	if b, ok := parseBeacon(payload); ok {
		d.recordBeacon(b, src)
	}
}

// recordBeacon notes that a fan is present at an address.
func (d *Discoverer) recordBeacon(b beacon, src net.IP) {
	d.mu.Lock()
	defer d.mu.Unlock()
	entry := d.entryLocked(b.MAC)
	if entry == nil {
		return
	}
	entry.ip = append(net.IP(nil), src...)
	entry.model = b.Model
	entry.seen = time.Now()
}

// recordState stores a decoded broadcast and pushes it to the fan that owns it.
// Retransmits of a message already seen are dropped, so one physical change
// does not publish several identical events.
func (d *Discoverer) recordState(mac string, src net.IP, messageID string, state fanState) {
	d.mu.Lock()
	entry := d.entryLocked(mac)
	if entry == nil {
		d.mu.Unlock()
		return
	}
	duplicate := messageID != "" && messageID == entry.messageID
	entry.ip = append(net.IP(nil), src...)
	entry.seen = time.Now()
	entry.messageID = messageID
	if !duplicate {
		entry.state = state
		entry.rev++
	}
	if state.Model != "" {
		entry.model = state.Model
	}
	notify := d.watch[mac].notify
	d.mu.Unlock()

	if duplicate || notify == nil {
		return
	}
	notify(Snapshot{State: state, MessageID: messageID})
}

// entryLocked returns the sighting for a MAC, creating it if there is room. The
// segment feeds this map, not Setu's configuration, so it needs a ceiling of
// its own on router hardware.
//
// At the cap, fans that have gone quiet are dropped first. Without that, a
// table filled once — by a busy segment, or by devices that have since left —
// would lock out a real fan that only appears later, permanently.
func (d *Discoverer) entryLocked(mac string) *sighting {
	if entry, ok := d.fans[mac]; ok {
		return entry
	}
	if len(d.fans) >= maxFans {
		d.evictStaleLocked()
	}
	if len(d.fans) >= maxFans {
		return nil
	}
	entry := &sighting{}
	d.fans[mac] = entry
	return entry
}

// evictStaleLocked drops fans that have not beaconed within the freshness
// window. A live fan announces itself every second, so anything stale is gone.
func (d *Discoverer) evictStaleLocked() {
	for mac, entry := range d.fans {
		if time.Since(entry.seen) > freshness {
			delete(d.fans, mac)
		}
	}
}

// watchFan registers a fan's push callback and returns a function that removes
// it. Called once per device, from its constructor.
//
// The returned function removes *this* registration only. Editing a device
// rebuilds it, and the rebuilt instance registers before the old one is closed
// — so an unwatch that deleted by MAC alone would take the new registration
// with it and silently end push updates until a restart.
func (d *Discoverer) watchFan(mac string, notify func(Snapshot)) func() {
	key := watchKey(mac)
	d.mu.Lock()
	d.watchSeq++
	seq := d.watchSeq
	d.watch[key] = watcher{seq: seq, notify: notify}
	d.mu.Unlock()
	return func() {
		d.mu.Lock()
		if current, ok := d.watch[key]; ok && current.seq == seq {
			delete(d.watch, key)
		}
		d.mu.Unlock()
	}
}

// watchKey canonicalises a MAC for the watcher table. A MAC that cannot be
// parsed is kept verbatim: it will simply never match a beacon, which is the
// right outcome for a device whose identity is malformed.
func watchKey(mac string) string {
	normalized, err := resolver.NormalizeMAC(mac)
	if err != nil {
		return mac
	}
	return resolver.FormatMAC(normalized)
}

// latest returns the last state heard from a fan and the revision it arrived
// on, plus whether any state has been heard at all. Fans only broadcast after a
// change, so this is empty until the first command or the first poll nudge.
func (d *Discoverer) latest(mac string) (state fanState, rev uint64, ok bool) {
	key, err := resolver.NormalizeMAC(mac)
	if err != nil {
		return fanState{}, 0, false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	entry, found := d.fans[resolver.FormatMAC(key)]
	if !found || entry.rev == 0 {
		return fanState{}, 0, false
	}
	return entry.state, entry.rev, true
}

// present reports whether a fan has beaconed recently. This is liveness for
// free: a fan that is powered and on the network says so every second, so a
// device can be shown online without any traffic from Setu.
func (d *Discoverer) present(mac string) bool {
	key, err := resolver.NormalizeMAC(mac)
	if err != nil {
		return false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	entry, ok := d.fans[resolver.FormatMAC(key)]
	return ok && time.Since(entry.seen) <= freshness
}

// Lookup implements resolver.Resolver: the address a fan's beacon came from.
//
// When the fan is not in the table yet it waits one beacon interval rather than
// failing straight away. That matters right after a restart: the table is cold,
// and without the wait the first command a user sends would fail even though
// the fan is present and about to announce itself.
func (d *Discoverer) Lookup(mac string) (net.IP, error) {
	want, err := resolver.NormalizeMAC(mac)
	if err != nil {
		return nil, err
	}
	if err := d.ensure(); err != nil {
		return nil, err
	}
	key := resolver.FormatMAC(want)
	deadline := time.Now().Add(beaconWait)
	for {
		if ip, ok := d.addressOf(key); ok {
			return ip, nil
		}
		if !time.Now().Before(deadline) {
			return nil, fmt.Errorf("atomberg discovery: no beacon from mac %s", want)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// addressOf returns a fan's last known address, if it beaconed recently enough.
func (d *Discoverer) addressOf(key string) (net.IP, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	entry, ok := d.fans[key]
	if !ok || entry.ip == nil || time.Since(entry.seen) > freshness {
		return nil, false
	}
	return append(net.IP(nil), entry.ip...), true
}

// Scan implements resolver.Scanner. Unlike a broadcast-and-wait scan there is
// nothing to ask: the fans are already talking, so a scan reports what has been
// heard, waiting only long enough for one beacon interval if the table is cold.
func (d *Discoverer) Scan(ctx context.Context) ([]resolver.Candidate, error) {
	if err := d.ensure(); err != nil {
		return nil, err
	}
	// A scan run moments after start-up would otherwise report nothing at all.
	// Beacons arrive every second, so a short wait is enough to hear every fan.
	if !d.anyFresh() {
		select {
		case <-ctx.Done():
			return nil, nil
		case <-time.After(2 * time.Second):
		}
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	found := make([]resolver.Candidate, 0, len(d.fans))
	for mac, entry := range d.fans {
		if entry.ip == nil || time.Since(entry.seen) > freshness {
			continue
		}
		found = append(found, resolver.Candidate{
			Brand:  Brand,
			Driver: driverFor(entry.model),
			Model:  entry.model,
			MAC:    mac,
			IP:     entry.ip.String(),
		})
	}
	return found, nil
}

// anyFresh reports whether any fan has beaconed recently. Stale entries do not
// count: a table holding only fans that have left should still wait, or the
// scan reports nothing without ever having listened.
func (d *Discoverer) anyFresh() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, entry := range d.fans {
		if time.Since(entry.seen) <= freshness {
			return true
		}
	}
	return false
}

// commandPortOverride points commands at a fake fan on a loopback port. Zero —
// always, outside tests — means the real command port.
var commandPortOverride int

// send delivers one command datagram to a fan. The protocol has no reply and no
// acknowledgement: the fan's own state broadcast afterwards is the confirmation.
func send(ip net.IP, payload []byte) error {
	port := commandPort
	if commandPortOverride != 0 {
		port = commandPortOverride
	}
	conn, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: ip, Port: port})
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := conn.SetWriteDeadline(time.Now().Add(2 * time.Second)); err != nil {
		return err
	}
	_, err = conn.Write(payload)
	return err
}

// listenShared binds the beacon port with the reuse options set before bind, so
// Setu can coexist with anything else on the host that watches the same
// broadcast (the vendor's own app, or a second Setu instance).
func listenShared(addr *net.UDPAddr) (*net.UDPConn, error) {
	cfg := net.ListenConfig{
		Control: func(_, _ string, c syscall.RawConn) error {
			var serr error
			if err := c.Control(func(fd uintptr) {
				serr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1)
				// SO_REUSEPORT is a bonus, not a requirement: it lets a second
				// process (the vendor's own app, another Setu) watch the same
				// broadcast. A kernel without it (or a platform Setu doesn't ship
				// a value for, see reuseport_*.go) must not cost us the whole
				// brand, so the error is deliberately ignored.
				if soReusePort != 0 {
					_ = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, soReusePort, 1)
				}
			}); err != nil {
				return err
			}
			return serr
		},
	}
	packet, err := cfg.ListenPacket(context.Background(), "udp4", addr.String())
	if err != nil {
		return nil, err
	}
	conn, ok := packet.(*net.UDPConn)
	if !ok {
		_ = packet.Close()
		return nil, fmt.Errorf("atomberg discovery: unexpected listener type %T", packet)
	}
	return conn, nil
}
