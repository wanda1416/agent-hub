package main

import (
	"context"
	"fmt"
	"path/filepath"
	goruntime "runtime"

	"agenthub/internal/config"
	"agenthub/internal/platform"
	"agenthub/internal/quota"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

type quotaAlertPayload struct {
	Alerts []quota.Alert `json:"alerts"`
}

// startQuotaManager wires the local quota collectors into the app lifecycle.
// The collectors only read user-provided local services and snapshot files.
func (a *App) startQuotaManager() {
	if a.quotaManager != nil || a.ctx == nil {
		return
	}
	cfg := a.currentConfig()
	a.quotaManager = quota.New(quota.Options{
		Config:    cfg.Quota,
		StatePath: filepath.Join(a.dataDir, "quota-state.json"),
		OnChange: func(snapshot quota.Snapshot) {
			a.emit("quota:changed", snapshot)
		},
		OnAlert: a.handleQuotaAlerts,
		Log:     a.LogSystemError,
	})
	a.quotaManager.Start(a.ctx)
	a.LogSystemError("quota", fmt.Sprintf("quota manager started (enabled=%t, interval=%dmin)",
		cfg.Quota.Enabled, cfg.Quota.IntervalMinutes))
}

func (a *App) stopQuotaManager() {
	if a.quotaManager != nil {
		a.quotaManager.Stop()
	}
}

// GetQuotaSnapshot returns the last known values, including stale values from a
// failed refresh so a temporary source outage never clears the UI.
func (a *App) GetQuotaSnapshot() quota.Snapshot {
	defer a.recoverPanic("GetQuotaSnapshot")
	if a.quotaManager == nil {
		return quota.Snapshot{}
	}
	return a.quotaManager.Snapshot()
}

// RefreshQuota actively refreshes enabled upstream services and returns the
// resulting snapshot. File-backed collectors are simply read again.
func (a *App) RefreshQuota() quota.Snapshot {
	defer a.recoverPanic("RefreshQuota")
	if a.quotaManager == nil {
		return quota.Snapshot{}
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return a.quotaManager.Refresh(ctx)
}

// SetQuotaConfig persists collector settings and updates the running manager
// through the same configuration write path used by the rest of the app.
func (a *App) SetQuotaConfig(value quota.Config) error {
	defer a.recoverPanic("SetQuotaConfig")
	if err := a.patchConfig(func(cfg *config.AppConfig) {
		cfg.Quota = value
	}, originGUI); err != nil {
		return err
	}
	if value.Enabled && goruntime.GOOS == "darwin" && a.ctx != nil {
		go func() {
			if _, err := runtime.RequestNotificationAuthorization(a.ctx); err != nil {
				a.LogSystemError("quota.notifications", "notification authorization failed: "+err.Error())
			}
		}()
	}
	return nil
}

func (a *App) handleQuotaAlerts(alerts []quota.Alert) {
	if len(alerts) == 0 {
		return
	}
	a.emit("quota:alert", quotaAlertPayload{Alerts: alerts})
	for _, alert := range alerts {
		a.sendQuotaNotification(alert)
	}
}

func (a *App) sendQuotaNotification(alert quota.Alert) {
	if a.ctx == nil {
		return
	}
	english := a.GetLocale() == "en"
	title := fmt.Sprintf("%s 额度使用已达 %d%%", alert.PoolName, alert.Threshold)
	body := fmt.Sprintf("已使用 %.1f%%", alert.UsedPercent)
	if english {
		title = fmt.Sprintf("%s quota usage reached %d%%", alert.PoolName, alert.Threshold)
		body = fmt.Sprintf("Used %.1f%%", alert.UsedPercent)
	}
	if alert.ResetAt != nil {
		resetAt := alert.ResetAt.Local().Format("2006-01-02 15:04")
		if english {
			body += " · resets " + resetAt
		} else {
			body += " · 重置时间 " + resetAt
		}
	}
	id := fmt.Sprintf("quota-%s-%d", alert.PoolID, alert.Threshold)
	if err := runtime.SendNotification(a.ctx, runtime.NotificationOptions{
		ID:    id,
		Title: title,
		Body:  truncateRunes(body, 240),
		Data:  map[string]interface{}{"quota": alert.PoolID},
	}); err != nil {
		a.LogSystemError("quota.notifications", "failed to send notification: "+err.Error())
	}
}

// focusQuotaNotification brings the quota page to the foreground after its
// system notification is clicked.
func (a *App) focusQuotaNotification() {
	if a.ctx == nil {
		return
	}
	platform.ShowMainWindow(a.ctx)
	a.emit("quota:focus", nil)
}
