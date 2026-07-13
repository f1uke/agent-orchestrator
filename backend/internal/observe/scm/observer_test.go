package scm

// This file tests the SCM observer orchestration contract with fake provider,
// store, and lifecycle collaborators so ETag decisions, batching, log fetching,
// review cadence, semantic hashes, and notification behavior stay provider-neutral.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var (
	testRepo    = ports.SCMRepo{Provider: "github", Host: "github.com", Owner: "o", Name: "r", Repo: "o/r"}
	testAPIRepo = ports.SCMRepo{Provider: "github", Host: "github.com", Owner: "o", Name: "api", Repo: "o/api"}
)

type fakeStore struct {
	mu sync.Mutex

	sessions       []domain.SessionRecord
	projects       map[string]domain.ProjectRecord
	workspaceRepos map[string][]domain.WorkspaceRepoRecord
	prs            map[domain.SessionID][]domain.PullRequest
	checks         map[string][]domain.PullRequestCheck
	writeErr       error

	writes []fakeWrite

	listEntered chan struct{}
	listRelease chan struct{}
}

type fakeWrite struct {
	pr         domain.PullRequest
	checks     []domain.PullRequestCheck
	reviews    []domain.PullRequestReview
	comments   []domain.PullRequestComment
	reviewMode ports.ReviewWriteMode
}

func (s *fakeStore) ListAllSessions(ctx context.Context) ([]domain.SessionRecord, error) {
	if s.listEntered != nil {
		select {
		case <-s.listEntered:
		default:
			close(s.listEntered)
		}
	}
	if s.listRelease != nil {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-s.listRelease:
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]domain.SessionRecord(nil), s.sessions...), nil
}

func (s *fakeStore) GetProject(_ context.Context, id string) (domain.ProjectRecord, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.projects[id]
	return p, ok, nil
}

func (s *fakeStore) UpsertProject(_ context.Context, row domain.ProjectRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.projects == nil {
		s.projects = map[string]domain.ProjectRecord{}
	}
	s.projects[row.ID] = row
	return nil
}

func (s *fakeStore) ListWorkspaceRepos(_ context.Context, projectID string) ([]domain.WorkspaceRepoRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]domain.WorkspaceRepoRecord(nil), s.workspaceRepos[projectID]...), nil
}

func (s *fakeStore) ListPRsBySession(_ context.Context, id domain.SessionID) ([]domain.PullRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]domain.PullRequest(nil), s.prs[id]...), nil
}

func (s *fakeStore) ListChecks(_ context.Context, prURL string) ([]domain.PullRequestCheck, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]domain.PullRequestCheck(nil), s.checks[prURL]...), nil
}

func (s *fakeStore) WriteSCMObservation(_ context.Context, pr domain.PullRequest, checks []domain.PullRequestCheck, reviews []domain.PullRequestReview, threads []domain.PullRequestReviewThread, comments []domain.PullRequestComment, reviewMode ports.ReviewWriteMode) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.writeErr != nil {
		return s.writeErr
	}
	s.writes = append(s.writes, fakeWrite{pr: pr, checks: append([]domain.PullRequestCheck(nil), checks...), reviews: append([]domain.PullRequestReview(nil), reviews...), comments: append([]domain.PullRequestComment(nil), comments...), reviewMode: reviewMode})
	return nil
}

type fakeProvider struct {
	mu           sync.Mutex
	repoGuards   map[string]ports.SCMGuardResult
	checkGuards  map[string]ports.SCMGuardResult
	openPRs      map[string][]ports.SCMPRObservation
	listErr      error
	observations map[string]ports.SCMObservation
	reviews      map[string]ports.SCMReviewObservation
	logTails     map[string]string
	fetchErr     error
	reviewErr    error

	credentialGate   bool
	credentialOK     bool
	credentialErr    error
	credentialChecks int
	repoGuardCalls   int
	listCalls        int
	fetchBatches     [][]ports.SCMPRRef
	logCalls         int
	reviewCalls      int
}

func (p *fakeProvider) SCMCredentialsAvailable(context.Context) (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.credentialChecks++
	if !p.credentialGate {
		return true, nil
	}
	return p.credentialOK, p.credentialErr
}

func (p *fakeProvider) ParseRepository(remote string) (ports.SCMRepo, bool) {
	remote = strings.TrimSpace(remote)
	if remote == "" {
		return ports.SCMRepo{}, false
	}
	s := strings.TrimSuffix(remote, ".git")
	s = strings.TrimPrefix(s, "https://github.com/")
	s = strings.TrimPrefix(s, "git@github.com:")
	parts := strings.Split(strings.Trim(s, "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return ports.SCMRepo{}, false
	}
	return ports.SCMRepo{Provider: "github", Host: "github.com", Owner: parts[0], Name: parts[1], Repo: parts[0] + "/" + parts[1]}, true
}
func (p *fakeProvider) RepoPRListGuard(_ context.Context, repo ports.SCMRepo, _ string) (ports.SCMGuardResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.repoGuardCalls++
	return p.repoGuards[prKey(repo, 0)], nil
}
func (p *fakeProvider) ListOpenPRsByRepo(_ context.Context, repo ports.SCMRepo) ([]ports.SCMPRObservation, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.listCalls++
	if p.listErr != nil {
		return nil, p.listErr
	}
	return p.openPRs[prKey(repo, 0)], nil
}
func (p *fakeProvider) CommitChecksGuard(_ context.Context, repo ports.SCMRepo, sha, _ string) (ports.SCMGuardResult, error) {
	return p.checkGuards[commitKey(repo, sha)], nil
}
func (p *fakeProvider) FetchPullRequests(_ context.Context, refs []ports.SCMPRRef) ([]ports.SCMObservation, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.fetchBatches = append(p.fetchBatches, append([]ports.SCMPRRef(nil), refs...))
	if p.fetchErr != nil {
		return nil, p.fetchErr
	}
	out := make([]ports.SCMObservation, 0, len(refs))
	for _, ref := range refs {
		if obs, ok := p.observations[prKey(ref.Repo, ref.Number)]; ok {
			out = append(out, obs)
		}
	}
	return out, nil
}
func (p *fakeProvider) FetchFailedCheckLogTail(_ context.Context, _ ports.SCMRepo, check ports.SCMCheckObservation) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.logCalls++
	return p.logTails[check.Name], nil
}
func (p *fakeProvider) FetchReviewThreads(_ context.Context, ref ports.SCMPRRef) (ports.SCMReviewObservation, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.reviewCalls++
	if p.reviewErr != nil {
		return ports.SCMReviewObservation{}, p.reviewErr
	}
	return p.reviews[prKey(ref.Repo, ref.Number)], nil
}

type fakeLifecycle struct {
	observed []ports.SCMObservation
	err      error
}

func (l *fakeLifecycle) ApplySCMObservation(_ context.Context, _ domain.SessionID, obs ports.SCMObservation) error {
	if l.err != nil {
		return l.err
	}
	l.observed = append(l.observed, obs)
	return nil
}

func newTestObserver(store *fakeStore, provider *fakeProvider, lc Lifecycle, now time.Time) *Observer {
	return New(provider, store, lc, Config{Clock: func() time.Time { return now }, Tick: time.Hour, Logger: quietSlog(), CacheMax: 128})
}

func quietSlog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func testStoreWithSession() *fakeStore {
	return &fakeStore{
		sessions: []domain.SessionRecord{{ID: "p-1", ProjectID: "p", Metadata: domain.SessionMetadata{Branch: "feat"}}},
		projects: map[string]domain.ProjectRecord{"p": {ID: "p", RepoOriginURL: "https://github.com/o/r.git"}},
		prs:      map[domain.SessionID][]domain.PullRequest{},
		checks:   map[string][]domain.PullRequestCheck{},
	}
}

func testObs(num int) ports.SCMObservation {
	return ports.SCMObservation{
		Fetched: true, Provider: "github", Host: "github.com", Repo: "o/r",
		PR:           ports.SCMPRObservation{URL: "https://github.com/o/r/pull/" + fmt.Sprint(num), Number: num, State: "open", SourceBranch: "feat", TargetBranch: "main", HeadSHA: "sha" + fmt.Sprint(num), Title: "PR"},
		CI:           ports.SCMCIObservation{Summary: string(domain.CIPassing), HeadSHA: "sha" + fmt.Sprint(num), Checks: []ports.SCMCheckObservation{{Name: "build", Status: string(domain.PRCheckPassed), Conclusion: "success", URL: "ci"}}},
		Review:       ports.SCMReviewObservation{Decision: string(domain.ReviewNone)},
		Mergeability: ports.SCMMergeabilityObservation{State: string(domain.MergeMergeable), Mergeable: true},
	}
}

func knownPR(num int) domain.PullRequest {
	obs := testObs(num)
	pr, _, _, _, _ := domainFromObservation("p-1", obs, domain.PullRequest{}, persistenceOptions{}, time.Unix(1, 0).UTC())
	return pr
}

func TestRepoForTrackedPRMatchesLegacyRepoOnlyRows(t *testing.T) {
	pr := knownPR(1)
	pr.Provider = ""
	pr.Host = ""
	pr.Repo = "o/r"
	repo, ok := repoForTrackedPR(pr, []ports.SCMRepo{testRepo})
	if !ok {
		t.Fatal("legacy repo-only row should match candidate repo")
	}
	if repoFullName(repo) != "o/r" {
		t.Fatalf("matched repo = %q, want o/r", repoFullName(repo))
	}
}

func TestRepoForTrackedPRUsesPersistedRepoWhenCurrentScanDropsUpstream(t *testing.T) {
	pr := knownPR(42)
	pr.Provider = "github"
	pr.Host = "github.com"
	pr.Repo = "upstream/api"
	repo, ok := repoForTrackedPR(pr, []ports.SCMRepo{testAPIRepo})
	if !ok {
		t.Fatal("persisted tracked PR repo should refresh even when current remotes no longer include it")
	}
	if repo.Provider != "github" || repo.Host != "github.com" || repo.Repo != "upstream/api" {
		t.Fatalf("repo = %#v, want persisted upstream/api tuple", repo)
	}
}

