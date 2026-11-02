// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package llmloop

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"sync/atomic"

	"github.com/alibaba/open-code-review/internal/llm"
	"github.com/alibaba/open-code-review/internal/stdout"
)

// Environment variables of the hard run budget. Unset, empty or non-positive
// limits are unlimited.
const (
	envMaxLLMCalls     = "OCR_MAX_LLM_CALLS"     // model calls of the run
	envMaxRunTokens    = "OCR_MAX_RUN_TOKENS"    // input (incl. cache reads) + output tokens
	envMaxRunCostUSD   = "OCR_MAX_RUN_COST_USD"  // cost at the prices below
	envPriceInputPerM  = "OCR_PRICE_INPUT_PER_M" // USD per 1 M uncached input tokens
	envPriceCachePerM  = "OCR_PRICE_CACHE_HIT_PER_M"
	envPriceOutputPerM = "OCR_PRICE_OUTPUT_PER_M"
	envUsageFile       = "OCR_USAGE_FILE" // JSON usage snapshot, rewritten after every call
)

// Prices are USD per million tokens.
type Prices struct {
	InputPerM, CacheHitPerM, OutputPerM float64
}

// Cost is the price of the given totals; cacheRead is part of input.
func (p Prices) Cost(input, cacheRead, output int64) float64 {
	uncached := input - cacheRead
	if uncached < 0 {
		uncached = 0
	}
	return (float64(uncached)*p.InputPerM + float64(cacheRead)*p.CacheHitPerM + float64(output)*p.OutputPerM) / 1e6
}

// Budget is a hard limit on one run (one fan-out leg): once a limit is
// reached, no further model call starts, and the run reports what it found so
// far, marked as truncated. Zero limits are unlimited.
type Budget struct {
	MaxCalls   int64
	MaxTokens  int64
	MaxCostUSD float64
	Prices     Prices
	// UsageFile, if set, receives a JSON usage snapshot after every call, so
	// the spend of a run that is killed (provider error, timeout) is known.
	UsageFile string
}

// BudgetFromEnv reads the Budget from the environment variables above.
func BudgetFromEnv() Budget {
	num := func(name string) float64 {
		v, err := strconv.ParseFloat(os.Getenv(name), 64)
		if err != nil || v < 0 {
			return 0
		}
		return v
	}
	return Budget{
		MaxCalls:   int64(num(envMaxLLMCalls)),
		MaxTokens:  int64(num(envMaxRunTokens)),
		MaxCostUSD: num(envMaxRunCostUSD),
		Prices:     Prices{num(envPriceInputPerM), num(envPriceCachePerM), num(envPriceOutputPerM)},
		UsageFile:  os.Getenv(envUsageFile),
	}
}

// UsageSnapshot is the run's usage so far; it is logged and written to the
// usage file after every model call.
type UsageSnapshot struct {
	Calls           int64   `json:"calls"`
	InputTokens     int64   `json:"input_tokens"`
	CacheReadTokens int64   `json:"cache_read_tokens"`
	OutputTokens    int64   `json:"output_tokens"`
	CostUSD         float64 `json:"cost_usd"`
	Truncated       bool    `json:"truncated"`
	TruncatedReason string  `json:"truncated_reason,omitempty"`
}

// addUsage records one model call: it adds the response's usage to the run
// totals, then logs and persists the new totals.
func (r *Runner) addUsage(u *llm.UsageInfo) {
	atomic.AddInt64(&r.totalCalls, 1)
	if u != nil {
		atomic.AddInt64(&r.totalInputTokens, u.PromptTokens)
		atomic.AddInt64(&r.totalOutputTokens, u.CompletionTokens)
		atomic.AddInt64(&r.totalCacheReadTokens, u.CacheReadTokens)
		atomic.AddInt64(&r.totalCacheWriteTokens, u.CacheWriteTokens)
	}
	r.reportUsage()
}

// Usage returns the run's usage so far.
func (r *Runner) Usage() UsageSnapshot {
	s := UsageSnapshot{
		Calls:           atomic.LoadInt64(&r.totalCalls),
		InputTokens:     atomic.LoadInt64(&r.totalInputTokens),
		CacheReadTokens: atomic.LoadInt64(&r.totalCacheReadTokens),
		OutputTokens:    atomic.LoadInt64(&r.totalOutputTokens),
	}
	s.CostUSD = r.budget.Prices.Cost(s.InputTokens, s.CacheReadTokens, s.OutputTokens)
	if reason, ok := r.truncation.Load().(string); ok {
		s.Truncated, s.TruncatedReason = true, reason
	}
	return s
}

// reportUsage logs the usage so far and rewrites the usage file.
func (r *Runner) reportUsage() {
	s := r.Usage()
	fmt.Fprintf(stdout.Writer(), "[ocr] usage: calls=%d input=%d cache_read=%d output=%d cost_usd=%.4f truncated=%t\n",
		s.Calls, s.InputTokens, s.CacheReadTokens, s.OutputTokens, s.CostUSD, s.Truncated)
	if r.budget.UsageFile == "" {
		return
	}
	r.usageFileMu.Lock()
	defer r.usageFileMu.Unlock()
	data, err := json.Marshal(s)
	if err != nil {
		return
	}
	tmp := r.budget.UsageFile + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err == nil {
		_ = os.Rename(tmp, r.budget.UsageFile)
	}
}

// Truncated reports whether the hard budget has already cut the run short.
// Unlike BudgetExhausted it never marks the run.
func (r *Runner) Truncated() bool {
	_, ok := r.truncation.Load().(string)
	return ok
}

// BudgetExhausted reports whether the run has reached a hard limit. The first
// time it has, the run is marked truncated (warning, usage file) with the
// limit as the reason.
func (r *Runner) BudgetExhausted() bool {
	if _, done := r.truncation.Load().(string); done {
		return true
	}
	b, s := r.budget, r.Usage()
	var reason string
	switch {
	case b.MaxCalls > 0 && s.Calls >= b.MaxCalls:
		reason = fmt.Sprintf("model-call cap %d reached", b.MaxCalls)
	case b.MaxTokens > 0 && s.InputTokens+s.OutputTokens >= b.MaxTokens:
		reason = fmt.Sprintf("token cap %d reached (%d input + %d output)", b.MaxTokens, s.InputTokens, s.OutputTokens)
	case b.MaxCostUSD > 0 && s.CostUSD >= b.MaxCostUSD:
		reason = fmt.Sprintf("cost cap $%.2f reached ($%.4f)", b.MaxCostUSD, s.CostUSD)
	default:
		return false
	}
	if r.truncation.CompareAndSwap(nil, reason) {
		fmt.Fprintf(stdout.Writer(), "[ocr] budget: %s; no further model calls, findings so far are kept (truncated)\n", reason)
		r.RecordWarning("budget_truncated", "", "review truncated: "+reason)
		r.reportUsage()
	}
	return true
}
