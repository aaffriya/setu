package automation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	"setu/internal/store"
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
func (*testSwitch) Driver() string         { return "switch" }
func (*testSwitch) Model() string          { return "" }
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

type failingSwitch struct{ testSwitch }

func (*failingSwitch) On() error { return errors.New("transport failed") }

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
	engine, err := New(mgr, bus, NewStore(store.New(filepath.Join(t.TempDir(), "setu.json"))), nil, log)
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

func TestTimedScheduleRunsOnlyTheDueOffsetAndIgnoresCooldown(t *testing.T) {
	target := &testSwitch{id: "target"}
	engine := newTestEngine(t, target)
	rule := Rule{
		ID:      "evening",
		Name:    "Evening",
		Enabled: true,
		Trigger: Trigger{Type: TriggerSchedule, Schedule: &Schedule{
			Time: "21:00", Weekdays: []int{1}, UTCOffsetMinutes: 0,
		}},
		Actions: []Action{
			{DeviceID: target.id, Action: "on"},
			{DeviceID: target.id, Action: "on", OffsetMinutes: 15},
			{DeviceID: target.id, Action: "on", OffsetMinutes: 30},
			{DeviceID: target.id, Action: "on", OffsetMinutes: 45},
		},
		CooldownSeconds: 3600,
	}
	replaceRules(t, engine, rule)
	start := time.Date(2026, time.July, 20, 21, 0, 10, 0, time.UTC) // Monday.
	for index, offset := range []int{0, 15, 30, 45} {
		engine.evaluateSchedules(start.Add(time.Duration(offset) * time.Minute))
		want := index + 1
		waitFor(t, func() bool { return target.onCount() == want })
		runs := engine.Snapshot().Runs
		if len(runs) == 0 || runs[0].OffsetMinutes != offset {
			t.Fatalf("offset %d run = %+v", offset, runs)
		}
	}

	engine.evaluateSchedules(start.Add(45*time.Minute + 20*time.Second))
	time.Sleep(30 * time.Millisecond)
	if target.onCount() != 4 {
		t.Fatalf("timed schedule ran %d times, want one run per offset", target.onCount())
	}
}

func TestTimedScheduleAnchorsWeekdayBeforeMidnightCrossingAndDoesNotCatchUp(t *testing.T) {
	target := &testSwitch{id: "target"}
	engine := newTestEngine(t, target)
	rule := Rule{
		ID:      "late",
		Name:    "Late",
		Enabled: true,
		Trigger: Trigger{Type: TriggerSchedule, Schedule: &Schedule{
			Time: "23:50", Weekdays: []int{1}, UTCOffsetMinutes: 0,
		}},
		Actions: []Action{
			{DeviceID: target.id, Action: "off"},
			{DeviceID: target.id, Action: "on", OffsetMinutes: 30},
		},
	}
	replaceRules(t, engine, rule)
	due := time.Date(2026, time.July, 21, 0, 20, 5, 0, time.UTC) // Tuesday, from Monday 23:50.
	engine.evaluateSchedules(due)
	waitFor(t, func() bool { return target.onCount() == 1 })

	engine.evaluateSchedules(due.Add(time.Minute))
	time.Sleep(30 * time.Millisecond)
	if target.onCount() != 1 {
		t.Fatalf("missed timed step was replayed: %d runs", target.onCount())
	}
}

func TestRunNowTimedScheduleRunsOnlyOffsetZero(t *testing.T) {
	target := &testSwitch{id: "target"}
	engine := newTestEngine(t, target)
	rule := Rule{
		ID:      "manual_timeline",
		Name:    "Manual timeline",
		Enabled: true,
		Trigger: Trigger{Type: TriggerSchedule, Schedule: &Schedule{
			Time: "21:00", Weekdays: []int{1}, UTCOffsetMinutes: 0,
		}},
		Actions: []Action{
			{DeviceID: target.id, Action: "on"},
			{DeviceID: target.id, Action: "on", OffsetMinutes: 15},
		},
	}
	replaceRules(t, engine, rule)
	if result, err := engine.RunNow(rule.ID); err != nil || result.Status != "queued" {
		t.Fatalf("run timed schedule now = %+v, %v", result, err)
	}
	waitFor(t, func() bool { return len(engine.Snapshot().Runs) == 1 })
	run := engine.Snapshot().Runs[0]
	if target.onCount() != 1 || len(run.Results) != 1 || run.OffsetMinutes != 0 {
		t.Fatalf("manual timed run = %+v, target runs = %d", run, target.onCount())
	}
}

