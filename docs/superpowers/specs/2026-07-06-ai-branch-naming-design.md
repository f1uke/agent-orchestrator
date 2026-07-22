# AI-generated gitflow branch names on New Task — Design Spec

Date: 2026-07-06
Status: Approved for planning
Branch: feat/gitlab-support

## Problem

When a user creates a task and leaves "New branch name" blank, AO auto-names
the worker branch `ao/<session-id>/root` — a machine name that carries no
meaning about the work. Users who follow a gitflow + Jira convention (e.g.
`feature/PROJ-2271-checkout-result`) must type the full name by hand every time.
They want to leave the field blank and have the agent name it for them, matching
their `<type>/<JIRA-KEY>-<short-desc>` house style.

## Goal

When "New branch name" is left blank in the New Task dialog, generate a
gitflow-style branch name by asking the session's own agent CLI (one-shot),
following `<type>/<JIRA-KEY>-<short-desc>` (e.g.
`feature/PROJ-2271-checkout-result`). If generation fails, times out, produces an
invalid name, or the harness has no one-shot mode, fall back to the current
`ao/<session-id>/root` name. Spawning must never fail or hang because of naming.

## Non-goals (this round)

- Changing orchestrator branch naming (stays `ao/<prefix>-orchestrator`).
- Auto-naming for the CLI (`ao start`) or any non-dialog caller — those keep
  today's behavior unless they explicitly opt in.
- Adding one-shot naming to every harness now. Only claude-code is implemented;
  other harnesses degrade to the fallback until an adapter opts in.
- Persisting a per-project naming convention/config. The convention lives in the
  prompt sent to the agent.

## Key facts established during design

- **Attribution is safe with any branch name.** `observer.go` `matchSession`
  attributes a PR when its source branch equals the session's own branch
  (`Metadata.Branch`) OR lives under `<session-branch>/`. The `ao/` prefix is
  just the default name, not a hard requirement. So
  `feature/PROJ-2271-checkout-result` is attributed, and stacked children
  `feature/PROJ-2271-checkout-result/<topic>` are attributed too.
- **No prompt change needed.** `workerMultiPRPrompt()`
  (`session_manager/manager.go:1132`) is already branch-name-agnostic — it refers
  to `<namespace>/<topic>` and `your-branch/<topic>` and handles the `/root`
  case separately. A gitflow branch (not ending in `/root`) takes the
  "otherwise" path: children are `your-branch/<topic>`. Correct as-is.
- **Spawn insertion point.** In `session_manager/manager.go` (~L219-233) the
  session `id` exists, `project`, `cfg`, `prompt`, and `systemPrompt` are all
  available, and the worktree is not yet created. Branch naming already happens
  here (L225-228). This is where generation slots in.
- **Agents build argv; the runtime runs it.** The `ports.Agent` adapters return
  an argv (`GetLaunchCommand`); the runtime spawns the PTY. There is no existing
  one-shot LLM call anywhere in the backend — this introduces the first.
- **Binary + auth already resolvable.** `claudecode.ResolveClaudeBinary(ctx)`
  resolves the claude binary; auth is ambient (`ANTHROPIC_API_KEY` / `~/.claude`),
  the same the daemon already relies on. Prefer `process.CommandContext` (the
  daemon's console-window-hiding wrapper) over raw `os/exec`.

## Architecture

Naming happens synchronously inside `Spawn`, gated on an explicit opt-in flag, and
always has a deterministic fallback. It reuses the session's harness adapter
through a new **optional** interface so no adapter is forced to implement it.

### 1. Opt-in request flag (additive)

- Add `AutoNameBranch bool` to `ports.SpawnConfig` (`internal/ports/session.go`).
- Add `autoNameBranch bool json:"autoNameBranch,omitempty"` to the create-session
  request DTO (`internal/httpd/controllers/dto.go`); the controller passes it to
  `Svc.Spawn`. Run `npm run api` to regenerate `openapi.yaml` + frontend
  `schema.ts`.
- Semantics: generation runs only when `AutoNameBranch == true` AND
  `cfg.Branch == ""` AND `cfg.Kind != domain.KindOrchestrator`. The dialog sets
  the flag true whenever the new-branch-name field is blank. CLI/other callers
  omit it → `false` → today's behavior (`ao/<id>/root`).

### 2. One-shot namer port (optional interface)

In `internal/ports/agent.go`:

```go
// OneShotNamer is implemented by agent adapters that can answer a single
// non-interactive prompt (e.g. `claude -p`). Adapters that only run
// interactive sessions do not implement it; callers must handle ok == false.
type OneShotNamer interface {
    // OneShotArgv returns the argv to run the given prompt non-interactively.
    // ok == false means this harness has no one-shot mode (caller falls back).
    OneShotArgv(ctx context.Context, prompt string) (argv []string, ok bool, err error)
}
```

- **claude-code adapter** (`internal/adapters/agent/claudecode/claudecode.go`)
  implements it: resolve the binary via the existing `ResolveClaudeBinary`/
  cached `claudeBinary(ctx)`, return
  `[]string{binary, "-p", prompt, "--output-format", "text"}, true, nil`. If the
  binary can't be resolved, return `nil, false, err`.
- Other adapters (codex, opencode, …) do **not** implement it yet — they are the
  fallback path. Extension point documented for later.
- The manager obtains the session's agent (it already resolves the adapter for
  `cfg.Harness` before `GetLaunchCommand`) and type-asserts it to
  `OneShotNamer`. Not implemented → fallback.

