package database

import (
	"context"
	"math"
	"path/filepath"
	"testing"
	"time"
)

func TestEstimateUsageCostUSDForGPT54(t *testing.T) {
	got := estimateUsageCostBreakdownUSD("gpt-5.4", 1_000_000, 100_000, 20_000, 200_000)
	if got.InputUSD != 0.8*2.5 {
		t.Fatalf("InputUSD = %v, want %v", got.InputUSD, 0.8*2.5)
	}
	if got.CacheUSD != 0.2*0.625 {
		t.Fatalf("CacheUSD = %v, want %v", got.CacheUSD, 0.2*0.625)
	}
	if got.OutputUSD != 0.12*15.0 {
		t.Fatalf("OutputUSD = %v, want %v", got.OutputUSD, 0.12*15.0)
	}
	if got.TotalUSD != (0.8*2.5 + 0.2*0.625 + 0.12*15.0) {
		t.Fatalf("TotalUSD = %v, want %v", got.TotalUSD, 0.8*2.5+0.2*0.625+0.12*15.0)
	}
}

func TestEstimateUsageCostUSDForUnknownModel(t *testing.T) {
	if got := estimateUsageCostBreakdownUSD("unknown-model", 100, 200, 0, 0); got.TotalUSD != 0 {
		t.Fatalf("estimateUsageCostBreakdownUSD() = %#v, want 0 total", got)
	}
}

func TestGetAccountCostEstimatesAggregatesByAccount(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "codex2api.db")
	db, err := New("sqlite", dbPath)
	if err != nil {
		t.Fatalf("New(sqlite) returned error: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	inputs := []*UsageLogInput{
		{
			AccountID:       1,
			Model:           "gpt-5.4",
			StatusCode:      200,
			InputTokens:     1_000_000,
			OutputTokens:    100_000,
			ReasoningTokens: 20_000,
			CachedTokens:    200_000,
		},
		{
			AccountID:       1,
			Model:           "gpt-5.5-codex",
			StatusCode:      200,
			InputTokens:     100_000,
			OutputTokens:    10_000,
			ReasoningTokens: 5_000,
			CachedTokens:    0,
		},
		{
			AccountID:    2,
			Model:        "unknown-model",
			StatusCode:   200,
			InputTokens:  9_999,
			OutputTokens: 8_888,
		},
	}

	for _, input := range inputs {
		if err := db.InsertUsageLog(ctx, input); err != nil {
			t.Fatalf("InsertUsageLog returned error: %v", err)
		}
	}
	db.flushLogs()

	got, err := db.GetAccountCostEstimates(ctx)
	if err != nil {
		t.Fatalf("GetAccountCostEstimates returned error: %v", err)
	}

	want54 := estimateUsageCostBreakdownUSD("gpt-5.4", 1_000_000, 100_000, 20_000, 200_000)
	want55 := estimateUsageCostBreakdownUSD("gpt-5.5-codex", 100_000, 10_000, 5_000, 0)
	wantAccount1 := AccountCostEstimate{
		InputUSD:  want54.InputUSD + want55.InputUSD,
		OutputUSD: want54.OutputUSD + want55.OutputUSD,
		CacheUSD:  want54.CacheUSD + want55.CacheUSD,
		TotalUSD:  want54.TotalUSD + want55.TotalUSD,
	}
	if got[1] != wantAccount1 {
		t.Fatalf("account 1 estimate = %#v, want %#v", got[1], wantAccount1)
	}
	if got[2].TotalUSD != 0 {
		t.Fatalf("account 2 estimate = %#v, want 0", got[2])
	}
}

