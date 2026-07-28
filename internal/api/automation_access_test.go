package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"testing"

	"setu/internal/automation"
	"setu/internal/config"
	"setu/internal/device"
	"setu/internal/events"
	"setu/internal/manager"
	"setu/internal/store"
	"setu/internal/users"
)

// scopedServer has two devices and an automation engine, so a restricted
// account can be given one device and tested against rules touching both.
func scopedServer(t *testing.T) (http.Handler, *users.Registry, *automation.Engine) {
	t.Helper()
	bus := events.NewBus()
	lamp := &storedDevice{spec: config.DeviceSpec{ID: "lamp", Brand: "test", Driver: "lamp", Name: "Lamp", MAC: "02:00:00:00:00:01"}}
	tv := &storedDevice{spec: config.DeviceSpec{ID: "tv", Brand: "test", Driver: "lamp", Name: "TV", MAC: "02:00:00:00:00:02"}}
	mgr := manager.New(bus, []device.Device{lamp, tv})
	t.Cleanup(mgr.Close)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	file := store.New(filepath.Join(t.TempDir(), "setu.json"))
	engine, err := automation.New(mgr, bus, automation.NewStore(file), nil, log)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := users.New(file)
	if err != nil {
		t.Fatal(err)
	}
	srv := New(Options{Manager: mgr, Bus: bus, Automation: engine, Users: registry, Token: "admin-token", Logger: log})
	return srv.Handler(), registry, engine
}

func rule(id, deviceID string) automation.Rule {
	return automation.Rule{
		ID: id, Name: id, Enabled: true,
		Trigger: automation.Trigger{Type: automation.TriggerWebhook, Webhook: &automation.Webhook{}},
		Actions: []automation.Action{{DeviceID: deviceID, Action: "on"}},
	}
}

func seedRules(t *testing.T, engine *automation.Engine, rules ...automation.Rule) {
	t.Helper()
	if _, err := engine.Replace(automation.State{
		Version:  automation.FormatVersion,
		Revision: engine.Snapshot().Revision,
		Items:    rules,
	}); err != nil {
		t.Fatalf("seed rules: %v", err)
	}
}

// A restricted account sees only the rules it could have written itself. A rule
// reaching a device it was never granted is not its business.
func TestAutomationsAreScopedToGrantedDevices(t *testing.T) {
	handler, registry, engine := scopedServer(t)
	seedRules(t, engine, rule("lamp-rule", "lamp"), rule("tv-rule", "tv"))
	_, token, err := registry.Create("Priya", users.RoleModify, []string{"lamp"})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	w := as(t, handler, token, http.MethodGet, "/api/automations", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	var snapshot automation.Snapshot
	if err := json.NewDecoder(w.Body).Decode(&snapshot); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(snapshot.Items) != 1 || snapshot.Items[0].ID != "lamp-rule" {
		t.Fatalf("rules = %+v, want only lamp-rule", snapshot.Items)
	}
}

// Automations are how a device gets controlled with nobody touching it. An
// account that could write a rule for a device it cannot see would have its
// device restriction only on paper.
func TestRestrictedAccountCannotWriteRulesForOtherDevices(t *testing.T) {
	handler, registry, engine := scopedServer(t)
	seedRules(t, engine, rule("lamp-rule", "lamp"))
	_, token, err := registry.Create("Priya", users.RoleModify, []string{"lamp"})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	body := `{"version":1,"revision":` + revisionOf(t, engine) + `,"paused":false,"items":[
		{"id":"lamp-rule","name":"lamp-rule","enabled":true,"trigger":{"type":"webhook","webhook":{}},"actions":[{"device_id":"lamp","action":"on"}]},
		{"id":"sneaky","name":"sneaky","enabled":true,"trigger":{"type":"webhook","webhook":{}},"actions":[{"device_id":"tv","action":"on"}]}]}`
	w := as(t, handler, token, http.MethodPut, "/api/automations", body)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", w.Code, w.Body.String())
	}
}

func TestRestrictedAccountCannotUseOtherDevicesInActionConditions(t *testing.T) {
	handler, registry, engine := scopedServer(t)
	_, token, err := registry.Create("Priya", users.RoleModify, []string{"lamp"})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	body := `{"version":1,"revision":` + revisionOf(t, engine) + `,"paused":false,"items":[
		{"id":"guarded","name":"guarded","enabled":true,"trigger":{"type":"webhook","webhook":{}},
		 "actions":[{"device_id":"lamp","action":"on","when":[{"device_id":"tv","on":true}]}]}]}`
	w := as(t, handler, token, http.MethodPut, "/api/automations", body)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", w.Code, w.Body.String())
	}
	if len(engine.Snapshot().Items) != 0 {
		t.Fatal("rule with an unauthorized action condition was stored")
	}
}

