# `ao crew`

The two agents that can work one task, in one worktree, at the same time.

A task starts as **dev** alone. It gains a **qa** only when somebody asks - dev
asks with `ao crew review` when it believes the change is done, and a person asks
with `ao crew add`. Nothing else creates one. AO used to add a qa by itself the
first time a task drove the app - taking the simulator, or pointing the desktop
browser panel at it - and that is
gone: driving the app is when dev STARTS looking at the change, so the qa it
created went for the same device dev was still using and the two fought over it.

Both members run at the same time. Starting one never stops the other, and
neither can stand the other down.

## Subcommands

| Command | What it does |
|---|---|
| `ao crew review` | **dev**: ask for a qa to check this task, now that you think it is done. Takes no argument - the task is this session |
| `ao crew add <session-id>` | A **person**: put a qa on a task by hand. Names either member; both resolve to the same task |
| `ao crew wake <session-id>` | Start one member. Its crewmate keeps running |
| `ao crew status` | Who is on each task and what they are doing |
| `ao crew run --start\|--end` | Bracket a build, a test suite or a device pass so a result read off a moving tree is discarded rather than trusted |

## `ao crew review` - dev asking for a review

```bash
ao crew review
```

Run it once the work is finished and your own checks pass, **not** while you are
still driving the app: qa starts working the moment it exists, and the device,
the worktree and the git index are all shared from then on.

It is refused if the task already has a qa, if the task is finished (its pull
request has merged), if the task was tagged `--task-size mechanical` - one agent
by design - or if this project is set to form no crews automatically. In each of
those a **person** can still add one, with the `+ qa` control on the task in the
app or `ao crew add <session-id>` in their own shell.

Asking is one-way and once. A task has one qa and it keeps its id; standing it
down (`ao session kill <qa-id>`) is the undo, and `ao session restore <qa-id>`
brings the same member back.

## If you never ask

Nothing refuses you, and nothing quietly forgives you either. When you report to
the orchestrator on a task that **drove the app** and never had a qa, AO appends
one clearly-attributed `[AO]` line to the report saying so, tells you the same on
your own stdout, and the task's card says it too, next to the `+ qa` control that
answers it. The message still goes: a report that never lands is worse than one
with a warning attached.

A task that never drove a runtime surface - a backend-only change - is never
warned and never needs a qa. That is the point: it stays one agent and costs
nothing extra.

## Once there are two of you

- **One git index, one branch.** A wide `git add -A` sweeps up your crewmate's
  half-written work. Commit the paths you meant to commit.
- **The smoke checklist is SHARED and per-case.** Use `ao smoke add` /
  `edit --case <id>` / `remove --case <id>`. Never `ao smoke set` - it replaces
  the whole list, so whoever runs it second deletes the other's cases. Leave
  `ao smoke record` (the machine's result) to qa.
- **Address the other by ROLE, never by id:**
  `ao send --crew qa --about <commit-sha|case-id> --message "..."`. dev cannot
  know qa's id: the crew is formed after dev is already running.
- **Bracket what you want to trust:** `ao crew run --start --kind build|test|device`
  ... `ao crew run --end --result pass|fail`.
