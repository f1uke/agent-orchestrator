# ao orchestrator

Manage orchestrator sessions.

## Syntax

```
ao orchestrator <subcommand> [flags]
```

## Subcommands

---

### ao orchestrator ls

List orchestrator sessions. Aliases: `ls`, `list`.

**Syntax:**
```
ao orchestrator ls [flags]
```

**Flags:**

| Flag | Meaning | Default / Required |
|---|---|---|
| `--json` | Output as JSON | - |

## Examples

```bash
# List all orchestrator sessions
ao orchestrator ls
```

```bash
# List orchestrator sessions as JSON
ao orchestrator ls --json
```

A worker reports to its project's orchestrator when it finishes, needs the human, or opens its PR. Its prompt names the
orchestrator's session id; when it does not, or that session has ended, the live one is the entry under the worker's
project that is not `[terminated]`.
