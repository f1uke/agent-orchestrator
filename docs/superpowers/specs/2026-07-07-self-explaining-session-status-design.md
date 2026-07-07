# Self-Explaining Session Status (reason-first)

- **Date:** 2026-07-07
- **Branch:** `worktree-needs-input-detection` (based on `main-fluke`)
- **Status:** Approved design — ready for implementation planning
- **Scope:** Phase 1 of improving the "working" vs "needs you" split. Observation-only.

## Problem

The app decides whether an agent session is **working** or **needs you** by deriving a
display status from durable facts at read time (`deriveStatus`,
`backend/internal/service/session/status.go`). The derivation is a pure function whose
decision — *which branch fired* — is discarded the moment it returns. As a result there
is **no way to see why a status pill flipped**.

The observed pain (confirmed with the user):

1. **False "needs you" while the agent is actually still working** is the main complaint.
2. **It's generally noisy and not reproducible** — the user cannot point at a specific
   transition, and there is no visibility to diagnose which one misfires.

Root-cause context: a false "needs you" while working can only come from a **timeout
branch** — `active` aging past `activeStaleGrace` (15m), or `idle` aging past
`waitingInputGrace` (45s) — both promote to `needs_input`. A real agent prompt comes from
a `waiting_input` activity state (a Claude `Notification` hook). Today these look identical
in the UI, so a timeout-driven false positive is indistinguishable from a genuine prompt.

### Prior art comparison (cmux)

The user believed cmux (`github.com/manaflow-ai/cmux`) detects this better. Investigation
found cmux uses the **same mechanism we do** — agent lifecycle hooks
(`UserPromptSubmit`→working, `Stop`→idle, `Notification`→needs-input), delivered over a
Unix socket instead of HTTP. It does **not** scrape the terminal/PTY. It already reads
`notification_type` (`permission_prompt` vs `idle_prompt`) — and **so do we**
(`backend/internal/adapters/agent/claudecode/activity.go:59`).

Where cmux is ahead: background-work modeling (stay "running" while a background task is
live) and a 3-way semantic split (idle-"done" distinct from needs-input). Where **we are
ahead**: stale-active aging so a lost `Stop` cannot pin "working" forever — cmux's *open*
issue #3749 asks for exactly what our PR #5 already shipped.

Conclusion: we are already ~90% aligned with cmux and ahead on the "stuck on working"
problem. Blindly porting more cmux behavior would be guessing. The right first move is
**visibility**, so the real misfire can be observed before any behavior change.

## Goal

Make the status derivation **self-explaining**: for every session, expose a machine-readable
**reason code** for the deciding branch, and — where a timeout is pending — a **countdown**
to the next flip. Surface both in the Session Inspector and `ao session get`.

## Non-goals (Phase 1)

- **No behavior change.** No status value changes; no transition timing changes. Every
  existing `status_test.go` case must still pass unchanged. This is observation-only.
- **No new persistence / migration.** Reason and countdown are pure read-time derivations
  from columns that already exist.
- **No permission-vs-idle-prompt sub-split / "last hook fired."** The original Claude event
  name and `notification_type` are discarded client-side in the CLI
  (`backend/internal/cli/hooks.go:78`, `setActivityAPIRequest` carries only `state`) before
  the daemon sees them. Recovering them needs a migration + 4-layer plumbing. Deferred.
- **No durable history table** (the heavier "Approach B"). The `change_log` CDC table
  already streams state transitions if a timeline is wanted later.
- **No background-work modeling, timeout tuning, or 3-way semantic split.** Deferred until
  the reason data shows which misfire is real.

## Design

### Reason taxonomy

One reason code per `deriveStatus` return branch (`status.go:47-98`), 9 total:

