package review

import (
	stdctx "context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/treewatch"
)

// A reviewer READS the worker's checkout while both crew members write it.
//
// It used to refuse to start while anybody was awake in that tree. Both members
// are awake continuously now, so the refusal would fire every time and review
// would never run at all. What replaced it is the mechanism qa already uses: the
// pass is bracketed with a write-generation lease, and a pass the tree moved
// under is thrown away instead of recorded.
//
// The preservation half matters as much: a SOLO worker takes no lease, because
// its tree has exactly one writer and that writer is the session being reviewed.

func crewWorker(t *testing.T) domain.SessionRecord {
	t.Helper()
	return domain.SessionRecord{
		ID:        "mer-1",
		ProjectID: "mer",
		Harness:   domain.HarnessClaudeCode,
		CrewID:    "mer-1",
		CrewRole:  domain.CrewRoleDev,
		Metadata:  domain.SessionMetadata{WorkspacePath: gitWorktreeForReview(t)},
	}
}

func gitWorktreeForReview(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(root, "app.go"), []byte("package app\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return root
}

func engineWithWatcher(t *testing.T, store Store, launcher Launcher, worker domain.SessionRecord, watcher Watcher) *Engine {
	t.Helper()
	return New(Deps{
		Store: store, Sessions: fakeSessions{rec: worker, ok: true}, PRs: prAt("sha1"),
		Projects: fakeProjects{}, Launcher: launcher, Watcher: watcher,
		Clock: func() time.Time { return time.Unix(0, 0).UTC() },
		NewID: func() string { return "id-1" },
	})
}

func liveRegistry(t *testing.T) *treewatch.Registry {
	t.Helper()
	reg := treewatch.NewRegistry(treewatch.Options{})
	t.Cleanup(reg.Close)
	return reg
}

// TestTriggerRunsWhileTheCrewWrites: the refusal is gone. A crew's tree always
// has a writer in it, so a review that waited for a quiet one would never start.
func TestTriggerRunsWhileTheCrewWrites(t *testing.T) {
	store := &fakeStore{}
	launcher := &fakeLauncher{handle: "review-mer-1"}
	eng := engineWithWatcher(t, store, launcher, crewWorker(t), liveRegistry(t))

	res, err := eng.Trigger(stdctx.Background(), "mer-1")
	if err != nil {
		t.Fatalf("Trigger over a crew's shared checkout: %v", err)
	}
	if !res.Created || !launcher.spawned {
		t.Fatalf("no reviewer ran: %+v / %+v", res, launcher)
	}
}

// TestBracketOnAQuietTreeIsClean: nothing wrote, so the verdict may be recorded.
func TestBracketOnAQuietTreeIsClean(t *testing.T) {
	store := &fakeStore{}
	worker := crewWorker(t)
	eng := engineWithWatcher(t, store, &fakeLauncher{handle: "review-mer-1"}, worker, liveRegistry(t))
	ctx := stdctx.Background()
	if _, err := eng.Trigger(ctx, "mer-1"); err != nil {
		t.Fatalf("Trigger: %v", err)
	}

	if v := eng.CloseBracket(ctx, "mer-1", store.runs[0].ID); v.Discard {
		t.Fatalf("a review of an untouched tree was discarded: %s", v.Reason)
	}
}

// THE ONE THAT MATTERS. A crew member writes the checkout while the reviewer is
// reading it, so the diff the reviewer judged is not the diff that is there. The
// verdict is thrown away and the paths that moved are named.
func TestBracketDiscardsAReviewTheTreeMovedUnder(t *testing.T) {
	store := &fakeStore{}
	worker := crewWorker(t)
	reg := liveRegistry(t)
	eng := engineWithWatcher(t, store, &fakeLauncher{handle: "review-mer-1"}, worker, reg)
	ctx := stdctx.Background()
	if _, err := eng.Trigger(ctx, "mer-1"); err != nil {
		t.Fatalf("Trigger: %v", err)
	}
	runID := store.runs[0].ID

	// dev types, mid-review.
	if err := os.WriteFile(filepath.Join(worker.Metadata.WorkspacePath, "app.go"), []byte("package app // still writing\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	waitForWrite(t, reg, worker.Metadata.WorkspacePath)

	v := eng.CloseBracket(ctx, "mer-1", runID)
	if !v.Discard {
		t.Fatal("a review that read a tree being written was recorded as a verdict")
	}
	if !strings.Contains(v.Reason, "app.go") {
		t.Fatalf("the discard did not name what moved: %q", v.Reason)
	}
}

// TestBracketDiscardsWhenNothingWatched: the lease is gone (the daemon restarted
// mid-review), so nothing can vouch for what the reviewer read. That is
// discarded, never recorded - the same rule the crew-run bracket obeys, because
// certifying on a detector that was not running is the failure this whole shape
// exists to prevent.
func TestBracketDiscardsWhenNothingWatched(t *testing.T) {
	store := &fakeStore{}
	worker := crewWorker(t)
	eng := engineWithWatcher(t, store, &fakeLauncher{handle: "review-mer-1"}, worker, liveRegistry(t))
	ctx := stdctx.Background()

	v := eng.CloseBracket(ctx, "mer-1", "a-run-from-a-previous-process")
	if !v.Discard {
		t.Fatal("a review nothing watched was allowed to record a verdict")
	}
}

// TestSoloReviewIsNeverBracketed is the preservation guard. A solo worker's tree
// has one writer and it IS the session under review, so no lease is taken - which
// also means a daemon restart cannot cost a solo review anything.
func TestSoloReviewIsNeverBracketed(t *testing.T) {
	store := &fakeStore{}
	launcher := &fakeLauncher{handle: "review-mer-1"}
	eng := engineWithWatcher(t, store, launcher, liveWorker(), liveRegistry(t))
	ctx := stdctx.Background()

	res, err := eng.Trigger(ctx, "mer-1")
	if err != nil {
		t.Fatalf("Trigger for a solo worker: %v", err)
	}
	if !res.Created || !launcher.spawned {
		t.Fatalf("the solo path stopped reviewing: %+v / %+v", res, launcher)
	}
	if v := eng.CloseBracket(ctx, "mer-1", store.runs[0].ID); v.Discard {
		t.Fatalf("a solo review was discarded by a bracket it never had: %s", v.Reason)
	}
}

// TestTriggerIsUnchangedWithNoWatcherWired: an Engine with no detector wired -
// every test built before this existed, and any daemon that cannot start one -
// reviews exactly as it always did.
func TestTriggerIsUnchangedWithNoWatcherWired(t *testing.T) {
	store := &fakeStore{}
	launcher := &fakeLauncher{handle: "review-mer-1"}
	eng := newEngineForTest(store, fakeSessions{rec: crewWorker(t), ok: true}, prAt("sha1"), fakeProjects{}, launcher)

	res, err := eng.Trigger(stdctx.Background(), "mer-1")
	if err != nil {
		t.Fatalf("Trigger with no Watcher wired: %v", err)
	}
	if !res.Created || !launcher.spawned {
		t.Fatalf("the unwired path stopped reviewing: %+v / %+v", res, launcher)
	}
	if v := eng.CloseBracket(stdctx.Background(), "mer-1", store.runs[0].ID); v.Discard {
		t.Fatalf("an unbracketed review was discarded: %s", v.Reason)
	}
}

// waitForWrite gives the filesystem event a moment to land: writing a file and
// immediately closing the bracket would test the scheduler, not the detector.
func waitForWrite(t *testing.T, reg *treewatch.Registry, root string) {
	t.Helper()
	lease, err := reg.Attach(stdctx.Background(), root)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	defer lease.Release()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if got, err := lease.Generation(); err == nil && got > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the watcher never saw the write")
}
