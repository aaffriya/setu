package automation

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"setu/internal/control"
	"setu/internal/events"
	"setu/internal/manager"
)

var (
	ErrRevision     = errors.New("automation revision changed")
	ErrNotFound     = errors.New("automation not found")
	ErrUnauthorized = errors.New("invalid webhook token")
	ErrRateLimited  = errors.New("webhook rate limit reached")
	ErrQueueFull    = errors.New("automation queue is full")
	ErrPaused       = errors.New("automations are paused")
	ErrDisabled     = errors.New("automation is disabled")
)

// ValidationError is safe to return as a 400 response when a proposed rule set
// is structurally invalid or references unsupported device capabilities.
type ValidationError struct{ Err error }

func (e ValidationError) Error() string { return e.Err.Error() }
func (e ValidationError) Unwrap() error { return e.Err }

const (
	workerCount       = 2
	queueSize         = 32
	maxRuns           = 20
	webhookRate       = 30
	idempotencyLimit  = 32
	idempotencyWindow = 5 * time.Minute
)

type Update struct {
	State           State             `json:"state"`
	GeneratedTokens map[string]string `json:"generated_tokens,omitempty"`
}

type TriggerResult struct {
	RunID  string `json:"run_id,omitempty"`
	Status string `json:"status"`
}

type runRequest struct {
	id     string
	rule   Rule
	source string
}

type rateWindow struct {
	started time.Time
	count   int
}

type delivery struct {
	runID string
	at    time.Time
}

// Engine owns a small immutable-at-execution rule set plus bounded runtime
// bookkeeping. Persistent writes happen only when configuration changes.
type Engine struct {
	mgr   *manager.Manager
	bus   *events.Bus
	store *Store
	log   *slog.Logger

	mu            sync.RWMutex
	state         State
	runs          []Run
	pending       map[string]bool
	running       map[string]bool
	lastTriggered map[string]time.Time
	lastSchedule  map[string]string
	latestPower   map[string]bool
	stableTimers  map[string]*time.Timer
	rates         map[string]rateWindow
	deliveries    map[string]map[string]delivery
	queue         chan runRequest
	ctx           context.Context
}

func New(mgr *manager.Manager, bus *events.Bus, store *Store, log *slog.Logger) (*Engine, error) {
	state, err := store.Load()
	if err != nil {
		return nil, err
	}
	disabled := disableInvalidRules(&state, mgr)
	if err := validateState(state, mgr); err != nil {
		return nil, fmt.Errorf("automations: invalid state: %w", err)
	}
	if len(disabled) > 0 {
		state.Revision++
		if err := store.Save(state); err != nil {
			log.Warn("could not persist disabled automations", "automations", disabled, "err", err)
		}
		log.Warn("disabled automations that no longer match configured devices", "automations", disabled)
	}
	e := &Engine{
		mgr:           mgr,
		bus:           bus,
		store:         store,
		log:           log,
		state:         cloneState(state),
		pending:       make(map[string]bool),
		running:       make(map[string]bool),
		lastTriggered: make(map[string]time.Time),
		lastSchedule:  make(map[string]string),
		latestPower:   make(map[string]bool),
		stableTimers:  make(map[string]*time.Timer),
		rates:         make(map[string]rateWindow),
		deliveries:    make(map[string]map[string]delivery),
		queue:         make(chan runRequest, queueSize),
	}
	return e, nil
}

