// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package agent

import (
	"sync"

	"github.com/alibaba/open-code-review/internal/model"
	"github.com/alibaba/open-code-review/internal/session"
)

// fanoutFileStats aggregates every project-rule task for one changed file.
// A file is complete only when every required task succeeds.
type fanoutFileStats struct {
	diff       model.Diff
	required   int
	succeeded  int
	failed     int
	failClass  session.FailureClass
	failReason string
	failDetail string
}

type fanoutTracker struct {
	mu    sync.Mutex
	files map[string]*fanoutFileStats
}

func newFanoutTracker(groups []FileGroup) *fanoutTracker {
	t := &fanoutTracker{files: make(map[string]*fanoutFileStats, len(groups))}
	for _, g := range groups {
		if len(g.Diffs) == 0 {
			continue
		}
		d := g.Diffs[0]
		stats := t.ensure(d)
		stats.required++
	}
	return t
}

func (t *fanoutTracker) ensure(d model.Diff) *fanoutFileStats {
	key := d.NewPath
	if stats, ok := t.files[key]; ok {
		return stats
	}
	stats := &fanoutFileStats{diff: d}
	t.files[key] = stats
	return stats
}

func (t *fanoutTracker) succeed(d model.Diff) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.ensure(d).succeeded++
}

func (t *fanoutTracker) fail(d model.Diff, class session.FailureClass, reason, detail string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	stats := t.ensure(d)
	stats.failed++
	stats.failClass = class
	stats.failReason = reason
	stats.failDetail = detail
}

func (t *fanoutTracker) finalize(a *Agent) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, stats := range t.files {
		d := stats.diff
		fingerprint := reviewItemFingerprint(a.reviewMode(), d)
		if stats.failed == 0 && stats.succeeded == stats.required && stats.required > 0 {
			a.markCompleted(d)
			comments := a.args.CommentCollector.CommentsForPath(d.NewPath)
			a.session.RecordReviewItemDone(d.NewPath, d.OldPath, d.NewPath, fingerprint, comments)
			continue
		}
		class := stats.failClass
		reason := stats.failReason
		if class == "" {
			class = session.FailureBudget
			reason = "not all project-rule review tasks completed"
		}
		a.markFailed(d, class, reason)
		if stats.failDetail != "" {
			a.session.RecordReviewItemFailed(d.NewPath, d.OldPath, d.NewPath, fingerprint, stats.failDetail)
		}
	}
}
