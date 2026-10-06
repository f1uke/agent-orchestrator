# ao testiny

Link a task's Testiny test runs and read them back.

On a project with a Testiny project set (`ao project set-config <project> --testiny-project <key>`),
each task has a Testiny tab. It lists the Testiny test runs the task's cases were played in.
AO keeps only the links. Titles, cases and results are read live from Testiny, and AO never
writes to Testiny: create runs and record results with the `testiny` CLI, as the
`managing-testiny-qa` skill says.

A run belongs to the TASK. Linked from a crew's qa, it lands on the task that dev and the
person see.

## Syntax

```
ao testiny <subcommand> [args] [flags]
```

## Subcommands

---

### ao testiny link

Link a run to a task. The run is a run id (`632`), `TR-632`, or the run's URL
(`https://app.testiny.io/MOB/testruns/tr/632`). AO links it only after Testiny confirms the
run exists and is in the project's Testiny project, so a typo never sits in the list.
Linking a run twice is a no-op.

**Syntax:**
```
ao testiny link <task> <run-id|url>
```

Prints the run it linked, e.g. `linked TR-632 "MOBILITY-4839 Chat notice disclaimer - iOS" (6 cases: 6 passed)`.

---

### ao testiny unlink

Unlink a run from a task. Unlinking a run that is not linked is not an error.

**Syntax:**
```
ao testiny unlink <task> <run-id>
```

---

### ao testiny runs

Show the runs linked to a task, read from Testiny now: each run's title and counts, every
case that did not pass with its status, and the run's evidence folder under
`~/Desktop/QA Evidence`. When Testiny cannot be read, the run says why and shows the last
read AO has.

**Syntax:**
```
ao testiny runs <task> [flags]
```

**Flags:**

| Flag | Meaning | Default / Required |
|---|---|---|
| `--json` | Print the daemon's JSON | - |

## Errors

| Code | Exit | Meaning |
|---|---|---|
| `TESTINY_BAD_RUN_REF` | 2 | Not a run id, `TR-<id>`, or run URL |
| `TESTINY_OFF` | 2 | The project has no Testiny project set |
| `TESTINY_RUN_NOT_FOUND` | 1 | Testiny has no such run |
| `TESTINY_RUN_WRONG_PROJECT` | 1 | The run is in another Testiny project |
| `TESTINY_PROJECT_NOT_FOUND` | 1 | The project's Testiny setting names no Testiny project |
| `TESTINY_AUTH` | 1 | Testiny refused the API key: run `testiny auth status` in a terminal |
| `TESTINY_UNAVAILABLE` | 1 | Testiny could not be reached or answered with an error |
| `TESTINY_CLI_MISSING` | 1 | The `testiny` CLI is not installed (on PATH or in `~/go/bin`) |

## Examples

```bash
# Link the run you just played the task's cases in (from inside the session)
ao testiny link "$AO_SESSION_ID" 632
```

```bash
# Link a run by its URL
ao testiny link mer-3 https://app.testiny.io/MOB/testruns/tr/565
```

```bash
# What the task's runs say now
ao testiny runs mer-3
```
