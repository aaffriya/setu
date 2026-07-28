// Package users owns the people who may use this installation besides the
// administrator.
//
// The admin is not stored here: they are whoever presents SETU_TOKEN, which
// comes from the environment and can therefore never be locked out by anything
// written to disk. Everyone else is created in the app, gets a token Setu
// generates, and is limited to the devices they were explicitly granted plus one
// of two roles:
//
//   - "read": control the devices they can see, and nothing else.
//   - "modify": additionally add devices and write automations — still only for
//     the devices they can see.
//
// Only the SHA-256 of a token is stored. The plaintext exists once, in the
// response that created or rotated it, exactly like an automation webhook token.
package users

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"setu/internal/store"
)

const (
	// FormatVersion is the schema of the users section of the state file.
	FormatVersion = 1

	// MaxUsers bounds a household. Authentication compares a presented token
	// against every stored hash, so this is also what keeps that loop cheap.
	MaxUsers = 32

	// MaxNameLength bounds the one field a person types.
	MaxNameLength = 32

	// maxDeviceGrants matches inventory.MaxDevices: a user cannot be granted more
	// devices than an installation can hold. Duplicated rather than imported so
	// this package does not depend on the device inventory.
	maxDeviceGrants = 64

	// MaxStateBytes bounds the section on disk. A user is ~200 bytes.
	MaxStateBytes = 32 * 1024
)

// Roles. A role is global; device access is a separate, per-user list.
const (
	// RoleRead may control the devices it can see. It is not read-only in the
	// HTTP sense — being able to switch the lights you were given is the point —
	// but it may not change what the installation *is*.
	RoleRead = "read"
	// RoleModify may additionally add devices and write automations.
	RoleModify = "modify"
)

// TokenPrefix marks a Setu-issued user token, so a person pasting one into the
// app can see what it is.
const TokenPrefix = "setu_user_"

// Errors the API maps to status codes.
var (
	ErrNotFound = fmt.Errorf("unknown user")
	ErrFull     = fmt.Errorf("at most %d users", MaxUsers)
)

// User is one person who may sign in. TokenHash is the only secret material and
// never leaves this package except through Export.
type User struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Role string `json:"role"`
	// Devices lists the device ids this user may see and control. A new device
	// is granted to nobody: access is always explicit.
	Devices   []string `json:"devices"`
	TokenHash string   `json:"token_hash,omitempty"`
	CreatedAt int64    `json:"created_at,omitempty"`
}

// CanSee reports whether this user may see and control a device.
func (u User) CanSee(deviceID string) bool {
	for _, id := range u.Devices {
		if id == deviceID {
			return true
		}
	}
	return false
}

// CanModify reports whether this user may change the installation — add devices,
// write automations — rather than only operate the devices they were given.
func (u User) CanModify() bool { return u.Role == RoleModify }

// Public returns the user without its token hash, for API responses.
func (u User) Public() User {
	u.TokenHash = ""
	u.Devices = append([]string(nil), u.Devices...)
	if u.Devices == nil {
		u.Devices = []string{}
	}
	return u
}

// State is the persisted users section.
type State struct {
	Version int    `json:"version"`
	Items   []User `json:"items"`
}

// Registry holds the users and persists them in the shared state file.
type Registry struct {
	file *store.Store

	mu    sync.RWMutex
	items []User
}

// New loads the stored users. A missing section is an installation that has only
// ever had its administrator, which is the normal case.
func New(file *store.Store) (*Registry, error) {
	state, err := load(file)
	if err != nil {
		return nil, err
	}
	return &Registry{file: file, items: state.Items}, nil
}

func load(file *store.Store) (State, error) {
	state := State{Version: FormatVersion, Items: []User{}}
	raw, err := file.Load()
	if err != nil {
		return State{}, err
	}
	if len(raw.Users) == 0 {
		return state, nil
	}
	if len(raw.Users) > MaxStateBytes {
		return State{}, fmt.Errorf("users: state is larger than %d KB", MaxStateBytes/1024)
	}
	if err := json.Unmarshal(raw.Users, &state); err != nil {
		return State{}, fmt.Errorf("users: decode state: %w", err)
	}
	if state.Version != FormatVersion {
		return State{}, fmt.Errorf("users: unsupported version %d (want %d)", state.Version, FormatVersion)
	}
	if state.Items == nil {
		state.Items = []User{}
	}
	for i := range state.Items {
		// The file is editable and an id becomes a URL segment. Anything that
		// could not have been written by Setu is a hand edit, and letting it
		// through would give it an authentication path.
		if err := validate(state.Items[i]); err != nil {
			return State{}, fmt.Errorf("users: %w", err)
		}
	}
	return state, nil
}

// List returns every user, without token hashes, in creation order.
func (r *Registry) List() []User {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]User, 0, len(r.items))
	for _, user := range r.items {
		out = append(out, user.Public())
	}
	return out
}

