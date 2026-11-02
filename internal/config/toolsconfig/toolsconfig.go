// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

// Package toolsconfig loads tool definitions from JSON config files.
package toolsconfig

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// ToolConfigEntry holds a single tool definition loaded from tools.json.
type ToolConfigEntry struct {
	Name       string          `json:"name"`
	PlanTask   bool            `json:"plan_task"`
	MainTask   bool            `json:"main_task"`
	Definition json.RawMessage `json:"definition"`
}

//go:embed tools.json
var defaultToolsJSON []byte

// Load parses the tools config file. When path is empty, falls back to
// the embedded default tools configuration.
func Load(path string) ([]ToolConfigEntry, error) {
	var data []byte
	var err error
	if path == "" {
		data = defaultToolsJSON
	} else {
		data, err = os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read tools file %s: %w", path, err)
		}
	}
	var tools []ToolConfigEntry
	if err := json.Unmarshal(data, &tools); err != nil {
		return nil, fmt.Errorf("unmarshal tools file: %w", err)
	}
	if err := RestrictRuleIDs(tools, ruleIDsFromEnv()); err != nil {
		return nil, err
	}
	return tools, nil
}

// envRuleIDs is the same variable as tool.EnvRuleIDs, named here to keep
// this package free of a dependency on package tool.
const envRuleIDs = "OCR_LEG_RULE_IDS"

func ruleIDsFromEnv() []string {
	return strings.FieldsFunc(os.Getenv(envRuleIDs), func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t' || r == '\n'
	})
}

// RestrictRuleIDs makes rule_id a required field of every code_comment item
// whose value must be one of ids (a JSON-schema enum), so the model states the
// rule of each finding from a closed set. No ids leave the tools unchanged.
func RestrictRuleIDs(tools []ToolConfigEntry, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	for i := range tools {
		if tools[i].Name != "code_comment" {
			continue
		}
		var def map[string]any
		if err := json.Unmarshal(tools[i].Definition, &def); err != nil {
			return fmt.Errorf("code_comment definition: %w", err)
		}
		item, ok := nested(def, "parameters", "properties", "comments", "items")
		if !ok {
			return fmt.Errorf("code_comment definition has no comments.items schema")
		}
		props, ok := item["properties"].(map[string]any)
		if !ok {
			return fmt.Errorf("code_comment items have no properties")
		}
		ruleID, _ := props["rule_id"].(map[string]any)
		if ruleID == nil {
			ruleID = map[string]any{"type": "string"}
		}
		enum := make([]any, len(ids))
		for j, id := range ids {
			enum[j] = id
		}
		ruleID["enum"] = enum
		props["rule_id"] = ruleID
		required, _ := item["required"].([]any)
		hasRuleID := false
		for _, r := range required {
			hasRuleID = hasRuleID || r == "rule_id"
		}
		if !hasRuleID {
			item["required"] = append(required, "rule_id")
		}
		patched, err := json.Marshal(def)
		if err != nil {
			return fmt.Errorf("code_comment definition: %w", err)
		}
		tools[i].Definition = patched
	}
	return nil
}

// nested walks m along keys and returns the object found there.
func nested(m map[string]any, keys ...string) (map[string]any, bool) {
	for _, k := range keys {
		next, ok := m[k].(map[string]any)
		if !ok {
			return nil, false
		}
		m = next
	}
	return m, true
}

// ToolDefsByPhase returns the parsed tool definitions filtered by phase.
// planOnly=true returns only tools with plan_task:true.
// planOnly=false returns only tools with main_task:true.
func (t *ToolConfigEntry) ToolDefsByPhase(planOnly bool) (json.RawMessage, bool) {
	switch {
	case planOnly && t.PlanTask:
		return t.Definition, true
	case !planOnly && t.MainTask:
		return t.Definition, true
	default:
		return nil, false
	}
}
