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

// Factory maps a (brand, driver) pair to the Constructor that builds it. Device
// packages register themselves at startup, so the factory imports no device
// packages: the dependency arrow points devices → config, never back. That is
// what lets config stay pure data + mechanism.
type Factory struct {
	constructors map[string]Constructor
	types        []DeviceType
}

// DeviceType is one registered (brand, driver) pair plus the category and human
// label that name it on screen. The UI lists these when a device has to be added
// by hand (a Wake-on-LAN target answers no scan), so the catalog stays in code —
// the only place that knows what Setu can actually drive.
//
// Label exists so a driver key never has to be shown: "tizen" is a lookup key,
// "Tizen TV" is what a person is choosing between.
type DeviceType struct {
	Category string `json:"category"`
	Brand    string `json:"brand"`
	Driver   string `json:"driver"`
	Label    string `json:"label"`
}

// NewFactory returns an empty Factory.
func NewFactory() *Factory {
	return &Factory{constructors: make(map[string]Constructor)}
}

// Types returns every registered driver, sorted the way the picker reads them:
// by category, brand, then label.
func (f *Factory) Types() []DeviceType {
	out := append([]DeviceType(nil), f.types...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Category != out[j].Category {
			return out[i].Category < out[j].Category
		}
		if out[i].Brand != out[j].Brand {
			return out[i].Brand < out[j].Brand
		}
		return out[i].Label < out[j].Label
	})
	return out
}

// key normalizes (brand, driver) to a case-insensitive lookup key, so a stored
// spec may say "WiZ", "wiz", or "WIZ" and still match the registered
// constructor. The brand's display spelling (Device.Brand) is the one the brand
// package registered.
func key(brand, driver string) string {
	return strings.ToLower(brand) + "/" + strings.ToLower(driver)
}

// Register associates a (brand, driver) pair with its Constructor, under the
// category and label the UI shows for it. It panics on invalid display metadata
// or a duplicate, since those are programming errors in the composition root
// (cmd/setu/main.go), not runtime conditions.
func (f *Factory) Register(category, brand, driver, label string, c Constructor) {
	k := key(brand, driver)
	if _, exists := f.constructors[k]; exists {
		panic(fmt.Sprintf("config: device driver %q already registered", k))
	}
	if category == "" {
		panic(fmt.Sprintf("config: device driver %q needs a category", k))
	}
	if label == "" {
		panic(fmt.Sprintf("config: device driver %q needs a label", k))
	}
	f.constructors[k] = c
	f.types = append(f.types, DeviceType{
		Category: category,
		Brand:    brand,
		Driver:   driver,
		Label:    label,
	})
}

// Supports reports whether a (brand, driver) pair can be built.
func (f *Factory) Supports(brand, driver string) bool {
	_, ok := f.constructors[key(brand, driver)]
	return ok
}

// Build constructs a single device from its spec.
func (f *Factory) Build(spec DeviceSpec, deps Deps) (device.Device, error) {
	c, ok := f.constructors[key(spec.Brand, spec.Driver)]
	if !ok {
		// Read by two audiences: a user restoring a backup from a build that had
		// more brands, and a developer who forgot the Register line in
		// cmd/setu/main.go. "in this build" is the fact both of them need.
		return nil, fmt.Errorf("no %q driver for brand %q in this build", spec.Driver, spec.Brand)
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
