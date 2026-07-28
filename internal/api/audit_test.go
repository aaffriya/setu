package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"setu/internal/automation"
	"setu/internal/config"
	"setu/internal/users"
)

// A restricted save answers with the caller's own rules. The whole rule set is
// what the engine stores, but returning it would hand back exactly what GET
// spent its filtering avoiding: another account's routine names, the device ids
// behind them, and the webhook path each one answers on.
func TestRestrictedAutomationSaveDoesNotReturnOtherRules(t *testing.T) {
	handler, registry, engine := scopedServer(t)
	seedRules(t, engine, rule("lamp-rule", "lamp"), rule("tv-rule", "tv"))
	_, token, err := registry.Create("Priya", users.RoleModify, []string{"lamp"})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	body, err := json.Marshal(automation.State{
		Version:  automation.FormatVersion,
		Revision: engine.Snapshot().Revision,
		Items:    []automation.Rule{rule("lamp-rule", "lamp")},
	})
	if err != nil {
		t.Fatal(err)
	}
	response := as(t, handler, token, http.MethodPut, "/api/automations", string(body))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", response.Code, response.Body.String())
	}
	var update automation.Update
	if err := json.NewDecoder(response.Body).Decode(&update); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, item := range update.State.Items {
		if item.ID != "lamp-rule" {
			t.Fatalf("save response leaked rule %q to an account that cannot see it", item.ID)
		}
	}
	if len(update.State.Items) != 1 {
		t.Fatalf("returned %d rules, want the caller's 1", len(update.State.Items))
	}
	// The engine must still hold both: filtering the answer may not delete
	// anything.
	if stored := engine.Snapshot().Items; len(stored) != 2 {
		t.Fatalf("engine holds %d rules, want 2", len(stored))
	}
}

