package session

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
)

type fakeTelemetrySink struct{ events []ports.TelemetryEvent }

func (f *fakeTelemetrySink) Emit(_ context.Context, ev ports.TelemetryEvent) {
	f.events = append(f.events, ev)
}
func (f *fakeTelemetrySink) Close(context.Context) error { return nil }

type fakeStore struct {
	queued        map[domain.SessionID]domain.QueuedMessageCounts
	getSessionErr error
	sessions      map[domain.SessionID]domain.SessionRecord
	pr            map[domain.SessionID]domain.PRFacts
	projects      map[string]domain.ProjectRecord
	checks        map[string][]domain.PullRequestCheck
	reviews       map[string][]domain.PullRequestReview
	threads       map[string][]domain.PullRequestReviewThread
	comments      map[string][]domain.PullRequestComment
	reviewRuns    map[domain.SessionID][]domain.ReviewRun
	prList        map[domain.SessionID][]domain.PullRequest
	openRuns      map[domain.SessionID]domain.CrewRun
	runDiscards   map[domain.SessionID]int
	num           int
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		sessions:   map[domain.SessionID]domain.SessionRecord{},
		pr:         map[domain.SessionID]domain.PRFacts{},
		projects:   map[string]domain.ProjectRecord{},
		checks:     map[string][]domain.PullRequestCheck{},
		reviews:    map[string][]domain.PullRequestReview{},
		threads:    map[string][]domain.PullRequestReviewThread{},
		comments:   map[string][]domain.PullRequestComment{},
		reviewRuns: map[domain.SessionID][]domain.ReviewRun{},
		prList:     map[domain.SessionID][]domain.PullRequest{},
	}
}

func (f *fakeStore) CreateSession(_ context.Context, rec domain.SessionRecord) (domain.SessionRecord, error) {
	f.num++
	rec.ID = domain.SessionID(fmt.Sprintf("%s-%d", rec.ProjectID, f.num))
	f.sessions[rec.ID] = rec
	return rec, nil
}

func (f *fakeStore) GetSession(_ context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	if f.getSessionErr != nil {
		return domain.SessionRecord{}, false, f.getSessionErr
	}
	r, ok := f.sessions[id]
	return r, ok, nil
}

