# PR Review Comments tab + editable nudge templates — Design

Date: 2026-07-09
Status: Approved for planning
Branch/PR: `bugfix/PROJ-2272-gitlab-mr-detection` (PR #36)

## Summary

Replace AO's automatic "unresolved review comment → nudge the worker" behavior
with a **Comments** tab in the session inspector rail where the human reads
review threads GitHub-style, replies, resolves, and *manually* dispatches them to
the worker with a default (or customized) prompt. Additionally, make every
runtime nudge message AO sends into a worker's pane editable from the global
Settings page.

The bulk of the comment data is already captured and persisted; this feature is
mostly a read/write API surface + a frontend tab + a behavior change + a
templating layer.

## Goals

- A **Comments** tab (4th tab beside Summary · Reviews · Browser) showing review
  threads per PR, GitHub-style: author, body, and the code the comment anchors
  to (diff hunk), with expand-to-full-file.
- **Reply** to a thread from within AO.
- **Resolve** a thread from within AO (also implements the currently-stubbed
  `resolve-comments` endpoint).
- **Send to worker**: a split-button that dispatches unresolved comments to the
  worker with a default prompt; a ▾ dropdown opens a small popover to add extra
  instructions before sending.
- **Stop** the automatic human-review nudge — dispatch becomes manual (via the
  button). Board status ("Changes Requested") is unaffected.
- Make **all runtime nudge templates** editable from the global Settings page.

## Non-goals (YAGNI)

- No avatar/image fetching — render author initials.
- No creating comments on arbitrary lines — only replying to existing threads.
- No editing/deleting others' comments.
- No new resolved-thread history beyond what the observer already persists.
- No change to how review *decision* / board status is computed.

## Background: what already exists (reused, not rebuilt)

- **Persistence.** `pr_review_threads` (`thread_id, path, line, resolved, is_bot,
  semantic_hash, updated_at`) and `pr_comment` (`comment_id, author, file, line,
  body, resolved, created_at, thread_id, url, is_bot`) hold every thread/comment
  for **both** GitHub and GitLab. The observer writes *all* threads (resolved +
  unresolved, human + bot) with flags — `observer.go` ~1062–1074. No schema
  change is needed to display threads.
- **Thread identifiers usable for writes.** GitHub thread `id` is the GraphQL
  node id (directly usable by `resolveReviewThread` /
  `addPullRequestReviewThreadReply`). GitLab thread `id` is the discussion id
  (usable by the discussions notes/resolve REST endpoints). Both are already
  persisted as `thread_id`.
- **Worktree on disk.** Each session has its git worktree checked out — the
  worker's branch — so code context is available locally without hitting the
  provider.
- **Send channel.** `messenger.Send(ctx, sessionID, msg)` injects a message into
  a worker's pane (used by the existing nudges). `POST /sessions/{id}/send`
  exists too, but the new dispatch uses a dedicated endpoint (below) so prompt
  construction + sanitization stay server-side.
- **Prompt override machinery.** `internal/prompts` (kinds: orchestrator /
  worker / reviewer, with defaults + coordination floors) and
  `internal/promptoverrides` (a JSON store: `Overrides{Base map[Kind]string}`,
  `Get/SetBase/ClearBase`) back a global Settings editor
  (`GET/PUT/DELETE /api/v1/settings/prompts/{kind}`,
  `SystemPromptsSection.tsx` / `GlobalSettingsForm.tsx`, route
  `_shell.settings.tsx`). The new message-templates surface mirrors this.

### Current auto-nudge being changed (`internal/lifecycle/reactions.go`)

```go
if o.Review == domain.ReviewChangesRequest || hasUnresolvedComments(o.Comments) {
	comments, sig := reviewContent(o.Comments)
	msg := "A reviewer left feedback on your PR. Address it and push."
	if comments != "" {
		msg += "\n\n" + comments
	}
	if sig == "" {
		sig = string(o.Review)
	}
	return m.sendOnce(ctx, id, o.URL, "review:"+o.URL, sig, msg, reviewMaxNudge)
}
```

The worker currently receives one hardcoded sentence + the sanitized comment
bodies. This branch's automatic `Send` is removed; `hasUnresolvedComments` /
`reviewContent` are reused by the new dispatch endpoint.

Other nudges (CI failing `reactions.go:152`, merge conflict `reactions.go:187`,
tracker-bot `reactions.go:464`, AO-internal-reviewer `reactions.go:70-85` /
`reactions.go:209-217`) stay automatic but are refactored to render from editable
templates.

## Decisions (resolved with the user)

1. **Code context source:** git-backed, on-demand from the session worktree — no
   DB storage, no per-provider hunk capture, provider-agnostic, and full-file
   expand is free. (Supersedes an earlier "store hunk in DB" idea.)
2. **Auto-nudge:** fully manual for human review feedback. CI-failure,
   merge-conflict, and AO-internal-reviewer nudges stay automatic.
3. **Resolve:** included in v1 (build the thread write-path for reply + resolve;
   implement the `resolve-comments` stub).
4. **Editable templates:** *all* runtime nudge templates editable via a new
   global Settings "Message templates" section.

## Architecture

Six work items. Each is independently testable.

### 1. Read API — list review threads (DB only)

`GET /api/v1/sessions/{sessionId}/pr-comments`

Reads `pr_review_threads` + `pr_comment` for every PR of the session and returns
threads grouped by PR. Pure DB read; fast; no provider calls.

Response shape (illustrative):

```jsonc
{
  "prs": [
    {
      "prUrl": "https://github.com/.../pull/36",
      "provider": "github",
      "number": 36,
      "threads": [
        {
          "threadId": "PRRT_kw...",
          "path": "backend/internal/adapters/scm/gitlab/observer_provider_test.go",
          "line": 172,
          "resolved": false,
          "isBot": false,
          "comments": [
            {
              "id": "PRRC_kw...",
              "author": "f1uke",
              "body": "remove this comment",
              "url": "https://github.com/.../#discussion_r...",
              "resolved": false,
              "isBot": false,
              "createdAt": "2026-07-09T10:00:00Z"
            }
          ]
        }
      ]
    }
  ]
}
```

Unresolved, non-bot threads are the primary content; resolved threads are
returned too (frontend collapses them). Bot threads may be filtered client-side
or included behind a toggle — default: show human threads, collapse resolved.

### 2. Diff-context API — code the comment anchors to (git-backed, on-demand)

`GET /api/v1/sessions/{sessionId}/diff-context?prUrl=&path=&line=&mode=hunk|file`

- `mode=hunk` (default): run `git diff <base>..<head> -- <path>` in the session
  worktree, locate the hunk containing `line`, return its lines with +/-
  classification (added / removed / context) — reproduces the screenshot.
- `mode=file`: run `git show <head>:<path>` — return the whole file for the
  **expand** action; frontend highlights `line`.

Inputs: base/head come from the PR row (source/target branch or head SHA). The
handler resolves the session's worktree path, shells out to git via
`aoprocess.CommandContext`, and returns structured lines:

```jsonc
{ "path": "...", "mode": "hunk",
  "lines": [ { "n": 169, "kind": "add", "text": "..." }, ... ],
  "truncated": false }
```

Fallbacks: if the commit/file is not present locally (rare — it is the worker's
branch) or git fails, return `available:false` and the frontend degrades to a
`file:line` link out to GitHub/GitLab.

Security: `path` is validated to stay within the repo (no `..`/absolute
traversal); git is invoked with an explicit `--` separator and the resolved
worktree as cwd; SHAs/refs are passed as separate args (no shell string
interpolation).

### 3. Thread write-path — reply + resolve (both providers)

New optional provider capability, discovered on the concrete provider and routed
by `composite.Provider` by `repo.Provider`:

```go
// internal/observe/scm (or ports) — optional capability
type ReviewThreadWriter interface {
    ReplyToThread(ctx context.Context, ref ports.SCMPRRef, threadID, body string) (ports.SCMReviewCommentObservation, error)
    ResolveThread(ctx context.Context, ref ports.SCMPRRef, threadID string) error
}
```

- **GitHub** (`internal/adapters/scm/github`): GraphQL mutations
  `addPullRequestReviewThreadReply(input:{pullRequestReviewThreadId, body})` and
  `resolveReviewThread(input:{threadId})`.
- **GitLab** (`internal/adapters/scm/gitlab`): REST
  `POST /projects/:id/merge_requests/:iid/discussions/:discussion_id/notes` and
  `PUT /projects/:id/merge_requests/:iid/discussions/:discussion_id?resolved=true`.

Writes use the same token source the observer already uses, so replies post as
the authenticated user (e.g. via `glab`/`gh` token).

APIs (on `PRsController`):

- `POST /api/v1/prs/{id}/threads/{threadId}/reply` — body `{ "body": "..." }` →
  calls `ReplyToThread`.
- `POST /api/v1/prs/{id}/resolve-comments` — **implement the existing stub**
  (`internal/service/pr/action_service.go:44`). Accept thread ids (extend
  `ResolveCommentsRequest` with `ThreadIDs`, keeping `CommentIDs` for
  compatibility — comment ids map to their `thread_id` via `pr_comment`). The
  per-thread "Resolve conversation" button calls this with a single thread id.
  `ErrNothingToResolve` is returned when there is nothing unresolved.

The service resolves `{id}` → `pr_url` the same way `Merge`/`ResolveComments`
already do, then loads the `SCMPRRef` (repo + number) to call the provider.

### 4. Send-to-worker API — manual dispatch (server renders template)

`POST /api/v1/sessions/{sessionId}/pr-comments/send-to-worker`
body `{ "prUrl": "...", "threadIds": ["..."]?, "note": "..."? }`

- Loads the target unresolved comments (all for the PR, or the given
  `threadIds`), builds the comment block via the existing `reviewContent`
  (sanitized), renders the `review-comment-dispatch` template with `{{.Comments}}`,
  appends the optional user `note`, and calls `messenger.Send`.
- Prompt construction + `SanitizeControlChars` stay server-side — identical
  safety to the old auto-nudge.
- Idempotency: unlike the auto-nudge (`sendOnce` dedup), a manual dispatch always
  sends (the human intends it); no dedup signature.

### 5. Editable message templates (global Settings)

A new small templating layer sitting beside `internal/prompts`.

- **Template registry** (`internal/messagetemplates`, name TBD): enumerates
  template names, their built-in default text, their documented placeholder set,
  and a `Render(name, data)` using Go `text/template`. Unknown/blank override →
  fall back to default.
- **Persistence:** extend `promptoverrides.Overrides` with
  `Templates map[string]string` and add `GetTemplate/SetTemplate/ClearTemplate`
  (mirrors `Base`). Stored in the same JSON file.
- **API:** `GET /api/v1/settings/message-templates`,
  `PUT /api/v1/settings/message-templates/{name}`,
  `DELETE /api/v1/settings/message-templates/{name}` (parallel to
  `settings/prompts`). `GET` returns each template's `{name, default, custom,
  placeholders}`.
- **Frontend:** a **Message templates** section in `GlobalSettingsForm.tsx`
  reusing the `SystemPromptsSection` editor pattern (textarea, default preview,
  reset-to-default), listing each template with its documented placeholders.
- **Rendering call sites:** `reactions.go` nudges and the send-to-worker endpoint
  render via the registry instead of hardcoded strings.

Templates and their placeholders (Go `text/template`):

| Name | Default (abridged) | Placeholders |
|------|--------------------|--------------|
| `review-comment-dispatch` | `A reviewer left feedback on your PR. Address it and push.\n\n{{.Comments}}` | `.Comments` |
| `ci-failing` | `CI is failing on your PR. Review the output below and push a fix.{{if .LogTail}}\n\nFailing output:\n{{.LogTail}}{{end}}` | `.LogTail` |
| `merge-conflict` | `Your PR has merge conflicts. Rebase onto the base branch and resolve them.` | (none) |
| `tracker-bot-comment` | `A bot left a new comment on your tracker issue. Address it and update the session.{{if .Comments}}\n\n{{.Comments}}{{end}}` | `.Comments` |
| `ao-reviewer-batch` | current `reactions.go:70-85` text, `{{range .Reviews}}…{{end}}` | `.Count`, `.Reviews[]{ PRURL, Verdict, TargetSHA, ReviewID, Body }` |
| `ao-reviewer-single` | current `reactions.go:209-217` text | `.PRURL`, `.Verdict`, `.ReviewID`, `.Body` |

The user's optional `note` in send-to-worker is appended by the endpoint after
rendering (not a template placeholder) to keep the template simple.

Rendering safety: `text/template` output for nudge messages is passed through
`SanitizeControlChars` before `messenger.Send`, exactly as today. Comment/log
placeholder values are already sanitized before injection. A malformed
user-edited template that fails to parse/execute falls back to the built-in
default (logged), so a bad edit can never drop a nudge.

### 6. Behavior change — manual dispatch (`reactions.go`)

Remove the automatic `Send` in the
`ReviewChangesRequest || hasUnresolvedComments` branch. Keep the branch's data
plumbing that the observer/board rely on unchanged (board still shows "Changes
Requested"). CI-failure, merge-conflict, tracker-bot, and AO-internal-reviewer
nudges remain automatic (now template-rendered). Update the reaction tests to
assert **no** human-review nudge fires on unresolved comments.

### 7. Frontend — Comments tab (`SessionInspector.tsx`)

- Add `"comments"` to `InspectorView` and a 4th entry to `VIEWS` (icon: chat
  bubble / message-square).
- `CommentsView`: react-query against `/sessions/{id}/pr-comments`; refetch on
  interval while visible and/or on the existing SCM SSE events (follow the
  Reviews tab pattern).
- Rendering: per-PR group → threads. Each thread:
  - file-path header (with `resolved`/`Outdated` badge as applicable),
  - collapsible **diff hunk** lazily loaded from `/diff-context` (`mode=hunk`),
    with an **expand** control that loads `mode=file`,
  - comments: author (initials avatar) · relative time · body,
  - **Reply** box → `POST …/threads/{threadId}/reply`,
  - **Resolve conversation** button → `POST …/resolve-comments`,
  - unresolved threads open by default, resolved collapsed.
- **Send to worker** split-button (per PR): primary = dispatch all unresolved;
  ▾ dropdown opens a popover with a textarea for extra instructions, then sends
  → `POST …/pr-comments/send-to-worker`.
- Empty state: "No review comments yet."
- Built from existing `components/ui/*` primitives per DESIGN.md; matches the
  agent-orchestrator web app look.

## Data flow

```
GitHub/GitLab ──poll──> observer ──persist──> pr_review_threads / pr_comment
                                                     │
Comments tab ──GET /pr-comments──────────────────────┘ (DB read)
Comments tab ──GET /diff-context──> git (worktree) ──> hunk / full file
Comments tab ──POST reply/resolve──> PR service ──> provider write ──> GitHub/GitLab
Comments tab ──POST send-to-worker─> dispatch svc ──render template──> messenger ──> worker pane
Settings ─────GET/PUT message-templates──> promptoverrides store (JSON)
reactions.go nudges ──render template (override|default)──> messenger ──> worker pane
```

## Testing (TDD)

Backend (Go, table/httptest style already used in the repo):
- `ReplyToThread` / `ResolveThread` for GitHub (fake GraphQL server) and GitLab
  (fake REST server): correct endpoint, payload, error mapping.
- Read service: groups threads by PR; includes resolved with flags.
- Diff-context: against a temp git repo — hunk slicing around a line, full-file
  mode, path-traversal rejection, missing-commit fallback.
- Send-to-worker service: builds prompt from unresolved comments, applies
  template override vs default, appends note, sanitizes, calls messenger; always
  sends (no dedup).
- `resolve-comments`: stub → real; `ErrNothingToResolve` path.
- Message-template registry: default render, override render, malformed override
  falls back to default; each placeholder set.
- `promptoverrides`: `Templates` get/set/clear round-trips + JSON persistence.
- `reactions.go`: asserts human-review nudge no longer fires; CI/conflict/
  tracker/AO-reviewer still fire and now render via templates.

Frontend (vitest + testing-library, mocked API, `VITE_NO_ELECTRON` mock path):
- CommentsView renders threads/hunks; expand loads full file.
- Reply submits and optimistically appends.
- Resolve calls endpoint and collapses the thread.
- Send-to-worker: primary sends default; ▾ popover appends note and sends.
- Message-templates settings section: edit, save, reset-to-default.

Repo gates per `AGENTS.md`: backend `go test` + lint; frontend tests + lint;
revert `routeTree.gen.ts` / `pnpm-lock.yaml` churn.

## Security considerations

- **Comment bodies & CI logs** are attacker-influenced (anyone who can comment)
  and reach the worker pane and the tab — sanitized with `SanitizeControlChars`
  before `messenger.Send`; the frontend renders them as text, not HTML.
- **Diff-context git access** validates `path` against traversal, runs git with
  the resolved worktree cwd and `--` arg separation, and never interpolates
  user input into a shell string.
- **Provider writes** use the existing authenticated token; replies post as the
  configured user identity.
- **User-edited templates** cannot drop a nudge: parse/execute failure falls back
  to the built-in default (logged).

## Rollout / suggested phases

1. Message-template registry + `promptoverrides.Templates` + settings API/UI;
   refactor existing `reactions.go` nudges onto templates (no behavior change
   yet). Ships value independently.
2. Read API (`/pr-comments`) + diff-context API + Comments tab (read-only view
   with expand). No writes yet.
3. Thread write-path (reply + resolve, both providers) + APIs + tab actions;
   implement `resolve-comments`.
4. Send-to-worker endpoint + split-button/dropdown; remove the human-review
   auto-nudge (manual dispatch replaces it).

## Open questions

None blocking. Minor: exact icon for the tab; whether to expose a bot-thread
toggle (default hidden). Both are cosmetic and decided during implementation.
