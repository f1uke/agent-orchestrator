---
name: bug-triage
description: Triage bugs reported in chat/issues, search for duplicates, file or update GitHub issues with full context, and push fix PRs.
trigger: User reports a bug, or asks to triage/file an issue for a reported problem.
---

# Bug Triage Skill

Triage bugs reported in chat/issues into well-structured GitHub issues on the correct upstream repo.

## Pre-flight

1. **Pull the latest code.** Run `git pull origin main` in the repo. Stale code = bad triage. No exceptions.
2. **Identify the target repo.** Always file on the **upstream org** (e.g., `ComposioHQ/agent-orchestrator`), NOT on user forks unless explicitly told otherwise.
3. **Record the source context:** chat URL, reporter name, any images attached.

## Step 0: Gather Report Context

Collect all available context about the bug:

1. **If from a chat thread:** Read the full thread history. Extract:
   - Reporter name and ID
   - Original bug description (the thread starter, not the person who tagged you)
   - All attachments and screenshots
   - Follow-up discussion and clarifications

2. **If from an existing issue:** Read the issue body and all comments.

3. **If from live observation:** Record session states, logs, metrics at the time of the bug.

## Step 1: Understand the Issue

1. Read the bug report carefully. Ask clarifying questions if ambiguous.
2. **Always trace the actual code path** — don't surface-level diagnose. The obvious answer isn't always the real answer. Example: [#1129](https://github.com/ComposioHQ/agent-orchestrator/issues/1129) looked like a simple `ao stop` issue but was actually a session lineage/cascade problem.
3. Look at the **latest main** code to trace the root cause:
   - Run `git fetch origin main && git log --oneline origin/main -5` to see current HEAD
   - Record the **commit hash** you're analyzing against
   - Use `grep`, `rg`, or file search to trace the code path
4. **Git archaeology with `git log -S`:** When a CSS property, class name, or code pattern changed and broke something, use:
   ```bash
   git log --oneline -S 'exact-string' -- <file>
   git show <sha> -- <file> | grep -B 5 -A 10 'pattern'
   ```
   This finds which commits introduced or removed specific code. Example: [#1391](https://github.com/ComposioHQ/agent-orchestrator/issues/1391) traced a mobile layout break to a `display: flex` → `display: grid` change that silently broke `flex-direction: column` overrides.
5. **Research upstream dependencies** when the bug involves a library (xterm, node-pty, React, etc.):
   - Check installed vs latest version
   - Search the dependency's GitHub issues for the same symptom
   - Check changelogs for fixes between versions
   - The root cause is often an upstream bug, not your code

## Step 2: Search for Duplicate Issues

```bash
gh issue list --repo <upstream-repo> --state open --search "<keywords>"
```

- Search with multiple keyword combinations (broad first, then narrow)
- If a match is found, go to Step 3. If not, go to Step 4.

## Step 3: Duplicate Found — Comment on Existing Issue

Add a comment with the new report's context:

```bash
gh issue comment <number> --repo <upstream-repo> --body "$(cat <<'EOF'
## New Report

**Reported by:** @<reporter> in [chat link](<url>)

**Date:** <YYYY-MM-DD>

**Checkout:** `<commit-hash>`

<Description of the new report, any additional context, differences from original>

<Screenshot if available>

<Observations — session states, metrics, logs>
EOF
)"
```

## Step 4: No Duplicate — File New Issue

### 4.1 Gather all context
- Source URL (chat thread, issue, etc.)
- Reporter name
- Screenshots
- Commit hash of checkout analyzed
- Root cause analysis with file paths and line numbers
- Live observability data if relevant

### 4.1b Upload screenshots to GitHub

**⛔ NEVER use placeholder URLs.** Every image must be uploaded BEFORE the issue is created. Placeholder URLs (`placeholder-will-upload`, `TODO`, etc.) always result in broken links that need follow-up fixes. See [#1151](https://github.com/ComposioHQ/agent-orchestrator/issues/1151) for an RCA on this pattern.

Create a dedicated branch for issue assets (main is usually protected):

```bash
# Create a branch for issue assets
gh api -X POST repos/<repo>/git/refs \
  -f ref="refs/heads/issue-assets-<issue-number>" \
  -f sha=$(git rev-parse origin/main)
```

Upload the image:

```bash
# Encode image as base64 and upload
IMG_B64=$(base64 -w 0 < /path/to/screenshot.png)
gh api -X PUT "repos/<repo>/contents/.issue-assets/<descriptive-name>.png" \
  -f message="chore: upload screenshot for issue" \
  -f content="$IMG_B64" \
  -f branch="issue-assets-<issue-number>"
```

Extract the `download_url` from the response. Use the raw URL in the issue body:
```
![screenshot](https://raw.githubusercontent.com/<repo>/issue-assets-<N>/.issue-assets/<filename>)
```

**Upload checklist — verify ALL before proceeding to 4.2:**
- [ ] Every image is uploaded to GitHub
- [ ] Every image has a working `raw.githubusercontent.com` URL
- [ ] The URL has been verified — confirm it resolves

### 4.2 Create the issue

```bash
gh issue create --repo <upstream-repo> \
  --title "<clear, concise title>" \
  --body "$(cat <<'EOF'
## Bug

<One-line summary>

**Source:** <url>
**Reported by:** @<reporter>
**Analyzed against:** `<commit-hash>`

## Screenshot

<Embed image or reference>

## Reproduction

1. <step>
2. <step>
3. <step>

## Root Cause

<Analysis with file paths and line numbers>

## Fix

<Suggested fix approach>

## Impact

- <effect 1>
- <effect 2>
EOF
)"
```

### 4.3 Add labels and priority

```bash
gh issue edit <number> --repo <upstream-repo> --add-label "bug"
```

**Priority assignment (use ONLY these labels):**
| Label | Criteria |
|-------|----------|
| `priority: critical` | Data loss, security, system down, all users affected |
| `priority: high` | Core feature broken, no workaround, many users affected |
| `priority: medium` | Feature degraded, workaround exists, some users affected |
| `priority: low` | Cosmetic, edge case, minor inconvenience |

**Available labels (complete list):**
- Priority: `priority: critical`, `priority: high`, `priority: medium`, `priority: low`
- Type: `bug`, `enhancement`
- Workflow: `good-first-issue`, `to-reproduce`, `to-explore`

Do NOT use other labels (no `p0`, `p1`, `p2`, etc.).

If the label doesn't exist:
```bash
gh label create "priority:medium" --repo <upstream-repo> --color "FBCA04" --description "Medium priority"
```

### 4.4 Create a PR for the fix (always attempt this)

**Always try to push a fix PR alongside the issue.**

**Guidelines:**
- **Trivial fix (few lines, obvious change):** Push the PR immediately.
- **Complex fix (needs new code, tests, architectural decisions):** Note the proposed fix in the issue and suggest spawning an agent.
- **Unclear fix:** Don't push a guess. Document findings and flag for investigation.

#### Push a fix via GitHub API

Use the `push_fix_to_github.py` script in this skill's `scripts/` directory:

```bash
OLD_STRING='<old code>' \
NEW_STRING='<new code>' \
python3 .skills/bug-triage/scripts/push_fix_to_github.py \
  <owner/repo> \
  fix/descriptive-branch-name \
  path/to/file.tsx \
  "fix(scope): description" \
  "fix(scope): PR title" \
  "Fixes #<issue-number>

## Summary
<what changed>

## Test
<how to verify>"
```

The script reads the file from GitHub API, applies the replacement, creates a branch, pushes the commit, and opens a PR — entirely via API. No local checkout needed.

**Important notes on the push script:**
- It reads the file from **main**, applies one replacement, and pushes. For multiple changes to the same file, see "Multiple edits" below.
- `OLD_STRING` must match the file byte-for-byte on GitHub. Always verify by fetching the actual file first:
  ```bash
  gh api repos/<repo>/contents/<path>?ref=main -q '.content' | base64 -d
  ```

#### Multiple edits to the same file

The push script only applies one replacement per run (it starts from main's copy each time). For multiple changes, use `execute_code` or a Python script to read from the branch, apply all replacements, then push once:

```python
import base64, json, subprocess

# 1. Get current content FROM BRANCH (not main)
result = subprocess.run(
    ["gh", "api", f"repos/<repo>/contents/<path>?ref=<branch>", "--jq", ".content"],
    capture_output=True, text=True
)
content = base64.b64decode(result.stdout.strip()).decode("utf-8")

# 2. Apply all replacements
content = content.replace(old1, new1).replace(old2, new2)

# 3. Get file SHA and push
sha_result = subprocess.run(
    ["gh", "api", f"repos/<repo>/contents/<path>?ref=<branch>", "--jq", ".sha"],
    capture_output=True, text=True
)
file_sha = sha_result.stdout.strip()

payload = {
    "message": "fix: all changes",
    "content": base64.b64encode(content.encode()).decode(),
    "sha": file_sha,
    "branch": "<branch>"
}
with open("/tmp/push.json", "w") as f:
    json.dump(payload, f)
subprocess.run(["gh", "api", "-X", "PUT", f"repos/<repo>/contents/<path>", "--input", "/tmp/push.json"])
```

### 4.5 Post confirmation back

Report back with:
- Issue URL
- PR URL (if created)
- Labels applied
- Brief summary of root cause
- Whether a fix agent was suggested

## NPM Package Regression Diffing

When a regression occurs after upgrading npm packages, diff the **actual published packages**:

```bash
# Download and extract both versions
mkdir -p /tmp/ao-diff/{v1,v2}
curl -sL https://registry.npmjs.org/@scope/pkg/-/pkg-OLD.tgz | tar xz -C /tmp/ao-diff/v1
curl -sL https://registry.npmjs.org/@scope/pkg/-/pkg-NEW.tgz | tar xz -C /tmp/ao-diff/v2

# Diff server-side code
diff /tmp/ao-diff/v1/package/dist-server/file.js /tmp/ao-diff/v2/package/dist-server/file.js

# Diff client-side chunks
diff -rq /tmp/ao-diff/v1/package/.next/static/chunks/ /tmp/ao-diff/v2/package/.next/static/chunks/
```

**Why this matters:** The npm package may include pre-built bundles that differ from what `pnpm build` produces locally. The only authoritative source of truth is what's published. Example: [PR #1608](https://github.com/ComposioHQ/agent-orchestrator/pull/1608) had a scroll regression where source analysis led to wrong theories, but diffing the actual npm packages showed the **only change** was a single `=` prefix on a tmux `set-option` call.

## Remote Code Inspection (repo not cloned locally)

When the repo isn't available locally, triage fully using `gh api`:

```bash
# List all files
gh api repos/{owner}/{repo}/git/trees/main?recursive=1 --jq '.tree[].path'

# Read a file
gh api repos/{owner}/{repo}/contents/{path} --jq '.content' | base64 -d

# Search for a string across the codebase
gh search code "search term" --repo {owner}/{repo} --json path --jq '.[].path'

# Find which commits touched a file
gh api "repos/{owner}/{repo}/commits?path={path}&per_page=10" --jq '.[] | "\(.sha[0:8]) \(.commit.message | split("\n")[0])"'

# Read a file at a specific commit
gh api "repos/{owner}/{repo}/contents/{path}?ref={sha}" --jq '.content' | base64 -d
```

## Pitfalls

- **Reporter ≠ person who tagged you.** The person who escalated the bug is often NOT the reporter. Always attribute to the actual reporter.
- **Always file on the upstream org repo**, not personal forks.
- **Always record the commit hash** you analyzed — code changes fast.
- **Always trace the code path** before speculating about root cause.
- **Include the source link and reporter name** — maintain the chain of context.
- **⛔ NEVER use placeholder image URLs.** Upload images BEFORE creating the issue. Get real URLs, then write the issue body.
- **GitHub issue is mandatory** — every triaged bug gets a GitHub issue, even if the fix is trivial. No exceptions.
- **`gh api --jq .content` truncates large files** — files over ~100KB get corrupted. Use local git for large files.
- **Push script `OSError: Argument list too long`** — long commit messages exceed OS arg limits. Use `execute_code` with JSON payloads instead.
- **Exact OLD_STRING matching** — `OLD_STRING` must match the file byte-for-byte on GitHub. Code traced locally may differ from what's on `origin/main`. Always fetch from GitHub API first.
- **Adding new required fields to shared TypeScript interfaces.** New fields on exported interfaces (`Session`, `SessionSpawnConfig`, etc.) MUST be optional (`field?: Type`). Downstream packages use `Partial<X>` spread — required fields break CI across all plugins. Progression: `field: T | null` → fails, `field?: T | null` → works. Example: [PR #1523](https://github.com/ComposioHQ/agent-orchestrator/pull/1523) hit this exact pattern.
