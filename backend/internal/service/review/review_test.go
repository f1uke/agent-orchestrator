package review

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/lifecycle"
	reviewcore "github.com/aoagents/agent-orchestrator/backend/internal/review"
	"github.com/aoagents/agent-orchestrator/backend/internal/treewatch"
)

type fakeStore struct {
	run       domain.ReviewRun
	ok        bool
	batchRuns []domain.ReviewRun
	prs       []domain.PullRequest
	review    *domain.Review

	updateCalls    int
	markCalls      int
	markedIDs      []string
	supersededBody string
}

func (f *fakeStore) GetReviewRun(_ context.Context, id string) (domain.ReviewRun, bool, error) {
	for _, run := range f.batchRuns {
		if run.ID == id {
			return run, true, nil
		}
	}
	if f.ok && f.run.ID == id {
		return f.run, true, nil
	}
	return domain.ReviewRun{}, false, nil
}

func (f *fakeStore) UpdateReviewRunResult(_ context.Context, id string, status domain.ReviewRunStatus, verdict domain.ReviewVerdict, body, githubReviewID string) (bool, error) {
	for i := range f.batchRuns {
		if f.batchRuns[i].ID == id {
			if f.batchRuns[i].Status != domain.ReviewRunRunning {
				return false, nil
			}
			f.updateCalls++
			f.batchRuns[i].Status = status
			f.batchRuns[i].Verdict = verdict
			f.batchRuns[i].Body = body
			f.batchRuns[i].GithubReviewID = githubReviewID
			if f.run.ID == id {
				f.run = f.batchRuns[i]
			}
			return true, nil
		}
	}
	if f.run.Status != domain.ReviewRunRunning {
		return false, nil
	}
	f.updateCalls++
	f.run.Status = status
	f.run.Verdict = verdict
	f.run.Body = body
	f.run.GithubReviewID = githubReviewID
	return true, nil
}

func (f *fakeStore) MarkReviewRunDelivered(_ context.Context, id string, deliveredAt time.Time) (bool, error) {
	f.markCalls++
	f.markedIDs = append(f.markedIDs, id)
	if f.run.ID == id && f.run.Status == domain.ReviewRunComplete && f.run.DeliveredAt == nil {
		f.run.Status = domain.ReviewRunDelivered
		f.run.DeliveredAt = &deliveredAt
	}
	for i := range f.batchRuns {
		if f.batchRuns[i].ID == id && f.batchRuns[i].Status == domain.ReviewRunComplete && f.batchRuns[i].DeliveredAt == nil {
			f.batchRuns[i].Status = domain.ReviewRunDelivered
			f.batchRuns[i].DeliveredAt = &deliveredAt
			return true, nil
		}
	}
	if f.run.ID != id || f.run.Status != domain.ReviewRunDelivered {
		return false, nil
	}
	return true, nil
}

func (f *fakeStore) ListReviewRunsByBatch(context.Context, domain.SessionID, string) ([]domain.ReviewRun, error) {
	out := append([]domain.ReviewRun(nil), f.batchRuns...)
	return out, nil
}

func (f *fakeStore) ListPRsBySession(context.Context, domain.SessionID) ([]domain.PullRequest, error) {
	out := append([]domain.PullRequest(nil), f.prs...)
	return out, nil
}

type fakeReducer struct {
	outcome    lifecycle.ReviewDeliveryOutcome
	err        error
	calls      int
	batchCalls int
	got        lifecycle.ReviewResult
	gotBatchID string
	gotBatch   []lifecycle.ReviewResult
}

func (f *fakeReducer) ApplyReviewResult(_ context.Context, _ domain.SessionID, result lifecycle.ReviewResult) (lifecycle.ReviewDeliveryOutcome, error) {
	f.calls++
	f.got = result
	return f.outcome, f.err
}

func (f *fakeReducer) ApplyReviewBatch(_ context.Context, _ domain.SessionID, batchID string, results []lifecycle.ReviewResult) (lifecycle.ReviewDeliveryOutcome, error) {
	f.batchCalls++
	f.gotBatchID = batchID
	f.gotBatch = append([]lifecycle.ReviewResult(nil), results...)
	return f.outcome, f.err
}