func (f *fakeStore) ListSessions(_ context.Context, p domain.ProjectID) ([]domain.SessionRecord, error) {
	var out []domain.SessionRecord
	for _, r := range f.sessions {
		if r.ProjectID == p {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *fakeStore) ListAllSessions(_ context.Context) ([]domain.SessionRecord, error) {
	out := make([]domain.SessionRecord, 0, len(f.sessions))
	for _, r := range f.sessions {
		out = append(out, r)
	}
	return out, nil
}

func (f *fakeStore) RenameSession(_ context.Context, id domain.SessionID, displayName string, updatedAt time.Time) (bool, error) {
	r, ok := f.sessions[id]
	if !ok {
		return false, nil
	}
	r.DisplayName = displayName
	r.UpdatedAt = updatedAt
	f.sessions[id] = r
	return true, nil
}

func (f *fakeStore) SetSessionPreviewURL(_ context.Context, id domain.SessionID, previewURL string, updatedAt time.Time) (bool, error) {
	r, ok := f.sessions[id]
	if !ok {
		return false, nil
	}
	r.Metadata.PreviewURL = previewURL
	r.UpdatedAt = updatedAt
	f.sessions[id] = r
	return true, nil
}

func (f *fakeStore) SetSessionAutoNudge(_ context.Context, id domain.SessionID, override *bool, updatedAt time.Time) (bool, error) {
	r, ok := f.sessions[id]
	if !ok {
		return false, nil
	}
	r.AutoNudgeComments = override
	r.UpdatedAt = updatedAt
	f.sessions[id] = r
	return true, nil
}

func (f *fakeStore) SetSessionAutoResolve(_ context.Context, id domain.SessionID, override *bool, updatedAt time.Time) (bool, error) {
	r, ok := f.sessions[id]
	if !ok {
		return false, nil
	}
	r.AutoResolveOnReply = override
	r.UpdatedAt = updatedAt
	f.sessions[id] = r
	return true, nil
}

func (f *fakeStore) SetSessionPRTarget(_ context.Context, id domain.SessionID, target string, updatedAt time.Time) (bool, error) {
	rec, ok := f.sessions[id]
	if !ok {
		return false, nil
	}
	rec.PRTarget = target
	rec.UpdatedAt = updatedAt
	f.sessions[id] = rec
	return true, nil
}

func (f *fakeStore) SetPRTargetBranch(_ context.Context, prURL, target string, updatedAt time.Time) (bool, error) {
	for id, pr := range f.pr {
		if pr.URL == prURL {
			pr.TargetBranch = target
			pr.UpdatedAt = updatedAt
			f.pr[id] = pr
			return true, nil
		}
	}
	return false, nil
}

func (f *fakeStore) SetSessionKeepWarmOnMerge(_ context.Context, id domain.SessionID, enabled bool, updatedAt time.Time) (bool, error) {
	r, ok := f.sessions[id]
	if !ok {
		return false, nil
	}
	r.KeepWarmOnMerge = enabled
	r.UpdatedAt = updatedAt
	f.sessions[id] = r
	return true, nil
}

func (f *fakeStore) SetSessionIssueBinding(_ context.Context, id domain.SessionID, issueID, displayName string, updatedAt time.Time) (bool, error) {
	r, ok := f.sessions[id]
	if !ok {
		return false, nil
	}
	r.IssueID = domain.IssueID(issueID)
	r.DisplayName = displayName
	r.UpdatedAt = updatedAt
	f.sessions[id] = r
	return true, nil
}

func (f *fakeStore) GetDisplayPRFactsForSession(_ context.Context, id domain.SessionID) (domain.PRFacts, bool, error) {
	pr, ok := f.pr[id]
	return pr, ok, nil
}

func (f *fakeStore) ListPRsBySession(_ context.Context, id domain.SessionID) ([]domain.PullRequest, error) {
	// prList lets a test supply full PullRequest rows (head SHA and all); without
	// it the list is projected from the PRFacts the other tests already seed.
	if rows, ok := f.prList[id]; ok {
		return append([]domain.PullRequest(nil), rows...), nil
	}
	pr, ok := f.pr[id]
	if !ok {
		return nil, nil
	}
	return []domain.PullRequest{{URL: pr.URL, SessionID: id, Number: pr.Number, Draft: pr.Draft, Merged: pr.Merged, Closed: pr.Closed, CI: pr.CI, Review: pr.Review, Mergeability: pr.Mergeability, SourceBranch: pr.SourceBranch, TargetBranch: pr.TargetBranch, UpdatedAt: pr.UpdatedAt}}, nil
}

func (f *fakeStore) SessionQueuedMessageCounts(_ context.Context, id domain.SessionID) (domain.QueuedMessageCounts, error) {
	return f.queued[id], nil
}

func (f *fakeStore) OpenCrewRunForSession(_ context.Context, id domain.SessionID) (domain.CrewRun, bool, error) {
	run, ok := f.openRuns[id]
	return run, ok, nil
}

func (f *fakeStore) ConsecutiveCrewRunDiscards(_ context.Context, id domain.SessionID) (int, error) {
	return f.runDiscards[id], nil
}

func (f *fakeStore) ListPRFactsForSession(_ context.Context, id domain.SessionID) ([]domain.PRFacts, error) {
	pr, ok := f.pr[id]
	if !ok {
		return nil, nil
	}
	return []domain.PRFacts{pr}, nil
}

func (f *fakeStore) ListChecks(_ context.Context, prURL string) ([]domain.PullRequestCheck, error) {
	return append([]domain.PullRequestCheck(nil), f.checks[prURL]...), nil
}

func (f *fakeStore) ListPRReviews(_ context.Context, prURL string) ([]domain.PullRequestReview, error) {
	return append([]domain.PullRequestReview(nil), f.reviews[prURL]...), nil
}

func (f *fakeStore) ListPRReviewThreads(_ context.Context, prURL string) ([]domain.PullRequestReviewThread, error) {
	return append([]domain.PullRequestReviewThread(nil), f.threads[prURL]...), nil
}

func (f *fakeStore) ListPRComments(_ context.Context, prURL string) ([]domain.PullRequestComment, error) {
	return append([]domain.PullRequestComment(nil), f.comments[prURL]...), nil
}

func (f *fakeStore) ListReviewRunsBySession(_ context.Context, id domain.SessionID) ([]domain.ReviewRun, error) {
	return append([]domain.ReviewRun(nil), f.reviewRuns[id]...), nil
}

func (f *fakeStore) GetProject(_ context.Context, id string) (domain.ProjectRecord, bool, error) {
	p, ok := f.projects[id]
	return p, ok, nil
}

func TestSessionListDerivesStatusFromPRFacts(t *testing.T) {
	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Activity: domain.Activity{State: domain.ActivityActive}}
	st.pr["mer-1"] = domain.PRFacts{URL: "pr1", CI: domain.CIFailing}

	list, err := (&Service{store: st}).List(context.Background(), ListFilter{ProjectID: "mer"})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Status != domain.StatusCIFailed {
		t.Fatalf("got %+v", list)
	}
}

func TestSessionRenameUpdatesDisplayName(t *testing.T) {
	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer"}

	err := (&Service{store: st}).Rename(context.Background(), "mer-1", "  Fix issue #90  ")
	if err != nil {
		t.Fatal(err)
	}
	if got := st.sessions["mer-1"].DisplayName; got != "Fix issue #90" {
		t.Fatalf("display name = %q, want trimmed rename", got)
	}
}

func TestSessionSetIssueBindingUpdatesIssueAndDisplayName(t *testing.T) {
	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", IssueID: "old title"}

	sess, err := (&Service{store: st}).SetIssueBinding(context.Background(), "mer-1", "jira:DEMO-2272", "Example issue summary")
	if err != nil {
		t.Fatal(err)
	}
	if sess.IssueID != "jira:DEMO-2272" || sess.DisplayName != "Example issue summary" {
		t.Fatalf("returned session = %+v, want the new binding", sess)
	}
	if got := st.sessions["mer-1"]; got.IssueID != "jira:DEMO-2272" || got.DisplayName != "Example issue summary" {
		t.Fatalf("persisted = %+v, want the new binding", got)
	}
}

func TestSessionSetIssueBindingUnknownSessionIs404(t *testing.T) {
	st := newFakeStore()
	if _, err := (&Service{store: st}).SetIssueBinding(context.Background(), "ghost-1", "jira:DEMO-1", "x"); err == nil {
		t.Fatal("expected an error for an unknown session")
	}
}

func TestSessionSetPreviewPersistsURL(t *testing.T) {
	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer"}

	sess, err := (&Service{store: st, clock: time.Now}).SetPreview(context.Background(), "mer-1", "file:///tmp/index.html")
	if err != nil {
		t.Fatal(err)
	}
	if sess.Metadata.PreviewURL != "file:///tmp/index.html" {
		t.Fatalf("returned preview url = %q, want set value", sess.Metadata.PreviewURL)
	}
	if got := st.sessions["mer-1"].Metadata.PreviewURL; got != "file:///tmp/index.html" {
		t.Fatalf("persisted preview url = %q, want set value", got)
	}
}

func TestSessionSetPreviewUnknownSession(t *testing.T) {
	st := newFakeStore()
	if _, err := (&Service{store: st}).SetPreview(context.Background(), "ghost-1", "http://x"); err == nil {
		t.Fatal("want error for unknown session")
	}
}

func TestSessionSetAutoNudgePersistsOverride(t *testing.T) {
	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer"}

	override := true
	sess, err := (&Service{store: st, clock: time.Now}).SetAutoNudge(context.Background(), "mer-1", &override)
	if err != nil {
		t.Fatal(err)
	}
	if sess.AutoNudgeComments == nil || *sess.AutoNudgeComments != true {
		t.Fatalf("returned auto-nudge override = %v, want true", sess.AutoNudgeComments)
	}
	if got := st.sessions["mer-1"].AutoNudgeComments; got == nil || *got != true {
		t.Fatalf("persisted auto-nudge override = %v, want true", got)
	}
}

func TestSessionSetAutoNudgeUnknownSession(t *testing.T) {
	st := newFakeStore()
	override := true
	if _, err := (&Service{store: st}).SetAutoNudge(context.Background(), "ghost-1", &override); err == nil {
		t.Fatal("want error for unknown session")
	}
}

func TestSessionSetAutoResolvePersistsOverride(t *testing.T) {
	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer"}

	override := true
	sess, err := (&Service{store: st, clock: time.Now}).SetAutoResolve(context.Background(), "mer-1", &override)
	if err != nil {
		t.Fatal(err)
	}
	if sess.AutoResolveOnReply == nil || *sess.AutoResolveOnReply != true {
		t.Fatalf("returned auto-resolve gate = %v, want true", sess.AutoResolveOnReply)
	}
	if got := st.sessions["mer-1"].AutoResolveOnReply; got == nil || *got != true {
		t.Fatalf("persisted auto-resolve gate = %v, want true", got)
	}
}

func TestSessionSetAutoResolveUnknownSession(t *testing.T) {
	st := newFakeStore()
	override := true
	if _, err := (&Service{store: st}).SetAutoResolve(context.Background(), "ghost-1", &override); err == nil {
		t.Fatal("want error for unknown session")
	}
}

func TestSessionRenameMissingSessionReturnsNotFound(t *testing.T) {
	st := newFakeStore()

	err := (&Service{store: st}).Rename(context.Background(), "mer-404", "Missing")
	var e *apierr.Error
	if !errors.As(err, &e) || e.Kind != apierr.KindNotFound || e.Code != "SESSION_NOT_FOUND" {
		t.Fatalf("err = %v, want apierr NotFound SESSION_NOT_FOUND", err)
	}
}

// fakeCommander records Kill/Spawn calls so a test can assert the
// clean-orchestrator ordering without wiring a real session engine.
type fakeCommander struct {
	sendOutcome     ports.SendOutcome
	teardownCauses  []string
	killed          []domain.SessionID
	restarted       []domain.SessionID
	retired         []domain.SessionID
	sent            []domain.SessionID
	lastMessage     string
	cleanupProjects []domain.ProjectID
	purged          []domain.SessionID
	purgedForce     []bool
	killErr         error
	teardownResult  *sessionmanager.TeardownResult
	restartErr      error
	retireErr       error
	sendErr         error
	cleanupErr      error
	spawnErr        error
	purgeErr        error
	spawnRecord     domain.SessionRecord
	restartRecord   domain.SessionRecord
	woken           []domain.SessionID
	crewWoken       []domain.SessionID
	crewWakeErr     error
	crewAttached    []domain.SessionID
	crewDevOf       map[domain.SessionID]domain.SessionRecord
	wakeRecord      domain.SessionRecord
	spawned         bool
	killsAtSpawn    int
	preparedTodo    bool
	startedTodo     []domain.SessionID
	updatedTodo     []domain.SessionID
}

func (f *fakeCommander) Spawn(_ context.Context, cfg ports.SpawnConfig) (domain.SessionRecord, error) {
	if f.spawnErr != nil {
		return domain.SessionRecord{}, f.spawnErr
	}
	f.spawned = true
	f.killsAtSpawn = len(f.retired)
	if f.spawnRecord.ID != "" {
		return f.spawnRecord, nil
	}
	return domain.SessionRecord{ID: "mer-9", ProjectID: cfg.ProjectID, Kind: cfg.Kind, Harness: cfg.Harness}, nil
}
func (f *fakeCommander) PrepareTodo(_ context.Context, cfg ports.SpawnConfig) (domain.SessionRecord, error) {
	if f.spawnErr != nil {
		return domain.SessionRecord{}, f.spawnErr
	}
	f.preparedTodo = true
	return domain.SessionRecord{ID: "mer-todo", ProjectID: cfg.ProjectID, Kind: cfg.Kind, Harness: cfg.Harness, IsTodo: true, BaseBranch: cfg.BaseBranch, PRTarget: cfg.PRTarget, CreatedBy: cfg.CreatedBy, Metadata: domain.SessionMetadata{Branch: cfg.Branch, Prompt: cfg.Prompt}}, nil
}
func (f *fakeCommander) StartTodo(_ context.Context, id domain.SessionID) (domain.SessionRecord, error) {
	if f.spawnErr != nil {
		return domain.SessionRecord{}, f.spawnErr
	}
	f.startedTodo = append(f.startedTodo, id)
	return domain.SessionRecord{ID: id, ProjectID: "mer", Kind: domain.KindWorker}, nil
}
func (f *fakeCommander) UpdateTodoSpec(_ context.Context, id domain.SessionID, patch ports.TodoSpecPatch) (domain.SessionRecord, error) {
	f.updatedTodo = append(f.updatedTodo, id)
	rec := domain.SessionRecord{ID: id, ProjectID: "mer", IsTodo: true}
	if patch.DisplayName != nil {
		rec.DisplayName = *patch.DisplayName
	}
	return rec, nil
}
func (f *fakeCommander) Restore(context.Context, domain.SessionID) (domain.SessionRecord, error) {
	return domain.SessionRecord{}, nil
}
func (f *fakeCommander) Wake(_ context.Context, id domain.SessionID) (domain.SessionRecord, error) {
	f.woken = append(f.woken, id)
	return f.wakeRecord, nil
}

// crewDevOf lets a test say "this id belongs to that task", which is the
// resolution the attach route does before it decides anything.
func (f *fakeCommander) CrewDevOf(_ context.Context, id domain.SessionID) (domain.SessionRecord, error) {
	if dev, ok := f.crewDevOf[id]; ok {
		return dev, nil
	}
	return domain.SessionRecord{ID: id, ProjectID: "mer", Kind: domain.KindWorker}, nil
}

func (f *fakeCommander) AttachCrewMember(_ context.Context, devID domain.SessionID, role domain.CrewRole) (domain.SessionRecord, error) {
	f.crewAttached = append(f.crewAttached, devID)
	rec := domain.SessionRecord{ID: devID + "-qa", ProjectID: "mer", Kind: domain.KindWorker, IsSuspended: true}
	rec.CrewID = devID
	rec.CrewRole = role
	return rec, nil
}

func (f *fakeCommander) WakeCrewMember(_ context.Context, id domain.SessionID) (domain.SessionRecord, error) {
	f.crewWoken = append(f.crewWoken, id)
	return f.wakeRecord, f.crewWakeErr
}
func (f *fakeCommander) Kill(ctx context.Context, id domain.SessionID) (bool, error) {
	res, err := f.Teardown(ctx, id, domain.TerminationCauseKill)
	return res.Freed, err
}

// teardownResult, when set, is what Teardown reports instead of a clean free.
// It is how a test stands in a dirty worktree that refuses removal.
func (f *fakeCommander) Teardown(_ context.Context, id domain.SessionID, cause string) (sessionmanager.TeardownResult, error) {
	if f.killErr != nil {
		return sessionmanager.TeardownResult{}, f.killErr
	}
	f.killed = append(f.killed, id)
	f.teardownCauses = append(f.teardownCauses, cause)
	if f.teardownResult != nil {
		return *f.teardownResult, nil
	}
	return sessionmanager.TeardownResult{Freed: true}, nil
}
func (f *fakeCommander) Restart(_ context.Context, id domain.SessionID) (domain.SessionRecord, error) {
	if f.restartErr != nil {
		return domain.SessionRecord{}, f.restartErr
	}
	f.restarted = append(f.restarted, id)
	return f.restartRecord, nil
}
func (f *fakeCommander) RetireForReplacement(_ context.Context, id domain.SessionID) error {
	if f.retireErr != nil {
		return f.retireErr
	}
	f.retired = append(f.retired, id)
	return nil
}
func (f *fakeCommander) Send(_ context.Context, id domain.SessionID, message string) (ports.SendOutcome, error) {
	if f.sendErr != nil {
		return ports.SendOutcome{}, f.sendErr
	}
	f.sent = append(f.sent, id)
	f.lastMessage = message
	return f.sendOutcome, nil
}
func (f *fakeCommander) Cleanup(_ context.Context, project domain.ProjectID) (sessionmanager.CleanupResult, error) {
	f.cleanupProjects = append(f.cleanupProjects, project)
	if f.cleanupErr != nil {
		return sessionmanager.CleanupResult{}, f.cleanupErr
	}
	return sessionmanager.CleanupResult{
		Cleaned: []domain.SessionID{"mer-1"},
		Skipped: []sessionmanager.CleanupSkip{{SessionID: "mer-2", Reason: "workspace has uncommitted changes"}},
	}, nil
}
func (f *fakeCommander) RollbackSpawn(context.Context, domain.SessionID) (bool, bool, error) {
	return false, false, nil
}
func (f *fakeCommander) PurgeSession(_ context.Context, id domain.SessionID, force bool) error {
	if f.purgeErr != nil {
		return f.purgeErr
	}
	f.purged = append(f.purged, id)
	f.purgedForce = append(f.purgedForce, force)
	return nil
}

// TestCleanupMapsManagerResult: the service forwards both reclaimed and
// skipped sessions, with non-nil slices so the wire shape stays stable.
func TestCleanupMapsManagerResult(t *testing.T) {
	svc := &Service{manager: &fakeCommander{}}
	out, err := svc.Cleanup(context.Background(), "mer")
	if err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if len(out.Cleaned) != 1 || out.Cleaned[0] != "mer-1" {
		t.Fatalf("cleaned = %#v", out.Cleaned)
	}
	if len(out.Skipped) != 1 || out.Skipped[0].SessionID != "mer-2" || out.Skipped[0].Reason != "workspace has uncommitted changes" {
		t.Fatalf("skipped = %#v", out.Skipped)
	}
}

func TestTeardownProjectKillsActiveSessionsThenCleansProject(t *testing.T) {
	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer"}
	st.sessions["mer-2"] = domain.SessionRecord{ID: "mer-2", ProjectID: "mer", IsTerminated: true}
	st.sessions["other-1"] = domain.SessionRecord{ID: "other-1", ProjectID: "other"}
	fc := &fakeCommander{}
	svc := &Service{manager: fc, store: st}

	if err := svc.TeardownProject(context.Background(), "mer"); err != nil {
		t.Fatalf("TeardownProject: %v", err)
	}
	if len(fc.killed) != 1 || fc.killed[0] != "mer-1" {
		t.Fatalf("killed = %#v, want only mer-1", fc.killed)
	}
	if len(fc.cleanupProjects) != 1 || fc.cleanupProjects[0] != "mer" {
		t.Fatalf("cleanup projects = %#v, want [mer]", fc.cleanupProjects)
	}
}

func TestTeardownProjectStopsOnKillError(t *testing.T) {
	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer"}
	boom := errors.New("boom")
	fc := &fakeCommander{killErr: boom}
	svc := &Service{manager: fc, store: st}

	err := svc.TeardownProject(context.Background(), "mer")
	if !errors.Is(err, boom) {
		t.Fatalf("TeardownProject err = %v, want boom", err)
	}
	if len(fc.cleanupProjects) != 0 {
		t.Fatalf("cleanup projects = %#v, want none after kill failure", fc.cleanupProjects)
	}
}

func TestSpawnOrchestratorCleanRetiresActiveOrchestratorsBeforeSpawn(t *testing.T) {
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer"}
	// Two active orchestrators plus an unrelated worker and a terminated
	// orchestrator that must be left alone.
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindOrchestrator}
	st.sessions["mer-2"] = domain.SessionRecord{ID: "mer-2", ProjectID: "mer", Kind: domain.KindOrchestrator}
	st.sessions["mer-3"] = domain.SessionRecord{ID: "mer-3", ProjectID: "mer", Kind: domain.KindWorker}
	st.sessions["mer-4"] = domain.SessionRecord{ID: "mer-4", ProjectID: "mer", Kind: domain.KindOrchestrator, IsTerminated: true}

	fc := &fakeCommander{}
	svc := &Service{manager: fc, store: st}

	if _, err := svc.SpawnOrchestrator(context.Background(), "mer", true); err != nil {
		t.Fatalf("SpawnOrchestrator: %v", err)
	}

	if len(fc.retired) != 2 {
		t.Fatalf("retired = %v, want the two active orchestrators", fc.retired)
	}
	if len(fc.sent) != 2 {
		t.Fatalf("retire notices = %v, want the two active orchestrators", fc.sent)
	}
	if !fc.spawned || fc.killsAtSpawn != 2 {
		t.Fatalf("spawn must run after both retirements: spawned=%v retirementsAtSpawn=%d", fc.spawned, fc.killsAtSpawn)
	}
	if len(fc.killed) != 0 {
		t.Fatalf("interactive Kill must not be used for replacement: killed=%v", fc.killed)
	}
}

