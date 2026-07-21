// Package control validates and executes device-agnostic commands against the
// small capability interfaces implemented by each device.
package control

import (
	"encoding/json"

	"setu/internal/device"
)

// Request is the uniform command shape shared by the HTTP API, manual scenes,
// and the automation engine.
type Request struct {
	Action string          `json:"action"`
	Value  json.RawMessage `json:"value,omitempty"`
}

// InputError reports an unsupported capability or invalid command value. It is
// safe to return to a caller; all other errors come from device I/O.
type InputError struct{ Message string }

func (e InputError) Error() string { return e.Message }

func invalid(message string) error { return InputError{Message: message} }

// Validate checks a command without touching the device transport.
func Validate(dev device.Device, req Request) error {
	return apply(dev, req, false)
}

// Execute validates and then performs a command through the matching device
// capability. No brand-specific knowledge lives here.
func Execute(dev device.Device, req Request) error {
	return apply(dev, req, true)
}

func apply(dev device.Device, req Request, execute bool) error {
	switch req.Action {
	case "on", "off":
		sw, ok := dev.(device.Switchable)
		if !ok {
			return invalid("device does not support on/off")
		}
		if !execute {
			return nil
		}
		if req.Action == "on" {
			return sw.On()
		}
		return sw.Off()

	case "set_brightness":
		d, ok := dev.(device.Dimmable)
		if !ok {
			return invalid("device does not support brightness")
		}
		var pct int
		if err := json.Unmarshal(req.Value, &pct); err != nil {
			return invalid("set_brightness requires an integer value 0–100")
		}
		if pct < 0 || pct > 100 {
			return invalid("brightness must be 0–100")
		}
		if execute {
			return d.SetBrightness(pct)
		}
		return nil

	case "set_color":
		d, ok := dev.(device.ColorControl)
		if !ok {
			return invalid("device does not support color")
		}
		var color device.Color
		if err := json.Unmarshal(req.Value, &color); err != nil {
			return invalid(`set_color requires a {"r","g","b"} value`)
		}
		if execute {
			return d.SetColor(color)
		}
		return nil

	case "set_color_temp":
		d, ok := dev.(device.ColorTempControl)
		if !ok {
			return invalid("device does not support color temperature")
		}
		var kelvin int
		if err := json.Unmarshal(req.Value, &kelvin); err != nil {
			return invalid("set_color_temp requires a Kelvin integer (e.g. 2700)")
		}
		minKelvin, maxKelvin := d.ColorTempRange()
		if kelvin < minKelvin || kelvin > maxKelvin {
			return invalid("color temperature is outside the device range")
		}
		if execute {
			return d.SetColorTemp(kelvin)
		}
		return nil

	case "set_scene":
		d, ok := dev.(device.SceneControl)
		if !ok {
			return invalid("device does not support scenes")
		}
		var id int
		if err := json.Unmarshal(req.Value, &id); err != nil {
			return invalid("set_scene requires a scene id integer")
		}
		supported := false
		for _, scene := range d.Scenes() {
			if scene.ID == id {
				supported = true
				break
			}
		}
		if !supported {
			return invalid("scene is not supported by this device")
		}
		if execute {
			return d.SetScene(id)
		}
		return nil

	case "set_scene_speed":
		d, ok := dev.(device.SceneControl)
		if !ok {
			return invalid("device does not support scenes")
		}
		var speed int
		if err := json.Unmarshal(req.Value, &speed); err != nil {
			return invalid("set_scene_speed requires an integer (10–200)")
		}
		if speed < 10 || speed > 200 {
			return invalid("scene speed must be 10–200")
		}
		if execute {
			return d.SetSceneSpeed(speed)
		}
		return nil

	case "volume_up", "volume_down", "mute":
		v, ok := dev.(device.Volume)
		if !ok {
			return invalid("device does not support volume")
		}
		if !execute {
			return nil
		}
		switch req.Action {
		case "volume_up":
			return v.VolumeUp()
		case "volume_down":
			return v.VolumeDown()
		default:
			return v.ToggleMute()
		}

	case "set_volume":
		v, ok := dev.(device.VolumeSetter)
		if !ok {
			return invalid("device does not support absolute volume")
		}
		var pct int
		if err := json.Unmarshal(req.Value, &pct); err != nil {
			return invalid("set_volume requires an integer 0–100")
		}
		if pct < 0 || pct > 100 {
			return invalid("volume must be 0–100")
		}
		if execute {
			return v.SetVolume(pct)
		}
		return nil

	case "key":
		k, ok := dev.(device.KeyControl)
		if !ok {
			return invalid("device does not support remote keys")
		}
		var key string
		if err := json.Unmarshal(req.Value, &key); err != nil {
			return invalid(`key requires a string value like "KEY_HOME"`)
		}
		if execute {
			return k.SendKey(key)
		}
		return nil

	case "key_down", "key_up":
		k, ok := dev.(device.KeyHold)
		if !ok {
			return invalid("device does not support press-and-hold keys")
		}
		var key string
		if err := json.Unmarshal(req.Value, &key); err != nil {
			return invalid(`key_down/key_up require a string value like "KEY_RIGHT"`)
		}
		if !execute {
			return nil
		}
		if req.Action == "key_down" {
			return k.PressKey(key)
		}
		return k.ReleaseKey(key)

	case "send_text":
		t, ok := dev.(device.TextInput)
		if !ok {
			return invalid("device does not support text input")
		}
		var text string
		if err := json.Unmarshal(req.Value, &text); err != nil {
			return invalid("send_text requires a string value")
		}
		if text == "" {
			return invalid("send_text requires a non-empty string")
		}
		if execute {
			return t.SendText(text)
		}
		return nil

	case "launch_app":
		a, ok := dev.(device.AppControl)
		if !ok {
			return invalid("device does not support apps")
		}
		var id string
		if err := json.Unmarshal(req.Value, &id); err != nil {
			return invalid("launch_app requires an app id string")
		}
		supported := false
		for _, app := range a.Apps() {
			if app.ID == id {
				supported = true
				break
			}
		}
		if !supported {
			return invalid("app is not supported by this device")
		}
		if execute {
			return a.LaunchApp(id)
		}
		return nil

	case "wake":
		w, ok := dev.(device.WakeOnLAN)
		if !ok {
			return invalid("device does not support wake-on-lan")
		}
		if execute {
			return w.Wake()
		}
		return nil

	default:
		return invalid("unknown action: " + req.Action)
	}
}