// Run starts the two fixed workers and waits for the poller's startup baseline
// before arming state-change rules. It returns after ctx is cancelled.
func (e *Engine) Run(ctx context.Context, ready <-chan struct{}) {
	e.mu.Lock()
	e.ctx = ctx
	e.mu.Unlock()

	var workers sync.WaitGroup
	for range workerCount {
		workers.Add(1)
		go func() {
			defer workers.Done()
			e.worker(ctx)
		}()
	}
	defer workers.Wait()
	stream, resync, unsubscribe := e.bus.SubscribeRecoverable()
	defer unsubscribe()

	select {
	case <-ready:
	case <-ctx.Done():
		return
	}

	baseline := e.readPower()
	e.mu.Lock()
	e.latestPower = baseline
	e.mu.Unlock()

	// Evaluate the current schedule minute once, then wake only on minute
	// boundaries. There is no per-rule ticker.
	e.evaluateSchedules(time.Now())
	timer := time.NewTimer(untilNextMinute(time.Now()))
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			e.stopStableTimers()
			return
		case event, ok := <-stream:
			if !ok {
				return
			}
			if event.Type == events.StateChanged {
				e.handlePower(event.DeviceID, event.State.On)
			}
		case _, ok := <-resync:
			if !ok {
				return
			}
			// Overflow means the buffered stream is no longer a complete history.
			// Drop those stale entries before installing one authoritative snapshot;
			// replaying them afterwards could manufacture a false power edge.
			alive := true
			e.bus.Resync(func() {
				alive = drainPendingEvents(stream)
				if alive {
					e.resyncPower()
				}
			})
			if !alive {
				return
			}
		case now := <-timer.C:
			e.evaluateSchedules(now)
			timer.Reset(untilNextMinute(time.Now()))
		}
	}
}

func drainPendingEvents(stream <-chan events.Event) bool {
	// At most one full buffer existed when overflow was signalled. Bounding the
	// drain prevents a continuously publishing device from starving the loop.
	for range cap(stream) {
		select {
		case _, ok := <-stream:
			if !ok {
				return false
			}
		default:
			return true
		}
	}
	return true
}

func untilNextMinute(now time.Time) time.Duration {
	next := now.Truncate(time.Minute).Add(time.Minute)
	return next.Sub(now)
}

func (e *Engine) Snapshot() Snapshot {
	e.mu.RLock()
	defer e.mu.RUnlock()
	runs := make([]Run, len(e.runs))
	copy(runs, e.runs)
	return Snapshot{State: publicState(e.state), Runs: runs}
}

// Export returns the persistent form, including webhook hashes, for the single
// user-requested backup file. It never contains plaintext webhook tokens.
func (e *Engine) Export() State {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return cloneState(e.state)
}

// Replace atomically validates and persists the complete small rule set. Empty
// webhook hashes preserve an existing secret by rule id; new hooks get a token
// that is returned once in GeneratedTokens.
func (e *Engine) Replace(incoming State) (Update, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if incoming.Revision != e.state.Revision {
		return Update{}, ErrRevision
	}
	candidate := cloneState(incoming)
	candidate.Version = FormatVersion
	candidate.Revision = e.state.Revision + 1
	generated, err := e.mergeWebhookSecrets(&candidate)
	if err != nil {
		return Update{}, err
	}
	if err := validateState(candidate, e.mgr); err != nil {
		return Update{}, ValidationError{Err: err}
	}
	if err := e.store.Save(candidate); err != nil {
		return Update{}, err
	}
	e.stopStableTimersLocked()
	e.state = candidate
	// Configuration changes are rare. Reset per-rule clocks and webhook
	// bookkeeping so deleted ids cannot accumulate over a long-lived process.
	e.lastTriggered = make(map[string]time.Time)
	e.lastSchedule = make(map[string]string)
	e.rates = make(map[string]rateWindow)
	e.deliveries = make(map[string]map[string]delivery)
	return Update{State: publicState(candidate), GeneratedTokens: generated}, nil
}

func (e *Engine) RotateWebhookToken(id string) (string, State, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	candidate := cloneState(e.state)
	var webhook *Webhook
	for i := range candidate.Items {
		if candidate.Items[i].ID == id && candidate.Items[i].Trigger.Type == TriggerWebhook {
			webhook = candidate.Items[i].Trigger.Webhook
			break
		}
	}
	if webhook == nil {
		return "", State{}, ErrNotFound
	}
	token, hash, err := newSecret()
	if err != nil {
		return "", State{}, err
	}
	webhook.SecretHash = hash
	webhook.HasSecret = true
	candidate.Revision++
	if err := e.store.Save(candidate); err != nil {
		return "", State{}, err
	}
	e.state = candidate
	delete(e.deliveries, id)
	delete(e.rates, id)
	return token, publicState(candidate), nil
}

