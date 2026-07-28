package users

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"setu/internal/config"
	"setu/internal/store"
)

func newRegistry(t *testing.T) (*Registry, *store.Store) {
	t.Helper()
	file := store.New(filepath.Join(t.TempDir(), "setu.json"))
	registry, err := New(file)
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	return registry, file
}

// A token exists once, in the response that created it. Everything afterwards
// works from its hash — including the file someone may copy off the device.
func TestTokenIsReturnedOnceAndStoredHashed(t *testing.T) {
	registry, file := newRegistry(t)

	user, token, err := registry.Create("Priya", RoleRead, []string{"lamp"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !strings.HasPrefix(token, TokenPrefix) {
		t.Fatalf("token = %q, want the %q prefix", token, TokenPrefix)
	}
	if user.TokenHash != "" {
		t.Fatalf("listed user carries a token hash: %+v", user)
	}

	state, err := file.Load()
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	if strings.Contains(string(state.Users), token) {
		t.Fatal("the state file contains the plaintext token")
	}

	authenticated, ok := registry.Authenticate(token)
	if !ok || authenticated.ID != user.ID {
		t.Fatalf("authenticate = %+v, %v; want %s", authenticated, ok, user.ID)
	}
	if _, ok := registry.Authenticate(token + "x"); ok {
		t.Fatal("a wrong token authenticated")
	}
}

// Rotation is the recovery path for a token that leaked: the replacement works
// and the old one stops working in the same step.
func TestRotateInvalidatesThePreviousToken(t *testing.T) {
	registry, _ := newRegistry(t)
	user, first, err := registry.Create("Arjun", RoleModify, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	_, second, err := registry.RotateToken(user.ID)
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if first == second {
		t.Fatal("rotation returned the same token")
	}
	if _, ok := registry.Authenticate(first); ok {
		t.Fatal("the rotated-away token still authenticates")
	}
	if _, ok := registry.Authenticate(second); !ok {
		t.Fatal("the new token does not authenticate")
	}
}

func TestDeleteEndsAccessImmediately(t *testing.T) {
	registry, _ := newRegistry(t)
	user, token, err := registry.Create("Guest", RoleRead, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := registry.Delete(user.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, ok := registry.Authenticate(token); ok {
		t.Fatal("a deleted user still authenticates")
	}
	if err := registry.Delete(user.ID); err != ErrNotFound {
		t.Fatalf("second delete = %v, want ErrNotFound", err)
	}
}

// Access is per device and explicit: a user sees exactly what was granted, and
// a role is separate from that list.
func TestAccessIsExplicitPerDevice(t *testing.T) {
	registry, _ := newRegistry(t)
	user, _, err := registry.Create("Priya", RoleRead, []string{"lamp", "lamp", " fan "})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if len(user.Devices) != 2 || user.Devices[0] != "lamp" || user.Devices[1] != "fan" {
		t.Fatalf("devices = %v, want [lamp fan] de-duplicated and trimmed", user.Devices)
	}
	if !user.CanSee("lamp") || user.CanSee("tv") {
		t.Fatalf("grants = %v; tv must not be visible", user.Devices)
	}
	if user.CanModify() {
		t.Fatal("a read user may not modify")
	}

	modify := RoleModify
	updated, err := registry.Update(user.ID, Patch{Role: &modify})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if !updated.CanModify() || len(updated.Devices) != 2 {
		t.Fatalf("a role change rewrote the grants: %+v", updated)
	}
}

// Removing hardware must take its grants with it: ids are derived from the
// brand and MAC, so the same id can come back on a re-add.
func TestForgetDeviceClearsGrants(t *testing.T) {
	registry, _ := newRegistry(t)
	user, _, err := registry.Create("Priya", RoleRead, []string{"lamp", "fan"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := registry.ForgetDevice("lamp"); err != nil {
		t.Fatalf("forget: %v", err)
	}
	reloaded, ok := registry.Get(user.ID)
	if !ok {
		t.Fatal("user disappeared")
	}
	if reloaded.CanSee("lamp") || !reloaded.CanSee("fan") {
		t.Fatalf("grants after forget = %v, want [fan]", reloaded.Devices)
	}
}

func TestRetainDevicesAndStateUpdateCommitTogether(t *testing.T) {
	registry, file := newRegistry(t)
	user, _, err := registry.Create("Priya", RoleRead, []string{"lamp"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := file.Update(func(state *store.State) error {
		state.Devices = []config.DeviceSpec{{
			ID: "lamp", Brand: "test", Driver: "lamp", Name: "Lamp", MAC: "02:00:00:00:00:01",
		}}
		return nil
	}); err != nil {
		t.Fatalf("seed devices: %v", err)
	}

	sentinel := errors.New("device mutation failed")
	err = registry.RetainDevicesAndUpdateState(nil, func(state *store.State) error {
		state.Devices = nil
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("combined update = %v, want sentinel", err)
	}
	reloaded, _ := registry.Get(user.ID)
	if !reloaded.CanSee("lamp") {
		t.Fatal("failed device mutation still committed the in-memory grant cleanup")
	}
	state, err := file.Load()
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	if len(state.Devices) != 1 || state.Devices[0].ID != "lamp" {
		t.Fatalf("failed combined update changed devices: %+v", state.Devices)
	}
	var persisted State
	if err := json.Unmarshal(state.Users, &persisted); err != nil {
		t.Fatalf("decode users: %v", err)
	}
	if len(persisted.Items) != 1 || !persisted.Items[0].CanSee("lamp") {
		t.Fatalf("failed combined update changed persisted grants: %+v", persisted.Items)
	}

	if err := registry.RetainDevicesAndUpdateState(nil, func(state *store.State) error {
		state.Devices = nil
		return nil
	}); err != nil {
		t.Fatalf("combined update: %v", err)
	}
	reloaded, _ = registry.Get(user.ID)
	if reloaded.CanSee("lamp") {
		t.Fatal("successful combined update kept the in-memory grant")
	}
	state, err = file.Load()
	if err != nil {
		t.Fatalf("reload state: %v", err)
	}
	if len(state.Devices) != 0 {
		t.Fatalf("successful combined update kept devices: %+v", state.Devices)
	}
	if err := json.Unmarshal(state.Users, &persisted); err != nil {
		t.Fatalf("decode updated users: %v", err)
	}
	if len(persisted.Items) != 1 || persisted.Items[0].CanSee("lamp") {
		t.Fatalf("successful combined update kept persisted grant: %+v", persisted.Items)
	}
}

func TestGrantIsIdempotent(t *testing.T) {
	registry, _ := newRegistry(t)
	user, _, err := registry.Create("Arjun", RoleModify, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	for range 3 {
		if err := registry.Grant(user.ID, "tv"); err != nil {
			t.Fatalf("grant: %v", err)
		}
	}
	reloaded, _ := registry.Get(user.ID)
	if len(reloaded.Devices) != 1 || !reloaded.CanSee("tv") {
		t.Fatalf("grants = %v, want [tv] once", reloaded.Devices)
	}
}

func TestUsersSurviveAReload(t *testing.T) {
	file := store.New(filepath.Join(t.TempDir(), "setu.json"))
	first, err := New(file)
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	user, token, err := first.Create("Priya", RoleModify, []string{"lamp"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	second, err := New(file)
	if err != nil {
		t.Fatalf("reload registry: %v", err)
	}
	reloaded, ok := second.Authenticate(token)
	if !ok {
		t.Fatal("the token does not authenticate after a restart")
	}
	if reloaded.ID != user.ID || reloaded.Role != RoleModify || !reloaded.CanSee("lamp") {
		t.Fatalf("reloaded user = %+v", reloaded)
	}
}

func TestInvalidInputIsRefused(t *testing.T) {
	registry, _ := newRegistry(t)
	if _, _, err := registry.Create("", RoleRead, nil); err == nil {
		t.Fatal("a nameless user was created")
	}
	if _, _, err := registry.Create("Priya", "superuser", nil); err == nil {
		t.Fatal("an unknown role was accepted")
	}
	if _, _, err := registry.Create(strings.Repeat("x", MaxNameLength+1), RoleRead, nil); err == nil {
		t.Fatal("an over-long name was accepted")
	}
}

// Names are what people type; ids are what URLs and grants use. Two people
// called the same thing must still be two accounts.
func TestIdsAreDerivedAndUnique(t *testing.T) {
	registry, _ := newRegistry(t)
	first, _, err := registry.Create("Priya Sharma", RoleRead, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	second, _, err := registry.Create("Priya Sharma", RoleRead, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if first.ID != "priyasharma" || second.ID != "priyasharma_2" {
		t.Fatalf("ids = %q, %q", first.ID, second.ID)
	}

	emoji, _, err := registry.Create("🙂", RoleRead, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if emoji.ID != "user" {
		t.Fatalf("id for an unusable name = %q, want %q", emoji.ID, "user")
	}
}

func TestRegistryIsBounded(t *testing.T) {
	registry, _ := newRegistry(t)
	for i := range MaxUsers {
		if _, _, err := registry.Create("person", RoleRead, nil); err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
	}
	if _, _, err := registry.Create("one too many", RoleRead, nil); err != ErrFull {
		t.Fatalf("create beyond the limit = %v, want ErrFull", err)
	}
}
