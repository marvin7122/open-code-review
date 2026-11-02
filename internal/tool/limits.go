// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package tool

import (
	"os"
	"strconv"
)

// Environment overrides for the tool output caps. Every line a tool returns
// stays in the conversation and is re-sent on each later turn, so smaller
// caps make long reviews cheaper. Unset, empty, or non-positive values keep
// the defaults.
const (
	envFileReadMaxLines   = "OCR_FILE_READ_MAX_LINES"
	envCodeSearchMaxCount = "OCR_CODE_SEARCH_MAX_COUNT"
)

// positiveIntFromEnv returns the positive integer in the environment variable
// name, or def when it is unset or not a positive integer.
func positiveIntFromEnv(name string, def int) int {
	v, err := strconv.Atoi(os.Getenv(name))
	if err != nil || v <= 0 {
		return def
	}
	return v
}

// fileReadLineCap is the maximum number of lines one file_read call returns.
func fileReadLineCap() int {
	return positiveIntFromEnv(envFileReadMaxLines, fileReadMaxLines)
}

// codeSearchMatchCap is the maximum number of matches one code_search call
// returns.
func codeSearchMatchCap() int {
	return positiveIntFromEnv(envCodeSearchMaxCount, gitGrepMaxCount)
}
