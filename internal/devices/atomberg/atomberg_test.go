package atomberg

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"setu/internal/config"
	"setu/internal/device"
	"setu/internal/events"
)

const testMAC = "a0:76:4e:5a:ee:98"

// emptyARP stands in for a host that has never seen the fan, so the tests
// exercise the beacon path rather than accidentally passing through ARP.
type emptyARP struct{}

func (emptyARP) Lookup(string) (net.IP, error) { return nil, errors.New("not in arp") }

// newTestListener starts an isolated listener on a loopback port, so tests
// never touch the real beacon port or each other.
func newTestListener(t *testing.T) *Discoverer {
	t.Helper()
	d := newDiscoverer(&net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err := d.ensure(); err != nil {
		t.Fatalf("start listener: %v", err)
	}
	return d
}

// feed sends one datagram to the listener as though a fan had broadcast it.
func feed(t *testing.T, d *Discoverer, payload string) {
	t.Helper()
	conn, err := net.DialUDP("udp", nil, d.addr)
	if err != nil {
		t.Fatalf("dial listener: %v", err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte(payload)); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func stateDatagram(mac, messageID, stateString string) string {
	payload, _ := json.Marshal(stateMessage{
		DeviceID: mac, MessageID: messageID, StateString: stateString,
	})
	return hex.EncodeToString(payload)
}

// waitFor polls a condition briefly: the listener consumes datagrams on its own
// goroutine, so arrival is not synchronous with the send.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestListenerResolvesFromBeacon(t *testing.T) {
	d := newTestListener(t)
	feed(t, d, "a0764e5aee98_R1")

	waitFor(t, "the beacon to be recorded", func() bool {
		_, err := d.Lookup(testMAC)
		return err == nil
	})

	ip, err := d.Lookup(testMAC)
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if !ip.IsLoopback() {
		t.Errorf("resolved %v, want the loopback address the beacon came from", ip)
	}
	if !d.present(testMAC) {
		t.Error("a fan that just beaconed should be present")
	}
}

func TestListenerLookupUnknownMAC(t *testing.T) {
	d := newTestListener(t)
	if _, err := d.Lookup("aa:bb:cc:dd:ee:ff"); err == nil {
		t.Error("Lookup should fail for a fan that has never beaconed")
	}
	if _, err := d.Lookup("not-a-mac"); err == nil {
		t.Error("Lookup should reject a malformed MAC")
	}
}

// Right after a restart the table is cold and the fan's next beacon is up to a
// second away. Lookup must wait for it, or the first command a user sends
// fails against a fan that is present and about to announce itself.
func TestLookupWaitsForTheFirstBeacon(t *testing.T) {
	d := newTestListener(t)

	// The beacon lands only after Lookup is already waiting.
	go func() {
		time.Sleep(300 * time.Millisecond)
		conn, err := net.DialUDP("udp", nil, d.addr)
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = conn.Write([]byte("a0764e5aee98_R1"))
	}()

	start := time.Now()
	ip, err := d.Lookup(testMAC)
	if err != nil {
		t.Fatalf("Lookup gave up before the first beacon: %v", err)
	}
	if !ip.IsLoopback() {
		t.Errorf("resolved %v", ip)
	}
	if elapsed := time.Since(start); elapsed > beaconWait {
		t.Errorf("Lookup took %v, past the %v budget", elapsed, beaconWait)
	}
}

func TestScanReportsBeaconedFans(t *testing.T) {
	d := newTestListener(t)
	feed(t, d, "a0764e5aee98_R1") // has a driver
	feed(t, d, "a0764e5aee99_ZZ") // recognised as Atomberg, but no driver

	waitFor(t, "both beacons", func() bool {
		found, _ := d.Scan(context.Background())
		return len(found) == 2
	})

	found, err := d.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	byMAC := make(map[string]string, len(found))
	for _, c := range found {
		if c.Brand != Brand {
			t.Errorf("candidate brand = %q", c.Brand)
		}
		byMAC[c.MAC] = c.Driver
	}
	if got := byMAC[testMAC]; got != DriverFan {
		t.Errorf("R1 mapped to driver %q, want %q", got, DriverFan)
	}
	// An unknown model must be reported as found-but-unsupported rather than
	// guessed at, or Setu would command it through the wrong driver.
	if got, ok := byMAC["a0:76:4e:5a:ee:99"]; !ok || got != "" {
		t.Errorf("unknown model mapped to driver %q, want empty", got)
	}
}

func TestListenerPushesStateToTheFan(t *testing.T) {
	d := newTestListener(t)

	received := make(chan Snapshot, 4)
	unwatch := d.watchFan(testMAC, func(s Snapshot) { received <- s })
	defer unwatch()

	feed(t, d, stateDatagram("a0764e5aee98", "msg-1", "20,1,B,5,50.00,0,0,R1,END"))

	select {
	case snapshot := <-received:
		if !snapshot.State.Power || snapshot.State.Speed != 4 {
			t.Errorf("pushed state = %+v", snapshot.State)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no state was pushed to the fan")
	}

	// A retransmit of the same message must not publish a second event.
	feed(t, d, stateDatagram("a0764e5aee98", "msg-1", "20,1,B,5,50.00,0,0,R1,END"))
	select {
	case snapshot := <-received:
		t.Errorf("a duplicate message_id was pushed again: %+v", snapshot)
	case <-time.After(300 * time.Millisecond):
	}

	// A genuinely new message still gets through.
	feed(t, d, stateDatagram("a0764e5aee98", "msg-2", "17,1,B,5,50.00,0,0,R1,END"))
	select {
	case snapshot := <-received:
		if snapshot.State.Speed != 1 {
			t.Errorf("second push = %+v", snapshot.State)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a new message_id was not pushed")
	}
}

// Renaming a device rebuilds it: the new instance registers its watcher, and
// only then is the old one closed. The old instance's unwatch must not take the
// new registration with it, or a rename silently kills push updates until the
// next restart.
func TestUnwatchDoesNotRemoveALaterRegistration(t *testing.T) {
	d := newTestListener(t)

	stale := make(chan Snapshot, 1)
	unwatchOld := d.watchFan(testMAC, func(s Snapshot) { stale <- s })

	fresh := make(chan Snapshot, 1)
	defer d.watchFan(testMAC, func(s Snapshot) { fresh <- s })()

	unwatchOld() // the rebuilt device replaced it; closing the old one comes after

	feed(t, d, stateDatagram("a0764e5aee98", "msg-1", "20,1,B,5,50.00,0,0,R1,END"))

	select {
	case <-fresh:
	case <-stale:
		t.Fatal("the replaced device is still receiving state")
	case <-time.After(2 * time.Second):
		t.Fatal("no state reached the current device: unwatch removed its registration")
	}
}

func TestListenerIgnoresForeignTraffic(t *testing.T) {
	d := newTestListener(t)
	// The beacon port is a broadcast port on a home network: other protocols
	// land on it, and none of them may become a device.
	feed(t, d, "hello world")
	feed(t, d, "{\"some\":\"json\"}")
	feed(t, d, hex.EncodeToString([]byte(`{"unrelated":true}`)))
	time.Sleep(200 * time.Millisecond)

	found, err := d.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(found) != 0 {
		t.Errorf("foreign traffic produced candidates: %+v", found)
	}
}

func TestListenerCapsTheSightingTable(t *testing.T) {
	d := newTestListener(t)
	for i := range maxFans + 10 {
		feed(t, d, fmt.Sprintf("a0764e5a%04x_R1", i))
	}
	waitFor(t, "the table to fill", func() bool {
		d.mu.Lock()
		defer d.mu.Unlock()
		return len(d.fans) >= maxFans
	})
	time.Sleep(200 * time.Millisecond)

	d.mu.Lock()
	size := len(d.fans)
	d.mu.Unlock()
	if size > maxFans {
		t.Errorf("sighting table grew to %d, past the %d cap", size, maxFans)
	}
}

// --- device-level behaviour -------------------------------------------------

// newTestFan builds a Fan wired to an isolated listener, so no test touches the
// real beacon port.
func newTestFan(t *testing.T) (*Fan, *Discoverer) {
	t.Helper()
	d := newTestListener(t)
	f := &Fan{base: base{
		id: "fan-test", name: "Test Fan", mac: testMAC,
		arp: emptyARP{}, listener: d, bus: events.NewBus(),
		state: device.State{Online: true},
	}}
	return f, d
}

func TestResolveIPReplacesStaleCacheWithFreshBeacon(t *testing.T) {
	f, d := newTestFan(t)
	stale := net.IPv4(192, 0, 2, 10)
	f.setIP(stale)

	fresh := net.IPv4(192, 0, 2, 25)
	d.recordBeacon(beacon{MAC: testMAC, Model: "R1"}, fresh)

	got, err := f.resolveIP()
	if err != nil {
		t.Fatalf("resolve IP: %v", err)
	}
	if !got.Equal(fresh) {
		t.Fatalf("resolved %v, want fresh beacon address %v instead of cached %v", got, fresh, stale)
	}
	f.mu.Lock()
	cached := append(net.IP(nil), f.ip...)
	f.mu.Unlock()
	if !cached.Equal(fresh) {
		t.Fatalf("cached IP = %v, want %v", cached, fresh)
	}
}

func TestFanRejectsOutOfRangeValuesBeforeTheTransport(t *testing.T) {
	f, _ := newTestFan(t)

	// Nothing has beaconed, so any command that reached the transport would
	// fail with a resolution error. A range error instead proves validation
	// happens first.
	for _, tc := range []struct {
		name string
		call func() error
	}{
		{"speed below range", func() error { return f.SetSpeed(0) }},
		{"speed above range", func() error { return f.SetSpeed(7) }},
		{"unsupported timer duration", func() error { return f.SetTimer(5) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call()
			if err == nil {
				t.Fatal("expected an error")
			}
			got := err.Error()
			if !strings.Contains(got, "out of range") && !strings.Contains(got, "is not one of") {
				t.Errorf("error %q looks like a transport failure, not validation", got)
			}
		})
	}
}

func TestFanCommandsGoOnTheWire(t *testing.T) {
	f, d := newTestFan(t)

	// Stand in for the fan's command port and capture what arrives.
	fake, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer fake.Close()
	f.setIP(fake.LocalAddr().(*net.UDPAddr).IP)
	// Point the driver's command port at the fake for the duration of the test.
	original := commandPortOverride
	commandPortOverride = fake.LocalAddr().(*net.UDPAddr).Port
	defer func() { commandPortOverride = original }()
	_ = d

	tests := []struct {
		name string
		call func() error
		want string
	}{
		{"on", f.On, `{"power":true}`},
		{"off", f.Off, `{"power":false}`},
		{"speed", func() error { return f.SetSpeed(4) }, `{"speed":4}`},
		{"sleep", func() error { return f.SetSleep(true) }, `{"sleep":true}`},
		// 6 hours is command index 4 — the asymmetry this protocol is easiest
		// to get wrong on.
		{"six-hour timer sends index 4", func() error { return f.SetTimer(6) }, `{"timer":4}`},
		{"timer off", func() error { return f.SetTimer(0) }, `{"timer":0}`},
		// A fan with no dimming controls its lamp with one datagram — no
		// brightness level follows.
		{"light on", func() error { return f.SetLight(true) }, `{"led":true}`},
		{"light off", func() error { return f.SetLight(false) }, `{"led":false}`},
	}

	buf := make([]byte, 512)
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.call(); err != nil {
				t.Fatalf("command: %v", err)
			}
			if err := fake.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
				t.Fatal(err)
			}
			n, _, err := fake.ReadFromUDP(buf)
			if err != nil {
				t.Fatalf("no datagram reached the fan: %v", err)
			}
			if got := string(buf[:n]); got != tc.want {
				t.Errorf("sent %s, want %s", got, tc.want)
			}
		})
	}
}