func TestSpawnOrchestratorCleanContinuesWhenRetireNoticeFails(t *testing.T) {
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer"}
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindOrchestrator}
	fc := &fakeCommander{sendErr: errors.New("pane closed")}
	svc := &Service{manager: fc, store: st}

	if _, err := svc.SpawnOrchestrator(context.Background(), "mer", true); err != nil {
		t.Fatalf("SpawnOrchestrator: %v", err)
	}
	if len(fc.retired) != 1 || fc.retired[0] != "mer-1" {
		t.Fatalf("retired = %v, want mer-1 despite retire notice failure", fc.retired)
	}
	if !fc.spawned {
		t.Fatal("replacement should still spawn when retire notice delivery fails")
	}
}

// TestSpawnUnknownProjectReturns404 covers Bug 1: an HTTP spawn for an
// unregistered projectId must surface PROJECT_NOT_FOUND (apierr.NotFound)
// BEFORE any session row is created, so no orphan terminated row is left
// behind under `--include-terminated`.
func TestSpawnUnknownProjectReturns404(t *testing.T) {
	st := newFakeStore()
	fc := &fakeCommander{}
	svc := &Service{manager: fc, store: st}

	_, err := svc.Spawn(context.Background(), ports.SpawnConfig{ProjectID: "ghost", Kind: domain.KindWorker})
	var e *apierr.Error
	if !errors.As(err, &e) || e.Kind != apierr.KindNotFound || e.Code != "PROJECT_NOT_FOUND" {
		t.Fatalf("err = %v, want apierr.NotFound PROJECT_NOT_FOUND", err)
	}
	if fc.spawned {
		t.Fatal("manager.Spawn must NOT be invoked for an unknown project")
	}
}

func TestSpawnEmitsFirstSessionOnboardingAndDuration(t *testing.T) {
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", RegisteredAt: time.Unix(100, 0).UTC()}
	sink := &fakeTelemetrySink{}
	fc := &fakeCommander{}
	svc := NewWithDeps(Deps{
		Manager:   fc,
		Store:     st,
		Telemetry: sink,
		Clock:     func() time.Time { return time.Unix(102, 0).UTC() },
	})

	if _, err := svc.Spawn(context.Background(), ports.SpawnConfig{ProjectID: "mer"}); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if len(sink.events) != 2 {
		t.Fatalf("events = %#v, want spawned + first_session", sink.events)
	}
	if sink.events[0].Name != "ao.session.spawned" || sink.events[1].Name != "ao.onboarding.first_session_spawned" {
		t.Fatalf("event names = %#v", []string{sink.events[0].Name, sink.events[1].Name})
	}
	if got := sink.events[0].Payload["duration_ms"]; got != int64(0) {
		t.Fatalf("spawn duration_ms = %#v, want 0 with fixed clock", got)
	}
	if got := sink.events[1].Payload["since_first_project_ms"]; got != int64(2000) {
		t.Fatalf("since_first_project_ms = %#v, want 2000", got)
	}
}

func TestSpawnFailedEmitsDuration(t *testing.T) {
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer"}
	sink := &fakeTelemetrySink{}
	fc := &fakeCommander{spawnErr: errors.New("boom")}
	now := time.Unix(200, 0).UTC()
	svc := NewWithDeps(Deps{
		Manager:   fc,
		Store:     st,
		Telemetry: sink,
		Clock: func() time.Time {
			v := now
			now = now.Add(1500 * time.Millisecond)
			return v
		},
	})

	if _, err := svc.Spawn(context.Background(), ports.SpawnConfig{ProjectID: "mer"}); err == nil {
		t.Fatal("Spawn should fail")
	}
	if len(sink.events) != 1 || sink.events[0].Name != "ao.session.spawn_failed" {
		t.Fatalf("events = %#v, want one spawn_failed", sink.events)
	}
	if got := sink.events[0].Payload["duration_ms"]; got != int64(1500) {
		t.Fatalf("spawn_failed duration_ms = %#v, want 1500", got)
	}
	if got := sink.events[0].Payload["error_kind"]; got != "internal" {
		t.Fatalf("spawn_failed error_kind = %#v, want internal", got)
	}
	if got := sink.events[0].Payload["component"]; got != "session_service" {
		t.Fatalf("spawn_failed component = %#v, want session_service", got)
	}
	if got := sink.events[0].Payload["operation"]; got != "spawn_session" {
		t.Fatalf("spawn_failed operation = %#v, want spawn_session", got)
	}
	if got := sink.events[0].Payload["fingerprint"]; got == "" {
		t.Fatalf("spawn_failed fingerprint = %#v, want non-empty", got)
	}
}

