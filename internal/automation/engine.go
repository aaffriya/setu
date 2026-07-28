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

	"setu/internal/device"
	"setu/internal/events"
	"setu/internal/manager"
	"setu/internal/resolver"
)

var (
	ErrRevision         = errors.New("automation revision changed")
	ErrNotFound         = errors.New("automation not found")
	ErrUnauthorized     = errors.New("invalid webhook token")
	ErrRateLimited      = errors.New("webhook rate limit reached")
	ErrQueueFull        = errors.New("automation queue is full")
	ErrPaused           = errors.New("automations are paused")
	ErrDisabled         = errors.New("automation is disabled")
	errConditionsNotMet = errors.New("automation conditions are not met")
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
	// presenceInterval is how often the neighbour table is read. Presence is
	// inherently coarse — an entry lingers for minutes after a device leaves —
	// so reading it faster would cost work without telling us anything new.
	presenceInterval = 30 * time.Second
)

// PresenceFunc reports which MAC addresses are currently visible on the LAN,
// keyed by their normalized form. A nil PresenceFunc means this host cannot see
// its neighbours, and presence rules are refused rather than silently inert.
//
// It is a function, not an interface: there is one implementation
// (resolver.ARPResolver) and nothing here varies but the answer.
type PresenceFunc func() (map[string]bool, error)

type Update struct {
	State           State             `json:"state"`
	GeneratedTokens map[string]string `json:"generated_tokens,omitempty"`
}

type TriggerResult struct {
	RunID  string `json:"run_id,omitempty"`
	Status string `json:"status"`
}

type runRequest struct {
	id            string
	rule          Rule
	source        string
	offsetMinutes int
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
	mgr      *manager.Manager
	bus      *events.Bus
	store    *Store
	presence PresenceFunc
	log      *slog.Logger

	mu            sync.RWMutex
	state         State
	runs          []Run
	pending       map[string]bool
	running       map[string]bool
	lastTriggered map[string]time.Time
	lastSchedule  map[string]string
	latestStates  map[string]device.State
	// offlineSince records when a device was first seen unreachable; an absent
	// entry means it is reachable. It drives both elapsed-offline rules and the
	// duration compared when a device returns. offlineFired keeps an offline
	// rule to one run per episode, and is cleared when its device returns.
	offlineSince map[string]time.Time
	offlineFired map[string]bool
	// latestPresence is the last neighbour-table answer per watched MAC.
	latestPresence map[string]bool
	stableTimers   map[string]*time.Timer
	rates          map[string]rateWindow
	deliveries     map[string]map[string]delivery
	queue          chan runRequest
	ctx            context.Context
}

