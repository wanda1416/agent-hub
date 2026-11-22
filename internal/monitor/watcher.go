package monitor

import (
	"context"
	"sync"
	"time"
)

// Options configures a Watcher.
type Options struct {
	Config    Config
	StatePath string
	Client    *Client
	OnAlert   func([]Alert)
	OnChange  func(Snapshot)
	Log       func(action, message string)
}

// Watcher polls the AIHOT API while the application runs, remembers which
// announcements were already reported, and calls OnAlert exactly once per new
// announced reset.
type Watcher struct {
	mu       sync.RWMutex
	cfg      Config
	client   *Client
	state    *stateStore
	snapshot Snapshot
	onAlert  func([]Alert)
	onChange func(Snapshot)
	log      func(action, message string)

	wake     chan struct{}
	stop     chan struct{}
	stopOnce sync.Once
	started  bool
}

// New creates a watcher. Call Start to begin polling.
func New(opts Options) *Watcher {
	cfg := normalizeConfig(opts.Config)
	w := &Watcher{
		cfg:      cfg,
		client:   opts.Client,
		state:    newStateStore(opts.StatePath),
		onAlert:  opts.OnAlert,
		onChange: opts.OnChange,
		log:      opts.Log,
		wake:     make(chan struct{}, 1),
		stop:     make(chan struct{}),
	}
	if w.client == nil {
		w.client = NewClient("")
	}
	if w.log == nil {
		w.log = func(string, string) {}
	}
	w.snapshot = Snapshot{
		Enabled:    cfg.Enabled,
		Interval:   cfg.Interval,
		AlertMode:  cfg.AlertMode,
		AlertSound: cfg.AlertSound,
		Models:     placeholderModels(cfg),
	}
	return w
}

func normalizeConfig(cfg Config) Config {
	if cfg.Interval < 5 || cfg.Interval > 120 {
		cfg.Interval = 10
	}
	switch cfg.AlertMode {
	case ModeInApp, ModeNotification, ModePopup:
	default:
		cfg.AlertMode = ModeNotification
	}
	return cfg
}

func placeholderModels(cfg Config) []ModelSnapshot {
	models := make([]ModelSnapshot, 0, len(Models))
	for _, model := range Models {
		followed := cfg.follows(model.ID)
		status := "disabled"
		if followed {
			status = ""
		}
		models = append(models, ModelSnapshot{
			ID:       model.ID,
			Name:     model.Name,
			PageURL:  model.PageURL,
			Followed: followed,
			Status:   status,
			Events:   []Event{},
		})
	}
	return models
}

// Start launches the polling loop. It returns immediately; the first poll runs
// in the background.
func (w *Watcher) Start(ctx context.Context) {
	w.mu.Lock()
	if w.started {
		w.mu.Unlock()
		return
	}
	w.started = true
	w.mu.Unlock()

	go w.run(ctx)
}

// Stop ends the polling loop. It is safe to call multiple times.
func (w *Watcher) Stop() {
	w.stopOnce.Do(func() { close(w.stop) })
}

// UpdateConfig replaces the polling configuration and wakes the loop so a new
// interval or model selection takes effect immediately.
func (w *Watcher) UpdateConfig(cfg Config) {
	cfg = normalizeConfig(cfg)
	w.mu.Lock()
	w.cfg = cfg
	w.snapshot.Enabled = cfg.Enabled
	w.snapshot.Interval = cfg.Interval
	w.snapshot.AlertMode = cfg.AlertMode
	w.snapshot.AlertSound = cfg.AlertSound
	w.mu.Unlock()
	w.publish()
	w.Refresh()
}

// Refresh asks the loop to poll again now instead of waiting for the interval.
func (w *Watcher) Refresh() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

// Snapshot returns the latest monitor state.
func (w *Watcher) Snapshot() Snapshot {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.snapshot
}

func (w *Watcher) config() Config {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.cfg
}

