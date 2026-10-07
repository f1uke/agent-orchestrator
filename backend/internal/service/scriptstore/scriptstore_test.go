package scriptstore_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/workspace/storetree"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/scriptstore"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

type fixture struct {
	t       *testing.T
	ctx     context.Context
	root    string
	scripts string
	data    string
	db      *sqlite.Store
	svc     *scriptstore.Service
	logs    *bytes.Buffer
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	root := t.TempDir()
	f := &fixture{t: t, ctx: context.Background(), root: root, scripts: filepath.Join(root, "mobile-ui-scripts"), data: filepath.Join(root, "data"), logs: &bytes.Buffer{}}
	f.git(root, "init", "-q", "-b", "main", f.scripts)
	f.commit(f.scripts, "projects/nter/login.yaml", "login\n")
	db, err := sqlite.Open(f.data)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	f.db = db
	if err := db.UpsertProject(f.ctx, domain.ProjectRecord{ID: "nter", Path: root, RegisteredAt: time.Now().UTC()}); err != nil {
		t.Fatalf("project: %v", err)
	}
	f.svc = scriptstore.New(scriptstore.Options{
		Store: db, Trees: storetree.New(), DataDir: f.data,
		Logger: slog.New(slog.NewTextHandler(f.logs, nil)),
	})
	return f
}

