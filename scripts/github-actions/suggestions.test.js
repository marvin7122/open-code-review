#!/usr/bin/env node

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

"use strict";

// Unit tests for scripts/github-actions/suggestions.js.
// Run via: node scripts/github-actions/suggestions.test.js

const assert = require("assert");
const path = require("path");
const {
  FALLBACK_REASONS,
  replacementLines,
  renderSuggestionBlock,
  parseSuggestionBlock,
  checkDiffHunks,
  checkRoundTrip,
  clangFormatCheck,
  findingChanges,
  renderFallback,
  applySuggestions,
} = require(path.join(__dirname, "suggestions.js"));

const FILE = [
  "int f(int x) {", //  1
  "  int y = x;", //    2
  "  return y;", //     3
  "}", //               4
  "", //                5
  "int g() {", //       6
  "  return f(1);", //  7
  "}", //               8
];
const OTHER = ["#pragma once", "int f(int x);", "int g();"];
const files = { "src/a.cpp": FILE, "src/a.h": OTHER };
const getLines = async (p) => files[p] || null;
const diff = {
  complete: true,
  known: new Set(["src/a.cpp", "src/a.h", "src/binary.bin"]),
  files: new Map([["src/a.cpp", [{ start: 1, end: 4 }, { start: 6, end: 8 }]], ["src/a.h", [{ start: 1, end: 3 }]]]),
};
const change = (start, end, replacement, p = "src/a.cpp") => ({ path: p, start_line: start, end_line: end, replacement });

const tests = [];
const test = (name, fn) => tests.push({ name, fn });

test("range -> suggestion block: the replacement lines, an empty one deletes", () => {
  assert.strictEqual(renderSuggestionBlock("  return x;"), "```suggestion\n  return x;\n```");
  assert.strictEqual(renderSuggestionBlock("a\nb\n"), "```suggestion\na\nb\n```");
  assert.strictEqual(renderSuggestionBlock(""), "```suggestion\n```");
  assert.strictEqual(renderSuggestionBlock("x\n\n"), "```suggestion\nx\n\n```");
  // A replacement containing a fence gets a longer one, and still round-trips.
  const tricky = "// ```\nint a;";
  assert.strictEqual(renderSuggestionBlock(tricky), "````suggestion\n// ```\nint a;\n````");
  assert.deepStrictEqual(parseSuggestionBlock(renderSuggestionBlock(tricky)), ["// ```", "int a;"]);
  assert.deepStrictEqual(replacementLines(""), []);
  assert.deepStrictEqual(parseSuggestionBlock("```diff\nx\n```"), null);
});

test("diff-hunk check: both ends in one hunk of the file", () => {
  assert.strictEqual(checkDiffHunks(change(2, 3, ""), diff), null);
  assert.strictEqual(checkDiffHunks(change(6, 8, ""), diff), null);
  assert.strictEqual(checkDiffHunks(change(4, 6, ""), diff), "outside_diff"); // spans two hunks
  assert.strictEqual(checkDiffHunks(change(9, 9, ""), diff), "outside_diff");
  assert.strictEqual(checkDiffHunks(change(1, 1, "", "src/untouched.cpp"), diff), "outside_diff");
  assert.strictEqual(checkDiffHunks(change(1, 1, "", "src/binary.bin"), diff), "diff_unknown");
  assert.strictEqual(checkDiffHunks(change(1, 1, "", "src/x.cpp"), { complete: false, known: new Set(), files: new Map() }),
    "diff_unknown");
});

test("round-trip check: applying the suggestion gives exactly the intended file", () => {
  const ok = checkRoundTrip(FILE, change(2, 3, "  return x;"));
  assert.strictEqual(ok.reason, null);
  assert.deepStrictEqual(ok.result, ["int f(int x) {", "  return x;", "}", "", "int g() {", "  return f(1);", "}"]);
  assert.deepStrictEqual(checkRoundTrip(FILE, change(2, 2, "")).result.length, FILE.length - 1);
  assert.strictEqual(checkRoundTrip(FILE, change(2, 3, "  int y = x;\n  return y;")).reason, "no_change");
  assert.strictEqual(checkRoundTrip(FILE, change(8, 9, "}")).reason, "past_end_of_file");
  assert.strictEqual(checkRoundTrip(FILE, change(3, 3, "  return y;\r")).reason, "round_trip");
  // Off by one: the range starts one line late, so the replacement repeats
  // line 1 and committing it would duplicate the signature.
  assert.strictEqual(checkRoundTrip(FILE, change(2, 2, "int f(int x) {\n  const int y = x;")).reason, "repeats_neighbor");
  assert.strictEqual(checkRoundTrip(FILE, change(2, 2, "  const int y = x;\n  return y;")).reason, "repeats_neighbor");
});

