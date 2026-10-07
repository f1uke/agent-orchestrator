# ao testiny

Link a task's Testiny test runs, read them and their cases back, record case results in them, and
upload a run's QA evidence to Google Drive with each file linked on its case's result.

On a project that uses Testiny (`ao project set-config <project> --testiny`), each task has a
Testiny tab. It lists the Testiny test runs the task's cases were played in. A project is not
tied to one Testiny project: run ids are global in Testiny, so each run names its own project,
and one task may hold runs from several.
AO keeps the links and a log of the results and evidence it recorded. Titles, cases and
results are read live from Testiny. AO makes two writes: a result with `ao testiny result`, and
a comment holding evidence links with `ao testiny evidence`. Create cases, plans and runs with
the `testiny` CLI, as the `managing-testiny-qa` skill says, and never record a result with
`testiny run results set` or post an evidence link with `testiny run results comment`
yourself, because AO cannot log it.

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
run exists, so a typo never sits in the list, and stores the run's own Testiny project with
the link. Give the run's URL, or its id with `--project`: a run that is not in the project the
URL's key or `--project` names is refused (`TR-632 is in STAR, not MOB`). Pick the project
from the task's Jira key (`STAR-2413` is in STAR, `MOBILITY-123` in MOBILITY, key MOB);
`testiny project ls` lists every project with its name and key. Linking a run twice is a no-op.

**Syntax:**
```
ao testiny link <task> <run-id|url> [--project <key>]
```

**Flags:**

| Flag | Meaning | Default / Required |
|---|---|---|
| `--project` | The Testiny project the run is in: its key (`project_key` in `testiny project ls`, e.g. `MOB`), name or id | - |

Prints the run it linked after its project's key, e.g. `linked MOB TR-632 "MOBILITY-4839 Chat notice disclaimer - iOS" (6 cases: 6 passed)`.

---

### ao testiny unlink

Unlink a run from a task. Unlinking a run that is not linked is not an error.

**Syntax:**
```
ao testiny unlink <task> <run-id>
```

---

### ao testiny runs

Show the runs linked to a task, read from Testiny now: each run's Testiny project (its key,
else its name) before its id, its title and counts, every
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
its Steps and Expected result as two texts, and a BDD case its Scenarios. Rich text comes as
the testiny CLI renders it (`testiny case view`): one block per line, lists as `- ` and `1. `,
a table row on one line with its cells joined by ` | `. The case is its id (`7166`) or
`TC-7166`, and it must be in a run linked to the task. A case read in the last minute is served
from memory.

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

Record case and step results in a run linked to the task. Give one case with `--status` (and
`--comment` for FAILED, BLOCKED and SKIPPED) and a `--step <n>=<status>` for each of its steps
you played, or a batch with `--from-file`: a JSON array of
`{"caseId": 7166, "status": "FAILED", "comment": "...", "steps": [{"n": 2, "status": "FAILED"}]}`.
The case is its id (`7166`) or `TC-7166`.

- Statuses, for a case and a step: `PASSED`, `FAILED`, `BLOCKED`, `SKIPPED`, `NOTRUN`.
- FAILED, BLOCKED and SKIPPED need a comment of at most 300 characters that says what
  happened. PASSED and NOTRUN take none. A step takes no comment: the case's comment says
  which step went wrong.
- Steps count from 1, as `ao testiny case` numbers them. Only a STEPS case has steps. A step
  you do not name keeps the result it has. Steps with no `--status` keep the case's status.
- When the task has a qa, only qa records results. A solo worker records its own task's.
- An agent never overwrites a case or step status a person set, in the app or in Testiny.
  That write is refused with `TESTINY_RESULT_SET_BY_PERSON`, which names each case
  (`TC-7166 (PASSED)`) and step (`TC-7166 step 2 (PASSED)`): report your result in the
  handback instead, and do not retry. A person sets the case or step back to NOTRUN to ask
  for a re-run.
- The whole batch is checked before anything is written.

The command sends `$AO_SESSION_ID` and the checkout's `git rev-parse --short HEAD`, so the
tab shows "set by qa on 4f2c9e1". It prints each case, and the steps you recorded under it,
as Testiny now has them, and the run's new counts.

**Syntax:**
```
ao testiny result <task> <run> <case> --status <status> [--comment <text>] [--step <n>=<status>]...
ao testiny result <task> <run> <case> --step <n>=<status>...
ao testiny result <task> <run> --from-file <path|->
```

**Flags:**

| Flag | Meaning | Default / Required |
|---|---|---|
| `--status` | The case's status | Required with a case, unless `--step` is given |
| `--comment` | What happened, at most 300 characters | Required for FAILED, BLOCKED, SKIPPED |
| `--step` | A step's result as `<n>=<status>`, counting from 1; repeatable | - |
| `--from-file` | A JSON array of results; `-` reads stdin | Instead of a case |

---

### ao testiny evidence

Upload a linked run's QA Evidence folder to Google Drive, then post each evidence file's Drive
link as a comment on its case's result. It never touches a result's status: record the
verdict with `ao testiny result`, which is unchanged. On a linked run, uploading and linking
need nobody's yes.

The folder is the one the `managing-testiny-qa` skill builds, with every name read from
Testiny, never typed:

```
~/Desktop/QA Evidence/<Project>/<YYYY>/<milestone>/TP-<n> - <plan>/TR-<n> - <run>/
    README.md
    TC-2124 pass.png
    TC-2130 FAIL MOBILITY-4533.mp4
    TC-2131 pass - iPhone 15 iOS 18.png
```

