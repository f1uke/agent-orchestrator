package review

import (
	stdctx "context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// The ephemeral reviewer is a READER of the worker's checkout - reviewerFloor
// forbids it from pushing commits, editing files or touching the branch - so it
// is not a participant in one-awake-at-a-time: it never takes the slot and never
// stops a member from taking one. But a reader can still see a half-written file,
// and a review of a torn tree is worse than no review. So it obeys exactly one
// scheduling rule: it starts only while nobody is writing.

// engineWithTreeWriter builds the engine through Deps, so the wiring from the
// dependency to the field is covered too rather than being reached around.
func engineWithTreeWriter(store Store, launcher Launcher, writer func(stdctx.Context, domain.SessionRecord) (domain.SessionID, bool, error)) *Engine {
	return New(Deps{
		Store: store, Sessions: fakeSessions{rec: liveWorker(), ok: true}, PRs: prAt("sha1"),
		Projects: fakeProjects{}, Launcher: launcher, TreeWriter: writer,
		Clock: func() time.Time { return time.Unix(0, 0).UTC() },
		NewID: func() string { return "id-1" },
	})
}

// TestTriggerRefusesWhileTheCheckoutIsBeingWritten: an agent is awake in the tree,
// so the review waits for the gap. Nothing is launched and no run is recorded -
// a refused pass must leave the board exactly as it found it, or the next trigger
// would see a phantom run for this commit and skip it.
func TestTriggerRefusesWhileTheCheckoutIsBeingWritten(t *testing.T) {
	store := &fakeStore{}
	launcher := &fakeLauncher{handle: "review-mer-1"}
	eng := engineWithTreeWriter(store, launcher, func(stdctx.Context, domain.SessionRecord) (domain.SessionID, bool, error) {
		return "mer-2", true, nil
	})

	_, err := eng.Trigger(stdctx.Background(), "mer-1")
	if !errors.Is(err, ErrTreeBusy) {
		t.Fatalf("Trigger while an agent writes the checkout = %v, want ErrTreeBusy", err)
	}
	if launcher.spawned || launcher.notified {
		t.Fatalf("a reviewer was started over a tree being written: %+v", launcher)
	}
	if len(store.runs) != 0 || store.review != nil {
		t.Fatalf("the refused pass recorded state: review=%+v runs=%+v", store.review, store.runs)
	}
}

// TestTriggerRunsInTheGap: the writer stood down, so the reader may read.
func TestTriggerRunsInTheGap(t *testing.T) {
	store := &fakeStore{}
	launcher := &fakeLauncher{handle: "review-mer-1"}
	eng := engineWithTreeWriter(store, launcher, func(stdctx.Context, domain.SessionRecord) (domain.SessionID, bool, error) {
		return "", false, nil
	})

	res, err := eng.Trigger(stdctx.Background(), "mer-1")
	if err != nil {
		t.Fatalf("Trigger in the handoff gap: %v", err)
	}
	if !res.Created || !launcher.spawned {
		t.Fatalf("no reviewer ran in the gap: %+v / %+v", res, launcher)
	}
}

// TestTriggerIsUnchangedWithNoTreeWriterWired is the preservation guard: a solo
// worker's tree has one writer and that writer IS the session under review.
// Reviewing while it works is what AO has always done, and the daemon reports
// nobody for it - as does an Engine with the hook left unwired.
func TestTriggerIsUnchangedWithNoTreeWriterWired(t *testing.T) {
	store := &fakeStore{}
	launcher := &fakeLauncher{handle: "review-mer-1"}
	eng := newEngineForTest(store, fakeSessions{rec: liveWorker(), ok: true}, prAt("sha1"), fakeProjects{}, launcher)

	res, err := eng.Trigger(stdctx.Background(), "mer-1")
	if err != nil {
		t.Fatalf("Trigger with no TreeWriter wired: %v", err)
	}
	if !res.Created || !launcher.spawned {
		t.Fatalf("the unwired path stopped reviewing: %+v / %+v", res, launcher)
	}
}

// TestTriggerSurfacesATreeWriterFailure: an unanswerable question about who is
// writing is not an answer of "nobody". Refuse, and let the next trigger ask
// again - the same direction every other probe in this daemon errs in.
func TestTriggerSurfacesATreeWriterFailure(t *testing.T) {
	store := &fakeStore{}
	launcher := &fakeLauncher{handle: "review-mer-1"}
	boom := errors.New("store unavailable")
	eng := engineWithTreeWriter(store, launcher, func(stdctx.Context, domain.SessionRecord) (domain.SessionID, bool, error) {
		return "", false, boom
	})

	if _, err := eng.Trigger(stdctx.Background(), "mer-1"); !errors.Is(err, boom) {
		t.Fatalf("Trigger = %v, want the tree-writer failure surfaced", err)
	}
	if launcher.spawned {
		t.Fatal("a reviewer was started although who is writing could not be determined")
	}
}
