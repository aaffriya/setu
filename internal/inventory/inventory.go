// Package inventory owns the set of devices the user has added: the one place
// that turns a stored spec into a live device and keeps the two in step.
//
// Devices used to be hand-written into a config file and built once at startup.
// They are now added from the UI — found by a network scan, or typed in for the
// hardware that answers no scan (a Wake-on-LAN target) — so something has to
// hold specs, the factory, the manager and the state file together. That
// coordination is all this package does; the rules it enforces (a free id, an
// unused MAC, a valid spec) exist so a bad entry is refused at the door instead
// of breaking the next start.
package inventory

import (
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"setu/internal/config"
	"setu/internal/device"
	"setu/internal/manager"
	"setu/internal/resolver"
	"setu/internal/store"
)

// MaxDevices bounds a home installation. Setu runs on router hardware; an
// unbounded device list would be an unbounded number of pollers and sockets.
const MaxDevices = 64

// Inventory is the device registry: stored specs on one side, live devices in
// the manager on the other.
type Inventory struct {
	factory *config.Factory
	deps    config.Deps
	mgr     *manager.Manager
	file    *store.Store
	log     *slog.Logger

	mu    sync.Mutex
	specs []config.DeviceSpec
}

// New loads the stored devices, builds them, and registers them with the
// manager. A spec that cannot be built (an unknown brand after a downgrade, a
// hand-edited entry) is logged and skipped rather than stopping the bridge: the
// other devices must still work. The entry itself is kept — deleting a user's
// device because this build cannot drive it would be worse — so it still
// appears in a backup export, can be removed by id, and comes online as soon as
// an edit makes it valid again.
func New(file *store.Store, factory *config.Factory, deps config.Deps, mgr *manager.Manager, log *slog.Logger) (*Inventory, error) {
	state, err := file.Load()
	if err != nil {
		return nil, err
	}
	inv := &Inventory{factory: factory, deps: deps, mgr: mgr, file: file, log: log, specs: state.Devices}
	for _, spec := range inv.specs {
		// Validate on the way in as well as on the way out. Everything Setu
		// writes has passed this already, but the file is editable, and an id
		// that never passed validation would go on to be used as a URL segment
		// and a token file name.
		if err := spec.Validate(); err != nil {
			log.Error("skipping invalid stored device", "device", spec.ID, "err", err)
			continue
		}
		dev, err := inv.build(spec)
		if err != nil {
			log.Error("skipping unusable device", "device", spec.ID, "err", err)
			continue
		}
		if err := mgr.Add(dev); err != nil {
			log.Error("skipping duplicate device", "device", spec.ID, "err", err)
		}
	}
	return inv, nil
}

// Types returns the (brand, model) pairs Setu can build, for the UI's manual
// add form.
func (i *Inventory) Types() []config.DeviceType { return i.factory.Types() }

// Specs returns the stored device list — the backup form of an installation.
func (i *Inventory) Specs() []config.DeviceSpec {
	i.mu.Lock()
	defer i.mu.Unlock()
	return append([]config.DeviceSpec(nil), i.specs...)
}

// Add stores a new device and brings it online. An empty id is filled in from
// the brand and MAC, so neither the scan nor the manual form has to invent one.
func (i *Inventory) Add(spec config.DeviceSpec) (config.DeviceSpec, error) {
	i.mu.Lock()
	defer i.mu.Unlock()

	spec = spec.Normalized()
	if len(i.specs) >= MaxDevices {
		return config.DeviceSpec{}, fmt.Errorf("at most %d devices", MaxDevices)
	}
	if spec.ID == "" {
		spec.ID = suggestID(spec.Brand, spec.MAC, i.takenIDs())
	}
	if err := spec.Validate(); err != nil {
		return config.DeviceSpec{}, err
	}
	if err := i.available(spec); err != nil {
		return config.DeviceSpec{}, err
	}

	dev, err := i.build(spec)
	if err != nil {
		return config.DeviceSpec{}, err
	}
	next := append(append([]config.DeviceSpec(nil), i.specs...), spec)
	if err := i.persist(next); err != nil {
		return config.DeviceSpec{}, err
	}
	if err := i.mgr.Add(dev); err != nil {
		// Nothing went live, so nothing may stay stored: an "add failed" that
		// still appears after the next restart is the worst of both answers.
		if rollback := i.persist(i.specs); rollback != nil {
			i.log.Error("could not roll back a failed device add", "device", spec.ID, "err", rollback)
		}
		return config.DeviceSpec{}, err
	}
	i.specs = next
	return spec, nil
}