func TestFanLightSendsBrightnessAndMode(t *testing.T) {
	d := newTestListener(t)
	f := &FanLight{base: base{
		id: "fanlight-test", name: "Test Fan Light", mac: testMAC,
		arp: emptyARP{}, listener: d, bus: events.NewBus(),
		state: device.State{Online: true},
	}}

	fake, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer fake.Close()
	f.setIP(fake.LocalAddr().(*net.UDPAddr).IP)
	original := commandPortOverride
	commandPortOverride = fake.LocalAddr().(*net.UDPAddr).Port
	defer func() { commandPortOverride = original }()

	if err := f.SetBrightness(60); err != nil {
		t.Fatalf("SetBrightness: %v", err)
	}
	// Turning the light on is two datagrams: the switch, then the level.
	want := []string{`{"led":true}`, `{"brightness":60}`}
	buf := make([]byte, 512)
	for _, expected := range want {
		if err := fake.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
			t.Fatal(err)
		}
		n, _, err := fake.ReadFromUDP(buf)
		if err != nil {
			t.Fatalf("expected %s: %v", expected, err)
		}
		if got := string(buf[:n]); got != expected {
			t.Errorf("sent %s, want %s", got, expected)
		}
	}

	// Below the hardware floor the driver must clamp, not send a level the fan
	// would ignore.
	if err := f.SetBrightness(3); err != nil {
		t.Fatalf("SetBrightness: %v", err)
	}
	_ = fake.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, _, err := fake.ReadFromUDP(buf); err != nil { // the {"led":true}
		t.Fatal(err)
	}
	n, _, err := fake.ReadFromUDP(buf)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(buf[:n]); got != fmt.Sprintf(`{"brightness":%d}`, minBrightness) {
		t.Errorf("sent %s, want the %d%% hardware floor", got, minBrightness)
	}

	if err := f.SetScene(sceneDaylight); err != nil {
		t.Fatalf("SetScene: %v", err)
	}
	_ = fake.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, _, err = fake.ReadFromUDP(buf)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(buf[:n]); got != `{"light_mode":"daylight"}` {
		t.Errorf("sent %s", got)
	}

	if err := f.SetScene(99); err == nil {
		t.Error("SetScene should reject an unknown mode")
	}
}