func TestTimedScheduleChecksTopLevelConditionsForEveryStep(t *testing.T) {
	condition := &testSwitch{id: "condition"}
	target := &testSwitch{id: "target"}
	engine := newTestEngine(t, condition, target)
	condition.set(true)
	rule := Rule{
		ID:      "guarded",
		Name:    "Guarded",
		Enabled: true,
		Trigger: Trigger{Type: TriggerSchedule, Schedule: &Schedule{
			Time: "18:00", Weekdays: []int{1}, UTCOffsetMinutes: 0,
		}},
		Conditions: []Condition{{DeviceID: condition.id, On: true}},
		Actions: []Action{
			{DeviceID: target.id, Action: "on"},
			{DeviceID: target.id, Action: "on", OffsetMinutes: 15},
		},
	}
	replaceRules(t, engine, rule)
	start := time.Date(2026, time.July, 20, 18, 0, 0, 0, time.UTC)
	engine.evaluateSchedules(start)
	waitFor(t, func() bool { return target.onCount() == 1 })
	condition.set(false)
	engine.evaluateSchedules(start.Add(15 * time.Minute))
	time.Sleep(30 * time.Millisecond)
	if target.onCount() != 1 {
		t.Fatal("later timed step ignored its current top-level condition")
	}
}

func TestTimedSchedulePreservesDeclaredOrderWithinOneOffset(t *testing.T) {
	condition := &testSwitch{id: "condition"}
	target := &testSwitch{id: "target"}
	engine := newTestEngine(t, condition, target)
	rule := Rule{
		ID:      "ordered_timeline",
		Name:    "Ordered timeline",
		Enabled: true,
		Trigger: Trigger{Type: TriggerSchedule, Schedule: &Schedule{
			Time: "18:00", Weekdays: []int{1}, UTCOffsetMinutes: 0,
		}},
		Actions: []Action{
			{DeviceID: target.id, Action: "off"},
			{DeviceID: condition.id, Action: "on", OffsetMinutes: 15},
			{DeviceID: target.id, Action: "on", OffsetMinutes: 15, When: []Condition{{DeviceID: condition.id, On: true}}},
		},
	}
	replaceRules(t, engine, rule)
	due := time.Date(2026, time.July, 20, 18, 15, 0, 0, time.UTC)
	engine.evaluateSchedules(due)
	waitFor(t, func() bool { return len(engine.Snapshot().Runs) == 1 })
	run := engine.Snapshot().Runs[0]
	if !run.OK || run.OffsetMinutes != 15 || target.onCount() != 1 || len(run.Results) != 2 {
		t.Fatalf("ordered timed step = %+v, target runs = %d", run, target.onCount())
	}
}

func TestTimedScheduleDoesNotBacklogAnOverlappingStep(t *testing.T) {
	target := &testSwitch{id: "target"}
	engine := newTestEngine(t, target)
	rule := Rule{
		ID:      "overlap",
		Name:    "Overlap",
		Enabled: true,
		Trigger: Trigger{Type: TriggerSchedule, Schedule: &Schedule{
			Time: "18:00", Weekdays: []int{1}, UTCOffsetMinutes: 0,
		}},
		Actions: []Action{
			{DeviceID: target.id, Action: "on"},
			{DeviceID: target.id, Action: "on", OffsetMinutes: 15},
		},
	}
	replaceRules(t, engine, rule)
	engine.mu.Lock()
	engine.running[rule.ID] = true
	engine.mu.Unlock()
	due := time.Date(2026, time.July, 20, 18, 15, 0, 0, time.UTC)
	engine.evaluateSchedules(due)
	engine.mu.Lock()
	delete(engine.running, rule.ID)
	engine.mu.Unlock()
	engine.evaluateSchedules(due.Add(20 * time.Second))
	time.Sleep(30 * time.Millisecond)
	if target.onCount() != 0 || len(engine.Snapshot().Runs) != 0 {
		t.Fatal("overlapping timed step was queued or replayed")
	}
}