func TestStartAsyncPerformsImmediatePollAndStopsOnCancel(t *testing.T) {
	store := testStoreWithSession()
	store.listEntered = make(chan struct{})
	store.listRelease = make(chan struct{})
	provider := &fakeProvider{repoGuards: map[string]ports.SCMGuardResult{}, observations: map[string]ports.SCMObservation{}}
	obs := newTestObserver(store, provider, &fakeLifecycle{}, time.Unix(1, 0).UTC())
	ctx, cancel := context.WithCancel(context.Background())
	done := obs.Start(ctx)
	select {
	case <-store.listEntered:
	case <-time.After(time.Second):
		t.Fatal("initial poll did not start asynchronously")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("observer did not exit after context cancellation")
	}
}

func TestPoll_DisablesOnceWhenCredentialsUnavailable(t *testing.T) {
	store := testStoreWithSession()
	provider := &fakeProvider{
		credentialGate: true,
		credentialOK:   false,
		repoGuards:     map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "v1"}},
		observations:   map[string]ports.SCMObservation{},
	}
	obs := newTestObserver(store, provider, &fakeLifecycle{}, time.Unix(1, 0).UTC())
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if provider.credentialChecks != 1 {
		t.Fatalf("credential checks = %d, want one lazy check", provider.credentialChecks)
	}
	if provider.repoGuardCalls != 0 || provider.listCalls != 0 || len(provider.fetchBatches) != 0 {
		t.Fatalf("provider API calls should be skipped without credentials: guards=%d lists=%d batches=%d",
			provider.repoGuardCalls, provider.listCalls, len(provider.fetchBatches))
	}
}

func TestPoll_RetriesTransientCredentialErrors(t *testing.T) {
	store := testStoreWithSession()
	provider := &fakeProvider{
		credentialGate: true,
		credentialErr:  errors.New("gh auth token failed transiently"),
		repoGuards:     map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "v1"}},
		observations:   map[string]ports.SCMObservation{},
	}
	obs := newTestObserver(store, provider, &fakeLifecycle{}, time.Unix(1, 0).UTC())
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if obs.credentialsChecked || obs.disabled {
		t.Fatalf("transient credential error should not commit checked/disabled: checked=%v disabled=%v", obs.credentialsChecked, obs.disabled)
	}
	if provider.credentialChecks != 1 || provider.repoGuardCalls != 0 {
		t.Fatalf("first poll should check credentials only: checks=%d repoGuards=%d", provider.credentialChecks, provider.repoGuardCalls)
	}

	provider.mu.Lock()
	provider.credentialErr = nil
	provider.credentialOK = true
	provider.mu.Unlock()
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !obs.credentialsChecked || obs.disabled {
		t.Fatalf("successful retry should commit checked without disabling: checked=%v disabled=%v", obs.credentialsChecked, obs.disabled)
	}
	if provider.credentialChecks != 2 || provider.repoGuardCalls != 1 {
		t.Fatalf("second poll should retry credentials and continue: checks=%d repoGuards=%d", provider.credentialChecks, provider.repoGuardCalls)
	}
}

// syncBuffer is a goroutine-safe wrapper around bytes.Buffer for capturing
// slog output emitted from the observer's background goroutine.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// TestStart_LogsDisabledWarningWhenNoTokenAndNoSubjects exercises the bug-7
// regression: on a fresh daemon with no tracked sessions/PRs, discoverSubjects
// returns empty and Poll short-circuits before reaching the credential gate.
// The "scm observer disabled: provider credentials unavailable" warn line must
// still fire exactly once from the observer loop's pre-Poll credential check.
func TestStart_LogsDisabledWarningWhenNoTokenAndNoSubjects(t *testing.T) {
	store := &fakeStore{
		sessions: nil, // no sessions → discoverSubjects returns empty
		projects: map[string]domain.ProjectRecord{},
		prs:      map[domain.SessionID][]domain.PullRequest{},
		checks:   map[string][]domain.PullRequestCheck{},
	}
	provider := &fakeProvider{
		credentialGate: true,
		credentialOK:   false,
		repoGuards:     map[string]ports.SCMGuardResult{},
		observations:   map[string]ports.SCMObservation{},
	}

	buf := &syncBuffer{}
	logger := slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	obs := New(provider, store, &fakeLifecycle{}, Config{
		Clock:    func() time.Time { return time.Unix(1, 0).UTC() },
		Tick:     time.Hour,
		Logger:   logger,
		CacheMax: 128,
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := obs.Start(ctx)
	// Wait until the loop has emitted the expected warn line, or fail on timeout.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(buf.String(), "scm observer disabled: provider credentials unavailable") {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("observer did not exit after context cancellation")
	}

	logged := buf.String()
	if !strings.Contains(logged, "scm observer disabled: provider credentials unavailable") {
		t.Fatalf("expected disabled-credentials warn line in logs; got:\n%s", logged)
	}
	if got := strings.Count(logged, "scm observer disabled: provider credentials unavailable"); got != 1 {
		t.Fatalf("warn line should fire exactly once, got %d occurrences:\n%s", got, logged)
	}
	if !obs.credentialsChecked || !obs.disabled {
		t.Fatalf("observer state after pre-poll credential check: checked=%v disabled=%v", obs.credentialsChecked, obs.disabled)
	}
	if provider.credentialChecks != 1 {
		t.Fatalf("credential checks = %d, want exactly one pre-poll check", provider.credentialChecks)
	}
	if provider.repoGuardCalls != 0 || provider.listCalls != 0 || len(provider.fetchBatches) != 0 {
		t.Fatalf("no provider API calls expected when disabled: guards=%d lists=%d batches=%d",
			provider.repoGuardCalls, provider.listCalls, len(provider.fetchBatches))
	}
}

func TestPoll_RepoETag304SkipsListPRs(t *testing.T) {
	store := testStoreWithSession()
	provider := &fakeProvider{repoGuards: map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "v1", NotModified: true}}, observations: map[string]ports.SCMObservation{}}
	obs := newTestObserver(store, provider, &fakeLifecycle{}, time.Unix(1, 0).UTC())
	obs.Cache.RepoPRListETag[prKey(testRepo, 0)] = "v1"
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if provider.listCalls != 0 {
		t.Fatalf("ListOpenPRsByRepo called on 304: %d", provider.listCalls)
	}
}

func TestPoll_NoOpenPRsCommitsRepoETag(t *testing.T) {
	store := testStoreWithSession()
	provider := &fakeProvider{
		repoGuards:   map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "v2"}},
		openPRs:      map[string][]ports.SCMPRObservation{},
		observations: map[string]ports.SCMObservation{},
	}
	obs := newTestObserver(store, provider, &fakeLifecycle{}, time.Unix(1, 0).UTC())
	obs.Cache.RepoPRListETag[prKey(testRepo, 0)] = "v1"
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if provider.listCalls != 1 {
		t.Fatalf("ListOpenPRsByRepo calls = %d, want 1", provider.listCalls)
	}
	if got := obs.Cache.RepoPRListETag[prKey(testRepo, 0)]; got != "v2" {
		t.Fatalf("repo ETag after empty listing = %q, want v2", got)
	}
	if len(provider.fetchBatches) != 0 {
		t.Fatalf("empty listing should not fetch PR batch: %#v", provider.fetchBatches)
	}
}

func TestPoll_RepoETag200DiscoversPRAndRefreshesSamePoll(t *testing.T) {
	store := testStoreWithSession()
	provider := &fakeProvider{
		repoGuards:   map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "v2"}},
		openPRs:      map[string][]ports.SCMPRObservation{prKey(testRepo, 0): {{URL: "https://github.com/o/r/pull/1", Number: 1, SourceBranch: "feat", HeadRepo: "o/r", TargetBranch: "main", HeadSHA: "sha1"}}},
		observations: map[string]ports.SCMObservation{prKey(testRepo, 1): testObs(1)},
	}
	lc := &fakeLifecycle{}
	obs := newTestObserver(store, provider, lc, time.Unix(1, 0).UTC())
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if provider.listCalls != 1 {
		t.Fatalf("ListOpenPRsByRepo calls = %d, want 1", provider.listCalls)
	}
	if len(provider.fetchBatches) != 1 || len(provider.fetchBatches[0]) != 1 || provider.fetchBatches[0][0].Number != 1 {
		t.Fatalf("new PR not refreshed in same poll: %#v", provider.fetchBatches)
	}
	if len(store.writes) < 1 || len(lc.observed) != 1 {
		t.Fatalf("write/lifecycle missing: writes=%d lifecycle=%d", len(store.writes), len(lc.observed))
	}
}

// A session whose branch is the prefix of two open PRs (its root plus a stacked
// child on branch "feat/child") picks up both PRs in a single poll.
func TestPoll_DiscoversStackedChildByBranchPrefix(t *testing.T) {
	store := testStoreWithSession()
	childObs := testObs(2)
	childObs.PR.SourceBranch = "feat/child"
	childObs.PR.TargetBranch = "feat"
	provider := &fakeProvider{
		repoGuards: map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "v2"}},
		openPRs: map[string][]ports.SCMPRObservation{prKey(testRepo, 0): {
			{URL: "https://github.com/o/r/pull/1", Number: 1, SourceBranch: "feat", HeadRepo: "o/r", TargetBranch: "main", HeadSHA: "sha1"},
			{URL: "https://github.com/o/r/pull/2", Number: 2, SourceBranch: "feat/child", HeadRepo: "o/r", TargetBranch: "feat", HeadSHA: "sha2"},
		}},
		observations: map[string]ports.SCMObservation{prKey(testRepo, 1): testObs(1), prKey(testRepo, 2): childObs},
	}
	lc := &fakeLifecycle{}
	obs := newTestObserver(store, provider, lc, time.Unix(1, 0).UTC())
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	fetched := map[int]bool{}
	for _, batch := range provider.fetchBatches {
		for _, ref := range batch {
			fetched[ref.Number] = true
		}
	}
	if !fetched[1] || !fetched[2] {
		t.Fatalf("expected both root and stacked child fetched, got %#v", fetched)
	}
}

