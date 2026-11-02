// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/alibaba/open-code-review/internal/config/template"
	"github.com/alibaba/open-code-review/internal/llm"
	"github.com/alibaba/open-code-review/internal/model"
	"github.com/alibaba/open-code-review/internal/session"
	"github.com/alibaba/open-code-review/internal/tool"
)

func newFilterAgent(t *testing.T, client llm.LLMClient, collector *tool.CommentCollector) *Agent {
	t.Helper()
	sess := session.New(t.TempDir(), "main", "test", session.SessionOptions{ReviewMode: "diff"})
	return New(Args{
		LLMClient:        client,
		Model:            "test",
		Session:          sess,
		CommentCollector: collector,
		Template: template.Template{
			ReviewFilterTask: &template.LlmConversation{
				Messages: []template.ChatMessage{{Role: "user", Content: "Filter: {{comments}}"}},
			},
			MaxTokens:           10000,
			MaxToolRequestTimes: 5,
			MainTask:            template.LlmConversation{Messages: []template.ChatMessage{{Role: "user", Content: "t"}}},
		},
	})
}

// Past the cost cap the filter makes no call; the group's findings are kept
// but marked unverified.
func TestExecuteReviewFilter_MarksUnverifiedPastTheCostCap(t *testing.T) {
	t.Setenv("OCR_MAX_RUN_COST_USD", "0.001")
	t.Setenv("OCR_PRICE_INPUT_PER_M", "1")
	client := &filterRequestCaptureClient{}
	collector := tool.NewCommentCollector()
	collector.Add(model.LlmComment{Path: "a.go", Content: "QC18: finding"})
	a := newFilterAgent(t, client, collector)
	a.runner.RecordUsage(&llm.UsageInfo{PromptTokens: 1000}) // $0.001: the whole cap

	a.executeGroupReviewFilter(context.Background(), FileGroup{Label: "a.go", Diffs: []model.Diff{{NewPath: "a.go", Diff: "+x"}}}, nil)

	if client.calls != 0 {
		t.Fatalf("LLM calls = %d, want 0 past the cap", client.calls)
	}
	got := collector.CommentsForPath("a.go")
	if len(got) != 1 || !got[0].Unverified {
		t.Fatalf("comments = %+v, want the finding kept and marked unverified", got)
	}
}

// With the run's rule statements, the filter re-assigns a finding to the rule
// it violates, drops one that fits no rule, keeps one that fits, and logs
// both changes as warnings.
func TestExecuteReviewFilter_RuleFit(t *testing.T) {
	statements := filepath.Join(t.TempDir(), "rules.json")
	if err := os.WriteFile(statements, []byte(`{"CPP47":"No references into a vector across a reallocating insert.",`+
		`"CPP7":"No UB patterns: dangling views, null dereference.","QC18":"Comments state the non-obvious."}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(envRuleStatements, statements)
	client := &fakeAgentClient{responses: []*llm.ChatResponse{{
		Choices: []llm.Choice{{Message: llm.ResponseMessage{ToolCalls: []llm.ToolCall{
			{ID: "1", Type: "function", Function: llm.FunctionCall{Name: "approve_all_comments", Arguments: `{}`}},
			{ID: "2", Type: "function", Function: llm.FunctionCall{Name: "report_rule_mismatches",
				Arguments: `{"analysis":[],"mismatches":[{"comment_id":"c-0","rule_id":"CPP7"},{"comment_id":"c-1"},{"comment_id":"c-2","rule_id":"NOPE"}]}`}},
		}}}},
		Usage: &llm.UsageInfo{PromptTokens: 10, CompletionTokens: 5},
	}}}
	collector := tool.NewCommentCollector()
	collector.Add(model.LlmComment{Path: "a.cpp", RuleID: "CPP47", Content: "CPP47: shared_ptr may be null here"})
	collector.Add(model.LlmComment{Path: "a.cpp", RuleID: "QC18", Content: "QC18: unrelated to any rule"})
	collector.Add(model.LlmComment{Path: "a.cpp", RuleID: "QC18", Content: "QC18: the comment hides the invariant"})
	a := newFilterAgent(t, client, collector)

	a.executeGroupReviewFilter(context.Background(), FileGroup{Label: "a.cpp", Diffs: []model.Diff{{NewPath: "a.cpp", Diff: "+x"}}}, nil)

	got := collector.CommentsForPath("a.cpp")
	if len(got) != 2 {
		t.Fatalf("comments = %+v, want 2 (one dropped)", got)
	}
	if got[0].RuleID != "CPP7" || got[0].Content != "CPP7: shared_ptr may be null here" {
		t.Errorf("re-assigned comment = %+v", got[0])
	}
	if got[1].RuleID != "QC18" || got[1].Content != "QC18: the comment hides the invariant" {
		t.Errorf("a fitting comment or an invalid re-assignment changed: %+v", got[1])
	}
	types := map[string]int{}
	for _, w := range a.Warnings() {
		types[w.Type]++
	}
	if types["filter_rule_reassigned"] != 1 || types["filter_rule_mismatch"] != 1 {
		t.Errorf("warnings = %v, want one re-assignment and one mismatch", types)
	}
}

// Within the reserve (exploration over, cap not reached) the filter runs.
func TestExecuteReviewFilter_RunsOnTheReserve(t *testing.T) {
	t.Setenv("OCR_MAX_RUN_COST_USD", "0.001")
	t.Setenv("OCR_PRICE_INPUT_PER_M", "1")
	client := &filterRequestCaptureClient{}
	collector := tool.NewCommentCollector()
	collector.Add(model.LlmComment{Path: "a.go", Content: "QC18: finding"})
	a := newFilterAgent(t, client, collector)
	a.runner.RecordUsage(&llm.UsageInfo{PromptTokens: 900}) // $0.0009: past 80 %, below the cap
	if !a.runner.BudgetExhausted() {
		t.Fatal("exploration should be over at 90 % of the cap")
	}

	a.executeGroupReviewFilter(context.Background(), FileGroup{Label: "a.go", Diffs: []model.Diff{{NewPath: "a.go", Diff: "+x"}}}, nil)

	if client.calls != 1 {
		t.Fatalf("LLM calls = %d, want the filter call", client.calls)
	}
	if got := collector.CommentsForPath("a.go"); len(got) != 1 || got[0].Unverified {
		t.Fatalf("comments = %+v, want the finding verified", got)
	}
}
