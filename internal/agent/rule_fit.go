// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/alibaba/open-code-review/internal/llm"
	"github.com/alibaba/open-code-review/internal/stdout"
)

// envRuleStatements names a JSON object {rule id: rule statement} with the
// rules of this run. When set, the review filter also checks that every
// finding violates the statement of the rule it names (rule_id), and either
// re-assigns it to a rule of the run whose statement it does violate or
// drops it.
const envRuleStatements = "OCR_RULE_STATEMENTS"

// loadRuleStatements reads the rule statements, or returns nil when none are
// configured or the file is unreadable (reported; the filter then checks
// correctness only).
func loadRuleStatements() map[string]string {
	path := os.Getenv(envRuleStatements)
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	var statements map[string]string
	if err == nil {
		err = json.Unmarshal(data, &statements)
	}
	if err != nil {
		fmt.Fprintf(stdout.Writer(), "[ocr] WARNING: ignoring %s: %v\n", envRuleStatements, err)
		return nil
	}
	return statements
}

func sortedRuleIDs(statements map[string]string) []string {
	ids := make([]string, 0, len(statements))
	for id := range statements {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// ruleFitSection is appended to the filter prompt: the run's rules and the
// rule-fit task.
func ruleFitSection(statements map[string]string) string {
	var b strings.Builder
	b.WriteString("\n\n### Rule fit\n\n")
	b.WriteString("Each comment names the rule it reports in `rule_id`. The rules of this review:\n\n")
	for _, id := range sortedRuleIDs(statements) {
		fmt.Fprintf(&b, "- %s: %s\n", id, statements[id])
	}
	b.WriteString("\nIn addition to the one tool above, call `report_rule_mismatches` once for the comments whose finding " +
		"does NOT violate the statement of its `rule_id`: give the rule from this list whose statement the finding does " +
		"violate, or no rule_id if none of them fits. A comment whose rule fits needs no entry. Judge the fit only against " +
		"the statements above, not the correctness of the finding.")
	return b.String()
}

// ruleFitTool is the filter tool for rule mismatches; rule_id is limited to
// the run's ids.
func ruleFitTool(statements map[string]string) llm.ToolDef {
	ids := sortedRuleIDs(statements)
	enum := make([]any, len(ids))
	for i, id := range ids {
		enum[i] = id
	}
	return llm.ToolDef{
		Type: "function",
		Function: llm.FunctionDef{
			Name: "report_rule_mismatches",
			Description: "Report comments whose finding does not violate the statement of the rule named in their rule_id. " +
				"For each, give the rule of this review whose statement it does violate, or omit rule_id to drop the comment.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"analysis": map[string]any{
						"type":        "array",
						"description": "First, for every comment: its rule's statement and whether the finding violates it.",
						"items":       map[string]any{"type": "string"},
					},
					"mismatches": map[string]any{
						"type": "array",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"comment_id": map[string]any{"type": "string", "description": "e.g. \"c-1\""},
								"rule_id": map[string]any{
									"type":        "string",
									"enum":        enum,
									"description": "The rule whose statement the finding violates; omit if none fits.",
								},
							},
							"required": []any{"comment_id"},
						},
					},
				},
				"required": []any{"analysis", "mismatches"},
			},
		},
	}
}

// parseRuleMismatches returns, per candidate index, the rule a mismatched
// comment is re-assigned to, or "" to drop it. Unknown ids and rules outside
// statements are ignored.
func parseRuleMismatches(calls []llm.ToolCall, total int, statements map[string]string) map[int]string {
	result := map[int]string{}
	for _, call := range calls {
		if call.Function.Name != "report_rule_mismatches" {
			continue
		}
		var args struct {
			Mismatches []struct {
				CommentID string `json:"comment_id"`
				RuleID    string `json:"rule_id"`
			} `json:"mismatches"`
		}
		if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
			fmt.Fprintf(stdout.Writer(), "[ocr] Review filter: failed to parse rule mismatches: %v\n", err)
			continue
		}
		for _, m := range args.Mismatches {
			var idx int
			if _, err := fmt.Sscanf(m.CommentID, "c-%d", &idx); err != nil || idx < 0 || idx >= total {
				continue
			}
			if _, known := statements[m.RuleID]; m.RuleID != "" && !known {
				continue
			}
			result[idx] = m.RuleID
		}
	}
	return result
}

// reassignRule sets a finding's rule and replaces the rule id its text starts
// with (if any) by the new one.
func reassignRule(content, oldRule, newRule string) string {
	trimmed := strings.TrimLeft(content, " ")
	if oldRule != "" && strings.HasPrefix(trimmed, oldRule+":") {
		return newRule + ":" + strings.TrimPrefix(trimmed, oldRule+":")
	}
	return newRule + ": " + content
}