func TestFullQueueDoesNotConsumeCooldown(t *testing.T) {
	bus := events.NewBus()
	target := &testSwitch{id: "target", bus: bus}
	mgr := manager.New(bus, []device.Device{target})
	defer mgr.Close()
	engine, err := New(
		mgr,
		bus,
		NewStore(store.New(filepath.Join(t.TempDir(), "setu.json"))),
		nil,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	rule := webhookRule("cooldown", target.id)
	rule.CooldownSeconds = 60
	replaceRules(t, engine, rule)

	for range cap(engine.queue) {
		engine.queue <- runRequest{}
	}
	if _, err := engine.RunNow(rule.ID); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("full queue error = %v, want ErrQueueFull", err)
	}

	<-engine.queue
	result, err := engine.RunNow(rule.ID)
	if err != nil || result.Status != "queued" {
		t.Fatalf("retry after queue space = %+v, %v; cooldown was consumed", result, err)
	}
}

func TestActionConditionSeesStateFromPreviousAction(t *testing.T) {
	condition := &testSwitch{id: "condition"}
	target := &testSwitch{id: "target"}
	engine := newTestEngine(t, condition, target)
	rule := webhookRule("guarded", target.id)
	rule.Actions = []Action{
		{DeviceID: condition.id, Action: "on"},
		{DeviceID: target.id, Action: "on", When: []Condition{{DeviceID: condition.id, On: true}}},
	}
	replaceRules(t, engine, rule)
	if result, err := engine.RunNow(rule.ID); err != nil || result.Status != "queued" {
		t.Fatalf("run guarded actions = %+v, %v", result, err)
	}
	waitFor(t, func() bool { return len(engine.Snapshot().Runs) == 1 })
	run := engine.Snapshot().Runs[0]
	if !run.OK || target.onCount() != 1 || len(run.Results) != 2 || !run.Results[1].OK {
		t.Fatalf("guarded run = %+v, target runs = %d", run, target.onCount())
	}
}

func TestUnmetActionConditionIsSuccessfulSkip(t *testing.T) {
	condition := &testSwitch{id: "condition"}
	target := &testSwitch{id: "target"}
	engine := newTestEngine(t, condition, target)
	rule := webhookRule("guarded", target.id)
	rule.Actions[0].When = []Condition{{DeviceID: condition.id, On: true}}
	replaceRules(t, engine, rule)
	if result, err := engine.RunNow(rule.ID); err != nil || result.Status != "queued" {
		t.Fatalf("run guarded action = %+v, %v", result, err)
	}
	waitFor(t, func() bool { return len(engine.Snapshot().Runs) == 1 })
	run := engine.Snapshot().Runs[0]
	if !run.OK || target.onCount() != 0 || len(run.Results) != 1 || !run.Results[0].Skipped || run.Results[0].OK {
		t.Fatalf("skipped run = %+v, target runs = %d", run, target.onCount())
	}
}

