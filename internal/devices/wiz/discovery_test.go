package wiz

import (
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"
)

// fakeBulbs answers one broadcast on loopback the way real bulbs do: several
// replies from one socket, and a duplicate, since a bulb may answer twice.
func fakeBulbs(t *testing.T, replies []string) *net.UDPAddr {
	t.Helper()
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })

	go func() {
		buf := make([]byte, 2048)
		_, client, err := conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		for _, reply := range replies {
			if _, err := conn.WriteToUDP([]byte(reply), client); err != nil {
				return
			}
		}
	}()
	return conn.LocalAddr().(*net.UDPAddr)
}

// Scan is what turns "a bulb is on the network" into a config entry the user can
// paste, so it has to report every distinct bulb once, and pick the model from
// the module name rather than assuming every WiZ light is a colour bulb.
func TestScanListsEveryDistinctBulb(t *testing.T) {
	addr := fakeBulbs(t, []string{
		`{"method":"getSystemConfig","result":{"mac":"d8a011ff5ef0","moduleName":"ESP01_SHRGB1C_31"}}`,
		`{"method":"getSystemConfig","result":{"mac":"d8a011ff5ef1","moduleName":"ESP01_SHTW1C_31"}}`,
		`{"method":"getSystemConfig","result":{"mac":"d8a011ff5ef2","moduleName":"ESP10_SOCKET_06"}}`,
		`{"method":"getSystemConfig","result":{"mac":"d8a011ff5ef0","moduleName":"ESP01_SHRGB1C_31"}}`,
		`not json at all`,
	})
	d := &Discoverer{timeout: 300 * time.Millisecond, bcast: addr}

	found, err := d.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan(): %v", err)
	}
	if len(found) != 3 {
		t.Fatalf("Scan() = %+v; want 3 distinct bulbs", found)
	}

	want := map[string]string{
		"d8:a0:11:ff:5e:f0": DriverColorBulb,
		"d8:a0:11:ff:5e:f1": DriverTunableWhite,
		"d8:a0:11:ff:5e:f2": "", // a socket: found, but no driver here
	}
	for _, c := range found {
		driver, ok := want[c.MAC]
		if !ok {
			t.Fatalf("Scan() returned unexpected mac %q", c.MAC)
		}
		if c.Driver != driver {
			t.Errorf("mac %s driver = %q; want %q", c.MAC, c.Driver, driver)
		}
		// The module name is the only hardware identifier WiZ gives, so it must
		// travel as the candidate's model — including for the socket, which has
		// no driver here but is still worth naming on screen.
		if c.Brand != Brand || c.IP != "127.0.0.1" || c.Model == "" {
			t.Errorf("candidate = %+v; want brand %q, the reporting IP and its module name", c, Brand)
		}
		delete(want, c.MAC)
	}
}

// A scan runs inside the request's context; a client that gives up must not
// leave the socket sitting out the rest of the reply window.
func TestScanStopsWhenContextEnds(t *testing.T) {
	addr := fakeBulbs(t, nil)
	d := &Discoverer{timeout: 30 * time.Second, bcast: addr}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	if _, err := d.Scan(ctx); err != nil {
		t.Fatalf("Scan(): %v", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("Scan() returned after %v; want it to stop with the context", elapsed)
	}
}

// Lookup and Scan share one transport; the filter is the only difference, so
// Lookup must still return exactly the bulb that owns the wanted MAC.
func TestLookupPicksTheMatchingBulb(t *testing.T) {
	addr := fakeBulbs(t, []string{
		`{"method":"getPilot","result":{"mac":"d8a011ff5ef0","state":true}}`,
		`{"method":"getPilot","result":{"mac":"aabbccddeeff","state":false}}`,
	})
	d := &Discoverer{timeout: 300 * time.Millisecond, bcast: addr}

	ip, err := d.Lookup("d8:a0:11:ff:5e:f0")
	if err != nil {
		t.Fatalf("Lookup(): %v", err)
	}
	if !ip.Equal(net.IPv4(127, 0, 0, 1)) {
		t.Fatalf("Lookup() = %v; want 127.0.0.1", ip)
	}

	if _, err := d.Lookup("00:11:22:33:44:55"); err == nil {
		t.Fatal("Lookup() of a MAC no bulb claimed must fail, not guess")
	}
}

// The module name is the only thing that tells WiZ models apart, and getting it
// wrong means driving a white bulb with colour commands.
func TestModelForModuleName(t *testing.T) {
	for name, want := range map[string]string{
		"ESP01_SHRGB1C_31": DriverColorBulb,
		"ESP01_SHTW1C_31":  DriverTunableWhite,
		"ESP20_SHTW_01":    DriverTunableWhite,
		"ESP10_SOCKET_06":  "",
		"ESP06_SHDW9_01":   "", // dimmable white: no colour temperature
		"":                 "",
	} {
		if got := driverFor(name); got != want {
			t.Errorf("driverFor(%q) = %q; want %q", name, got, want)
		}
	}
}

// Guards the reply shape the scan depends on: WiZ nests identity under "result".
func TestSysConfigReplyShape(t *testing.T) {
	var resp sysConfigResponse
	if err := json.Unmarshal([]byte(`{"result":{"mac":"d8a011ff5ef0","moduleName":"ESP01_SHRGB1C_31"}}`), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Result == nil || resp.Result.Mac != "d8a011ff5ef0" || resp.Result.ModuleName != "ESP01_SHRGB1C_31" {
		t.Fatalf("decoded reply = %+v", resp.Result)
	}
}