// Labels are a device's editable fields. A nil field is left exactly as it is:
// the UI edits name and series in two separate inputs, and saving one must not
// send — and possibly revert — a stale copy of the other.
type Labels struct {
	Name   *string
	Series *string
}

// Update edits a device's labels. The device is rebuilt because its name is
// fixed at construction, but it keeps its place and its last known state, so
// renaming does not blank the card until the next poll.
func (i *Inventory) Update(id string, labels Labels) (config.DeviceSpec, error) {
	i.mu.Lock()
	defer i.mu.Unlock()

	index := i.indexOf(id)
	if index < 0 {
		return config.DeviceSpec{}, errNotFound
	}
	spec := i.specs[index]
	if labels.Name != nil {
		spec.Name = *labels.Name
	}
	if labels.Series != nil {
		spec.Series = *labels.Series
	}
	spec = spec.Normalized()
	if err := spec.Validate(); err != nil {
		return config.DeviceSpec{}, err
	}

	dev, err := i.build(spec)
	if err != nil {
		return config.DeviceSpec{}, err
	}
	next := append([]config.DeviceSpec(nil), i.specs...)
	next[index] = spec
	if err := i.persist(next); err != nil {
		return config.DeviceSpec{}, err
	}
	if !i.mgr.Replace(dev) {
		// The entry was stored but not live — it failed validation or its build
		// when Setu started. The edit just repaired it, so bring it online now
		// rather than making the user restart to see their own fix.
		if err := i.mgr.Add(dev); err != nil {
			return config.DeviceSpec{}, err
		}
	}
	i.specs = next
	return spec, nil
}

// Remove deletes a device. Its automations are left alone: rules that reference
// it are disabled at the engine's next start, and silently rewriting a user's
// automations from a delete would be a surprise.
func (i *Inventory) Remove(id string) error {
	i.mu.Lock()
	defer i.mu.Unlock()

	index := i.indexOf(id)
	if index < 0 {
		return errNotFound
	}
	next := append(append([]config.DeviceSpec(nil), i.specs[:index]...), i.specs[index+1:]...)
	if err := i.persist(next); err != nil {
		return err
	}
	i.mgr.Remove(id)
	i.specs = next
	return nil
}

// Replace swaps the whole device list — the restore path. Everything is
// validated and built before anything is touched, so a bad backup leaves the
// running installation exactly as it was.
func (i *Inventory) Replace(specs []config.DeviceSpec) ([]config.DeviceSpec, error) {
	i.mu.Lock()
	defer i.mu.Unlock()

	if len(specs) > MaxDevices {
		return nil, fmt.Errorf("at most %d devices", MaxDevices)
	}
	next := make([]config.DeviceSpec, 0, len(specs))
	devices := make([]device.Device, 0, len(specs))
	ids := make(map[string]struct{}, len(specs))
	seen := make(map[string]struct{}, len(specs))
	for _, spec := range specs {
		spec = spec.Normalized()
		if spec.ID == "" {
			spec.ID = suggestID(spec.Brand, spec.MAC, ids)
		}
		if err := spec.Validate(); err != nil {
			return nil, err
		}
		if _, clash := ids[spec.ID]; clash {
			return nil, fmt.Errorf("duplicate device id %q", spec.ID)
		}
		key, err := identity(spec.Brand, spec.MAC)
		if err != nil {
			return nil, err
		}
		if _, clash := seen[key]; clash {
			return nil, fmt.Errorf("%s %s appears twice", spec.Brand, spec.MAC)
		}
		dev, err := i.build(spec)
		if err != nil {
			return nil, err
		}
		ids[spec.ID] = struct{}{}
		seen[key] = struct{}{}
		next = append(next, spec)
		devices = append(devices, dev)
	}

	if err := i.persist(next); err != nil {
		return nil, err
	}
	for _, spec := range i.specs {
		i.mgr.Remove(spec.ID)
	}
	for _, dev := range devices {
		if err := i.mgr.Add(dev); err != nil {
			i.log.Error("could not register restored device", "device", dev.ID(), "err", err)
		}
	}
	i.specs = next
	return next, nil
}

