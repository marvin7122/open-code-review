// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package agent

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/alibaba/open-code-review/internal/stdout"
)

// envReviewFilterExtraBody names a JSON object whose fields replace the
// configured extra_body fields for the REVIEW_FILTER_TASK requests only. It
// lets a run review without thinking mode (cheap) and still verify its
// findings with thinking enabled, e.g.
// OCR_REVIEW_FILTER_EXTRA_BODY='{"thinking": {"type": "enabled"}}'.
const envReviewFilterExtraBody = "OCR_REVIEW_FILTER_EXTRA_BODY"

// reviewFilterExtraBody returns the override from envReviewFilterExtraBody, or
// nil when it is unset or not a JSON object; an invalid value is reported and
// otherwise ignored, so a typo never fails the review.
func reviewFilterExtraBody() map[string]any {
	raw := os.Getenv(envReviewFilterExtraBody)
	if raw == "" {
		return nil
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		fmt.Fprintf(stdout.Writer(), "[ocr] WARNING: ignoring %s: %v\n", envReviewFilterExtraBody, err)
		return nil
	}
	return body
}
