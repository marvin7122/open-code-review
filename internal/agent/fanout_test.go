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
