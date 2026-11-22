package main

import (
	"fmt"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"time"

	"agenthub/internal/config"
	"agenthub/internal/monitor"
	"agenthub/internal/platform"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// monitorAlertPayload is emitted over "monitor:alert" so the frontend can show
// the strong alert modal. Alerts are always emitted for popup mode; the normal
// notification and in-app modes rely on the OS notification and the red dot.
type monitorAlertPayload struct {
	Alerts []monitor.Alert `json:"alerts"`
	Sound  bool            `json:"sound"`
	Test   bool            `json:"test"`
}

func monitorConfigFrom(cfg config.AppConfig) monitor.Config {
	return monitor.Config{
		Enabled:    cfg.MonitorEnabled,
		Interval:   cfg.MonitorInterval,
		Models:     cfg.MonitorModels,
		AlertMode:  cfg.MonitorAlertMode,
		AlertSound: cfg.MonitorAlertSound,
	}
}

// startResetMonitor wires the AIHOT watcher into the app. It must run after the
// Wails context exists so notifications and frontend events work.
func (a *App) startResetMonitor() {
	if a.resetMonitor != nil || a.ctx == nil {
		return
	}
	a.resetMonitor = monitor.New(monitor.Options{
		Config:    monitorConfigFrom(a.config),
		StatePath: filepath.Join(a.dataDir, "monitor-state.json"),
		OnAlert:   a.handleResetAlerts,
		OnChange: func(snapshot monitor.Snapshot) {
			a.emit("monitor:changed", snapshot)
		},
		Log: a.LogSystemError,
	})

	if err := runtime.InitializeNotifications(a.ctx); err != nil {
		a.LogSystemError("monitor.notifications", "failed to initialize notifications: "+err.Error())
	} else if goruntime.GOOS == "darwin" && (a.config.MonitorEnabled || a.config.Quota.Enabled) {
		go func() {
			if _, err := runtime.RequestNotificationAuthorization(a.ctx); err != nil {
				a.LogSystemError("monitor.notifications", "notification authorization failed: "+err.Error())
			}
		}()
	}

	// Route notification clicks by their payload so quota alerts do not open the
	// reset monitor (and monitor alerts retain their existing destination).
	runtime.OnNotificationResponse(a.ctx, func(result runtime.NotificationResult) {
		if result.Error != nil {
			a.LogSystemError("notifications", "notification response failed: "+result.Error.Error())
			return
		}
		if _, ok := result.Response.UserInfo["quota"]; ok {
			a.focusQuotaNotification()
			return
		}
		if a.currentConfig().MonitorAlertMode == monitor.ModeInApp {
			return
		}
		platform.ShowMainWindow(a.ctx)
		a.emit("monitor:focus", nil)
	})

	a.resetMonitor.Start(a.ctx)
	a.LogSystemError("monitor", fmt.Sprintf("reset monitor started (enabled=%t, interval=%dmin, models=%s)",
		a.config.MonitorEnabled, a.config.MonitorInterval, strings.Join(a.config.MonitorModels, ",")))
}

func (a *App) stopResetMonitor() {
	if a.resetMonitor != nil {
		a.resetMonitor.Stop()
	}
}

// currentConfig returns a copy of the configuration. The monitor callbacks run
// on a different goroutine than config writes, so reads must take configMu.
func (a *App) currentConfig() config.AppConfig {
	a.configMu.Lock()
	defer a.configMu.Unlock()
	return a.config
}

// GetMonitorSnapshot returns the latest monitor state for the monitor page.
func (a *App) GetMonitorSnapshot() monitor.Snapshot {
	defer a.recoverPanic("GetMonitorSnapshot")
	if a.resetMonitor == nil {
		return monitor.Snapshot{}
	}
	return a.resetMonitor.Snapshot()
}

// RefreshMonitor requests an immediate poll instead of waiting for the next
// interval. The result arrives through the "monitor:changed" event.
func (a *App) RefreshMonitor() {
	defer a.recoverPanic("RefreshMonitor")
	if a.resetMonitor != nil {
		a.resetMonitor.Refresh()
	}
}

// SetMonitorConfig persists the monitor settings. normalizeConfig clamps the
// values and applyConfigLocked forwards the change to the running watcher.
func (a *App) SetMonitorConfig(enabled bool, interval int, models []string, alertMode string, alertSound bool) error {
	defer a.recoverPanic("SetMonitorConfig")
	if err := a.patchConfig(func(cfg *config.AppConfig) {
		cfg.MonitorEnabled = enabled
		cfg.MonitorInterval = interval
		cfg.MonitorModels = models
		cfg.MonitorAlertMode = alertMode
		cfg.MonitorAlertSound = alertSound
	}, originGUI); err != nil {
		return err
	}
	if enabled && goruntime.GOOS == "darwin" && a.ctx != nil {
		go func() {
			if _, err := runtime.RequestNotificationAuthorization(a.ctx); err != nil {
				a.LogSystemError("monitor.notifications", "notification authorization failed: "+err.Error())
			}
		}()
	}
	return nil
}

// TestMonitorAlert delivers a synthetic alert through the configured channel so
// users can verify system permissions, sound and the modal. It always shows the
// strong modal, even when the saved mode is weaker.
func (a *App) TestMonitorAlert() {
	defer a.recoverPanic("TestMonitorAlert")
	english := a.GetLocale() == "en"
	title := "测试提醒：Codex 额度重置"
	body := "这是一条测试提醒，用于确认强弹窗、系统通知和声音是否正常。"
	if english {
		title = "Test alert: Codex reset announced"
		body = "This is a test alert to verify the popup, system notification and sound."
	}
	alert := monitor.Alert{
		ModelID:   "test",
		ModelName: "Codex",
		PageURL:   "https://aihot.news/resets",
		Events: []monitor.Event{{
			ID:           fmt.Sprintf("test-%d", time.Now().UnixNano()),
			DisplayLabel: title,
			Title:        title,
			Active:       true,
			Estimate:     &monitor.Estimate{Label: "08:30–13:30"},
			Posts:        []monitor.Post{{Text: body, URL: "https://aihot.news/resets"}},
		}},
	}
	a.deliverResetAlerts([]monitor.Alert{alert}, true)
}

// handleResetAlerts is called by the watcher exactly once per newly announced
// reset. The configured alert mode decides which channels are used.
func (a *App) handleResetAlerts(alerts []monitor.Alert) {
	if len(alerts) == 0 {
		return
	}
	a.deliverResetAlerts(alerts, false)
}

func (a *App) deliverResetAlerts(alerts []monitor.Alert, forcePopup bool) {
	cfg := a.currentConfig()
	popup := forcePopup || cfg.MonitorAlertMode == monitor.ModePopup
	if cfg.MonitorAlertMode == monitor.ModeInApp && !forcePopup {
		return
	}

	if popup {
		platform.ShowMainWindow(a.ctx)
		platform.FlashMainWindow()
	}
	// The popup mode also sends a system notification so the alert is visible
	// when the window is on another desktop.
	for _, alert := range alerts {
		a.sendResetNotification(alert)
	}
	if popup {
		a.emit("monitor:alert", monitorAlertPayload{
			Alerts: alerts,
			Sound:  cfg.MonitorAlertSound,
			Test:   forcePopup,
		})
	}
}

func (a *App) sendResetNotification(alert monitor.Alert) {
	if a.ctx == nil {
		return
	}
	english := a.GetLocale() == "en"
	title := alert.ModelName + " 额度重置已宣布"
	if english {
		title = alert.ModelName + " reset announced"
	}

	lines := make([]string, 0, len(alert.Events))
	for _, event := range alert.Events {
		line := event.Title
		if line == "" {
			line = event.DisplayLabel
		}
		if line == "" {
			line = event.Label
		}
		if event.Estimate != nil && event.Estimate.Label != "" {
			if english {
				line += " · expected " + event.Estimate.Label
			} else {
				line += " · 预计 " + event.Estimate.Label
			}
		}
		lines = append(lines, line)
	}
	body := truncateRunes(strings.Join(lines, "\n"), 240)

	id := "reset-" + alert.ModelID
	if len(alert.Events) > 0 {
		id += "-" + alert.Events[0].ID
	}
	if err := runtime.SendNotification(a.ctx, runtime.NotificationOptions{
		ID:    id,
		Title: title,
		Body:  body,
		Data:  map[string]interface{}{"monitor": alert.ModelID},
	}); err != nil {
		a.LogSystemError("monitor.notifications", "failed to send notification: "+err.Error())
	}
}

func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit-1]) + "…"
}
