package quota

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestCollectGemini(t *testing.T) {
	var posts atomic.Int32
	var gets atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("unexpected authorization header %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/admin/api/accounts":
			gets.Add(1)
			fmt.Fprint(w, `{"items":[{"id":7,"label":"Work","quota":{"used_percent":25,"reset_at":2000,"five_hour_limited":false,"weekly_used_percent":50,"weekly_reset_at":3000,"weekly_limited":true,"checked_at":1000}}]}`)
		case r.Method == http.MethodPost && r.URL.Path == "/admin/api/accounts/7/quota":
			posts.Add(1)
			fmt.Fprint(w, `{}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cfg := ServiceConfig{Enabled: true, BaseURL: server.URL, AdminToken: "secret"}
	result := collectGemini(context.Background(), cfg, server.Client(), true)
	if result.err != nil {
		t.Fatalf("collectGemini failed: %v", result.err)
	}
	if posts.Load() != 1 || gets.Load() != 2 {
		t.Fatalf("active refresh calls: posts=%d gets=%d", posts.Load(), gets.Load())
	}
	if len(result.pools) != 2 {
		t.Fatalf("expected two quota windows, got %d", len(result.pools))
	}
	fiveHour := result.pools[0]
	if fiveHour.UsedPercent == nil || *fiveHour.UsedPercent != 25 || fiveHour.RemainingPercent == nil || *fiveHour.RemainingPercent != 75 {
		t.Fatalf("unexpected five-hour usage: %+v", fiveHour)
	}
	if fiveHour.ObservedAt == nil || fiveHour.ObservedAt.Unix() != 1000 {
		t.Fatalf("upstream observation time not retained: %v", fiveHour.ObservedAt)
	}
	weekly := result.pools[1]
	if weekly.WindowKind != "weekly" || weekly.UsedPercent == nil || *weekly.UsedPercent != 50 || !weekly.Limited {
		t.Fatalf("unexpected weekly pool: %+v", weekly)
	}
}

func TestCollectAntigravity(t *testing.T) {
	var refreshes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-admin-token") != "secret" {
			t.Errorf("unexpected admin token %q", r.Header.Get("x-admin-token"))
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/accounts/refresh":
			refreshes.Add(1)
			fmt.Fprint(w, `{}`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/accounts":
			fmt.Fprint(w, `{"items":[{"id":12,"email":"person@example.com"}]}`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/accounts/12/quota":
			fmt.Fprint(w, `{"data":{"subscription_tier":"PRO","last_updated":1700000000,"quota_groups":[{"display_name":"Gemini Models","buckets":[{"bucket_id":"gemini-weekly","window":"weekly","remaining_fraction":0.4,"reset_time":"2026-11-23T00:00:00Z","observed_at":1700000000000}]}]}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cfg := ServiceConfig{Enabled: true, BaseURL: server.URL, AdminToken: "secret"}
	result := collectAntigravity(context.Background(), cfg, server.Client(), true)
	if result.err != nil {
		t.Fatalf("collectAntigravity failed: %v", result.err)
	}
	if refreshes.Load() != 1 {
		t.Fatalf("expected active account refresh, got %d", refreshes.Load())
	}
	if len(result.pools) != 1 {
		t.Fatalf("expected one pool, got %d", len(result.pools))
	}
	pool := result.pools[0]
	if pool.UsedPercent == nil || *pool.UsedPercent != 60 || pool.RemainingPercent == nil || *pool.RemainingPercent != 40 {
		t.Fatalf("remaining fraction was not converted correctly: %+v", pool)
	}
	if pool.ObservedAt == nil || pool.ObservedAt.UnixMilli() != 1700000000000 {
		t.Fatalf("bucket observation time not retained: %v", pool.ObservedAt)
	}
	if pool.ResetAt == nil || pool.ResetAt.Format(time.RFC3339) != "2026-11-23T00:00:00Z" {
		t.Fatalf("unexpected reset time: %v", pool.ResetAt)
	}
}

func TestCollectGrokUnifiedUsesOneSharedPool(t *testing.T) {
	path := writeGrokSnapshot(t, `{
		"status":"complete",
		"plan":"X Premium+",
		"fetched_at":"2026-11-22T10:00:00Z",
		"period":{"end":"2026-11-29T00:00:00Z"},
		"used_percent":10,
		"remaining_percent":90,
		"products_used_percent":{"Grok":8,"Imagine":2},
		"cursor":{"grok_bot":{"used_percent":65,"remaining_percent":35,"resets_at":"2026-11-28T00:00:00Z","isUnified":true}}
	}`)
	result := collectGrok(GrokConfig{Enabled: true, SnapshotPath: path, PlanKind: PlanAuto})
	if result.err != nil {
		t.Fatalf("collectGrok failed: %v", result.err)
	}
	if len(result.pools) != 1 {
		t.Fatalf("unified subscription must produce one shared pool, got %d", len(result.pools))
	}
	pool := result.pools[0]
	if pool.PlanKind != PlanUnified || pool.UsedPercent == nil || *pool.UsedPercent != 65 {
		t.Fatalf("Grok Bot total must be the shared pool total: %+v", pool)
	}
	if pool.ResetAt == nil || pool.ResetAt.Format(time.RFC3339) != "2026-11-28T00:00:00Z" {
		t.Fatalf("Grok Bot reset must win over root reset: %v", pool.ResetAt)
	}
	if len(pool.Products) != 2 {
		t.Fatalf("only root product composition should be attached: %+v", pool.Products)
	}
	for _, product := range pool.Products {
		if product.Product == "Grok Bot" {
			t.Fatal("shared total must not be presented as a Grok Bot product")
		}
	}
}

func TestCollectGrokLegacyDoesNotAddPools(t *testing.T) {
	path := writeGrokSnapshot(t, `{
		"status":"complete",
		"fetched_at":"2026-11-22T10:00:00Z",
		"period":{"end":"2026-11-29T00:00:00Z"},
		"used_percent":10,
		"remaining_percent":90,
		"products_used_percent":{"Grok":10},
		"cursor":{"grok_bot":{"used_percent":65,"remaining_percent":35,"resets_at":"2026-11-28T00:00:00Z"}}
	}`)
	result := collectGrok(GrokConfig{Enabled: true, SnapshotPath: path, PlanKind: PlanAuto})
	if result.err != nil {
		t.Fatalf("collectGrok failed: %v", result.err)
	}
	if len(result.pools) != 2 {
		t.Fatalf("legacy snapshot should retain separate pools, got %d", len(result.pools))
	}
	if *result.pools[0].UsedPercent != 10 || *result.pools[1].UsedPercent != 65 {
		t.Fatalf("legacy usage grants must not be added together: %+v", result.pools)
	}
	if result.pools[0].PlanKind != PlanLegacy || result.pools[1].PlanKind != PlanLegacy {
		t.Fatalf("auto mode must not infer unification without an explicit flag: %+v", result.pools)
	}
}

func writeGrokSnapshot(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "usage.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