func TestRestrictedAccountCannotUseOtherDevicesInOnlineTriggers(t *testing.T) {
	handler, registry, engine := scopedServer(t)
	_, token, err := registry.Create("Priya", users.RoleModify, []string{"lamp"})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	body := `{"version":1,"revision":` + revisionOf(t, engine) + `,"paused":false,"items":[
		{"id":"recovered","name":"recovered","enabled":true,
		 "trigger":{"type":"device_online","online":{"device_id":"tv","operator":"above","minutes":10}},
		 "actions":[{"device_id":"lamp","action":"on"}]}]}`
	w := as(t, handler, token, http.MethodPut, "/api/automations", body)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", w.Code, w.Body.String())
	}
	if len(engine.Snapshot().Items) != 0 {
		t.Fatal("rule with an unauthorized online trigger was stored")
	}
}

// The account was only ever shown its own rules, so a straight replace would
// delete everyone else's. The server carries the rest through untouched.
func TestRestrictedSaveKeepsRulesItCannotSee(t *testing.T) {
	handler, registry, engine := scopedServer(t)
	seedRules(t, engine, rule("lamp-rule", "lamp"), rule("tv-rule", "tv"))
	_, token, err := registry.Create("Priya", users.RoleModify, []string{"lamp"})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	// Priya deletes her own rule and adds another; the TV rule is not in her
	// request at all because she never received it.
	body := `{"version":1,"revision":` + revisionOf(t, engine) + `,"paused":false,"items":[
		{"id":"lamp-two","name":"lamp-two","enabled":true,"trigger":{"type":"webhook","webhook":{}},"actions":[{"device_id":"lamp","action":"off"}]}]}`
	w := as(t, handler, token, http.MethodPut, "/api/automations", body)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}

	stored := engine.Snapshot().Items
	ids := make(map[string]bool, len(stored))
	for _, item := range stored {
		ids[item.ID] = true
	}
	if !ids["tv-rule"] {
		t.Fatalf("the restricted save deleted a rule it could not see: %+v", stored)
	}
	if ids["lamp-rule"] || !ids["lamp-two"] {
		t.Fatalf("the account's own edit did not apply: %+v", stored)
	}
}

// Running someone else's rule would reach the devices in it. The id is refused
// even though the engine would happily run it.
func TestRestrictedAccountCannotRunRulesItCannotSee(t *testing.T) {
	handler, registry, engine := scopedServer(t)
	seedRules(t, engine, rule("tv-rule", "tv"))
	_, token, err := registry.Create("Priya", users.RoleModify, []string{"lamp"})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	if w := as(t, handler, token, http.MethodPost, "/api/automations/tv-rule/run", ""); w.Code != http.StatusForbidden {
		t.Fatalf("run status = %d, want 403: %s", w.Code, w.Body.String())
	}
	if w := as(t, handler, token, http.MethodPost, "/api/automations/tv-rule/token", ""); w.Code != http.StatusForbidden {
		t.Fatalf("rotate status = %d, want 403: %s", w.Code, w.Body.String())
	}
	if w := as(t, handler, "admin-token", http.MethodPost, "/api/automations/tv-rule/run", ""); w.Code != http.StatusAccepted {
		t.Fatalf("the administrator was refused: %d %s", w.Code, w.Body.String())
	}
}

// Running a rule runs the actions of every rule it calls, so calling a rule is
// exactly as powerful as owning it. Naming an id it was never shown must not be
// a way around the device restriction.
func TestRestrictedAccountCannotCallARuleItCannotSee(t *testing.T) {
	handler, registry, engine := scopedServer(t)
	seedRules(t, engine, rule("tv-rule", "tv"))
	_, token, err := registry.Create("Priya", users.RoleModify, []string{"lamp"})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	// Every device Priya names is her own; the reach is entirely in the call.
	body := `{"version":1,"revision":` + revisionOf(t, engine) + `,"paused":false,"items":[
		{"id":"proxy","name":"proxy","enabled":true,"trigger":{"type":"webhook","webhook":{}},
		 "actions":[{"device_id":"lamp","action":"on"},{"action":"run_automation","automation_id":"tv-rule"}]}]}`
	w := as(t, handler, token, http.MethodPut, "/api/automations", body)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", w.Code, w.Body.String())
	}
	for _, item := range engine.Snapshot().Items {
		if item.ID == "proxy" {
			t.Fatal("the proxying rule was stored anyway")
		}
	}
}

