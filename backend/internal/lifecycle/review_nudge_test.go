package lifecycle

import (
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// The auto-send review nudge must be driven by REVIEWER feedback the worker has
// not been told about yet — never by the worker's own replies. These tests drive
// the real SCM observation shape (threads with notes, a PR author that equals the
// worker's SCM identity) through ApplySCMObservation, which is the path the
// running daemon uses, so the fixtures cannot accidentally degrade into "one note
// per thread".
//
// The scenario mirrors the reported loop: auto-send ON, auto-resolve OFF (so an
// addressed thread deliberately stays unresolved after the worker replies).

const selfLogin = "ao-worker"

// autoNudgeSession returns a working session that opted into the auto-send nudge.
func autoNudgeSession(id domain.SessionID) domain.SessionRecord {
	rec := working(id)
	on := true
	rec.AutoNudgeComments = &on
	return rec
}

// reviewObs builds a fetched SCM observation whose PR is authored by selfLogin
// (AO's worker opens the PR and replies with the same token, so the PR author is
// "our side") and which carries the given review threads.
func reviewObs(threads ...ports.SCMReviewThreadObservation) ports.SCMObservation {
	return ports.SCMObservation{
		Fetched: true,
		PR:      ports.SCMPRObservation{URL: "pr1", Number: 7, Title: "add nil guard", Author: selfLogin},
		Review:  ports.SCMReviewObservation{Threads: threads},
	}
}

// thread builds one unresolved, diff-anchored review thread.
func thread(id, path string, line int, comments ...ports.SCMReviewCommentObservation) ports.SCMReviewThreadObservation {
	return ports.SCMReviewThreadObservation{ID: id, Path: path, Line: line, Comments: comments}
}

func reviewerNote(id, body string) ports.SCMReviewCommentObservation {
	return ports.SCMReviewCommentObservation{ID: id, Author: "alice", Body: body}
}

func selfNote(id, body string) ports.SCMReviewCommentObservation {
	return ports.SCMReviewCommentObservation{ID: id, Author: selfLogin, Body: body}
}

// TestSCMObservation_SelfReplyDoesNotRenudge is the reported bug: the worker
// addresses the thread and posts its reply, the thread stays unresolved (the
// confirm-before-reply rule plus auto-resolve OFF), and the next observation
// nudges the worker again about work it has already done.
func TestSCMObservation_SelfReplyDoesNotRenudge(t *testing.T) {
	m, st, msg := newManager()
	st.sessions["mer-1"] = autoNudgeSession("mer-1")

	first := reviewObs(thread("t1", "handler.go", 75, reviewerNote("n1", "guard this nil")))
	if err := m.ApplySCMObservation(ctx, "mer-1", first); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) != 1 {
		t.Fatalf("want one nudge for the reviewer comment, got %d: %v", len(msg.msgs), msg.msgs)
	}

	// The worker made the change and (after the human confirmed) posted its reply.
	// The thread is STILL unresolved: auto-resolve is off and the worker does not
	// resolve threads on its own.
	replied := reviewObs(thread("t1", "handler.go", 75,
		reviewerNote("n1", "guard this nil"),
		selfNote("n2", "Done - added the nil guard."),
	))
	if err := m.ApplySCMObservation(ctx, "mer-1", replied); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) != 1 {
		t.Fatalf("our own reply must not re-nudge the worker, got %d nudges: %v", len(msg.msgs), msg.msgs)
	}
}

// TestSCMObservation_ThreadWithSeveralNotesCountsOnce pins the unit of the count
// and the list: one unresolved THREAD is one item, however many notes it holds.
func TestSCMObservation_ThreadWithSeveralNotesCountsOnce(t *testing.T) {
	m, st, msg := newManager()
	st.sessions["mer-1"] = autoNudgeSession("mer-1")

	// A reviewer conversation on ONE thread: their note, our reply, their
	// follow-up. This is exactly what the human sees as a single open thread.
	obs := reviewObs(thread("t1", "handler.go", 75,
		reviewerNote("n1", "guard this nil"),
		selfNote("n2", "Done - added the nil guard."),
		reviewerNote("n3", "also log it"),
	))
	if err := m.ApplySCMObservation(ctx, "mer-1", obs); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) != 1 {
		t.Fatalf("want one nudge, got %d: %v", len(msg.msgs), msg.msgs)
	}
	got := msg.msgs[0]
	if strings.Contains(got, "There are 3") || strings.Contains(got, "There are 2") {
		t.Fatalf("one thread must count as one item, got %q", got)
	}
	if !strings.Contains(got, "A reviewer left an unresolved comment") {
		t.Fatalf("want the single-item phrasing for one thread, got %q", got)
	}
	if !strings.Contains(got, "handler.go:75") {
		t.Fatalf("nudge must locate the thread, got %q", got)
	}
	if strings.Contains(got, "Done - added the nil guard.") {
		t.Fatalf("our own reply must never be quoted back as a comment to address: %q", got)
	}
	for _, want := range []string{"guard this nil", "also log it"} {
		if !strings.Contains(got, want) {
			t.Fatalf("nudge dropped reviewer feedback %q: %q", want, got)
		}
	}
}