func TestPollReportsNoResponseWhenTheFanIsSilent(t *testing.T) {
	f, _ := newTestFan(t)
	// Nothing beaconed, so the fan cannot even be resolved.
	state, err := f.Poll()
	if !errors.Is(err, device.ErrPollNoResponse) {
		t.Fatalf("Poll error = %v, want it to wrap ErrPollNoResponse", err)
	}
	// The manager keeps the state returned with ErrPollNoResponse, so it has to
	// say offline — otherwise an unreachable fan keeps rendering as healthy.
	if state.Online {
		t.Error("an unresolvable fan reported Online")
	}
}

// Poll nudges the fan and adopts the broadcast that follows. It must not depend
// on message_id being populated: a fan that leaves it empty still reports real
// state, and a poll that ignored it would leave the card permanently stale.
func TestPollAdoptsBroadcastWithoutAMessageID(t *testing.T) {
	f, d := newTestFan(t)

	fake, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer fake.Close()
	f.setIP(fake.LocalAddr().(*net.UDPAddr).IP)
	original := commandPortOverride
	commandPortOverride = fake.LocalAddr().(*net.UDPAddr).Port
	defer func() { commandPortOverride = original }()

	// Stand in for the fan: answer the poll nudge with a state broadcast whose
	// message_id is empty.
	go func() {
		buf := make([]byte, 512)
		if _, _, err := fake.ReadFromUDP(buf); err != nil {
			return
		}
		conn, err := net.DialUDP("udp", nil, d.addr)
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = conn.Write([]byte(stateDatagram("a0764e5aee98", "", "22,1,B,5,50.00,0,0,R1,END")))
	}()

	state, err := f.Poll()
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if !state.On || state.Speed != 6 {
		t.Errorf("polled state = %+v, want the broadcast on/speed 6", state)
	}
	if !state.Online {
		t.Error("a fan that answered the poll reported offline")
	}
}

