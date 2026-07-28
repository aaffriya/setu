package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"setu/internal/automation"
)

const automationBodyLimit = automation.MaxStateBytes
const webhookReadTimeout = 10 * time.Second

// namesOnlyGrantedDevices reports whether every device a rule mentions directly
// was granted to this account.
//
// Automations are how a device gets controlled with nobody touching it, so an
// account able to write a rule for a device it cannot see would have its device
// restriction only on paper.
func namesOnlyGrantedDevices(principal Principal, rule automation.Rule) bool {
	if trigger := rule.Trigger.Device; trigger != nil && !principal.CanSee(trigger.DeviceID) {
		return false
	}
	if trigger := rule.Trigger.Offline; trigger != nil && !principal.CanSee(trigger.DeviceID) {
		return false
	}
	if trigger := rule.Trigger.Online; trigger != nil && !principal.CanSee(trigger.DeviceID) {
		return false
	}
	for _, condition := range rule.Conditions {
		if !principal.CanSee(condition.DeviceID) {
			return false
		}
	}
	for _, action := range rule.Actions {
		for _, condition := range action.When {
			if !principal.CanSee(condition.DeviceID) {
				return false
			}
		}
		// A nested call names no device of its own; it is resolved below.
		if action.Action == automation.ActionAutomation {
			continue
		}
		if !principal.CanSee(action.DeviceID) {
			return false
		}
	}
	return true
}

// ownedRules reports, for one rule set, which rules this account may see and act
// on.
//
// Naming only granted devices is necessary but not sufficient: running a rule
// runs the actions of every rule it calls, so calling a rule is exactly as
// powerful as owning it. A rule that reaches something this account does not own
// — including an id it was never shown, and therefore cannot name by accident —
// is not owned either. The cascade repeats until stable, so a chain of calls is
// resolved end to end.
//
// The administrator owns all of them, which is the ordinary case.
func ownedRules(principal Principal, items []automation.Rule) map[string]bool {
	owned := make(map[string]bool, len(items))
	if principal.Admin {
		for _, rule := range items {
			owned[rule.ID] = true
		}
		return owned
	}
	for _, rule := range items {
		owned[rule.ID] = namesOnlyGrantedDevices(principal, rule)
	}
	for changed := true; changed; {
		changed = false
		for _, rule := range items {
			if !owned[rule.ID] {
				continue
			}
			for _, action := range rule.Actions {
				// An unknown callee stays unowned: it is either missing, which
				// validation refuses anyway, or a rule this account cannot see.
				if action.Action == automation.ActionAutomation && !owned[action.AutomationID] {
					owned[rule.ID] = false
					changed = true
					break
				}
			}
		}
	}
	return owned
}

// handleAutomations returns the rules this account owns. A restricted account
// never learns that the others exist, and its run history is filtered to match.
func (s *Server) handleAutomations(w http.ResponseWriter, r *http.Request) {
	snapshot := s.automation.Snapshot()
	principal := principalOf(r)
	if principal.Admin {
		writeJSON(w, http.StatusOK, snapshot)
		return
	}
	owned := ownedRules(principal, snapshot.Items)
	mine := make([]automation.Rule, 0, len(snapshot.Items))
	for _, rule := range snapshot.Items {
		if owned[rule.ID] {
			mine = append(mine, rule)
		}
	}
	runs := make([]automation.Run, 0, len(snapshot.Runs))
	for _, run := range snapshot.Runs {
		if owned[run.RuleID] {
			runs = append(runs, run)
		}
	}
	snapshot.Items = mine
	snapshot.Runs = runs
	writeJSON(w, http.StatusOK, snapshot)
}

func (s *Server) handleAutomationExport(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.automation.Export())
}

func (s *Server) handleReplaceAutomations(w http.ResponseWriter, r *http.Request) {
	var state automation.State
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, automationBodyLimit))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&state); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "automation data is too large")
			return
		}
		writeError(w, http.StatusBadRequest, "invalid automation data")
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "automation data has trailing content")
		return
	}
	if principal := principalOf(r); !principal.Admin {
		stored := s.automation.Snapshot()
		merged, err := mergeRestrictedRules(principal, stored.Items, state.Items)
		if err != nil {
			writeError(w, http.StatusForbidden, err.Error())
			return
		}
		state.Items = merged
		// Pausing is installation-wide: it stops rules this account was never
		// shown. Whatever the administrator set stays set; a restricted account
		// disables its own rules individually instead.
		state.Paused = stored.Paused
	}
	update, err := s.automation.Replace(state)
	if err == nil {
		writeJSON(w, http.StatusOK, update)
		return
	}
	if errors.Is(err, automation.ErrRevision) {
		writeError(w, http.StatusConflict, "automations changed; reload and try again")
		return
	}
	var invalid automation.ValidationError
	if errors.As(err, &invalid) {
		writeError(w, http.StatusBadRequest, invalid.Error())
		return
	}
	s.log.Error("save automations failed", "err", err)
	writeError(w, http.StatusInternalServerError, "could not save automations")
}

