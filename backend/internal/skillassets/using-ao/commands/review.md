# ao review

Manage AO code reviews of a worker's PR.

## Syntax

```
ao review <subcommand> [args] [flags]
```

## Subcommands

---

### ao review submit

Record a reviewer's result for a worker's PR.

**Syntax:**
```
ao review submit [worker-session-id] [flags]
```

**Flags:**

| Flag | Meaning | Default / Required |
|---|---|---|
| `--body string` | Review body: a path to a Markdown file, or `-` to read from stdin | - |
| `--review-id string` | Id of the GitHub PR review just posted (the `.id` from the `gh api` POST that created the review) | - |
| `--reviews string` | JSON review results array or object: a path, or `-` to read from stdin | - |
| `--run string` | Review run id | Required |
| `--session string` | Worker session id (or pass it as the positional argument) | - |
| `--verdict string` | Review verdict: `approved` or `changes_requested` | Required |

---

### ao review reset

Clear a worker's stuck "Reviewing…" state by failing its orphaned running reviews.
Use this when a review is stuck because its reviewer terminal was closed (or the
reviewer died) before it finished: it fails every still-running review run for the
worker so the review can be triggered again. Completed and changes-requested
reviews are left untouched.

**Syntax:**
```
ao review reset [worker-session-id] [flags]
```

**Flags:**

| Flag | Meaning | Default / Required |
|---|---|---|
| `--session string` | Worker session id (or pass it as the positional argument) | - |

## Examples

```bash
# Submit an approved review for session mer-3
ao review submit mer-3 --run review-run-1 --verdict approved
```

```bash
# Submit a changes-requested review with a body from stdin
echo "Please fix the null check on line 42." | ao review submit --session mer-3 --run review-run-1 --verdict changes_requested --body -
```

```bash
# Unstick a worker whose reviewer terminal was closed mid-review
ao review reset mer-3
```
