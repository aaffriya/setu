package control

import (
	"encoding/json"
	"testing"

	"setu/internal/device"
)

type dimmer struct{ brightness int }

func (*dimmer) ID() string             { return "dimmer" }
func (*dimmer) Name() string           { return "Dimmer" }
func (*dimmer) Brand() string          { return "test" }
func (*dimmer) Model() string          { return "dimmer" }
func (*dimmer) MAC() string            { return "02:00:00:00:00:03" }
func (*dimmer) Capabilities() []string { return []string{device.CapBrightness} }
func (*dimmer) State() device.State    { return device.State{} }
func (d *dimmer) SetBrightness(value int) error {
	d.brightness = value
	return nil
}

type catalogDevice struct{}

func (*catalogDevice) ID() string             { return "catalog" }
func (*catalogDevice) Name() string           { return "Catalog" }
func (*catalogDevice) Brand() string          { return "test" }
func (*catalogDevice) Model() string          { return "catalog" }
func (*catalogDevice) MAC() string            { return "02:00:00:00:00:04" }
func (*catalogDevice) Capabilities() []string { return nil }
func (*catalogDevice) State() device.State    { return device.State{} }
func (*catalogDevice) SetColorTemp(int) error { return nil }
func (*catalogDevice) ColorTempRange() (int, int) {
	return 2700, 6500
}
func (*catalogDevice) Scenes() []device.Scene {
	return []device.Scene{{ID: 11, Name: "Warm"}}
}
func (*catalogDevice) SetScene(int) error      { return nil }
func (*catalogDevice) SetSceneSpeed(int) error { return nil }
func (*catalogDevice) Apps() []device.App {
	return []device.App{{ID: "youtube", Name: "YouTube"}}
}
func (*catalogDevice) LaunchApp(string) error { return nil }

func TestValidateDoesNotExecuteAndExecuteSharesValidation(t *testing.T) {
	dev := &dimmer{}
	req := Request{Action: "set_brightness", Value: json.RawMessage("65")}
	if err := Validate(dev, req); err != nil {
		t.Fatal(err)
	}
	if dev.brightness != 0 {
		t.Fatal("Validate performed device I/O")
	}
	if err := Execute(dev, req); err != nil {
		t.Fatal(err)
	}
	if dev.brightness != 65 {
		t.Fatalf("brightness = %d, want 65", dev.brightness)
	}
	if err := Validate(dev, Request{Action: "set_brightness", Value: json.RawMessage("101")}); err == nil {
		t.Fatal("out-of-range command passed validation")
	}
}

func TestValidateRejectsValuesOutsideDeviceCatalogs(t *testing.T) {
	dev := &catalogDevice{}
	tests := []Request{
		{Action: "set_color_temp", Value: json.RawMessage("2600")},
		{Action: "set_scene", Value: json.RawMessage("12")},
		{Action: "set_scene_speed", Value: json.RawMessage("201")},
		{Action: "launch_app", Value: json.RawMessage(`"unknown"`)},
	}
	for _, req := range tests {
		if err := Validate(dev, req); err == nil {
			t.Errorf("%s accepted unsupported value %s", req.Action, req.Value)
		}
	}

	valid := []Request{
		{Action: "set_color_temp", Value: json.RawMessage("2700")},
		{Action: "set_scene", Value: json.RawMessage("11")},
		{Action: "set_scene_speed", Value: json.RawMessage("200")},
		{Action: "launch_app", Value: json.RawMessage(`"youtube"`)},
	}
	for _, req := range valid {
		if err := Validate(dev, req); err != nil {
			t.Errorf("%s rejected supported value %s: %v", req.Action, req.Value, err)
		}
	}
}