func TestSubmitPersistsThenAppliesThenStampsDelivered(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	st := &fakeStore{ok: true, run: domain.ReviewRun{ID: "run-1", SessionID: "mer-1", PRURL: "pr1", TargetSHA: "sha1", Status: domain.ReviewRunRunning}}
	reducer := &fakeReducer{outcome: lifecycle.ReviewDeliverySent}
	svc := New(nil, st, WithLifecycleReducer(reducer), WithClock(func() time.Time { return now }))

	run, err := svc.Submit(context.Background(), "mer-1", "run-1", domain.VerdictChangesRequested, "fix it", "987")
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if st.updateCalls != 1 || reducer.calls != 1 || st.markCalls != 1 {
		t.Fatalf("calls update/reducer/mark = %d/%d/%d", st.updateCalls, reducer.calls, st.markCalls)
	}
	if reducer.got.Verdict != domain.VerdictChangesRequested || reducer.got.Body != "fix it" || reducer.got.GithubReviewID != "987" {
		t.Fatalf("reducer saw wrong result: %+v", reducer.got)
	}
	if run.Status != domain.ReviewRunDelivered || run.DeliveredAt == nil || !run.DeliveredAt.Equal(now) {
		t.Fatalf("run not stamped delivered: %+v", run)
	}
}

func TestSubmitBatchRunDoesNotWaitForOtherRunningRuns(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	st := &fakeStore{
		ok:  true,
		run: domain.ReviewRun{ID: "run-1", SessionID: "mer-1", BatchID: "batch-1", PRURL: "pr1", TargetSHA: "sha1", Status: domain.ReviewRunRunning},
		batchRuns: []domain.ReviewRun{
			{ID: "run-1", SessionID: "mer-1", BatchID: "batch-1", PRURL: "pr1", TargetSHA: "sha1", Status: domain.ReviewRunRunning},
			{ID: "run-2", SessionID: "mer-1", BatchID: "batch-1", PRURL: "pr2", TargetSHA: "sha2", Status: domain.ReviewRunRunning},
		},
		prs: []domain.PullRequest{{URL: "pr1", HeadSHA: "sha1"}, {URL: "pr2", HeadSHA: "sha2"}},
	}
	reducer := &fakeReducer{outcome: lifecycle.ReviewDeliverySent}
	svc := New(nil, st, WithLifecycleReducer(reducer), WithClock(func() time.Time { return now }))

	run, err := svc.Submit(context.Background(), "mer-1", "run-1", domain.VerdictChangesRequested, "fix pr1", "101")
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if run.Status != domain.ReviewRunDelivered || run.DeliveredAt == nil || !run.DeliveredAt.Equal(now) {
		t.Fatalf("first submit status = %+v, want delivered", run)
	}
	if reducer.batchCalls != 1 || len(reducer.gotBatch) != 1 || reducer.gotBatch[0].RunID != "run-1" || st.markCalls != 1 {
		t.Fatalf("submitted run should deliver independently: batchCalls=%d got=%+v markCalls=%d", reducer.batchCalls, reducer.gotBatch, st.markCalls)
	}
}

