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
			if got := deriveStatus(tt.rec, tt.pr, statusNow, !tt.hookless, domain.DefaultMinApprovals); got != tt.want {
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
			// The nter-ios-app !3028 bug: rule configured, not enough approvals,
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
			if got := deriveStatus(rec, statusPR(tt.pr), statusNow, true, domain.DefaultMinApprovals); got != tt.want {
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
			if got := deriveStatus(tt.rec, tt.prs, statusNow, true, domain.DefaultMinApprovals); got != tt.want {
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
	if got := deriveStatus(recap, prs, statusNow, true, domain.DefaultMinApprovals); got != domain.StatusMergeable {
		t.Fatalf("recap over an open mergeable PR: status = %q, want %q (a recap must not flip a ready PR to needs_input)", got, domain.StatusMergeable)
	}

	// A genuine tool-permission prompt IS real pending input: it still surfaces as
	// needs_input, even over a mergeable PR.
	state, ok := claudecode.DeriveActivityState("notification", []byte(`{"notification_type":"permission_prompt"}`))
	if !ok || state != domain.ActivityWaitingInput {
		t.Fatalf("permission_prompt must derive waiting_input; got (%q, %v)", state, ok)
	}
	blocked := statusRec(state, false)
	if got := deriveStatus(blocked, prs, statusNow, true, domain.DefaultMinApprovals); got != domain.StatusNeedsInput {
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
			if got := deriveStatus(statusRec(domain.ActivityIdle, false), tt.prs, statusNow, true, domain.DefaultMinApprovals); got != tt.want {
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
		{"terminated", statusRec(domain.ActivityExited, true), nil, false, domain.StatusTerminated, domain.ReasonTerminated, ""},
		{"merged", statusRec(domain.ActivityIdle, true), statusPR(domain.PRFacts{Merged: true}), false, domain.StatusMerged, domain.ReasonMerged, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := deriveStatusDetail(tt.rec, tt.pr, statusNow, !tt.hookless, domain.DefaultMinApprovals)
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
	got := deriveStatusDetail(active, nil, statusNow, true, domain.DefaultMinApprovals)
	wantAt := active.Activity.LastActivityAt.Add(activeStaleGrace)
	if got.NextTransitionAt == nil || !got.NextTransitionAt.Equal(wantAt) {
		t.Fatalf("active nextTransitionAt: got %v want %v", got.NextTransitionAt, wantAt)
	}
	// idle-fresh (signalled) flips to needs_input at last + waitingInputGrace.
	idle := idleAgedRec(waitingInputGrace / 2)
	got = deriveStatusDetail(idle, nil, statusNow, true, domain.DefaultMinApprovals)
	wantAt = idle.Activity.LastActivityAt.Add(waitingInputGrace)
	if got.NextTransitionAt == nil || !got.NextTransitionAt.Equal(wantAt) {
		t.Fatalf("idle nextTransitionAt: got %v want %v", got.NextTransitionAt, wantAt)
	}
}

func TestPRPipelineStatus_MinApprovalsThreshold(t *testing.T) {
	base := domain.PRFacts{
		URL:          "https://gitlab.example.com/g/p/-/merge_requests/1",
		Number:       1,
		CI:           domain.CIPassing,
		Mergeability: domain.MergeUnknown,
	}

	// No rule, count >= threshold → approved.
	pr := base
	pr.ApprovalRuleConfigured = false
	pr.ApprovalsCount = 3
	if got := prPipelineStatus(pr, 3); got != domain.StatusApproved {
		t.Fatalf("count 3 / min 3: got %s, want approved", got)
	}

	// No rule, count < threshold → stays pr_open (In review).
	pr.ApprovalsCount = 2
	if got := prPipelineStatus(pr, 3); got != domain.StatusPROpen {
		t.Fatalf("count 2 / min 3: got %s, want pr_open", got)
	}

	// Rule configured → threshold ignored; GitLab's decision (here none) wins → pr_open.
	pr.ApprovalRuleConfigured = true
	pr.ApprovalsCount = 1
	if got := prPipelineStatus(pr, 3); got != domain.StatusPROpen {
		t.Fatalf("rule configured: got %s, want pr_open", got)
	}

	// Threshold met but an unresolved thread still wins (worst-wins).
	pr.ApprovalRuleConfigured = false
	pr.ApprovalsCount = 3
	pr.ReviewComments = true
	if got := prPipelineStatus(pr, 3); got != domain.StatusChangesRequested {
		t.Fatalf("unresolved thread: got %s, want changes_requested", got)
	}
}
