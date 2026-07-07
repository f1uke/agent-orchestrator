# Auto-close idle sessions Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Auto-close sessions that have been idle past a configurable TTL by destroying their tmux and marking them terminated while keeping the worktree, so tmux survives app reopen and closed sessions stay restorable.

**Architecture:** Add an env-var TTL (`AO_SESSION_IDLE_CLOSE`, default 24h) read by the daemon. The session manager gains `CloseIdleSessions`, which sweeps every session and closes the idle ones (destroy runtime + mark terminated + clear restore marker, **keep worktree**). The old immediate boot-time reap (`reconcileReap`) is replaced by this idle-gated sweep, called both from `Reconcile` at boot and from a ~5-minute daemon ticker. Restore is unchanged — a terminated session with its worktree on disk is already restorable via the existing "Restore session" button / `POST /api/v1/sessions/{id}/restore`.

**Tech Stack:** Go (backend daemon + session manager), standard library only (`time`, `context`, `log/slog`). No new dependencies, no migration, no HTTP/API/frontend changes.

## Global Constraints

- App state resolves under `~/.ao` only (see CLAUDE.md); this change adds no new state files.
- Backend module root is `backend/`; run all Go commands from there.
- TTL semantics: `AO_SESSION_IDLE_CLOSE` is a Go duration; `<= 0` disables the sweep entirely (no auto-close, no reap). Default `24h`.
- "Close" keeps the worktree: never call `workspace.Destroy`/`ForceDestroy` from the idle path.
- Idle is measured from `Activity.LastActivityAt`, falling back to `CreatedAt` when no signal has arrived yet.
- Best-effort throughout: a per-session failure is logged and never aborts the sweep or blocks boot.

---

### Task 1: Config — `AO_SESSION_IDLE_CLOSE`

**Files:**
- Modify: `backend/internal/config/config.go` (struct ~line 74-97, const block ~line 17-39, `Load` ~line 125-230, doc comment ~line 109-123)
- Test: `backend/internal/config/config_test.go`

**Interfaces:**
- Produces: `config.Config.SessionIdleClose time.Duration`; `config.DefaultSessionIdleClose = 24 * time.Hour`.

- [ ] **Step 1: Write the failing tests**

In `backend/internal/config/config_test.go`, add a new test and extend `TestLoadDefaults`.

Add this standalone test:

```go
func TestLoadSessionIdleClose(t *testing.T) {
	t.Run("override", func(t *testing.T) {
		t.Setenv("AO_SESSION_IDLE_CLOSE", "48h")
		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if cfg.SessionIdleClose != 48*time.Hour {
			t.Errorf("SessionIdleClose = %s, want 48h", cfg.SessionIdleClose)
		}
	})
	t.Run("zero disables", func(t *testing.T) {
		t.Setenv("AO_SESSION_IDLE_CLOSE", "0")
		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if cfg.SessionIdleClose != 0 {
			t.Errorf("SessionIdleClose = %s, want 0 (disabled)", cfg.SessionIdleClose)
		}
	})
	t.Run("malformed errors", func(t *testing.T) {
		t.Setenv("AO_SESSION_IDLE_CLOSE", "banana")
		if _, err := Load(); err == nil {
			t.Fatal("Load() = nil error for malformed AO_SESSION_IDLE_CLOSE, want error")
		}
	})
}
```

In `TestLoadDefaults`, add `"AO_SESSION_IDLE_CLOSE"` to the `for _, k := range []string{...}` clear list, and add this assertion after the `ShutdownTimeout` check:

```go
	if cfg.SessionIdleClose != DefaultSessionIdleClose {
		t.Errorf("SessionIdleClose = %s, want %s", cfg.SessionIdleClose, DefaultSessionIdleClose)
	}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd backend && go test ./internal/config/ -run 'TestLoadSessionIdleClose|TestLoadDefaults' -v`
Expected: FAIL to compile — `cfg.SessionIdleClose undefined` and `DefaultSessionIdleClose undefined`.

- [ ] **Step 3: Add the config field, default, and parsing**

In `config.go`, add to the const block (after `DefaultShutdownTimeout`, ~line 31):

