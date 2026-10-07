package storetree_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/workspace/storetree"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// fixture is a scripts store on main with one committed script, and the place
// a session's worktree of it goes.
type fixture struct {
	t     *testing.T
	store string
	path  string
	trees *storetree.Trees
}

const branch = "ao/nter-ios-app-7"

func newFixture(t *testing.T) *fixture {
	t.Helper()
	root := t.TempDir()
	f := &fixture{t: t, store: filepath.Join(root, "mobile-ui-scripts"), path: filepath.Join(root, "data", "store-worktrees", "mobile-ui-scripts", "nter-ios-app-7"), trees: storetree.New()}
	f.git(root, "init", "-q", "-b", "main", f.store)
	f.write(f.store, "projects/nter/login.yaml", "login\n")
	f.write(f.store, "README.md", "readme\n")
	f.git(f.store, "add", ".")
	f.git(f.store, "commit", "-q", "-m", "base")
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
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) commit(dir, name, content string) {
	f.t.Helper()
	f.write(dir, name, content)
	f.git(dir, "add", name)
	f.git(dir, "commit", "-q", "-m", "edit "+name)
}

func (f *fixture) ensure() {
	f.t.Helper()
	if err := f.trees.Ensure(context.Background(), f.store, f.path, branch, "main"); err != nil {
		f.t.Fatalf("ensure: %v", err)
	}
}

func (f *fixture) publish() ports.ScriptsPublishResult {
	f.t.Helper()
	res, err := f.trees.Publish(context.Background(), f.store, branch, "main", "Merge scripts from nter-ios-app-7")
	if err != nil {
		f.t.Fatalf("publish: %v", err)
	}
	return res
}

// untouched asserts a refused publish left the main checkout as it was: same
// tip, no merge in progress, and only the files that were dirty before.
func (f *fixture) untouched(head string, dirty ...string) {
	f.t.Helper()
	if got := f.git(f.store, "rev-parse", "HEAD"); got != head {
		f.t.Fatalf("store HEAD moved to %s, want %s", got, head)
	}
	if _, err := os.Stat(filepath.Join(f.store, ".git", "MERGE_HEAD")); err == nil {
		f.t.Fatal("a merge was left in progress in the store")
	}
	got, err := f.trees.Dirty(context.Background(), f.store)
	if err != nil {
		f.t.Fatal(err)
	}
	if len(got) != len(dirty) || (len(dirty) > 0 && !reflect.DeepEqual(got, dirty)) {
		f.t.Fatalf("store dirty files = %v, want %v", got, dirty)
	}
}

func TestProbe(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	trees := storetree.New()

	got, err := trees.Probe(ctx, f.store)
	if err != nil || !got.OK || got.Base != "main" {
		t.Fatalf("probe store = %+v, %v; want ok on main", got, err)
	}

	empty := filepath.Join(t.TempDir(), "empty")
	f.git(filepath.Dir(empty), "init", "-q", "-b", "main", empty)
	detached := filepath.Join(t.TempDir(), "detached")
	f.git(filepath.Dir(detached), "clone", "-q", f.store, detached)
	f.git(detached, "checkout", "-q", "--detach")
	cases := map[string]struct{ dir, reason string }{
		"missing":    {filepath.Join(t.TempDir(), "nope"), "does not exist"},
		"not a repo": {t.TempDir(), "is not a git repository"},
		"subfolder":  {filepath.Join(f.store, "projects"), "not its top level"},
		"detached":   {detached, "detached HEAD"},
		"no commits": {empty, "has no commits"},
	}
	for name, c := range cases {
		got, err := trees.Probe(ctx, c.dir)
		if err != nil || got.OK || !strings.Contains(got.Reason, c.reason) {
			t.Errorf("%s: probe = %+v, %v; want refused with %q", name, got, err, c.reason)
		}
	}
}

func TestEnsureCreatesThenConverges(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.ensure()
	if got := f.git(f.path, "symbolic-ref", "--short", "HEAD"); got != branch {
		t.Fatalf("worktree on %s, want %s", got, branch)
	}
	f.commit(f.path, "projects/nter/new.yaml", "new\n")
	tip := f.git(f.path, "rev-parse", "HEAD")

	f.ensure()
	if got := f.git(f.path, "rev-parse", "HEAD"); got != tip {
		t.Fatalf("a second ensure moved the worktree to %s, want %s", got, tip)
	}

	if err := os.RemoveAll(f.path); err != nil {
		t.Fatal(err)
	}
	f.ensure()
	if got := f.git(f.path, "rev-parse", "HEAD"); got != tip {
		t.Fatalf("recreated worktree at %s, want the kept branch's tip %s", got, tip)
	}
	registered, err := f.trees.Registered(ctx, f.store)
	if err != nil || len(registered) != 1 || filepath.Base(registered[0]) != filepath.Base(f.path) {
		t.Fatalf("registered = %v, %v; want only the recreated worktree", registered, err)
	}
}

