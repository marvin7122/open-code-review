// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package rules

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestProjectRuleEntryTriggerUnmarshal covers the optional trigger field:
// present triggers parse, absent triggers stay nil (fail-open: always run).
func TestProjectRuleEntryTriggerUnmarshal(t *testing.T) {
	var pr ProjectRule
	in := `{"rules": [` +
		`{"path": "**/*.cpp", "rule": "r1", "trigger": ["string_view", "span"]},` +
		`{"path": "**/*.cpp", "rule": "r2"}` +
		`]}`
	if err := json.Unmarshal([]byte(in), &pr); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(pr.Rules) != 2 {
		t.Fatalf("rules len = %d, want 2", len(pr.Rules))
	}
	got := pr.Rules[0].Trigger
	if len(got) != 2 || got[0] != "string_view" || got[1] != "span" {
		t.Errorf("triggers = %q, want [string_view span]", got)
	}
	if pr.Rules[1].Trigger != nil {
		t.Errorf("absent triggers = %q, want nil", pr.Rules[1].Trigger)
	}
}

// TestValidateRuleTriggers accepts compilable patterns and rejects malformed
// ones with the offending rule index.
func TestValidateRuleTriggers(t *testing.T) {
	valid := []ProjectRuleEntry{
		{Path: "**/*.cpp", Rule: "r", Trigger: []string{`std::move`, `noexcept`, ``}},
		{Path: "**/*.cpp", Rule: "r"},
	}
	if err := validateRuleTriggers(valid); err != nil {
		t.Errorf("valid triggers rejected: %v", err)
	}
	invalid := []ProjectRuleEntry{
		{Path: "**/*.cpp", Rule: "r", Trigger: []string{`ok`}},
		{Path: "**/*.cpp", Rule: "r", Trigger: []string{`([`}},
	}
	err := validateRuleTriggers(invalid)
	if err == nil {
		t.Fatal("invalid trigger accepted, want error")
	}
	if !strings.Contains(err.Error(), "rule 1") {
		t.Errorf("error = %q, want rule index 1", err.Error())
	}
}

// TestResolveAllProjectRulesPrefersCustom ensures an explicit --rule file
// partitions fan-out: matching paths resolve from the custom layer only,
// paths it does not cover fall back to the project layer, and a nil custom
// layer preserves the legacy project-only behavior.
func TestResolveAllProjectRulesPrefersCustom(t *testing.T) {
	newResolver := func() *composedResolver {
		return &composedResolver{
			custom: &ProjectRule{Rules: []ProjectRuleEntry{
				{Path: "src/chunk/**", Rule: "chunk-rule"},
			}},
			project: &ProjectRule{Rules: []ProjectRuleEntry{
				{Path: "**/*.cpp", Rule: "project-rule"},
			}},
			system: &SystemRule{DefaultRule: "system"},
		}
	}
	got := newResolver().ResolveAllProjectRules("src/chunk/a.cpp")
	if len(got) != 1 || got[0].Rule != "chunk-rule" || got[0].Source != "custom" {
		t.Fatalf("custom match = %+v, want single custom chunk-rule", got)
	}
	got = newResolver().ResolveAllProjectRules("src/other/b.cpp")
	if len(got) != 1 || got[0].Rule != "project-rule" || got[0].Source != "project" {
		t.Fatalf("fallback match = %+v, want single project project-rule", got)
	}
	legacy := &composedResolver{
		project: &ProjectRule{Rules: []ProjectRuleEntry{
			{Path: "**/*.cpp", Rule: "project-rule"},
		}},
		system: &SystemRule{DefaultRule: "system"},
	}
	got = legacy.ResolveAllProjectRules("src/other/b.cpp")
	if len(got) != 1 || got[0].Source != "project" {
		t.Fatalf("nil-custom match = %+v, want single project detail", got)
	}
}

// TestResolveAllProjectRulesCarriesTriggers ensures the fan-out gate sees the
// entry triggers on the resolved details.
func TestResolveAllProjectRulesCarriesTriggers(t *testing.T) {
	resolver := &composedResolver{
		project: &ProjectRule{Rules: []ProjectRuleEntry{
			{Path: "**/*.cpp", Rule: "gated", Trigger: []string{`string_view`}},
			{Path: "**/*.cpp", Rule: "ungated"},
		}},
		system: &SystemRule{DefaultRule: "system"},
	}
	got := resolver.ResolveAllProjectRules("src/a.cpp")
	if len(got) != 2 {
		t.Fatalf("got %d matches, want 2", len(got))
	}
	if len(got[0].Trigger) != 1 || got[0].Trigger[0] != "string_view" {
		t.Errorf("gated triggers = %q, want [string_view]", got[0].Trigger)
	}
	if got[1].Trigger != nil {
		t.Errorf("ungated triggers = %q, want nil", got[1].Trigger)
	}
}
