package quota

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

type Options struct {
	Config    Config
	StatePath string
	Client    *http.Client
	OnChange  func(Snapshot)
	OnAlert   func([]Alert)
	Log       func(action, message string)
}

type Manager struct {
	mu        sync.RWMutex
	refreshMu sync.Mutex
	cfg       Config
	statePath string
	client    *http.Client
	onChange  func(Snapshot)
	onAlert   func([]Alert)
	log       func(action, message string)
	snapshot  Snapshot
	history   map[string][]historySample
	alerted   map[string]bool
	wake      chan struct{}
	cancel    context.CancelFunc
	done      chan struct{}
	running   bool
}

func New(options Options) *Manager {
	client := options.Client
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	state := loadState(options.StatePath)
	cfg := NormalizeConfig(options.Config)
	manager := &Manager{
		cfg:       cfg,
		statePath: options.StatePath,
		client:    client,
		onChange:  options.OnChange,
		onAlert:   options.OnAlert,
		log:       options.Log,
		snapshot:  state.Snapshot,
		history:   state.History,
		alerted:   state.Alerted,
		wake:      make(chan struct{}, 1),
	}
	manager.snapshot.Enabled = cfg.Enabled
	manager.snapshot.IntervalMinutes = cfg.IntervalMinutes
	manager.removeDisabledSourcesLocked(cfg)
	return manager
}

func (m *Manager) Start(ctx context.Context) {
	m.mu.Lock()
	if m.running {
		m.mu.Unlock()
		return
	}
	runCtx, cancel := context.WithCancel(ctx)
	m.cancel = cancel
	m.done = make(chan struct{})
	m.running = true
	done := m.done
	m.mu.Unlock()

	go func() {
		defer close(done)
		for {
			m.poll(runCtx, false)
			timer := time.NewTimer(m.interval())
			select {
			case <-runCtx.Done():
				if !timer.Stop() {
					<-timer.C
				}
				return
			case <-timer.C:
			case <-m.wake:
				if !timer.Stop() {
					<-timer.C
				}
			}
		}
	}()
}

func (m *Manager) Stop() {
	m.mu.Lock()
	if !m.running {
		m.mu.Unlock()
		return
	}
	cancel := m.cancel
	done := m.done
	m.running = false
	m.mu.Unlock()
	cancel()
	<-done
}

func (m *Manager) UpdateConfig(cfg Config) {
	cfg = NormalizeConfig(cfg)
	m.mu.Lock()
	m.cfg = cfg
	m.snapshot.Enabled = cfg.Enabled
	m.snapshot.IntervalMinutes = cfg.IntervalMinutes
	m.removeDisabledSourcesLocked(cfg)
	m.persistLocked()
	snapshot := m.snapshotLocked()
	onChange := m.onChange
	m.mu.Unlock()
	if onChange != nil {
		onChange(snapshot)
	}
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

func (m *Manager) Snapshot() Snapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.snapshotLocked()
}

func (m *Manager) Refresh(ctx context.Context) Snapshot {
	return m.poll(ctx, true)
}

func (m *Manager) interval() time.Duration {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return time.Duration(m.cfg.IntervalMinutes) * time.Minute
}

func (m *Manager) poll(ctx context.Context, active bool) Snapshot {
	m.refreshMu.Lock()
	defer m.refreshMu.Unlock()

	m.mu.RLock()
	cfg := m.cfg
	m.mu.RUnlock()
	if !cfg.Enabled {
		return m.Snapshot()
	}

	type outcome struct {
		id     string
		result collection
	}
	outcomes := make([]outcome, 0, 3)
	for _, id := range []string{SourceGemini, SourceAntigravity, SourceGrok} {
		if !sourceEnabled(cfg, id) {
			continue
		}
		var result collection
		switch id {
		case SourceGemini:
			result = collectGemini(ctx, cfg.Gemini, m.client, active)
		case SourceAntigravity:
			result = collectAntigravity(ctx, cfg.Antigravity, m.client, active)
		case SourceGrok:
			result = collectGrok(cfg.Grok)
		}
		outcomes = append(outcomes, outcome{id: id, result: result})
	}

	now := time.Now().UTC()
	m.mu.Lock()
	m.snapshot.LastPoll = &now
	m.snapshot.Enabled = cfg.Enabled
	m.snapshot.IntervalMinutes = cfg.IntervalMinutes
	m.removeDisabledSourcesLocked(cfg)
	alerts := make([]Alert, 0)
	for _, outcome := range outcomes {
		oldPools := poolsForProvider(m.snapshot.Pools, outcome.id)
		oldSource, hadSource := sourceForID(m.snapshot.Sources, outcome.id)
		m.snapshot.Pools = removePoolsForProvider(m.snapshot.Pools, outcome.id)
		status := SourceStatus{
			ID:        outcome.id,
			Provider:  outcome.id,
			Name:      sourceName(outcome.id),
			CheckedAt: &now,
		}
		if outcome.result.err != nil {
			status.Error = outcome.result.err.Error()
			status.Status = "error"
			if hadSource || len(oldPools) > 0 {
				status.Status = "stale"
			}
			if hadSource {
				status.LastSuccessAt = oldSource.LastSuccessAt
				status.Detail = oldSource.Detail
			}
			for _, pool := range oldPools {
				pool.Status = "stale"
				pool.Error = status.Error
				m.snapshot.Pools = append(m.snapshot.Pools, pool)
			}
			if m.log != nil {
				m.log("quota."+outcome.id, status.Error)
			}
		} else {
			status.Status = outcome.result.status
			if status.Status == "" {
				status.Status = "ok"
			}
			status.LastSuccessAt = &now
			status.Detail = outcome.result.detail
			for _, pool := range outcome.result.pools {
				if isShortWindow(pool.WindowKind) {
					delete(m.history, pool.ID)
					continue
				}
				if pool.Status == "" {
					pool.Status = "ok"
				}
				if pool.Products == nil {
					pool.Products = []ProductUsage{}
				}
				m.history[pool.ID] = recordHistory(m.history[pool.ID], pool)
				pool.BurnRatePerHour, pool.EstimatedExhaustionAt = calculateRate(m.history[pool.ID], pool)
				m.snapshot.Pools = append(m.snapshot.Pools, pool)
				alerts = append(alerts, m.alertsForPoolLocked(pool, cfg.AlertThresholds)...)
			}
		}
		m.snapshot.Sources = removeSourceForID(m.snapshot.Sources, outcome.id)
		m.snapshot.Sources = append(m.snapshot.Sources, status)
	}
	m.persistLocked()
	snapshot := m.snapshotLocked()
	onChange := m.onChange
	onAlert := m.onAlert
	m.mu.Unlock()

	if onChange != nil {
		onChange(snapshot)
	}
	if len(alerts) > 0 && onAlert != nil {
		onAlert(alerts)
	}
	return snapshot
}