func (e *Engine) mergeWebhookSecrets(candidate *State) (map[string]string, error) {
	existing := make(map[string]string)
	for _, rule := range e.state.Items {
		if rule.Trigger.Webhook != nil {
			existing[rule.ID] = rule.Trigger.Webhook.SecretHash
		}
	}
	generated := make(map[string]string)
	for i := range candidate.Items {
		webhook := candidate.Items[i].Trigger.Webhook
		if webhook == nil {
			continue
		}
		if webhook.SecretHash == "" {
			webhook.SecretHash = existing[candidate.Items[i].ID]
		}
		if webhook.SecretHash == "" {
			token, hash, err := newSecret()
			if err != nil {
				return nil, err
			}
			webhook.SecretHash = hash
			generated[candidate.Items[i].ID] = token
		}
		webhook.HasSecret = true
	}
	if len(generated) == 0 {
		return nil, nil
	}
	return generated, nil
}

func newSecret() (string, string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "", fmt.Errorf("generate webhook token: %w", err)
	}
	token := "setu_hook_" + base64.RawURLEncoding.EncodeToString(raw)
	digest := sha256.Sum256([]byte(token))
	return token, hex.EncodeToString(digest[:]), nil
}

func newRunID() (string, error) {
	raw := make([]byte, 9)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return "run_" + base64.RawURLEncoding.EncodeToString(raw), nil
}

// RunNow uses the normal safety, condition, cooldown, and bounded-queue path.
func (e *Engine) RunNow(id string) (TriggerResult, error) {
	return e.enqueue(id, "manual")
}