func TestCommandFailureStillFailsRun(t *testing.T) {
	target := &failingSwitch{testSwitch: testSwitch{id: "target"}}
	engine := newTestEngine(t, target)
	rule := webhookRule("failing", target.id)
	replaceRules(t, engine, rule)
	if result, err := engine.RunNow(rule.ID); err != nil || result.Status != "queued" {
		t.Fatalf("run failing action = %+v, %v", result, err)
	}
	waitFor(t, func() bool { return len(engine.Snapshot().Runs) == 1 })
	run := engine.Snapshot().Runs[0]
	if run.OK || len(run.Results) != 1 || run.Results[0].Skipped || run.Results[0].Error == "" {
		t.Fatalf("failed run = %+v", run)
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

func TestNestedAutomationRunsInlineInActionOrder(t *testing.T) {
	target := &testSwitch{id: "target"}
	engine := newTestEngine(t, target)
	child := webhookRule("child", target.id)
	child.Name = "Turn target on"
	parent := Rule{
		ID:      "parent",
		Name:    "Run child then turn off",
		Enabled: true,
		Trigger: Trigger{Type: TriggerWebhook, Webhook: &Webhook{}},
		Actions: []Action{
			{Action: ActionAutomation, AutomationID: child.ID},
			{DeviceID: target.id, Action: "off"},
		},
	}
	replaceRules(t, engine, child, parent)
	result, err := engine.RunNow(parent.ID)
	if err != nil || result.Status != "queued" {
		t.Fatalf("run parent = %+v, %v", result, err)
	}
	waitFor(t, func() bool { return len(engine.Snapshot().Runs) >= 2 })
	if target.State().On {
		t.Fatal("parent continued before nested automation completed")
	}
	runs := engine.Snapshot().Runs
	if !runs[0].OK || runs[0].RuleID != parent.ID {
		t.Fatalf("parent run = %+v", runs[0])
	}
	if !runs[1].OK || runs[1].RuleID != child.ID || runs[1].Source != "automation:"+parent.ID {
		t.Fatalf("nested run = %+v", runs[1])
	}
}

func TestNestedAutomationWithUnmetConditionsIsSkipped(t *testing.T) {
	condition := &testSwitch{id: "condition"}
	target := &testSwitch{id: "target"}
	engine := newTestEngine(t, condition, target)
	child := webhookRule("child", target.id)
	child.Conditions = []Condition{{DeviceID: condition.id, On: true}}
	parent := Rule{
		ID:      "parent",
		Name:    "Conditional child",
		Enabled: true,
		Trigger: Trigger{Type: TriggerWebhook, Webhook: &Webhook{}},
		Actions: []Action{{Action: ActionAutomation, AutomationID: child.ID}},
	}
	replaceRules(t, engine, child, parent)
	if result, err := engine.RunNow(parent.ID); err != nil || result.Status != "queued" {
		t.Fatalf("run parent = %+v, %v", result, err)
	}
	waitFor(t, func() bool { return len(engine.Snapshot().Runs) == 1 })
	run := engine.Snapshot().Runs[0]
	if !run.OK || run.RuleID != parent.ID || len(run.Results) != 1 || !run.Results[0].Skipped || target.onCount() != 0 {
		t.Fatalf("conditional child run = %+v, target runs = %d", run, target.onCount())
	}
}

func TestActionConditionAndOffsetValidationIsBounded(t *testing.T) {
	target := &testSwitch{id: "target"}
	engine := newTestEngine(t, target)
	rule := webhookRule("invalid", target.id)
	rule.Actions[0].When = make([]Condition, MaxConditions+1)
	_, err := engine.Replace(State{Version: FormatVersion, Revision: 0, Items: []Rule{rule}})
	var invalid ValidationError
	if !errors.As(err, &invalid) {
		t.Fatalf("oversized action condition error = %v, want ValidationError", err)
	}

	rule = webhookRule("invalid", target.id)
	rule.Actions[0].OffsetMinutes = 15
	_, err = engine.Replace(State{Version: FormatVersion, Revision: 0, Items: []Rule{rule}})
	if !errors.As(err, &invalid) {
		t.Fatalf("non-schedule offset error = %v, want ValidationError", err)
	}

	rule.Trigger = Trigger{Type: TriggerSchedule, Schedule: &Schedule{
		Time: "18:00", Weekdays: []int{1}, UTCOffsetMinutes: 0,
	}}
	_, err = engine.Replace(State{Version: FormatVersion, Revision: 0, Items: []Rule{rule}})
	if !errors.As(err, &invalid) {
		t.Fatalf("timed schedule without start error = %v, want ValidationError", err)
	}
}

func TestNestedAutomationCycleIsRejected(t *testing.T) {
	target := &testSwitch{id: "target"}
	engine := newTestEngine(t, target)
	rules := []Rule{
		{ID: "first", Name: "First", Enabled: true, Trigger: Trigger{Type: TriggerWebhook, Webhook: &Webhook{}}, Actions: []Action{{Action: ActionAutomation, AutomationID: "second"}}},
		{ID: "second", Name: "Second", Enabled: true, Trigger: Trigger{Type: TriggerWebhook, Webhook: &Webhook{}}, Actions: []Action{{Action: ActionAutomation, AutomationID: "first"}}},
	}
	_, err := engine.Replace(State{Version: FormatVersion, Revision: 0, Items: rules})
	var invalid ValidationError
	if !errors.As(err, &invalid) {
		t.Fatalf("nested cycle error = %v, want ValidationError", err)
	}
}

func TestEnabledAutomationCannotCallDisabledTarget(t *testing.T) {
	target := &testSwitch{id: "target"}
	engine := newTestEngine(t, target)
	child := webhookRule("child", target.id)
	child.Enabled = false
	parent := Rule{
		ID:      "parent",
		Name:    "Parent",
		Enabled: true,
		Trigger: Trigger{Type: TriggerWebhook, Webhook: &Webhook{}},
		Actions: []Action{{Action: ActionAutomation, AutomationID: child.ID}},
	}

	_, err := engine.Replace(State{Version: FormatVersion, Revision: 0, Items: []Rule{child, parent}})
	var invalid ValidationError
	if !errors.As(err, &invalid) {
		t.Fatalf("disabled nested target error = %v, want ValidationError", err)
	}
}

func TestNestedPowerRelationCycleIsRejected(t *testing.T) {
	a := &testSwitch{id: "a"}
	b := &testSwitch{id: "b"}
	engine := newTestEngine(t, a, b)
	rules := []Rule{
		{ID: "turn_b_on", Name: "Turn B on", Enabled: true, Trigger: Trigger{Type: TriggerWebhook, Webhook: &Webhook{}}, Actions: []Action{{DeviceID: "b", Action: "on"}}},
		{ID: "a_to_b", Name: "A to B", Enabled: true, Trigger: Trigger{Type: TriggerDeviceState, Device: &DeviceTrigger{DeviceID: "a", On: true}}, Actions: []Action{{Action: ActionAutomation, AutomationID: "turn_b_on"}}},
		{ID: "b_to_a", Name: "B to A", Enabled: true, Trigger: Trigger{Type: TriggerDeviceState, Device: &DeviceTrigger{DeviceID: "b", On: true}}, Actions: []Action{{DeviceID: "a", Action: "on"}}},
	}
	_, err := engine.Replace(State{Version: FormatVersion, Revision: 0, Items: rules})
	var invalid ValidationError
	if !errors.As(err, &invalid) {
		t.Fatalf("nested power cycle error = %v, want ValidationError", err)
	}
}

func TestNestedTimedScheduleOnlyContributesImmediateEffects(t *testing.T) {
	source := &testSwitch{id: "source"}
	target := &testSwitch{id: "target"}
	engine := newTestEngine(t, source, target)
	timeline := Rule{
		ID:      "timeline",
		Name:    "Timeline",
		Enabled: true,
		Trigger: Trigger{Type: TriggerSchedule, Schedule: &Schedule{
			Time: "18:00", Weekdays: []int{1}, UTCOffsetMinutes: 0,
		}},
		Actions: []Action{
			{DeviceID: target.id, Action: "on"},
			{DeviceID: source.id, Action: "on", OffsetMinutes: 15},
		},
	}
	relation := Rule{
		ID:      "relation",
		Name:    "Relation",
		Enabled: true,
		Trigger: Trigger{Type: TriggerDeviceState, Device: &DeviceTrigger{DeviceID: source.id, On: true}},
		Actions: []Action{{Action: ActionAutomation, AutomationID: timeline.ID}},
	}
	if _, err := engine.Replace(State{Version: FormatVersion, Revision: 0, Items: []Rule{timeline, relation}}); err != nil {
		t.Fatalf("non-immediate timed effect was treated as a nested feedback loop: %v", err)
	}
}

func TestNestedAutomationExecutionHasBoundedActionBudget(t *testing.T) {
	target := &testSwitch{id: "target"}
	engine := newTestEngine(t, target)
	leaf := Rule{ID: "leaf", Name: "Leaf", Enabled: true, Trigger: Trigger{Type: TriggerWebhook, Webhook: &Webhook{}}, Actions: []Action{{DeviceID: target.id, Action: "on"}}}
	middleActions := make([]Action, MaxActions)
	rootActions := make([]Action, MaxActions)
	for i := 0; i < MaxActions; i++ {
		middleActions[i] = Action{Action: ActionAutomation, AutomationID: leaf.ID}
		rootActions[i] = Action{Action: ActionAutomation, AutomationID: "middle"}
	}
	middle := Rule{ID: "middle", Name: "Middle", Enabled: true, Trigger: Trigger{Type: TriggerWebhook, Webhook: &Webhook{}}, Actions: middleActions}
	root := Rule{ID: "root", Name: "Root", Enabled: true, Trigger: Trigger{Type: TriggerWebhook, Webhook: &Webhook{}}, Actions: rootActions}
	replaceRules(t, engine, leaf, middle, root)
	if result, err := engine.RunNow(root.ID); err != nil || result.Status != "queued" {
		t.Fatalf("run root = %+v, %v", result, err)
	}
	waitFor(t, func() bool {
		runs := engine.Snapshot().Runs
		return len(runs) > 0 && runs[0].RuleID == root.ID
	})
	if engine.Snapshot().Runs[0].OK {
		t.Fatal("expanding nested run exceeded its action budget without failing")
	}
	if target.onCount() >= MaxRunActions {
		t.Fatalf("executed %d device actions with a total budget of %d", target.onCount(), MaxRunActions)
	}
}

func TestNestedAutomationExecutionHasBoundedDelayBudget(t *testing.T) {
	target := &testSwitch{id: "target"}
	engine := newTestEngine(t, target)
	rule := webhookRule("delayed", target.id)
	rule.Actions[0].DelaySeconds = 1
	remaining := MaxRunActions
	remainingDelay := 0
	run := engine.executeRequest(context.Background(), runRequest{id: "run", rule: rule, source: "test"}, 1, &remaining, &remainingDelay)
	if run.OK || target.onCount() != 0 {
		t.Fatalf("delay-budget run = %+v, on count = %d", run, target.onCount())
	}
	if len(run.Results) != 1 || run.Results[0].Error != fmt.Sprintf("nested delay limit exceeds %d seconds", MaxRunDelay) {
		t.Fatalf("delay-budget result = %+v", run.Results)
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
	path := filepath.Join(t.TempDir(), "setu.json")
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
	rules := NewStore(store.New(path))
	if err := rules.Save(state); err != nil {
		t.Fatal(err)
	}
	engine, err := New(mgr, bus, rules, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
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
	persisted, err := rules.Load()
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Items[0].Enabled {
		t.Fatal("disabled rule was not persisted")
	}
}

func TestNewCascadeDisablesCallerOfInvalidTarget(t *testing.T) {
	bus := events.NewBus()
	target := &testSwitch{id: "target", bus: bus}
	mgr := manager.New(bus, []device.Device{target})
	defer mgr.Close()
	path := filepath.Join(t.TempDir(), "setu.json")
	child := webhookRule("missing_child", "missing")
	parent := Rule{
		ID:      "parent",
		Name:    "Parent",
		Enabled: true,
		Trigger: Trigger{Type: TriggerWebhook, Webhook: &Webhook{}},
		Actions: []Action{{Action: ActionAutomation, AutomationID: child.ID}},
	}
	rules := NewStore(store.New(path))
	if err := rules.Save(State{Version: FormatVersion, Items: []Rule{child, parent}}); err != nil {
		t.Fatal(err)
	}

	engine, err := New(mgr, bus, rules, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	for _, rule := range engine.Snapshot().Items {
		if rule.Enabled {
			t.Fatalf("dependent automation %q remained enabled", rule.ID)
		}
	}
}

// The rule set shares a file with the device list now, so its own cap has to be
// enforced on the section — not on the file, which is allowed to be bigger.
func TestStoreRejectsOversizedState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "setu.json")
	oversized, err := json.Marshal(State{
		Version: FormatVersion,
		Items:   []Rule{{Name: string(bytes.Repeat([]byte("x"), MaxStateBytes))}},
	})
	if err != nil {
		t.Fatal(err)
	}
	file := store.New(path)
	if err := file.Update(func(state *store.State) error {
		state.Automations = oversized
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := NewStore(file).Load(); err == nil {
		t.Fatal("oversized automation state was accepted")
	}
}

func TestStoreRefusesToWriteOversizedState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "setu.json")
	state := State{Version: FormatVersion, Items: []Rule{{Name: string(bytes.Repeat([]byte("x"), MaxStateBytes))}}}
	if err := NewStore(store.New(path)).Save(state); err == nil {
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
	path := filepath.Join(t.TempDir(), "setu.json")
	engine, err := New(mgr, bus, NewStore(store.New(path)), nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
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
