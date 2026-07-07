# Auto-close idle sessions (keep tmux across reopen, GC on inactivity)

**Date:** 2026-07-07
**Status:** Design — awaiting review

## Problem

Two related pains with the current session/tmux lifecycle:

1. **tmux is reaped too eagerly on app reopen.** When a session's agent exits
   (e.g. Claude finishes), the `session-end` hook posts `exited` →
   `lifecycle.Manager.ApplyActivitySignal` sets `IsTerminated = true` **without**
   destroying the tmux session. The `exec "${SHELL}" -i` keep-alive shell keeps
   the tmux alive as an idle `zsh`. On the next daemon boot, `reconcileReap`
   (`backend/internal/session_manager/manager.go:818`) destroys that leftover
   tmux immediately. Net effect the user sees: close app → tmux survives →
   reopen app → tmux gone. The user wants the tmux to persist across reopen.

2. **No inactivity-based cleanup.** Nothing closes sessions that have sat idle
   for a long time; the only cleanup is the immediate boot-time reap above.

## Goal

Replace the immediate boot-time reap with a **time-based idle close**:

- A tmux session survives daemon restarts / app reopens.
- Any session with no activity for longer than a configurable TTL is
  auto-closed: its tmux is destroyed and the session is marked `terminated`,
  **but its worktree is kept on disk** so it stays restorable.
- Restore is unchanged and already trivial (existing "Restore session" button /
  `POST /api/v1/sessions/{id}/restore` / `ao session restore`).

## Decisions (locked with the user)

| Decision | Choice |
|---|---|
| Which sessions are eligible | **All** sessions idle beyond the TTL — no exceptions (terminated leftovers, idle-but-alive agents, and waiting-for-input sessions all qualify). Basis: `Activity.LastActivityAt`. |
| What "close" does | Destroy tmux + `MarkTerminated` + clear the restore marker. **Keep the worktree** (do not call `workspace.Destroy`). Uncommitted work stays on disk; no capture/preserve-ref dance needed. |
| Restore | Reuse existing machinery. Keeping the worktree makes `manager.Restore` adopt it losslessly and `--resume` the agent. |
| Where the TTL lives | **Env var** `AO_SESSION_IDLE_CLOSE` (Go duration), read at daemon launch. No UI, no backend settings store. |
| When the sweep runs | **Boot pass + periodic ticker (~5 min)** — auto-closes over time while the app stays open, and also on each reopen. |
| Default | Enabled by default at **24h**. `AO_SESSION_IDLE_CLOSE=0` (or negative) disables. |

## Non-goals

- No Global Settings UI / backend settings store / HTTP endpoint (explicitly cut
  to minimize change; a UI toggle can be layered on later, reading the same value).
- No worktree removal / disk reclamation on auto-close (worktrees are kept by design).
- No fix for the pre-existing spawn name-collision / orphan bug (see Known limitations).

## Why "keep the worktree" is the key simplifier

A session that is `terminated` with its worktree still on disk and no restore
marker is a **stable, restorable state** across boots, using only existing code paths:

- `reconcileLive` only processes non-terminated sessions → skips it.
- `reconcileReap` finds its tmux already gone → no-op.
- `RestoreAll` restores only sessions with a `session_worktrees` marker → skips it
  (no auto-relaunch; the user restores on demand).
- The `TerminalPane` "Restore session" button appears because
  `session.status === "terminated"` (`frontend/src/renderer/components/TerminalPane.tsx:149`).
- `manager.Restore` (`manager.go:596`) re-adopts the existing worktree
  (`workspace.Restore` is a no-op when the dir is present) and relaunches with
  `--resume` — uncommitted work intact.

## Design

### 1. Config (`backend/internal/config/config.go`)

Add one field, parsed exactly like `RequestTimeout` / `ShutdownTimeout`:

```
// SessionIdleClose is the inactivity window after which an idle session is
// auto-closed (tmux destroyed, session terminated, worktree kept). 0 disables.
SessionIdleClose time.Duration   // AO_SESSION_IDLE_CLOSE, default 24h
```

- Default `24h`. Values `<= 0` disable the feature.
- Documented in the env-var comment block alongside the other `AO_*` vars.

### 2. `Manager.CloseIdleSessions` (`backend/internal/session_manager/manager.go`)

New method + a `idleCloseTTL time.Duration` field on `Manager` (wired via `Deps`,
`0` = disabled). The Manager already has an injected `clock func() time.Time`.

```
func (m *Manager) CloseIdleSessions(ctx context.Context) error
```

Behavior (best-effort, per-session failure logged, never aborts the pass):

