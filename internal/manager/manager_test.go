package manager

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"setu/internal/control"
	"setu/internal/device"
	"setu/internal/events"
)

type rangedWhiteDevice struct{}

func (rangedWhiteDevice) ID() string                 { return "white" }
func (rangedWhiteDevice) Name() string               { return "White" }
func (rangedWhiteDevice) Brand() string              { return "test" }
func (rangedWhiteDevice) Model() string              { return "ranged_white" }
func (rangedWhiteDevice) MAC() string                { return "00:11:22:33:44:55" }
func (rangedWhiteDevice) Capabilities() []string     { return []string{device.CapColorTemp} }
func (rangedWhiteDevice) State() device.State        { return device.State{} }
func (rangedWhiteDevice) SetColorTemp(int) error     { return nil }
func (rangedWhiteDevice) ColorTempRange() (int, int) { return 2700, 6500 }

func TestViewOfIncludesColorTempRange(t *testing.T) {
	view := ViewOf(rangedWhiteDevice{})
	if view.ColorTempMin != 2700 || view.ColorTempMax != 6500 {
		t.Fatalf("color temperature range = %d–%d, want 2700–6500", view.ColorTempMin, view.ColorTempMax)
	}
}

type stalePollDevice struct {
	mu          sync.Mutex
	state       device.State
	pollStarted chan struct{}
	releasePoll chan struct{}
	startOnce   sync.Once
}

func (*stalePollDevice) ID() string             { return "race" }
func (*stalePollDevice) Name() string           { return "Race" }
func (*stalePollDevice) Brand() string          { return "test" }
func (*stalePollDevice) Model() string          { return "race" }
func (*stalePollDevice) MAC() string            { return "02:00:00:00:00:02" }
func (*stalePollDevice) Capabilities() []string { return []string{device.CapSwitch} }
func (d *stalePollDevice) State() device.State {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.state
}
func (d *stalePollDevice) On() error {
	d.mu.Lock()
	d.state.On = true
	d.mu.Unlock()
	return nil
}
func (d *stalePollDevice) Off() error {
	d.mu.Lock()
	d.state.On = false
	d.mu.Unlock()
	return nil
}
func (d *stalePollDevice) Poll() (device.State, error) {
	d.mu.Lock()
	stale := d.state
	d.mu.Unlock()
	d.startOnce.Do(func() { close(d.pollStarted) })
	<-d.releasePoll
	d.mu.Lock()
	d.state = stale
	d.mu.Unlock()
	return stale, nil
}

func TestCommandWaitsForInFlightPoll(t *testing.T) {
	bus := events.NewBus()
	dev := &stalePollDevice{
		state:       device.State{Online: true},
		pollStarted: make(chan struct{}),
		releasePoll: make(chan struct{}),
	}
	mgr := New(bus, []device.Device{dev})
	defer mgr.Close()

	pollDone := make(chan struct{})
	go func() {
		_, _, _, _ = mgr.Poll(dev.ID())
		close(pollDone)
	}()
	<-dev.pollStarted

	commandDone := make(chan struct{})
	go func() {
		_, _, _ = mgr.Command(dev.ID(), control.Request{Action: "on"})
		close(commandDone)
	}()
	select {
	case <-commandDone:
		t.Fatal("command overlapped an in-flight poll")
	case <-time.After(20 * time.Millisecond):
	}

	close(dev.releasePoll)
	<-pollDone
	<-commandDone
	if !dev.State().On {
		t.Fatal("stale poll response replaced the later successful command")
	}
}

type uncertainCommandDevice struct {
	mu    sync.Mutex
	state device.State
	polls int
}

func (*uncertainCommandDevice) ID() string             { return "uncertain" }
func (*uncertainCommandDevice) Name() string           { return "Uncertain" }
func (*uncertainCommandDevice) Brand() string          { return "test" }
func (*uncertainCommandDevice) Model() string          { return "uncertain" }
func (*uncertainCommandDevice) MAC() string            { return "02:00:00:00:00:03" }
func (*uncertainCommandDevice) Capabilities() []string { return []string{device.CapSwitch} }
func (d *uncertainCommandDevice) State() device.State {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.state
}
func (d *uncertainCommandDevice) On() error {
	d.mu.Lock()
	d.state.On = true // hardware applied the command, but its acknowledgement was lost
	d.mu.Unlock()
	return errors.New("acknowledgement lost")
}
func (d *uncertainCommandDevice) Off() error { return nil }
func (d *uncertainCommandDevice) Poll() (device.State, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.polls++
	return d.state, nil
}

