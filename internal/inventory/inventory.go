// Package inventory owns the set of devices the user has added: the one place
// that turns a stored spec into a live device and keeps the two in step.
//
// Devices are added from the UI — found by a network scan, or typed in for the
// hardware that answers no scan (a Wake-on-LAN target) — so this package holds
// specs, the factory, the manager and the state file together. The rules it
// enforces (a free id, an unused MAC, a valid spec) refuse a bad entry at the
// door instead of breaking the next start.
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
	"setu/internal/users"
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
	users   *users.Registry
	log     *slog.Logger

	mu    sync.Mutex
	specs []config.DeviceSpec
	// unusable maps the id of a stored device that is not running to why. Only
	// New and Replace can put one here — everywhere else a spec is stored only
	// after it has been built and registered — and Update and Remove take it
	// back out. Recording the reason where the failure happens is what lets the
	// app say "no wiz driver in this build" instead of "unknown device".
	unusable map[string]string
}

// Unusable is one stored device that is not running: the entry exactly as it
// was kept, and why this build could not bring it online. The spec is embedded
// so the JSON is one flat object — the same fields a backup carries, plus the
// reason.
type Unusable struct {
	config.DeviceSpec
	Reason string `json:"reason"`
	// Repairable reports that editing the labels could bring this entry online,
	// so the app knows whether to offer that edit at all. A name the stored
	// limit refused is fixable in place; a driver this build does not have is
	// not, and the only honest thing to offer there is removal.
	Repairable bool `json:"repairable"`
}

// New loads the stored devices, builds them, and registers them with the
// manager. A spec that cannot be built (an unknown brand after a downgrade, a
// hand-edited entry) is logged and skipped rather than stopping the bridge: the
// other devices must still work. The entry itself is kept — deleting a user's
// device because this build cannot drive it would be worse — so it still
// appears in a backup export, can be removed by id, and comes online as soon as
// an edit makes it valid again.
func New(file *store.Store, factory *config.Factory, deps config.Deps, mgr *manager.Manager, accounts *users.Registry, log *slog.Logger) (*Inventory, error) {
	state, err := file.Load()
	if err != nil {
		return nil, err
	}
	inv := &Inventory{
		factory: factory, deps: deps, mgr: mgr, file: file, users: accounts, log: log,
		specs:    state.Devices,
		unusable: make(map[string]string),
	}
	for _, spec := range inv.specs {
		// Validate on the way in as well as on the way out. Everything Setu
		// writes has passed this already, but the file is editable, and an id
		// that never passed validation would go on to be used as a URL segment
		// and a token file name.
		if err := spec.Validate(); err != nil {
			log.Error("skipping invalid stored device", "device", spec.ID, "err", err)
			inv.unusable[spec.ID] = err.Error()
			continue
		}
		dev, err := inv.build(spec)
		if err != nil {
			log.Error("skipping unusable device", "device", spec.ID, "err", err)
			inv.unusable[spec.ID] = err.Error()
			continue
		}
		if err := mgr.Add(dev); err != nil {
			release(dev)
			log.Error("skipping duplicate device", "device", spec.ID, "err", err)
			inv.unusable[spec.ID] = err.Error()
		}
	}
	return inv, nil
}

// Unusable lists the stored devices that are not running, in stored order, so
// the app can show what it is otherwise silently hiding.
//
// A spec that cannot be built is deliberately kept (see New), which is only
// useful if its owner can find it: it does not appear in the manager, so it is
// in no device list, on no card, and in no picker. Without this the only way to
// discover one is to read the logs or export a backup.
func (i *Inventory) Unusable() []Unusable {
	i.mu.Lock()
	defer i.mu.Unlock()
	out := make([]Unusable, 0, len(i.unusable))
	for _, spec := range i.specs {
		if reason, broken := i.unusable[spec.ID]; broken {
			out = append(out, Unusable{DeviceSpec: spec, Reason: reason, Repairable: i.repairable(spec)})
		}
	}
	return out
}