func TestSpawnEmitsTelemetryOnSuccess(t *testing.T) {
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer"}
	st.sessions["old-1"] = domain.SessionRecord{ID: "old-1", ProjectID: "other"}
	fc := &fakeCommander{}
	ts := &fakeTelemetrySink{}
	svc := NewWithDeps(Deps{Manager: fc, Store: st, Telemetry: ts, Clock: func() time.Time { return time.Unix(1700000000, 0).UTC() }})

	_, err := svc.Spawn(context.Background(), ports.SpawnConfig{
		ProjectID: "mer",
		Kind:      domain.KindWorker,
		Harness:   domain.HarnessCodex,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if len(ts.events) != 1 {
		t.Fatalf("telemetry events = %d, want 1", len(ts.events))
	}
	ev := ts.events[0]
	if ev.Name != "ao.session.spawned" || ev.Source != "session_service" {
		t.Fatalf("event = %+v", ev)
	}
	if ev.ProjectID == nil || *ev.ProjectID != "mer" || ev.SessionID == nil || *ev.SessionID != "mer-9" {
		t.Fatalf("event ids = %+v", ev)
	}
}

func TestSpawnEmitsTelemetryOnFailure(t *testing.T) {
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer"}
	fc := &fakeCommander{spawnErr: errors.New("boom")}
	ts := &fakeTelemetrySink{}
	svc := NewWithDeps(Deps{Manager: fc, Store: st, Telemetry: ts, Clock: func() time.Time { return time.Unix(1700000000, 0).UTC() }})

	_, err := svc.Spawn(context.Background(), ports.SpawnConfig{
		ProjectID: "mer",
		Kind:      domain.KindWorker,
		Harness:   domain.HarnessCodex,
	})
	if err == nil {
		t.Fatal("Spawn error = nil, want failure")
	}
	if len(ts.events) != 1 {
		t.Fatalf("telemetry events = %d, want 1", len(ts.events))
	}
	ev := ts.events[0]
	if ev.Name != "ao.session.spawn_failed" || ev.Source != "session_service" || ev.Level != ports.TelemetryLevelError {
		t.Fatalf("event = %+v", ev)
	}
	if ev.ProjectID == nil || *ev.ProjectID != "mer" || ev.SessionID != nil {
		t.Fatalf("event ids = %+v", ev)
	}
	if got := ev.Payload["error_kind"]; got != "internal" {
		t.Fatalf("event payload error_kind = %#v, want internal", got)
	}
	if got := ev.Payload["component"]; got != "session_service" {
		t.Fatalf("event payload component = %#v, want session_service", got)
	}
	if got := ev.Payload["operation"]; got != "spawn_session" {
		t.Fatalf("event payload operation = %#v, want spawn_session", got)
	}
	if got := ev.Payload["fingerprint"]; got == "" {
		t.Fatalf("event payload fingerprint = %#v, want non-empty", got)
	}
	if _, ok := ev.Payload["error"]; ok {
		t.Fatalf("event payload leaked raw error: %+v", ev.Payload)
	}
}

func TestSpawnEmitsTypedErrorCodeOnFailure(t *testing.T) {
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer"}
	fc := &fakeCommander{spawnErr: fmt.Errorf("spawn: %w: %q", sessionmanager.ErrUnknownHarness, "bogus")}
	ts := &fakeTelemetrySink{}
	svc := NewWithDeps(Deps{Manager: fc, Store: st, Telemetry: ts, Clock: func() time.Time { return time.Unix(1700000000, 0).UTC() }})

	_, err := svc.Spawn(context.Background(), ports.SpawnConfig{
		ProjectID: "mer",
		Kind:      domain.KindWorker,
		Harness:   domain.HarnessCodex,
	})
	if err == nil {
		t.Fatal("Spawn error = nil, want failure")
	}
	if len(ts.events) != 1 {
		t.Fatalf("telemetry events = %d, want 1", len(ts.events))
	}
	ev := ts.events[0]
	if got := ev.Payload["error_kind"]; got != "invalid" {
		t.Fatalf("event payload error_kind = %#v, want invalid", got)
	}
	if got := ev.Payload["error_code"]; got != "UNKNOWN_HARNESS" {
		t.Fatalf("event payload error_code = %#v, want UNKNOWN_HARNESS", got)
	}
}

// TestSpawnOrchestratorUnknownProjectReturns404 is the orchestrator-side guard
// for Bug 1: same pre-validation, same typed envelope.
func TestSpawnOrchestratorUnknownProjectReturns404(t *testing.T) {
	st := newFakeStore()
	fc := &fakeCommander{}
	svc := &Service{manager: fc, store: st}

	_, err := svc.SpawnOrchestrator(context.Background(), "ghost", false)
	var e *apierr.Error
	if !errors.As(err, &e) || e.Kind != apierr.KindNotFound || e.Code != "PROJECT_NOT_FOUND" {
		t.Fatalf("err = %v, want apierr.NotFound PROJECT_NOT_FOUND", err)
	}
	if fc.spawned {
		t.Fatal("manager.Spawn must NOT be invoked for an unknown project")
	}
}

// TestToAPIErrorMapsWorkspaceBranchSentinels covers Bug 3: the workspace
// adapter's typed branch errors map to typed envelope errors instead of
// collapsing to a 500.
func TestToAPIErrorMapsWorkspaceBranchSentinels(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		wantKind apierr.Kind
		wantCode string
	}{
		{"checked out elsewhere", fmt.Errorf("spawn mer-1: workspace: %w: \"x\" is checked out at \"/tmp\"", ports.ErrWorkspaceBranchCheckedOutElsewhere), apierr.KindConflict, "BRANCH_CHECKED_OUT_ELSEWHERE"},
		{"not fetched", fmt.Errorf("spawn mer-1: workspace: %w: \"x\" has no local head", ports.ErrWorkspaceBranchNotFetched), apierr.KindInvalid, "BRANCH_NOT_FETCHED"},
		{"invalid branch", fmt.Errorf("spawn mer-1: workspace: %w: \"bad!!\" (exit 1)", ports.ErrWorkspaceBranchInvalid), apierr.KindInvalid, "INVALID_BRANCH"},
		{"agent binary not found", fmt.Errorf("spawn mer-1: %w", ports.ErrAgentBinaryNotFound), apierr.KindInvalid, "AGENT_BINARY_NOT_FOUND"},
		{"runtime prerequisite missing", fmt.Errorf("spawn: %w: tmux required on macOS/Linux but not in PATH", ports.ErrRuntimePrerequisite), apierr.KindInvalid, "RUNTIME_PREREQUISITE_MISSING"},
		{"unknown harness", fmt.Errorf("spawn: %w: %q", sessionmanager.ErrUnknownHarness, "bogus"), apierr.KindInvalid, "UNKNOWN_HARNESS"},
		{"missing harness", fmt.Errorf("spawn: %w: configure project worker.agent or pass --harness", sessionmanager.ErrMissingHarness), apierr.KindInvalid, "AGENT_REQUIRED"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mapped := toAPIError(tc.err)
			var e *apierr.Error
			if !errors.As(mapped, &e) || e.Kind != tc.wantKind || e.Code != tc.wantCode {
				t.Fatalf("mapped = %v, want %s %s", mapped, tc.wantCode, e)
			}
		})
	}
}

// TestToAPIError_NotResumable asserts that ErrNotResumable (promptless worker
// with no adapter resume handle) maps to a Conflict with code SESSION_NOT_RESUMABLE.
func TestToAPIError_NotResumable(t *testing.T) {
	err := fmt.Errorf("restore mer-1: %w", sessionmanager.ErrNotResumable)
	mapped := toAPIError(err)
	var e *apierr.Error
	if !errors.As(mapped, &e) || e.Kind != apierr.KindConflict || e.Code != "SESSION_NOT_RESUMABLE" {
		t.Fatalf("mapped = %v, want Conflict SESSION_NOT_RESUMABLE", mapped)
	}
}

// TestSpawnOrchestratorNoCleanReturnsExistingWhenActiveExists is the RED test
// for the idempotency fix: when an active orchestrator already exists and
// clean=false, SpawnOrchestrator must return that orchestrator without minting
// a second one. Before the fix this test fails because a duplicate is spawned.
func TestSpawnOrchestratorNoCleanReturnsExistingWhenActiveExists(t *testing.T) {
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer"}
	// Pre-load an active orchestrator.
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindOrchestrator}

	fc := &fakeCommander{}
	svc := &Service{manager: fc, store: st}

	got, err := svc.SpawnOrchestrator(context.Background(), "mer", false)
	if err != nil {
		t.Fatalf("SpawnOrchestrator: %v", err)
	}
	// Must return the existing orchestrator, not a newly minted one.
	if got.ID != "mer-1" {
		t.Fatalf("returned id = %q, want existing orchestrator mer-1", got.ID)
	}
	// Must NOT have called manager.Spawn (no duplicate created).
	if fc.spawned {
		t.Fatal("manager.Spawn must NOT be called when an active orchestrator already exists")
	}
	// Must NOT have killed anything.
	if len(fc.killed) != 0 {
		t.Fatalf("no kills expected with clean=false, got %v", fc.killed)
	}
	// Exactly one session in the store (no duplicate).
	if len(st.sessions) != 1 {
		t.Fatalf("session count = %d, want 1 (no duplicate)", len(st.sessions))
	}
}

// TestSpawnOrchestratorNoCleanSpawnsWhenNoneExists: clean=false spawns a new
// orchestrator when no active one exists for the project.
func TestSpawnOrchestratorNoCleanSpawnsWhenNoneExists(t *testing.T) {
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer"}
	// No active orchestrator present.

	fc := &fakeCommander{}
	svc := &Service{manager: fc, store: st}

	got, err := svc.SpawnOrchestrator(context.Background(), "mer", false)
	if err != nil {
		t.Fatalf("SpawnOrchestrator: %v", err)
	}
	if !fc.spawned {
		t.Fatal("manager.Spawn must be called when no active orchestrator exists")
	}
	if len(fc.killed) != 0 {
		t.Fatalf("no kills expected with clean=false, got %v", fc.killed)
	}
	if got.ID == "" {
		t.Fatal("returned session must have an id")
	}
}

