package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"setu/internal/config"
	"setu/internal/device"
	"setu/internal/events"
	"setu/internal/inventory"
	"setu/internal/manager"
	"setu/internal/resolver"
	"setu/internal/store"
)

// storedDevice is a device built from a spec, the way a real brand builds one:
// everything it reports comes from what the user stored.
type storedDevice struct{ spec config.DeviceSpec }

func (d *storedDevice) ID() string             { return d.spec.ID }
func (d *storedDevice) Name() string           { return d.spec.Name }
func (d *storedDevice) Brand() string          { return d.spec.Brand }
func (d *storedDevice) Model() string          { return d.spec.Model }
func (d *storedDevice) Series() string         { return d.spec.Series }
func (d *storedDevice) MAC() string            { return d.spec.MAC }
func (d *storedDevice) Capabilities() []string { return []string{device.CapSwitch} }
func (d *storedDevice) State() device.State    { return device.State{Online: true} }
func (d *storedDevice) On() error              { return nil }
func (d *storedDevice) Off() error             { return nil }

// deviceServer wires the same pieces the composition root does: a manager, an
// inventory over a temporary state file, and one registered brand.
func deviceServer(t *testing.T, specs ...config.DeviceSpec) (http.Handler, *inventory.Inventory, *manager.Manager) {
	t.Helper()
	return newTestServer(t, specs, nil)
}

