package session

import (
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/claudecode"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

var statusNow = time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)

// statusRec builds a session whose agent HAS delivered a hook signal; the
// no-signal cases below zero FirstSignalAt explicitly.
func statusRec(activity domain.ActivityState, terminated bool) domain.SessionRecord {
	return domain.SessionRecord{
		Activity:      domain.Activity{State: activity, LastActivityAt: statusNow},
		FirstSignalAt: statusNow,
		IsTerminated:  terminated,
	}
}

// silentRec builds a live session that has never delivered a hook signal,
// seeded (spawned/restored) `age` before the derivation time.
func silentRec(age time.Duration) domain.SessionRecord {
	return domain.SessionRecord{
		Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: statusNow.Add(-age)},
	}
}

// idleAgedRec builds a session that HAS signalled (its Stop landed idle) and has
// then sat idle for `age`, used to exercise the sustained-idle → needs-input
// promotion.
func idleAgedRec(age time.Duration) domain.SessionRecord {
	return domain.SessionRecord{
		Activity:      domain.Activity{State: domain.ActivityIdle, LastActivityAt: statusNow.Add(-age)},
		FirstSignalAt: statusNow.Add(-age),
	}
}

// activeAgedRec builds a session whose last signal was active but which has then
// gone silent for `age` with no refreshing signal — the shape left behind when a
// turn's closing Stop hook is lost (a hung agent, a dropped hook, a daemon
// restart mid-turn). Used to exercise the stale-active demotion.
func activeAgedRec(age time.Duration) domain.SessionRecord {
	return domain.SessionRecord{
		Activity:      domain.Activity{State: domain.ActivityActive, LastActivityAt: statusNow.Add(-age)},
		FirstSignalAt: statusNow.Add(-age),
	}
}

func statusPR(facts domain.PRFacts) []domain.PRFacts { return []domain.PRFacts{facts} }