func TestCommandReconcilesAmbiguousTransportFailure(t *testing.T) {
	bus := events.NewBus()
	dev := &uncertainCommandDevice{state: device.State{Online: true}}
	mgr := New(bus, []device.Device{dev})
	defer mgr.Close()

	view, found, err := mgr.Command(dev.ID(), control.Request{Action: "on"})
	if !found || err == nil {
		t.Fatalf("command result = found %v, err %v", found, err)
	}
	if !view.State.On || dev.polls != 1 {
		t.Fatalf("reconciled view = %+v, polls = %d", view.State, dev.polls)
	}
	if got := mgr.Snapshot()[0].State; !got.On {
		t.Fatalf("manager snapshot was not reconciled: %+v", got)
	}
}

func TestCommandDoesNotPollAfterInputError(t *testing.T) {
	bus := events.NewBus()
	dev := &uncertainCommandDevice{state: device.State{Online: true}}
	mgr := New(bus, []device.Device{dev})
	defer mgr.Close()

	view, found, err := mgr.Command(dev.ID(), control.Request{Action: "set_brightness"})
	var inputErr control.InputError
	if !found || !errors.As(err, &inputErr) {
		t.Fatalf("command result = found %v, view %+v, err %v", found, view, err)
	}
	if dev.polls != 0 {
		t.Fatalf("input error triggered %d hardware polls", dev.polls)
	}
}

func TestDiagnosticsKeepLatestPollAndCommandOutcome(t *testing.T) {
	bus := events.NewBus()
	dev := &uncertainCommandDevice{state: device.State{Online: true}}
	mgr := New(bus, []device.Device{dev})
	defer mgr.Close()

	initial := mgr.Diagnostics()
	if len(initial) != 1 || !initial[0].Pollable || initial[0].LastPollAt != 0 {
		t.Fatalf("initial diagnostics = %+v", initial)
	}

	if _, _, _, err := mgr.Poll(dev.ID()); err != nil {
		t.Fatalf("poll: %v", err)
	}
	_, _, commandErr := mgr.Command(dev.ID(), control.Request{Action: "on"})
	if commandErr == nil {
		t.Fatal("command unexpectedly succeeded")
	}

	got := mgr.Diagnostics()[0]
	if got.LastPollAt == 0 || got.LastPollError != "" {
		t.Fatalf("poll diagnostics = %+v", got)
	}
	if got.LastCommandAt == 0 || got.LastCommandAction != "on" {
		t.Fatalf("command diagnostics = %+v", got)
	}
	if got.LastCommandError != commandErr.Error() {
		t.Fatalf("command error = %q, want %q", got.LastCommandError, commandErr)
	}
}

func TestDiagnosticsBoundRetainedText(t *testing.T) {
	bus := events.NewBus()
	dev := &uncertainCommandDevice{state: device.State{Online: true}}
	mgr := New(bus, []device.Device{dev})
	defer mgr.Close()

	action := strings.Repeat("界", 100)
	if _, _, err := mgr.Command(dev.ID(), control.Request{Action: action}); err == nil {
		t.Fatal("oversized unknown action unexpectedly succeeded")
	}
	got := mgr.Diagnostics()[0]
	if len(got.LastCommandAction) > maxDiagnosticActionBytes ||
		!utf8.ValidString(got.LastCommandAction) ||
		!strings.HasSuffix(got.LastCommandAction, "…") {
		t.Fatalf("bounded action = %q (%d bytes)", got.LastCommandAction, len(got.LastCommandAction))
	}

	longError := errors.New(strings.Repeat("x", maxDiagnosticErrorBytes+100))
	boundedError := diagnosticError(longError)
	if len(boundedError) > maxDiagnosticErrorBytes ||
		!utf8.ValidString(boundedError) ||
		!strings.HasSuffix(boundedError, "…") {
		t.Fatalf("bounded error = %q (%d bytes)", boundedError, len(boundedError))
	}
}

