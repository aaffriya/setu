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
	if got := cfg.Listen.String(); got != ":80" {
		t.Errorf("default listen = %q, want :80", got)
	}
	if cfg.Listen.Port != 80 || cfg.Listen.Interface != "" || cfg.Listen.Socket != "" {
		t.Errorf("default listen fields = %+v, want {Interface:\"\" Port:80 Socket:\"\"}", cfg.Listen)
	}
	if cfg.PollInterval.Duration() != 250*time.Millisecond {
		t.Errorf("poll = %v, want 250ms", cfg.PollInterval.Duration())
	}
	if len(cfg.Devices) != 1 || cfg.Devices[0].ID != "a" {
		t.Fatalf("devices not parsed: %+v", cfg.Devices)
	}
}

func TestListenConfig(t *testing.T) {
	// interface set, port omitted -> defaults to 80 on that address.
	cfg, err := Load(write(t, `
auth: {token: x}
listen:
  interface: 192.168.1.10
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.Listen.String(); got != "192.168.1.10:80" {
		t.Errorf("listen = %q, want 192.168.1.10:80", got)
	}

	// explicit interface + port.
	cfg, err = Load(write(t, `
auth: {token: x}
listen: {interface: 127.0.0.1, port: 8080}
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if net, addr := cfg.Listen.Network(); net != "tcp" || addr != "127.0.0.1:8080" {
		t.Errorf("Network() = %q %q, want tcp 127.0.0.1:8080", net, addr)
	}

	// socket takes precedence over interface/port.
	cfg, err = Load(write(t, `
auth: {token: x}
listen: {socket: /run/setu.sock}
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if net, addr := cfg.Listen.Network(); net != "unix" || addr != "/run/setu.sock" {
		t.Errorf("Network() = %q %q, want unix /run/setu.sock", net, addr)
	}

	// out-of-range port is rejected.
	if _, err := Load(write(t, "auth: {token: x}\nlisten: {port: 70000}\n")); err == nil {
		t.Error("expected error for out-of-range port")
	}
}

func TestListenTLS(t *testing.T) {
	// Both cert and key set -> TLS enabled.
	cfg, err := Load(write(t, `
auth: {token: x}
listen:
  port: 443
  tls:
    cert: /etc/setu/cert.pem
    key: /etc/setu/key.pem
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.Listen.TLS.Enabled() {
		t.Error("TLS.Enabled() = false, want true when cert+key set")
	}

	// Neither set -> TLS disabled (plain HTTP, the default).
	cfg, err = Load(write(t, "auth: {token: x}\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Listen.TLS.Enabled() {
		t.Error("TLS.Enabled() = true, want false by default")
	}

	// Only one of cert/key set is a configuration error.
	if _, err := Load(write(t, "auth: {token: x}\nlisten: {tls: {cert: /c.pem}}\n")); err == nil {
		t.Error("expected error when only tls.cert is set")
	}
	if _, err := Load(write(t, "auth: {token: x}\nlisten: {tls: {key: /k.pem}}\n")); err == nil {
		t.Error("expected error when only tls.key is set")
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
