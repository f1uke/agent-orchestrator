# poteto-mode compliance audit

Checks recent AO worker sessions against the rules of pstack poteto-mode. For each rule, a session gets pass, fail or n/a. There is no model in the loop. Every verdict comes from a pattern over the worker's tool calls, their order, and the text it wrote.

    node scripts/poteto-compliance/run.mjs [--limit 25] [--sessions id,id] [--json out.json] [--md out.md]

`run.mjs` reads `ao session ls/get --json`, the session transcripts under `~/.claude/projects/`, the branch commits (`git log`, or the forge's PR commit list when the branch is gone), and the PR or MR body (`gh pr view`, `glab api`). It sends GET requests only. It skips qa sessions, sessions that never loaded `/pstack:poteto-mode`, and its own session.

`audit.mjs` holds the rules. Each rule has its detection method and its known false-positive and false-negative risks next to the check. `audit.test.mjs` pins the rules with fictional sessions:

    node --test scripts/poteto-compliance/audit.test.mjs

The output names real sessions and quotes nothing from them. Keep it out of the repository anyway.