```go
	// DefaultSessionIdleClose is the inactivity window after which an idle
	// session is auto-closed. Zero (via AO_SESSION_IDLE_CLOSE=0) disables it.
	DefaultSessionIdleClose = 24 * time.Hour
```

Add to the `Config` struct (after `ShutdownTimeout`, ~line 82):

```go
	// SessionIdleClose is the inactivity window after which an idle session is
	// auto-closed: its tmux is destroyed and the session is marked terminated,
	// but its worktree is kept so it stays restorable. 0 disables auto-close.
	SessionIdleClose time.Duration
```

Add to the defaults literal in `Load` (after `ShutdownTimeout: DefaultShutdownTimeout,`):

```go
		SessionIdleClose: DefaultSessionIdleClose,
```

Add the parse block (after the `AO_SHUTDOWN_TIMEOUT` block, ~line 164). Note this uses `time.ParseDuration` directly, NOT `parsePositiveDuration`, because `0` must be accepted to disable:

```go
	if raw := os.Getenv("AO_SESSION_IDLE_CLOSE"); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil {
			return Config{}, fmt.Errorf("invalid AO_SESSION_IDLE_CLOSE %q: %w", raw, err)
		}
		cfg.SessionIdleClose = d
	}
```

Add to the recognised-variables doc comment (in the `//	AO_...` list, ~line 113):

```go
//	AO_SESSION_IDLE_CLOSE  idle auto-close window (Go duration, 0 disables, default 24h)
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd backend && go test ./internal/config/ -run 'TestLoadSessionIdleClose|TestLoadDefaults|TestLoadOverrides|TestLoadInvalid' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/config/config.go backend/internal/config/config_test.go
git commit -m "feat(config): add AO_SESSION_IDLE_CLOSE idle auto-close window (default 24h)"
```

---

### Task 2: `Manager.CloseIdleSessions` + replace boot reap

**Files:**
- Modify: `backend/internal/session_manager/manager.go` (add struct field ~line 137, `Deps` field ~line 166, `New` ~line 170-200, delete `reconcileReap` ~line 814-834, rewrite `Reconcile` ~line 836-871, add `CloseIdleSessions`/`closeIdle`/`idleReference`)
- Test: `backend/internal/session_manager/manager_test.go` (delete the two `reconcileReap` tests ~line 2088-2127, add `CloseIdleSessions` tests)
- Test: `backend/internal/integration/lifecycle_sqlite_test.go` (add `newStackWithIdleClose` ~line 121, update `TestReconcile_TerminatesDeadLiveSessionAndReapsLeakedTmux` ~line 217)

**Interfaces:**
- Consumes: `config.Config.SessionIdleClose` (wired in Task 3 via `Deps.IdleCloseTTL`).
- Produces:
  - `sessionmanager.Deps.IdleCloseTTL time.Duration` (0 disables).
  - `(*Manager).CloseIdleSessions(ctx context.Context) error` — sweeps and closes idle sessions.
  - Removes `(*Manager).reconcileReap`.

- [ ] **Step 1: Write the failing unit tests**

In `backend/internal/session_manager/manager_test.go`, DELETE `TestReconcileReap_TerminatedButAliveTmuxDestroyed` and `TestReconcileReap_TerminatedAndDeadTmuxLeftAlone` (they call the soon-removed `reconcileReap`). Add these tests (the file already has a package-level `ctx` and the `fakeStore`/`fakeRuntime`/`fakeWorkspace`/`fakeLCM` helpers):