func TestManagerResyncsAfterEventOverflow(t *testing.T) {
	bus := events.NewBus()
	dev := &stalePollDevice{state: device.State{Online: true}}
	mgr := New(bus, []device.Device{dev})
	defer mgr.Close()

	// Block normal cache writes long enough to overflow the manager's small event
	// buffer, then make the device's live state newer than every queued event.
	mgr.mu.Lock()
	for i := 0; i < 40; i++ {
		bus.Publish(events.Event{
			Type:     events.StateChanged,
			DeviceID: dev.ID(),
			State:    device.State{Online: true, On: i%2 == 0},
		})
	}
	dev.mu.Lock()
	dev.state.On = true
	dev.state.Brightness = 99
	dev.mu.Unlock()
	mgr.mu.Unlock()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if mgr.Snapshot()[0].State.On && mgr.Snapshot()[0].State.Brightness == 99 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("manager cache did not recover from overflow using live device state")
}

// failingPollDevice reports a failed hardware read. Its returned state is always
// meaningful (an unreachable device is offline); whether it wraps
// ErrPollNoResponse decides whether the manager is allowed to believe it.
type failingPollDevice struct {
	wrapSentinel bool
}

func (*failingPollDevice) ID() string             { return "failing" }
func (*failingPollDevice) Name() string           { return "Failing" }
func (*failingPollDevice) Brand() string          { return "test" }
func (*failingPollDevice) Model() string          { return "failing" }
func (*failingPollDevice) MAC() string            { return "02:00:00:00:00:04" }
func (*failingPollDevice) Capabilities() []string { return []string{device.CapSwitch} }
func (*failingPollDevice) State() device.State    { return device.State{Online: false} }
func (*failingPollDevice) On() error              { return nil }
func (*failingPollDevice) Off() error             { return nil }

func (d *failingPollDevice) Poll() (device.State, error) {
	state := device.State{Online: false}
	if d.wrapSentinel {
		return state, fmt.Errorf("no reply: %w", device.ErrPollNoResponse)
	}
	return state, errors.New("no reply")
}

// This is the contract that keeps an unreachable device from rendering as a
// healthy one: a driver whose fallback state is meaningful MUST wrap
// ErrPollNoResponse, and only then does the manager adopt and publish it. Both
// directions are pinned here because the difference is invisible at the call
// site — a driver that forgets the sentinel silently leaves the read model
// showing the last good state forever.
func TestPollAdoptsFallbackStateOnlyWhenSentinelIsWrapped(t *testing.T) {
	for _, tt := range []struct {
		name         string
		wrapSentinel bool
		wantOnline   bool
		wantEvent    bool
	}{
		{name: "wrapped sentinel is adopted", wrapSentinel: true, wantOnline: false, wantEvent: true},
		{name: "plain error is discarded", wrapSentinel: false, wantOnline: true, wantEvent: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			bus := events.NewBus()
			sub, unsubscribe := bus.Subscribe()
			defer unsubscribe()

			dev := &failingPollDevice{wrapSentinel: tt.wrapSentinel}
			mgr := New(bus, []device.Device{dev})
			defer mgr.Close()

			// Seed the read model with a healthy state, as a successful poll would.
			mgr.mu.Lock()
			mgr.latest[dev.ID()] = device.State{Online: true, On: true}
			mgr.mu.Unlock()

			state, pollable, changed, err := mgr.Poll(dev.ID())
			if !pollable || err == nil {
				t.Fatalf("Poll() = pollable %v, err %v; want a pollable device reporting failure", pollable, err)
			}
			if state.Online {
				t.Errorf("returned state.Online = true; the driver reported offline")
			}
			if changed != tt.wantEvent {
				t.Errorf("changed = %v, want %v", changed, tt.wantEvent)
			}

			if got := mgr.Snapshot()[0].State.Online; got != tt.wantOnline {
				t.Errorf("snapshot online = %v, want %v", got, tt.wantOnline)
			}

			select {
			case ev := <-sub:
				if !tt.wantEvent {
					t.Fatalf("unexpected state event published: %+v", ev.State)
				}
				if ev.State.Online {
					t.Errorf("published state.Online = true, want the offline state")
				}
			case <-time.After(100 * time.Millisecond):
				if tt.wantEvent {
					t.Fatal("no state event published for the adopted offline state")
				}
			}

			// Either way the failed contact is recorded, so diagnostics stay honest.
			if got := mgr.Diagnostics()[0].LastPollError; got == "" {
				t.Error("diagnostics recorded no poll error")
			}
		})
	}
}
