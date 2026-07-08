# View & edit AO's system prompts — design

**Date:** 2026-07-08
**Branch:** `feature/edit-system-prompts` (base + PR target: `main-fluke`)
**Status:** design, pending user review

## Goal

Let a user **view and edit** every standing system prompt AO emits, without ever
being able to strand a session. Two editable layers per prompt kind:

- **Global base** (per kind): the currently-hardcoded prompt becomes an editable
  global base, seeded with the built-in default, with **Reset-to-default**.
  Applies to all projects.
- **Per-project addition** (per kind): extra text appended on top of the global
  base for that project. Default empty.

Dynamic pieces (git convention, spawn-confirm gate, session/project ids, the AO
skill pointer) stay **automatic and non-editable**. A small **protected floor**
of non-negotiable invariants is always injected so editing or clearing a base can
never break AO's own coordination or the confidentiality guard.

## Enumeration — every prompt kind AO emits

Traced from `session_manager/manager.go:buildSystemPrompt` and `review/prompt.go`.

### Editable kinds (get global base + per-project addition)

| Kind | Built-in default source | Notes |
|---|---|---|
| **Orchestrator** | `orchestratorPrompt(project)` | Role/dispatcher text. Weaves the project id in via `%s` → becomes a `{{.ProjectID}}` placeholder (see "Project-id placeholder"). |
| **Worker** | `workerMultiPRPrompt()` | The PR/branch-namespace rules. Fully editable per the user decision; its invariant is re-guaranteed by the floor. |
| **Reviewer** | `review/prompt.go` `reviewTexts` `systemPrompt` | Standing reviewer role. Assembled in the review engine (has the worker's `ProjectID`). |

### Non-editable pieces (dynamic injections — automatic)

Structurally outside the editable base, so editing/clearing a base cannot remove
them. Composed as an **ordered list** appended after `base + addition + floor`.

| Injection | Source | Trigger |
|---|---|---|
| Worker → orchestrator `ao send` coordination | `workerOrchestratorPrompt(orchestratorID)` | only when an active orchestrator exists; carries a live session id |
| Orchestrator git convention | `orchestratorGitConventionPrompt()` (PR #24) | project sets a convention |
| Worker git convention | `workerGitConventionPrompt()` (PR #24) | project sets a convention |
| Spawn-confirm gate | `orchestratorSpawnConfirmPrompt()` (PR #25) | global spawn-confirm toggle ON |
| AO skill pointer | `aoSkillPointer()` | always (data-dir path) |
| Project id | woven into orchestrator base via placeholder substitution | always |

### Protected floor (always injected, non-editable)

Replaces the current single `systemPromptGuard` tail with a small per-kind block:

- **All kinds:** confidentiality guard (verbatim today's `systemPromptGuard` text).
- **Worker:** one-line non-negotiable — "keep every branch you create within your
  session's branch namespace so AO can attribute your pull requests."
- **Reviewer:** one-line non-negotiable — "review only; do not push commits, edit
  files, or modify the branch."

### Out of scope (one-shot task prompts, not standing system prompts)

Documented for completeness; **not** exposed. These are per-invocation task text,
not standing configuration:

- User task prompt — `buildPrompt(cfg)` (manager.go)
- Branch-naming prompt — `buildNamingPrompt` (session_manager/branchname.go)
- Tracker-intake issue prompt — `BuildIssuePrompt` (observe/trackerintake)
- Reviewer **task** text (which PR, submit command) — `review/prompt.go` `prompt`
  (only the reviewer **system** prompt is a standing kind)

## Layered assembly model

For each kind, the final system prompt is:

```
final = [ GLOBAL BASE : stored override else built-in default ]   (editable, Reset restores)
      + [ PER-PROJECT ADDITION : default empty ]                  (editable)
      + [ PROTECTED FLOOR : per-kind invariants ]                 (always injected, non-editable)
      + [ DYNAMIC INJECTIONS : ordered list ]                     (automatic, non-editable)
```

- The **built-in default** is preserved as a named constant/function so it can seed
  the editor and back Reset-to-default.
- The **global base** is stored as a per-kind override string; absent/empty ⇒ use
  the built-in default.
- The **per-project addition** is a per-kind string on the project config; empty ⇒
  nothing appended.
- The dynamic list is where PR #24 (git convention) and PR #25 (spawn-confirm)
  already inject; new injections compose by appending to the list.

### Project-id placeholder

`orchestratorPrompt` currently weaves the project id into its text via `fmt.Sprintf`.
Because ids are a **dynamic injection** (not user-authored), the stored/editable
orchestrator base default uses a documented placeholder token `{{.ProjectID}}` in
place of the woven id. Assembly substitutes the live project id. This keeps the
effective wording byte-identical to today, keeps the base fully editable, and keeps
the id out of user authorship. If a user deletes the placeholder, the id simply
doesn't appear (graceful, non-critical — `ao spawn --help` still lists `--project`).
The UI documents the placeholder next to the orchestrator editor.

### Reconciliation with PR #24 and #25 (concurrency)

`buildSystemPrompt` today (post-#25):

```go
case domain.KindOrchestrator:
    base = orchestratorPrompt(projectID) +
        orchestratorGitConventionPrompt(conv, cfg.DefaultBranch) +
        orchestratorSpawnConfirmPrompt(m.confirmBeforeSpawn(), conv, cfg.DefaultBranch)
```

The refactor turns the **first term** (`orchestratorPrompt(projectID)`) into
`effectiveBase(orchestrator) + perProjectAddition(orchestrator) + floor(orchestrator)`
and leaves the git-convention + spawn-confirm terms as the dynamic tail. So the
confirm-gate injection is **preserved as a dynamic injection — not dropped, not
duplicated**. Same treatment for the worker branch.

## Storage

Two stores, both under `~/.ao` only (hard rule).

### Global base overrides — new `promptoverrides` package

Mirrors `spawnconfirm`/`reclaimsettings`: a mutex-guarded, atomic-write JSON file
`system-prompt-overrides.json` under the data dir. Missing/corrupt ⇒ defaults
(no overrides), so the daemon always boots.

```go
package promptoverrides

type Kind string // "orchestrator" | "worker" | "reviewer"

// Overrides holds a custom global base per kind. Absent key ⇒ built-in default.
type Overrides struct {
    Base map[Kind]string `json:"base,omitempty"`
}

func NewStore(dir string) (*Store, error)
func (s *Store) Get() Overrides                 // full snapshot (session manager + review engine read this)
func (s *Store) SetBase(kind Kind, text string) error
func (s *Store) ClearBase(kind Kind) error      // Reset-to-default
```

Built-in defaults live in one place (a `prompts` accessor or exported functions
in `session_manager`) so both the assembler and the API GET can read them.

### Per-project additions — extend `ProjectConfig`

New typed field on `domain.ProjectConfig` (typed, no free-form map, per house style):

```go
// SystemPromptAdditions is per-kind text appended on top of the global base for
// this project's sessions. Empty fields append nothing.
type SystemPromptAdditions struct {
    Orchestrator string `json:"orchestrator,omitempty"`
    Worker       string `json:"worker,omitempty"`
    Reviewer     string `json:"reviewer,omitempty"`
}

// on ProjectConfig:
SystemPromptAdditions SystemPromptAdditions `json:"systemPromptAdditions,omitempty"`
```

Persisted through the existing project-config JSON blob and the existing
project-config write path — no new migration.

## API

Extend the existing `SettingsController` (same file as reclaim/spawn-confirm),
new routes under `/api/v1/settings/prompts`:

- `GET /api/v1/settings/prompts` → per kind: `{ kind, default, override|null }`.
  `default` seeds the editor placeholder / Reset; `override` is the current custom
  base (null ⇒ using default).
- `PUT /api/v1/settings/prompts/{kind}` `{ base: string }` → set the override.
- `DELETE /api/v1/settings/prompts/{kind}` → clear the override (Reset-to-default).

DTOs in `controllers/dto.go`; operations registered in
`apispec/specgen/build.go`; `openapi.yaml` + `frontend/src/api/schema.ts`
regenerated via `npm run api`.

Per-project additions ride the **existing** project-config GET/PUT (the field is
part of `ProjectConfig`); no new endpoint. Regenerate types after the DTO changes.

## Frontend

Renderer clones the agent-orchestrator web app (DESIGN.md); build from shadcn
primitives (`components/ui/*`).

- **Global settings** — new `SystemPromptsSection` card in `GlobalSettingsForm.tsx`
  (alongside AutoReclaim, SpawnConfirm, Updates, Migration). One editable
  `<textarea>` per kind, prefilled with the effective base (override else default),
  **Edit** (PUT) and **Reset to default** (DELETE, disabled when no override).
  Uses `apiClient.GET/PUT/DELETE("/api/v1/settings/prompts...")` like
  `AutoReclaimSection`/`SpawnConfirmSection`. Orchestrator editor documents the
  `{{.ProjectID}}` placeholder. A read-only note lists the protected floor +
  dynamic injections so the user sees they exist and are managed by AO.
- **Per-project settings** — in `ProjectSettingsForm.tsx`, a per-kind
  "Additional prompt (appended on top of the global base)" `<textarea>` for
  orchestrator, worker, and reviewer. Empty default. Saved through the existing
  project-config mutation.

## Assembly wiring

- `Manager` gains a `promptOverrides func() promptoverrides.Overrides` dep
  (nil-safe, like `spawnConfirmEnabled`), wired in `daemon.go` from the new store.
- `buildSystemPrompt` reads the overrides snapshot + the project's
  `SystemPromptAdditions` and composes `base + addition + floor + dynamic` per kind.
- Reviewer: thread `ProjectID` into `review.LaunchSpec`; the review engine (already
  loads the worker's project) assembles `effectiveReviewerBase + projectAddition`
  and passes it into `reviewTexts`. The reviewer floor ("review only" +
  confidentiality) stays hardcoded in `reviewTexts`.

## Safety boundary (for the PR writeup)

Editing can never strand a session because the load-bearing pieces live **outside**
the editable base:

1. **Dynamic injections** (git convention, spawn-confirm, worker→orchestrator
   `ao send`, skill pointer, ids) are appended by AO after the base; a user editing
   or clearing a base cannot touch them.
2. **Protected floor** re-injects the irreducible invariants regardless of base
   content: PR-namespace attribution (worker), review-only (reviewer), and the
   confidentiality guard (all). The user chose "editable base + re-injected floor,"
   so the worker's namespace rules are fully editable in the base **and** a
   condensed reminder is force-injected by the floor. Minor intentional overlap at
   default is the accepted tradeoff for "edit everything + stay safe."
3. **Reset-to-default** clears the override, fully restoring the built-in default.

## Testing

- **Assembly (table tests)** in `session_manager`: default vs. override vs.
  +per-project addition vs. +floor vs. +dynamic (git convention, spawn-confirm),
  for each kind; project-id placeholder substitution; empty-override ⇒ default;
  cleared base still contains floor invariants + dynamic injections.
- **Stores**: `promptoverrides` persistence/reload/clear; `ProjectConfig`
  round-trip incl. `SystemPromptAdditions`; validation.
- **Controllers**: GET/PUT/DELETE `/settings/prompts` incl. unknown-kind and the
  nil-service 501 path (match existing settings_test.go).
- **Reviewer**: `reviewTexts`/engine composes base override + project addition +
  hardcoded floor.
- **Frontend**: `SystemPromptsSection` (prefill, edit, reset-disabled-when-default)
  and `ProjectSettingsForm` additions, in the style of the existing tests.
- Run `go test ./...` for touched packages, `npm run frontend:typecheck` +
  frontend test. Revert `routeTree.gen.ts` / `pnpm-lock.yaml` churn.

## End-to-end verification (`ao preview`)

Edit a global base and confirm the effective prompt changes; add a per-project
addition and confirm the effective prompt shows **both** the edited base and the
addition **plus** the dynamic injections (git convention, spawn-confirm) and the
floor; Reset-to-default and confirm the built-in default is fully restored.

## Non-goals

- No CLI for global prompt overrides (matches reclaim/spawn-confirm: API + UI
  only). Per-project additions ride the existing project-config surface.
- No versioning/history of edits, no per-session one-off overrides.
- No exposure of the out-of-scope one-shot task prompts.
