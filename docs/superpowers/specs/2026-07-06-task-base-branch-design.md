# New Task: choose base branch + new branch name — Design Spec

Date: 2026-07-06
Status: Approved for planning
Branch: feat/gitlab-support

## Problem

When creating a task, the "Branch" field names the new working branch, but the
branch is always created off the **project default branch** — the base is not
selectable. `session_manager/manager.go` sets `WorkspaceConfig.BaseBranch =
project.Config.DefaultBranch` unconditionally, and `SpawnConfig` has no
`BaseBranch` field. So a user who wants a task to start from an existing branch
(e.g. a stacked `PROJ-2270`) gets a fresh branch off `main` that lacks that work.

## Goal

In the New Task dialog let the user specify BOTH:

- **Start from** — the base branch the new worktree/branch is created from
  (searchable dropdown of the repo's branches; defaults to project default).
- **New branch name** — the working branch name (optional; blank = auto-named).

## Non-goals (this round)

- Basing a task off an arbitrary commit SHA or tag (branches only).
- Changing orchestrator branch behavior (stays `ao/<prefix>-orchestrator`).
- Editing the base of an already-created session.

## Semantics (must be explicit)

- The chosen base is used **only when creating a new branch**.
- If "New branch name" matches a branch that **already exists**, gitworktree
  checks that branch out and the base is ignored (standard git worktree
  semantics — you cannot re-base an existing branch by checking it out).
- Blank "New branch name" → existing auto-name (`ao/<id>/root` etc.), but now
  based off the chosen base.
- Empty base → falls back to the project default branch (today's behavior),
  so existing callers/CLI are unaffected.

## Backend

### 1. List branches endpoint

`GET /api/v1/projects/{projectId}/branches` → `{ "branches": ["develop", "main",
"origin/PROJ-2270", ...] }`.

- Implemented via git: `for-each-ref --format=%(refname:short) refs/heads
refs/remotes/origin`, deduped, with `origin/HEAD` dropped. Follows the
  existing `gitOutput`/git-exec pattern in `service/project`.
- Thin read; errors surface as the standard daemon error envelope. If the repo
  isn't registered/available, return an empty list rather than 500 so the
  dialog degrades gracefully to just the default branch.

### 2. `SpawnConfig.BaseBranch`

Add `BaseBranch string` to `ports.SpawnConfig` (session.go). The create-session
controller reads `in.BaseBranch` from the request DTO and passes it through
`Svc.Spawn`.

### 3. Manager threads the base

In `session_manager/manager.go` (~L225-235):

```
base := cfg.BaseBranch
if base == "" {
    base = project.Config.WithDefaults().DefaultBranch
}
ws, err := m.workspace.Create(ctx, ports.WorkspaceConfig{..., Branch: branch, BaseBranch: base})
```

`gitworktree.Create`/`addWorktree` already accept `BaseBranch`; no adapter
change needed beyond receiving the chosen base.

### 4. DTO + codegen

Add `BaseBranch string json:"baseBranch,omitempty"` to the create-session
request DTO (dto.go:~129). Run `npm run api` to regenerate openapi.yaml +
frontend schema.ts.

## Frontend (NewTaskDialog)

### 5. Fetch branches

A react-query hook fetches `GET /projects/{projectId}/branches` when a project
is selected (and on project change). Loading/empty → dialog still works with
just the default branch typed/selected.

### 6. "Start from" combobox

A lightweight searchable dropdown (no new dependency): a text input that filters
the fetched branch list shown in a small scrollable list; selecting fills the
value. Default value = the project's default branch. Built from existing
primitives (`Input` + a filtered list, styled per DESIGN.md). Sends `baseBranch`
in the POST body (omit when it equals the default / is blank).

### 7. Relabel existing field

Rename the current "Branch" field to **"New branch name"**, placeholder
`optional — auto-named if blank`. Its value continues to map to `branch`.

## Testing

- Backend: table test the branches endpoint with faked git output (names,
  origin dedupe, `origin/HEAD` dropped, repo-missing → empty). Manager test:
  `cfg.BaseBranch` set → `WorkspaceConfig.BaseBranch` equals it; empty → falls
  back to project default. No real git/network (fakes).
- Frontend: NewTaskDialog test — branch list renders/filters; selecting a base
  and submitting includes `baseBranch` in the payload; blank new-name still
  submits `branch: undefined`.

## Rollout

Purely additive. Existing New Task flow (no base chosen) behaves exactly as
today (falls back to project default). CLI `ao start` unaffected (BaseBranch
optional, empty → default).