// TestSCMObservation_NewReviewerNoteOnAddressedThreadRenudges is the behavior the
// fix must preserve: genuinely new reviewer feedback on an already-addressed
// thread still reaches the worker.
func TestSCMObservation_NewReviewerNoteOnAddressedThreadRenudges(t *testing.T) {
	m, st, msg := newManager()
	st.sessions["mer-1"] = autoNudgeSession("mer-1")

	steps := []struct {
		name      string
		obs       ports.SCMObservation
		wantTotal int
	}{
		{
			name:      "reviewer opens the thread",
			obs:       reviewObs(thread("t1", "handler.go", 75, reviewerNote("n1", "guard this nil"))),
			wantTotal: 1,
		},
		{
			name: "we reply, thread stays unresolved",
			obs: reviewObs(thread("t1", "handler.go", 75,
				reviewerNote("n1", "guard this nil"),
				selfNote("n2", "Done - added the nil guard."),
			)),
			wantTotal: 1,
		},
		{
			name: "reviewer is not satisfied",
			obs: reviewObs(thread("t1", "handler.go", 75,
				reviewerNote("n1", "guard this nil"),
				selfNote("n2", "Done - added the nil guard."),
				reviewerNote("n3", "still panics on empty input"),
			)),
			wantTotal: 2,
		},
	}
	for _, s := range steps {
		if err := m.ApplySCMObservation(ctx, "mer-1", s.obs); err != nil {
			t.Fatalf("%s: %v", s.name, err)
		}
		if len(msg.msgs) != s.wantTotal {
			t.Fatalf("%s: want %d nudges in total, got %d: %v", s.name, s.wantTotal, len(msg.msgs), msg.msgs)
		}
	}
	last := msg.msgs[len(msg.msgs)-1]
	if !strings.Contains(last, "still panics on empty input") {
		t.Fatalf("the re-nudge must carry the NEW reviewer note, got %q", last)
	}
}

// TestSCMObservation_UntouchedThreadNudgesExactlyOnce covers the plain case: an
// unresolved thread nobody has replied to nudges once, and re-observing the same
// state (the common poll cycle) nudges nothing.
func TestSCMObservation_UntouchedThreadNudgesExactlyOnce(t *testing.T) {
	m, st, msg := newManager()
	st.sessions["mer-1"] = autoNudgeSession("mer-1")

	obs := reviewObs(thread("t1", "handler.go", 75, reviewerNote("n1", "guard this nil")))
	for i := 0; i < 3; i++ {
		if err := m.ApplySCMObservation(ctx, "mer-1", obs); err != nil {
			t.Fatal(err)
		}
	}
	if len(msg.msgs) != 1 {
		t.Fatalf("an unchanged observation must nudge exactly once, got %d: %v", len(msg.msgs), msg.msgs)
	}
}

// TestSCMObservation_NewThreadRenudges: a brand new reviewer thread is new work
// and must nudge, even while an older thread stays unresolved.
func TestSCMObservation_NewThreadRenudges(t *testing.T) {
	m, st, msg := newManager()
	st.sessions["mer-1"] = autoNudgeSession("mer-1")

	if err := m.ApplySCMObservation(ctx, "mer-1", reviewObs(
		thread("t1", "handler.go", 75, reviewerNote("n1", "guard this nil")),
	)); err != nil {
		t.Fatal(err)
	}
	if err := m.ApplySCMObservation(ctx, "mer-1", reviewObs(
		thread("t1", "handler.go", 75, reviewerNote("n1", "guard this nil"), selfNote("n2", "Done.")),
		thread("t2", "store.go", 12, reviewerNote("n3", "wrap this error")),
	)); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) != 2 {
		t.Fatalf("a new reviewer thread must nudge, got %d: %v", len(msg.msgs), msg.msgs)
	}
	second := msg.msgs[1]
	if !strings.Contains(second, "store.go:12") || !strings.Contains(second, "wrap this error") {
		t.Fatalf("the nudge must carry the new thread, got %q", second)
	}
	// Only the NEW thread is listed: the worker was already told about t1.
	if strings.Contains(second, "handler.go:75") {
		t.Fatalf("an already-dispatched thread must not be repeated, got %q", second)
	}
}

