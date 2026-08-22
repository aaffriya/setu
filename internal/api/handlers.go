package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"setu/internal/control"
	"setu/internal/manager"
)

// handleListDevices returns all devices with capabilities and current state. A
// refresh=true request performs a one-shot hardware poll first and then reads
// the cache, which the poll has already updated.
//
// The list is the caller's own: an account limited to a few devices never
// receives the others, so nothing it cannot use ever reaches the browser.
func (s *Server) handleListDevices(w http.ResponseWriter, r *http.Request) {
	principal := principalOf(r)
	if s.poller == nil {
		writeJSON(w, http.StatusOK, granted(principal, s.mgr.Snapshot()))
		return
	}
	if r.URL.Query().Get("refresh") != "true" {
		s.poller.Activity()
		writeJSON(w, http.StatusOK, granted(principal, s.mgr.Snapshot()))
		return
	}

	if err := s.poller.Refresh(r.Context()); err != nil {
		writeError(w, http.StatusGatewayTimeout, "device refresh timed out")
		return
	}
	// Snapshot after the poll rather than overlaying its result: Manager.Poll
	// and Manager.Command both write the read model synchronously, so the cache
	// already holds everything this poll read — plus any command that landed
	// while it ran, which is newer. Overlaying would put the older reading back
	// and visibly revert the change the user just made. A poll cycle's result is
	// also reused for a few seconds, so it can be older than the request itself.
	writeJSON(w, http.StatusOK, granted(principal, s.mgr.Snapshot()))
}

// granted keeps only the devices this caller may see. It always returns a
// non-nil slice so the API emits [] rather than null.
func granted(principal Principal, views []manager.DeviceView) []manager.DeviceView {
	if principal.Admin {
		return views
	}
	out := make([]manager.DeviceView, 0, len(views))
	for _, view := range views {
		if principal.CanSee(view.ID) {
			out = append(out, view)
		}
	}
	return out
}

// reachable answers the two questions every route that talks to hardware asks:
// is this device live, and may this caller use it? A device that exists but was
// not granted is reported as forbidden rather than missing — the administrator
// has to be able to tell "I never shared that" from "that is gone".
func (s *Server) reachable(w http.ResponseWriter, r *http.Request, id string) bool {
	if _, ok := s.mgr.Device(id); !ok {
		writeError(w, http.StatusNotFound, "unknown device")
		return false
	}
	return s.permitted(w, r, id)
}

// manageable is reachable's counterpart for the routes that edit the stored
// device list instead of talking to hardware, so it asks the inventory rather
// than the manager. A spec this build cannot construct is kept, not deleted
// (see inventory.New), and asking the manager here would make it both
// unfixable and undeletable through the API.
func (s *Server) manageable(w http.ResponseWriter, r *http.Request, id string) bool {
	if !s.inventory.Has(id) {
		writeError(w, http.StatusNotFound, "unknown device")
		return false
	}
	return s.permitted(w, r, id)
}

func (s *Server) permitted(w http.ResponseWriter, r *http.Request, id string) bool {
	if !principalOf(r).CanSee(id) {
		writeError(w, http.StatusForbidden, "you do not have access to this device")
		return false
	}
	return true
}

// affordable reports whether this caller may spend one more unit of hardware
// work on a device. Commands and single-device refreshes share the budget: both
// reach the same hardware, so a client looping on either hammers it equally
// (see ratelimit.go).
func (s *Server) affordable(w http.ResponseWriter, r *http.Request, id string) bool {
	return s.withinBudget(w, limiterKey(principalOf(r), id), "too many requests for this device; slow down")
}

// affordableRun is the same budget for running an automation by hand. It is
// keyed by the rule rather than a device because the rule is what a loop here
// repeats, and one run may touch several devices at once.
func (s *Server) affordableRun(w http.ResponseWriter, r *http.Request, ruleID string) bool {
	return s.withinBudget(w, automationKey(principalOf(r), ruleID), "too many runs for this automation; slow down")
}

func (s *Server) withinBudget(w http.ResponseWriter, key, message string) bool {
	if s.commands.allow(key) {
		return true
	}
	w.Header().Set("Retry-After", "1")
	writeError(w, http.StatusTooManyRequests, message)
	return false
}

