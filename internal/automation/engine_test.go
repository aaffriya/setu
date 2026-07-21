package automation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"setu/internal/device"
	"setu/internal/events"
	"setu/internal/manager"
)

type testSwitch struct {
	id  string
	bus *events.Bus

	mu    sync.Mutex
	state device.State
	ons   int
}

func (d *testSwitch) ID() string           { return d.id }
func (d *testSwitch) Name() string         { return d.id }
func (*testSwitch) Brand() string          { return "test" }
func (*testSwitch) Model() string          { return "switch" }
func (*testSwitch) MAC() string            { return "02:00:00:00:00:01" }
func (*testSwitch) Capabilities() []string { return []string{device.CapSwitch} }
func (d *testSwitch) State() device.State {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.state
}
func (d *testSwitch) On() error  { d.set(true); return nil }
func (d *testSwitch) Off() error { d.set(false); return nil }
func (d *testSwitch) set(on bool) {
	d.mu.Lock()
	d.state = device.State{Online: true, On: on}
	if on {
		d.ons++
	}
	state := d.state
	d.mu.Unlock()
	d.bus.Publish(events.Event{Type: events.StateChanged, DeviceID: d.id, State: state})
}
func (d *testSwitch) onCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.ons
}

func newTestEngine(t *testing.T, devices ...device.Device) *Engine {
	t.Helper()
	bus := events.NewBus()
	for _, raw := range devices {
		if dev, ok := raw.(*testSwitch); ok {
			dev.bus = bus
		}
	}
	mgr := manager.New(bus, devices)
	t.Cleanup(mgr.Close)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	engine, err := New(mgr, bus, NewStore(filepath.Join(t.TempDir(), stateFileName)), log)
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	done := make(chan struct{})
	go func() {
		engine.Run(ctx, ready)
		close(done)
	}()
	close(ready)
	t.Cleanup(func() {
		cancel()
		<-done
	})
	time.Sleep(20 * time.Millisecond)
	return engine
}

func webhookRule(id, target string) Rule {
	return Rule{
		ID:      id,
		Name:    "Webhook light",
		Enabled: true,
		Trigger: Trigger{Type: TriggerWebhook, Webhook: &Webhook{}},
		Actions: []Action{{DeviceID: target, Action: "on"}},
	}
}

func replaceRules(t *testing.T, engine *Engine, rules ...Rule) Update {
	t.Helper()
	update, err := engine.Replace(State{Version: FormatVersion, Revision: engine.Snapshot().Revision, Items: rules})
	if err != nil {
		t.Fatalf("replace rules: %v", err)
	}
	return update
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition was not met")
}

func TestWebhookSecretIsHashedAndDeliveryIsIdempotent(t *testing.T) {
	target := &testSwitch{id: "target"}
	engine := newTestEngine(t, target)
	update := replaceRules(t, engine, webhookRule("hook", target.id))
	token := update.GeneratedTokens["hook"]
	if token == "" {
		t.Fatal("new webhook token was not returned")
	}
	view := engine.Snapshot().Items[0].Trigger.Webhook
	if view.SecretHash != "" || !view.HasSecret {
		t.Fatalf("public webhook = %+v, want hash redacted and has_secret", view)
	}
	exported := engine.Export().Items[0].Trigger.Webhook
	if exported.SecretHash == "" || exported.SecretHash == token {
		t.Fatalf("exported webhook hash = %q", exported.SecretHash)
	}

	first, err := engine.TriggerWebhook("hook", token, "delivery-1")
	if err != nil || first.Status != "queued" || first.RunID == "" {
		t.Fatalf("first trigger = %+v, %v", first, err)
	}
	waitFor(t, func() bool { return target.onCount() == 1 })
	duplicate, err := engine.TriggerWebhook("hook", token, "delivery-1")
	if err != nil || duplicate.Status != "duplicate" || duplicate.RunID != first.RunID {
		t.Fatalf("duplicate trigger = %+v, %v", duplicate, err)
	}
	if target.onCount() != 1 {
		t.Fatalf("on count = %d, want 1", target.onCount())
	}
	if _, err := engine.TriggerWebhook("hook", "wrong-token-that-is-long-enough", ""); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("wrong token error = %v, want unauthorized", err)
	}
}

func TestOrdinaryEditPreservesWebhookSecret(t *testing.T) {
	target := &testSwitch{id: "target"}
	engine := newTestEngine(t, target)
	update := replaceRules(t, engine, webhookRule("hook", target.id))
	originalHash := engine.Export().Items[0].Trigger.Webhook.SecretHash

	view := engine.Snapshot().State
	view.Items[0].Name = "Renamed hook"
	edited, err := engine.Replace(view)
	if err != nil {
		t.Fatal(err)
	}
	if len(edited.GeneratedTokens) != 0 {
		t.Fatalf("ordinary edit generated a new token: %+v", edited.GeneratedTokens)
	}
	if got := engine.Export().Items[0].Trigger.Webhook.SecretHash; got != originalHash {
		t.Fatalf("secret hash changed from %q to %q", originalHash, got)
	}
	if update.GeneratedTokens["hook"] == "" {
		t.Fatal("initial token missing")
	}
}

