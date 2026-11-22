package quota

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

type antigravityAccount struct {
	ID    flexID            `json:"id"`
	Email string            `json:"email"`
	Name  string            `json:"name"`
	Label string            `json:"label"`
	Quota *antigravityQuota `json:"quota"`
}

type flexID string

func (id *flexID) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		*id = flexID(text)
		return nil
	}
	var number json.Number
	if err := json.Unmarshal(data, &number); err != nil {
		return err
	}
	*id = flexID(number.String())
	return nil
}

type antigravityQuota struct {
	SubscriptionTier *string            `json:"subscription_tier"`
	LastUpdated      int64              `json:"last_updated"`
	IsForbidden      bool               `json:"is_forbidden"`
	ForbiddenReason  string             `json:"forbidden_reason"`
	QuotaGroups      []antigravityGroup `json:"quota_groups"`
}

type antigravityGroup struct {
	DisplayName string              `json:"display_name"`
	Buckets     []antigravityBucket `json:"buckets"`
}

type antigravityBucket struct {
	BucketID          string   `json:"bucket_id"`
	Window            string   `json:"window"`
	RemainingFraction *float64 `json:"remaining_fraction"`
	ResetTime         string   `json:"reset_time"`
	ObservedAt        int64    `json:"observed_at"`
}

func collectAntigravity(ctx context.Context, cfg ServiceConfig, client *http.Client, active bool) collection {
	headers := antigravityHeaders(cfg.AdminToken)
	if active {
		if err := requestJSON(ctx, client, http.MethodPost, cfg.BaseURL+"/api/accounts/refresh", headers, nil); err != nil {
			return collection{err: err}
		}
	}
	accounts, err := antigravityAccounts(ctx, client, cfg.BaseURL, headers)
	if err != nil {
		return collection{err: err}
	}
	result := collection{status: "ok", pools: []Pool{}}
	for _, account := range accounts {
		accountID := string(account.ID)
		if accountID == "" {
			result.status = "degraded"
			result.detail = "account without id skipped"
			continue
		}
		var quotaData antigravityQuota
		if account.Quota != nil && len(account.Quota.QuotaGroups) > 0 {
			quotaData = *account.Quota
		} else {
			var raw json.RawMessage
			endpoint := cfg.BaseURL + "/api/accounts/" + url.PathEscape(accountID) + "/quota"
			if err := requestJSON(ctx, client, http.MethodGet, endpoint, headers, &raw); err != nil {
				return collection{err: err}
			}
			decoded, err := decodeAntigravityQuota(raw)
			if err != nil {
				return collection{err: fmt.Errorf("account %s: %w", accountID, err)}
			}
			quotaData = decoded
		}
		if quotaData.IsForbidden {
			reason := quotaData.ForbiddenReason
			if reason == "" {
				reason = "quota access forbidden"
			}
			return collection{err: fmt.Errorf("account %s: %s", accountID, reason)}
		}
		if len(quotaData.QuotaGroups) == 0 {
			return collection{err: fmt.Errorf("account %s: no quota groups returned", accountID)}
		}
		name := firstNonEmpty(account.Label, account.Name, account.Email, accountID)
		plan := ""
		if quotaData.SubscriptionTier != nil {
			plan = *quotaData.SubscriptionTier
		}
		for groupIndex, group := range quotaData.QuotaGroups {
			for bucketIndex, bucket := range group.Buckets {
				var used, remaining *float64
				status := "ok"
				poolError := ""
				if bucket.RemainingFraction != nil && *bucket.RemainingFraction >= 0 && *bucket.RemainingFraction <= 1 {
					remaining = floatPointer(*bucket.RemainingFraction * 100)
					used = floatPointer((1 - *bucket.RemainingFraction) * 100)
				} else {
					status = "degraded"
					poolError = "invalid remaining_fraction"
					result.status = "degraded"
					result.detail = poolError
				}
				observedAt := unixMilliseconds(bucket.ObservedAt)
				if observedAt == nil {
					observedAt = unixSeconds(quotaData.LastUpdated)
				}
				bucketID := firstNonEmpty(bucket.BucketID, bucket.Window, fmt.Sprintf("bucket-%d", bucketIndex))
				result.pools = append(result.pools, Pool{
					ID:               fmt.Sprintf("antigravity:%s:group-%d:%s", accountID, groupIndex, bucketID),
					Provider:         SourceAntigravity,
					AccountID:        accountID,
					AccountName:      name,
					GroupName:        group.DisplayName,
					Plan:             plan,
					PlanKind:         plan,
					WindowKind:       bucket.Window,
					UsedPercent:      used,
					RemainingPercent: remaining,
					ResetAt:          parseTimeValue(bucket.ResetTime),
					ObservedAt:       observedAt,
					Status:           status,
					Error:            poolError,
					SourceKind:       "http",
					Limited:          remaining != nil && *remaining <= 0,
					Products:         []ProductUsage{},
				})
			}
		}
	}
	return result
}

func antigravityHeaders(token string) map[string]string {
	headers := map[string]string{}
	token = strings.TrimSpace(token)
	if token == "" {
		return headers
	}
	// v5 renamed the admin token to the proxy API key and accepts it as a
	// bearer token; older builds only read x-admin-token.
	headers["Authorization"] = "Bearer " + token
	headers["x-api-key"] = token
	headers["x-admin-token"] = token
	return headers
}

func antigravityAccounts(ctx context.Context, client *http.Client, baseURL string, headers map[string]string) ([]antigravityAccount, error) {
	var raw json.RawMessage
	if err := requestJSON(ctx, client, http.MethodGet, baseURL+"/api/accounts", headers, &raw); err != nil {
		return nil, err
	}
	var accounts []antigravityAccount
	if err := json.Unmarshal(raw, &accounts); err == nil {
		return accounts, nil
	}
	var wrapper struct {
		Accounts []antigravityAccount `json:"accounts"`
		Items    []antigravityAccount `json:"items"`
	}
	if err := json.Unmarshal(raw, &wrapper); err != nil {
		return nil, fmt.Errorf("decode accounts: %w", err)
	}
	if wrapper.Accounts != nil {
		return wrapper.Accounts, nil
	}
	return wrapper.Items, nil
}

func decodeAntigravityQuota(raw json.RawMessage) (antigravityQuota, error) {
	var quotaData antigravityQuota
	if err := json.Unmarshal(raw, &quotaData); err != nil {
		return quotaData, err
	}
	if quotaData.LastUpdated != 0 || quotaData.QuotaGroups != nil || quotaData.IsForbidden {
		return quotaData, nil
	}
	var wrapper struct {
		Data  json.RawMessage `json:"data"`
		Quota json.RawMessage `json:"quota"`
	}
	if err := json.Unmarshal(raw, &wrapper); err != nil {
		return quotaData, err
	}
	nested := wrapper.Quota
	if len(nested) == 0 {
		nested = wrapper.Data
	}
	if len(nested) == 0 {
		return quotaData, nil
	}
	if err := json.Unmarshal(nested, &quotaData); err != nil {
		return quotaData, err
	}
	return quotaData, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
