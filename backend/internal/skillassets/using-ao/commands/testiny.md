# ao testiny

Link a task's Testiny runs, read them and their cases, record case results, and upload a run's
QA evidence to Google Drive with each file linked on its case's result. Flags and arguments:
`ao testiny <command> --help`.

## What AO writes, and what it does not

On a project with Testiny on (`ao project set-config <project> --testiny`), each task has a
Testiny tab listing the runs its cases were played in. AO makes exactly two writes to Testiny:
a result (`ao testiny result`) and a comment holding evidence links (`ao testiny evidence`).

- Create cases, plans and runs with the `testiny` CLI, as the `managing-testiny-qa` skill says.
- Never record a result with `testiny run results set`, or post an evidence link with
  `testiny run results comment`: AO cannot log it, and the task's tab will not show it.
- A run belongs to the TASK: linked from a crew's qa, it shows for dev and the person too.
- **When the task has a qa, only qa records results and uploads evidence.** A solo worker does
  its own task's.

## Link, then read the case before you play it

```bash
ao testiny link "$AO_SESSION_ID" 632 --project MOB          # or the run's URL
ao testiny runs "$AO_SESSION_ID"                            # counts, failing cases, evidence folder
ao testiny case "$AO_SESSION_ID" TC-7166                    # test data, precondition, steps
```

Run ids are global in Testiny, so a task can hold runs from several Testiny projects. Give the
run's URL, or its id with `--project`; pick the project from the task's Jira key (`STAR-2413` is
in STAR, `MOBILITY-123` in project key MOB). `testiny project ls` lists keys. Linking checks the
run exists, and linking twice is a no-op; `ao testiny unlink <task> <run>` takes one off. A case's Test data names the int/uat account it needs.

## Record results

```bash
ao testiny result "$AO_SESSION_ID" 632 TC-7167 --status FAILED \
  --comment "ขั้นที่ 2 ไม่เห็นปุ่มยืนยัน" --step 1=PASSED --step 2=FAILED --step 3=BLOCKED
echo '[{"caseId":7166,"status":"PASSED"},{"caseId":7168,"status":"BLOCKED","comment":"..."}]' \
  | ao testiny result "$AO_SESSION_ID" 632 --from-file -
```

- Statuses, for a case and a step: `PASSED`, `FAILED`, `BLOCKED`, `SKIPPED`, `NOTRUN`.
- FAILED, BLOCKED and SKIPPED need a comment (at most 300 characters) saying what happened. A
  step takes no comment: the case's comment says which step went wrong.
- Steps count from 1, as `ao testiny case` numbers them. A step you do not name keeps its result.
- The whole batch is checked before anything is written.
- **Never overwrite what a person set.** A case or step a person set, in the app or in Testiny,
  is refused with `TESTINY_RESULT_SET_BY_PERSON`, naming each one. Do not retry: report your
  result in the handback. A person sets it back to NOTRUN to ask for a re-run.

## Upload evidence

The run's folder must already sit at this path, every name read from Testiny, never typed:

```
~/Desktop/QA Evidence/<Project>/<YYYY>/<milestone>/TP-<n> - <plan>/TR-<n> - <run>/
    README.md
    TC-2124 pass.png
    TC-2130 FAIL MOBILITY-4533.mp4
    TC-2131 pass - iPhone 15 iOS 18.png
```

- `<Project>` is the Testiny project's name (`MOBILITY`, not `MOB`); `<YYYY>` is the year the
  milestone starts. The run needs a test plan and a milestone; attaching them is the human's call.
- `README.md` is required. Every other file is `TC-<id> pass[ - <device>].<ext>` or
  `TC-<id> FAIL <JIRA-KEY>[ - <device>].<ext>`, for a case in the run. No subfolders.
- AO never renames, moves or deletes, locally or on Drive. Put the files in place yourself; a
  wrong folder or file is refused with every problem listed and the path it must have.

Then `ao testiny evidence "$AO_SESSION_ID" 632`. It uploads through the person's `rclone` remote
to the Drive folder set in AO's Settings, and posts one comment per case with the links no
comment on that result has yet. Re-running sends only what is missing, so after a failure just
run it again. Upload before the run is closed: a closed run takes no links. It never changes a
result's status, and it can take up to 35 minutes for recordings.

## Errors

| Code | Do this |
|---|---|
| `TESTINY_RESULT_SET_BY_PERSON` | Report your result in the handback; do not retry |
| `TESTINY_WRITE_NOT_YOURS` | The task has a qa and you are not it: hand the result to qa |
| `TESTINY_RUN_NOT_LINKED` | `ao testiny link` the run first |
| `TESTINY_RUN_WRONG_PROJECT`, `TESTINY_PROJECT_NOT_FOUND` | Fix `--project` (or use the run's URL) |
| `TESTINY_RESULT_INVALID`, `TESTINY_EVIDENCE_INVALID` | Fix every problem the message lists, then run again |
| `TESTINY_RUN_CLOSED` | Ask the human to reopen the run |
| `TESTINY_OFF` | The project does not use Testiny: `ao project set-config <project> --testiny` |
| `TESTINY_EVIDENCE_OFF`, `DRIVE_REMOTE_MISSING` | Ask the human to set the Drive folder or rclone remote |
| `DRIVE_AUTH` | The human runs `rclone config reconnect <remote>:` |
| `DRIVE_DUPLICATE` | The human removes the extra copy on Drive (AO never deletes there) |
| `DRIVE_UNAVAILABLE`, `TESTINY_UNAVAILABLE` | Run it again; what reached Drive is logged |
| `TESTINY_AUTH` | Run `testiny auth status` in a terminal |
| `TESTINY_CLI_MISSING`, `TESTINY_CLI_TOO_OLD` | `cd ~/Documents/Projects/testiny-cli && git pull && go install ./cmd/testiny` |
| `DRIVE_RCLONE_MISSING` | `brew install rclone` |
