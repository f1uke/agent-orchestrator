package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	simsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/sim"
	"github.com/aoagents/agent-orchestrator/backend/internal/simbridge"
	"github.com/aoagents/agent-orchestrator/backend/internal/simstream"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// fakeSimScreenReader is a screen that always answers with one labeled button,
// standing in for the resident bridge the daemon really passes (a NodeDriver
// needs Node, a mac and a booted simulator, none of which CI has).
type fakeSimScreenReader struct{ calls int }

func (f *fakeSimScreenReader) AX(context.Context, string) (simbridge.Snapshot, error) {
	f.calls++
	return simbridge.Snapshot{
		Frontmost: simbridge.Frontmost{BundleID: "com.app.a"},
		Elements: []simbridge.Element{{
			Path:  "0",
			Label: "Continue",
			Box:   &simbridge.Box{X1: 0.1, Y1: 0.1, X2: 0.5, Y2: 0.3},
			Tap:   &simbridge.Point{X: 0.3, Y: 0.2},
		}},
	}, nil
}

// TestWiring_SimServiceRecordsGestures is the test no test inside
// internal/service/sim can be: every one of those injects a recorder itself,
// so the whole suite stayed green while the daemon built the service with no
// recorder at all - `record start` succeeded, `status` reported zero steps for
// ever, and `stop` wrote a header-only flow.
//
// This drives the service the daemon actually constructs (newSimService, the
// one call daemon.Run makes) through a real store and one whole gesture, and
// asserts a step came out the far end. A service built without WithRecorder
// records nothing here, so this fails the moment that wiring is dropped again.
func TestWiring_SimServiceRecordsGestures(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	screen := &fakeSimScreenReader{}
	svc := newSimService(store, screen)

	const udid = "087DF306-1FC9-4E5A-B9ED-AD36D6A1A0F1"
	now := time.Now().UTC()
	if err := store.UpsertProject(ctx, domain.ProjectRecord{ID: "mer", Path: "/tmp/mer", RegisteredAt: now}); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	session, err := store.CreateSession(ctx, domain.SessionRecord{
		ProjectID: "mer", Kind: domain.KindWorker,
		Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: now},
	})
	if err != nil {
		t.Fatalf("seed session: %v", err)
	}
	if _, err := svc.Acquire(ctx, session.ID, udid, 0); err != nil {
		t.Fatalf("claim the device: %v", err)
	}
	if _, err := svc.StartRecording(ctx, session.ID, udid, "flow"); err != nil {
		t.Fatalf("start recording: %v", err)
	}

	hold, err := svc.AcquireHold(ctx, session.ID, udid, 0, simsvc.GestureIntent{Kind: "tap", X: 0.3, Y: 0.2})
	if err != nil {
		t.Fatalf("hold: %v", err)
	}
	if screen.calls != 1 {
		t.Fatalf("the screen was read %d times while recording a gesture; want 1 - "+
			"the daemon's service was built without a recorder", screen.calls)
	}
	if err := svc.ReleaseHold(ctx, udid, hold.Token, simsvc.GestureOutcome{Performed: true}); err != nil {
		t.Fatalf("release: %v", err)
	}

	_, steps, err := svc.StopRecording(ctx, session.ID, udid)
	if err != nil {
		t.Fatalf("stop recording: %v", err)
	}
	if len(steps) != 1 {
		t.Fatalf("steps = %d, want 1 - the daemon's own service must record the gestures it brackets", len(steps))
	}
	if steps[0].Selector != "Continue" {
		t.Fatalf("selector = %q, want the label the screen reported", steps[0].Selector)
	}
}

// TestWiring_SimScreenIsAScreenReader pins the other half of the wiring: the
// daemon hands newSimService its one resident simstream.Screen, so that type
// has to satisfy the recorder's ScreenReader without an adapter in between. A
// second bridge process would be a second Node, a second addon load and a
// second finger on a device that has one.
func TestWiring_SimScreenIsAScreenReader(t *testing.T) {
	screen := simstream.NewScreen(t.TempDir())
	t.Cleanup(screen.Shutdown)
	// Compiles only if *simstream.Screen implements simsvc.ScreenReader, which
	// is what daemon.Run relies on.
	if svc := newSimService(nil, screen); svc == nil {
		t.Fatal("newSimService returned nil")
	}
}