// isShortWindow reports whether a pool covers a rolling few-hour window.
// Those pools are dropped: only weekly pools are surfaced in the UI.
func isShortWindow(kind string) bool {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "five_hour", "5h", "5_hour", "5-hour":
		return true
	}
	return false
}

func (m *Manager) alertsForPoolLocked(pool Pool, thresholds []int) []Alert {
	if pool.UsedPercent == nil || pool.Status == "stale" || pool.Status == "error" {
		return nil
	}
	alerts := make([]Alert, 0)
	for _, threshold := range thresholds {
		if *pool.UsedPercent < float64(threshold) {
			continue
		}
		key := alertKey(pool, threshold)
		if m.alerted[key] {
			continue
		}
		m.alerted[key] = true
		name := pool.AccountName
		if pool.GroupName != "" {
			name += " · " + pool.GroupName
		}
		if pool.WindowKind != "" {
			name += " · " + pool.WindowKind
		}
		alerts = append(alerts, Alert{PoolID: pool.ID, PoolName: name, Threshold: threshold, UsedPercent: *pool.UsedPercent, ResetAt: pool.ResetAt})
	}
	return alerts
}

func alertKey(pool Pool, threshold int) string {
	reset := ""
	if pool.ResetAt != nil {
		reset = pool.ResetAt.UTC().Format(time.RFC3339Nano)
	}
	return fmt.Sprintf("%s|%s|%d", pool.ID, reset, threshold)
}

func (m *Manager) removeDisabledSourcesLocked(cfg Config) {
	for _, id := range []string{SourceGemini, SourceAntigravity, SourceGrok} {
		if sourceEnabled(cfg, id) {
			continue
		}
		m.snapshot.Pools = removePoolsForProvider(m.snapshot.Pools, id)
		m.snapshot.Sources = removeSourceForID(m.snapshot.Sources, id)
	}
}

func (m *Manager) snapshotLocked() Snapshot {
	snapshot := m.snapshot
	snapshot.Pools = make([]Pool, len(m.snapshot.Pools))
	for i, pool := range m.snapshot.Pools {
		pool.Products = append([]ProductUsage(nil), pool.Products...)
		if pool.Products == nil {
			pool.Products = []ProductUsage{}
		}
		snapshot.Pools[i] = pool
	}
	snapshot.Credits = append([]CreditBalance{}, m.snapshot.Credits...)
	snapshot.Sources = append([]SourceStatus{}, m.snapshot.Sources...)
	return snapshot
}

func (m *Manager) persistLocked() {
	if err := writeState(m.statePath, persistedState{Snapshot: m.snapshot, History: m.history, Alerted: m.alerted}); err != nil && m.log != nil {
		m.log("quota.state", err.Error())
	}
}

func sourceEnabled(cfg Config, id string) bool {
	switch id {
	case SourceGemini:
		return cfg.Gemini.Enabled
	case SourceAntigravity:
		return cfg.Antigravity.Enabled
	case SourceGrok:
		return cfg.Grok.Enabled
	default:
		return false
	}
}

func sourceName(id string) string {
	switch id {
	case SourceGemini:
		return "Gemini Web"
	case SourceAntigravity:
		return "Antigravity"
	case SourceGrok:
		return "GrokUsagePet"
	default:
		return id
	}
}

func poolsForProvider(pools []Pool, provider string) []Pool {
	result := make([]Pool, 0)
	for _, pool := range pools {
		if pool.Provider == provider {
			result = append(result, pool)
		}
	}
	return result
}

func removePoolsForProvider(pools []Pool, provider string) []Pool {
	result := make([]Pool, 0, len(pools))
	for _, pool := range pools {
		if pool.Provider != provider {
			result = append(result, pool)
		}
	}
	return result
}

func removeSourceForID(sources []SourceStatus, id string) []SourceStatus {
	result := make([]SourceStatus, 0, len(sources))
	for _, source := range sources {
		if source.ID != id {
			result = append(result, source)
		}
	}
	return result
}

func sourceForID(sources []SourceStatus, id string) (SourceStatus, bool) {
	for _, source := range sources {
		if source.ID == id {
			return source, true
		}
	}
	return SourceStatus{}, false
}