### 3. Branch namer (`internal/session_manager/branchname.go`, new file)

Pure, testable helpers plus one method that shells out.

**Jira key extraction:**
```go
var jiraKeyRe = regexp.MustCompile(`\b[A-Z][A-Z0-9]+-\d+\b`)
func extractJiraKey(texts ...string) string // first match across title+prompt, or ""
```

**Naming prompt builder:**
```go
func buildNamingPrompt(title, brief, jiraKeyHint string) string
```
Instructs the agent to output ONLY a git branch name, no prose, following:
- gitflow type: one of `feature`, `bugfix`, `hotfix`, `chore` (infer from intent)
- include the Jira key when one is provided/detectable, uppercase, right after the
  `/` (e.g. `feature/PROJ-2271-...`); omit gracefully when none
- short description: 2–4 words, kebab-case, lowercase, abbreviated
- total length ≤ 60 chars; ASCII `[a-z0-9/-]` only; no trailing `/`, no `..`
- example given verbatim: `feature/PROJ-2271-checkout-result`

**Sanitizer (defends against chatty output):**
```go
func sanitizeBranchName(raw string) (string, bool)
```
- take the first non-empty line; strip surrounding backticks/quotes/whitespace and
  a leading `branch:`-style label if present
- lowercase; replace runs of disallowed chars with `-`; collapse repeated `-`/`/`;
  trim leading/trailing `-` and `/`
- reject (`ok=false`) if: empty, no `<type>/` gitflow prefix from the allowed set,
  contains `..`, ends in `.lock`, exceeds 80 chars, or fails a git-ref-safety
  check (no space, no `~^:?*[`, no control chars)
- otherwise return the cleaned name, `ok=true`

**Uniqueness:**
```go
func ensureUniqueBranch(existing map[string]bool, candidate string) string
```
- `existing` holds every taken name (local `refs/heads/*`, `refs/remotes/origin/*`,
  and active worktree branches), keyed without the `refs/...` prefix
- if `candidate` is free, return it; else append `-2`, `-3`, … until free
- the manager builds `existing` by reusing the repo's ref listing (the same git
  plumbing as `service/project.ListBranches`) plus `git worktree list`

**Orchestrating method (shells out):**
```go
func (m *Manager) generateBranchName(ctx context.Context, agent ports.Agent,
    cfg ports.SpawnConfig, project domain.ProjectRecord) (string, bool)
```
1. type-assert `agent.(OneShotNamer)`; not ok → return `"", false`
2. `key := extractJiraKey(cfg.IssueID, cfg.Prompt)`
3. `prompt := buildNamingPrompt(cfg.IssueID, cfg.Prompt, key)`
4. `argv, ok, err := namer.OneShotArgv(ctx, prompt)`; `!ok || err` → `"", false`
5. run argv via `process.CommandContext` with a bounded timeout
   (default 20s, override `AO_BRANCH_NAME_TIMEOUT`), `cmd.Dir` = a throwaway OS
   temp dir (keeps the call context-free and fast; not app state, so the `~/.ao`
   rule does not apply), capture stdout; any error/timeout → `"", false`
6. `name, ok := sanitizeBranchName(stdout)`; `!ok` → `"", false`
7. return `name, true`

### 4. Manager wiring (`session_manager/manager.go` ~L225-228)

```go
branch := cfg.Branch
if branch == "" {
    if cfg.AutoNameBranch && cfg.Kind != domain.KindOrchestrator {
        if name, ok := m.generateBranchName(ctx, agent, cfg, project); ok {
            existing := m.existingBranchNames(ctx, project) // local+origin+worktrees
            branch = ensureUniqueBranch(existing, name)
        }
    }
    if branch == "" { // fallback: generation off, unsupported, or failed
        branch = defaultSessionBranch(id, cfg.Kind, sessionPrefix(project))
    }
}
```