func TestPoll_DiscoversSiblingUnderRootSessionNamespace(t *testing.T) {
	store := testStoreWithSession()
	store.sessions[0].Metadata.Branch = "ao/p-1/root"
	prObs := testObs(1)
	prObs.PR.SourceBranch = "ao/p-1/fix"
	prObs.PR.TargetBranch = "main"
	provider := &fakeProvider{
		repoGuards: map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "v2"}},
		openPRs: map[string][]ports.SCMPRObservation{prKey(testRepo, 0): {
			{URL: "https://github.com/o/r/pull/1", Number: 1, SourceBranch: "ao/p-1/fix", HeadRepo: "o/r", TargetBranch: "main", HeadSHA: "sha1"},
		}},
		observations: map[string]ports.SCMObservation{prKey(testRepo, 1): prObs},
	}
	lc := &fakeLifecycle{}
	obs := newTestObserver(store, provider, lc, time.Unix(1, 0).UTC())
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.writes) == 0 {
		t.Fatal("expected discovered PR write")
	}
	if got := store.writes[0].pr.SourceBranch; got != "ao/p-1/fix" {
		t.Fatalf("source branch = %q, want ao/p-1/fix", got)
	}
	if got := store.writes[0].pr.SessionID; got != "p-1" {
		t.Fatalf("session id = %q, want p-1", got)
	}
	if len(lc.observed) != 1 {
		t.Fatalf("lifecycle observations = %d, want 1", len(lc.observed))
	}
}

// A session whose original branch already merged is restored and continues on a
// NEW branch in the same worktree, opening a NEW PR. The new branch does not
// descend from the stored (merged) session branch, so branch-snapshot attribution
// alone misses it and the card stays stuck at "merged" (the done bucket).
// Discovery must consult the worktree's live git branch so the new open PR is
// auto-claimed for the session — no manual `ao session claim-pr`.
func TestPoll_ClaimsNewPROnLiveWorktreeBranchAfterRestore(t *testing.T) {
	store := &fakeStore{
		sessions: []domain.SessionRecord{{ID: "p-1", ProjectID: "p", Metadata: domain.SessionMetadata{Branch: "ao/p-1/open-in-menu", WorkspacePath: "/wt/p-1"}}},
		projects: map[string]domain.ProjectRecord{"p": {ID: "p", RepoOriginURL: "https://github.com/o/r.git"}},
		prs: map[domain.SessionID][]domain.PullRequest{
			// The original PR merged: it is filtered out of refresh subjects yet keeps
			// the session in the merged/done bucket until a new open PR is attributed.
			"p-1": {{URL: "https://github.com/o/r/pull/10", SessionID: "p-1", Number: 10, Merged: true, SourceBranch: "ao/p-1/open-in-menu", Provider: "github", Host: "github.com", Repo: "o/r"}},
		},
		checks: map[string][]domain.PullRequestCheck{},
	}
	newObs := testObs(15)
	newObs.PR.SourceBranch = "ao/p-1/xcode-versioned-bundle"
	provider := &fakeProvider{
		repoGuards: map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "v2"}},
		openPRs: map[string][]ports.SCMPRObservation{prKey(testRepo, 0): {
			{URL: "https://github.com/o/r/pull/15", Number: 15, SourceBranch: "ao/p-1/xcode-versioned-bundle", HeadRepo: "o/r", TargetBranch: "main", HeadSHA: "sha15"},
		}},
		observations: map[string]ports.SCMObservation{prKey(testRepo, 15): newObs},
	}
	lc := &fakeLifecycle{}
	obs := New(provider, store, lc, Config{
		Clock:    func() time.Time { return time.Unix(1, 0).UTC() },
		Tick:     time.Hour,
		Logger:   quietSlog(),
		CacheMax: 128,
		WorktreeBranch: func(path string) string {
			if path == "/wt/p-1" {
				return "ao/p-1/xcode-versioned-bundle"
			}
			return ""
		},
	})
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	claimed := false
	for _, w := range store.writes {
		if w.pr.Number == 15 && w.pr.SessionID == "p-1" && w.pr.SourceBranch == "ao/p-1/xcode-versioned-bundle" {
			claimed = true
		}
	}
	if !claimed {
		t.Fatalf("new PR #15 on the live worktree branch was not auto-claimed for the session; writes=%#v", store.writes)
	}
	if len(lc.observed) != 1 {
		t.Fatalf("lifecycle observations = %d, want 1 (the newly claimed PR)", len(lc.observed))
	}
}

func TestPoll_DiscoversWorkspaceChildRepoPR(t *testing.T) {
	store := testStoreWithSession()
	store.sessions[0].Metadata.Branch = "ao/p-1/root"
	store.projects["p"] = domain.ProjectRecord{
		ID:            "p",
		RepoOriginURL: "https://github.com/o/r.git",
		Kind:          domain.ProjectKindWorkspace,
	}
	store.workspaceRepos = map[string][]domain.WorkspaceRepoRecord{
		"p": {{ProjectID: "p", Name: "api", RelativePath: "api", RepoOriginURL: "https://github.com/o/api.git"}},
	}
	prObs := testObs(12)
	prObs.Repo = "o/api"
	prObs.PR.URL = "https://github.com/o/api/pull/12"
	prObs.PR.HTMLURL = "https://github.com/o/api/pull/12"
	prObs.PR.Number = 12
	prObs.PR.SourceBranch = "ao/p-1/api-billing"
	prObs.PR.TargetBranch = "main"
	prObs.PR.HeadSHA = "api-sha"
	prObs.CI.HeadSHA = "api-sha"
	provider := &fakeProvider{
		repoGuards: map[string]ports.SCMGuardResult{
			prKey(testRepo, 0):    {ETag: "root-v2"},
			prKey(testAPIRepo, 0): {ETag: "api-v2"},
		},
		openPRs: map[string][]ports.SCMPRObservation{
			prKey(testAPIRepo, 0): {
				{URL: "https://github.com/o/api/pull/12", Number: 12, SourceBranch: "ao/p-1/api-billing", HeadRepo: "o/api", TargetBranch: "main", HeadSHA: "api-sha"},
			},
		},
		observations: map[string]ports.SCMObservation{prKey(testAPIRepo, 12): prObs},
	}
	lc := &fakeLifecycle{}
	obs := newTestObserver(store, provider, lc, time.Unix(1, 0).UTC())
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(provider.fetchBatches) != 1 || len(provider.fetchBatches[0]) != 1 {
		t.Fatalf("child repo PR not refreshed: %#v", provider.fetchBatches)
	}
	ref := provider.fetchBatches[0][0]
	if ref.Repo.Repo != "o/api" || ref.Number != 12 {
		t.Fatalf("fetched ref = %#v, want o/api#12", ref)
	}
	if len(store.writes) == 0 {
		t.Fatal("expected child repo PR write")
	}
	if got := store.writes[0].pr.Repo; got != "o/api" {
		t.Fatalf("persisted repo = %q, want o/api", got)
	}
	if got := store.writes[0].pr.SessionID; got != "p-1" {
		t.Fatalf("session id = %q, want p-1", got)
	}
	if len(lc.observed) != 1 {
		t.Fatalf("lifecycle observations = %d, want 1", len(lc.observed))
	}
}

func TestPoll_DiscoversWorkspaceChildRepoUpstreamPR(t *testing.T) {
	oldRemoteURLs := gitRemoteURLsFunc
	gitRemoteURLsFunc = func(path string) []string {
		if strings.HasSuffix(path, "/api") {
			return []string{"https://github.com/o/api.git", "https://github.com/upstream/api.git"}
		}
		return nil
	}
	defer func() { gitRemoteURLsFunc = oldRemoteURLs }()

	store := testStoreWithSession()
	store.sessions[0].Metadata.Branch = "ao/p-1/api-billing"
	store.projects["p"] = domain.ProjectRecord{
		ID:            "p",
		Path:          "/repo/workspace",
		RepoOriginURL: "https://github.com/o/root.git",
		Kind:          domain.ProjectKindWorkspace,
	}
	store.workspaceRepos = map[string][]domain.WorkspaceRepoRecord{
		"p": {{ProjectID: "p", Name: "api", RelativePath: "api", RepoOriginURL: "https://github.com/o/api.git"}},
	}
	upstreamRepo := ports.SCMRepo{Provider: "github", Host: "github.com", Owner: "upstream", Name: "api", Repo: "upstream/api"}
	prObs := testObs(44)
	prObs.Repo = "upstream/api"
	prObs.PR.URL = "https://github.com/upstream/api/pull/44"
	prObs.PR.HTMLURL = "https://github.com/upstream/api/pull/44"
	prObs.PR.Number = 44
	prObs.PR.SourceBranch = "ao/p-1/api-billing"
	prObs.PR.HeadRepo = "o/api"
	prObs.PR.TargetBranch = "main"
	prObs.PR.HeadSHA = "api-sha"
	prObs.CI.HeadSHA = "api-sha"
	provider := &fakeProvider{
		repoGuards: map[string]ports.SCMGuardResult{
			prKey(upstreamRepo, 0): {ETag: "upstream-v1"},
		},
		openPRs: map[string][]ports.SCMPRObservation{
			prKey(upstreamRepo, 0): {
				{URL: "https://github.com/upstream/api/pull/44", Number: 44, SourceBranch: "ao/p-1/api-billing", HeadRepo: "o/api", TargetBranch: "main", HeadSHA: "api-sha"},
			},
		},
		observations: map[string]ports.SCMObservation{prKey(upstreamRepo, 44): prObs},
	}
	lc := &fakeLifecycle{}
	obs := newTestObserver(store, provider, lc, time.Unix(1, 0).UTC())
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.writes) == 0 {
		t.Fatal("expected discovered upstream child PR write")
	}
	if got := store.writes[0].pr.Repo; got != "upstream/api" {
		t.Fatalf("written repo = %q, want upstream/api", got)
	}
	if got := store.writes[0].pr.SessionID; got != "p-1" {
		t.Fatalf("session id = %q, want p-1", got)
	}
}

