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

// recordingJoiner is the crew's half of the lazy-creation trigger, standing in
// for the session manager.
type recordingJoiner struct {
	mu      sync.Mutex
	touches []struct {
		id     domain.SessionID
		reason domain.CrewJoinReason
	}
}

func (r *recordingJoiner) NoteRuntimeTouch(_ context.Context, id domain.SessionID, reason domain.CrewJoinReason) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.touches = append(r.touches, struct {
		id     domain.SessionID
		reason domain.CrewJoinReason
	}{id, reason})
}

func (r *recordingJoiner) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.touches)
}

// The device every test here drives. A lease key is normalized, so any valid
// UDID does; this is the one the rest of the package uses.
const udid = "087DF306-1FC9-4E5A-B9ED-AD36D6A1A0F1"

func joinerService(t *testing.T, now func() time.Time) (*sim.Service, *sqlite.Store, *recordingJoiner) {
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
	joiner := &recordingJoiner{}
	return sim.New(store, sim.WithClock(now), sim.WithCrewJoiner(joiner)), store, joiner
}

// TestAcquire_ReportsTheRuntimeTouch. Taking the lease is the daemon-side FACT
// that says "this task has a device": it is what creates the task's qa, and it
// needs no instrumentation beyond the lease AO already owns.
func TestAcquire_ReportsTheRuntimeTouch(t *testing.T) {
	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	svc, store, joiner := joinerService(t, fixedClock(now))
	session := newSession(t, store, now)

	if _, err := svc.Acquire(context.Background(), session, udid, 0); err != nil {
		t.Fatalf("Acquire: %v", err)
	}

	if joiner.count() != 1 {
		t.Fatalf("a granted lease reported %d touches, want 1", joiner.count())
	}
	got := joiner.touches[0]
	if got.id != session || got.reason != domain.CrewJoinSim {
		t.Fatalf("reported %s/%q, want %s/%q", got.id, got.reason, session, domain.CrewJoinSim)
	}
}

// TestAcquire_ReportsNothingWhenTheDeviceIsRefused: the touch is a GRANT, not an
// attempt. A session that was refused the device is not driving it.
func TestAcquire_ReportsNothingWhenTheDeviceIsRefused(t *testing.T) {
	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	svc, store, joiner := joinerService(t, fixedClock(now))
	holder := newSession(t, store, now)
	other := newSession(t, store, now)

	if _, err := svc.Acquire(context.Background(), holder, udid, 0); err != nil {
		t.Fatalf("Acquire(holder): %v", err)
	}
	before := joiner.count()
	if _, err := svc.Acquire(context.Background(), other, udid, 0); err == nil {
		t.Fatal("a device held by another session was granted")
	}
	if joiner.count() != before {
		t.Fatalf("a refused claim reported a runtime touch (%d -> %d)", before, joiner.count())
	}
}

// TestRelease_ReportsNothing: letting the device go is not evidence that a task
// has one, and re-reporting on release would say the opposite of what happened.
func TestRelease_ReportsNothing(t *testing.T) {
	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	svc, store, joiner := joinerService(t, fixedClock(now))
	session := newSession(t, store, now)

	if _, err := svc.Acquire(context.Background(), session, udid, 0); err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	before := joiner.count()
	if err := svc.Release(context.Background(), session, udid); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if joiner.count() != before {
		t.Fatalf("releasing reported a runtime touch (%d -> %d)", before, joiner.count())
	}
}

// TestAcquire_WithNoJoinerIsUnchanged: every caller that is not the daemon wires
// no observer, and a claim must work exactly as it always has.
func TestAcquire_WithNoJoinerIsUnchanged(t *testing.T) {
	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	svc, store := newService(t, fixedClock(now))
	session := newSession(t, store, now)

	if _, err := svc.Acquire(context.Background(), session, udid, 0); err != nil {
		t.Fatalf("Acquire with no crew joiner: %v", err)
	}
}