func TestStatusCountsUncommittedFilesAndUnpublishedCommits(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.ensure()
	f.commit(f.path, "projects/nter/a.yaml", "a\n")
	f.commit(f.path, "projects/nter/b.yaml", "b\n")
	f.write(f.path, "projects/nter/new/c.yaml", "c\n")
	f.write(f.path, "projects/nter/login.yaml", "changed\n")

	got, err := f.trees.Status(ctx, f.path, branch, "main")
	if err != nil {
		t.Fatal(err)
	}
	want := ports.ScriptsTreeStatus{Uncommitted: []string{"projects/nter/login.yaml", "projects/nter/new/c.yaml"}, Unpublished: 2}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("status = %+v, want %+v (an untracked file in a new folder is named, not its folder)", got, want)
	}
}

func TestPublishNothingFastForwardAndMerge(t *testing.T) {
	f := newFixture(t)
	f.ensure()
	if res := f.publish(); res.Outcome != ports.PublishNothing {
		t.Fatalf("publish with no commits = %+v, want nothing", res)
	}

	f.commit(f.path, "projects/nter/a.yaml", "a\n")
	f.write(f.path, "projects/nter/draft.yaml", "not committed\n")
	tip := f.git(f.path, "rev-parse", "HEAD")
	res := f.publish()
	if res.Outcome != ports.PublishFastForward || res.SHA != tip || res.Commits != 1 {
		t.Fatalf("publish = %+v, want a fast forward to %s (a dirty worktree publishes its commits)", res, tip)
	}
	if got := f.git(f.store, "rev-parse", "main"); got != tip {
		t.Fatalf("store main = %s, want %s", got, tip)
	}
	if _, err := os.Stat(filepath.Join(f.store, "projects/nter/draft.yaml")); err == nil {
		t.Fatal("an uncommitted file reached the store")
	}

	f.commit(f.store, "README.md", "a person edited the store\n")
	f.commit(f.path, "projects/nter/b.yaml", "b\n")
	res = f.publish()
	if res.Outcome != ports.PublishMerged || res.Commits != 1 {
		t.Fatalf("publish after the store moved = %+v, want a merge commit", res)
	}
	parents := strings.Fields(f.git(f.store, "rev-list", "--parents", "-n", "1", "HEAD"))
	if len(parents) != 3 || parents[0] != res.SHA {
		t.Fatalf("store HEAD %v is not a two-parent merge at %s", parents, res.SHA)
	}
	if msg := f.git(f.store, "log", "-1", "--format=%s"); msg != "Merge scripts from nter-ios-app-7" {
		t.Fatalf("merge message = %q", msg)
	}
	if res := f.publish(); res.Outcome != ports.PublishNothing {
		t.Fatalf("publish again = %+v, want nothing", res)
	}
}