// A PR whose head branch matches a session branch but lives in a fork (its head
// repo differs from the project repo) must not be auto-attributed: its commits
// are not the session's work. It is neither fetched nor persisted.
func TestPoll_IgnoresForkPRWithMatchingBranch(t *testing.T) {
	store := testStoreWithSession()
	provider := &fakeProvider{
		repoGuards:   map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "v2"}},
		openPRs:      map[string][]ports.SCMPRObservation{prKey(testRepo, 0): {{URL: "https://github.com/forker/r/pull/1", Number: 1, SourceBranch: "feat", HeadRepo: "forker/r", TargetBranch: "main", HeadSHA: "sha1"}}},
		observations: map[string]ports.SCMObservation{prKey(testRepo, 1): testObs(1)},
	}
	obs := newTestObserver(store, provider, &fakeLifecycle{}, time.Unix(1, 0).UTC())
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(provider.fetchBatches) != 0 {
		t.Fatalf("fork PR must not be fetched, got %#v", provider.fetchBatches)
	}
	if len(store.writes) != 0 {
		t.Fatalf("fork PR must not be persisted, got %d writes", len(store.writes))
	}
}

func mustGit(t *testing.T, args ...string) {
	t.Helper()
	if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v (%s)", args, err, out)
	}
}

// A PR opened from the fork push origin against an upstream base repo (the
// standard fork -> upstream contribution flow) must be discovered and attributed
// by scanning every remote in the project checkout, not just origin. Its head
// lives in origin (o/r) while its base lives in upstream (up/r); the persisted
// row records the upstream base repo so refresh refetches against it.
func TestPoll_DiscoversCrossForkPRFromUpstreamRemote(t *testing.T) {
	dir := t.TempDir()
	mustGit(t, "init", dir)
	mustGit(t, "-C", dir, "remote", "add", "origin", "https://github.com/o/r.git")
	mustGit(t, "-C", dir, "remote", "add", "upstream", "https://github.com/up/r.git")

	upstream := ports.SCMRepo{Provider: "github", Host: "github.com", Owner: "up", Name: "r", Repo: "up/r"}
	store := &fakeStore{
		sessions: []domain.SessionRecord{{ID: "p-1", ProjectID: "p", Metadata: domain.SessionMetadata{Branch: "ao/p-1/root"}}},
		projects: map[string]domain.ProjectRecord{"p": {ID: "p", Path: dir, RepoOriginURL: "https://github.com/o/r.git"}},
		prs:      map[domain.SessionID][]domain.PullRequest{},
		checks:   map[string][]domain.PullRequestCheck{},
	}
	crossObs := ports.SCMObservation{
		Fetched: true, Provider: "github", Host: "github.com", Repo: "up/r",
		PR:           ports.SCMPRObservation{URL: "https://github.com/up/r/pull/1", Number: 1, State: "open", SourceBranch: "ao/p-1/feat", HeadRepo: "o/r", TargetBranch: "main", HeadSHA: "sha1", Title: "PR"},
		CI:           ports.SCMCIObservation{Summary: string(domain.CIPassing), HeadSHA: "sha1"},
		Review:       ports.SCMReviewObservation{Decision: string(domain.ReviewNone)},
		Mergeability: ports.SCMMergeabilityObservation{State: string(domain.MergeMergeable), Mergeable: true},
	}
	provider := &fakeProvider{
		repoGuards: map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "origin"}, prKey(upstream, 0): {ETag: "up"}},
		openPRs: map[string][]ports.SCMPRObservation{
			prKey(upstream, 0): {{URL: "https://github.com/up/r/pull/1", Number: 1, SourceBranch: "ao/p-1/feat", HeadRepo: "o/r", TargetBranch: "main", HeadSHA: "sha1"}},
		},
		observations: map[string]ports.SCMObservation{prKey(upstream, 1): crossObs},
	}
	lc := &fakeLifecycle{}
	obs := newTestObserver(store, provider, lc, time.Unix(1, 0).UTC())
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.writes) == 0 {
		t.Fatal("expected cross-fork PR write")
	}
	got := store.writes[0].pr
	if got.SessionID != "p-1" {
		t.Fatalf("session id = %q, want p-1", got.SessionID)
	}
	if got.Repo != "up/r" {
		t.Fatalf("pr repo = %q, want up/r (upstream base)", got.Repo)
	}
	if got.SourceBranch != "ao/p-1/feat" {
		t.Fatalf("source branch = %q, want ao/p-1/feat", got.SourceBranch)
	}
	fetched := false
	for _, batch := range provider.fetchBatches {
		for _, ref := range batch {
			if ref.Repo.Repo == "up/r" && ref.Number == 1 {
				fetched = true
			}
		}
	}
	if !fetched {
		t.Fatalf("cross-fork PR must be refreshed against upstream, batches=%#v", provider.fetchBatches)
	}
}

// A PR on a scanned upstream remote whose head lives in some third-party fork
// (not this project's origin) must never be attributed, even though its branch
// name matches a session. Scanning extra remotes stays safe.
func TestPoll_IgnoresUpstreamPRFromForeignHead(t *testing.T) {
	dir := t.TempDir()
	mustGit(t, "init", dir)
	mustGit(t, "-C", dir, "remote", "add", "origin", "https://github.com/o/r.git")
	mustGit(t, "-C", dir, "remote", "add", "upstream", "https://github.com/up/r.git")

	upstream := ports.SCMRepo{Provider: "github", Host: "github.com", Owner: "up", Name: "r", Repo: "up/r"}
	store := &fakeStore{
		sessions: []domain.SessionRecord{{ID: "p-1", ProjectID: "p", Metadata: domain.SessionMetadata{Branch: "ao/p-1/root"}}},
		projects: map[string]domain.ProjectRecord{"p": {ID: "p", Path: dir, RepoOriginURL: "https://github.com/o/r.git"}},
		prs:      map[domain.SessionID][]domain.PullRequest{},
		checks:   map[string][]domain.PullRequestCheck{},
	}
	provider := &fakeProvider{
		repoGuards: map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "origin"}, prKey(upstream, 0): {ETag: "up"}},
		openPRs: map[string][]ports.SCMPRObservation{
			prKey(upstream, 0): {{URL: "https://github.com/up/r/pull/9", Number: 9, SourceBranch: "ao/p-1/feat", HeadRepo: "stranger/r", TargetBranch: "main", HeadSHA: "sha9"}},
		},
		observations: map[string]ports.SCMObservation{},
	}
	obs := newTestObserver(store, provider, &fakeLifecycle{}, time.Unix(1, 0).UTC())
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(provider.fetchBatches) != 0 {
		t.Fatalf("foreign-head upstream PR must not be fetched, got %#v", provider.fetchBatches)
	}
	if len(store.writes) != 0 {
		t.Fatalf("foreign-head upstream PR must not be persisted, got %d writes", len(store.writes))
	}
}

// A newly discovered open PR is persisted as a baseline row during discovery,
// before the refresh/lifecycle pass. This is what lets a same-poll terminal
// observation for a sibling PR see the open PR in the store and avoid completing
// the session prematurely. The persist holds even when the refresh fetch yields
// no observation for the new PR.
func TestPoll_DiscoveredPRPersistedAsBaselineBeforeRefresh(t *testing.T) {
	store := testStoreWithSession()
	provider := &fakeProvider{
		repoGuards:   map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "v2"}},
		openPRs:      map[string][]ports.SCMPRObservation{prKey(testRepo, 0): {{URL: "https://github.com/o/r/pull/1", Number: 1, SourceBranch: "feat", HeadRepo: "o/r", TargetBranch: "main", HeadSHA: "sha1"}}},
		observations: map[string]ports.SCMObservation{}, // refresh returns nothing
	}
	obs := newTestObserver(store, provider, &fakeLifecycle{}, time.Unix(1, 0).UTC())
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	var baseline *domain.PullRequest
	for i := range store.writes {
		if store.writes[i].pr.Number == 1 {
			baseline = &store.writes[i].pr
			break
		}
	}
	if baseline == nil {
		t.Fatalf("discovered PR #1 not persisted as a baseline row; writes=%#v", store.writes)
		return
	}
	if baseline.Merged || baseline.Closed {
		t.Fatalf("baseline row must be open, got merged=%v closed=%v", baseline.Merged, baseline.Closed)
	}
}

func TestPoll_CIETagChangeRefreshesWhenRepoUnchanged(t *testing.T) {
	store := testStoreWithSession()
	store.prs["p-1"] = []domain.PullRequest{knownPR(1)}
	provider := &fakeProvider{
		repoGuards:   map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "repo", NotModified: true}},
		checkGuards:  map[string]ports.SCMGuardResult{commitKey(testRepo, "sha1"): {ETag: "ci2"}},
		observations: map[string]ports.SCMObservation{prKey(testRepo, 1): testObs(1)},
	}
	obs := newTestObserver(store, provider, &fakeLifecycle{}, time.Unix(2, 0).UTC())
	obs.Cache.RepoPRListETag[prKey(testRepo, 0)] = "repo"
	obs.Cache.CommitChecksETag[commitKey(testRepo, "sha1")] = "ci1"
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(provider.fetchBatches) != 1 {
		t.Fatalf("CI ETag 200 should trigger batch fetch, got %d", len(provider.fetchBatches))
	}
}

// pendingObs models GitHub's GraphQL statusCheckRollup while a commit's checks
// have NOT yet been aggregated: rollup state=PENDING, zero context nodes. This
// is the shape that produced the observed DB row (ci=pending, checks=0,
// mergeability=unstable) for the stuck PR.
func pendingObs(num int) ports.SCMObservation {
	o := testObs(num)
	o.CI = ports.SCMCIObservation{Summary: string(domain.CIPending), HeadSHA: "sha" + fmt.Sprint(num)}
	o.Mergeability = ports.SCMMergeabilityObservation{State: string(domain.MergeUnstable)}
	return o
}

// knownPendingPR is the local baseline persisted from a first observation that
// landed before the rollup populated (pending / zero checks).
func knownPendingPR(num int) domain.PullRequest {
	pr, _, _, _, _ := domainFromObservation("p-1", pendingObs(num), domain.PullRequest{}, persistenceOptions{}, time.Unix(1, 0).UTC())
	return pr
}

