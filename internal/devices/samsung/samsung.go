// Package samsung controls Samsung Tizen Smart TVs (e.g. the AU7700 series).
//
// A TV spans three transports, all implemented here with the standard library
// plus our existing WebSocket dependency:
//
//   - HTTP REST (DIAL) on :8001 — used here for reachability (Poll).
//   - WebSocket over TLS on :8002 — remote keys (power off, volume, navigation).
//     Token-authenticated: the TV returns a token after the user taps "Allow"
//     once; we persist it and reuse it.
//   - Wake-on-LAN (UDP) — power on (unreliable over Wi-Fi; see On).
//
// It implements the Switchable, Volume, and KeyControl capabilities. This is the
// blueprint (internal/devices/example) applied to a non-light device, showing
// how new capabilities light up matching UI controls automatically.
package samsung

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/coder/websocket"

	"setu/internal/config"
	"setu/internal/device"
	"setu/internal/events"
	"setu/internal/resolver"
)

const (
	Brand      = "Samsung"
	ModelTizen = "tizen"

	restPort       = "8001"
	wsPort         = "8002"
	appName        = "setu-remote" // shown in the TV's Device Connection Manager
	defaultTimeout = 4 * time.Second
	pairTimeout    = 15 * time.Second       // first connect: time for the user to tap "Allow"
	flushDelay     = 500 * time.Millisecond // let a key frame flush before closing the socket
)

// keyPattern restricts remote keys to the documented KEY_* form, so arbitrary
// strings can't be funneled to the TV.
var keyPattern = regexp.MustCompile(`^KEY_[A-Z0-9_]+$`)

// tvApps are the streaming apps we surface as one-tap shortcuts. IDs are
// Samsung's DIAL application ids (see docs/devices/samsung.md §6); they can
// change per firmware release. LaunchApp only accepts an id from this set.
var tvApps = []device.App{
	{ID: "111299001912", Name: "YouTube"},
	{ID: "3201907018807", Name: "Netflix"},
	{ID: "3201512006785", Name: "Prime Video"},
}

// base is the shared Samsung brand foundation: identity, IP resolution, the
// shared HTTP client (REST + WS dial), and the pairing token.
type base struct {
	id, name, mac, ipHint string
	arp                   resolver.Resolver
	bus                   *events.Bus
	http                  *http.Client
	timeout               time.Duration
	tokenPath             string

	mu    sync.Mutex
	ip    net.IP
	token string
	state device.State
}

func (b *base) ID() string    { return b.id }
func (b *base) Name() string  { return b.name }
func (b *base) Brand() string { return Brand }
func (b *base) MAC() string   { return b.mac }

func (b *base) State() device.State {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state
}

// resolveIP: cached → injected ARP resolver → config hint. (Samsung has no
// simple broadcast discovery like WiZ; SSDP/mDNS would slot in here later.)
func (b *base) resolveIP() (net.IP, error) {
	b.mu.Lock()
	cached := b.ip
	b.mu.Unlock()
	if cached != nil {
		return cached, nil
	}
	if b.arp != nil {
		if ip, err := b.arp.Lookup(b.mac); err == nil {
			b.setIP(ip)
			return ip, nil
		}
	}
	if b.ipHint != "" {
		if ip := net.ParseIP(b.ipHint); ip != nil {
			b.setIP(ip)
			return ip, nil
		}
	}
	return nil, fmt.Errorf("samsung %s: cannot resolve ip for mac %s", b.id, b.mac)
}

func (b *base) setIP(ip net.IP) { b.mu.Lock(); b.ip = ip; b.mu.Unlock() }
func (b *base) invalidateIP()   { b.mu.Lock(); b.ip = nil; b.mu.Unlock() }

func (b *base) applyState(mutate func(*device.State)) {
	b.mu.Lock()
	mutate(&b.state)
	snap := b.state
	b.mu.Unlock()
	if b.bus != nil {
		b.bus.Publish(events.Event{Type: events.StateChanged, DeviceID: b.id, State: snap})
	}
}

func (b *base) updateState(mutate func(*device.State)) {
	b.mu.Lock()
	mutate(&b.state)
	b.mu.Unlock()
}

