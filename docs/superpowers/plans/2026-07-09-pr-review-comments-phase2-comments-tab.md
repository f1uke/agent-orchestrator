# PR Review Comments — Phase 2: Comments tab (read side) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a read-only **Comments** tab to the session inspector that shows each PR's review threads GitHub-style — author, body, and the code the comment anchors to (a diff hunk, expandable to the whole file) — backed by two new read APIs.

**Architecture:** A DB-only read endpoint `GET /sessions/{id}/pr-comments` composes the existing per-PR store reads (`ListPRsBySession` + `ListPRComments` + `ListPRReviewThreads`) into threads-with-comments grouped by PR. A git-backed endpoint `GET /sessions/{id}/diff-context` resolves the session's worktree + the PR's base/head SHAs, runs `git diff`/`git show` in the worktree, and returns classified lines (a unified-diff hunk parser lives in a pure `internal/diffhunk` package). The frontend adds a 4th inspector tab that renders threads and lazily loads code context with expand-to-file.

**Tech Stack:** Go (backend; `os/exec` via `aoprocess`, code-first OpenAPI via `specgen`), React + TanStack Query + openapi-fetch (frontend), vitest + testing-library.

## Global Constraints

- App state under `~/.ao` only; the session worktree is `SessionRecord.Metadata.WorkspacePath`.
- API is **code-first**: edit `backend/internal/httpd/controllers/dto.go` + `backend/internal/httpd/apispec/specgen/build.go` (operation registry in `sessionOperations()` + `schemaNames` map), then `npm run api` (repo root) regenerates `backend/internal/httpd/apispec/openapi.yaml` and `frontend/src/api/schema.ts`. Never hand-edit those two generated files. Routes must stay 1:1 with build.go operations (`internal/httpd/apispec/parity_test.go` enforces it). `go test ./internal/httpd/...` runs drift + parity guards.
- Backend gate (repo root): `npm run lint` (= `cd backend && go test ./... && golangci-lint run`). Inside this live AO session, CLI e2e tests (`TestSpawn*`, package `internal/cli/...`) fail spuriously — run TARGETED packages, not `./...`; `go build ./...` is the compile check.
- Frontend gates (from `frontend/`): `npm run test` (vitest) + `npm run typecheck`. No frontend lint script; format with tabs per `.prettierrc`.
- After frontend build/test, revert incidental `routeTree.gen.ts` / `pnpm-lock.yaml` / `pnpm-workspace.yaml` churn — do not commit it.
- **Read-only phase:** NO reply/resolve/send-to-worker and NO change to the auto-nudge here — those are Phases 3–4. This phase only reads and displays.
- Git subprocess safety: pass the worktree via `cmd.Dir` (or `-C <path>`), pass refs/paths as separate args (never shell-interpolated), and validate the file path with `preview.ConfinedPath` before use.
- Commit after each task. Branch: `bugfix/PROJ-2272-gitlab-mr-detection` (PR #36).

---

## File Structure

Created:

- `backend/internal/diffhunk/diffhunk.go` — pure unified-diff hunk parser (`Line`, `Kind`, `HunkForLine`).
- `backend/internal/diffhunk/diffhunk_test.go`.
- `backend/internal/service/session/pr_comments.go` — `ListPRCommentThreads` + its return types.
- `backend/internal/service/session/pr_comments_test.go`.
- `backend/internal/service/session/diff_context.go` — `DiffContext` (git runner + parser) + return types.
- `backend/internal/service/session/diff_context_test.go`.
- `frontend/src/renderer/hooks/useSessionPRComments.ts` — react-query hook.
- `frontend/src/renderer/components/CommentsView.tsx` — the tab body.
- `frontend/src/renderer/components/CommentsView.test.tsx`.
- `frontend/src/renderer/components/DiffHunk.tsx` — hunk render + expand.
- `frontend/src/renderer/components/DiffHunk.test.tsx`.

Modified:

- `backend/internal/httpd/controllers/dto.go` — response DTOs.
- `backend/internal/httpd/controllers/sessions.go` — 2 handlers, 2 routes, `SessionService` interface additions.
- `backend/internal/httpd/controllers/sessions_test.go` — handler tests.
- `backend/internal/httpd/apispec/specgen/build.go` — 2 operations + `schemaNames`.
- `backend/internal/service/session/service.go` — (only if a new Store method is needed — it is NOT; see Task 4).
- `frontend/src/renderer/components/SessionInspector.tsx` — add `"comments"` to `InspectorView` + `VIEWS`, render `<CommentsView />`.
- Generated (via `npm run api`): `openapi.yaml`, `frontend/src/api/schema.ts`.

Reference facts (verified against the tree):

- `session.Service` already has store methods `GetSession`, `ListPRsBySession`, `ListPRComments`, `ListPRReviewThreads` (interface at `internal/service/session/service.go`). No new store plumbing.
- `domain.PullRequest`: `URL, Number, Provider, HTMLURL, HeadSHA, BaseSHA, SourceBranch, TargetBranch, Draft, Merged, Closed`.
- `domain.PullRequestComment`: `ThreadID, ID, Author, File, Line, Body, URL, Resolved, IsBot, CreatedAt`.
- `domain.PullRequestReviewThread`: `ThreadID, Path, Line, Resolved, IsBot, SemanticHash, UpdatedAt`.
- `SessionRecord.Metadata.WorkspacePath` = the git worktree dir; read via `store.GetSession`.
- `preview.ConfinedPath(workspacePath, rel) (abs string, ok bool)` at `internal/preview/entry.go` (imported as `previewutil` in controllers).
- Git idiom: `aoprocess.CommandContext(ctx, "git", "-C", dir, ...)` then `.Output()` (see `internal/session_manager/branchname.go:199`).
- Endpoint template: `build.go` `sessionOperations()`, entry `listSessionPRs` (`GET /sessions/{sessionId}/pr`, `pathParams: []any{controllers.SessionIDParam{}}`). `SessionIDParam` reused (no new param type).

---

## Task 1: `internal/diffhunk` — unified-diff hunk parser

**Files:**

- Create: `backend/internal/diffhunk/diffhunk.go`
- Test: `backend/internal/diffhunk/diffhunk_test.go`

**Interfaces:**

- Produces:
  - `type Kind string` with `KindContext`, `KindAdd`, `KindDel`.
  - `type Line struct { Kind Kind; OldLine int; NewLine int; Text string }` (line numbers 1-based; 0 where N/A — `OldLine==0` for adds, `NewLine==0` for deletions).
  - `func HunkForLine(diff string, newLine int) (lines []Line, found bool)` — parses `git diff` unified output and returns the single hunk whose body covers `newLine` on the new side.

- [ ] **Step 1: Write the failing test**

Create `backend/internal/diffhunk/diffhunk_test.go`:

```go
package diffhunk

import "testing"

const sampleDiff = `diff --git a/foo.go b/foo.go
index 111..222 100644
--- a/foo.go
+++ b/foo.go
@@ -10,6 +10,7 @@ func foo() {
 	ctx := 10
 	ctx2 := 11
-	old := 12
+	added := 12
+	added2 := 13
 	ctx3 := 14
 	ctx4 := 15
`

func TestHunkForLineFindsCoveringHunk(t *testing.T) {
	// New line 13 is the "added2 := 13" line.
	lines, found := HunkForLine(sampleDiff, 13)
	if !found {
		t.Fatal("expected to find a hunk covering new line 13")
	}
	// The added line at new 13 must be classified add.
	var got *Line
	for i := range lines {
		if lines[i].Kind == KindAdd && lines[i].NewLine == 13 {
			got = &lines[i]
		}
	}
	if got == nil {
		t.Fatalf("no add line at new 13 in %+v", lines)
	}
	if got.Text != "	added2 := 13" {
		t.Fatalf("add text = %q", got.Text)
	}
	// The deletion must be present with OldLine set, NewLine 0.
	sawDel := false
	for _, l := range lines {
		if l.Kind == KindDel {
			sawDel = true
			if l.NewLine != 0 || l.OldLine != 12 {
				t.Fatalf("del line numbering wrong: %+v", l)
			}
		}
	}
	if !sawDel {
		t.Fatal("expected a deletion line in the hunk")
	}
	// First context line: old 10 / new 10.
	if lines[0].Kind != KindContext || lines[0].OldLine != 10 || lines[0].NewLine != 10 {
		t.Fatalf("first line = %+v, want context 10/10", lines[0])
	}
}

func TestHunkForLineContextLineMatch(t *testing.T) {
	// New line 10 is a context line ("ctx := 10").
	lines, found := HunkForLine(sampleDiff, 10)
	if !found || len(lines) == 0 {
		t.Fatalf("expected hunk for context new line 10")
	}
}

func TestHunkForLineNotFound(t *testing.T) {
	if _, found := HunkForLine(sampleDiff, 9999); found {
		t.Fatal("expected no hunk for line outside any hunk")
	}
	if _, found := HunkForLine("", 1); found {
		t.Fatal("empty diff has no hunks")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/diffhunk/`
Expected: FAIL — package/functions undefined.

- [ ] **Step 3: Write minimal implementation**

Create `backend/internal/diffhunk/diffhunk.go`:

```go
// Package diffhunk parses `git diff` unified output into classified lines and
// extracts the single hunk that covers a given new-side line number. It is pure
// (no I/O) so the SCM comment code-context feature can render a diff hunk around
// a review comment's anchor line.
package diffhunk

import (
	"strconv"
	"strings"
)

// Kind classifies one diff line.
type Kind string

const (
	// KindContext is an unchanged line present on both sides.
	KindContext Kind = "context"
	// KindAdd is a line added on the new side (OldLine == 0).
	KindAdd Kind = "add"
	// KindDel is a line removed from the old side (NewLine == 0).
	KindDel Kind = "del"
)

// Line is one classified diff line with 1-based old/new line numbers (0 where
// the line does not exist on that side). Text excludes the leading +/-/space.
type Line struct {
	Kind    Kind
	OldLine int
	NewLine int
	Text    string
}

// HunkForLine parses the unified diff for a single file (the output of
// `git diff <base>..<head> -- <path>`) and returns the lines of the one hunk
// whose body covers newLine on the new side. found is false when no hunk covers
// newLine (e.g. the anchor is in an unchanged region far from any change).
func HunkForLine(diff string, newLine int) ([]Line, bool) {
	rows := strings.Split(diff, "\n")
	i := 0
	for i < len(rows) {
		if !strings.HasPrefix(rows[i], "@@") {
			i++
			continue
		}
		oldCur, newCur, ok := parseHunkHeader(rows[i])
		if !ok {
			i++
			continue
		}
		i++
		body := make([]Line, 0, 16)
		covers := false
		for i < len(rows) {
			r := rows[i]
			if strings.HasPrefix(r, "@@") || strings.HasPrefix(r, "diff ") ||
				strings.HasPrefix(r, "--- ") || strings.HasPrefix(r, "+++ ") ||
				strings.HasPrefix(r, "index ") {
				break // next hunk or next file header — end this hunk body
			}
			if r == "" {
				i++
				continue // trailing blank from the split
			}
			switch r[0] {
			case ' ':
				body = append(body, Line{Kind: KindContext, OldLine: oldCur, NewLine: newCur, Text: r[1:]})
				if newCur == newLine {
					covers = true
				}
				oldCur++
				newCur++
			case '+':
				body = append(body, Line{Kind: KindAdd, NewLine: newCur, Text: r[1:]})
				if newCur == newLine {
					covers = true
				}
				newCur++
			case '-':
				body = append(body, Line{Kind: KindDel, OldLine: oldCur, Text: r[1:]})
				oldCur++
			case '\\':
				// "\ No newline at end of file" — metadata, ignore.
			default:
				// Unexpected content; stop consuming this hunk defensively.
				i = len(rows)
			}
			i++
		}
		if covers {
			return body, true
		}
	}
	return nil, false
}

// parseHunkHeader reads "@@ -oldStart[,oldCount] +newStart[,newCount] @@ ..."
// and returns the 1-based old/new start line numbers.
func parseHunkHeader(h string) (oldStart, newStart int, ok bool) {
	if !strings.HasPrefix(h, "@@ ") {
		return 0, 0, false
	}
	rest := h[3:]
	end := strings.Index(rest, " @@")
	if end < 0 {
		return 0, 0, false
	}
	parts := strings.Fields(rest[:end]) // ["-10,6", "+10,7"]
	if len(parts) != 2 {
		return 0, 0, false
	}
	o, ok1 := parseStart(parts[0], '-')
	n, ok2 := parseStart(parts[1], '+')
	if !ok1 || !ok2 {
		return 0, 0, false
	}
	return o, n, true
}

// parseStart reads a "<sign><start>[,<count>]" token, returning start.
func parseStart(tok string, sign byte) (int, bool) {
	if len(tok) == 0 || tok[0] != sign {
		return 0, false
	}
	tok = tok[1:]
	if c := strings.IndexByte(tok, ','); c >= 0 {
		tok = tok[:c]
	}
	n, err := strconv.Atoi(tok)
	if err != nil {
		return 0, false
	}
	return n, true
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/diffhunk/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/diffhunk/
git commit -m "feat(diffhunk): unified-diff hunk parser for a target line

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 2: `session.Service.ListPRCommentThreads`

**Files:**

- Create: `backend/internal/service/session/pr_comments.go`
- Test: `backend/internal/service/session/pr_comments_test.go`

**Interfaces:**

- Consumes (already on the service's `store`): `GetSession`, `ListPRsBySession`, `ListPRComments`, `ListPRReviewThreads`.
- Produces (package `session`):
  - `type PRThreadComment struct { ID, Author, Body, URL string; Resolved, IsBot bool; CreatedAt time.Time }`
  - `type PRCommentThread struct { ThreadID, Path string; Line int; Resolved, IsBot bool; Comments []PRThreadComment }`
  - `type PRCommentGroup struct { PRURL, HTMLURL, Provider string; Number int; HeadSHA string; Threads []PRCommentThread }`
  - `func (s *Service) ListPRCommentThreads(ctx context.Context, id domain.SessionID) ([]PRCommentGroup, error)`

Behavior: for each PR of the session, build threads from `ListPRReviewThreads`, attach each comment (`ListPRComments`) to its `ThreadID`; a comment whose `ThreadID` matches no known thread gets a synthesized thread from its `File`/`Line` so no comment is dropped; threads are ordered by the thread list order then synthesized ones; comments within a thread stay oldest-first (store already orders by `created_at`). Unknown session → `apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")` (mirror `ListPRSummaries`).

- [ ] **Step 1: Write the failing test**

Create `backend/internal/service/session/pr_comments_test.go`. First read `internal/service/session/pr_summary_test.go` to reuse its fake store + `newTestService` harness (mirror that construction exactly); the snippet below assumes a fake store with settable `prs`, `comments[prURL]`, `threads[prURL]` and a constructor like `newServiceWithStore(t, fake)` — adapt names to the real harness:

```go
func TestListPRCommentThreads_GroupsCommentsUnderThreads(t *testing.T) {
	fake := newFakeStore() // adapt to the real fake in pr_summary_test.go
	fake.putSession("s1")
	fake.prs["s1"] = []domain.PullRequest{{
		URL: "https://gh/pr/1", Number: 1, Provider: "github",
		HTMLURL: "https://gh/pr/1", HeadSHA: "abc",
	}}
	fake.threads["https://gh/pr/1"] = []domain.PullRequestReviewThread{
		{ThreadID: "T1", Path: "a.go", Line: 10, Resolved: false, IsBot: false},
	}
	fake.comments["https://gh/pr/1"] = []domain.PullRequestComment{
		{ThreadID: "T1", ID: "C1", Author: "alice", Body: "fix this", File: "a.go", Line: 10},
		{ThreadID: "T1", ID: "C2", Author: "bob", Body: "agreed", File: "a.go", Line: 10},
		{ThreadID: "T2", ID: "C3", Author: "carol", Body: "orphan", File: "b.go", Line: 5}, // thread not in list
	}

	svc := newServiceWithStore(t, fake)
	groups, err := svc.ListPRCommentThreads(context.Background(), "s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 || groups[0].PRURL != "https://gh/pr/1" || groups[0].HeadSHA != "abc" {
		t.Fatalf("groups = %+v", groups)
	}
	threads := groups[0].Threads
	if len(threads) != 2 {
		t.Fatalf("want 2 threads (T1 + synthesized T2), got %d: %+v", len(threads), threads)
	}
	// T1 keeps both comments oldest-first.
	if threads[0].ThreadID != "T1" || len(threads[0].Comments) != 2 ||
		threads[0].Comments[0].ID != "C1" || threads[0].Comments[1].ID != "C2" {
		t.Fatalf("T1 = %+v", threads[0])
	}
	// Orphan comment gets a synthesized thread anchored to its file/line.
	if threads[1].ThreadID != "T2" || threads[1].Path != "b.go" || threads[1].Line != 5 ||
		len(threads[1].Comments) != 1 || threads[1].Comments[0].ID != "C3" {
		t.Fatalf("synthesized thread = %+v", threads[1])
	}
}

func TestListPRCommentThreads_UnknownSession(t *testing.T) {
	svc := newServiceWithStore(t, newFakeStore())
	_, err := svc.ListPRCommentThreads(context.Background(), "nope")
	if err == nil {
		t.Fatal("expected NotFound for unknown session")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/service/session/ -run PRCommentThreads`
Expected: FAIL — `ListPRCommentThreads` undefined.

- [ ] **Step 3: Write minimal implementation**

Create `backend/internal/service/session/pr_comments.go`:

```go
package session

import (
	"context"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// PRThreadComment is one review comment on a PR thread.
type PRThreadComment struct {
	ID        string
	Author    string
	Body      string
	URL       string
	Resolved  bool
	IsBot     bool
	CreatedAt time.Time
}

// PRCommentThread is a review thread with its comments, anchored to a file/line.
type PRCommentThread struct {
	ThreadID string
	Path     string
	Line     int
	Resolved bool
	IsBot    bool
	Comments []PRThreadComment
}

// PRCommentGroup is one PR's review threads.
type PRCommentGroup struct {
	PRURL    string
	HTMLURL  string
	Provider string
	Number   int
	HeadSHA  string
	Threads  []PRCommentThread
}

// ListPRCommentThreads returns each of the session's PRs with its review threads
// and comments. Comments are attached to their thread; a comment referencing an
// unknown thread id gets a synthesized thread from its own file/line so nothing
// is dropped.
func (s *Service) ListPRCommentThreads(ctx context.Context, id domain.SessionID) ([]PRCommentGroup, error) {
	if _, ok, err := s.store.GetSession(ctx, id); err != nil {
		return nil, fmt.Errorf("get %s: %w", id, err)
	} else if !ok {
		return nil, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	prs, err := s.store.ListPRsBySession(ctx, id)
	if err != nil {
		return nil, err
	}
	out := make([]PRCommentGroup, 0, len(prs))
	for _, pr := range prs {
		threads, err := s.store.ListPRReviewThreads(ctx, pr.URL)
		if err != nil {
			return nil, err
		}
		comments, err := s.store.ListPRComments(ctx, pr.URL)
		if err != nil {
			return nil, err
		}
		out = append(out, PRCommentGroup{
			PRURL:    pr.URL,
			HTMLURL:  pr.HTMLURL,
			Provider: pr.Provider,
			Number:   pr.Number,
			HeadSHA:  pr.HeadSHA,
			Threads:  buildThreads(threads, comments),
		})
	}
	return out, nil
}

// buildThreads keys threads by id (preserving list order), attaches comments,
// and synthesizes a thread for any comment whose thread id is unknown.
func buildThreads(threads []domain.PullRequestReviewThread, comments []domain.PullRequestComment) []PRCommentThread {
	order := make([]string, 0, len(threads))
	byID := make(map[string]*PRCommentThread, len(threads))
	add := func(id, path string, line int, resolved, isBot bool) *PRCommentThread {
		t := &PRCommentThread{ThreadID: id, Path: path, Line: line, Resolved: resolved, IsBot: isBot}
		byID[id] = t
		order = append(order, id)
		return t
	}
	for _, th := range threads {
		add(th.ThreadID, th.Path, th.Line, th.Resolved, th.IsBot)
	}
	for _, c := range comments {
		t, ok := byID[c.ThreadID]
		if !ok {
			t = add(c.ThreadID, c.File, c.Line, c.Resolved, c.IsBot)
		}
		t.Comments = append(t.Comments, PRThreadComment{
			ID: c.ID, Author: c.Author, Body: c.Body, URL: c.URL,
			Resolved: c.Resolved, IsBot: c.IsBot, CreatedAt: c.CreatedAt,
		})
	}
	res := make([]PRCommentThread, 0, len(order))
	for _, id := range order {
		res = append(res, *byID[id])
	}
	return res
}
```

Note: confirm the import path for `apierr` matches what `pr_summary.go` uses (grep `apierr.NotFound` in that file). If `pr_summary.go` returns NotFound differently, mirror it exactly.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/service/session/ -run PRCommentThreads`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/service/session/pr_comments.go backend/internal/service/session/pr_comments_test.go
git commit -m "feat(session): ListPRCommentThreads composes threads + comments per PR

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 3: Read API — `GET /sessions/{sessionId}/pr-comments`

**Files:**

- Modify: `backend/internal/httpd/controllers/dto.go`
- Modify: `backend/internal/httpd/controllers/sessions.go`
- Modify: `backend/internal/httpd/apispec/specgen/build.go`
- Test: `backend/internal/httpd/controllers/sessions_test.go`
- Regenerate: `openapi.yaml`, `frontend/src/api/schema.ts`

**Interfaces:**

- Consumes: `session.Service.ListPRCommentThreads` (Task 2).
- Produces (wire):
  - DTOs `ListSessionPRCommentsResponse{SessionID; PRs []SessionPRCommentGroup}`, `SessionPRCommentGroup{PrURL, HtmlURL, Provider string; Number int; HeadSHA string; Threads []SessionPRCommentThread}`, `SessionPRCommentThread{ThreadID, Path string; Line int; Resolved, IsBot bool; Comments []SessionPRThreadComment}`, `SessionPRThreadComment{ID, Author, Body, URL string; Resolved, IsBot bool; CreatedAt string}`.
  - Route `GET /api/v1/sessions/{sessionId}/pr-comments`.
  - `SessionService` interface gains `ListPRCommentThreads(ctx, id) ([]sessionsvc.PRCommentGroup, error)`.

- [ ] **Step 1: Write the failing test**

Append to `backend/internal/httpd/controllers/sessions_test.go`, matching the file's existing harness (find how it constructs a `SessionsController` with a fake `SessionService` and issues requests — the fake is `fakeSessionService`; add the new method to it):

```go
func TestSessionsAPI_ListPRComments(t *testing.T) {
	fake := &fakeSessionService{ /* mirror existing construction */ }
	fake.prCommentGroups = []sessionsvc.PRCommentGroup{{
		PRURL: "https://gh/pr/1", HTMLURL: "https://gh/pr/1", Provider: "github", Number: 1, HeadSHA: "abc",
		Threads: []sessionsvc.PRCommentThread{{
			ThreadID: "T1", Path: "a.go", Line: 10, Resolved: false, IsBot: false,
			Comments: []sessionsvc.PRThreadComment{{ID: "C1", Author: "alice", Body: "fix this"}},
		}},
	}}
	srv := newSessionsTestServer(t, fake) // mirror existing helper
	body, status, _ := doRequest(t, srv, "GET", "/api/v1/sessions/s1/pr-comments", "")
	if status != http.StatusOK {
		t.Fatalf("status %d: %s", status, body)
	}
	if !strings.Contains(body, `"threadId":"T1"`) || !strings.Contains(body, `"body":"fix this"`) ||
		!strings.Contains(body, `"headSha":"abc"`) {
		t.Fatalf("unexpected body: %s", body)
	}
}
```

Add the method to the existing `fakeSessionService` (with a `prCommentGroups` field):

```go
func (f *fakeSessionService) ListPRCommentThreads(_ context.Context, _ domain.SessionID) ([]sessionsvc.PRCommentGroup, error) {
	return f.prCommentGroups, nil
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/httpd/controllers/ -run ListPRComments`
Expected: FAIL — route/handler/interface method missing (won't compile until Step 3).

- [ ] **Step 3a: DTOs + mapper**

In `backend/internal/httpd/controllers/dto.go`, after `ListSessionPRsResponse`, add:

```go
// ListSessionPRCommentsResponse is the body of GET /sessions/{sessionId}/pr-comments.
type ListSessionPRCommentsResponse struct {
	SessionID domain.SessionID        `json:"sessionId"`
	PRs       []SessionPRCommentGroup `json:"prs"`
}

// SessionPRCommentGroup is one PR's review threads.
type SessionPRCommentGroup struct {
	PrURL    string                   `json:"prUrl"`
	HtmlURL  string                   `json:"htmlUrl"`
	Provider string                   `json:"provider"`
	Number   int                      `json:"number"`
	HeadSHA  string                   `json:"headSha"`
	Threads  []SessionPRCommentThread `json:"threads"`
}

// SessionPRCommentThread is a review thread anchored to a file/line.
type SessionPRCommentThread struct {
	ThreadID string                    `json:"threadId"`
	Path     string                    `json:"path"`
	Line     int                       `json:"line"`
	Resolved bool                      `json:"resolved"`
	IsBot    bool                      `json:"isBot"`
	Comments []SessionPRThreadComment  `json:"comments"`
}

// SessionPRThreadComment is one review comment.
type SessionPRThreadComment struct {
	ID        string `json:"id"`
	Author    string `json:"author"`
	Body      string `json:"body"`
	URL       string `json:"url"`
	Resolved  bool   `json:"resolved"`
	IsBot     bool   `json:"isBot"`
	CreatedAt string `json:"createdAt"`
}

// sessionPRCommentGroups maps service models to wire DTOs.
func sessionPRCommentGroups(groups []sessionsvc.PRCommentGroup) []SessionPRCommentGroup {
	out := make([]SessionPRCommentGroup, 0, len(groups))
	for _, g := range groups {
		threads := make([]SessionPRCommentThread, 0, len(g.Threads))
		for _, t := range g.Threads {
			comments := make([]SessionPRThreadComment, 0, len(t.Comments))
			for _, c := range t.Comments {
				createdAt := ""
				if !c.CreatedAt.IsZero() {
					createdAt = c.CreatedAt.UTC().Format(time.RFC3339)
				}
				comments = append(comments, SessionPRThreadComment{
					ID: c.ID, Author: c.Author, Body: c.Body, URL: c.URL,
					Resolved: c.Resolved, IsBot: c.IsBot, CreatedAt: createdAt,
				})
			}
			threads = append(threads, SessionPRCommentThread{
				ThreadID: t.ThreadID, Path: t.Path, Line: t.Line,
				Resolved: t.Resolved, IsBot: t.IsBot, Comments: comments,
			})
		}
		out = append(out, SessionPRCommentGroup{
			PrURL: g.PRURL, HtmlURL: g.HTMLURL, Provider: g.Provider,
			Number: g.Number, HeadSHA: g.HeadSHA, Threads: threads,
		})
	}
	return out
}
```

Confirm `dto.go` already imports `time` and the `sessionsvc` alias (it maps other session-service models — grep `sessionsvc` in dto.go; if the alias differs, use the existing one).

- [ ] **Step 3b: Interface + handler + route**

In `backend/internal/httpd/controllers/sessions.go`, add to the `SessionService` interface (near `ListPRSummaries`):

```go
	ListPRCommentThreads(ctx context.Context, id domain.SessionID) ([]sessionsvc.PRCommentGroup, error)
```

Add the route in `Register` (after the `pr` route):

```go
	r.Get("/sessions/{sessionId}/pr-comments", c.listPRComments)
```

Add the handler (after `listPRs`):

```go
func (c *SessionsController) listPRComments(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/sessions/{sessionId}/pr-comments")
		return
	}
	groups, err := c.Svc.ListPRCommentThreads(r.Context(), sessionID(r))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ListSessionPRCommentsResponse{SessionID: sessionID(r), PRs: sessionPRCommentGroups(groups)})
}
```

- [ ] **Step 3c: Register the operation**

In `backend/internal/httpd/apispec/specgen/build.go`, inside `sessionOperations()`, after the `listSessionPRs` entry, add:

```go
		{
			method: http.MethodGet, path: "/api/v1/sessions/{sessionId}/pr-comments", id: "listSessionPRComments", tag: "sessions",
			summary:    "List review comment threads across a session's pull requests",
			pathParams: []any{controllers.SessionIDParam{}},
			resps: []respUnit{
				{http.StatusOK, controllers.ListSessionPRCommentsResponse{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
```

In the `schemaNames` map, add:

```go
	"ControllersListSessionPRCommentsResponse": "ListSessionPRCommentsResponse",
	"ControllersSessionPRCommentGroup":         "SessionPRCommentGroup",
	"ControllersSessionPRCommentThread":        "SessionPRCommentThread",
	"ControllersSessionPRThreadComment":        "SessionPRThreadComment",
```

- [ ] **Step 4a: Run the controller test**

Run: `cd backend && go test ./internal/httpd/controllers/ -run ListPRComments`
Expected: PASS.

- [ ] **Step 4b: Regenerate + drift/parity guards**

Run (repo root): `npm run api`
Then: `cd backend && go test ./internal/httpd/...`
Expected: PASS incl. `TestBuild_MatchesEmbedded` + route/spec parity. `git status` shows `openapi.yaml` + `frontend/src/api/schema.ts` modified with the new path + schemas.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/httpd/controllers/dto.go backend/internal/httpd/controllers/sessions.go backend/internal/httpd/controllers/sessions_test.go backend/internal/httpd/apispec/specgen/build.go backend/internal/httpd/apispec/openapi.yaml frontend/src/api/schema.ts
git commit -m "feat(api): GET /sessions/{id}/pr-comments read endpoint

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 4: `session.Service.DiffContext` (git-backed)

**Files:**

- Create: `backend/internal/service/session/diff_context.go`
- Test: `backend/internal/service/session/diff_context_test.go`

**Interfaces:**

- Consumes: `s.store.GetSession` (worktree path), `s.store.ListPRsBySession` (PR base/head SHAs — also scopes authorization to the session's own PRs), `diffhunk.HunkForLine`, `preview.ConfinedPath`, `aoprocess.CommandContext`.
- Produces:
  - `type DiffContextLine struct { Kind string; OldLine, NewLine int; Text string }` (Kind = "context"/"add"/"del").
  - `type DiffContextResult struct { Available bool; Mode, Path string; Lines []DiffContextLine; Truncated bool }`
  - `type DiffContextQuery struct { PRURL, Path string; Line int; Mode string }` (Mode "hunk" default, or "file").
  - `func (s *Service) DiffContext(ctx context.Context, id domain.SessionID, q DiffContextQuery) (DiffContextResult, error)`
  - A package-level indirection `var gitOutput = func(ctx context.Context, dir string, args ...string) ([]byte, error) { ... }` so tests can run against a real temp git repo (default impl shells to git).

Behavior:

- Resolve session → `rec.Metadata.WorkspacePath`; unknown session → NotFound.
- Find the PR in `ListPRsBySession` by `q.PRURL`; not found → NotFound (`PR_NOT_FOUND`) — this both fetches SHAs and enforces the PR belongs to the session.
- Validate `q.Path` via `preview.ConfinedPath(workspacePath, q.Path)`; invalid → `Available:false`.
- `mode=file`: `git -C <wt> show <headRef>:<path>` (headRef = pr.HeadSHA, fallback "HEAD"); number lines as context; cap at `maxFileLines` (e.g. 2000) with `Truncated`.
- `mode=hunk` (default): needs `pr.BaseSHA` non-empty; `git -C <wt> diff <base>..<head> -- <path>`, then `diffhunk.HunkForLine(out, q.Line)`. If base empty or git fails or hunk not found → `Available:false` (frontend falls back to `mode=file` or a link).

- [ ] **Step 1: Write the failing test**

Create `backend/internal/service/session/diff_context_test.go`. Build a temp git repo with two commits so `git diff` and `git show` produce real output; point the fake store's session workspace at it:

```go
func TestDiffContext_HunkMode(t *testing.T) {
	dir := t.TempDir()
	runGit := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	runGit("init", "-q")
	runGit("config", "user.email", "t@t")
	runGit("config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("l1\nl2\nl3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", "a.go")
	runGit("commit", "-q", "-m", "base")
	baseSHA := gitRevParse(t, dir, "HEAD")
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("l1\nCHANGED\nl3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", "a.go")
	runGit("commit", "-q", "-m", "head")
	headSHA := gitRevParse(t, dir, "HEAD")

	fake := newFakeStore()
	fake.putSessionWithWorkspace("s1", dir)
	fake.prs["s1"] = []domain.PullRequest{{URL: "pr1", BaseSHA: baseSHA, HeadSHA: headSHA}}
	svc := newServiceWithStore(t, fake)

	res, err := svc.DiffContext(context.Background(), "s1", DiffContextQuery{PRURL: "pr1", Path: "a.go", Line: 2, Mode: "hunk"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Available || res.Mode != "hunk" {
		t.Fatalf("res = %+v", res)
	}
	var sawAdd bool
	for _, l := range res.Lines {
		if l.Kind == "add" && l.NewLine == 2 && l.Text == "CHANGED" {
			sawAdd = true
		}
	}
	if !sawAdd {
		t.Fatalf("expected the CHANGED add line at new 2: %+v", res.Lines)
	}
}

func TestDiffContext_FileMode(t *testing.T) {
	// ... same repo setup ...
	res, err := svc.DiffContext(context.Background(), "s1", DiffContextQuery{PRURL: "pr1", Path: "a.go", Mode: "file"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Available || res.Mode != "file" || len(res.Lines) != 3 || res.Lines[1].Text != "CHANGED" || res.Lines[1].NewLine != 2 {
		t.Fatalf("file mode = %+v", res)
	}
}

func TestDiffContext_PathTraversalRejected(t *testing.T) {
	// ... repo setup ...
	res, _ := svc.DiffContext(context.Background(), "s1", DiffContextQuery{PRURL: "pr1", Path: "../../etc/passwd", Mode: "file"})
	if res.Available {
		t.Fatal("traversal path must be rejected (Available=false)")
	}
}
```

Add the helper `gitRevParse(t, dir, ref)` and extend the fake store with `putSessionWithWorkspace(id, path)` (sets `Metadata.WorkspacePath`). Mirror the real fake store from `pr_summary_test.go`.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/service/session/ -run DiffContext`
Expected: FAIL — `DiffContext` undefined.

- [ ] **Step 3: Write minimal implementation**

Create `backend/internal/service/session/diff_context.go`:

```go
package session

import (
	"context"
	"fmt"
	"strings"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
	"github.com/aoagents/agent-orchestrator/backend/internal/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/diffhunk"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	previewutil "github.com/aoagents/agent-orchestrator/backend/internal/preview"
)

const maxFileLines = 2000

// DiffContextLine is one classified line of returned code context.
type DiffContextLine struct {
	Kind    string
	OldLine int
	NewLine int
	Text    string
}

// DiffContextResult is the code context for a review comment anchor.
type DiffContextResult struct {
	Available bool
	Mode      string
	Path      string
	Lines     []DiffContextLine
	Truncated bool
}

// DiffContextQuery selects the code context to return.
type DiffContextQuery struct {
	PRURL string
	Path  string
	Line  int
	Mode  string // "hunk" (default) or "file"
}

// gitOutput runs git in dir and returns stdout. Overridable in tests.
var gitOutput = func(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := aoprocess.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	return cmd.Output()
}

// DiffContext returns the diff hunk (or whole file) the review comment anchors
// to, read from the session's git worktree. Unavailable context (missing SHA,
// git failure, path outside the repo, or no hunk covering the line) is reported
// as Available:false rather than an error, so the UI can degrade gracefully.
func (s *Service) DiffContext(ctx context.Context, id domain.SessionID, q DiffContextQuery) (DiffContextResult, error) {
	rec, ok, err := s.store.GetSession(ctx, id)
	if err != nil {
		return DiffContextResult{}, fmt.Errorf("get %s: %w", id, err)
	}
	if !ok {
		return DiffContextResult{}, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	workspace := rec.Metadata.WorkspacePath
	prs, err := s.store.ListPRsBySession(ctx, id)
	if err != nil {
		return DiffContextResult{}, err
	}
	var pr domain.PullRequest
	found := false
	for _, p := range prs {
		if p.URL == q.PRURL {
			pr, found = p, true
			break
		}
	}
	if !found {
		return DiffContextResult{}, apierr.NotFound("PR_NOT_FOUND", "Unknown PR for session")
	}

	abs, ok := previewutil.ConfinedPath(workspace, q.Path)
	if !ok || workspace == "" {
		return DiffContextResult{Available: false, Mode: q.Mode, Path: q.Path}, nil
	}
	_ = abs // ConfinedPath validates traversal; git reads via the repo-relative q.Path.

	headRef := pr.HeadSHA
	if strings.TrimSpace(headRef) == "" {
		headRef = "HEAD"
	}

	if q.Mode == "file" {
		out, err := gitOutput(ctx, workspace, "show", headRef+":"+q.Path)
		if err != nil {
			return DiffContextResult{Available: false, Mode: "file", Path: q.Path}, nil
		}
		return fileResult(q.Path, string(out)), nil
	}

	// hunk mode
	if strings.TrimSpace(pr.BaseSHA) == "" {
		return DiffContextResult{Available: false, Mode: "hunk", Path: q.Path}, nil
	}
	out, err := gitOutput(ctx, workspace, "diff", pr.BaseSHA+".."+headRef, "--", q.Path)
	if err != nil {
		return DiffContextResult{Available: false, Mode: "hunk", Path: q.Path}, nil
	}
	lines, hit := diffhunk.HunkForLine(string(out), q.Line)
	if !hit {
		return DiffContextResult{Available: false, Mode: "hunk", Path: q.Path}, nil
	}
	res := DiffContextResult{Available: true, Mode: "hunk", Path: q.Path}
	for _, l := range lines {
		res.Lines = append(res.Lines, DiffContextLine{Kind: string(l.Kind), OldLine: l.OldLine, NewLine: l.NewLine, Text: l.Text})
	}
	return res, nil
}

func fileResult(path, content string) DiffContextResult {
	rows := strings.Split(strings.TrimSuffix(content, "\n"), "\n")
	res := DiffContextResult{Available: true, Mode: "file", Path: path}
	for i, r := range rows {
		if i >= maxFileLines {
			res.Truncated = true
			break
		}
		res.Lines = append(res.Lines, DiffContextLine{Kind: "context", NewLine: i + 1, Text: r})
	}
	return res
}
```

Confirm the `apierr` and `previewutil` import paths against `pr_summary.go` / `sessions.go` (the preview package is imported as `previewutil` in the controllers layer; here import `internal/preview` — verify the package's actual import name and adjust the alias).

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/service/session/ -run DiffContext`
Expected: PASS (requires `git` on PATH — it is, per the build environment).

- [ ] **Step 5: Commit**

```bash
git add backend/internal/service/session/diff_context.go backend/internal/service/session/diff_context_test.go
git commit -m "feat(session): git-backed DiffContext (hunk + full-file)

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 5: Diff-context API — `GET /sessions/{sessionId}/diff-context`

**Files:**

- Modify: `backend/internal/httpd/controllers/dto.go`, `sessions.go`, `sessions_test.go`, `apispec/specgen/build.go`
- Regenerate: `openapi.yaml`, `schema.ts`

**Interfaces:**

- Consumes: `session.Service.DiffContext` (Task 4).
- Produces: route `GET /api/v1/sessions/{sessionId}/diff-context` (query: `prUrl`, `path`, `line`, `mode`); DTOs `DiffContextResponse{Available bool; Mode, Path string; Lines []DiffContextLineDTO; Truncated bool}`, `DiffContextLineDTO{Kind string; OldLine, NewLine int; Text string}`; `SessionService` gains `DiffContext(ctx, id, sessionsvc.DiffContextQuery) (sessionsvc.DiffContextResult, error)`.

- [ ] **Step 1: Write the failing test**

Append to `sessions_test.go`:

```go
func TestSessionsAPI_DiffContext(t *testing.T) {
	fake := &fakeSessionService{ /* mirror */ }
	fake.diffContext = sessionsvc.DiffContextResult{
		Available: true, Mode: "hunk", Path: "a.go",
		Lines: []sessionsvc.DiffContextLine{{Kind: "add", NewLine: 2, Text: "CHANGED"}},
	}
	srv := newSessionsTestServer(t, fake)
	body, status, _ := doRequest(t, srv, "GET", "/api/v1/sessions/s1/diff-context?prUrl=pr1&path=a.go&line=2&mode=hunk", "")
	if status != http.StatusOK {
		t.Fatalf("status %d: %s", status, body)
	}
	if !strings.Contains(body, `"available":true`) || !strings.Contains(body, `"kind":"add"`) || !strings.Contains(body, `"text":"CHANGED"`) {
		t.Fatalf("body: %s", body)
	}
}
```

Add to `fakeSessionService`:

```go
func (f *fakeSessionService) DiffContext(_ context.Context, _ domain.SessionID, _ sessionsvc.DiffContextQuery) (sessionsvc.DiffContextResult, error) {
	return f.diffContext, nil
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/httpd/controllers/ -run DiffContext`
Expected: FAIL (won't compile until Step 3).

- [ ] **Step 3a: DTOs + mapper** (in `dto.go`)

```go
// DiffContextResponse is the body of GET /sessions/{sessionId}/diff-context.
type DiffContextResponse struct {
	Available bool                `json:"available"`
	Mode      string              `json:"mode"`
	Path      string              `json:"path"`
	Lines     []DiffContextLineDTO `json:"lines"`
	Truncated bool                `json:"truncated"`
}

// DiffContextLineDTO is one classified code-context line.
type DiffContextLineDTO struct {
	Kind    string `json:"kind"`
	OldLine int    `json:"oldLine"`
	NewLine int    `json:"newLine"`
	Text    string `json:"text"`
}

func diffContextResponse(res sessionsvc.DiffContextResult) DiffContextResponse {
	lines := make([]DiffContextLineDTO, 0, len(res.Lines))
	for _, l := range res.Lines {
		lines = append(lines, DiffContextLineDTO{Kind: l.Kind, OldLine: l.OldLine, NewLine: l.NewLine, Text: l.Text})
	}
	return DiffContextResponse{Available: res.Available, Mode: res.Mode, Path: res.Path, Lines: lines, Truncated: res.Truncated}
}
```

- [ ] **Step 3b: Interface + handler + route** (in `sessions.go`)

Interface addition:

```go
	DiffContext(ctx context.Context, id domain.SessionID, q sessionsvc.DiffContextQuery) (sessionsvc.DiffContextResult, error)
```

Route (in `Register`):

```go
	r.Get("/sessions/{sessionId}/diff-context", c.diffContext)
```

Handler:

```go
func (c *SessionsController) diffContext(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/sessions/{sessionId}/diff-context")
		return
	}
	mode := r.URL.Query().Get("mode")
	if mode == "" {
		mode = "hunk"
	}
	line, _ := strconv.Atoi(r.URL.Query().Get("line"))
	q := sessionsvc.DiffContextQuery{
		PRURL: r.URL.Query().Get("prUrl"),
		Path:  r.URL.Query().Get("path"),
		Line:  line,
		Mode:  mode,
	}
	res, err := c.Svc.DiffContext(r.Context(), sessionID(r), q)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, diffContextResponse(res))
}
```

Confirm `sessions.go` imports `strconv` (add if missing).

- [ ] **Step 3c: build.go operation + schemaNames**

In `sessionOperations()`:

```go
		{
			method: http.MethodGet, path: "/api/v1/sessions/{sessionId}/diff-context", id: "sessionDiffContext", tag: "sessions",
			summary:    "Return the diff hunk or full file a review comment anchors to",
			pathParams: []any{controllers.SessionIDParam{}},
			queryParams: []any{controllers.DiffContextParams{}},
			resps: []respUnit{
				{http.StatusOK, controllers.DiffContextResponse{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
```

IMPORTANT: check whether the `operation` struct in build.go supports a `queryParams` field. Grep `queryParams` / `query:"` in build.go and an existing operation that takes query params (e.g. a list endpoint with filters). If `queryParams` is NOT a supported field on the operation struct, OMIT it from the entry and instead document the query params only in the handler (the spec will still validate; query-param typing in the generated client is optional — the frontend passes them via `params: { query: {...} }` or a raw URL). If build.go DOES support query params via a struct, define `DiffContextParams` in dto.go:

```go
// DiffContextParams documents the query parameters for GET /sessions/{sessionId}/diff-context.
type DiffContextParams struct {
	PrURL string `query:"prUrl" description:"PR URL the comment belongs to."`
	Path  string `query:"path" description:"Repo-relative file path the comment anchors to."`
	Line  int    `query:"line" description:"1-based new-side line number of the anchor."`
	Mode  string `query:"mode" description:"hunk (default) or file." enum:"hunk,file"`
}
```

Add schemaNames entries for the new named types:

```go
	"ControllersDiffContextResponse": "DiffContextResponse",
	"ControllersDiffContextLineDTO":  "DiffContextLineDTO",
```

(and `"ControllersDiffContextParams": "DiffContextParams"` only if you kept the params struct.)

- [ ] **Step 4a: Controller test**

Run: `cd backend && go test ./internal/httpd/controllers/ -run DiffContext`
Expected: PASS.

- [ ] **Step 4b: Regenerate + guards**

Run: `npm run api` (root), then `cd backend && go test ./internal/httpd/...`
Expected: PASS incl. drift + parity.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/httpd/controllers/dto.go backend/internal/httpd/controllers/sessions.go backend/internal/httpd/controllers/sessions_test.go backend/internal/httpd/apispec/specgen/build.go backend/internal/httpd/apispec/openapi.yaml frontend/src/api/schema.ts
git commit -m "feat(api): GET /sessions/{id}/diff-context endpoint

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 6: Frontend — Comments tab + thread list (read-only)

**Files:**

- Create: `frontend/src/renderer/hooks/useSessionPRComments.ts`
- Create: `frontend/src/renderer/components/CommentsView.tsx`
- Create: `frontend/src/renderer/components/CommentsView.test.tsx`
- Modify: `frontend/src/renderer/components/SessionInspector.tsx`

**Interfaces:**

- Consumes: generated `apiClient` path `/api/v1/sessions/{sessionId}/pr-comments` (Task 3 regen).
- Produces: `useSessionPRComments(sessionId)` hook; `CommentsView` component; a new `"comments"` member of `InspectorView`.

- [ ] **Step 1: Write the failing test**

Create `frontend/src/renderer/components/CommentsView.test.tsx` (mirror the mock-api pattern used by other inspector tests):

```tsx
import { QueryClientProvider, QueryClient } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const { getMock } = vi.hoisted(() => ({ getMock: vi.fn() }));
vi.mock("../lib/api-client", () => ({
	apiClient: { GET: getMock },
	apiErrorMessage: (e: unknown, fb = "Request failed") => (e instanceof Error ? e.message : fb),
}));

import { CommentsView } from "./CommentsView";

function renderView(sessionId = "s1") {
	const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	render(
		<QueryClientProvider client={qc}>
			<CommentsView sessionId={sessionId} />
		</QueryClientProvider>,
	);
}

beforeEach(() => {
	getMock.mockReset().mockResolvedValue({
		data: {
			sessionId: "s1",
			prs: [
				{
					prUrl: "https://gh/pr/1",
					htmlUrl: "https://gh/pr/1",
					provider: "github",
					number: 1,
					headSha: "abc",
					threads: [
						{
							threadId: "T1",
							path: "a.go",
							line: 10,
							resolved: false,
							isBot: false,
							comments: [
								{
									id: "C1",
									author: "alice",
									body: "please fix",
									url: "",
									resolved: false,
									isBot: false,
									createdAt: "2026-07-09T10:00:00Z",
								},
							],
						},
					],
				},
			],
		},
		error: undefined,
	});
});

describe("CommentsView", () => {
	it("renders a thread's file, author, and comment body", async () => {
		renderView();
		expect(await screen.findByText("a.go")).toBeInTheDocument();
		expect(await screen.findByText("please fix")).toBeInTheDocument();
		expect(screen.getByText("alice")).toBeInTheDocument();
	});

	it("shows an empty state when there are no threads", async () => {
		getMock.mockReset().mockResolvedValue({ data: { sessionId: "s1", prs: [] }, error: undefined });
		renderView();
		expect(await screen.findByText(/no review comments/i)).toBeInTheDocument();
	});
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd frontend && npm run test -- CommentsView`
Expected: FAIL — component missing.

- [ ] **Step 3a: Hook**

Create `frontend/src/renderer/hooks/useSessionPRComments.ts`:

```ts
import { useQuery } from "@tanstack/react-query";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import type { components } from "../../api/schema";

export type PRCommentGroup = components["schemas"]["SessionPRCommentGroup"];

export function useSessionPRComments(sessionId: string) {
	return useQuery({
		queryKey: ["session-pr-comments", sessionId],
		queryFn: async () => {
			const { data, error } = await apiClient.GET("/api/v1/sessions/{sessionId}/pr-comments", {
				params: { path: { sessionId } },
			});
			if (error) throw new Error(apiErrorMessage(error, "Unable to load comments"));
			return data?.prs ?? [];
		},
	});
}
```

- [ ] **Step 3b: Component**

Create `frontend/src/renderer/components/CommentsView.tsx` (tabs for indentation):

```tsx
import { useSessionPRComments, type PRCommentGroup } from "../hooks/useSessionPRComments";
import { apiErrorMessage } from "../lib/api-client";

type Thread = PRCommentGroup["threads"][number];
type Comment = Thread["comments"][number];

export function CommentsView({ sessionId }: { sessionId: string }) {
	const query = useSessionPRComments(sessionId);
	if (query.isLoading) {
		return <p className="inspector-empty">Loading comments…</p>;
	}
	if (query.error) {
		return <p className="inspector-empty">{apiErrorMessage(query.error, "Unable to load comments")}</p>;
	}
	const groups = (query.data ?? []).filter((g) => g.threads.length > 0);
	if (groups.length === 0) {
		return <p className="inspector-empty">No review comments yet.</p>;
	}
	return (
		<div role="tabpanel" className="flex flex-col gap-4">
			{groups.map((g) => (
				<section key={g.prUrl} className="flex flex-col gap-2">
					<div className="text-[12px] font-medium text-muted-foreground">
						{g.provider === "gitlab" ? "MR" : "PR"} #{g.number}
					</div>
					{g.threads.map((t) => (
						<ThreadCard key={t.threadId} thread={t} />
					))}
				</section>
			))}
		</div>
	);
}

function ThreadCard({ thread }: { thread: Thread }) {
	return (
		<div className="rounded-[7px] border border-border bg-surface">
			<div className="flex items-center gap-2 border-b border-border px-3 py-1.5">
				<span className="font-mono text-[11.5px] text-foreground">{thread.path}</span>
				<span className="text-[11px] text-muted-foreground">:{thread.line}</span>
				{thread.resolved && <span className="ml-auto rounded px-1.5 py-0.5 text-[10px] text-success">Resolved</span>}
			</div>
			<div className="flex flex-col gap-2 px-3 py-2">
				{thread.comments.map((c) => (
					<CommentRow key={c.id} comment={c} />
				))}
			</div>
		</div>
	);
}

function CommentRow({ comment }: { comment: Comment }) {
	return (
		<div className="flex flex-col gap-0.5">
			<div className="flex items-center gap-2">
				<span className="grid h-5 w-5 place-items-center rounded-full bg-raised text-[10px] font-medium text-muted-foreground">
					{initials(comment.author)}
				</span>
				<span className="text-[12px] font-medium text-foreground">{comment.author || "unknown"}</span>
			</div>
			<p className="whitespace-pre-wrap pl-7 text-[12px] leading-snug text-foreground">{comment.body}</p>
		</div>
	);
}

function initials(name: string): string {
	const s = (name || "?").trim();
	return s ? s.slice(0, 2).toUpperCase() : "?";
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd frontend && npm run test -- CommentsView`
Expected: PASS.

- [ ] **Step 5: Add the tab to SessionInspector**

In `frontend/src/renderer/components/SessionInspector.tsx`:

Change the `InspectorView` type:

```tsx
export type InspectorView = "summary" | "reviews" | "comments" | "browser";
```

Add a `VIEWS` entry (after the `reviews` entry) — reuse a message-square style icon:

```tsx
	{
		id: "comments",
		label: "Comments",
		icon: (
			<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" aria-hidden="true">
				<path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z" />
			</svg>
		),
	},
```

Render it in the body (after the reviews line):

```tsx
{
	view === "comments" ? <CommentsView sessionId={session.id} /> : null;
}
```

Add the import at the top:

```tsx
import { CommentsView } from "./CommentsView";
```

- [ ] **Step 6: Typecheck + tests**

Run: `cd frontend && npm run typecheck && npm run test -- CommentsView SessionInspector`
Expected: PASS. Revert `routeTree.gen.ts` churn if any.

- [ ] **Step 7: Commit**

```bash
git add frontend/src/renderer/hooks/useSessionPRComments.ts frontend/src/renderer/components/CommentsView.tsx frontend/src/renderer/components/CommentsView.test.tsx frontend/src/renderer/components/SessionInspector.tsx
git commit -m "feat(inspector): read-only Comments tab with review threads

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 7: Frontend — diff hunk display + expand-to-file

**Files:**

- Create: `frontend/src/renderer/components/DiffHunk.tsx`
- Create: `frontend/src/renderer/components/DiffHunk.test.tsx`
- Modify: `frontend/src/renderer/components/CommentsView.tsx` (render `<DiffHunk>` per thread)

**Interfaces:**

- Consumes: generated `apiClient` path `/api/v1/sessions/{sessionId}/diff-context` (Task 5 regen).
- Produces: `DiffHunk` component that lazily loads and renders the anchored code, with an Expand button switching `mode=hunk`→`mode=file`.

- [ ] **Step 1: Write the failing test**

Create `frontend/src/renderer/components/DiffHunk.test.tsx`:

```tsx
import { QueryClientProvider, QueryClient } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

const { getMock } = vi.hoisted(() => ({ getMock: vi.fn() }));
vi.mock("../lib/api-client", () => ({
	apiClient: { GET: getMock },
	apiErrorMessage: (e: unknown, fb = "Request failed") => (e instanceof Error ? e.message : fb),
}));

import { DiffHunk } from "./DiffHunk";

function renderHunk() {
	const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	render(
		<QueryClientProvider client={qc}>
			<DiffHunk sessionId="s1" prUrl="pr1" path="a.go" line={2} />
		</QueryClientProvider>,
	);
}

beforeEach(() => {
	getMock.mockReset().mockImplementation(async (_path, opts) => {
		const mode = opts?.params?.query?.mode ?? "hunk";
		if (mode === "file") {
			return {
				data: {
					available: true,
					mode: "file",
					path: "a.go",
					truncated: false,
					lines: [
						{ kind: "context", oldLine: 1, newLine: 1, text: "l1" },
						{ kind: "context", oldLine: 2, newLine: 2, text: "CHANGED" },
					],
				},
				error: undefined,
			};
		}
		return {
			data: {
				available: true,
				mode: "hunk",
				path: "a.go",
				truncated: false,
				lines: [{ kind: "add", oldLine: 0, newLine: 2, text: "CHANGED" }],
			},
			error: undefined,
		};
	});
});

describe("DiffHunk", () => {
	it("renders the hunk lines", async () => {
		renderHunk();
		expect(await screen.findByText("CHANGED")).toBeInTheDocument();
	});

	it("expands to the full file on click", async () => {
		renderHunk();
		await screen.findByText("CHANGED");
		await userEvent.click(screen.getByRole("button", { name: /expand/i }));
		await waitFor(() => expect(screen.getByText("l1")).toBeInTheDocument());
	});
});
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd frontend && npm run test -- DiffHunk`
Expected: FAIL — component missing.

- [ ] **Step 3: Component**

Create `frontend/src/renderer/components/DiffHunk.tsx`:

```tsx
import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import type { components } from "../../api/schema";

type DiffContext = components["schemas"]["DiffContextResponse"];
type Mode = "hunk" | "file";

export function DiffHunk({
	sessionId,
	prUrl,
	path,
	line,
}: {
	sessionId: string;
	prUrl: string;
	path: string;
	line: number;
}) {
	const [mode, setMode] = useState<Mode>("hunk");
	const query = useQuery({
		queryKey: ["diff-context", sessionId, prUrl, path, line, mode],
		queryFn: async () => {
			const { data, error } = await apiClient.GET("/api/v1/sessions/{sessionId}/diff-context", {
				params: { path: { sessionId }, query: { prUrl, path, line, mode } },
			});
			if (error) throw new Error(apiErrorMessage(error, "Unable to load code"));
			return data as DiffContext;
		},
	});

	if (query.isLoading) return <div className="px-3 py-1 text-[11px] text-muted-foreground">Loading code…</div>;
	const ctx = query.data;
	if (!ctx || !ctx.available || ctx.lines.length === 0) {
		return null; // no code context; the thread still shows its file:line header
	}
	return (
		<div className="overflow-x-auto border-b border-border bg-raised font-mono text-[11.5px]">
			{ctx.lines.map((l, i) => (
				<div
					key={i}
					className={
						l.kind === "add"
							? "bg-success/10 text-success"
							: l.kind === "del"
								? "bg-error/10 text-error"
								: "text-muted-foreground"
					}
				>
					<span className="inline-block w-10 select-none pr-2 text-right opacity-50">
						{l.newLine || l.oldLine || ""}
					</span>
					<span className="whitespace-pre">
						{lineSign(l.kind)}
						{l.text}
					</span>
				</div>
			))}
			{mode === "hunk" && (
				<button
					type="button"
					className="w-full py-1 text-[11px] text-accent hover:underline"
					onClick={() => setMode("file")}
				>
					Expand full file
				</button>
			)}
			{ctx.truncated && <div className="px-3 py-1 text-[11px] text-muted-foreground">File truncated…</div>}
		</div>
	);
}

function lineSign(kind: string): string {
	if (kind === "add") return "+";
	if (kind === "del") return "-";
	return " ";
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `cd frontend && npm run test -- DiffHunk`
Expected: PASS.

- [ ] **Step 5: Wire into CommentsView**

In `frontend/src/renderer/components/CommentsView.tsx`, `ThreadCard` needs the session id and PR url to render `<DiffHunk>`. Thread the `sessionId` and `prUrl` down and render the hunk between the file header and the comments:

Change `CommentsView`'s thread map to pass context:

```tsx
{
	g.threads.map((t) => <ThreadCard key={t.threadId} sessionId={sessionId} prUrl={g.prUrl} thread={t} />);
}
```

Update `ThreadCard`:

```tsx
function ThreadCard({ sessionId, prUrl, thread }: { sessionId: string; prUrl: string; thread: Thread }) {
	return (
		<div className="rounded-[7px] border border-border bg-surface">
			<div className="flex items-center gap-2 border-b border-border px-3 py-1.5">
				<span className="font-mono text-[11.5px] text-foreground">{thread.path}</span>
				<span className="text-[11px] text-muted-foreground">:{thread.line}</span>
				{thread.resolved && <span className="ml-auto rounded px-1.5 py-0.5 text-[10px] text-success">Resolved</span>}
			</div>
			{thread.path && thread.line > 0 && (
				<DiffHunk sessionId={sessionId} prUrl={prUrl} path={thread.path} line={thread.line} />
			)}
			<div className="flex flex-col gap-2 px-3 py-2">
				{thread.comments.map((c) => (
					<CommentRow key={c.id} comment={c} />
				))}
			</div>
		</div>
	);
}
```

Add the import: `import { DiffHunk } from "./DiffHunk";`

The existing `CommentsView.test.tsx` mocks only the `pr-comments` GET; `DiffHunk` will also call the `diff-context` GET. Update the `CommentsView.test.tsx` `getMock` to return a benign `available:false` for the diff-context path so the thread still renders (add a branch: if the called path includes `diff-context`, return `{ data: { available: false, mode: "hunk", path: "", lines: [], truncated: false }, error: undefined }`).

- [ ] **Step 6: Typecheck + tests**

Run: `cd frontend && npm run typecheck && npm run test -- CommentsView DiffHunk`
Expected: PASS. Revert `routeTree.gen.ts` churn if any.

- [ ] **Step 7: Commit**

```bash
git add frontend/src/renderer/components/DiffHunk.tsx frontend/src/renderer/components/DiffHunk.test.tsx frontend/src/renderer/components/CommentsView.tsx frontend/src/renderer/components/CommentsView.test.tsx
git commit -m "feat(inspector): diff hunk with expand-to-file in Comments tab

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 8: Full-suite verification

- [ ] **Step 1: Backend build + touched-package tests**

Run:

```bash
cd backend && go build ./... && go test ./internal/diffhunk/ ./internal/service/session/ ./internal/httpd/...
```

Expected: PASS (incl. drift + parity guards). Then `go vet ./internal/diffhunk/ ./internal/service/session/ ./internal/httpd/...` and `gofmt -l internal/diffhunk internal/service/session internal/httpd/controllers internal/httpd/apispec` (clean). Run golangci-lint on the touched packages if available.

- [ ] **Step 2: Frontend typecheck + suite**

Run:

```bash
cd frontend && npm run typecheck && npm run test
```

Expected: PASS.

- [ ] **Step 3: Cleanliness**

Run: `git status` — no unintended churn (`pnpm-lock.yaml`, `routeTree.gen.ts`, `node_modules`). The two generated files (`openapi.yaml`, `schema.ts`) should already be committed in Tasks 3 and 5. Revert any stray churn.

---

## Self-Review

**Spec coverage (Phase 2 slice of `2026-07-09-pr-review-comments-tab-design.md`):**

- "Read API `/pr-comments` — threads grouped per PR (DB only)" → Tasks 2, 3. ✅
- "Diff-context API — git-backed hunk + full-file expand" → Tasks 1, 4, 5. ✅
- "Comments tab (read-only view with expand)" → Tasks 6, 7. ✅
- Provider-agnostic (GitHub + GitLab): the read side reads persisted `pr_comment`/`pr_review_threads` (both providers populate them); the git side is provider-neutral (operates on the worktree). ✅
- Security: sanitization is not needed on the read path (bodies render as React text, not HTML; not sent to a PTY); git path-traversal guarded via `preview.ConfinedPath`; refs/paths passed as separate args. ✅
- Out of scope here (Phases 3–4): reply, resolve, send-to-worker, auto-nudge removal. Correctly NOT in this plan. ✅

**Placeholder scan:** No TBD/TODO. Two "confirm against the real harness/struct" notes (Task 3/5 build.go `queryParams` support; Task 2/4 `apierr`/`previewutil` import names; the fake-store/test-harness names in Tasks 2–5) instruct the implementer to match existing code exactly — production code + assertions are fully specified; only local identifiers must be reconciled with the current tree.

**Type consistency:** service models (`PRCommentGroup`/`PRCommentThread`/`PRThreadComment`, `DiffContextResult`/`DiffContextLine`/`DiffContextQuery`) are defined in Tasks 2/4 and consumed identically by the controllers (Tasks 3/5) and DTO mappers. Wire field names (`prUrl`, `htmlUrl`, `headSha`, `threadId`, `path`, `line`, `body`, `available`, `mode`, `kind`, `newLine`, `oldLine`, `text`) are consistent between DTOs (Tasks 3/5) and the frontend hook/components (Tasks 6/7). `diffhunk.Line.Kind` (`KindAdd`/`KindDel`/`KindContext`) is stringified to `"add"/"del"/"context"` at the service boundary and matched verbatim in `DiffHunk.tsx`.

**Open risk flagged for the executor:** build.go may or may not support a `queryParams` field on its operation struct — Task 5 Step 3c tells the implementer to check and adapt (keep the endpoint working either way; query-param typing in the generated client is a nicety, not required — the frontend can pass a typed `params.query` only if the generated path type includes it, else fall back to documented query params).