func lastWriteForPR(store *fakeStore, num int) *domain.PullRequest {
	store.mu.Lock()
	defer store.mu.Unlock()
	var out *domain.PullRequest
	for i := range store.writes {
		if store.writes[i].pr.Number == num {
			cp := store.writes[i].pr
			out = &cp
		}
	}
	return out
}

// TestPoll_CIStuckPendingWhenRollupLagsBehindCheckRunsETag reproduces the
// "CI stuck at pending forever" bug (board: "ci checks stuck pending").
//
// Root cause: the observer gates its GraphQL statusCheckRollup re-fetch on the
// REST /commits/{sha}/check-runs ETag (CommitChecksGuard). Those are two
// independent GitHub subsystems. When the REST check-runs list reaches its
// terminal state (all runs completed) the ETag stabilizes, but GitHub's GraphQL
// rollup can still report PENDING / zero contexts for a short window afterward.
// If a tick observes the stabilized check-runs ETag while the rollup still lags,
// the observer commits the terminal ETag (skip-write path, nothing changed) and
// from then on the guard returns 304 forever — so the rollup's later
// PENDING->SUCCESS transition (which does NOT alter the check-runs list, hence
// no ETag change) is never re-read. CI freezes at pending.
//
// This test asserts the CORRECT behavior (observer should eventually persist the
// passing CI). It currently FAILS (red), demonstrating the frozen state.
func TestPoll_CIStuckPendingWhenRollupLagsBehindCheckRunsETag(t *testing.T) {
	key := prKey(testRepo, 1)
	ck := commitKey(testRepo, "sha1")

	store := testStoreWithSession()
	store.prs["p-1"] = []domain.PullRequest{knownPendingPR(1)}

	provider := &fakeProvider{
		// Quiet repo: no open-PR-list change, so the repo guard never forces a refetch.
		repoGuards: map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "repo", NotModified: true}},
		// Tick 1: REST check-runs has advanced to its terminal ETag (differs from cache).
		checkGuards: map[string]ports.SCMGuardResult{ck: {ETag: "ci-final", NotModified: false}},
		// ...but the GraphQL rollup still lags: pending / zero checks (== local baseline).
		observations: map[string]ports.SCMObservation{key: pendingObs(1)},
	}

	obs := newTestObserver(store, provider, &fakeLifecycle{}, time.Unix(2, 0).UTC())
	obs.Cache.RepoPRListETag[prKey(testRepo, 0)] = "repo"
	obs.Cache.CommitChecksETag[ck] = "ci-pending"           // cached from the first (pending) observation
	obs.Cache.LastReviewPollAt[key] = time.Unix(2, 0).UTC() // isolate: suppress review re-poll noise

	// Tick 1: check-runs ETag changed -> PR is a candidate -> GraphQL fetched ->
	// rollup still pending (== local) -> write skipped -> "ci-final" ETag committed.
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(provider.fetchBatches) != 1 {
		t.Fatalf("tick 1: expected the changed check-runs ETag to trigger one GraphQL fetch, got %d", len(provider.fetchBatches))
	}

	// The rollup now catches up to the true state: passing, with real checks.
	// The REST check-runs list is UNCHANGED (all runs already completed), so its
	// ETag stays "ci-final" and the guard returns 304.
	provider.mu.Lock()
	provider.observations[key] = testObs(1) // passing + 1 check
	provider.checkGuards[ck] = ports.SCMGuardResult{ETag: "ci-final", NotModified: true}
	provider.mu.Unlock()

	fetchesBeforeTick2 := len(provider.fetchBatches)

	// Tick 2: SCM truly reports passing now. A correct observer must re-read and
	// persist it. The bug: nothing re-triggers the fetch, so CI stays pending.
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}

	if len(provider.fetchBatches) == fetchesBeforeTick2 {
		t.Fatalf("BUG (ci-checks-stuck-pending): observer never re-fetched PR #1 after its checks completed; "+
			"CI is frozen at pending because the check-runs ETag stabilized before the GraphQL rollup did "+
			"(fetch batches stayed at %d)", fetchesBeforeTick2)
	}

	last := lastWriteForPR(store, 1)
	if last == nil {
		t.Fatalf("BUG (ci-checks-stuck-pending): no write persisted PR #1's completed CI; it is stuck at pending")
	}
	if last.CI != domain.CIPassing {
		t.Fatalf("BUG (ci-checks-stuck-pending): CI stuck at %q; SCM reports passing but the observer never re-read the rollup", last.CI)
	}
}

// conflictingObs models a PR whose provider mergeability reads "conflicting"
// (a real conflict against a moved base branch), CI otherwise passing.
func conflictingObs(num int) ports.SCMObservation {
	o := testObs(num)
	o.Mergeability = ports.SCMMergeabilityObservation{State: string(domain.MergeConflicting), Conflict: true, Blockers: []string{"conflicts"}}
	return o
}

// knownConflictingPR is the local baseline persisted from an earlier observation
// when the conflict was real (mergeability=conflicting), with every local-state
// field set so missingLocalState is false — the precondition for the freeze.
func knownConflictingPR(num int) domain.PullRequest {
	pr, _, _, _, _ := domainFromObservation("p-1", conflictingObs(num), domain.PullRequest{}, persistenceOptions{}, time.Unix(1, 0).UTC())
	return pr
}

// TestPoll_MergeabilityStuckConflictingWhenGuardsQuiet reproduces the stale
// "Merge Conflict" bug (board: "gl mergeable stale and false nudge").
//
// A stacked GitLab MR briefly reports a real conflict after its base branch
// moves; AO observes conflicting and persists it. The worker then rebases and
// force-pushes, and GitLab recomputes the MR to mergeable. But that
// conflict->mergeable transition changes neither the open-MR-list ETag
// (RepoPRListGuard) nor the per-commit pipelines ETag (CommitChecksGuard) — the
// same guard-decoupling class as the CI-stuck-pending bug — so both guards keep
// returning 304, the MR is never a refresh candidate, and its mergeability
// freezes at "conflicting" forever even though GitLab now reports it mergeable.
//
// This test asserts the CORRECT behavior: the observer must eventually re-read
// the MR and persist the mergeable state. It currently FAILS (red): with the
// guards quiet and CI already terminal, nothing forces a refetch.
func TestPoll_MergeabilityStuckConflictingWhenGuardsQuiet(t *testing.T) {
	key := prKey(testRepo, 1)
	ck := commitKey(testRepo, "sha1")

	store := testStoreWithSession()
	store.prs["p-1"] = []domain.PullRequest{knownConflictingPR(1)}

	provider := &fakeProvider{
		// Quiet repo: the open-MR-list ETag never changes (our MR is not the
		// newest-created, so a per_page=1 list guard cannot see its update).
		repoGuards: map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "repo", NotModified: true}},
		// CI is already terminal (passing), so the per-commit pipelines ETag is
		// stable too — the guard returns 304.
		checkGuards: map[string]ports.SCMGuardResult{ck: {ETag: "ci1", NotModified: true}},
		// If the observer DOES re-read the MR, GitLab now reports it mergeable.
		observations: map[string]ports.SCMObservation{key: testObs(1)},
	}

	lc := &fakeLifecycle{}
	obs := newTestObserver(store, provider, lc, time.Unix(2, 0).UTC())
	obs.Cache.RepoPRListETag[prKey(testRepo, 0)] = "repo"
	obs.Cache.CommitChecksETag[ck] = "ci1"
	obs.Cache.LastReviewPollAt[key] = time.Unix(2, 0).UTC() // isolate: suppress review re-poll noise

	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}

	if len(provider.fetchBatches) == 0 {
		t.Fatalf("BUG (gl-mergeable-stale): observer never re-fetched the conflicting MR; " +
			"its mergeability is frozen at conflicting because both ETag guards stayed 304 " +
			"across the conflict->mergeable transition")
	}
	last := lastWriteForPR(store, 1)
	if last == nil {
		t.Fatalf("BUG (gl-mergeable-stale): no write persisted the MR's recomputed mergeability; it is stuck at conflicting")
	}
	if last.Mergeability != domain.MergeMergeable {
		t.Fatalf("BUG (gl-mergeable-stale): mergeability stuck at %q; GitLab reports mergeable but the observer never re-read it", last.Mergeability)
	}
	// The fresh mergeable observation must reach lifecycle so the merge-conflict
	// nudge is not (re-)raised from a stale conflicting value.
	if len(lc.observed) == 0 || domain.Mergeability(lc.observed[len(lc.observed)-1].Mergeability.State) != domain.MergeMergeable {
		t.Fatalf("BUG (gl-mergeable-stale): lifecycle did not receive the cleared mergeable observation: %#v", lc.observed)
	}
}

func TestPoll_GraphQLBatchChunksAt25(t *testing.T) {
	store := &fakeStore{projects: map[string]domain.ProjectRecord{"p": {ID: "p", RepoOriginURL: "https://github.com/o/r.git"}}, prs: map[domain.SessionID][]domain.PullRequest{}, checks: map[string][]domain.PullRequestCheck{}}
	provider := &fakeProvider{repoGuards: map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "repo"}}, observations: map[string]ports.SCMObservation{}}
	for i := 1; i <= 26; i++ {
		id := domain.SessionID("p-" + fmt.Sprint(i))
		store.sessions = append(store.sessions, domain.SessionRecord{ID: id, ProjectID: "p", Metadata: domain.SessionMetadata{Branch: "b" + fmt.Sprint(i)}})
		pr := knownPR(i)
		pr.SessionID = id
		pr.MetadataHash = "" // force candidate
		store.prs[id] = []domain.PullRequest{pr}
		provider.observations[prKey(testRepo, i)] = testObs(i)
	}
	obs := newTestObserver(store, provider, &fakeLifecycle{}, time.Unix(1, 0).UTC())
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(provider.fetchBatches) != 2 || len(provider.fetchBatches[0]) != 25 || len(provider.fetchBatches[1]) != 1 {
		t.Fatalf("batch sizes = %#v", provider.fetchBatches)
	}
}

