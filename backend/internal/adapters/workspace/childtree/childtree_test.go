package childtree_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/workspace/childtree"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// fixture is a repo with a linked worker worktree on feature/w, one commit
// ahead of main, the shape every AO worker has.
type fixture struct {
	t      *testing.T
	root   string
	worker string
	trees  *childtree.Trees
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	root := t.TempDir()
	f := &fixture{t: t, root: root, worker: filepath.Join(root, "worker"), trees: childtree.New()}
	repo := filepath.Join(root, "repo")
	f.git(root, "init", "-q", "-b", "main", repo)
	f.write(repo, "A", "a\n")
	f.write(repo, "B", "b\n")
	f.write(repo, "C", "c\n")
	f.git(repo, "add", ".")
	f.git(repo, "commit", "-q", "-m", "base")
	f.git(repo, "worktree", "add", "-q", "-b", "feature/w", f.worker)
	f.write(f.worker, "W", "worker\n")
	f.git(f.worker, "add", ".")
	f.git(f.worker, "commit", "-q", "-m", "worker commit")
	return f
}

func (f *fixture) git(dir string, args ...string) string {
	f.t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		f.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (f *fixture) write(dir, name, content string) {
	f.t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) read(dir, name string) string {
	f.t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		f.t.Fatal(err)
	}
	return string(b)
}

func (f *fixture) child(name string) (string, string) {
	f.t.Helper()
	path := filepath.Join(f.root, "children", name)
	branch := "ao-child/s1/" + name
	if _, err := f.trees.Create(context.Background(), ports.ChildTreeSpec{WorkerPath: f.worker, Path: path, Branch: branch}); err != nil {
		f.t.Fatalf("create %s: %v", name, err)
	}
	return path, branch
}

func (f *fixture) commitIn(dir, name, content string) {
	f.t.Helper()
	f.write(dir, name, content)
	f.git(dir, "add", name)
	f.git(dir, "commit", "-q", "-m", "edit "+name)
}

func TestCreateCutsFromWorkerHeadOutsideTheWorkerFolder(t *testing.T) {
	f := newFixture(t)
	f.write(f.worker, "A", "a\nworker-dirty\n")
	path := filepath.Join(f.root, "children", "c1")
	spec := ports.ChildTreeSpec{WorkerPath: f.worker, Path: path, Branch: "ao-child/s1/c1"}

	got, err := f.trees.Create(context.Background(), spec)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	head := f.git(f.worker, "rev-parse", "HEAD")
	if got.BaseSHA != head || got.TargetBranch != "feature/w" || !reflect.DeepEqual(got.WorkerDirty, []string{"A"}) {
		t.Fatalf("created = %+v, want base %s on feature/w with worker dirty [A]", got, head)
	}
	if branch := f.git(path, "branch", "--show-current"); branch != "ao-child/s1/c1" {
		t.Fatalf("child branch = %q", branch)
	}
	if f.read(path, "W") != "worker\n" {
		t.Fatal("child does not carry the worker's committed file")
	}
	if f.read(path, "A") != "a\n" {
		t.Fatal("child carries the worker's uncommitted edit")
	}
	if status := f.git(f.worker, "status", "--porcelain"); status != "M A" {
		t.Fatalf("worker status = %q, want only its own edit (the child must not show up in it)", status)
	}

	again, err := f.trees.Create(context.Background(), spec)
	if err != nil || again.BaseSHA != head {
		t.Fatalf("second create = %+v, %v; want the same child back", again, err)
	}
}

func TestInspectCountsCommitsFilesAndDirt(t *testing.T) {
	f := newFixture(t)
	path, _ := f.child("c1")
	base := f.git(path, "rev-parse", "HEAD")
	f.commitIn(path, "B", "b\nchild\n")
	f.commitIn(path, "N", "new\n")
	f.write(path, "C", "c\nuncommitted\n")

	facts, err := f.trees.Inspect(context.Background(), path, base)
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if !facts.Dirty || facts.Commits != 2 || facts.FilesChanged != 2 || facts.Head != f.git(path, "rev-parse", "HEAD") {
		t.Fatalf("facts = %+v, want dirty, 2 commits, 2 files", facts)
	}

	if err := f.trees.CommitAll(context.Background(), path, "AO: leftover work"); err != nil {
		t.Fatalf("commit all: %v", err)
	}
	facts, _ = f.trees.Inspect(context.Background(), path, base)
	if facts.Dirty || facts.Commits != 3 || facts.FilesChanged != 3 {
		t.Fatalf("after commit all = %+v, want clean, 3 commits, 3 files", facts)
	}
}

