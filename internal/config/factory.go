package config

import (
	"fmt"
	"sort"
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
	types        []DeviceType
}

// DeviceType is one registered (brand, model) pair, exactly as the brand
// package spelled it. The UI lists these when a device has to be added by hand
// (a Wake-on-LAN target answers no scan), so the catalog stays in code — the
// only place that knows what Setu can actually drive.
type DeviceType struct {
	Brand string `json:"brand"`
	Model string `json:"model"`
}

// NewFactory returns an empty Factory.
func NewFactory() *Factory {
	return &Factory{constructors: make(map[string]Constructor)}
}

// Types returns every registered (brand, model) pair, sorted for a stable UI.
func (f *Factory) Types() []DeviceType {
	out := append([]DeviceType(nil), f.types...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Brand != out[j].Brand {
			return out[i].Brand < out[j].Brand
		}
		return out[i].Model < out[j].Model
	})
	return out
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
	f.types = append(f.types, DeviceType{Brand: brand, Model: model})
}

// Supports reports whether a (brand, model) pair can be built.
func (f *Factory) Supports(brand, model string) bool {
	_, ok := f.constructors[key(brand, model)]
	return ok
}

// Build constructs a single device from its spec.
func (f *Factory) Build(spec DeviceSpec, deps Deps) (device.Device, error) {
	c, ok := f.constructors[key(spec.Brand, spec.Model)]
	if !ok {
		// Read by two audiences: a user restoring a backup from a build that had
		// more brands, and a developer who forgot the Register line in
		// cmd/setu/main.go. "in this build" is the fact both of them need.
		return nil, fmt.Errorf("no driver for brand %q model %q in this build", spec.Brand, spec.Model)
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