func TestDriverFor(t *testing.T) {
	tests := map[string]string{
		"R1": DriverFan, "R2": DriverFan, "K1": DriverFan, "I2": DriverFan,
		"I1": DriverFanLight, "I5": DriverFanLight, "S1": DriverFanLight,
		"": "", "ZZ": "", "R9": "",
	}
	for model, want := range tests {
		if got := driverFor(model); got != want {
			t.Errorf("driverFor(%q) = %q, want %q", model, got, want)
		}
	}
}

func TestTimerOptionsMatchTheIndexMap(t *testing.T) {
	// The two tables must agree, or a duration the UI offers would be rejected
	// (or worse, sent as the wrong index).
	f, _ := newTestFan(t)
	for _, hours := range f.TimerOptions() {
		if _, ok := timerIndex[hours]; !ok {
			t.Errorf("TimerOptions offers %dh but timerIndex cannot encode it", hours)
		}
	}
	if len(timerIndex) != len(timerHours) {
		t.Errorf("timerIndex has %d entries, timerHours has %d", len(timerIndex), len(timerHours))
	}
}

func TestRegisterExposesBothDrivers(t *testing.T) {
	factory := config.NewFactory()
	Register(factory)
	for _, driver := range []string{DriverFan, DriverFanLight} {
		if !factory.Supports(Brand, driver) {
			t.Errorf("factory cannot build %s/%s", Brand, driver)
		}
	}
}
