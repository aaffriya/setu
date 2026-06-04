package config

import (
	"fmt"
	"strings"

	"setu/internal/device"
	"setu/internal/events"
	"setu/internal/resolver"
)

// Deps are the shared runtime dependencies handed to every device constructor.
// Bundling them means adding a new dependency later won't change every
// constructor signature.
type Deps struct {
	Resolver resolver.Resolver
	Bus      *events.Bus
}

// Constructor builds one device from its config entry and the shared deps. Each
// device package exports a function of this type (see example.New).
type Constructor func(spec DeviceSpec, deps Deps) (device.Device, error)

// Factory maps a (brand, model) pair to the Constructor that builds it. Device
// packages register themselves at startup, so the factory imports no device
// packages: the dependency arrow points devices → config, never back. That is
// what lets config stay pure data + mechanism.
type Factory struct {
	constructors map[string]Constructor
}

// NewFactory returns an empty Factory.
func NewFactory() *Factory {
	return &Factory{constructors: make(map[string]Constructor)}
}

// key normalizes (brand, model) to a case-insensitive lookup key, so config may
// write "WiZ", "wiz", or "WIZ" and still match the registered constructor. The
// brand's display name (Device.Brand) is kept as registered.
func key(brand, model string) string {
	return strings.ToLower(brand) + "/" + strings.ToLower(model)
}

// Register associates a (brand, model) pair with its Constructor. It panics on a
// duplicate, since that is always a programming error in the composition root
// (cmd/setu/main.go), not a runtime condition.
func (f *Factory) Register(brand, model string, c Constructor) {
	k := key(brand, model)
	if _, exists := f.constructors[k]; exists {
		panic(fmt.Sprintf("config: device type %q already registered", k))
	}
	f.constructors[k] = c
}

// Build constructs a single device from its spec.
func (f *Factory) Build(spec DeviceSpec, deps Deps) (device.Device, error) {
	c, ok := f.constructors[key(spec.Brand, spec.Model)]
	if !ok {
		return nil, fmt.Errorf("config: no device registered for brand %q model %q (did you register it in main?)", spec.Brand, spec.Model)
	}
	return c(spec, deps)
}

// BuildAll constructs every device in specs, preserving order, and fails fast on
// the first error so a misconfigured device is caught at startup.
func (f *Factory) BuildAll(specs []DeviceSpec, deps Deps) ([]device.Device, error) {
	devs := make([]device.Device, 0, len(specs))
	for _, spec := range specs {
		d, err := f.Build(spec, deps)
		if err != nil {
			return nil, err
		}
		devs = append(devs, d)
	}
	return devs, nil
}
