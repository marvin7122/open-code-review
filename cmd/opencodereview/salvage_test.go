// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package main

import (
	"bytes"
	"testing"
)

func TestLogSessionIDForSalvage(t *testing.T) {
	var buf bytes.Buffer
	logSessionIDForSalvage(&buf, "9f2d7a1b")
	if got := buf.String(); got != "[ocr] Session: 9f2d7a1b\n" {
		t.Errorf("got %q, want session line", got)
	}
	buf.Reset()
	logSessionIDForSalvage(&buf, "")
	if got := buf.String(); got != "" {
		t.Errorf("empty id wrote %q, want silence", got)
	}
}
