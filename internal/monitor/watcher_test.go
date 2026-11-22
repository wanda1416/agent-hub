package monitor

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

const announcedPayload = `{
  "checkedAt": "2026-11-22T15:50:00+08:00",
  "count": 1,
  "events": [
    {
      "id": "evt-1",
      "type": "direct_reset",
      "label": "全员重置",
      "displayLabel": "全员重置（来源未说明适用范围）",
      "presentation": {
        "status": "announced",
        "scopeKnown": true,
        "scopeLabel": "所有付费用户",
        "timeInferred": false
      },
      "estimate": {
        "from": "2026-11-23T08:30:00+08:00",
        "through": "2026-11-23T13:30:00+08:00",
        "basis": "model",
        "label": "11-23 08:30–13:30",
        "reason": ""
      },
      "status": "announced",
      "title": "Codex 额度重置",
      "scope": "所有付费用户",
      "posts": [{"author": "thsottiaux", "text": "重置已宣布", "url": "https://x.com/i/status/1"}],
      "url": "https://aihot.news/codex-reset"
    }
  ],
  "monitor": {"status": "healthy", "lastVerifiedAt": "2026-11-22T15:50:00+08:00"}
}`

func newTestWatcher(t *testing.T, handler http.HandlerFunc, cfg Config, statePath string) (*Watcher, *[]Alert) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	alerts := []Alert{}
	watcher := New(Options{
		Config:    cfg,
		StatePath: statePath,
		Client:    NewClient(server.URL),
		OnAlert: func(a []Alert) {
			alerts = append(alerts, a...)
		},
	})
	return watcher, &alerts
}

func TestPollNotifiesOncePerAnnouncement(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "monitor-state.json")
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(announcedPayload))
	}
	cfg := Config{Enabled: true, Interval: 10, Models: []string{"codex"}}
	watcher, alerts := newTestWatcher(t, handler, cfg, statePath)

	watcher.poll(context.Background())
	if len(*alerts) != 1 {
		t.Fatalf("expected 1 alert, got %d", len(*alerts))
	}
	if (*alerts)[0].ModelID != "codex" || len((*alerts)[0].Events) != 1 {
		t.Fatalf("unexpected alert: %+v", (*alerts)[0])
	}

	watcher.poll(context.Background())
	if len(*alerts) != 1 {
		t.Fatalf("expected the announcement to be deduped, got %d alerts", len(*alerts))
	}

	// A new watcher sharing the state file must not repeat the notification.
	restarted, restartedAlerts := newTestWatcher(t, handler, cfg, statePath)
	restarted.poll(context.Background())
	if len(*restartedAlerts) != 0 {
		t.Fatalf("expected no alert after restart, got %d", len(*restartedAlerts))
	}

	snapshot := restarted.Snapshot()
	if len(snapshot.Models) != len(Models) {
		t.Fatalf("expected %d model snapshots, got %d", len(Models), len(snapshot.Models))
	}
	var codex ModelSnapshot
	for _, model := range snapshot.Models {
		if model.ID == "codex" {
			codex = model
		}
	}
	if codex.Status != "ok" || len(codex.Events) != 1 || !codex.Events[0].Active {
		t.Fatalf("unexpected codex snapshot: %+v", codex)
	}
}

func TestPollKeepsEventsOnNotModified(t *testing.T) {
	var requests atomic.Int32
	handler := func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("If-None-Match") == `"v1"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"v1"`)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(announcedPayload))
	}
	watcher, _ := newTestWatcher(t, handler,
		Config{Enabled: true, Interval: 10, Models: []string{"codex"}},
		filepath.Join(t.TempDir(), "state.json"))

	watcher.poll(context.Background())
	first := watcher.Snapshot()
	watcher.poll(context.Background())
	second := watcher.Snapshot()

	if requests.Load() != 2 {
		t.Fatalf("expected 2 requests, got %d", requests.Load())
	}
	if len(second.Models[0].Events) != len(first.Models[0].Events) {
		t.Fatalf("304 should keep the previous events")
	}
}

func TestPollOnlyFollowsConfiguredModels(t *testing.T) {
	var codexHits atomic.Int32
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/codex-resets/recent" {
			codexHits.Add(1)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"checkedAt":"","count":0,"events":[],"monitor":{"status":"healthy"}}`))
	}
	watcher, _ := newTestWatcher(t, handler,
		Config{Enabled: true, Interval: 10, Models: []string{"claude"}},
		filepath.Join(t.TempDir(), "state.json"))

	watcher.poll(context.Background())
	if codexHits.Load() != 0 {
		t.Fatalf("codex must not be polled when it is not followed")
	}
	snapshot := watcher.Snapshot()
	for _, model := range snapshot.Models {
		if model.ID == "codex" && (model.Followed || model.Status != "disabled") {
			t.Fatalf("unfollowed model should be disabled: %+v", model)
		}
		if model.ID == "claude" && !model.Followed {
			t.Fatalf("claude should be followed")
		}
	}
}

func TestEventIsActive(t *testing.T) {
	cases := []struct {
		name   string
		event  Event
		active bool
	}{
		{"announced", Event{Presentation: &Presentation{Status: "announced"}}, true},
		{"in progress", Event{Presentation: &Presentation{Status: "in_progress"}}, true},
		{"confirmed", Event{Status: "announced", Presentation: &Presentation{Status: "confirmed"}}, false},
		{"expired", Event{Status: "announced", Presentation: &Presentation{Status: "expired_unconfirmed"}}, false},
		{"legacy announced", Event{Status: "announced"}, true},
		{"legacy confirmed", Event{Status: "confirmed"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.event.IsActive(); got != tc.active {
				t.Fatalf("IsActive()=%v, want %v", got, tc.active)
			}
		})
	}
}

func TestPollReportsHTTPError(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
	}
	watcher, alerts := newTestWatcher(t, handler,
		Config{Enabled: true, Interval: 10, Models: []string{"codex"}},
		filepath.Join(t.TempDir(), "state.json"))

	watcher.poll(context.Background())
	if len(*alerts) != 0 {
		t.Fatalf("HTTP errors must not produce alerts")
	}
	model := watcher.Snapshot().Models[0]
	if model.Status != "error" || model.Error == "" {
		t.Fatalf("expected an error snapshot, got %+v", model)
	}
}

func TestStateFileIsPersisted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	watcher, _ := newTestWatcher(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"abc"`)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(announcedPayload))
	}, Config{Enabled: true, Interval: 10, Models: []string{"codex"}}, path)

	watcher.poll(context.Background())
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("state file not written: %v", err)
	}
	var state persistedState
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatalf("invalid state file: %v", err)
	}
	entry, ok := state.Models["codex"]
	if !ok || entry.ETag != `"abc"` || len(entry.Seen) != 1 {
		t.Fatalf("unexpected persisted state: %+v", state)
	}
}