- `<Project>` is the Testiny project's name (`MOBILITY`, not `MOB`). `<YYYY>` is the year the
  milestone starts in, local time (the year it was created when it has no start date).
- The run needs a test plan and a milestone in Testiny. Attaching them is the human's call.
- `README.md` is required. Every other file is `TC-<id> pass[ - <device>].<ext>` or
  `TC-<id> FAIL <JIRA-KEY>[ - <device>].<ext>`, for a case in the run: `pass` lower case,
  `FAIL` upper case, the Jira key of the defect. No subfolders. Dotfiles are ignored.
- AO never renames, moves or deletes, here or on Drive. A folder that is missing, sits
  elsewhere in the tree, or exists twice is refused with the path it must have, and every
  problem with the files is listed at once. Fix them all, then run it again.
- It goes to the Drive folder set in AO (Settings, an rclone path such as `finnomena:QA`) at
  the same path, through the person's `rclone` remote. The link is the one Drive's own "Copy
  link" gives: who can open it is whatever the shared drive grants.
- Each case's result gets one new comment with the links of its files that no comment on that
  result links yet, one per line, in file name order. A link a person pasted counts. Running it
  again sends only what Drive lacks and posts nothing new, so after a failure just run it again.
- The run must be open: a closed run takes no more links, so upload before the run is closed.
- When the task has a qa, only qa uploads. A solo worker uploads its own task's evidence.

The command sends `$AO_SESSION_ID`, waits up to 35 minutes (recordings take a while), and
prints the local folder, the Drive path, the files sent this time, and per case the files it
linked and those already linked.

**Syntax:**
```
ao testiny evidence <task> <run>
```

## Errors

| Code | Exit | Meaning |
|---|---|---|
| `TESTINY_BAD_RUN_REF` | 2 | Not a run id, `TR-<id>`, or run URL |
| `TESTINY_BAD_CASE_REF` | 2 | Not a case id or `TC-<id>` |
| `TESTINY_OFF` | 2 | The project does not use Testiny: `ao project set-config <project> --testiny` turns it on |
| `TESTINY_RESULT_INVALID` | 2 | A result breaks a rule above, names a case that is not in the run, or names a step the case does not have |
| `TESTINY_RESULT_SET_BY_PERSON` | 2 | A person set one of the cases or steps: report it in the handback, do not retry |
| `TESTINY_WRITE_NOT_YOURS` | 2 | The task has a qa and you are not it, or you are not on the task |
| `TESTINY_EVIDENCE_INVALID` | 2 | The evidence folder or a file in it breaks a rule above, or the run has no plan or milestone; the message lists every problem and the path the folder must have |
| `TESTINY_RUN_CLOSED` | 2 | The run is closed and takes no more links: ask the human to reopen it |
| `TESTINY_RUN_NOT_LINKED` | 1 (2 for `evidence`) | The run is not linked to the task: `ao testiny link` it first |
| `TESTINY_EVIDENCE_OFF` | 1 | No Google Drive folder is set in AO's Settings: ask the human to set one |
| `DRIVE_RCLONE_MISSING` | 1 | `rclone` is not installed: `brew install rclone` |
| `DRIVE_REMOTE_MISSING` | 1 | rclone has no remote of the name the Drive folder setting gives |
| `DRIVE_AUTH` | 1 | The remote's sign-in to Google expired or was revoked: the human runs `rclone config reconnect <remote>:` in a terminal |
| `DRIVE_UNAVAILABLE` | 1 | rclone failed or timed out; whatever reached Drive is logged, so run it again |
| `DRIVE_DUPLICATE` | 1 | The Drive folder holds two files with one name; AO never deletes on Drive, so the human removes the extra copy |
| `TESTINY_CASE_NOT_IN_TASK` | 1 | The case is in none of the runs linked to the task |
| `TESTINY_RUN_NOT_FOUND` | 1 | Testiny has no such run |
| `TESTINY_RUN_WRONG_PROJECT` | 1 | The run is not in the Testiny project the URL or `--project` names |
| `TESTINY_PROJECT_NOT_FOUND` | 1 | `--project` names no Testiny project |
| `TESTINY_AUTH` | 1 | Testiny refused the API key: run `testiny auth status` in a terminal |
| `TESTINY_UNAVAILABLE` | 1 | Testiny could not be reached or answered with an error |
| `TESTINY_CLI_MISSING` | 1 | The `testiny` CLI is not installed (on PATH or in `~/go/bin`) |
| `TESTINY_CLI_TOO_OLD` | 1 | The `testiny` CLI predates a command AO uses: `cd ~/Documents/Projects/testiny-cli && git pull && go install ./cmd/testiny` |

## Examples

```bash
# Link the run you just played the task's cases in (from inside the session)
ao testiny link "$AO_SESSION_ID" 632 --project MOB
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
# Record a case and each of its steps in one call
ao testiny result "$AO_SESSION_ID" 632 TC-7167 --status FAILED --comment "ขั้นที่ 2 ไม่เห็นปุ่มยืนยัน" \
  --step 1=PASSED --step 2=FAILED --step 3=BLOCKED
```

```bash
# Upload the run's evidence folder and link each file on its case's result
ao testiny evidence "$AO_SESSION_ID" 632
```

```bash
# Record a whole run at once
echo '[{"caseId":7166,"status":"PASSED"},{"caseId":7168,"status":"BLOCKED","comment":"ไม่มีบัญชีทดสอบที่ถือกองทุนนี้"}]' \
  | ao testiny result "$AO_SESSION_ID" 632 --from-file -
```
