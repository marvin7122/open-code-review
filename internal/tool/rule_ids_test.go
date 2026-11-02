// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package tool

import (
	"reflect"
	"strings"
	"testing"

	"github.com/alibaba/open-code-review/internal/model"
)

func TestFilterCommentsByRuleID(t *testing.T) {
	comments := []model.LlmComment{
		{Content: "a", RuleID: "QC18"},
		{Content: "b"},
		{Content: "c", RuleID: "CPP99"},
		{Content: "d", RuleID: "PP7"},
	}
	kept, rejected := FilterCommentsByRuleID(comments, []string{"QC18", "PP7"})
	if len(kept) != 2 || kept[0].Content != "a" || kept[1].Content != "d" {
		t.Errorf("kept = %+v", kept)
	}
	if len(rejected) != 2 || rejected[0].Content != "b" || rejected[1].Content != "c" {
		t.Errorf("rejected = %+v", rejected)
	}
	note := RejectedRuleIDNote(rejected, []string{"QC18", "PP7"})
	for _, want := range []string{"2 comment(s) were NOT recorded", "(none)", `"CPP99"`, "QC18, PP7"} {
		if !strings.Contains(note, want) {
			t.Errorf("note %q lacks %q", note, want)
		}
	}

	all, none := FilterCommentsByRuleID(comments, nil)
	if !reflect.DeepEqual(all, comments) || none != nil || RejectedRuleIDNote(none, nil) != "" {
		t.Error("without rule ids every comment is kept")
	}
}

func TestRuleIDsFromEnv(t *testing.T) {
	t.Setenv(EnvRuleIDs, " QC18,PP7  rule:includes\n")
	if got, want := RuleIDsFromEnv(), []string{"QC18", "PP7", "rule:includes"}; !reflect.DeepEqual(got, want) {
		t.Errorf("RuleIDsFromEnv() = %v, want %v", got, want)
	}
}
