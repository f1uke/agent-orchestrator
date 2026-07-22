# Branch-mirroring tmux session names

## Problem

Worker worktree directories now nest under their sanitized branch
(`<managedRoot>/<projectID>/<branch>`, commit `6c704e62`) so the folder on disk is
recognizable. The tmux session name did not follow: it is still derived from the
opaque session id (`tmux.go` → `SessionName(cfg.SessionID)`), so `tmux ls` shows
names like `mer-1` that no longer line up with the branch or the worktree folder.

We want the tmux session name to mirror the branch too, so the runtime, the
worktree, and `tmux ls` all tell the same story.

## Constraints

- **tmux's namespace is flat and global.** Unlike the worktree path, which nests
  under `<projectID>`, a single tmux server shares one flat set of session names
  across every project. A raw branch name (e.g. an AI-generated `feature/PROJ-xxx`)
  could collide across two projects.
- **tmux reserves `.` and `:`.** They are target syntax (`session:window.pane`), so
  a session name must not contain them — a deliberate divergence from the worktree
  charset, which keeps `.`.
- **Downstream ops already use the persisted handle.** `Create` returns a
  `RuntimeHandle{ID: <name>}` that is stored as `SessionMetadata.RuntimeHandleID`.
  Destroy, attach, capture, send-keys, and the reaper all read that stored value,
  so they stay consistent with any naming change for free. The only place that
  _re-derives_ the name from the session id is the `ao spawn` attach hint
  (`cli/spawn.go`).

## Naming rule

For a session that has a branch (worker or orchestrator):

```
name = sanitize(projectID + "/" + branch)
```

`sanitize` keeps `[A-Za-z0-9_-]`, collapses every other run of characters
(`/`, `.`, space, `@`, …) to a single `-`, trims leading/trailing `-`, and caps
length. This is the existing `sanitizedSessionName` base logic **minus its hash
suffix**, factored into a shared helper.

Examples:

| projectID | branch                     | tmux name                      |
| --------- | -------------------------- | ------------------------------ |
| `mer`     | `feature/PROJ-2271-x`      | `mer-feature-PROJ-2271-x`      |
| `mer`     | `ao/mer-1/root` (default)  | `mer-ao-mer-1-root`            |
| `mer`     | `ao/<prefix>-orchestrator` | `mer-ao-<prefix>-orchestrator` |

The `projectID` prefix mirrors the worktree's `<projectID>/<branch>` nesting and
keeps names unique across projects, since a branch is unique within a project.

A session with **no branch** (the reviewer pane, which uses a synthetic
`reviewerHandleID`) keeps today's session-id-based `SessionName(sessionID)`.

### Accepted tradeoff: no hash suffix

Without a hash, two branches that differ only in sanitized-away punctuation
(`x.y` vs `x-y`) collapse to the same tmux name, and `tmux new-session` fails
loudly. This is rare and surfaces as a visible spawn error rather than silent
breakage. Clean, readable names were the explicit choice over the hashed variant.

## Scope

- **Workers + orchestrators** get branch-based names (any session with a branch).
  The orchestrator's worktree path is _not_ branch-based, so its tmux name matches
  its branch, not its folder — an accepted consequence of the uniform rule.
- **Reviewer panes:** unchanged (no branch → fallback).
- **ConPTY (Windows) runtime:** unchanged. This request is tmux-specific; ConPTY
  ignores the new config fields.
- **API schema:** unchanged. `projectId` and `branch` are already serialized on the
  session read model, so the CLI attach hint reuses them.

## Changes

1. **`ports.RuntimeConfig`** — add `ProjectID domain.ProjectID` and `Branch string`.
2. **`session_manager/manager.go`** — populate `ProjectID` and `Branch` at both
   `RuntimeConfig{}` construction sites (spawn ≈ line 323, restore ≈ line 629) from
   `cfg.ProjectID`/`rec.ProjectID` and `ws.Branch`.
3. **`tmux/tmux.go`** —
   - Factor the char-sanitizer out of `sanitizedSessionName` into a shared base
     helper (parameterized max length; branch names allow a longer cap than the
     hashed ids do).
   - Add exported `SessionNameFor(projectID, branch, sessionID string) (string, error)`:
     branch-based when both `projectID` and `branch` are non-empty, else today's
     `tmuxSessionName(sessionID)` (which preserves the empty-id error).
   - `Create` derives its name via `SessionNameFor(string(cfg.ProjectID), cfg.Branch, string(cfg.SessionID))`.
4. **`cli/spawn.go`** — the attach hint calls `tmux.SessionNameFor(...)` with
   `projectId` + `branch` from the spawn response instead of re-deriving from the id
   alone. Add `projectId`/`branch` to the `spawnResult.Session` decode struct.
5. **Unchanged:** `review/launcher.go` (leaves the new fields empty → fallback).

## Tests

- **`tmux` unit tests** for `SessionNameFor`:
  - projectID + gitflow branch → `mer-feature-PROJ-2271-x`
  - projectID + default `ao/<id>/root` branch
  - punctuation/unsafe chars collapsed to dashes (`.`, space, `@`, `/`)
  - empty branch → session-id fallback
  - empty branch _and_ empty session id → error (preserved)
- **`tmux` create test** asserts the created session name for a config carrying a
  branch.
- **`cli` attach-hint** expectation updated to the branch-based name.
