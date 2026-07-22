# PR Review Comments — Phase 4a: Send-to-worker Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Add a manual "Send to worker" action on each review thread in the Comments tab — a split button that dispatches the thread's comment(s) to the worker session using the editable `review-comment-dispatch` template as the default prompt, with a dropdown to add extra instructions.

**Architecture:** A backend endpoint renders the `review-comment-dispatch` message template server-side (comment bodies sanitized), appends the operator's optional extra prompt, and delivers it to the worker via the existing `Service.Send` → runtime messenger path — the same rendering the removed auto-nudge did, now triggered manually. The frontend adds a split button to `ThreadCard`.

**Tech Stack:** Go (code-first OpenAPI via specgen), React + TanStack Query + openapi-fetch, vitest.

## Global Constraints

- The auto-nudge for human review comments was already removed (commit `fb18eb2a`); this feature is the manual replacement. Do NOT re-introduce any automatic dispatch.
- Comment bodies and the extra prompt are attacker-influenceable and reach the worker's PTY: sanitize BOTH with `domain.SanitizeControlChars` before they enter the message. (The generic `/send` HTTP handler sanitizes, but this new path builds the message in the service layer and calls `Service.Send` directly, which does NOT sanitize — so the service method must.)
- API is code-first: edit `controllers/dto.go` + `apispec/specgen/build.go` (`sessionOperations()` + `schemaNames`), then `npm run api` (repo root) regenerates `openapi.yaml` + `frontend/src/api/schema.ts`. Never hand-edit generated files. `go test ./internal/httpd/...` runs drift + parity guards.
- Backend gate: `go build ./...` + targeted `go test` (CLI e2e `TestSpawn*` fail spuriously inside a live AO session — run touched packages only). gofmt tabs; `gofmt -l`/`go vet` clean.
- Frontend gates (from `frontend/`): `npm run test` + `npm run typecheck`. Tabs indentation. Revert `routeTree.gen.ts`/`pnpm-lock.yaml` churn.
- Renderer reuses the `review-comment-dispatch` template (`messagetemplates.NameReviewCommentDispatch`, data `messagetemplates.ReviewCommentData{Comments string}`), which stays operator-editable via Global Settings.
- Commit after each task. Branch: `bugfix/PROJ-2272-gitlab-mr-detection` (PR #36).

## File Structure

Modified:

- `backend/internal/service/session/service.go` — add `Renderer` to `Deps` + a `renderer` field + a small interface for it.
- `backend/internal/daemon/lifecycle_wiring.go` — build a renderer in `startSession` and pass it in `sessionsvc.Deps`.
- `backend/internal/httpd/controllers/dto.go`, `sessions.go`, `sessions_test.go`, `apispec/specgen/build.go` — the endpoint.
- `frontend/src/renderer/components/CommentsView.tsx` — render the new button in `ThreadCard`.
  Created:
- `backend/internal/service/session/comment_dispatch.go` + `_test.go` — the service method.
- `frontend/src/renderer/components/SendToWorkerButton.tsx` + `_test.tsx` — the split button.

Reference facts (verified):

- `Service` struct + `Deps` + `NewWithDeps` at `service/session/service.go` (Deps has Manager/Store/PRClaimer/SCM/Clock/Telemetry/SignalCapable).
- `Service.Send(ctx, id, message) error` (service.go:478) → `manager.Send`. Does NOT sanitize.
- Store methods already available: `GetSession`, `ListPRsBySession`, `ListPRComments`.
- `domain.PullRequestComment{ThreadID, ID, Author, File, Line, Body, URL, Resolved, IsBot, CreatedAt}`.
- Renderer: `messagetemplates.NewRenderer(func() map[string]string) *Renderer`, `(*Renderer).Render(name messagetemplates.Name, data any) (string, error)`, `messagetemplates.NameReviewCommentDispatch`, `messagetemplates.ReviewCommentData{Comments string}`.
- Daemon: `startSession` (lifecycle_wiring.go:92) receives `promptOverrides *promptoverrides.Store`; the templates closure is `func() map[string]string { return promptOverrides.Get().Templates }` (daemon.go:136 uses the same for lifecycle). `sessionsvc.NewWithDeps(sessionsvc.Deps{...})` is at lifecycle_wiring.go:141.
- Mirror endpoint: `POST /sessions/{sessionId}/send` handler `send` (sessions.go:481) + `SendSessionMessageRequest` (dto.go:276) + its build.go operation. Reuse `controllers.SessionIDParam`.

---

## Task 1: Wire a message Renderer into `session.Service`

**Files:**

- Modify: `backend/internal/service/session/service.go`
- Modify: `backend/internal/daemon/lifecycle_wiring.go`

**Interfaces:**

- Produces: a `messageRenderer` interface + `Service.renderer` field + `Deps.Renderer`, wired in the daemon. Task 2 consumes `s.renderer`.

- [ ] **Step 1: Add the interface, field, and Deps entry**

In `service/session/service.go`, add near the top (after imports):

```go
// messageRenderer renders an editable nudge/dispatch template. *messagetemplates.Renderer
// satisfies it; kept as an interface so tests can inject a stub.
type messageRenderer interface {
	Render(name messagetemplates.Name, data any) (string, error)
}
```

Add `import "github.com/aoagents/agent-orchestrator/backend/internal/messagetemplates"` (verify the module path prefix against the file's existing imports).

Add a field to `Service`:

```go
	renderer messageRenderer
```

Add to `Deps`:

```go
	// Renderer renders dispatch templates (send-to-worker). nil disables the
	// comment-dispatch endpoint (it returns 501-style unavailable).
	Renderer messageRenderer
```

In `NewWithDeps`, set it: `s.renderer = d.Renderer` (add to the struct literal or after it, matching the file's style).

- [ ] **Step 2: Wire the renderer in the daemon**

In `backend/internal/daemon/lifecycle_wiring.go`, inside `startSession` (which has `promptOverrides` in scope), build a renderer and pass it in the `sessionsvc.Deps{...}` literal (the one at ~line 141):

```go
	msgRenderer := messagetemplates.NewRenderer(func() map[string]string { return promptOverrides.Get().Templates })
```

Add `Renderer: msgRenderer,` to the `sessionsvc.Deps{...}` literal. Ensure `messagetemplates` is imported in this file (add if missing).

- [ ] **Step 3: Build**

Run: `cd backend && go build ./... && gofmt -l internal/service/session/service.go internal/daemon/lifecycle_wiring.go`
Expected: builds; gofmt clean. (No behavior change yet — nothing reads `renderer`.)

- [ ] **Step 4: Commit**

```bash
git add backend/internal/service/session/service.go backend/internal/daemon/lifecycle_wiring.go
git commit -m "feat(session): wire message renderer into the session service

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 2: `Service.DispatchCommentToWorker`

**Files:**

- Create: `backend/internal/service/session/comment_dispatch.go`
- Test: `backend/internal/service/session/comment_dispatch_test.go`

**Interfaces:**

- Consumes: `s.store.GetSession`, `s.store.ListPRsBySession`, `s.store.ListPRComments`, `s.renderer.Render`, `s.Send`.
- Produces: `func (s *Service) DispatchCommentToWorker(ctx context.Context, id domain.SessionID, prURL, threadID, extraPrompt string) error`.

Behavior:

- Unknown session → `apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")`.
- `prURL` not among the session's PRs (`ListPRsBySession`) → `apierr.NotFound("PR_NOT_FOUND", "Unknown PR for session")` (authorization).
- Collect the thread's comments (`ListPRComments(prURL)` filtered by `ThreadID == threadID`); if none → `apierr.Invalid("NO_COMMENTS", "Thread has no comments to dispatch", nil)`.
- Build content: join sanitized comment bodies (`domain.SanitizeControlChars(c.Body)`) with `"\n\n"`.
- `s.renderer == nil` → `apierr.Invalid("DISPATCH_UNAVAILABLE", "Comment dispatch is not configured", nil)`.
- Render `NameReviewCommentDispatch` with `ReviewCommentData{Comments: content}`; on render error, return it.
- If `strings.TrimSpace(extraPrompt) != ""`, append `"\n\n" + domain.SanitizeControlChars(extraPrompt)`.
- `return s.Send(ctx, id, message)`.

- [ ] **Step 1: Write the failing test**

Create `comment_dispatch_test.go`. Reuse the package fake store (`fakeStore`/`multiPRFakeStore`, `&Service{...}`). Inject a stub renderer and a captured Send. The service's `Send` calls `s.manager.Send` — the fake store isn't the manager. Check how existing tests stub `manager` (the `commander` interface); if a fake commander exists, use it to capture the sent message. If none exists, add a minimal fake commander capturing `Send`. Test:

```go
func TestDispatchCommentToWorker_RendersSanitizesAndSends(t *testing.T) {
	fake := newFakeStore()
	fake.sessions["s1"] = domain.SessionRecord{ID: "s1", ProjectID: "p", Kind: domain.KindWorker}
	stList := &multiPRFakeStore{fakeStore: fake, prs: []domain.PullRequest{{URL: "pr1"}}}
	stList.comments = map[string][]domain.PullRequestComment{ // adapt to how the fake exposes comments
		"pr1": {{ThreadID: "T1", ID: "c1", Body: "please\x1b]0;pwned\afix"}},
	}
	sent := &captureCommander{} // fake commander recording Send(id,message)
	svc := &Service{store: stList, manager: sent, renderer: stubRenderer{out: "PROMPT:\n\n{{comments}}"}}

	err := svc.DispatchCommentToWorker(context.Background(), "s1", "pr1", "T1", "also add a test")
	if err != nil {
		t.Fatal(err)
	}
	got := sent.lastMessage
	if strings.ContainsRune(got, '\x1b') || strings.ContainsRune(got, '\a') {
		t.Fatalf("dispatched message carries control bytes: %q", got)
	}
	if !strings.Contains(got, "also add a test") {
		t.Fatalf("extra prompt missing: %q", got)
	}
}

func TestDispatchCommentToWorker_UnknownSessionAndPR(t *testing.T) {
	// unknown session → NotFound; known session + unknown prURL → NotFound.
	// (Two sub-cases; assert errors are non-nil apierr.)
}
```

Provide the small stubs (adapt names to what the package already has — grep for an existing `commander` fake in `service_test.go` first and reuse it if present):

```go
type stubRenderer struct{ out string; err error }
func (s stubRenderer) Render(_ messagetemplates.Name, data any) (string, error) {
	if s.err != nil { return "", s.err }
	// echo the comments so the test can assert sanitization/content
	if d, ok := data.(messagetemplates.ReviewCommentData); ok { return s.out + "\n" + d.Comments, nil }
	return s.out, nil
}
type captureCommander struct { lastID domain.SessionID; lastMessage string /* + satisfy the commander interface */ }
func (c *captureCommander) Send(_ context.Context, id domain.SessionID, msg string) error { c.lastID = id; c.lastMessage = msg; return nil }
```

IMPORTANT: `captureCommander` must satisfy the full `commander` interface the `Service.manager` field requires. Read the `commander` interface in `service.go`; if it has many methods, either embed an existing fake or add no-op methods. Prefer reusing an existing test commander if the package has one.

- [ ] **Step 2: Run test → RED** (`go test ./internal/service/session/ -run DispatchCommentToWorker`) — undefined method.

- [ ] **Step 3: Implement** `comment_dispatch.go`:

```go
package session

import (
	"context"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/apierr" // verify: internal/httpd/apierr
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/messagetemplates"
)

// DispatchCommentToWorker renders the review-comment-dispatch template for one
// PR review thread's comments and delivers it to the worker session, appending
// the operator's optional extra prompt. Comment bodies and the extra prompt are
// attacker-influenceable and reach the worker PTY, so both are sanitized.
func (s *Service) DispatchCommentToWorker(ctx context.Context, id domain.SessionID, prURL, threadID, extraPrompt string) error {
	if _, ok, err := s.store.GetSession(ctx, id); err != nil {
		return err
	} else if !ok {
		return apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	prs, err := s.store.ListPRsBySession(ctx, id)
	if err != nil {
		return err
	}
	found := false
	for _, p := range prs {
		if p.URL == prURL {
			found = true
			break
		}
	}
	if !found {
		return apierr.NotFound("PR_NOT_FOUND", "Unknown PR for session")
	}
	comments, err := s.store.ListPRComments(ctx, prURL)
	if err != nil {
		return err
	}
	bodies := make([]string, 0, len(comments))
	for _, c := range comments {
		if c.ThreadID == threadID {
			bodies = append(bodies, domain.SanitizeControlChars(c.Body))
		}
	}
	if len(bodies) == 0 {
		return apierr.Invalid("NO_COMMENTS", "Thread has no comments to dispatch", nil)
	}
	if s.renderer == nil {
		return apierr.Invalid("DISPATCH_UNAVAILABLE", "Comment dispatch is not configured", nil)
	}
	msg, err := s.renderer.Render(messagetemplates.NameReviewCommentDispatch, messagetemplates.ReviewCommentData{Comments: strings.Join(bodies, "\n\n")})
	if err != nil {
		return err
	}
	if extra := strings.TrimSpace(extraPrompt); extra != "" {
		msg += "\n\n" + domain.SanitizeControlChars(extra)
	}
	return s.Send(ctx, id, msg)
}
```

Verify the `apierr` import path against `pr_comments.go` (it was `internal/httpd/apierr`). Verify `apierr.Invalid(code, msg, nil)` signature exists (grep an existing caller).

- [ ] **Step 4: Run test → GREEN.** `gofmt -l` + `go vet` clean.

- [ ] **Step 5: Commit** (`comment_dispatch.go` + `_test.go`), message `feat(session): DispatchCommentToWorker renders + sanitizes + sends`.

---

## Task 3: Endpoint `POST /sessions/{sessionId}/comment-dispatch`

**Files:** Modify `controllers/dto.go`, `sessions.go`, `sessions_test.go`, `apispec/specgen/build.go`; regen `openapi.yaml`, `schema.ts`.

**Interfaces:**

- Consumes: `session.Service.DispatchCommentToWorker`.
- Produces: route + `SessionService` interface method `DispatchCommentToWorker(ctx, id, prURL, threadID, extraPrompt) error`; DTOs `DispatchCommentRequest{PrURL, ThreadID, ExtraPrompt string}` + `DispatchCommentResponse{OK bool; SessionID domain.SessionID}`.

- [ ] **Step 1: Failing controller test** in `sessions_test.go` (mirror `TestSessionsAPI_...` + the fake `SessionService`):

```go
func TestSessionsAPI_CommentDispatch(t *testing.T) {
	fake := &fakeSessionService{ /* mirror */ }
	srv := newSessionsTestServer(t, fake)
	body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions/ao-1/comment-dispatch",
		`{"prUrl":"pr1","threadId":"T1","extraPrompt":"add a test"}`)
	if status != http.StatusOK || !strings.Contains(body, `"ok":true`) {
		t.Fatalf("status %d body %s", status, body)
	}
	if fake.dispatchedThread != "T1" || fake.dispatchedExtra != "add a test" {
		t.Fatalf("service not called with the right args: %+v", fake)
	}
}
```

Add to the fake: fields + `func (f *fakeSessionService) DispatchCommentToWorker(_ context.Context, _ domain.SessionID, prURL, threadID, extra string) error { f.dispatchedPR=prURL; f.dispatchedThread=threadID; f.dispatchedExtra=extra; return nil }`.

- [ ] **Step 2: RED** (`go test ./internal/httpd/controllers/ -run CommentDispatch`).

- [ ] **Step 3a: DTOs** (dto.go):

```go
// DispatchCommentRequest is the body of POST /sessions/{sessionId}/comment-dispatch.
type DispatchCommentRequest struct {
	PrURL       string `json:"prUrl"`
	ThreadID    string `json:"threadId"`
	ExtraPrompt string `json:"extraPrompt,omitempty"`
}

// DispatchCommentResponse acknowledges a manual comment dispatch to the worker.
type DispatchCommentResponse struct {
	OK        bool             `json:"ok"`
	SessionID domain.SessionID `json:"sessionId"`
}
```

- [ ] **Step 3b: Interface + route + handler** (sessions.go). Interface add: `DispatchCommentToWorker(ctx context.Context, id domain.SessionID, prURL, threadID, extraPrompt string) error`. Route (near `/send`): `r.Post("/sessions/{sessionId}/comment-dispatch", c.commentDispatch)`. Handler:

```go
func (c *SessionsController) commentDispatch(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/comment-dispatch")
		return
	}
	var in DispatchCommentRequest
	if err := decodeJSON(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	if in.PrURL == "" || in.ThreadID == "" {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "THREAD_REQUIRED", "prUrl and threadId are required", nil)
		return
	}
	if len(in.ExtraPrompt) > maxMessageLen {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "MESSAGE_TOO_LONG", "Extra prompt is too long", nil)
		return
	}
	if err := c.Svc.DispatchCommentToWorker(r.Context(), sessionID(r), in.PrURL, in.ThreadID, in.ExtraPrompt); err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, DispatchCommentResponse{OK: true, SessionID: sessionID(r)})
}
```

(Reuse `maxMessageLen`, `decodeJSON`, `envelope.*`, `sessionID` — grep the `send` handler for exact names.) Add the no-op `DispatchCommentToWorker` to the SECOND fake in `cli/dto_drift_e2e_test.go` too (interface compliance — same as Tasks 3/5 of Phase 2).

- [ ] **Step 3c: build.go operation + schemaNames.** Add to `sessionOperations()` (mirror the `send` op, method POST, `reqBody: controllers.DispatchCommentRequest{}`, `pathParams: []any{controllers.SessionIDParam{}}`, 200 `controllers.DispatchCommentResponse{}`, plus 400/404/500/501 `envelope.APIError{}`). Add schemaNames: `"ControllersDispatchCommentRequest": "DispatchCommentRequest"`, `"ControllersDispatchCommentResponse": "DispatchCommentResponse"`.

- [ ] **Step 4a:** `go test ./internal/httpd/controllers/ -run CommentDispatch` → GREEN.
- [ ] **Step 4b:** `npm run api` (root), then `cd backend && go test ./internal/httpd/...` → drift + parity PASS.
- [ ] **Step 5: Commit** all touched + generated files. Message `feat(api): POST /sessions/{id}/comment-dispatch (send comment to worker)`.

---

## Task 4: Frontend — "Send to worker" split button

**Files:**

- Create: `frontend/src/renderer/components/SendToWorkerButton.tsx` + `.test.tsx`
- Modify: `frontend/src/renderer/components/CommentsView.tsx`

**Interfaces:**

- Consumes: generated `POST /api/v1/sessions/{sessionId}/comment-dispatch`.
- Produces: `<SendToWorkerButton sessionId prUrl threadId />` rendered in `ThreadCard`.

- [ ] **Step 1: Failing test** `SendToWorkerButton.test.tsx` — mock `../lib/api-client` (mirror `DiffHunk.test.tsx`). Cover: (a) clicking the main "Send to worker" button POSTs with `extraPrompt: ""` and shows a "Sent" state; (b) opening the dropdown, typing extra instructions, and clicking send POSTs with the typed `extraPrompt`.

```tsx
// assert apiClient.POST called with path "/api/v1/sessions/{sessionId}/comment-dispatch",
// params.path.sessionId, and body { prUrl, threadId, extraPrompt }.
```

- [ ] **Step 2: RED** (`npm run test -- SendToWorkerButton`).

- [ ] **Step 3: Implement** `SendToWorkerButton.tsx`. A split button matching the renderer's design (read `DESIGN.md`; reuse shadcn `Button` from `components/ui/button` and, for the dropdown, an existing popover/dropdown primitive in `components/ui/*` if present — grep; otherwise a small controlled `useState` panel). Behavior:
  - Main button "Send to worker" → `mutate({ extraPrompt: "" })`.
  - Caret button toggles a small panel with a `<textarea>` ("Extra instructions for the worker (optional)") + a "Send with instructions" button → `mutate({ extraPrompt: text })`.
  - `useMutation` calls `apiClient.POST("/api/v1/sessions/{sessionId}/comment-dispatch", { params: { path: { sessionId } }, body: { prUrl, threadId, extraPrompt } })`; throw `apiErrorMessage(error)` on error.
  - On success: show a transient "Sent to worker ✓" (disable button briefly), close the panel, clear the textarea.
  - On error: show `apiErrorMessage` inline.

- [ ] **Step 4: Wire into `ThreadCard`** (CommentsView.tsx): render `<SendToWorkerButton sessionId={sessionId} prUrl={prUrl} threadId={thread.threadId} />` in the thread's action row (e.g. next to the file:line header or below the comments). `ThreadCard` already receives `sessionId` and `prUrl` (Task 7 of Phase 2). Update `CommentsView.test.tsx`'s api-client mock if the new button issues a call on render (it should NOT — it only POSTs on click — so no mock change needed; verify tests still pass).

- [ ] **Step 5:** `npm run test -- SendToWorkerButton CommentsView` + `npm run typecheck` → PASS. Revert codegen churn.
- [ ] **Step 6: Commit.** Message `feat(inspector): Send-to-worker split button on review threads`.

---

## Task 5: Full-suite verification

- [ ] **Step 1:** `cd backend && go build ./... && go test ./internal/service/session/ ./internal/httpd/... ./internal/daemon/... && go vet ./internal/service/session/ ./internal/httpd/... && gofmt -l internal/service/session internal/httpd/controllers internal/httpd/apispec internal/daemon` — all pass/clean.
- [ ] **Step 2:** `cd frontend && npm run typecheck && npm run test` — pass.
- [ ] **Step 3:** `git status` clean (no lockfile/routeTree churn; generated files committed in Task 3).

## Self-Review

- Manual dispatch replaces the removed auto-nudge; no automatic dispatch reintroduced. ✅
- Sanitization on both comment bodies and extra prompt before the PTY (Task 2). ✅
- Template stays operator-editable (reuses `NameReviewCommentDispatch`; the Global Settings editor is unchanged). ✅
- Authorization: dispatch only to the session's own PRs (ListPRsBySession filter). ✅
- Renderer nil-guard so a mis-wired daemon fails safe, not panics. ✅
- Open reconciliation notes for the executor: exact `apierr` import path + `apierr.Invalid` signature; the `commander` interface shape for the test fake; the fake `SessionService`/request-helper names in `sessions_test.go`; presence of a popover primitive in `components/ui/*`. Each instructs the implementer to match existing code.
