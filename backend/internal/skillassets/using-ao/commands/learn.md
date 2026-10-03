# ao learn

See what AO kept from projects that learn from sessions, or delete it.

A project with `learnFromSessions` on (`ao project set-config <id> --learn-from-sessions`) has its Claude Code transcripts read in the background. AO keeps only the human's own turns, redacted (secrets, test-account values, emails and pasted payloads are taken out), each with a short window of what the agent said and did just before and after. Messages from AO, from other sessions and spawn briefs are recognised and left out. Nothing is sent anywhere. A project with the switch off is never read.

## Subcommands

### `ao learn status`

Per project: transcripts tracked, turns kept (by how they arrived: `typed`, `queued`, `suggestion_accepted`, `app_send`, `smoke_report`), the last capture, and three warnings:

- prompts the agent hooks saw that never turned up in a transcript (the transcript format may have changed),
- transcripts with unread turns that are 25+ days old (Claude Code deletes them at 30),
- transcripts whose last capture pass failed, with the error.

| Flag | Meaning | Default / Required |
|---|---|---|
| `--json` | Output as JSON | - |

### `ao learn excerpts`

The turns kept for a project, newest first.

| Flag | Meaning | Default / Required |
|---|---|---|
| `--project id` | Project to list | the current session's project |
| `--limit n` | Most turns to show (at most 500) | `20` |
| `--context` | Also show what the agent said and did around each turn | off |
| `--json` | Output as JSON | - |

### `ao learn forget`

Deletes every turn, capture cursor and fingerprint AO kept for a project. The project must have learning off first (`ao project set-config <id> --learn-from-sessions=false`), or capture would read every transcript again on its next pass.

| Flag | Meaning | Default / Required |
|---|---|---|
| `--project id` | Project to forget | required |

## Examples

```bash
# Is capture running, and is anything at risk?
ao learn status
```

```bash
# The last five things the human typed to this project's sessions, with context
ao learn excerpts --project agent-orchestrator --limit 5 --context
```