func TestMergeBringsChildCommitsAndKeepsWorkerEdits(t *testing.T) {
	f := newFixture(t)
	path, branch := f.child("c1")
	f.commitIn(path, "B", "b\nchild\n")
	f.write(f.worker, "A", "a\nworker-dirty\n")

	if conflicts, err := f.trees.Conflicts(context.Background(), f.worker, "feature/w", branch); err != nil || len(conflicts) != 0 {
		t.Fatalf("conflicts = %v, %v; want none", conflicts, err)
	}
	res, err := f.trees.Merge(context.Background(), f.worker, "feature/w", branch, "Merge subagent c1")
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if !res.Merged || res.Held != "" || res.SHA != f.git(f.worker, "rev-parse", "HEAD") {
		t.Fatalf("merge = %+v, want merged at worker HEAD", res)
	}
	if f.read(f.worker, "B") != "b\nchild\n" || f.read(f.worker, "A") != "a\nworker-dirty\n" {
		t.Fatal("worker lost the child's commit or its own uncommitted edit")
	}
	if parents := strings.Fields(f.git(f.worker, "log", "-1", "--format=%P")); len(parents) != 2 {
		t.Fatalf("merge commit parents = %v, want a --no-ff merge commit", parents)
	}

	if err := f.trees.Remove(context.Background(), f.worker, path, branch, true); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("child folder still there: %v", err)
	}
	if out := f.git(f.worker, "branch", "--list", branch); out != "" {
		t.Fatalf("child branch still there: %q", out)
	}
}

func TestMergeIsHeldWhenWorkerHasUncommittedEditsInTheSameFile(t *testing.T) {
	f := newFixture(t)
	path, branch := f.child("c1")
	f.commitIn(path, "A", "a\nchild\n")
	f.write(f.worker, "A", "a\nworker-dirty\n")
	before := f.git(f.worker, "rev-parse", "HEAD")

	res, err := f.trees.Merge(context.Background(), f.worker, "feature/w", branch, "Merge subagent c1")
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if res.Merged || !strings.Contains(res.Held, "A") {
		t.Fatalf("merge = %+v, want held naming A", res)
	}
	if f.git(f.worker, "rev-parse", "HEAD") != before || f.read(f.worker, "A") != "a\nworker-dirty\n" {
		t.Fatal("a held merge changed the worker")
	}
	if _, err := os.Stat(filepath.Join(f.git(f.worker, "rev-parse", "--absolute-git-dir"), "MERGE_HEAD")); !os.IsNotExist(err) {
		t.Fatal("a held merge left MERGE_HEAD behind")
	}
}

func TestMergeIsHeldWhenWorkerLeftItsBranch(t *testing.T) {
	f := newFixture(t)
	path, branch := f.child("c1")
	f.commitIn(path, "B", "b\nchild\n")
	f.git(f.worker, "checkout", "-q", "-b", "elsewhere")

	res, err := f.trees.Merge(context.Background(), f.worker, "feature/w", branch, "Merge subagent c1")
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if res.Merged || !strings.Contains(res.Held, "elsewhere") {
		t.Fatalf("merge = %+v, want held naming the branch the worker is on", res)
	}
}

func TestConflictsNamesFilesWithoutTouchingTheWorker(t *testing.T) {
	f := newFixture(t)
	path, branch := f.child("c1")
	f.commitIn(path, "C", "c\nchild\n")
	f.commitIn(f.worker, "C", "c\nworker\n")

	conflicts, err := f.trees.Conflicts(context.Background(), f.worker, "feature/w", branch)
	if err != nil {
		t.Fatalf("conflicts: %v", err)
	}
	if !reflect.DeepEqual(conflicts, []string{"C"}) {
		t.Fatalf("conflicts = %v, want [C]", conflicts)
	}
	if status := f.git(f.worker, "status", "--porcelain"); status != "" {
		t.Fatalf("worker status after a conflict probe = %q", status)
	}
}

