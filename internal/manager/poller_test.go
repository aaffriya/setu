package manager

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"setu/internal/device"
	"setu/internal/events"
)

// fakeDevice is a minimal Pollable device. Each Poll bumps Brightness (so every
// tick is a state change → a publish) and can be given a fixed delay to stand
// in for a device stuck in a network timeout.
type fakeDevice struct {
	id    string
	delay time.Duration
	polls atomic.Int64
}

func (f *fakeDevice) ID() string             { return f.id }
func (f *fakeDevice) Name() string           { return f.id }
func (f *fakeDevice) Brand() string          { return "fake" }
func (f *fakeDevice) Model() string          { return "test" }
func (f *fakeDevice) MAC() string            { return "02:00:00:00:00:01" }
func (f *fakeDevice) Capabilities() []string { return []string{device.CapSwitch} }
func (f *fakeDevice) State() device.State {
	return device.State{Online: true, Brightness: int(f.polls.Load())}
}
func (f *fakeDevice) Poll() (device.State, error) {
	if f.delay > 0 {
		time.Sleep(f.delay)
	}
	return device.State{Online: true, Brightness: int(f.polls.Add(1))}, nil
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestPollOnceConcurrent pins the poller's concurrency contract: a cycle costs
// the SLOWEST device, not the sum — one off TV in a connect timeout must not
// delay every other device's state freshness (the bug the concurrent pollOnce
// fixed; a regression to serial polling makes this fail on time).
func TestPollOnceConcurrent(t *testing.T) {
	bus := events.NewBus()
	const n, delay = 5, 50 * time.Millisecond
	devs := make([]device.Device, n)
	for i := range devs {
		devs[i] = &fakeDevice{id: fmt.Sprintf("d%d", i), delay: delay}
	}
	m := New(bus, devs)
	defer m.Close()

	p := NewPoller(m, bus, time.Second, testLogger())
	start := time.Now()
	p.pollOnce()
	elapsed := time.Since(start)

	// Serial would be n*delay (250ms); concurrent ≈ delay. Allow generous slack.
	if elapsed > time.Duration(n)*delay/2 {
		t.Errorf("pollOnce took %v — devices appear to be polled serially", elapsed)
	}
	for _, d := range devs {
		if d.(*fakeDevice).polls.Load() != 1 {
			t.Errorf("device %s polled %d times, want 1", d.ID(), d.(*fakeDevice).polls.Load())
		}
	}
}

// TestPollerRace exercises the poller, the bus, and concurrent API reads
// (Snapshot, the handler path) together under -race: the poller publishes from
// per-device goroutines while the manager consumes events and serves snapshots.
func TestPollerRace(t *testing.T) {
	bus := events.NewBus()
	devs := make([]device.Device, 8)
	for i := range devs {
		devs[i] = &fakeDevice{id: fmt.Sprintf("d%d", i)}
	}
	m := New(bus, devs)
	defer m.Close()

	p := NewPoller(m, bus, time.Millisecond, testLogger())
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	done := make(chan struct{})
	go func() { p.Run(ctx); close(done) }()

	for ctx.Err() == nil {
		_ = m.Snapshot() // concurrent reader, like GET /api/devices
		time.Sleep(2 * time.Millisecond)
	}
	<-done

	// Every device must have been polled and its change reached the cache.
	for _, v := range m.Snapshot() {
		if v.State.Brightness == 0 {
			t.Errorf("device %s: poll results never reached the snapshot cache", v.ID)
		}
	}
}

func TestAdaptiveInterval(t *testing.T) {
	const base = 45 * time.Second
	tests := []struct {
		name string
		idle time.Duration
		want time.Duration
	}{
		{"active", time.Minute, base},
		{"short idle", 2 * time.Minute, 5 * time.Minute},
		{"idle", 15 * time.Minute, 10 * time.Minute},
		{"long idle", time.Hour, 30 * time.Minute},
		{"very long idle", 6 * time.Hour, time.Hour},
		{"day idle", 24 * time.Hour, 6 * time.Hour},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := adaptiveInterval(base, tt.idle); got != tt.want {
				t.Fatalf("adaptiveInterval(%v, %v) = %v, want %v", base, tt.idle, got, tt.want)
			}
		})
	}

	// A configured cadence slower than an idle stage remains the floor.
	if got := adaptiveInterval(20*time.Minute, 3*time.Minute); got != 20*time.Minute {
		t.Fatalf("slow configured floor = %v, want 20m", got)
	}
}

func TestManualRefreshWhenScheduledPollingDisabled(t *testing.T) {
	bus := events.NewBus()
	dev := &fakeDevice{id: "manual"}
	m := New(bus, []device.Device{dev})
	defer m.Close()

	p := NewPoller(m, bus, 0, testLogger())
	runCtx, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		p.Run(runCtx)
		close(done)
	}()

	refreshCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	states, err := p.Refresh(refreshCtx)
	if err != nil {
		cancel()
		stop()
		<-done
		t.Fatalf("Refresh: %v", err)
	}
	if got := states[dev.ID()].Brightness; got != 1 {
		t.Errorf("refreshed brightness = %d, want 1", got)
	}
	states, err = p.Refresh(refreshCtx)
	cancel()
	if err != nil {
		stop()
		<-done
		t.Fatalf("second Refresh: %v", err)
	}
	if got := states[dev.ID()].Brightness; got != 1 {
		t.Errorf("reused brightness = %d, want 1", got)
	}
	if got := dev.polls.Load(); got != 1 {
		t.Errorf("back-to-back refreshes ran %d hardware polls, want 1", got)
	}

	stop()
	<-done
}

func TestRefreshReusesInFlightInitialPoll(t *testing.T) {
	bus := events.NewBus()
	dev := &fakeDevice{id: "startup", delay: 50 * time.Millisecond}
	m := New(bus, []device.Device{dev})
	defer m.Close()

	p := NewPoller(m, bus, time.Hour, testLogger())
	runCtx, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		p.Run(runCtx)
		close(done)
	}()

	refreshCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	states, err := p.Refresh(refreshCtx)
	cancel()
	if err != nil {
		stop()
		<-done
		t.Fatalf("Refresh: %v", err)
	}
	if got := states[dev.ID()].Brightness; got != 1 {
		t.Errorf("startup brightness = %d, want 1", got)
	}
	if got := dev.polls.Load(); got != 1 {
		t.Errorf("startup refresh ran %d hardware polls, want 1", got)
	}

	stop()
	<-done
}

func TestActivityDoesNotPostponeActivePolls(t *testing.T) {
	bus := events.NewBus()
	dev := &fakeDevice{id: "active"}
	m := New(bus, []device.Device{dev})
	defer m.Close()

	p := NewPoller(m, bus, 20*time.Millisecond, testLogger())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		p.Run(ctx)
		close(done)
	}()

	deadline := time.After(140 * time.Millisecond)
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-tick.C:
			p.Activity()
		case <-deadline:
			cancel()
			<-done
			if got := dev.polls.Load(); got < 4 {
				t.Fatalf("continuous activity postponed polling: got %d polls, want at least 4", got)
			}
			return
		}
	}
}
