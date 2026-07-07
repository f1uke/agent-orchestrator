# Auto-reclaim & Delete Finished Sessions — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Auto-reclaim (tear down tmux + worktree, keep git branch) finished worker sessions after a grace period, and let the user permanently delete finished sessions from the Done/Terminated bar without deleting their git branch.

**Architecture:** Approach A — no new lifecycle state. Reclaim reuses the existing `Manager.Kill` teardown (which already keeps the branch and preserves dirty worktrees). A new daemon poll loop (`reclaimer`) reads two settings from a JSON file under `~/.ao`, asks the session service which finished sessions still hold resources, applies an in-memory grace clock, and calls a service `Reclaim`. Delete is a new terminal-only `PurgeSession` that hard-deletes the session row (FK cascades clean dependents; `git branch -D` is never run), exposed as `DELETE /api/v1/sessions/{sessionId}`.

**Tech Stack:** Go (backend daemon, chi router, sqlite), code-generated OpenAPI + TypeScript client, React + TanStack Query + Radix (Electron renderer), Vitest.

## Global Constraints

- **App state under `~/.ao` only.** The settings file MUST resolve under `cfg.DataDir` (which is `~/.ao`, overridable via `AO_DATA_DIR`). Never write to `~/Library/Application Support` or any OS-default location.
- **Never delete git branches.** No code path may run `git branch -D`. `workspace.Destroy` / `ForceDestroy` only remove the worktree; the branch is the restore anchor.
- **Preserve uncommitted work.** A dirty worktree is never force-destroyed during auto-reclaim; delete only force-destroys a dirty worktree when the caller passes `force`.
- **Worker sessions only.** Auto-reclaim skips orchestrators (`rec.Kind == domain.KindOrchestrator`).
- **Renderer clones agent-orchestrator verbatim.** Build UI from existing primitives (`components/ui/*`, existing dialog patterns). Do not invent new visual styles. See `DESIGN.md`.
- **Code-first API.** Never hand-edit `openapi.yaml` or `frontend/src/api/schema.ts`. Edit `specgen/build.go`, then run `npm run api`. Commit `openapi.yaml` + `schema.ts` with the Go changes.
- **Do not hand-edit `backend/internal/storage/sqlite/gen/*`.** New raw deletes go through `tx.ExecContext` like `DeleteSession`, so no `npm run sqlc` is needed for this plan.
- Settings defaults: **Enabled = true, GraceMinutes = 15.** Reclaimer tick: **60s**.

---

## File Structure

**Backend — create**
- `backend/internal/reclaimsettings/settings.go` — `Settings` struct, `Default()`, file-backed `Store` (Get/Set) under `~/.ao/reclaim-settings.json`.
- `backend/internal/reclaimsettings/settings_test.go`
- `backend/internal/observe/reclaimer/reclaimer.go` — the grace-clock poll loop (`New`, `Tick`, `Start`).
- `backend/internal/observe/reclaimer/reclaimer_test.go`
- `backend/internal/daemon/reclaim_wiring.go` — construct settings store + reclaimer, start/drain.

**Backend — modify**
- `backend/internal/storage/sqlite/store/session_store.go` — add `PurgeSession`.
- `backend/internal/session_manager/manager.go` — add `ErrNotTerminal`, `Store.PurgeSession`, `Manager.PurgeSession`.
- `backend/internal/service/session/service.go` — add `Reclaim`, `ListReclaimable`, `Delete`; extend `commander`; extend `toAPIError`.
- `backend/internal/httpd/controllers/sessions.go` — add `delete` handler + route + `SessionService.Delete`.
- `backend/internal/httpd/controllers/settings.go` (create) — `SettingsController` get/set.
- `backend/internal/httpd/controllers/dto.go` — add `DeleteSessionResponse`, `DeleteSessionQuery`, `ReclaimSettingsResponse`, `SetReclaimSettingsRequest`.
- `backend/internal/httpd/api.go` — add `settings` controller + `APIDeps.Settings`.
- `backend/internal/httpd/apispec/specgen/build.go` — add DELETE-session op, settings ops, `schemaNames` entries.
- `backend/internal/daemon/daemon.go` — wire settings store, reclaimer, `Settings` dep.
- Regenerated: `backend/internal/httpd/apispec/openapi.yaml`, `frontend/src/api/schema.ts`.

**Frontend — modify**
- `frontend/src/renderer/lib/api-client.ts` — add DELETE-session + settings routes to `ROUTE_TEMPLATES`.
- `frontend/src/renderer/components/SessionsBoard.tsx` — per-chip delete + Clear-all.
- `frontend/src/renderer/components/AutoReclaimSection.tsx` (create) — settings card.
- `frontend/src/renderer/components/GlobalSettingsForm.tsx` — mount `AutoReclaimSection`.
- Tests: `SessionsBoard.test.tsx`, `GlobalSettingsForm.test.tsx`, `AutoReclaimSection.test.tsx` (create).

---

## Task 1: `store.PurgeSession` — hard-delete a session row

**Files:**
- Modify: `backend/internal/storage/sqlite/store/session_store.go`
- Test: `backend/internal/storage/sqlite/store/session_store_test.go` (add a test; file exists)

**Interfaces:**
- Produces: `func (s *Store) PurgeSession(ctx context.Context, id domain.SessionID) error`

