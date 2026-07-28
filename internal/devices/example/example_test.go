package example

import (
	"errors"
	"net"
	"testing"

	"setu/internal/config"
	"setu/internal/device"
	"setu/internal/events"
)

// emptyARP stands in for the injected resolver on a host whose ARP table cannot
// answer — an empty table right after boot, or any non-Linux dev machine, where
// /proc/net/arp does not exist at all.
type emptyARP struct{}

func (emptyARP) Lookup(string) (net.IP, error) { return nil, errors.New("no arp entry") }

func TestBulbAdvertisesLiveReachability(t *testing.T) {
	if !device.ReportsReachability(&Bulb{}) {
		t.Fatal("example bulb did not advertise live reachability")
	}
}

func newTestBulb(t *testing.T) *Bulb {
	t.Helper()
	dev, err := New(config.DeviceSpec{
		ID:   "fake_lamp",
		Name: "Fake Lamp",
		MAC:  "02:00:00:00:00:01",
	}, config.Deps{Resolver: emptyARP{}, Bus: events.NewBus()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	bulb, ok := dev.(*Bulb)
	if !ok {
		t.Fatalf("New returned %T, want *Bulb", dev)
	}
	return bulb
}

// The blueprint is only useful as a blueprint if it runs. Without a brand
// discoverer it depends entirely on the injected ARP resolver, so on a dev
// machine every command fails and the template cannot stand in for a real
// device while working on the UI or the API.
func TestBulbWorksWithoutAnARPTable(t *testing.T) {
	bulb := newTestBulb(t)

	if err := bulb.On(); err != nil {
		t.Fatalf("On() with an unanswerable ARP table = %v; the brand discoverer must resolve it", err)
	}
	if state := bulb.State(); !state.On || !state.Online {
		t.Fatalf("state after On() = %+v, want online and on", state)
	}

	if err := bulb.SetBrightness(60); err != nil {
		t.Fatalf("SetBrightness: %v", err)
	}
	if err := bulb.SetColor(device.Color{R: 10, G: 20, B: 30}); err != nil {
		t.Fatalf("SetColor: %v", err)
	}
	state := bulb.State()
	if state.Brightness != 60 || state.Color != (device.Color{R: 10, G: 20, B: 30}) {
		t.Fatalf("state = %+v, want brightness 60 and the set colour", state)
	}

	if err := bulb.Off(); err != nil {
		t.Fatalf("Off: %v", err)
	}
	if bulb.State().On {
		t.Error("state.On = true after Off()")
	}
}

// Input validation must be rejected before the transport is touched, so the
// manager can tell a client mistake from a device failure.
func TestBulbRejectsOutOfRangeBrightness(t *testing.T) {
	bulb := newTestBulb(t)
	if err := bulb.SetBrightness(101); err == nil {
		t.Error("SetBrightness(101) was accepted")
	}
}

// Resolution order is part of the pattern this template teaches: a resolver that
// can answer wins, and the brand discoverer is only the fallback.
func TestResolutionPrefersTheInjectedResolver(t *testing.T) {
	bulb := newTestBulb(t)
	bulb.arp = fixedARP{ip: net.IPv4(192, 168, 1, 42)}

	ip, err := bulb.resolveIP()
	if err != nil {
		t.Fatalf("resolveIP: %v", err)
	}
	if !ip.Equal(net.IPv4(192, 168, 1, 42)) {
		t.Fatalf("resolveIP() = %v, want the ARP answer rather than the discoverer's", ip)
	}
}

type fixedARP struct{ ip net.IP }

func (f fixedARP) Lookup(string) (net.IP, error) { return f.ip, nil }

// A malformed MAC is a config mistake and must not resolve to anything.
func TestDiscovererRejectsInvalidMAC(t *testing.T) {
	if _, err := NewDiscoverer().Lookup("not-a-mac"); err == nil {
		t.Error("Lookup accepted a malformed MAC")
	}
}
