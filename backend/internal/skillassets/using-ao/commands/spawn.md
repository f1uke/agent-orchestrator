# ao spawn

Spawn a worker agent session in a registered project. The session runs the chosen agent in a fresh git worktree. Register the project first with `ao project add`.

## Syntax

```
ao spawn [flags]
```

## Flags

| Flag | Meaning | Default / Required |
|---|---|---|
| `--from string` | Source branch the worktree is created from (the UI "Start from" field), e.g. `main` | **Required** |
| `--branch string` | New branch name for the session worktree | AI-named from the task when blank (like the UI); falls back to `ao/<session-id>/root` if naming fails |
| `--claim-pr string` | Immediately claim an existing PR for the spawned session | - |
| `--harness string` | Agent harness to use (see list below) | Project `worker.agent`; required if the project has none |
| `--issue string` | Issue id to associate with the session | - |
| `--name string` | Display name shown in the sidebar (max 20 characters) | Required |
| `--no-takeover` | Refuse if another active session owns the claimed PR (requires `--claim-pr`) | - |
| `--project string` | Project id to spawn the session in | Required |
| `--prompt string` | Initial prompt for the agent | - |

`--agent` is an alias for `--harness`.

`--from` is required and names the existing branch the worktree starts from — omitting it fails fast without spawning. Leave `--branch` blank to let AO name the new branch from the task (the same auto-naming the UI New task modal does when "New branch name" is left empty), or pass `--branch <name>` to set it yourself. When the project configures a git branch convention (gitflow or a custom prefix), an omitted `--branch` is auto-named on-convention; your standing orchestrator instructions describe the project's prefix, base branch, and PR target.

Available harnesses: `claude-code`, `codex`, `aider`, `opencode`, `grok`, `droid`, `amp`, `agy`, `crush`, `cursor`, `qwen`, `copilot`, `goose`, `auggie`, `continue`, `devin`, `cline`, `kimi`, `kiro`, `kilocode`, `vibe`, `pi`, `autohand`.

## Examples

```bash
# Spawn a worker for issue 142; AO auto-names the new branch from the task
ao spawn --project agent-orchestrator --from main --issue 142 --name "fix-session-leak" --prompt "Fix the session leak described in issue 142."
```

```bash
# Spawn a worker off develop with an explicit new branch name
ao spawn --project agent-orchestrator --from develop --branch feature/STAR-142-session-leak --name "fix-session-leak" --prompt "Fix the session leak."
```

```bash
# Spawn a worker and immediately claim an open PR
ao spawn --project agent-orchestrator --from main --name "review-pr-88" --claim-pr 88 --harness claude-code
```
