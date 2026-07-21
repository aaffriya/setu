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

func (s *Server) handleAutomations(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.automation.Snapshot())
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

func (s *Server) handleRunAutomation(w http.ResponseWriter, r *http.Request) {
	result, err := s.automation.RunNow(r.PathValue("id"))
	writeTriggerResult(w, result, err)
}

func (s *Server) handleRotateWebhook(w http.ResponseWriter, r *http.Request) {
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
