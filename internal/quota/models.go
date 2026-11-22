package quota

import (
	"sort"
	"strings"
	"time"
)

const (
	PlanAuto          = "auto"
	PlanUnified       = "x_unified"
	PlanLegacy        = "legacy"
	SourceGemini      = "gemini"
	SourceAntigravity = "antigravity"
	SourceGrok        = "grok"
)

type ServiceConfig struct {
	Enabled    bool   `json:"enabled"`
	BaseURL    string `json:"baseURL"`
	AdminToken string `json:"adminToken"`
}

type GrokConfig struct {
	Enabled      bool   `json:"enabled"`
	SnapshotPath string `json:"snapshotPath"`
	PlanKind     string `json:"planKind"`
}

type Config struct {
	Enabled         bool          `json:"enabled"`
	IntervalMinutes int           `json:"intervalMinutes"`
	AlertThresholds []int         `json:"alertThresholds"`
	Gemini          ServiceConfig `json:"gemini"`
	Antigravity     ServiceConfig `json:"antigravity"`
	Grok            GrokConfig    `json:"grok"`
}

func DefaultConfig() Config {
	return Config{
		Enabled:         true,
		IntervalMinutes: 5,
		AlertThresholds: []int{80, 90, 100},
		Gemini: ServiceConfig{
			BaseURL: "http://127.0.0.1:8083",
		},
		Antigravity: ServiceConfig{
			BaseURL: "http://127.0.0.1:8045",
		},
		Grok: GrokConfig{
			SnapshotPath: `%LOCALAPPDATA%\GrokUsagePet\usage.json`,
			PlanKind:     PlanAuto,
		},
	}
}

func NormalizeConfig(cfg Config) Config {
	defaults := DefaultConfig()
	if cfg.IntervalMinutes < 5 || cfg.IntervalMinutes > 120 {
		cfg.IntervalMinutes = defaults.IntervalMinutes
	}
	cfg.Gemini.BaseURL = normalizeBaseURL(cfg.Gemini.BaseURL, defaults.Gemini.BaseURL)
	cfg.Antigravity.BaseURL = normalizeBaseURL(cfg.Antigravity.BaseURL, defaults.Antigravity.BaseURL)
	if strings.TrimSpace(cfg.Grok.SnapshotPath) == "" {
		cfg.Grok.SnapshotPath = defaults.Grok.SnapshotPath
	} else {
		cfg.Grok.SnapshotPath = strings.TrimSpace(cfg.Grok.SnapshotPath)
	}
	switch cfg.Grok.PlanKind {
	case PlanAuto, PlanUnified, PlanLegacy:
	default:
		cfg.Grok.PlanKind = PlanAuto
	}

	seen := make(map[int]bool, len(cfg.AlertThresholds))
	thresholds := make([]int, 0, len(cfg.AlertThresholds))
	for _, threshold := range cfg.AlertThresholds {
		if threshold < 1 || threshold > 100 || seen[threshold] {
			continue
		}
		seen[threshold] = true
		thresholds = append(thresholds, threshold)
	}
	if len(thresholds) == 0 {
		thresholds = append(thresholds, defaults.AlertThresholds...)
	}
	sort.Ints(thresholds)
	cfg.AlertThresholds = thresholds
	return cfg
}

func normalizeBaseURL(value, fallback string) string {
	value = strings.TrimRight(strings.TrimSpace(value), "/")
	if value == "" {
		return fallback
	}
	return value
}

type ProductUsage struct {
	Product     string   `json:"product"`
	UsedPercent *float64 `json:"usedPercent"`
}

type Pool struct {
	ID                    string         `json:"id"`
	Provider              string         `json:"provider"`
	AccountID             string         `json:"accountId"`
	AccountName           string         `json:"accountName"`
	GroupName             string         `json:"groupName"`
	Plan                  string         `json:"plan"`
	PlanKind              string         `json:"planKind"`
	WindowKind            string         `json:"windowKind"`
	UsedPercent           *float64       `json:"usedPercent"`
	RemainingPercent      *float64       `json:"remainingPercent"`
	ResetAt               *time.Time     `json:"resetAt"`
	BurnRatePerHour       *float64       `json:"burnRatePerHour"`
	EstimatedExhaustionAt *time.Time     `json:"estimatedExhaustionAt"`
	ObservedAt            *time.Time     `json:"observedAt"`
	Status                string         `json:"status"`
	Error                 string         `json:"error"`
	SourceKind            string         `json:"sourceKind"`
	Limited               bool           `json:"limited"`
	Products              []ProductUsage `json:"products"`
}

type CreditBalance struct {
	AccountID  string     `json:"accountId"`
	Balance    float64    `json:"balance"`
	Unit       string     `json:"unit"`
	ExpiresAt  *time.Time `json:"expiresAt"`
	ObservedAt *time.Time `json:"observedAt"`
}

type SourceStatus struct {
	ID            string     `json:"id"`
	Provider      string     `json:"provider"`
	Name          string     `json:"name"`
	Status        string     `json:"status"`
	Error         string     `json:"error"`
	CheckedAt     *time.Time `json:"checkedAt"`
	LastSuccessAt *time.Time `json:"lastSuccessAt"`
	Detail        string     `json:"detail"`
}

type Snapshot struct {
	Enabled         bool            `json:"enabled"`
	IntervalMinutes int             `json:"intervalMinutes"`
	LastPoll        *time.Time      `json:"lastPoll"`
	Pools           []Pool          `json:"pools"`
	Credits         []CreditBalance `json:"credits"`
	Sources         []SourceStatus  `json:"sources"`
}

type Alert struct {
	PoolID      string     `json:"poolId"`
	PoolName    string     `json:"poolName"`
	Threshold   int        `json:"threshold"`
	UsedPercent float64    `json:"usedPercent"`
	ResetAt     *time.Time `json:"resetAt"`
}
