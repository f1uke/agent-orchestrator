package session

import (
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// noSignalGrace is how long after spawn/restore a session may stay silent
// before its idle reading is downgraded to StatusNoSignal. It covers the
// agent's TUI boot plus the gap to the first activity-bearing hook callback
// (for Codex that is UserPromptSubmit, seconds after the auto-submitted spawn
// prompt — its SessionStart hook fires earlier but carries no activity state);
// past it, a silent session is indistinguishable from one with a broken hook
// pipeline, and the dashboard must not claim a confident "idle".
const noSignalGrace = 90 * time.Second

// waitingInputGrace is how long a session that HAS signalled may sit idle (its
// turn ended via a Stop hook) before AO treats it as waiting for the human.
// Claude Code's idle "waiting for input" Notification is unreliable/often
// absent, so AO promotes a sustained idle to needs-input itself rather than
// depending on it. Kept short enough to feel responsive, long enough that a
// brief between-turns pause — or a Stop hook that immediately continues the
// agent — reverts to active before it ever promotes (no false "needs you").
const waitingInputGrace = 45 * time.Second

// activeStaleGrace is how long a session may sit in the active state with no
// refreshing signal before AO stops trusting the "working" reading. active is
// the one activity state a lost hook can strand: a turn reports active, then its
// closing Stop never lands (a hung agent, a dropped callback, a daemon restart
// mid-turn) and nothing ever demotes it. Every genuinely working session keeps
// re-reporting active — a per-tool-use hook, or at minimum a UserPromptSubmit —
// so a gap this long means the feed died, not that the agent is busy. Kept
// above the longest plausible single tool run (a long build, a full test suite)
// so a real between-signals lull never trips it.
const activeStaleGrace = 10 * time.Minute

// statusResult is the full outcome of the status derivation: the display Status
// plus WHY it was chosen and, for timeout-based readings, when/what it will flip
// to next. All fields are derived on read from durable facts; none is stored.
type statusResult struct {
	Status           domain.SessionStatus
	Reason           domain.StatusReason
	NextTransitionAt *time.Time
	NextTransitionTo domain.SessionStatus
}

// deriveStatus computes the display status. It delegates to deriveStatusDetail
// and drops the reason/countdown, preserving the original signature for callers
// and tests that only need the status.
//
//nolint:unparam // minApprovals is kept to preserve deriveStatusDetail's signature for callers/tests, even though current callers pass the default
func deriveStatus(rec domain.SessionRecord, prs []domain.PRFacts, now time.Time, signalCapable bool, minApprovals int) domain.SessionStatus {
	return deriveStatusDetail(rec, prs, now, signalCapable, minApprovals).Status
}

// deriveStatusDetail computes the display status AND the reason that produced it,
// plus the pending timeout transition where one applies. The Status it returns
// is identical to the historical deriveStatus for every input — it only adds the
// explanatory metadata. signalCapable says whether this session's harness has an
// activity hook pipeline at all; only then can prolonged silence mean the
// pipeline is broken (no_signal) rather than a hook-less harness's normal quiet.
//
// A session may own several PRs at once (independent or stacked). The PR-derived
// status is the worst-wins aggregate across its open PRs; stacked children whose
// parent is still open are exempt from the aggregation since they cannot merge
// until the parent does. Merged/closed PRs only matter once no open PR remains.
func deriveStatusDetail(rec domain.SessionRecord, prs []domain.PRFacts, now time.Time, signalCapable bool, minApprovals int) statusResult {
	if rec.IsTerminated {
		if anyMerged(prs) {
			return statusResult{Status: domain.StatusMerged, Reason: domain.ReasonMerged}
		}
		return statusResult{Status: domain.StatusTerminated, Reason: domain.ReasonTerminated}
	}

	if rec.Activity.State == domain.ActivityWaitingInput {
		return statusResult{Status: domain.StatusNeedsInput, Reason: domain.ReasonWaitingInput}
	}

	open := openPRs(prs)
	if len(open) > 0 {
		prStatus := aggregatePRStatus(open, minApprovals)
		// While the agent is actively working (auto mode), an open PR sitting in
		// a PROBLEM pipeline state — failing CI, requested changes, pending
		// review — is being fixed autonomously; the human doesn't need to act
		// yet, so surface it as working and defer the problem state until the
		// agent goes stale/idle (mirrors the Reactivated active branch below).
		// POSITIVE states (mergeable/approved) and neutral ones (draft/pr_open)
		// are never overridden: a ready-to-merge PR stays ready even while active.
		if isDeferrableProblemStatus(prStatus) &&
			rec.Activity.State == domain.ActivityActive &&
			now.Sub(rec.Activity.LastActivityAt) <= activeStaleGrace {
			at := rec.Activity.LastActivityAt.Add(activeStaleGrace)
			return statusResult{
				Status:           domain.StatusWorking,
				Reason:           domain.ReasonWorking,
				NextTransitionAt: &at,
				NextTransitionTo: prStatus,
			}
		}
		return statusResult{Status: prStatus, Reason: domain.ReasonPRPipeline}
	}
	// A reactivated session (brought back via Reopen/restore) is waiting for you to
	// direct it: surface it as needs_input so it returns to the board in the "Needs
	// you" zone rather than being pinned to Done by a previously-merged PR — until it
	// takes on new work (an open PR already won above) or is finished again
	// (terminated already won above). An actively-working one still shows working.
	if rec.Reactivated {
		if rec.Activity.State == domain.ActivityActive && now.Sub(rec.Activity.LastActivityAt) <= activeStaleGrace {
			at := rec.Activity.LastActivityAt.Add(activeStaleGrace)
			return statusResult{
				Status:           domain.StatusWorking,
				Reason:           domain.ReasonWorking,
				NextTransitionAt: &at,
				NextTransitionTo: domain.StatusNeedsInput,
			}
		}
		return statusResult{Status: domain.StatusNeedsInput, Reason: domain.ReasonWaitingInput}
	}
	if anyMerged(prs) {
		return statusResult{Status: domain.StatusMerged, Reason: domain.ReasonMerged}
	}

	if rec.Activity.State == domain.ActivityActive {
		if now.Sub(rec.Activity.LastActivityAt) <= activeStaleGrace {
			at := rec.Activity.LastActivityAt.Add(activeStaleGrace)
			return statusResult{
				Status:           domain.StatusWorking,
				Reason:           domain.ReasonWorking,
				NextTransitionAt: &at,
				NextTransitionTo: domain.StatusNeedsInput,
			}
		}
		// active but no signal refreshed it within the grace: the turn's closing
		// Stop was lost and nothing else demoted it, so surface it as
		// waiting-for-human rather than a permanent false "working".
		return statusResult{Status: domain.StatusNeedsInput, Reason: domain.ReasonActiveStale}
	}

	if rec.Activity.State == domain.ActivityIdle && !rec.FirstSignalAt.IsZero() &&
		now.Sub(rec.Activity.LastActivityAt) > waitingInputGrace {
		return statusResult{Status: domain.StatusNeedsInput, Reason: domain.ReasonIdleAged}
	}

	if signalCapable && rec.FirstSignalAt.IsZero() && now.Sub(rec.Activity.LastActivityAt) > noSignalGrace {
		return statusResult{Status: domain.StatusNoSignal, Reason: domain.ReasonNoSignal}
	}

	// Fresh idle: report idle now, and where a promotion is pending compute when
	// and to what it will flip so the UI can count down to it.
	at, to := idleCountdown(rec, signalCapable)
	return statusResult{Status: domain.StatusIdle, Reason: domain.ReasonIdle, NextTransitionAt: at, NextTransitionTo: to}
}

// idleCountdown returns the pending transition for a fresh idle session (one the
// branches above did not already promote): a signalled idle will promote to
// needs_input at last+waitingInputGrace; an unsignalled but hook-capable idle
// will degrade to no_signal at last+noSignalGrace; a hook-less idle never flips.
func idleCountdown(rec domain.SessionRecord, signalCapable bool) (*time.Time, domain.SessionStatus) {
	if rec.Activity.State != domain.ActivityIdle {
		return nil, ""
	}
	if !rec.FirstSignalAt.IsZero() {
		at := rec.Activity.LastActivityAt.Add(waitingInputGrace)
		return &at, domain.StatusNeedsInput
	}
	if signalCapable {
		at := rec.Activity.LastActivityAt.Add(noSignalGrace)
		return &at, domain.StatusNoSignal
	}
	return nil, ""
}

// openPRs returns the PRs that are neither merged nor closed, preserving order.
func openPRs(prs []domain.PRFacts) []domain.PRFacts {
	out := make([]domain.PRFacts, 0, len(prs))
	for _, p := range prs {
		if !p.Merged && !p.Closed {
			out = append(out, p)
		}
	}
	return out
}

func anyMerged(prs []domain.PRFacts) bool {
	for _, p := range prs {
		if p.Merged {
			return true
		}
	}
	return false
}

// aggregatePRStatus is the worst-wins reduction over a session's open PRs.
// A stacked child blocked by an open parent cannot merge yet, so its readiness
// signals (mergeable/approved/review-pending/open) are not actionable for the
// session and are suppressed. Its problem signals are still actionable: failing
// CI, draft state, and requested-changes/unresolved-comments must stay visible
// so a broken child is not hidden behind the stack. If no PR contributes any
// signal (a degenerate stack with no visible root), it falls back to aggregating
// the raw status across all open PRs so the session never goes dark.
func aggregatePRStatus(open []domain.PRFacts, minApprovals int) domain.SessionStatus {
	stacks := buildStacks(open)
	candidates := make([]domain.SessionStatus, 0, len(open))
	for _, p := range open {
		s := prPipelineStatus(p, minApprovals)
		if stacks[p.URL].Blocked && !isActionableChildSignal(s) {
			continue
		}
		candidates = append(candidates, s)
	}
	if len(candidates) == 0 {
		for _, p := range open {
			candidates = append(candidates, prPipelineStatus(p, minApprovals))
		}
	}
	worst := candidates[0]
	for _, s := range candidates[1:] {
		if statusSeverity(s) < statusSeverity(worst) {
			worst = s
		}
	}
	return worst
}

// isDeferrableProblemStatus reports whether an open-PR pipeline status is a
// problem an actively-working agent may be resolving on its own — failing CI,
// requested changes, or a pending review — and so should read as working rather
// than pulling the human in while the agent is active. Positive states
// (mergeable/approved) and neutral ones (draft/pr_open) are not deferred: they
// keep their pipeline status even while the agent runs.
func isDeferrableProblemStatus(s domain.SessionStatus) bool {
	switch s {
	case domain.StatusCIFailed, domain.StatusChangesRequested, domain.StatusReviewPending:
		return true
	default:
		return false
	}
}

// isActionableChildSignal reports whether a blocked stacked child's pipeline
// status is a problem the user can act on now, independent of the child's
// inability to merge until its parent does.
func isActionableChildSignal(s domain.SessionStatus) bool {
	switch s {
	case domain.StatusCIFailed, domain.StatusDraft, domain.StatusChangesRequested:
		return true
	default:
		return false
	}
}

// statusSeverity ranks PR pipeline statuses from most to least urgent so the
// aggregate surfaces the PR that most needs attention. mergeable is least urgent
// so a session only reports mergeable when every aggregated PR is mergeable.
func statusSeverity(s domain.SessionStatus) int {
	switch s {
	case domain.StatusCIFailed:
		return 0
	case domain.StatusChangesRequested:
		return 1
	case domain.StatusDraft:
		return 2
	case domain.StatusReviewPending:
		return 3
	case domain.StatusPROpen:
		return 4
	case domain.StatusApproved:
		return 5
	case domain.StatusMergeable:
		return 6
	default:
		return 7
	}
}

func prPipelineStatus(pr domain.PRFacts, minApprovals int) domain.SessionStatus {
	switch {
	case pr.CI == domain.CIFailing:
		return domain.StatusCIFailed
	case pr.Draft:
		return domain.StatusDraft
	case pr.Review == domain.ReviewChangesRequest || pr.ReviewComments:
		return domain.StatusChangesRequested
	case pr.Mergeability == domain.MergeMergeable && !approvalRuleUnsatisfied(pr):
		return domain.StatusMergeable
	case pr.Review == domain.ReviewApproved:
		return domain.StatusApproved
	case pr.Review == domain.ReviewRequired:
		return domain.StatusReviewPending
	case !pr.ApprovalRuleConfigured && pr.ApprovalsCount >= minApprovals:
		// No SCM rule of its own: AO's per-project floor decides readiness.
		return domain.StatusApproved
	default:
		return domain.StatusPROpen
	}
}

// approvalRuleUnsatisfied reports whether the SCM enforces an approval rule for
// this PR that is not yet satisfied. It guards the mergeable → "Ready to merge"
// transition so an under-approved PR can never be promoted on a mergeability
// reading alone.
//
// This is the GitLab safety net: GitLab's coarse merge_status returns
// can_be_merged even when an approval rule is unmet, and while the adapter now
// prefers detailed_merge_status (which downgrades such an MR to blocked), older
// GitLab omits that field. Keying on ApprovalRuleConfigured keeps GitHub
// untouched — the GitHub adapter never sets it, folding its required-review gate
// into mergeability (mergeStateStatus=BLOCKED) instead. When a rule is
// configured, GitLab reports approved (ReviewApproved) only once approvals_left
// hits zero, so any other decision means the rule is still outstanding.
func approvalRuleUnsatisfied(pr domain.PRFacts) bool {
	return pr.ApprovalRuleConfigured && pr.Review != domain.ReviewApproved
}