func TestSubmitManySendsCombinedChangesRequested(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	st := &fakeStore{
		ok: true,
		batchRuns: []domain.ReviewRun{
			{ID: "run-1", SessionID: "mer-1", BatchID: "batch-1", PRURL: "pr1", TargetSHA: "sha1", Status: domain.ReviewRunRunning},
			{ID: "run-2", SessionID: "mer-1", BatchID: "batch-1", PRURL: "pr2", TargetSHA: "sha2", Status: domain.ReviewRunRunning},
			{ID: "run-3", SessionID: "mer-1", BatchID: "batch-1", PRURL: "pr3", TargetSHA: "sha3", Status: domain.ReviewRunComplete, Verdict: domain.VerdictApproved},
			{ID: "run-4", SessionID: "mer-1", BatchID: "batch-1", PRURL: "pr4", TargetSHA: "old", Status: domain.ReviewRunComplete, Verdict: domain.VerdictChangesRequested, Body: "stale"},
			{ID: "run-5", SessionID: "mer-1", BatchID: "batch-1", PRURL: "pr5", TargetSHA: "sha5", Status: domain.ReviewRunFailed},
		},
		prs: []domain.PullRequest{
			{URL: "pr1", HeadSHA: "sha1"},
			{URL: "pr2", HeadSHA: "sha2"},
			{URL: "pr3", HeadSHA: "sha3"},
			{URL: "pr4", HeadSHA: "new"},
			{URL: "pr5", HeadSHA: "sha5"},
		},
	}
	reducer := &fakeReducer{outcome: lifecycle.ReviewDeliverySent}
	svc := New(nil, st, WithLifecycleReducer(reducer), WithClock(func() time.Time { return now }))

	runs, err := svc.SubmitMany(context.Background(), "mer-1", []SubmittedReview{
		{RunID: "run-1", Verdict: domain.VerdictChangesRequested, Body: "fix pr1", GithubReviewID: "101"},
		{RunID: "run-2", Verdict: domain.VerdictChangesRequested, Body: "fix pr2", GithubReviewID: "102"},
		{RunID: "run-3", Verdict: domain.VerdictApproved},
	})
	if err != nil {
		t.Fatalf("SubmitMany: %v", err)
	}
	if reducer.batchCalls != 1 || reducer.gotBatchID != "batch-1" {
		t.Fatalf("batch delivery calls/id = %d/%q", reducer.batchCalls, reducer.gotBatchID)
	}
	if len(reducer.gotBatch) != 2 || reducer.gotBatch[0].RunID != "run-1" || reducer.gotBatch[1].RunID != "run-2" {
		t.Fatalf("delivered batch = %+v, want run-1 and run-2 only", reducer.gotBatch)
	}
	if st.markCalls != 2 {
		t.Fatalf("markCalls = %d, want 2", st.markCalls)
	}
	if runs[0].Status != domain.ReviewRunDelivered || runs[0].DeliveredAt == nil || !runs[0].DeliveredAt.Equal(now) ||
		runs[1].Status != domain.ReviewRunDelivered || runs[1].DeliveredAt == nil || !runs[1].DeliveredAt.Equal(now) {
		t.Fatalf("submitted runs not stamped delivered: %+v", runs)
	}
}

func TestSubmitBatchApprovedOnlySendsNothing(t *testing.T) {
	st := &fakeStore{
		ok:  true,
		run: domain.ReviewRun{ID: "run-2", SessionID: "mer-1", BatchID: "batch-1", PRURL: "pr2", TargetSHA: "sha2", Status: domain.ReviewRunRunning},
		batchRuns: []domain.ReviewRun{
			{ID: "run-1", SessionID: "mer-1", BatchID: "batch-1", PRURL: "pr1", TargetSHA: "sha1", Status: domain.ReviewRunComplete, Verdict: domain.VerdictApproved},
			{ID: "run-2", SessionID: "mer-1", BatchID: "batch-1", PRURL: "pr2", TargetSHA: "sha2", Status: domain.ReviewRunRunning},
		},
		prs: []domain.PullRequest{{URL: "pr1", HeadSHA: "sha1"}, {URL: "pr2", HeadSHA: "sha2"}},
	}
	reducer := &fakeReducer{outcome: lifecycle.ReviewDeliverySent}
	svc := New(nil, st, WithLifecycleReducer(reducer))

	if _, err := svc.Submit(context.Background(), "mer-1", "run-2", domain.VerdictApproved, "", "102"); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if reducer.batchCalls != 0 || st.markCalls != 0 {
		t.Fatalf("approved-only batch should not deliver: batchCalls=%d markCalls=%d", reducer.batchCalls, st.markCalls)
	}
}

