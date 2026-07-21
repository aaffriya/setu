package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"setu/internal/control"
	"setu/internal/manager"
)

// handleListDevices returns all devices with capabilities and current state. A
// refresh=true request performs a one-shot hardware poll first; successfully
// read states are overlaid on the cached snapshot so the response cannot race
// the manager's asynchronous event consumer.
func (s *Server) handleListDevices(w http.ResponseWriter, r *http.Request) {
	if s.poller == nil {
		writeJSON(w, http.StatusOK, s.mgr.Snapshot())
		return
	}
	if r.URL.Query().Get("refresh") != "true" {
		s.poller.Activity()
		writeJSON(w, http.StatusOK, s.mgr.Snapshot())
		return
	}

	states, err := s.poller.Refresh(r.Context())
	if err != nil {
		writeError(w, http.StatusGatewayTimeout, "device refresh timed out")
		return
	}
	views := s.mgr.Snapshot()
	for i := range views {
		if state, ok := states[views[i].ID]; ok {
			views[i].State = state
		}
	}
	writeJSON(w, http.StatusOK, views)
}

// handleActivity keeps the active poll cadence warm without polling hardware.
// The browser throttles these tiny hints, and Poller coalesces bursts again.
func (s *Server) handleActivity(w http.ResponseWriter, _ *http.Request) {
	if s.poller != nil {
		s.poller.Activity()
	}
	w.WriteHeader(http.StatusNoContent)
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
	dev, ok := s.mgr.Device(id)
	if !ok {
		writeError(w, http.StatusNotFound, "unknown device")
		return
	}

	var req commandRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := control.Execute(dev, req); err != nil {
		// Distinguish client errors (unsupported capability / bad input) from
		// device or I/O failures (upstream).
		var ce control.InputError
		if errors.As(err, &ce) {
			writeError(w, http.StatusBadRequest, ce.Message)
			return
		}
		s.log.Warn("command failed", "device", id, "action", req.Action, "err", err)
		writeError(w, http.StatusBadGateway, "device command failed")
		return
	}
	// Return the device's fresh view so the client can reconcile its optimistic
	// update immediately; the WebSocket will also broadcast the change.
	writeJSON(w, http.StatusOK, manager.ViewOf(dev))
}