// Get returns one user without its token hash.
func (r *Registry) Get(id string) (User, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	index := r.indexOf(id)
	if index < 0 {
		return User{}, false
	}
	return r.items[index].Public(), true
}

// Count reports how many users exist. The API uses it to decide whether this
// installation has anyone besides its administrator.
func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.items)
}

// Create adds a user and returns them together with their token. The token is
// returned exactly once — only its hash is stored — so the caller must hand it
// to the person now or rotate it later.
func (r *Registry) Create(name, role string, devices []string) (User, string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.items) >= MaxUsers {
		return User{}, "", ErrFull
	}
	user := User{
		Name:      strings.TrimSpace(name),
		Role:      role,
		Devices:   normalizeDevices(devices),
		CreatedAt: time.Now().Unix(),
	}
	user.ID = suggestID(user.Name, r.takenIDs())
	token, hash, err := newToken()
	if err != nil {
		return User{}, "", err
	}
	user.TokenHash = hash
	if err := validate(user); err != nil {
		return User{}, "", err
	}
	next := append(append([]User(nil), r.items...), user)
	if err := r.persist(next); err != nil {
		return User{}, "", err
	}
	r.items = next
	return user.Public(), token, nil
}

// Patch is the editable part of a user. A nil field is left as it is, so the
// screen can save a renamed user without resending their device grants.
type Patch struct {
	Name    *string
	Role    *string
	Devices *[]string
}

// Update edits a user. Their token is untouched: changing what someone may do
// must not sign them out.
func (r *Registry) Update(id string, patch Patch) (User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	index := r.indexOf(id)
	if index < 0 {
		return User{}, ErrNotFound
	}
	user := r.items[index]
	if patch.Name != nil {
		user.Name = strings.TrimSpace(*patch.Name)
	}
	if patch.Role != nil {
		user.Role = *patch.Role
	}
	if patch.Devices != nil {
		user.Devices = normalizeDevices(*patch.Devices)
	}
	if err := validate(user); err != nil {
		return User{}, err
	}
	next := append([]User(nil), r.items...)
	next[index] = user
	if err := r.persist(next); err != nil {
		return User{}, err
	}
	r.items = next
	return user.Public(), nil
}

// Delete removes a user, which immediately invalidates their token.
func (r *Registry) Delete(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	index := r.indexOf(id)
	if index < 0 {
		return ErrNotFound
	}
	next := append(append([]User(nil), r.items[:index]...), r.items[index+1:]...)
	if err := r.persist(next); err != nil {
		return err
	}
	r.items = next
	return nil
}

// RotateToken issues a new token and invalidates the previous one. Like Create,
// the plaintext is returned once.
func (r *Registry) RotateToken(id string) (User, string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	index := r.indexOf(id)
	if index < 0 {
		return User{}, "", ErrNotFound
	}
	token, hash, err := newToken()
	if err != nil {
		return User{}, "", err
	}
	user := r.items[index]
	user.TokenHash = hash
	next := append([]User(nil), r.items...)
	next[index] = user
	if err := r.persist(next); err != nil {
		return User{}, "", err
	}
	r.items = next
	return user.Public(), token, nil
}

// Grant gives a user access to one more device. It is how the person who added a
// device keeps being able to use it: everything else has to be granted by the
// administrator on purpose.
func (r *Registry) Grant(id, deviceID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	index := r.indexOf(id)
	if index < 0 {
		return ErrNotFound
	}
	user := r.items[index]
	if user.CanSee(deviceID) {
		return nil
	}
	if len(user.Devices) >= maxDeviceGrants {
		return fmt.Errorf("a user may be given at most %d devices", maxDeviceGrants)
	}
	user.Devices = append(append([]string(nil), user.Devices...), deviceID)
	next := append([]User(nil), r.items...)
	next[index] = user
	if err := r.persist(next); err != nil {
		return err
	}
	r.items = next
	return nil
}

// ForgetDevice drops a deleted device from every user's grants, so removing and
// re-adding hardware with the same id cannot silently restore old access.
func (r *Registry) ForgetDevice(deviceID string) error {
	return r.prune(func(id string) bool { return id != deviceID })
}

// RetainDevices drops every grant naming a device that is not in keep. It is the
// restore path: replacing the whole inventory removes devices without deleting
// them one by one, and a grant left pointing at an id that comes back later
// would silently restore access nobody re-granted.
func (r *Registry) RetainDevices(keep []string) error {
	wanted := make(map[string]struct{}, len(keep))
	for _, id := range keep {
		wanted[id] = struct{}{}
	}
	return r.prune(func(id string) bool {
		_, ok := wanted[id]
		return ok
	})
}

// prune rewrites every user's grants, keeping the ids that pass. It persists
// only when something actually changed.
func (r *Registry) prune(keep func(deviceID string) bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	next := append([]User(nil), r.items...)
	changed := false
	for i := range next {
		kept := make([]string, 0, len(next[i].Devices))
		for _, id := range next[i].Devices {
			if keep(id) {
				kept = append(kept, id)
			}
		}
		if len(kept) == len(next[i].Devices) {
			continue
		}
		next[i].Devices = kept
		changed = true
	}
	if !changed {
		return nil
	}
	if err := r.persist(next); err != nil {
		return err
	}
	r.items = next
	return nil
}

