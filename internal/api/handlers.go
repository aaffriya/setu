package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"setu/internal/device"
	"setu/internal/manager"
)

// handleListDevices returns all devices with capabilities and current state. It
// returns an empty list (not null) until devices are configured.
func (s *Server) handleListDevices(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.mgr.Snapshot())
}

// commandRequest is the uniform, device-agnostic command body, e.g.
//
//	{"action":"on"}
//	{"action":"set_brightness","value":70}
//	{"action":"set_color","value":{"r":255,"g":120,"b":0}}
type commandRequest struct {
	Action string          `json:"action"`
	Value  json.RawMessage `json:"value"`
}

// handleCommand routes a uniform command to the right capability on a device.
// Capability support is discovered with type assertions, so a device lacking a
// capability yields a clean 400 rather than a panic.
func (s *Server) handleCommand(w http.ResponseWriter, r *http.Request) {
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

	if err := s.dispatch(dev, req); err != nil {
		// Distinguish client errors (unsupported capability / bad input) from
		// device or I/O failures (upstream).
		var ce clientError
		if errors.As(err, &ce) {
			writeError(w, http.StatusBadRequest, ce.msg)
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

// clientError marks an error as the caller's fault (unsupported capability or
// bad input) → HTTP 400, as opposed to a device/I/O failure → 502.
type clientError struct{ msg string }

func (e clientError) Error() string { return e.msg }

func badRequest(msg string) error { return clientError{msg: msg} }

// dispatch maps a command action to a capability method via type assertions.
// This is the device-agnostic seam: new capabilities add a case here and a new
// interface in package device, without touching devices that lack them.
func (s *Server) dispatch(dev device.Device, req commandRequest) error {
	switch req.Action {
	case "on", "off":
		sw, ok := dev.(device.Switchable)
		if !ok {
			return badRequest("device does not support on/off")
		}
		if req.Action == "on" {
			return sw.On()
		}
		return sw.Off()

	case "set_brightness":
		d, ok := dev.(device.Dimmable)
		if !ok {
			return badRequest("device does not support brightness")
		}
		var pct int
		if err := json.Unmarshal(req.Value, &pct); err != nil {
			return badRequest("set_brightness requires an integer value 0–100")
		}
		if pct < 0 || pct > 100 {
			return badRequest("brightness must be 0–100")
		}
		return d.SetBrightness(pct)

	case "set_color":
		d, ok := dev.(device.ColorControl)
		if !ok {
			return badRequest("device does not support color")
		}
		var c device.Color
		if err := json.Unmarshal(req.Value, &c); err != nil {
			return badRequest(`set_color requires a {"r","g","b"} value`)
		}
		return d.SetColor(c)

	case "set_color_temp":
		d, ok := dev.(device.ColorTempControl)
		if !ok {
			return badRequest("device does not support color temperature")
		}
		var kelvin int
		if err := json.Unmarshal(req.Value, &kelvin); err != nil {
			return badRequest("set_color_temp requires a Kelvin integer (e.g. 2700)")
		}
		return d.SetColorTemp(kelvin)

	case "set_scene":
		d, ok := dev.(device.SceneControl)
		if !ok {
			return badRequest("device does not support scenes")
		}
		var id int
		if err := json.Unmarshal(req.Value, &id); err != nil {
			return badRequest("set_scene requires a scene id integer")
		}
		return d.SetScene(id)

	case "set_scene_speed":
		d, ok := dev.(device.SceneControl)
		if !ok {
			return badRequest("device does not support scenes")
		}
		var speed int
		if err := json.Unmarshal(req.Value, &speed); err != nil {
			return badRequest("set_scene_speed requires an integer (10–200)")
		}
		return d.SetSceneSpeed(speed)

	case "volume_up", "volume_down", "mute":
		v, ok := dev.(device.Volume)
		if !ok {
			return badRequest("device does not support volume")
		}
		switch req.Action {
		case "volume_up":
			return v.VolumeUp()
		case "volume_down":
			return v.VolumeDown()
		default:
			return v.ToggleMute()
		}

	case "key":
		kc, ok := dev.(device.KeyControl)
		if !ok {
			return badRequest("device does not support remote keys")
		}
		var key string
		if err := json.Unmarshal(req.Value, &key); err != nil {
			return badRequest(`key requires a string value like "KEY_HOME"`)
		}
		return kc.SendKey(key)

	case "launch_app":
		ac, ok := dev.(device.AppControl)
		if !ok {
			return badRequest("device does not support apps")
		}
		var id string
		if err := json.Unmarshal(req.Value, &id); err != nil {
			return badRequest(`launch_app requires an app id string`)
		}
		return ac.LaunchApp(id)

	default:
		return badRequest("unknown action: " + req.Action)
	}
}