// TriggerWebhook authenticates a per-rule token, coalesces caller retries, and
// enqueues the predefined rule. Payloads never select actions.
func (e *Engine) TriggerWebhook(id, token, idempotencyKey string) (TriggerResult, error) {
	if len(idempotencyKey) > 64 {
		return TriggerResult{}, ErrUnauthorized
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.authenticateWebhookLocked(id, token); err != nil {
		return TriggerResult{}, err
	}

	now := time.Now()
	if idempotencyKey != "" {
		entries := e.deliveries[id]
		for key, item := range entries {
			if now.Sub(item.at) > idempotencyWindow {
				delete(entries, key)
			}
		}
		if item, ok := entries[idempotencyKey]; ok {
			return TriggerResult{RunID: item.runID, Status: "duplicate"}, nil
		}
	}
	window := e.rates[id]
	if window.started.IsZero() || now.Sub(window.started) >= time.Minute {
		window = rateWindow{started: now}
	}
	if window.count >= webhookRate {
		return TriggerResult{}, ErrRateLimited
	}
	window.count++
	e.rates[id] = window

	result, err := e.enqueueLocked(id, "webhook")
	if err != nil {
		return TriggerResult{}, err
	}
	if idempotencyKey != "" && result.RunID != "" {
		entries := e.deliveries[id]
		if entries == nil || len(entries) >= idempotencyLimit {
			entries = make(map[string]delivery, idempotencyLimit)
			e.deliveries[id] = entries
		}
		entries[idempotencyKey] = delivery{runID: result.RunID, at: now}
	}
	return result, nil
}

// AuthenticateWebhook checks a per-rule secret without consuming a request
// body or mutating rate/queue state. The HTTP layer uses it before reading the
// ignored, bounded payload.
func (e *Engine) AuthenticateWebhook(id, token string) error {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.authenticateWebhookLocked(id, token)
}

func (e *Engine) authenticateWebhookLocked(id, token string) error {
	if len(token) < 16 || len(token) > 128 {
		return ErrUnauthorized
	}
	got := sha256.Sum256([]byte(token))
	var want [32]byte
	found := false
	for _, rule := range e.state.Items {
		if rule.ID != id || rule.Trigger.Type != TriggerWebhook || rule.Trigger.Webhook == nil {
			continue
		}
		decoded, err := hex.DecodeString(rule.Trigger.Webhook.SecretHash)
		if err == nil && len(decoded) == len(want) {
			copy(want[:], decoded)
			found = true
		}
		break
	}
	if subtle.ConstantTimeCompare(got[:], want[:]) != 1 || !found {
		return ErrUnauthorized
	}
	return nil
}

func (e *Engine) enqueue(id, source string) (TriggerResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.enqueueLocked(id, source)
}

func (e *Engine) enqueueAtRevision(id, source string, revision uint64) (TriggerResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.state.Revision != revision {
		return TriggerResult{}, ErrRevision
	}
	return e.enqueueLocked(id, source)
}

// enqueueLocked performs the atomic rule/dedupe/cooldown/queue decision. The
// caller must hold e.mu.
func (e *Engine) enqueueLocked(id, source string) (TriggerResult, error) {
	if e.state.Paused {
		return TriggerResult{}, ErrPaused
	}
	var rule *Rule
	for i := range e.state.Items {
		if e.state.Items[i].ID == id {
			copy := e.state.Items[i]
			rule = &copy
			break
		}
	}
	if rule == nil {
		return TriggerResult{}, ErrNotFound
	}
	if !rule.Enabled {
		return TriggerResult{}, ErrDisabled
	}
	if e.pending[id] || e.running[id] {
		return TriggerResult{Status: "already_running"}, nil
	}
	now := time.Now()
	if cooldown := time.Duration(rule.CooldownSeconds) * time.Second; cooldown > 0 && now.Sub(e.lastTriggered[id]) < cooldown {
		return TriggerResult{Status: "cooldown"}, nil
	}
	if !e.conditionsMet(rule.Conditions) {
		return TriggerResult{Status: "conditions_not_met"}, nil
	}
	runID, err := newRunID()
	if err != nil {
		return TriggerResult{}, err
	}
	e.pending[id] = true
	e.lastTriggered[id] = now
	request := runRequest{id: runID, rule: cloneState(State{Items: []Rule{*rule}}).Items[0], source: source}
	select {
	case e.queue <- request:
		return TriggerResult{RunID: runID, Status: "queued"}, nil
	default:
		delete(e.pending, id)
		return TriggerResult{}, ErrQueueFull
	}
}

func (e *Engine) conditionsMet(conditions []Condition) bool {
	for _, condition := range conditions {
		dev, ok := e.mgr.Device(condition.DeviceID)
		if !ok || dev.State().On != condition.On {
			return false
		}
	}
	return true
}

func (e *Engine) worker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case request := <-e.queue:
			e.execute(ctx, request)
		}
	}
}

func (e *Engine) execute(ctx context.Context, request runRequest) {
	e.mu.Lock()
	delete(e.pending, request.rule.ID)
	e.running[request.rule.ID] = true
	e.mu.Unlock()

	started := time.Now()
	results := make([]ActionResult, 0, len(request.rule.Actions))
	allOK := true
	for _, action := range request.rule.Actions {
		if ctx.Err() != nil {
			allOK = false
			results = append(results, ActionResult{DeviceID: action.DeviceID, Action: action.Action, Error: "shutting down"})
			break
		}
		if action.DelaySeconds > 0 {
			timer := time.NewTimer(time.Duration(action.DelaySeconds) * time.Second)
			select {
			case <-ctx.Done():
				if !timer.Stop() {
					<-timer.C
				}
				allOK = false
				results = append(results, ActionResult{DeviceID: action.DeviceID, Action: action.Action, Error: "shutting down"})
				goto done
			case <-timer.C:
			}
		}
		result := ActionResult{DeviceID: action.DeviceID, Action: action.Action}
		dev, ok := e.mgr.Device(action.DeviceID)
		if !ok {
			result.Error = "device is no longer configured"
			allOK = false
		} else if err := control.Execute(dev, action.request()); err != nil {
			result.Error = err.Error()
			allOK = false
			e.log.Warn("automation action failed", "automation", request.rule.ID, "device", action.DeviceID, "action", action.Action, "err", err)
		} else {
			result.OK = true
		}
		results = append(results, result)
	}

done:
	run := Run{
		ID:         request.id,
		RuleID:     request.rule.ID,
		RuleName:   request.rule.Name,
		Source:     request.source,
		StartedAt:  started,
		DurationMS: time.Since(started).Milliseconds(),
		OK:         allOK,
		Results:    results,
	}
	e.mu.Lock()
	delete(e.running, request.rule.ID)
	e.runs = append([]Run{run}, e.runs...)
	if len(e.runs) > maxRuns {
		e.runs = e.runs[:maxRuns]
	}
	e.mu.Unlock()
}