func TestPoll_FailingCIFetchesLogTailOnlyWhenFingerprintChanged(t *testing.T) {
	failing := testObs(1)
	failing.CI.Summary = string(domain.CIFailing)
	failing.CI.Checks = []ports.SCMCheckObservation{{Name: "build", Status: string(domain.PRCheckFailed), Conclusion: "failure", ProviderID: "99"}}
	failing.CI.FailedChecks = failing.CI.Checks
	failing.CI.FailedFingerprint = "fp"

	store := testStoreWithSession()
	local := knownPR(1)
	local.CIHash = "old"
	store.prs["p-1"] = []domain.PullRequest{local}
	provider := &fakeProvider{repoGuards: map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "repo"}}, checkGuards: map[string]ports.SCMGuardResult{commitKey(testRepo, "sha1"): {ETag: "ci2"}}, observations: map[string]ports.SCMObservation{prKey(testRepo, 1): failing}, logTails: map[string]string{"build": "tail"}}
	obs := newTestObserver(store, provider, &fakeLifecycle{}, time.Unix(1, 0).UTC())
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if provider.logCalls != 1 {
		t.Fatalf("log calls = %d, want 1", provider.logCalls)
	}

	provider.logCalls = 0
	store.writes = nil
	withTail := failing
	withTail.CI.Checks[0].LogTail = "tail"
	withTail.CI.FailedChecks[0].LogTail = "tail"
	withTail.CI.FailureLogTail = "tail"
	local.CIHash = ciSemanticHash(withTail.CI)
	store.prs["p-1"] = []domain.PullRequest{local}
	store.checks[local.URL] = []domain.PullRequestCheck{{Name: "build", Status: domain.PRCheckFailed, LogTail: "tail"}}
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if provider.logCalls != 0 {
		t.Fatalf("unchanged fingerprint fetched logs again: %d", provider.logCalls)
	}
	if len(store.writes) != 0 {
		t.Fatalf("unchanged failed fingerprint with stored log tail should not write, got %d writes", len(store.writes))
	}
}

func TestEnrichFailureLogsDoesNotRefetchExistingTailOrMissingProviderID(t *testing.T) {
	obsValue := testObs(1)
	obsValue.CI.Summary = string(domain.CIFailing)
	obsValue.CI.FailedFingerprint = "fp"
	obsValue.CI.Checks = []ports.SCMCheckObservation{
		{Name: "build", Status: string(domain.PRCheckFailed), Conclusion: "failure", ProviderID: "99", LogTail: "provider supplied tail"},
		{Name: "lint", Status: string(domain.PRCheckFailed), Conclusion: "failure"},
	}
	obsValue.CI.FailedChecks = append([]ports.SCMCheckObservation(nil), obsValue.CI.Checks...)

	provider := &fakeProvider{logTails: map[string]string{"build": "fetched tail", "lint": "should not fetch"}}
	obs := newTestObserver(testStoreWithSession(), provider, &fakeLifecycle{}, time.Unix(1, 0).UTC())
	obs.enrichFailureLogs(context.Background(), &obsValue, domain.PullRequest{})

	if provider.logCalls != 0 {
		t.Fatalf("log calls = %d, want 0 when tail already exists or provider id is missing", provider.logCalls)
	}
	if got := obsValue.CI.FailedChecks[0].LogTail; got != "provider supplied tail" {
		t.Fatalf("existing tail changed: got %q", got)
	}
	if got := obsValue.CI.FailedChecks[1].LogTail; got != "" {
		t.Fatalf("tail without provider id = %q, want empty", got)
	}
	if got := obsValue.CI.FailureLogTail; got != "provider supplied tail" {
		t.Fatalf("FailureLogTail = %q, want only existing tail", got)
	}
}

func TestPoll_ReviewPollingRespectsInterval(t *testing.T) {
	store := testStoreWithSession()
	local := knownPR(1)
	local.Review = domain.ReviewChangesRequest
	local.ReviewHash = "old-review"
	store.prs["p-1"] = []domain.PullRequest{local}
	provider := &fakeProvider{repoGuards: map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "repo", NotModified: true}}, observations: map[string]ports.SCMObservation{}, reviews: map[string]ports.SCMReviewObservation{prKey(testRepo, 1): {Decision: string(domain.ReviewChangesRequest), Threads: []ports.SCMReviewThreadObservation{{ID: "t1", Path: "f.go", Line: 1, Comments: []ports.SCMReviewCommentObservation{{ID: "c1", Body: "fix"}}}}}}}
	obs := newTestObserver(store, provider, &fakeLifecycle{}, time.Unix(120, 0).UTC())
	obs.Cache.RepoPRListETag[prKey(testRepo, 0)] = "repo"
	obs.Cache.LastReviewPollAt[prKey(testRepo, 1)] = time.Unix(90, 0).UTC()
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if provider.reviewCalls != 0 {
		t.Fatalf("review fetched before interval: %d", provider.reviewCalls)
	}
	obs.Cache.LastReviewPollAt[prKey(testRepo, 1)] = time.Unix(0, 0).UTC()
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if provider.reviewCalls != 1 {
		t.Fatalf("review not fetched after interval: %d", provider.reviewCalls)
	}
	if len(store.writes) == 0 || store.writes[0].reviewMode != ports.ReviewWriteReplace {
		t.Fatalf("review refresh not persisted with replace mode: %#v", store.writes)
	}
}

// TestPoll_ReviewPollingPeriodicForNonChangesRequested locks in that review
// threads are re-polled on the review interval for ANY open PR, not only when
// the decision is "changes requested". Review threads (unresolved comments,
// resolutions) change with no accompanying metadata/CI change, and providers
// like GitLab never surface a changes_requested decision — so gating periodic
// re-polling on that decision froze GitLab MR review state after the first
// fetch (an added comment never appeared).
func TestPoll_ReviewPollingPeriodicForNonChangesRequested(t *testing.T) {
	store := testStoreWithSession()
	local := knownPR(1)
	local.Review = domain.ReviewNone
	local.ReviewHash = "old-review"
	store.prs["p-1"] = []domain.PullRequest{local}
	// Repo guard NotModified => no metadata refresh; isolates the review re-poll.
	provider := &fakeProvider{repoGuards: map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "repo", NotModified: true}}, observations: map[string]ports.SCMObservation{}, reviews: map[string]ports.SCMReviewObservation{prKey(testRepo, 1): {Decision: string(domain.ReviewNone), Threads: []ports.SCMReviewThreadObservation{{ID: "t1", Path: "f.go", Line: 1, Comments: []ports.SCMReviewCommentObservation{{ID: "c1", Body: "please fix"}}}}}}}
	obs := newTestObserver(store, provider, &fakeLifecycle{}, time.Unix(1000, 0).UTC())
	obs.Cache.RepoPRListETag[prKey(testRepo, 0)] = "repo"
	// Last poll well beyond the 120s review interval.
	obs.Cache.LastReviewPollAt[prKey(testRepo, 1)] = time.Unix(800, 0).UTC()
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if provider.reviewCalls != 1 {
		t.Fatalf("review not re-polled for a non-changes_requested PR past the interval: reviewCalls=%d", provider.reviewCalls)
	}
}

func TestPoll_UnchangedHashesDoNotWriteOrNotify(t *testing.T) {
	store := testStoreWithSession()
	obsValue := testObs(1)
	local := knownPR(1)
	local.MetadataHash = metadataSemanticHash(obsValue)
	local.CIHash = ciSemanticHash(obsValue.CI)
	local.ReviewHash = reviewSemanticHash(obsValue.Review)
	store.prs["p-1"] = []domain.PullRequest{local}
	provider := &fakeProvider{repoGuards: map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "repo"}}, observations: map[string]ports.SCMObservation{prKey(testRepo, 1): obsValue}}
	lc := &fakeLifecycle{}
	obs := newTestObserver(store, provider, lc, time.Unix(1, 0).UTC())
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.writes) != 0 || len(lc.observed) != 0 {
		t.Fatalf("unchanged hashes wrote/notified: writes=%d observed=%d", len(store.writes), len(lc.observed))
	}
}

func TestPoll_ReviewHashDrivesPersistenceAndLifecycle(t *testing.T) {
	store := testStoreWithSession()
	local := knownPR(1)
	local.ReviewHash = "old"
	local.Review = domain.ReviewChangesRequest
	store.prs["p-1"] = []domain.PullRequest{local}
	review := ports.SCMReviewObservation{
		Decision: string(domain.ReviewChangesRequest),
		Reviews:  []ports.SCMReviewSummaryObservation{{ID: "review-1", Author: "ann", State: string(domain.ReviewChangesRequest), URL: "https://github.com/o/r/pull/1#pullrequestreview-1", SubmittedAt: time.Unix(199, 0).UTC()}},
		Threads:  []ports.SCMReviewThreadObservation{{ID: "t1", Path: "f.go", Line: 2, Comments: []ports.SCMReviewCommentObservation{{ID: "c1", Author: "ann", Body: "fix this"}}}},
	}
	provider := &fakeProvider{repoGuards: map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "repo", NotModified: true}}, observations: map[string]ports.SCMObservation{}, reviews: map[string]ports.SCMReviewObservation{prKey(testRepo, 1): review}}
	lc := &fakeLifecycle{}
	obs := newTestObserver(store, provider, lc, time.Unix(200, 0).UTC())
	obs.Cache.RepoPRListETag[prKey(testRepo, 0)] = "repo"
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.writes) == 0 || len(store.writes[0].comments) != 1 {
		t.Fatalf("review change not persisted: %#v", store.writes)
	}
	if len(store.writes[0].reviews) != 1 || store.writes[0].reviews[0].URL != "https://github.com/o/r/pull/1#pullrequestreview-1" {
		t.Fatalf("review summaries not persisted: %#v", store.writes[0].reviews)
	}
	if len(store.writes) != 2 {
		t.Fatalf("review change with lifecycle should write held-back facts then acknowledgement, got %d writes", len(store.writes))
	}
	if store.writes[0].reviewMode != ports.ReviewWriteReplace {
		t.Fatalf("initial review write mode = %v, want replace", store.writes[0].reviewMode)
	}
	if store.writes[1].reviewMode != ports.ReviewWritePreserve || len(store.writes[1].comments) != 0 {
		t.Fatalf("lifecycle acknowledgement should preserve review rows, got mode=%v comments=%d", store.writes[1].reviewMode, len(store.writes[1].comments))
	}
	if len(lc.observed) != 1 || !lc.observed[0].Changed.Review {
		t.Fatalf("review change not notified: %#v", lc.observed)
	}
}

