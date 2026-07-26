package api

import (
	"encoding/json"
	"net/http"

	"setu/internal/config"
	"setu/internal/inventory"
)

const (
	deviceBodyLimit     = 4 << 10  // one device spec
	deviceListBodyLimit = 64 << 10 // the whole list, for a restore
)

// deviceList is the portable form of the device inventory: what a backup
// carries and what a restore replays. Same shape in both directions.
type deviceList struct {
	Version int                 `json:"version"`
	Items   []config.DeviceSpec `json:"items"`
}

// handleDeviceTypes lists the (brand, model) pairs this build can drive. The UI
// needs it for the manual add form — hardware that answers no scan, like a
// Wake-on-LAN target — so the catalog comes from the registered brands rather
// than a list duplicated in the frontend.
func (s *Server) handleDeviceTypes(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.inventory.Types())
}

// handleAddDevice adds one device and brings it online. The id may be omitted;
// the inventory derives a stable one from the brand and MAC.
func (s *Server) handleAddDevice(w http.ResponseWriter, r *http.Request) {
	var spec config.DeviceSpec
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, deviceBodyLimit)).Decode(&spec); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	added, err := s.inventory.Add(spec)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.log.Info("device added", "device", added.ID, "brand", added.Brand, "model", added.Model)

	// Read the new device once so its card opens with real state instead of
	// waiting for the next poll cycle. A device that does not answer is still
	// added — that is what the diagnostics panel is for.
	if _, pollable, _, err := s.mgr.Poll(added.ID); pollable && err != nil {
		s.log.Warn("new device did not answer its first poll", "device", added.ID, "err", err)
	}
	view, ok := s.mgr.View(added.ID)
	if !ok {
		writeError(w, http.StatusInternalServerError, "device was stored but could not be started")
		return
	}
	writeJSON(w, http.StatusCreated, view)
}

// handleUpdateDevice edits a device's labels (its name, and the friendly series
// shown under it). Identity — brand, model, MAC — is not editable: that would
// be a different device, so it is a remove and an add.
//
// It is a real PATCH: an omitted field is left as it is. The UI edits name and
// series in two inputs, and saving one must not carry a stale copy of the other.
func (s *Server) handleUpdateDevice(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name   *string `json:"name"`
		Series *string `json:"series"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, deviceBodyLimit)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	id := r.PathValue("id")
	if _, err := s.inventory.Update(id, inventory.Labels{Name: body.Name, Series: body.Series}); err != nil {
		writeInventoryError(w, err)
		return
	}
	view, ok := s.mgr.View(id)
	if !ok {
		writeError(w, http.StatusNotFound, "unknown device")
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// handleDeleteDevice removes a device from the installation.
func (s *Server) handleDeleteDevice(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.inventory.Remove(id); err != nil {
		writeInventoryError(w, err)
		return
	}
	s.log.Info("device removed", "device", id)
	w.WriteHeader(http.StatusNoContent)
}

// handleExportDevices returns the stored specs — the backup form.
func (s *Server) handleExportDevices(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, deviceList{Version: deviceFormatVersion, Items: s.inventory.Specs()})
}

// handleReplaceDevices swaps the whole device list (restore). Everything is
// validated and built first, so a rejected list leaves the running devices
// untouched.
func (s *Server) handleReplaceDevices(w http.ResponseWriter, r *http.Request) {
	var body deviceList
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, deviceListBodyLimit)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.Version != deviceFormatVersion {
		writeError(w, http.StatusBadRequest, "unsupported device list version")
		return
	}
	stored, err := s.inventory.Replace(body.Items)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.log.Info("device list replaced", "count", len(stored))
	writeJSON(w, http.StatusOK, deviceList{Version: deviceFormatVersion, Items: stored})
}

// deviceFormatVersion is the schema version of the exported device list.
const deviceFormatVersion = 1

func writeInventoryError(w http.ResponseWriter, err error) {
	if inventory.IsNotFound(err) {
		writeError(w, http.StatusNotFound, "unknown device")
		return
	}
	writeError(w, http.StatusBadRequest, err.Error())
}
