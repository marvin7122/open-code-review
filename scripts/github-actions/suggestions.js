// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

"use strict";

// One-click suggestions (OCR_ONE_CLICK_SUGGESTIONS=true).
//
// A finding that implies a code change carries it as `changes`: replacement
// text for exact line ranges of the files at the head commit. Every range that
// passes the gate below becomes a ```suggestion block on a review comment
// anchored exactly on those lines, so GitHub offers "Commit suggestion"; a
// change at several places becomes one comment per place. A range that fails
// the gate is shown as a diff block under a visible warning with the reason.
//
// The gate, per range:
//   1. the lines lie in one hunk of the PR diff (GitHub only anchors there);
//   2. round trip: the suggestion block as rendered, parsed back the way GitHub
//      reads it and applied to the file at the head commit, gives exactly the
//      file with the replacement applied; the replacement changes something;
//      and it does not repeat the line just outside the range (a range that is
//      off by one would duplicate that line when committed);
//   3. for C and C++ files, clang-format passes on the changed lines of the
//      result (same invocation as the deterministic stage 1 check).

const { spawnSync } = require("child_process");

// Why a range got no one-click suggestion; the keys go into the stats file.
const FALLBACK_REASONS = Object.freeze({
  outside_diff: "the lines are not in the PR diff",
  diff_unknown: "the PR diff of this file is not available",
  file_unreadable: "the file could not be read at the head commit",
  past_end_of_file: "the lines are past the end of the file at the head commit",
  no_change: "the replacement equals the current lines",
  repeats_neighbor: "the replacement repeats the line next to the range (the range looks off by one)",
  round_trip: "the rendered suggestion does not reproduce the intended text",
  clang_format: "the result is not clang-formatted",
  clang_format_failed: "clang-format could not check the result",
  unresolved_suggestion: "the suggested code has no exact line range",
});

const CPP_EXTENSIONS = /\.(h|hh|hpp|hxx|ipp|c|cc|cpp|cxx)$/i;

// The lines a replacement text stands for: one trailing newline is not a line
// of its own, and an empty replacement deletes the range.
function replacementLines(text) {
  const s = String(text == null ? "" : text);
  if (s === "") return [];
  return s.replace(/\n$/, "").split("\n");
}

