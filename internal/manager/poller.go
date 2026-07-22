package manager

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"setu/internal/device"
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
	interval time.Duration
	log      *slog.Logger

	activity  chan struct{}
	refresh   chan refreshRequest
	ready     chan struct{}
	readyOnce sync.Once
}

type refreshRequest struct {
	reply chan refreshResult
}

type refreshResult struct {
	states map[string]device.State
}

const (
	activeWindow       = 2 * time.Minute
	idleFive           = 15 * time.Minute
	idleTen            = time.Hour
	idleThirty         = 6 * time.Hour
	idleHour           = 24 * time.Hour
	refreshReuseWindow = 5 * time.Second
)

// NewPoller creates a Poller. A non-positive interval disables scheduled
// polling; Refresh still performs a user-requested one-shot poll.
func NewPoller(mgr *Manager, interval time.Duration, log *slog.Logger) *Poller {
	return &Poller{
		mgr:      mgr,
		interval: interval,
		log:      log,
		activity: make(chan struct{}, 1),
		refresh:  make(chan refreshRequest),
		ready:    make(chan struct{}),
	}
}

// Ready closes after the initial hardware baseline has completed, or
// immediately when scheduled polling is disabled. State-triggered automation
// uses it to avoid treating startup discovery as a real transition.
func (p *Poller) Ready() <-chan struct{} { return p.ready }

// Run polls until ctx is cancelled. The configured interval is the active
// cadence. With no app activity or physical state changes, it progressively
// backs off to 5m, 10m, 30m, 1h, then 6h. Activity and manual refreshes reset it
// to the active cadence.
func (p *Poller) Run(ctx context.Context) {
	lastActivity := time.Now()
	var timer *time.Timer
	var timerC <-chan time.Time
	var nextPoll time.Time
	var lastPollAt time.Time
	var lastPollStates map[string]device.State

	if p.interval > 0 {
		states, changed := p.pollOnce() // prime state before the first client connects
		lastPollAt = time.Now()
		lastPollStates = states
		if changed {
			lastActivity = lastPollAt
		}
		timer = time.NewTimer(p.interval)
		timerC = timer.C
		nextPoll = time.Now().Add(p.interval)
		defer timer.Stop()
	} else {
		// Keep the coordinator alive: scheduled polling is disabled, but an end
		// user must still be able to request a one-shot hardware refresh.
		p.log.Info("scheduled state polling disabled (poll_interval <= 0)")
	}
	p.readyOnce.Do(func() { close(p.ready) })

	for {
		select {
		case <-ctx.Done():
			return
		case <-p.activity:
			now := time.Now()
			lastActivity = now
			// Wake an idle/backed-off timer, but never postpone a sooner active
			// poll. Continuous interaction must not starve polling indefinitely.
			if timer != nil && nextPoll.After(now.Add(p.interval)) {
				resetTimer(timer, p.interval)
				nextPoll = now.Add(p.interval)
			}
		case req := <-p.refresh:
			now := time.Now()
			states := lastPollStates
			if lastPollAt.IsZero() || now.Sub(lastPollAt) > refreshReuseWindow {
				states, _ = p.pollOnce()
				now = time.Now()
				lastPollAt = now
				lastPollStates = states
			}
			lastActivity = now
			req.reply <- refreshResult{states: states}
			if timer != nil {
				resetTimer(timer, p.interval)
				nextPoll = now.Add(p.interval)
			}
		case <-timerC:
			states, changed := p.pollOnce()
			now := time.Now()
			lastPollAt = now
			lastPollStates = states
			if changed {
				// A physical/out-of-band change is useful activity too: stay fresh
				// briefly in case more changes follow.
				lastActivity = now
			}
			next := adaptiveInterval(p.interval, now.Sub(lastActivity))
			resetTimer(timer, next)
			nextPoll = now.Add(next)
		}
	}
}

// Activity records real app use without touching any device. Calls are
// coalesced, so pointer/key bursts never build an unbounded queue.
func (p *Poller) Activity() {
	select {
	case p.activity <- struct{}{}:
	default:
	}
}

// Refresh returns every successfully read state from a hardware poll. Requests
// arriving during or just after a cycle reuse that recent result, preventing a
// startup or multi-client burst from polling every device again. It works even
// when scheduled polling is disabled. The caller can overlay the result on the
// manager snapshot without racing the manager's async bus consumer.
func (p *Poller) Refresh(ctx context.Context) (map[string]device.State, error) {
	reply := make(chan refreshResult, 1)
	select {
	case p.refresh <- refreshRequest{reply: reply}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	select {
	case result := <-reply:
		return result.states, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func resetTimer(timer *time.Timer, interval time.Duration) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(interval)
}

// adaptiveInterval returns the next delay for the time since meaningful app or
// device activity. The configured interval remains a floor, so a deliberately
// slower installation is never sped up by the adaptive schedule.
func adaptiveInterval(base, idle time.Duration) time.Duration {
	var next time.Duration
	switch {
	case idle < activeWindow:
		next = base
	case idle < idleFive:
		next = 5 * time.Minute
	case idle < idleTen:
		next = 10 * time.Minute
	case idle < idleThirty:
		next = 30 * time.Minute
	case idle < idleHour:
		next = time.Hour
	default:
		next = 6 * time.Hour
	}
	if next < base {
		return base
	}
	return next
}

// pollOnce polls every Pollable device once and publishes any changes.
//
// Devices are polled concurrently, NOT in sequence: a Poll on an unreachable
// device runs to its full network timeout (an off TV is ~4s of REST connect
// timeout per cycle; an unplugged WiZ bulb ~3.5s of discovery + rpc), and done
// serially one slow device would delay every other device's state freshness to
// its own worst case. One goroutine per device per cycle is bounded and cheap.
// The coordinator and final Wait keep cycles from overlapping, so a device is
// never polled twice at once; the next delay starts after the cycle completes.
func (p *Poller) pollOnce() (map[string]device.State, bool) {
	var wg sync.WaitGroup
	var resultMu sync.Mutex
	states := make(map[string]device.State)
	changedAny := false
	for _, d := range p.mgr.Devices() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			state, pollable, changed, err := p.mgr.Poll(d.ID())
			if !pollable {
				return
			}
			if err != nil {
				p.log.Debug("poll failed", "device", d.ID(), "err", err)
				return
			}
			resultMu.Lock()
			states[d.ID()] = state
			if changed {
				changedAny = true
			}
			resultMu.Unlock()
		}()
	}
	wg.Wait()
	return states, changedAny
}
