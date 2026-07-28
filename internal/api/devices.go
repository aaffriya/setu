package api

import (
	"encoding/json"
	"io"
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

// handleDeviceTypes lists the categorized, labelled drivers this build can
// drive. The UI needs it for the manual add form — hardware that answers no
// scan, like a Wake-on-LAN target — so the catalog comes from the registered
// brands rather than a list duplicated in the frontend.
func (s *Server) handleDeviceTypes(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.inventory.Types())
}

// handleUnusableDevices lists the stored devices that are not running, so the
// device screen can show what would otherwise be invisible: such an entry is
// absent from the manager, so it appears in no device list, on no card, and in
// no picker. The reason rides along so the fix is obvious — rename a refused
// label, or remove the entry whose driver this build no longer has.
func (s *Server) handleUnusableDevices(w http.ResponseWriter, r *http.Request) {
	principal := principalOf(r)
	entries := s.inventory.Unusable()
	out := make([]inventory.Unusable, 0, len(entries))
	for _, entry := range entries {
		// Filtered like every other read: a broken device is still a device this
		// account was or was not given.
		if principal.CanSee(entry.ID) {
			out = append(out, entry)
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// handleAddDevice adds one device and brings it online. The id may be omitted;
// the inventory derives a stable one from the brand and MAC.
func (s *Server) handleAddDevice(w http.ResponseWriter, r *http.Request) {
	var spec config.DeviceSpec
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, deviceBodyLimit)).Decode(&spec); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	// A new device is granted to nobody — access is always explicit — but the
	// person who just added one clearly meant to use it, and would otherwise
	// have to ask the administrator to share back what they contributed. The
	// grant is part of the same write as the device, so an add that reports
	// success is never one its own author cannot see.
	added, err := s.inventory.Add(spec, principalOf(r).UserID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.log.Info("device added", "device", added.ID, "brand", added.Brand, "driver", added.Driver)

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
	s.publishDeviceInventoryChanged(false)
	writeJSON(w, http.StatusCreated, view)
}

// handleUpdateDevice edits a device's labels: its name, and the model shown
// under it. Identity — brand, driver, MAC — is not editable: that would be a
// different device, so it is a remove and an add.
//
// It is a real PATCH: an omitted field is left as it is. The UI edits the name
// and the model in two inputs, and saving one must not carry a stale copy of
// the other.
func (s *Server) handleUpdateDevice(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name  *string `json:"name"`
		Model *string `json:"model"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, deviceBodyLimit)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	id := r.PathValue("id")
	if !s.manageable(w, r, id) {
		return
	}
	if _, err := s.inventory.Update(id, inventory.Labels{Name: body.Name, Model: body.Model}); err != nil {
		writeInventoryError(w, err)
		return
	}
	view, ok := s.mgr.View(id)
	if !ok {
		writeError(w, http.StatusNotFound, "unknown device")
		return
	}
	s.publishDeviceInventoryChanged(false)
	writeJSON(w, http.StatusOK, view)
}

// handleDeleteDevice removes a device from the installation.
func (s *Server) handleDeleteDevice(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.manageable(w, r, id) {
		return
	}
	if err := s.inventory.Remove(id); err != nil {
		writeInventoryError(w, err)
		return
	}
	s.log.Info("device removed", "device", id)
	s.publishDeviceInventoryChanged(false)
	w.WriteHeader(http.StatusNoContent)
}

// handleExportDevices returns the stored specs — the backup form.
func (s *Server) handleExportDevices(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, deviceList{Version: deviceFormatVersion, Items: s.inventory.Specs()})
}

// handleReplaceDevices swaps the whole device list (restore). Everything is
// validated and built first, so a rejected list leaves the running devices
// untouched.
//
// A version-1 file still restores: a backup is a file someone keeps, and
// refusing yesterday's export because the vocabulary changed would lose the
// devices it was taken to protect.
func (s *Server) handleReplaceDevices(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, deviceListBodyLimit))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	var items []config.DeviceSpec
	var version struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(raw, &version); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	switch version.Version {
	case deviceFormatVersion:
		var body deviceList
		if err := json.Unmarshal(raw, &body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		items = body.Items
	case 1:
		var body struct {
			Items []config.LegacyDeviceSpec `json:"items"`
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		items = config.UpgradeDeviceSpecs(body.Items)
	default:
		writeError(w, http.StatusBadRequest, "unsupported device list version")
		return
	}

	stored, err := s.inventory.Replace(items)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.log.Info("device list replaced", "count", len(stored))
	s.publishDeviceInventoryChanged(true)
	writeJSON(w, http.StatusOK, deviceList{Version: deviceFormatVersion, Items: stored})
}

// deviceFormatVersion is the schema version of the exported device list. It
// tracks the state file's own version (internal/store), since both carry the
// same specs.
const deviceFormatVersion = 2

func writeInventoryError(w http.ResponseWriter, err error) {
	if inventory.IsNotFound(err) {
		writeError(w, http.StatusNotFound, "unknown device")
		return
	}
	writeError(w, http.StatusBadRequest, err.Error())
}
