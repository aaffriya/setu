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
	"setu/internal/store"
	"setu/internal/users"
)

// accessServer wires the pieces an account actually meets: an inventory of
// devices and a user registry, behind the administrator's token.
func accessServer(t *testing.T, specs ...config.DeviceSpec) (http.Handler, *users.Registry, *manager.Manager) {
	t.Helper()
	handler, registry, mgr, _ := accessServerWithBus(t, specs...)
	return handler, registry, mgr
}

func accessServerWithBus(t *testing.T, specs ...config.DeviceSpec) (http.Handler, *users.Registry, *manager.Manager, *events.Bus) {
	t.Helper()
	bus := events.NewBus()
	mgr := manager.New(bus, nil)
	t.Cleanup(mgr.Close)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	factory := config.NewFactory()
	factory.Register("Light", "test", "lamp", "Lamp", func(spec config.DeviceSpec, _ config.Deps) (device.Device, error) {
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
	registry, err := users.New(file)
	if err != nil {
		t.Fatal(err)
	}
	inv, err := inventory.New(file, factory, config.Deps{}, mgr, registry, log)
	if err != nil {
		t.Fatal(err)
	}
	srv := New(Options{
		Manager:   mgr,
		Bus:       bus,
		Inventory: inv,
		Users:     registry,
		Token:     "admin-token",
		Logger:    log,
	})
	return srv.Handler(), registry, mgr, bus
}

func as(t *testing.T, handler http.Handler, token, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Authorization", "Bearer "+token)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	return w
}

// The device list is the account's own. A device that was never granted must
// not appear in it at all — not greyed out, not present and refused later.
func TestDeviceListIsLimitedToGrantedDevices(t *testing.T) {
	handler, registry, _ := accessServer(t, lamp("lamp", "98:77:d5:a2:34:f2"), lamp("tv", "98:77:d5:a2:34:f3"))
	_, token, err := registry.Create("Priya", users.RoleRead, []string{"lamp"})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	w := as(t, handler, token, http.MethodGet, "/api/devices", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	var views []manager.DeviceView
	if err := json.NewDecoder(w.Body).Decode(&views); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(views) != 1 || views[0].ID != "lamp" {
		t.Fatalf("devices = %+v, want only lamp", views)
	}

	if admin := as(t, handler, "admin-token", http.MethodGet, "/api/devices", ""); admin.Code == http.StatusOK {
		var all []manager.DeviceView
		if err := json.NewDecoder(admin.Body).Decode(&all); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(all) != 2 {
			t.Fatalf("the administrator sees %d devices, want 2", len(all))
		}
	}
}

// A read account controls what it was given. That is the point of the role, so
// it must not be confused with "may not send commands".
func TestReadAccountControlsItsOwnDevices(t *testing.T) {
	handler, registry, _ := accessServer(t, lamp("lamp", "98:77:d5:a2:34:f2"), lamp("tv", "98:77:d5:a2:34:f3"))
	_, token, err := registry.Create("Priya", users.RoleRead, []string{"lamp"})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	if w := as(t, handler, token, http.MethodPost, "/api/devices/lamp/command", `{"action":"on"}`); w.Code != http.StatusOK {
		t.Fatalf("granted command status = %d, want 200: %s", w.Code, w.Body.String())
	}
	if w := as(t, handler, token, http.MethodPost, "/api/devices/tv/command", `{"action":"on"}`); w.Code != http.StatusForbidden {
		t.Fatalf("ungranted command status = %d, want 403: %s", w.Code, w.Body.String())
	}
	if w := as(t, handler, token, http.MethodPost, "/api/devices/nope/command", `{"action":"on"}`); w.Code != http.StatusNotFound {
		t.Fatalf("unknown device status = %d, want 404", w.Code)
	}
}

// "read" is the whole-installation restriction: it stops an account changing
// what exists, not what it does with what it has.
func TestReadAccountCannotChangeTheInstallation(t *testing.T) {
	handler, registry, _ := accessServer(t, lamp("lamp", "98:77:d5:a2:34:f2"))
	_, token, err := registry.Create("Priya", users.RoleRead, []string{"lamp"})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	body := `{"brand":"test","driver":"lamp","name":"New","mac":"98:77:d5:a2:34:f9"}`
	if w := as(t, handler, token, http.MethodPost, "/api/devices", body); w.Code != http.StatusForbidden {
		t.Fatalf("add device status = %d, want 403: %s", w.Code, w.Body.String())
	}
	if w := as(t, handler, token, http.MethodDelete, "/api/devices/lamp", ""); w.Code != http.StatusForbidden {
		t.Fatalf("delete device status = %d, want 403", w.Code)
	}
	if w := as(t, handler, token, http.MethodGet, "/api/users", ""); w.Code != http.StatusForbidden {
		t.Fatalf("list users status = %d, want 403", w.Code)
	}
	if w := as(t, handler, token, http.MethodGet, "/api/devices/export", ""); w.Code != http.StatusForbidden {
		t.Fatalf("export status = %d, want 403", w.Code)
	}
}

// Someone who may add hardware keeps what they added. Otherwise they would have
// to ask the administrator to share back the device they just contributed.
func TestAddingADeviceGrantsItToTheAccountThatAddedIt(t *testing.T) {
	handler, registry, _ := accessServer(t)
	user, token, err := registry.Create("Arjun", users.RoleModify, nil)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	body := `{"brand":"test","driver":"lamp","name":"Desk lamp","mac":"98:77:d5:a2:34:f2"}`
	w := as(t, handler, token, http.MethodPost, "/api/devices", body)
	if w.Code != http.StatusCreated {
		t.Fatalf("add device status = %d, want 201: %s", w.Code, w.Body.String())
	}
	var view manager.DeviceView
	if err := json.NewDecoder(w.Body).Decode(&view); err != nil {
		t.Fatalf("decode: %v", err)
	}
	reloaded, ok := registry.Get(user.ID)
	if !ok || !reloaded.CanSee(view.ID) {
		t.Fatalf("the account that added %q was not granted it: %+v", view.ID, reloaded)
	}
	if list := as(t, handler, token, http.MethodPost, "/api/devices/"+view.ID+"/command", `{"action":"on"}`); list.Code != http.StatusOK {
		t.Fatalf("command on the new device = %d, want 200: %s", list.Code, list.Body.String())
	}
}

// Deleting hardware takes its grants with it. Device ids are derived from the
// brand and MAC, so the same id reappears when the same device is re-added.
func TestDeletingADeviceClearsItsGrants(t *testing.T) {
	handler, registry, _ := accessServer(t, lamp("lamp", "98:77:d5:a2:34:f2"))
	user, _, err := registry.Create("Priya", users.RoleModify, []string{"lamp"})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if w := as(t, handler, "admin-token", http.MethodDelete, "/api/devices/lamp", ""); w.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204: %s", w.Code, w.Body.String())
	}
	reloaded, _ := registry.Get(user.ID)
	if reloaded.CanSee("lamp") {
		t.Fatalf("the grant outlived the device: %+v", reloaded)
	}
}

// A restore removes devices without deleting them one at a time, so the
// per-device cleanup never runs. The grants still have to go with them.
func TestRestoreClearsGrantsForRemovedDevices(t *testing.T) {
	handler, registry, _ := accessServer(t, lamp("lamp", "98:77:d5:a2:34:f2"), lamp("tv", "98:77:d5:a2:34:f3"))
	user, _, err := registry.Create("Priya", users.RoleRead, []string{"lamp", "tv"})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	// A backup taken before the TV existed.
	body := `{"version":2,"items":[{"id":"lamp","brand":"test","driver":"lamp","name":"Lamp","mac":"98:77:d5:a2:34:f2"}]}`
	if w := as(t, handler, "admin-token", http.MethodPut, "/api/devices", body); w.Code != http.StatusOK {
		t.Fatalf("restore status = %d, want 200: %s", w.Code, w.Body.String())
	}
	reloaded, _ := registry.Get(user.ID)
	if reloaded.CanSee("tv") {
		t.Fatalf("a grant outlived the restore that removed its device: %+v", reloaded)
	}
	if !reloaded.CanSee("lamp") {
		t.Fatalf("the restore dropped a grant for a device it kept: %+v", reloaded)
	}
}

func TestSessionDescribesTheAccount(t *testing.T) {
	handler, registry, _ := accessServer(t, lamp("lamp", "98:77:d5:a2:34:f2"))
	_, token, err := registry.Create("Priya", users.RoleRead, []string{"lamp"})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	var session sessionResponse
	w := as(t, handler, token, http.MethodGet, "/api/session", "")
	if err := json.NewDecoder(w.Body).Decode(&session); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if session.Admin || session.AllDevices || session.Role != users.RoleRead ||
		session.Name != "Priya" || len(session.Devices) != 1 {
		t.Fatalf("user session = %+v", session)
	}

	var admin sessionResponse
	w = as(t, handler, "admin-token", http.MethodGet, "/api/session", "")
	if err := json.NewDecoder(w.Body).Decode(&admin); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !admin.Admin || !admin.AllDevices || admin.Role != users.RoleModify || !admin.Users {
		t.Fatalf("admin session = %+v", admin)
	}
}

// The administrator comes from the environment, so an installation with a
// broken or emptied users section can still be repaired by its owner.
func TestAdminTokenWorksWithoutAUserRegistry(t *testing.T) {
	bus := events.NewBus()
	mgr := manager.New(bus, nil)
	t.Cleanup(mgr.Close)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := New(Options{Manager: mgr, Bus: bus, Token: "admin-token", Logger: log})
	handler := srv.Handler()

	if w := as(t, handler, "admin-token", http.MethodGet, "/api/devices", ""); w.Code != http.StatusOK {
		t.Fatalf("admin status = %d, want 200", w.Code)
	}
	if w := as(t, handler, "anything-else", http.MethodGet, "/api/devices", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("unknown token status = %d, want 401", w.Code)
	}
	if w := as(t, handler, "admin-token", http.MethodGet, "/api/users", ""); w.Code != http.StatusNotFound {
		t.Fatalf("user routes without a registry = %d, want 404", w.Code)
	}
}

func TestUserManagementRoundTrip(t *testing.T) {
	handler, _, _ := accessServer(t, lamp("lamp", "98:77:d5:a2:34:f2"))

	w := as(t, handler, "admin-token", http.MethodPost, "/api/users", `{"name":"Priya","role":"read","devices":["lamp"]}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201: %s", w.Code, w.Body.String())
	}
	var created userResponse
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.Token == "" || created.User.ID == "" {
		t.Fatalf("create response = %+v", created)
	}

	// A grant naming hardware that does not exist is a typo, and a typo that is
	// accepted looks like a device that never appears.
	bad := as(t, handler, "admin-token", http.MethodPatch, "/api/users/"+created.User.ID, `{"devices":["ghost"]}`)
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("unknown device grant status = %d, want 400: %s", bad.Code, bad.Body.String())
	}

	rotated := as(t, handler, "admin-token", http.MethodPost, "/api/users/"+created.User.ID+"/token", "")
	var second userResponse
	if err := json.NewDecoder(rotated.Body).Decode(&second); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if second.Token == "" || second.Token == created.Token {
		t.Fatalf("rotation returned %q", second.Token)
	}
	if w := as(t, handler, created.Token, http.MethodGet, "/api/devices", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("the old token still works: %d", w.Code)
	}

	if w := as(t, handler, "admin-token", http.MethodDelete, "/api/users/"+created.User.ID, ""); w.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204", w.Code)
	}
	if w := as(t, handler, second.Token, http.MethodGet, "/api/devices", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("a deleted user's token still works: %d", w.Code)
	}
}

// A client stuck in a retry loop must not be able to drive one device as fast
// as the LAN allows — but an ordinary burst of slider commits still lands.
func TestCommandsAreRateLimitedPerDevice(t *testing.T) {
	handler, _, _ := accessServer(t, lamp("lamp", "98:77:d5:a2:34:f2"), lamp("tv", "98:77:d5:a2:34:f3"))

	for i := range commandBurst {
		if w := as(t, handler, "admin-token", http.MethodPost, "/api/devices/lamp/command", `{"action":"on"}`); w.Code != http.StatusOK {
			t.Fatalf("command %d status = %d, want 200 within the burst: %s", i, w.Code, w.Body.String())
		}
	}
	w := as(t, handler, "admin-token", http.MethodPost, "/api/devices/lamp/command", `{"action":"on"}`)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("status past the burst = %d, want 429", w.Code)
	}
	if w.Header().Get("Retry-After") == "" {
		t.Fatal("429 without a Retry-After header")
	}
	// A busy device must not starve the others.
	if other := as(t, handler, "admin-token", http.MethodPost, "/api/devices/tv/command", `{"action":"on"}`); other.Code != http.StatusOK {
		t.Fatalf("a second device was limited too: %d", other.Code)
	}
	// A refresh is a hardware read too, so it shares the budget rather than
	// offering a second way to hammer the same device.
	if refresh := as(t, handler, "admin-token", http.MethodPost, "/api/devices/lamp/refresh", ""); refresh.Code != http.StatusTooManyRequests {
		t.Fatalf("refresh past the budget = %d, want 429", refresh.Code)
	}
}

// User ids come from names, so somebody called "Admin" gets the id "admin".
// Their budget must still be their own.
func TestRateLimitKeysDistinguishAccounts(t *testing.T) {
	handler, registry, _ := accessServer(t, lamp("lamp", "98:77:d5:a2:34:f2"))
	_, token, err := registry.Create("Admin", users.RoleRead, []string{"lamp"})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	for range commandBurst {
		as(t, handler, "admin-token", http.MethodPost, "/api/devices/lamp/command", `{"action":"on"}`)
	}
	if w := as(t, handler, "admin-token", http.MethodPost, "/api/devices/lamp/command", `{"action":"on"}`); w.Code != http.StatusTooManyRequests {
		t.Fatalf("the administrator was not limited: %d", w.Code)
	}
	if w := as(t, handler, token, http.MethodPost, "/api/devices/lamp/command", `{"action":"on"}`); w.Code != http.StatusOK {
		t.Fatalf("an account sharing the administrator's id was limited with them: %d", w.Code)
	}
}

// The probe says whether this process is serving HTTP and nothing else: no
// token, and nothing about the installation behind it.
func TestHealthzIsPublicAndDisclosesNothing(t *testing.T) {
	handler, _, _ := accessServer(t, lamp("lamp", "98:77:d5:a2:34:f2"))

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body) != 1 || body["status"] != "ok" {
		t.Fatalf("body = %v, want only a status", body)
	}
}