func TestRemoveRefusesADirtyChild(t *testing.T) {
	f := newFixture(t)
	path, branch := f.child("c1")
	f.write(path, "B", "b\nunsaved\n")

	if err := f.trees.Remove(context.Background(), f.worker, path, branch, true); err == nil {
		t.Fatal("remove of a dirty child succeeded")
	}
	if f.read(path, "B") != "b\nunsaved\n" {
		t.Fatal("refused remove lost the child's edit")
	}
}

func TestPreserveCommitsLeftoversKeepsTheBranchAndRemovesTheFolder(t *testing.T) {
	f := newFixture(t)
	path, branch := f.child("c1")
	f.commitIn(path, "B", "b\ncommitted\n")
	f.write(path, "C", "c\nuncommitted\n")
	f.write(path, "U", "untracked\n")

	committed, err := f.trees.Preserve(context.Background(), f.worker, path, branch, "AO: preserved child work")
	if err != nil {
		t.Fatalf("preserve: %v", err)
	}
	if !committed {
		t.Fatal("preserve reported nothing committed")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("child folder still there: %v", err)
	}
	for file, want := range map[string]string{"B": "b\ncommitted\n", "C": "c\nuncommitted\n", "U": "untracked\n"} {
		if got := f.git(f.worker, "show", branch+":"+file); got+"\n" != want {
			t.Errorf("%s on the kept branch = %q, want %q", file, got, want)
		}
	}
	if list := f.git(f.worker, "worktree", "list"); strings.Contains(list, path) {
		t.Fatalf("preserved child still registered: %s", list)
	}
}

func TestRecoverMergeSettlesACompletedMerge(t *testing.T) {
	f := newFixture(t)
	path, branch := f.child("c1")
	f.commitIn(path, "B", "b\nchild\n")
	before := f.git(f.worker, "rev-parse", "HEAD")
	f.git(f.worker, "merge", "-q", "--no-ff", "--no-edit", branch)

	res, err := f.trees.RecoverMerge(context.Background(), f.worker, branch, before)
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	if !res.Merged || res.SHA != f.git(f.worker, "rev-parse", "HEAD") {
		t.Fatalf("recover = %+v, want merged", res)
	}
}

func TestRecoverMergeAbortsAMergeLeftHalfDone(t *testing.T) {
	f := newFixture(t)
	path, branch := f.child("c1")
	f.commitIn(path, "C", "c\nchild\n")
	f.commitIn(f.worker, "C", "c\nworker\n")
	before := f.git(f.worker, "rev-parse", "HEAD")
	cmd := exec.Command("git", "-C", f.worker, "merge", "--no-ff", "--no-edit", branch)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	if err := cmd.Run(); err == nil {
		t.Fatal("setup: expected a conflicted merge")
	}

	res, err := f.trees.RecoverMerge(context.Background(), f.worker, branch, before)
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	if res.Merged || res.Held == "" {
		t.Fatalf("recover = %+v, want held after aborting", res)
	}
	if status := f.git(f.worker, "status", "--porcelain"); status != "" {
		t.Fatalf("worker status after abort = %q, want clean", status)
	}
	if f.read(f.worker, "C") != "c\nworker\n" {
		t.Fatal("abort did not restore the worker's file")
	}
}

func TestCreateCopiesIgnoredFilesNamedByWorktreeInclude(t *testing.T) {
	f := newFixture(t)
	f.write(f.worker, ".gitignore", ".env\nbuild/\n")
	f.write(f.worker, ".worktreeinclude", ".env\n")
	f.git(f.worker, "add", ".gitignore", ".worktreeinclude")
	f.git(f.worker, "commit", "-q", "-m", "ignore rules")
	f.write(f.worker, ".env", "TOKEN=placeholder\n")
	if err := os.MkdirAll(filepath.Join(f.worker, "build"), 0o750); err != nil {
		t.Fatal(err)
	}
	f.write(filepath.Join(f.worker, "build"), "out.bin", "x")

	path, _ := f.child("c1")
	if f.read(path, ".env") != "TOKEN=placeholder\n" {
		t.Fatal(".env named by .worktreeinclude was not copied")
	}
	if _, err := os.Stat(filepath.Join(path, "build", "out.bin")); !os.IsNotExist(err) {
		t.Fatalf("an ignored file .worktreeinclude does not name was copied: %v", err)
	}
}
