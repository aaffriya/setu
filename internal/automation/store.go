package automation

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"setu/internal/store"
)

// MaxStateBytes bounds both API input and the automation section on disk.
const MaxStateBytes = 256 * 1024

// Store persists the rule set as one section of Setu's shared state file. The
// engine owns the schema; the file (and its atomic write) is owned by
// internal/store, so saving rules can never disturb the device list stored
// beside them.
type Store struct{ file *store.Store }

// NewStore returns the automation view of the shared state file.
func NewStore(file *store.Store) *Store { return &Store{file: file} }

// Load returns the stored rule set, or an empty one when nothing is stored yet.
func (s *Store) Load() (State, error) {
	state := State{Version: FormatVersion, Items: []Rule{}}
	file, err := s.file.Load()
	if err != nil {
		return State{}, err
	}
	if len(file.Automations) == 0 {
		return state, nil
	}
	if len(file.Automations) > MaxStateBytes {
		return State{}, fmt.Errorf("automations: state is larger than 256 KB")
	}

	decoder := json.NewDecoder(bytes.NewReader(file.Automations))
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

// Save replaces the automation section, leaving every other section untouched.
func (s *Store) Save(state State) error {
	encoded, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("automations: encode state: %w", err)
	}
	if len(encoded) > MaxStateBytes {
		return fmt.Errorf("automations: state is larger than 256 KB")
	}
	return s.file.Update(func(file *store.State) error {
		file.Automations = encoded
		return nil
	})
}
