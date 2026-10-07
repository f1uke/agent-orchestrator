# Quick Reference

Natural-language-to-command mappings for common AO tasks.

| You want to... | Command |
|---|---|
| Show me this webpage / open this page | `ao preview "<url>"` | <!-- web-ui -->
| Spawn a worker on issue N | `ao spawn --project <p> --from <base-branch> --issue N --name "<=22 chars>" --prompt "..."` |
| Spawn a worker whose PR merges into a different branch | `ao spawn --project <p> --from <base-branch> --target <pr-target-branch> --name "<=22 chars>" --prompt "..."` |
| Message a running agent | `ao send --session <id> --message "..."` |
| Kill a session | `ao session kill <id>` |
| List sessions | `ao session ls` |
| Register a repo as a project | `ao project add --path <abs-path> --name <name>` |
| List projects | `ao project ls` |
| Rename a session | `ao session rename <id> "<name>"` |
| Restore a killed session | `ao session restore <id>` |
| Clean up terminated sessions | `ao session cleanup` |
| See a session's details | `ao session get <id>` |
| Open the desktop app | `ao start` |
| Check the daemon is up | `ao status` |
| Run health checks | `ao doctor` |
| Clear the preview panel | `ao preview clear` | <!-- web-ui -->
| List orchestrator sessions | `ao orchestrator ls` |
| Claim an existing PR for a session | `ao session claim-pr <id> <pr-ref>` |
| Submit a code review verdict | `ao review submit <session-id> --run <run-id> --verdict approved` |
| Link the Testiny run a task's cases were played in | `ao testiny link <task> <run-id\|url>` |
| See what a task's Testiny runs say | `ao testiny runs <task>` |
| Read a case in a task's runs in full (test data, precondition, steps) | `ao testiny case <task> <case-id\|TC-id>` |
| Record a case's result and its steps' results in a linked run | `ao testiny result <task> <run> <case> --status <status> [--comment <text>] [--step <n>=<status>]...` |
| Upload a run's QA Evidence folder to Drive and link each file on its case's result | `ao testiny evidence <task> <run>` |
| Configure a project's default branch or model | `ao project set-config <id> --default-branch <branch> --model <model>` |
| Import projects from a legacy AO install | `ao import --dry-run` first, then `ao import -y` |
