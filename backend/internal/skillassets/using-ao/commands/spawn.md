# ao spawn

Spawn a worker agent session in a registered project. The session runs the chosen agent in a fresh git worktree. Register the project first with `ao project add`.

## Syntax

```
ao spawn [flags]
```

## Flags

| Flag | Meaning | Default / Required |
|---|---|---|
| `--from string` | Source branch the worktree is created FROM (the UI "Start from" field), e.g. `main` | **Required** |
| `--target string` | Branch the worker's PR will merge INTO, e.g. `develop` | Optional; defaults to the `--from` branch |
| `--branch string` | New branch name for the session worktree | AI-named from the task when blank (like the UI); falls back to `ao/<session-id>/root` if naming fails |
| `--claim-pr string` | Immediately claim an existing PR for the spawned session: a github.com PR URL/number, or a full GitLab merge-request URL | - |
| `--harness string` | Agent harness to use (see list below) | Project `worker.agent`; required if the project has none |
| `--issue string` | Issue id to associate with the session | - |
| `--keep-warm` | Keep the worker on the board (suspend in place, resumable) instead of archiving it to Done when its PR merges — for a worker that will open more PRs | - |
| `--name string` | Display name shown in the sidebar (max 20 characters) | Derived from `--prompt` when omitted |
| `--no-takeover` | Refuse if another active session owns the claimed PR (requires `--claim-pr`) | - |
| `--project string` | Project id to spawn the session in | `AO_PROJECT_ID` or the current registered repo |
| `--prompt string` | Initial prompt for the agent | - |
| `--prompt-file string` | Read the initial prompt from a file, or `-` for stdin; mutually exclusive with `--prompt`. Use for large prompts that would exceed the shell's argument-length limit | - |
| `--skip-agent-check` | Skip the advisory agent catalog install/auth preflight before spawning | - |
| `--task-size string` | Worker ceremony level: `mechanical` \| `standard` \| `deep`. `mechanical` authorizes the worker to skip the brainstorm/plan/TDD process skills and go straight to edit + verify; use only for small, well-scoped changes | `standard` |
| `--todo` | Stage the worker as a prepared TODO on the board instead of starting it now (no branch/worktree/tmux until `ao session start <id>`) | - |

`--agent` is an alias for `--harness`.

`--from` and `--target` are distinct: `--from` is the git base ref the worktree is **cut from**, `--target` is the branch the worker's pull request **merges into**. They differ whenever a task branches off one line and lands on another — e.g. a hotfix cut from `release/2.1` that merges into `develop`. `--from` is required and omitting it fails fast without spawning; `--target` is optional and resolves to the `--from` branch when omitted. Either way the resolved target is recorded on the session and shown in its Summary tab.

Leave `--branch` blank to let AO name the new branch from the task (the same auto-naming the UI New task modal does when "New branch name" is left empty), or pass `--branch <name>` to set it yourself. When the project configures a git branch convention (gitflow or a custom prefix), an omitted `--branch` is auto-named on-convention; your standing orchestrator instructions describe the project's prefix, base branch, and PR target.

Available harnesses: `claude-code`, `codex`, `aider`, `opencode`, `grok`, `droid`, `amp`, `agy`, `crush`, `cursor`, `qwen`, `copilot`, `goose`, `auggie`, `continue`, `devin`, `cline`, `kimi`, `kiro`, `kilocode`, `vibe`, `pi`, `autohand`.

## Examples

```bash
# Spawn a worker for issue 142; AO auto-names the new branch from the task
ao spawn --project agent-orchestrator --from main --issue 142 --name "fix-session-leak" --prompt "Fix the session leak described in issue 142."
```

```bash
# Spawn a worker off develop with an explicit new branch name
ao spawn --project agent-orchestrator --from develop --branch feature/PROJ-142-session-leak --name "fix-session-leak" --prompt "Fix the session leak."
```

```bash
# Gitflow hotfix: cut the worktree from release/2.1, but merge the PR into develop
ao spawn --project agent-orchestrator --from release/2.1 --target develop --branch hotfix/PROJ-142-session-leak --name "hotfix-session-leak" --prompt "Fix the session leak."
```

```bash
# Spawn a worker and immediately claim an open PR
ao spawn --project agent-orchestrator --from main --name "review-pr-88" --claim-pr 88 --harness claude-code
```
