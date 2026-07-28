package config

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"setu/internal/device"
)

// An installation with nothing configured must still run, on the settings Setu
// has always shipped — that is what makes the environment optional rather than
// a new thing every user has to learn.
func TestLoadDefaultsWithEmptyEnvironment(t *testing.T) {
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.Listen.String(); got != ":80" {
		t.Errorf("default listen = %q, want :80", got)
	}
	if cfg.Token != DefaultToken {
		t.Errorf("default token = %q, want %q", cfg.Token, DefaultToken)
	}
	if cfg.PollInterval != 45*time.Second {
		t.Errorf("default poll interval = %v, want 45s", cfg.PollInterval)
	}
	if cfg.Listen.TLS.Enabled() {
		t.Error("TLS is on by default; it must need an explicit certificate")
	}
}

func TestLoadFromEnvironment(t *testing.T) {
	t.Setenv(EnvToken, "secret")
	t.Setenv(EnvInterface, "127.0.0.1")
	t.Setenv(EnvPort, "8080")
	t.Setenv(EnvPollInterval, "250ms")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Token != "secret" {
		t.Errorf("token = %q, want secret", cfg.Token)
	}
	if address := cfg.Listen.Address(); address != "127.0.0.1:8080" {
		t.Errorf("Address() = %q, want 127.0.0.1:8080", address)
	}
	if cfg.PollInterval != 250*time.Millisecond {
		t.Errorf("poll interval = %v, want 250ms", cfg.PollInterval)
	}
}

// A typo in the environment must fail at startup with a clear message, not
// silently fall back to a default the user did not ask for.
func TestLoadRejectsBadValues(t *testing.T) {
	t.Run("port out of range", func(t *testing.T) {
		t.Setenv(EnvPort, "70000")
		if _, err := Load(); err == nil {
			t.Error("expected an error for an out-of-range port")
		}
	})
	t.Run("port not a number", func(t *testing.T) {
		t.Setenv(EnvPort, "http")
		if _, err := Load(); err == nil {
			t.Error("expected an error for a non-numeric port")
		}
	})
	t.Run("bad duration", func(t *testing.T) {
		t.Setenv(EnvPollInterval, "45")
		if _, err := Load(); err == nil {
			t.Error("expected an error for a duration without a unit")
		}
	})
	t.Run("half-configured TLS", func(t *testing.T) {
		t.Setenv(EnvTLSCert, "/c.pem")
		if _, err := Load(); err == nil {
			t.Error("expected an error when only the certificate is set")
		}
	})
}

// Configuring a certificate changes the scheme, so it changes the port a user
// would expect — 443, exactly as a browser assumes.
func TestTLSNeedsBothFilesAndMovesTheDefaultPort(t *testing.T) {
	t.Setenv(EnvTLSCert, "/c.pem")
	t.Setenv(EnvTLSKey, "/k.pem")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.Listen.TLS.Enabled() {
		t.Error("TLS.Enabled() = false with both files set")
	}
	if got := cfg.Listen.String(); got != ":443" {
		t.Errorf("TLS listen = %q, want :443", got)
	}

	// An explicit port still wins over the scheme's default.
	t.Setenv(EnvPort, "8443")
	cfg, err = Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.Listen.String(); got != ":8443" {
		t.Errorf("explicit TLS port = %q, want :8443", got)
	}
}

// Specs now arrive from the API — a scan result, a typed-in form, a restored
// backup — so validation is the only thing standing between a bad entry and a
// state file that breaks the next start.
func TestDeviceSpecValidation(t *testing.T) {
	valid := DeviceSpec{ID: "wiz_a234f2", Brand: "WiZ", Driver: "color_bulb", Name: "Lamp", MAC: "98:77:d5:a2:34:f2"}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid spec rejected: %v", err)
	}

	for name, spec := range map[string]DeviceSpec{
		"no id":        {Brand: "WiZ", Driver: "color_bulb", Name: "Lamp", MAC: "98:77:d5:a2:34:f2"},
		"id with dot":  {ID: "wiz.1", Brand: "WiZ", Driver: "color_bulb", Name: "Lamp", MAC: "98:77:d5:a2:34:f2"},
		"id with dash": {ID: "-lamp", Brand: "WiZ", Driver: "color_bulb", Name: "Lamp", MAC: "98:77:d5:a2:34:f2"},
		"no brand":     {ID: "lamp", Driver: "color_bulb", Name: "Lamp", MAC: "98:77:d5:a2:34:f2"},
		"no model":     {ID: "lamp", Brand: "WiZ", Name: "Lamp", MAC: "98:77:d5:a2:34:f2"},
		"no name":      {ID: "lamp", Brand: "WiZ", Driver: "color_bulb", MAC: "98:77:d5:a2:34:f2"},
		"no mac":       {ID: "lamp", Brand: "WiZ", Driver: "color_bulb", Name: "Lamp"},
		"bad mac":      {ID: "lamp", Brand: "WiZ", Driver: "color_bulb", Name: "Lamp", MAC: "98:77:d5"},
	} {
		if err := spec.Validate(); err == nil {
			t.Errorf("%s: expected a validation error", name)
		}
	}
}

