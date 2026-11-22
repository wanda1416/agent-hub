package quota

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type grokFile struct {
	UsedPercent         *float64            `json:"used_percent"`
	RemainingPercent    *float64            `json:"remaining_percent"`
	Period              grokPeriod          `json:"period"`
	ProductsUsedPercent map[string]*float64 `json:"products_used_percent"`
	FetchedAt           json.RawMessage     `json:"fetched_at"`
	Plan                string              `json:"plan"`
	Status              string              `json:"status"`
	Errors              json.RawMessage     `json:"errors"`
	Cursor              *grokNode           `json:"cursor"`
	GrokBot             *grokNode           `json:"grok_bot"`
	IsUnified           bool                `json:"isUnified"`
	IsUnifiedSnake      bool                `json:"is_unified"`
}

type grokPeriod struct {
	End json.RawMessage `json:"end"`
}

type grokNode struct {
	UsedPercent      *float64        `json:"used_percent"`
	RemainingPercent *float64        `json:"remaining_percent"`
	ResetsAt         json.RawMessage `json:"resets_at"`
	FetchedAt        json.RawMessage `json:"fetched_at"`
	GrokBot          *grokNode       `json:"grok_bot"`
	IsUnified        bool            `json:"isUnified"`
	IsUnifiedSnake   bool            `json:"is_unified"`
}

func collectGrok(cfg GrokConfig) collection {
	path := expandSnapshotPath(cfg.SnapshotPath)
	file, err := os.Open(path)
	if err != nil {
		return collection{err: err}
	}
	defer file.Close()
	body, err := io.ReadAll(io.LimitReader(file, 8<<20))
	if err != nil {
		return collection{err: err}
	}
	var snapshot grokFile
	if err := json.Unmarshal(body, &snapshot); err != nil {
		return collection{err: fmt.Errorf("decode %s: %w", path, err)}
	}
	warning := grokErrorText(snapshot.Errors)
	if strings.EqualFold(snapshot.Status, "failed") {
		if warning == "" {
			warning = "GrokUsagePet snapshot status is failed"
		}
		return collection{err: fmt.Errorf("%s", warning)}
	}

	unified := cfg.PlanKind == PlanUnified || (cfg.PlanKind == PlanAuto && grokUnified(&snapshot))
	observedAt := parseRawTime(snapshot.FetchedAt)
	status := "ok"
	if strings.EqualFold(snapshot.Status, "partial") {
		status = "degraded"
	}
	planKind := PlanLegacy
	if unified {
		planKind = PlanUnified
	}
	result := collection{status: status, detail: warning, pools: []Pool{}}
	if unified {
		pool := unifiedGrokPool(&snapshot, observedAt, planKind, status, warning)
		result.pools = append(result.pools, pool)
		return result
	}

	rootUsed, rootRemaining := usagePercent(snapshot.UsedPercent, snapshot.RemainingPercent)
	rootReset := parseRawTime(snapshot.Period.End)
	if rootUsed != nil || rootRemaining != nil || rootReset != nil {
		result.pools = append(result.pools, Pool{
			ID:               "grok:grok:weekly",
			Provider:         SourceGrok,
			AccountID:        "local",
			AccountName:      "Grok",
			GroupName:        "SuperGrok",
			Plan:             snapshot.Plan,
			PlanKind:         planKind,
			WindowKind:       "weekly",
			UsedPercent:      rootUsed,
			RemainingPercent: rootRemaining,
			ResetAt:          rootReset,
			ObservedAt:       observedAt,
			Status:           status,
			Error:            poolWarning(status, warning),
			SourceKind:       "file",
			Limited:          rootRemaining != nil && *rootRemaining <= 0,
			Products:         grokProducts(snapshot.ProductsUsedPercent),
		})
	}

	bot := grokBotNode(&snapshot)
	if bot != nil {
		used, remaining := usagePercent(bot.UsedPercent, bot.RemainingPercent)
		resetAt := parseRawTime(bot.ResetsAt)
		botObserved := parseRawTime(bot.FetchedAt)
		if botObserved == nil {
			botObserved = observedAt
		}
		if used != nil || remaining != nil || resetAt != nil {
			result.pools = append(result.pools, Pool{
				ID:               "grok:grok-bot:weekly",
				Provider:         SourceGrok,
				AccountID:        "local",
				AccountName:      "Grok Bot",
				GroupName:        "Grok Bot",
				Plan:             snapshot.Plan,
				PlanKind:         planKind,
				WindowKind:       "weekly",
				UsedPercent:      used,
				RemainingPercent: remaining,
				ResetAt:          resetAt,
				ObservedAt:       botObserved,
				Status:           status,
				Error:            poolWarning(status, warning),
				SourceKind:       "file",
				Limited:          remaining != nil && *remaining <= 0,
				Products:         []ProductUsage{},
			})
		}
	}

	if snapshot.Cursor != nil {
		used, remaining := usagePercent(snapshot.Cursor.UsedPercent, snapshot.Cursor.RemainingPercent)
		resetAt := parseRawTime(snapshot.Cursor.ResetsAt)
		if used != nil || remaining != nil || resetAt != nil {
			result.pools = append(result.pools, Pool{
				ID:               "grok:cursor:weekly",
				Provider:         SourceGrok,
				AccountID:        "local",
				AccountName:      "Cursor",
				GroupName:        "Cursor",
				Plan:             snapshot.Plan,
				PlanKind:         planKind,
				WindowKind:       "weekly",
				UsedPercent:      used,
				RemainingPercent: remaining,
				ResetAt:          resetAt,
				ObservedAt:       observedAt,
				Status:           status,
				Error:            poolWarning(status, warning),
				SourceKind:       "file",
				Limited:          remaining != nil && *remaining <= 0,
				Products:         []ProductUsage{},
			})
		}
	}
	if len(result.pools) == 0 {
		return collection{err: fmt.Errorf("no Grok quota observations in %s", path)}
	}
	return result
}

