package automation

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"setu/internal/device"
	"setu/internal/events"
	"setu/internal/manager"
	"setu/internal/store"
)

// testLamp reports power and brightness and can be polled, so it can stand in
// for every kind of device trigger: a power edge, a numeric metric, and
// reachability.
type testLamp struct {
	id  string
	bus *events.Bus

	mu    sync.Mutex
	state device.State
}

func (d *testLamp) ID() string   { return d.id }
func (d *testLamp) Name() string { return d.id }
func (*testLamp) Brand() string  { return "test" }
func (*testLamp) Driver() string { return "lamp" }
func (*testLamp) Model() string  { return "" }
func (*testLamp) MAC() string    { return "02:00:00:00:00:07" }
func (*testLamp) Capabilities() []string {
	return []string{device.CapSwitch, device.CapBrightness}
}
func (d *testLamp) State() device.State {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.state
}
func (d *testLamp) Poll() (device.State, error) { return d.State(), nil }
func (d *testLamp) On() error                   { d.publish(func(s *device.State) { s.On = true }); return nil }
func (d *testLamp) Off() error                  { d.publish(func(s *device.State) { s.On = false }); return nil }
func (d *testLamp) SetBrightness(pct int) error {
	d.publish(func(s *device.State) { s.On = true; s.Brightness = pct })
	return nil
}

func (d *testLamp) publish(mutate func(*device.State)) {
	d.mu.Lock()
	// Answering at all means reachable, which is what a real driver reports too.
	d.state.Online = true
	mutate(&d.state)
	state := d.state
	d.mu.Unlock()
	if d.bus != nil {
		d.bus.Publish(events.Event{Type: events.StateChanged, DeviceID: d.id, State: state})
	}
}

// lampEngine builds an engine over one lamp and one plain switch, with a
// presence source the test controls.
func lampEngine(t *testing.T, presence PresenceFunc, devices ...device.Device) (*Engine, *events.Bus) {
	t.Helper()
	bus := events.NewBus()
	for _, raw := range devices {
		switch dev := raw.(type) {
		case *testLamp:
			dev.bus = bus
		case *testSwitch:
			dev.bus = bus
		}
	}
	mgr := manager.New(bus, devices)
	t.Cleanup(mgr.Close)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	engine, err := New(mgr, bus, NewStore(store.New(filepath.Join(t.TempDir(), "setu.json"))), presence, log)
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
	return engine, bus
}

func metricRule(id, watched, target string, operator string, value int) Rule {
	return Rule{
		ID:      id,
		Name:    "Metric rule",
		Enabled: true,
		Trigger: Trigger{Type: TriggerDeviceState, Device: &DeviceTrigger{
			DeviceID: watched, Metric: MetricBrightness, Operator: operator, Value: value,
		}},
		Actions: []Action{{DeviceID: target, Action: "on"}},
	}
}

// Crossing a threshold is the event; sitting past it is not. A rule that fired
// once must stay quiet while the value keeps satisfying it.
func TestBrightnessTriggerFiresOnTheCrossingOnly(t *testing.T) {
	lamp := &testLamp{id: "lamp"}
	target := &testSwitch{id: "target"}
	engine, _ := lampEngine(t, nil, lamp, target)
	replaceRules(t, engine, metricRule("bright", lamp.id, target.id, OpAbove, 50))

	if err := lamp.SetBrightness(20); err != nil {
		t.Fatal(err)
	}
	time.Sleep(60 * time.Millisecond)
	if target.onCount() != 0 {
		t.Fatalf("rule fired below the threshold (%d runs)", target.onCount())
	}

	if err := lamp.SetBrightness(80); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return target.onCount() == 1 })

	// Still above: no new edge, so no new run.
	if err := lamp.SetBrightness(90); err != nil {
		t.Fatal(err)
	}
	time.Sleep(60 * time.Millisecond)
	if target.onCount() != 1 {
		t.Fatalf("staying above the threshold fired again (%d runs)", target.onCount())
	}

	// Down and back up is a new crossing.
	if err := lamp.SetBrightness(10); err != nil {
		t.Fatal(err)
	}
	if err := lamp.SetBrightness(70); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return target.onCount() == 2 })
}