// Rotating a webhook token is the same disclosure through a smaller door.
func TestRestrictedWebhookRotationDoesNotReturnOtherRules(t *testing.T) {
	handler, registry, engine := scopedServer(t)
	seedRules(t, engine, rule("lamp-rule", "lamp"), rule("tv-rule", "tv"))
	_, token, err := registry.Create("Priya", users.RoleModify, []string{"lamp"})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	response := as(t, handler, token, http.MethodPost, "/api/automations/lamp-rule/token", "")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", response.Code, response.Body.String())
	}
	var body struct {
		Token string           `json:"token"`
		State automation.State `json:"state"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Token == "" {
		t.Fatal("no token was issued")
	}
	for _, item := range body.State.Items {
		if item.ID != "lamp-rule" {
			t.Fatalf("rotation response leaked rule %q", item.ID)
		}
	}
}

// Running a rule by hand reaches the same hardware a command does. Without a
// budget of its own it is the way around the per-device one: a loop on this
// route drives a bulb or a TV as fast as the LAN allows.
func TestManualAutomationRunsAreRateLimited(t *testing.T) {
	handler, registry, engine := scopedServer(t)
	seedRules(t, engine, rule("lamp-rule", "lamp"))
	_, token, err := registry.Create("Priya", users.RoleModify, []string{"lamp"})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	limited := false
	for attempt := 0; attempt < commandBurst+5; attempt++ {
		response := as(t, handler, token, http.MethodPost, "/api/automations/lamp-rule/run", "")
		if response.Code == http.StatusTooManyRequests {
			limited = true
			break
		}
		if response.Code != http.StatusAccepted {
			t.Fatalf("attempt %d: status = %d: %s", attempt, response.Code, response.Body.String())
		}
	}
	if !limited {
		t.Fatalf("%d rapid manual runs were all accepted; the device budget is bypassed", commandBurst+5)
	}
}

// A stored device this build cannot construct is kept on purpose — deleting
// someone's hardware because a downgrade dropped its driver would be worse.
// Keeping it is only defensible if it can still be repaired or removed, and
// both of those go through routes that ask the manager, which never saw it.
func TestUnusableStoredDeviceCanStillBeRepairedAndRemoved(t *testing.T) {
	broken := config.DeviceSpec{ID: "ghost", Brand: "test", Driver: "unknown", Name: "Ghost", MAC: "98:77:d5:a2:34:f9"}
	handler, inv, mgr := deviceServer(t, lamp("desk", "98:77:d5:a2:34:f2"), broken)

	if _, live := mgr.Device("ghost"); live {
		t.Fatal("precondition: the broken spec should not be live")
	}
	if specs := inv.Specs(); len(specs) != 2 {
		t.Fatalf("stored specs = %d, want the broken one kept", len(specs))
	}

	// The entry is addressable by id — which is how a backup export names it —
	// even though the manager never saw it.
	if response := deviceRequest(t, handler, http.MethodPatch, "/api/devices/ghost", `{"name":"Repaired"}`); response.Code == http.StatusNotFound {
		t.Fatalf("PATCH on a stored-but-unusable device = 404; it can never be repaired: %s", response.Body.String())
	}
	if response := deviceRequest(t, handler, http.MethodDelete, "/api/devices/ghost", ""); response.Code != http.StatusNoContent {
		t.Fatalf("DELETE on a stored-but-unusable device = %d, want 204: %s", response.Code, response.Body.String())
	}
	for _, spec := range inv.Specs() {
		if spec.ID == "ghost" {
			t.Fatal("the entry is still stored after a successful delete")
		}
	}
	// The working device beside it is untouched.
	if _, live := mgr.Device("desk"); !live {
		t.Fatal("removing the broken entry disturbed the working device")
	}
}

// A grant is permission to use a device, not to take it away from everyone else
// who has one. Adding hardware affects only the account that adds it; removing
// it is a whole-installation change, so it sits with the administrator next to
// export and restore (see the users package doc).
func TestOnlyTheAdministratorMayDeleteADevice(t *testing.T) {
	handler, registry, mgr := accessServer(t, lamp("lamp", "98:77:d5:a2:34:f2"))
	_, token, err := registry.Create("Priya", users.RoleModify, []string{"lamp"})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	response := as(t, handler, token, http.MethodDelete, "/api/devices/lamp", "")
	if response.Code != http.StatusForbidden {
		t.Fatalf("delete by a modify account = %d, want 403: %s", response.Code, response.Body.String())
	}
	if _, live := mgr.Device("lamp"); !live {
		t.Fatal("the device was removed by an account that may not remove it")
	}

	// The rest of what "modify" means is untouched: it may still add and rename.
	if response := as(t, handler, token, http.MethodPatch, "/api/devices/lamp", `{"name":"Reading lamp"}`); response.Code != http.StatusOK {
		t.Fatalf("rename by a modify account = %d, want 200: %s", response.Code, response.Body.String())
	}
	if response := as(t, handler, token, http.MethodPost, "/api/devices", `{"brand":"test","driver":"lamp","name":"Shelf","mac":"98:77:d5:a2:34:f3"}`); response.Code != http.StatusCreated {
		t.Fatalf("add by a modify account = %d, want 201: %s", response.Code, response.Body.String())
	}

	// And the administrator still can.
	if response := as(t, handler, "admin-token", http.MethodDelete, "/api/devices/lamp", ""); response.Code != http.StatusNoContent {
		t.Fatalf("delete by the administrator = %d, want 204: %s", response.Code, response.Body.String())
	}
}

// A restricted save still receives the token for a hook it just created — that
// token is shown once and is the only way to use the hook — but never one for a
// rule it does not own.
func TestRestrictedSaveReceivesOnlyItsOwnWebhookTokens(t *testing.T) {
	handler, registry, engine := scopedServer(t)
	seedRules(t, engine, rule("tv-rule", "tv"))
	_, token, err := registry.Create("Priya", users.RoleModify, []string{"lamp"})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	body, err := json.Marshal(automation.State{
		Version:  automation.FormatVersion,
		Revision: engine.Snapshot().Revision,
		Items:    []automation.Rule{rule("my-rule", "lamp")},
	})
	if err != nil {
		t.Fatal(err)
	}
	response := as(t, handler, token, http.MethodPut, "/api/automations", string(body))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", response.Code, response.Body.String())
	}
	var update automation.Update
	if err := json.NewDecoder(response.Body).Decode(&update); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if update.GeneratedTokens["my-rule"] == "" {
		t.Fatal("the account received no token for the hook it just created")
	}
	for id := range update.GeneratedTokens {
		if id != "my-rule" {
			t.Fatalf("received a webhook token for rule %q, which this account does not own", id)
		}
	}
}

// Being addressable by id only helps someone who knows the id. A stored device
// that could not be started is in no device list and on no card, so the device
// screen has to be able to ask for it by name — with the reason, which is what
// turns "something is wrong" into "rename it" or "this build has no such
// driver".
func TestUnusableStoredDevicesAreListedWithTheirReason(t *testing.T) {
	noDriver := config.DeviceSpec{ID: "ghost", Brand: "test", Driver: "unknown", Name: "Ghost", MAC: "98:77:d5:a2:34:f9"}
	handler, inv, _ := deviceServer(t, lamp("desk", "98:77:d5:a2:34:f2"), noDriver)

	response := deviceRequest(t, handler, http.MethodGet, "/api/devices/unusable", "")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", response.Code, response.Body.String())
	}
	var listed []struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		MAC    string `json:"mac"`
		Reason string `json:"reason"`
	}
	if err := json.NewDecoder(response.Body).Decode(&listed); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("listed %d unusable devices, want only the broken one: %+v", len(listed), listed)
	}
	if listed[0].ID != "ghost" || listed[0].Name != "Ghost" || listed[0].MAC == "" {
		t.Fatalf("entry does not carry the stored spec: %+v", listed[0])
	}
	// The reason has to name the actual problem, not just report one.
	if !strings.Contains(listed[0].Reason, "unknown") || !strings.Contains(listed[0].Reason, "test") {
		t.Fatalf("reason %q does not say which driver is missing", listed[0].Reason)
	}

	// Removing it takes it off this list too.
	if response := deviceRequest(t, handler, http.MethodDelete, "/api/devices/ghost", ""); response.Code != http.StatusNoContent {
		t.Fatalf("delete = %d: %s", response.Code, response.Body.String())
	}
	if left := inv.Unusable(); len(left) != 0 {
		t.Fatalf("removed device is still listed as unusable: %+v", left)
	}
}

// The other half of the promise in inventory.New: an entry kept because a label
// was refused comes online again as soon as that label is edited, without a
// restart — and then it is a normal device, gone from the unusable list.
func TestRepairingAStoredDeviceBringsItOnlineAndClearsIt(t *testing.T) {
	// A name past the stored limit: written by hand, refused by validation, and
	// fixable with exactly the rename the device screen already offers.
	tooLong := config.DeviceSpec{
		ID: "shelf", Brand: "test", Driver: "lamp",
		Name: strings.Repeat("n", config.MaxNameLength+1), MAC: "98:77:d5:a2:34:f8",
	}
	handler, inv, mgr := deviceServer(t, tooLong)

	if _, live := mgr.Device("shelf"); live {
		t.Fatal("precondition: an over-long name should have been refused at load")
	}
	if broken := inv.Unusable(); len(broken) != 1 || broken[0].ID != "shelf" {
		t.Fatalf("unusable = %+v, want the shelf entry", broken)
	}

	response := deviceRequest(t, handler, http.MethodPatch, "/api/devices/shelf", `{"name":"Shelf"}`)
	if response.Code != http.StatusOK {
		t.Fatalf("repair = %d, want 200: %s", response.Code, response.Body.String())
	}
	if _, live := mgr.Device("shelf"); !live {
		t.Fatal("the repaired device did not come online")
	}
	if broken := inv.Unusable(); len(broken) != 0 {
		t.Fatalf("repaired device is still listed as unusable: %+v", broken)
	}
	// And it is now in the ordinary device list, which is where it belongs.
	response = deviceRequest(t, handler, http.MethodGet, "/api/devices", "")
	var views []struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(response.Body).Decode(&views); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(views) != 1 || views[0].ID != "shelf" {
		t.Fatalf("device list = %+v, want the repaired shelf", views)
	}
}

// Update edits the name and the model and nothing else, so whether a rename can
// help depends on what is actually wrong. Offering that edit on an entry whose
// driver is missing is a promise the server refuses on every attempt.
func TestUnusableDevicesSayWhetherAnEditCouldFixThem(t *testing.T) {
	noDriver := config.DeviceSpec{ID: "ghost", Brand: "test", Driver: "unknown", Name: "Ghost", MAC: "98:77:d5:a2:34:f9"}
	badLabel := config.DeviceSpec{
		ID: "shelf", Brand: "test", Driver: "lamp",
		Name: strings.Repeat("n", config.MaxNameLength+1), MAC: "98:77:d5:a2:34:f8",
	}
	badMAC := config.DeviceSpec{ID: "wired", Brand: "test", Driver: "lamp", Name: "Wired", MAC: "not-a-mac"}
	handler, _, _ := deviceServer(t, noDriver, badLabel, badMAC)

	response := deviceRequest(t, handler, http.MethodGet, "/api/devices/unusable", "")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	var listed []struct {
		ID         string `json:"id"`
		Repairable bool   `json:"repairable"`
	}
	if err := json.NewDecoder(response.Body).Decode(&listed); err != nil {
		t.Fatalf("decode: %v", err)
	}
	got := make(map[string]bool, len(listed))
	for _, entry := range listed {
		got[entry.ID] = entry.Repairable
	}
	// A refused label is the one case a rename fixes. A missing driver is not,
	// and neither is a MAC — identity is not editable, so no edit reaches it.
	want := map[string]bool{"shelf": true, "ghost": false, "wired": false}
	for id, expected := range want {
		if _, listedID := got[id]; !listedID {
			t.Fatalf("%q is missing from the unusable list: %+v", id, listed)
		}
		if got[id] != expected {
			t.Errorf("%q repairable = %v, want %v", id, got[id], expected)
		}
	}

	// The claim has to hold when acted on: the repairable one takes the edit.
	if response := deviceRequest(t, handler, http.MethodPatch, "/api/devices/shelf", `{"name":"Shelf"}`); response.Code != http.StatusOK {
		t.Fatalf("repairable entry refused the edit: %d %s", response.Code, response.Body.String())
	}
	// And the ones marked otherwise really do refuse it.
	for _, id := range []string{"ghost", "wired"} {
		if response := deviceRequest(t, handler, http.MethodPatch, "/api/devices/"+id, `{"name":"Renamed"}`); response.Code == http.StatusOK {
			t.Errorf("%q accepted a rename but was reported as not repairable", id)
		}
	}
}