func TestSubmitDeliveryFailureLeavesCompletedUndeliveredForRetry(t *testing.T) {
	sendErr := errors.New("dead pane")
	st := &fakeStore{ok: true, run: domain.ReviewRun{ID: "run-1", SessionID: "mer-1", PRURL: "pr1", TargetSHA: "sha1", Status: domain.ReviewRunRunning}}
	reducer := &fakeReducer{err: sendErr}
	svc := New(nil, st, WithLifecycleReducer(reducer))

	if _, err := svc.Submit(context.Background(), "mer-1", "run-1", domain.VerdictChangesRequested, "fix it", "987"); !errors.Is(err, sendErr) {
		t.Fatalf("err = %v, want sendErr", err)
	}
	if st.run.Status != domain.ReviewRunComplete || st.run.DeliveredAt != nil || st.markCalls != 0 {
		t.Fatalf("failed delivery should leave completed/undelivered without stamp: %+v markCalls=%d", st.run, st.markCalls)
	}

	reducer.err = nil
	reducer.outcome = lifecycle.ReviewDeliverySent
	if _, err := svc.Submit(context.Background(), "mer-1", "run-1", domain.VerdictChangesRequested, "fix it", "987"); err != nil {
		t.Fatalf("retry Submit: %v", err)
	}
	if st.updateCalls != 1 || reducer.calls != 2 || st.run.Status != domain.ReviewRunDelivered || st.run.DeliveredAt == nil {
		t.Fatalf("retry should not rewrite result and should stamp delivery: update=%d reducer=%d run=%+v", st.updateCalls, reducer.calls, st.run)
	}
}

func TestSubmitCompletedRetryRejectsDifferentRecordedFields(t *testing.T) {
	tests := []struct {
		name           string
		body           string
		githubReviewID string
	}{
		{name: "different body", body: "different", githubReviewID: "987"},
		{name: "different review id", body: "fix it", githubReviewID: "654"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := &fakeStore{ok: true, run: domain.ReviewRun{
				ID: "run-1", SessionID: "mer-1", PRURL: "pr1", TargetSHA: "sha1",
				Status: domain.ReviewRunComplete, Verdict: domain.VerdictChangesRequested,
				Body: "fix it", GithubReviewID: "987",
			}}
			reducer := &fakeReducer{outcome: lifecycle.ReviewDeliverySent}
			svc := New(nil, st, WithLifecycleReducer(reducer))

			if _, err := svc.Submit(context.Background(), "mer-1", "run-1", domain.VerdictChangesRequested, tt.body, tt.githubReviewID); !errors.Is(err, ErrInvalid) {
				t.Fatalf("err = %v, want ErrInvalid", err)
			}
			if st.updateCalls != 0 || st.markCalls != 0 || reducer.calls != 0 {
				t.Fatalf("mismatched retry should not rewrite or deliver: update=%d mark=%d reducer=%d", st.updateCalls, st.markCalls, reducer.calls)
			}
		})
	}
}

// --- close-on-submit ---
//
// The reviewer pane used to be kept warm between passes. It is not any more: a
// pass that submits is over, and a surviving pane drags its whole transcript into
// every later pass. These tests pin the new contract and its one guard rail — a
// multi-PR queue that has only submitted half its runs keeps its reviewer.

// The remaining reviewcore.Store surface, so one fake can back both the service
// and a real engine. Only the review-row methods matter here; the run methods
// above already do the work.
func (f *fakeStore) UpsertReview(_ context.Context, r domain.Review) error {
	cp := r
	f.review = &cp
	return nil
}

func (f *fakeStore) GetReviewBySession(context.Context, domain.SessionID) (domain.Review, bool, error) {
	if f.review == nil {
		return domain.Review{}, false, nil
	}
	return *f.review, true, nil
}

func (f *fakeStore) ListReviews(context.Context) ([]domain.Review, error) {
	if f.review == nil {
		return nil, nil
	}
	return []domain.Review{*f.review}, nil
}

func (f *fakeStore) InsertReviewRun(_ context.Context, r domain.ReviewRun) error {
	f.batchRuns = append(f.batchRuns, r)
	return nil
}

func (f *fakeStore) SupersedeReviewRun(_ context.Context, id, body string) (bool, error) {
	f.supersededBody = body
	if f.run.ID == id {
		f.run.Status = domain.ReviewRunFailed
		f.run.Body = body
	}
	return true, nil
}