func (f *fixture) session() domain.SessionRecord {
	f.t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	rec, err := f.db.CreateSession(f.ctx, domain.SessionRecord{
		ProjectID: "nter", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
		Activity:  domain.Activity{State: domain.ActivityActive, LastActivityAt: now},
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		f.t.Fatalf("session: %v", err)
	}
	return rec
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

func (f *fixture) ensure(owner domain.SessionID) domain.ScriptsStoreWorktree {
	f.t.Helper()
	w, err := f.svc.Ensure(f.ctx, "nter", owner, f.scripts, "main")
	if err != nil {
		f.t.Fatalf("ensure %s: %v", owner, err)
	}
	return w
}

func (f *fixture) row(owner domain.SessionID) domain.ScriptsStoreWorktree {
	f.t.Helper()
	w, ok, err := f.db.GetScriptsStoreWorktree(f.ctx, owner)
	if err != nil || !ok {
		f.t.Fatalf("row %s: ok %v, err %v", owner, ok, err)
	}
	return w
}

func TestLayoutIsPerStoreAndOwner(t *testing.T) {
	path, branch := scriptstore.Layout("/ao/data", "/Users/me/Documents/Projects/mobile-ui-scripts/", "nter-ios-app-7")
	if path != "/ao/data/store-worktrees/mobile-ui-scripts/nter-ios-app-7" || branch != "ao/nter-ios-app-7" {
		t.Fatalf("layout = %s %s", path, branch)
	}
}

func TestEnsureCutsOnceThenReattaches(t *testing.T) {
	f := newFixture(t)
	rec := f.session()
	w := f.ensure(rec.ID)
	wantPath, wantBranch := scriptstore.Layout(f.data, f.scripts, rec.ID)
	if w.Path != wantPath || w.Branch != wantBranch || w.BaseBranch != "main" || w.State != domain.ScriptsStoreActive {
		t.Fatalf("worktree = %+v", w)
	}
	f.commit(w.Path, "projects/nter/new.yaml", "new\n")
	tip := f.git(w.Path, "rev-parse", "HEAD")

	if err := os.RemoveAll(w.Path); err != nil {
		t.Fatal(err)
	}
	again := f.ensure(rec.ID)
	if got := f.git(again.Path, "rev-parse", "HEAD"); got != tip {
		t.Fatalf("re-attached worktree at %s, want its kept branch %s", got, tip)
	}
	if again.Unpublished != 1 || !again.CreatedAt.Equal(w.CreatedAt) {
		t.Fatalf("re-attached row = %+v, want one unpublished commit and the first creation time", again)
	}
}

func TestPublishMovesTheStoreAndRecordsIt(t *testing.T) {
	f := newFixture(t)
	rec := f.session()
	if _, err := f.svc.Publish(f.ctx, rec.ID); !errors.Is(err, scriptstore.ErrNoWorktree) {
		t.Fatalf("publish without a worktree = %v, want ErrNoWorktree", err)
	}
	w := f.ensure(rec.ID)
	f.commit(w.Path, "projects/nter/a.yaml", "a\n")
	out, err := f.svc.Publish(f.ctx, rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if out.Result.Outcome != ports.PublishFastForward || out.Worktree.Unpublished != 0 || out.Worktree.PublishedAt.IsZero() {
		t.Fatalf("publish = %+v", out)
	}
	if got := f.row(rec.ID); got.Unpublished != 0 || got.PublishedAt.IsZero() {
		t.Fatalf("row after publish = %+v", got)
	}
}

func TestConcurrentPublishesIntoOneStoreAllLand(t *testing.T) {
	f := newFixture(t)
	const n = 4
	owners := make([]domain.SessionID, n)
	for i := range owners {
		rec := f.session()
		owners[i] = rec.ID
		w := f.ensure(rec.ID)
		f.commit(w.Path, "projects/nter/"+string(rec.ID)+".yaml", "x\n")
	}
	var wg sync.WaitGroup
	results := make([]scriptstore.Published, n)
	errs := make([]error, n)
	for i, owner := range owners {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i], errs[i] = f.svc.Publish(f.ctx, owner)
		}()
	}
	wg.Wait()
	for i := range owners {
		if errs[i] != nil || results[i].Result.Outcome == ports.PublishRefused {
			t.Fatalf("publish %s = %+v, %v; want every publish to land", owners[i], results[i].Result, errs[i])
		}
		if _, err := os.Stat(filepath.Join(f.scripts, "projects/nter", string(owners[i])+".yaml")); err != nil {
			t.Fatalf("%s's script is not in the store: %v", owners[i], err)
		}
	}
}

func TestSettle(t *testing.T) {
	t.Run("check reports and keeps everything", func(t *testing.T) {
		f := newFixture(t)
		rec := f.session()
		w := f.ensure(rec.ID)
		f.commit(w.Path, "projects/nter/a.yaml", "a\n")
		f.write(w.Path, "projects/nter/draft.yaml", "draft\n")
		got, err := f.svc.Settle(f.ctx, rec.ID, scriptstore.SettleCheck)
		if err != nil {
			t.Fatal(err)
		}
		if !got.Blocked || got.Removed || !reflect.DeepEqual(got.Worktree.Uncommitted, []string{"projects/nter/draft.yaml"}) || got.Publish.Outcome != ports.PublishFastForward {
			t.Fatalf("settle = %+v, want blocked by the draft after publishing the commit", got)
		}
		if f.row(rec.ID).State != domain.ScriptsStoreActive {
			t.Fatal("a check changed the row's state")
		}
	})
	t.Run("keep holds uncommitted work", func(t *testing.T) {
		f := newFixture(t)
		rec := f.session()
		w := f.ensure(rec.ID)
		f.write(w.Path, "projects/nter/draft.yaml", "draft\n")
		got, err := f.svc.Settle(f.ctx, rec.ID, scriptstore.SettleKeep)
		if err != nil {
			t.Fatal(err)
		}
		row := f.row(rec.ID)
		if !got.Blocked || got.Removed || row.State != domain.ScriptsStoreHeld || row.HeldReason != domain.HoldUncommitted || !reflect.DeepEqual(row.HeldFiles, []string{"projects/nter/draft.yaml"}) {
			t.Fatalf("settle = %+v, row = %+v; want held over the draft", got, row)
		}
		if _, err := os.Stat(filepath.Join(w.Path, "projects/nter/draft.yaml")); err != nil {
			t.Fatalf("held worktree lost the draft: %v", err)
		}

		restored := f.ensure(rec.ID)
		if restored.State != domain.ScriptsStoreActive || restored.HeldReason != "" {
			t.Fatalf("restored row = %+v, want active again", restored)
		}
	})
	t.Run("keep holds a conflicting branch", func(t *testing.T) {
		f := newFixture(t)
		rec := f.session()
		w := f.ensure(rec.ID)
		f.commit(w.Path, "projects/nter/login.yaml", "session\n")
		f.commit(f.scripts, "projects/nter/login.yaml", "person\n")
		if _, err := f.svc.Settle(f.ctx, rec.ID, scriptstore.SettleKeep); err != nil {
			t.Fatal(err)
		}
		row := f.row(rec.ID)
		if row.State != domain.ScriptsStoreHeld || row.HeldReason != domain.HoldPublishConflict || !reflect.DeepEqual(row.HeldFiles, []string{"projects/nter/login.yaml"}) {
			t.Fatalf("row = %+v, want held over the conflict", row)
		}
	})
	t.Run("keep removes a clean worktree once published", func(t *testing.T) {
		f := newFixture(t)
		rec := f.session()
		w := f.ensure(rec.ID)
		f.commit(w.Path, "projects/nter/a.yaml", "a\n")
		got, err := f.svc.Settle(f.ctx, rec.ID, scriptstore.SettleKeep)
		if err != nil {
			t.Fatal(err)
		}
		if got.Blocked || !got.Removed || got.Publish.Outcome != ports.PublishFastForward || f.row(rec.ID).State != domain.ScriptsStoreRemoved {
			t.Fatalf("settle = %+v", got)
		}
		if _, err := os.Stat(w.Path); !os.IsNotExist(err) {
			t.Fatalf("worktree folder still there: %v", err)
		}
		if _, err := os.Stat(filepath.Join(f.scripts, "projects/nter/a.yaml")); err != nil {
			t.Fatalf("the script did not reach the store: %v", err)
		}
		if again, err := f.svc.Settle(f.ctx, rec.ID, scriptstore.SettleKeep); err != nil || again.Present {
			t.Fatalf("settle after removal = %+v, %v; want nothing to do", again, err)
		}
	})
	t.Run("discard removes whatever it holds", func(t *testing.T) {
		f := newFixture(t)
		rec := f.session()
		w := f.ensure(rec.ID)
		f.write(w.Path, "projects/nter/draft.yaml", "draft\n")
		got, err := f.svc.Settle(f.ctx, rec.ID, scriptstore.SettleDiscard)
		if err != nil || !got.Removed {
			t.Fatalf("discard = %+v, %v", got, err)
		}
		if out := f.git(f.scripts, "branch", "--list", w.Branch); out != "" {
			t.Fatalf("branch %s survived a discard", w.Branch)
		}
	})
}

func TestRefreshWritesFactsOnlyWhenTheyChange(t *testing.T) {
	f := newFixture(t)
	rec := f.session()
	w := f.ensure(rec.ID)
	before := f.row(rec.ID).UpdatedAt
	if err := f.svc.Refresh(f.ctx); err != nil {
		t.Fatal(err)
	}
	if got := f.row(rec.ID).UpdatedAt; !got.Equal(before) {
		t.Fatalf("refresh with nothing changed rewrote the row (%v -> %v)", before, got)
	}
	f.commit(w.Path, "projects/nter/a.yaml", "a\n")
	f.write(w.Path, "projects/nter/b.yaml", "b\n")
	if err := f.svc.Refresh(f.ctx); err != nil {
		t.Fatal(err)
	}
	row := f.row(rec.ID)
	if row.Unpublished != 1 || !reflect.DeepEqual(row.Uncommitted, []string{"projects/nter/b.yaml"}) {
		t.Fatalf("row after refresh = %+v", row)
	}
}

func TestReconcile(t *testing.T) {
	f := newFixture(t)
	live := f.session()
	ended := f.session()
	liveW := f.ensure(live.ID)
	endedW := f.ensure(ended.ID)
	ended.IsTerminated = true
	if err := f.db.UpdateSession(f.ctx, ended); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{liveW.Path, endedW.Path} {
		if err := os.RemoveAll(p); err != nil {
			t.Fatal(err)
		}
	}
	orphan, _ := scriptstore.Layout(f.data, f.scripts, "nter-orphan")
	f.git(f.scripts, "worktree", "add", "-q", "-b", "ao/nter-orphan", orphan)

	if err := f.svc.Reconcile(f.ctx); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if _, err := os.Stat(liveW.Path); err != nil {
		t.Fatalf("a live session's worktree was not recreated: %v", err)
	}
	if _, err := os.Stat(endedW.Path); !os.IsNotExist(err) {
		t.Fatalf("an ended session's worktree was recreated: %v", err)
	}
	if _, err := os.Stat(orphan); err != nil {
		t.Fatalf("the orphan worktree was touched: %v", err)
	}
	if !strings.Contains(f.logs.String(), "no session owns") || !strings.Contains(f.logs.String(), "nter-orphan") {
		t.Fatalf("the orphan was not reported; logs:\n%s", f.logs)
	}
}