// The same reasoning one step removed: a rule is only owned when everything it
// can reach, however indirectly, is owned too.
func TestOwnershipOfNestedCallsCascades(t *testing.T) {
	handler, registry, engine := scopedServer(t)
	reach := automation.Rule{
		ID: "reach", Name: "reach", Enabled: true,
		Trigger: automation.Trigger{Type: automation.TriggerWebhook, Webhook: &automation.Webhook{}},
		Actions: []automation.Action{{DeviceID: "tv", Action: "on"}},
	}
	middle := automation.Rule{
		ID: "middle", Name: "middle", Enabled: true,
		Trigger: automation.Trigger{Type: automation.TriggerWebhook, Webhook: &automation.Webhook{}},
		Actions: []automation.Action{{Action: automation.ActionAutomation, AutomationID: "reach"}},
	}
	// "middle" names no device at all, so only the cascade can disqualify it.
	seedRules(t, engine, reach, middle, rule("lamp-rule", "lamp"))
	_, token, err := registry.Create("Priya", users.RoleModify, []string{"lamp"})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	w := as(t, handler, token, http.MethodGet, "/api/automations", "")
	var snapshot automation.Snapshot
	if err := json.NewDecoder(w.Body).Decode(&snapshot); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(snapshot.Items) != 1 || snapshot.Items[0].ID != "lamp-rule" {
		t.Fatalf("rules = %+v, want only lamp-rule; a call chain leaked", snapshot.Items)
	}
	if w := as(t, handler, token, http.MethodPost, "/api/automations/middle/run", ""); w.Code != http.StatusForbidden {
		t.Fatalf("running the chain = %d, want 403: %s", w.Code, w.Body.String())
	}
}

// Pausing is installation-wide: it would stop rules a restricted account was
// never shown, so only the administrator may do it.
func TestRestrictedAccountCannotPauseTheInstallation(t *testing.T) {
	handler, registry, engine := scopedServer(t)
	seedRules(t, engine, rule("lamp-rule", "lamp"))
	_, token, err := registry.Create("Priya", users.RoleModify, []string{"lamp"})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	body := `{"version":1,"revision":` + revisionOf(t, engine) + `,"paused":true,"items":[
		{"id":"lamp-rule","name":"lamp-rule","enabled":true,"trigger":{"type":"webhook","webhook":{}},"actions":[{"device_id":"lamp","action":"on"}]}]}`
	if w := as(t, handler, token, http.MethodPut, "/api/automations", body); w.Code != http.StatusOK {
		t.Fatalf("save status = %d, want 200: %s", w.Code, w.Body.String())
	}
	if engine.Snapshot().Paused {
		t.Fatal("a restricted account paused the whole installation")
	}

	// The administrator still can, and the restricted account keeps saving.
	admin := `{"version":1,"revision":` + revisionOf(t, engine) + `,"paused":true,"items":[
		{"id":"lamp-rule","name":"lamp-rule","enabled":true,"trigger":{"type":"webhook","webhook":{}},"actions":[{"device_id":"lamp","action":"on"}]}]}`
	if w := as(t, handler, "admin-token", http.MethodPut, "/api/automations", admin); w.Code != http.StatusOK {
		t.Fatalf("admin pause = %d: %s", w.Code, w.Body.String())
	}
	if !engine.Snapshot().Paused {
		t.Fatal("the administrator could not pause")
	}
	stay := `{"version":1,"revision":` + revisionOf(t, engine) + `,"paused":false,"items":[
		{"id":"lamp-rule","name":"lamp-rule","enabled":false,"trigger":{"type":"webhook","webhook":{}},"actions":[{"device_id":"lamp","action":"on"}]}]}`
	if w := as(t, handler, token, http.MethodPut, "/api/automations", stay); w.Code != http.StatusOK {
		t.Fatalf("restricted save while paused = %d: %s", w.Code, w.Body.String())
	}
	if !engine.Snapshot().Paused {
		t.Fatal("a restricted save resumed the installation")
	}
}

// A "read" account operates its devices; it does not write the rules that
// operate them on their own.
func TestReadAccountCannotWriteAutomations(t *testing.T) {
	handler, registry, engine := scopedServer(t)
	seedRules(t, engine, rule("lamp-rule", "lamp"))
	_, token, err := registry.Create("Priya", users.RoleRead, []string{"lamp"})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	if w := as(t, handler, token, http.MethodGet, "/api/automations", ""); w.Code != http.StatusOK {
		t.Fatalf("read account cannot see its automations: %d", w.Code)
	}
	body := `{"version":1,"revision":` + revisionOf(t, engine) + `,"paused":true,"items":[]}`
	if w := as(t, handler, token, http.MethodPut, "/api/automations", body); w.Code != http.StatusForbidden {
		t.Fatalf("save status = %d, want 403: %s", w.Code, w.Body.String())
	}
	if w := as(t, handler, token, http.MethodGet, "/api/automations/export", ""); w.Code != http.StatusForbidden {
		t.Fatalf("export status = %d, want 403", w.Code)
	}
}

func revisionOf(t *testing.T, engine *automation.Engine) string {
	t.Helper()
	encoded, err := json.Marshal(engine.Snapshot().Revision)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}