func TestServiceDerivesStatusFromSessionFactsAndPR(t *testing.T) {
	tests := []struct {
		name string
		rec  domain.SessionRecord
		pr   []domain.PRFacts
		// hookless marks a harness with no activity pipeline (signalCapable
		// false): silence is its permanent normal state, never no_signal.
		hookless bool
		want     domain.SessionStatus
	}{
		{"terminated", statusRec(domain.ActivityExited, true), nil, false, domain.StatusTerminated},
		{"merged-pr", statusRec(domain.ActivityIdle, true), statusPR(domain.PRFacts{Merged: true}), false, domain.StatusMerged},
		// A restored/continued (non-terminated) session whose first PR merged and
		// which then opened a NEW PR must leave the merged/done bucket: the open PR
		// outranks the merged one. This is the read-side half of the restored-session
		// auto-claim — once the observer attributes the new open PR, the card lands in
		// an active zone with no manual claim-pr.
		{"open-pr-beats-merged-leaves-done", statusRec(domain.ActivityIdle, false), []domain.PRFacts{{Merged: true}, {}}, false, domain.StatusPROpen},
		{"needs-input", statusRec(domain.ActivityWaitingInput, false), statusPR(domain.PRFacts{CI: domain.CIFailing}), false, domain.StatusNeedsInput},
		{"ci-failed", statusRec(domain.ActivityIdle, false), statusPR(domain.PRFacts{CI: domain.CIFailing}), false, domain.StatusCIFailed},
		{"draft", statusRec(domain.ActivityIdle, false), statusPR(domain.PRFacts{Draft: true}), false, domain.StatusDraft},
		{"changes-requested", statusRec(domain.ActivityIdle, false), statusPR(domain.PRFacts{Review: domain.ReviewChangesRequest}), false, domain.StatusChangesRequested},
		{"mergeable", statusRec(domain.ActivityIdle, false), statusPR(domain.PRFacts{Mergeability: domain.MergeMergeable}), false, domain.StatusMergeable},
		{"approved", statusRec(domain.ActivityIdle, false), statusPR(domain.PRFacts{Review: domain.ReviewApproved}), false, domain.StatusApproved},
		{"review-pending", statusRec(domain.ActivityIdle, false), statusPR(domain.PRFacts{Review: domain.ReviewRequired}), false, domain.StatusReviewPending},
		{"pr-open", statusRec(domain.ActivityIdle, false), statusPR(domain.PRFacts{}), false, domain.StatusPROpen},
		{"working", statusRec(domain.ActivityActive, false), nil, false, domain.StatusWorking},
		{"idle", statusRec(domain.ActivityIdle, false), nil, false, domain.StatusIdle},

		// An active session whose signals stopped past the grace almost certainly
		// lost its closing Stop (a hung agent, a dropped hook) and is not really
		// working — surface it as waiting-for-human instead of a permanent false
		// "working". Within the grace it stays working so a long tool call between
		// activity signals never flips it.
		{"active-past-grace-no-pr-needs-you", activeAgedRec(2 * activeStaleGrace), nil, false, domain.StatusNeedsInput},
		{"active-within-grace-stays-working", activeAgedRec(activeStaleGrace / 2), nil, false, domain.StatusWorking},
		// An open PR is derived before the active reading, so a stale active with a
		// PR keeps its PR status rather than promoting to needs-input.
		{"active-past-grace-open-pr-stays-pr", activeAgedRec(2 * activeStaleGrace), statusPR(domain.PRFacts{}), false, domain.StatusPROpen},

		// A signalled session that has sat idle past the grace with no PR is
		// treated as waiting for the human (the agent stopped and is waiting).
		{"idle-past-grace-no-pr-needs-you", idleAgedRec(2 * waitingInputGrace), nil, false, domain.StatusNeedsInput},
		// Still within the grace: a brief between-turns pause stays idle.
		{"idle-within-grace-stays-idle", idleAgedRec(waitingInputGrace / 2), nil, false, domain.StatusIdle},
		// The promotion must NOT clobber PR status: a finished worker that opened
		// a PR keeps its review/merge signal even after sitting idle past grace.
		{"idle-past-grace-mergeable-pr-stays-mergeable", idleAgedRec(2 * waitingInputGrace), statusPR(domain.PRFacts{Mergeability: domain.MergeMergeable}), false, domain.StatusMergeable},
		{"idle-past-grace-review-pending-pr-stays-review", idleAgedRec(2 * waitingInputGrace), statusPR(domain.PRFacts{Review: domain.ReviewRequired}), false, domain.StatusReviewPending},
		{"idle-past-grace-ci-failing-pr-stays-ci-failed", idleAgedRec(2 * waitingInputGrace), statusPR(domain.PRFacts{CI: domain.CIFailing}), false, domain.StatusCIFailed},

		// An agent actively working an open PR that is in a PROBLEM pipeline
		// state (failing CI, requested changes, pending review) is autonomously
		// fixing it (auto mode): show working, not a "needs you" problem state,
		// until the agent goes stale/idle. Once it goes stale, the underlying
		// problem status surfaces again.
		{"active-ci-failing-pr-shows-working", statusRec(domain.ActivityActive, false), statusPR(domain.PRFacts{CI: domain.CIFailing}), false, domain.StatusWorking},
		{"stale-active-ci-failing-pr-stays-ci-failed", activeAgedRec(2 * activeStaleGrace), statusPR(domain.PRFacts{CI: domain.CIFailing}), false, domain.StatusCIFailed},
		{"active-changes-requested-pr-shows-working", statusRec(domain.ActivityActive, false), statusPR(domain.PRFacts{Review: domain.ReviewChangesRequest}), false, domain.StatusWorking},
		{"stale-active-changes-requested-stays-changes-requested", activeAgedRec(2 * activeStaleGrace), statusPR(domain.PRFacts{Review: domain.ReviewChangesRequest}), false, domain.StatusChangesRequested},
		{"active-review-pending-pr-shows-working", statusRec(domain.ActivityActive, false), statusPR(domain.PRFacts{Review: domain.ReviewRequired}), false, domain.StatusWorking},
		// POSITIVE pipeline states are never overridden by active-working: a
		// ready-to-merge or approved PR still shows its readiness even while the
		// agent runs. Neutral states (draft/pr_open) are likewise not deferred.
		{"active-mergeable-pr-stays-mergeable", statusRec(domain.ActivityActive, false), statusPR(domain.PRFacts{Mergeability: domain.MergeMergeable}), false, domain.StatusMergeable},
		{"active-approved-pr-stays-approved", statusRec(domain.ActivityActive, false), statusPR(domain.PRFacts{Review: domain.ReviewApproved}), false, domain.StatusApproved},
		{"active-open-pr-stays-pr-open", statusRec(domain.ActivityActive, false), statusPR(domain.PRFacts{}), false, domain.StatusPROpen},
		// WaitingInput (a real prompt) is resolved before the open-PR branch, so
		// a genuine block still surfaces as needs_input even over a problem PR.
		{"waiting-input-ci-failing-pr-needs-input", statusRec(domain.ActivityWaitingInput, false), statusPR(domain.PRFacts{CI: domain.CIFailing}), false, domain.StatusNeedsInput},

		// A live session whose hook-capable agent never signaled is no_signal
		// once the grace passes — never a confident idle.
		{"no-signal-after-grace", silentRec(2 * noSignalGrace), nil, false, domain.StatusNoSignal},
		// A hook-less harness can never signal: its silence stays idle forever
		// instead of degrading into a false "needs you".
		{"hookless-silent-stays-idle", silentRec(2 * noSignalGrace), nil, true, domain.StatusIdle},
		// Right after spawn the agent legitimately hasn't called back yet.
		{"silent-within-grace-is-idle", silentRec(10 * time.Second), nil, false, domain.StatusIdle},
		// Termination and PR facts outrank the missing-signal downgrade.
		{
			"no-signal-terminated-wins",
			domain.SessionRecord{Activity: domain.Activity{State: domain.ActivityExited, LastActivityAt: statusNow.Add(-2 * noSignalGrace)}, IsTerminated: true},
			nil,
			false,
			domain.StatusTerminated,
		},
		{"no-signal-pr-wins", silentRec(2 * noSignalGrace), statusPR(domain.PRFacts{}), false, domain.StatusPROpen},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := deriveStatus(tt.rec, tt.pr, statusNow, !tt.hookless, domain.ApprovalRule{}); got != tt.want {
				t.Fatalf("got %q want %q", got, tt.want)
			}
		})
	}
}

