# ao learn

See what AO kept from projects that learn from sessions, or delete it.

A project with `learnFromSessions` on (`ao project set-config <id> --learn-from-sessions`) has its Claude Code transcripts read in the background. AO keeps only the human's own turns, redacted (secrets, test-account values, emails and pasted payloads are taken out), each with a short window of what the agent said and did just before and after. Messages from AO, from other sessions and spawn briefs are recognised and left out. A project with the switch off is never read.

**Collect** then asks a model, through the human's own `claude` login, which of those redacted turns teach something durable, and keeps them as drafts. Only redacted turns are sent; the call runs sealed (no tools, no settings, no transcript of its own) and stops at the daily budget.

**Rules** is the corpus a lesson is checked against: what agents are already told (the human's CLAUDE.md and skills, each learning project's repo CLAUDE.md, AGENTS.md and skills, AO's standing prompt, the knowledge INDEX, the project's Claude Code memory), split into statements and refreshed hourly. Repo files are only read. **Protected rules** are the ones the human pins; their forbidden patterns block any proposed change that matches.

**Decide** turns a finished task's drafts into **proposals** - a new or changed Claude Code memory file of the project (with its line in `MEMORY.md`), a rule for every project added to `~/.claude/CLAUDE.md`, a change to one of the human's own skills, or a conflict card when the human's words contradict a standing rule - each with the diff AO computed. A second, adversarial model call and code gates refuse what is not grounded in the human's own words, breaks a rule, or carries sensitive data. Nothing is applied: the human approves every proposal.

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

### `ao learn rules`

The standing rules that apply in a project (its own and every global one), in source order, or the best matches for `--search`. Each has an id (`<chunk>-<n>`) that `protect --from` takes.

| Flag | Meaning | Default / Required |
|---|---|---|
| `--project id` | Project | the current session's project; none lists global rules only |
| `--search text` | Rank the rules against this text (BM25) | - |
| `--limit n` | Most rules to show | `20` |
| `--json` | Output as JSON | - |

Subcommands:

- `ao learn rules sources [--project id]` - every file and prompt the corpus is built from, with chunks, rules and why any chunk is not atomized yet, then the last refresh.
- `ao learn rules refresh [--budget dollars]` - refresh now under its own budget (default `1`); the hourly loop does it within the daily budget.
- `ao learn rules protect (--text "rule" | --from <rule-id>) [--pattern re]... [--project id] [--note why]` - pin a rule. Patterns are RE2, case-insensitive; without `--project` the rule applies everywhere.
- `ao learn rules protected [--project id]` - list pinned rules and their patterns.
- `ao learn rules unprotect <id>` - unpin.
- `ao learn rules check (--text "..." | --file path) [--project id]` - report every forbidden pattern a text matches; exits 1 when one does.

### `ao learn decide`

Decides now every ready task (sessions ended, an orchestrator's day over, or a long-lived session's draft a day old), for one project or all, spending at most `--budget`. The background loop does it every 30 minutes within the daily budget.

| Flag | Meaning | Default / Required |
|---|---|---|
| `--project id` | Project to decide | every project that learns from sessions |
| `--task key` | Decide this one task now, ready or not (`solo:<session>`, `crew:<id>`, `orch:<project>:<day>`) | - |
| `--budget dollars` | Most this run may spend, at API prices (at most 50) | `2` |
| `--wait` | Wait for the run to finish, printing progress | off |

### `ao learn proposals`

The proposals, newest first: status (a snoozed one says `snoozed until <date>`), action, scope, confidence, the task's outcome, title and target, and for a decided one when it was written or why it was rejected. `--all` adds the dropped ones with the reason a gate or the verifier gave. `ao learn proposals show <id>` prints the rationale, the verifier's notes, the rule verdicts, the evidence (the human's own words), every decision so far (when, and from the app or the CLI with the AO session that ran it), and the diff - or, once applied, the file as it is now and whether it changed since AO wrote it.

| Flag | Meaning | Default / Required |
|---|---|---|
| `--project id` | Project | every project |
| `--status s` | Only `waiting` (pending, not snoozed), `snoozed`, `pending` (both), `applied`, `rejected`, `stale`, `superseded`, `dropped` or `all` | - |
| `--all` | Include dropped, rejected and settled proposals | pending only |
| `--json` | Output as JSON | - |

### Deciding: `ao learn approve|reject|snooze|unsnooze|reopen|undo|edit <id>`

The human decides in the Memory inbox (`/memory` in the app). These do the same from a shell, so an orchestrator can act on what the human said; each decision is kept in the proposal's history with `cli` and `$AO_SESSION_ID`. No decision is final.

- `ao learn approve <id> [--file edited] [--side keep_rule|words_win|both --text ...]` - write it (a snoozed one too); `--file` writes an edit after the same checks; a conflict needs `--side`.
- `ao learn reject <id> --reason "..."` - the reason is required; learning reads it so it does not propose the same thing again.
- `ao learn snooze <id> [--days N]` (default 7, at most 90) and `ao learn unsnooze <id>` - hide it, or bring it back now.
- `ao learn reopen <id>` - a rejected proposal back in the queue. Refused while another proposal for the same file is waiting, and for a new memory whose file exists already (it would be written twice).
- `ao learn undo <id> [--confirm token]` - take back what an approved proposal wrote and put it back in the queue: a new memory's file and the `MEMORY.md` line it added are removed, a changed file gets its earlier version, a conflict's pinned rule its earlier text (a conflict whose rule was kept is reopened).
- `ao learn edit <id> --file path [--confirm token]` - replace what an approved proposal wrote with the file's content, after the same checks.

If what AO wrote changed since (by hand, by an agent, by another proposal), `undo` and `edit` touch nothing: they print the diff and a token, and exit 1. Re-run with `--confirm <token>` to go ahead; what is there is backed up first. Never confirm on the human's behalf without showing them the diff.

### `ao learn settings`

Shows the collect model and effort, the rules model, the decide model and effort, and the daily budget, or changes them.

| Flag | Meaning | Default / Required |
|---|---|---|
| `--model id` | Collect model | `claude-sonnet-5-5` |
| `--effort level` | `low`, `medium`, `high`, `xhigh` or `max` | `low` |
| `--rules-model id` | Model that splits the standing rules into statements | `claude-haiku-4-5-20251001` |
| `--decide-model id` | Model that draws proposals from finished tasks and verifies them | `claude-opus-5-5` |
| `--decide-effort level` | `low`, `medium`, `high`, `xhigh` or `max` | `medium` |
| `--daily-budget dollars` | Background model runs (collect and rules) stop for the day at this spend; `0` pauses them | `2` |

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

```bash
# What are agents already told about the simulator? Pin the rule and forbid taps.
ao learn rules --project nter-ios-app --search "tap the simulator"
ao learn rules protect --from 3f2a9c0d1e2b-4 --pattern '\bao sim (tap|type|drag)\b'
```