| Reason code | Fires when | Meaning for diagnosis |
|---|---|---|
| `working` | `active`, `now - lastActivityAt <= 15m` | Genuinely busy; heartbeat fresh |
| `waiting_input` | `activity_state == waiting_input` | **Real** agent prompt (Notification hook) |
| `active_stale` | `active` aged past `activeStaleGrace` (15m) → `needs_input` | **Timeout guess** — likely false "needs you" while working |
| `idle_aged` | `idle` + has signalled + aged past `waitingInputGrace` (45s) → `needs_input` | **Timeout guess** — turn ended, promoted by clock |
| `idle` | fresh idle within grace, or hook-less harness | Recently active / normal quiet |
| `no_signal` | signal-capable, never signalled, silent past `noSignalGrace` (90s) | Hook pipeline may be broken |
| `pr_pipeline` | open-PR worst-wins aggregate produced the status | The `status` field itself names which (ci_failed/changes_requested/…) |
| `terminated` | terminated, no merged PR | — |
| `merged` | merged branch, or terminated with a merged PR | — |

**Key property:** a false "needs you" while working always carries reason `active_stale`
or `idle_aged` (timeout-driven), never `waiting_input` (real prompt). That single
distinction is the diagnostic the user is missing.

### Countdown

For the three states sitting on a pending timeout, also emit **`nextTransitionAt`** (absolute
timestamp, so the UI can tick locally and stay accurate between reads) and
**`nextTransitionTo`** (the status it will become):

| Current | `nextTransitionAt` | `nextTransitionTo` |
|---|---|---|
| `working` (active, within grace) | `lastActivityAt + 15m` | `needs_input` |
| fresh `idle`, has signalled | `lastActivityAt + 45s` | `needs_input` |
| fresh `idle`, never signalled (signal-capable) | `lastActivityAt + 90s` | `no_signal` |
| all others (waiting_input, active_stale, idle_aged, no_signal, pr_*, terminated, merged) | — (nil) | — |

Exactly one countdown row applies per session (a fresh idle is either signalled or not,
per `FirstSignalAt`). Computed against the injected clock `s.now()` (`service.go:597`) so
tests are deterministic.

### Backend (no migration)

- `backend/internal/service/session/status.go`: replace/extend `deriveStatus` with a
  variant that also returns `(reason StatusReason, nextAt *time.Time, nextTo SessionStatus)`.
  All inputs already available: `rec.Activity.State`, `rec.Activity.LastActivityAt`,
  `rec.FirstSignalAt`, and the existing unexported grace constants
  (`noSignalGrace`/`waitingInputGrace`/`activeStaleGrace`). Keep the existing
  `deriveStatus` signature/behavior intact or have it delegate, so no caller or test breaks.
- `backend/internal/domain/`: `type StatusReason string` + the 9 constants.
- `backend/internal/domain/session.go` (~line 70, adjacent to `Status`): three new
  **optional** flat fields on `domain.Session`:
  ```go
  StatusReason     StatusReason  `json:"statusReason,omitempty" enum:"working,waiting_input,active_stale,idle_aged,idle,no_signal,pr_pipeline,terminated,merged"`
  NextTransitionAt *time.Time    `json:"nextTransitionAt,omitempty"`
  NextTransitionTo SessionStatus `json:"nextTransitionTo,omitempty"`
  ```
  Flat fields (not a nested struct) so no new OpenAPI named-schema type is required.
- `backend/internal/service/session/service.go:592` (`toSession`): set the three fields in
  the returned `domain.Session` literal. `toSession` is the single choke point for the read
  path (`Get`/`List`/`Spawn`/`Restore`).
- API regen: `SessionView` (`controllers/dto.go:132`) embeds `domain.Session`, so the fields
  serialize automatically. Run `npm run api` to regenerate `openapi.yaml` +
  `frontend/src/api/schema.ts`. Verify the drift/spec-parity tests
  (`cd backend && go test ./internal/httpd/...`); if the `StatusReason` enum field needs a
  `schemaNames` entry in `apispec/specgen/build.go`, add it (the drift test flags this).

### CLI (`ao session get`)