// A configured-but-unmet SCM approval rule must NOT surface as "Ready to merge",
// even when mergeability reads mergeable. GitLab's coarse merge_status returns
// can_be_merged while an approval rule (e.g. requires >= 3 approvals) is still
// unsatisfied; if the adapter's richer signal lags, prPipelineStatus must still
// hold the card back from StatusMergeable until the rule is met. GitHub is
// unaffected: it never sets ApprovalRuleConfigured (its required-review gate is
// already folded into mergeStateStatus=BLOCKED, i.e. Mergeability=blocked).
func TestApprovalRuleGatesReadyToMerge(t *testing.T) {
	rec := statusRec(domain.ActivityIdle, false)
	tests := []struct {
		name string
		pr   domain.PRFacts
		want domain.SessionStatus
	}{
		{
			// The demo-ios-app !3028 bug: rule configured, not enough approvals,
			// yet mergeability says mergeable. Must not be Ready to merge.
			"gitlab-rule-unmet-mergeable-not-ready",
			domain.PRFacts{Mergeability: domain.MergeMergeable, ApprovalRuleConfigured: true, Review: domain.ReviewNone},
			domain.StatusPROpen,
		},
		{
			// Rule met (approvalDecision -> approved) with no conflicts: ready.
			"gitlab-rule-met-mergeable-ready",
			domain.PRFacts{Mergeability: domain.MergeMergeable, ApprovalRuleConfigured: true, Review: domain.ReviewApproved},
			domain.StatusMergeable,
		},
		{
			// Primary fix path: adapter already downgraded mergeability via
			// detailed_merge_status=not_approved. Still not ready.
			"gitlab-rule-unmet-blocked-not-ready",
			domain.PRFacts{Mergeability: domain.MergeBlocked, ApprovalRuleConfigured: true, Review: domain.ReviewNone},
			domain.StatusPROpen,
		},
		{
			// Conflicts are never Ready to merge.
			"conflicts-not-ready",
			domain.PRFacts{Mergeability: domain.MergeConflicting, ApprovalRuleConfigured: true, Review: domain.ReviewNone},
			domain.StatusPROpen,
		},
		{
			// GitHub-shaped PR: no AO-visible rule, mergeability already folds in
			// the review gate. A mergeable one stays Ready to merge (unchanged).
			"github-no-rule-mergeable-ready",
			domain.PRFacts{Mergeability: domain.MergeMergeable, ApprovalRuleConfigured: false, Review: domain.ReviewNone},
			domain.StatusMergeable,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := deriveStatus(rec, statusPR(tt.pr), statusNow, true, domain.ApprovalRule{}); got != tt.want {
				t.Fatalf("got %q want %q", got, tt.want)
			}
		})
	}
}