func unifiedGrokPool(snapshot *grokFile, observedAt *time.Time, planKind, status, warning string) Pool {
	used, remaining := usagePercent(snapshot.UsedPercent, snapshot.RemainingPercent)
	resetAt := parseRawTime(snapshot.Period.End)
	bot := grokBotNode(snapshot)
	if bot != nil && (bot.UsedPercent != nil || bot.RemainingPercent != nil) {
		used, remaining = usagePercent(bot.UsedPercent, bot.RemainingPercent)
	}
	botReset := (*time.Time)(nil)
	if bot != nil {
		botReset = parseRawTime(bot.ResetsAt)
	}
	cursorReset := (*time.Time)(nil)
	if snapshot.Cursor != nil {
		cursorReset = parseRawTime(snapshot.Cursor.ResetsAt)
	}
	if botReset != nil {
		resetAt = botReset
	} else if cursorReset != nil {
		resetAt = cursorReset
	}
	if used == nil && snapshot.Cursor != nil && (snapshot.Cursor.UsedPercent != nil || snapshot.Cursor.RemainingPercent != nil) {
		used, remaining = usagePercent(snapshot.Cursor.UsedPercent, snapshot.Cursor.RemainingPercent)
	}
	if bot != nil {
		if botObserved := parseRawTime(bot.FetchedAt); botObserved != nil {
			observedAt = botObserved
		}
	}
	return Pool{
		ID:               "grok:x-subscription:weekly",
		Provider:         SourceGrok,
		AccountID:        "local",
		AccountName:      "X Subscription",
		GroupName:        "Grok + Grok Bot + Cursor",
		Plan:             snapshot.Plan,
		PlanKind:         planKind,
		WindowKind:       "weekly",
		UsedPercent:      used,
		RemainingPercent: remaining,
		ResetAt:          resetAt,
		ObservedAt:       observedAt,
		Status:           status,
		Error:            poolWarning(status, warning),
		SourceKind:       "file",
		Limited:          remaining != nil && *remaining <= 0,
		Products:         grokProducts(snapshot.ProductsUsedPercent),
	}
}

func grokBotNode(snapshot *grokFile) *grokNode {
	if snapshot.Cursor != nil && snapshot.Cursor.GrokBot != nil {
		return snapshot.Cursor.GrokBot
	}
	return snapshot.GrokBot
}

func grokUnified(snapshot *grokFile) bool {
	if snapshot.IsUnified || snapshot.IsUnifiedSnake {
		return true
	}
	if snapshot.Cursor != nil && (snapshot.Cursor.IsUnified || snapshot.Cursor.IsUnifiedSnake) {
		return true
	}
	bot := grokBotNode(snapshot)
	return bot != nil && (bot.IsUnified || bot.IsUnifiedSnake)
}

func grokProducts(products map[string]*float64) []ProductUsage {
	names := make([]string, 0, len(products))
	for name := range products {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]ProductUsage, 0, len(names))
	for _, name := range names {
		result = append(result, ProductUsage{Product: name, UsedPercent: products[name]})
	}
	return result
}

func poolWarning(status, warning string) string {
	if status == "degraded" {
		return warning
	}
	return ""
}

func grokErrorText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var values map[string]string
	if err := json.Unmarshal(raw, &values); err == nil {
		keys := make([]string, 0, len(values))
		for key := range values {
			if !strings.HasPrefix(strings.ToLower(key), "codex") {
				keys = append(keys, key)
			}
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, key := range keys {
			parts = append(parts, key+": "+values[key])
		}
		return strings.Join(parts, "; ")
	}
	var list []string
	if err := json.Unmarshal(raw, &list); err == nil {
		return strings.Join(list, "; ")
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}
	return ""
}

func parseRawTime(raw json.RawMessage) *time.Time {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return parseTimeValue(text)
	}
	var value float64
	if err := json.Unmarshal(raw, &value); err != nil || value <= 0 {
		return nil
	}
	if value > 1e12 {
		return unixMilliseconds(int64(value))
	}
	return unixSeconds(int64(value))
}

func expandSnapshotPath(path string) string {
	path = os.ExpandEnv(strings.TrimSpace(path))
	for _, key := range []string{"LOCALAPPDATA", "APPDATA", "USERPROFILE", "HOME"} {
		if value := os.Getenv(key); value != "" {
			path = strings.ReplaceAll(path, "%"+key+"%", value)
		}
	}
	return filepath.Clean(path)
}
