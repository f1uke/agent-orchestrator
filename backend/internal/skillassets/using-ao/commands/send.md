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
| `--about string` | The commit SHA, or on a Testiny project a case or run id, this message is about | Required when messaging your crewmate |

## Messaging your crewmate

A task worked by two agents has a **dev** and a **qa**, both running at the same
time in one worktree. Address the other one by ROLE, never by id:

```bash
ao send --crew dev --about 4a1b2c3 --message "run finished on this commit; 1 finding, details below"
ao send --crew qa  --about 9f3e2a1 --message "fixed and pushed"
```

`--crew` exists because an id would be wrong exactly when you need it: the crew
is formed after dev is already running, so dev's environment never carries qa's
id. The daemon resolves the role.

**These messages are CAPPED, and the caps are mechanism rather than advice** -
two agents that can each answer the other will otherwise talk forever with
nobody watching:

- **`--about` is required.** Every message names a durable artifact - a commit,
  or on a Testiny project a case or run - so there is no "what do you think?" to
  answer.
- **Three messages per subject, per direction.** The fourth is refused and the
  task goes to NEEDS YOU for a human.
- **Twenty messages per hour per crew**, as a backstop.

**There is no obligation to reply, because the artifact IS the reply.** dev
answers a finding by COMMITTING; qa answers a handoff by RUNNING it and handing
back. Do not send an acknowledgement and do not wait for one.

The one message that is not a reply and IS required: **qa tells dev when a run
finishes**, pass or fail. The end of qa's run is the start of dev's, and a result
nobody is told about leaves a task with nobody working on it.

### A report on work nobody checked

When a WORKER reports to the orchestrator on a task that **drove the app** and
never had a qa on it, AO appends one clearly attributed `[AO]` line to the
report and tells you the same on your own stdout: nothing here has been checked
by anything but the agent that wrote it.

It is **not refused**: a report that never lands is worse than one with a
warning attached, and a refusal here would be indistinguishable from the
runaway-loop refusal that parks a task at NEEDS YOU. `ao crew review` is how
dev puts a qa on the task when the change is ready; if you have decided it does
not need one, say so in the report rather than leaving it unsaid. A task that
never drove a runtime surface - a backend-only change - is never checked here and
never warned.

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
ao send --crew dev --about "$(git rev-parse --short HEAD)" --message "run finished; 1 finding, 2 things a person must check by hand"
```