func TestSpawnOrchestratorVerifiesReplacementHarness(t *testing.T) {
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{
		ID:     "mer",
		Config: domain.ProjectConfig{Orchestrator: domain.RoleOverride{Harness: domain.HarnessCodex}},
	}
	fc := &fakeCommander{
		spawnRecord: domain.SessionRecord{
			ID:        "mer-9",
			ProjectID: "mer",
			Kind:      domain.KindOrchestrator,
			Harness:   domain.HarnessClaudeCode,
			Metadata:  domain.SessionMetadata{Branch: "ao/mer-orchestrator"},
		},
	}
	svc := &Service{manager: fc, store: st}

	_, err := svc.SpawnOrchestrator(context.Background(), "mer", false)
	if err == nil || !strings.Contains(err.Error(), `uses harness "claude-code", want "codex"`) {
		t.Fatalf("SpawnOrchestrator err = %v, want harness verification failure", err)
	}
}

type fakePRClaimer struct {
	out errorFreeClaimOutcome
	err error
}

type errorFreeClaimOutcome struct {
	ports.ClaimOutcome
}

func (f fakePRClaimer) ClaimPR(context.Context, domain.PullRequest, []domain.PullRequestCheck, []domain.PullRequestReview, []domain.PullRequestReviewThread, []domain.PullRequestComment, ports.ReviewWriteMode, bool) (ports.ClaimOutcome, error) {
	return f.out.ClaimOutcome, f.err
}

type fakeSCM struct {
	obs          ports.SCMObservation
	review       ports.SCMReviewObservation
	fetchErr     error
	reviewErr    error
	replyComment ports.SCMReviewCommentObservation
	replyErr     error
	resolveErr   error
}

func (f fakeSCM) ParseRepository(remote string) (ports.SCMRepo, bool) {
	owner, repo, err := githubRepoFromURL(remote)
	if err != nil {
		return ports.SCMRepo{}, false
	}
	return ports.SCMRepo{Provider: "github", Host: "github.com", Owner: owner, Name: repo, Repo: owner + "/" + repo}, true
}

func (f fakeSCM) FetchPullRequests(context.Context, []ports.SCMPRRef) ([]ports.SCMObservation, error) {
	if f.fetchErr != nil {
		return nil, f.fetchErr
	}
	if !f.obs.Fetched && f.obs.PR.URL == "" && f.obs.PR.Number == 0 {
		return nil, nil
	}
	return []ports.SCMObservation{f.obs}, nil
}

func (f fakeSCM) FetchReviewThreads(context.Context, ports.SCMPRRef) (ports.SCMReviewObservation, error) {
	return f.review, f.reviewErr
}

func (f fakeSCM) ReplyToThread(_ context.Context, _ ports.SCMPRRef, _, _ string) (ports.SCMReviewCommentObservation, error) {
	return f.replyComment, f.replyErr
}

func (f fakeSCM) ResolveThread(_ context.Context, _ ports.SCMPRRef, _ string) error {
	return f.resolveErr
}

func TestClaimPRMapsObserverAndStoreErrors(t *testing.T) {
	st := newFakeStore()
	now := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker, Metadata: domain.SessionMetadata{WorkspacePath: "/ws"}}
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", RepoOriginURL: "https://github.com/acme/repo"}

	cases := []struct {
		name string
		svc  *Service
		want error
	}{
		{"missing scm", NewWithDeps(Deps{Store: st}), ErrSCMUnavailable},
		{"not found", NewWithDeps(Deps{Store: st, PRClaimer: fakePRClaimer{}, SCM: fakeSCM{fetchErr: ports.ErrSCMNotFound}}), ErrPRNotFound},
		{"closed", NewWithDeps(Deps{Store: st, PRClaimer: fakePRClaimer{}, SCM: fakeSCM{obs: ports.SCMObservation{Fetched: true, Provider: "github", Host: "github.com", Repo: "acme/repo", PR: ports.SCMPRObservation{URL: "https://github.com/acme/repo/pull/7", Number: 7, Closed: true}}}}), ErrPRNotOpen},
		{"active owner", NewWithDeps(Deps{Store: st, PRClaimer: fakePRClaimer{err: ports.PRClaimedByActiveSessionError{Owner: "mer-2"}}, SCM: fakeSCM{obs: ports.SCMObservation{Fetched: true, Provider: "github", Host: "github.com", Repo: "acme/repo", PR: ports.SCMPRObservation{URL: "https://github.com/acme/repo/pull/7", Number: 7}}}}), ports.ErrPRClaimedByActiveSession},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.svc.ClaimPR(context.Background(), "mer-1", "7", ClaimPROptions{AllowTakeover: false})
			if !errors.Is(err, tc.want) {
				t.Fatalf("err=%v, want %v", err, tc.want)
			}
		})
	}

	st.pr["mer-1"] = domain.PRFacts{URL: "https://github.com/acme/repo/pull/7", Number: 7, CI: domain.CIPassing, UpdatedAt: now}
	svc := NewWithDeps(Deps{Store: st, PRClaimer: fakePRClaimer{out: errorFreeClaimOutcome{ports.ClaimOutcome{PreviousOwner: "mer-2"}}}, SCM: fakeSCM{obs: ports.SCMObservation{Fetched: true, Provider: "github", Host: "github.com", Repo: "acme/repo", PR: ports.SCMPRObservation{URL: "https://github.com/acme/repo/pull/7", Number: 7}}}})
	res, err := svc.ClaimPR(context.Background(), "mer-1", "7", ClaimPROptions{AllowTakeover: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.TakenOverFrom) != 1 || res.TakenOverFrom[0] != "mer-2" || len(res.PRs) != 1 || res.PRs[0].URL == "" {
		t.Fatalf("claim result = %+v", res)
	}
}

func TestListPRsOrdersActiveBeforeClosedThenUpdatedDesc(t *testing.T) {
	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker}
	now := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)
	st.pr = map[domain.SessionID]domain.PRFacts{}
	stList := &multiPRFakeStore{fakeStore: st, prs: []domain.PullRequest{
		{URL: "closed-new", SessionID: "mer-1", Number: 1, Closed: true, UpdatedAt: now.Add(2 * time.Hour)},
		{URL: "open-old", SessionID: "mer-1", Number: 2, UpdatedAt: now},
		{URL: "open-new", SessionID: "mer-1", Number: 3, UpdatedAt: now.Add(time.Hour)},
	}}
	got, err := (&Service{store: stList}).ListPRs(context.Background(), "mer-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].URL != "open-new" || got[1].URL != "open-old" || got[2].URL != "closed-new" {
		t.Fatalf("order = %+v", got)
	}
}

func TestListPRSummariesOmitsRawLogsAndReviewBodies(t *testing.T) {
	st := newFakeStore()
	now := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker}
	prURL := "https://github.com/acme/repo/pull/7"
	stList := &multiPRFakeStore{fakeStore: st, prs: []domain.PullRequest{{
		URL:                      prURL,
		HTMLURL:                  prURL,
		SessionID:                "mer-1",
		Number:                   7,
		CI:                       domain.CIFailing,
		Review:                   domain.ReviewChangesRequest,
		Mergeability:             domain.MergeConflicting,
		Provider:                 "github",
		Repo:                     "acme/repo",
		Title:                    "Fix dashboard",
		Author:                   "ada",
		SourceBranch:             "fix/dashboard",
		TargetBranch:             "main",
		HeadSHA:                  "abc123",
		ProviderMergeStateStatus: "dirty",
		UpdatedAt:                now,
		ObservedAt:               now.Add(-time.Minute),
		CIObservedAt:             now.Add(-time.Minute),
		ReviewObservedAt:         now.Add(-time.Minute),
	}}}
	stList.checks[prURL] = []domain.PullRequestCheck{
		{Name: "unit", Status: domain.PRCheckFailed, Conclusion: "failure", URL: "https://github.com/acme/repo/actions/runs/1", LogTail: "panic: secret"},
		{Name: "lint", Status: domain.PRCheckPassed, Conclusion: "success", URL: "https://github.com/acme/repo/actions/runs/2"},
	}
	stList.reviews[prURL] = []domain.PullRequestReview{
		{ID: "review-1", Author: "reviewer-a", State: domain.ReviewChangesRequest, URL: "https://github.com/acme/repo/pull/7#pullrequestreview-1", SubmittedAt: now.Add(-30 * time.Second)},
	}
	stList.comments[prURL] = []domain.PullRequestComment{
		{Author: "reviewer-a", File: "main.go", Line: 12, Body: "raw body must stay private", URL: "https://github.com/acme/repo/pull/7#discussion_r1"},
		{Author: "ci-bot", File: "main.go", Line: 13, Body: "bot body", URL: "https://github.com/acme/repo/pull/7#discussion_r2", IsBot: true},
		{Author: "reviewer-a", File: "test.go", Line: 22, Body: "another raw body", URL: "https://github.com/acme/repo/pull/7#discussion_r3"},
	}

	got, err := (&Service{store: stList}).ListPRSummaries(context.Background(), "mer-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("summaries = %+v", got)
	}
	pr := got[0]
	if pr.Title != "Fix dashboard" || pr.State != domain.PRStateOpen || pr.Provider != "github" || pr.Repo != "acme/repo" || pr.HeadSHA != "abc123" {
		t.Fatalf("metadata = %+v", pr)
	}
	if len(pr.CI.FailingChecks) != 1 || pr.CI.FailingChecks[0].Name != "unit" || pr.CI.FailingChecks[0].URL == "" {
		t.Fatalf("failing checks = %+v", pr.CI.FailingChecks)
	}
	if pr.Review.Decision != domain.ReviewChangesRequest || !pr.Review.HasUnresolvedHumanComments || len(pr.Review.UnresolvedBy) != 1 {
		t.Fatalf("review = %+v", pr.Review)
	}
	if reviewer := pr.Review.UnresolvedBy[0]; reviewer.ReviewerID != "reviewer-a" || reviewer.Count != 2 || len(reviewer.Links) != 2 {
		t.Fatalf("reviewer = %+v", reviewer)
	} else if reviewer.ReviewURL != "https://github.com/acme/repo/pull/7#pullrequestreview-1" {
		t.Fatalf("review url = %q", reviewer.ReviewURL)
	}
	if pr.Mergeability.State != domain.MergeConflicting || len(pr.Mergeability.ConflictFiles) != 0 || !containsString(pr.Mergeability.Reasons, "conflicts") {
		t.Fatalf("mergeability = %+v", pr.Mergeability)
	}
}

