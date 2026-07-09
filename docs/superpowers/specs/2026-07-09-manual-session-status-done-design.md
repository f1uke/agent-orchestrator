# Manual session status control (Jira-style "Move to Done") — design proposal

**Status:** proposal (awaiting human confirmation — no code written yet)
**Date:** 2026-07-09
**Scope:** let the user manually move a board session to **Done**, which *terminates*
the session (kills runtime + reclaims worktree), and back out again (Reopen).

---

## TL;DR

- AO **never stores display status** — it's derived on every read from durable facts
  (`deriveStatusDetail`, `status.go:66`). The manual-vs-auto "crux" dissolves once you
  see this: **"Done" is already a durable fact — `is_terminated`** — and the derivation
  **short-circuits on it before any auto rule runs** (`status.go:67-72`). So a manual
  Done can *never* be flipped back to `needs_input` by `idle_aged`. **No new "pin"
  column, no override state machine, no migration.**
- Everything the backend needs already exists: **`POST /sessions/{id}/kill`** (terminate
  + reclaim worktree, keep branch, restorable), **`POST /sessions/{id}/restore`** (Reopen),
  **`DELETE /sessions/{id}`** (permanent). The done-bar already wires Reopen + Delete.
- **The gap is UI-only:** active board cards have *no action affordance* today (click =
  open). Add a **⋯ menu on the card** whose headline item is **Move to Done → `/kill`**.
- **Recommendation:** the two manual transitions with real, non-stale semantics are
  **Done (terminate)** and **Reopen (restore)**. Keep the intermediate lanes
  (Working / Needs you / In review / Ready to merge) **auto-derived** — manually pinning
  them would fight the live signal and go stale, violating the architecture's core
  invariant. This is a **frontend-only** change.

---

## (a) Status model + manual-vs-auto approach

### How status works today (grounded)

`Session.Status` is **derived, never persisted** (`domain/session.go:73-77`,
`domain/status.go:3-5`). `deriveStatusDetail` (`service/session/status.go:66`) reduces
durable facts → one display status, in this precedence:

1. `rec.IsTerminated` → `merged` (if any PR merged) else `terminated` — **first, wins over everything** (`status.go:67-72`)
2. `Activity == WaitingInput` → `needs_input` (agent asked) (`status.go:74`)
3. open PRs → worst-wins PR aggregate → `pr_open`/`ci_failed`/`mergeable`/… (`status.go:78-81`)
4. `Reactivated` → `working` or `needs_input` (`status.go:87-98`)
5. any merged PR → `merged` (`status.go:99`)
6. `Activity == Active` → `working`, or `needs_input`/`active_stale` if aged (`status.go:103-117`)
7. `Activity == Idle` aged past grace → `needs_input`/**`idle_aged`** (`status.go:119-122`)
8. `no_signal`, then fresh `idle` (`status.go:124-131`)

The board maps that status → one of five kanban lanes via `attentionZone`
(`types/workspace.ts:432`): **Working · Needs you · In review · Ready to merge · Done**
(`terminated`/`merged` → `done`, `workspace.ts:435-437`).

### The crux (decision #3), resolved

> *"A manual 'Done' must NOT get flipped back to needs_input by idle_aged."*

**It structurally can't, because Done = terminate.** `Kill` records terminal intent via
`MarkTerminated` (`lifecycle/manager.go:276`), setting the persisted `is_terminated=true`.
Rule **1** returns `terminated`/`merged` **before** rules 6–8 (`active_stale`,
`idle_aged`, `no_signal`) can ever run — those only apply to non-terminated sessions.
So **the terminate bit *is* the pin.** No parallel `manual_status` field is needed.

Bonus properties this buys for free:
- **Survives daemon restart** — it's a DB column, not in-memory state. Re-derivation on
  boot reads the same fact → still Done.
- **Reversible** — Reopen (`/restore`) revives the row; `MarkSpawned` sets `Reactivated`
  (`lifecycle/manager.go:263`) so it returns as `needs_input` in the "Needs you" lane
  (`status.go:87-98`), not pinned to a stale Done.
- **Auto-derive resumes naturally** on reopen: the session is live again and every rule
  applies as normal.

### Why NOT a general stored override (the alternative I reject)

A "true Jira" version would add a nullable `manual_status` column consulted by
`deriveStatusDetail`. I recommend **against** it:

- The intermediate lanes are derived from **live, continuously-refreshed** signals
  (activity heartbeats; PR/CI/review facts). A manual "Working" pin on a session that's
  actually idle is exactly the **lie the derived-status design exists to prevent**.
- **"In review" is definitionally a PR-facts status** — you can't be in review without a
  PR, and the board already moves the card there the instant a PR is observed. There is
  nothing to pin.
- It forces a hard, bug-prone policy question: **"when may auto reclaim control?"** (clear
  on new PR? on a `waiting_input` hook? on the next active signal? on a timer?) — plus a
  migration, CDC surface, and a break of the "never store display status" invariant.
- **High complexity + staleness, low value.** The only genuinely useful manual transitions
  — Done and Reopen — need none of it.

**Net:** manual control = **Done (forward) + Reopen (reverse)**. Lanes 1–4 stay derived.
This honors decision #2 (map to the existing lifecycle; invent no taxonomy) and #3.

---

## (b) Backend changes — **none required**

Everything is already built and battle-tested; I cite it so review can confirm reuse:

| Capability | Endpoint | Service → Manager | Notes |
|---|---|---|---|
| **Done = terminate + reclaim worktree** | `POST /api/v1/sessions/{id}/kill` (`controllers/sessions.go:84`, handler `sessions.go` `kill`) | `Service.Kill` (`service.go:405`) → `Manager.Kill` (`session_manager/manager.go:523`) | Marks terminated, deletes restore marker, destroys runtime, destroys worktree. **Keeps the git branch.** Dirty worktree is **preserved** (returns `freed=false`, `manager.go:552-554`) — never force-discarded. |
| **Reopen (reverse Done)** | `POST /api/v1/sessions/{id}/restore` (`sessions.go:82`) | `Service.Restore` (`service.go:385`) → `Manager.Restore` (`manager.go:662`) | Relaunches; `Reactivated=true` → returns as `needs_input`. `SESSION_NOT_RESTORABLE` for a still-live merged session is a **benign no-op** the UI already handles (`SessionsBoard.tsx:313`). |
| **Permanent delete (from Done)** | `DELETE /api/v1/sessions/{id}?force=` (`sessions.go:74`) | `Service.Delete` (`service.go:459`) → `Manager.PurgeSession` (`manager.go:568`) | Gated on terminal status; refuses dirty worktree unless `force`. |

The `KillSessionResponse{ OK, SessionID, Freed }` (`controllers/dto.go:221`) already
returns `freed` so the UI can tell "worktree reclaimed" from "worktree preserved
(uncommitted work)". No DTO/schema/openapi change.

> Optional (only if we later want intermediate manual pins → the rejected Approach B):
> a `0xxx_add_session_manual_status.sql` migration + a field on `SessionRecord` +
> a branch in `deriveStatusDetail`. **Not in this plan.**

---

## (c) UI/UX design (DESIGN.md-compliant)

**Design constraints honored:** renderer clones agent-orchestrator; build from shadcn
primitives (`components/ui/dropdown-menu.tsx` **exists**); lucide icons, no emoji; the
established session-action idiom is the topbar **⋯ menu (rename/restart/kill/claim)**
(DESIGN.md:224). So the on-design choice is a **card-level ⋯ overflow menu**, not drag.

### Why menu, not drag-between-columns

- The board is a CSS-grid of **derived** lanes; cards are **click-to-open**. Drag breaks
  that model, needs a DnD lib + a real a11y lift.
- **Only one drop target has meaning** (→ Done). The other lanes are derived and can't
  accept a manual drop — a "drop anywhere" affordance would mislead.
- The human said **"buttons/menu"** — a menu is explicitly sanctioned and matches the
  existing idiom. (Drag remains a possible future polish, not v1.)

### Sketch

```
 ┌ Working ────────────┐   card with hover/focus-revealed ⋯ menu:
 │ ● Working      Claude ⋯│      ┌───────────────────────────┐
 │ Investigate flaky test │      │  Move to Done             │   ← headline, destructive tint
 │ feature/flaky-probe    │      │  ─────────────────────    │
 │ ─────────────────────  │      │  Rename            (opt)  │   ← existing Service.Rename
 │ no PR yet              │      │  Restart           (opt)  │   ← existing Service.Restart
 └────────────────────────┘      └───────────────────────────┘

 Clicking "Move to Done" arms an inline confirm on the card (reuse TopbarKillButton):
 ┌ Working ────────────┐
 │ ● Working      Claude ⋯│
 │ Investigate flaky test │
 │ [■ Confirm — stops agent, frees worktree]  [Cancel]        │
 └────────────────────────┘
        │ POST /sessions/{id}/kill → invalidate workspaceQuery
        ▼
 card animates out of "Working" into the collapsed  ▸ Done / Terminated  bar,
 where the existing DoneChip already offers  ⟳ Reopen  and  🗑 Delete.
```

- **Placement:** the ⋯ trigger sits in the card header's right slot next to the
  `agentLabel` (`SessionsBoard.tsx:583`), revealed on hover **and** keyboard focus
  (a11y). `MoreHorizontal` (lucide). `onClick` stops propagation so it doesn't open the
  session.
- **Confirm:** inline arm-confirm (reuse the exact pattern in `TopbarKillButton`,
  `ShellTopbar.tsx:248-317`), not a modal — lighter, matches the idiom, and Done is
  reversible via Reopen so heavy friction isn't warranted.
- **Confirm copy** makes the semantics explicit: *"Stops the agent and frees the worktree
  (the branch is kept). Any open PR stays open. Reversible via Reopen."*
- **Workers only:** the board renders only `workerSessions` (`SessionsBoard.tsx:89`), so
  the menu never appears on an orchestrator — matching the topbar, which hides Kill for
  orchestrators (`ShellTopbar.tsx:198`).
- **Reopen** needs no new UI — it already lives on the `DoneChip` (`SessionsBoard.tsx:307`).

---

## (d) Edge cases

1. **Reverse a Done / un-terminate** — supported today: DoneChip Reopen → `/restore` →
   `Reactivated` → returns as `needs_input` (`status.go:87-98`). Merged-but-live →
   `SESSION_NOT_RESTORABLE` handled as no-op (`SessionsBoard.tsx:313`). **No new work.**
2. **Dirty worktree on Done** — `Kill` **never** force-removes it (`manager.go:552-554`):
   the session still terminates (`is_terminated=true`, card moves to Done) but returns
   `freed=false`, **preserving uncommitted work on disk**. Surface a subtle *"worktree
   kept — uncommitted changes"* note from `freed=false`. Permanent Delete later still
   refuses dirty unless forced (existing "Delete anyway", `SessionsBoard.tsx:419`). **No
   data-loss path.**
3. **Open PR moved to Done** — `Kill` tears down runtime + worktree but makes **no SCM
   call**, so the PR stays open on the remote. `anyMerged` is false → status reads
   `terminated` (not `merged`). Correct semantic for "abandon the session, leave the PR
   for a human." Confirm copy says so. On Reopen, the SCM observer re-claims the open PR
   on the branch and re-derives an active PR lane (documented at `SessionsBoard.tsx:299-306`).
4. **Survives daemon restart** — Done is the persisted `is_terminated` column; boot
   re-derivation reads the same fact → still Done. (A hypothetical in-memory pin would be
   lost — another reason the no-new-pin design is correct.)
5. **Auto-reclaim race** — the reclaim loop (`ListReclaimable`/`Reclaim`,
   `service.go:425-455`) already targets terminated/merged sessions; a manual Done is
   consistent with it and `Kill`/`MarkTerminated` are idempotent (`manager.go:277-284`).
6. **Double-click / already-terminated** — `Kill` on a gone/terminated session is a
   benign no-op (`manager.go:528-529`, `MarkTerminated` skip). Safe.

---

## (e) Implementation plan (frontend-only)

1. **`SessionCardMenu`** — add a shadcn `DropdownMenu` to `SessionCard`
   (`SessionsBoard.tsx:555`), trigger in the header right slot, hover/focus-revealed,
   keyboard-accessible, `stopPropagation` on the trigger.
2. **"Move to Done"** item → inline arm-confirm → `POST /api/v1/sessions/{id}/kill` →
   `invalidateQueries(workspaceQueryKey)`. Handle `freed=false` → "worktree kept" note.
   **Extract the shared logic** from `TopbarKillButton` into a small
   `useKillSession(session)` hook so topbar + card menu don't duplicate the mutation.
3. **Confirm copy** per (c). Keep telemetry (`ao.renderer.session_kill_*`,
   `ShellTopbar.tsx:255-267`) or add a board-scoped variant.
4. **(v1 minimal)** ship the menu with just **Move to Done**; optionally fold in the
   existing **Rename**/**Restart** actions (open question #2).
5. **Reopen** — no change (DoneChip already covers it); verify the round-trip in tests.
6. **Tests** — extend `SessionsBoard.test.tsx`: menu opens; Move to Done arms confirm and
   fires `/kill`; card leaves the active lane; `freed=false` shows the preserved-worktree
   note. `useKillSession` unit test. (Reopen path already covered by DoneChip tests.)
7. **Demo** — `ao preview` from inside the session so the board change renders in the
   desktop browser panel (CLAUDE.md requirement).

No backend, DTO, openapi, or migration changes in the recommended plan.

---

## (f) Open questions for the human

1. **Menu vs drag** — I recommend the ⋯ card menu (on-design, sanctioned by "buttons/menu",
   low risk). Confirm you don't specifically want drag-between-columns (bigger lift, weaker
   semantics since only → Done is meaningful).
2. **Card menu scope** — v1 = just **Move to Done**, or also surface **Rename/Restart**
   (both already exist as services) to make the card menu the board's full session menu?
3. **"Snooze / dismiss Needs-you"** — the *one* intermediate manual gesture with arguable
   value: quiet a session that's nagging in the Needs-you lane **without** terminating it.
   This is notification-noise, **not** a status pin (so it doesn't reopen the derived-status
   can of worms). In scope, or defer?
4. **Confirm friction** — inline arm-confirm (recommended; matches `TopbarKillButton`,
   reversible via Reopen) vs a full modal dialog?
