package automation

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const stateFileName = "setu-automations.json"

// MaxStateBytes bounds both API input and the on-disk automation file.
const MaxStateBytes = 256 * 1024

type Store struct{ path string }

func NewStore(path string) *Store { return &Store{path: path} }

// DefaultPath reuses the state directory already used by Samsung pairing
// tokens. The boolean reports whether the OS temporary-directory fallback is
// in use so the composition root can warn that it may not survive a reboot.
func DefaultPath() (string, bool) {
	dir := os.Getenv("SETU_STATE_DIR")
	temporary := dir == ""
	if temporary {
		dir = os.TempDir()
	}
	return filepath.Join(dir, stateFileName), temporary
}

func (s *Store) Load() (State, error) {
	state := State{Version: FormatVersion, Items: []Rule{}}
	f, err := os.Open(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return State{}, fmt.Errorf("automations: open state: %w", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return State{}, fmt.Errorf("automations: inspect state: %w", err)
	}
	if info.Size() > MaxStateBytes {
		return State{}, fmt.Errorf("automations: state is larger than 256 KB")
	}

	decoder := json.NewDecoder(f)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&state); err != nil {
		return State{}, fmt.Errorf("automations: decode state: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return State{}, fmt.Errorf("automations: state has trailing data")
	}
	if state.Items == nil {
		state.Items = []Rule{}
	}
	return state, nil
}

func (s *Store) Save(state State) error {
	var encoded bytes.Buffer
	if err := json.NewEncoder(&encoded).Encode(state); err != nil {
		return fmt.Errorf("automations: encode state: %w", err)
	}
	if encoded.Len() > MaxStateBytes {
		return fmt.Errorf("automations: state is larger than 256 KB")
	}

	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("automations: create state directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".setu-automations-*")
	if err != nil {
		return fmt.Errorf("automations: create temporary state: %w", err)
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
		return fmt.Errorf("automations: protect temporary state: %w", err)
	}
	if _, err := tmp.Write(encoded.Bytes()); err != nil {
		return fmt.Errorf("automations: write state: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("automations: sync state: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("automations: close state: %w", err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return fmt.Errorf("automations: replace state: %w", err)
	}
	ok = true
	return nil
}
