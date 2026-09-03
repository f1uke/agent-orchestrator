package sim_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/sim"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// recordingWatcher is the "this task drove the app" observer, standing in for
// the session manager.
type recordingWatcher struct {
	mu      sync.Mutex
	touches []struct {
		id    domain.SessionID
		touch domain.RuntimeTouch
	}
}

func (r *recordingWatcher) NoteRuntimeTouch(_ context.Context, id domain.SessionID, touch domain.RuntimeTouch) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.touches = append(r.touches, struct {
		id    domain.SessionID
		touch domain.RuntimeTouch
	}{id, touch})
}

func (r *recordingWatcher) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.touches)
}

// The device every test here drives. A lease key is normalized, so any valid
// UDID does; this is the one the rest of the package uses.
const udid = "087DF306-1FC9-4E5A-B9ED-AD36D6A1A0F1"

func watcherService(t *testing.T, now func() time.Time) (*sim.Service, *sqlite.Store, *recordingWatcher) {
	t.Helper()
	store, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.UpsertProject(context.Background(), domain.ProjectRecord{
		ID: "mer", Path: "/tmp/mer", RegisteredAt: now(),
	}); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	watcher := &recordingWatcher{}
	return sim.New(store, sim.WithClock(now), sim.WithRuntimeWatcher(watcher)), store, watcher
}

// TestAcquire_ReportsTheRuntimeTouch. Taking the lease is the daemon-side FACT
// that says "this task has a device". It creates nobody - dev asks for its own
// qa - and it needs no instrumentation beyond the lease AO already owns.
func TestAcquire_ReportsTheRuntimeTouch(t *testing.T) {
	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	svc, store, watcher := watcherService(t, fixedClock(now))
	session := newSession(t, store, now)

	if _, err := svc.Acquire(context.Background(), session, udid, 0); err != nil {
		t.Fatalf("Acquire: %v", err)
	}

	if watcher.count() != 1 {
		t.Fatalf("a granted lease reported %d touches, want 1", watcher.count())
	}
	got := watcher.touches[0]
	if got.id != session || got.touch != domain.RuntimeTouchSim {
		t.Fatalf("reported %s/%q, want %s/%q", got.id, got.touch, session, domain.RuntimeTouchSim)
	}
}

// TestAcquire_ReportsNothingWhenTheDeviceIsRefused: the touch is a GRANT, not an
// attempt. A session that was refused the device is not driving it.
func TestAcquire_ReportsNothingWhenTheDeviceIsRefused(t *testing.T) {
	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	svc, store, watcher := watcherService(t, fixedClock(now))
	holder := newSession(t, store, now)
	other := newSession(t, store, now)

	if _, err := svc.Acquire(context.Background(), holder, udid, 0); err != nil {
		t.Fatalf("Acquire(holder): %v", err)
	}
	before := watcher.count()
	if _, err := svc.Acquire(context.Background(), other, udid, 0); err == nil {
		t.Fatal("a device held by another session was granted")
	}
	if watcher.count() != before {
		t.Fatalf("a refused claim reported a runtime touch (%d -> %d)", before, watcher.count())
	}
}

// TestRelease_ReportsNothing: letting the device go is not evidence that a task
// has one, and re-reporting on release would say the opposite of what happened.
func TestRelease_ReportsNothing(t *testing.T) {
	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	svc, store, watcher := watcherService(t, fixedClock(now))
	session := newSession(t, store, now)

	if _, err := svc.Acquire(context.Background(), session, udid, 0); err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	before := watcher.count()
	if err := svc.Release(context.Background(), session, udid); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if watcher.count() != before {
		t.Fatalf("releasing reported a runtime touch (%d -> %d)", before, watcher.count())
	}
}

// TestAcquire_WithNoJoinerIsUnchanged: every caller that is not the daemon wires
// no observer, and a claim must work exactly as it always has.
func TestAcquire_WithNoJoinerIsUnchanged(t *testing.T) {
	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	svc, store := newService(t, fixedClock(now))
	session := newSession(t, store, now)

	if _, err := svc.Acquire(context.Background(), session, udid, 0); err != nil {
		t.Fatalf("Acquire with no crew watcher: %v", err)
	}
}
