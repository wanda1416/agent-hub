package quota

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestManagerFailureRetainsStaleSnapshot(t *testing.T) {
	path := writeGrokSnapshot(t, `{
		"status":"complete",
		"fetched_at":"2026-11-22T10:00:00Z",
		"period":{"end":"2026-11-29T00:00:00Z"},
		"used_percent":20,
		"remaining_percent":80
	}`)
	manager := New(Options{Config: grokOnlyConfig(path), StatePath: filepath.Join(t.TempDir(), "state.json")})
	snapshot := manager.Refresh(context.Background())
	if len(snapshot.Pools) != 1 || snapshot.Pools[0].UsedPercent == nil || *snapshot.Pools[0].UsedPercent != 20 {
		t.Fatalf("unexpected initial snapshot: %+v", snapshot)
	}
	observedAt := snapshot.Pools[0].ObservedAt

	if err := os.WriteFile(path, []byte(`{"status":"failed","errors":{"grok":"upstream unavailable"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot = manager.Refresh(context.Background())
	if len(snapshot.Pools) != 1 || snapshot.Pools[0].UsedPercent == nil || *snapshot.Pools[0].UsedPercent != 20 {
		t.Fatalf("failed refresh cleared old usage: %+v", snapshot)
	}
	if snapshot.Pools[0].Status != "stale" || snapshot.Pools[0].Error == "" {
		t.Fatalf("failed refresh must mark old pool stale: %+v", snapshot.Pools[0])
	}
	if snapshot.Pools[0].ObservedAt == nil || !snapshot.Pools[0].ObservedAt.Equal(*observedAt) {
		t.Fatalf("upstream observation time changed during failure: %v", snapshot.Pools[0].ObservedAt)
	}
	if len(snapshot.Sources) != 1 || snapshot.Sources[0].Status != "stale" {
		t.Fatalf("source should retain stale status: %+v", snapshot.Sources)
	}
}

func TestManagerAlertDeduplicationSurvivesRestart(t *testing.T) {
	path := writeGrokSnapshot(t, `{
		"status":"complete",
		"fetched_at":"2026-11-22T10:00:00Z",
		"period":{"end":"2026-11-29T00:00:00Z"},
		"used_percent":85,
		"remaining_percent":15
	}`)
	statePath := filepath.Join(t.TempDir(), "state.json")
	alerts := 0
	manager := New(Options{Config: grokOnlyConfig(path), StatePath: statePath, OnAlert: func(items []Alert) { alerts += len(items) }})
	manager.Refresh(context.Background())
	manager.Refresh(context.Background())
	if alerts != 1 {
		t.Fatalf("same threshold must alert once, got %d", alerts)
	}

	restarted := New(Options{Config: grokOnlyConfig(path), StatePath: statePath, OnAlert: func(items []Alert) { alerts += len(items) }})
	restarted.Refresh(context.Background())
	if alerts != 1 {
		t.Fatalf("restored manager repeated an old alert: %d", alerts)
	}
}

func TestManagerDisableSourceRemovesPools(t *testing.T) {
	path := writeGrokSnapshot(t, `{
		"status":"complete",
		"fetched_at":"2026-11-22T10:00:00Z",
		"period":{"end":"2026-11-29T00:00:00Z"},
		"used_percent":20
	}`)
	cfg := grokOnlyConfig(path)
	manager := New(Options{Config: cfg})
	manager.Refresh(context.Background())
	cfg.Grok.Enabled = false
	manager.UpdateConfig(cfg)
	snapshot := manager.Snapshot()
	if len(snapshot.Pools) != 0 || len(snapshot.Sources) != 0 {
		t.Fatalf("disabled source must be removed from snapshot: %+v", snapshot)
	}
}

func TestDefaultConfigDoesNotEnableExternalSources(t *testing.T) {
	cfg := DefaultConfig()
	if !cfg.Enabled || cfg.IntervalMinutes != 5 {
		t.Fatalf("unexpected global defaults: %+v", cfg)
	}
	if cfg.Gemini.Enabled || cfg.Antigravity.Enabled || cfg.Grok.Enabled {
		t.Fatalf("external sources must be opt-in: %+v", cfg)
	}
}

func grokOnlyConfig(path string) Config {
	cfg := DefaultConfig()
	cfg.AlertThresholds = []int{80}
	cfg.Grok.Enabled = true
	cfg.Grok.SnapshotPath = path
	return cfg
}
