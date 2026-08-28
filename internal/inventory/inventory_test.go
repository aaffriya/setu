package inventory

import (
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"setu/internal/config"
	"setu/internal/device"
	"setu/internal/events"
	"setu/internal/manager"
	"setu/internal/store"
)

type fakeDevice struct{ spec config.DeviceSpec }

func (d *fakeDevice) ID() string             { return d.spec.ID }
func (d *fakeDevice) Name() string           { return d.spec.Name }
func (d *fakeDevice) Brand() string          { return d.spec.Brand }
func (d *fakeDevice) Driver() string         { return d.spec.Driver }
func (d *fakeDevice) Model() string          { return d.spec.Model }
func (d *fakeDevice) MAC() string            { return d.spec.MAC }
func (d *fakeDevice) Capabilities() []string { return []string{device.CapSwitch} }
func (d *fakeDevice) State() device.State    { return device.State{Online: true} }

func newInventory(t *testing.T, file *store.Store) (*Inventory, *manager.Manager) {
	t.Helper()
	bus := events.NewBus()
	mgr := manager.New(bus, nil)
	t.Cleanup(mgr.Close)

	factory := config.NewFactory()
	factory.Register("Light", "test", "lamp", "Lamp", func(spec config.DeviceSpec, _ config.Deps) (device.Device, error) {
		return &fakeDevice{spec: spec}, nil
	})

	inv, err := New(file, factory, config.Deps{}, mgr, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return inv, mgr
}

func ptr(s string) *string { return &s }

func spec(id, mac string) config.DeviceSpec {
	return config.DeviceSpec{ID: id, Brand: "test", Driver: "lamp", Name: "Lamp", MAC: mac}
}

// The whole point of moving devices out of a config file: what you add is there
// after a restart, without editing anything.
func TestAddedDeviceSurvivesRestart(t *testing.T) {
	file := store.New(filepath.Join(t.TempDir(), "setu.json"))
	inv, _ := newInventory(t, file)

	added, err := inv.Add(config.DeviceSpec{Brand: "test", Driver: "lamp", Name: "Desk", MAC: "98:77:d5:a2:34:f2"}, "")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if added.ID != "test_a234f2" {
		t.Errorf("derived id = %q, want test_a234f2", added.ID)
	}

	// A fresh process over the same file.
	restarted, mgr := newInventory(t, file)
	if specs := restarted.Specs(); len(specs) != 1 || specs[0].ID != added.ID {
		t.Fatalf("specs after restart = %+v", specs)
	}
	if _, live := mgr.Device(added.ID); !live {
		t.Fatal("stored device was not brought online after restart")
	}
}

// One brand talking to one MAC is one device — whichever notation it arrives
// in. A different brand on the same MAC is not a duplicate, though: a
// Wake-on-LAN card for a TV shares the TV's MAC on purpose.
func TestAddRejectsDuplicateIdentity(t *testing.T) {
	inv, _ := newInventory(t, store.New(filepath.Join(t.TempDir(), "setu.json")))
	if _, err := inv.Add(spec("desk", "98:77:d5:a2:34:f2"), ""); err != nil {
		t.Fatal(err)
	}

	if _, err := inv.Add(spec("shelf", "9877d5a234f2"), ""); err == nil {
		t.Error("the same MAC in another notation was added twice")
	}
	if _, err := inv.Add(spec("desk", "aa:bb:cc:dd:ee:ff"), ""); err == nil {
		t.Error("a duplicate id was accepted")
	}
	if _, err := inv.Add(config.DeviceSpec{Brand: "nope", Driver: "lamp", Name: "X", MAC: "aa:bb:cc:dd:ee:ff"}, ""); err == nil {
		t.Error("a device with no registered driver was accepted")
	}
}

// Two identical bulbs whose MAC tails collide must still get distinct ids: an id
// is the handle every command and automation uses.
func TestDerivedIDsDoNotCollide(t *testing.T) {
	inv, _ := newInventory(t, store.New(filepath.Join(t.TempDir(), "setu.json")))
	first, err := inv.Add(config.DeviceSpec{Brand: "test", Driver: "lamp", Name: "A", MAC: "aa:bb:cc:dd:ee:ff"}, "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := inv.Add(config.DeviceSpec{Brand: "test", Driver: "lamp", Name: "B", MAC: "00:11:22:dd:ee:ff"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == second.ID {
		t.Fatalf("both devices got id %q", first.ID)
	}
}

// A device that cannot be built (an unknown brand after a downgrade) must not
// stop the rest of the installation from starting.
func TestUnbuildableDeviceIsSkippedNotFatal(t *testing.T) {
	file := store.New(filepath.Join(t.TempDir(), "setu.json"))
	if err := file.Update(func(state *store.State) error {
		state.Devices = []config.DeviceSpec{
			{ID: "ghost", Brand: "gone", Driver: "lamp", Name: "Ghost", MAC: "aa:bb:cc:dd:ee:ff"},
			spec("desk", "98:77:d5:a2:34:f2"),
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	inv, mgr := newInventory(t, file)
	if _, live := mgr.Device("desk"); !live {
		t.Fatal("a broken entry stopped the working device from starting")
	}
	if _, live := mgr.Device("ghost"); live {
		t.Fatal("an unbuildable device was registered anyway")
	}
	// The entry is kept, so it can still be removed from the UI.
	if specs := inv.Specs(); len(specs) != 2 {
		t.Fatalf("specs = %+v, want the broken entry kept", specs)
	}
}

func TestUpdateAndRemove(t *testing.T) {
	file := store.New(filepath.Join(t.TempDir(), "setu.json"))
	inv, mgr := newInventory(t, file)
	if _, err := inv.Add(spec("desk", "98:77:d5:a2:34:f2"), ""); err != nil {
		t.Fatal(err)
	}
	before, _ := mgr.Device("desk")

	if _, err := inv.Update("desk", Labels{Name: ptr("Reading lamp"), Model: ptr("A60")}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	after, ok := mgr.Device("desk")
	if !ok || after != before {
		t.Fatal("rename rebuilt the live protocol driver")
	}
	view, ok := mgr.View("desk")
	if !ok || view.Name != "Reading lamp" || view.Model != "A60" {
		t.Fatalf("live view after rename = %+v", view)
	}
	if _, err := inv.Update("missing", Labels{Name: ptr("x")}); !IsNotFound(err) {
		t.Errorf("Update of an unknown device = %v, want not-found", err)
	}

	// A save that carries only one field must leave the other alone — the UI
	// edits them in two inputs, and the second save must not revert the first.
	updated, err := inv.Update("desk", Labels{Model: ptr("A67")})
	if err != nil {
		t.Fatalf("Update model only: %v", err)
	}
	if updated.Name != "Reading lamp" || updated.Model != "A67" {
		t.Fatalf("partial update = %+v; want the name kept", updated)
	}
	view, _ = mgr.View("desk")
	if view.Name != "Reading lamp" || view.Model != "A67" {
		t.Fatalf("view after partial update = %+v", view)
	}

	if err := inv.Remove("desk"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, live := mgr.Device("desk"); live {
		t.Error("removed device is still live")
	}
	if state, _ := file.Load(); len(state.Devices) != 0 {
		t.Errorf("stored devices after remove = %+v", state.Devices)
	}
	if err := inv.Remove("desk"); !IsNotFound(err) {
		t.Errorf("second Remove = %v, want not-found", err)
	}
}

// Restore replaces everything at once; a list with one bad entry must leave the
// running installation exactly as it was.
func TestReplaceIsAllOrNothing(t *testing.T) {
	file := store.New(filepath.Join(t.TempDir(), "setu.json"))
	inv, mgr := newInventory(t, file)
	if _, err := inv.Add(spec("desk", "98:77:d5:a2:34:f2"), ""); err != nil {
		t.Fatal(err)
	}

	_, err := inv.Replace([]config.DeviceSpec{
		spec("shelf", "98:77:d5:a2:34:f3"),
		{ID: "broken", Brand: "gone", Driver: "lamp", Name: "Broken", MAC: "aa:bb:cc:dd:ee:ff"},
	})
	if err == nil {
		t.Fatal("a list with an unbuildable device was accepted")
	}
	if specs := inv.Specs(); len(specs) != 1 || specs[0].ID != "desk" {
		t.Fatalf("failed replace changed the inventory: %+v", specs)
	}

	if _, err := inv.Replace([]config.DeviceSpec{spec("shelf", "98:77:d5:a2:34:f3")}); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	if _, live := mgr.Device("desk"); live {
		t.Error("replaced device is still live")
	}
	if _, live := mgr.Device("shelf"); !live {
		t.Error("restored device is not live")
	}
}

func TestConfiguredMatchesBrandAndMAC(t *testing.T) {
	inv, _ := newInventory(t, store.New(filepath.Join(t.TempDir(), "setu.json")))
	if _, err := inv.Add(spec("desk", "98:77:d5:a2:34:f2"), ""); err != nil {
		t.Fatal(err)
	}

	if id, ok := inv.Configured("test", "9877d5a234f2"); !ok || id != "desk" {
		t.Errorf("Configured() = %q, %v; want desk, true", id, ok)
	}
	if _, ok := inv.Configured("test", "aa:bb:cc:dd:ee:ff"); ok {
		t.Error("an unknown MAC was reported as added")
	}
	// Another brand's scan must not claim this device as its own.
	if _, ok := inv.Configured("other", "98:77:d5:a2:34:f2"); ok {
		t.Error("a different brand on the same MAC was reported as added")
	}
}

// A Wake-on-LAN card beside a TV is the real case: same MAC, different brand,
// different job.
func TestSameMACIsAllowedForAnotherBrand(t *testing.T) {
	file := store.New(filepath.Join(t.TempDir(), "setu.json"))
	bus := events.NewBus()
	mgr := manager.New(bus, nil)
	t.Cleanup(mgr.Close)
	factory := config.NewFactory()
	build := func(spec config.DeviceSpec, _ config.Deps) (device.Device, error) {
		return &fakeDevice{spec: spec}, nil
	}
	factory.Register("Light", "test", "lamp", "Lamp", build)
	factory.Register("Wake-on-LAN", "wol", "device", "Wake-on-LAN Target", build)
	inv, err := New(file, factory, config.Deps{}, mgr, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := inv.Add(spec("tv", "a0:d7:f3:9e:74:b2"), ""); err != nil {
		t.Fatal(err)
	}
	if _, err := inv.Add(config.DeviceSpec{
		ID: "tv_wake", Brand: "wol", Driver: "device", Name: "Wake TV", MAC: "a0:d7:f3:9e:74:b2",
	}, ""); err != nil {
		t.Fatalf("a Wake-on-LAN card for the same hardware was refused: %v", err)
	}
}

// closableDevice records whether it was released.
type closableDevice struct {
	fakeDevice
	closed *bool
}

func (d *closableDevice) Close() error { *d.closed = true; return nil }

// A restore that fails part-way must not leave devices constructed and
// abandoned: building is what opens sockets and registers per-MAC callbacks, so
// an unreleased instance keeps them — and displaces the live device's own.
func TestFailedReplaceReleasesEveryDeviceItBuilt(t *testing.T) {
	file := store.New(filepath.Join(t.TempDir(), "setu.json"))
	bus := events.NewBus()
	mgr := manager.New(bus, nil)
	t.Cleanup(mgr.Close)

	var closed []*bool
	factory := config.NewFactory()
	factory.Register("Light", "test", "lamp", "Lamp", func(spec config.DeviceSpec, _ config.Deps) (device.Device, error) {
		released := false
		closed = append(closed, &released)
		return &closableDevice{fakeDevice: fakeDevice{spec: spec}, closed: &released}, nil
	})
	factory.Register("Light", "test", "broken", "Broken", func(config.DeviceSpec, config.Deps) (device.Device, error) {
		return nil, errors.New("this build cannot drive that")
	})
	inv, err := New(file, factory, config.Deps{}, mgr, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := inv.Replace([]config.DeviceSpec{
		spec("lamp", "98:77:d5:a2:34:f2"),
		{ID: "bad", Brand: "test", Driver: "broken", Name: "Bad", MAC: "98:77:d5:a2:34:f3"},
	}); err == nil {
		t.Fatal("a list holding an unbuildable entry was accepted")
	}

	if len(closed) != 1 {
		t.Fatalf("built %d devices before the failure, want 1", len(closed))
	}
	if !*closed[0] {
		t.Error("the device built before the failure was never released")
	}
}