// TestEnabledApprovalRuleGatesApprovedWhenGitLabHasNoRuleOfItsOwn is the
// approval-rule-aware derivation for the demo-ios-app !3028 shape (board: "gl
// mergeable stale and false nudge", defect 3). GitLab's own approval state has
// ZERO rules (approvals_required=0, approved_by=[]) so its raw `approved` flag is
// trivially true — but the project's AO approval rule is ENABLED with threshold 2.
// The rule applies precisely because GitLab has no rule of its own
// (ApprovalRuleConfigured=false), so 0 approvals < 2 must NOT surface as Approved
// or Ready to merge. This is the single source of truth every approval surface
// (board, Summary, readiness strip) derives from; it must never promote to
// StatusApproved/StatusMergeable until real approvals reach the threshold.
func TestEnabledApprovalRuleGatesApprovedWhenGitLabHasNoRuleOfItsOwn(t *testing.T) {
	rec := statusRec(domain.ActivityIdle, false)
	rule := domain.ApprovalRule{Enabled: true, Threshold: 2}
	tests := []struct {
		name string
		pr   domain.PRFacts
		want domain.SessionStatus
	}{
		{
			// The exact !3028 state once mergeability un-freezes: mergeable, no SCM
			// rule, zero approvals, AO rule 2. Not approved, not ready.
			"no-scm-rule-zero-approvals-mergeable-not-ready",
			domain.PRFacts{Mergeability: domain.MergeMergeable, ApprovalRuleConfigured: false, ApprovalsCount: 0, Review: domain.ReviewNone},
			domain.StatusPROpen,
		},
		{
			// One approval is still short of the threshold: still not approved.
			"no-scm-rule-one-approval-below-threshold-not-ready",
			domain.PRFacts{Mergeability: domain.MergeMergeable, ApprovalRuleConfigured: false, ApprovalsCount: 1, Review: domain.ReviewNone},
			domain.StatusPROpen,
		},
		{
			// Threshold met by real approvals: promoted to Ready to merge.
			"no-scm-rule-threshold-met-ready",
			domain.PRFacts{Mergeability: domain.MergeMergeable, ApprovalRuleConfigured: false, ApprovalsCount: 2, Review: domain.ReviewNone},
			domain.StatusMergeable,
		},
		{
			// Threshold met but not yet mergeable: surfaced as Approved (the count
			// gate), still honest that it is not mergeable yet.
			"no-scm-rule-threshold-met-not-mergeable-approved",
			domain.PRFacts{Mergeability: domain.MergeUnknown, ApprovalRuleConfigured: false, ApprovalsCount: 2, Review: domain.ReviewNone},
			domain.StatusApproved,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := deriveStatus(rec, statusPR(tt.pr), statusNow, true, rule); got != tt.want {
				t.Fatalf("got %q want %q", got, tt.want)
			}
		})
	}
}

// A session brought back from a terminal state via Reopen (restore) is marked
// reactivated. It must return to the board — surfaced as needs_input (the "Needs
// you" zone) — instead of staying pinned to Done by a previously-merged PR, until
// it takes on new work or is finished again. An actively-working one shows
// working, an open PR still wins, and a genuinely terminal session is unaffected.
func TestReactivatedSessionSurfacesAsNeedsYou(t *testing.T) {
	reactivated := func(activity domain.ActivityState, terminated bool) domain.SessionRecord {
		r := statusRec(activity, terminated)
		r.Reactivated = true
		return r
	}
	tests := []struct {
		name string
		rec  domain.SessionRecord
		prs  []domain.PRFacts
		want domain.SessionStatus
	}{
		{"reactivated-merged-idle-needs-you", reactivated(domain.ActivityIdle, false), statusPR(domain.PRFacts{Merged: true}), domain.StatusNeedsInput},
		{"reactivated-no-pr-needs-you", reactivated(domain.ActivityIdle, false), nil, domain.StatusNeedsInput},
		{"reactivated-active-shows-working", reactivated(domain.ActivityActive, false), statusPR(domain.PRFacts{Merged: true}), domain.StatusWorking},
		{"reactivated-open-pr-wins", reactivated(domain.ActivityIdle, false), statusPR(domain.PRFacts{Mergeability: domain.MergeMergeable}), domain.StatusMergeable},
		{"reactivated-but-terminated-stays-merged", reactivated(domain.ActivityIdle, true), statusPR(domain.PRFacts{Merged: true}), domain.StatusMerged},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := deriveStatus(tt.rec, tt.prs, statusNow, true, domain.ApprovalRule{}); got != tt.want {
				t.Fatalf("got %q want %q", got, tt.want)
			}
		})
	}
}

// A WORKER suspended after its PR merged (feature/merge-suspend-in-place) must
// surface as needs_input — the "Needs you" lane — so its card stays visible with
// a Continue/Close chip instead of the merged status archiving it to Done. The
// override is narrow: it fires only on the merge signature (suspended + a merged
// PR + no open PR). A non-suspended merged session still reads merged; an open PR
// still wins (an idle-suspended in-review session keeps its lane); a suspended
// session with no merged PR is unaffected (idle-suspend unchanged).
func TestSuspendedMergedSurfacesAsNeedsYou(t *testing.T) {
	suspended := func(activity domain.ActivityState) domain.SessionRecord {
		r := statusRec(activity, false)
		r.IsSuspended = true
		return r
	}
	tests := []struct {
		name string
		rec  domain.SessionRecord
		prs  []domain.PRFacts
		want domain.SessionStatus
	}{
		{"suspended-merged-needs-you", suspended(domain.ActivityIdle), statusPR(domain.PRFacts{Merged: true}), domain.StatusNeedsInput},
		{"non-suspended-merged-stays-merged", statusRec(domain.ActivityIdle, false), statusPR(domain.PRFacts{Merged: true}), domain.StatusMerged},
		{"suspended-open-pr-keeps-its-lane", suspended(domain.ActivityIdle), statusPR(domain.PRFacts{Mergeability: domain.MergeMergeable}), domain.StatusMergeable},
		{"suspended-no-merged-pr-unaffected", suspended(domain.ActivityIdle), nil, domain.StatusIdle},
		{"suspended-but-terminated-stays-merged", func() domain.SessionRecord { r := suspended(domain.ActivityIdle); r.IsTerminated = true; return r }(), statusPR(domain.PRFacts{Merged: true}), domain.StatusMerged},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := deriveStatus(tt.rec, tt.prs, statusNow, true, domain.ApprovalRule{}); got != tt.want {
				t.Fatalf("got %q want %q", got, tt.want)
			}
		})
	}
}