`agent` must be resolved before this block (move/duplicate the existing adapter
resolution up if needed). Orchestrator path is untouched.

### 5. Frontend (NewTaskDialog)

**Layout — full-width stacked (chosen).** Replace the `Agent + New branch name`
two-column grid (`NewTaskDialog.tsx:204-240`) with three stacked full-width
fields, in order: **Start from**, **New branch name**, **Agent** (with "Refresh
agents" under Agent). Each field is its own `space-y-1.5` block like Title/Brief.
Branch names are long; full width avoids truncation and puts the two branch
fields ("Start from", "New branch name") adjacent as a logical group, with Agent
separated below.

**Behavior.**
- New-branch-name placeholder → `optional — AI names it if blank`.
- On submit, send `autoNameBranch: cleanBranch === "" ? true : undefined` (true
  only when the field is blank). Continue sending `branch: cleanBranch ||
  undefined`.
- Because naming blocks the spawn a few seconds, the existing submit spinner
  covers it; update the submitting label to `Naming branch…` when
  `autoNameBranch` was sent and the field was blank (otherwise `Starting…`).

## Data flow

```
NewTaskDialog (blank name) ──POST /sessions {autoNameBranch:true, branch:undefined}──▶ controller
  controller ─ SpawnConfig{AutoNameBranch:true} ─▶ Manager.Spawn
    Spawn: id created ▶ resolve agent(cfg.Harness)
      generateBranchName: OneShotNamer? ─yes─▶ claude -p <prompt> ─stdout─▶ sanitize ─ok─▶ name
                                        └─no──▶ fallback
      ensureUniqueBranch(local+origin+worktrees, name) ─▶ branch
      (any failure) ─▶ defaultSessionBranch(id,…) = ao/<id>/root
    workspace.Create{Branch: branch, BaseBranch: base}  ── worktree created ──▶ …
```

## Error handling

- Naming is best-effort. Every failure mode (adapter not a `OneShotNamer`, argv
  build error, exec error, non-zero exit, timeout, empty/invalid/unsafe output)
  resolves to the `ao/<id>/root` fallback. No error surfaces to the user and the
  spawn proceeds.
- Uniqueness collision resolves deterministically by numeric suffix; if suffixing
  somehow can't find a free name within a small bound (e.g. 50 tries), fall back
  to `defaultSessionBranch`.
- Timeout is bounded (`AO_BRANCH_NAME_TIMEOUT`, default 20s) so a hung/
  unauthenticated CLI cannot block a spawn indefinitely.

## Testing

No network and no real agent CLI in Go tests — the `OneShotNamer` is faked.

- **Sanitizer table** (`branchname_test.go`): plain name passes; prose/backticked
  output (`` `feature/PROJ-2271-x`\nSure! ``) → `feature/PROJ-2271-x`; uppercase
  → lowercased; spaces/invalid chars → `-`; missing gitflow prefix → `ok=false`;
  `..`, overlength, control chars → `ok=false`.
- **Jira key extraction table**: `PROJ-2271` in title; in brief URL; none →
  `""`; lowercase `proj-2271` not matched.
- **ensureUniqueBranch**: candidate free → unchanged; taken → `-2`; `-2` also
  taken → `-3`.
- **Manager** (fake `OneShotNamer` + fake workspace capturing `WorkspaceConfig`):
  - `AutoNameBranch=false` → branch `ao/<id>/root`.
  - `AutoNameBranch=true`, namer returns `feature/PROJ-2271-x` → `WorkspaceConfig.
    Branch == "feature/proj-2271-x"` (post-sanitize), unique-suffixed vs. a seeded
    existing set.
  - `AutoNameBranch=true`, namer returns garbage → fallback `ao/<id>/root`.
  - `AutoNameBranch=true`, `Kind=orchestrator` → orchestrator name, namer never
    called.
- **claude adapter**: `OneShotArgv` returns argv beginning with the resolved
  binary and containing `-p` and the prompt; shape assertion only (no exec).
- **Frontend** (`NewTaskDialog` test): blank name → payload has
  `autoNameBranch: true` and no `branch`; typed name → `autoNameBranch` absent,
  `branch` set; layout renders three stacked fields.

## Rollout

Purely additive. Dialog spawns with a blank name gain AI naming (with fallback);
typed names and all non-dialog callers are unchanged. No schema migration; the
new request field is optional.
