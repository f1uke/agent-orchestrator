package sim_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/sim"
	"github.com/aoagents/agent-orchestrator/backend/internal/simowner"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// The machine-wide half of the lease: two daemons on one machine - the
// human's and a sandbox daemon a worker runs with its own AO_DATA_DIR - each
// with its own ao.db, sharing only the ownership registry. This is the
// incident of 2026-10-03 as a test: a session on one daemon must not be able
// to claim a device a session on the other is driving.

type machine struct {
	path  string
	mu    sync.Mutex
	dead  map[int]bool
	clock time.Time
}

func (m *machine) alive(pid int) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return !m.dead[pid]
}

func (m *machine) kill(pid int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.dead[pid] = true
}

func (m *machine) now() time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.clock
}

func (m *machine) advance(d time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.clock = m.clock.Add(d)
}

type aoDaemon struct {
	svc   *sim.Service
	store *sqlite.Store
	reg   *simowner.Registry
	dir   string
}

func newMachine(t *testing.T) *machine {
	t.Helper()
	return &machine{
		path:  filepath.Join(t.TempDir(), simowner.FileName),
		dead:  map[int]bool{},
		clock: time.Date(2026, 10, 3, 20, 7, 0, 0, time.UTC),
	}
}

func (m *machine) daemon(t *testing.T, dataDir string, pid, port int) aoDaemon {
	t.Helper()
	store, err := sqlite.Open(dataDir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.UpsertProject(context.Background(), domain.ProjectRecord{
		ID: "mer", Path: "/tmp/mer", RegisteredAt: m.now(),
	}); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	reg, err := simowner.Open(m.path, simowner.Self{DataDir: dataDir, PID: pid, Port: port}, simowner.WithLiveness(m.alive))
	if err != nil {
		t.Fatalf("open registry: %v", err)
	}
	t.Cleanup(func() { _ = reg.Close() })
	svc := sim.New(store, sim.WithClock(m.now), sim.WithOwnership(reg))
	return aoDaemon{svc: svc, store: store, reg: reg, dir: dataDir}
}

func mainAndSandbox(t *testing.T) (*machine, aoDaemon, aoDaemon) {
	t.Helper()
	m := newMachine(t)
	root := t.TempDir()
	return m, m.daemon(t, filepath.Join(root, "main"), 100, 3001), m.daemon(t, filepath.Join(root, "sandbox"), 200, 3399)
}

func TestOwnership_ClaimOnAnotherDaemonsDeviceIsRefusedWithItsHolder(t *testing.T) {
	m, main, sandbox := mainAndSandbox(t)
	ctx := context.Background()
	driver := newSession(t, sandbox.store, m.now())
	intruder := newSession(t, main.store, m.now())

	if _, err := sandbox.svc.Acquire(ctx, driver, udidProMax, 0); err != nil {
		t.Fatalf("sandbox claim: %v", err)
	}
	_, err := main.svc.Acquire(ctx, intruder, udidProMax, 0)
	var held *sim.HeldError
	if !errors.As(err, &held) {
		t.Fatalf("claim on a device driven through another daemon = %v, want a refusal", err)
	}
	if held.Lease.SessionID != driver || held.Lease.OtherDaemon == nil || held.Lease.OtherDaemon.PID != 200 {
		t.Fatalf("refusal names %+v, want %s through the sandbox daemon", held.Lease, driver)
	}
	if msg := err.Error(); !strings.Contains(msg, "port 3399") || !strings.Contains(msg, string(driver)) {
		t.Fatalf("refusal %q does not say who holds it and where", msg)
	}
	// Refused without a trace: the main daemon holds nothing locally.
	if local, _ := main.store.ListSimLeases(ctx, m.now()); len(local) != 0 {
		t.Fatalf("a refused claim left a local lease: %+v", local)
	}
}

func TestOwnership_SameSessionIDOnTheOtherDaemonIsSomebodyElse(t *testing.T) {
	// Both daemons number their sessions from 1, so the incident's two
	// sessions can carry the same id. The id must not be read as ownership.
	m, main, sandbox := mainAndSandbox(t)
	ctx := context.Background()
	driver := newSession(t, sandbox.store, m.now())
	twin := newSession(t, main.store, m.now())
	if driver != twin {
		t.Fatalf("setup: want colliding ids, got %s and %s", driver, twin)
	}
	if _, err := sandbox.svc.Acquire(ctx, driver, udidProMax, 0); err != nil {
		t.Fatal(err)
	}
	var held *sim.HeldError
	if _, err := main.svc.Acquire(ctx, twin, udidProMax, 0); !errors.As(err, &held) {
		t.Fatalf("a same-id session on another daemon was granted the device: %v", err)
	}
	if err := main.svc.Release(ctx, twin, udidProMax); !errors.As(err, &held) {
		t.Fatalf("a same-id session on another daemon released the device: %v", err)
	}
	if _, err := main.svc.TakeOver(ctx, twin, udidProMax, 0); !errors.As(err, &held) {
		t.Fatalf("a take-over crossed daemons: %v", err)
	}
}

func TestOwnership_ListShowsTheOtherDaemonsHolder(t *testing.T) {
	m, main, sandbox := mainAndSandbox(t)
	ctx := context.Background()
	driver := newSession(t, sandbox.store, m.now())
	mine := newSession(t, main.store, m.now())
	if _, err := sandbox.svc.Acquire(ctx, driver, udidProMax, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := main.svc.Acquire(ctx, mine, udidPro, 0); err != nil {
		t.Fatal(err)
	}
	leases, err := main.svc.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	byUDID := map[string]domain.SimLease{}
	for _, l := range leases {
		byUDID[l.UDID] = l
	}
	if got := byUDID[udidProMax]; got.SessionID != driver || got.OtherDaemon == nil || got.OtherDaemon.Port != 3399 {
		t.Fatalf("main lists the sandbox's device as %+v", got)
	}
	if got := byUDID[udidPro]; got.SessionID != mine || got.OtherDaemon != nil {
		t.Fatalf("main lists its own device as %+v", got)
	}
}

func TestOwnership_KilledHolderFreesTheDevice(t *testing.T) {
	m, main, sandbox := mainAndSandbox(t)
	ctx := context.Background()
	driver := newSession(t, sandbox.store, m.now())
	next := newSession(t, main.store, m.now())
	if _, err := sandbox.svc.Acquire(ctx, driver, udidProMax, time.Hour); err != nil {
		t.Fatal(err)
	}
	m.kill(200)
	if _, err := main.svc.Acquire(ctx, next, udidProMax, 0); err != nil {
		t.Fatalf("a device held by a killed sandbox daemon is still refused: %v", err)
	}
}

func TestOwnership_EndedSessionFreesTheDeviceOnTheNextSync(t *testing.T) {
	m, main, sandbox := mainAndSandbox(t)
	ctx := context.Background()
	driver := newSession(t, sandbox.store, m.now())
	next := newSession(t, main.store, m.now())
	if _, err := sandbox.svc.Acquire(ctx, driver, udidProMax, 0); err != nil {
		t.Fatal(err)
	}
	// The session ends on the sandbox daemon. Its trigger drops the local
	// lease - nothing in this service is called.
	rec, _, _ := sandbox.store.GetSession(ctx, driver)
	rec.IsTerminated = true
	if err := sandbox.store.UpdateSession(ctx, rec); err != nil {
		t.Fatal(err)
	}
	if err := sandbox.svc.SyncOwnership(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := main.svc.Acquire(ctx, next, udidProMax, 0); err != nil {
		t.Fatalf("a device whose holding session ended is still refused: %v", err)
	}
}

func TestOwnership_RestartedDaemonGivesUpWhatWasTakenWhileItWasDown(t *testing.T) {
	m := newMachine(t)
	root := t.TempDir()
	mainDir := filepath.Join(root, "main")
	main := m.daemon(t, mainDir, 100, 3001)
	sandbox := m.daemon(t, filepath.Join(root, "sandbox"), 200, 3399)
	ctx := context.Background()
	first := newSession(t, main.store, m.now())
	if _, err := main.svc.Acquire(ctx, first, udidProMax, 0); err != nil {
		t.Fatal(err)
	}
	m.kill(100)
	taker := newSession(t, sandbox.store, m.now())
	if _, err := sandbox.svc.Acquire(ctx, taker, udidProMax, 0); err != nil {
		t.Fatalf("the sandbox could not take a dead daemon's device: %v", err)
	}

	restarted := m.daemon(t, mainDir, 101, 3001)
	if err := restarted.svc.SyncOwnership(ctx); err != nil {
		t.Fatal(err)
	}
	if local, _ := restarted.store.ListSimLeases(ctx, m.now()); len(local) != 0 {
		t.Fatalf("the restarted daemon still believes it holds %+v", local)
	}
	// Its session's next gesture is refused, naming the real holder.
	_, err := restarted.svc.AcquireHold(ctx, first, udidProMax, 0, sim.GestureIntent{})
	var refused *sim.HoldRefusedError
	if !errors.As(err, &refused) || refused.Reason != sim.HoldRefusedLeasedByOther || refused.Lease.OtherDaemon == nil {
		t.Fatalf("gesture after losing the device = %v, want leased_by_other naming the sandbox", err)
	}
}

func TestOwnership_LapsedLeaseIsClaimable(t *testing.T) {
	m, main, sandbox := mainAndSandbox(t)
	ctx := context.Background()
	driver := newSession(t, sandbox.store, m.now())
	next := newSession(t, main.store, m.now())
	if _, err := sandbox.svc.Acquire(ctx, driver, udidProMax, 30*time.Second); err != nil {
		t.Fatal(err)
	}
	m.advance(31 * time.Second)
	if _, err := main.svc.Acquire(ctx, next, udidProMax, 0); err != nil {
		t.Fatalf("a lapsed lease on another daemon is still refused: %v", err)
	}
}

// racyOwnership hides the other daemon from the pre-check and then loses the
// claim, which is what happens when two daemons claim at the same instant.
type racyOwnership struct {
	sim.Ownership
	holder domain.SimLease
}

func (r racyOwnership) Foreign(context.Context, string, time.Time) (domain.SimLease, bool, error) {
	return domain.SimLease{}, false, nil
}

func (r racyOwnership) Claim(context.Context, domain.SimLease, time.Time) (domain.SimLease, bool, error) {
	return r.holder, false, nil
}

func TestOwnership_LosingTheRaceUndoesTheLocalGrant(t *testing.T) {
	now := time.Date(2026, 10, 3, 20, 7, 0, 0, time.UTC)
	store, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.UpsertProject(context.Background(), domain.ProjectRecord{ID: "mer", Path: "/tmp/mer", RegisteredAt: now}); err != nil {
		t.Fatal(err)
	}
	winner := domain.SimLease{
		UDID: udidProMax, SessionID: "other-1", AcquiredAt: now, ExpiresAt: now.Add(time.Minute),
		OtherDaemon: &domain.SimDaemon{DataDir: "/tmp/sandbox", PID: 200, Port: 3399},
	}
	changed := 0
	svc := sim.New(store, sim.WithClock(fixedClock(now)), sim.WithOwnership(racyOwnership{holder: winner}),
		sim.WithLeaseChanged(func() { changed++ }))
	session := newSession(t, store, now)

	_, err = svc.Acquire(context.Background(), session, udidProMax, 0)
	var held *sim.HeldError
	if !errors.As(err, &held) || held.Lease.SessionID != "other-1" {
		t.Fatalf("losing the machine-wide claim = %v, want the winner named", err)
	}
	if local, _ := store.ListSimLeases(context.Background(), now); len(local) != 0 {
		t.Fatalf("the local grant survived losing the race: %+v - two daemons would drive one device", local)
	}
	if changed == 0 {
		t.Fatal("the runners were not told the briefly granted lease is gone")
	}
}
