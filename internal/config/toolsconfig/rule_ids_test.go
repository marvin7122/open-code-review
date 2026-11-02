// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package toolsconfig

import (
	"encoding/json"
	"reflect"
	"testing"
)

func commentItem(t *testing.T, tools []ToolConfigEntry) map[string]any {
	t.Helper()
	for _, tool := range tools {
		if tool.Name != "code_comment" {
			continue
		}
		var def map[string]any
		if err := json.Unmarshal(tool.Definition, &def); err != nil {
			t.Fatal(err)
		}
		item, ok := nested(def, "parameters", "properties", "comments", "items")
		if !ok {
			t.Fatal("no comments.items schema")
		}
		return item
	}
	t.Fatal("no code_comment tool")
	return nil
}

// With the run's rule ids, rule_id is a required enum of exactly those ids.
func TestRestrictRuleIDs(t *testing.T) {
	t.Setenv(envRuleIDs, "QC18, PP7 rule:includes")
	tools, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	item := commentItem(t, tools)
	ruleID := item["properties"].(map[string]any)["rule_id"].(map[string]any)
	if got, want := ruleID["enum"], []any{"QC18", "PP7", "rule:includes"}; !reflect.DeepEqual(got, want) {
		t.Errorf("enum = %v, want %v", got, want)
	}
	required := item["required"].([]any)
	count := 0
	for _, r := range required {
		if r == "rule_id" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("rule_id appears %d times in required %v, want once", count, required)
	}
	// Applying it again does not duplicate the requirement.
	if err := RestrictRuleIDs(tools, []string{"QC18"}); err != nil {
		t.Fatal(err)
	}
	if n := len(commentItem(t, tools)["required"].([]any)); n != len(required) {
		t.Errorf("required grew from %d to %d", len(required), n)
	}
}

// Without rule ids the schema keeps rule_id optional and open.
func TestRestrictRuleIDsUnset(t *testing.T) {
	t.Setenv(envRuleIDs, "")
	tools, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	item := commentItem(t, tools)
	if _, ok := item["properties"].(map[string]any)["rule_id"].(map[string]any)["enum"]; ok {
		t.Error("rule_id has an enum without rule ids")
	}
	for _, r := range item["required"].([]any) {
		if r == "rule_id" {
			t.Error("rule_id is required without rule ids")
		}
	}
}
