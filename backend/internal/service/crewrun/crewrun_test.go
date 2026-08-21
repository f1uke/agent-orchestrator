package crewrun

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/treewatch"
)

// fakeStore is the in-memory half. The DETECTOR half of every test below is
// real: a real git worktree, a real filesystem watcher, real writes. The failure
// this feature prevents is a real file changing under a real build, so proving
// it against a fake tree would prove nothing.
type fakeStore struct {
	mu        sync.Mutex
	sessions  map[domain.SessionID]domain.SessionRecord
	runs      []domain.CrewRun
	insertErr error
}

func newFakeStore(rec domain.SessionRecord) *fakeStore {
	return &fakeStore{sessions: map[domain.SessionID]domain.SessionRecord{rec.ID: rec}}
}

func (f *fakeStore) GetSession(_ context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rec, ok := f.sessions[id]
	return rec, ok, nil
}

func (f *fakeStore) InsertCrewRun(_ context.Context, run domain.CrewRun) error {
	if f.insertErr != nil {
		return f.insertErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.runs = append(f.runs, run)
	return nil
}

func (f *fakeStore) EndCrewRun(_ context.Context, run domain.CrewRun) (domain.CrewRun, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.runs {
		if f.runs[i].ID == run.ID && f.runs[i].Open() {
			f.runs[i] = run
			return run, true, nil
		}
	}
	return domain.CrewRun{}, false, nil
}

func (f *fakeStore) GetCrewRun(_ context.Context, id string) (domain.CrewRun, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.runs {
		if r.ID == id {
			return r, true, nil
		}
	}
	return domain.CrewRun{}, false, nil
}

func (f *fakeStore) ListCrewRunsForSession(_ context.Context, id domain.SessionID, _ int) ([]domain.CrewRun, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []domain.CrewRun{}
	for i := len(f.runs) - 1; i >= 0; i-- {
		if f.runs[i].SessionID == id {
			out = append(out, f.runs[i])
		}
	}
	return out, nil
}

func (f *fakeStore) OpenCrewRunForSession(_ context.Context, id domain.SessionID) (domain.CrewRun, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.runs) - 1; i >= 0; i-- {
		if f.runs[i].SessionID == id && f.runs[i].Open() {
			return f.runs[i], true, nil
		}
	}
	return domain.CrewRun{}, false, nil
}

// Mirrors the real store: only a TRUSTED run ends the streak, and an uncertified
// one is skipped rather than treated as a clear.
func (f *fakeStore) ConsecutiveCrewRunDiscards(_ context.Context, id domain.SessionID) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	streak := 0
	for i := len(f.runs) - 1; i >= 0; i-- {
		if f.runs[i].SessionID != id || f.runs[i].Open() {
			continue
		}
		switch f.runs[i].Outcome {
		case domain.CrewRunDiscarded:
			streak++
		case domain.CrewRunUncertified:
			continue
		default:
			return streak, nil
		}
	}
	return streak, nil
}

func (f *fakeStore) AbandonOpenCrewRuns(_ context.Context, now time.Time) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	closed := 0
	for i := range f.runs {
		if f.runs[i].Open() {
			end := now
			f.runs[i].EndedAt = &end
			f.runs[i].Outcome = domain.CrewRunUncertified
			f.runs[i].Detector = domain.CrewRunDetectorDown
			closed++
		}
	}
	return closed, nil
}

const sessionID = domain.SessionID("agent-orchestrator-231")

