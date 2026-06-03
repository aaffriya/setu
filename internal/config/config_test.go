package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"setu/internal/device"
)

func write(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadDefaultsAndDuration(t *testing.T) {
	cfg, err := Load(write(t, `
auth:
  token: "secret"
poll_interval: 250ms
devices:
  - id: a
    brand: example
    model: bulb
    name: A
    mac: "a8:bb:50:11:22:33"
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Listen != ":8080" {
		t.Errorf("default listen = %q, want :8080", cfg.Listen)
	}
	if cfg.PollInterval.Duration() != 250*time.Millisecond {
		t.Errorf("poll = %v, want 250ms", cfg.PollInterval.Duration())
	}
	if len(cfg.Devices) != 1 || cfg.Devices[0].ID != "a" {
		t.Fatalf("devices not parsed: %+v", cfg.Devices)
	}
}

func TestValidate(t *testing.T) {
	if _, err := Load(write(t, "poll_interval: 5s\n")); err == nil {
		t.Error("expected error for missing token")
	}
	if _, err := Load(write(t, `
auth: {token: x}
devices:
  - {id: a, brand: b, model: m, mac: "a8:bb:50:11:22:33"}
  - {id: a, brand: b, model: m, mac: "a8:bb:50:11:22:34"}
`)); err == nil {
		t.Error("expected error for duplicate device id")
	}
}

func TestFactory(t *testing.T) {
	f := NewFactory()
	var built DeviceSpec
	f.Register("acme", "widget", func(spec DeviceSpec, deps Deps) (device.Device, error) {
		built = spec
		return nil, nil
	})

	if _, err := f.Build(DeviceSpec{ID: "x", Brand: "acme", Model: "widget"}, Deps{}); err != nil {
		t.Fatalf("Build: %v", err)
	}
	if built.ID != "x" {
		t.Errorf("constructor received spec %+v", built)
	}
	if _, err := f.Build(DeviceSpec{Brand: "nope", Model: "nope"}, Deps{}); err == nil {
		t.Error("expected error for unregistered (brand, model)")
	}
}