func TestListPRSummariesSuppressesFailingChecksUnlessCIFailing(t *testing.T) {
	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker}
	prURL := "https://github.com/acme/repo/pull/8"
	stList := &multiPRFakeStore{fakeStore: st, prs: []domain.PullRequest{{
		URL:       prURL,
		SessionID: "mer-1",
		Number:    8,
		CI:        domain.CIPassing,
		HeadSHA:   "new-sha",
		UpdatedAt: time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC),
	}}}
	stList.checks[prURL] = []domain.PullRequestCheck{
		{Name: "copy-check", CommitHash: "old-sha", Status: domain.PRCheckFailed, Conclusion: "failure", URL: "https://github.com/acme/repo/actions/runs/1"},
	}

	got, err := (&Service{store: stList}).ListPRSummaries(context.Background(), "mer-1")
	if err != nil {
		t.Fatal(err)
	}
	if got[0].CI.State != domain.CIPassing || len(got[0].CI.FailingChecks) != 0 {
		t.Fatalf("ci summary = %+v", got[0].CI)
	}
}

func TestListPRSummariesFiltersFailedChecksToCurrentHead(t *testing.T) {
	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker}
	prURL := "https://github.com/acme/repo/pull/9"
	stList := &multiPRFakeStore{fakeStore: st, prs: []domain.PullRequest{{
		URL:       prURL,
		SessionID: "mer-1",
		Number:    9,
		CI:        domain.CIFailing,
		HeadSHA:   "new-sha",
		UpdatedAt: time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC),
	}}}
	stList.checks[prURL] = []domain.PullRequestCheck{
		{Name: "old-copy-check", CommitHash: "old-sha", Status: domain.PRCheckFailed, Conclusion: "failure"},
		{Name: "current-lint", CommitHash: "new-sha", Status: domain.PRCheckFailed, Conclusion: "failure"},
	}

	got, err := (&Service{store: stList}).ListPRSummaries(context.Background(), "mer-1")
	if err != nil {
		t.Fatal(err)
	}
	checks := got[0].CI.FailingChecks
	if len(checks) != 1 || checks[0].Name != "current-lint" {
		t.Fatalf("failing checks = %+v", checks)
	}
}

func TestListPRSummariesSuppressesActiveDetailsForClosedOrMergedPRs(t *testing.T) {
	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker}
	prURL := "https://github.com/acme/repo/pull/10"
	stList := &multiPRFakeStore{fakeStore: st, prs: []domain.PullRequest{{
		URL:                      prURL,
		SessionID:                "mer-1",
		Number:                   10,
		Merged:                   true,
		CI:                       domain.CIFailing,
		Review:                   domain.ReviewChangesRequest,
		Mergeability:             domain.MergeConflicting,
		ProviderMergeStateStatus: "dirty",
		UpdatedAt:                time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC),
	}}}
	stList.checks[prURL] = []domain.PullRequestCheck{{Name: "unit", Status: domain.PRCheckFailed}}
	stList.comments[prURL] = []domain.PullRequestComment{{Author: "reviewer-a", File: "main.go", Line: 12, URL: "https://github.com/acme/repo/pull/10#discussion_r1"}}

	got, err := (&Service{store: stList}).ListPRSummaries(context.Background(), "mer-1")
	if err != nil {
		t.Fatal(err)
	}
	pr := got[0]
	if pr.State != domain.PRStateMerged {
		t.Fatalf("state = %q", pr.State)
	}
	if len(pr.CI.FailingChecks) != 0 || len(pr.Review.UnresolvedBy) != 0 || len(pr.Mergeability.Reasons) != 0 {
		t.Fatalf("active details should be suppressed for merged PR: ci=%+v review=%+v merge=%+v", pr.CI, pr.Review, pr.Mergeability)
	}
}

func TestListPRSummariesOnlyEmitsMergeReasonsForBlockedStates(t *testing.T) {
	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker}
	now := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)
	stList := &multiPRFakeStore{fakeStore: st, prs: []domain.PullRequest{
		{
			URL:                      "mergeable",
			SessionID:                "mer-1",
			Number:                   11,
			CI:                       domain.CIFailing,
			Review:                   domain.ReviewRequired,
			Mergeability:             domain.MergeMergeable,
			ProviderMergeStateStatus: "behind",
			UpdatedAt:                now,
		},
		{
			URL:                      "blocked",
			SessionID:                "mer-1",
			Number:                   12,
			Review:                   domain.ReviewRequired,
			Mergeability:             domain.MergeBlocked,
			ProviderMergeStateStatus: "behind",
			UpdatedAt:                now.Add(time.Minute),
		},
	}}

	got, err := (&Service{store: stList}).ListPRSummaries(context.Background(), "mer-1")
	if err != nil {
		t.Fatal(err)
	}
	byNumber := map[int]PRSummary{}
	for _, pr := range got {
		byNumber[pr.Number] = pr
	}
	if reasons := byNumber[11].Mergeability.Reasons; len(reasons) != 0 {
		t.Fatalf("mergeable reasons = %+v", reasons)
	}
	if reasons := byNumber[12].Mergeability.Reasons; !containsString(reasons, "behind_base") || !containsString(reasons, "review_required") {
		t.Fatalf("blocked reasons = %+v", reasons)
	}
}

type multiPRFakeStore struct {
	*fakeStore
	prs []domain.PullRequest
}

func (f *multiPRFakeStore) ListPRsBySession(context.Context, domain.SessionID) ([]domain.PullRequest, error) {
	return f.prs, nil
}

func containsString(values []string, want string) bool {
	for _, got := range values {
		if got == want {
			return true
		}
	}
	return false
}

