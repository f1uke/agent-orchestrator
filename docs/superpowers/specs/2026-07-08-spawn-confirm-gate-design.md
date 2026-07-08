# Orchestrator "confirm before spawning a worker" gate — design

Status: approved (product owner, 2026-07-08)
Merge target: `main-fluke`

## Problem

The orchestrator spawns workers with `ao spawn` the moment a task is clear. The
product owner wants an optional gate: **before** running `ao spawn`, the
orchestrator should present a confirmation summary in chat and wait for explicit
human approval, then spawn only after the user confirms. A single global setting
toggles the gate on or off; default **on**.

The summary must show, per the requirement:

- **Task** — a short description of what the worker will do.
- **Source branch** — the `--from` base branch.
- **New branch** — the branch that will be created, carrying the project's
  configured convention prefix (`feature/<topic>`) from the git-convention
  feature (PR #24).
- **PR target** — the branch the worker's pull request merges into.

This is a **conversational** confirmation (the orchestrator asks in chat and
waits), not a native UI modal.

## What already exists (context — reuse, do not reinvent)

- **Git branching convention (PR #24).** `backend/internal/domain/gitconvention.go`
  models `GitConventionConfig` (none / gitflow / custom + branch prefix).
  `manager.go buildSystemPrompt` already injects `orchestratorGitConventionPrompt(conv, cfg.DefaultBranch)`
  into the orchestrator prompt, telling it the branch prefix, base branch, and PR
  target. **The confirm-gate text reuses this**: base + PR target are the
  project's existing `DefaultBranch`; the "New branch" line references the
  convention section already injected above it. No branch-naming logic is
  duplicated.
- **Global daemon-backed setting precedent: auto-reclaim.**
  `backend/internal/reclaimsettings/settings.go` is a file-backed `Store`
  (`Get`/`Set`/`Default`) persisted as one JSON file under `~/.ao`. It is exposed
  via `controllers/settings.go` at `GET/PUT /api/v1/settings/reclaim`
  (DTOs in `dto.go`, OpenAPI in `specgen/build.go settingsOperations()`), and
  edited from the `AutoReclaimSection` card in `GlobalSettingsForm.tsx`. The
  spawn-confirm setting mirrors this pattern part for part.
- **Prompt assembly.** `manager.go buildSystemPrompt(ctx, kind, projectID)` builds
  the orchestrator prompt and is re-run on every spawn **and** restore, so a
  restarted/newly-spawned orchestrator always reflects the current setting value.

## Decisions (product owner, 2026-07-08)

- **Scope: global.** One app-wide toggle in Global Settings, not per-project. A
  global daemon-backed setting is well-supported (reclaim precedent) and the gate
  is a global UX behavior, not a per-repo property.
- **Default: ON** (confirm before spawning).
- **OFF injects nothing.** When the gate is off, no confirm section is added; the
  base orchestrator prompt already directs it to hand tasks to a worker via
  `ao spawn`. There is no separate "spawn directly" paragraph.

## Config model — global settings store

New package `backend/internal/spawnconfirm`, a verbatim structural clone of
`reclaimsettings`:

```go
type Settings struct {
    Enabled bool `json:"enabled"`
}

func Default() Settings { return Settings{Enabled: true} } // ON

type Store struct { /* path, mu, cur — same as reclaimsettings.Store */ }
func NewStore(dir string) (*Store, error)  // loads dir/spawn-confirm-settings.json, degrades to Default()
func (s *Store) Get() Settings
func (s *Store) Set(next Settings) error    // atomic temp+rename write
```

- File: `~/.ao/spawn-confirm-settings.json` (honors `AO_DATA_DIR`).
- Missing/corrupt file → `Default()` (Enabled = true), so the daemon always boots
  and the safe default is "confirm".
- No numeric validation needed (single bool); `Set` just persists.

## Manager wiring (prompt assembly)

`buildSystemPrompt` is a `*Manager` method, but the Manager currently receives no
settings store. Add a getter dependency:

```go
// sessionmanager.Deps
// SpawnConfirmEnabled reports whether the orchestrator must confirm before
// spawning. Nil defaults to enabled (safe default: confirm).
SpawnConfirmEnabled func() bool
```

Stored on the Manager; a small wrapper `m.spawnConfirmEnabled()` returns `true`
when the getter is nil (keeps existing tests that build a bare Manager on the
"confirm" default, and is the safe default).

In `buildSystemPrompt`'s orchestrator branch:

```go
base = orchestratorPrompt(projectID) +
    orchestratorGitConventionPrompt(conv, cfg.DefaultBranch) +
    orchestratorSpawnConfirmPrompt(m.spawnConfirmEnabled(), conv, cfg.DefaultBranch)
```

Worker prompts are unchanged — the gate is purely orchestrator behavior.

### `orchestratorSpawnConfirmPrompt(enabled, conv, baseBranch)`

- **enabled == false → return "".** (Decision: OFF injects nothing.)
- **enabled == true →** a "Confirm before spawning" section:

```
## Confirm before spawning

Before you run `ao spawn`, present a short confirmation summary to the human and
wait for their explicit approval. Do NOT spawn until they confirm. The summary
must list:
- **Task** — one line on what the worker will do
- **Source branch** — the `--from` base branch (default `<baseBranch>`)
- **New branch** — the branch that will be created<, following the git branch
  convention above (e.g. `feature/<topic>`)>        ← convention clause only when conv.Active()
- **PR target** — where the worker's pull request will merge (`<baseBranch>`)

If the human asks for changes, revise and re-confirm. Run `ao spawn` only after
they approve. This confirmation is conversational — ask in chat and wait; there
is no separate UI dialog.
```

- `<baseBranch>` is the project's `DefaultBranch` (reused, same value the
  convention section uses).
- The bracketed convention clause is included only when `conv.Active()`; with no
  convention the "New branch" line stays generic. This is the single point of
  reuse with PR #24: the prefix itself is described by the convention section
  right above, so the confirm text points at it rather than repeating the prefix
  rules.

The whole section is placed **before** `aoSkillPointer` + `systemPromptGuard`,
same as the convention section (it is standing configuration covered by the
confidentiality guard).

## REST surface

Extend the existing `SettingsController` (do not add a new controller):

- Add `SpawnConfirm SpawnConfirmService` (an interface satisfied by
  `*spawnconfirm.Store`) alongside the existing reclaim `Svc`.
- Routes `GET/PUT /api/v1/settings/spawn-confirm`, nil-service → OpenAPI-backed
  501 (same guard as reclaim).
- DTOs in `dto.go`: `SpawnConfirmSettingsResponse{ Enabled bool }` and
  `SetSpawnConfirmSettingsRequest{ Enabled bool }`.
- `specgen/build.go`: add the two schema-name mappings and two operations in
  `settingsOperations()`; regenerate with `npm run api` (commits `openapi.yaml`
  + `schema.ts`).

## Daemon wiring

- Construct the spawn-confirm store **before** `startSession` in `daemon.go`
  (the Manager needs its getter), then pass a getter closing over the store
  (`func() bool { return store.Get().Enabled }`) into `startSession` →
  `sessionmanager.Deps{SpawnConfirmEnabled: ...}`, and pass the store to
  `httpd.APIDeps` for the `SettingsController`.
- `startSession` gains one parameter (`spawnConfirmEnabled func() bool`) threaded
  into the Manager deps.
- A store-construction failure is cleaned up the same way the reclaim-store
  failure is.

## Frontend

New `SpawnConfirmSection.tsx`, a clone of `AutoReclaimSection.tsx`:

- shadcn `Card` + `Select` (Enabled / Disabled), title
  **"Confirm before spawning workers"**, a one-line description of the gate.
- `useQuery`/`useMutation` against `apiClient.GET/PUT("/api/v1/settings/spawn-confirm")`,
  query key `["settings", "spawnConfirm"]`, Save button + saved/error affordances
  — identical mechanics to `AutoReclaimSection`.
- Added to `GlobalSettingsForm.tsx` as the first card (above `AutoReclaimSection`).

## Known limitation (documented, not fixed)

A **live** orchestrator keeps its launch-time system prompt until it is restarted
or re-spawned — toggling the setting mid-session does not rewrite a running
agent's prompt. This matches the git-convention feature's behavior and every
other prompt-affecting setting. Noted in the PR.

## Tests

- **Go**
  - `spawnconfirm/settings_test.go`: `Default()` is Enabled=true; `Set`→`Get`
    round-trip; persistence to file across a reopened store; missing/corrupt file
    → `Default()`.
  - `manager_test.go TestSystemPrompt_SpawnConfirm`: ON → orchestrator prompt
    contains the "Confirm before spawning" section and names Source/New/PR-target;
    the convention clause appears when a convention is active and is absent
    otherwise; OFF → the section is absent; worker prompt is unaffected in both
    states.
  - `controllers/settings_test.go`: GET returns the store value; PUT persists and
    echoes it; nil service → 501.
- **Frontend**
  - `SpawnConfirmSection.test.tsx`: renders with the fetched value, toggling the
    Select and saving PUTs `{ enabled: ... }`, error surfaces on failure — mirror
    of `AutoReclaimSection.test.tsx`.
- Run `go test ./...` for the touched backend packages; `npm run test` +
  `npm run typecheck` in `frontend/`. Revert `routeTree.gen.ts` / `pnpm-lock.yaml`
  churn.

## Verification

Build/run the daemon + renderer, open **Global settings**, toggle
**Confirm before spawning workers**, and confirm the value persists
(`~/.ao/spawn-confirm-settings.json`). Demo the card via `ao preview`. In the PR:
name the prompt-injection point (`buildSystemPrompt` →
`orchestratorSpawnConfirmPrompt`), the toggle wiring (store → Manager getter +
`SettingsController`), and paste the exact confirmation-summary text the
orchestrator produces.

## Scope guard (YAGNI)

No CLI flag for this setting (reclaim has none); no per-project override; no
change to worker prompts; the confirm text reuses PR #24's convention section
rather than duplicating branch-naming rules.