func newTestServer(t *testing.T, specs []config.DeviceSpec, scanners []resolver.Scanner) (http.Handler, *inventory.Inventory, *manager.Manager) {
	t.Helper()
	bus := events.NewBus()
	mgr := manager.New(bus, nil)
	t.Cleanup(mgr.Close)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	factory := config.NewFactory()
	factory.Register("test", "lamp", func(spec config.DeviceSpec, _ config.Deps) (device.Device, error) {
		return &storedDevice{spec: spec}, nil
	})

	file := store.New(filepath.Join(t.TempDir(), "setu.json"))
	if len(specs) > 0 {
		if err := file.Update(func(state *store.State) error {
			state.Devices = specs
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	inv, err := inventory.New(file, factory, config.Deps{}, mgr, log)
	if err != nil {
		t.Fatal(err)
	}
	srv := New(Options{
		Manager:   mgr,
		Bus:       bus,
		Inventory: inv,
		Scanners:  scanners,
		Token:     "secret",
		Logger:    log,
	})
	return srv.Handler(), inv, mgr
}

func deviceRequest(t *testing.T, handler http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Authorization", "Bearer secret")
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	return w
}

func lamp(id, mac string) config.DeviceSpec {
	return config.DeviceSpec{ID: id, Brand: "test", Model: "lamp", Name: "Lamp", MAC: mac}
}

// Adding a device is the whole point of the scan: it has to reach the API,
// become a live device, and survive a restart — all three, or the user has
// added nothing.
func TestAddDeviceGoesLiveAndIsStored(t *testing.T) {
	handler, inv, mgr := deviceServer(t)

	w := deviceRequest(t, handler, http.MethodPost, "/api/devices",
		`{"brand":"test","model":"lamp","name":"Desk lamp","mac":"98:77:d5:a2:34:f2"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", w.Code, w.Body.String())
	}
	var view manager.DeviceView
	if err := json.NewDecoder(w.Body).Decode(&view); err != nil {
		t.Fatal(err)
	}
	// The id is derived from brand + MAC tail, so neither form has to invent one.
	if view.ID != "test_a234f2" || view.Name != "Desk lamp" {
		t.Fatalf("created device = %+v", view)
	}
	if _, live := mgr.Device(view.ID); !live {
		t.Fatal("added device is not in the manager")
	}
	if specs := inv.Specs(); len(specs) != 1 || specs[0].ID != view.ID {
		t.Fatalf("stored specs = %+v", specs)
	}
}

// The MAC is the device's identity. Adding the same hardware twice would give
// it two cards that fight over the same bulb.
func TestAddDeviceRejectsDuplicatesAndBadInput(t *testing.T) {
	handler, _, _ := deviceServer(t, lamp("desk", "98:77:d5:a2:34:f2"))

	for name, body := range map[string]string{
		"same mac in another notation": `{"brand":"test","model":"lamp","name":"Again","mac":"9877d5a234f2"}`,
		"same id":                      `{"id":"desk","brand":"test","model":"lamp","name":"Again","mac":"aa:bb:cc:dd:ee:ff"}`,
		"unknown brand":                `{"brand":"nope","model":"lamp","name":"Nope","mac":"aa:bb:cc:dd:ee:ff"}`,
		"no mac":                       `{"brand":"test","model":"lamp","name":"No mac"}`,
		"no name":                      `{"brand":"test","model":"lamp","mac":"aa:bb:cc:dd:ee:ff"}`,
	} {
		if w := deviceRequest(t, handler, http.MethodPost, "/api/devices", body); w.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400: %s", name, w.Code, w.Body.String())
		}
	}
}

// Renaming rebuilds the device, so the risk is losing what the card shows.
func TestUpdateDeviceKeepsItsPlaceAndState(t *testing.T) {
	handler, inv, mgr := deviceServer(t, lamp("desk", "98:77:d5:a2:34:f2"), lamp("shelf", "98:77:d5:a2:34:f3"))

	w := deviceRequest(t, handler, http.MethodPatch, "/api/devices/desk", `{"name":"Reading lamp","series":"A60"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	var view manager.DeviceView
	if err := json.NewDecoder(w.Body).Decode(&view); err != nil {
		t.Fatal(err)
	}
	if view.Name != "Reading lamp" || view.Series != "A60" {
		t.Fatalf("renamed device = %+v", view)
	}
	if !view.State.Online {
		t.Error("rename dropped the device's known state")
	}
	snapshot := mgr.Snapshot()
	if len(snapshot) != 2 || snapshot[0].ID != "desk" {
		t.Fatalf("rename moved the device: %+v", snapshot)
	}
	if specs := inv.Specs(); specs[0].Name != "Reading lamp" {
		t.Fatalf("rename was not stored: %+v", specs)
	}

	// The UI edits name and series in two inputs. A save carrying only one of
	// them must not revert the other — the second input's value can be a render
	// behind while the first save is still in flight.
	w = deviceRequest(t, handler, http.MethodPatch, "/api/devices/desk", `{"series":"A67"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("partial update status = %d: %s", w.Code, w.Body.String())
	}
	if err := json.NewDecoder(w.Body).Decode(&view); err != nil {
		t.Fatal(err)
	}
	if view.Name != "Reading lamp" || view.Series != "A67" {
		t.Fatalf("partial update = %+v; want the name untouched", view)
	}

	if w := deviceRequest(t, handler, http.MethodPatch, "/api/devices/missing", `{"name":"x"}`); w.Code != http.StatusNotFound {
		t.Errorf("unknown device status = %d, want 404", w.Code)
	}
	if w := deviceRequest(t, handler, http.MethodPatch, "/api/devices/desk", `{"name":""}`); w.Code != http.StatusBadRequest {
		t.Errorf("empty name status = %d, want 400", w.Code)
	}
}

func TestDeleteDevice(t *testing.T) {
	handler, inv, mgr := deviceServer(t, lamp("desk", "98:77:d5:a2:34:f2"))

	if w := deviceRequest(t, handler, http.MethodDelete, "/api/devices/desk", ""); w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204: %s", w.Code, w.Body.String())
	}
	if _, live := mgr.Device("desk"); live {
		t.Error("removed device is still in the manager")
	}
	if specs := inv.Specs(); len(specs) != 0 {
		t.Fatalf("stored specs = %+v, want none", specs)
	}
	if w := deviceRequest(t, handler, http.MethodDelete, "/api/devices/desk", ""); w.Code != http.StatusNotFound {
		t.Errorf("second delete status = %d, want 404", w.Code)
	}
}

// Export and replace are the two halves of backup/restore.
func TestExportAndReplaceDevices(t *testing.T) {
	handler, inv, mgr := deviceServer(t, lamp("desk", "98:77:d5:a2:34:f2"))

	w := deviceRequest(t, handler, http.MethodGet, "/api/devices/export", "")
	if w.Code != http.StatusOK {
		t.Fatalf("export status = %d: %s", w.Code, w.Body.String())
	}
	var exported deviceList
	if err := json.NewDecoder(w.Body).Decode(&exported); err != nil {
		t.Fatal(err)
	}
	if len(exported.Items) != 1 || exported.Items[0].ID != "desk" {
		t.Fatalf("exported = %+v", exported)
	}

	restore := `{"version":1,"items":[{"id":"shelf","brand":"test","model":"lamp","name":"Shelf","mac":"98:77:d5:a2:34:f3"}]}`
	if w := deviceRequest(t, handler, http.MethodPut, "/api/devices", restore); w.Code != http.StatusOK {
		t.Fatalf("replace status = %d: %s", w.Code, w.Body.String())
	}
	if specs := inv.Specs(); len(specs) != 1 || specs[0].ID != "shelf" {
		t.Fatalf("stored specs after restore = %+v", specs)
	}
	if _, live := mgr.Device("desk"); live {
		t.Error("replaced device is still in the manager")
	}
	if _, live := mgr.Device("shelf"); !live {
		t.Error("restored device is not live")
	}

	// A rejected restore must leave the running installation untouched.
	bad := `{"version":1,"items":[{"id":"ok","brand":"test","model":"lamp","name":"Ok","mac":"98:77:d5:a2:34:f4"},{"id":"broken","brand":"nope","model":"lamp","name":"Broken","mac":"98:77:d5:a2:34:f5"}]}`
	if w := deviceRequest(t, handler, http.MethodPut, "/api/devices", bad); w.Code != http.StatusBadRequest {
		t.Fatalf("bad restore status = %d, want 400", w.Code)
	}
	if specs := inv.Specs(); len(specs) != 1 || specs[0].ID != "shelf" {
		t.Fatalf("failed restore changed the device list: %+v", specs)
	}
}

// Manual add needs the catalog of what this build can drive; the UI must not
// carry its own copy.
func TestDeviceTypesListsRegisteredBrands(t *testing.T) {
	handler, _, _ := deviceServer(t)

	w := deviceRequest(t, handler, http.MethodGet, "/api/device-types", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	var types []config.DeviceType
	if err := json.NewDecoder(w.Body).Decode(&types); err != nil {
		t.Fatal(err)
	}
	if len(types) != 1 || types[0].Brand != "test" || types[0].Model != "lamp" {
		t.Fatalf("types = %+v", types)
	}
}
