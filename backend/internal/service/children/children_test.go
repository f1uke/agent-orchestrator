package children_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/workspace/childtree"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/children"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

type fixture struct {
	t      *testing.T
	ctx    context.Context
	root   string
	repo   string
	worker string
	store  *sqlite.Store
	svc    *children.Service
	rec    domain.SessionRecord
}

func newFixture(t *testing.T, ios bool) *fixture {
	t.Helper()
	root := t.TempDir()
	f := &fixture{t: t, ctx: context.Background(), root: root, repo: filepath.Join(root, "repo"), worker: filepath.Join(root, "worker")}
	f.git(root, "init", "-q", "-b", "main", f.repo)
	for _, name := range []string{"A", "B", "C"} {
		f.write(f.repo, name, strings.ToLower(name)+"\n")
	}
	f.git(f.repo, "add", ".")
	f.git(f.repo, "commit", "-q", "-m", "base")
	f.git(f.repo, "worktree", "add", "-q", "-b", "feature/w", f.worker)

	store, err := sqlite.Open(filepath.Join(root, "data"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	f.store = store
	if err := store.UpsertProject(f.ctx, domain.ProjectRecord{
		ID: "prj", Path: f.repo, RegisteredAt: time.Now().UTC(), Config: domain.ProjectConfig{HasIOSSimulator: ios},
	}); err != nil {
		t.Fatalf("project: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	f.rec, err = store.CreateSession(f.ctx, domain.SessionRecord{
		ProjectID: "prj", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
		Activity:  domain.Activity{State: domain.ActivityActive, LastActivityAt: now},
		Metadata:  domain.SessionMetadata{Branch: "feature/w", WorkspacePath: f.worker},
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	f.svc = children.New(children.Options{Store: store, Trees: childtree.New(), Root: filepath.Join(root, "child-worktrees")})
	return f
}

func (f *fixture) git(dir string, args ...string) string {
	f.t.Helper()
	out, err := f.gitErr(dir, args...)
	if err != nil {
		f.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return out
}

func (f *fixture) gitErr(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func (f *fixture) write(dir, name, content string) {
	f.t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) commitIn(dir, name, content string) {
	f.t.Helper()
	f.write(dir, name, content)
	f.git(dir, "add", name)
	f.git(dir, "commit", "-q", "-m", "edit "+name)
}

func (f *fixture) create(agentID string) domain.SessionChild {
	f.t.Helper()
	c, err := f.svc.Create(f.ctx, f.rec.ID, children.CreateInput{Name: "agent-" + agentID, Cwd: f.worker})
	if err != nil {
		f.t.Fatalf("create %s: %v", agentID, err)
	}
	return c
}

func (f *fixture) stop(agentID string) children.StopOutcome {
	f.t.Helper()
	out, err := f.svc.Stop(f.ctx, f.rec.ID, agentID)
	if err != nil {
		f.t.Fatalf("stop %s: %v", agentID, err)
	}
	return out
}

func (f *fixture) state(agentID string) domain.SessionChild {
	f.t.Helper()
	c, ok, err := f.store.GetSessionChild(f.ctx, f.rec.ID, agentID)
	if err != nil || !ok {
		f.t.Fatalf("get %s: ok=%v err=%v", agentID, ok, err)
	}
	return c
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func TestCreatePlacesTheChildOutsideTheWorkerAndIsIdempotent(t *testing.T) {
	f := newFixture(t, false)
	c := f.create("a1")
	if c.State != domain.ChildRunning || c.TargetBranch != "feature/w" || c.Branch != "ao-child/"+string(f.rec.ID)+"/a1" {
		t.Fatalf("child = %+v", c)
	}
	if strings.HasPrefix(c.WorktreePath, f.worker) || !exists(c.WorktreePath) {
		t.Fatalf("child folder %s must exist outside the worker %s", c.WorktreePath, f.worker)
	}
	again := f.create("a1")
	if again.WorktreePath != c.WorktreePath || again.CreatedAt != c.CreatedAt {
		t.Fatalf("second create = %+v, want the first child back", again)
	}
}

func TestCreateRefusesWhatIsNotADirectChildOfALiveWorker(t *testing.T) {
	f := newFixture(t, false)
	child := f.create("a1")
	cases := []struct {
		name string
		in   children.CreateInput
		want error
	}{
		{"EnterWorktree's own name", children.CreateInput{Name: "manual-test", Cwd: f.worker}, children.ErrBadName},
		{"the primary checkout", children.CreateInput{Name: "agent-b1", Cwd: f.repo}, children.ErrForeignCwd},
		{"a grandchild", children.CreateInput{Name: "agent-b2", Cwd: child.WorktreePath}, children.ErrNested},
	}
	for _, tc := range cases {
		if _, err := f.svc.Create(f.ctx, f.rec.ID, tc.in); !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", tc.name, err, tc.want)
		}
	}
	f.rec.IsTerminated = true
	if err := f.store.UpdateSession(f.ctx, f.rec); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Create(f.ctx, f.rec.ID, children.CreateInput{Name: "agent-b3", Cwd: f.worker}); !errors.Is(err, children.ErrNotWorker) {
		t.Errorf("terminated worker: err = %v, want ErrNotWorker", err)
	}
}

func TestStopAsksForACommitThenMergesAndTellsTheWorkerOnce(t *testing.T) {
	f := newFixture(t, false)
	c := f.create("a1")
	if err := f.svc.Describe(f.ctx, f.rec.ID, "a1", "general-purpose", "Write B"); err != nil {
		t.Fatal(err)
	}
	f.write(c.WorktreePath, "B", "b\nchild\n")

	out := f.stop("a1")
	if !out.Known || !out.Block || !strings.Contains(out.Reason, "git commit") {
		t.Fatalf("dirty stop = %+v, want a block asking for a commit", out)
	}
	f.git(c.WorktreePath, "commit", "-q", "-am", "child edit")

	out = f.stop("a1")
	if out.Block || out.Child.State != domain.ChildMerged {
		t.Fatalf("clean stop = %+v, want merged", out)
	}
	if got := f.git(f.worker, "show", "HEAD:B"); got != "b\nchild" {
		t.Fatalf("worker B = %q, want the child's edit", got)
	}
	if subject := f.git(f.worker, "log", "-1", "--format=%s"); subject != "Merge subagent general-purpose: Write B" {
		t.Fatalf("merge subject = %q", subject)
	}
	if exists(c.WorktreePath) || f.git(f.worker, "branch", "--list", c.Branch) != "" {
		t.Fatal("a merged child left its folder or branch behind")
	}

	notes, err := f.svc.Notes(f.ctx, f.rec.ID)
	if err != nil || len(notes) != 1 || !strings.Contains(notes[0], "AO merged subagent") || !strings.Contains(notes[0], "1 commit") {
		t.Fatalf("notes = %q, %v", notes, err)
	}
	if again, _ := f.svc.Notes(f.ctx, f.rec.ID); len(again) != 0 {
		t.Fatalf("second notes = %q, want nothing new", again)
	}
}

func TestStopWithNothingCommittedRemovesTheChild(t *testing.T) {
	f := newFixture(t, false)
	c := f.create("a1")
	if out := f.stop("a1"); out.Child.State != domain.ChildRemoved {
		t.Fatalf("stop = %+v, want removed", out)
	}
	if exists(c.WorktreePath) {
		t.Fatal("removed child left its folder")
	}
}

func TestAConflictIsTheChildsToResolveThenParked(t *testing.T) {
	f := newFixture(t, false)
	c := f.create("a1")
	f.commitIn(c.WorktreePath, "C", "c\nchild\n")
	f.commitIn(f.worker, "C", "c\nworker\n")

	for i := 0; i < domain.ChildStopBlockLimit; i++ {
		out := f.stop("a1")
		if !out.Block || !strings.Contains(out.Reason, "git rebase feature/w") || !strings.Contains(out.Reason, "C") {
			t.Fatalf("stop %d = %+v, want a block asking for a rebase naming C", i, out)
		}
	}
	out := f.stop("a1")
	if out.Block || out.Child.State != domain.ChildConflict || !exists(c.WorktreePath) {
		t.Fatalf("stop past the limit = %+v, want parked as conflict with the folder kept", out)
	}
	notes, _ := f.svc.Notes(f.ctx, f.rec.ID)
	if len(notes) != 1 || !strings.Contains(notes[0], "git merge "+c.Branch) {
		t.Fatalf("notes = %q, want the merge instruction", notes)
	}
}

func TestAHeldMergeRetriesOnTheWorkersNextHook(t *testing.T) {
	f := newFixture(t, false)
	c := f.create("a1")
	f.commitIn(c.WorktreePath, "A", "a\nchild\n")
	f.write(f.worker, "A", "a\nworker-dirty\n")

	if out := f.stop("a1"); out.Child.State != domain.ChildHeld || !strings.Contains(out.Child.Detail, "A") {
		t.Fatalf("stop = %+v, want held naming A", out)
	}
	notes, _ := f.svc.Notes(f.ctx, f.rec.ID)
	if len(notes) != 1 || !strings.Contains(notes[0], "could not merge") {
		t.Fatalf("notes while held = %q", notes)
	}
	if again, _ := f.svc.Notes(f.ctx, f.rec.ID); len(again) != 0 {
		t.Fatalf("still held, notes = %q, want nothing new", again)
	}

	f.git(f.worker, "checkout", "--", "A")
	notes, _ = f.svc.Notes(f.ctx, f.rec.ID)
	if len(notes) != 1 || !strings.Contains(notes[0], "AO merged") || f.state("a1").State != domain.ChildMerged {
		t.Fatalf("notes after the worker cleared A = %q (state %s), want merged", notes, f.state("a1").State)
	}
}

func TestTeardownPreservesUndeliveredWorkOnTheKeptBranch(t *testing.T) {
	f := newFixture(t, false)
	busy := f.create("a1")
	f.commitIn(busy.WorktreePath, "B", "b\ncommitted\n")
	f.write(busy.WorktreePath, "C", "c\nleft over\n")
	idle := f.create("a2")

	if undelivered, _ := f.svc.Undelivered(f.ctx, f.rec.ID); len(undelivered) != 2 {
		t.Fatalf("undelivered = %d, want 2", len(undelivered))
	}
	if err := f.svc.SettleForTeardown(f.ctx, f.rec.ID); err != nil {
		t.Fatalf("settle: %v", err)
	}
	if got := f.state("a1"); got.State != domain.ChildPreserved || exists(busy.WorktreePath) {
		t.Fatalf("busy child = %+v, want preserved with the folder gone", got)
	}
	if got := f.git(f.worker, "show", busy.Branch+":C"); got != "c\nleft over" {
		t.Fatalf("kept branch C = %q, want the uncommitted edit", got)
	}
	if got := f.state("a2"); got.State != domain.ChildRemoved || exists(idle.WorktreePath) {
		t.Fatalf("idle child = %+v, want removed", got)
	}
	if undelivered, _ := f.svc.Undelivered(f.ctx, f.rec.ID); len(undelivered) != 0 {
		t.Fatalf("undelivered after settle = %d", len(undelivered))
	}
}

func TestOrphansAreCommittedAndMergedWithoutAsking(t *testing.T) {
	f := newFixture(t, false)
	c := f.create("a1")
	f.write(c.WorktreePath, "B", "b\norphan\n")

	if err := f.svc.SettleOrphans(f.ctx, f.rec.ID); err != nil {
		t.Fatalf("settle orphans: %v", err)
	}
	if got := f.state("a1"); got.State != domain.ChildMerged {
		t.Fatalf("orphan = %+v, want merged", got)
	}
	if got := f.git(f.worker, "show", "HEAD:B"); got != "b\norphan" {
		t.Fatalf("worker B = %q", got)
	}
}

func TestReconcileSettlesAnInterruptedMergeAndAnEndedWorkersChild(t *testing.T) {
	f := newFixture(t, false)
	merging := f.create("a1")
	f.commitIn(merging.WorktreePath, "B", "b\nchild\n")
	row := f.state("a1")
	row.State, row.MergeHeadBefore = domain.ChildMerging, f.git(f.worker, "rev-parse", "HEAD")
	if _, err := f.store.UpdateSessionChild(f.ctx, row); err != nil {
		t.Fatal(err)
	}
	f.git(f.worker, "merge", "-q", "--no-ff", "--no-edit", merging.Branch)

	if err := f.svc.Reconcile(f.ctx); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if got := f.state("a1"); got.State != domain.ChildMerged || exists(merging.WorktreePath) {
		t.Fatalf("interrupted merge = %+v, want merged and cleaned up", got)
	}

	running := f.create("a2")
	f.write(running.WorktreePath, "C", "c\nunsaved\n")
	f.rec.IsTerminated = true
	if err := f.store.UpdateSession(f.ctx, f.rec); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Reconcile(f.ctx); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if got := f.state("a2"); got.State != domain.ChildPreserved {
		t.Fatalf("ended worker's child = %+v, want preserved", got)
	}
}

func TestBriefTellsTheChildWhereItIsAndHowToBuild(t *testing.T) {
	f := newFixture(t, true)
	f.write(f.worker, "A", "a\nworker-dirty\n")
	c := f.create("a1")
	brief, ok, err := f.svc.Brief(f.ctx, f.rec.ID, "a1")
	if err != nil || !ok {
		t.Fatalf("brief: ok=%v err=%v", ok, err)
	}
	for _, want := range []string{c.WorktreePath, c.Branch, "git commit", "uncommitted changes in A", "-derivedDataPath " + c.WorktreePath + ".derived", "simulator"} {
		if !strings.Contains(brief, want) {
			t.Errorf("brief lacks %q:\n%s", want, brief)
		}
	}
	if _, ok, _ := f.svc.Brief(f.ctx, f.rec.ID, "not-a-child"); ok {
		t.Error("brief for an unknown subagent")
	}
}

// Found in the sandbox run: a worker was told "Subagent ... is running" for
// every child it had just launched, which is noise.
func TestNotesSayNothingAboutAChildThatIsStillRunning(t *testing.T) {
	f := newFixture(t, false)
	f.create("a1")
	if err := f.svc.Describe(f.ctx, f.rec.ID, "a1", "general-purpose", "Write B"); err != nil {
		t.Fatal(err)
	}
	if notes, _ := f.svc.Notes(f.ctx, f.rec.ID); len(notes) != 0 {
		t.Fatalf("notes = %q, want none while the child runs", notes)
	}
}

// Found in the sandbox run: a child that rebased onto the worker's branch was
// reported with the worker's own commits counted as its own, and the worker
// was not told the child had resolved a conflict, so a line another child
// wrote was overwritten without anyone noticing.
func TestAChildThatRebasedIsCountedByItsOwnCommitsAndTheConflictIsReported(t *testing.T) {
	f := newFixture(t, false)
	c := f.create("a1")
	f.commitIn(c.WorktreePath, "C", "c\nchild\n")
	f.commitIn(f.worker, "C", "c\nworker\n")
	f.commitIn(f.worker, "W", "worker only\n")

	if out := f.stop("a1"); !out.Block {
		t.Fatalf("stop = %+v, want a rebase block", out)
	}
	if _, err := f.gitErr(c.WorktreePath, "rebase", "feature/w"); err == nil {
		t.Fatal("setup: expected the rebase to stop on the conflict")
	}
	f.write(c.WorktreePath, "C", "c\nchild\n")
	f.git(c.WorktreePath, "add", "C")
	cmd := exec.Command("git", "-C", c.WorktreePath, "-c", "core.editor=true", "rebase", "--continue")
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("rebase --continue: %v\n%s", err, out)
	}

	out := f.stop("a1")
	if out.Child.State != domain.ChildMerged {
		t.Fatalf("stop after rebase = %+v, want merged", out)
	}
	if out.Child.Commits != 1 || out.Child.FilesChanged != 1 {
		t.Fatalf("counted %d commits, %d files; want the child's own 1 commit and 1 file", out.Child.Commits, out.Child.FilesChanged)
	}
	notes, _ := f.svc.Notes(f.ctx, f.rec.ID)
	if len(notes) != 1 || !strings.Contains(notes[0], "conflict in C") {
		t.Fatalf("notes = %q, want the merge note to say the child resolved a conflict in C", notes)
	}
}

// A merge held because AO's own commit of leftover work was refused (here by a
// pre-commit hook) must, on retry, re-read the tree and commit that work before
// merging, not merge the branch without it.
func TestARetriedHoldCommitsLeftoverWorkBeforeMerging(t *testing.T) {
	f := newFixture(t, false)
	c := f.create("a1")
	f.write(c.WorktreePath, "B", "b\nleft over\n")
	hooks := filepath.Join(f.root, "hooks")
	if err := os.MkdirAll(hooks, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hooks, "pre-commit"), []byte("#!/bin/sh\nexit 1\n"), 0o700); err != nil { // #nosec G306 -- a git hook must be executable.
		t.Fatal(err)
	}
	f.git(f.repo, "config", "core.hooksPath", hooks)

	if err := f.svc.SettleOrphans(f.ctx, f.rec.ID); err != nil {
		t.Fatal(err)
	}
	if got := f.state("a1"); got.State != domain.ChildHeld {
		t.Fatalf("with the commit refused, state = %+v, want held", got)
	}

	f.git(f.repo, "config", "--unset", "core.hooksPath")
	if _, err := f.svc.Notes(f.ctx, f.rec.ID); err != nil {
		t.Fatal(err)
	}
	if got := f.state("a1"); got.State != domain.ChildMerged {
		t.Fatalf("after the hook went away, state = %+v, want merged", got)
	}
	if got := f.git(f.worker, "show", "HEAD:B"); got != "b\nleft over" {
		t.Fatalf("worker B = %q, want the leftover work merged", got)
	}
}
