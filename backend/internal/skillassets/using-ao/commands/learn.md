# ao learn

See what AO kept from projects that learn from sessions, or delete it.

A project with `learnFromSessions` on (`ao project set-config <id> --learn-from-sessions`) has its Claude Code transcripts read in the background. AO keeps only the human's own turns, redacted (secrets, test-account values, emails and pasted payloads are taken out), each with a short window of what the agent said and did just before and after. Messages from AO, from other sessions and spawn briefs are recognised and left out. A project with the switch off is never read.

**Collect** then asks a model, through the human's own `claude` login, which of those redacted turns teach something durable, and keeps them as drafts. Only redacted turns are sent; the call runs sealed (no tools, no settings, no transcript of its own) and stops at the daily budget.

## Subcommands

### `ao learn status`

Per project: transcripts tracked, turns kept (by how they arrived: `typed`, `queued`, `suggestion_accepted`, `app_send`, `smoke_report`), the last capture, turns waiting for the model, drafts, the last model-run failure with its stderr, and three warnings:

- prompts the agent hooks saw that never turned up in a transcript (the transcript format may have changed),
- transcripts with unread turns that are 25+ days old (Claude Code deletes them at 30),
- transcripts whose last capture pass failed, with the error.

Then the collect stage: model and effort, today's spend against the daily budget, and the current or last run (runs, failures, drafts, cost, why it stopped).

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

### `ao learn collect`

Asks the model now about every captured turn no model has read yet - the backlog - for one project or every learning project, spending at most `--budget`. The background loop collects on its own every 10 minutes, but only sessions the human has gone quiet in, and only within the daily budget; this is how a backlog is done at once. One run at a time.

| Flag | Meaning | Default / Required |
|---|---|---|
| `--project id` | Project to collect | every project that learns from sessions |
| `--budget dollars` | Most this run may spend, at API prices (at most 50) | `1` |
| `--wait` | Wait for the run to finish, printing progress | off |

### `ao learn drafts`

The candidate lessons the model found, newest first: kind (correction, rule, procedure, fact, preference), the statement, when it applies, and the human's own words it rests on. `weak` marks one resting only on a suggestion the human accepted; `reversed` one a later turn took back. Nothing here is a skill yet - drafts are what a later stage proposes from.

| Flag | Meaning | Default / Required |
|---|---|---|
| `--project id` | Project to list | the current session's project |
| `--limit n` | Most drafts to show (at most 500) | `20` |
| `--json` | Output as JSON | - |

### `ao learn settings`

Shows the collect model, effort and daily budget, or changes them.

| Flag | Meaning | Default / Required |
|---|---|---|
| `--model id` | Collect model | `claude-sonnet-5-5` |
| `--effort level` | `low`, `medium`, `high`, `xhigh` or `max` | `low` |
| `--daily-budget dollars` | Background collect stops for the day at this spend; `0` pauses it | `2` |

### `ao learn forget`

Deletes every turn, draft, model run, capture cursor and fingerprint AO kept for a project. The project must have learning off first (`ao project set-config <id> --learn-from-sessions=false`), or capture would read every transcript again on its next pass.

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