Surface the reason (and countdown, if present) in `ao session get`. Add the fields to the
hand-mirrored `sessionDTO`/`sessionActivity` (`backend/internal/cli/session.go:42-59`) and
print them in the human view. Extra JSON fields are ignored by the other mirrors
(`sessionListEntry`, `spawnResult`), so only the `get` path needs touching. This is the
headless answer to "I can't tell why."

### Frontend (Inspector only)

- Thread the new fields from the generated API type through
  `frontend/src/renderer/hooks/useWorkspaceQuery.ts:60` → `WorkspaceSession`
  (`frontend/src/renderer/types/workspace.ts:115-159`) → the component.
- In `frontend/src/renderer/components/SessionInspector.tsx`, at the live "now" node of the
  Activity timeline (`:289-299`, where the activity pill + `no_signal` warning already
  render), add:
  - a muted **"why" caption** driven by a reason-code→sentence map, e.g.
    - `active_stale` → *"No signal for 16m — assumed waiting (turn's Stop hook may have been lost)"*
    - `idle_aged` → *"Turn ended, idle 48s — assumed waiting"*
    - `waiting_input` → *"Agent requested input"*
    - `working` → *"Working — heartbeat 2m ago"*
  - a **countdown** caption where `nextTransitionAt` is set, e.g. *"→ Needs input in 13m"*,
    ticking locally from the absolute timestamp.
- Reason labels that end in "assumed" make it explicit that a `needs_input` came from a
  timeout inference, not a real prompt.

### Data flow (end to end)

```
activity_state / lastActivityAt / firstSignalAt (already stored, unchanged)
        │
deriveStatusWithReason()  →  (status, reason, nextTransitionAt, nextTransitionTo)   [status.go]
        │
toSession() sets fields on domain.Session                                           [service.go:592]
        │
SessionView embeds domain.Session → openapi.yaml → schema.ts                        [dto.go:132 / npm run api]
        │
useWorkspaceQuery → WorkspaceSession → SessionInspector "why" + countdown           [frontend]
        └── ao session get (sessionDTO)                                             [cli/session.go]
```

## Testing

- **Backend (TDD, write first):** extend the table-driven `status_test.go` to assert
  `(status, reason, nextTransitionAt, nextTransitionTo)` for every branch — including the
  two timeout branches at/just-past their grace boundaries, the two fresh-idle countdown
  variants (signalled → 45s→needs_input; never-signalled → 90s→no_signal), the active
  countdown (→15m→needs_input), and the sticky/terminal branches (nil countdown). Assert the
  existing status outputs are unchanged.
- **API:** `go test ./internal/httpd/...` for spec drift / route-spec parity after regen.
- **Frontend:** a unit test for the reason-code→label map and countdown formatting **if** a
  frontend test harness exists (confirm during planning; do not invent one).
- **Manual verification:** `ao preview` / run the app, open the Inspector on a live session,
  confirm the "why" line and countdown match the pill, and that a timeout-driven `needs_input`
  reads differently from a real `waiting_input` prompt.

## Risks / decisions

- **Countdown is a read-time snapshot.** The status only actually flips on the next server
  read (SSE/CDC push or refetch); the UI counts down locally from `nextTransitionAt` but the
  pill won't change until re-derivation. Acceptable — the countdown is informational.
- **New enum field may need a `schemaNames` entry** in `build.go`; the drift test will catch
  it. Minor.
- **`StatusReason` value set is load-bearing** for the frontend. The API-level type reaches
  the frontend via the generated `schema.ts` (from `npm run api`), but the reason-code→label
  map in the renderer is **hand-maintained** and must cover all 9 codes with a safe fallback
  for any unmapped value, mirroring how `workspace.ts` already handles `unknown` status.

## Success criteria

1. Every session exposes a plain-language reason for its current status in the Inspector and
   `ao session get`.
2. Where a timeout is pending, a live countdown shows when and to what the status will flip.
3. A `needs_input` caused by a timeout (`active_stale` / `idle_aged`) is textually
   distinguishable from one caused by a real agent prompt (`waiting_input`).
4. No existing status value or transition timing changes — verified by the unchanged
   `status_test.go` assertions still passing.