// --- token persistence -----------------------------------------------------

func (b *base) getToken() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.token
}

func (b *base) loadToken() {
	if data, err := os.ReadFile(b.tokenPath); err == nil {
		b.mu.Lock()
		b.token = strings.TrimSpace(string(data))
		b.mu.Unlock()
	}
}

func (b *base) saveToken(tok string) {
	b.mu.Lock()
	changed := tok != b.token
	b.token = tok
	b.mu.Unlock()
	if changed {
		_ = os.WriteFile(b.tokenPath, []byte(tok), 0o600)
	}
}

// --- REST (reachability) ----------------------------------------------------

// reachable reports whether the TV answers its DIAL REST endpoint.
func (b *base) reachable(ctx context.Context) bool {
	ip, err := b.resolveIP()
	if err != nil {
		return false
	}
	u := fmt.Sprintf("http://%s/api/v2/", net.JoinHostPort(ip.String(), restPort))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return false
	}
	resp, err := b.http.Do(req)
	if err != nil {
		b.invalidateIP()
		return false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode == http.StatusOK
}

// --- WebSocket remote keys --------------------------------------------------

// sendKey opens a token-authenticated WebSocket to the TV, captures/refreshes
// the pairing token from the connect event, sends one "Click" key, and closes.
func (b *base) sendKey(ctx context.Context, key string) error {
	ip, err := b.resolveIP()
	if err != nil {
		return err
	}

	q := url.Values{}
	q.Set("name", base64.StdEncoding.EncodeToString([]byte(appName)))
	if tok := b.getToken(); tok != "" {
		q.Set("token", tok)
	}
	u := url.URL{
		Scheme:   "wss",
		Host:     net.JoinHostPort(ip.String(), wsPort),
		Path:     "/api/v2/channels/samsung.remote.control",
		RawQuery: q.Encode(),
	}

	dialCtx, cancel := context.WithTimeout(ctx, b.timeout)
	defer cancel()
	c, _, err := websocket.Dial(dialCtx, u.String(), &websocket.DialOptions{HTTPClient: b.http})
	if err != nil {
		b.invalidateIP()
		return fmt.Errorf("samsung %s: ws dial: %w", b.id, err)
	}
	defer c.Close(websocket.StatusNormalClosure, "")

	// The first server message is ms.channel.connect, carrying the (new or
	// refreshed) token. On first-ever pairing this arrives only after the user
	// taps "Allow" on the TV, so we wait a bit longer.
	connCtx, cancelConn := context.WithTimeout(ctx, pairTimeout)
	defer cancelConn()
	if _, data, err := c.Read(connCtx); err == nil {
		var ev struct {
			Event string `json:"event"`
			Data  struct {
				Token string `json:"token"`
			} `json:"data"`
		}
		if json.Unmarshal(data, &ev) == nil && ev.Data.Token != "" {
			b.saveToken(ev.Data.Token)
		}
	}

	cmd, err := json.Marshal(map[string]any{
		"method": "ms.remote.control",
		"params": map[string]string{
			"Cmd":          "Click",
			"DataOfCmd":    key,
			"Option":       "false",
			"TypeOfRemote": "SendRemoteKey",
		},
	})
	if err != nil {
		return err
	}
	writeCtx, cancelWrite := context.WithTimeout(ctx, b.timeout)
	defer cancelWrite()
	if err := c.Write(writeCtx, websocket.MessageText, cmd); err != nil {
		return fmt.Errorf("samsung %s: send key %s: %w", b.id, key, err)
	}
	// Let the frame flush before the deferred close. Samsung drops the command
	// if the socket closes too soon; the reference clients use ~500 ms.
	time.Sleep(flushDelay)
	return nil
}

// sendKeyNow runs sendKey with its own timeout context.
func (b *base) sendKeyNow(key string) error {
	ctx, cancel := context.WithTimeout(context.Background(), pairTimeout+b.timeout)
	defer cancel()
	return b.sendKey(ctx, key)
}

// --- Wake-on-LAN ------------------------------------------------------------