// An unreachable device reports zeros. A bulb that merely lost Wi-Fi must not
// read as "brightness fell below 20".
func TestNumericTriggersIgnoreAnUnreachableDevice(t *testing.T) {
	lamp := &testLamp{id: "lamp"}
	target := &testSwitch{id: "target"}
	engine, bus := lampEngine(t, nil, lamp, target)
	replaceRules(t, engine, metricRule("dim", lamp.id, target.id, OpBelow, 20))

	if err := lamp.SetBrightness(80); err != nil {
		t.Fatal(err)
	}
	time.Sleep(40 * time.Millisecond)
	bus.Publish(events.Event{Type: events.StateChanged, DeviceID: lamp.id,
		State: device.State{Online: false, Brightness: 0}})
	time.Sleep(80 * time.Millisecond)
	if target.onCount() != 0 {
		t.Fatalf("losing contact fired a brightness rule (%d runs)", target.onCount())
	}
}

func TestMetricTriggerNeedsTheCapability(t *testing.T) {
	plain := &testSwitch{id: "plain"}
	target := &testSwitch{id: "target"}
	engine, _ := lampEngine(t, nil, plain, target)

	rule := metricRule("bright", plain.id, target.id, OpAbove, 50)
	_, err := engine.Replace(State{Version: FormatVersion, Revision: engine.Snapshot().Revision, Items: []Rule{rule}})
	var invalid ValidationError
	if err == nil || !asValidation(err, &invalid) {
		t.Fatalf("watching brightness on a plain switch = %v, want ValidationError", err)
	}
}

// An offline rule is a one-shot per episode: a device that flaps must not queue
// a run every minute it stays away.
func TestOfflineTriggerFiresOncePerEpisode(t *testing.T) {
	lamp := &testLamp{id: "lamp"}
	target := &testSwitch{id: "target"}
	engine, bus := lampEngine(t, nil, lamp, target)
	replaceRules(t, engine, Rule{
		ID: "gone", Name: "Lamp is gone", Enabled: true,
		Trigger: Trigger{Type: TriggerDeviceOffline, Offline: &OfflineTrigger{DeviceID: lamp.id, Minutes: 5}},
		Actions: []Action{{DeviceID: target.id, Action: "on"}},
	})

	bus.Publish(events.Event{Type: events.StateChanged, DeviceID: lamp.id, State: device.State{Online: true}})
	time.Sleep(40 * time.Millisecond)
	bus.Publish(events.Event{Type: events.StateChanged, DeviceID: lamp.id, State: device.State{Online: false}})
	time.Sleep(40 * time.Millisecond)

	// Not yet: four minutes is less than the five the rule asks for.
	engine.evaluateOffline(time.Now().Add(4 * time.Minute))
	time.Sleep(40 * time.Millisecond)
	if target.onCount() != 0 {
		t.Fatalf("fired before the configured time (%d runs)", target.onCount())
	}

	engine.evaluateOffline(time.Now().Add(6 * time.Minute))
	waitFor(t, func() bool { return target.onCount() == 1 })

	// Still away: the rule has already spoken for this episode.
	engine.evaluateOffline(time.Now().Add(20 * time.Minute))
	time.Sleep(60 * time.Millisecond)
	if target.onCount() != 1 {
		t.Fatalf("fired again during the same episode (%d runs)", target.onCount())
	}

	// Back, then away again: a new episode, so it arms again.
	bus.Publish(events.Event{Type: events.StateChanged, DeviceID: lamp.id, State: device.State{Online: true}})
	time.Sleep(40 * time.Millisecond)
	bus.Publish(events.Event{Type: events.StateChanged, DeviceID: lamp.id, State: device.State{Online: false}})
	time.Sleep(40 * time.Millisecond)
	engine.evaluateOffline(time.Now().Add(10 * time.Minute))
	waitFor(t, func() bool { return target.onCount() == 2 })
}

// Editing an unrelated rule during an outage must not announce that outage
// again. A rule rearms when its device is seen back, and nowhere else.
func TestEditingRulesDoesNotRearmAnOngoingOutage(t *testing.T) {
	lamp := &testLamp{id: "lamp"}
	target := &testSwitch{id: "target"}
	engine, bus := lampEngine(t, nil, lamp, target)
	gone := Rule{
		ID: "gone", Name: "Lamp is gone", Enabled: true,
		Trigger: Trigger{Type: TriggerDeviceOffline, Offline: &OfflineTrigger{DeviceID: lamp.id, Minutes: 5}},
		Actions: []Action{{DeviceID: target.id, Action: "on"}},
	}
	replaceRules(t, engine, gone)

	bus.Publish(events.Event{Type: events.StateChanged, DeviceID: lamp.id, State: device.State{Online: true}})
	time.Sleep(40 * time.Millisecond)
	bus.Publish(events.Event{Type: events.StateChanged, DeviceID: lamp.id, State: device.State{Online: false}})
	time.Sleep(40 * time.Millisecond)
	engine.evaluateOffline(time.Now().Add(6 * time.Minute))
	waitFor(t, func() bool { return target.onCount() == 1 })

	// An edit that has nothing to do with the outage.
	other := webhookRule("other", target.id)
	replaceRules(t, engine, gone, other)

	engine.evaluateOffline(time.Now().Add(10 * time.Minute))
	time.Sleep(80 * time.Millisecond)
	if target.onCount() != 1 {
		t.Fatalf("an unrelated edit re-announced the outage (%d runs)", target.onCount())
	}
}