```go
func newIdleManager(ttl time.Duration, now time.Time) (*Manager, *fakeStore, *fakeRuntime, *fakeWorkspace, *fakeLCM) {
	st := newFakeStore()
	rt := &fakeRuntime{aliveByHandle: map[string]bool{}}
	ws := &fakeWorkspace{}
	lcm := &fakeLCM{store: st}
	m := New(Deps{
		Runtime: rt, Agents: fakeAgents{}, Workspace: ws, Store: st,
		Messenger: &fakeMessenger{}, Lifecycle: lcm,
		LookPath:     func(string) (string, error) { return "/bin/true", nil },
		IdleCloseTTL: ttl,
		Clock:        func() time.Time { return now },
	})
	return m, st, rt, ws, lcm
}

func TestCloseIdleSessions_IdleAlive_DestroysTmuxTerminatesKeepsWorktree(t *testing.T) {
	now := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)
	m, st, rt, ws, _ := newIdleManager(time.Hour, now)
	rt.aliveByHandle["h1"] = true
	st.sessions["s1"] = domain.SessionRecord{
		ID: "s1", ProjectID: "mer",
		Metadata:  domain.SessionMetadata{RuntimeHandleID: "h1", WorkspacePath: "/ws/s1"},
		Activity:  domain.Activity{State: domain.ActivityIdle, LastActivityAt: now.Add(-2 * time.Hour)},
		CreatedAt: now.Add(-3 * time.Hour),
	}
	if err := m.CloseIdleSessions(ctx); err != nil {
		t.Fatalf("CloseIdleSessions: %v", err)
	}
	if len(rt.destroyedIDs) != 1 || rt.destroyedIDs[0] != "h1" {
		t.Fatalf("destroyedIDs = %v, want [h1]", rt.destroyedIDs)
	}
	if !st.sessions["s1"].IsTerminated {
		t.Fatal("idle session must be marked terminated")
	}
	if ws.destroyed != 0 {
		t.Fatalf("worktree must be kept; workspace Destroy called %d times", ws.destroyed)
	}
}

func TestCloseIdleSessions_TerminatedLeftover_DestroysTmuxOnly(t *testing.T) {
	now := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)
	m, st, rt, _, lcm := newIdleManager(time.Hour, now)
	rt.aliveByHandle["h2"] = true
	st.sessions["s2"] = domain.SessionRecord{
		ID: "s2", ProjectID: "mer", IsTerminated: true,
		Metadata: domain.SessionMetadata{RuntimeHandleID: "h2"},
		Activity: domain.Activity{State: domain.ActivityExited, LastActivityAt: now.Add(-2 * time.Hour)},
	}
	if err := m.CloseIdleSessions(ctx); err != nil {
		t.Fatalf("CloseIdleSessions: %v", err)
	}
	if rt.destroyed != 1 {
		t.Fatalf("Destroy calls = %d, want 1", rt.destroyed)
	}
	if lcm.terminated["s2"] != 0 {
		t.Fatalf("already-terminated session must not be re-marked; MarkTerminated calls = %d", lcm.terminated["s2"])
	}
}

func TestCloseIdleSessions_NotIdle_LeftAlone(t *testing.T) {
	now := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)
	m, st, rt, _, _ := newIdleManager(time.Hour, now)
	rt.aliveByHandle["h3"] = true
	st.sessions["s3"] = domain.SessionRecord{
		ID: "s3", ProjectID: "mer",
		Metadata:  domain.SessionMetadata{RuntimeHandleID: "h3"},
		Activity:  domain.Activity{State: domain.ActivityIdle, LastActivityAt: now.Add(-30 * time.Minute)},
		CreatedAt: now.Add(-30 * time.Minute),
	}
	if err := m.CloseIdleSessions(ctx); err != nil {
		t.Fatalf("CloseIdleSessions: %v", err)
	}
	if rt.destroyed != 0 {
		t.Fatalf("recent session must be untouched; Destroy calls = %d, want 0", rt.destroyed)
	}
	if st.sessions["s3"].IsTerminated {
		t.Fatal("recent session must not be terminated")
	}
}

func TestCloseIdleSessions_Disabled_NoOp(t *testing.T) {
	now := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)
	m, st, rt, _, _ := newIdleManager(0, now) // ttl <= 0 disables
	rt.aliveByHandle["h4"] = true
	st.sessions["s4"] = domain.SessionRecord{
		ID: "s4", ProjectID: "mer",
		Metadata:  domain.SessionMetadata{RuntimeHandleID: "h4"},
		Activity:  domain.Activity{State: domain.ActivityIdle, LastActivityAt: now.Add(-1000 * time.Hour)},
		CreatedAt: now.Add(-1000 * time.Hour),
	}
	if err := m.CloseIdleSessions(ctx); err != nil {
		t.Fatalf("CloseIdleSessions: %v", err)
	}
	if rt.destroyed != 0 {
		t.Fatalf("disabled sweep must not destroy; Destroy calls = %d, want 0", rt.destroyed)
	}
}

func TestCloseIdleSessions_NoSignalUsesCreatedAt(t *testing.T) {
	now := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)
	m, st, rt, _, _ := newIdleManager(time.Hour, now)
	rt.aliveByHandle["h5"] = true
	st.sessions["s5"] = domain.SessionRecord{
		ID: "s5", ProjectID: "mer",
		Metadata:  domain.SessionMetadata{RuntimeHandleID: "h5"},
		Activity:  domain.Activity{}, // no signal yet: LastActivityAt zero
		CreatedAt: now.Add(-10 * time.Minute),
	}
	if err := m.CloseIdleSessions(ctx); err != nil {
		t.Fatalf("CloseIdleSessions: %v", err)
	}
	if rt.destroyed != 0 {
		t.Fatalf("freshly created session must not be closed; Destroy calls = %d, want 0", rt.destroyed)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd backend && go test ./internal/session_manager/ -run TestCloseIdleSessions -v`
