# ao review

Manage AO code reviews of a worker's branch or PR.

A review does not need a pull request. AO can review a worker's checkout **before**
any PR/MR is opened: the pass is keyed on (branch, head commit) instead of
(PR, head commit), the reviewer diffs the branch against its base, and — because
there is nowhere to post — the body submitted in `ao review submit` is the only
place that review lands. Once a PR does open on that same commit, AO does not
review it again; the pre-MR verdict already covers it.

The reviewer pane closes as soon as a pass submits. Each pass is an independent
run, not a conversation that grows across passes.

## Syntax

```
ao review <subcommand> [args] [flags]
```

## Subcommands

---

### ao review submit

Record a reviewer's result for a worker's branch or PR. On a pass with no PR yet,
the `--body` (or the `body` field in `--reviews`) is the whole review — write the
summary and every finding into it, as `<path>:<line> — <finding>`, because there
is no inline comment and no PR thread carrying it.

**Syntax:**
```
ao review submit [worker-session-id] [flags]
```

**Flags:**

| Flag | Meaning | Default / Required |
|---|---|---|
| `--body string` | Review body: a path to a Markdown file, or `-` to read from stdin. Required for `changes_requested`, and the only carrier of a review on a branch with no PR yet | - |
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
# Submit a pre-MR review (no PR exists yet) — the body IS the review
printf '%s' '{ "reviews": [ { "runId": "review-run-1", "verdict": "approved", "githubReviewId": "", "body": "Looks good. No blocking findings." } ] }' \
  | ao review submit --session mer-3 --reviews -
```

```bash
# Unstick a worker whose reviewer terminal was closed mid-review
ao review reset mer-3
```