func TestGetUsageStatsIncludesCumulativeCostTotals(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "codex2api.db")
	db, err := New("sqlite", dbPath)
	if err != nil {
		t.Fatalf("New(sqlite) returned error: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	oldCreatedAt := time.Now().Add(-48 * time.Hour)
	if _, err := db.conn.ExecContext(ctx, `
		INSERT INTO usage_logs (
			account_id, endpoint, model, prompt_tokens, completion_tokens, total_tokens,
			status_code, duration_ms, input_tokens, output_tokens, reasoning_tokens, cached_tokens, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
	`, 1, "/v1/responses", "gpt-5.4", 1500, 700, 2200, 200, 180, 1500, 500, 200, 300, sqliteTimeParam(oldCreatedAt)); err != nil {
		t.Fatalf("insert old usage log returned error: %v", err)
	}

	recent := &UsageLogInput{
		AccountID:        1,
		Endpoint:         "/v1/responses",
		Model:            "gpt-5.5-codex",
		StatusCode:       200,
		DurationMs:       240,
		PromptTokens:     1800,
		CompletionTokens: 1100,
		TotalTokens:      2900,
		InputTokens:      1800,
		OutputTokens:     900,
		ReasoningTokens:  200,
		CachedTokens:     400,
	}
	if err := db.InsertUsageLog(ctx, recent); err != nil {
		t.Fatalf("InsertUsageLog returned error: %v", err)
	}
	db.flushLogs()

	stats, err := db.GetUsageStats(ctx)
	if err != nil {
		t.Fatalf("GetUsageStats returned error: %v", err)
	}

	oldCost := estimateUsageCostBreakdownUSD("gpt-5.4", 1500, 500, 200, 300)
	recentCost := estimateUsageCostBreakdownUSD("gpt-5.5-codex", 1800, 900, 200, 400)
	totalCost := AccountCostEstimate{
		InputUSD:  oldCost.InputUSD + recentCost.InputUSD,
		OutputUSD: oldCost.OutputUSD + recentCost.OutputUSD,
		CacheUSD:  oldCost.CacheUSD + recentCost.CacheUSD,
		TotalUSD:  oldCost.TotalUSD + recentCost.TotalUSD,
	}

	if stats.TotalRequests != 2 {
		t.Fatalf("TotalRequests = %d, want 2", stats.TotalRequests)
	}
	if stats.TodayRequests != 1 {
		t.Fatalf("TodayRequests = %d, want 1", stats.TodayRequests)
	}
	if stats.TotalTokens != 5100 {
		t.Fatalf("TotalTokens = %d, want 5100", stats.TotalTokens)
	}
	if stats.TodayTokens != 2900 {
		t.Fatalf("TodayTokens = %d, want 2900", stats.TodayTokens)
	}
	if stats.TotalPrompt != 3300 {
		t.Fatalf("TotalPrompt = %d, want 3300", stats.TotalPrompt)
	}
	if stats.TotalCompletion != 1800 {
		t.Fatalf("TotalCompletion = %d, want 1800", stats.TotalCompletion)
	}
	if stats.TotalCachedTokens != 700 {
		t.Fatalf("TotalCachedTokens = %d, want 700", stats.TotalCachedTokens)
	}
	assertFloatEquals(t, stats.TotalInputCostUSD, totalCost.InputUSD)
	assertFloatEquals(t, stats.TotalOutputCostUSD, totalCost.OutputUSD)
	assertFloatEquals(t, stats.TotalCacheCostUSD, totalCost.CacheUSD)
	assertFloatEquals(t, stats.TotalCostUSD, totalCost.TotalUSD)

	if err := db.ClearUsageLogs(ctx); err != nil {
		t.Fatalf("ClearUsageLogs returned error: %v", err)
	}

	statsAfterClear, err := db.GetUsageStats(ctx)
	if err != nil {
		t.Fatalf("GetUsageStats after clear returned error: %v", err)
	}
	if statsAfterClear.TodayRequests != 0 {
		t.Fatalf("TodayRequests after clear = %d, want 0", statsAfterClear.TodayRequests)
	}
	if statsAfterClear.TotalRequests != 2 {
		t.Fatalf("TotalRequests after clear = %d, want 2", statsAfterClear.TotalRequests)
	}
	if statsAfterClear.TotalTokens != 5100 {
		t.Fatalf("TotalTokens after clear = %d, want 5100", statsAfterClear.TotalTokens)
	}
	if statsAfterClear.TotalPrompt != 3300 {
		t.Fatalf("TotalPrompt after clear = %d, want 3300", statsAfterClear.TotalPrompt)
	}
	if statsAfterClear.TotalCompletion != 1800 {
		t.Fatalf("TotalCompletion after clear = %d, want 1800", statsAfterClear.TotalCompletion)
	}
	if statsAfterClear.TotalCachedTokens != 700 {
		t.Fatalf("TotalCachedTokens after clear = %d, want 700", statsAfterClear.TotalCachedTokens)
	}
	assertFloatEquals(t, statsAfterClear.TotalInputCostUSD, totalCost.InputUSD)
	assertFloatEquals(t, statsAfterClear.TotalOutputCostUSD, totalCost.OutputUSD)
	assertFloatEquals(t, statsAfterClear.TotalCacheCostUSD, totalCost.CacheUSD)
	assertFloatEquals(t, statsAfterClear.TotalCostUSD, totalCost.TotalUSD)
}

func assertFloatEquals(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("float mismatch: got=%v want=%v", got, want)
	}
}