Model on `DeleteSession` (`session_store.go:96-149`) but WITHOUT the seed-state guard. A single `DELETE FROM sessions WHERE id = ?` cascades pr/pr_checks/pr_comment/pr_review_threads/pr_reviews/session_worktrees/notifications/review/review_run; only `change_log` (no cascade) blocks, so delete its rows first. Both statements use raw `tx.ExecContext` (the codebase's documented sqlc-parser workaround).

- [ ] **Step 1: Write the failing test**

Add to `session_store_test.go` (reuse the file's existing store-open helper — find how sibling tests obtain a `*Store` + seed a session; mirror that seeding):

```go
func TestPurgeSession_RemovesTerminalRowAndCascades(t *testing.T) {
	s := newTestStore(t) // existing helper in this package's tests
	ctx := context.Background()

	// Seed a project + a terminated session with a worktree row (a dependent
	// that MUST cascade away).
	seedProject(t, s, "proj-1")
	rec := domain.SessionRecord{
		ID: "sess-1", ProjectID: "proj-1", Kind: domain.KindWorker,
		IsTerminated: true,
		Metadata:     domain.SessionMetadata{Branch: "feat/x", WorkspacePath: "/tmp/x", RuntimeHandleID: "h1"},
	}
	if _, err := s.CreateSession(ctx, rec); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertSessionWorktree(ctx, domain.SessionWorktreeRecord{SessionID: "sess-1", Branch: "feat/x"}); err != nil {
		t.Fatal(err)
	}

	if err := s.PurgeSession(ctx, "sess-1"); err != nil {
		t.Fatalf("PurgeSession: %v", err)
	}

	if _, ok, err := s.GetSession(ctx, "sess-1"); err != nil || ok {
		t.Fatalf("session row still present (ok=%v err=%v)", ok, err)
	}
	rows, err := s.ListSessionWorktrees(ctx, "sess-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("worktree rows not cascaded: %d", len(rows))
	}
}
```

> If `newTestStore`/`seedProject` helpers are named differently in this package, use whatever the neighboring tests already use to open a store and seed a project/session — do not invent new helpers.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/storage/sqlite/store/ -run TestPurgeSession -v`
Expected: FAIL — `s.PurgeSession undefined`.

- [ ] **Step 3: Implement `PurgeSession`**

Add to `session_store.go` (mirror the tx + raw ExecContext shape of `DeleteSession`):

```go
// PurgeSession hard-deletes a session row and everything that FK-cascades from
// it (PRs, worktree rows, notifications, review rows). change_log has no cascade
// (RESTRICT), so its rows are deleted first inside the same transaction. Unlike
// DeleteSession this has NO seed-state guard: callers (the session service) gate
// on terminal status. The git branch is untouched — only DB rows are removed.
func (s *Store) PurgeSession(ctx context.Context, id domain.SessionID) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("purge session %s: begin: %w", id, err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM change_log WHERE session_id = ?`, string(id)); err != nil {
		return fmt.Errorf("purge session %s: change_log: %w", id, err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, string(id)); err != nil {
		return fmt.Errorf("purge session %s: sessions: %w", id, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("purge session %s: commit: %w", id, err)
	}
	return nil
}
```

> Confirm the raw DB handle + mutex field names against `DeleteSession` (`session_store.go:96-149`) — reuse the exact `s.writeMu` / `s.db` (or the tx helper) that method uses. If `DeleteSession` uses a helper like `s.beginTx`, use the same.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/storage/sqlite/store/ -run TestPurgeSession -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/storage/sqlite/store/session_store.go backend/internal/storage/sqlite/store/session_store_test.go
git commit -m "feat(store): add PurgeSession hard-delete for terminal sessions"
```

---

## Task 2: `Manager.PurgeSession` + `ErrNotTerminal`

**Files:**
- Modify: `backend/internal/session_manager/manager.go`
- Test: `backend/internal/session_manager/manager_test.go`

**Interfaces:**
- Consumes: `Store.PurgeSession` (Task 1), existing `runtimeHandle`/`workspaceInfo` helpers, `ports.ErrWorkspaceDirty`, `workspace.Destroy`/`ForceDestroy`.
- Produces:
  - `var ErrNotTerminal = errors.New("session: not terminal")`
  - `Store` interface gains `PurgeSession(ctx context.Context, id domain.SessionID) error`
  - `func (m *Manager) PurgeSession(ctx context.Context, id domain.SessionID, force bool) error`

Teardown mirrors `Kill` (manager.go:488) but adds the `force` path for dirty worktrees and finishes with the row purge. The terminal-status guard lives in the service (Task 3), not here — the manager trusts its caller.

- [ ] **Step 1: Add `ErrNotTerminal` to the sentinel block**

In the `var ( ErrNotFound ... )` block (manager.go:25-46), append:

```go
	// ErrNotTerminal means a delete was requested for a session that is not
	// finished (neither merged nor terminated). The API maps it to 409.
	ErrNotTerminal = errors.New("session: not terminal")
```

- [ ] **Step 2: Add `PurgeSession` to the `Store` interface**

In the `Store` interface (manager.go:83-110), after `DeleteSessionWorktrees`:

```go
	// PurgeSession hard-deletes a session row and its cascading dependents,
	// regardless of state. Callers gate on terminal status; the branch is kept.
	PurgeSession(ctx context.Context, id domain.SessionID) error
```

- [ ] **Step 3: Write the failing test**

Add to `manager_test.go` (reuse the package's existing fake store/runtime/workspace + `newManager`-style helper — mirror the existing `TestKill*` tests):

```go
func TestPurgeSession_CleanWorktree_TearsDownAndPurges(t *testing.T) {
	h := newManagerHarness(t) // whatever the Kill tests use
	h.store.put(domain.SessionRecord{
		ID: "sess-1", ProjectID: "proj-1", IsTerminated: true,
		Metadata: domain.SessionMetadata{Branch: "feat/x", WorkspacePath: "/tmp/x", RuntimeHandleID: "h1"},
	})

	if err := h.mgr.PurgeSession(context.Background(), "sess-1", false); err != nil {
		t.Fatalf("PurgeSession: %v", err)
	}
	if !h.runtime.destroyed["h1"] {
		t.Fatal("runtime not destroyed")
	}
	if !h.workspace.destroyed["/tmp/x"] {
		t.Fatal("worktree not destroyed")
	}
	if !h.store.purged["sess-1"] {
		t.Fatal("row not purged")
	}
}

func TestPurgeSession_DirtyWorktree_NoForce_Refuses(t *testing.T) {
	h := newManagerHarness(t)
	h.workspace.destroyErr = ports.ErrWorkspaceDirty
	h.store.put(domain.SessionRecord{
		ID: "sess-1", ProjectID: "proj-1", IsTerminated: true,
		Metadata: domain.SessionMetadata{Branch: "feat/x", WorkspacePath: "/tmp/x"},
	})

	err := h.mgr.PurgeSession(context.Background(), "sess-1", false)
	if !errors.Is(err, ports.ErrWorkspaceDirty) {
		t.Fatalf("want ErrWorkspaceDirty, got %v", err)
	}
	if h.store.purged["sess-1"] {
		t.Fatal("row purged despite dirty refusal")
	}
}

func TestPurgeSession_DirtyWorktree_Force_ForceDestroysAndPurges(t *testing.T) {
	h := newManagerHarness(t)
	h.workspace.destroyErr = ports.ErrWorkspaceDirty
	h.store.put(domain.SessionRecord{
		ID: "sess-1", ProjectID: "proj-1", IsTerminated: true,
		Metadata: domain.SessionMetadata{Branch: "feat/x", WorkspacePath: "/tmp/x"},
	})

	if err := h.mgr.PurgeSession(context.Background(), "sess-1", true); err != nil {
		t.Fatalf("PurgeSession(force): %v", err)
	}
	if !h.workspace.forceDestroyed["/tmp/x"] {
		t.Fatal("worktree not force-destroyed")
	}
	if !h.store.purged["sess-1"] {
		t.Fatal("row not purged")
	}
}
```

> Match the harness/fakes to what `manager_test.go` already defines for the `Kill` tests. If the fake workspace has no `forceDestroyed`/`destroyErr` fields yet, add them minimally alongside its existing `destroyed` map. If the fake store has no `purged` map, add it and have its `PurgeSession` record into it.

- [ ] **Step 4: Run test to verify it fails**

Run: `cd backend && go test ./internal/session_manager/ -run TestPurgeSession -v`
Expected: FAIL — `m.PurgeSession undefined` (and fake-store missing `PurgeSession`).

- [ ] **Step 5: Implement `Manager.PurgeSession`**

Add after `Kill` (manager.go:526):

```go
// PurgeSession tears the session down like Kill, then hard-deletes its row and
// cascading dependents. A dirty worktree is refused (ErrWorkspaceDirty) unless
// force is set, in which case it is force-destroyed. The git branch is never
// removed — only the runtime, the worktree directory, and DB rows are. Callers
// (the session service) must gate this on terminal status.
func (m *Manager) PurgeSession(ctx context.Context, id domain.SessionID, force bool) error {
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil {
		return fmt.Errorf("purge %s: %w", id, err)
	}
	if !ok {
		return nil // already gone: benign race
	}
	handle := runtimeHandle(rec.Metadata)
	ws := workspaceInfo(rec)

	if !rec.IsTerminated {
		if err := m.lcm.MarkTerminated(ctx, id); err != nil {
			return fmt.Errorf("purge %s: %w", id, err)
		}
	}
	if handle.ID != "" {
		if err := m.runtime.Destroy(ctx, handle); err != nil {
			return fmt.Errorf("purge %s: runtime: %w", id, err)
		}
	}
	if ws.Path != "" {
		if err := m.workspace.Destroy(ctx, ws); err != nil {
			if errors.Is(err, ports.ErrWorkspaceDirty) {
				if !force {
					return fmt.Errorf("purge %s: %w", id, ports.ErrWorkspaceDirty)
				}
				if ferr := m.workspace.ForceDestroy(ctx, ws); ferr != nil {
					return fmt.Errorf("purge %s: force destroy: %w", id, ferr)
				}
			} else {
				return fmt.Errorf("purge %s: workspace: %w", id, err)
			}
		}
	}
	return m.store.PurgeSession(ctx, id)
}
```

> Confirm `m.workspace` exposes `ForceDestroy` on the manager's `ports.Workspace` interface (it exists on the gitworktree adapter at `workspace.go:204`; if the manager's `Workspace` port omits it, add `ForceDestroy(ctx, ports.WorkspaceInfo) error` to that port interface — check `ports/outbound.go`).

- [ ] **Step 6: Run tests to verify they pass**

Run: `cd backend && go test ./internal/session_manager/ -run TestPurgeSession -v`
Expected: PASS (all three).

- [ ] **Step 7: Commit**

```bash
git add backend/internal/session_manager/manager.go backend/internal/session_manager/manager_test.go
git commit -m "feat(session): Manager.PurgeSession teardown+hard-delete, ErrNotTerminal"
```

---

## Task 3: Service `Reclaim`, `ListReclaimable`, `Delete` + error mapping

**Files:**
- Modify: `backend/internal/service/session/service.go`
- Test: `backend/internal/service/session/service_test.go`

**Interfaces:**
- Consumes: `commander.Kill` (existing), `commander.PurgeSession` (Task 2), `s.store.ListAllSessions`, `s.store.GetSession`, `s.toSession` (existing, returns `domain.Session` with `.Status`), `domain.StatusMerged`/`StatusTerminated`, `domain.KindOrchestrator`.
- Produces:
  - `func (s *Service) Reclaim(ctx context.Context, id domain.SessionID) error`
  - `func (s *Service) ListReclaimable(ctx context.Context) ([]domain.SessionID, error)`
  - `func (s *Service) Delete(ctx context.Context, id domain.SessionID, force bool) error`
  - `commander` interface gains `PurgeSession(ctx context.Context, id domain.SessionID, force bool) error`
  - `toAPIError` maps `ErrNotTerminal` (409) and `ports.ErrWorkspaceDirty` (409).

- [ ] **Step 1: Extend the `commander` interface**

In `commander` (service.go:45-53) add:

```go
	PurgeSession(ctx context.Context, id domain.SessionID, force bool) error
```

- [ ] **Step 2: Add `toAPIError` cases**

In `toAPIError` (service.go:545-581), before `default:`:

```go
	case errors.Is(err, sessionmanager.ErrNotTerminal):
		return apierr.Conflict("SESSION_NOT_TERMINAL", "Session is not finished (merged or terminated)", nil)
	case errors.Is(err, ports.ErrWorkspaceDirty):
		return apierr.Conflict("SESSION_WORKSPACE_DIRTY", "Session worktree has uncommitted changes; delete with force to discard them", nil)
```

- [ ] **Step 3: Write the failing tests**

Add to `service_test.go` (reuse the existing fake commander + fake store harness — mirror the `Kill`/`Restore` service tests):

```go
func TestListReclaimable_SelectsFinishedWorkersWithResources(t *testing.T) {
	svc, store := newServiceHarness(t) // whatever the package tests use
	// merged worker, still has worktree -> included
	store.put(recWithPRMerged("sess-merged", "/tmp/a"))
	// terminated worker, still has worktree -> included
	store.put(recTerminated("sess-term", "/tmp/b"))
	// terminated worker, already torn down (no handle, no worktree) -> excluded
	store.put(recTerminatedNoResources("sess-done"))
	// working worker -> excluded
	store.put(recWorking("sess-live", "/tmp/c"))
	// orchestrator, terminated -> excluded
	store.put(recOrchestratorTerminated("proj-orchestrator", "/tmp/d"))

	got, err := svc.ListReclaimable(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := map[domain.SessionID]bool{"sess-merged": true, "sess-term": true}
	if len(got) != len(want) {
		t.Fatalf("got %v, want keys %v", got, want)
	}
	for _, id := range got {
		if !want[id] {
			t.Fatalf("unexpected candidate %s", id)
		}
	}
}

func TestReclaim_DelegatesToKill(t *testing.T) {
	svc, _ := newServiceHarness(t)
	if err := svc.Reclaim(context.Background(), "sess-1"); err != nil {
		t.Fatalf("Reclaim: %v", err)
	}
	// assert the fake commander recorded a Kill("sess-1")
}

func TestDelete_NonTerminal_ReturnsConflict(t *testing.T) {
	svc, store := newServiceHarness(t)
	store.put(recWorking("sess-live", "/tmp/c"))
	err := svc.Delete(context.Background(), "sess-live", false)
	// toAPIError wraps ErrNotTerminal into apierr.Conflict -> assert 409 code
	if err == nil {
		t.Fatal("want conflict, got nil")
	}
}

func TestDelete_Terminal_DelegatesToPurge(t *testing.T) {
	svc, store := newServiceHarness(t)
	store.put(recTerminated("sess-term", "/tmp/b"))
	if err := svc.Delete(context.Background(), "sess-term", true); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	// assert fake commander recorded PurgeSession("sess-term", true)
}
```

> Build the `rec*` fixtures with the package's existing record builders. `recWithPRMerged` needs the fake store's `ListPRFactsForSession` to return one PR with `Merged: true` (so `deriveStatus` returns `StatusMerged`). If the harness's fake store returns no PRs, extend it with a per-session PR map. The orchestrator fixture sets `Kind: domain.KindOrchestrator`.

- [ ] **Step 4: Run tests to verify they fail**

Run: `cd backend && go test ./internal/service/session/ -run 'TestListReclaimable|TestReclaim|TestDelete' -v`
Expected: FAIL — methods undefined; fake commander missing `PurgeSession`.

- [ ] **Step 5: Implement the three service methods**

Add to `service.go`:

```go
// Reclaim tears a finished session down (tmux + worktree) while keeping its
// branch, so it stays restorable. It reuses Kill's teardown; the auto-reclaim
// loop is the caller that distinguishes this from a user-initiated kill.
func (s *Service) Reclaim(ctx context.Context, id domain.SessionID) error {
	_, err := s.manager.Kill(ctx, id)
	return toAPIError(err)
}

// ListReclaimable returns worker sessions whose display status is merged or
// terminated AND that still hold a runtime handle or worktree — i.e. sessions
// with resources left to reclaim. Already-torn-down sessions are excluded.
func (s *Service) ListReclaimable(ctx context.Context) ([]domain.SessionID, error) {
	recs, err := s.store.ListAllSessions(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]domain.SessionID, 0, len(recs))
	for _, rec := range recs {
		if rec.Kind == domain.KindOrchestrator {
			continue
		}
		if rec.Metadata.RuntimeHandleID == "" && rec.Metadata.WorkspacePath == "" {
			continue
		}
		sess, err := s.toSession(ctx, rec)
		if err != nil {
			continue // a single unreadable row must not sink the pass
		}
		if sess.Status == domain.StatusMerged || sess.Status == domain.StatusTerminated {
			out = append(out, rec.ID)
		}
	}
	return out, nil
}

// Delete permanently removes a finished (merged or terminated) session from AO,
// keeping its git branch. A dirty worktree is refused unless force is set.
func (s *Service) Delete(ctx context.Context, id domain.SessionID, force bool) error {
	rec, ok, err := s.store.GetSession(ctx, id)
	if err != nil {
		return toAPIError(err)
	}
	if !ok {
		return toAPIError(sessionmanager.ErrNotFound)
	}
	sess, err := s.toSession(ctx, rec)
	if err != nil {
		return err
	}
	if sess.Status != domain.StatusMerged && sess.Status != domain.StatusTerminated {
		return toAPIError(sessionmanager.ErrNotTerminal)
	}
	return toAPIError(s.manager.PurgeSession(ctx, id, force))
}
```

Also add `PurgeSession` to the package's fake commander in `service_test.go` (record calls) so the test harness satisfies the widened interface.

- [ ] **Step 6: Run tests to verify they pass**

Run: `cd backend && go test ./internal/service/session/ -v`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add backend/internal/service/session/service.go backend/internal/service/session/service_test.go
git commit -m "feat(session-svc): Reclaim, ListReclaimable, Delete + error mapping"
```

---

## Task 4: `reclaimsettings` package — file-backed settings under `~/.ao`

**Files:**
- Create: `backend/internal/reclaimsettings/settings.go`
- Test: `backend/internal/reclaimsettings/settings_test.go`

**Interfaces:**
- Produces:
  - `type Settings struct { Enabled bool json:"enabled"; GraceMinutes int json:"graceMinutes" }`
  - `func Default() Settings` → `{Enabled: true, GraceMinutes: 15}`
  - `func NewStore(dir string) (*Store, error)` — loads `dir/reclaim-settings.json`, defaults if absent/corrupt
  - `func (s *Store) Get() Settings`
  - `func (s *Store) Set(next Settings) error` — validates `GraceMinutes >= 0`, writes file atomically, updates memory

- [ ] **Step 1: Write the failing test**

```go
package reclaimsettings

import (
	"path/filepath"
	"testing"
)

func TestNewStore_AbsentFile_ReturnsDefaults(t *testing.T) {
	st, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got := st.Get(); !got.Enabled || got.GraceMinutes != 15 {
		t.Fatalf("defaults = %+v, want {true 15}", got)
	}
}

func TestSet_PersistsAndReloads(t *testing.T) {
	dir := t.TempDir()
	st, _ := NewStore(dir)
	if err := st.Set(Settings{Enabled: false, GraceMinutes: 30}); err != nil {
		t.Fatal(err)
	}
	if got := st.Get(); got.Enabled || got.GraceMinutes != 30 {
		t.Fatalf("in-memory = %+v", got)
	}
	// A fresh store over the same dir reloads the persisted value.
	st2, _ := NewStore(dir)
	if got := st2.Get(); got.Enabled || got.GraceMinutes != 30 {
		t.Fatalf("reloaded = %+v, want {false 30}", got)
	}
	_ = filepath.Join(dir, "reclaim-settings.json")
}

func TestSet_NegativeGrace_Rejected(t *testing.T) {
	st, _ := NewStore(t.TempDir())
	if err := st.Set(Settings{Enabled: true, GraceMinutes: -1}); err == nil {
		t.Fatal("want error for negative grace")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/reclaimsettings/ -v`
Expected: FAIL — package/symbols undefined.

- [ ] **Step 3: Implement the package**

```go
// Package reclaimsettings holds the user-editable auto-reclaim settings,
// persisted as a small JSON file under the data dir (~/.ao). The daemon's
// reclaim loop reads Get() each tick; the REST layer edits via Set().
package reclaimsettings

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

const fileName = "reclaim-settings.json"

// Settings are the two knobs behind auto-reclaim.
type Settings struct {
	Enabled      bool `json:"enabled"`
	GraceMinutes int  `json:"graceMinutes"`
}

// Default is auto-reclaim ON with a 15-minute grace.
func Default() Settings { return Settings{Enabled: true, GraceMinutes: 15} }

// Store is a mutex-guarded, file-backed Settings holder.
type Store struct {
	path string
	mu   sync.RWMutex
	cur  Settings
}

// NewStore loads dir/reclaim-settings.json. A missing or corrupt file degrades
// to Default() rather than erroring, so the daemon always boots.
func NewStore(dir string) (*Store, error) {
	if dir == "" {
		return nil, errors.New("reclaimsettings: data dir is required")
	}
	s := &Store{path: filepath.Join(dir, fileName), cur: Default()}
	if b, err := os.ReadFile(s.path); err == nil {
		var loaded Settings
		if json.Unmarshal(b, &loaded) == nil && loaded.GraceMinutes >= 0 {
			s.cur = loaded
		}
	}
	return s, nil
}

// Get returns the current settings.
func (s *Store) Get() Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cur
}

// Set validates, persists (atomic write via temp+rename), and updates memory.
func (s *Store) Set(next Settings) error {
	if next.GraceMinutes < 0 {
		return fmt.Errorf("reclaimsettings: graceMinutes must be >= 0, got %d", next.GraceMinutes)
	}
	b, err := json.Marshal(next)
	if err != nil {
		return fmt.Errorf("reclaimsettings: marshal: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("reclaimsettings: write: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("reclaimsettings: rename: %w", err)
	}
	s.cur = next
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/reclaimsettings/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/reclaimsettings/
git commit -m "feat(reclaimsettings): file-backed auto-reclaim settings under ~/.ao"
```

---

## Task 5: `reclaimer` poll loop with in-memory grace clock

**Files:**
- Create: `backend/internal/observe/reclaimer/reclaimer.go`
- Test: `backend/internal/observe/reclaimer/reclaimer_test.go`

**Interfaces:**
- Consumes: `reclaimService` (satisfied by `*session.Service`: `ListReclaimable`, `Reclaim`), `settingsReader` (satisfied by `*reclaimsettings.Store`: `Get() reclaimsettings.Settings`), `observe.StartPollLoop` (for `Start`).
- Produces:
  - `func New(svc reclaimService, settings settingsReader, cfg Config) *Reclaimer`
  - `func (r *Reclaimer) Tick(ctx context.Context) error`
  - `func (r *Reclaimer) Start(ctx context.Context) <-chan struct{}`
  - `const DefaultTickInterval = time.Minute`

- [ ] **Step 1: Write the failing test**

```go
package reclaimer

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/reclaimsettings"
)

type fakeSvc struct {
	candidates []domain.SessionID
	reclaimed  []domain.SessionID
}

func (f *fakeSvc) ListReclaimable(context.Context) ([]domain.SessionID, error) { return f.candidates, nil }
func (f *fakeSvc) Reclaim(_ context.Context, id domain.SessionID) error {
	f.reclaimed = append(f.reclaimed, id)
	return nil
}

type fakeSettings struct{ s reclaimsettings.Settings }

func (f fakeSettings) Get() reclaimsettings.Settings { return f.s }

func newAt(base time.Time, clk *time.Time) func() time.Time {
	return func() time.Time { return *clk }
}

func TestTick_ReclaimsOnlyAfterGrace(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	svc := &fakeSvc{candidates: []domain.SessionID{"sess-1"}}
	r := New(svc, fakeSettings{reclaimsettings.Settings{Enabled: true, GraceMinutes: 15}},
		Config{Clock: func() time.Time { return now }})

	// First tick: stamps first-seen, does NOT reclaim.
	_ = r.Tick(context.Background())
	if len(svc.reclaimed) != 0 {
		t.Fatalf("reclaimed too early: %v", svc.reclaimed)
	}

	// Advance past grace, tick again: reclaims.
	now = now.Add(16 * time.Minute)
	_ = r.Tick(context.Background())
	if len(svc.reclaimed) != 1 || svc.reclaimed[0] != "sess-1" {
		t.Fatalf("want reclaim sess-1, got %v", svc.reclaimed)
	}
}

func TestTick_DisabledSetting_NoReclaim(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	svc := &fakeSvc{candidates: []domain.SessionID{"sess-1"}}
	r := New(svc, fakeSettings{reclaimsettings.Settings{Enabled: false, GraceMinutes: 0}},
		Config{Clock: func() time.Time { return now }})
	_ = r.Tick(context.Background())
	now = now.Add(time.Hour)
	_ = r.Tick(context.Background())
	if len(svc.reclaimed) != 0 {
		t.Fatalf("reclaimed while disabled: %v", svc.reclaimed)
	}
}

func TestTick_CandidateDisappears_ClearsClock(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	svc := &fakeSvc{candidates: []domain.SessionID{"sess-1"}}
	r := New(svc, fakeSettings{reclaimsettings.Settings{Enabled: true, GraceMinutes: 15}},
		Config{Clock: func() time.Time { return now }})
	_ = r.Tick(context.Background()) // stamps sess-1

	// sess-1 no longer a candidate; then reappears later — grace restarts.
	svc.candidates = nil
	now = now.Add(20 * time.Minute)
	_ = r.Tick(context.Background())

	svc.candidates = []domain.SessionID{"sess-1"}
	now = now.Add(1 * time.Minute)
	_ = r.Tick(context.Background()) // re-stamp, not reclaim
	if len(svc.reclaimed) != 0 {
		t.Fatalf("grace should have restarted, got %v", svc.reclaimed)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/observe/reclaimer/ -v`
Expected: FAIL — undefined symbols.

- [ ] **Step 3: Implement the reclaimer**

```go
// Package reclaimer is the OBSERVE-layer poll loop that auto-reclaims finished
// worker sessions (tear down tmux + worktree, keep branch) once they have sat
// in a merged/terminated state past the configured grace period.
package reclaimer

import (
	"context"
	"log/slog"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/observe"
	"github.com/aoagents/agent-orchestrator/backend/internal/reclaimsettings"
)

// DefaultTickInterval is the poll cadence. Grace is in minutes, so a slow tick
// is fine.
const DefaultTickInterval = time.Minute

type reclaimService interface {
	ListReclaimable(ctx context.Context) ([]domain.SessionID, error)
	Reclaim(ctx context.Context, id domain.SessionID) error
}

type settingsReader interface {
	Get() reclaimsettings.Settings
}

// Config holds optional knobs; zero values fall back to safe defaults.
type Config struct {
	Tick   time.Duration
	Clock  func() time.Time
	Logger *slog.Logger
}

// Reclaimer holds the grace clock: first-seen timestamps per candidate session.
type Reclaimer struct {
	svc       reclaimService
	settings  settingsReader
	firstSeen map[domain.SessionID]time.Time
	tick      time.Duration
	clock     func() time.Time
	logger    *slog.Logger
}

// New constructs a Reclaimer.
func New(svc reclaimService, settings settingsReader, cfg Config) *Reclaimer {
	r := &Reclaimer{
		svc:       svc,
		settings:  settings,
		firstSeen: map[domain.SessionID]time.Time{},
		tick:      cfg.Tick,
		clock:     cfg.Clock,
		logger:    cfg.Logger,
	}
	if r.tick <= 0 {
		r.tick = DefaultTickInterval
	}
	if r.clock == nil {
		r.clock = time.Now
	}
	if r.logger == nil {
		r.logger = slog.Default()
	}
	return r
}

// Start runs the loop until ctx is cancelled; the returned channel closes when
// the loop exits. Mirrors the reaper's shutdown contract.
func (r *Reclaimer) Start(ctx context.Context) <-chan struct{} {
	return observe.StartPollLoop(ctx, r.tick, r.Tick, r.logger, "reclaimer")
}

// Tick runs one grace-clock pass. Disabled settings make it a no-op.
func (r *Reclaimer) Tick(ctx context.Context) error {
	set := r.settings.Get()
	if !set.Enabled {
		return nil
	}
	now := r.clock()
	grace := time.Duration(set.GraceMinutes) * time.Minute

	candidates, err := r.svc.ListReclaimable(ctx)
	if err != nil {
		return err
	}
	current := make(map[domain.SessionID]bool, len(candidates))
	for _, id := range candidates {
		current[id] = true
	}
	// Drop clock entries for sessions no longer eligible so grace restarts if
	// they return.
	for id := range r.firstSeen {
		if !current[id] {
			delete(r.firstSeen, id)
		}
	}
	for _, id := range candidates {
		seen, ok := r.firstSeen[id]
		if !ok {
			r.firstSeen[id] = now
			continue
		}
		if now.Sub(seen) >= grace {
			if err := r.svc.Reclaim(ctx, id); err != nil {
				r.logger.Error("reclaimer: reclaim failed", "session", id, "err", err)
				continue
			}
			r.logger.Info("reclaimer: reclaimed finished session", "session", id)
			delete(r.firstSeen, id)
		}
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/observe/reclaimer/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/observe/reclaimer/
git commit -m "feat(reclaimer): grace-clock poll loop for auto-reclaim"
```

---

## Task 6: OpenAPI operations — DELETE session + settings GET/PUT

**Files:**
- Modify: `backend/internal/httpd/controllers/dto.go` (add response/request/param types)
- Modify: `backend/internal/httpd/apispec/specgen/build.go` (operations + `schemaNames`)
- Regenerated: `backend/internal/httpd/apispec/openapi.yaml`, `frontend/src/api/schema.ts`

**Interfaces:**
- Produces (DTOs): `DeleteSessionResponse{OK bool; SessionID domain.SessionID}`, `DeleteSessionQuery{Force bool}` (query param), `ReclaimSettingsResponse{Enabled bool; GraceMinutes int}`, `SetReclaimSettingsRequest{Enabled bool; GraceMinutes int}`.
- Produces (spec op ids): `deleteSession`, `getReclaimSettings`, `setReclaimSettings`.

- [ ] **Step 1: Add the DTOs**

In `dto.go`, mirroring existing response structs (e.g. `KillSessionResponse`) and the query-param struct convention used by `ListSessionsQuery`:

```go
// DeleteSessionResponse is returned by DELETE /api/v1/sessions/{sessionId}.
type DeleteSessionResponse struct {
	OK        bool             `json:"ok"`
	SessionID domain.SessionID `json:"sessionId"`
}

// DeleteSessionQuery carries the force flag for DELETE /api/v1/sessions/{id}.
type DeleteSessionQuery struct {
	// Force discards uncommitted worktree changes instead of refusing.
	Force bool `query:"force"`
}

// ReclaimSettingsResponse mirrors reclaimsettings.Settings on the wire.
type ReclaimSettingsResponse struct {
	Enabled      bool `json:"enabled"`
	GraceMinutes int  `json:"graceMinutes"`
}

// SetReclaimSettingsRequest is the PUT body for the reclaim settings.
type SetReclaimSettingsRequest struct {
	Enabled      bool `json:"enabled"`
	GraceMinutes int  `json:"graceMinutes"`
}
```

> Match `DeleteSessionQuery`'s tag style to whatever `ListSessionsQuery` uses for query params (open `dto.go` and copy its tag convention exactly — `query:"..."` vs a custom tag).

- [ ] **Step 2: Register the operations in `build.go`**

In `sessionOperations()` (build.go:530), add inside the returned slice (after `getSession`, alongside the other session ops):

```go
		{
			method: http.MethodDelete, path: "/api/v1/sessions/{sessionId}", id: "deleteSession", tag: "sessions",
			summary:    "Permanently delete a finished session (keeps the git branch)",
			pathParams: []any{controllers.SessionIDParam{}, controllers.DeleteSessionQuery{}},
			resps: []respUnit{
				{http.StatusOK, controllers.DeleteSessionResponse{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusConflict, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
			},
		},
```

Add a new `settingsOperations()` function and include it in the `operations()` aggregator (build.go:286):

```go
func settingsOperations() []operation {
	return []operation{
		{
			method: http.MethodGet, path: "/api/v1/settings/reclaim", id: "getReclaimSettings", tag: "settings",
			summary: "Fetch the auto-reclaim settings",
			resps: []respUnit{
				{http.StatusOK, controllers.ReclaimSettingsResponse{}},
				{http.StatusInternalServerError, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPut, path: "/api/v1/settings/reclaim", id: "setReclaimSettings", tag: "settings",
			summary: "Replace the auto-reclaim settings",
			reqBody: controllers.SetReclaimSettingsRequest{},
			resps: []respUnit{
				{http.StatusOK, controllers.ReclaimSettingsResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
			},
		},
	}
}
```

In `operations()`, append `settingsOperations()...` to the aggregated slice next to `sessionOperations()...`.

- [ ] **Step 3: Add `schemaNames` entries**

Find the `schemaNames` registry in `build.go` (AGENTS.md: "add a `schemaNames` entry for any new named type") and add entries for `controllers.DeleteSessionResponse`, `controllers.ReclaimSettingsResponse`, `controllers.SetReclaimSettingsRequest`, mirroring existing entries.

- [ ] **Step 4: Regenerate the spec + TS types**

Run: `npm run api`
Expected: updates `backend/internal/httpd/apispec/openapi.yaml` and `frontend/src/api/schema.ts` with the three new operations. No manual edits.

- [ ] **Step 5: Verify spec drift + route parity tests pass**

Run: `cd backend && go test ./internal/httpd/...`
Expected: PASS. (The `build_test.go` golden test fails if the embedded spec is stale — it should now be fresh. The parity test will FAIL until the routes exist; that is expected and is fixed in Tasks 7–8. If parity fails only on the missing `deleteSession`/settings routes, proceed; re-run after Task 8.)

- [ ] **Step 6: Commit**

```bash
git add backend/internal/httpd/controllers/dto.go backend/internal/httpd/apispec/specgen/build.go backend/internal/httpd/apispec/openapi.yaml frontend/src/api/schema.ts
git commit -m "feat(api): OpenAPI ops for deleteSession + reclaim settings"
```

---

## Task 7: `SessionsController.delete` handler + route

**Files:**
- Modify: `backend/internal/httpd/controllers/sessions.go`
- Test: `backend/internal/httpd/controllers/sessions_test.go` (mirror the `kill` handler test)

**Interfaces:**
- Consumes: `Service.Delete(ctx, id, force)` (Task 3), `DeleteSessionResponse` (Task 6).
- Produces: `SessionService` interface gains `Delete(ctx context.Context, id domain.SessionID, force bool) error`; route `DELETE /sessions/{sessionId}`.

- [ ] **Step 1: Extend the `SessionService` interface**

In `sessions.go` (interface at lines 34-48) add:

```go
	Delete(ctx context.Context, id domain.SessionID, force bool) error
```

- [ ] **Step 2: Write the failing handler test**

Mirror the existing `kill` handler test (find it in `sessions_test.go`); assert a `DELETE` with `?force=true` calls `Svc.Delete(id, true)` and returns 200 with `{ok:true}`. Also assert a service `ErrNotTerminal` surfaces as 409.

```go
func TestDeleteSession_CallsServiceAndReturnsOK(t *testing.T) {
	svc := &fakeSessionService{} // the package's existing fake
	c := &SessionsController{Svc: svc}
	r := chi.NewRouter()
	c.Register(r)

	req := httptest.NewRequest(http.MethodDelete, "/sessions/sess-1?force=true", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if !svc.deleteCalled || svc.deleteID != "sess-1" || !svc.deleteForce {
		t.Fatalf("Delete not called as expected: %+v", svc)
	}
}
```

> Extend the package's `fakeSessionService` with `Delete` recording fields (`deleteCalled`, `deleteID`, `deleteForce`, `deleteErr`).

- [ ] **Step 3: Run test to verify it fails**

Run: `cd backend && go test ./internal/httpd/controllers/ -run TestDeleteSession -v`
Expected: FAIL — no route / `Delete` undefined on fake.

- [ ] **Step 4: Implement handler + route**

Add the route in `Register` (sessions.go:67), next to the kill route:

```go
	r.Delete("/sessions/{sessionId}", c.delete)
```

Add the handler (mirror `kill`, sessions.go:349):

```go
func (c *SessionsController) delete(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "DELETE", "/api/v1/sessions/{sessionId}")
		return
	}
	force := r.URL.Query().Get("force") == "true"
	if err := c.Svc.Delete(r.Context(), sessionID(r), force); err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, DeleteSessionResponse{OK: true, SessionID: sessionID(r)})
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `cd backend && go test ./internal/httpd/controllers/ -run TestDeleteSession -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add backend/internal/httpd/controllers/sessions.go backend/internal/httpd/controllers/sessions_test.go
git commit -m "feat(api): DELETE /sessions/{id} handler for finished-session delete"
```

---

## Task 8: `SettingsController` + registration

**Files:**
- Create: `backend/internal/httpd/controllers/settings.go`
- Test: `backend/internal/httpd/controllers/settings_test.go`
- Modify: `backend/internal/httpd/api.go`

**Interfaces:**
- Consumes: `reclaimsettings.Settings`, `ReclaimSettingsResponse`/`SetReclaimSettingsRequest` DTOs (Task 6).
- Produces:
  - `type SettingsService interface { Get() reclaimsettings.Settings; Set(reclaimsettings.Settings) error }`
  - `type SettingsController struct { Svc SettingsService }` with `Register`, `get`, `set`
  - `APIDeps.Settings controllers.SettingsService`; `api.go` constructs + registers it.

- [ ] **Step 1: Write the failing test**

```go
func TestSettingsController_GetReturnsCurrent(t *testing.T) {
	svc := &fakeSettingsSvc{cur: reclaimsettings.Settings{Enabled: true, GraceMinutes: 15}}
	c := &SettingsController{Svc: svc}
	r := chi.NewRouter()
	c.Register(r)

	req := httptest.NewRequest(http.MethodGet, "/settings/reclaim", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"graceMinutes":15`) {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
}

func TestSettingsController_PutValidatesAndSaves(t *testing.T) {
	svc := &fakeSettingsSvc{}
	c := &SettingsController{Svc: svc}
	r := chi.NewRouter()
	c.Register(r)

	body := strings.NewReader(`{"enabled":false,"graceMinutes":30}`)
	req := httptest.NewRequest(http.MethodPut, "/settings/reclaim", body)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	if svc.saved.GraceMinutes != 30 || svc.saved.Enabled {
		t.Fatalf("saved=%+v", svc.saved)
	}
}
```

```go
type fakeSettingsSvc struct {
	cur   reclaimsettings.Settings
	saved reclaimsettings.Settings
	err   error
}

func (f *fakeSettingsSvc) Get() reclaimsettings.Settings { return f.cur }
func (f *fakeSettingsSvc) Set(s reclaimsettings.Settings) error {
	f.saved = s
	f.cur = s
	return f.err
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/httpd/controllers/ -run TestSettingsController -v`
Expected: FAIL — `SettingsController` undefined.

- [ ] **Step 3: Implement the controller**

```go
package controllers

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/reclaimsettings"
)

// SettingsService is the reclaim-settings store surface the controller needs.
type SettingsService interface {
	Get() reclaimsettings.Settings
	Set(reclaimsettings.Settings) error
}

// SettingsController serves the global auto-reclaim settings.
type SettingsController struct {
	Svc SettingsService
}

// Register mounts the settings routes.
func (c *SettingsController) Register(r chi.Router) {
	r.Get("/settings/reclaim", c.get)
	r.Put("/settings/reclaim", c.set)
}

func (c *SettingsController) get(w http.ResponseWriter, r *http.Request) {
	s := c.Svc.Get()
	envelope.WriteJSON(w, http.StatusOK, ReclaimSettingsResponse{Enabled: s.Enabled, GraceMinutes: s.GraceMinutes})
}

func (c *SettingsController) set(w http.ResponseWriter, r *http.Request) {
	var req SetReclaimSettingsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_BODY", "Invalid settings body", nil)
		return
	}
	next := reclaimsettings.Settings{Enabled: req.Enabled, GraceMinutes: req.GraceMinutes}
	if err := c.Svc.Set(next); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_SETTINGS", err.Error(), nil)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ReclaimSettingsResponse{Enabled: next.Enabled, GraceMinutes: next.GraceMinutes})
}
```

> Confirm `envelope.WriteAPIError`'s exact signature against its use at `sessions.go:447` and match arg order.

- [ ] **Step 4: Register in `api.go`**

Add to `APIDeps` (api.go:21): `Settings controllers.SettingsService`. Add the field to the `API` struct (api.go:40-47): `settings *controllers.SettingsController`. Construct it in `NewAPI` (api.go:56-70): `settings: &controllers.SettingsController{Svc: deps.Settings},`. Register it (api.go:88-94): `a.settings.Register(r)`.

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd backend && go test ./internal/httpd/... -v`
Expected: PASS (controller tests + the parity test now that both new routes exist).

- [ ] **Step 6: Commit**

```bash
git add backend/internal/httpd/controllers/settings.go backend/internal/httpd/controllers/settings_test.go backend/internal/httpd/api.go
git commit -m "feat(api): SettingsController for reclaim settings GET/PUT"
```

---

## Task 9: Daemon wiring — settings store, reclaimer loop, Settings dep

**Files:**
- Create: `backend/internal/daemon/reclaim_wiring.go`
- Modify: `backend/internal/daemon/daemon.go`

**Interfaces:**
- Consumes: `reclaimsettings.NewStore`, `reclaimer.New(...).Start`, `sessionSvc` (*session.Service), `httpd.APIDeps.Settings`.
- Produces: `func startReclaimer(ctx, sessionSvc, settings, log) <-chan struct{}`.

- [ ] **Step 1: Add the wiring helper**

`reclaim_wiring.go`:

```go
package daemon

import (
	"context"
	"log/slog"

	"github.com/aoagents/agent-orchestrator/backend/internal/observe/reclaimer"
	"github.com/aoagents/agent-orchestrator/backend/internal/reclaimsettings"
	sessionsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/session"
)

// startReclaimer launches the auto-reclaim poll loop. The returned channel
// closes when the loop exits (ctx cancel), mirroring the reaper's contract.
func startReclaimer(ctx context.Context, sessions *sessionsvc.Service, settings *reclaimsettings.Store, log *slog.Logger) <-chan struct{} {
	return reclaimer.New(sessions, settings, reclaimer.Config{Logger: log}).Start(ctx)
}
```

> Verify the session-service package import alias used elsewhere in `daemon/` (`lifecycle_wiring.go` uses `sessionsvc "..."/service/session`) and match it.

- [ ] **Step 2: Wire into `daemon.go`**

After `sessionSvc` is built (daemon.go:124) and before/around the preview poller (daemon.go:134), construct the settings store and start the reclaimer:

```go
	reclaimSettings, err := reclaimsettings.NewStore(cfg.DataDir)
	if err != nil {
		return fmt.Errorf("reclaim settings: %w", err)
	}
	reclaimerDone := startReclaimer(ctx, sessionSvc, reclaimSettings, log)
```

Add `Settings: reclaimSettings,` to the `httpd.APIDeps{...}` literal (daemon.go:142-145 area).

Drain `reclaimerDone` on shutdown next to `<-previewDone` (daemon.go:203) and in the two early-error paths that call `lcStack.Stop()`:

```go
	<-previewDone
	<-reclaimerDone
	lcStack.Stop()
```

Add the imports for `reclaimsettings` (and `reclaimer` is used only inside `reclaim_wiring.go`).

- [ ] **Step 3: Build + run the daemon package tests**

Run: `cd backend && go build ./... && go test ./internal/daemon/...`
Expected: PASS (compiles; existing wiring tests still green).

- [ ] **Step 4: Smoke-test the whole backend**

Run: `cd backend && go test ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/daemon/reclaim_wiring.go backend/internal/daemon/daemon.go
git commit -m "feat(daemon): wire reclaim settings store + auto-reclaim loop"
```

---

## Task 10: Frontend api-client route templates

**Files:**
- Modify: `frontend/src/renderer/lib/api-client.ts`

**Interfaces:**
- Consumes: regenerated `paths` in `schema.ts` (Task 6).
- Produces: telemetry-safe route labels for the new endpoints.

`ROUTE_TEMPLATES` (api-client.ts:51-80) keeps IDs out of telemetry operation labels; a route missing here logs a raw path with the session id. Add the new templates.

- [ ] **Step 1: Add the templates**

In the `ROUTE_TEMPLATES` array add:

```ts
	"/api/v1/settings/reclaim",
```

The existing `"/api/v1/sessions/{sessionId}"` template already covers the DELETE verb (templates are path-keyed, not verb-keyed), so no session entry is needed — confirm it is present (it is, api-client.ts:66).

- [ ] **Step 2: Typecheck**

Run: `npm run frontend:typecheck`
Expected: PASS — `apiClient.DELETE("/api/v1/sessions/{sessionId}", …)` and `apiClient.GET/PUT("/api/v1/settings/reclaim", …)` now typecheck against the regenerated schema.

- [ ] **Step 3: Commit**

```bash
git add frontend/src/renderer/lib/api-client.ts
git commit -m "chore(web): register reclaim-settings route template"
```

---

## Task 11: Board — per-chip delete with inline confirm

**Files:**
- Modify: `frontend/src/renderer/components/SessionsBoard.tsx`
- Test: `frontend/src/renderer/components/SessionsBoard.test.tsx`

**Interfaces:**
- Consumes: `apiClient.DELETE`, `apiErrorMessage`, `workspaceQueryKey`, `useMutation`/`useQueryClient`.
- Produces: a `DoneChip` component replacing the inline `done.map` button; deletes via the DELETE endpoint and invalidates the board query.

The current done bar renders each session as a bare `<button>` (SessionsBoard.tsx:249-258). Replace with a `DoneChip` that keeps the open-on-click title area and adds a trash button with an inline arm-confirm (mirrors `TopbarKillButton`, avoids a nested-button and a new dialog dep).

- [ ] **Step 1: Write the failing test**

Add to `SessionsBoard.test.tsx` (this file currently mocks router + workspace query only; add an `apiClient` mock and feed a terminated session):

```tsx
vi.mock("../lib/api-client", () => ({
	apiClient: { DELETE: deleteMock },
	apiErrorMessage: (e: unknown, fb = "Request failed") => (e instanceof Error ? e.message : fb),
}));

// in a test:
it("deletes a done session after confirm", async () => {
	deleteMock.mockResolvedValue({ error: undefined });
	workspaceQueryMock.mockReturnValue({
		data: [{ id: "proj-1", sessions: [doneSession("sess-1")] }],
		isError: false,
	});
	renderBoard();
	await userEvent.click(screen.getByRole("button", { name: /Done \/ Terminated/i })); // expand
	await userEvent.click(screen.getByRole("button", { name: "Delete session" }));
	expect(deleteMock).not.toHaveBeenCalled();
	await userEvent.click(screen.getByRole("button", { name: "Confirm delete" }));
	await waitFor(() =>
		expect(deleteMock).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}", {
			params: { path: { sessionId: "sess-1" }, query: { force: false } },
		}),
	);
});
```

> Add `deleteMock` to the `vi.hoisted(...)` block and a `doneSession(id)` fixture (a `WorkspaceSession` with `status: "terminated"`, `kind: "worker"`, `prs: []`). Wrap `renderBoard` in a `QueryClientProvider` (mirror `ShellTopbar.test.tsx`). The board reads data via `useWorkspaceQuery`; the mock must return the `{ data: [workspace] }` shape `SessionsBoard` expects.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd frontend && npx vitest run src/renderer/components/SessionsBoard.test.tsx -t "deletes a done session"`
Expected: FAIL — no "Delete session" button.

- [ ] **Step 3: Implement `DoneChip`**

Replace the `done.map(...)` block (SessionsBoard.tsx:248-259) with `<DoneChip key={s.id} session={s} onOpen={() => openSession(s)} />`, and add the component:

```tsx
function DoneChip({ session, onOpen }: { session: WorkspaceSession; onOpen: () => void }) {
	const queryClient = useQueryClient();
	const [confirming, setConfirming] = useState(false);
	const [error, setError] = useState<string | null>(null);

	const del = useMutation({
		mutationFn: async (force: boolean) => {
			const { error: apiError } = await apiClient.DELETE("/api/v1/sessions/{sessionId}", {
				params: { path: { sessionId: session.id }, query: { force } },
			});
			if (apiError) throw new Error(apiErrorMessage(apiError));
		},
		onSuccess: () => {
			setConfirming(false);
			void queryClient.invalidateQueries({ queryKey: workspaceQueryKey });
		},
		onError: (e) => setError(e instanceof Error ? e.message : "Delete failed"),
	});

	return (
		<div className="flex items-center gap-1 rounded-[7px] border border-border bg-surface pl-2.5 pr-1 py-1.5 transition-colors hover:border-border-strong">
			<button className="text-left text-[12px] text-muted-foreground" onClick={onOpen} type="button">
				{session.title}
			</button>
			{confirming ? (
				<>
					<button
						aria-label="Confirm delete"
						className="text-[11px] text-error"
						disabled={del.isPending}
						onClick={() => del.mutate(false)}
						type="button"
					>
						Confirm
					</button>
					<button
						aria-label="Cancel delete"
						className="text-[11px] text-passive"
						onClick={() => setConfirming(false)}
						type="button"
					>
						Cancel
					</button>
				</>
			) : (
				<button
					aria-label="Delete session"
					className="rounded p-1 text-passive hover:text-error"
					onClick={() => {
						setError(null);
						setConfirming(true);
					}}
					type="button"
				>
					<Trash2 className="h-3 w-3" aria-hidden="true" />
				</button>
			)}
			{error && (
				<span className="flex items-center gap-1 text-[10px] text-error">
					{error}
					{/* A dirty-worktree refusal (SESSION_WORKSPACE_DIRTY) is the expected
					    reason; offer a force delete that discards uncommitted changes. */}
					<button
						aria-label="Delete anyway"
						className="underline hover:text-error"
						disabled={del.isPending}
						onClick={() => del.mutate(true)}
						type="button"
					>
						Delete anyway
					</button>
				</span>
			)}
		</div>
	);
}
```

Add imports at the top of the file: `useMutation, useQueryClient` from `@tanstack/react-query` (react-query is already imported for `useQueryClient`; add `useMutation`), `apiClient, apiErrorMessage` from `../lib/api-client`, and `Trash2` from `lucide-react` (the file already imports from `lucide-react`).

- [ ] **Step 4: Run test to verify it passes**

Run: `cd frontend && npx vitest run src/renderer/components/SessionsBoard.test.tsx`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add frontend/src/renderer/components/SessionsBoard.tsx frontend/src/renderer/components/SessionsBoard.test.tsx
git commit -m "feat(web): delete finished sessions from the Done bar"
```

---

## Task 12: Board — "Clear all" for the Done bucket

**Files:**
- Modify: `frontend/src/renderer/components/SessionsBoard.tsx`
- Test: `frontend/src/renderer/components/SessionsBoard.test.tsx`

**Interfaces:**
- Consumes: the same DELETE mutation, `done` list.
- Produces: a "Clear all" button in the done-bar header that deletes every done session (iterated DELETE; there is no bulk endpoint) behind a confirm.

- [ ] **Step 1: Write the failing test**

```tsx
it("clears all done sessions", async () => {
	deleteMock.mockResolvedValue({ error: undefined });
	workspaceQueryMock.mockReturnValue({
		data: [{ id: "proj-1", sessions: [doneSession("s1"), doneSession("s2")] }],
		isError: false,
	});
	renderBoard();
	await userEvent.click(screen.getByRole("button", { name: /Done \/ Terminated/i }));
	await userEvent.click(screen.getByRole("button", { name: "Clear all" }));
	await userEvent.click(screen.getByRole("button", { name: "Delete all" })); // confirm in dialog
	await waitFor(() => expect(deleteMock).toHaveBeenCalledTimes(2));
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd frontend && npx vitest run src/renderer/components/SessionsBoard.test.tsx -t "clears all"`
Expected: FAIL — no "Clear all" button.

- [ ] **Step 3: Implement Clear-all**

Add a `ClearAllButton` rendered in the done-bar header row (SessionsBoard.tsx:244-246, beside the count). Use the same `useMutation` DELETE keyed over the `done` array, guarded by a small controlled Radix dialog (mirror `RestoreUnavailableDialog.tsx`):

```tsx
function ClearAllButton({ sessions }: { sessions: WorkspaceSession[] }) {
	const queryClient = useQueryClient();
	const [open, setOpen] = useState(false);
	const [error, setError] = useState<string | null>(null);

	const clear = useMutation({
		mutationFn: async () => {
			// No bulk endpoint: delete each finished session. force=false so a dirty
			// worktree surfaces rather than silently discarding uncommitted work.
			const results = await Promise.allSettled(
				sessions.map((s) =>
					apiClient.DELETE("/api/v1/sessions/{sessionId}", {
						params: { path: { sessionId: s.id }, query: { force: false } },
					}),
				),
			);
			const failed = results.filter((r) => r.status === "rejected" || (r.value && "error" in r.value && r.value.error));
			if (failed.length > 0) throw new Error(`${failed.length} session(s) could not be deleted (uncommitted changes?)`);
		},
		onSuccess: () => {
			setOpen(false);
			void queryClient.invalidateQueries({ queryKey: workspaceQueryKey });
		},
		onError: (e) => setError(e instanceof Error ? e.message : "Clear failed"),
	});

	return (
		<>
			<button
				aria-label="Clear all"
				className="font-mono text-[10px] text-passive hover:text-error"
				onClick={(e) => {
					e.stopPropagation();
					setError(null);
					setOpen(true);
				}}
				type="button"
			>
				Clear all
			</button>
			<Dialog.Root open={open} onOpenChange={setOpen}>
				<Dialog.Portal>
					<Dialog.Overlay className="fixed inset-0 z-50 bg-black/50" />
					<Dialog.Content className="fixed left-1/2 top-1/2 z-50 w-[420px] -translate-x-1/2 -translate-y-1/2 rounded-lg border border-border bg-surface p-5 shadow-lg">
						<Dialog.Title className="text-sm font-medium text-foreground">Clear all finished sessions</Dialog.Title>
						<Dialog.Description className="mt-2 text-[13px] text-muted-foreground">
							Permanently remove {sessions.length} finished session(s) from AO. Their git branches are kept.
						</Dialog.Description>
						{error && <div className="mt-3 text-[12px] text-error">{error}</div>}
						<div className="mt-4 flex justify-end gap-2">
							<Button variant="ghost" onClick={() => setOpen(false)} disabled={clear.isPending}>
								Cancel
							</Button>
							<Button onClick={() => clear.mutate()} disabled={clear.isPending}>
								Delete all
							</Button>
						</div>
					</Dialog.Content>
				</Dialog.Portal>
			</Dialog.Root>
		</>
	);
}
```

Render it in the header: in the done-bar button row, add (outside the toggle `<button>` to avoid nesting) `{done.length > 0 && <ClearAllButton sessions={done} />}` positioned in the header flex. Import `* as Dialog from "@radix-ui/react-dialog"` and `Button` from `./ui/button`.

> Placing an interactive control inside the toggle `<button>` is invalid HTML — restructure the header row (SessionsBoard.tsx:228-246) so the chevron+label toggle and the Clear-all control are siblings in a flex row, not nested.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd frontend && npx vitest run src/renderer/components/SessionsBoard.test.tsx`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add frontend/src/renderer/components/SessionsBoard.tsx frontend/src/renderer/components/SessionsBoard.test.tsx
git commit -m "feat(web): Clear-all for the Done/Terminated bar"
```

---

## Task 13: Settings — `AutoReclaimSection` card

**Files:**
- Create: `frontend/src/renderer/components/AutoReclaimSection.tsx`
- Test: `frontend/src/renderer/components/AutoReclaimSection.test.tsx`
- Modify: `frontend/src/renderer/components/GlobalSettingsForm.tsx`

**Interfaces:**
- Consumes: `apiClient.GET/PUT("/api/v1/settings/reclaim")`, `apiErrorMessage`, react-query.
- Produces: a settings card with an Enabled toggle + a grace-minutes number input, saved via PUT. Mirrors `UpdatesSection` (toggle + Save) and `ProjectSettingsForm`'s daemon-backed mutation, but hits the daemon via `apiClient` (not `aoBridge`).

- [ ] **Step 1: Write the failing test**

```tsx
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

const { getMock, putMock } = vi.hoisted(() => ({ getMock: vi.fn(), putMock: vi.fn() }));
vi.mock("../lib/api-client", () => ({
	apiClient: { GET: getMock, PUT: putMock },
	apiErrorMessage: (e: unknown, fb = "Request failed") => (e instanceof Error ? e.message : fb),
}));

import { AutoReclaimSection } from "./AutoReclaimSection";

function renderSection() {
	const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	render(
		<QueryClientProvider client={qc}>
			<AutoReclaimSection />
		</QueryClientProvider>,
	);
}

beforeEach(() => {
	getMock.mockReset().mockResolvedValue({ data: { enabled: true, graceMinutes: 15 }, error: undefined });
	putMock.mockReset().mockResolvedValue({ data: { enabled: true, graceMinutes: 20 }, error: undefined });
});

describe("AutoReclaimSection", () => {
	it("loads settings and saves an edited grace", async () => {
		renderSection();
		const input = await screen.findByLabelText(/grace/i);
		await userEvent.clear(input);
		await userEvent.type(input, "20");
		await userEvent.click(screen.getByRole("button", { name: "Save changes" }));
		await waitFor(() =>
			expect(putMock).toHaveBeenCalledWith("/api/v1/settings/reclaim", {
				body: { enabled: true, graceMinutes: 20 },
			}),
		);
	});
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd frontend && npx vitest run src/renderer/components/AutoReclaimSection.test.tsx`
Expected: FAIL — module not found.

- [ ] **Step 3: Implement `AutoReclaimSection`**

Mirror `UpdatesSection` (Card + toggle + Save/status row) and `ProjectSettingsForm`'s daemon mutation. Use the same `Card`/`Label`/`Button`/`Select` primitives and the `EnabledSelect` on/off pattern:

```tsx
import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Button } from "./ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "./ui/card";
import { Label } from "./ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "./ui/select";
import { apiClient, apiErrorMessage } from "../lib/api-client";

type ReclaimSettings = { enabled: boolean; graceMinutes: number };
const reclaimSettingsQueryKey = ["settings", "reclaim"] as const;

export function AutoReclaimSection() {
	const queryClient = useQueryClient();
	const query = useQuery({
		queryKey: reclaimSettingsQueryKey,
		queryFn: async () => {
			const { data, error } = await apiClient.GET("/api/v1/settings/reclaim", {});
			if (error) throw new Error(apiErrorMessage(error));
			return data as ReclaimSettings;
		},
	});
	const [form, setForm] = useState<ReclaimSettings>({ enabled: true, graceMinutes: 15 });
	const [savedAt, setSavedAt] = useState<number | null>(null);

	useEffect(() => {
		if (query.data) setForm(query.data);
	}, [query.data]);

	const save = useMutation({
		mutationFn: async (next: ReclaimSettings) => {
			const { error } = await apiClient.PUT("/api/v1/settings/reclaim", { body: next });
			if (error) throw new Error(apiErrorMessage(error));
		},
		onSuccess: () => {
			setSavedAt(Date.now());
			void queryClient.invalidateQueries({ queryKey: reclaimSettingsQueryKey });
		},
	});

	return (
		<Card>
			<CardHeader>
				<CardTitle className="text-[13px]">Auto-reclaim finished sessions</CardTitle>
			</CardHeader>
			<CardContent className="flex flex-col gap-4">
				<p className="text-[12px] text-muted-foreground">
					When a session is merged or terminated, AO tears down its tmux and worktree after the grace period. The git branch
					is kept, so the session can still be restored.
				</p>
				<div className="flex flex-col gap-1.5">
					<Label htmlFor="reclaimEnabled" className="text-[12px] text-muted-foreground">
						Auto-reclaim
					</Label>
					<Select
						value={form.enabled ? "on" : "off"}
						onValueChange={(v) => {
							setSavedAt(null);
							setForm((f) => ({ ...f, enabled: v === "on" }));
						}}
					>
						<SelectTrigger id="reclaimEnabled" className="h-8 w-full text-[13px]">
							<SelectValue />
						</SelectTrigger>
						<SelectContent>
							<SelectItem value="on">Enabled</SelectItem>
							<SelectItem value="off">Disabled</SelectItem>
						</SelectContent>
					</Select>
				</div>
				<div className="flex flex-col gap-1.5">
					<Label htmlFor="reclaimGrace" className="text-[12px] text-muted-foreground">
						Grace period (minutes)
					</Label>
					<input
						id="reclaimGrace"
						type="number"
						min={0}
						className="h-8 w-full rounded-md border border-input bg-transparent px-2.5 text-[13px] text-foreground focus-visible:border-accent focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent-weak"
						value={form.graceMinutes}
						onChange={(e) => {
							setSavedAt(null);
							setForm((f) => ({ ...f, graceMinutes: Math.max(0, Number(e.target.value) || 0) }));
						}}
					/>
				</div>
				<div className="flex items-center gap-3">
					<Button type="button" variant="primary" onClick={() => save.mutate(form)} disabled={save.isPending}>
						{save.isPending ? "Saving…" : "Save changes"}
					</Button>
					{save.isError && (
						<span className="text-[12px] text-error">
							{save.error instanceof Error ? save.error.message : "Save failed"}
						</span>
					)}
					{savedAt && !save.isPending && !save.isError && <span className="text-[12px] text-success">Saved.</span>}
				</div>
			</CardContent>
		</Card>
	);
}
```

> Confirm the `Button` variant name (`"primary"`) and the Card/Select import paths against `UpdatesSection.tsx` and adjust if this repo names them differently.

- [ ] **Step 4: Mount it in `GlobalSettingsForm`**

Edit `GlobalSettingsForm.tsx`: import `AutoReclaimSection` and add `<AutoReclaimSection />` into the `flex flex-col gap-4` stack (above `<UpdatesSection />`).

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd frontend && npx vitest run src/renderer/components/AutoReclaimSection.test.tsx src/renderer/components/GlobalSettingsForm.test.tsx`
Expected: PASS.

- [ ] **Step 6: Full frontend gate**

Run: `npm run frontend:typecheck && cd frontend && npx vitest run`
Expected: PASS. (Revert any `routeTree.gen.ts` formatting churn before committing — do not stage it.)

- [ ] **Step 7: Commit**

```bash
git add frontend/src/renderer/components/AutoReclaimSection.tsx frontend/src/renderer/components/AutoReclaimSection.test.tsx frontend/src/renderer/components/GlobalSettingsForm.tsx
git commit -m "feat(web): auto-reclaim settings card in Global settings"
```

---

## Final verification

- [ ] **Backend:** `cd backend && go test ./... && go test -race ./internal/observe/... ./internal/session_manager/... ./internal/service/session/...`
- [ ] **Lint:** `npm run lint`
- [ ] **Frontend:** `npm run frontend:typecheck && cd frontend && npx vitest run`
- [ ] **API drift:** `cd backend && go test ./internal/httpd/...` (spec + parity green; `openapi.yaml` + `schema.ts` committed).
- [ ] **Manual smoke (use the `verify` skill / `ao preview`):** build+run the app, spawn a worker, merge or kill it, confirm after the grace it disappears from live columns and its tmux + worktree are gone (`tmux ls`, worktree dir) while its branch survives (`git branch --list`); confirm the Done bar trash + Clear-all remove rows and keep branches; confirm the settings card toggles/saves and disabling stops auto-reclaim.

---

## Notes for the executor

- **DRY:** `Reclaim` deliberately reuses `Kill` rather than duplicating teardown. `PurgeSession` reuses `Kill`'s teardown shape but must not be collapsed into `Kill` (it adds force + row purge).
- **Order matters:** Task 6 (spec regen) must land before Tasks 7–8 typecheck the generated client, and before Task 10's frontend typecheck.
- **Grace clock is in-memory:** a daemon restart resets pending grace timers (accepted per spec). No persistence.
- **Never** `git branch -D`. If any test or step tempts you to remove a branch, stop — that violates a global constraint.
