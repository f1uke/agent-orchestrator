# Auto-reclaim and delete finished sessions

## Problem

Every live worker session pins a tmux session, a git worktree on disk, and a
running agent process. Nothing reclaims these when the work is done:

- A **merged** session (`deriveStatus` → `StatusMerged`, `service/session/status.go:63`)
  is still `IsTerminated=false`, so its tmux + worktree + agent stay alive
  indefinitely after the PR merged.
- A session **terminated by process death** (the reaper reports `ProbeDead`) has
  its record marked terminated but its worktree lingers on disk until someone runs
  `ao session cleanup`.

So when a user runs many tasks, tmux sessions and worktrees accumulate with no
automatic floor. Today the only reclamation paths are manual: `Kill`
(`session_manager/manager.go:488`) and `Cleanup` (`manager.go:989`).

Separately, the board's **Done / Terminated** bar (`SessionsBoard.tsx:219`) lists
finished sessions but offers no way to remove them — it only opens a session. The
list grows unbounded and the only way to clear it is to never look.

We want two things:

1. **Auto-reclaim** — once a worker session lands in the Done zone
   (merged or terminated), automatically tear down its tmux + worktree after a
   grace period while **keeping the git branch**, so the session stays restorable.
2. **Delete** — from the Done / Terminated bar, permanently remove finished
   sessions from AO (declutter) while **never deleting the git branch**.

## Scope

- **Worker sessions only.** Orchestrators are excluded — the Done bar already
  filters to workers (`workerSessions`, `types/workspace.ts:271`), and
  orchestrator lifecycle (retire/replace) is handled elsewhere.
- Reuse the existing `Kill` / `Cleanup` / `Restore` / `DeleteSession` machinery.
  No new lifecycle state is introduced (Approach A, chosen over a dedicated
  `reclaimed` marker, for minimal surface and risk).

## Key facts this design relies on

- **`Kill`'s teardown keeps the branch.** `workspace.Destroy`
  (`gitworktree/workspace.go:149`) removes only the worktree directory and prunes
  it; it never runs `git branch -D`. No code in the gitworktree adapter deletes
  branches. So a torn-down session is restorable from its branch via `Restore`
  (`manager.go:584`), which re-adds the worktree with `git worktree add`.
- **`Restore` gates on `IsTerminated`**, not on the boot-restore marker, so a
  killed/reclaimed session is manually restorable even though `Kill` deletes the
  marker.
- **Dirty worktrees are already protected.** `workspace.Destroy` refuses a dirty
  worktree with `ErrWorkspaceDirty`; `Kill` treats that as `freed=false` and
  leaves the worktree intact (`manager.go:516-524`). Uncommitted work is never
  force-destroyed.
- **The reaper skips terminated sessions** (`reaper.go:128`), so a reclaimed
  (now-terminated) session is not probed and will not be re-flagged.
- **PR facts survive teardown.** `session.prs` is independent of runtime state, so
  a merged session that becomes `terminated` after reclaim still carries its
  merged PR — the UI can show a "merged" badge from PR facts.

## Feature 1 — Auto-reclaim sweeper

### Trigger

A worker session whose computed display status (`deriveStatus`) is
`StatusMerged` or `StatusTerminated` — i.e. the same "Done zone" the board's
Done / Terminated bar already groups.

### Mechanism

A new OBSERVE-layer sweeper, modelled on `reaper` (`observe/reaper/reaper.go`):

- Ticks on a timer (**~60s**; grace is measured in minutes, so a fast tick buys
  nothing). Exposes a synchronous `Tick(ctx)` for tests, like the reaper.
- Each tick, for every non-orchestrator session:
  1. Compute display status. If it is **not** merged/terminated → clear any
     tracked timestamp for that session and continue.
  2. If merged/terminated, record a **first-seen-terminal timestamp** in an
     **in-memory** map (`sessionID → time`) the first time it is seen in that
     state. No schema change / migration.
  3. If the setting is **off** → skip. If the grace has **not** elapsed → skip.
  4. If the grace has elapsed **and** the session still holds a runtime handle
     or a worktree path → call `Reclaim(id)`.
- A session already torn down (no runtime handle, no worktree — e.g. terminated
  by an explicit `Kill`) matches step 4's guard as a no-op and is skipped.

**In-memory grace clock trade-off:** the first-seen map is not persisted, so a
daemon restart resets the grace for still-pending sessions. Accepted — the worst
case is a session waits one extra grace period after a restart. This avoids a
storage column + migration and keeps Approach A minimal.

### Reclaim operation