// Presence watches a MAC no device has to be added for. The first read is a
// baseline: arriving means the answer changed, not that it was true once.
func TestPresenceTriggerFiresOnArrival(t *testing.T) {
	target := &testSwitch{id: "target"}
	var mu sync.Mutex
	home := false
	presence := func() (map[string]bool, error) {
		mu.Lock()
		defer mu.Unlock()
		return map[string]bool{"aabbccddeeff": home}, nil
	}
	engine, _ := lampEngine(t, presence, target)
	replaceRules(t, engine, Rule{
		ID: "home", Name: "Phone is home", Enabled: true,
		Trigger: Trigger{Type: TriggerPresence, Presence: &Presence{MAC: "aa:bb:cc:dd:ee:ff", Present: true}},
		Actions: []Action{{DeviceID: target.id, Action: "on"}},
	})

	engine.syncPresence() // baseline: away
	time.Sleep(40 * time.Millisecond)
	if target.onCount() != 0 {
		t.Fatalf("the baseline read fired the rule (%d runs)", target.onCount())
	}

	mu.Lock()
	home = true
	mu.Unlock()
	engine.syncPresence()
	waitFor(t, func() bool { return target.onCount() == 1 })

	// Still home is not an arrival.
	engine.syncPresence()
	time.Sleep(60 * time.Millisecond)
	if target.onCount() != 1 {
		t.Fatalf("staying home fired again (%d runs)", target.onCount())
	}
}

// A presence rule that goes away and comes back has to start from a fresh
// baseline. Remembering what was true when it last existed would fire it the
// moment it returned.
func TestReaddedPresenceRuleStartsFromABaseline(t *testing.T) {
	target := &testSwitch{id: "target"}
	var mu sync.Mutex
	home := true
	presence := func() (map[string]bool, error) {
		mu.Lock()
		defer mu.Unlock()
		return map[string]bool{"aabbccddeeff": home}, nil
	}
	engine, _ := lampEngine(t, presence, target)
	watch := Rule{
		ID: "home", Name: "Phone is home", Enabled: true,
		Trigger: Trigger{Type: TriggerPresence, Presence: &Presence{MAC: "aa:bb:cc:dd:ee:ff", Present: true}},
		Actions: []Action{{DeviceID: target.id, Action: "on"}},
	}
	setHome := func(value bool) {
		mu.Lock()
		home = value
		mu.Unlock()
	}

	setHome(false)
	replaceRules(t, engine, watch)
	engine.syncPresence() // baseline: away

	// The rule is removed, the phone comes home while nothing is watching, and
	// the rule is added back.
	replaceRules(t, engine)
	setHome(true)
	replaceRules(t, engine, watch)

	engine.syncPresence()
	time.Sleep(80 * time.Millisecond)
	if target.onCount() != 0 {
		t.Fatalf("re-adding the rule fired it against an answer from before it existed (%d runs)", target.onCount())
	}

	// A real arrival still works afterwards.
	setHome(false)
	engine.syncPresence()
	setHome(true)
	engine.syncPresence()
	waitFor(t, func() bool { return target.onCount() == 1 })
}

// A host with no neighbour table cannot answer a presence rule. Saying so is
// better than storing a rule that will never fire.
func TestPresenceRuleIsRefusedWithoutASource(t *testing.T) {
	target := &testSwitch{id: "target"}
	engine, _ := lampEngine(t, nil, target)

	_, err := engine.Replace(State{Version: FormatVersion, Revision: engine.Snapshot().Revision, Items: []Rule{{
		ID: "home", Name: "Phone is home", Enabled: true,
		Trigger: Trigger{Type: TriggerPresence, Presence: &Presence{MAC: "aa:bb:cc:dd:ee:ff", Present: true}},
		Actions: []Action{{DeviceID: target.id, Action: "on"}},
	}}})
	var invalid ValidationError
	if err == nil || !asValidation(err, &invalid) {
		t.Fatalf("presence rule without a source = %v, want ValidationError", err)
	}
}

