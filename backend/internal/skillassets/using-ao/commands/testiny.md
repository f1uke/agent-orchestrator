# ao testiny

Link a task's Testiny test runs, read them and their cases back, and record case results in them.

On a project with a Testiny project set (`ao project set-config <project> --testiny-project <key>`),
each task has a Testiny tab. It lists the Testiny test runs the task's cases were played in.
AO keeps the links and a log of the results it recorded. Titles, cases and results are read
live from Testiny. Recording a result with `ao testiny result` is the only write AO makes:
create cases, plans and runs with the `testiny` CLI, as the `managing-testiny-qa` skill says,
and never record a result with `testiny run results set` yourself, because AO cannot log it.

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

---

### ao testiny case

Show a case in full, read from Testiny: its title, one meta line (priority, type, platforms,
Jira, features), then each section the case fills in: Test data, Precondition, Steps (each
action numbered, with its expected result under it), Description and Remark. A TEXT case shows
its Steps and Expected result as two texts, and a BDD case its Scenarios. Rich text keeps its
lists and paragraphs. The case is its id (`7166`) or `TC-7166`, and it must be in a run linked
to the task. A case read in the last minute is served from memory.

Read the case before you play it: its Test data names the int/uat test account and data the
case needs.

**Syntax:**
```
ao testiny case <task> <case-id|TC-id> [flags]
```

**Flags:**

| Flag | Meaning | Default / Required |
|---|---|---|
| `--json` | Print the daemon's JSON | - |

---

### ao testiny result

Record case results in a run linked to the task. Give one case with `--status` (and
`--comment` for FAILED, BLOCKED and SKIPPED), or a batch with `--from-file`: a JSON array of
`{"caseId": 7166, "status": "FAILED", "comment": "..."}`. The case is its id (`7166`) or
`TC-7166`.

- Statuses: `PASSED`, `FAILED`, `BLOCKED`, `SKIPPED`, `NOTRUN`.
- FAILED, BLOCKED and SKIPPED need a comment of at most 300 characters that says what
  happened. PASSED and NOTRUN take none.
- When the task has a qa, only qa records results. A solo worker records its own task's.
- An agent never overwrites a status a person set, in the app or in Testiny. That write is
  refused with `TESTINY_RESULT_SET_BY_PERSON`: report your result in the handback instead,
  and do not retry. A person sets the case back to NOTRUN to ask for a re-run.
- The whole batch is checked before anything is written.

The command sends `$AO_SESSION_ID` and the checkout's `git rev-parse --short HEAD`, so the
tab shows "set by qa on 4f2c9e1". It prints each case as Testiny now has it, and the run's
new counts.

**Syntax:**
```
ao testiny result <task> <run> <case> --status <status> [--comment <text>]
ao testiny result <task> <run> --from-file <path|->
```

**Flags:**

| Flag | Meaning | Default / Required |
|---|---|---|
| `--status` | The case's status | Required with a case |
| `--comment` | What happened, at most 300 characters | Required for FAILED, BLOCKED, SKIPPED |
| `--from-file` | A JSON array of results; `-` reads stdin | Instead of a case |

## Errors

| Code | Exit | Meaning |
|---|---|---|
| `TESTINY_BAD_RUN_REF` | 2 | Not a run id, `TR-<id>`, or run URL |
| `TESTINY_BAD_CASE_REF` | 2 | Not a case id or `TC-<id>` |
| `TESTINY_OFF` | 2 | The project has no Testiny project set |
| `TESTINY_RESULT_INVALID` | 2 | A result breaks a rule above, or names a case that is not in the run |
| `TESTINY_RESULT_SET_BY_PERSON` | 2 | A person set one of the cases: report it in the handback, do not retry |
| `TESTINY_WRITE_NOT_YOURS` | 2 | The task has a qa and you are not it, or you are not on the task |
| `TESTINY_RUN_NOT_LINKED` | 1 | The run is not linked to the task: `ao testiny link` it first |
| `TESTINY_CASE_NOT_IN_TASK` | 1 | The case is in none of the runs linked to the task |
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

```bash
# What a case asks for, before you play it
ao testiny case "$AO_CREW_ID" TC-7166
```

```bash
# Record one failed case
ao testiny result "$AO_SESSION_ID" 632 TC-7167 --status FAILED --comment "ปุ่มยืนยันไม่แสดงหลังกรอก PIN"
```

```bash
# Record a whole run at once
echo '[{"caseId":7166,"status":"PASSED"},{"caseId":7168,"status":"BLOCKED","comment":"ไม่มีบัญชีทดสอบที่ถือกองทุนนี้"}]' \
  | ao testiny result "$AO_SESSION_ID" 632 --from-file -
```