// A recap / auto-summary turn ends the turn (a Stop hook -> idle); Claude Code
// then emits an idle_prompt Notification while the session sits quiet. That
// notification is INFORMATIONAL and must not make an idle, finished session look
// like it is "waiting on the human": it must never demote an open, ready-to-merge
// PR back to needs_input. Only a genuine block (permission_prompt) is real
// pending input and still surfaces as needs_input.
func TestRecapNotificationDoesNotDemoteReadyPR(t *testing.T) {
	prs := statusPR(domain.PRFacts{Mergeability: domain.MergeMergeable})

	// The recap left the session idle (its Stop hook), then an idle_prompt
	// Notification landed. Feed the state Claude Code actually derives for that
	// notification into the status derivation alongside the ready PR.
	recap := statusRec(domain.ActivityIdle, false)
	if state, ok := claudecode.DeriveActivityState("notification", []byte(`{"notification_type":"idle_prompt"}`)); ok {
		recap.Activity.State = state
	}
	if got := deriveStatus(recap, prs, statusNow, true, domain.ApprovalRule{}); got != domain.StatusMergeable {
		t.Fatalf("recap over an open mergeable PR: status = %q, want %q (a recap must not flip a ready PR to needs_input)", got, domain.StatusMergeable)
	}

	// A genuine tool-permission prompt IS real pending input: it still surfaces as
	// needs_input, even over a mergeable PR.
	state, ok := claudecode.DeriveActivityState("notification", []byte(`{"notification_type":"permission_prompt"}`))
	if !ok || state != domain.ActivityWaitingInput {
		t.Fatalf("permission_prompt must derive waiting_input; got (%q, %v)", state, ok)
	}
	blocked := statusRec(state, false)
	if got := deriveStatus(blocked, prs, statusNow, true, domain.ApprovalRule{}); got != domain.StatusNeedsInput {
		t.Fatalf("genuine permission prompt over a PR: status = %q, want %q", got, domain.StatusNeedsInput)
	}
}

// A blocked stacked child cannot merge until its parent does, so its readiness
// signals are suppressed, but its problem signals (failing CI, draft,
// requested-changes/unresolved-comments) must still surface for the session.
func TestAggregateStackedChildSignals(t *testing.T) {
	parent := domain.PRFacts{URL: "parent", SourceBranch: "feat", Mergeability: domain.MergeMergeable}
	child := func(f domain.PRFacts) domain.PRFacts {
		f.URL = "child"
		f.SourceBranch = "feat/child"
		f.TargetBranch = "feat"
		return f
	}
	tests := []struct {
		name string
		prs  []domain.PRFacts
		want domain.SessionStatus
	}{
		{"blocked-child-ci-failing-surfaces", []domain.PRFacts{parent, child(domain.PRFacts{CI: domain.CIFailing})}, domain.StatusCIFailed},
		{"blocked-child-draft-surfaces", []domain.PRFacts{parent, child(domain.PRFacts{Draft: true})}, domain.StatusDraft},
		{"blocked-child-changes-requested-surfaces", []domain.PRFacts{parent, child(domain.PRFacts{Review: domain.ReviewChangesRequest})}, domain.StatusChangesRequested},
		{"blocked-child-unresolved-comments-surfaces", []domain.PRFacts{parent, child(domain.PRFacts{ReviewComments: true})}, domain.StatusChangesRequested},
		// A blocked child's readiness signals stay hidden: only the parent's
		// mergeable state drives the session.
		{"blocked-child-mergeable-suppressed", []domain.PRFacts{parent, child(domain.PRFacts{Mergeability: domain.MergeMergeable})}, domain.StatusMergeable},
		{"blocked-child-approved-suppressed", []domain.PRFacts{parent, child(domain.PRFacts{Review: domain.ReviewApproved})}, domain.StatusMergeable},
		// Degenerate set where every open PR is blocked and none is actionable:
		// fall back to the raw aggregate so the session never goes dark.
		{
			"all-blocked-no-actionable-falls-back",
			[]domain.PRFacts{
				{URL: "a", SourceBranch: "feat/a", TargetBranch: "feat/b", Mergeability: domain.MergeMergeable},
				{URL: "b", SourceBranch: "feat/b", TargetBranch: "feat/a", Mergeability: domain.MergeMergeable},
			},
			domain.StatusMergeable,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := deriveStatus(statusRec(domain.ActivityIdle, false), tt.prs, statusNow, true, domain.ApprovalRule{}); got != tt.want {
				t.Fatalf("got %q want %q", got, tt.want)
			}
		})
	}
}

