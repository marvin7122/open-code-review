// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package llmloop

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/alibaba/open-code-review/internal/llm"
)

func TestPricesCost(t *testing.T) {
	p := Prices{InputPerM: 0.14, CacheHitPerM: 0.0028, OutputPerM: 0.28}
	// 1 M input of which 0.9 M cache hits, 0.1 M output.
	got := p.Cost(1_000_000, 900_000, 100_000)
	want := 0.1*0.14 + 0.9*0.0028 + 0.1*0.28
	if math.Abs(got-want) > 1e-12 {
		t.Errorf("Cost = %v, want %v", got, want)
	}
	if p.Cost(10, 20, 0) < 0 {
		t.Error("more cache reads than input must not give a negative cost")
	}
}

func TestBudgetFromEnv(t *testing.T) {
	t.Setenv(envMaxLLMCalls, "40")
	t.Setenv(envMaxRunTokens, "3000000")
	t.Setenv(envMaxRunCostUSD, "0.25")
	t.Setenv(envPriceInputPerM, "0.14")
	t.Setenv(envPriceCachePerM, "bad")
	t.Setenv(envPriceOutputPerM, "-1")
	t.Setenv(envUsageFile, "/tmp/usage.json")
	b := BudgetFromEnv()
	want := Budget{MaxCalls: 40, MaxTokens: 3_000_000, MaxCostUSD: 0.25, Prices: Prices{InputPerM: 0.14}, UsageFile: "/tmp/usage.json"}
	if b != want {
		t.Errorf("BudgetFromEnv() = %+v, want %+v", b, want)
	}
}

func newBudgetRunner(b Budget) *Runner {
	r := NewRunner(Deps{})
	r.budget = b
	return r
}

func TestBudgetExhausted(t *testing.T) {
	usage := &llm.UsageInfo{PromptTokens: 1000, CompletionTokens: 100, CacheReadTokens: 800}

	calls := newBudgetRunner(Budget{MaxCalls: 2})
	calls.addUsage(usage)
	if calls.BudgetExhausted() {
		t.Fatal("call cap reached after 1 of 2 calls")
	}
	calls.addUsage(usage)
	if !calls.BudgetExhausted() || !calls.Usage().Truncated {
		t.Fatal("call cap not reached after 2 of 2 calls")
	}
	if len(calls.Warnings()) != 1 {
		t.Errorf("want one budget warning, got %v", calls.Warnings())
	}
	calls.BudgetExhausted()
	if len(calls.Warnings()) != 1 {
		t.Error("the truncation warning must be recorded once")
	}

	tokens := newBudgetRunner(Budget{MaxTokens: 2000})
	tokens.addUsage(usage)
	if tokens.BudgetExhausted() {
		t.Fatal("token cap reached at 1100 of 2000")
	}
	tokens.addUsage(usage)
	if !tokens.BudgetExhausted() {
		t.Fatal("token cap not reached at 2200 of 2000")
	}

	cost := newBudgetRunner(Budget{MaxCostUSD: 0.0001, Prices: Prices{InputPerM: 100}})
	cost.addUsage(usage) // 200 uncached tokens at $100/M = $0.02
	if !cost.BudgetExhausted() {
		t.Fatal("cost cap not reached")
	}

	if newBudgetRunner(Budget{}).BudgetExhausted() {
		t.Error("an empty budget is unlimited")
	}
}

func TestUsageFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "usage.json")
	r := newBudgetRunner(Budget{MaxCalls: 1, UsageFile: file, Prices: Prices{OutputPerM: 1}})
	r.addUsage(&llm.UsageInfo{PromptTokens: 500, CompletionTokens: 1_000_000, CacheReadTokens: 400})
	r.BudgetExhausted()

	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var s UsageSnapshot
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatal(err)
	}
	if s.Calls != 1 || s.InputTokens != 500 || s.CacheReadTokens != 400 || s.OutputTokens != 1_000_000 {
		t.Errorf("unexpected totals %+v", s)
	}
	if math.Abs(s.CostUSD-1) > 1e-9 || !s.Truncated || s.TruncatedReason == "" {
		t.Errorf("want cost 1 and truncated, got %+v", s)
	}
}

// The main loop stops at the hard budget before its next model call and
// reports StopBudget; the calls already made stay counted.
func TestRunMainTask_StopsAtHardBudget(t *testing.T) {
	content := ""
	client := &fakeClient{responses: []*llm.ChatResponse{{
		Choices: []llm.Choice{{Message: llm.ResponseMessage{Content: &content}}},
		Model:   "fake",
		Usage:   &llm.UsageInfo{PromptTokens: 5, CompletionTokens: 5},
	}}}
	deps := newTestDeps(client)
	deps.Template.MaxToolRequestTimes = 10
	runner := NewRunner(deps)
	runner.budget = Budget{MaxCalls: 1}

	msgs := []llm.Message{llm.NewTextMessage("user", "review")}
	completed, stop, err := runner.RunMainTask(context.Background(), msgs, "main.go")
	if err != nil {
		t.Fatalf("RunMainTask: %v", err)
	}
	if completed || stop != StopBudget {
		t.Fatalf("completed = %v, stop = %v; want a StopBudget stop", completed, stop)
	}
	if client.calls != 1 || runner.Usage().Calls != 1 {
		t.Errorf("want exactly 1 model call, got %d (counted %d)", client.calls, runner.Usage().Calls)
	}
}