func TestScheduleRunsOncePerMatchingMinute(t *testing.T) {
	target := &testSwitch{id: "target"}
	engine := newTestEngine(t, target)
	rule := Rule{
		ID:      "morning",
		Name:    "Morning",
		Enabled: true,
		Trigger: Trigger{Type: TriggerSchedule, Schedule: &Schedule{
			Time: "05:30", Weekdays: []int{1}, UTCOffsetMinutes: 330,
		}},
		Actions: []Action{{DeviceID: target.id, Action: "on"}},
	}
	replaceRules(t, engine, rule)
	now := time.Date(2026, time.July, 20, 0, 0, 10, 0, time.UTC) // Monday, 05:30 at +05:30.
	engine.evaluateSchedules(now)
	waitFor(t, func() bool { return target.onCount() == 1 })
	engine.evaluateSchedules(now.Add(20 * time.Second))
	time.Sleep(30 * time.Millisecond)
	if target.onCount() != 1 {
		t.Fatalf("schedule ran %d times in one minute, want 1", target.onCount())
	}
}

func TestStaleRevisionIsRejected(t *testing.T) {
	target := &testSwitch{id: "target"}
	engine := newTestEngine(t, target)
	replaceRules(t, engine, webhookRule("hook", target.id))
	_, err := engine.Replace(State{Version: FormatVersion, Revision: 0, Items: []Rule{webhookRule("other", target.id)}})
	if !errors.Is(err, ErrRevision) {
		t.Fatalf("stale replace error = %v, want ErrRevision", err)
	}
}

func TestStaleInternalTriggerCannotRunReplacementRule(t *testing.T) {
	target := &testSwitch{id: "target"}
	engine := newTestEngine(t, target)
	replaceRules(t, engine, webhookRule("same_id", target.id))
	oldRevision := engine.Snapshot().Revision

	state := engine.Snapshot().State
	state.Items[0].Name = "Replacement"
	if _, err := engine.Replace(state); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.enqueueAtRevision("same_id", "device", oldRevision); !errors.Is(err, ErrRevision) {
		t.Fatalf("stale internal trigger error = %v, want ErrRevision", err)
	}
	if target.onCount() != 0 {
		t.Fatal("stale internal trigger ran the replacement rule")
	}
}

func TestWebhookRateLimitIsBoundedPerRule(t *testing.T) {
	target := &testSwitch{id: "target"}
	engine := newTestEngine(t, target)
	token := replaceRules(t, engine, webhookRule("hook", target.id)).GeneratedTokens["hook"]
	for i := 0; i < webhookRate; i++ {
		if _, err := engine.TriggerWebhook("hook", token, ""); err != nil {
			t.Fatalf("delivery %d: %v", i+1, err)
		}
	}
	if _, err := engine.TriggerWebhook("hook", token, ""); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("delivery above limit error = %v, want ErrRateLimited", err)
	}
}

func TestDeviceRuleUsesStartupBaselineAndRunsOnEdge(t *testing.T) {
	source := &testSwitch{id: "source"}
	target := &testSwitch{id: "target"}
	engine := newTestEngine(t, source, target)
	rule := Rule{
		ID:      "relation",
		Name:    "Follow source",
		Enabled: true,
		Trigger: Trigger{Type: TriggerDeviceState, Device: &DeviceTrigger{DeviceID: source.id, On: true}},
		Actions: []Action{{DeviceID: target.id, Action: "on"}},
	}
	replaceRules(t, engine, rule)

	// Re-reporting the baseline is not a transition.
	source.bus.Publish(events.Event{Type: events.StateChanged, DeviceID: source.id, State: source.State()})
	time.Sleep(30 * time.Millisecond)
	if target.onCount() != 0 {
		t.Fatal("startup/baseline state triggered the relation")
	}
	source.set(true)
	waitFor(t, func() bool { return target.onCount() == 1 })
}

func TestOverflowRecoveryDrainsStaleEventsBeforeSnapshot(t *testing.T) {
	stream := make(chan events.Event, 3)
	stream <- events.Event{Type: events.StateChanged, DeviceID: "source", State: device.State{On: true}}
	stream <- events.Event{Type: events.StateChanged, DeviceID: "source", State: device.State{On: false}}
	stream <- events.Event{Type: events.StateChanged, DeviceID: "source", State: device.State{On: true}}

	if !drainPendingEvents(stream) {
		t.Fatal("open event stream reported closed")
	}
	select {
	case stale := <-stream:
		t.Fatalf("stale event remained after overflow recovery: %+v", stale)
	default:
	}
}