test("clang-format check: C++ only, changed lines of the result, reason by outcome", () => {
  const calls = [];
  const run = (status, stderr, error) => (bin, args, opts) => {
    calls.push({ bin, args, opts });
    return { status, stderr, error };
  };
  const env = { OCR_SUGGESTION_CLANG_FORMAT: "clang-format-18", OCR_SUGGESTION_STYLE_FILE: "/s/.clang-format" };
  assert.strictEqual(clangFormatCheck({}), null);
  assert.strictEqual(clangFormatCheck(env, run(0, ""))("src/a.cpp", ["a"], 2, 3), null);
  assert.deepStrictEqual(calls[0].args, ["--style=file:/s/.clang-format", "--dry-run", "-Werror",
    "--assume-filename=src/a.cpp", "--lines=2:3"]);
  assert.strictEqual(calls[0].opts.input, "a\n");
  assert.strictEqual(clangFormatCheck(env, run(1, "a.cpp:2:3: error: code should be clang-formatted"))("src/a.h", ["a"], 2, 2),
    "clang_format");
  // A violation reported next to the changed lines only does not count.
  assert.strictEqual(clangFormatCheck(env, run(1, "a.cpp:1:10: error: code should be clang-formatted"))("src/a.h", ["a"], 2, 2),
    null);
  assert.strictEqual(clangFormatCheck(env, run(1, "invalid style"))("src/a.cc", ["a"], 1, 1), "clang_format_failed");
  assert.strictEqual(clangFormatCheck(env, run(null, "", new Error("ENOENT")))("x.hpp", ["a"], 1, 1), "clang_format_failed");
  const before = calls.length;
  assert.strictEqual(clangFormatCheck(env, run(1, ""))("README.md", ["a"], 1, 1), null);
  assert.strictEqual(clangFormatCheck(env, run(1, ""))("src/a.cpp", ["a"], 3, 2), null); // pure deletion
  assert.strictEqual(calls.length, before);
});

test("legacy suggestion_code: the unique exact place of existing_code", async () => {
  const legacy = { path: "src/a.cpp", start_line: 1, end_line: 8, existing_code: "  return y;", suggestion_code: "  return x;" };
  assert.deepStrictEqual(await findingChanges(legacy, getLines), [change(3, 3, "  return x;")]);
  const missing = { ...legacy, existing_code: "  return z;" };
  assert.strictEqual((await findingChanges(missing, getLines))[0].start_line, 0);
  assert.deepStrictEqual(await findingChanges({ path: "src/a.cpp", content: "x" }, getLines), []);
});

test("multi-location splitting: one comment per place, each with its suggestion", async () => {
  const finding = {
    path: "src/a.cpp", start_line: 1, end_line: 4, content: "QC1: rename f\nmore text", rule_id: "QC1",
    category: "style", severity: "low",
    changes: [change(1, 1, "int h(int x) {"), change(2, 2, "int h(int x);", "src/a.h"), change(7, 7, "  return h(1);")],
  };
  const { comments, stats } = await applySuggestions({ comments: [finding], diff, getLines });
  assert.strictEqual(comments.length, 3);
  assert.strictEqual(comments[0], finding);
  assert.deepStrictEqual([finding.path, finding.start_line, finding.end_line], ["src/a.cpp", 1, 1]);
  assert.strictEqual(finding.suggestionBlock, "```suggestion\nint h(int x) {\n```");
  assert.strictEqual(finding.suggestionFallback, "");
  const [, second, third] = comments;
  assert.deepStrictEqual([second.path, second.start_line, second.end_line], ["src/a.h", 2, 2]);
  assert.strictEqual(second.suggestionBlock, "```suggestion\nint h(int x);\n```");
  assert.strictEqual(second.companionOf, finding);
  assert.strictEqual(second.part, "2/3");
  assert.strictEqual(second.rule_id, "QC1");
  assert.ok(second.content.startsWith("Part 2 of 3 of the change for the finding at `src/a.cpp:1`: QC1: rename f"));
  assert.deepStrictEqual([third.start_line, third.part], [7, "3/3"]);
  assert.deepStrictEqual(stats, { findings_with_change: 1, one_click: 1, locations: 3, locations_one_click: 3, fallback: {} });
});

