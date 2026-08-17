package reclaimer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/reclaimlog"
	sessionsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/session"
)

type fakePaths struct {
	known []string
	err   error
}

func (f fakePaths) ListKnownWorkspacePaths(context.Context) ([]string, error) {
	return f.known, f.err
}

// worktreeRoot builds <root>/<project>/<kind>/<name> directories and returns
// the root plus the full paths, mirroring the real managed layout.
func worktreeRoot(t *testing.T, names ...string) (string, []string) {
	t.Helper()
	root := t.TempDir()
	paths := make([]string, 0, len(names))
	for _, n := range names {
		p := filepath.Join(root, "demo", "feature", n)
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}
	return root, paths
}

func orphanReclaimer(t *testing.T, root string, paths workspacePathLister, log auditLog) *Reclaimer {
	t.Helper()
	now := time.Unix(1_000_000, 0)
	return New(&fakeSvc{}, on(15), Config{
		Clock:        func() time.Time { return now },
		SelfPath:     filepath.Join(t.TempDir(), "somewhere-else"),
		WorktreeRoot: root,
		Paths:        paths,
		Audit:        log,
	})
}

// TestOrphans_RemovesEmptyUnownedDirectories covers the leftover-directory gap:
// zero-byte worktree directories that no session record claims.
func TestOrphans_RemovesEmptyUnownedDirectories(t *testing.T) {
	root, paths := worktreeRoot(t, "leftover-a", "leftover-b")
	log := &memLog{}
	r := orphanReclaimer(t, root, fakePaths{known: nil}, log)

	if err := r.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}

	for _, p := range paths {
		if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("empty orphan %s should have been removed: %v", p, err)
		}
	}
	if len(log.entries) != 2 {
		t.Fatalf("both removals must be logged, got %+v", log.entries)
	}
	for _, e := range log.entries {
		if e.Action != reclaimlog.ActionReclaimed || e.Qualified != "orphan" {
			t.Fatalf("entry = %+v", e)
		}
	}
}

// TestOrphans_NeverTouchesAnOwnedWorktree: a directory a session record claims
// is not an orphan, however empty it looks.
func TestOrphans_NeverTouchesAnOwnedWorktree(t *testing.T) {
	root, paths := worktreeRoot(t, "owned")
	r := orphanReclaimer(t, root, fakePaths{known: paths}, &memLog{})

	if err := r.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(paths[0]); err != nil {
		t.Fatalf("an owned worktree must survive: %v", err)
	}
}

// TestOrphans_NeverDeletesANonEmptyOrphan: an unowned directory with content in
// it has no recovery route (no record means no branch name), so it is reported,
// never removed.
func TestOrphans_NeverDeletesANonEmptyOrphan(t *testing.T) {
	root, paths := worktreeRoot(t, "has-content")
	if err := os.WriteFile(filepath.Join(paths[0], "something.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	log := &memLog{}
	r := orphanReclaimer(t, root, fakePaths{known: nil}, log)

	if err := r.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(paths[0]); err != nil {
		t.Fatalf("a non-empty orphan must be preserved: %v", err)
	}
	if len(log.entries) != 1 || log.entries[0].Action != reclaimlog.ActionSkipped ||
		log.entries[0].Reason != reasonOrphanNotEmpty {
		t.Fatalf("it must be reported so the user can decide: %+v", log.entries)
	}
}

// TestOrphans_AbortsWhenThereAreSuspiciouslyMany: an unexpectedly large orphan
// set means the ownership lookup is broken, not that the user made a mess. The
// sweep must stop rather than delete.
func TestOrphans_AbortsWhenThereAreSuspiciouslyMany(t *testing.T) {
	names := make([]string, maxOrphansPerSweep+1)
	for i := range names {
		names[i] = "leftover-" + string(rune('a'+i%26)) + string(rune('a'+i/26))
	}
	root, paths := worktreeRoot(t, names...)
	log := &memLog{}
	// Ownership resolution "fails": nothing is reported as owned.
	r := orphanReclaimer(t, root, fakePaths{known: nil}, log)

	if err := r.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}

	for _, p := range paths {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("nothing may be deleted once the sweep aborts: %v", err)
		}
	}
	if len(log.entries) != 1 || log.entries[0].Action != reclaimlog.ActionAborted ||
		log.entries[0].Reason != reasonOrphanFlood {
		t.Fatalf("the abort must be recorded loudly: %+v", log.entries)
	}
}

