// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package rules

import "testing"

func TestProjectRuleMatchesAllInDeclarationOrder(t *testing.T) {
	resolver := &composedResolver{
		project: &ProjectRule{Rules: []ProjectRuleEntry{
			{Path: "**/*.go", Rule: "first"},
			{Path: "src/*.go", Rule: "second", MergeSystemRule: true},
			{Path: "**/*.go", Rule: "third"},
		}},
		system: &SystemRule{DefaultRule: "system"},
	}

	got := resolver.ResolveAllProjectRules("src/main.go")
	if len(got) != 3 {
		t.Fatalf("got %d matches, want 3: %+v", len(got), got)
	}
	for i, want := range []string{"first", "second", "third"} {
		if got[i].Rule != want && (i != 1 || got[i].Rule == "") {
			t.Errorf("match %d rule = %q, want %q", i, got[i].Rule, want)
		}
	}
	if got[1].Source != "project" || got[1].Pattern != "src/*.go" {
		t.Fatalf("merged match metadata = %+v", got[1])
	}
	if legacy := resolver.Resolve("src/main.go"); legacy != "first" {
		t.Fatalf("legacy Resolve = %q, want first match", legacy)
	}
}

func TestProjectRuleMatchesAllReturnsNoFallback(t *testing.T) {
	resolver := &composedResolver{project: &ProjectRule{Rules: []ProjectRuleEntry{{Path: "**/*.go", Rule: "go"}}}}
	if got := resolver.ResolveAllProjectRules("README.md"); got != nil {
		t.Fatalf("unmatched project rules = %+v, want nil", got)
	}
}