// Without an injected capability predicate the service must never claim
// no_signal; with one, capability follows the predicate per harness.
func TestHarnessSignalsCapabilityGate(t *testing.T) {
	if (&Service{}).harnessSignals(domain.HarnessCodex) {
		t.Fatal("zero-value Service reports signal-capable; want incapable (never no_signal)")
	}
	s := NewWithDeps(Deps{SignalCapable: func(h domain.AgentHarness) bool { return h == domain.HarnessCodex }})
	if !s.harnessSignals(domain.HarnessCodex) {
		t.Fatal("harnessSignals(codex) = false with codex-capable predicate")
	}
	if s.harnessSignals(domain.HarnessAmp) {
		t.Fatal("harnessSignals(amp) = true with codex-only predicate")
	}
}

func TestDeriveStatusDetailReason(t *testing.T) {
	tests := []struct {
		name       string
		rec        domain.SessionRecord
		pr         []domain.PRFacts
		hookless   bool
		wantStatus domain.SessionStatus
		wantReason domain.StatusReason
		// wantNextTo is "" when no timed transition is pending.
		wantNextTo domain.SessionStatus
	}{
		{"working", statusRec(domain.ActivityActive, false), nil, false, domain.StatusWorking, domain.ReasonWorking, domain.StatusNeedsInput},
		{"active-stale", activeAgedRec(2 * activeStaleGrace), nil, false, domain.StatusNeedsInput, domain.ReasonActiveStale, ""},
		{"waiting-input", statusRec(domain.ActivityWaitingInput, false), nil, false, domain.StatusNeedsInput, domain.ReasonWaitingInput, ""},
		{"idle-aged", idleAgedRec(2 * waitingInputGrace), nil, false, domain.StatusNeedsInput, domain.ReasonIdleAged, ""},
		{"idle-fresh-signalled", idleAgedRec(waitingInputGrace / 2), nil, false, domain.StatusIdle, domain.ReasonIdle, domain.StatusNeedsInput},
		{"idle-fresh-never-signalled", silentRec(10 * time.Second), nil, false, domain.StatusIdle, domain.ReasonIdle, domain.StatusNoSignal},
		{"no-signal", silentRec(2 * noSignalGrace), nil, false, domain.StatusNoSignal, domain.ReasonNoSignal, ""},
		{"hookless-idle", silentRec(2 * noSignalGrace), nil, true, domain.StatusIdle, domain.ReasonIdle, ""},
		{"pr-open", statusRec(domain.ActivityIdle, false), statusPR(domain.PRFacts{}), false, domain.StatusPROpen, domain.ReasonPRPipeline, ""},
		// Active over a problem PR reads working, with the pending transition set
		// to the underlying problem status it flips to once the agent goes stale.
		{"active-ci-failing-pr-working", statusRec(domain.ActivityActive, false), statusPR(domain.PRFacts{CI: domain.CIFailing}), false, domain.StatusWorking, domain.ReasonWorking, domain.StatusCIFailed},
		{"active-changes-requested-pr-working", statusRec(domain.ActivityActive, false), statusPR(domain.PRFacts{Review: domain.ReviewChangesRequest}), false, domain.StatusWorking, domain.ReasonWorking, domain.StatusChangesRequested},
		{"active-review-pending-pr-working", statusRec(domain.ActivityActive, false), statusPR(domain.PRFacts{Review: domain.ReviewRequired}), false, domain.StatusWorking, domain.ReasonWorking, domain.StatusReviewPending},
		// A positive PR state is not deferred: reason stays pr_pipeline, no timed transition.
		{"active-mergeable-pr-pipeline", statusRec(domain.ActivityActive, false), statusPR(domain.PRFacts{Mergeability: domain.MergeMergeable}), false, domain.StatusMergeable, domain.ReasonPRPipeline, ""},
		{"terminated", statusRec(domain.ActivityExited, true), nil, false, domain.StatusTerminated, domain.ReasonTerminated, ""},
		{"merged", statusRec(domain.ActivityIdle, true), statusPR(domain.PRFacts{Merged: true}), false, domain.StatusMerged, domain.ReasonMerged, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := deriveStatusDetail(tt.rec, tt.pr, statusNow, !tt.hookless, domain.ApprovalRule{})
			if got.Status != tt.wantStatus {
				t.Fatalf("status: got %q want %q", got.Status, tt.wantStatus)
			}
			if got.Reason != tt.wantReason {
				t.Fatalf("reason: got %q want %q", got.Reason, tt.wantReason)
			}
			if tt.wantNextTo == "" {
				if got.NextTransitionAt != nil {
					t.Fatalf("nextTransitionAt: got %v want nil", got.NextTransitionAt)
				}
				return
			}
			if got.NextTransitionAt == nil {
				t.Fatalf("nextTransitionAt: got nil want non-nil")
			}
			if got.NextTransitionTo != tt.wantNextTo {
				t.Fatalf("nextTransitionTo: got %q want %q", got.NextTransitionTo, tt.wantNextTo)
			}
		})
	}
}

