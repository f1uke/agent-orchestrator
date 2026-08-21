package integration

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/workspace/gitworktree"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/lifecycle"
	"github.com/aoagents/agent-orchestrator/backend/internal/observe/reclaimer"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/reclaimlog"
	"github.com/aoagents/agent-orchestrator/backend/internal/reclaimsettings"
	sessionsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/session"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// The crew lifetime tests run against a REAL git repository, a REAL gitworktree
// adapter, a REAL SQLite store and the REAL auto-reclaim loop. Nothing about
// "does the worktree still exist" is mocked, because the failure this feature has
// to rule out is a directory being deleted, and a fake workspace cannot fail that
// way.

type crewStack struct {
	store *sqlite.Store
	svc   *sessionsvc.Service
	mgr   *sessionmanager.Manager
	lcm   *lifecycle.Manager
	rt    *stubRuntime
	repo  string
	root  string
	now   time.Time
}

func newCrewStack(t *testing.T) *crewStack {
	t.Helper()
	return newCrewStackWithIdle(t, 0)
}

// newCrewStackWithIdle is newCrewStack with the idle-close window configured, for
// the tests that drive the sweep. Zero disables it, which is what every other
// test here wants.
func newCrewStackWithIdle(t *testing.T, idleTTL time.Duration) *crewStack {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not on PATH")
	}
	tmp := t.TempDir()
	repo := seedCrewRepo(t, git, filepath.Join(tmp, "repo"))
	managed := filepath.Join(tmp, "managed")

	ws, err := gitworktree.New(gitworktree.Options{
		Binary:       git,
		ManagedRoot:  managed,
		RepoResolver: gitworktree.StaticRepoResolver{"mer": repo},
	})
	if err != nil {
		t.Fatalf("gitworktree.New: %v", err)
	}
	store, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	ctx := context.Background()
	if err := store.UpsertProject(ctx, domain.ProjectRecord{
		ID: "mer", Path: repo, RegisteredAt: time.Now(),
		Config: domain.ProjectConfig{
			DefaultBranch: "main",
			Worker:        domain.RoleOverride{Harness: domain.HarnessClaudeCode},
			Orchestrator:  domain.RoleOverride{Harness: domain.HarnessClaudeCode},
		},
	}); err != nil {
		t.Fatal(err)
	}
	msg := &captureMessenger{}
	lcm := lifecycle.New(store, msg)
	// Distinct handles per session: two crew members that shared one handle id
	// could not be told apart by a liveness probe, and the one-awake-at-a-time
	// guard would be probing the wrong agent. Real tmux already names them apart
	// (a non-dev member launches with an empty branch, which selects the
	// session-id fallback), so this is fidelity, not convenience.
	rt := &stubRuntime{perSessionHandles: true, trackLiveness: true}
	st := &crewStack{store: store, rt: rt, repo: repo, root: managed, now: time.Now()}
	st.lcm = lcm
	st.mgr = sessionmanager.New(sessionmanager.Deps{
		Runtime: rt, Agents: stubAgents{}, Workspace: ws, Store: store, Messenger: msg,
		Lifecycle:    lcm,
		LookPath:     func(string) (string, error) { return "/usr/bin/true", nil },
		Clock:        func() time.Time { return st.now },
		IdleCloseTTL: idleTTL,
	})
	// Mirror daemon/lifecycle_wiring.go: the reducer terminates a finished dev's
	// row without going through Teardown, so it needs the session manager's crew
	// fan-out injected or a merged dev leaves its qa running.
	lcm.SetCrewReaper(st.mgr.TeardownCrewSubordinates)
	st.svc = sessionsvc.New(st.mgr, store)
	return st
}