Expected: FAIL to compile — `IdleCloseTTL` unknown field in `Deps` and `CloseIdleSessions` undefined.

- [ ] **Step 3: Add the TTL dependency and the sweep implementation**

In `manager.go`, add to the `Manager` struct (after `clock func() time.Time`, ~line 123):

```go
	// idleCloseTTL is the inactivity window after which CloseIdleSessions closes
	// a session. Zero disables the sweep.
	idleCloseTTL time.Duration
```

Add to `Deps` (after `Clock`, ~line 154):

```go
	// IdleCloseTTL is the inactivity window for CloseIdleSessions. 0 disables it.
	IdleCloseTTL time.Duration
```

In `New`, set it in the `&Manager{...}` literal (after `clock: d.Clock,`):

```go
		idleCloseTTL: d.IdleCloseTTL,
```

DELETE the entire `reconcileReap` method (the doc comment starting `// reconcileReap kills the leaked tmux...` through the closing brace, ~line 814-834).

Replace the `Reconcile` method (~line 836-871) with:

```go
// Reconcile is the boot-time consistency pass. It replaces the bare RestoreAll
// call so that however the previous daemon died (clean shutdown, SIGKILL, or
// crash), live reality matches the DB:
//
//  1. Live pass: for each non-terminated session, adopt it if its runtime
//     survived, else capture work and mark terminated (reconcileLive).
//  2. Idle sweep: close sessions idle past the configured TTL — destroy their
//     tmux and mark them terminated while KEEPING the worktree, so a normally
//     terminated session's tmux survives app reopen and only ages out on
//     inactivity (CloseIdleSessions). Replaces the old immediate reap.
//  3. Restore pass: relaunch shutdown-saved sessions (existing RestoreAll).
//
// Best-effort throughout: a per-session failure is logged and never aborts the
// pass or blocks boot.
func (m *Manager) Reconcile(ctx context.Context) error {
	recs, err := m.store.ListAllSessions(ctx)
	if err != nil {
		return fmt.Errorf("reconcile: list sessions: %w", err)
	}
	for _, rec := range recs {
		if rec.IsTerminated {
			continue
		}
		if err := m.reconcileLive(ctx, rec); err != nil {
			m.logger.Error("reconcile: live pass failed, skipping", "sessionID", rec.ID, "error", err)
		}
	}
	if err := m.CloseIdleSessions(ctx); err != nil {
		m.logger.Error("reconcile: idle sweep failed", "error", err)
	}
	return m.RestoreAll(ctx)
}

// CloseIdleSessions auto-closes every session idle longer than the configured
// TTL: it destroys the session's runtime (tmux) and marks it terminated while
// KEEPING its worktree on disk, so the session stays restorable via the existing
// Restore path. A non-positive TTL disables the sweep. Best-effort: a per-session
// failure is logged and never aborts the pass.
func (m *Manager) CloseIdleSessions(ctx context.Context) error {
	if m.idleCloseTTL <= 0 {
		return nil
	}
	recs, err := m.store.ListAllSessions(ctx)
	if err != nil {
		return fmt.Errorf("close idle: list sessions: %w", err)
	}
	now := m.clock()
	for _, rec := range recs {
		if now.Sub(idleReference(rec)) <= m.idleCloseTTL {
			continue
		}
		if err := m.closeIdle(ctx, rec); err != nil {
			m.logger.Error("close idle: failed, skipping", "sessionID", rec.ID, "error", err)
		}
	}
	return nil
}

// closeIdle destroys a session's runtime (if any survives) and marks it
// terminated, deliberately keeping the worktree so the session stays restorable.
func (m *Manager) closeIdle(ctx context.Context, rec domain.SessionRecord) error {
	handle := runtimeHandle(rec.Metadata)
	if handle.ID != "" {
		alive, err := m.runtime.IsAlive(ctx, handle)
		if err != nil {
			return fmt.Errorf("close idle %s: probe: %w", rec.ID, err)
		}
		if alive {
			if err := m.runtime.Destroy(ctx, handle); err != nil {
				return fmt.Errorf("close idle %s: destroy: %w", rec.ID, err)
			}
		}
	}
	if rec.IsTerminated {
		return nil
	}
	// Clear any shutdown-restore marker so boot never auto-relaunches it: the
	// user restores on demand. The worktree is deliberately kept on disk.
	if err := m.store.DeleteSessionWorktrees(ctx, rec.ID); err != nil {
		return fmt.Errorf("close idle %s: clear restore marker: %w", rec.ID, err)
	}
	if err := m.lcm.MarkTerminated(ctx, rec.ID); err != nil {
		return fmt.Errorf("close idle %s: mark terminated: %w", rec.ID, err)
	}
	return nil
}

// idleReference is the timestamp idle time is measured from: the last activity
// signal, or the session's creation time when no signal has arrived yet (so a
// freshly-spawned, not-yet-reporting session is not closed immediately).
func idleReference(rec domain.SessionRecord) time.Time {
	if !rec.Activity.LastActivityAt.IsZero() {
		return rec.Activity.LastActivityAt
	}
	return rec.CreatedAt
}
```