// mergeRestrictedRules turns what a restricted account submitted into the whole
// rule set the engine expects.
//
// That account was only ever shown its own rules, so a straight replace would
// delete everyone else's. Instead the stored rules it does not own are carried
// through untouched, its own are taken from the request, and anything it may not
// have — a rule reaching a device it was never granted, or an id already used by
// a rule it cannot see — is refused outright.
func mergeRestrictedRules(principal Principal, stored, submitted []automation.Rule) ([]automation.Rule, error) {
	storedOwned := ownedRules(principal, stored)
	incoming := make(map[string]automation.Rule, len(submitted))
	for _, rule := range submitted {
		incoming[rule.ID] = rule
	}

	// Walk the stored order so preserved rules keep their place and a restricted
	// save does not reshuffle the administrator's list.
	merged := make([]automation.Rule, 0, len(stored)+len(submitted))
	kept := make(map[string]struct{}, len(stored))
	for _, rule := range stored {
		kept[rule.ID] = struct{}{}
		if !storedOwned[rule.ID] {
			if _, claimed := incoming[rule.ID]; claimed {
				return nil, errors.New("automation id " + rule.ID + " is already in use")
			}
			merged = append(merged, rule)
			continue
		}
		if edited, ok := incoming[rule.ID]; ok {
			merged = append(merged, edited)
		}
		// Otherwise this account owned the rule and removed it, which is theirs
		// to do: leave it out.
	}
	for _, rule := range submitted {
		if _, existing := kept[rule.ID]; !existing {
			merged = append(merged, rule)
		}
	}

	// Ownership is decided on the result, not the request: a submitted rule may
	// call another submitted rule, and a rule that reaches anything this account
	// does not own has to be refused here rather than run later.
	mergedOwned := ownedRules(principal, merged)
	for _, rule := range submitted {
		if !mergedOwned[rule.ID] {
			return nil, errors.New("automation " + rule.Name + " uses a device or automation you do not have access to")
		}
	}
	return merged, nil
}

// owns reports whether this account may act on one stored rule by id.
func (s *Server) owns(principal Principal, id string) bool {
	if principal.Admin {
		return true
	}
	items := s.automation.Snapshot().Items
	for _, rule := range items {
		if rule.ID == id {
			return ownedRules(principal, items)[id]
		}
	}
	// An id that does not exist is left to the engine, so a 404 comes from the
	// same place for every caller rather than this code disclosing which ids
	// exist by refusing some of them differently.
	return true
}

func (s *Server) handleRunAutomation(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.owns(principalOf(r), id) {
		writeError(w, http.StatusForbidden, "this automation uses a device you do not have access to")
		return
	}
	result, err := s.automation.RunNow(id)
	writeTriggerResult(w, result, err)
}

func (s *Server) handleRotateWebhook(w http.ResponseWriter, r *http.Request) {
	if !s.owns(principalOf(r), r.PathValue("id")) {
		writeError(w, http.StatusForbidden, "this automation uses a device you do not have access to")
		return
	}
	token, state, err := s.automation.RotateWebhookToken(r.PathValue("id"))
	if errors.Is(err, automation.ErrNotFound) {
		writeError(w, http.StatusNotFound, "webhook automation not found")
		return
	}
	if err != nil {
		s.log.Error("rotate webhook token failed", "automation", r.PathValue("id"), "err", err)
		writeError(w, http.StatusInternalServerError, "could not rotate webhook token")
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Token string           `json:"token"`
		State automation.State `json:"state"`
	}{Token: token, State: state})
}

// handleAutomationWebhook is intentionally outside the admin auth middleware.
// It accepts only the per-rule bearer token and can run only that rule's saved
// actions. The body is bounded and discarded; payloads never become commands.
func (s *Server) handleAutomationWebhook(w http.ResponseWriter, r *http.Request) {
	header := r.Header.Get("Authorization")
	token, ok := strings.CutPrefix(header, "Bearer ")
	if !ok || token == "" {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if err := s.automation.AuthenticateWebhook(r.PathValue("id"), token); err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	idempotencyKey := r.Header.Get("Idempotency-Key")
	if len(idempotencyKey) > 64 {
		writeError(w, http.StatusBadRequest, "idempotency key is too long")
		return
	}
	if r.ContentLength > 4096 {
		writeError(w, http.StatusRequestEntityTooLarge, "webhook body is too large")
		return
	}
	// The payload is ignored, but a valid caller still cannot hold this small
	// server connection open indefinitely while dribbling it in.
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(webhookReadTimeout))
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if _, err := io.Copy(io.Discard, r.Body); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "webhook body is too large")
		} else {
			writeError(w, http.StatusRequestTimeout, "webhook body was not received in time")
		}
		return
	}
	result, err := s.automation.TriggerWebhook(r.PathValue("id"), token, idempotencyKey)
	writeTriggerResult(w, result, err)
}

func writeTriggerResult(w http.ResponseWriter, result automation.TriggerResult, err error) {
	switch {
	case err == nil:
		writeJSON(w, http.StatusAccepted, result)
	case errors.Is(err, automation.ErrUnauthorized):
		writeError(w, http.StatusUnauthorized, "unauthorized")
	case errors.Is(err, automation.ErrNotFound):
		writeError(w, http.StatusNotFound, "automation not found")
	case errors.Is(err, automation.ErrRateLimited):
		w.Header().Set("Retry-After", "60")
		writeError(w, http.StatusTooManyRequests, "webhook rate limit reached")
	case errors.Is(err, automation.ErrQueueFull):
		w.Header().Set("Retry-After", "2")
		writeError(w, http.StatusServiceUnavailable, "automation queue is busy")
	case errors.Is(err, automation.ErrPaused), errors.Is(err, automation.ErrDisabled):
		writeError(w, http.StatusConflict, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "could not start automation")
	}
}
