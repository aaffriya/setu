// Package store owns Setu's state file: one small JSON document holding the
// devices the user added and the automation rules, written atomically.
//
// It is the only persistent state Setu has. Server settings come from the
// environment (internal/config) and everything else is code, so this file plus
// the environment fully describe an installation — which is what makes backup
// and restore a copy of two things, not a filesystem tour.
//
// The store knows the shape of the file, not the meaning of its sections: the
// device list is config.DeviceSpec (validated by config), and the automation
// section stays raw JSON owned by internal/automation. Both are written through
// Update, so a change to one section can never lose the other.
package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"setu/internal/config"
)

const (
	stateFileName = "setu.json"
	// legacyAutomationFileName is where automations lived before devices moved
	// into the state file. See Load.
	legacyAutomationFileName = "setu-automations.json"

	// FormatVersion is the state file's schema version.
	FormatVersion = 1

	// MaxBytes bounds the whole file. The automation section is separately
	// capped at 256 KB where it enters the API; the rest is headroom for the
	// device list, which is tiny (a spec is ~150 bytes and devices are bounded).
	MaxBytes = 384 * 1024
)

// State is the complete contents of the state file.
type State struct {
	Version int                 `json:"version"`
	Devices []config.DeviceSpec `json:"devices"`
	// Automations is opaque here: internal/automation owns that schema.
	Automations json.RawMessage `json:"automations,omitempty"`
}

// Store reads and writes the state file. It is safe for concurrent use: every
// write is a read-modify-write of the whole document under one lock.
type Store struct {
	path string
	mu   sync.Mutex
}

// New returns a Store for the file at path.
func New(path string) *Store { return &Store{path: path} }

// DefaultPath returns the state file path, reusing the state directory already
// used by Samsung pairing tokens. The boolean reports whether the OS
// temporary-directory fallback is in use, so the composition root can warn that
// state may not survive a reboot.
func DefaultPath() (string, bool) {
	dir := os.Getenv("SETU_STATE_DIR")
	temporary := dir == ""
	if temporary {
		dir = os.TempDir()
	}
	return filepath.Join(dir, stateFileName), temporary
}

// Load reads the state file. A missing file is an empty installation, not an
// error — that is a first run.
func (s *Store) Load() (State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadLocked()
}

func (s *Store) loadLocked() (State, error) {
	state := State{Version: FormatVersion, Devices: []config.DeviceSpec{}}
	data, err := read(s.path)
	if errors.Is(err, os.ErrNotExist) {
		// One-time upgrade: automations used to have their own file next door.
		// Adopt it so rules survive, and let the next write produce the merged
		// file. Removable once no installation predates the state file.
		//
		// The bytes are checked first: an unparseable section would make every
		// later write fail (json.Marshal refuses an invalid RawMessage), which
		// would block adding a device over a corrupt file the user no longer
		// even uses. A broken legacy file is simply not adopted.
		if legacy, err := read(filepath.Join(filepath.Dir(s.path), legacyAutomationFileName)); err == nil && json.Valid(legacy) {
			state.Automations = legacy
		}
		return state, nil
	}
	if err != nil {
		return State{}, err
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&state); err != nil {
		return State{}, fmt.Errorf("store: decode state: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return State{}, fmt.Errorf("store: state has trailing data")
	}
	if state.Devices == nil {
		state.Devices = []config.DeviceSpec{}
	}
	return state, nil
}

// Update applies mutate to the current state and writes the result. The whole
// file is rewritten atomically, so a failure leaves the previous file intact.
func (s *Store) Update(mutate func(*State) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.loadLocked()
	if err != nil {
		return err
	}
	if err := mutate(&state); err != nil {
		return err
	}
	state.Version = FormatVersion
	return s.save(state)
}

func read(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("store: inspect %s: %w", path, err)
	}
	if info.Size() > MaxBytes {
		return nil, fmt.Errorf("store: %s is larger than %d KB", path, MaxBytes/1024)
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, fmt.Errorf("store: read %s: %w", path, err)
	}
	return data, nil
}

// save writes the state through a temporary file and one rename, so a crash or
// a full disk can never leave a half-written state file behind.
func (s *Store) save(state State) error {
	var encoded bytes.Buffer
	if err := json.NewEncoder(&encoded).Encode(state); err != nil {
		return fmt.Errorf("store: encode state: %w", err)
	}
	if encoded.Len() > MaxBytes {
		return fmt.Errorf("store: state is larger than %d KB", MaxBytes/1024)
	}

	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("store: create state directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".setu-state-*")
	if err != nil {
		return fmt.Errorf("store: create temporary state: %w", err)
	}
	tmpName := tmp.Name()
	ok := false
	defer func() {
		_ = tmp.Close()
		if !ok {
			_ = os.Remove(tmpName)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return fmt.Errorf("store: protect temporary state: %w", err)
	}
	if _, err := tmp.Write(encoded.Bytes()); err != nil {
		return fmt.Errorf("store: write state: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("store: sync state: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("store: close state: %w", err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return fmt.Errorf("store: replace state: %w", err)
	}
	ok = true
	return nil
}