func TestDeriveStatusDetailCountdownTimestamps(t *testing.T) {
	// active within grace flips to needs_input at last + activeStaleGrace.
	active := activeAgedRec(activeStaleGrace / 2)
	got := deriveStatusDetail(active, nil, statusNow, true, domain.ApprovalRule{})
	wantAt := active.Activity.LastActivityAt.Add(activeStaleGrace)
	if got.NextTransitionAt == nil || !got.NextTransitionAt.Equal(wantAt) {
		t.Fatalf("active nextTransitionAt: got %v want %v", got.NextTransitionAt, wantAt)
	}
	// idle-fresh (signalled) flips to needs_input at last + waitingInputGrace.
	idle := idleAgedRec(waitingInputGrace / 2)
	got = deriveStatusDetail(idle, nil, statusNow, true, domain.ApprovalRule{})
	wantAt = idle.Activity.LastActivityAt.Add(waitingInputGrace)
	if got.NextTransitionAt == nil || !got.NextTransitionAt.Equal(wantAt) {
		t.Fatalf("idle nextTransitionAt: got %v want %v", got.NextTransitionAt, wantAt)
	}
	// active over a PROBLEM PR flips to that problem status at last + activeStaleGrace.
	activePR := statusRec(domain.ActivityActive, false)
	got = deriveStatusDetail(activePR, statusPR(domain.PRFacts{CI: domain.CIFailing}), statusNow, true, domain.ApprovalRule{})
	wantAt = activePR.Activity.LastActivityAt.Add(activeStaleGrace)
	if got.NextTransitionAt == nil || !got.NextTransitionAt.Equal(wantAt) {
		t.Fatalf("active problem-PR nextTransitionAt: got %v want %v", got.NextTransitionAt, wantAt)
	}
	if got.NextTransitionTo != domain.StatusCIFailed {
		t.Fatalf("active problem-PR nextTransitionTo: got %q want %q", got.NextTransitionTo, domain.StatusCIFailed)
	}
}

// A disabled approval rule (the default) imposes no approval-count gate: a
// mergeable PR is Ready to merge regardless of its approval count, and an
// under-approved PR is never promoted to Approved on count alone. This is the
// exact behavior a project that never opts in keeps.
func TestIdleCloseAt(t *testing.T) {
	ttl := 72 * time.Hour
	last := statusNow.Add(-time.Hour)
	live := domain.SessionRecord{Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: last}}

	t.Run("live session -> idleReference + TTL", func(t *testing.T) {
		s := &Service{idleCloseTTL: ttl}
		at := s.idleCloseAt(live)
		if at == nil {
			t.Fatal("live session must expose an idle-close deadline")
		}
		if want := last.Add(ttl); !at.Equal(want) {
			t.Fatalf("idleCloseAt = %v, want %v", at, want)
		}
	})

	t.Run("opened after last activity -> LastOpenedAt + TTL", func(t *testing.T) {
		// Opening a session (POST /wake → TouchIdleClose) stamps LastOpenedAt
		// without touching LastActivityAt. The idle-close countdown pushes forward
		// to the later timestamp so a session you are actively viewing is not
		// suspended out from under you — while the derived status (aged off
		// LastActivityAt) is left untouched.
		opened := statusNow.Add(-5 * time.Minute) // later than `last` (-1h)
		rec := live
		rec.LastOpenedAt = opened
		s := &Service{idleCloseTTL: ttl}
		at := s.idleCloseAt(rec)
		if at == nil || !at.Equal(opened.Add(ttl)) {
			t.Fatalf("idleCloseAt = %v, want %v (max of LastActivityAt and LastOpenedAt)", at, opened.Add(ttl))
		}
	})

	t.Run("opened before last activity -> LastActivityAt + TTL (max)", func(t *testing.T) {
		// A stale open never pulls the deadline in: idleReference is the max, so
		// genuine recent activity still governs.
		rec := live
		rec.LastOpenedAt = last.Add(-30 * time.Minute) // older than `last`
		s := &Service{idleCloseTTL: ttl}
		at := s.idleCloseAt(rec)
		if at == nil || !at.Equal(last.Add(ttl)) {
			t.Fatalf("idleCloseAt = %v, want %v (activity wins when it is newer)", at, last.Add(ttl))
		}
	})

	t.Run("no signal yet -> CreatedAt + TTL", func(t *testing.T) {
		s := &Service{idleCloseTTL: ttl}
		created := statusNow.Add(-10 * time.Minute)
		rec := domain.SessionRecord{CreatedAt: created}
		at := s.idleCloseAt(rec)
		if at == nil || !at.Equal(created.Add(ttl)) {
			t.Fatalf("idleCloseAt = %v, want %v (falls back to CreatedAt)", at, created.Add(ttl))
		}
	})

	t.Run("TTL disabled -> nil", func(t *testing.T) {
		s := &Service{idleCloseTTL: 0}
		if at := s.idleCloseAt(live); at != nil {
			t.Fatalf("idleCloseAt = %v, want nil when the sweep is disabled", at)
		}
	})

	for _, tc := range []struct {
		name string
		rec  domain.SessionRecord
	}{
		{"terminated", domain.SessionRecord{IsTerminated: true, Activity: live.Activity}},
		{"todo", domain.SessionRecord{IsTodo: true, Activity: live.Activity}},
		{"suspended", domain.SessionRecord{IsSuspended: true, Activity: live.Activity}},
	} {
		t.Run(tc.name+" -> nil (not a live suspend candidate)", func(t *testing.T) {
			s := &Service{idleCloseTTL: ttl}
			if at := s.idleCloseAt(tc.rec); at != nil {
				t.Fatalf("idleCloseAt = %v, want nil for a %s session", at, tc.name)
			}
		})
	}
}

