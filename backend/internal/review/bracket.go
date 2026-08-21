package review

import (
	stdctx "context"
	"fmt"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/treewatch"
)

// THE REVIEWER READS A TREE SOMEBODY IS WRITING, AND SAYS SO.
//
// A reviewer is a pure reader - `reviewerFloor` forbids it from committing,
// editing or touching the branch - but a reader can still see a half-written
// file, and a review of a torn tree is worse than no review.
//
// This used to be a REFUSAL: Trigger returned ErrTreeBusy while another member
// of the crew was awake, and the review ran later, in the gap. Both members are
// awake continuously now, so there is no gap: the refusal would fire on every
// trigger and review would simply never run.
//
// So review takes the same answer qa took, from the same mechanism: it brackets
// its run with a write-generation lease over the worktree, and a run the tree
// moved under is thrown away instead of recorded. One mechanism, two consumers -
// the registry refcounts per worktree, so a reviewer's lease and qa's read one
// consistent counter.
//
// A SOLO worker is bracketed by nothing at all. Its tree has exactly one writer,
// that writer is the session under review, and reviewing while it works is what
// AO has always done - so no lease is taken, no watcher starts, and a solo review
// is byte-for-byte what it was.

// Watcher is the tree-write detector. *treewatch.Registry satisfies it, and it is
// the same registry the crew-run bracket uses.
type Watcher interface {
	Attach(ctx stdctx.Context, root string) (*treewatch.Lease, error)
}

// BracketVerdict is what a closed bracket says about the run it wrapped.
type BracketVerdict struct {
	// Discard is true when the result must not be recorded: either the tree moved
	// under the reviewer, or nothing was watching it and no reading can vouch for
	// what it read.
	Discard bool
	// Reason is the body written onto the discarded run, phrased for the human who
	// finds it on the Summary strip.
	Reason string
}

// openBrackets attaches one lease per created run, for a worker whose checkout
// has a second writer in it.
//
// Failure to attach is NOT a failure to review. The lease is absent, so the run
// ends up discarded at submit rather than certified - which is the honest answer
// and the same one the crew-run bracket gives, instead of blocking a review the
// human asked for.
func (e *Engine) openBrackets(ctx stdctx.Context, worker domain.SessionRecord, runs []domain.ReviewRun) {
	if !e.bracketable(worker) {
		return
	}
	root := worker.Metadata.WorkspacePath
	for _, run := range runs {
		lease, err := e.watcher.Attach(ctx, root)
		// Both failure shapes fall through to the SAME place, and deliberately: an
		// unattached run is simply not in the map, so CloseBracket discards it with
		// "nothing was watching this". The absence is reported to the human on the
		// run rather than swallowed in a log line nobody reads.
		if err != nil {
			continue
		}
		if _, down := lease.Down(); down {
			lease.Release()
			continue
		}
		e.bracketMu.Lock()
		if e.brackets == nil {
			e.brackets = map[string]*treewatch.Lease{}
		}
		e.brackets[run.ID] = lease
		e.bracketMu.Unlock()
	}
}

// bracketable answers the one question that keeps solo review untouched: does
// this checkout have a writer OTHER than the session being reviewed? Only a crew
// shares a worktree, so only a crew is bracketed.
func (e *Engine) bracketable(worker domain.SessionRecord) bool {
	return e.watcher != nil && worker.InCrew() && strings.TrimSpace(worker.Metadata.WorkspacePath) != ""
}

// CloseBracket ends a run's bracket and says whether its result may be recorded.
//
// A run that was never bracketed - every solo review, and every review on a
// daemon with no detector - returns a clean verdict, because there was no second
// writer to spoil it.
//
// A run whose lease is MISSING on a bracketed worker is discarded, never
// recorded. The lease lives in memory, so a missing one means the daemon
// restarted mid-review and nothing watched the tree across it. Recording that
// verdict would be certifying on a mechanism that was not running, which is the
// one thing this whole shape must not do.
func (e *Engine) CloseBracket(ctx stdctx.Context, workerID domain.SessionID, runID string) BracketVerdict {
	e.bracketMu.Lock()
	lease, bracketed := e.brackets[runID]
	delete(e.brackets, runID)
	e.bracketMu.Unlock()
	if bracketed {
		defer lease.Release()
	}

	worker, ok, err := e.sessions.GetSession(ctx, workerID)
	if err != nil || !ok || !e.bracketable(worker) {
		return BracketVerdict{}
	}
	if !bracketed {
		return BracketVerdict{
			Discard: true,
			Reason: "Discarded, not reviewed: nothing was watching " + worker.Metadata.WorkspacePath +
				" while this pass ran, so there is no way to tell whether it read a tree the other agent was writing. Trigger it again.",
		}
	}
	end, err := lease.Generation()
	if err != nil {
		return BracketVerdict{
			Discard: true,
			Reason:  "Discarded, not reviewed: the tree-write detector stopped certifying during this pass (" + err.Error() + "). Trigger it again.",
		}
	}
	if end == lease.StartGeneration() {
		return BracketVerdict{}
	}
	return BracketVerdict{
		Discard: true,
		Reason: "Discarded, not reviewed: the other agent wrote to " + worker.Metadata.WorkspacePath +
			" while this pass was reading it" + changedSuffix(lease.Changed()) +
			", so the diff it judged is not the diff that is there now. Trigger it again.",
	}
}

// releaseBracket drops a run's lease without asking a verdict. Every path that
// takes a run out of `running` other than a submit calls it, so a watcher is not
// kept alive by a review nobody will ever close.
func (e *Engine) releaseBracket(runID string) {
	e.bracketMu.Lock()
	lease, ok := e.brackets[runID]
	delete(e.brackets, runID)
	e.bracketMu.Unlock()
	if ok {
		lease.Release()
	}
}

// releaseBracketsForSession drops every lease held for one worker. Used where a
// whole session's running runs are failed at once (an exited reviewer pane, a
// reset, boot reconciliation).
func (e *Engine) releaseBracketsForSession(ctx stdctx.Context, workerID domain.SessionID) {
	runs, err := e.store.ListReviewRunsBySession(ctx, workerID)
	if err != nil {
		return
	}
	for _, run := range runs {
		if run.Status != domain.ReviewRunRunning {
			e.releaseBracket(run.ID)
		}
	}
}

// changedSuffix names the paths that moved, bounded so a big refactor does not
// write an essay onto the run.
func changedSuffix(paths []string) string {
	if len(paths) == 0 {
		return ""
	}
	const shownAtMost = 5
	shown := paths
	extra := ""
	if len(shown) > shownAtMost {
		shown = shown[:shownAtMost]
		extra = fmt.Sprintf(" and %d more", len(paths)-shownAtMost)
	}
	return " (" + strings.Join(shown, ", ") + extra + ")"
}
