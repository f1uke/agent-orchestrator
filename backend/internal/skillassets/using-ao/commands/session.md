# ao session

List, inspect, rename, kill, restore and clean up agent sessions, and attach PRs to them.
Flags: `ao session <command> --help`. `-p <project>` scopes a lookup when ids could clash.

| Command | What it does |
|---|---|
| `ao session ls` | Active worker sessions. `-a` adds orchestrators, `--include-terminated` the ended ones |
| `ao session get <id>` | One session's details; `--json` for everything |
| `ao session rename <id> "<name>"` | Change its display name |
| `ao session kill <id>` | Terminate it. Its worktree is kept |
| `ao session restore <id>` | Relaunch a terminated session in its kept worktree |
| `ao session cleanup` | Reclaim the worktrees of terminated sessions. Dirty worktrees are skipped; `-y` skips the prompt |
| `ao session claim-pr <id> <pr>` | Attach an existing PR to a session. By default it takes the PR over from whichever session owns it; `--no-takeover` refuses instead |
| `ao session set-branch <id> <branch>` | Record the session's own branch. A `git branch -m` in its worktree is followed by itself; use this when the old branch was kept or the worktree is detached |
| `ao session set-claude-profile <id> <profile>` | Switch a claude-code session to another Claude profile. `--restart` restarts the agent onto it now when it is idle, or once it is idle when it is mid-turn; without it the profile applies on the next restart |

```bash
ao session restore mer-3
ao session claim-pr mer-3 88 --no-takeover
```

Read a session's live status with `ao session ls` or `ao session get` before reporting on it: a
status from when it was spawned is stale.

## Claude profiles

A Claude profile is a named Claude Code settings file a claude-code session launches with
(`claude --settings <file>`, on top of `~/.claude/settings.json`). `Subscription` (no file, the
default) and `OmniRoute` are built in; the human adds more in the app's Settings.
`ao claude-profile ls` lists them. A session takes the profile `ao spawn --claude-profile` names,
else its project's (`ao project set-config --claude-profile`), else `Subscription`. Other agents
have no profile.

```bash
ao claude-profile ls
ao session set-claude-profile mer-3 OmniRoute --restart
```

AO checks the profile's file before every launch and refuses a missing file or one that is not a
JSON object, so a restart onto a broken profile leaves the running agent alone.