// Configured returns the id of this brand's device with that MAC, if any. The
// scan uses it to tell "already yours" from "new".
func (i *Inventory) Configured(brand, mac string) (string, bool) {
	want, err := identity(brand, mac)
	if err != nil {
		return "", false
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	for _, spec := range i.specs {
		if existing, err := identity(spec.Brand, spec.MAC); err == nil && existing == want {
			return spec.ID, true
		}
	}
	return "", false
}

// identity is what makes two entries the same device: one brand talking to one
// MAC. Deliberately not the MAC alone — a Wake-on-LAN card for a TV shares the
// TV's MAC on purpose, and that pair is two devices doing different jobs.
func identity(brand, mac string) (string, error) {
	normalized, err := resolver.NormalizeMAC(mac)
	if err != nil {
		return "", err
	}
	return strings.ToLower(brand) + "/" + normalized, nil
}

// errNotFound reports an unknown device id; the API maps it to 404.
//
// Like the validation messages, the errors this package returns are written for
// the person using the app — the API shows them as they are — so they carry no
// package prefix.
var errNotFound = fmt.Errorf("unknown device")

// IsNotFound reports whether err means "no such device".
func IsNotFound(err error) bool { return err == errNotFound }

func (i *Inventory) build(spec config.DeviceSpec) (device.Device, error) {
	return i.factory.Build(spec, i.deps)
}

func (i *Inventory) persist(specs []config.DeviceSpec) error {
	return i.file.Update(func(state *store.State) error {
		state.Devices = specs
		return nil
	})
}

func (i *Inventory) indexOf(id string) int {
	for index, spec := range i.specs {
		if spec.ID == id {
			return index
		}
	}
	return -1
}

func (i *Inventory) takenIDs() map[string]struct{} {
	taken := make(map[string]struct{}, len(i.specs))
	for _, spec := range i.specs {
		taken[spec.ID] = struct{}{}
	}
	return taken
}

// available rejects a spec that would collide with an existing device: the id is
// every reference's handle, and one brand talking to one MAC is one device —
// adding it twice would give the same hardware two cards that fight each other.
func (i *Inventory) available(spec config.DeviceSpec) error {
	want, err := identity(spec.Brand, spec.MAC)
	if err != nil {
		return err
	}
	for _, existing := range i.specs {
		if existing.ID == spec.ID {
			return fmt.Errorf("device id %q is already in use", spec.ID)
		}
		if current, err := identity(existing.Brand, existing.MAC); err == nil && current == want {
			return fmt.Errorf("%s %s is already added, as %q", existing.Brand, spec.MAC, existing.ID)
		}
	}
	return nil
}

// suggestID builds a readable id from the brand and the last three MAC bytes —
// stable for a given device, and distinct across a shelf of identical bulbs. A
// collision gets a numeric suffix.
func suggestID(brand, mac string, taken map[string]struct{}) string {
	base := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			return r
		case r >= 'A' && r <= 'Z':
			return r + ('a' - 'A')
		default:
			return -1
		}
	}, brand)
	if base == "" {
		base = "device"
	}
	if normalized, err := resolver.NormalizeMAC(mac); err == nil {
		base += "_" + normalized[6:]
	}
	id := base
	for suffix := 2; ; suffix++ {
		if _, clash := taken[id]; !clash {
			return id
		}
		id = fmt.Sprintf("%s_%d", base, suffix)
	}
}