- [ ] **Step 4: Run the unit tests to verify they pass**

Run: `cd backend && go test ./internal/session_manager/ -run 'TestCloseIdleSessions|TestReconcile' -v`
Expected: PASS (including the existing `TestReconcile_AdoptAcrossDaemonRestart`, which uses the default `IdleCloseTTL` 0 → sweep is a no-op).

- [ ] **Step 5: Update the integration reap test to the idle-gated contract**

In `backend/internal/integration/lifecycle_sqlite_test.go`, replace the `newStack` function (~line 121-148) so it delegates to a TTL-aware variant. Keep the body identical except the `mgr :=` line:

```go
func newStack(t *testing.T) *stack { return newStackWithIdleClose(t, 0, nil) }

func newStackWithIdleClose(t *testing.T, idleTTL time.Duration, clock func() time.Time) *stack {
	t.Helper()
	ctx := context.Background()
	store, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.UpsertProject(ctx, domain.ProjectRecord{
		ID:           "mer",
		Path:         "/repo/mer",
		RegisteredAt: time.Now(),
		Config: domain.ProjectConfig{
			Worker:       domain.RoleOverride{Harness: domain.HarnessClaudeCode},
			Orchestrator: domain.RoleOverride{Harness: domain.HarnessClaudeCode},
		},
	}); err != nil {
		t.Fatal(err)
	}
	msg := &captureMessenger{}
	lcm := lifecycle.New(store, msg)
	prm := prsvc.New(prsvc.Deps{Writer: store, Lifecycle: lcm})
	rt := &stubRuntime{}
	ws := &stubWorkspace{}
	mgr := sessionmanager.New(sessionmanager.Deps{Runtime: rt, Agents: stubAgents{}, Workspace: ws, Store: store, Messenger: msg, Lifecycle: lcm, LookPath: func(string) (string, error) { return "/usr/bin/true", nil }, IdleCloseTTL: idleTTL, Clock: clock})
	sm := sessionsvc.New(mgr, store)
	return &stack{store: store, sm: sm, mgr: mgr, lcm: lcm, prm: prm, rt: rt, ws: ws, msg: msg}
}
```

Then update `TestReconcile_TerminatesDeadLiveSessionAndReapsLeakedTmux` (~line 217): the leaked terminated runtime is now reaped only when idle past the TTL, so build the stack with a 1h TTL and a fixed clock, and make session B idle for 2h. Change the top of the test:

```go
func TestReconcile_TerminatesDeadLiveSessionAndReapsLeakedTmux(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)
	st := newStackWithIdleClose(t, time.Hour, func() time.Time { return now })

	// Script liveness: handle "hdl-A" is dead; handle "hdl-B" is alive.
	st.rt.aliveByHandle = map[string]bool{
		"hdl-A": false,
		"hdl-B": true,
	}
```

Delete the old `now := time.Now().UTC()` line further down. For session B's seed, set its activity/creation older than the TTL so the idle sweep reaps its leaked runtime (keep everything else about recB the same):

```go
		Activity:  domain.Activity{State: domain.ActivityIdle, LastActivityAt: now.Add(-2 * time.Hour)},
		CreatedAt: now.Add(-2 * time.Hour),
```

Session A keeps `LastActivityAt: now` / `CreatedAt: now`; it is terminated by `reconcileLive` on liveness (dead runtime), independent of idle. All existing assertions stay: A terminated, `st.rt.created == 0`, and `st.rt.wasDestroyed("hdl-B")` true (now via the idle sweep).

- [ ] **Step 6: Run the full backend suite to verify green**

Run: `cd backend && go build ./... && go test ./internal/session_manager/ ./internal/integration/ ./internal/config/`
Expected: PASS. (`go build ./...` confirms no dangling `reconcileReap` references remain anywhere.)

- [ ] **Step 7: Commit**

```bash
git add backend/internal/session_manager/manager.go backend/internal/session_manager/manager_test.go backend/internal/integration/lifecycle_sqlite_test.go
git commit -m "feat(session): CloseIdleSessions idle sweep; replace immediate boot reap"
```

---

### Task 3: Daemon idle-sweep ticker + wiring

**Files:**
- Create: `backend/internal/daemon/idle_sweep.go`
- Create: `backend/internal/daemon/idle_sweep_test.go`
- Modify: `backend/internal/daemon/lifecycle_wiring.go` (`sessionLifecycle` interface ~line 73-76; pass `IdleCloseTTL` into `sessionmanager.New` ~line 104-114)
- Modify: `backend/internal/daemon/daemon.go` (start the ticker before `srv.Run` ~line 189, drain it on shutdown ~line 190-210)
- Modify: `backend/internal/daemon/wiring_test.go` (`fakeSessionLifecycle` ~line 439-454)

**Interfaces:**
- Consumes: `(*Manager).CloseIdleSessions` (Task 2); `config.Config.SessionIdleClose` (Task 1).
- Produces: `daemon.idleSweepInterval` const; `daemon.startIdleSweep(ctx, interval, sweep, log) <-chan struct{}`; `sessionLifecycle.CloseIdleSessions(ctx) error`.

- [ ] **Step 1: Write the failing ticker test**

Create `backend/internal/daemon/idle_sweep_test.go`:

```go
package daemon

import (
	"context"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"
)

func TestStartIdleSweep_DisabledClosesImmediately(t *testing.T) {
	var calls int32
	done := startIdleSweep(context.Background(), 0, func(context.Context) error {
		atomic.AddInt32(&calls, 1)
		return nil
	}, slog.Default())

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("done channel not closed for a disabled (interval<=0) sweep")
	}
	if got := atomic.LoadInt32(&calls); got != 0 {
		t.Fatalf("sweep called %d times when disabled, want 0", got)
	}
}

func TestStartIdleSweep_TicksThenStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	ticked := make(chan struct{}, 1)
	done := startIdleSweep(ctx, 5*time.Millisecond, func(context.Context) error {
		select {
		case ticked <- struct{}{}:
		default:
		}
		return nil
	}, slog.Default())

	select {
	case <-ticked:
	case <-time.After(2 * time.Second):
		t.Fatal("sweep was never called")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("done channel not closed after context cancel")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd backend && go test ./internal/daemon/ -run TestStartIdleSweep -v`
Expected: FAIL to compile — `startIdleSweep` undefined.

- [ ] **Step 3: Implement the ticker**

Create `backend/internal/daemon/idle_sweep.go`:

```go
package daemon

import (
	"context"
	"log/slog"
	"time"
)

// idleSweepIntervalDefault is how often the daemon scans for idle sessions to
// auto-close while it is running. Independent of the idle TTL: the TTL decides
// WHICH sessions close, this decides HOW PROMPTLY they are noticed.
const idleSweepIntervalDefault = 5 * time.Minute

// startIdleSweep launches a background goroutine that calls sweep on every tick
// until ctx is cancelled, returning a channel closed when the goroutine exits so
// daemon shutdown can drain it (mirroring the preview poller's lifecycle). A
// non-positive interval disables the sweep: the returned channel is already
// closed and sweep is never called.
func startIdleSweep(ctx context.Context, interval time.Duration, sweep func(context.Context) error, log *slog.Logger) <-chan struct{} {
	done := make(chan struct{})
	if interval <= 0 {
		close(done)
		return done
	}
	go func() {
		defer close(done)
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if err := sweep(ctx); err != nil {
					log.Warn("idle session sweep failed", "err", err)
				}
			}
		}
	}()
	return done
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `cd backend && go test ./internal/daemon/ -run TestStartIdleSweep -v`
Expected: PASS.

- [ ] **Step 5: Add `CloseIdleSessions` to the interface + fake + wire the TTL**

In `lifecycle_wiring.go`, extend the `sessionLifecycle` interface (~line 73):

```go
type sessionLifecycle interface {
	Reconcile(ctx context.Context) error
	RestoreAll(ctx context.Context) error
	CloseIdleSessions(ctx context.Context) error
}
```

In the same file, pass the TTL into the manager (`sessionmanager.New(sessionmanager.Deps{...})`, ~line 104) by adding one field to the Deps literal:

```go
		IdleCloseTTL: cfg.SessionIdleClose,
```

In `wiring_test.go`, add the method to `fakeSessionLifecycle` (after its `RestoreAll`, ~line 454) so it still satisfies the interface:

```go
func (f *fakeSessionLifecycle) CloseIdleSessions(_ context.Context) error { return nil }
```

- [ ] **Step 6: Start and drain the ticker in the daemon boot sequence**

In `daemon.go`, immediately before `runErr := srv.Run(ctx)` (~line 190), add:

```go
	// Auto-close idle sessions while the daemon runs. Disabled (interval 0) when
	// AO_SESSION_IDLE_CLOSE <= 0; boot-time closing already ran inside Reconcile.
	sweepInterval := time.Duration(0)
	if cfg.SessionIdleClose > 0 {
		sweepInterval = idleSweepIntervalDefault
	}
	idleSweepDone := startIdleSweep(ctx, sweepInterval, sessMgr.CloseIdleSessions, log)
```

In the shutdown section, after the existing `<-previewDone` line (~line 197 region, appears in both the happy-path shutdown and is guarded by the goroutine only starting here), add:

```go
	<-idleSweepDone
```

directly after the `<-previewDone` that follows `stop()` in the normal shutdown path (the block beginning with the comment "Shut the background goroutines down in order"). Do NOT add it to the earlier error-return cleanup paths (lines ~155-163): the sweep goroutine is started only after those, so they must not wait on it.

- [ ] **Step 7: Run the daemon tests + full build**

Run: `cd backend && go build ./... && go test ./internal/daemon/`
Expected: PASS (interface compile-check `var _ sessionLifecycle = (*sessionmanager.Manager)(nil)` in `wiring_test.go` still holds because `Manager.CloseIdleSessions` exists from Task 2).

- [ ] **Step 8: Commit**

```bash
git add backend/internal/daemon/idle_sweep.go backend/internal/daemon/idle_sweep_test.go backend/internal/daemon/lifecycle_wiring.go backend/internal/daemon/daemon.go backend/internal/daemon/wiring_test.go
git commit -m "feat(daemon): periodic idle-session sweep wired to AO_SESSION_IDLE_CLOSE"
```

---

## Final verification

- [ ] **Full backend test + vet**

Run: `cd backend && go vet ./... && go test ./...`
Expected: PASS across the module.

- [ ] **Manual smoke (optional, requires a real daemon)**

Set a short window and confirm an idle session's tmux is closed but restorable:

```bash
AO_SESSION_IDLE_CLOSE=1m ao start   # or launch the app with the env set
# spawn/idle a session, wait >1m, confirm its board card shows "terminated"
# and the "Restore session" button relaunches it with its worktree intact.
```
Expected: after ~1m idle the session is terminated (tmux gone) with its worktree preserved; Restore brings it back.