func (f *fakeStore) SupersedeStaleRunningReviewRuns(context.Context, domain.SessionID, string, string, string) (int64, error) {
	return 0, nil
}

func (f *fakeStore) FailRunningReviewRunsBySession(context.Context, domain.SessionID, string) (int64, error) {
	return 0, nil
}

func (f *fakeStore) GetReviewRunBySessionPRAndSHA(context.Context, domain.SessionID, string, string) (domain.ReviewRun, bool, error) {
	return domain.ReviewRun{}, false, nil
}

func (f *fakeStore) ListSessionIDsWithRunningReviewRuns(context.Context) ([]domain.SessionID, error) {
	return nil, nil
}

func (f *fakeStore) ListReviewRunsBySession(context.Context, domain.SessionID) ([]domain.ReviewRun, error) {
	if f.batchRuns != nil {
		return append([]domain.ReviewRun(nil), f.batchRuns...), nil
	}
	if f.ok {
		return []domain.ReviewRun{f.run}, nil
	}
	return nil, nil
}

type fakeSessions struct{}

func (fakeSessions) GetSession(_ context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	return domain.SessionRecord{ID: id, ProjectID: "mer", Metadata: domain.SessionMetadata{WorkspacePath: "/ws/" + string(id)}}, true, nil
}

type fakeProjects struct{}

func (fakeProjects) GetProject(_ context.Context, id string) (domain.ProjectRecord, bool, error) {
	return domain.ProjectRecord{ID: id}, true, nil
}

type fakeLauncher struct{ teardowns []domain.SessionID }

func (f *fakeLauncher) Spawn(context.Context, reviewcore.LaunchSpec) (string, error) {
	return "review-mer-1", nil
}
func (f *fakeLauncher) Notify(context.Context, string, reviewcore.LaunchSpec) error { return nil }
func (f *fakeLauncher) Alive(context.Context, string) (bool, error)                 { return true, nil }
func (f *fakeLauncher) Teardown(_ context.Context, id domain.SessionID) error {
	f.teardowns = append(f.teardowns, id)
	return nil
}

func newEngineForService(st *fakeStore, launcher reviewcore.Launcher) *reviewcore.Engine {
	return reviewcore.New(reviewcore.Deps{
		Store: st, Sessions: fakeSessions{}, PRs: st, Projects: fakeProjects{}, Launcher: launcher,
	})
}

func TestSubmitClosesTheReviewerPaneWhenNothingIsStillRunning(t *testing.T) {
	st := &fakeStore{
		ok:     true,
		review: &domain.Review{ID: "rev-1", SessionID: "mer-1", ReviewerHandleID: "review-mer-1"},
		run:    domain.ReviewRun{ID: "run-1", SessionID: "mer-1", PRURL: "pr1", TargetSHA: "sha1", Status: domain.ReviewRunRunning},
		batchRuns: []domain.ReviewRun{
			{ID: "run-1", SessionID: "mer-1", PRURL: "pr1", TargetSHA: "sha1", Status: domain.ReviewRunRunning},
		},
		prs: []domain.PullRequest{{URL: "pr1", HeadSHA: "sha1"}},
	}
	launcher := &fakeLauncher{}
	svc := New(newEngineForService(st, launcher), st, WithLifecycleReducer(&fakeReducer{outcome: lifecycle.ReviewDeliverySent}))

	if _, err := svc.Submit(context.Background(), "mer-1", "run-1", domain.VerdictApproved, "looks good", ""); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if len(launcher.teardowns) != 1 || launcher.teardowns[0] != "mer-1" {
		t.Fatalf("expected the reviewer pane to be closed on submit, teardowns = %+v", launcher.teardowns)
	}
	if st.review.ReviewerHandleID != "" {
		t.Fatalf("the closed pane's handle should be forgotten, got %q", st.review.ReviewerHandleID)
	}
}