// TestSCMObservation_ResolvingOneThreadDoesNotRenudgeTheRest guards the shape of
// the dedup state: it records what the worker has been TOLD, so a change in the
// set of currently-unresolved threads is not by itself new work.
func TestSCMObservation_ResolvingOneThreadDoesNotRenudgeTheRest(t *testing.T) {
	m, st, msg := newManager()
	st.sessions["mer-1"] = autoNudgeSession("mer-1")

	both := reviewObs(
		thread("t1", "handler.go", 75, reviewerNote("n1", "guard this nil")),
		thread("t2", "store.go", 12, reviewerNote("n2", "wrap this error")),
	)
	if err := m.ApplySCMObservation(ctx, "mer-1", both); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) != 1 || !strings.Contains(msg.msgs[0], "There are 2 unresolved review comments") {
		t.Fatalf("want one nudge listing two threads, got %v", msg.msgs)
	}

	// The human resolved t1 after confirming our reply; t2 is still open and still
	// already-dispatched.
	t1Resolved := reviewObs(
		ports.SCMReviewThreadObservation{ID: "t1", Path: "handler.go", Line: 75, Resolved: true, Comments: []ports.SCMReviewCommentObservation{reviewerNote("n1", "guard this nil")}},
		thread("t2", "store.go", 12, reviewerNote("n2", "wrap this error")),
	)
	if err := m.ApplySCMObservation(ctx, "mer-1", t1Resolved); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) != 1 {
		t.Fatalf("resolving one thread must not re-nudge the remaining one, got %d: %v", len(msg.msgs), msg.msgs)
	}
}

// TestSCMObservation_SystemNoteDoesNotNudge: GitLab appends system notes ("changed
// this line in version 2 of the diff") to a thread when the diff moves. Those are
// not review feedback and must neither nudge nor appear in the list.
func TestSCMObservation_SystemNoteDoesNotNudge(t *testing.T) {
	m, st, msg := newManager()
	st.sessions["mer-1"] = autoNudgeSession("mer-1")

	if err := m.ApplySCMObservation(ctx, "mer-1", reviewObs(
		thread("t1", "handler.go", 75, reviewerNote("n1", "guard this nil")),
	)); err != nil {
		t.Fatal(err)
	}
	system := ports.SCMReviewCommentObservation{ID: "n2", Author: "alice", Body: "changed this line in version 2 of the diff", System: true}
	if err := m.ApplySCMObservation(ctx, "mer-1", reviewObs(
		thread("t1", "handler.go", 75, reviewerNote("n1", "guard this nil"), system),
	)); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) != 1 {
		t.Fatalf("a system note is not review feedback and must not nudge, got %d: %v", len(msg.msgs), msg.msgs)
	}
}

// TestSCMObservation_BareChangesRequestedNudgesOnce: a changes-requested decision
// with no inline threads still nudges, and stays nudged once while the decision
// holds — the decision itself is not repeatable news.
func TestSCMObservation_BareChangesRequestedNudgesOnce(t *testing.T) {
	m, st, msg := newManager()
	st.sessions["mer-1"] = autoNudgeSession("mer-1")

	obs := ports.SCMObservation{
		Fetched: true,
		PR:      ports.SCMPRObservation{URL: "pr1", Number: 7, Author: selfLogin},
		Review:  ports.SCMReviewObservation{Decision: string(domain.ReviewChangesRequest)},
	}
	for i := 0; i < 2; i++ {
		if err := m.ApplySCMObservation(ctx, "mer-1", obs); err != nil {
			t.Fatal(err)
		}
	}
	if len(msg.msgs) != 1 {
		t.Fatalf("want exactly one bare changes-requested nudge, got %d: %v", len(msg.msgs), msg.msgs)
	}
	if !strings.Contains(msg.msgs[0], "requested changes") {
		t.Fatalf("want the changes-requested phrasing, got %q", msg.msgs[0])
	}
}

