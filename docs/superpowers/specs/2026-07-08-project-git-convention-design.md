# Per-project git branching convention — design

Status: approved (product owner, 2026-07-08)
Merge target: `main-fluke`

## Problem

Agent Orchestrator should be aware of each project's git branching convention
(e.g. gitflow) so spawned worker branches get the right prefix
(`feature/…`, `bugfix/…`, `hotfix/…`) and workers follow the project's git flow.

Key constraint from the product owner: the **orchestrator** builds the `ao spawn`
command, so the convention **must** be injected into the orchestrator's system
prompt — so it passes an explicit `--branch <prefix>/<topic>` and tells the worker
which base branch / PR target / naming rules to follow. The convention is also
injected into the worker prompt so the worker independently follows it.

## What already exists (context)

- `ProjectConfig` (`backend/internal/domain/projectconfig.go`) is a typed struct
  with `WithDefaults()` / `Validate()` / `IsZero()`, reflected into OpenAPI
  (`DomainProjectConfig` → `ProjectConfig`) → `frontend/src/api/schema.ts` →
  CLI mirror `projectConfig` (`backend/internal/cli/project.go`) →
  `ProjectSettingsForm.tsx`. `TrackerIntakeConfig` is the nested-struct precedent.
- **The daemon's auto-namer already emits gitflow branch names.**
  `backend/internal/session_manager/branchname.go` asks the agent for
  `<type>/<JIRA-KEY>-<short-desc>` with `<type>` ∈ {feature, bugfix, hotfix, chore},
  preserves the Jira key casing, and de-dups. So a bare `feature/…` is already the
  default when `--branch` is omitted.
- Prompts are built in `backend/internal/session_manager/manager.go`:
  `buildSystemPrompt(kind, projectID)` → `orchestratorPrompt(projectID)` /
  `workerOrchestratorPrompt` / `workerMultiPRPrompt`.
- `DefaultBranch` on `ProjectConfig` already means "base branch new worktrees start
  from". `ao spawn` **requires** `--from`, so the orchestrator already chooses the
  base explicitly.

The delta is therefore smaller than it looks: make the prefix **configured &
explicit** (not just AI-inferred), **inject it into the orchestrator prompt**, and
**force it in auto-naming** for the custom case.

## Config model

New nested type on `ProjectConfig`, mirroring `TrackerIntakeConfig`:

```go
type GitWorkflow string // "" (none) | "gitflow" | "custom"

const (
    GitWorkflowNone    GitWorkflow = ""
    GitWorkflowGitflow GitWorkflow = "gitflow"
    GitWorkflowCustom  GitWorkflow = "custom"
)

type GitConventionConfig struct {
    Workflow     GitWorkflow `json:"workflow,omitempty" enum:"gitflow,custom"`
    BranchPrefix string      `json:"branchPrefix,omitempty"`
}
```

Added as `GitConvention GitConventionConfig json:"gitConvention,omitempty"`.

- **PR target / base = existing `DefaultBranch`.** No new base/target field (product
  decision 2026-07-08).
- `WithDefaults()`: gitflow with empty `BranchPrefix` → `feature/`. none → untouched
  so an otherwise-empty config still persists as SQL NULL (`IsZero` stays true).
- `Validate()`:
  - unknown `Workflow` (not "", "gitflow", "custom") → error.
  - custom requires a non-empty `BranchPrefix`.
  - `BranchPrefix`, when set, must be a clean branch fragment: no whitespace, no
    `..`, no leading `/`, valid ref chars. A helper `NormalizedBranchPrefix()`
    trims and guarantees exactly one trailing `/`.
- **Default (Workflow == none) leaves every current behavior unchanged.**

## Auto-naming (requirement 3)

Base + PR target stay `DefaultBranch`. Only the prefix is new, as a **pure,
unit-tested function** applied after `sanitizeBranchName`:

```go
// applyConventionPrefix rewrites a sanitized auto-named branch to satisfy the
// project's git convention.
func applyConventionPrefix(sanitized string, cfg GitConventionConfig) string
```

- **none / gitflow:** return `sanitized` unchanged (the namer already emits gitflow
  `<type>/…`; gitflow only nudges the naming prompt with the default prefix).
- **custom:** strip the AI's `<type>/` segment, prepend the normalized custom prefix
  → `feat/STAR-123-x` (Jira key + description preserved).

Wired into `manager.go Spawn`: after `gen(...)` returns a name, run
`applyConventionPrefix(name, cfg.GitConvention)` before `ensureUniqueBranch`.
Explicit `--branch` still wins (it bypasses the namer entirely). The
`ao/<id>/root` fallback (AI naming failed) stays session-unique for worktree
safety and is intentionally NOT reprefixed.

## Prompt injection (requirement 2)

`buildSystemPrompt` loads the project config (via the store) and threads the
convention into both prompts. A new helper `gitConventionPrompt(cfg, defaultBranch)`
returns "" for none, else a "Git branch convention" section:

- **Orchestrator** (primary mechanism): instruct it to spawn workers with
  `--from <defaultBranch>` and an explicit `--branch <prefix><topic>`; list the
  gitflow types (feature/bugfix/hotfix) or the single custom prefix; give the
  Jira-key format (`feature/STAR-2270-ecoupon-list`); say PRs target
  `<defaultBranch>`. Placed before the confidentiality guard.
- **Worker**: a shorter note so it keeps sibling/stacked branches on-convention and
  targets `<defaultBranch>`. Appended alongside `workerMultiPRPrompt`.
- **none:** no section added; prompts identical to today.

`buildSystemPrompt` gains a project-config read; all callers (spawn + restore) pass
`projectID` already, so loading inside keeps every path correct.

## Plumbing (mechanical)

- CLI `set-config`: `--git-workflow` + `--branch-prefix` flags, mirror fields on the
  CLI `projectConfig` struct, wired in `buildProjectConfig`.
- OpenAPI/types: add `schemaNames["DomainGitConventionConfig"] = "GitConventionConfig"`
  in `specgen/build.go`; regenerate with `npm run api` (commits `openapi.yaml` +
  `schema.ts`).
- Frontend: new "Git convention" `Card` in `ProjectSettingsForm.tsx` — shadcn
  `Select` (None / gitflow / custom) + a `Branch prefix` input shown when
  workflow ≠ none. Save merges into the existing whole-config PUT.

## Tests

- Go: `GitConventionConfig.Validate` / `WithDefaults` / `NormalizedBranchPrefix`;
  `applyConventionPrefix` (none/gitflow/custom, Jira-key preservation, missing
  slash); prompt assembly in `manager_test.go` (orchestrator + worker sections for
  gitflow/custom, absent for none); spawn auto-name prefix path.
- Frontend: `ProjectSettingsForm.test.tsx` — card renders, prefix toggles with
  workflow, value persists into the PUT body.
- Run `go test ./...` for touched backend packages; `npm run test` +
  `npm run typecheck` in frontend. Revert `routeTree.gen.ts` / `pnpm-lock.yaml`
  churn.

## Scope guard (YAGNI)

No per-type base/target matrix, no separate PR-target field, no change to the
`ao/<id>/root` fallback, no new migration (config is one JSON blob already).
