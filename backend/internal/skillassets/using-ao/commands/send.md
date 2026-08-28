# ao send

Send a message to a running agent session. Use this to correct or direct a live agent mid-stream without killing and respawning it.

A session that is not listening right now has the message HELD and delivered when it is, so a message is never silently lost.

## Syntax

```
ao send [flags]
```

## Flags

| Flag | Meaning | Default / Required |
|---|---|---|
| `--message string` | Message body | Required (unless `--message-file`) |
| `--message-file string` | Read the body from a file, or `-` for stdin | Optional; mutually exclusive with `--message` |
| `--session string` | Session id | Required unless `--crew` |
| `--crew string` | Message your crewmate by ROLE (`dev` or `qa`) | Required unless `--session` |
| `--about string` | The commit SHA or smoke case id this message is about | Required when messaging your crewmate |
| `--still-working` | qa only: this is a mid-run update, not the end of your run | `false` |

## Messaging your crewmate

A task worked by two agents has a **dev** and a **qa**, both running at the same
time in one worktree. Address the other one by ROLE, never by id:

```bash
ao send --crew dev --about 4a1b2c3 --message "tests pass on this commit; 2 cases left for the human"
ao send --crew qa  --about tab-stays-live --message "fixed and pushed"
```

`--crew` exists because an id would be wrong exactly when you need it: the crew
is formed after dev is already running, so dev's environment never carries qa's
id. The daemon resolves the role.

**These messages are CAPPED, and the caps are mechanism rather than advice** -
two agents that can each answer the other will otherwise talk forever with
nobody watching:

- **`--about` is required.** Every message names a durable artifact - a commit or
  a smoke case - so there is no "what do you think?" to answer.
- **Three messages per subject, per direction.** The fourth is refused and the
  task goes to NEEDS YOU for a human.
- **Twenty messages per hour per crew**, as a backstop.

**There is no obligation to reply, because the artifact IS the reply.** dev
answers a finding by COMMITTING; qa answers a handoff by RECORDING a result. Do
not send an acknowledgement and do not wait for one.

The one message that is not a reply and IS required: **qa tells dev when a run
finishes**, pass or fail. The end of qa's run is the start of dev's, and a result
nobody is told about leaves a task with nobody working on it.

### The handback is checked

A qa -> dev message is read as the END of qa's run, so AO looks at the task's
smoke checklist and reports how many cases carry nothing from any machine. Every
case should be in one of two states:

- **driven** - `ao smoke record` put something on it: a verdict, or `--evidence`
  with no verdict;
- **declared undriveable** - `ao smoke record --case <id> --verdict skip --note
  "<why you could not run it>"`, which is the machine lane's "I could not run
  this one" and requires its reason. **The reason has to come from an ATTEMPT.**
  "The agent cannot press and hold" is a finding after you have tried it and a
  guess before it, and the note is where a person can tell which they are
  reading.

Whatever is in neither state is named - to qa, and in the message dev receives.
It is **not refused**: a handback that never lands is worse than an incomplete
one, and a refusal here would be indistinguishable from the runaway-loop refusal
that parks a task at NEEDS YOU.

If your run is genuinely not over, pass `--still-working` rather than skipping
cases to quiet the count. A case declared undriveable that nobody tried is the
one thing that makes the whole count worthless.

## Examples

```bash
# Send a correction to a running session
ao send --session mer-3 --message "Focus only on the backend; ignore frontend files."
```

```bash
# Give the agent new instructions mid-task
ao send --session mer-3 --message "The issue is in session_manager.go line 142, not in the CLI. Investigate there."
```

```bash
# qa handing back to dev when its run is done
ao send --crew dev --about "$(git rev-parse --short HEAD)" --message "recorded 1 case, retired none; 2 left for the human"
```