// TestSCMObservation_ChangesRequestedWithSelfReplyDoesNotRenudge combines the two
// triggers: the decision stays changes_requested (the reviewer has not re-reviewed)
// AND our reply lands on the thread. Neither is new work.
func TestSCMObservation_ChangesRequestedWithSelfReplyDoesNotRenudge(t *testing.T) {
	m, st, msg := newManager()
	st.sessions["mer-1"] = autoNudgeSession("mer-1")

	obs := reviewObs(thread("t1", "handler.go", 75, reviewerNote("n1", "guard this nil")))
	obs.Review.Decision = string(domain.ReviewChangesRequest)
	if err := m.ApplySCMObservation(ctx, "mer-1", obs); err != nil {
		t.Fatal(err)
	}
	replied := reviewObs(thread("t1", "handler.go", 75,
		reviewerNote("n1", "guard this nil"),
		selfNote("n2", "Done - added the nil guard."),
	))
	replied.Review.Decision = string(domain.ReviewChangesRequest)
	if err := m.ApplySCMObservation(ctx, "mer-1", replied); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) != 1 {
		t.Fatalf("a held changes-requested decision plus our own reply must not re-nudge, got %d: %v", len(msg.msgs), msg.msgs)
	}
}

// TestSCMObservation_ReviewCommentFromThePRAuthorAccountStillNudges guards the
// trap in "self = the PR author": AO's worker opens the PR with the HUMAN's SCM
// token, so a review comment the human leaves on their own worker's PR carries the
// PR author's identity too. Authorship alone cannot separate that from the worker's
// reply — position can, because feedback OPENS a thread and a reply follows one.
// Silencing the first case would turn this noisy bug into a silent one.
func TestSCMObservation_ReviewCommentFromThePRAuthorAccountStillNudges(t *testing.T) {
	m, st, msg := newManager()
	st.sessions["mer-1"] = autoNudgeSession("mer-1")

	// The human opened this thread from the forge UI, under the same account that
	// opened the PR.
	opened := reviewObs(thread("t1", "handler.go", 75, selfNote("n1", "this needs a nil guard")))
	if err := m.ApplySCMObservation(ctx, "mer-1", opened); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) != 1 || !strings.Contains(msg.msgs[0], "this needs a nil guard") {
		t.Fatalf("a review comment from the PR author's account is still feedback, got %v", msg.msgs)
	}

	// The worker addressed it and replied on the SAME thread, under the SAME
	// account. That reply is ours and must not re-nudge.
	replied := reviewObs(thread("t1", "handler.go", 75,
		selfNote("n1", "this needs a nil guard"),
		selfNote("n2", "Done - added the nil guard."),
	))
	if err := m.ApplySCMObservation(ctx, "mer-1", replied); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) != 1 {
		t.Fatalf("our reply on the thread must not re-nudge, got %d: %v", len(msg.msgs), msg.msgs)
	}
}

// TestSCMObservation_ThreadOpenerIsTheFirstHumanNote: a provider system note can
// precede the note that actually opened the thread, so the opener is the first
// HUMAN note, not literally the first entry. Otherwise a thread the human opened on
// their own worker's PR would be misread as our reply and never nudge.
func TestSCMObservation_ThreadOpenerIsTheFirstHumanNote(t *testing.T) {
	m, st, msg := newManager()
	st.sessions["mer-1"] = autoNudgeSession("mer-1")

	system := ports.SCMReviewCommentObservation{ID: "n0", Author: "alice", Body: "changed this line in version 2 of the diff", System: true}
	// The human opened this thread (under the PR author's account) after a system
	// note had already landed on it.
	if err := m.ApplySCMObservation(ctx, "mer-1", reviewObs(
		thread("t1", "handler.go", 75, system, selfNote("n1", "this needs a nil guard")),
	)); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) != 1 || !strings.Contains(msg.msgs[0], "this needs a nil guard") {
		t.Fatalf("the first human note opens the thread and is feedback, got %v", msg.msgs)
	}
	// Our reply follows it and must not re-nudge.
	if err := m.ApplySCMObservation(ctx, "mer-1", reviewObs(
		thread("t1", "handler.go", 75, system, selfNote("n1", "this needs a nil guard"), selfNote("n2", "Done.")),
	)); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) != 1 {
		t.Fatalf("our reply on the thread must not re-nudge, got %d: %v", len(msg.msgs), msg.msgs)
	}
}