// Two spellings of one MAC must not become two devices, so specs are stored in
// one notation.
func TestDeviceSpecNormalized(t *testing.T) {
	spec := DeviceSpec{ID: " WIZ_A234F2 ", Brand: " WiZ ", Driver: "color_bulb", Name: "  Lamp ", MAC: "9877d5a234f2"}
	got := spec.Normalized()
	if got.ID != "wiz_a234f2" || got.Brand != "WiZ" || got.Name != "Lamp" {
		t.Errorf("Normalized() = %+v", got)
	}
	if got.MAC != "98:77:d5:a2:34:f2" {
		t.Errorf("Normalized().MAC = %q, want the canonical colon form", got.MAC)
	}
}

func TestFactory(t *testing.T) {
	f := NewFactory()
	var built DeviceSpec
	f.Register("Tool", "acme", "widget", "Widget", func(spec DeviceSpec, deps Deps) (device.Device, error) {
		built = spec
		return nil, nil
	})

	if _, err := f.Build(DeviceSpec{ID: "x", Brand: "acme", Driver: "widget"}, Deps{}); err != nil {
		t.Fatalf("Build: %v", err)
	}
	if built.ID != "x" {
		t.Errorf("constructor received spec %+v", built)
	}
	if _, err := f.Build(DeviceSpec{Brand: "nope", Driver: "nope"}, Deps{}); err == nil {
		t.Error("expected error for unregistered (brand, driver)")
	}
	if !f.Supports("ACME", "Widget") {
		t.Error("Supports() must match the same case-insensitive key as Build()")
	}
	if types := f.Types(); len(types) != 1 || types[0].Category != "Tool" ||
		types[0].Brand != "acme" || types[0].Driver != "widget" || types[0].Label != "Widget" {
		t.Errorf("Types() = %+v, want the registered driver as spelled, with its label", types)
	}
}

func TestFactoryTypesSortForPicker(t *testing.T) {
	f := NewFactory()
	build := func(DeviceSpec, Deps) (device.Device, error) { return nil, nil }
	f.Register("TV", "Zulu", "tv", "Television", build)
	f.Register("Light", "Zulu", "white", "White Bulb", build)
	f.Register("Light", "Acme", "color", "Colour Bulb", build)
	f.Register("Light", "Acme", "basic", "Basic Bulb", build)

	got := f.Types()
	want := []DeviceType{
		{Category: "Light", Brand: "Acme", Driver: "basic", Label: "Basic Bulb"},
		{Category: "Light", Brand: "Acme", Driver: "color", Label: "Colour Bulb"},
		{Category: "Light", Brand: "Zulu", Driver: "white", Label: "White Bulb"},
		{Category: "TV", Brand: "Zulu", Driver: "tv", Label: "Television"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Types() = %+v; want %+v", got, want)
	}
}

func TestFactoryRejectsMissingCatalogLabels(t *testing.T) {
	build := func(DeviceSpec, Deps) (device.Device, error) { return nil, nil }
	for name, register := range map[string]func(){
		"category": func() { NewFactory().Register("", "acme", "widget", "Widget", build) },
		"label":    func() { NewFactory().Register("Tool", "acme", "widget", "", build) },
	} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("Register did not panic")
				}
			}()
			register()
		})
	}
}

// The limits are shown to the person typing the value, so they have to mean
// characters. Counting bytes cut a Devanagari or emoji name off at a third of
// the stated length.
func TestNameLimitCountsCharactersNotBytes(t *testing.T) {
	spec := DeviceSpec{ID: "lamp", Brand: "test", Driver: "lamp", MAC: "98:77:d5:a2:34:f2"}

	spec.Name = strings.Repeat("क", MaxNameLength)
	if err := spec.Validate(); err != nil {
		t.Errorf("a %d-character name was refused: %v", MaxNameLength, err)
	}

	spec.Name = strings.Repeat("क", MaxNameLength+1)
	if err := spec.Validate(); err == nil {
		t.Errorf("a %d-character name was accepted", MaxNameLength+1)
	}
}