func (e *Engine) readPower() map[string]bool {
	states := make(map[string]bool)
	for _, dev := range e.mgr.Devices() {
		states[dev.ID()] = dev.State().On
	}
	return states
}

func (e *Engine) resyncPower() {
	for id, on := range e.readPower() {
		e.handlePower(id, on)
	}
}

func (e *Engine) handlePower(deviceID string, on bool) {
	e.mu.Lock()
	revision := e.state.Revision
	previous, known := e.latestPower[deviceID]
	e.latestPower[deviceID] = on
	rules := make([]Rule, 0)
	for _, rule := range e.state.Items {
		trigger := rule.Trigger.Device
		if !rule.Enabled || trigger == nil || trigger.DeviceID != deviceID {
			continue
		}
		if on != trigger.On {
			if timer := e.stableTimers[rule.ID]; timer != nil {
				timer.Stop()
				delete(e.stableTimers, rule.ID)
			}
			continue
		}
		if !known || previous == on {
			continue
		}
		if trigger.StableSeconds == 0 {
			rules = append(rules, rule)
			continue
		}
		if timer := e.stableTimers[rule.ID]; timer != nil {
			timer.Stop()
		}
		ruleID := rule.ID
		want := trigger.On
		e.stableTimers[rule.ID] = time.AfterFunc(time.Duration(trigger.StableSeconds)*time.Second, func() {
			e.mu.Lock()
			delete(e.stableTimers, ruleID)
			still := e.latestPower[deviceID] == want
			ctx := e.ctx
			e.mu.Unlock()
			if still && ctx != nil && ctx.Err() == nil {
				_, _ = e.enqueueAtRevision(ruleID, "device", revision)
			}
		})
	}
	e.mu.Unlock()
	for _, rule := range rules {
		_, _ = e.enqueueAtRevision(rule.ID, "device", revision)
	}
}

func (e *Engine) evaluateSchedules(now time.Time) {
	e.mu.Lock()
	revision := e.state.Revision
	rules := make([]Rule, 0)
	for _, rule := range e.state.Items {
		schedule := rule.Trigger.Schedule
		if !rule.Enabled || schedule == nil {
			continue
		}
		local := now.UTC().Add(time.Duration(schedule.UTCOffsetMinutes) * time.Minute)
		minuteKey := local.Format("2006-01-02 15:04")
		if local.Format("15:04") != schedule.Time || !containsDay(schedule.Weekdays, int(local.Weekday())) || e.lastSchedule[rule.ID] == minuteKey {
			continue
		}
		e.lastSchedule[rule.ID] = minuteKey
		rules = append(rules, rule)
	}
	e.mu.Unlock()
	for _, rule := range rules {
		_, _ = e.enqueueAtRevision(rule.ID, "schedule", revision)
	}
}

func containsDay(days []int, want int) bool {
	for _, day := range days {
		if day == want {
			return true
		}
	}
	return false
}

func (e *Engine) stopStableTimers() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.stopStableTimersLocked()
}

func (e *Engine) stopStableTimersLocked() {
	for id, timer := range e.stableTimers {
		timer.Stop()
		delete(e.stableTimers, id)
	}
}