func (w *Watcher) run(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		cfg := w.config()
		if cfg.Enabled {
			w.poll(ctx)
		} else {
			w.publish()
		}

		timer := time.NewTimer(time.Duration(cfg.Interval) * time.Minute)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-w.stop:
			timer.Stop()
			return
		case <-w.wake:
			timer.Stop()
		}
	}
}

// poll performs one round over all models. It is exported indirectly through
// the loop; tests call it directly to avoid waiting on timers.
func (w *Watcher) poll(ctx context.Context) {
	cfg := w.config()
	previous := make(map[string]ModelSnapshot, len(w.Snapshot().Models))
	for _, snap := range w.Snapshot().Models {
		previous[snap.ID] = snap
	}

	snapshots := make([]ModelSnapshot, 0, len(Models))
	alerts := make([]Alert, 0, len(Models))

	for _, model := range Models {
		snap := ModelSnapshot{
			ID:       model.ID,
			Name:     model.Name,
			PageURL:  model.PageURL,
			Followed: cfg.follows(model.ID),
			Events:   []Event{},
		}
		prev := previous[model.ID]
		if !snap.Followed {
			snap.Status = "disabled"
			snapshots = append(snapshots, snap)
			continue
		}

		result, err := w.client.Fetch(ctx, model, w.state.ETag(model.ID))
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			snap.Status = "error"
			snap.Error = err.Error()
			// Keep the last successful events visible while the source is
			// unreachable; stale data is better than an empty panel.
			snap.Events = prev.Events
			snap.CheckedAt = prev.CheckedAt
			snap.SiteStatus = prev.SiteStatus
			snap.LastVerifiedAt = prev.LastVerifiedAt
			w.log("monitor.fetch", model.Name+": "+err.Error())
			snapshots = append(snapshots, snap)
			continue
		}

		snap.Status = "ok"
		if result.NotModified {
			snap.Events = prev.Events
			snap.CheckedAt = prev.CheckedAt
			snap.SiteStatus = prev.SiteStatus
			snap.LastVerifiedAt = prev.LastVerifiedAt
			snapshots = append(snapshots, snap)
			continue
		}

		w.state.SetETag(model.ID, result.ETag)
		events := make([]Event, 0, len(result.Events))
		newEvents := make([]Event, 0)
		for _, event := range result.Events {
			event.Active = event.IsActive()
			events = append(events, event)
			if event.Active && w.state.MarkSeen(model.ID, event.ID) {
				newEvents = append(newEvents, event)
			}
		}
		snap.Events = events
		snap.CheckedAt = result.CheckedAt
		if result.Monitor != nil {
			snap.SiteStatus = result.Monitor.Status
			if result.Monitor.LastVerifiedAt != nil {
				snap.LastVerifiedAt = *result.Monitor.LastVerifiedAt
			}
		}
		if len(newEvents) > 0 {
			alerts = append(alerts, Alert{
				ModelID:   model.ID,
				ModelName: model.Name,
				PageURL:   model.PageURL,
				Events:    newEvents,
			})
		}
		snapshots = append(snapshots, snap)
	}

	if err := w.state.Save(); err != nil {
		w.log("monitor.state", "failed to persist monitor state: "+err.Error())
	}

	w.mu.Lock()
	w.snapshot = Snapshot{
		Enabled:    cfg.Enabled,
		Interval:   cfg.Interval,
		AlertMode:  cfg.AlertMode,
		AlertSound: cfg.AlertSound,
		LastPoll:   time.Now().Format(time.RFC3339),
		Models:     snapshots,
	}
	snapshot := w.snapshot
	w.mu.Unlock()

	if w.onChange != nil {
		w.onChange(snapshot)
	}
	if len(alerts) > 0 && w.onAlert != nil {
		w.onAlert(alerts)
	}
}

// publish re-emits the current snapshot (used when monitoring is toggled off).
func (w *Watcher) publish() {
	w.mu.Lock()
	snapshot := w.snapshot
	w.mu.Unlock()
	if w.onChange != nil {
		w.onChange(snapshot)
	}
}