func TestPublishRefusals(t *testing.T) {
	t.Run("conflict", func(t *testing.T) {
		f := newFixture(t)
		f.ensure()
		f.commit(f.path, "projects/nter/login.yaml", "session\n")
		f.commit(f.store, "projects/nter/login.yaml", "person\n")
		head := f.git(f.store, "rev-parse", "HEAD")
		res := f.publish()
		if res.Outcome != ports.PublishRefused || res.Hold != domain.HoldPublishConflict || !reflect.DeepEqual(res.Files, []string{"projects/nter/login.yaml"}) {
			t.Fatalf("publish = %+v, want refused over the conflict in login.yaml", res)
		}
		f.untouched(head)
	})
	t.Run("dirty overlap on a fast forward", func(t *testing.T) {
		f := newFixture(t)
		f.ensure()
		f.commit(f.path, "projects/nter/login.yaml", "session\n")
		f.write(f.store, "projects/nter/login.yaml", "a person's unsaved edit\n")
		head := f.git(f.store, "rev-parse", "HEAD")
		res := f.publish()
		if res.Outcome != ports.PublishRefused || res.Hold != domain.HoldStoreDirtyOverlap || !reflect.DeepEqual(res.Files, []string{"projects/nter/login.yaml"}) {
			t.Fatalf("publish = %+v, want refused over the main checkout's edit to login.yaml", res)
		}
		f.untouched(head, "projects/nter/login.yaml")
	})
	t.Run("dirty overlap on a merge", func(t *testing.T) {
		f := newFixture(t)
		f.ensure()
		f.commit(f.path, "projects/nter/a.yaml", "session\n")
		f.commit(f.store, "README.md", "moved\n")
		f.write(f.store, "projects/nter/a.yaml", "an untracked file in the way\n")
		head := f.git(f.store, "rev-parse", "HEAD")
		res := f.publish()
		if res.Outcome != ports.PublishRefused || res.Hold != domain.HoldStoreDirtyOverlap || !reflect.DeepEqual(res.Files, []string{"projects/nter/a.yaml"}) {
			t.Fatalf("publish = %+v, want refused over the untracked a.yaml", res)
		}
		f.untouched(head, "projects/nter/a.yaml")
	})
	t.Run("dirty elsewhere does not block", func(t *testing.T) {
		f := newFixture(t)
		f.ensure()
		f.commit(f.path, "projects/nter/a.yaml", "session\n")
		f.write(f.store, "README.md", "unrelated edit\n")
		if res := f.publish(); res.Outcome != ports.PublishFastForward {
			t.Fatalf("publish = %+v, want a fast forward past an unrelated edit", res)
		}
	})
	t.Run("off base", func(t *testing.T) {
		f := newFixture(t)
		f.ensure()
		f.commit(f.path, "projects/nter/a.yaml", "session\n")
		f.git(f.store, "checkout", "-q", "-b", "experiment")
		head := f.git(f.store, "rev-parse", "HEAD")
		res := f.publish()
		if res.Outcome != ports.PublishRefused || res.Hold != domain.HoldStoreOffBase || !strings.Contains(res.Detail, "experiment") {
			t.Fatalf("publish = %+v, want refused because the store is on experiment", res)
		}
		f.untouched(head)
	})
}

func TestRemove(t *testing.T) {
	ctx := context.Background()
	t.Run("clean and published", func(t *testing.T) {
		f := newFixture(t)
		f.ensure()
		f.commit(f.path, "projects/nter/a.yaml", "a\n")
		f.publish()
		if err := f.trees.Remove(ctx, f.store, f.path, branch, false); err != nil {
			t.Fatalf("remove: %v", err)
		}
		if _, err := os.Stat(f.path); !os.IsNotExist(err) {
			t.Fatalf("worktree folder still there: %v", err)
		}
		if out := f.git(f.store, "branch", "--list", branch); out != "" {
			t.Fatalf("branch %s still there", branch)
		}
	})
	t.Run("dirty refuses without force", func(t *testing.T) {
		f := newFixture(t)
		f.ensure()
		f.write(f.path, "projects/nter/draft.yaml", "draft\n")
		if err := f.trees.Remove(ctx, f.store, f.path, branch, false); err == nil {
			t.Fatal("remove of a dirty worktree succeeded without force")
		}
		if _, err := os.Stat(filepath.Join(f.path, "projects/nter/draft.yaml")); err != nil {
			t.Fatalf("the draft is gone after a refused remove: %v", err)
		}
	})
	t.Run("unpublished branch refuses without force", func(t *testing.T) {
		f := newFixture(t)
		f.ensure()
		f.commit(f.path, "projects/nter/a.yaml", "a\n")
		if err := f.trees.Remove(ctx, f.store, f.path, branch, false); err == nil {
			t.Fatal("remove deleted a branch the store does not contain")
		}
		if out := f.git(f.store, "branch", "--list", branch); out == "" {
			t.Fatal("the unpublished branch is gone")
		}
	})
	t.Run("force", func(t *testing.T) {
		f := newFixture(t)
		f.ensure()
		f.commit(f.path, "projects/nter/a.yaml", "a\n")
		f.write(f.path, "projects/nter/draft.yaml", "draft\n")
		if err := f.trees.Remove(ctx, f.store, f.path, branch, true); err != nil {
			t.Fatalf("forced remove: %v", err)
		}
		if out := f.git(f.store, "branch", "--list", branch); out != "" {
			t.Fatal("branch survived a forced remove")
		}
	})
}