func TestPoll_ApprovalsCountChangeRoundTripsThroughRefreshReviews(t *testing.T) {
	store := testStoreWithSession()
	local := knownPR(1)
	local.ReviewHash = "old"
	local.Review = domain.ReviewChangesRequest
	local.ApprovalsCount = 2
	local.ApprovalRuleConfigured = true
	store.prs["p-1"] = []domain.PullRequest{local}
	review := ports.SCMReviewObservation{
		Decision:               string(domain.ReviewChangesRequest),
		ApprovalsCount:         3,
		ApprovalRuleConfigured: false,
	}
	provider := &fakeProvider{repoGuards: map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "repo", NotModified: true}}, observations: map[string]ports.SCMObservation{}, reviews: map[string]ports.SCMReviewObservation{prKey(testRepo, 1): review}}
	lc := &fakeLifecycle{}
	obs := newTestObserver(store, provider, lc, time.Unix(200, 0).UTC())
	obs.Cache.RepoPRListETag[prKey(testRepo, 0)] = "repo"
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.writes) == 0 {
		t.Fatalf("approvals-count change not persisted: %#v", store.writes)
	}
	if store.writes[0].pr.ApprovalsCount != 3 {
		t.Fatalf("persisted PR ApprovalsCount = %d, want 3 (refreshReviews must copy review.ApprovalsCount onto obs.Review)", store.writes[0].pr.ApprovalsCount)
	}
	if store.writes[0].pr.ApprovalRuleConfigured {
		t.Fatalf("persisted PR ApprovalRuleConfigured = true, want false (refreshReviews must copy review.ApprovalRuleConfigured onto obs.Review)")
	}
	if len(lc.observed) != 1 || !lc.observed[0].Changed.Review {
		t.Fatalf("approvals count change from 2 to 3 must flip Changed.Review: %#v", lc.observed)
	}
	if lc.observed[0].Review.ApprovalsCount != 3 {
		t.Fatalf("observation delivered to lifecycle has ApprovalsCount = %d, want 3", lc.observed[0].Review.ApprovalsCount)
	}
}

func TestPoll_PartialReviewRefreshUsesMergeMode(t *testing.T) {
	store := testStoreWithSession()
	local := knownPR(1)
	local.ReviewHash = "old"
	local.Review = domain.ReviewChangesRequest
	store.prs["p-1"] = []domain.PullRequest{local}
	review := ports.SCMReviewObservation{
		Decision: string(domain.ReviewChangesRequest),
		Partial:  true,
		Threads:  []ports.SCMReviewThreadObservation{{ID: "t1", Path: "f.go", Line: 2, Comments: []ports.SCMReviewCommentObservation{{ID: "c1", Author: "ann", Body: "fix this"}}}},
	}
	provider := &fakeProvider{
		repoGuards: map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "repo", NotModified: true}},
		reviews:    map[string]ports.SCMReviewObservation{prKey(testRepo, 1): review},
	}
	obs := newTestObserver(store, provider, nil, time.Unix(210, 0).UTC())
	obs.Cache.RepoPRListETag[prKey(testRepo, 0)] = "repo"
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.writes) != 1 {
		t.Fatalf("writes = %#v, want one partial review merge", store.writes)
	}
	if store.writes[0].reviewMode != ports.ReviewWriteMerge {
		t.Fatalf("review mode = %v, want merge", store.writes[0].reviewMode)
	}
	if store.writes[0].pr.ReviewHash != reviewSemanticHash(review) {
		t.Fatalf("review hash = %q, want partial hash %q", store.writes[0].pr.ReviewHash, reviewSemanticHash(review))
	}
}

func TestPoll_ReviewOnlyRefreshPreservesLocalCIAndMetadata(t *testing.T) {
	store := testStoreWithSession()
	localObs := testObs(1)
	local := knownPR(1)
	local.CI = domain.CIPassing
	local.Review = domain.ReviewChangesRequest
	local.ReviewHash = "old-review"
	local.MetadataHash = metadataSemanticHash(localObs)
	local.CIHash = ciSemanticHash(localObs.CI)
	local.ObservedAt = time.Unix(10, 0).UTC()
	local.CIObservedAt = time.Unix(11, 0).UTC()
	local.ReviewObservedAt = time.Unix(12, 0).UTC()
	store.prs["p-1"] = []domain.PullRequest{local}
	store.checks[local.URL] = []domain.PullRequestCheck{
		{Name: "build", CommitHash: "sha1", Status: domain.PRCheckPassed, Conclusion: "success", URL: "ci"},
		{Name: "stale", CommitHash: "old-sha", Status: domain.PRCheckFailed, Conclusion: "failure", URL: "old-ci", LogTail: "old tail"},
	}
	review := ports.SCMReviewObservation{
		Decision: string(domain.ReviewChangesRequest),
		Threads:  []ports.SCMReviewThreadObservation{{ID: "t1", Path: "f.go", Line: 2, Comments: []ports.SCMReviewCommentObservation{{ID: "c1", Author: "ann", Body: "fix"}}}},
	}
	provider := &fakeProvider{
		repoGuards: map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "repo", NotModified: true}},
		reviews:    map[string]ports.SCMReviewObservation{prKey(testRepo, 1): review},
	}
	now := time.Unix(200, 0).UTC()
	obs := newTestObserver(store, provider, &fakeLifecycle{}, now)
	obs.Cache.RepoPRListETag[prKey(testRepo, 0)] = "repo"
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.writes) == 0 {
		t.Fatalf("writes = %d, want review-only write", len(store.writes))
	}
	write := store.writes[len(store.writes)-1]
	if write.pr.MetadataHash != local.MetadataHash {
		t.Fatalf("metadata hash changed on review-only refresh: got %q want %q", write.pr.MetadataHash, local.MetadataHash)
	}
	if write.pr.CIHash != local.CIHash {
		t.Fatalf("CI hash changed on review-only refresh: got %q want %q", write.pr.CIHash, local.CIHash)
	}
	if !write.pr.ObservedAt.Equal(local.ObservedAt) {
		t.Fatalf("ObservedAt changed on review-only refresh: got %s want %s", write.pr.ObservedAt, local.ObservedAt)
	}
	if !write.pr.CIObservedAt.Equal(local.CIObservedAt) {
		t.Fatalf("CIObservedAt changed on review-only refresh: got %s want %s", write.pr.CIObservedAt, local.CIObservedAt)
	}
	if !write.pr.ReviewObservedAt.Equal(now) {
		t.Fatalf("ReviewObservedAt = %s, want %s", write.pr.ReviewObservedAt, now)
	}
	if len(write.checks) != 1 || write.checks[0].Name != "build" {
		t.Fatalf("review-only local reconstruction should include current-head checks only: %#v", write.checks)
	}
}

func TestPoll_ReviewFetchFailureDoesNotUpdateReviewDecision(t *testing.T) {
	store := testStoreWithSession()
	local := knownPR(1)
	local.Review = domain.ReviewChangesRequest
	local.ReviewHash = reviewSemanticHash(ports.SCMReviewObservation{Decision: string(domain.ReviewChangesRequest), Threads: []ports.SCMReviewThreadObservation{{ID: "old", Comments: []ports.SCMReviewCommentObservation{{ID: "c-old", Body: "old"}}}}})
	obsValue := testObs(1)
	obsValue.Review.Decision = string(domain.ReviewApproved)
	local.MetadataHash = metadataSemanticHash(obsValue)
	local.CIHash = ciSemanticHash(obsValue.CI)
	store.prs["p-1"] = []domain.PullRequest{local}
	provider := &fakeProvider{
		repoGuards:   map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "repo2"}},
		observations: map[string]ports.SCMObservation{prKey(testRepo, 1): obsValue},
		reviewErr:    errors.New("review API down"),
	}
	lc := &fakeLifecycle{}
	obs := newTestObserver(store, provider, lc, time.Unix(300, 0).UTC())
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if provider.reviewCalls != 1 {
		t.Fatalf("reviewCalls = %d, want 1", provider.reviewCalls)
	}
	if len(store.writes) != 0 || len(lc.observed) != 0 {
		t.Fatalf("review fetch failure should not persist/notify stale review data: writes=%#v lifecycle=%#v", store.writes, lc.observed)
	}
	if !obs.Cache.ReviewRefreshFailed[prKey(testRepo, 1)] {
		t.Fatalf("review fetch failure was not marked for retry")
	}
}

func TestPoll_SuccessfulReviewRefreshClearsRetryCacheSlot(t *testing.T) {
	store := testStoreWithSession()
	local := knownPR(1)
	local.Review = domain.ReviewChangesRequest
	local.ReviewHash = "old-review"
	store.prs["p-1"] = []domain.PullRequest{local}
	review := ports.SCMReviewObservation{
		Decision: string(domain.ReviewChangesRequest),
		Threads:  []ports.SCMReviewThreadObservation{{ID: "t1", Path: "f.go", Line: 2, Comments: []ports.SCMReviewCommentObservation{{ID: "c1", Body: "fix"}}}},
	}
	provider := &fakeProvider{
		repoGuards: map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "repo", NotModified: true}},
		reviews:    map[string]ports.SCMReviewObservation{prKey(testRepo, 1): review},
	}
	obs := newTestObserver(store, provider, nil, time.Unix(350, 0).UTC())
	obs.Cache.RepoPRListETag[prKey(testRepo, 0)] = "repo"
	obs.cacheSetBool(obs.Cache.ReviewRefreshFailed, &obs.Cache.reviewFailedOrder, prKey(testRepo, 1), true)

	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok := obs.Cache.ReviewRefreshFailed[prKey(testRepo, 1)]; ok {
		t.Fatalf("successful review refresh should delete retry map entry, got %#v", obs.Cache.ReviewRefreshFailed)
	}
	for _, key := range obs.Cache.reviewFailedOrder {
		if key == prKey(testRepo, 1) {
			t.Fatalf("successful review refresh should remove retry order slot, got %#v", obs.Cache.reviewFailedOrder)
		}
	}
}