// One reviewer serves a whole multi-PR queue and is told to review every task
// before submitting. Closing on the first submitted run would decapitate the rest.
func TestSubmitKeepsTheReviewerPaneWhileAnotherRunIsStillRunning(t *testing.T) {
	st := &fakeStore{
		ok:     true,
		review: &domain.Review{ID: "rev-1", SessionID: "mer-1", ReviewerHandleID: "review-mer-1"},
		batchRuns: []domain.ReviewRun{
			{ID: "run-1", SessionID: "mer-1", BatchID: "batch-1", PRURL: "pr1", TargetSHA: "sha1", Status: domain.ReviewRunRunning},
			{ID: "run-2", SessionID: "mer-1", BatchID: "batch-1", PRURL: "pr2", TargetSHA: "sha2", Status: domain.ReviewRunRunning},
		},
		prs: []domain.PullRequest{{URL: "pr1", HeadSHA: "sha1"}, {URL: "pr2", HeadSHA: "sha2"}},
	}
	launcher := &fakeLauncher{}
	svc := New(newEngineForService(st, launcher), st, WithLifecycleReducer(&fakeReducer{outcome: lifecycle.ReviewDeliverySent}))

	if _, err := svc.Submit(context.Background(), "mer-1", "run-1", domain.VerdictApproved, "", ""); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if len(launcher.teardowns) != 0 {
		t.Fatalf("run-2 is still running; the pane must survive: %+v", launcher.teardowns)
	}

	// Once the last run submits, the pane goes.
	if _, err := svc.Submit(context.Background(), "mer-1", "run-2", domain.VerdictApproved, "", ""); err != nil {
		t.Fatalf("second Submit: %v", err)
	}
	if len(launcher.teardowns) != 1 {
		t.Fatalf("expected the pane to close once the queue is done: %+v", launcher.teardowns)
	}
}

// Preservation: a pre-MR changes_requested verdict still reaches the worker. It
// has no PR row to compare heads against, so the head guard that protects the
// post-MR path must not be applied to it — otherwise every pre-MR verdict is
// silently dropped.
func TestSubmitDeliversAPreMRChangesRequestedVerdict(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	st := &fakeStore{
		ok:     true,
		review: &domain.Review{ID: "rev-1", SessionID: "mer-1", ReviewerHandleID: "review-mer-1"},
		batchRuns: []domain.ReviewRun{
			{ID: "run-1", SessionID: "mer-1", BatchID: "batch-1", PRURL: "", TargetSHA: "sha1", Status: domain.ReviewRunRunning},
		},
	}
	reducer := &fakeReducer{outcome: lifecycle.ReviewDeliverySent}
	launcher := &fakeLauncher{}
	svc := New(newEngineForService(st, launcher), st, WithLifecycleReducer(reducer), WithClock(func() time.Time { return now }))

	run, err := svc.Submit(context.Background(), "mer-1", "run-1", domain.VerdictChangesRequested, "fix the null check", "")
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if reducer.batchCalls != 1 || len(reducer.gotBatch) != 1 || reducer.gotBatch[0].PRURL != "" {
		t.Fatalf("pre-MR changes_requested should be delivered: batchCalls=%d got=%+v", reducer.batchCalls, reducer.gotBatch)
	}
	if run.Status != domain.ReviewRunDelivered {
		t.Fatalf("run = %+v, want delivered", run)
	}
}

