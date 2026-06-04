package manager

import (
	"context"
	"log/slog"
	"time"

	"setu/internal/device"
	"setu/internal/events"
)

// Poller periodically re-reads the state of every device.Pollable device and
// publishes a StateChanged event when it detects a change. This catches
// out-of-band changes (e.g. a physical switch) the app didn't initiate. Devices
// that don't implement device.Pollable are skipped.
//
// It is wired generically over whatever devices the registry holds, so it needs
// no per-brand knowledge.
type Poller struct {
	mgr      *Manager
	bus      *events.Bus
	interval time.Duration
	log      *slog.Logger

	last map[string]device.State
}

// NewPoller creates a Poller. A non-positive interval disables polling.
func NewPoller(mgr *Manager, bus *events.Bus, interval time.Duration, log *slog.Logger) *Poller {
	return &Poller{
		mgr:      mgr,
		bus:      bus,
		interval: interval,
		log:      log,
		last:     make(map[string]device.State),
	}
}

// Run polls on each tick until ctx is cancelled. It blocks, so run it in its own
// goroutine.
func (p *Poller) Run(ctx context.Context) {
	if p.interval <= 0 {
		p.log.Info("state poller disabled (poll_interval <= 0)")
		return
	}
	p.pollOnce() // prime device state immediately so the UI isn't blank on startup
	t := time.NewTicker(p.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			p.pollOnce()
		}
	}
}

// pollOnce polls every Pollable device once and publishes any changes. State is
// a comparable struct, so a plain == detects changes without extra bookkeeping.
func (p *Poller) pollOnce() {
	for _, d := range p.mgr.Devices() {
		pd, ok := d.(device.Pollable)
		if !ok {
			continue
		}
		state, err := pd.Poll()
		if err != nil {
			p.log.Debug("poll failed", "device", d.ID(), "err", err)
			continue
		}
		if prev, seen := p.last[d.ID()]; seen && prev == state {
			continue // unchanged; emit nothing
		}
		p.last[d.ID()] = state
		p.bus.Publish(events.Event{
			Type:     events.StateChanged,
			DeviceID: d.ID(),
			State:    state,
		})
	}
}