// repairable reports whether an edit the app can actually make would fix this
// entry. Update changes only the name and the model, so everything it cannot
// touch — the id, the MAC, the brand and driver pair — has to already be sound,
// or offering the field is a promise the server will break. The driver is
// checked through the factory rather than by building one: building opens
// sockets, and this only asks whether it could.
func (i *Inventory) repairable(spec config.DeviceSpec) bool {
	probe := spec
	probe.Name = "device"
	probe.Model = ""
	return probe.Normalized().Validate() == nil && i.factory.Supports(spec.Brand, spec.Driver)
}

// Types returns the categorized, labelled drivers Setu can build, for the UI's
// manual add form and to describe scan results.
func (i *Inventory) Types() []config.DeviceType { return i.factory.Types() }

// Specs returns the stored device list — the backup form of an installation.
func (i *Inventory) Specs() []config.DeviceSpec {
	i.mu.Lock()
	defer i.mu.Unlock()
	return append([]config.DeviceSpec(nil), i.specs...)
}

// Has reports whether an id is stored, whether or not it is live. The routes
// that manage the device list ask this rather than the manager, so an entry
// that was skipped at startup — the one that most needs repairing or removing —
// is still addressable.
func (i *Inventory) Has(id string) bool {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.indexOf(id) >= 0
}

// Add stores a new device and brings it online. An empty id is filled in from
// the brand and MAC, so neither the scan nor the manual form has to invent one.
//
// grantTo, when set, is the account that added it. Membership and that
// account's access to it are written together: a device stored without the
// grant that makes it usable is one its own adder cannot see, and a grant
// written without the device is access to an id that may come back later as
// different hardware.
func (i *Inventory) Add(spec config.DeviceSpec, grantTo string) (config.DeviceSpec, error) {
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
	if err := i.persistWithGrant(next, spec.ID, grantTo); err != nil {
		release(dev)
		return config.DeviceSpec{}, err
	}
	if err := i.mgr.Add(dev); err != nil {
		release(dev)
		// Nothing went live, so nothing may stay stored: an "add failed" that
		// still appears after the next restart is the worst of both answers. The
		// rollback prunes grants as well, so the access just written for a device
		// that no longer exists goes with it.
		if rollback := i.persistWithPrunedGrants(i.specs); rollback != nil {
			i.log.Error("could not roll back a failed device add", "device", spec.ID, "err", rollback)
		}
		return config.DeviceSpec{}, err
	}
	i.specs = next
	return spec, nil
}

// Labels are a device's editable fields — everything that is not identity. A
// nil field is left exactly as it is: the UI edits the name and the model in two
// separate inputs, and saving one must not send — and possibly revert — a stale
// copy of the other.
type Labels struct {
	Name  *string
	Model *string
}

// Update edits a device's labels without rebuilding a live protocol driver.
// Rebuilding for presentation-only metadata would discard its cached state and
// any long-lived transport resources. An unusable entry is still built here so
// a label repair can bring it online without a restart.
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
	if labels.Model != nil {
		spec.Model = *labels.Model
	}
	spec = spec.Normalized()
	if err := spec.Validate(); err != nil {
		return config.DeviceSpec{}, err
	}

	_, live := i.mgr.Device(id)
	var dev device.Device
	var err error
	if !live {
		dev, err = i.build(spec)
		if err != nil {
			return config.DeviceSpec{}, err
		}
	}
	next := append([]config.DeviceSpec(nil), i.specs...)
	next[index] = spec
	if err := i.persist(next); err != nil {
		if dev != nil {
			release(dev)
		}
		return config.DeviceSpec{}, err
	}
	if live {
		if !i.mgr.UpdateLabels(id, spec.Name, spec.Model) {
			return config.DeviceSpec{}, fmt.Errorf("device %q stopped while its labels were being updated", id)
		}
	} else {
		// The entry was stored but not live — it failed validation or its build
		// when Setu started. The edit just repaired it, so bring it online now
		// rather than making the user restart to see their own fix.
		if err := i.mgr.Add(dev); err != nil {
			release(dev)
			return config.DeviceSpec{}, err
		}
	}
	// It is running either way now, so it is no longer one of the broken ones.
	delete(i.unusable, id)
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
	if err := i.persistWithPrunedGrants(next); err != nil {
		return err
	}
	i.mgr.Remove(id)
	delete(i.unusable, id)
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
	next, devices, err := i.prepare(specs)
	if err != nil {
		return nil, err
	}

	if err := i.persistWithPrunedGrants(next); err != nil {
		releaseAll(devices)
		return nil, err
	}
	for _, spec := range i.specs {
		i.mgr.Remove(spec.ID)
	}
	// Everything in next validated and built above, so the restore starts from a
	// clean slate; only a device the manager itself refuses stays broken.
	clear(i.unusable)
	for _, dev := range devices {
		if err := i.mgr.Add(dev); err != nil {
			release(dev)
			i.log.Error("could not register restored device", "device", dev.ID(), "err", err)
			i.unusable[dev.ID()] = err.Error()
		}
	}
	i.specs = next
	return next, nil
}