// New loads and validates the stored rules. presence may be nil on a host whose
// neighbour table cannot be read; presence rules are then rejected instead of
// being accepted and never firing.
func New(mgr *manager.Manager, bus *events.Bus, store *Store, presence PresenceFunc, log *slog.Logger) (*Engine, error) {
	state, err := store.Load()
	if err != nil {
		return nil, err
	}
	disabled := disableInvalidRules(&state, mgr, presence != nil)
	if err := validateState(state, mgr, presence != nil); err != nil {
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
		mgr:            mgr,
		bus:            bus,
		store:          store,
		presence:       presence,
		log:            log,
		state:          cloneState(state),
		pending:        make(map[string]bool),
		running:        make(map[string]bool),
		lastTriggered:  make(map[string]time.Time),
		lastSchedule:   make(map[string]string),
		latestStates:   make(map[string]device.State),
		offlineSince:   make(map[string]time.Time),
		offlineFired:   make(map[string]bool),
		latestPresence: make(map[string]bool),
		stableTimers:   make(map[string]*time.Timer),
		rates:          make(map[string]rateWindow),
		deliveries:     make(map[string]map[string]delivery),
		queue:          make(chan runRequest, queueSize),
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

	baseline := e.readStates()
	now := time.Now()
	e.mu.Lock()
	e.latestStates = baseline
	// Startup is a baseline, not a transition: a device that is already
	// unreachable starts its clock now rather than looking like it just left.
	for id, state := range baseline {
		if !state.Online {
			e.offlineSince[id] = now
		}
	}
	e.mu.Unlock()

	// Evaluate the current schedule minute once, then wake only on minute
	// boundaries. There is no per-rule ticker.
	e.evaluateSchedules(now)
	timer := time.NewTimer(untilNextMinute(now))
	defer timer.Stop()

	// Presence has its own, slower clock. Seed it before the first tick so the
	// devices already on the LAN at startup are a baseline too.
	presence := newPresenceTicker(e.presence)
	defer presence.Stop()
	e.syncPresence()

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
				e.handleState(event.DeviceID, event.State)
			} else if event.Type == events.InventoryChanged && event.DeviceIDs != nil {
				e.reconcileInventory(event.DeviceIDs, event.ResetDevices)
			}
		case _, ok := <-resync:
			if !ok {
				return
			}
			// Overflow means the buffered stream is no longer a complete history.
			// Drop those stale entries before installing one authoritative snapshot;
			// replaying them afterwards could manufacture a false edge.
			alive := true
			e.bus.Resync(func() {
				alive = drainPendingEvents(stream)
				if alive {
					e.resyncStates()
				}
			})
			if !alive {
				return
			}
		case <-presence.C:
			e.syncPresence()
		case now := <-timer.C:
			e.evaluateSchedules(now)
			// Offline rules are measured in minutes, so the schedule clock is
			// exactly the right one to check them on — no extra goroutine.
			e.evaluateOffline(now)
			timer.Reset(untilNextMinute(time.Now()))
		}
	}
}

