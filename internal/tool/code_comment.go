// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/alibaba/open-code-review/internal/model"
)

const (
	codeCommentCategoryBug             = "bug"
	codeCommentCategorySecurity        = "security"
	codeCommentCategoryPerformance     = "performance"
	codeCommentCategoryMaintainability = "maintainability"
	codeCommentCategoryTest            = "test"
	codeCommentCategoryStyle           = "style"
	codeCommentCategoryDocumentation   = "documentation"
	codeCommentCategoryOther           = "other"

	codeCommentSeverityCritical = "critical"
	codeCommentSeverityHigh     = "high"
	codeCommentSeverityMedium   = "medium"
	codeCommentSeverityLow      = "low"
)

var validCodeCommentCategories = map[string]struct{}{
	codeCommentCategoryBug:             {},
	codeCommentCategorySecurity:        {},
	codeCommentCategoryPerformance:     {},
	codeCommentCategoryMaintainability: {},
	codeCommentCategoryTest:            {},
	codeCommentCategoryStyle:           {},
	codeCommentCategoryDocumentation:   {},
	codeCommentCategoryOther:           {},
}

var validCodeCommentSeverities = map[string]struct{}{
	codeCommentSeverityCritical: {},
	codeCommentSeverityHigh:     {},
	codeCommentSeverityMedium:   {},
	codeCommentSeverityLow:      {},
}

// CodeCommentProvider submits review comments to the per-Agent CommentCollector.
type CodeCommentProvider struct {
	Collector *CommentCollector
}

func (p *CodeCommentProvider) Tool() Tool { return CodeComment }

func (p *CodeCommentProvider) Execute(_ context.Context, args map[string]any) (string, error) {
	if p.Collector == nil {
		return "Error: comment collector is not configured", nil
	}

	comments, errMsg := ParseComments(args)
	if errMsg != "" {
		return errMsg, nil
	}

	for i := range comments {
		p.Collector.Add(comments[i])
	}
	return CommentSucceed, nil
}

// ParseCommentsWithPath is like ParseComments but uses defaultPath as a fallback
// when individual comment objects omit the path field.
//
// repair is non-nil when `comments` arrived as a serialized string that only
// parsed after the deterministic repair (see comment_args_repair.go). It reports a
// schema violation, not a change to the findings: a repair is accepted only when
// no recovered value looks cut short.
func ParseCommentsWithPath(args map[string]any, defaultPath string) (comments []model.LlmComment, repair *CommentRepair, errMsg string) {
	return parseCommentsInner(args, defaultPath)
}

// ParseComments extracts LlmComment entries from tool call arguments without writing
// to the Collector. Returns parsed comments and an error message (empty on success).
//
// The repair description is discarded because this path has no warning channel
// today. A future caller that starts carrying real traffic should switch to
// ParseCommentsWithPath rather than inherit silent repairs.
func ParseComments(args map[string]any) ([]model.LlmComment, string) {
	comments, _, errMsg := parseCommentsInner(args, "")
	return comments, errMsg
}

func parseCommentsInner(args map[string]any, defaultPath string) ([]model.LlmComment, *CommentRepair, string) {
	var rawComments []any
	var repair *CommentRepair
	if arr, ok := args["comments"].([]any); ok && len(arr) > 0 {
		rawComments = arr
	} else if s, ok := args["comments"].(string); ok && s != "" {
		if err := json.Unmarshal([]byte(s), &rawComments); err != nil {
			// A string here is a schema violation that in practice fails to
			// parse and takes the whole batch with it. Try the repair first.
			entries, rep := parseRepairedComments(s)
			if entries == nil {
				// Keep "invalid character" as the parser produced it: that
				// phrasing is what leads the model to regenerate the batch,
				// while naming the schema violation instead makes it resend the
				// same broken string.
				return nil, nil, fmt.Sprintf("Error: failed to parse 'comments' JSON string: %v", err)
			}
			rawComments = entries
			repair = rep
		}
	}
	if len(rawComments) == 0 {
		raw, _ := json.Marshal(args)
		return nil, nil, fmt.Sprintf("Error: 'comments' array is required. Got args: %s", string(raw))
	}

	var comments []model.LlmComment
	for _, raw := range rawComments {
		obj, ok := raw.(map[string]any)
		if !ok {
			continue
		}

		cm := model.LlmComment{}

		if content, ok := obj["content"].(string); ok {
			cm.Content = content
		}
		if suggestion, ok := obj["suggestion_code"].(string); ok {
			cm.SuggestionCode = suggestion
		}
		if existing, ok := obj["existing_code"].(string); ok {
			cm.ExistingCode = existing
		}
		if thinking, ok := obj["thinking"].(string); ok {
			cm.Thinking = thinking
		}
		if category, ok := obj["category"].(string); ok {
			cm.Category = normalizeCodeCommentCategory(category)
		}
		if severity, ok := obj["severity"].(string); ok {
			cm.Severity = normalizeCodeCommentSeverity(severity)
		}
		if ruleID, ok := obj["rule_id"].(string); ok {
			cm.RuleID = strings.TrimSpace(ruleID)
		}
		if path, ok := obj["path"].(string); ok && path != "" {
			cm.Path = path
		}
		if cm.Path == "" {
			cm.Path = defaultPath
		}
		cm.Changes = parseCodeChanges(obj["changes"], cm.Path)

		if cm.Path == "" || cm.Content == "" {
			continue
		}

		comments = append(comments, cm)
	}
	return comments, repair, ""
}

// parseCodeChanges reads the comment's `changes`: replacement text for exact
// line ranges. An entry without a valid range or a string replacement is
// dropped; an entry without a path applies to the comment's file.
func parseCodeChanges(raw any, defaultPath string) []model.CodeChange {
	items, ok := raw.([]any)
	if !ok {
		return nil
	}
	var changes []model.CodeChange
	for _, item := range items {
		obj, ok := item.(map[string]any)
		if !ok {
			continue
		}
		replacement, ok := obj["replacement"].(string)
		if !ok {
			continue
		}
		start, okStart := lineNumber(obj["start_line"])
		end, okEnd := lineNumber(obj["end_line"])
		if !okStart || !okEnd || start < 1 || end < start {
			continue
		}
		path, _ := obj["path"].(string)
		if path = strings.TrimSpace(path); path == "" {
			path = defaultPath
		}
		if path == "" {
			continue
		}
		changes = append(changes, model.CodeChange{Path: path, StartLine: start, EndLine: end, Replacement: replacement})
	}
	return changes
}

// lineNumber reads a line number given as a JSON number or a numeric string.
func lineNumber(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		if n != float64(int(n)) {
			return 0, false
		}
		return int(n), true
	case json.Number:
		i, err := n.Int64()
		return int(i), err == nil
	case string:
		i, err := strconv.Atoi(strings.TrimSpace(n))
		return i, err == nil
	}
	return 0, false
}

func normalizeCodeCommentCategory(category string) string {
	normalized := strings.ToLower(category)
	if _, ok := validCodeCommentCategories[normalized]; ok {
		return normalized
	}
	return codeCommentCategoryOther
}

func normalizeCodeCommentSeverity(severity string) string {
	normalized := strings.ToLower(severity)
	if _, ok := validCodeCommentSeverities[normalized]; ok {
		return normalized
	}
	return codeCommentSeverityLow
}