func TestPowerRelationCycleIsRejected(t *testing.T) {
	a := &testSwitch{id: "a"}
	b := &testSwitch{id: "b"}
	engine := newTestEngine(t, a, b)
	rules := []Rule{
		{ID: "a_to_b", Name: "A to B", Enabled: true, Trigger: Trigger{Type: TriggerDeviceState, Device: &DeviceTrigger{DeviceID: "a", On: true}}, Actions: []Action{{DeviceID: "b", Action: "on"}}},
		{ID: "b_to_a", Name: "B to A", Enabled: true, Trigger: Trigger{Type: TriggerDeviceState, Device: &DeviceTrigger{DeviceID: "b", On: true}}, Actions: []Action{{DeviceID: "a", Action: "on"}}},
	}
	_, err := engine.Replace(State{Version: FormatVersion, Revision: 0, Items: rules})
	var invalid ValidationError
	if !errors.As(err, &invalid) {
		t.Fatalf("cycle error = %v, want ValidationError", err)
	}
}

func TestDisabledMissingDeviceRuleIsPortable(t *testing.T) {
	target := &testSwitch{id: "target"}
	engine := newTestEngine(t, target)
	rule := webhookRule("portable", "missing")
	rule.Enabled = false
	if _, err := engine.Replace(State{Version: FormatVersion, Revision: 0, Items: []Rule{rule}}); err != nil {
		t.Fatalf("restore disabled missing-device rule: %v", err)
	}
	rule.Enabled = true
	_, err := engine.Replace(State{Version: FormatVersion, Revision: 1, Items: []Rule{rule}})
	var invalid ValidationError
	if !errors.As(err, &invalid) {
		t.Fatalf("enabled missing-device error = %v, want ValidationError", err)
	}
}

func TestNewDisablesRuleAfterDeviceCapabilityChanges(t *testing.T) {
	bus := events.NewBus()
	target := &testSwitch{id: "target", bus: bus}
	mgr := manager.New(bus, []device.Device{target})
	defer mgr.Close()
	path := filepath.Join(t.TempDir(), stateFileName)
	state := State{
		Version: FormatVersion,
		Items: []Rule{{
			ID:      "old_dimmer_rule",
			Name:    "Old dimmer rule",
			Enabled: true,
			Trigger: Trigger{Type: TriggerSchedule, Schedule: &Schedule{
				Time: "18:00", Weekdays: []int{0, 1, 2, 3, 4, 5, 6},
			}},
			Actions: []Action{{DeviceID: target.id, Action: "set_brightness", Value: json.RawMessage("50")}},
		}},
	}
	store := NewStore(path)
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}
	engine, err := New(mgr, bus, store, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("new engine after capability change: %v", err)
	}
	got := engine.Snapshot().State
	if got.Items[0].Enabled {
		t.Fatal("rule with obsolete capability stayed enabled")
	}
	if got.Revision != 1 {
		t.Fatalf("reconciled revision = %d, want 1", got.Revision)
	}
	persisted, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Items[0].Enabled {
		t.Fatal("disabled rule was not persisted")
	}
}

func TestStoreRejectsOversizedState(t *testing.T) {
	path := filepath.Join(t.TempDir(), stateFileName)
	if err := os.WriteFile(path, make([]byte, MaxStateBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewStore(path).Load(); err == nil {
		t.Fatal("oversized automation state was accepted")
	}
}

func TestStoreRefusesToWriteOversizedState(t *testing.T) {
	path := filepath.Join(t.TempDir(), stateFileName)
	state := State{Version: FormatVersion, Items: []Rule{{Name: string(bytes.Repeat([]byte("x"), MaxStateBytes))}}}
	if err := NewStore(path).Save(state); err == nil {
		t.Fatal("oversized automation state was written")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("state file exists after rejected write: %v", err)
	}
}

func TestStoredStateContainsNoPlaintextWebhookToken(t *testing.T) {
	target := &testSwitch{id: "target"}
	bus := events.NewBus()
	target.bus = bus
	mgr := manager.New(bus, []device.Device{target})
	defer mgr.Close()
	path := filepath.Join(t.TempDir(), stateFileName)
	engine, err := New(mgr, bus, NewStore(path), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	update := replaceRules(t, engine, webhookRule("hook", "target"))
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(data) {
		t.Fatal("stored state is not valid JSON")
	}
	if token := update.GeneratedTokens["hook"]; token == "" || bytes.Contains(data, []byte(token)) {
		t.Fatal("plaintext webhook token was written to disk")
	}
}
