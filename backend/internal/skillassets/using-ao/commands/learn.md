# ao learn

See what AO kept from projects that learn from sessions, the candidate lessons and the memory
proposals made from them; act on the human's decision about a proposal; run a stage now; change
the budget; delete what was kept. Flags: `ao learn <command> --help`.

## How learning works

A project with `ao project set-config <id> --learn-from-sessions` has its Claude Code
transcripts read in the background. Off means nothing there is read.

1. **Capture** keeps only the human's own turns, redacted (secrets, test-account values,
   emails, pasted payloads), each with a short window of what the agent did around it.
2. **Collect** asks a model, through the human's own `claude` login and sealed (no tools, no
   settings), which turns teach something durable, and keeps those as **drafts**.
3. **Rules** is the corpus a lesson is checked against: what agents are already told (CLAUDE.md
   files, skills, AGENTS.md, AO's standing prompt, the knowledge INDEX, the project's memory),
   split into statements hourly. **Protected rules** are ones the human pinned; their forbidden
   patterns block any proposal that matches.
4. **Decide** turns a finished task's drafts into **proposals**: a new or changed memory file
   (with its `MEMORY.md` line), a rule for `~/.claude/CLAUDE.md`, a change to one of the human's
   skills, or a conflict card. A second, adversarial model call and code gates drop what is not
   grounded in the human's words, breaks a rule, or carries sensitive data.

**Nothing is applied until the human approves it.** Collect and rules run every 10 minutes and
hourly within the daily budget; decide every 30 minutes.

## Reading

| Command | Shows |
|---|---|
| `ao learn status` | Per project: what capture kept, its health warnings, and today's spend against the budget |
| `ao learn excerpts [--context]` | The turns kept, newest first |
| `ao learn drafts` | Candidate lessons; `weak` rests only on an accepted suggestion, `reversed` was taken back later |
| `ao learn rules [--search text]` | Standing rules, each with the id `protect --from` takes |
| `ao learn proposals [--status s]` | Proposals; `ao learn proposals show <id>` gives rationale, evidence, decision history and the diff |
| `ao learn settings` | Models, effort and the daily budget (flags change them) |

## Running a stage now

`ao learn collect` and `ao learn decide` (`--task <key>` for one task, ready or not) run now,
capped by `--budget`; `--wait` prints progress. `ao learn rules refresh` rebuilds the corpus.

## Deciding a proposal

The human decides in the Memory inbox (`/memory` in the app). From a shell, an agent acts only
on what the human said; each decision is logged with `$AO_SESSION_ID`. No decision is final.

```bash
ao learn approve p-12                      # --file <edited> writes an edit; a conflict needs --side
ao learn reject p-12 --reason "..."        # the reason is required: learning reads it
ao learn snooze p-12 --days 14             # ao learn unsnooze p-12 brings it back
ao learn reopen p-12                       # a rejected proposal back in the queue
ao learn undo p-12                         # take back what an approved one wrote
ao learn edit p-12 --file edited.md        # replace what an approved one wrote
```

**If what AO wrote changed since** (by hand, by an agent, by another proposal), `undo` and
`edit` touch nothing: they print the diff and a token, and exit 1. Show the human that diff.
Re-run with `--confirm <token>` only after they agree; never confirm on their behalf.

## Pinning a rule

```bash
ao learn rules --project nter-ios-app --search "tap the simulator"
ao learn rules protect --from 3f2a9c0d1e2b-4 --pattern '\bao sim (tap|type|drag)\b'
ao learn rules check --text "..."          # exits 1 when the text matches a forbidden pattern
```

Patterns are RE2, case-insensitive; without `--project` a pin applies everywhere.
`ao learn rules protected` lists pins, `ao learn rules unprotect <id>` removes one.

## Deleting what was kept

`ao learn forget --project <id>` deletes every turn, draft and cursor for a project. Turn
learning off first (`--learn-from-sessions=false`), or capture reads every transcript again.
