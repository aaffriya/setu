package wiz

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"setu/internal/config"
	"setu/internal/device"
)

// An unreachable bulb still has a meaningful state — offline — so Poll must wrap
// ErrPollNoResponse. Without it the manager discards the read model update, and
// an unplugged bulb keeps rendering as online and controllable in the UI.
func TestPollOfUnreachableBulbReportsOffline(t *testing.T) {
	bulb := &ColorBulb{base: base{
		id:         "bulb",
		mac:        "aa:bb:cc:dd:ee:ff",
		discoverer: &Discoverer{timeout: 10 * time.Millisecond},
		timeout:    10 * time.Millisecond,
		state:      device.State{Online: true, On: true, Brightness: 80},
	}}

	state, err := bulb.Poll()
	if err == nil {
		t.Fatal("Poll() of an unreachable bulb must report the failed contact")
	}
	if !errors.Is(err, device.ErrPollNoResponse) {
		t.Fatalf("Poll() error = %v; want it to wrap ErrPollNoResponse so the manager publishes the offline state", err)
	}
	if state.Online {
		t.Error("Poll() state.Online = true; an unreachable bulb must report offline")
	}
}

func TestTunableWhiteModelCapabilities(t *testing.T) {
	dev, err := NewTunableWhite(config.DeviceSpec{
		ID: "white",
	}, config.Deps{})
	if err != nil {
		t.Fatalf("NewTunableWhite: %v", err)
	}

	if got := dev.Driver(); got != DriverTunableWhite {
		t.Errorf("Driver() = %q, want %q", got, DriverTunableWhite)
	}
	wantCaps := []string{
		device.CapSwitch,
		device.CapBrightness,
		device.CapColorTemp,
		device.CapScene,
	}
	if got := dev.Capabilities(); !reflect.DeepEqual(got, wantCaps) {
		t.Errorf("Capabilities() = %v, want %v", got, wantCaps)
	}
	if _, ok := dev.(device.ColorControl); ok {
		t.Error("tunable-white model must not implement ColorControl")
	}
	control, ok := dev.(device.ColorTempControl)
	if !ok {
		t.Error("tunable-white model must implement ColorTempControl")
	} else {
		if min, max := control.ColorTempRange(); min != 2700 || max != 6500 {
			t.Errorf("ColorTempRange() = %d–%d, want 2700–6500", min, max)
		}
	}
	if _, ok := dev.(device.SceneControl); !ok {
		t.Error("tunable-white model must implement SceneControl")
	}
}

func TestColorBulbTemperatureRange(t *testing.T) {
	dev, err := New(config.DeviceSpec{ID: "color"}, config.Deps{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	control := dev.(device.ColorTempControl)
	if min, max := control.ColorTempRange(); min != 2200 || max != 6500 {
		t.Errorf("ColorTempRange() = %d–%d, want 2200–6500", min, max)
	}
}

func TestTunableWhiteScenes(t *testing.T) {
	b := &TunableWhiteBulb{base: base{id: "white"}}
	got := b.Scenes()
	if len(got) != 8 || got[0].ID != 9 || got[len(got)-1].ID != 16 {
		t.Fatalf("Scenes() = %+v, want WiZ white modes 9–16", got)
	}
	for _, scene := range got {
		if scene.Dynamic {
			t.Errorf("white scene %d unexpectedly marked dynamic", scene.ID)
		}
	}
	if err := b.SetScene(1); err == nil {
		t.Error("SetScene(1) should reject the unsupported Ocean colour mode")
	}
	if err := b.SetSceneSpeed(100); err != nil {
		t.Errorf("SetSceneSpeed on static white scenes should no-op, got %v", err)
	}
}

func TestRegisterIncludesTunableWhite(t *testing.T) {
	factory := config.NewFactory()
	Register(factory)

	dev, err := factory.Build(config.DeviceSpec{
		ID:     "white",
		Brand:  "wiz",
		Driver: "TUNABLE_WHITE",
	}, config.Deps{})
	if err != nil {
		t.Fatalf("Build tunable-white: %v", err)
	}
	if _, ok := dev.(*TunableWhiteBulb); !ok {
		t.Fatalf("Build returned %T, want *TunableWhiteBulb", dev)
	}
}