function fenceFor(lines) {
  const ticks = lines.join("\n").match(/`+/g) || [];
  const longest = ticks.reduce((max, t) => Math.max(max, t.length), 0);
  return "`".repeat(Math.max(3, longest + 1));
}

// The ```suggestion block for a replacement. Unlike a plain fenced block, an
// empty replacement renders as an empty block (GitHub: delete the lines), not
// as one empty line.
function renderSuggestionBlock(replacement) {
  const lines = replacementLines(replacement);
  const fence = fenceFor(lines);
  const body = lines.length ? lines.join("\n") + "\n" : "";
  return `${fence}suggestion\n${body}${fence}`;
}

// The replacement lines GitHub reads from a suggestion block, or null when the
// text is not exactly one suggestion block.
function parseSuggestionBlock(block) {
  const lines = String(block).split("\n");
  if (lines.length < 2) return null;
  const open = /^(`{3,})suggestion$/.exec(lines[0]);
  if (!open || lines[lines.length - 1] !== open[1]) return null;
  const inner = lines.slice(1, -1);
  if (inner.some((l) => new RegExp(`^${open[1]}`).test(l))) return null;
  return inner;
}

function applyLines(fileLines, start, end, lines) {
  return [...fileLines.slice(0, start - 1), ...lines, ...fileLines.slice(end)];
}

function sameLines(a, b) {
  return a.length === b.length && a.every((l, i) => l === b[i]);
}

// Check 1: both ends of the range inside one hunk of the file's diff.
// `diff` is the poster's inventory {files: Map(path -> [{start, end}]), known}.
function checkDiffHunks(change, diff) {
  const ranges = diff && diff.files ? diff.files.get(change.path) : null;
  if (!ranges) {
    if (diff && diff.known && diff.known.has(change.path)) return "diff_unknown";
    return diff && diff.complete === false ? "diff_unknown" : "outside_diff";
  }
  const inOne = ranges.some((r) => change.start_line >= r.start && change.end_line <= r.end);
  return inOne ? null : "outside_diff";
}

// Check 2. Returns {reason, result}: reason null on success, result the file
// lines after the change.
function checkRoundTrip(fileLines, change) {
  const { start_line: start, end_line: end } = change;
  if (end > fileLines.length) return { reason: "past_end_of_file" };
  if (/\r/.test(change.replacement)) return { reason: "round_trip" };
  const wanted = replacementLines(change.replacement);
  const intended = applyLines(fileLines, start, end, wanted);
  const parsed = parseSuggestionBlock(renderSuggestionBlock(change.replacement));
  if (!parsed || !sameLines(applyLines(fileLines, start, end, parsed), intended)) {
    return { reason: "round_trip" };
  }
  const current = fileLines.slice(start - 1, end);
  if (sameLines(current, wanted)) return { reason: "no_change" };
  const before = fileLines[start - 2];
  const after = fileLines[end];
  const repeats = (line, neighbor, own) => line !== undefined && neighbor !== undefined &&
    line.trim() !== "" && line === neighbor && line !== own;
  if (wanted.length > 0 && (repeats(wanted[0], before, current[0]) ||
      repeats(wanted[wanted.length - 1], after, current[current.length - 1]))) {
    return { reason: "repeats_neighbor" };
  }
  return { reason: null, result: intended };
}

// Check 3: clang-format on the changed lines of the result, through stdin under
// the file's real path, as in stage 1. Built from OCR_SUGGESTION_CLANG_FORMAT
// (the binary) and OCR_SUGGESTION_STYLE_FILE; without a binary the check is off.
function clangFormatCheck(env = process.env, run = spawnSync) {
  const binary = env.OCR_SUGGESTION_CLANG_FORMAT || "";
  if (!binary) return null;
  const style = env.OCR_SUGGESTION_STYLE_FILE ? `--style=file:${env.OCR_SUGGESTION_STYLE_FILE}` : "--style=file";
  return (path, resultLines, first, last) => {
    if (!CPP_EXTENSIONS.test(path) || last < first) return null;
    const proc = run(binary, [style, "--dry-run", "-Werror", `--assume-filename=${path}`, `--lines=${first}:${last}`],
      { input: resultLines.join("\n") + "\n", encoding: "utf8" });
    if (proc.error) return "clang_format_failed";
    if (proc.status === 0) return null;
    // As in stage 1: a violation counts on the changed lines only (with
    // --lines, clang-format also reports the whitespace just before them).
    const bad = [...String(proc.stderr || "").matchAll(/:(\d+):\d+: (?:error|warning): code should be clang-formatted/g)]
      .map((m) => Number(m[1]));
    if (bad.length === 0) return "clang_format_failed";
    return bad.some((n) => n >= first && n <= last) ? "clang_format" : null;
  };
}

// The change ranges of a finding: its `changes`, or, for a finding with only
// the older suggestion_code/existing_code pair, the one place in the file where
// existing_code occurs exactly (inside the finding's own lines if it occurs
// more than once). Returns [{path, start_line, end_line, replacement}] where
// an unresolvable pair gives start_line 0.
async function findingChanges(comment, getLines) {
  if (Array.isArray(comment.changes) && comment.changes.length > 0) {
    return comment.changes.map((c) => ({
      path: c.path || comment.path,
      start_line: Number(c.start_line),
      end_line: Number(c.end_line),
      replacement: String(c.replacement == null ? "" : c.replacement),
    }));
  }
  if (!comment.suggestion_code || !comment.existing_code) return [];
  const legacy = { path: comment.path, start_line: 0, end_line: 0, replacement: comment.suggestion_code,
    existing: comment.existing_code };
  const lines = await getLines(comment.path);
  const needle = replacementLines(comment.existing_code);
  if (!lines || needle.length === 0) return [legacy];
  const hits = [];
  for (let i = 0; i + needle.length <= lines.length; i++) {
    if (needle.every((l, j) => lines[i + j] === l)) hits.push(i + 1);
  }
  const own = hits.filter((s) => s <= comment.end_line && s + needle.length - 1 >= comment.start_line);
  const pick = hits.length === 1 ? hits[0] : own.length === 1 ? own[0] : 0;
  if (!pick) return [legacy];
  return [{ path: comment.path, start_line: pick, end_line: pick + needle.length - 1,
    replacement: comment.suggestion_code }];
}

// Gate every range of a finding. Returns [{change, reason, oldLines}].
async function gateFindingChanges({ comment, diff, getLines, formatCheck }) {
  const located = [];
  for (const change of await findingChanges(comment, getLines)) {
    if (!(change.start_line >= 1 && change.end_line >= change.start_line)) {
      located.push({ change, reason: "unresolved_suggestion", oldLines: replacementLines(change.existing) });
      continue;
    }
    const lines = await getLines(change.path);
    const oldLines = lines ? lines.slice(change.start_line - 1, change.end_line) : null;
    let reason = checkDiffHunks(change, diff);
    if (!reason && !lines) reason = "file_unreadable";
    let result = null;
    if (!reason) ({ reason, result } = checkRoundTrip(lines, change));
    if (!reason && formatCheck) {
      const added = replacementLines(change.replacement).length;
      reason = formatCheck(change.path, result, change.start_line, change.start_line + added - 1);
    }
    located.push({ change, reason: reason || null, oldLines });
  }
  return located;
}

function renderSuggestion(loc) {
  return renderSuggestionBlock(loc.change.replacement);
}

// The change as a diff block: the current lines and their replacement.
function renderChangeDiff(loc) {
  const { change } = loc;
  const range = change.start_line >= 1 ? `${change.path}:${change.start_line}-${change.end_line}` : change.path;
  const body = [
    `@@ ${range} @@`,
    ...(loc.oldLines || []).map((l) => `-${l}`),
    ...replacementLines(change.replacement).map((l) => `+${l}`),
  ].join("\n");
  const fence = fenceFor([body]);
  return `${fence}diff\n${body}\n${fence}`;
}

// The fallback: the diff block under a visible warning with the reason.
function renderFallback(loc) {
  return `⚠ no one-click suggestion possible: ${FALLBACK_REASONS[loc.reason] || loc.reason}\n\n` +
    renderChangeDiff(loc);
}

function headline(content) {
  const first = String(content || "").split("\n").find((l) => l.trim() !== "") || "";
  return first.length > 200 ? first.slice(0, 199) + "…" : first;
}

function emptyStats() {
  return { findings_with_change: 0, one_click: 0, locations: 0, locations_one_click: 0, fallback: {} };
}

// Gate the changes of every finding and turn them into comments: the finding
// moves onto its first range that passed (with that suggestion), every further
// passing range becomes a companion comment with its own suggestion, and every
// failing range is shown in the finding as a marked diff block. Returns
// {comments, stats}; comments keep their order, companions follow their
// finding. A finding counts as one-click when all its ranges passed.
async function applySuggestions({ comments, diff, getLines, formatCheck, log = () => {} }) {
  const stats = emptyStats();
  const out = [];
  for (const comment of comments) {
    const located = await gateFindingChanges({ comment, diff, getLines, formatCheck });
    comment.suggestionsGated = true;
    if (located.length === 0) {
      out.push(comment);
      continue;
    }
    stats.findings_with_change += 1;
    stats.locations += located.length;
    const passed = located.filter((l) => !l.reason);
    const failed = located.filter((l) => l.reason);
    stats.locations_one_click += passed.length;
    if (failed.length === 0) stats.one_click += 1;
    for (const l of failed) {
      stats.fallback[l.reason] = (stats.fallback[l.reason] || 0) + 1;
      log(`No one-click suggestion (${l.reason}) for ${l.change.path}:${l.change.start_line}-${l.change.end_line}`);
    }
    comment.suggestionLocations = located;
    comment.suggestionFallback = failed.map(renderFallback).join("\n\n");
    if (passed.length === 0) {
      out.push(comment);
      continue;
    }
    const [first, ...rest] = passed;
    comment.path = first.change.path;
    comment.start_line = first.change.start_line;
    comment.end_line = first.change.end_line;
    comment.suggestionBlock = renderSuggestion(first);
    out.push(comment);
    rest.forEach((loc, i) => {
      out.push({
        path: loc.change.path,
        start_line: loc.change.start_line,
        end_line: loc.change.end_line,
        content: `Part ${i + 2} of ${passed.length} of the change for the finding at ` +
          `\`${comment.path}:${comment.start_line}\`: ${headline(comment.content)}`,
        category: comment.category,
        severity: comment.severity,
        rule_id: comment.rule_id,
        suggestionsGated: true,
        suggestionBlock: renderSuggestion(loc),
        companionOf: comment,
        part: `${i + 2}/${passed.length}`,
      });
    });
  }
  return { comments: out, stats };
}

module.exports = {
  FALLBACK_REASONS,
  replacementLines,
  renderSuggestionBlock,
  parseSuggestionBlock,
  checkDiffHunks,
  checkRoundTrip,
  clangFormatCheck,
  findingChanges,
  gateFindingChanges,
  renderChangeDiff,
  renderFallback,
  applySuggestions,
  emptyStats,
};
