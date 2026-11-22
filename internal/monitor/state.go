package monitor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// maxSeenPerModel bounds the persisted dedupe history. Event IDs are never
// reused, so keeping the most recent few hundred is enough to survive restarts
// without letting the state file grow forever.
const maxSeenPerModel = 300

type persistedModel struct {
	Seen []string `json:"seen,omitempty"`
	ETag string   `json:"etag,omitempty"`
}

type persistedState struct {
	Models map[string]*persistedModel `json:"models"`
}

// stateStore persists the ETag cache and the set of already-notified events so
// a restart neither refetches unchanged data nor repeats old notifications.
type stateStore struct {
	mu   sync.Mutex
	path string
	data persistedState
}

func newStateStore(path string) *stateStore {
	store := &stateStore{
		path: path,
		data: persistedState{Models: map[string]*persistedModel{}},
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return store
	}
	var loaded persistedState
	if err := json.Unmarshal(raw, &loaded); err != nil {
		return store
	}
	if loaded.Models == nil {
		loaded.Models = map[string]*persistedModel{}
	}
	store.data = loaded
	return store
}

func (s *stateStore) model(id string) *persistedModel {
	entry, ok := s.data.Models[id]
	if !ok {
		entry = &persistedModel{}
		s.data.Models[id] = entry
	}
	return entry
}

func (s *stateStore) ETag(id string) string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.model(id).ETag
}

func (s *stateStore) SetETag(id, etag string) {
	if s == nil || etag == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.model(id).ETag = etag
}

// MarkSeen records an event ID and reports whether it was previously unknown.
func (s *stateStore) MarkSeen(id, eventID string) bool {
	if s == nil || eventID == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	entry := s.model(id)
	for _, seen := range entry.Seen {
		if seen == eventID {
			return false
		}
	}
	entry.Seen = append(entry.Seen, eventID)
	if len(entry.Seen) > maxSeenPerModel {
		entry.Seen = entry.Seen[len(entry.Seen)-maxSeenPerModel:]
	}
	return true
}

// Save writes the state file atomically. Errors are deliberately ignored by
// callers: failing to persist only risks a duplicate notification after the
// next restart, which is preferable to dropping a reset alert.
func (s *stateStore) Save() error {
	if s == nil || s.path == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	raw, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
