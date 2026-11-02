// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package tool

import (
	"fmt"
	"os"
	"strings"

	"github.com/alibaba/open-code-review/internal/model"
)

// EnvRuleIDs names the rule ids a run reviews against (comma- or
// space-separated). When set, every code_comment must name one of them in
// rule_id: the tool schema restricts rule_id to them (toolsconfig), comments
// with any other rule_id are rejected here, and the poster drops what is
// left without one.
const EnvRuleIDs = "OCR_LEG_RULE_IDS"

// RuleIDsFromEnv returns the run's rule ids, or nil when none are set.
func RuleIDsFromEnv() []string {
	return strings.FieldsFunc(os.Getenv(EnvRuleIDs), func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t' || r == '\n'
	})
}

// FilterCommentsByRuleID splits comments into those whose RuleID is one of
// allowed and those without a valid one. With no allowed ids every comment
// is kept.
func FilterCommentsByRuleID(comments []model.LlmComment, allowed []string) (kept, rejected []model.LlmComment) {
	if len(allowed) == 0 {
		return comments, nil
	}
	valid := make(map[string]struct{}, len(allowed))
	for _, id := range allowed {
		valid[id] = struct{}{}
	}
	for _, cm := range comments {
		if _, ok := valid[cm.RuleID]; ok {
			kept = append(kept, cm)
		} else {
			rejected = append(rejected, cm)
		}
	}
	return kept, rejected
}

// RejectedRuleIDNote is the text appended to the tool result when comments
// were rejected for their rule_id, so the model can resend them correctly.
func RejectedRuleIDNote(rejected []model.LlmComment, allowed []string) string {
	if len(rejected) == 0 {
		return ""
	}
	got := make([]string, 0, len(rejected))
	for _, cm := range rejected {
		if cm.RuleID == "" {
			got = append(got, "(none)")
		} else {
			got = append(got, fmt.Sprintf("%q", cm.RuleID))
		}
	}
	return fmt.Sprintf("\n%d comment(s) were NOT recorded: rule_id %s is not one of the rules of this review (%s). "+
		"Resend each finding with the rule_id of the rule it violates, or drop it if no rule covers it.",
		len(rejected), strings.Join(got, ", "), strings.Join(allowed, ", "))
}