// TestSCMObservation_WithdrawnChangesRequestedCanAnnounceAgain: the decision
// announcement is retracted when the reviewer withdraws it, so requesting changes a
// second time is news again. Without the retraction the "already announced" mark
// would silence a real second round.
func TestSCMObservation_WithdrawnChangesRequestedCanAnnounceAgain(t *testing.T) {
	m, st, msg := newManager()
	st.sessions["mer-1"] = autoNudgeSession("mer-1")

	bare := func(decision domain.ReviewDecision) ports.SCMObservation {
		return ports.SCMObservation{
			Fetched: true,
			PR:      ports.SCMPRObservation{URL: "pr1", Number: 7, Author: selfLogin},
			Review:  ports.SCMReviewObservation{Decision: string(decision)},
		}
	}
	steps := []struct {
		name      string
		decision  domain.ReviewDecision
		wantTotal int
	}{
		{name: "changes requested", decision: domain.ReviewChangesRequest, wantTotal: 1},
		{name: "reviewer approves", decision: domain.ReviewApproved, wantTotal: 1},
		{name: "changes requested again", decision: domain.ReviewChangesRequest, wantTotal: 2},
	}
	for _, s := range steps {
		if err := m.ApplySCMObservation(ctx, "mer-1", bare(s.decision)); err != nil {
			t.Fatalf("%s: %v", s.name, err)
		}
		if len(msg.msgs) != s.wantTotal {
			t.Fatalf("%s: want %d nudges in total, got %d: %v", s.name, s.wantTotal, len(msg.msgs), msg.msgs)
		}
	}
}

// TestSCMObservation_PersistedLegacySignatureDoesNotRenudge: signatures written
// before the dispatch mark existed are a bare comma-joined list of dispatched
// comment ids. Upgrading the daemon must read them as "already dispatched" rather
// than re-nudging every worker whose PR has an open thread.
func TestSCMObservation_PersistedLegacySignatureDoesNotRenudge(t *testing.T) {
	m, st, msg := newManager()
	st.sessions["mer-1"] = autoNudgeSession("mer-1")
	st.signatures["pr1"] = `{"seen":{"review:pr1":"n1,n2"},"attempts":{"review:pr1":1}}`

	obs := reviewObs(thread("t1", "handler.go", 75,
		reviewerNote("n1", "guard this nil"),
		reviewerNote("n2", "and here"),
	))
	if err := m.ApplySCMObservation(ctx, "mer-1", obs); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) != 0 {
		t.Fatalf("comments dispatched before the upgrade must not nudge again, got %v", msg.msgs)
	}
	// A note that is genuinely new still nudges through the migrated mark.
	obs.Review.Threads[0].Comments = append(obs.Review.Threads[0].Comments, reviewerNote("n3", "one more"))
	if err := m.ApplySCMObservation(ctx, "mer-1", obs); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) != 1 || !strings.Contains(msg.msgs[0], "one more") {
		t.Fatalf("want one nudge carrying only the new note, got %v", msg.msgs)
	}
	if strings.Contains(msg.msgs[0], "guard this nil") {
		t.Fatalf("already-dispatched notes must not be repeated: %q", msg.msgs[0])
	}
}

// TestSCMObservation_SelfReplyStillBlocksReadyToMerge: the nudge stops firing, but
// the thread is still open on the forge, so the PR is NOT ready to merge. The fix
// must not quietly turn "we replied" into "the review is done".
func TestSCMObservation_SelfReplyStillBlocksReadyToMerge(t *testing.T) {
	obs := reviewObs(thread("t1", "handler.go", 75,
		reviewerNote("n1", "guard this nil"),
		selfNote("n2", "Done - added the nil guard."),
	))
	obs.CI = ports.SCMCIObservation{Summary: string(domain.CIPassing)}
	obs.Mergeability = ports.SCMMergeabilityObservation{State: string(domain.MergeMergeable)}
	if scmObservationIsReadyToMerge(obs, false) {
		t.Fatal("an unresolved thread we merely replied to must still block ready-to-merge")
	}
}

// TestPRObservation_ThreadlessCommentsEachCount guards the legacy projection: a
// PRObservation whose comments carry no thread id (the pre-thread DTO shape) must
// keep counting one item per comment rather than collapsing into a single item.
func TestPRObservation_ThreadlessCommentsEachCount(t *testing.T) {
	m, st, msg := newManager()
	st.sessions["mer-1"] = autoNudgeSession("mer-1")

	o := ports.PRObservation{Fetched: true, URL: "pr1", Comments: []ports.PRCommentObservation{
		{ID: "1", Author: "alice", File: "a.go", Line: 1, Body: "first"},
		{ID: "2", Author: "alice", File: "b.go", Line: 2, Body: "second"},
	}}
	if err := m.ApplyPRObservation(ctx, "mer-1", o); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) != 1 || !strings.Contains(msg.msgs[0], "There are 2 unresolved review comments") {
		t.Fatalf("threadless comments must each count, got %v", msg.msgs)
	}
}