// newPresenceTicker returns a ticker that never fires when presence cannot be
// read, so the select above needs no special case for an unsupported host.
func newPresenceTicker(presence PresenceFunc) *time.Ticker {
	ticker := time.NewTicker(presenceInterval)
	if presence == nil {
		ticker.Stop()
	}
	return ticker
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
	return Snapshot{State: publicState(e.state), Runs: runs, Presence: e.presence != nil}
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
	if err := validateState(candidate, e.mgr, e.presence != nil); err != nil {
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
	// Both of these are pruned rather than cleared, because clearing and keeping
	// are each wrong in one direction.
	//
	// Clearing which offline rules have fired would rearm every one of them, so
	// editing an unrelated rule during an outage would announce that outage
	// again; an offline rule rearms when its device is seen back, and nowhere
	// else. Keeping what a MAC last looked like for a rule that has gone would
	// fire that rule the moment it came back, comparing against an answer from
	// before it existed. Pruning gives both the behaviour that survives an edit
	// and a fresh baseline for anything genuinely new.
	surviving := make(map[string]bool, len(e.offlineFired))
	for _, rule := range candidate.Items {
		if rule.Trigger.Offline != nil && e.offlineFired[rule.ID] {
			surviving[rule.ID] = true
		}
	}
	e.offlineFired = surviving

	stillWatched := watchedMACs(candidate.Items)
	for mac := range e.latestPresence {
		if _, ok := stillWatched[mac]; !ok {
			delete(e.latestPresence, mac)
		}
	}
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
// A timed schedule runs only its zero-offset actions; later wall-clock steps
// remain owned by the minute scheduler.
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

func (e *Engine) enqueueScheduleAtRevision(id string, offset int, revision uint64) (TriggerResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.state.Revision != revision {
		return TriggerResult{}, ErrRevision
	}
	return e.enqueueRuleLocked(id, "schedule", &offset, false)
}

// enqueueLocked performs the atomic rule/dedupe/cooldown/queue decision. The
// caller must hold e.mu.
func (e *Engine) enqueueLocked(id, source string) (TriggerResult, error) {
	return e.enqueueRuleLocked(id, source, nil, true)
}

// enqueueRuleLocked queues either a complete ordinary rule or one due step of
// a timed schedule. Scheduled steps are already deduplicated by wall-clock
// minute, so rule cooldown must not suppress a later planned offset.
func (e *Engine) enqueueRuleLocked(id, source string, scheduledOffset *int, checkCooldown bool) (TriggerResult, error) {
	if e.state.Paused {
		return TriggerResult{}, ErrPaused
	}
	var rule *Rule
	for i := range e.state.Items {
		if e.state.Items[i].ID == id {
			match := e.state.Items[i]
			rule = &match
			break
		}
	}
	if rule == nil {
		return TriggerResult{}, ErrNotFound
	}
	if !rule.Enabled {
		return TriggerResult{}, ErrDisabled
	}
	requestRule := cloneState(State{Items: []Rule{*rule}}).Items[0]
	requestOffset := 0
	if scheduledOffset != nil {
		requestOffset = *scheduledOffset
		requestRule.Actions = actionsAtOffset(requestRule.Actions, requestOffset)
	} else if source == "manual" && requestRule.Trigger.Type == TriggerSchedule {
		requestRule.Actions = actionsAtOffset(requestRule.Actions, 0)
	}
	if len(requestRule.Actions) == 0 {
		return TriggerResult{Status: "no_immediate_actions"}, nil
	}
	if e.pending[id] || e.running[id] {
		return TriggerResult{Status: "already_running"}, nil
	}
	now := time.Now()
	if cooldown := time.Duration(rule.CooldownSeconds) * time.Second; checkCooldown && cooldown > 0 && now.Sub(e.lastTriggered[id]) < cooldown {
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
	request := runRequest{id: runID, rule: requestRule, source: source, offsetMinutes: requestOffset}
	select {
	case e.queue <- request:
		// Cooldown starts only once the run is accepted. A full queue did not
		// trigger anything and must not suppress the caller's next attempt.
		// Timed schedule steps update the clock for manual/nested callers, but
		// their own exact future offsets deliberately bypass the check.
		e.lastTriggered[id] = now
		return TriggerResult{RunID: runID, Status: "queued"}, nil
	default:
		delete(e.pending, id)
		return TriggerResult{}, ErrQueueFull
	}
}

func actionsAtOffset(actions []Action, offset int) []Action {
	out := make([]Action, 0, len(actions))
	for _, action := range actions {
		if action.OffsetMinutes == offset {
			out = append(out, action)
		}
	}
	return out
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
	remaining := MaxRunActions
	remainingDelay := MaxRunDelay
	e.executeRequest(ctx, request, 1, &remaining, &remainingDelay)
}

func (e *Engine) executeRequest(ctx context.Context, request runRequest, depth int, remaining, remainingDelay *int) Run {
	started := time.Now()
	results := make([]ActionResult, 0, len(request.rule.Actions))
	allOK := true
	for _, action := range request.rule.Actions {
		if *remaining <= 0 {
			allOK = false
			results = append(results, actionFailure(action, fmt.Sprintf("nested action limit exceeds %d", MaxRunActions)))
			break
		}
		*remaining = *remaining - 1
		if ctx.Err() != nil {
			allOK = false
			results = append(results, actionFailure(action, "shutting down"))
			break
		}
		if action.DelaySeconds > 0 {
			if action.DelaySeconds > *remainingDelay {
				allOK = false
				results = append(results, actionFailure(action, fmt.Sprintf("nested delay limit exceeds %d seconds", MaxRunDelay)))
				break
			}
			*remainingDelay -= action.DelaySeconds
			timer := time.NewTimer(time.Duration(action.DelaySeconds) * time.Second)
			select {
			case <-ctx.Done():
				if !timer.Stop() {
					<-timer.C
				}
				allOK = false
				results = append(results, actionFailure(action, "shutting down"))
				goto done
			case <-timer.C:
			}
		}
		result := ActionResult{DeviceID: action.DeviceID, AutomationID: action.AutomationID, Action: action.Action}
		if !e.conditionsMet(action.When) {
			result.Skipped = true
			results = append(results, result)
			continue
		}
		var err error
		if action.Action == ActionAutomation {
			err = e.runNested(ctx, action.AutomationID, request.rule.ID, depth+1, remaining, remainingDelay)
		} else {
			_, found, commandErr := e.mgr.Command(action.DeviceID, action.request())
			if !found {
				err = fmt.Errorf("device is no longer configured")
			} else {
				err = commandErr
			}
		}
		if errors.Is(err, errConditionsNotMet) {
			result.Skipped = true
		} else if err != nil {
			result.Error = err.Error()
			allOK = false
			e.log.Warn("automation action failed", "automation", request.rule.ID, "target", actionTarget(action), "action", action.Action, "err", err)
		} else {
			result.OK = true
		}
		results = append(results, result)
	}

done:
	run := Run{
		ID:            request.id,
		RuleID:        request.rule.ID,
		RuleName:      request.rule.Name,
		Source:        request.source,
		OffsetMinutes: request.offsetMinutes,
		StartedAt:     started,
		DurationMS:    time.Since(started).Milliseconds(),
		OK:            allOK,
		Results:       results,
	}
	e.mu.Lock()
	delete(e.running, request.rule.ID)
	e.runs = append([]Run{run}, e.runs...)
	if len(e.runs) > maxRuns {
		e.runs = e.runs[:maxRuns]
	}
	e.mu.Unlock()
	return run
}

func actionFailure(action Action, message string) ActionResult {
	return ActionResult{
		DeviceID: action.DeviceID, AutomationID: action.AutomationID,
		Action: action.Action, Error: message,
	}
}

func actionTarget(action Action) string {
	if action.Action == ActionAutomation {
		return action.AutomationID
	}
	return action.DeviceID
}

// runNested executes a referenced rule inline so its actions preserve the
// parent's declared order. Configuration validation rejects cycles and caps the
// chain depth; the runtime checks remain as defence against an edit racing a run.
func (e *Engine) runNested(ctx context.Context, id, parentID string, depth int, remaining, remainingDelay *int) error {
	if depth > MaxNesting {
		return fmt.Errorf("nested automation depth exceeds %d", MaxNesting)
	}

	e.mu.Lock()
	if e.state.Paused {
		e.mu.Unlock()
		return ErrPaused
	}
	var rule *Rule
	for i := range e.state.Items {
		if e.state.Items[i].ID == id {
			match := e.state.Items[i]
			rule = &match
			break
		}
	}
	if rule == nil {
		e.mu.Unlock()
		return ErrNotFound
	}
	if !rule.Enabled {
		e.mu.Unlock()
		return ErrDisabled
	}
	if e.pending[id] || e.running[id] {
		e.mu.Unlock()
		return fmt.Errorf("automation is already running")
	}
	now := time.Now()
	if cooldown := time.Duration(rule.CooldownSeconds) * time.Second; cooldown > 0 && now.Sub(e.lastTriggered[id]) < cooldown {
		e.mu.Unlock()
		return fmt.Errorf("automation is in cooldown")
	}
	if !e.conditionsMet(rule.Conditions) {
		e.mu.Unlock()
		return errConditionsNotMet
	}
	requestRule := cloneState(State{Items: []Rule{*rule}}).Items[0]
	if requestRule.Trigger.Type == TriggerSchedule {
		requestRule.Actions = actionsAtOffset(requestRule.Actions, 0)
		if len(requestRule.Actions) == 0 {
			e.mu.Unlock()
			return fmt.Errorf("automation has no immediate actions")
		}
	}
	runID, err := newRunID()
	if err != nil {
		e.mu.Unlock()
		return err
	}
	e.running[id] = true
	e.lastTriggered[id] = now
	request := runRequest{
		id: runID, rule: requestRule,
		source: "automation:" + parentID,
	}
	e.mu.Unlock()

	run := e.executeRequest(ctx, request, depth, remaining, remainingDelay)
	if !run.OK {
		return fmt.Errorf("nested automation failed")
	}
	return nil
}

func (e *Engine) readStates() map[string]device.State {
	states := make(map[string]device.State)
	for _, dev := range e.mgr.Devices() {
		states[dev.ID()] = dev.State()
	}
	return states
}

// reconcileInventory installs new devices as a baseline and forgets every
// transient clock belonging to a removed device. deviceIDs is captured by the
// inventory mutation itself: consulting only the manager here would race a
// quick remove-and-re-add of the same MAC-derived id.
func (e *Engine) reconcileInventory(deviceIDs []string, reset bool) {
	current := e.readStates()
	live := make(map[string]device.State, len(deviceIDs))
	for _, id := range deviceIDs {
		if state, ok := current[id]; ok {
			live[id] = state
		}
	}

	now := time.Now()
	e.mu.Lock()
	defer e.mu.Unlock()
	if reset {
		for id := range e.latestStates {
			e.forgetDeviceLocked(id)
		}
	}
	for id := range e.latestStates {
		if _, ok := live[id]; !ok {
			e.forgetDeviceLocked(id)
		}
	}
	for id := range e.offlineSince {
		if _, ok := live[id]; !ok {
			e.forgetDeviceLocked(id)
		}
	}
	for id, state := range live {
		if _, known := e.latestStates[id]; known {
			continue
		}
		e.latestStates[id] = state
		if !state.Online {
			e.offlineSince[id] = now
		}
	}
}

// forgetDeviceLocked cancels state derived from membership that no longer
// exists. Rules themselves stay stored, matching inventory's documented
// behavior, but they cannot fire until the device is added and observed again.
func (e *Engine) forgetDeviceLocked(deviceID string) {
	delete(e.latestStates, deviceID)
	delete(e.offlineSince, deviceID)
	for _, rule := range e.state.Items {
		if trigger := rule.Trigger.Device; trigger != nil && trigger.DeviceID == deviceID {
			if timer := e.stableTimers[rule.ID]; timer != nil {
				timer.Stop()
				delete(e.stableTimers, rule.ID)
			}
		}
		if trigger := rule.Trigger.Offline; trigger != nil && trigger.DeviceID == deviceID {
			delete(e.offlineFired, rule.ID)
		}
	}
}

func (e *Engine) resyncStates() {
	states := e.readStates()
	now := time.Now()
	e.mu.Lock()
	for id := range e.latestStates {
		if _, live := states[id]; !live {
			e.forgetDeviceLocked(id)
		}
	}
	for id := range e.offlineSince {
		if _, live := states[id]; !live {
			e.forgetDeviceLocked(id)
		}
	}
	// An overflow lost the transition history, so the current reachability is a
	// fresh baseline. Preserve state-trigger edge handling below, but never
	// manufacture a recovery from a partial event stream.
	for id, state := range states {
		delete(e.offlineSince, id)
		if !state.Online {
			e.offlineSince[id] = now
		}
	}
	e.mu.Unlock()
	for id, state := range states {
		e.handleState(id, state)
	}
}

// handleState is the edge detector for every device-state trigger: power, and
// the numeric metrics that compare one reported value. A rule fires when its
// device stops matching and starts matching, never while it merely keeps
// matching, so a value that stays above a threshold produces one run and not a
// stream of them.
func (e *Engine) handleState(deviceID string, state device.State) {
	e.mu.Lock()
	revision := e.state.Revision
	previous, known := e.latestStates[deviceID]
	e.latestStates[deviceID] = state
	offlineFor, recovered := e.trackReachabilityLocked(deviceID, state, time.Now())

	rules := make([]Rule, 0)
	onlineRules := make([]Rule, 0)
	for _, rule := range e.state.Items {
		trigger := rule.Trigger.Device
		if !rule.Enabled || trigger == nil || trigger.DeviceID != deviceID {
			continue
		}
		if !trigger.matches(state) {
			// No longer matching: a settle timer that was counting down toward
			// this rule is now measuring something that stopped being true.
			if timer := e.stableTimers[rule.ID]; timer != nil {
				timer.Stop()
				delete(e.stableTimers, rule.ID)
			}
			continue
		}
		if !known || trigger.matches(previous) {
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
		want := *trigger
		e.stableTimers[rule.ID] = time.AfterFunc(time.Duration(trigger.StableSeconds)*time.Second, func() {
			e.mu.Lock()
			delete(e.stableTimers, ruleID)
			still := want.matches(e.latestStates[deviceID])
			ctx := e.ctx
			e.mu.Unlock()
			if still && ctx != nil && ctx.Err() == nil {
				_, _ = e.enqueueAtRevision(ruleID, "device", revision)
			}
		})
	}
	if recovered {
		for _, rule := range e.state.Items {
			trigger := rule.Trigger.Online
			if rule.Enabled && trigger != nil && trigger.DeviceID == deviceID && trigger.matches(offlineFor) {
				onlineRules = append(onlineRules, rule)
			}
		}
	}
	e.mu.Unlock()
	for _, rule := range rules {
		_, _ = e.enqueueAtRevision(rule.ID, "device", revision)
	}
	for _, rule := range onlineRules {
		_, _ = e.enqueueAtRevision(rule.ID, "online", revision)
	}
}

// trackReachabilityLocked maintains the clock behind offline and online
// recovery triggers. It returns the just-finished observed outage exactly once.
// The caller must hold e.mu.
func (e *Engine) trackReachabilityLocked(deviceID string, state device.State, now time.Time) (time.Duration, bool) {
	if state.Online {
		since, recovered := e.offlineSince[deviceID]
		delete(e.offlineSince, deviceID)
		// Coming back is what rearms the rules watching this device; without it
		// a device that flaps would only ever fire once per process.
		for _, rule := range e.state.Items {
			if trigger := rule.Trigger.Offline; trigger != nil && trigger.DeviceID == deviceID {
				delete(e.offlineFired, rule.ID)
			}
		}
		if recovered {
			return now.Sub(since), true
		}
		return 0, false
	}
	if _, counting := e.offlineSince[deviceID]; !counting {
		e.offlineSince[deviceID] = now
	}
	return 0, false
}

// evaluateOffline runs on the minute tick and fires the rules whose device has
// now been unreachable for long enough. Each fires once per episode.
func (e *Engine) evaluateOffline(now time.Time) {
	e.mu.Lock()
	revision := e.state.Revision
	rules := make([]Rule, 0)
	for _, rule := range e.state.Items {
		trigger := rule.Trigger.Offline
		if !rule.Enabled || trigger == nil || e.offlineFired[rule.ID] {
			continue
		}
		since, offline := e.offlineSince[trigger.DeviceID]
		if !offline || now.Sub(since) < time.Duration(trigger.Minutes)*time.Minute {
			continue
		}
		e.offlineFired[rule.ID] = true
		rules = append(rules, rule)
	}
	e.mu.Unlock()
	for _, rule := range rules {
		_, _ = e.enqueueAtRevision(rule.ID, "offline", revision)
	}
}

// watchedMACs returns the normalized MACs the enabled presence rules watch.
// Only these are ever remembered, so a busy LAN cannot grow the presence map.
func watchedMACs(rules []Rule) map[string]struct{} {
	watched := make(map[string]struct{})
	for _, rule := range rules {
		trigger := rule.Trigger.Presence
		if !rule.Enabled || trigger == nil {
			continue
		}
		if mac, err := resolver.NormalizeMAC(trigger.MAC); err == nil {
			watched[mac] = struct{}{}
		}
	}
	return watched
}

// syncPresence reads the neighbour table once and turns it into edges for the
// MACs presence rules are watching.
func (e *Engine) syncPresence() {
	if e.presence == nil {
		return
	}
	e.mu.RLock()
	watched := watchedMACs(e.state.Items)
	e.mu.RUnlock()
	if len(watched) == 0 {
		return
	}

	seen, err := e.presence()
	if err != nil {
		e.log.Debug("could not read LAN presence", "err", err)
		return
	}

	e.mu.Lock()
	revision := e.state.Revision
	changed := make(map[string]bool, len(watched))
	for mac := range watched {
		present := seen[mac]
		previous, known := e.latestPresence[mac]
		e.latestPresence[mac] = present
		if known && previous != present {
			changed[mac] = present
		}
	}

	rules := make([]Rule, 0)
	for _, rule := range e.state.Items {
		trigger := rule.Trigger.Presence
		if !rule.Enabled || trigger == nil {
			continue
		}
		mac, err := resolver.NormalizeMAC(trigger.MAC)
		if err != nil {
			continue
		}
		if e.latestPresence[mac] != trigger.Present {
			if timer := e.stableTimers[rule.ID]; timer != nil {
				timer.Stop()
				delete(e.stableTimers, rule.ID)
			}
			continue
		}
		if _, edge := changed[mac]; !edge {
			continue
		}
		if trigger.StableSeconds == 0 {
			rules = append(rules, rule)
			continue
		}
		if timer := e.stableTimers[rule.ID]; timer != nil {
			timer.Stop()
		}
		ruleID, want := rule.ID, trigger.Present
		e.stableTimers[rule.ID] = time.AfterFunc(time.Duration(trigger.StableSeconds)*time.Second, func() {
			e.mu.Lock()
			delete(e.stableTimers, ruleID)
			still := e.latestPresence[mac] == want
			ctx := e.ctx
			e.mu.Unlock()
			if still && ctx != nil && ctx.Err() == nil {
				_, _ = e.enqueueAtRevision(ruleID, "presence", revision)
			}
		})
	}
	e.mu.Unlock()
	for _, rule := range rules {
		_, _ = e.enqueueAtRevision(rule.ID, "presence", revision)
	}
}

func (e *Engine) evaluateSchedules(now time.Time) {
	e.mu.Lock()
	revision := e.state.Revision
	type dueStep struct {
		ruleID string
		offset int
	}
	due := make([]dueStep, 0)
	for _, rule := range e.state.Items {
		schedule := rule.Trigger.Schedule
		if !rule.Enabled || schedule == nil {
			continue
		}
		local := now.UTC().Add(time.Duration(schedule.UTCOffsetMinutes) * time.Minute)
		for _, offset := range actionOffsets(rule.Actions) {
			origin := local.Add(-time.Duration(offset) * time.Minute)
			minuteKey := origin.Format("2006-01-02 15:04")
			key := fmt.Sprintf("%s:%d", rule.ID, offset)
			if origin.Format("15:04") != schedule.Time || !containsDay(schedule.Weekdays, int(origin.Weekday())) || e.lastSchedule[key] == minuteKey {
				continue
			}
			e.lastSchedule[key] = minuteKey
			due = append(due, dueStep{ruleID: rule.ID, offset: offset})
		}
	}
	e.mu.Unlock()
	for _, step := range due {
		_, _ = e.enqueueScheduleAtRevision(step.ruleID, step.offset, revision)
	}
}

func actionOffsets(actions []Action) []int {
	seen := make(map[int]bool)
	offsets := make([]int, 0, len(actions))
	for _, action := range actions {
		if seen[action.OffsetMinutes] {
			continue
		}
		seen[action.OffsetMinutes] = true
		offsets = append(offsets, action.OffsetMinutes)
	}
	return offsets
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
