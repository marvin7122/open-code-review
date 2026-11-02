// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package llm

import "context"

type extraBodyOverrideKey struct{}

// WithExtraBodyOverride returns a context whose requests send the fields of
// override in place of the configured extra_body fields of the same name
// (e.g. {"thinking": {"type": "enabled"}} for one task while the rest of the
// run keeps thinking disabled). The configured streaming choice is not
// affected. A nil or empty override returns ctx unchanged.
func WithExtraBodyOverride(ctx context.Context, override map[string]any) context.Context {
	if len(override) == 0 {
		return ctx
	}
	return context.WithValue(ctx, extraBodyOverrideKey{}, override)
}

// effectiveExtraBody merges the override carried by ctx (if any) over the
// configured extra_body. The configured map is not modified.
func effectiveExtraBody(ctx context.Context, configured map[string]any) map[string]any {
	override, _ := ctx.Value(extraBodyOverrideKey{}).(map[string]any)
	if len(override) == 0 {
		return configured
	}
	merged := make(map[string]any, len(configured)+len(override))
	for k, v := range configured {
		merged[k] = v
	}
	for k, v := range override {
		merged[k] = v
	}
	return merged
}