func seedCrewRepo(t *testing.T, git, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command(git, args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=ao", "GIT_AUTHOR_EMAIL=ao@example.com",
			"GIT_COMMITTER_NAME=ao", "GIT_COMMITTER_EMAIL=ao@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "--initial-branch=main")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("seed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-m", "seed")
	return dir
}

// spawnCrew brings up dev and one qa member on one worktree.
func (s *crewStack) spawnCrew(t *testing.T) (dev, qa domain.SessionRecord) {
	t.Helper()
	ctx := context.Background()
	devRec, err := s.mgr.Spawn(ctx, ports.SpawnConfig{
		ProjectID: "mer", Kind: domain.KindWorker, Branch: "feature/task", Prompt: "build it",
		// mechanical is what makes this a SOLO spawn: a standard task now forms
		// its own crew, which is a different scenario from the one under test.
		TaskSize: domain.TaskSizeMechanical,
	})
	if err != nil {
		t.Fatalf("spawn dev: %v", err)
	}
	// ONE AWAKE AT A TIME: dev has to stand down before a second member may be
	// born into its tree. This is the release half of a handover, and it is the
	// only way a crew can be formed.
	if err := s.mgr.ReleaseCrewSlot(ctx, devRec.ID); err != nil {
		t.Fatalf("release dev's slot: %v", err)
	}
	qaRec, err := s.mgr.Spawn(ctx, ports.SpawnConfig{
		ProjectID: "mer", Kind: domain.KindWorker, Prompt: "test it",
		CrewOf: devRec.ID, CrewRole: domain.CrewRoleQA,
	})
	if err != nil {
		t.Fatalf("spawn crew member: %v", err)
	}
	devRec, ok, err := s.store.GetSession(ctx, devRec.ID)
	if err != nil || !ok {
		t.Fatalf("re-read dev: %v", err)
	}
	if devRec.Metadata.WorkspacePath == "" || qaRec.Metadata.WorkspacePath != devRec.Metadata.WorkspacePath {
		t.Fatalf("crew is not on one worktree: dev %q, member %q", devRec.Metadata.WorkspacePath, qaRec.Metadata.WorkspacePath)
	}
	if _, err := os.Stat(devRec.Metadata.WorkspacePath); err != nil {
		t.Fatalf("the shared worktree does not exist on disk: %v", err)
	}
	return devRec, qaRec
}

func dirExists(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Stat(path)
	if err == nil {
		return true
	}
	if os.IsNotExist(err) {
		return false
	}
	t.Fatalf("stat %s: %v", path, err)
	return false
}

// TestCrew_TerminatingOneMemberLeavesTheOtherWorking is the invariant, against a
// real worktree: end qa, and dev still has its files, its branch and its checkout,
// and can still be relaunched into them.
func TestCrew_TerminatingOneMemberLeavesTheOtherWorking(t *testing.T) {
	ctx := context.Background()
	s := newCrewStack(t)
	dev, qa := s.spawnCrew(t)
	tree := dev.Metadata.WorkspacePath

	// dev's work in progress: the entire value of the worktree.
	const inFlight = "half-written analysis\nsecond line\n"
	if err := os.WriteFile(filepath.Join(tree, "work-in-progress.txt"), []byte(inFlight), 0o600); err != nil {
		t.Fatal(err)
	}

	freed, err := s.svc.Kill(ctx, qa.ID)
	if err != nil {
		t.Fatalf("kill crew member: %v", err)
	}
	if freed {
		t.Fatal("killing a crew member reported the worktree freed; dev is still in it")
	}
	if !dirExists(t, tree) {
		t.Fatal("killing a crew member deleted the shared worktree")
	}
	got, err := os.ReadFile(filepath.Join(tree, "work-in-progress.txt"))
	if err != nil || string(got) != inFlight {
		t.Fatalf("dev's uncommitted work = %q, %v; want it byte-for-byte intact", got, err)
	}

	devRec, ok, err := s.store.GetSession(ctx, dev.ID)
	if err != nil || !ok {
		t.Fatalf("read dev: %v", err)
	}
	if devRec.IsTerminated {
		t.Fatal("killing a crew member terminated its dev")
	}
	if devRec.Metadata.WorkspacePath != tree {
		t.Fatalf("dev workspace = %q, want %q", devRec.Metadata.WorkspacePath, tree)
	}

	// And it can still RUN: a restart relaunches the agent in the same tree.
	restarted, err := s.mgr.Restart(ctx, dev.ID)
	if err != nil {
		t.Fatalf("dev could not be relaunched after its crew member was killed: %v", err)
	}
	if restarted.IsTerminated || restarted.Metadata.WorkspacePath != tree {
		t.Fatalf("relaunched dev = terminated %v, workspace %q", restarted.IsTerminated, restarted.Metadata.WorkspacePath)
	}
	if !dirExists(t, tree) {
		t.Fatal("the relaunch lost the worktree")
	}
}

// TestCrew_TerminatingOneMemberLeavesACLEANWorktreeStanding is the same invariant
// with the accidental safety net removed.
//
// gitworktree refuses to remove a DIRTY worktree, so a task with work in progress
// would survive a missing refcount by luck. A crew whose tree happens to be clean
// - between commits, or a qa member that only ever ran tests - has no such luck:
// here the refcount is the only thing standing between the running dev and an
// empty directory.
func TestCrew_TerminatingOneMemberLeavesACLEANWorktreeStanding(t *testing.T) {
	ctx := context.Background()
	s := newCrewStack(t)
	dev, qa := s.spawnCrew(t)
	tree := dev.Metadata.WorkspacePath
	assertCleanWorktree(t, tree)

	if _, err := s.svc.Kill(ctx, qa.ID); err != nil {
		t.Fatalf("kill crew member: %v", err)
	}
	if !dirExists(t, tree) {
		t.Fatal("killing a crew member deleted the clean worktree its dev is working in")
	}
	if _, err := os.Stat(filepath.Join(tree, "README.md")); err != nil {
		t.Fatalf("the checkout is gone from under dev: %v", err)
	}
	if _, err := s.mgr.Restart(ctx, dev.ID); err != nil {
		t.Fatalf("dev could not be relaunched: %v", err)
	}
}

// assertCleanWorktree fails unless git reports nothing to commit, so a test that
// relies on the tree being clean cannot silently start relying on it being dirty.
func assertCleanWorktree(t *testing.T, tree string) {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not on PATH")
	}
	cmd := exec.Command(git, "status", "--porcelain")
	cmd.Dir = tree
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git status in %s: %v\n%s", tree, err, out)
	}
	if len(out) != 0 {
		t.Fatalf("precondition: %s is dirty, so this test would pass on the dirty-worktree refusal instead of the refcount:\n%s", tree, out)
	}
}