// wakeOnLAN broadcasts a magic packet to the TV's MAC.
func (b *base) wakeOnLAN() error {
	norm, err := resolver.NormalizeMAC(b.mac)
	if err != nil {
		return err
	}
	hw, err := hex.DecodeString(norm)
	if err != nil || len(hw) != 6 {
		return fmt.Errorf("samsung %s: bad mac %q", b.id, b.mac)
	}
	packet := make([]byte, 0, 6+16*6)
	for i := 0; i < 6; i++ {
		packet = append(packet, 0xff)
	}
	for i := 0; i < 16; i++ {
		packet = append(packet, hw...)
	}

	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := enableBroadcast(conn); err != nil {
		return err
	}

	// Spray the magic packet at the limited broadcast and every interface's
	// directed broadcast (e.g. 192.168.0.255), on the two common WoL ports.
	// Directed broadcast is more reliable than 255.255.255.255 for same-subnet
	// WoL. (Note: a Wi-Fi TV in standby may still not honour WoL — see README.)
	sent := 0
	for _, ip := range broadcastIPs() {
		for _, port := range []int{9, 7} {
			if _, err := conn.WriteToUDP(packet, &net.UDPAddr{IP: ip, Port: port}); err == nil {
				sent++
			}
		}
	}
	if sent == 0 {
		return fmt.Errorf("samsung %s: wake-on-lan: no broadcast target reachable", b.id)
	}
	return nil
}

// broadcastIPs returns the limited broadcast plus each non-loopback IPv4
// interface's directed broadcast address (host bits set to 1).
func broadcastIPs() []net.IP {
	out := []net.IP{net.IPv4bcast}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return out
	}
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		ip4 := ipnet.IP.To4()
		if ip4 == nil || ip4.IsLoopback() {
			continue
		}
		mask := ipnet.Mask
		if len(mask) == 16 {
			mask = mask[12:]
		}
		if len(mask) != 4 {
			continue
		}
		out = append(out, net.IP{
			ip4[0] | ^mask[0], ip4[1] | ^mask[1], ip4[2] | ^mask[2], ip4[3] | ^mask[3],
		})
	}
	return out
}

func enableBroadcast(conn *net.UDPConn) error {
	raw, err := conn.SyscallConn()
	if err != nil {
		return err
	}
	var serr error
	if err := raw.Control(func(fd uintptr) {
		serr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_BROADCAST, 1)
	}); err != nil {
		return err
	}
	return serr
}

// ---------------------------------------------------------------------------
// TV: the Tizen TV model — power, volume, and arbitrary remote keys.
// ---------------------------------------------------------------------------

type TV struct {
	base
}

var (
	_ device.Device     = (*TV)(nil)
	_ device.Switchable = (*TV)(nil)
	_ device.Volume     = (*TV)(nil)
	_ device.KeyControl = (*TV)(nil)
	_ device.AppControl = (*TV)(nil)
	_ device.Pollable   = (*TV)(nil)
)

func (t *TV) Model() string { return ModelTizen }

func (t *TV) Capabilities() []string {
	return []string{device.CapSwitch, device.CapVolume, device.CapKey, device.CapApp}
}

// On powers the TV on via Wake-on-LAN. NOTE: WoL is unreliable over Wi-Fi (this
// model is wireless); if it doesn't wake, the next poll corrects the On state
// (Online stays true — see Poll — so the power control remains usable to retry).
func (t *TV) On() error {
	if err := t.wakeOnLAN(); err != nil {
		return err
	}
	t.applyState(func(s *device.State) { s.Online = true; s.On = true })
	return nil
}

// Off powers the TV off via the remote KEY_POWER (reliable when the TV is on).
// The TV stays Online (off ≠ offline): an off TV no longer answers REST but can
// still be woken by Wake-on-LAN, so we keep it controllable.
func (t *TV) Off() error {
	if err := t.sendKeyNow("KEY_POWER"); err != nil {
		return err
	}
	t.applyState(func(s *device.State) { s.Online = true; s.On = false })
	return nil
}

func (t *TV) VolumeUp() error   { return t.sendKeyNow("KEY_VOLUP") }
func (t *TV) VolumeDown() error { return t.sendKeyNow("KEY_VOLDOWN") }
func (t *TV) ToggleMute() error { return t.sendKeyNow("KEY_MUTE") }

