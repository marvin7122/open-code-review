// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package agent

import (
	"testing"

	"github.com/alibaba/open-code-review/internal/config/rules"
	"github.com/alibaba/open-code-review/internal/model"
	"github.com/alibaba/open-code-review/internal/tool"
)

type fanoutTestResolver struct {
	matches map[string][]rules.RuleDetail
}

func (r fanoutTestResolver) Resolve(path string) string                 { return "legacy" }
func (r fanoutTestResolver) ResolveDetail(path string) rules.RuleDetail { return rules.RuleDetail{} }
func (r fanoutTestResolver) ResolveAllProjectRules(path string) []rules.RuleDetail {
	return r.matches[path]
}

func TestFanoutProjectRuleGroupsCreatesIndependentTasks(t *testing.T) {
	resolver := fanoutTestResolver{matches: map[string][]rules.RuleDetail{
		"a.go": {{Pattern: "*.go", Rule: "rule-a"}, {Pattern: "**/*.go", Rule: "rule-b"}},
	}}
	groups := fanOutProjectRuleGroups([]model.Diff{{NewPath: "a.go"}, {NewPath: "b.go"}}, resolver)
	if len(groups) != 3 {
		t.Fatalf("got %d groups, want 3", len(groups))
	}
	if groups[0].RuleIdentity == "" || groups[0].TaskKey == groups[1].TaskKey {
		t.Fatalf("fan-out identities are not unique: %+v", groups)
	}
	if len(groups[2].Diffs) != 1 || groups[2].Diffs[0].NewPath != "b.go" || groups[2].RuleIdentity != "" {
		t.Fatalf("fallback group = %+v", groups[2])
	}
}

func TestFanoutDeduplicatesOnlyNewComments(t *testing.T) {
	collector := tool.NewCommentCollector()
	collector.Add(model.LlmComment{Path: "a.go", Content: "legacy"})
	a := New(Args{CommentCollector: collector})
	start := collector.Snapshot()
	collector.Add(model.LlmComment{Path: "a.go", Content: "same"})
	collector.Add(model.LlmComment{Path: "a.go", Content: "same"})
	a.deduplicateFanoutComments(start)
	got := collector.Comments()
	if len(got) != 2 || got[0].Content != "legacy" || got[1].Content != "same" {
		t.Fatalf("comments = %+v, want legacy plus one new finding", got)
	}
}

func TestFanoutDisabledPreservesLegacyGrouping(t *testing.T) {
	a := New(Args{})
	if a.args.FanOutProjectRules {
		t.Fatal("fan-out must default to disabled")
	}
}

func TestRuleTriggersHit(t *testing.T) {
	cases := []struct {
		name     string
		diff     string
		triggers []string
		want     bool
	}{
		{"no triggers always runs", "+int x;", nil, true},
		{"matching trigger runs", "+std::string_view s;", []string{`string_view`}, true},
		{"second trigger runs", "+std::span<int> s;", []string{`string_view`, `span`}, true},
		{"no match skips", "+int x;", []string{`string_view`}, false},
		{"empty diff skips gated rule", "", []string{`string_view`}, false},
		{"regex trigger", "+  noexcept  ", []string{`^\+\s*noexcept`}, true},
		{"case-sensitive", "+STRING_VIEW s;", []string{`string_view`}, false},
		{"invalid trigger fails open", "+int x;", []string{`([`}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ruleTriggersHit(tc.diff, tc.triggers); got != tc.want {
				t.Errorf("ruleTriggersHit(%q, %q) = %v, want %v", tc.diff, tc.triggers, got, tc.want)
			}
		})
	}
}

func TestFanoutTriggerGateSkipsNonMatchingGroups(t *testing.T) {
	resolver := fanoutTestResolver{matches: map[string][]rules.RuleDetail{
		"a.cpp": {
			{Pattern: "*.cpp", Rule: "gated", Trigger: []string{`string_view`}},
			{Pattern: "*.cpp", Rule: "ungated"},
			{Pattern: "*.cpp", Rule: "broken-trigger", Trigger: []string{`([`}},
		},
	}}
	diffWithMatch := "@@ +std::string_view s;\n"
	diffWithoutMatch := "@@ +int x;\n"
	groups := fanOutProjectRuleGroups([]model.Diff{
		{NewPath: "a.cpp", Diff: diffWithMatch},
	}, resolver)
	if len(groups) != 3 {
		t.Fatalf("matching diff: got %d groups, want 3 (gated+ungated+broken-trigger)", len(groups))
	}
	groups = fanOutProjectRuleGroups([]model.Diff{
		{NewPath: "a.cpp", Diff: diffWithoutMatch},
	}, resolver)
	// Gated rule skipped; ungated and broken-trigger (fail-open) still run.
	if len(groups) != 2 {
		t.Fatalf("non-matching diff: got %d groups, want 2, groups=%+v", len(groups), groups)
	}
	for _, g := range groups {
		if g.Rule == "gated" {
			t.Errorf("gated rule produced a group without trigger match: %+v", g)
		}
	}
}