// TestListReclaimable_SelectsFinishedWorkersWithResources covers the
// auto-reclaim candidate set: worker sessions displayed as merged or
// terminated that still hold a runtime handle or worktree. Orchestrators,
// still-working sessions, and already-torn-down terminals are excluded.
func TestListReclaimable_SelectsFinishedWorkersWithResources(t *testing.T) {
	st := newFakeStore()
	// merged worker, still has worktree -> included
	st.sessions["sess-merged"] = domain.SessionRecord{ID: "sess-merged", ProjectID: "mer", Kind: domain.KindWorker, IsTerminated: true, Metadata: domain.SessionMetadata{WorkspacePath: "/tmp/a"}}
	st.pr["sess-merged"] = domain.PRFacts{URL: "pr1", Merged: true}
	// terminated worker, still has worktree -> included
	st.sessions["sess-term"] = domain.SessionRecord{ID: "sess-term", ProjectID: "mer", Kind: domain.KindWorker, IsTerminated: true, Metadata: domain.SessionMetadata{WorkspacePath: "/tmp/b"}}
	// terminated worker, already torn down (no handle, no worktree) -> excluded
	st.sessions["sess-done"] = domain.SessionRecord{ID: "sess-done", ProjectID: "mer", Kind: domain.KindWorker, IsTerminated: true}
	// working worker -> excluded
	st.sessions["sess-live"] = domain.SessionRecord{ID: "sess-live", ProjectID: "mer", Kind: domain.KindWorker, Activity: domain.Activity{State: domain.ActivityActive}, Metadata: domain.SessionMetadata{WorkspacePath: "/tmp/c"}}
	// orchestrator, terminated -> excluded (worker-only auto-reclaim)
	st.sessions["proj-orchestrator"] = domain.SessionRecord{ID: "proj-orchestrator", ProjectID: "mer", Kind: domain.KindOrchestrator, IsTerminated: true, Metadata: domain.SessionMetadata{WorkspacePath: "/tmp/d"}}

	svc := &Service{store: st}
	got, err := svc.ListReclaimable(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := map[domain.SessionID]bool{"sess-merged": true, "sess-term": true}
	if len(got) != len(want) {
		t.Fatalf("got %v, want keys %v", got, want)
	}
	for _, c := range got {
		if !want[c.ID] {
			t.Fatalf("unexpected candidate %s", c.ID)
		}
	}
}

// TestListReclaimable_NeverTakesALiveWorkerWhosePRMerged is the "do not take out
// an active participant" rule.
//
// A worker that is STILL RUNNING but whose PR has merged derives the display
// status "merged" (deriveStatusDetail's anyMerged branch is reached with
// IsTerminated false) — the exact shape of a keep-warm worker that merged one
// PR and is already building the next. Selecting on that status would pull the
// worktree out from under an agent mid-task, so eligibility keys off the
// record's own IsTerminated flag instead.
func TestListReclaimable_NeverTakesALiveWorkerWhosePRMerged(t *testing.T) {
	st := newFakeStore()
	st.sessions["sess-live-merged"] = domain.SessionRecord{
		ID: "sess-live-merged", ProjectID: "mer", Kind: domain.KindWorker,
		IsTerminated: false, // still alive
		Activity:     domain.Activity{State: domain.ActivityActive, LastActivityAt: time.Now()},
		Metadata:     domain.SessionMetadata{WorkspacePath: "/tmp/live"},
	}
	st.pr["sess-live-merged"] = domain.PRFacts{URL: "pr1", Merged: true}

	svc := &Service{store: st}
	// Guard the premise: this session really does read as merged.
	sess, err := svc.Get(context.Background(), "sess-live-merged")
	if err != nil {
		t.Fatal(err)
	}
	if sess.Status != domain.StatusMerged {
		t.Fatalf("premise broken: want a live session displaying merged, got %q", sess.Status)
	}

	got, err := svc.ListReclaimable(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("a live worker must never be reclaimable, got %+v", got)
	}
}

// TestListReclaimable_NeverTakesASuspendedSession: a suspended (sleeping)
// session holds no tmux and shows no activity, yet is fully alive — one sitting
// at needs_input for days is the case that a "no tmux means dead" rule would
// wrongly delete. IsTerminated is what excludes it.
func TestListReclaimable_NeverTakesASuspendedSession(t *testing.T) {
	st := newFakeStore()
	st.sessions["sess-asleep"] = domain.SessionRecord{
		ID: "sess-asleep", ProjectID: "mer", Kind: domain.KindWorker,
		IsSuspended: true, IsTerminated: false,
		Metadata: domain.SessionMetadata{WorkspacePath: "/tmp/asleep"},
	}

	svc := &Service{store: st}
	got, err := svc.ListReclaimable(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("a suspended session must never be reclaimable, got %+v", got)
	}
}

// TestListReclaimable_TakesASuspendedSessionOnceTerminated is the other side of
// that rule, and the reason suspension is not a second gate: IsSuspended is
// orthogonal to IsTerminated, so a session that was paused and later finished is
// genuinely done. Gating on suspension too would strand its worktree forever.
func TestListReclaimable_TakesASuspendedSessionOnceTerminated(t *testing.T) {
	st := newFakeStore()
	st.sessions["sess-was-asleep"] = domain.SessionRecord{
		ID: "sess-was-asleep", ProjectID: "mer", Kind: domain.KindWorker,
		IsSuspended: true, IsTerminated: true,
		Metadata: domain.SessionMetadata{WorkspacePath: "/tmp/was-asleep"},
	}

	svc := &Service{store: st}
	got, err := svc.ListReclaimable(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "sess-was-asleep" {
		t.Fatalf("a terminated session must be reclaimable even if it was suspended, got %+v", got)
	}
}

// TestReclaim_DelegatesToKill: Reclaim reuses Kill's teardown (tmux + worktree,
// branch kept) so the auto-reclaim loop shares the exact same teardown path a
// user-initiated kill uses.
func TestReclaim_DelegatesToKill(t *testing.T) {
	st := newFakeStore()
	st.sessions["sess-1"] = domain.SessionRecord{
		ID: "sess-1", ProjectID: "demo", Kind: domain.KindWorker, IsTerminated: true,
		Activity: domain.Activity{State: domain.ActivityExited, LastActivityAt: time.Now()},
	}
	fc := &fakeCommander{}
	svc := &Service{manager: fc, store: st}
	out, err := svc.Reclaim(context.Background(), "sess-1")
	if err != nil {
		t.Fatalf("Reclaim: %v", err)
	}
	if len(fc.killed) != 1 || fc.killed[0] != "sess-1" {
		t.Fatalf("killed = %v, want [sess-1]", fc.killed)
	}
	if !out.Freed {
		t.Fatal("a successful teardown must report Freed")
	}
}

// TestReclaim_ReportsAPreservedWorkspaceAsNotFreed is the core of the bug this
// feature fixes: a worktree kept because it holds uncommitted work used to come
// back from Reclaim indistinguishable from one that was deleted, so the reclaim
// loop recorded a success that never happened and never retried.
func TestReclaim_ReportsAPreservedWorkspaceAsNotFreed(t *testing.T) {
	st := newFakeStore()
	st.sessions["sess-1"] = domain.SessionRecord{
		ID: "sess-1", ProjectID: "demo", Kind: domain.KindWorker, IsTerminated: true,
		Activity: domain.Activity{State: domain.ActivityExited, LastActivityAt: time.Now()},
	}
	fc := &fakeCommander{teardownResult: &sessionmanager.TeardownResult{
		Freed:         false,
		Reason:        sessionmanager.ReasonWorkspaceDirty,
		WorkspacePath: "/tmp/dirty",
	}}
	svc := &Service{manager: fc, store: st}

	out, err := svc.Reclaim(context.Background(), "sess-1")
	if err != nil {
		t.Fatalf("a refusal is not an error: %v", err)
	}
	if out.Freed {
		t.Fatal("a preserved workspace must NOT report Freed")
	}
	if out.Reason != sessionmanager.ReasonWorkspaceDirty {
		t.Fatalf("reason = %q, want %q", out.Reason, sessionmanager.ReasonWorkspaceDirty)
	}
	if out.BytesFreed != 0 {
		t.Fatalf("nothing was freed, so BytesFreed must be 0, got %d", out.BytesFreed)
	}
}

// TestRestart_DelegatesToManager: the session service forwards a restart to the
// manager's atomic Restart (kill-then-restore) and returns the relaunched read
// model.
func TestRestart_DelegatesToManager(t *testing.T) {
	st := newFakeStore()
	st.sessions["sess-1"] = domain.SessionRecord{ID: "sess-1", ProjectID: "mer", Kind: domain.KindWorker}
	fc := &fakeCommander{restartRecord: domain.SessionRecord{ID: "sess-1", ProjectID: "mer", Kind: domain.KindWorker}}
	svc := &Service{manager: fc, store: st}

	sess, err := svc.Restart(context.Background(), "sess-1")
	if err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if len(fc.restarted) != 1 || fc.restarted[0] != "sess-1" {
		t.Fatalf("restarted = %v, want [sess-1]", fc.restarted)
	}
	if sess.ID != "sess-1" {
		t.Fatalf("returned session id = %q, want sess-1", sess.ID)
	}
}

// TestRestart_MapsManagerError: a manager restart failure (e.g. an
// unrestorable session) is mapped through the API-error translation, not
// returned raw.
func TestRestart_MapsManagerError(t *testing.T) {
	fc := &fakeCommander{restartErr: sessionmanager.ErrNotRestorable}
	svc := &Service{manager: fc, store: newFakeStore()}

	_, err := svc.Restart(context.Background(), "sess-1")
	var e *apierr.Error
	if !errors.As(err, &e) || e.Kind != apierr.KindConflict {
		t.Fatalf("err = %v, want apierr.Conflict", err)
	}
}

// TestDelete_NonTerminal_ReturnsConflict: Delete refuses a session that is
// still working, mapping sessionmanager.ErrNotTerminal to a 409.
func TestDelete_NonTerminal_ReturnsConflict(t *testing.T) {
	st := newFakeStore()
	st.sessions["sess-live"] = domain.SessionRecord{ID: "sess-live", ProjectID: "mer", Kind: domain.KindWorker, Activity: domain.Activity{State: domain.ActivityActive}, Metadata: domain.SessionMetadata{WorkspacePath: "/tmp/c"}}
	fc := &fakeCommander{}
	svc := &Service{manager: fc, store: st}

	err := svc.Delete(context.Background(), "sess-live", false)
	var e *apierr.Error
	if !errors.As(err, &e) || e.Kind != apierr.KindConflict || e.Code != "SESSION_NOT_TERMINAL" {
		t.Fatalf("err = %v, want apierr.Conflict SESSION_NOT_TERMINAL", err)
	}
	if len(fc.purged) != 0 {
		t.Fatalf("purged = %v, want none for a non-terminal session", fc.purged)
	}
}

// TestDelete_Terminal_DelegatesToPurge: a merged/terminated session delegates
// to manager.PurgeSession with the caller's force flag.
func TestDelete_Terminal_DelegatesToPurge(t *testing.T) {
	st := newFakeStore()
	st.sessions["sess-term"] = domain.SessionRecord{ID: "sess-term", ProjectID: "mer", Kind: domain.KindWorker, IsTerminated: true, Metadata: domain.SessionMetadata{WorkspacePath: "/tmp/b"}}
	fc := &fakeCommander{}
	svc := &Service{manager: fc, store: st}

	if err := svc.Delete(context.Background(), "sess-term", true); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if len(fc.purged) != 1 || fc.purged[0] != "sess-term" {
		t.Fatalf("purged = %v, want [sess-term]", fc.purged)
	}
	if len(fc.purgedForce) != 1 || !fc.purgedForce[0] {
		t.Fatalf("purgedForce = %v, want [true]", fc.purgedForce)
	}
}

// TestDelete_UnknownSession_ReturnsNotFound: a nonexistent id maps to 404
// before any manager call.
func TestDelete_UnknownSession_ReturnsNotFound(t *testing.T) {
	st := newFakeStore()
	fc := &fakeCommander{}
	svc := &Service{manager: fc, store: st}

	err := svc.Delete(context.Background(), "ghost", false)
	var e *apierr.Error
	if !errors.As(err, &e) || e.Kind != apierr.KindNotFound || e.Code != "SESSION_NOT_FOUND" {
		t.Fatalf("err = %v, want apierr.NotFound SESSION_NOT_FOUND", err)
	}
	if len(fc.purged) != 0 {
		t.Fatalf("purged = %v, want none for an unknown session", fc.purged)
	}
}

// TestDelete_WorkspaceDirty_ReturnsConflict: the manager's ErrWorkspaceDirty
// (a non-force purge on a dirty worktree) maps to a 409 the UI can offer to
// retry with force, rather than a 500.
func TestDelete_WorkspaceDirty_ReturnsConflict(t *testing.T) {
	st := newFakeStore()
	st.sessions["sess-term"] = domain.SessionRecord{ID: "sess-term", ProjectID: "mer", Kind: domain.KindWorker, IsTerminated: true, Metadata: domain.SessionMetadata{WorkspacePath: "/tmp/b"}}
	fc := &fakeCommander{purgeErr: fmt.Errorf("purge sess-term: %w", ports.ErrWorkspaceDirty)}
	svc := &Service{manager: fc, store: st}

	err := svc.Delete(context.Background(), "sess-term", false)
	var e *apierr.Error
	if !errors.As(err, &e) || e.Kind != apierr.KindConflict || e.Code != "SESSION_WORKSPACE_DIRTY" {
		t.Fatalf("err = %v, want apierr.Conflict SESSION_WORKSPACE_DIRTY", err)
	}
}

// TestToAPIErrorMapsNotTerminalAndWorkspaceDirty: direct coverage of the two
// new toAPIError cases this task adds.
func TestToAPIErrorMapsNotTerminalAndWorkspaceDirty(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		wantKind apierr.Kind
		wantCode string
	}{
		{"not terminal", fmt.Errorf("delete sess-1: %w", sessionmanager.ErrNotTerminal), apierr.KindConflict, "SESSION_NOT_TERMINAL"},
		{"workspace dirty", fmt.Errorf("purge sess-1: %w", ports.ErrWorkspaceDirty), apierr.KindConflict, "SESSION_WORKSPACE_DIRTY"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mapped := toAPIError(tc.err)
			var e *apierr.Error
			if !errors.As(mapped, &e) || e.Kind != tc.wantKind || e.Code != tc.wantCode {
				t.Fatalf("mapped = %v, want %s %s", mapped, tc.wantCode, e)
			}
		})
	}
}

