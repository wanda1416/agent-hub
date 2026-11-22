package process

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// RuntimeState records the operating-system identity of a child process so a
// restarted AgentHub can recognise and re-adopt it instead of spawning a
// duplicate. StartTime and ExePath guard against a reused PID: a plain PID is
// not enough to claim a process that belonged to a previous run.
type RuntimeState struct {
	ID        string    `json:"id"`
	PID       int       `json:"pid"`
	StartTime int64     `json:"startTime,omitempty"`
	ExePath   string    `json:"exePath,omitempty"`
	Command   string    `json:"command,omitempty"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// ProcessIdentity is the OS-level fingerprint used to verify that a persisted
// PID still belongs to the process that was recorded.
type ProcessIdentity struct {
	// StartTime is the platform-specific process creation timestamp. Persistent
	// PIDs are matched on it, so a reused PID is rejected instead of adopted.
	StartTime int64
	// ExePath is the absolute executable path, compared case-insensitively.
	ExePath string
}

// stateStore persists runtime state to a JSON file. The whole map is rewritten
// atomically on every change; the file is tiny and this avoids partial writes.
type stateStore struct {
	mu    sync.Mutex
	path  string
	items map[string]RuntimeState
}

func newStateStore(path string) *stateStore {
	s := &stateStore{
		path:  path,
		items: make(map[string]RuntimeState),
	}
	s.loadFromDisk()
	return s
}

func (s *stateStore) loadFromDisk() {
	if s.path == "" {
		return
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	var items map[string]RuntimeState
	if err := json.Unmarshal(data, &items); err != nil {
		return
	}
	if items != nil {
		s.items = items
	}
}

// snapshot returns a copy of the persisted entries.
func (s *stateStore) snapshot() map[string]RuntimeState {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make(map[string]RuntimeState, len(s.items))
	for id, item := range s.items {
		out[id] = item
	}
	return out
}

// put stores one entry and rewrites the file.
func (s *stateStore) put(state RuntimeState) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	s.items[state.ID] = state
	return s.persistLocked()
}

// delete drops one entry and rewrites the file.
func (s *stateStore) delete(id string) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.items[id]; !ok {
		return nil
	}
	delete(s.items, id)
	return s.persistLocked()
}

func (s *stateStore) persistLocked() error {
	if s.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(s.items, "", "  ")
	if err != nil {
		return err
	}

	tmp := fmt.Sprintf("%s.tmp-%d", s.path, os.Getpid())
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
