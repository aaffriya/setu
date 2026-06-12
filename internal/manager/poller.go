package manager

import (
	"context"
	"log/slog"
	"sync"
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

	mu   sync.Mutex // guards last: pollOnce polls devices concurrently
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

// pollOnce polls every Pollable device once and publishes any changes.
//
// Devices are polled concurrently, NOT in sequence: a Poll on an unreachable
// device runs to its full network timeout (an off TV is ~4s of REST connect
// timeout per tick; an unplugged WiZ bulb ~3.5s of discovery + rpc), and done
// serially one slow device would delay every other device's state freshness to
// its own worst case. One goroutine per device per tick is bounded and cheap.
// The final Wait keeps cycles from overlapping, so a device is never polled
// twice at once; if a cycle outruns the interval the ticker just drops ticks.
func (p *Poller) pollOnce() {
	var wg sync.WaitGroup
	for _, d := range p.mgr.Devices() {
		pd, ok := d.(device.Pollable)
		if !ok {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			state, err := pd.Poll()
			if err != nil {
				p.log.Debug("poll failed", "device", d.ID(), "err", err)
				return
			}
			// State is a comparable struct, so a plain == detects changes
			// without extra bookkeeping.
			p.mu.Lock()
			prev, seen := p.last[d.ID()]
			changed := !seen || prev != state
			if changed {
				p.last[d.ID()] = state
			}
			p.mu.Unlock()
			if changed {
				p.bus.Publish(events.Event{
					Type:     events.StateChanged,
					DeviceID: d.ID(),
					State:    state,
				})
			}
		}()
	}
	wg.Wait()
}