// TestOrphans_SkipsWhenOwnershipCannotBeRead: if the owned set is unavailable,
// EVERYTHING would look orphaned. The sweep must not run at all.
func TestOrphans_SkipsWhenOwnershipCannotBeRead(t *testing.T) {
	root, paths := worktreeRoot(t, "leftover")
	r := orphanReclaimer(t, root, fakePaths{err: errors.New("db down")}, &memLog{})

	if err := r.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(paths[0]); err != nil {
		t.Fatalf("nothing may be deleted when ownership is unknown: %v", err)
	}
}

// TestOrphans_NeverRemovesTheDirectoryTheSweepRunsIn.
func TestOrphans_NeverRemovesTheDirectoryTheSweepRunsIn(t *testing.T) {
	root, paths := worktreeRoot(t, "self")
	now := time.Unix(1_000_000, 0)
	r := New(&fakeSvc{}, on(15), Config{
		Clock:        func() time.Time { return now },
		SelfPath:     paths[0], // running inside the orphan
		WorktreeRoot: root,
		Paths:        fakePaths{known: nil},
		Audit:        &memLog{},
	})

	if err := r.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(paths[0]); err != nil {
		t.Fatalf("the sweep removed the directory it is running in: %v", err)
	}
}

// TestOrphans_DisabledWithoutARoot: the sweep is opt-in on configuration, so a
// Reclaimer built without a worktree root never scans anything.
func TestOrphans_DisabledWithoutARoot(t *testing.T) {
	_, paths := worktreeRoot(t, "leftover")
	r := orphanReclaimer(t, "", fakePaths{known: nil}, &memLog{})

	if err := r.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(paths[0]); err != nil {
		t.Fatalf("no root configured means no scan: %v", err)
	}
}

// TestOrphans_LogsEachDecisionOnce keeps the audit log readable across the many
// ticks of a long-running daemon.
func TestOrphans_LogsEachDecisionOnce(t *testing.T) {
	root, paths := worktreeRoot(t, "has-content")
	if err := os.WriteFile(filepath.Join(paths[0], "x.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	log := &memLog{}
	r := orphanReclaimer(t, root, fakePaths{known: nil}, log)

	for i := 0; i < 5; i++ {
		if err := r.Tick(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if len(log.entries) != 1 {
		t.Fatalf("want one line for the repeated decision, got %d", len(log.entries))
	}
}

func TestWorktreeDirs_ListsOnlyFullDepthDirectories(t *testing.T) {
	root, paths := worktreeRoot(t, "one", "two")
	// A stray file and a too-shallow directory must not be reported.
	if err := os.WriteFile(filepath.Join(root, "stray.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "shallow"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := worktreeDirs(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(paths) {
		t.Fatalf("got %v, want the %d full-depth worktrees", got, len(paths))
	}
}

// A candidate list and an orphan scan coexist in one tick without interfering.
func TestTick_ReclaimAndOrphanSweepCoexist(t *testing.T) {
	root, paths := worktreeRoot(t, "leftover")
	now := time.Unix(1_000_000, 0)
	log := &memLog{}
	svc := &fakeSvc{candidates: []sessionsvc.ReclaimCandidate{candidate("sess-1", now)}}
	r := New(svc, on(15), Config{
		Clock:        func() time.Time { return now },
		SelfPath:     filepath.Join(t.TempDir(), "elsewhere"),
		WorktreeRoot: root,
		Paths:        fakePaths{known: nil},
		Audit:        log,
	})

	runPast(t, r, &now, 15*time.Minute)

	if len(svc.reclaimed) != 1 {
		t.Fatalf("the session sweep must still run, got %v", svc.reclaimed)
	}
	if _, err := os.Stat(paths[0]); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the orphan sweep must still run: %v", err)
	}
}