// handleHealth is the public liveness probe. It is deliberately the only
// unauthenticated JSON route that is not part of the app: it answers whether
// this process is serving HTTP and nothing else — no device count, no version,
// no configuration — so exposing it discloses nothing about the installation.
//
// HEAD needs no branch here: net/http sends the headers and discards the body.
func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, struct {
		Status string `json:"status"`
	}{Status: "ok"})
}

// handleActivity keeps the active poll cadence warm without polling hardware.
// The browser throttles these tiny hints, and Poller coalesces bursts again.
func (s *Server) handleActivity(w http.ResponseWriter, _ *http.Request) {
	if s.poller != nil {
		s.poller.Activity()
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleDiagnostics returns the latest bounded operation summary from RAM. It
// never contacts hardware; explicit per-device refresh owns that cost. Like the
// device list, it covers only the devices this caller was granted.
func (s *Server) handleDiagnostics(w http.ResponseWriter, r *http.Request) {
	if s.poller != nil {
		s.poller.Activity()
	}
	principal := principalOf(r)
	records := s.mgr.Diagnostics()
	if principal.Admin {
		writeJSON(w, http.StatusOK, records)
		return
	}
	out := make([]manager.DeviceDiagnostics, 0, len(records))
	for _, record := range records {
		if principal.CanSee(record.ID) {
			out = append(out, record)
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// handleRefreshDevice performs one serialized hardware read for a single
// device, avoiding the all-device cost when investigating one card.
func (s *Server) handleRefreshDevice(w http.ResponseWriter, r *http.Request) {
	if s.poller != nil {
		s.poller.Activity()
	}
	id := r.PathValue("id")
	if !s.reachable(w, r, id) {
		return
	}
	if !s.affordable(w, r, id) {
		return
	}
	_, pollable, _, err := s.mgr.Poll(id)
	if !pollable {
		writeError(w, http.StatusBadRequest, "device does not support status refresh")
		return
	}
	if err != nil {
		s.log.Warn("device refresh failed", "device", id, "err", err)
		if view, ok := s.mgr.View(id); ok {
			writeDeviceError(w, http.StatusBadGateway, "device refresh failed", view)
		} else {
			writeError(w, http.StatusBadGateway, "device refresh failed")
		}
		return
	}
	view, ok := s.mgr.View(id)
	if !ok {
		writeError(w, http.StatusNotFound, "unknown device")
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// commandRequest is the uniform, device-agnostic command body, e.g.
//
//	{"action":"on"}
//	{"action":"set_brightness","value":70}
//	{"action":"set_color","value":{"r":255,"g":120,"b":0}}
type commandRequest = control.Request

// handleCommand routes a uniform command to the right capability on a device.
// Capability support is discovered with type assertions, so a device lacking a
// capability yields a clean 400 rather than a panic.
func (s *Server) handleCommand(w http.ResponseWriter, r *http.Request) {
	if s.poller != nil {
		s.poller.Activity()
	}
	id := r.PathValue("id")
	if !s.reachable(w, r, id) {
		return
	}
	// Checked before the body is read: a client stuck in a retry loop should cost
	// this server as little as possible.
	if !s.affordable(w, r, id) {
		return
	}
	var req commandRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	view, found, err := s.mgr.Command(id, req)
	if !found {
		writeError(w, http.StatusNotFound, "unknown device")
		return
	}
	if err != nil {
		// Distinguish client errors (unsupported capability / bad input) from
		// device or I/O failures (upstream).
		var ce control.InputError
		if errors.As(err, &ce) {
			writeError(w, http.StatusBadRequest, ce.Message)
			return
		}
		s.log.Warn("command failed", "device", id, "action", req.Action, "err", err)
		if view.ID != "" {
			writeDeviceError(w, http.StatusBadGateway, "device command failed", view)
		} else {
			writeError(w, http.StatusBadGateway, "device command failed")
		}
		return
	}
	// Return the device's fresh view so the client can reconcile its optimistic
	// update immediately; the WebSocket will also broadcast the change.
	writeJSON(w, http.StatusOK, view)
}