// prepare normalizes, validates and builds a whole replacement list. It is
// all-or-nothing: a failure releases whatever it had already built, so a
// rejected restore leaves nothing behind.
func (i *Inventory) prepare(specs []config.DeviceSpec) ([]config.DeviceSpec, []device.Device, error) {
	next := make([]config.DeviceSpec, 0, len(specs))
	devices := make([]device.Device, 0, len(specs))
	ids := make(map[string]struct{}, len(specs))
	seen := make(map[string]struct{}, len(specs))
	fail := func(err error) ([]config.DeviceSpec, []device.Device, error) {
		releaseAll(devices)
		return nil, nil, err
	}
	for _, spec := range specs {
		spec = spec.Normalized()
		if spec.ID == "" {
			spec.ID = suggestID(spec.Brand, spec.MAC, ids)
		}
		if err := spec.Validate(); err != nil {
			return fail(err)
		}
		if _, clash := ids[spec.ID]; clash {
			return fail(fmt.Errorf("duplicate device id %q", spec.ID))
		}
		key, err := identity(spec.Brand, spec.MAC)
		if err != nil {
			return fail(err)
		}
		if _, clash := seen[key]; clash {
			return fail(fmt.Errorf("%s %s appears twice", spec.Brand, spec.MAC))
		}
		dev, err := i.build(spec)
		if err != nil {
			return fail(err)
		}
		ids[spec.ID] = struct{}{}
		seen[key] = struct{}{}
		next = append(next, spec)
		devices = append(devices, dev)
	}
	return next, devices, nil
}

// release closes a device that was built but never handed to the manager.
// Building is what opens sockets and registers callbacks, so an abandoned
// instance keeps them for the life of the process — and on a brand that keys
// those callbacks by MAC it displaces the live device's own registration.
func release(d device.Device) {
	if c, ok := d.(device.Closer); ok {
		_ = c.Close()
	}
}

func releaseAll(devices []device.Device) {
	for _, d := range devices {
		release(d)
	}
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

// persistWithGrant writes membership and — when an account added the device —
// that account's access to it in one update, so the two can never half-commit.
func (i *Inventory) persistWithGrant(specs []config.DeviceSpec, deviceID, grantTo string) error {
	if grantTo == "" || i.users == nil {
		return i.persist(specs)
	}
	return i.users.GrantWithState(grantTo, deviceID, func(state *store.State) error {
		state.Devices = specs
		return nil
	})
}

// persistWithPrunedGrants writes membership and access together. Device ids are
// MAC-derived and may return later, so a successful removal must never leave an
// old grant behind in either memory or the state file.
func (i *Inventory) persistWithPrunedGrants(specs []config.DeviceSpec) error {
	if i.users == nil {
		return i.persist(specs)
	}
	ids := make([]string, 0, len(specs))
	for _, spec := range specs {
		ids = append(ids, spec.ID)
	}
	return i.users.RetainDevicesAndUpdateState(ids, func(state *store.State) error {
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
