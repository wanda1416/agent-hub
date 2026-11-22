package quota

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const maxHistoryPerPool = 240

type historySample struct {
	UsedPercent float64    `json:"usedPercent"`
	ObservedAt  time.Time  `json:"observedAt"`
	ResetAt     *time.Time `json:"resetAt"`
}

type persistedState struct {
	Snapshot Snapshot                   `json:"snapshot"`
	History  map[string][]historySample `json:"history"`
	Alerted  map[string]bool            `json:"alerted"`
}

func loadState(path string) persistedState {
	state := persistedState{
		Snapshot: Snapshot{Pools: []Pool{}, Credits: []CreditBalance{}, Sources: []SourceStatus{}},
		History:  map[string][]historySample{},
		Alerted:  map[string]bool{},
	}
	if path == "" {
		return state
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return state
	}
	if err := json.Unmarshal(body, &state); err != nil {
		return persistedState{Snapshot: Snapshot{Pools: []Pool{}, Credits: []CreditBalance{}, Sources: []SourceStatus{}}, History: map[string][]historySample{}, Alerted: map[string]bool{}}
	}
	if state.Snapshot.Pools == nil {
		state.Snapshot.Pools = []Pool{}
	}
	if state.Snapshot.Credits == nil {
		state.Snapshot.Credits = []CreditBalance{}
	}
	if state.Snapshot.Sources == nil {
		state.Snapshot.Sources = []SourceStatus{}
	}
	if state.History == nil {
		state.History = map[string][]historySample{}
	}
	if state.Alerted == nil {
		state.Alerted = map[string]bool{}
	}
	return state
}

func writeState(path string, state persistedState) error {
	if path == "" {
		return nil
	}
	body, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func recordHistory(samples []historySample, pool Pool) []historySample {
	if pool.UsedPercent == nil || pool.ObservedAt == nil {
		return samples
	}
	current := historySample{UsedPercent: *pool.UsedPercent, ObservedAt: pool.ObservedAt.UTC(), ResetAt: pool.ResetAt}
	if len(samples) > 0 && samples[len(samples)-1].ObservedAt.Equal(current.ObservedAt) && sameReset(samples[len(samples)-1].ResetAt, current.ResetAt) {
		samples[len(samples)-1] = current
	} else {
		samples = append(samples, current)
	}
	sort.SliceStable(samples, func(i, j int) bool { return samples[i].ObservedAt.Before(samples[j].ObservedAt) })
	if len(samples) > maxHistoryPerPool {
		samples = samples[len(samples)-maxHistoryPerPool:]
	}
	return samples
}

func calculateRate(samples []historySample, pool Pool) (*float64, *time.Time) {
	if pool.UsedPercent == nil || pool.ObservedAt == nil {
		return nil, nil
	}
	matching := make([]historySample, 0, len(samples))
	for _, sample := range samples {
		if sameReset(sample.ResetAt, pool.ResetAt) {
			matching = append(matching, sample)
		}
	}
	if len(matching) < 2 {
		return nil, nil
	}
	first := matching[0]
	last := matching[len(matching)-1]
	hours := last.ObservedAt.Sub(first.ObservedAt).Hours()
	if last.ObservedAt.Equal(first.ObservedAt) || hours < 1.0/12.0 {
		return nil, nil
	}
	delta := last.UsedPercent - first.UsedPercent
	if delta <= 0 {
		return nil, nil
	}
	rate := delta / hours
	if rate <= 0 {
		return nil, nil
	}
	remaining := 100 - last.UsedPercent
	if remaining < 0 {
		remaining = 0
	}
	exhaustion := last.ObservedAt.Add(time.Duration(remaining / rate * float64(time.Hour)))
	if pool.ResetAt != nil && exhaustion.After(*pool.ResetAt) {
		return &rate, nil
	}
	return &rate, &exhaustion
}

func sameReset(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(*b)
}