// Two rules that each undo the other's trigger would oscillate forever. Setting
// a brightness also powers most hardware on, which is why that pairing counts.
func TestMetricFeedbackLoopIsRejected(t *testing.T) {
	lamp := &testLamp{id: "lamp"}
	engine, _ := lampEngine(t, nil, lamp)

	dim := Rule{
		ID: "dim", Name: "Dim when bright", Enabled: true,
		Trigger: Trigger{Type: TriggerDeviceState, Device: &DeviceTrigger{
			DeviceID: lamp.id, Metric: MetricBrightness, Operator: OpAbove, Value: 70,
		}},
		Actions: []Action{{DeviceID: lamp.id, Action: "set_brightness", Value: []byte("10")}},
	}
	raise := Rule{
		ID: "raise", Name: "Raise when dim", Enabled: true,
		Trigger: Trigger{Type: TriggerDeviceState, Device: &DeviceTrigger{
			DeviceID: lamp.id, Metric: MetricBrightness, Operator: OpBelow, Value: 20,
		}},
		Actions: []Action{{DeviceID: lamp.id, Action: "set_brightness", Value: []byte("90")}},
	}
	_, err := engine.Replace(State{Version: FormatVersion, Revision: engine.Snapshot().Revision, Items: []Rule{dim, raise}})
	var invalid ValidationError
	if err == nil || !asValidation(err, &invalid) {
		t.Fatalf("oscillating rules = %v, want ValidationError", err)
	}
}

// "Dim it when it goes above 70%" settles at the value it writes and cannot run
// again. Refusing that as a loop would rule out the most obvious use of a
// threshold trigger.
func TestSelfLimitingMetricRuleIsAllowed(t *testing.T) {
	lamp := &testLamp{id: "lamp"}
	engine, _ := lampEngine(t, nil, lamp)

	dim := Rule{
		ID: "dim", Name: "Dim when bright", Enabled: true,
		Trigger: Trigger{Type: TriggerDeviceState, Device: &DeviceTrigger{
			DeviceID: lamp.id, Metric: MetricBrightness, Operator: OpAbove, Value: 70,
		}},
		Actions: []Action{{DeviceID: lamp.id, Action: "set_brightness", Value: []byte("40")}},
	}
	if _, err := engine.Replace(State{Version: FormatVersion, Revision: engine.Snapshot().Revision, Items: []Rule{dim}}); err != nil {
		t.Fatalf("a self-limiting rule was rejected: %v", err)
	}

	// It also has to actually behave that way: one run, not a runaway.
	if err := lamp.SetBrightness(90); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return lamp.State().Brightness == 40 })
	time.Sleep(120 * time.Millisecond)
	if got := lamp.State().Brightness; got != 40 {
		t.Fatalf("brightness settled at %d, want 40", got)
	}
}

// Watching one value and changing an unrelated one is not a loop, and refusing
// it would rule out most useful automations.
func TestUnrelatedMetricsAreNotALoop(t *testing.T) {
	lamp := &testLamp{id: "lamp"}
	target := &testSwitch{id: "target"}
	engine, _ := lampEngine(t, nil, lamp, target)

	watch := Rule{
		ID: "watch", Name: "Bright lamp wakes the switch", Enabled: true,
		Trigger: Trigger{Type: TriggerDeviceState, Device: &DeviceTrigger{
			DeviceID: lamp.id, Metric: MetricBrightness, Operator: OpAbove, Value: 70,
		}},
		Actions: []Action{{DeviceID: target.id, Action: "on"}},
	}
	back := Rule{
		ID: "back", Name: "Switch on turns the lamp on", Enabled: true,
		Trigger: Trigger{Type: TriggerDeviceState, Device: &DeviceTrigger{DeviceID: target.id, On: true}},
		Actions: []Action{{DeviceID: lamp.id, Action: "on"}},
	}
	if _, err := engine.Replace(State{Version: FormatVersion, Revision: engine.Snapshot().Revision, Items: []Rule{watch, back}}); err != nil {
		t.Fatalf("rules watching different values were rejected: %v", err)
	}
}

// asValidation is errors.As with the concrete type this package returns.
func asValidation(err error, target *ValidationError) bool {
	if invalid, ok := err.(ValidationError); ok {
		*target = invalid
		return true
	}
	return false
}