// The guard this incident asked for. A live worker's eligibility is decided by
// ListReclaimable, but between that listing and this teardown the session can
// come back — a restore, a resume, a TODO started — and the reclaim loop would
// then tear down a session with an agent working in it. Reclaim re-reads the
// record it is about to destroy and refuses anything not terminated, so the
// filter is not the only thing standing between a live agent and its worktree.
func TestReclaim_RefusesALiveSession(t *testing.T) {
	st := newFakeStore()
	st.sessions["sess-1"] = domain.SessionRecord{
		ID: "sess-1", ProjectID: "demo", Kind: domain.KindWorker,
		Activity:     domain.Activity{State: domain.ActivityActive, LastActivityAt: time.Now()},
		IsTerminated: false,
		Metadata:     domain.SessionMetadata{WorkspacePath: "/tmp/live-worktree"},
	}
	fc := &fakeCommander{}
	svc := &Service{manager: fc, store: st}

	out, err := svc.Reclaim(context.Background(), "sess-1")
	if err != nil {
		t.Fatalf("a refusal is not an error: %v", err)
	}
	if len(fc.killed) != 0 {
		t.Fatalf("teardown ran against a live session: %v", fc.killed)
	}
	if out.Freed {
		t.Fatal("nothing may be freed from a live session")
	}
	if out.Reason != ReasonNotTerminated {
		t.Fatalf("reason = %q, want %q", out.Reason, ReasonNotTerminated)
	}
}

// A session that IS terminated still reclaims: the guard must refuse live
// sessions, not stop the feature working.
func TestReclaim_StillTearsDownATerminatedSession(t *testing.T) {
	st := newFakeStore()
	st.sessions["sess-1"] = domain.SessionRecord{
		ID: "sess-1", ProjectID: "demo", Kind: domain.KindWorker,
		Activity:     domain.Activity{State: domain.ActivityExited, LastActivityAt: time.Now()},
		IsTerminated: true,
	}
	fc := &fakeCommander{}
	svc := &Service{manager: fc, store: st}

	out, err := svc.Reclaim(context.Background(), "sess-1")
	if err != nil {
		t.Fatalf("Reclaim: %v", err)
	}
	if len(fc.killed) != 1 || fc.killed[0] != "sess-1" {
		t.Fatalf("killed = %v, want [sess-1]", fc.killed)
	}
	if !out.Freed {
		t.Fatal("a terminated session's teardown must still report Freed")
	}
}

// An unreadable record is not permission to proceed. If the store cannot say
// whether the session is finished, tearing its worktree down anyway is exactly
// the gamble the repo forbids elsewhere ("do not treat a failed probe as proof a
// session is dead"). It surfaces as an error so the loop logs it loudly and
// retries, rather than being filed alongside the routine "still running" skip.
func TestReclaim_RefusesWhenTheRecordCannotBeRead(t *testing.T) {
	st := newFakeStore()
	st.getSessionErr = errors.New("db unavailable")
	fc := &fakeCommander{}
	svc := &Service{manager: fc, store: st}

	_, err := svc.Reclaim(context.Background(), "sess-1")
	if err == nil {
		t.Fatal("an unreadable record must surface as an error, not a silent skip")
	}
	if len(fc.killed) != 0 {
		t.Fatalf("teardown ran without knowing the session was finished: %v", fc.killed)
	}
}

// TestWakeCrewMember_BusyIsAConflictNotAFailure: "the other member is running"
// is a state the caller can act on (stand it down, ask again), not a broken
// daemon. It must surface as a 409 with a named code, the same way the review
// engine's tree-busy refusal does.
func TestWakeCrewMember_BusyIsAConflictNotAFailure(t *testing.T) {
	fc := &fakeCommander{crewWakeErr: fmt.Errorf("wrap: %w", sessionmanager.ErrCrewBusy)}
	svc := &Service{store: newFakeStore(), manager: fc}

	_, err := svc.WakeCrewMember(context.Background(), "mer-2")
	var e *apierr.Error
	if !errors.As(err, &e) || e.Kind != apierr.KindConflict || e.Code != "CREW_SLOT_BUSY" {
		t.Fatalf("err = %v, want apierr Conflict CREW_SLOT_BUSY", err)
	}
}

// TestAttachCrewMember_RefusesAFinishedTask. Attaching a qa to a task that is
// over would create a session holding a worktree that is about to be reclaimed,
// on a branch nobody will push, with no turn it could ever be given. Refusing is
// the honest answer, and the refusal is a CONFLICT: the request is well formed,
// the task is simply past it.
//
// It is checked HERE rather than in the manager because "finished" is a derived
// status assembled from PR facts at read time - AO deliberately stores no
// display status - and the manager cannot see PR facts.
func TestAttachCrewMember_RefusesAFinishedTask(t *testing.T) {
	live := func() domain.SessionRecord {
		return domain.SessionRecord{
			ID: "dev-1", ProjectID: "mer", Kind: domain.KindWorker,
			Activity: domain.Activity{State: domain.ActivityActive, LastActivityAt: time.Now()},
			Metadata: domain.SessionMetadata{WorkspacePath: "/tmp/dev-1", Branch: "feature/x"},
		}
	}
	for _, tc := range []struct {
		name    string
		mutate  func(*fakeStore)
		wantErr bool
	}{
		{
			name: "the PR merged",
			mutate: func(st *fakeStore) {
				st.pr["dev-1"] = domain.PRFacts{URL: "pr1", Merged: true}
			},
			wantErr: true,
		},
		{
			name: "dev was torn down",
			mutate: func(st *fakeStore) {
				rec := st.sessions["dev-1"]
				rec.IsTerminated = true
				rec.Activity = domain.Activity{State: domain.ActivityExited}
				st.sessions["dev-1"] = rec
			},
			wantErr: true,
		},
		{
			// Parked is not finished. The new member is born asleep, so attaching
			// here wakes nothing and changes nothing until a human moves the baton.
			name: "dev is merely suspended",
			mutate: func(st *fakeStore) {
				rec := st.sessions["dev-1"]
				rec.IsSuspended = true
				st.sessions["dev-1"] = rec
			},
		},
		{name: "dev is working"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newFakeStore()
			st.sessions["dev-1"] = live()
			if tc.mutate != nil {
				tc.mutate(st)
			}
			// The route resolves the id to the task's dev before it decides
			// anything, so the fake answers with the row the store now holds.
			cmd := &fakeCommander{crewDevOf: map[domain.SessionID]domain.SessionRecord{"dev-1": st.sessions["dev-1"]}}
			svc := &Service{store: st, manager: cmd}

			member, err := svc.AttachCrewMember(context.Background(), "dev-1", domain.CrewRoleQA)
			if tc.wantErr {
				if err == nil {
					t.Fatal("a finished task accepted a new crew member")
				}
				if len(cmd.crewAttached) != 0 {
					t.Fatalf("the refusal still reached the manager: %v", cmd.crewAttached)
				}
				var apiErr *apierr.Error
				if !errors.As(err, &apiErr) || apiErr.Code != "CREW_TASK_FINISHED" {
					t.Fatalf("err = %v, want a CREW_TASK_FINISHED conflict", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("AttachCrewMember: %v", err)
			}
			if !member.IsSuspended {
				t.Fatal("the attached member must be born suspended")
			}
			if len(cmd.crewAttached) != 1 || cmd.crewAttached[0] != "dev-1" {
				t.Fatalf("manager saw %v, want exactly [dev-1]", cmd.crewAttached)
			}
		})
	}
}

// TestAttachCrewMember_ResolvesEitherMemberToTheTask: a human holding qa's id
// must not have to know it is holding the wrong one. The route resolves to dev,
// which is the crew's root and the only session an attach can hang off.
func TestAttachCrewMember_ResolvesEitherMemberToTheTask(t *testing.T) {
	st := newFakeStore()
	st.sessions["dev-1"] = domain.SessionRecord{
		ID: "dev-1", ProjectID: "mer", Kind: domain.KindWorker,
		Activity: domain.Activity{State: domain.ActivityActive, LastActivityAt: time.Now()},
		Metadata: domain.SessionMetadata{WorkspacePath: "/tmp/dev-1", Branch: "feature/x"},
	}
	cmd := &fakeCommander{crewDevOf: map[domain.SessionID]domain.SessionRecord{
		"qa-1": st.sessions["dev-1"],
	}}
	svc := &Service{store: st, manager: cmd}

	if _, err := svc.AttachCrewMember(context.Background(), "qa-1", domain.CrewRoleQA); err != nil {
		t.Fatalf("AttachCrewMember: %v", err)
	}
	if len(cmd.crewAttached) != 1 || cmd.crewAttached[0] != "dev-1" {
		t.Fatalf("manager saw %v, want the TASK's dev [dev-1]", cmd.crewAttached)
	}
}