// TestCrew_TerminatingTheTaskFreesTheWorktree: killing dev is killing the task.
// The subordinate goes with it and the directory is actually gone from disk.
func TestCrew_TerminatingTheTaskFreesTheWorktree(t *testing.T) {
	ctx := context.Background()
	s := newCrewStack(t)
	dev, qa := s.spawnCrew(t)
	tree := dev.Metadata.WorkspacePath

	freed, err := s.svc.Kill(ctx, dev.ID)
	if err != nil {
		t.Fatalf("kill dev: %v", err)
	}
	if !freed {
		t.Fatal("killing the task did not free the worktree")
	}
	if dirExists(t, tree) {
		t.Fatalf("the worktree is still on disk at %s", tree)
	}
	qaRec, _, err := s.store.GetSession(ctx, qa.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !qaRec.IsTerminated {
		t.Fatal("the crew member outlived its dev, on a worktree that no longer exists")
	}
}

// TestCrew_ReclaimFreesTheTreeOnlyWhenNoMemberNeedsIt drives the REAL auto-reclaim
// loop over a real worktree, in the two orders it can see a crew in.
func TestCrew_ReclaimFreesTheTreeOnlyWhenNoMemberNeedsIt(t *testing.T) {
	ctx := context.Background()
	s := newCrewStack(t)
	dev, qa := s.spawnCrew(t)
	tree := dev.Metadata.WorkspacePath

	log := &memAudit{}
	const graceMinutes = 15
	grace := graceMinutes * time.Minute
	r := reclaimer.New(s.svc, fixedSettings{reclaimsettings.Settings{Enabled: true, GraceMinutes: graceMinutes}},
		reclaimer.Config{
			Clock:    func() time.Time { return s.now },
			SelfPath: filepath.Join(t.TempDir(), "elsewhere"),
			Audit:    log,
		})

	// Phase 1: the crew member is finished, dev is still working. The tree is not
	// the loop's to take, however long it waits.
	if _, err := s.svc.Kill(ctx, qa.ID); err != nil {
		t.Fatal(err)
	}
	s.tick(t, r, grace)
	s.tick(t, r, grace)
	if !dirExists(t, tree) {
		t.Fatal("auto-reclaim took a worktree a live dev is working in")
	}
	if got := log.reasons(); len(got) == 0 || got[0] != sessionmanager.ReasonWorkspaceShared {
		t.Fatalf("reclaim log reasons = %v, want a %q refusal naming the branch", got, sessionmanager.ReasonWorkspaceShared)
	}

	// Phase 2: dev finishes too. Now nothing needs the tree, and the next pass
	// past the grace period frees it.
	if _, err := s.svc.Kill(ctx, dev.ID); err != nil {
		t.Fatal(err)
	}
	if dirExists(t, tree) {
		// Kill already frees it; if a future change makes Kill defer, the loop
		// must still finish the job.
		s.tick(t, r, grace)
		s.tick(t, r, grace)
	}
	if dirExists(t, tree) {
		t.Fatalf("the worktree survived both members finishing: %s", tree)
	}
}

// TestCrew_AbandonedHalfCrewIsReclaimed pins auto-reclaim's SAFETY NET: dev's row
// terminated while the subordinate was left running, with no teardown in between.
// The merge path no longer produces that state (it fans out to the crew first -
// see crew_merge_ends_the_crew_test.go), but a fan-out that failed, or a row a
// crash left half-written, still can. Nothing else would ever end that
// subordinate - the idle sweep only suspends - so the tree would be pinned
// forever. Auto-reclaim ends the crew and frees it. It is a backstop, not the
// common case, which is exactly why it is still here.
func TestCrew_AbandonedHalfCrewIsReclaimed(t *testing.T) {
	ctx := context.Background()
	s := newCrewStack(t)
	dev, qa := s.spawnCrew(t)
	tree := dev.Metadata.WorkspacePath

	// dev merged and was terminated by the reducer. qa never heard about it.
	lcm := lifecycle.New(s.store, &captureMessenger{})
	if err := lcm.MarkTerminated(ctx, dev.ID, domain.TerminationCauseWorkComplete); err != nil {
		t.Fatal(err)
	}
	if !dirExists(t, tree) {
		t.Fatal("precondition: the worktree should still be on disk")
	}

	log := &memAudit{}
	const graceMinutes = 15
	r := reclaimer.New(s.svc, fixedSettings{reclaimsettings.Settings{Enabled: true, GraceMinutes: graceMinutes}},
		reclaimer.Config{
			Clock:    func() time.Time { return s.now },
			SelfPath: filepath.Join(t.TempDir(), "elsewhere"),
			Audit:    log,
		})
	s.tick(t, r, graceMinutes*time.Minute)
	s.tick(t, r, graceMinutes*time.Minute)

	if dirExists(t, tree) {
		t.Fatalf("an abandoned half-crew pinned its worktree forever: %s (log %v)", tree, log.reasons())
	}
	qaRec, _, err := s.store.GetSession(ctx, qa.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !qaRec.IsTerminated {
		t.Fatal("the abandoned crew member is still live on a worktree that has been reclaimed")
	}
	if qaRec.Termination.Reason != domain.TerminationCauseAutoReclaim {
		t.Fatalf("crew member termination cause = %q, want %q so the record names who took it",
			qaRec.Termination.Reason, domain.TerminationCauseAutoReclaim)
	}
}

// TestSolo_ReclaimIsUnchanged is the preservation guard against the real loop: a
// solo session with nothing sharing its tree is reclaimed after the grace period,
// exactly as before.
func TestSolo_ReclaimIsUnchanged(t *testing.T) {
	ctx := context.Background()
	s := newCrewStack(t)
	rec, err := s.mgr.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Branch: "feature/solo", Prompt: "work",
		// mechanical is what makes this a SOLO spawn: a standard task now forms
		// its own crew, which is a different scenario from the one under test.
		TaskSize: domain.TaskSizeMechanical})
	if err != nil {
		t.Fatal(err)
	}
	tree := rec.Metadata.WorkspacePath
	if rec.InCrew() {
		t.Fatal("an ordinary spawn produced a crew member")
	}
	lcm := lifecycle.New(s.store, &captureMessenger{})
	if err := lcm.MarkTerminated(ctx, rec.ID, domain.TerminationCauseWorkComplete); err != nil {
		t.Fatal(err)
	}

	log := &memAudit{}
	const graceMinutes = 15
	r := reclaimer.New(s.svc, fixedSettings{reclaimsettings.Settings{Enabled: true, GraceMinutes: graceMinutes}},
		reclaimer.Config{
			Clock:    func() time.Time { return s.now },
			SelfPath: filepath.Join(t.TempDir(), "elsewhere"),
			Audit:    log,
		})
	s.tick(t, r, graceMinutes*time.Minute)
	s.tick(t, r, graceMinutes*time.Minute)

	if dirExists(t, tree) {
		t.Fatalf("a finished solo session's worktree was not reclaimed: %s (log %v)", tree, log.reasons())
	}
	if got := log.actions(); len(got) != 1 || got[0] != reclaimlog.ActionReclaimed {
		t.Fatalf("audit actions = %v, want one reclaimed", got)
	}
}

// tick advances the clock past the grace period and runs one pass. The loop needs
// two passes before it acts (a durable age gate and an in-memory debounce), so
// callers run it twice.
func (s *crewStack) tick(t *testing.T, r *reclaimer.Reclaimer, grace time.Duration) {
	t.Helper()
	s.now = s.now.Add(grace + time.Minute)
	if err := r.Tick(context.Background()); err != nil {
		t.Fatalf("reclaimer tick: %v", err)
	}
}

type fixedSettings struct{ s reclaimsettings.Settings }

func (f fixedSettings) Get() reclaimsettings.Settings { return f.s }

type memAudit struct{ entries []reclaimlog.Entry }

func (m *memAudit) Append(e reclaimlog.Entry) error {
	m.entries = append(m.entries, e)
	return nil
}

func (m *memAudit) reasons() []string {
	out := make([]string, 0, len(m.entries))
	for _, e := range m.entries {
		if e.Reason != "" {
			out = append(out, e.Reason)
		}
	}
	return out
}

func (m *memAudit) actions() []string {
	out := make([]string, 0, len(m.entries))
	for _, e := range m.entries {
		out = append(out, e.Action)
	}
	return out
}