func TestPRPipelineStatus_ApprovalRuleDisabled(t *testing.T) {
	base := domain.PRFacts{
		URL:          "https://gitlab.example.com/g/p/-/merge_requests/1",
		Number:       1,
		CI:           domain.CIPassing,
		Mergeability: domain.MergeMergeable,
	}
	off := domain.ApprovalRule{}

	// Mergeable with 0 approvals is still Ready to merge when the rule is off.
	pr := base
	pr.ApprovalsCount = 0
	if got := prPipelineStatus(pr, off); got != domain.StatusMergeable {
		t.Fatalf("rule off, mergeable/0 approvals: got %s, want mergeable", got)
	}

	// Non-mergeable with plenty of approvals is NOT promoted to approved on count
	// alone when the rule is off (that promotion is opt-in now).
	pr = base
	pr.Mergeability = domain.MergeUnknown
	pr.ApprovalsCount = 5
	if got := prPipelineStatus(pr, off); got != domain.StatusPROpen {
		t.Fatalf("rule off, 5 approvals, not mergeable: got %s, want pr_open", got)
	}
}

// An enabled approval rule gates the mergeable → Ready-to-merge transition on
// the approval count and (when no SCM rule exists) promotes a sufficiently
// approved PR to Approved.
func TestPRPipelineStatus_ApprovalRuleEnabled(t *testing.T) {
	base := domain.PRFacts{
		URL:          "https://gitlab.example.com/g/p/-/merge_requests/1",
		Number:       1,
		CI:           domain.CIPassing,
		Mergeability: domain.MergeMergeable,
	}
	rule := domain.ApprovalRule{Enabled: true, Threshold: 2}

	// Mergeable but under threshold → gated out of Ready to merge.
	pr := base
	pr.ApprovalsCount = 1
	if got := prPipelineStatus(pr, rule); got != domain.StatusPROpen {
		t.Fatalf("enabled, mergeable/1 approval < 2: got %s, want pr_open", got)
	}

	// Mergeable and threshold met → Ready to merge.
	pr.ApprovalsCount = 2
	if got := prPipelineStatus(pr, rule); got != domain.StatusMergeable {
		t.Fatalf("enabled, mergeable/2 approvals: got %s, want mergeable", got)
	}

	// Not mergeable, threshold met, no SCM rule → promoted to Approved.
	pr = base
	pr.Mergeability = domain.MergeUnknown
	pr.ApprovalsCount = 3
	if got := prPipelineStatus(pr, rule); got != domain.StatusApproved {
		t.Fatalf("enabled, 3 approvals, not mergeable: got %s, want approved", got)
	}

	// SCM enforces its own rule → the count gate does not manufacture readiness;
	// GitLab's decision (here none) wins → pr_open even with approvals.
	pr = base
	pr.Mergeability = domain.MergeUnknown
	pr.ApprovalRuleConfigured = true
	pr.ApprovalsCount = 5
	if got := prPipelineStatus(pr, rule); got != domain.StatusPROpen {
		t.Fatalf("enabled, SCM rule configured: got %s, want pr_open", got)
	}

	// Threshold met but an unresolved thread still wins (worst-wins).
	pr = base
	pr.ApprovalsCount = 3
	pr.ReviewComments = true
	if got := prPipelineStatus(pr, rule); got != domain.StatusChangesRequested {
		t.Fatalf("unresolved thread: got %s, want changes_requested", got)
	}
}
