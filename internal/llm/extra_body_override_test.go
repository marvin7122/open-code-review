// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package llm

import (
	"context"
	"reflect"
	"testing"
)

func TestEffectiveExtraBody(t *testing.T) {
	configured := map[string]any{
		"thinking": map[string]any{"type": "disabled"},
		"top_k":    20,
	}

	if got := effectiveExtraBody(context.Background(), configured); !reflect.DeepEqual(got, configured) {
		t.Errorf("without override: got %v, want %v", got, configured)
	}
	if ctx := WithExtraBodyOverride(context.Background(), nil); ctx != context.Background() {
		t.Error("an empty override must return the context unchanged")
	}

	ctx := WithExtraBodyOverride(context.Background(), map[string]any{
		"thinking": map[string]any{"type": "enabled"},
	})
	want := map[string]any{
		"thinking": map[string]any{"type": "enabled"},
		"top_k":    20,
	}
	if got := effectiveExtraBody(ctx, configured); !reflect.DeepEqual(got, want) {
		t.Errorf("with override: got %v, want %v", got, want)
	}
	if configured["thinking"].(map[string]any)["type"] != "disabled" {
		t.Error("the configured extra_body must not be modified")
	}
}
