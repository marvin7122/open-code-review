// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package agent

import (
	"reflect"
	"testing"
)

func TestReviewFilterExtraBody(t *testing.T) {
	t.Setenv(envReviewFilterExtraBody, "")
	if got := reviewFilterExtraBody(); got != nil {
		t.Errorf("unset: got %v, want nil", got)
	}

	t.Setenv(envReviewFilterExtraBody, "not json")
	if got := reviewFilterExtraBody(); got != nil {
		t.Errorf("invalid JSON: got %v, want nil", got)
	}

	t.Setenv(envReviewFilterExtraBody, `{"thinking": {"type": "enabled"}}`)
	want := map[string]any{"thinking": map[string]any{"type": "enabled"}}
	if got := reviewFilterExtraBody(); !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}
