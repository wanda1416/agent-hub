package config

import "agenthub/internal/process"

type AppConfig struct {
	Locale        string               `json:"locale"`
	Theme         string               `json:"theme"`
	AutoStart     bool                 `json:"autoStart"`
	AutoStartWSL  bool                 `json:"autoStartWSL"`
	LogDir        string               `json:"logDir"`
	MaxLogLines   int                  `json:"maxLogLines"`
	MaxLogFiles   int                  `json:"maxLogFiles"`
	MaxRestart    int                  `json:"maxRestart"`
	RestartPolicy string               `json:"restartPolicy"`
	DeviceUUID    string               `json:"deviceUUID"`
	Processes     []process.Definition `json:"processes"`

	// Reset monitor settings. Models holds the followed model IDs; an empty
	// (but non-nil) slice means the user explicitly follows none.
	MonitorEnabled    bool     `json:"monitorEnabled"`
	MonitorInterval   int      `json:"monitorInterval"` // minutes
	MonitorModels     []string `json:"monitorModels"`
	MonitorAlertMode  string   `json:"monitorAlertMode"` // inapp | notification | popup
	MonitorAlertSound bool     `json:"monitorAlertSound"`
}

// DefaultMonitorModels returns the four models tracked by the AIHOT reset
// monitor. The order is also the display order.
func DefaultMonitorModels() []string {
	return []string{"codex", "antigravity", "claude", "grok-bot"}
}

func DefaultConfig() AppConfig {
	return AppConfig{
		Locale:            "zh",
		Theme:             "light",
		AutoStart:         false,
		LogDir:            "logs",
		MaxLogLines:       1000,
		MaxLogFiles:       5,
		MaxRestart:        5,
		RestartPolicy:     "on_failure",
		MonitorEnabled:    true,
		MonitorInterval:   10,
		MonitorModels:     DefaultMonitorModels(),
		MonitorAlertMode:  "notification",
		MonitorAlertSound: true,
	}
}
