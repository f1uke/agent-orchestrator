// This file is the end-to-end regression guard for the "PR #N is ready to merge"
// notification. It wires the real sqlite.Store, the real notify.Manager and the
// real lifecycle.Manager into the real observe/scm.Observer and drives
// Observer.Poll directly, so it sees exactly what the daemon does.
//
// The lifecycle package's own notification tests all use a fake sink, so they
// can only assert "an intent was produced" — they cannot see the production
// dedup, which lives one layer down in the store's unread-only partial unique
// index. That gap is what let a level-triggered notification re-fire for a PR
// that never moved.
package integration

import (
	"context"
	"io"
	"log/slog"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/lifecycle"
	"github.com/aoagents/agent-orchestrator/backend/internal/notify"
	scmobserve "github.com/aoagents/agent-orchestrator/backend/internal/observe/scm"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// restartDaemon rebuilds every in-memory collaborator (lifecycle, notify,
// observer) over the SAME on-disk store, reproducing what an app reinstall does.
// Any dedup state that lives only in memory is discarded here.
func (f *scmFixture) restartDaemon(t *testing.T) {
	t.Helper()
	f.spy = &scmMessengerSpy{}
	f.lcm = lifecycle.New(f.store, f.spy, lifecycle.WithNotificationSink(newSCMNotifier(f.store, f.now)))
	f.observer = scmobserve.New(f.provider, f.store, f.lcm, scmobserve.Config{
		Tick:   time.Hour,
		Clock:  func() time.Time { return f.now },
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
}

// scmNotifierSeq backs newSCMNotifier's ids. It is package-level on purpose:
// a per-Manager counter restarts at 1 when restartDaemon builds a new notifier,
// and the reissued "ntf_1" then collides with the existing row's PRIMARY KEY.
// The insert fails, no notification appears, and a restart test passes without
// proving anything. Ids must stay unique across restarts.
var scmNotifierSeq atomic.Int64

// newSCMNotifier builds the production write-side notification manager over the
// real store, with deterministic ids so failures are readable.
func newSCMNotifier(store *sqlite.Store, now time.Time) *notify.Manager {
	return notify.New(notify.Deps{
		Store: store,
		Clock: func() time.Time { return now },
		NewID: func() string { return "ntf_" + strconv.FormatInt(scmNotifierSeq.Add(1), 10) },
	})
}

// readySCMObservation is a PR with no known merge blockers: open, not draft,
// CI passing, no review objection, mergeable. This is the exact shape
// scmObservationIsReadyToMerge accepts.
func readySCMObservation(prURL string, num int, headSHA string) ports.SCMObservation {
	return ports.SCMObservation{
		Fetched:  true,
		Provider: "github", Host: "github.com", Repo: "octocat/hello",
		PR: ports.SCMPRObservation{
			URL:          prURL,
			HTMLURL:      prURL,
			Number:       num,
			State:        string(domain.PRStateOpen),
			SourceBranch: "feat/x",
			TargetBranch: "main",
			HeadSHA:      headSHA,
			Title:        "Ready when you are",
		},
		CI:           ports.SCMCIObservation{Summary: string(domain.CIPassing), HeadSHA: headSHA},
		Review:       ports.SCMReviewObservation{Decision: string(domain.ReviewNone)},
		Mergeability: ports.SCMMergeabilityObservation{State: string(domain.MergeMergeable), Mergeable: true},
	}
}

// seedReadyPR points the canned provider at a single ready-to-merge PR.
func seedReadyPR(f *scmFixture, prURL string, num int, headSHA string) {
	f.provider.detected["feat/x"] = ports.SCMPRObservation{
		URL: prURL, Number: num, SourceBranch: "feat/x", HeadRepo: scmTestRepo.Repo,
		TargetBranch: "main", HeadSHA: headSHA,
	}
	f.provider.observations[num] = readySCMObservation(prURL, num, headSHA)
}

func unreadCount(t *testing.T, f *scmFixture) int {
	t.Helper()
	rows, err := f.store.ListUnreadNotifications(context.Background(), 50)
	if err != nil {
		t.Fatalf("ListUnreadNotifications: %v", err)
	}
	return len(rows)
}

func pollN(t *testing.T, f *scmFixture, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if err := f.observer.Poll(context.Background()); err != nil {
			t.Fatalf("Poll #%d: %v", i+1, err)
		}
	}
}

// TestReadyToMergeNotifiesOnceForAnUnchangedPR reproduces the reported bug: the
// human opens the tray, reads the notifications, and the very next poll cycle
// re-creates them for merge requests that never moved.
//
// The notification is only useful if it means "this just became ready". If it
// means "this is ready", it re-fires forever and buries the real ones.
func TestReadyToMergeNotifiesOnceForAnUnchangedPR(t *testing.T) {
	ctx := context.Background()
	f := newSCMFixture(t, "feat/x")
	const (
		prURL   = "https://github.com/octocat/hello/pull/42"
		headSHA = "cafebabe"
	)
	seedReadyPR(f, prURL, 42, headSHA)

	// The PR becomes ready: exactly one notification. This is the legitimate edge.
	pollN(t, f, 1)
	if got := unreadCount(t, f); got != 1 {
		t.Fatalf("after first poll unread = %d, want 1 (the PR just became ready)", got)
	}

	// Nothing about the PR changes. Repeat polls must not pile up notifications.
	// This much already holds today, via the unread-only dedup index.
	pollN(t, f, 3)
	if got := unreadCount(t, f); got != 1 {
		t.Fatalf("after repeat polls unread = %d, want 1 (PR did not move)", got)
	}

	// The human opens the tray and marks everything read — the same store call
	// POST /api/v1/notifications/read-all makes.
	if _, err := f.store.MarkAllNotificationsRead(ctx); err != nil {
		t.Fatalf("MarkAllNotificationsRead: %v", err)
	}
	if got := unreadCount(t, f); got != 0 {
		t.Fatalf("after mark-all-read unread = %d, want 0", got)
	}

	// Still nothing about the PR has changed: no new push, no pipeline, no review
	// activity. The observation is byte-identical to the one already notified on.
	// Reading a notification is not a state transition on the PR, so no further
	// notification may be produced.
	pollN(t, f, 3)
	if got := unreadCount(t, f); got != 0 {
		t.Fatalf("unread = %d after polling an UNCHANGED already-notified PR, want 0: "+
			"the ready-to-merge notification re-fired because the condition is still "+
			"true, not because the PR became ready again", got)
	}
}

// TestReadyToMergeDoesNotRepeatWhenAnAlreadyReadyPRChangesUnrelatedly pins the
// notification as EDGE-triggered rather than level-triggered.
//
// The store's dedup index only suppresses while the previous row is still
// unread, so once the human reads the notification the suppression is gone. Any
// later observation that reaches lifecycle then re-notifies — even though the PR
// was ready before the change, during it, and after it. It never left the ready
// state, so there is no new edge and nothing to tell the human.
func TestReadyToMergeDoesNotRepeatWhenAnAlreadyReadyPRChangesUnrelatedly(t *testing.T) {
	ctx := context.Background()
	f := newSCMFixture(t, "feat/x")
	const (
		prURL   = "https://github.com/octocat/hello/pull/64"
		headSHA = "feedface"
	)
	seedReadyPR(f, prURL, 64, headSHA)

	pollN(t, f, 1)
	if got := unreadCount(t, f); got != 1 {
		t.Fatalf("after becoming ready unread = %d, want 1", got)
	}

	// The human reads it. This is not a fact about the PR.
	if _, err := f.store.MarkAllNotificationsRead(ctx); err != nil {
		t.Fatalf("MarkAllNotificationsRead: %v", err)
	}

	// Something unrelated moves that does not affect readiness: a pipeline is
	// re-run on the SAME head commit and reports an extra passing job. CI stays
	// passing and the PR stays mergeable throughout — it never leaves the ready
	// state, so the human has nothing new to learn.
	obs := readySCMObservation(prURL, 64, headSHA)
	obs.CI.Checks = []ports.SCMCheckObservation{{
		Name:       "lint",
		Status:     string(domain.PRCheckPassed),
		Conclusion: "success",
		ProviderID: "4242",
	}}
	f.provider.observations[64] = obs

	pollN(t, f, 2)
	if got := unreadCount(t, f); got != 0 {
		t.Fatalf("unread = %d after an unrelated change to a PR that never left the "+
			"ready state, want 0: the notification is level-triggered, so it re-fires "+
			"whenever anything reaches lifecycle while the PR happens to be ready", got)
	}
}

// TestReadyToMergeSurvivesDaemonRestart covers the restart-replay theory: the
// human installs builds often, so a marker that lives only in memory would
// replay every available PR at once on restart. Rebuilding lifecycle + observer
// over the SAME store is exactly that restart.
func TestReadyToMergeSurvivesDaemonRestart(t *testing.T) {
	ctx := context.Background()
	f := newSCMFixture(t, "feat/x")
	const (
		prURL   = "https://github.com/octocat/hello/pull/77"
		headSHA = "d00dfeed"
	)
	seedReadyPR(f, prURL, 77, headSHA)

	pollN(t, f, 1)
	if got := unreadCount(t, f); got != 1 {
		t.Fatalf("after first poll unread = %d, want 1", got)
	}
	if _, err := f.store.MarkAllNotificationsRead(ctx); err != nil {
		t.Fatalf("MarkAllNotificationsRead: %v", err)
	}

	// Daemon restart: fresh lifecycle.Manager, fresh notify.Manager, fresh
	// Observer, same on-disk store and same untouched PR. Every in-memory dedup
	// map is discarded here; only what reached disk survives.
	f.restartDaemon(t)

	// An unchanged PR never reaches lifecycle at all (the observer stops at the
	// unchanged-hash gate), which would make this test pass without proving
	// anything. Give it a readiness-irrelevant change so lifecycle really runs
	// after the restart: a pipeline re-run on the same head, still passing.
	obs := readySCMObservation(prURL, 77, headSHA)
	obs.CI.Checks = []ports.SCMCheckObservation{{
		Name: "lint", Status: string(domain.PRCheckPassed), Conclusion: "success", ProviderID: "7777",
	}}
	f.provider.observations[77] = obs

	pollN(t, f, 2)
	if got := unreadCount(t, f); got != 0 {
		t.Fatalf("unread = %d after a daemon restart over an unchanged already-notified PR, want 0", got)
	}
}

// TestReadyToMergeNotifiesAgainAfterLeavingAndReenteringReadyState is the
// control: suppression must be edge-triggered, not permanent. A PR that stops
// being ready (CI goes red on a new push) and later becomes ready again is a
// genuine new edge and must notify again.
func TestReadyToMergeNotifiesAgainAfterLeavingAndReenteringReadyState(t *testing.T) {
	ctx := context.Background()
	f := newSCMFixture(t, "feat/x")
	const prURL = "https://github.com/octocat/hello/pull/99"
	seedReadyPR(f, prURL, 99, "aaaa1111")

	pollN(t, f, 1)
	if got := unreadCount(t, f); got != 1 {
		t.Fatalf("after becoming ready unread = %d, want 1", got)
	}
	if _, err := f.store.MarkAllNotificationsRead(ctx); err != nil {
		t.Fatalf("MarkAllNotificationsRead: %v", err)
	}

	// A new push breaks CI: the PR leaves the ready state.
	f.provider.detected["feat/x"] = ports.SCMPRObservation{
		URL: prURL, Number: 99, SourceBranch: "feat/x", HeadRepo: scmTestRepo.Repo,
		TargetBranch: "main", HeadSHA: "bbbb2222",
	}
	f.provider.observations[99] = failingSCMObservation(prURL, 99, "bbbb2222", "FAILED: build broke\n")
	pollN(t, f, 1)
	if got := unreadCount(t, f); got != 0 {
		t.Fatalf("unread = %d while CI is failing, want 0 (not ready)", got)
	}

	// CI is fixed: the PR re-enters the ready state. That is a real transition.
	f.provider.detected["feat/x"] = ports.SCMPRObservation{
		URL: prURL, Number: 99, SourceBranch: "feat/x", HeadRepo: scmTestRepo.Repo,
		TargetBranch: "main", HeadSHA: "cccc3333",
	}
	f.provider.observations[99] = readySCMObservation(prURL, 99, "cccc3333")
	pollN(t, f, 1)
	if got := unreadCount(t, f); got != 1 {
		t.Fatalf("unread = %d after the PR became ready again, want 1: "+
			"re-entering the ready state is a genuine edge and must notify", got)
	}
}

// closedSCMObservation is the same PR after it closed without merging.
// (mergedSCMObservation for the merged case already lives in scm_observer_test.go.)
func closedSCMObservation(prURL string, num int, headSHA string) ports.SCMObservation {
	o := readySCMObservation(prURL, num, headSHA)
	o.PR.State = string(domain.PRStateClosed)
	o.PR.Closed = true
	return o
}

// TestMergedNotifiesOnceForAnAlreadyMergedPR pins pr_merged as EDGE-triggered,
// the same property #148 gave ready_to_merge and left off the other two.
//
// This is driven through lifecycle rather than Observer.Poll on purpose. The
// observer's openTrackedPRs filter drops merged and closed PRs from the poll set,
// so in production this defect does not fire today - by luck, not by design.
// Anything that delivers a second observation of a merged PR (a reopen, a manual
// refresh, a backfill) resurrects the exact #148 symptom, so the guarantee has to
// be pinned where the decision is actually made.
func TestMergedNotifiesOnceForAnAlreadyMergedPR(t *testing.T) {
	ctx := context.Background()
	f := newSCMFixture(t, "feat/x")
	const (
		prURL   = "https://github.com/octocat/hello/pull/77"
		headSHA = "d15ea5e"
	)
	seedReadyPR(f, prURL, 77, headSHA)
	pollN(t, f, 1) // establishes the PR row

	merged := mergedSCMObservation(prURL, 77, headSHA)
	if err := f.lcm.ApplySCMObservation(ctx, f.session.ID, merged); err != nil {
		t.Fatalf("ApplySCMObservation (merge edge): %v", err)
	}
	before := unreadCount(t, f)

	// The human reads the tray, clearing the store's unread-only dedup index.
	if _, err := f.store.MarkAllNotificationsRead(ctx); err != nil {
		t.Fatalf("MarkAllNotificationsRead: %v", err)
	}

	// The PR is still merged. It did not merge again, so there is no new edge.
	for i := 0; i < 3; i++ {
		if err := f.lcm.ApplySCMObservation(ctx, f.session.ID, merged); err != nil {
			t.Fatalf("ApplySCMObservation (repeat %d): %v", i+1, err)
		}
	}
	if got := unreadCount(t, f); got != 0 {
		t.Fatalf("unread = %d after re-observing an already-merged PR, want 0: "+
			"pr_merged re-fired because the PR IS merged, not because it just merged "+
			"(first edge produced %d)", got, before)
	}
}

// TestClosedNotifiesOnceAndAgainAfterAReopen is the control for the clearing
// side of the marker: a reopened PR that closes a second time is genuinely news.
func TestClosedNotifiesOnceAndAgainAfterAReopen(t *testing.T) {
	ctx := context.Background()
	f := newSCMFixture(t, "feat/x")
	const (
		prURL   = "https://github.com/octocat/hello/pull/78"
		headSHA = "b0bacafe"
	)
	seedReadyPR(f, prURL, 78, headSHA)
	pollN(t, f, 1)
	// The seeding poll legitimately announces ready-to-merge. Clear it so the
	// counts below are about the close/reopen edges only.
	if _, err := f.store.MarkAllNotificationsRead(ctx); err != nil {
		t.Fatalf("MarkAllNotificationsRead (baseline): %v", err)
	}

	closed := closedSCMObservation(prURL, 78, headSHA)
	apply := func(o ports.SCMObservation, what string) {
		t.Helper()
		if err := f.lcm.ApplySCMObservation(ctx, f.session.ID, o); err != nil {
			t.Fatalf("ApplySCMObservation (%s): %v", what, err)
		}
	}

	apply(closed, "close edge")
	apply(closed, "still closed")
	if got := unreadOfType(t, f, domain.NotificationPRClosedUnmerged); got != 1 {
		t.Fatalf("unread pr_closed_unmerged = %d after closing once, want 1", got)
	}
	if _, err := f.store.MarkAllNotificationsRead(ctx); err != nil {
		t.Fatalf("MarkAllNotificationsRead: %v", err)
	}

	// Reopened: the closed state is left, so the marker must clear. The reopen
	// also re-enters the ready state, which is its own legitimate notification -
	// hence the assertions below count pr_closed_unmerged specifically.
	apply(readySCMObservation(prURL, 78, headSHA), "reopen")
	// Closed again. This IS a new edge and must notify.
	apply(closed, "second close edge")
	if got := unreadOfType(t, f, domain.NotificationPRClosedUnmerged); got != 1 {
		t.Fatalf("unread pr_closed_unmerged = %d after a reopen-then-close, want 1: "+
			"closing a second time is genuine news and the marker must have cleared "+
			"on the reopen", got)
	}
}

func unreadOfType(t *testing.T, f *scmFixture, typ domain.NotificationType) int {
	t.Helper()
	rows, err := f.store.ListUnreadNotifications(context.Background(), 50)
	if err != nil {
		t.Fatalf("ListUnreadNotifications: %v", err)
	}
	n := 0
	for _, r := range rows {
		if r.Type == typ {
			n++
		}
	}
	return n
}
