# ao smoke

Author and read a session's manual smoke-test checklist, record a machine's
result beside the user's, and retire a case out of the checklist.

The checklist is stored AO-private under `~/.ao`, keyed to the session. It is
never written into your checkout, so pass JSON on stdin (`--from-file -`) rather
than creating a file on your branch.

## Two results per case, never merged

Every case carries **two** results, in two separate sets of fields:

| | who writes it | answers | opens the merge gate? |
|---|---|---|---|
| verdict / note / evidence | the **user**, playing the case in the Tests tab | "does this actually work for a person?" | yes |
| agent verdict / note / evidence / sha | **`ao smoke record`** | "did the steps run?" | **no, never** |

They are not interchangeable. Recording latency, dead drag-scroll, keystrokes
never arriving, a tab pausing when unfocused, control lost after a lease lapse -
every regression a person has caught by hand lives in the gap between those two
questions. A recorded pass moves a label; the person still plays the case.

Evidence keeps its provenance for the same reason: files attached by a person and
files captured by a machine live in **separate lists**, because evidence is
exactly what you go back to when you distrust a verdict.

## The one destructive edge, and the way out

`ao smoke set` replaces the **whole** checklist. A case id is derived from the
case NAME when you omit `"id"`, so **rewording a name produces a different id**
and the old case falls out of the payload.

- A case nobody has played is dropped freely, blobs and all.
- A case the user HAS played (verdict, note, or evidence) is **not** dropped: the
  call is refused with `SMOKE_RESULTS_AT_RISK`, naming each case. Those results
  are the one part of a checklist AO cannot regenerate.

Three ways past that refusal:

1. Re-send the case under the id it already has - add `"id": "<existing-id>"` to
   the case that replaces the reworded one. This is what you want when the case
   still exists and only its wording changed.
2. `ao smoke retire` it - the case keeps its name, steps and results, records why
   it went, and then legitimately falls out of the checklist.
3. Ask the user to Reset the case in the Tests tab, which discards their results
   deliberately.

## Syntax

```
ao smoke <subcommand> [session] [flags]
```

The session id is positional or `--session`; inside a worker it is
`"$AO_CREW_ID"`.

**Use `$AO_CREW_ID`, not `$AO_SESSION_ID`.** A checklist belongs to the TASK, and
a task can be worked by two agents on one worktree - dev, which owns the branch
and the pull request, and qa, which writes and runs the tests. The human plays the
checklist on DEV's card, so a checklist qa writes against its own session id is
one nobody ever sees. `$AO_CREW_ID` is dev's id on a crew and your own id when you
are working alone, so it is right in both shapes.

## Subcommands

---

### ao smoke set

Author or replace the whole checklist (typically 3-6 cases). A keyed upsert: an
id that matches keeps the user's verdict/note/evidence, new ids are added, and
ids absent from the payload are removed - subject to the refusal above. Retired
cases are outside all of this: they are never dropped for being absent, and
naming a retired id is refused rather than reviving it.

**Flags:**

| Flag | Meaning | Default / Required |
|---|---|---|
| `--session string` | Session id (or pass it as the positional argument) | - |
| `--from-file string` | Path to the checklist JSON, or `-` for stdin | Required |

The JSON is `{ "cases": [ ... ] }` (a bare `[ ... ]` array is also accepted).
Each case:

```json
{
  "id":       "gitlab-mr-appears",
  "name":     "A fresh MR shows up in Reviews on its own",
  "why":      "Confirms re-polling surfaces a new MR without a manual refresh.",
  "steps":    ["Open the Reviews tab.", "Open a new MR.", "Wait ~60s."],
  "expected": "The new MR appears automatically with CI + review status.",
  "prNum":    36,
  "fileRef":  "scmobserver.go:936"
}
```

`name` is required; `id` is optional (derived from the name when omitted - supply
it to keep results across a rename).

---

### ao smoke list

Print the checklist with its results. Each case prints in full - why it matters,
its numbered steps, the expected result and its id - so it can be played straight
from this output. A machine's result prints on its own indented lines under the
user's verdict, never folded into it. Retired cases are listed last with the
reason they went.

**Flags:**

| Flag | Meaning | Default / Required |
|---|---|---|
| `--session string` | Session id (or pass it as the positional argument) | - |
| `--brief` | One line per case, omitting why/steps/expected | `false` |
| `--json` | Print the raw JSON response | `false` |

---

### ao smoke record

Record a **machine's** result for one case. Strictly additive: it cannot rewrite
a case's authored content, cannot touch the user's verdict, note or evidence, and
cannot remove a case. Running it again re-records the machine's result, which is
what re-running a case means. It is refused on a retired case.

**Flags:**

| Flag | Meaning | Default / Required |
|---|---|---|
| `--session string` | Session id (or pass it as the positional argument) | - |
| `--case string` | Case id to record against (see `ao smoke list`) | Required |
| `--verdict string` | `pass`, `fail`, or `skip`. Omit for an evidence-only record | - |
| `--note string` | What the machine saw | - |
| `--sha string` | Commit the case was run against | HEAD of the repo in the current directory |
| `--evidence string` | Screenshot/clip the machine captured; repeatable | - |

`--sha` is what lets a reader tell a fresh result from one that predates the
current head, so let it default rather than omitting it.

Omitting `--verdict` while attaching `--evidence` is a legitimate record: it says
"I ran it and captured this, I am not the one who can judge it" - the permanent
state of a case about paint, focus, timing or feel.

---

### ao smoke retire

Retire a case out of the checklist. **Retire is not delete.** The case stops
being something the user is asked to play, and everything about it stays: name,
steps, the user's verdict, note and evidence, plus the reason it went and when.
That trace is the point - "retired 3, now covered by tests" is worth far more
than three cases quietly disappearing.

A retired case is frozen: no verdict, no reset, no machine result, and no
re-author. If it genuinely needs to come back, add it under a **new** id - it
comes back unplayed, which is right, because the old results were recorded
against the old steps.

**Flags:**

| Flag | Meaning | Default / Required |
|---|---|---|
| `--session string` | Session id (or pass it as the positional argument) | - |
| `--case string` | Case id to retire (see `ao smoke list`) | Required |
| `--reason string` | Why it is no longer worth playing | Required |

## Examples

```bash
# Author the checklist (JSON on stdin so nothing lands in your checkout)
cat <<'JSON' | ao smoke set "$AO_CREW_ID" --from-file -
{ "cases": [ { "name": "A fresh MR shows up in Reviews on its own",
               "why": "Confirms re-polling surfaces a new MR without a refresh.",
               "steps": ["Open the Reviews tab.", "Open a new MR.", "Wait ~60s."],
               "expected": "The new MR appears automatically.",
               "prNum": 0, "fileRef": "scmobserver.go:936" } ] }
JSON
```

```bash
# Read it back, with ids, to see what the user has played
ao smoke list "$AO_CREW_ID"
```

```bash
# Record that a machine ran a case (the user's verdict is untouched)
ao smoke record "$AO_CREW_ID" --case gitlab-mr-appears --verdict pass \
    --note "3 runs, MR listed within 40s each time"
```

```bash
# Record what a machine saw without judging it
ao smoke record "$AO_CREW_ID" --case tab-stays-live --evidence /tmp/shot.png
```

```bash
# Retire a case that a real test now covers
ao smoke retire "$AO_CREW_ID" --case drag-scroll --reason "now covered by TestDragScroll"
```
