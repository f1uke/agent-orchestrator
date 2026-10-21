# Playbook adherence harness

Measures whether an AO dev worker opens the matching pstack poteto-mode
playbook and starts a todolist of its steps before it explores code.

    node scripts/playbook-adherence/run.mjs --out <dir> [--arms base,A,B,C,C-wrong,D] [--tasks bug,feature,perf] [--reps 5] [--jobs 6]
    node scripts/playbook-adherence/grade.mjs --out <dir>

`run.mjs` composes the dev standing prompt with the real `buildSystemPrompt`
(through `TestDumpGatedDevPrompt`), copies `fixture/` into a fresh git repo
per run, and starts `claude -p` with that prompt appended, the same way AO
launches a worker. A finished run is skipped on a rerun, so an interrupted
run resumes. Each run costs about $0.50 to $1.30.

The arms and tasks are in `arms.mjs`; the grading rules are at the top of
`grade.mjs` and pinned by `transcript.test.mjs` (`node --test scripts/playbook-adherence/transcript.test.mjs`).

Workers write their plans into `~/.ao/knowledge/mer/`, the fictional project
the prompt names. Delete that directory after a run.