// SendKey sends an arbitrary validated remote key (e.g. KEY_HOME, KEY_UP).
func (t *TV) SendKey(key string) error {
	if !keyPattern.MatchString(key) {
		return fmt.Errorf("samsung %s: invalid key %q", t.id, key)
	}
	if key == "KEY_FACTORY" {
		return fmt.Errorf("samsung %s: refusing service-menu key", t.id)
	}
	return t.sendKeyNow(key)
}

// Apps lists the launchable streaming apps exposed as UI shortcuts.
func (t *TV) Apps() []device.App { return tvApps }

// LaunchApp opens an app by its DIAL id over REST (POST /api/v2/applications/<id>;
// see docs/devices/samsung.md §2). The id must be one of Apps(); the first launch
// of an app shows a one-time "Allow" prompt on the TV.
func (t *TV) LaunchApp(id string) error {
	known := false
	for _, a := range tvApps {
		if a.ID == id {
			known = true
			break
		}
	}
	if !known {
		return fmt.Errorf("samsung %s: unknown app %q", t.id, id)
	}

	ip, err := t.resolveIP()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), t.timeout)
	defer cancel()
	u := fmt.Sprintf("http://%s/api/v2/applications/%s", net.JoinHostPort(ip.String(), restPort), url.PathEscape(id))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, nil)
	if err != nil {
		return err
	}
	resp, err := t.http.Do(req)
	if err != nil {
		t.invalidateIP()
		return fmt.Errorf("samsung %s: launch app %s: %w", t.id, id, err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("samsung %s: launch app %s: status %d", t.id, id, resp.StatusCode)
	}
	// A successful launch means the TV is on and reachable.
	t.applyState(func(s *device.State) { s.Online = true; s.On = true })
	return nil
}

// Poll reflects the TV's power state. REST reachability is a *power* proxy, not
// a presence proxy: an off TV stops answering REST, but it can still be woken by
// Wake-on-LAN, so reporting it "offline" would only hide the power control
// needed to wake it. We therefore treat the TV as Online whenever its address
// resolves (config hint / ARP — WoL works by MAC regardless), and use
// reachability only to force the On state off; the on state otherwise follows
// the last command. (We can't read the TV's volume.)
func (t *TV) Poll() (device.State, error) {
	ctx, cancel := context.WithTimeout(context.Background(), t.timeout)
	defer cancel()

	_, err := t.resolveIP()
	known := err == nil
	reachable := known && t.reachable(ctx)
	t.updateState(func(s *device.State) {
		s.Online = known
		if !reachable {
			s.On = false // not answering REST ⇒ off (a command sets it back on)
		}
	})
	return t.State(), nil
}

// New builds a Samsung TV from its config entry (matches config.Constructor).
func New(spec config.DeviceSpec, deps config.Deps) (device.Device, error) {
	// The TV serves its WebSocket/HTTPS with a self-signed cert, so we must skip
	// verification. The target is a specific LAN device resolved from its MAC.
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // self-signed TV cert
	}
	// No Client.Timeout: it would abort the long-lived WebSocket; we use context
	// deadlines per operation instead.
	client := &http.Client{Transport: transport}

	dir := os.Getenv("SETU_STATE_DIR")
	if dir == "" {
		dir = os.TempDir()
	}

	t := &TV{base: base{
		id:        spec.ID,
		name:      spec.Name,
		mac:       spec.MAC,
		ipHint:    spec.IP,
		arp:       deps.Resolver,
		bus:       deps.Bus,
		http:      client,
		timeout:   defaultTimeout,
		tokenPath: filepath.Join(dir, "setu-samsung-"+sanitizeID(spec.ID)+".token"),
	}}
	t.loadToken()
	return t, nil
}

// Register wires Samsung models into the factory (called from cmd/setu/main.go).
func Register(f *config.Factory) {
	f.Register(Brand, ModelTizen, New)
}

// sanitizeID makes a config id safe to embed in a token filename.
func sanitizeID(s string) string {
	var sb strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			sb.WriteRune(r)
		default:
			sb.WriteByte('_')
		}
	}
	if sb.Len() == 0 {
		return "device"
	}
	return sb.String()
}