// A REVIEW THE TREE MOVED UNDER IS NOT A VERDICT.
//
// The reviewer reads a checkout both crew members are writing. It used to refuse
// to start while anybody was awake in that tree; with both awake continuously
// that refusal would fire every time and review would never run. So a pass over a
// SHARED checkout is bracketed with the tree-write detector, and one it cannot
// vouch for is superseded - no verdict at all - rather than recorded.
//
// This is the SERVICE half of that: the submit path has to ask, and act on the
// answer. The detector's own arithmetic is proved in internal/review.
func TestSubmit_DiscardsAPassNothingWatched(t *testing.T) {
	ctx := context.Background()
	st := &fakeStore{
		run: domain.ReviewRun{ID: "run-1", SessionID: "mer-1", Status: domain.ReviewRunRunning, PRURL: "https://github.com/o/r/pull/1", TargetSHA: "sha1"},
		ok:  true,
	}
	// A CREW worker (its tree has a second writer) on a daemon that has a
	// detector, and a run that carries no lease - which is what a review that
	// spanned a daemon restart looks like.
	engine := reviewcore.New(reviewcore.Deps{
		Store:    st,
		Sessions: crewSessions{},
		PRs:      noPRs{},
		Projects: noProjects{},
		Launcher: noLauncher{},
		Watcher:  neverWatches{},
	})
	svc := New(engine, st)

	run, err := svc.Submit(ctx, "mer-1", "run-1", domain.VerdictApproved, "looks good", "")
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if run.Verdict != domain.VerdictNone {
		t.Fatalf("verdict = %q, want none: a pass nothing watched must not certify anything", run.Verdict)
	}
	if st.updateCalls != 0 {
		t.Fatalf("the verdict was recorded anyway (%d updates)", st.updateCalls)
	}
	if !strings.Contains(st.supersededBody, "Discarded") {
		t.Fatalf("the discarded run does not say why: %q", st.supersededBody)
	}
}

// And the preservation half: a SOLO worker is never bracketed, because its tree
// has one writer and that writer is the session under review. Its verdict is
// recorded exactly as it always was, on the same daemon, with the same detector
// wired.
func TestSubmit_ASoloWorkersVerdictIsRecordedAsItAlwaysWas(t *testing.T) {
	ctx := context.Background()
	st := &fakeStore{
		run: domain.ReviewRun{ID: "run-1", SessionID: "mer-1", Status: domain.ReviewRunRunning, PRURL: "https://github.com/o/r/pull/1", TargetSHA: "sha1"},
		ok:  true,
	}
	engine := reviewcore.New(reviewcore.Deps{
		Store: st, Sessions: soloSessions{}, PRs: noPRs{}, Projects: noProjects{},
		Launcher: noLauncher{}, Watcher: neverWatches{},
	})
	svc := New(engine, st)

	run, err := svc.Submit(ctx, "mer-1", "run-1", domain.VerdictApproved, "looks good", "")
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if run.Verdict != domain.VerdictApproved || st.updateCalls != 1 {
		t.Fatalf("a solo review = verdict %q after %d updates, want approved after 1", run.Verdict, st.updateCalls)
	}
}

// noLauncher stands in for the reviewer runtime: nothing here launches a pane,
// and the close-on-submit sweep must not need one to be able to run.
type noLauncher struct{}

func (noLauncher) Spawn(context.Context, reviewcore.LaunchSpec) (string, error) { return "", nil }
func (noLauncher) Notify(context.Context, string, reviewcore.LaunchSpec) error  { return nil }
func (noLauncher) Alive(context.Context, string) (bool, error)                  { return false, nil }
func (noLauncher) Teardown(context.Context, domain.SessionID) error             { return nil }

type crewSessions struct{}

func (crewSessions) GetSession(context.Context, domain.SessionID) (domain.SessionRecord, bool, error) {
	return domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", CrewID: "mer-1", CrewRole: domain.CrewRoleDev,
		Metadata: domain.SessionMetadata{WorkspacePath: "/ws/mer-1"},
	}, true, nil
}

type soloSessions struct{}

func (soloSessions) GetSession(context.Context, domain.SessionID) (domain.SessionRecord, bool, error) {
	return domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer",
		Metadata: domain.SessionMetadata{WorkspacePath: "/ws/mer-1"},
	}, true, nil
}

type noPRs struct{}

func (noPRs) ListPRsBySession(context.Context, domain.SessionID) ([]domain.PullRequest, error) {
	return nil, nil
}

type noProjects struct{}

func (noProjects) GetProject(context.Context, string) (domain.ProjectRecord, bool, error) {
	return domain.ProjectRecord{}, false, nil
}

// neverWatches is a detector that is present but cannot attach - the honest
// stand-in for a run whose lease is gone.
type neverWatches struct{}

func (neverWatches) Attach(context.Context, string) (*treewatch.Lease, error) {
	return nil, errors.New("no watcher in this test")
}
