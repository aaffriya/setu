package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"setu/internal/config"
)

func tempStore(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), stateFileName)
	return New(path), path
}

// A first run has no file. That is an empty installation, not a failure — the
// user is about to add their first device.
func TestLoadMissingFileIsEmpty(t *testing.T) {
	file, _ := tempStore(t)
	state, err := file.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(state.Devices) != 0 || len(state.Automations) != 0 {
		t.Fatalf("empty install = %+v", state)
	}
	if state.Version != FormatVersion {
		t.Fatalf("empty install version = %d; want %d", state.Version, FormatVersion)
	}
}

// Devices and automations share one file, so the thing that must never happen
// is one section's write dropping the other.
func TestSectionsAreIndependent(t *testing.T) {
	file, _ := tempStore(t)

	if err := file.Update(func(state *State) error {
		state.Automations = json.RawMessage(`{"version":1,"items":[]}`)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := file.Update(func(state *State) error {
		state.Devices = []config.DeviceSpec{{ID: "desk", Brand: "WiZ", Driver: "color_bulb", Name: "Desk", MAC: "98:77:d5:a2:34:f2"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	state, err := file.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Devices) != 1 || state.Devices[0].ID != "desk" {
		t.Fatalf("devices = %+v", state.Devices)
	}
	if string(state.Automations) != `{"version":1,"items":[]}` {
		t.Fatalf("writing devices disturbed the automations section: %s", state.Automations)
	}

	// And the other way round.
	if err := file.Update(func(state *State) error {
		state.Automations = json.RawMessage(`{"version":1,"items":[],"paused":true}`)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	state, err = file.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Devices) != 1 {
		t.Fatalf("writing automations dropped the devices: %+v", state)
	}
}

// Upgrading from a Setu that kept automations in their own file must not lose
// the user's rules.
func TestLoadAdoptsLegacyAutomationFile(t *testing.T) {
	dir := t.TempDir()
	legacy := `{"version":1,"revision":3,"paused":false,"items":[]}`
	if err := os.WriteFile(filepath.Join(dir, legacyAutomationFileName), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}

	file := New(filepath.Join(dir, stateFileName))
	state, err := file.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if string(state.Automations) != legacy {
		t.Fatalf("legacy automations = %s; want them adopted", state.Automations)
	}
}

// A hand-edited or truncated file must fail loudly at startup rather than
// silently starting an installation with no devices.
func TestLoadRejectsBrokenFile(t *testing.T) {
	file, path := tempStore(t)
	if err := os.WriteFile(path, []byte(`{"version":1,"devices":[]`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := file.Load(); err == nil {
		t.Fatal("truncated state file was accepted")
	}
}

func TestLoadRejectsUnsupportedStateVersion(t *testing.T) {
	tests := []struct {
		name string
		data string
	}{
		{name: "missing", data: `{"devices":[]}`},
		{name: "future", data: `{"version":3,"devices":[]}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			file, path := tempStore(t)
			if err := os.WriteFile(path, []byte(test.data), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := file.Load(); err == nil {
				t.Fatalf("state file with %s version was accepted", test.name)
			} else if !strings.Contains(err.Error(), "unsupported state version") {
				t.Fatalf("Load error = %q; want unsupported state version", err)
			}
		})
	}
}

// The vocabulary changed under an installation that already had devices in it.
// Version 1 filed the driver key under "model" and the hardware under "series",
// so a file written then must come back meaning the same devices — otherwise an
// upgrade silently drops everything the user added.
func TestLoadUpgradesVersionOneDevices(t *testing.T) {
	file, path := tempStore(t)
	legacy := `{"version":1,"devices":[` +
		`{"id":"tv","brand":"Samsung","model":"tizen","series":"UE43AU7700","name":"TV","mac":"a0:d7:f3:9e:74:b2"},` +
		`{"id":"nas","brand":"wol","model":"device","name":"NAS","mac":"98:77:d5:a2:34:f2"}],` +
		`"automations":{"version":1,"items":[]}}`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}

	state, err := file.Load()
	if err != nil {
		t.Fatalf("Load a version-1 file: %v", err)
	}
	if state.Version != FormatVersion {
		t.Fatalf("upgraded version = %d; want %d", state.Version, FormatVersion)
	}
	want := []config.DeviceSpec{
		{ID: "tv", Brand: "Samsung", Driver: "tizen", Model: "UE43AU7700", Name: "TV", MAC: "a0:d7:f3:9e:74:b2"},
		{ID: "nas", Brand: "wol", Driver: "device", Name: "NAS", MAC: "98:77:d5:a2:34:f2"},
	}
	if !reflect.DeepEqual(state.Devices, want) {
		t.Fatalf("upgraded devices = %+v; want %+v", state.Devices, want)
	}
	// The automation section is not this package's business and must ride
	// across a device-shape change untouched.
	if string(state.Automations) != `{"version":1,"items":[]}` {
		t.Fatalf("automations after upgrade = %s", state.Automations)
	}

	// The upgrade lands on disk at the next write, so it happens once.
	if err := file.Update(func(*State) error { return nil }); err != nil {
		t.Fatal(err)
	}
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(written), `"series"`) || !strings.Contains(string(written), `"driver"`) {
		t.Fatalf("rewritten file is still in the old shape: %s", written)
	}
}

// The write is atomic: a failure leaves the previous file, never a half-written
// one. A mutate that fails must not touch the file at all.
func TestUpdateKeepsPreviousStateWhenMutateFails(t *testing.T) {
	file, path := tempStore(t)
	if err := file.Update(func(state *State) error {
		state.Devices = []config.DeviceSpec{{ID: "desk"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if err := file.Update(func(*State) error { return os.ErrInvalid }); err == nil {
		t.Fatal("a failing mutate reported success")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("state changed after a failed update:\n%s\n%s", before, after)
	}
}
