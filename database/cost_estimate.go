package database

import (
	"context"
	"strings"
)

type modelPrice struct {
	InputPerMillion       float64
	OutputPerMillion      float64
	CachedInputPerMillion float64
}

type AccountCostEstimate struct {
	InputUSD  float64
	OutputUSD float64
	CacheUSD  float64
	TotalUSD  float64
}

func collectUsageCostEstimateTotals(ctx context.Context, queryer sqlQueryer) (AccountCostEstimate, error) {
	rows, err := queryer.QueryContext(ctx, `
		SELECT
			COALESCE(model, ''),
			COALESCE(SUM(input_tokens), 0),
			COALESCE(SUM(output_tokens), 0),
			COALESCE(SUM(reasoning_tokens), 0),
			COALESCE(SUM(cached_tokens), 0)
		FROM usage_logs
		WHERE status_code <> 499
		GROUP BY model
	`)
	if err != nil {
		return AccountCostEstimate{}, err
	}
	defer rows.Close()

	var result AccountCostEstimate
	for rows.Next() {
		var model string
		var inputTokens int64
		var outputTokens int64
		var reasoningTokens int64
		var cachedTokens int64
		if err := rows.Scan(&model, &inputTokens, &outputTokens, &reasoningTokens, &cachedTokens); err != nil {
			return AccountCostEstimate{}, err
		}
		breakdown := estimateUsageCostBreakdownUSD(model, inputTokens, outputTokens, reasoningTokens, cachedTokens)
		result.InputUSD += breakdown.InputUSD
		result.OutputUSD += breakdown.OutputUSD
		result.CacheUSD += breakdown.CacheUSD
		result.TotalUSD += breakdown.TotalUSD
	}
	if err := rows.Err(); err != nil {
		return AccountCostEstimate{}, err
	}
	return result, nil
}

var modelPriceTable = map[string]modelPrice{
	"codex-mini-latest": {InputPerMillion: 1.50, OutputPerMillion: 6.00, CachedInputPerMillion: 0.375},
	"gpt-5-codex":       {InputPerMillion: 1.25, OutputPerMillion: 10.00, CachedInputPerMillion: 0.125},
	"gpt-5.1-codex":     {InputPerMillion: 1.25, OutputPerMillion: 10.00, CachedInputPerMillion: 0.125},
	"gpt-5.1-codex-max": {InputPerMillion: 1.25, OutputPerMillion: 10.00, CachedInputPerMillion: 0.125},
	"gpt-5.2-codex":     {InputPerMillion: 1.75, OutputPerMillion: 14.00, CachedInputPerMillion: 0.175},
	"gpt-5.3-codex":     {InputPerMillion: 1.75, OutputPerMillion: 14.00, CachedInputPerMillion: 0.17},
	"gpt-5.4":           {InputPerMillion: 2.50, OutputPerMillion: 15.00, CachedInputPerMillion: 0.625},
	"gpt-5.4-codex":     {InputPerMillion: 2.50, OutputPerMillion: 15.00, CachedInputPerMillion: 0.625},
	"gpt-5.5":           {InputPerMillion: 5.00, OutputPerMillion: 30.00, CachedInputPerMillion: 0.50},
	"gpt-5.5-codex":     {InputPerMillion: 5.00, OutputPerMillion: 30.00, CachedInputPerMillion: 0.50},
}

func normalizePriceModel(model string) string {
	return strings.ToLower(strings.TrimSpace(model))
}

func lookupModelPrice(model string) (modelPrice, bool) {
	normalized := normalizePriceModel(model)
	if normalized == "" {
		return modelPrice{}, false
	}
	if price, ok := modelPriceTable[normalized]; ok {
		return price, true
	}
	switch {
	case strings.HasPrefix(normalized, "gpt-5.4"):
		return modelPriceTable["gpt-5.4"], true
	case strings.HasPrefix(normalized, "gpt-5.5"):
		return modelPriceTable["gpt-5.5"], true
	case strings.HasPrefix(normalized, "gpt-5.3-codex"):
		return modelPriceTable["gpt-5.3-codex"], true
	case strings.HasPrefix(normalized, "gpt-5.2-codex"):
		return modelPriceTable["gpt-5.2-codex"], true
	case strings.HasPrefix(normalized, "gpt-5.1-codex-max"):
		return modelPriceTable["gpt-5.1-codex-max"], true
	case strings.HasPrefix(normalized, "gpt-5.1-codex"):
		return modelPriceTable["gpt-5.1-codex"], true
	case strings.HasPrefix(normalized, "gpt-5-codex"):
		return modelPriceTable["gpt-5-codex"], true
	case strings.HasPrefix(normalized, "codex-mini"):
		return modelPriceTable["codex-mini-latest"], true
	default:
		return modelPrice{}, false
	}
}

func estimateUsageCostBreakdownUSD(model string, inputTokens, outputTokens, reasoningTokens, cachedTokens int64) AccountCostEstimate {
	price, ok := lookupModelPrice(model)
	if !ok {
		return AccountCostEstimate{}
	}

	uncachedInputTokens := inputTokens - cachedTokens
	if uncachedInputTokens < 0 {
		uncachedInputTokens = 0
	}
	billableOutputTokens := outputTokens + reasoningTokens
	if billableOutputTokens < 0 {
		billableOutputTokens = 0
	}

	inputUSD := float64(uncachedInputTokens) * price.InputPerMillion / 1_000_000
	cacheUSD := float64(cachedTokens) * price.CachedInputPerMillion / 1_000_000
	outputUSD := float64(billableOutputTokens) * price.OutputPerMillion / 1_000_000

	return AccountCostEstimate{
		InputUSD:  inputUSD,
		OutputUSD: outputUSD,
		CacheUSD:  cacheUSD,
		TotalUSD:  inputUSD + cacheUSD + outputUSD,
	}
}

func (db *DB) GetTotalUsageCostEstimate(ctx context.Context) (AccountCostEstimate, error) {
	return collectUsageCostEstimateTotals(ctx, db.conn)
}

// GetAccountCostEstimates 按账号聚合 usage_logs，返回估算成本明细（美元）。
func (db *DB) GetAccountCostEstimates(ctx context.Context) (map[int64]AccountCostEstimate, error) {
	rows, err := db.conn.QueryContext(ctx, `
		SELECT
			account_id,
			COALESCE(model, ''),
			COALESCE(SUM(input_tokens), 0),
			COALESCE(SUM(output_tokens), 0),
			COALESCE(SUM(reasoning_tokens), 0),
			COALESCE(SUM(cached_tokens), 0)
		FROM usage_logs
		WHERE status_code <> 499
		GROUP BY account_id, model
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[int64]AccountCostEstimate)
	for rows.Next() {
		var accountID int64
		var model string
		var inputTokens int64
		var outputTokens int64
		var reasoningTokens int64
		var cachedTokens int64
		if err := rows.Scan(&accountID, &model, &inputTokens, &outputTokens, &reasoningTokens, &cachedTokens); err != nil {
			return nil, err
		}
		if accountID <= 0 {
			continue
		}
		item := result[accountID]
		breakdown := estimateUsageCostBreakdownUSD(model, inputTokens, outputTokens, reasoningTokens, cachedTokens)
		item.InputUSD += breakdown.InputUSD
		item.OutputUSD += breakdown.OutputUSD
		item.CacheUSD += breakdown.CacheUSD
		item.TotalUSD += breakdown.TotalUSD
		result[accountID] = item
	}
	return result, rows.Err()
}