func TestPoll_DoesNotCommitCommitETagWhenFetchFails(t *testing.T) {
	store := testStoreWithSession()
	local := knownPR(1)
	store.prs["p-1"] = []domain.PullRequest{local}
	provider := &fakeProvider{
		repoGuards:  map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "repo", NotModified: true}},
		checkGuards: map[string]ports.SCMGuardResult{commitKey(testRepo, "sha1"): {ETag: "ci2"}},
		fetchErr:    errors.New("graphql down"),
	}
	obs := newTestObserver(store, provider, &fakeLifecycle{}, time.Unix(400, 0).UTC())
	obs.Cache.RepoPRListETag[prKey(testRepo, 0)] = "repo"
	obs.Cache.CommitChecksETag[commitKey(testRepo, "sha1")] = "ci1"
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := obs.Cache.CommitChecksETag[commitKey(testRepo, "sha1")]; got != "ci1" {
		t.Fatalf("commit ETag advanced after failed fetch: got %q want ci1", got)
	}
}

func TestPoll_LifecycleFailureHoldsBackHashesForDurableRetry(t *testing.T) {
	store := testStoreWithSession()
	local := knownPR(1)
	local.MetadataHash = "old-metadata"
	local.CIHash = "old-ci"
	store.prs["p-1"] = []domain.PullRequest{local}
	changed := testObs(1)
	changed.PR.Title = "changed title"
	provider := &fakeProvider{
		repoGuards:   map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "repo2"}},
		checkGuards:  map[string]ports.SCMGuardResult{commitKey(testRepo, "sha1"): {ETag: "ci2"}},
		observations: map[string]ports.SCMObservation{prKey(testRepo, 1): changed},
	}
	lc := &fakeLifecycle{err: errors.New("lifecycle down")}
	obs := newTestObserver(store, provider, lc, time.Unix(500, 0).UTC())
	obs.Cache.RepoPRListETag[prKey(testRepo, 0)] = "repo1"
	obs.Cache.CommitChecksETag[commitKey(testRepo, "sha1")] = "ci1"
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.writes) != 1 {
		t.Fatalf("DB write should succeed before lifecycle retry is queued, got writes=%#v", store.writes)
	}
	if store.writes[0].pr.Title != "changed title" {
		t.Fatalf("DB write did not persist the observed PR state: %#v", store.writes[0].pr)
	}
	if store.writes[0].pr.MetadataHash != local.MetadataHash {
		t.Fatalf("metadata hash advanced before lifecycle acknowledgement: got %q want %q", store.writes[0].pr.MetadataHash, local.MetadataHash)
	}
	if store.writes[0].pr.CIHash != local.CIHash {
		t.Fatalf("CI hash advanced before lifecycle acknowledgement: got %q want %q", store.writes[0].pr.CIHash, local.CIHash)
	}
	if got := obs.Cache.RepoPRListETag[prKey(testRepo, 0)]; got != "repo1" {
		t.Fatalf("repo ETag advanced after lifecycle failure: got %q want repo1", got)
	}
	if got := obs.Cache.CommitChecksETag[commitKey(testRepo, "sha1")]; got != "ci1" {
		t.Fatalf("commit ETag advanced after lifecycle failure: got %q want ci1", got)
	}

	lc.err = nil
	store.prs["p-1"] = []domain.PullRequest{store.writes[0].pr}
	restarted := newTestObserver(store, provider, lc, time.Unix(501, 0).UTC())
	if err := restarted.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(lc.observed) != 1 {
		t.Fatalf("held-back semantic hashes did not force lifecycle retry after restart: %#v", lc.observed)
	}
	if len(store.writes) != 3 {
		t.Fatalf("retry should write held-back facts then acknowledge hashes, got writes=%d", len(store.writes))
	}
	last := store.writes[len(store.writes)-1].pr
	if last.MetadataHash != metadataSemanticHash(changed) {
		t.Fatalf("metadata hash not acknowledged after lifecycle success: got %q want %q", last.MetadataHash, metadataSemanticHash(changed))
	}
	if last.CIHash != ciSemanticHash(changed.CI) {
		t.Fatalf("CI hash not acknowledged after lifecycle success: got %q want %q", last.CIHash, ciSemanticHash(changed.CI))
	}
}

func TestPoll_WriteFailureDoesNotAdvanceGuardETags(t *testing.T) {
	store := testStoreWithSession()
	store.writeErr = errors.New("db down")
	local := knownPR(1)
	local.MetadataHash = "old-metadata"
	local.CIHash = "old-ci"
	store.prs["p-1"] = []domain.PullRequest{local}
	changed := testObs(1)
	changed.PR.Title = "changed title"
	provider := &fakeProvider{
		repoGuards:   map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "repo2"}},
		checkGuards:  map[string]ports.SCMGuardResult{commitKey(testRepo, "sha1"): {ETag: "ci2"}},
		observations: map[string]ports.SCMObservation{prKey(testRepo, 1): changed},
	}
	obs := newTestObserver(store, provider, &fakeLifecycle{}, time.Unix(550, 0).UTC())
	obs.Cache.RepoPRListETag[prKey(testRepo, 0)] = "repo1"
	obs.Cache.CommitChecksETag[commitKey(testRepo, "sha1")] = "ci1"
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := obs.Cache.RepoPRListETag[prKey(testRepo, 0)]; got != "repo1" {
		t.Fatalf("repo ETag advanced after write failure: got %q want repo1", got)
	}
	if got := obs.Cache.CommitChecksETag[commitKey(testRepo, "sha1")]; got != "ci1" {
		t.Fatalf("commit ETag advanced after write failure: got %q want ci1", got)
	}
}

func TestPoll_DuplicateTrackedPRKeepsFirstSession(t *testing.T) {
	store := &fakeStore{
		sessions: []domain.SessionRecord{
			{ID: "p-1", ProjectID: "p", Metadata: domain.SessionMetadata{Branch: "feat"}},
			{ID: "p-2", ProjectID: "p", Metadata: domain.SessionMetadata{Branch: "feat"}},
		},
		projects: map[string]domain.ProjectRecord{"p": {ID: "p", RepoOriginURL: "https://github.com/o/r.git"}},
		prs:      map[domain.SessionID][]domain.PullRequest{},
		checks:   map[string][]domain.PullRequestCheck{},
	}
	pr1 := knownPR(1)
	pr1.MetadataHash = "old-1"
	pr2 := pr1
	pr2.SessionID = "p-2"
	store.prs["p-1"] = []domain.PullRequest{pr1}
	store.prs["p-2"] = []domain.PullRequest{pr2}
	provider := &fakeProvider{
		repoGuards:   map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "repo2"}},
		observations: map[string]ports.SCMObservation{prKey(testRepo, 1): testObs(1)},
	}
	obs := newTestObserver(store, provider, &fakeLifecycle{}, time.Unix(600, 0).UTC())
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.writes) == 0 {
		t.Fatalf("writes = %d, want exactly one owner write", len(store.writes))
	}
	if store.writes[0].pr.SessionID != "p-1" {
		t.Fatalf("duplicate owner overwrote first session: wrote session %s", store.writes[0].pr.SessionID)
	}
}

// TestDiscoverSubjects_BackfillsRepoOriginURL asserts that a project row with
// an empty RepoOriginURL but a real on-disk repo gets its origin filled in
// during discovery and persisted, so the same project becomes observable
// without re-running project add.
func TestDiscoverSubjects_BackfillsRepoOriginURL(t *testing.T) {
	dir := t.TempDir()
	if out, err := exec.Command("git", "init", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v (%s)", err, out)
	}
	if out, err := exec.Command("git", "-C", dir, "remote", "add", "origin", "https://github.com/o/r.git").CombinedOutput(); err != nil {
		t.Fatalf("git remote add: %v (%s)", err, out)
	}

	store := &fakeStore{
		sessions: []domain.SessionRecord{{ID: "p-1", ProjectID: "p", Metadata: domain.SessionMetadata{Branch: "feat"}}},
		projects: map[string]domain.ProjectRecord{"p": {ID: "p", Path: dir}}, // empty RepoOriginURL
		prs:      map[domain.SessionID][]domain.PullRequest{},
		checks:   map[string][]domain.PullRequestCheck{},
	}
	provider := &fakeProvider{}
	obs := newTestObserver(store, provider, &fakeLifecycle{}, time.Unix(0, 0).UTC())

	if _, _, err := obs.discoverSubjects(context.Background()); err != nil {
		t.Fatalf("discoverSubjects: %v", err)
	}
	if got := store.projects["p"].RepoOriginURL; got != "https://github.com/o/r.git" {
		t.Fatalf("RepoOriginURL after backfill = %q, want https://github.com/o/r.git", got)
	}
}

// TestDiscoverSubjects_NonGitPathDoesNotBackfill confirms the backfill is
// best-effort: a non-git project path leaves RepoOriginURL empty without
// erroring or persisting a stub, so the project is simply skipped.
func TestDiscoverSubjects_NonGitPathDoesNotBackfill(t *testing.T) {
	dir := t.TempDir()
	store := &fakeStore{
		sessions: []domain.SessionRecord{{ID: "p-1", ProjectID: "p", Metadata: domain.SessionMetadata{Branch: "feat"}}},
		projects: map[string]domain.ProjectRecord{"p": {ID: "p", Path: dir}},
		prs:      map[domain.SessionID][]domain.PullRequest{},
		checks:   map[string][]domain.PullRequestCheck{},
	}
	obs := newTestObserver(store, &fakeProvider{}, &fakeLifecycle{}, time.Unix(0, 0).UTC())
	subjects, sessionRepos, err := obs.discoverSubjects(context.Background())
	if err != nil {
		t.Fatalf("discoverSubjects: %v", err)
	}
	if len(subjects) != 0 || len(sessionRepos) != 0 {
		t.Fatalf("non-git project should be skipped, got %d subjects %d sessionRepos", len(subjects), len(sessionRepos))
	}
	if got := store.projects["p"].RepoOriginURL; got != "" {
		t.Fatalf("RepoOriginURL = %q, want empty (no persist on failed backfill)", got)
	}
}
