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

```bash
ao session restore mer-3
ao session claim-pr mer-3 88 --no-takeover
```

Read a session's live status with `ao session ls` or `ao session get` before reporting on it: a
status from when it was spawned is stale.