func gitWorktree(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	for _, args := range [][]string{
		{"init", "-q"}, {"config", "user.email", "t@example.com"}, {"config", "user.name", "t"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	mustWrite(t, filepath.Join(root, ".gitignore"), "node_modules/\n")
	mustWrite(t, filepath.Join(root, "app.go"), "package app\n")
	if err := os.MkdirAll(filepath.Join(root, "node_modules"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	mustWrite(t, filepath.Join(root, "node_modules", "dep.js"), "1\n")
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-qm", "seed"}} {
		if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return root
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func newService(t *testing.T, worktree string, watcher Watcher) (*Service, *fakeStore) {
	t.Helper()
	rec := domain.SessionRecord{
		ID:        sessionID,
		ProjectID: "agent-orchestrator",
		CrewID:    "agent-orchestrator-230",
		CrewRole:  domain.CrewRoleQA,
	}
	rec.Metadata.WorkspacePath = worktree
	store := newFakeStore(rec)
	return New(Options{Store: store, Watcher: watcher, Logger: nil}), store
}

func liveWatcher(t *testing.T) Watcher {
	t.Helper()
	reg := treewatch.NewRegistry(treewatch.Options{})
	t.Cleanup(reg.Close)
	return reg
}

// waitForGeneration gives the filesystem event a moment to land. Writing a file
// and immediately ending the bracket would test the scheduler, not the detector.
func waitForGeneration(t *testing.T, worktree string, reg Watcher, above uint64) {
	t.Helper()
	lease, err := reg.Attach(context.Background(), worktree)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	defer lease.Release()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if got, err := lease.Generation(); err == nil && got > above {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("the watcher never saw a write past generation %d", above)
}

// An untouched run is trusted, and it reads as the result it reported.
func TestUntouchedRunIsTrusted(t *testing.T) {
	worktree := gitWorktree(t)
	svc, _ := newService(t, worktree, liveWatcher(t))
	ctx := context.Background()

	started, err := svc.Start(ctx, sessionID, StartInput{Kind: domain.CrewRunTest, Label: "go test ./..."})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !started.Certified {
		t.Fatalf("the detector did not come up on a real worktree: %s", started.Run.DetectorReason)
	}

	ended, err := svc.End(ctx, sessionID, EndInput{Result: domain.CrewRunResultPass})
	if err != nil {
		t.Fatalf("End: %v", err)
	}
	if ended.Run.Outcome != domain.CrewRunTrusted {
		t.Fatalf("outcome = %q, want trusted (changed: %v)", ended.Run.Outcome, ended.Run.ChangedPaths)
	}
	if got := ended.Run.State(); got != domain.CrewRunStatePassed {
		t.Fatalf("state = %q, want passed", got)
	}
	if ended.Retry || ended.Escalated {
		t.Fatal("a trusted run asked for a retry")
	}
	if ended.Run.HeadSHA == "" {
		t.Fatal("a run in a real git worktree recorded no HEAD")
	}
}

// THE ONE THAT MATTERS. A write lands in the tree while the bracket is open,
// from outside the run, and the result is thrown away rather than reported.
func TestMidRunWriteMakesTheResultUntrustworthy(t *testing.T) {
	worktree := gitWorktree(t)
	reg := liveWatcher(t)
	svc, _ := newService(t, worktree, reg)
	ctx := context.Background()

	started, err := svc.Start(ctx, sessionID, StartInput{Kind: domain.CrewRunBuild})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	// dev types, mid-run.
	mustWrite(t, filepath.Join(worktree, "app.go"), "package app // dev is still writing\n")
	waitForGeneration(t, worktree, reg, started.Run.GenAtStart)

	ended, err := svc.End(ctx, sessionID, EndInput{Result: domain.CrewRunResultPass})
	if err != nil {
		t.Fatalf("End: %v", err)
	}
	if ended.Run.Outcome != domain.CrewRunDiscarded {
		t.Fatalf("outcome = %q, want discarded", ended.Run.Outcome)
	}
	// The reported PASS must not survive as a verdict. That substitution is the
	// laundering the whole mechanism exists to stop.
	if got := ended.Run.State(); got != domain.CrewRunStateDiscarded {
		t.Fatalf("a discarded run reads as %q; it must read as discarded, never as the result it reported", got)
	}
	if len(ended.Run.ChangedPaths) == 0 || ended.Run.ChangedPaths[0] != "app.go" {
		t.Fatalf("the discard did not name what moved: %v", ended.Run.ChangedPaths)
	}
	if !ended.Retry || ended.Attempt != 1 {
		t.Fatalf("first discard: retry=%v attempt=%d, want retry on attempt 1", ended.Retry, ended.Attempt)
	}
}

// A write to an ignored path is not the tree moving: a build writing its own
// output must not throw away its own run.
func TestIgnoredWriteDoesNotDiscard(t *testing.T) {
	worktree := gitWorktree(t)
	svc, _ := newService(t, worktree, liveWatcher(t))
	ctx := context.Background()

	if _, err := svc.Start(ctx, sessionID, StartInput{Kind: domain.CrewRunBuild}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	mustWrite(t, filepath.Join(worktree, "node_modules", "dep.js"), "2\n")
	time.Sleep(300 * time.Millisecond)

	ended, err := svc.End(ctx, sessionID, EndInput{Result: domain.CrewRunResultPass})
	if err != nil {
		t.Fatalf("End: %v", err)
	}
	if ended.Run.Outcome != domain.CrewRunTrusted {
		t.Fatalf("outcome = %q, want trusted (changed: %v)", ended.Run.Outcome, ended.Run.ChangedPaths)
	}
}

// The retry cap TERMINATES: three discards and the member is told to stop, not
// to go round again.
func TestRetryCapTerminatesAtThree(t *testing.T) {
	worktree := gitWorktree(t)
	reg := liveWatcher(t)
	svc, store := newService(t, worktree, reg)
	ctx := context.Background()

	for attempt := 1; attempt <= domain.CappedRepeat; attempt++ {
		started, err := svc.Start(ctx, sessionID, StartInput{Kind: domain.CrewRunTest})
		if err != nil {
			t.Fatalf("Start %d: %v", attempt, err)
		}
		if started.Run.Attempt != attempt {
			t.Fatalf("attempt = %d, want %d", started.Run.Attempt, attempt)
		}
		mustWrite(t, filepath.Join(worktree, "app.go"), "package app // "+time.Now().String()+"\n")
		waitForGeneration(t, worktree, reg, started.Run.GenAtStart)

		ended, err := svc.End(ctx, sessionID, EndInput{Result: domain.CrewRunResultPass})
		if err != nil {
			t.Fatalf("End %d: %v", attempt, err)
		}
		if ended.Run.Outcome != domain.CrewRunDiscarded {
			t.Fatalf("attempt %d: outcome = %q, want discarded", attempt, ended.Run.Outcome)
		}
		wantRetry := attempt < domain.CappedRepeat
		if ended.Retry != wantRetry {
			t.Fatalf("attempt %d: retry = %v, want %v", attempt, ended.Retry, wantRetry)
		}
		if ended.Escalated != !wantRetry {
			t.Fatalf("attempt %d: escalated = %v, want %v", attempt, ended.Escalated, !wantRetry)
		}
	}

	streak, err := store.ConsecutiveCrewRunDiscards(ctx, sessionID)
	if err != nil {
		t.Fatalf("streak: %v", err)
	}
	if streak != domain.CappedRepeat {
		t.Fatalf("streak = %d, want %d", streak, domain.CappedRepeat)
	}
}

// One quiet run clears the streak, so a human who pauses dev and lets qa go
// again gets the card back with no button to press.
func TestOneQuietRunClearsTheStreak(t *testing.T) {
	worktree := gitWorktree(t)
	reg := liveWatcher(t)
	svc, store := newService(t, worktree, reg)
	ctx := context.Background()

	started, err := svc.Start(ctx, sessionID, StartInput{Kind: domain.CrewRunTest})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	mustWrite(t, filepath.Join(worktree, "app.go"), "package app // moved\n")
	waitForGeneration(t, worktree, reg, started.Run.GenAtStart)
	if _, err := svc.End(ctx, sessionID, EndInput{}); err != nil {
		t.Fatalf("End: %v", err)
	}

	if _, err := svc.Start(ctx, sessionID, StartInput{Kind: domain.CrewRunTest}); err != nil {
		t.Fatalf("Start 2: %v", err)
	}
	if _, err := svc.End(ctx, sessionID, EndInput{Result: domain.CrewRunResultPass}); err != nil {
		t.Fatalf("End 2: %v", err)
	}
	streak, err := store.ConsecutiveCrewRunDiscards(ctx, sessionID)
	if err != nil {
		t.Fatalf("streak: %v", err)
	}
	if streak != 0 {
		t.Fatalf("streak = %d after a trusted run, want 0", streak)
	}
}

// downWatcher stands in for a worktree the detector cannot watch.
type downWatcher struct{ err error }

func (d downWatcher) Attach(context.Context, string) (*treewatch.Lease, error) { return nil, d.err }

// A detector that cannot start yields UNCERTIFIED - never a silent clean.
func TestWatcherThatCannotStartYieldsUncertified(t *testing.T) {
	worktree := gitWorktree(t)
	svc, _ := newService(t, worktree, downWatcher{err: os.ErrPermission})
	ctx := context.Background()

	started, err := svc.Start(ctx, sessionID, StartInput{Kind: domain.CrewRunBuild})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if started.Certified {
		t.Fatal("a start with no detector reported itself certified")
	}
	if started.Run.Detector != domain.CrewRunDetectorDown || started.Run.DetectorReason == "" {
		t.Fatalf("detector = %q reason = %q, want down with a reason", started.Run.Detector, started.Run.DetectorReason)
	}

	ended, err := svc.End(ctx, sessionID, EndInput{Result: domain.CrewRunResultPass})
	if err != nil {
		t.Fatalf("End: %v", err)
	}
	if ended.Run.Outcome != domain.CrewRunUncertified {
		t.Fatalf("outcome = %q, want uncertified", ended.Run.Outcome)
	}
	if got := ended.Run.State(); got != domain.CrewRunStateUncertified {
		t.Fatalf("state = %q; an unwatched run must not read as passed", got)
	}
	if ended.Retry || ended.Escalated {
		t.Fatal("an uncertified run asked for a retry; only a discarded one does")
	}
}

// A daemon with no detector configured at all behaves the same way: uncertified,
// never trusted.
func TestNoDetectorConfiguredYieldsUncertified(t *testing.T) {
	worktree := gitWorktree(t)
	svc, _ := newService(t, worktree, nil)
	ctx := context.Background()

	if _, err := svc.Start(ctx, sessionID, StartInput{Kind: domain.CrewRunBuild}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	ended, err := svc.End(ctx, sessionID, EndInput{Result: domain.CrewRunResultPass})
	if err != nil {
		t.Fatalf("End: %v", err)
	}
	if ended.Run.Outcome != domain.CrewRunUncertified {
		t.Fatalf("outcome = %q, want uncertified", ended.Run.Outcome)
	}
}

// A run whose lease died with the previous daemon cannot be certified by a new
// one, however quiet the tree looks now.
func TestRunLostAcrossADaemonRestartIsUncertified(t *testing.T) {
	worktree := gitWorktree(t)
	reg := liveWatcher(t)
	svc, store := newService(t, worktree, reg)
	ctx := context.Background()

	if _, err := svc.Start(ctx, sessionID, StartInput{Kind: domain.CrewRunTest}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// A new process: same rows, no leases.
	restarted := New(Options{Store: store, Watcher: reg})

	ended, err := restarted.End(ctx, sessionID, EndInput{Result: domain.CrewRunResultPass})
	if err != nil {
		t.Fatalf("End: %v", err)
	}
	if ended.Run.Outcome != domain.CrewRunUncertified {
		t.Fatalf("outcome = %q, want uncertified", ended.Run.Outcome)
	}
}

// A start that finds a run still open closes it as uncertified and SAYS SO. A
// run thrown away silently is no better than a mixed result reported as clean.
func TestStartSupersedesAnAbandonedRun(t *testing.T) {
	worktree := gitWorktree(t)
	svc, store := newService(t, worktree, liveWatcher(t))
	ctx := context.Background()

	first, err := svc.Start(ctx, sessionID, StartInput{Kind: domain.CrewRunTest})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	second, err := svc.Start(ctx, sessionID, StartInput{Kind: domain.CrewRunBuild})
	if err != nil {
		t.Fatalf("Start 2: %v", err)
	}
	if second.SupersededRunID != first.Run.ID {
		t.Fatalf("superseded = %q, want %q", second.SupersededRunID, first.Run.ID)
	}
	abandoned, _, err := store.GetCrewRun(ctx, first.Run.ID)
	if err != nil {
		t.Fatalf("GetCrewRun: %v", err)
	}
	if abandoned.Outcome != domain.CrewRunUncertified {
		t.Fatalf("abandoned run outcome = %q, want uncertified", abandoned.Outcome)
	}
}

// Ending without a bracket is a usage error, not a silent success.
func TestEndWithNoOpenRunIsRefused(t *testing.T) {
	svc, _ := newService(t, gitWorktree(t), liveWatcher(t))
	if _, err := svc.End(context.Background(), sessionID, EndInput{}); err == nil {
		t.Fatal("ending with no open run succeeded")
	}
}

// An unknown kind is refused before anything is watched or written.
func TestStartRefusesAnUnknownKind(t *testing.T) {
	svc, store := newService(t, gitWorktree(t), liveWatcher(t))
	if _, err := svc.Start(context.Background(), sessionID, StartInput{Kind: "vibes"}); err == nil {
		t.Fatal("an unknown kind was accepted")
	}
	if len(store.runs) != 0 {
		t.Fatal("a refused start still wrote a run")
	}
}

// A session with no workspace has no tree to watch, and says so rather than
// pretending to watch one.
func TestStartRefusesASessionWithNoWorkspace(t *testing.T) {
	svc, store := newService(t, gitWorktree(t), liveWatcher(t))
	store.mu.Lock()
	rec := store.sessions[sessionID]
	rec.Metadata.WorkspacePath = ""
	store.sessions[sessionID] = rec
	store.mu.Unlock()

	if _, err := svc.Start(context.Background(), sessionID, StartInput{Kind: domain.CrewRunTest}); err == nil {
		t.Fatal("a session with no workspace started a run")
	}
}

// Two starts that arrive together must not both insert. Without the per-session
// lock a start is a check-then-act, and the loser leaves a run open forever with
// the board claiming a build is running that nothing is running.
func TestConcurrentStartsLeaveExactlyOneRunOpen(t *testing.T) {
	worktree := gitWorktree(t)
	svc, store := newService(t, worktree, liveWatcher(t))
	ctx := context.Background()

	var wg sync.WaitGroup
	for range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := svc.Start(ctx, sessionID, StartInput{Kind: domain.CrewRunTest}); err != nil {
				t.Errorf("Start: %v", err)
			}
		}()
	}
	wg.Wait()

	store.mu.Lock()
	open := 0
	for _, run := range store.runs {
		if run.Open() {
			open++
		}
	}
	total := len(store.runs)
	store.mu.Unlock()
	if open != 1 {
		t.Fatalf("%d runs left open out of %d, want exactly 1", open, total)
	}
}