Add `Manager.Reclaim(ctx, id)` as a thin wrapper with the **same teardown
semantics as `Kill`**: `MarkTerminated` → delete restore marker → destroy runtime
(if handle present) → destroy worktree (if path present, **skipped when dirty**)
→ branch kept. It is a separate method (not a direct `Kill` call) purely so logs
and telemetry distinguish an automatic reclaim from a user-initiated kill.

Deleting the restore marker matches `Kill`: a reclaimed session must **not** be
auto-resurrected by boot `RestoreAll`; the user restores it explicitly when they
want it back.

Dirty worktrees are left intact (teardown skipped for that session); the next
tick retries. This means a merged session with uncommitted changes keeps its
worktree until the user commits or resolves it — deliberate, to never lose work.

### UI impact

- A reclaimed **merged** session's status flips merged → terminated. The Done bar
  continues to show a **"merged"** badge sourced from `session.prs` so the
  merged-vs-killed distinction survives at the display level.
- The dead-session **Restore** affordance already appears when
  `status === "terminated"` (`TerminalPane.tsx`), so restore works unchanged.

## Feature 2 — Delete finished sessions

### Behavior

A permanent, declutter-oriented delete of a **terminal** (merged/terminated)
worker session:

1. Best-effort reclaim any leftover runtime + worktree (keep branch) — reuse the
   `Reclaim`/`Cleanup` teardown.
2. Delete the session row and its dependent rows (worktree / restore-marker rows,
   PR rows) from the store.
3. **Never** run `git branch -D`. The git branch remains for manual use.

After delete the session disappears from the board and is **no longer restorable
via AO** (the record is gone); the branch is untouched.

### Guards

- **Terminal-only.** Refuse to delete a session that is not merged/terminated, so
  a live/working session can never be nuked.
- **Dirty worktree.** If a leftover worktree is dirty (uncommitted work not yet on
  the branch), refuse with a typed error and surface it in the confirm dialog,
  unless the user explicitly confirms a force. Committed work is safe on the
  branch regardless.

### Backend

Today `DeleteSession` (`manager.go` / `store`) only removes seed-state rows, a
guarantee other callers rely on. Rather than widen it, add a **new**
`PurgeSession(ctx, id, force)` that implements the terminal-only hard delete above
and returns typed errors (`ErrNotTerminal`, `ErrWorkspaceDirty`). `DeleteSession`
is left untouched.

### API

`DELETE /api/v1/sessions/{sessionId}` — distinct from the existing
`POST /sessions/{sessionId}/kill`. A `force` query param opts past the dirty
guard. Add the operation to the OpenAPI spec + generated client.

### Frontend

In `SessionsBoard.tsx`'s Done / Terminated bar:

- A trash affordance on each done chip → confirm dialog → `DELETE`.
- A **"Clear all"** control on the bar header that deletes the whole done bucket
  in one confirmed action (dialog states the count and that branches are kept).
- On a dirty-worktree refusal, the dialog explains uncommitted work will be lost
  and offers force.

## Settings & defaults

A global setting in `GlobalSettingsForm`:

- **"Auto-reclaim finished sessions"** — toggle, **default ON**.
- **Grace period (minutes)** — numeric, **default 15**. Long enough for a quick
  follow-up or inspection after merge; short enough to reclaim.

Stored in the existing global settings store. The sweeper reads the current value
each tick; setting off makes every tick a no-op.

## Testing

**Backend**
- `Reclaim`: clean worktree → teardown, branch ref still present, record
  terminated, restore marker deleted; dirty worktree → skipped (`freed=false`),
  worktree intact.
- Sweeper `Tick`: grace not elapsed → no teardown; elapsed + worktree present →
  reclaim; no handle & no worktree → skip (no-op); setting off → skip; status not
  terminal → tracked timestamp cleared.
- `PurgeSession`: terminal → row + dependents gone, branch ref still present;
  non-terminal → `ErrNotTerminal`; dirty + no force → `ErrWorkspaceDirty`; dirty +
  force → deleted.
- Post-reclaim `Restore` still relaunches from the branch.

**Frontend**
- `SessionsBoard.test.tsx`: delete chip + confirm calls `DELETE`; Clear-all
  deletes the bucket; a reclaimed merged session still renders a "merged" badge;
  dirty-refusal surfaces the force path.

## Out of scope / risks

- **Idle-hibernate** (auto-reclaiming sessions that are still active/idle but not
  done) is intentionally deferred to a separate feature.
- **Orchestrator lifecycle** is untouched.
- **In-memory grace clock** resets on daemon restart (accepted, see above).
