package quota

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

type geminiAccount struct {
	ID    int64  `json:"id"`
	Label string `json:"label"`
	Email string `json:"email"`
	Quota struct {
		UsedPercent       *float64 `json:"used_percent"`
		ResetAt           int64    `json:"reset_at"`
		FiveHourLimited   bool     `json:"five_hour_limited"`
		WeeklyUsedPercent *float64 `json:"weekly_used_percent"`
		WeeklyResetAt     int64    `json:"weekly_reset_at"`
		WeeklyLimited     bool     `json:"weekly_limited"`
		CheckedAt         int64    `json:"checked_at"`
		Error             string   `json:"error"`
	} `json:"quota"`
}

type geminiAccountsResponse struct {
	Items []geminiAccount `json:"items"`
}

func collectGemini(ctx context.Context, cfg ServiceConfig, client *http.Client, active bool) collection {
	headers := map[string]string{"Authorization": "Bearer " + cfg.AdminToken}
	accounts, err := geminiAccounts(ctx, client, cfg.BaseURL, headers)
	if err != nil {
		return collection{err: err}
	}
	if active {
		for _, account := range accounts {
			endpoint := cfg.BaseURL + "/admin/api/accounts/" + url.PathEscape(strconv.FormatInt(account.ID, 10)) + "/quota"
			if err := requestJSON(ctx, client, http.MethodPost, endpoint, headers, nil); err != nil {
				return collection{err: err}
			}
		}
		accounts, err = geminiAccounts(ctx, client, cfg.BaseURL, headers)
		if err != nil {
			return collection{err: err}
		}
	}

	result := collection{status: "ok", pools: make([]Pool, 0, len(accounts)*2)}
	for _, account := range accounts {
		name := strings.TrimSpace(account.Label)
		if name == "" {
			name = strings.TrimSpace(account.Email)
		}
		if name == "" {
			name = strconv.FormatInt(account.ID, 10)
		}
		accountID := strconv.FormatInt(account.ID, 10)
		observedAt := unixSeconds(account.Quota.CheckedAt)
		used, remaining := usagePercent(account.Quota.UsedPercent, nil)
		weeklyUsed, weeklyRemaining := usagePercent(account.Quota.WeeklyUsedPercent, nil)
		status := "ok"
		if account.Quota.Error != "" {
			status = "degraded"
			result.status = "degraded"
			result.detail = account.Quota.Error
		}
		for _, item := range []struct {
			window    string
			used      *float64
			remaining *float64
			resetAt   int64
			limited   bool
		}{
			{window: "five_hour", used: used, remaining: remaining, resetAt: account.Quota.ResetAt, limited: account.Quota.FiveHourLimited},
			{window: "weekly", used: weeklyUsed, remaining: weeklyRemaining, resetAt: account.Quota.WeeklyResetAt, limited: account.Quota.WeeklyLimited},
		} {
			result.pools = append(result.pools, Pool{
				ID:               fmt.Sprintf("gemini:%s:%s", accountID, item.window),
				Provider:         SourceGemini,
				AccountID:        accountID,
				AccountName:      name,
				GroupName:        "Gemini Web",
				Plan:             "web",
				PlanKind:         "web",
				WindowKind:       item.window,
				UsedPercent:      item.used,
				RemainingPercent: item.remaining,
				ResetAt:          unixSeconds(item.resetAt),
				ObservedAt:       observedAt,
				Status:           status,
				Error:            account.Quota.Error,
				SourceKind:       "http",
				Limited:          item.limited,
				Products:         []ProductUsage{},
			})
		}
	}
	return result
}

func geminiAccounts(ctx context.Context, client *http.Client, baseURL string, headers map[string]string) ([]geminiAccount, error) {
	var response geminiAccountsResponse
	if err := requestJSON(ctx, client, http.MethodGet, baseURL+"/admin/api/accounts", headers, &response); err != nil {
		return nil, err
	}
	return response.Items, nil
}
