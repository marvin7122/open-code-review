// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package tool

import "testing"

func TestToolOutputCapsFromEnv(t *testing.T) {
	cases := []struct {
		value string
		want  int
	}{
		{"", fileReadMaxLines},
		{"abc", fileReadMaxLines},
		{"0", fileReadMaxLines},
		{"-5", fileReadMaxLines},
		{"200", 200},
	}
	for _, c := range cases {
		t.Setenv(envFileReadMaxLines, c.value)
		if got := fileReadLineCap(); got != c.want {
			t.Errorf("%s=%q: fileReadLineCap() = %d, want %d", envFileReadMaxLines, c.value, got, c.want)
		}
	}

	t.Setenv(envCodeSearchMaxCount, "")
	if got := codeSearchMatchCap(); got != gitGrepMaxCount {
		t.Errorf("codeSearchMatchCap() = %d, want default %d", got, gitGrepMaxCount)
	}
	t.Setenv(envCodeSearchMaxCount, "40")
	if got := codeSearchMatchCap(); got != 40 {
		t.Errorf("codeSearchMatchCap() = %d, want 40", got)
	}
}