// Authenticate resolves a presented token to its user.
//
// It hashes the candidate once and then compares that digest against every
// stored hash in constant time, without stopping at the first match, so neither
// the response time nor the number of comparisons reveals which user — or
// whether any user — a token belongs to.
func (r *Registry) Authenticate(token string) (User, bool) {
	if len(token) < 16 || len(token) > 128 {
		return User{}, false
	}
	got := sha256.Sum256([]byte(token))

	r.mu.RLock()
	defer r.mu.RUnlock()
	match := -1
	for i, user := range r.items {
		decoded, err := hex.DecodeString(user.TokenHash)
		if err != nil || len(decoded) != len(got) {
			continue
		}
		if subtle.ConstantTimeCompare(got[:], decoded) == 1 {
			match = i
		}
	}
	if match < 0 {
		return User{}, false
	}
	// A copy, not the stored value: the caller keeps this after the lock is
	// released, and handing out the live grant slice would let a future in-place
	// edit here change what an already-authenticated request may see.
	return r.items[match].Public(), true
}

func (r *Registry) persist(items []User) error {
	encoded, err := json.Marshal(State{Version: FormatVersion, Items: items})
	if err != nil {
		return fmt.Errorf("users: encode state: %w", err)
	}
	if len(encoded) > MaxStateBytes {
		return fmt.Errorf("users: state is larger than %d KB", MaxStateBytes/1024)
	}
	return r.file.Update(func(state *store.State) error {
		state.Users = encoded
		return nil
	})
}

func (r *Registry) indexOf(id string) int {
	for i, user := range r.items {
		if user.ID == id {
			return i
		}
	}
	return -1
}

func (r *Registry) takenIDs() map[string]struct{} {
	taken := make(map[string]struct{}, len(r.items))
	for _, user := range r.items {
		taken[user.ID] = struct{}{}
	}
	return taken
}

// newToken returns the plaintext token and the hash to store.
func newToken() (string, string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "", fmt.Errorf("users: generate token: %w", err)
	}
	token := TokenPrefix + base64.RawURLEncoding.EncodeToString(raw)
	digest := sha256.Sum256([]byte(token))
	return token, hex.EncodeToString(digest[:]), nil
}

// validate is the single gate for anything that reaches the state file. Its
// messages are shown to the administrator who just typed the value.
func validate(u User) error {
	if !validID(u.ID) {
		return fmt.Errorf("user id %q is not valid", u.ID)
	}
	if u.Name == "" {
		return fmt.Errorf("a name is required")
	}
	if len(u.Name) > MaxNameLength {
		return fmt.Errorf("the name is longer than %d characters", MaxNameLength)
	}
	if u.Role != RoleRead && u.Role != RoleModify {
		return fmt.Errorf("role must be %q or %q", RoleRead, RoleModify)
	}
	if len(u.Devices) > maxDeviceGrants {
		return fmt.Errorf("a user may be given at most %d devices", maxDeviceGrants)
	}
	seen := make(map[string]struct{}, len(u.Devices))
	for _, id := range u.Devices {
		if id == "" {
			return fmt.Errorf("a device id is empty")
		}
		if _, duplicate := seen[id]; duplicate {
			return fmt.Errorf("device %q is listed twice", id)
		}
		seen[id] = struct{}{}
	}
	decoded, err := hex.DecodeString(u.TokenHash)
	if err != nil || len(decoded) != sha256.Size {
		return fmt.Errorf("user %q has no usable token", u.ID)
	}
	return nil
}

func validID(id string) bool {
	if id == "" || len(id) > 32 {
		return false
	}
	for i, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
		case (r == '_' || r == '-') && i > 0:
		default:
			return false
		}
	}
	return true
}

// normalizeDevices trims and de-duplicates a grant list while keeping the order
// the administrator chose.
func normalizeDevices(devices []string) []string {
	out := make([]string, 0, len(devices))
	seen := make(map[string]struct{}, len(devices))
	for _, id := range devices {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, duplicate := seen[id]; duplicate {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

// suggestID builds a readable id from the person's name, the way device ids are
// derived from a brand and MAC. A name with nothing usable in it (or a clash)
// still produces a stable, unique id.
func suggestID(name string, taken map[string]struct{}) string {
	base := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			return r
		case r >= 'A' && r <= 'Z':
			return r + ('a' - 'A')
		default:
			return -1
		}
	}, name)
	if len(base) > 24 {
		base = base[:24]
	}
	if base == "" {
		base = "user"
	}
	id := base
	for suffix := 2; ; suffix++ {
		if _, clash := taken[id]; !clash {
			return id
		}
		id = fmt.Sprintf("%s_%d", base, suffix)
	}
}