test("fallback marker: failing places become marked diff blocks, counted per reason", async () => {
  const finding = {
    path: "src/a.cpp", start_line: 6, end_line: 8, content: "QC2: x",
    changes: [change(4, 6, "}\n\nint g() {"), change(7, 7, "  return f(2);"), change(3, 3, "  return y;")],
  };
  const plain = { path: "src/a.cpp", start_line: 2, end_line: 2, content: "QC3: no change implied" };
  const formatCheck = (p, result, first, last) => (first === 7 ? "clang_format" : null);
  const { comments, stats } = await applySuggestions({ comments: [finding, plain], diff, getLines, formatCheck });
  assert.strictEqual(comments.length, 2);
  // No place passed: the finding keeps its anchor and shows all three as diffs.
  assert.deepStrictEqual([finding.start_line, finding.end_line, finding.suggestionBlock], [6, 8, undefined]);
  const blocks = finding.suggestionFallback.split("\n\n⚠").length;
  assert.strictEqual(blocks, 3);
  assert.ok(finding.suggestionFallback.startsWith(
    `⚠ no one-click suggestion possible: ${FALLBACK_REASONS.outside_diff}\n\n\`\`\`diff\n@@ src/a.cpp:4-6 @@\n-}\n-\n-int g() {\n+}\n+\n+int g() {\n\`\`\``));
  assert.ok(finding.suggestionFallback.includes(FALLBACK_REASONS.clang_format));
  assert.ok(finding.suggestionFallback.includes(FALLBACK_REASONS.no_change));
  assert.strictEqual(plain.suggestionBlock, undefined);
  assert.deepStrictEqual(stats, { findings_with_change: 1, one_click: 0, locations: 3, locations_one_click: 0,
    fallback: { outside_diff: 1, clang_format: 1, no_change: 1 } });
  const unresolved = renderFallback({ change: { path: "src/a.cpp", start_line: 0, end_line: 0, replacement: "b" },
    reason: "unresolved_suggestion", oldLines: ["a"] });
  assert.strictEqual(unresolved,
    `⚠ no one-click suggestion possible: ${FALLBACK_REASONS.unresolved_suggestion}\n\n\`\`\`diff\n@@ src/a.cpp @@\n-a\n+b\n\`\`\``);
});

test("real clang-format, when installed", () => {
  const { spawnSync } = require("child_process");
  const bin = process.env.CLANG_FORMAT || "clang-format";
  if (spawnSync(bin, ["--version"]).status !== 0) {
    console.log("  (skipped: no clang-format)");
    return;
  }
  const fs = require("fs");
  const os = require("os");
  const style = path.join(fs.mkdtempSync(path.join(os.tmpdir(), "ocr-style-")), ".clang-format");
  fs.writeFileSync(style, "BasedOnStyle: LLVM\n");
  const check = clangFormatCheck({ OCR_SUGGESTION_CLANG_FORMAT: bin, OCR_SUGGESTION_STYLE_FILE: style }, spawnSync);
  assert.strictEqual(check("x.cpp", ["int f() {", "  int a = 1;", "  return a;", "}"], 2, 2), null);
  assert.strictEqual(check("x.cpp", ["int f() {", "  int a  =  1;", "  return a;", "}"], 2, 2), "clang_format");
});

(async () => {
  for (const { name, fn } of tests) {
    try {
      await fn();
      console.log(`ok - ${name}`);
    } catch (e) {
      console.error(`FAIL - ${name}\n${e.stack}`);
      process.exitCode = 1;
    }
  }
  if (!process.exitCode) console.log("All suggestions tests passed.");
})();