1. If `m.idleCloseTTL <= 0`, return nil (disabled).
2. `now := m.clock()`; `recs := m.store.ListAllSessions(ctx)`.
3. For each `rec`:
   - Compute idle reference `ref := rec.Activity.LastActivityAt`; if zero, fall
     back to `rec.CreatedAt` (protects a freshly-spawned session that has not
     emitted a signal yet from being closed instantly).
   - If `now.Sub(ref) <= m.idleCloseTTL`, skip.
   - Otherwise close it:
     - `handle := runtimeHandle(rec.Metadata)`; if `handle.ID != ""` and
       `runtime.IsAlive` → `runtime.Destroy(ctx, handle)` (idempotent).
     - If `!rec.IsTerminated`: `lcm.MarkTerminated(ctx, rec.ID)` and
       `store.DeleteSessionWorktrees(ctx, rec.ID)` (clear any restore marker so
       boot never auto-relaunches it).
     - **Do not** call `workspace.Destroy` — the worktree is kept.

Note: an actively-working agent emits `active` heartbeats that refresh
`LastActivityAt`, so it never crosses the TTL. Only genuinely-silent sessions do.

### 3. Reconcile changes (`Manager.Reconcile`)

- **Remove the immediate destroy in `reconcileReap`.** Terminated leftovers'
  tmux must now survive reopen and be cleaned only by `CloseIdleSessions` once
  idle beyond the TTL. Simplest: delete `reconcileReap` and its loop; the idle
  sweep subsumes it.
- Keep `reconcileLive` as-is, including its "runtime definitely gone → capture +
  terminate + remove worktree" branch (that path is about crashed tmux, not
  inactivity, and legitimately reclaims a dead worktree).
- At the end of `Reconcile`, after the live pass and before `RestoreAll`, call
  `m.CloseIdleSessions(ctx)` so a reopen closes anything already past the TTL.

### 4. Ticker (`backend/internal/daemon/daemon.go`)

When `cfg.SessionIdleClose > 0`, launch a background goroutine following the
existing lifecycle pattern (started after the manager is built; cancelled via the
same `stop()` context-cancel and drained on shutdown like `previewDone`):

```
t := time.NewTicker(idleSweepInterval) // const, ~5 * time.Minute
for {
    select {
    case <-ctx.Done(): return
    case <-t.C:
        if err := sessMgr.CloseIdleSessions(ctx); err != nil {
            log.Warn("idle sweep failed", "err", err)
        }
    }
}
```

`idleSweepInterval` is a package constant (~5m), independent of the TTL.

### 5. Frontend / API

No changes. Restore already exists end-to-end (UI button, CLI, HTTP).

## Data flow

```
agent goes quiet ──► LastActivityAt frozen
        │
        ▼ (ticker every ~5m, or next boot)
CloseIdleSessions: now - LastActivityAt > TTL ?
        │ yes
        ▼
runtime.Destroy(tmux) ; MarkTerminated ; DeleteSessionWorktrees ; keep worktree
        │
        ▼
board shows "terminated" ──► user clicks "Restore session"
        │
        ▼
manager.Restore: adopt existing worktree + --resume  ──► live again
```

## Edge cases

- **Freshly spawned, no signal yet:** `LastActivityAt` zero → fall back to
  `CreatedAt`; not closed until `CreatedAt` is older than the TTL.
- **Disabled:** `AO_SESSION_IDLE_CLOSE <= 0` → `CloseIdleSessions` is a no-op and
  the ticker is not started.
- **Already-terminated leftover shell:** tmux destroyed once idle beyond TTL;
  DB already terminated so only the `runtime.Destroy` runs.
- **Reviewer sessions:** swept the same way (tmux destroyed); they are already
  excluded from the Restore button (`canRestoreSession`), so no behavior change.
- **Orchestrator restore:** relaunches fresh (system prompt only, no conversation
  `--resume`) but on the same branch/worktree — accepted.

## Known limitations (accepted for this iteration)

- **Disk growth:** kept worktrees are never reclaimed by auto-close. Manual
  `Kill` still removes them; a future disk-GC could prune very old terminated
  worktrees.
- **Spawn name-collision window:** with the immediate reap removed, a leftover
  tmux under a per-project/branch name can linger until the TTL, so a fresh
  `Spawn`/`Restore` of the same-named orchestrator during that window can hit
  `duplicate session` — the pre-existing low-severity orphan bug the user already
  chose not to fix. Recovery is unchanged (restart, or `tmux kill-session`). A
  clear-before-`new-session` (or `-A`) can be added later if it starts biting.
- **TTL change requires daemon restart:** the value is read from the env at
  launch. Acceptable for an env-var config; a UI toggle would remove this.

## Testing

- `config` test: `AO_SESSION_IDLE_CLOSE` parsed; default `24h`; `0`/invalid disables.
- `CloseIdleSessions` unit tests with a fake clock:
  - idle > TTL, alive tmux → `Destroy` called, `MarkTerminated`, restore marker
    cleared, `workspace.Destroy` **not** called.
  - idle > TTL, already terminated → only `Destroy`.
  - idle <= TTL → untouched.
  - no-signal session younger than TTL (via `CreatedAt`) → untouched.
  - `idleCloseTTL <= 0` → no-op.
- `Reconcile` test: a terminated session with a live tmux that is **not** idle
  past the TTL is left alone at boot (proves tmux survives reopen); one past the
  TTL is closed.
- Ticker: covered indirectly via `CloseIdleSessions`; a light daemon test can
  assert the goroutine starts only when the TTL is positive.
```
