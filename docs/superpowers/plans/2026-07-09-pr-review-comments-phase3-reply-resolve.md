# PR Review Comments — Phase 3: Reply + Resolve Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let an operator reply to a PR/MR review thread and resolve it from inside the Comments tab, writing back to both GitHub and GitLab.

**Architecture:** Add an optional `ReviewThreadWriter` capability to the SCM provider layer (GitHub via GraphQL `addPullRequestReviewThreadReply` + `resolveReviewThread`; GitLab via REST `POST discussions/:id/notes` + `PUT discussions/:id?resolved=true`). The `composite.Provider` discovers the capability by type-assertion and routes by provider name. The session service exposes `ReplyToThread`/`ResolveThread` guarded by the same session→PR membership authz as Phase 4a's `DispatchCommentToWorker`. Two new session-scoped endpoints back a reply box + resolve button in `ThreadCard`. The write is authoritative on the remote immediately; the local SQLite read model reconciles on the observer's next review poll (≤2 min), so the frontend patches the React Query cache optimistically rather than round-tripping the stale store.

**Tech Stack:** Go (chi, code-first OpenAPI via specgen), React + TanStack Query, openapi-fetch, vitest.

## Global Constraints

- **Session-scoped endpoints, NOT `/prs/{id}`.** The design spec (`docs/superpowers/specs/2026-07-09-pr-review-comments-tab-design.md` §3) proposed `POST /prs/{id}/threads/{threadId}/reply` and reusing the `resolve-comments` stub. This plan deliberately deviates: the Comments tab and Phase 4a Send-to-worker are session-scoped and already carry `{sessionId, prUrl, threadId}`, and no `prID→pr_url` store lookup exists. New routes: `POST /api/v1/sessions/{sessionId}/comment-reply` and `POST /api/v1/sessions/{sessionId}/comment-resolve`. The `service/pr` `resolve-comments` stub is left untouched (it is unwired — `daemon.go` sets no `PRs:` dep — so nothing regresses).
- **No sqlc / store write-through in v1.** After a successful remote write, the frontend patches the `["session-pr-comments", sessionId]` query cache optimistically. The observer's review poll (`DefaultReviewInterval = 2m`) reconciles the SQLite read model. Do not add store INSERT/UPDATE for comments/threads in this phase.
- **Authz mirrors Phase 4a exactly:** `GetSession` (unknown → `apierr.NotFound("SESSION_NOT_FOUND", …)`) then `ListPRsBySession` membership check on `prUrl` (unknown → `apierr.NotFound("PR_NOT_FOUND", …)`). See `internal/service/session/comment_dispatch.go`.
- **Error contract:** `ports.ErrSCMNotFound` from the provider → `apierr.NotFound("THREAD_NOT_FOUND", "Review thread not found")` (404). A new `ports.ErrSCMForbidden` (write-scope failure) → session sentinel `ErrSCMWriteForbidden` → 403 `SCM_WRITE_FORBIDDEN`. Nil `s.scm` or any other provider error → existing `ErrSCMUnavailable` → 503 `SCM_UNAVAILABLE`.
- **Reply body is operator-typed**, sanitized with `domain.SanitizeControlChars` in the controller (consistent with `/send`), capped at `maxMessageLen` (4096).
- **OpenAPI is code-first.** Edit `backend/internal/httpd/controllers/dto.go` + `backend/internal/httpd/apispec/specgen/build.go`, then run `npm run api` at repo root. NEVER hand-edit `openapi.yaml` or `frontend/src/api/schema.ts`. Drift guard `TestBuild_MatchesEmbedded` and `parity_test.go` run under `go test ./internal/httpd/...`.
- **Frontend clones agent-orchestrator design** (see DESIGN.md banner); build from `components/ui/*` primitives. Reuse the `Textarea` + `Button` idiom already in `SendToWorkerButton.tsx`.
- **api-client `ROUTE_TEMPLATES`** (`frontend/src/renderer/lib/api-client.ts`) must gain both new routes ("keep in sync with schema.ts").

---

### Task 1: GitHub write methods + shared capability contract

**Files:**
- Modify: `backend/internal/ports/scm_observations.go` (add `ErrSCMForbidden`)
- Create: `backend/internal/observe/scm/thread_writer.go` (the `ReviewThreadWriter` interface)
- Create: `backend/internal/adapters/scm/github/write.go`
- Test: `backend/internal/adapters/scm/github/write_test.go`

**Interfaces:**
- Produces (consumed by Tasks 2–4):
  ```go
  // internal/observe/scm/thread_writer.go
  package scm
  type ReviewThreadWriter interface {
      ReplyToThread(ctx context.Context, ref ports.SCMPRRef, threadID, body string) (ports.SCMReviewCommentObservation, error)
      ResolveThread(ctx context.Context, ref ports.SCMPRRef, threadID string) error
  }
  ```
- Produces: `ports.ErrSCMForbidden = errors.New("scm: forbidden")` (write-scope/permission failure sentinel, sibling to `ErrSCMNotFound`).
- Consumes: `github.Client.doGraphQL(ctx, query string, variables map[string]any) (map[string]any, error)` (client.go:267); `github.ErrAuthFailed` (client.go:31); `github.ErrNotFound == ports.ErrSCMNotFound` (client.go:30); helpers `str`, `num`, `boolv`, `isBotAuthor`, `authorLogin` in observer_provider.go.

- [ ] **Step 1: Write failing tests** in `write_test.go`. Mirror the httptest harness in `provider_test.go` (a test server returning canned GraphQL JSON; construct the provider pointing `graphqlURL`/`APIBase` at it — copy the existing test's provider construction). Cases:
  - `TestReplyToThread_PostsMutationAndParsesComment`: server asserts the request body contains `addPullRequestReviewThreadReply` and the `variables` map `{"threadId":"PRRT_x","body":"looks good"}`; responds `{"data":{"addPullRequestReviewThreadReply":{"comment":{"id":"PRRC_1","body":"looks good","url":"https://gh/c1","author":{"login":"me","__typename":"User"}}}}}`. Assert returned `ports.SCMReviewCommentObservation` has `ID=="PRRC_1"`, `Author=="me"`, `Body=="looks good"`, `URL=="https://gh/c1"`, `IsBot==false`.
  - `TestResolveThread_PostsMutation`: server asserts body contains `resolveReviewThread` and variables `{"threadId":"PRRT_x"}`; responds `{"data":{"resolveReviewThread":{"thread":{"id":"PRRT_x","isResolved":true}}}}`. Assert `err == nil`.
  - `TestReplyToThread_AuthFailedMapsToForbidden`: server returns HTTP 401. Assert `errors.Is(err, ports.ErrSCMForbidden)`.
  - `TestResolveThread_NotFoundMapsToSCMNotFound`: server returns a GraphQL `errors` array with message `"Could not resolve to a node"`. Assert `errors.Is(err, ports.ErrSCMNotFound)`.
  - Add a compile assertion in the test file: `var _ scmobserve.ReviewThreadWriter = (*Provider)(nil)` (import the observe/scm package aliased `scmobserve`).

- [ ] **Step 2: Run tests to verify they fail** — `go test ./internal/adapters/scm/github/ -run 'ReplyToThread|ResolveThread'` → FAIL (methods undefined).

- [ ] **Step 3: Add `ports.ErrSCMForbidden`** to `scm_observations.go` next to `ErrSCMNotFound`:
  ```go
  // ErrSCMForbidden is the provider-neutral sentinel for a write the token is
  // not permitted to make (missing write scope / insufficient permission).
  var ErrSCMForbidden = errors.New("scm: forbidden")
  ```

- [ ] **Step 4: Create `thread_writer.go`** with the `ReviewThreadWriter` interface above (package `scm`, imports `context` + `ports`).

- [ ] **Step 5: Implement `write.go`:**
  ```go
  package github

  const replyThreadMutation = `mutation($threadId:ID!,$body:String!){addPullRequestReviewThreadReply(input:{pullRequestReviewThreadId:$threadId,body:$body}){comment{id body url author{login __typename}}}}`
  const resolveThreadMutation = `mutation($threadId:ID!){resolveReviewThread(input:{threadId:$threadId}){thread{id isResolved}}}`

  func (p *Provider) ReplyToThread(ctx context.Context, ref ports.SCMPRRef, threadID, body string) (ports.SCMReviewCommentObservation, error) {
      data, err := p.client.doGraphQL(ctx, replyThreadMutation, map[string]any{"threadId": threadID, "body": body})
      if err != nil {
          return ports.SCMReviewCommentObservation{}, classifyWriteErr(err)
      }
      reply, _ := data["addPullRequestReviewThreadReply"].(map[string]any)
      cn, _ := reply["comment"].(map[string]any)
      author, _ := cn["author"].(map[string]any)
      return ports.SCMReviewCommentObservation{
          ID:     str(cn["id"]),
          Author: str(author["login"]),
          Body:   str(cn["body"]),
          URL:    str(cn["url"]),
          IsBot:  isBotAuthor(author),
      }, nil
  }

  func (p *Provider) ResolveThread(ctx context.Context, ref ports.SCMPRRef, threadID string) error {
      _, err := p.client.doGraphQL(ctx, resolveThreadMutation, map[string]any{"threadId": threadID})
      return classifyWriteErr(err)
  }

  // classifyWriteErr maps client transport errors onto the provider-neutral
  // write sentinels. ErrNotFound is already ports.ErrSCMNotFound (client.go:30),
  // so it passes through; auth failures become ports.ErrSCMForbidden so the
  // service can render a distinct 403 instead of a generic 503.
  func classifyWriteErr(err error) error {
      if err == nil {
          return nil
      }
      if errors.Is(err, ErrAuthFailed) {
          return fmt.Errorf("%w: %w", ports.ErrSCMForbidden, err)
      }
      return err
  }
  ```
  Add a `var _ scmobserve.ReviewThreadWriter = (*Provider)(nil)` in write.go too (import alias `scmobserve "…/internal/observe/scm"`).

- [ ] **Step 6: Run tests to verify they pass** — `go test ./internal/adapters/scm/github/ -run 'ReplyToThread|ResolveThread'` → PASS. Then `go build ./... && gofmt -l internal/adapters/scm/github/` (expect no output).

- [ ] **Step 7: Commit** — `git commit -am "feat(scm/github): reply + resolve review thread writes"`

---

### Task 2: GitLab write methods

**Files:**
- Create: `backend/internal/adapters/scm/gitlab/write.go`
- Test: `backend/internal/adapters/scm/gitlab/write_test.go`

**Interfaces:**
- Produces: `gitlab.Provider` satisfies `scmobserve.ReviewThreadWriter`.
- Consumes: `gitlab.Client.doRESTWithETagAndMethod(ctx, method, path string, q url.Values, etag string, body any) (restResponse, error)` (client.go:87); `restResponse{Body []byte; Status int}` (client.go:66); `restNote` (observer_provider.go:529); `projectID(repo ports.SCMRepo) string` (observer_provider.go:88); `isBotUsername` (observer_provider.go:682); `gitlab.ErrAuthFailed` (client.go:197); GitLab's `classifyError` maps 404 → the package `ErrNotFound`. Verify GitLab's not-found sentinel: `grep -n "ErrNotFound" internal/adapters/scm/gitlab/*.go`. If GitLab does NOT already alias `ports.ErrSCMNotFound`, translate 404 explicitly in `classifyGitlabWriteErr` (below) by checking `resp.Status == 404`.

- [ ] **Step 1: Write failing tests** in `write_test.go`, mirroring `fetch_test.go`'s httptest harness. Cases:
  - `TestReplyToThread_PostsNoteAndParses`: server asserts `POST /api/v4/projects/{esc}/merge_requests/7/discussions/disc1/notes` with JSON body `{"body":"thanks"}`; responds `{"id":42,"body":"thanks","author":{"username":"me"}}`. Ref built as `ports.SCMPRRef{Repo: ports.SCMRepo{Provider:"gitlab", Repo:"grp/proj"}, Number:7}`, threadID `"disc1"`. Assert returned obs `ID=="42"`, `Author=="me"`, `Body=="thanks"`, `IsBot==false`.
  - `TestResolveThread_PutsResolved`: server asserts `PUT /api/v4/projects/{esc}/merge_requests/7/discussions/disc1` with query `resolved=true`; responds `{"id":"disc1"}`, HTTP 200. Assert `err == nil`.
  - `TestReplyToThread_AuthFailedMapsToForbidden`: server returns HTTP 401 → assert `errors.Is(err, ports.ErrSCMForbidden)`.
  - `TestResolveThread_NotFoundMapsToSCMNotFound`: server returns HTTP 404 → assert `errors.Is(err, ports.ErrSCMNotFound)`.
  - Compile assertion: `var _ scmobserve.ReviewThreadWriter = (*Provider)(nil)`.

- [ ] **Step 2: Run tests to verify they fail** — `go test ./internal/adapters/scm/gitlab/ -run 'ReplyToThread|ResolveThread'` → FAIL.

- [ ] **Step 3: Implement `write.go`:**
  ```go
  package gitlab

  func (p *Provider) ReplyToThread(ctx context.Context, ref ports.SCMPRRef, threadID, body string) (ports.SCMReviewCommentObservation, error) {
      path := "projects/" + projectID(ref.Repo) + "/merge_requests/" + strconv.Itoa(ref.Number) + "/discussions/" + url.PathEscape(threadID) + "/notes"
      resp, err := p.client.doRESTWithETagAndMethod(ctx, http.MethodPost, path, nil, "", map[string]string{"body": body})
      if err != nil {
          return ports.SCMReviewCommentObservation{}, classifyGitlabWriteErr(resp, err)
      }
      var n restNote
      if err := json.Unmarshal(resp.Body, &n); err != nil {
          return ports.SCMReviewCommentObservation{}, fmt.Errorf("gitlab scm: decode reply note: %w", err)
      }
      return ports.SCMReviewCommentObservation{
          ID:     strconv.Itoa(n.ID),
          Author: n.Author.Username,
          Body:   n.Body,
          IsBot:  isBotUsername(n.Author.Username),
      }, nil
  }

  func (p *Provider) ResolveThread(ctx context.Context, ref ports.SCMPRRef, threadID string) error {
      path := "projects/" + projectID(ref.Repo) + "/merge_requests/" + strconv.Itoa(ref.Number) + "/discussions/" + url.PathEscape(threadID)
      q := url.Values{"resolved": {"true"}}
      resp, err := p.client.doRESTWithETagAndMethod(ctx, http.MethodPut, path, q, "", nil)
      if err != nil {
          return classifyGitlabWriteErr(resp, err)
      }
      return nil
  }

  func classifyGitlabWriteErr(resp restResponse, err error) error {
      if err == nil {
          return nil
      }
      if errors.Is(err, ErrAuthFailed) {
          return fmt.Errorf("%w: %w", ports.ErrSCMForbidden, err)
      }
      if resp.Status == http.StatusNotFound {
          return fmt.Errorf("%w: %w", ports.ErrSCMNotFound, err)
      }
      return err
  }
  ```
  Add `var _ scmobserve.ReviewThreadWriter = (*Provider)(nil)`.

- [ ] **Step 4: Run tests to verify they pass** — `go test ./internal/adapters/scm/gitlab/ -run 'ReplyToThread|ResolveThread'` → PASS. Then `go build ./... && gofmt -l internal/adapters/scm/gitlab/`.

- [ ] **Step 5: Commit** — `git commit -am "feat(scm/gitlab): reply + resolve MR discussion writes"`

---

### Task 3: Composite routing for the write capability

**Files:**
- Modify: `backend/internal/adapters/scm/composite/provider.go`
- Test: `backend/internal/adapters/scm/composite/provider_test.go` (add cases)

**Interfaces:**
- Produces: `composite.Provider` satisfies `scmobserve.ReviewThreadWriter`, routing by `ref.Repo.Provider`.
- Consumes: `p.lookup(name)` (provider.go:42); `scmobserve.ReviewThreadWriter` (Task 1).

- [ ] **Step 1: Write failing tests.** Add a fake child in the test file that implements both `scmobserve.Provider` and `ReviewThreadWriter`, recording the `(ref, threadID, body)` it received. Cases:
  - `TestReplyToThread_RoutesByProvider`: composite of `{"github": writer}`; call with `ref.Repo.Provider=="github"`; assert the github child received the call and the returned comment propagates.
  - `TestResolveThread_UnknownProviderErrors`: call with `ref.Repo.Provider=="nope"` → error (from `lookup`).
  - `TestReplyToThread_ChildWithoutWriterErrors`: composite whose child implements only `scmobserve.Provider` (not the writer); assert a clear "does not support" error and `errors.Is` is neither ErrSCMNotFound nor ErrSCMForbidden.

- [ ] **Step 2: Run to verify fail** — `go test ./internal/adapters/scm/composite/ -run 'ReplyToThread|ResolveThread'` → FAIL.

- [ ] **Step 3: Implement** in provider.go:
  ```go
  func (p *Provider) ReplyToThread(ctx context.Context, ref ports.SCMPRRef, threadID, body string) (ports.SCMReviewCommentObservation, error) {
      child, err := p.lookup(ref.Repo.Provider)
      if err != nil {
          return ports.SCMReviewCommentObservation{}, err
      }
      w, ok := child.(scmobserve.ReviewThreadWriter)
      if !ok {
          return ports.SCMReviewCommentObservation{}, fmt.Errorf("composite scm: provider %q does not support thread writes", ref.Repo.Provider)
      }
      return w.ReplyToThread(ctx, ref, threadID, body)
  }

  func (p *Provider) ResolveThread(ctx context.Context, ref ports.SCMPRRef, threadID string) error {
      child, err := p.lookup(ref.Repo.Provider)
      if err != nil {
          return err
      }
      w, ok := child.(scmobserve.ReviewThreadWriter)
      if !ok {
          return fmt.Errorf("composite scm: provider %q does not support thread writes", ref.Repo.Provider)
      }
      return w.ResolveThread(ctx, ref, threadID)
  }
  ```
  Add `var _ scmobserve.ReviewThreadWriter = (*Provider)(nil)` near the existing `var _ scmobserve.Provider` line (provider.go:28).

- [ ] **Step 4: Run to verify pass** — `go test ./internal/adapters/scm/composite/` → PASS. `go build ./... && gofmt -l internal/adapters/scm/composite/`.

- [ ] **Step 5: Commit** — `git commit -am "feat(scm/composite): route reply + resolve thread writes"`

---

### Task 4: Session service `ReplyToThread` + `ResolveThread`

**Files:**
- Modify: `backend/internal/service/session/service.go` (extend `scmProvider` interface, add `ErrSCMWriteForbidden`)
- Create: `backend/internal/service/session/comment_write.go`
- Test: `backend/internal/service/session/comment_write_test.go`
- Modify: `backend/internal/service/session/service_test.go` (extend `fakeSCM` at :728)

**Interfaces:**
- Consumes: `scmRepoForClaim(provider scmProvider, projectOrigin, prURL string) (ports.SCMRepo, error)` (claim_pr.go:176); `s.store.GetSession`, `s.store.ListPRsBySession` (returns `[]domain.PullRequest` with `.URL`, `.Number`), `s.store.GetProject(ctx, string(projectID)) (Project, bool, error)` where `Project.RepoOriginURL` is the git origin; `s.clock() time.Time`; `PRThreadComment{ID, Author, Body, URL, Resolved, IsBot, CreatedAt}` (pr_comments.go); `apierr`, `ports.ErrSCMNotFound`, `ports.ErrSCMForbidden`; existing `ErrSCMUnavailable`.
- Produces (consumed by Task 5):
  ```go
  func (s *Service) ReplyToThread(ctx context.Context, id domain.SessionID, prURL, threadID, body string) (PRThreadComment, error)
  func (s *Service) ResolveThread(ctx context.Context, id domain.SessionID, prURL, threadID string) error
  var ErrSCMWriteForbidden = errors.New("scm write forbidden")
  ```

- [ ] **Step 1: Extend the `scmProvider` interface** in service.go (:85) with the two write methods:
  ```go
  ReplyToThread(ctx context.Context, ref ports.SCMPRRef, threadID, body string) (ports.SCMReviewCommentObservation, error)
  ResolveThread(ctx context.Context, ref ports.SCMPRRef, threadID string) error
  ```
  Add `var ErrSCMWriteForbidden = errors.New("scm write forbidden")` alongside `ErrSCMUnavailable`.

- [ ] **Step 2: Extend `fakeSCM`** (service_test.go:728) with configurable fields + the two methods (value receiver, matching the existing style):
  ```go
  // add fields:
  replyComment ports.SCMReviewCommentObservation
  replyErr     error
  resolveErr   error
  lastRef      ports.SCMPRRef
  lastThreadID string
  lastBody     string
  // NOTE: value receiver can't record into the struct; make these methods on a
  // *pointer* fakeSCM OR return the configured value without recording. The
  // happy-path assertions below only need the RETURN value, so value-receiver
  // methods returning f.replyComment/f.replyErr suffice. Keep it value-receiver
  // to match ParseRepository/FetchReviewThreads.
  func (f fakeSCM) ReplyToThread(_ context.Context, _ ports.SCMPRRef, _, _ string) (ports.SCMReviewCommentObservation, error) {
      return f.replyComment, f.replyErr
  }
  func (f fakeSCM) ResolveThread(_ context.Context, _ ports.SCMPRRef, _ string) error {
      return f.resolveErr
  }
  ```

- [ ] **Step 3: Write failing tests** in `comment_write_test.go`. Build the service with a `fakeStore`/`multiPRFakeStore` seeded with a session + one PR (`URL:"pr1"`, `Number:7`) and a project resolvable via `GetProject`, plus a `fakeSCM` that `ParseRepository` returns a github repo for. (Inspect claim_pr_test.go for the exact store seeding of session + project + PR.) Cases:
  - `TestReplyToThread_ReturnsComment`: `fakeSCM.replyComment = {ID:"c9", Author:"me", Body:"ok"}`; call `ReplyToThread(ctx,"s1","pr1","T1","ok")`; assert returned `PRThreadComment` has `ID=="c9"`, `Author=="me"`, `Body=="ok"`, `Resolved==false`, `CreatedAt` non-zero.
  - `TestResolveThread_OK`: `fakeSCM.resolveErr = nil`; assert `err == nil`.
  - `TestReplyToThread_UnknownSession` → error (SESSION_NOT_FOUND).
  - `TestReplyToThread_UnknownPR` (prURL not in ListPRsBySession) → error (PR_NOT_FOUND).
  - `TestReplyToThread_NilSCMUnavailable`: service with `scm: nil` → `errors.Is(err, ErrSCMUnavailable)`.
  - `TestReplyToThread_ProviderNotFound`: `fakeSCM.replyErr = fmt.Errorf("%w", ports.ErrSCMNotFound)` → returned error is `apierr` NotFound with code `THREAD_NOT_FOUND` (assert via `errors.As(err, &apierr.Error{})` and `.Code`).
  - `TestReplyToThread_ProviderForbidden`: `fakeSCM.replyErr = fmt.Errorf("%w", ports.ErrSCMForbidden)` → `errors.Is(err, ErrSCMWriteForbidden)`.

- [ ] **Step 4: Run to verify fail** — `go test ./internal/service/session/ -run 'ReplyToThread|ResolveThread'` → FAIL.

- [ ] **Step 5: Implement `comment_write.go`.** Factor the shared authz+ref building into one helper, then the two public methods:
  ```go
  package session

  // resolveThreadRef runs the Phase-4a authz (session exists + PR belongs to
  // session) and rebuilds the SCM ref for a write. Returns the ref and the
  // matched PR number.
  func (s *Service) resolveThreadRef(ctx context.Context, id domain.SessionID, prURL string) (ports.SCMPRRef, error) {
      rec, ok, err := s.store.GetSession(ctx, id)
      if err != nil {
          return ports.SCMPRRef{}, err
      }
      if !ok {
          return ports.SCMPRRef{}, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
      }
      prs, err := s.store.ListPRsBySession(ctx, id)
      if err != nil {
          return ports.SCMPRRef{}, err
      }
      var number int
      found := false
      for _, p := range prs {
          if p.URL == prURL {
              number, found = p.Number, true
              break
          }
      }
      if !found {
          return ports.SCMPRRef{}, apierr.NotFound("PR_NOT_FOUND", "Unknown PR for session")
      }
      if s.scm == nil {
          return ports.SCMPRRef{}, ErrSCMUnavailable
      }
      var origin string
      if proj, ok, err := s.store.GetProject(ctx, string(rec.ProjectID)); err == nil && ok {
          origin = proj.RepoOriginURL
      }
      repo, err := scmRepoForClaim(s.scm, origin, prURL)
      if err != nil {
          return ports.SCMPRRef{}, err
      }
      return ports.SCMPRRef{Repo: repo, Number: number, URL: prURL}, nil
  }

  // mapThreadWriteErr converts provider-neutral SCM write sentinels into the
  // API error vocabulary. ErrSCMNotFound → 404 THREAD_NOT_FOUND; ErrSCMForbidden
  // → ErrSCMWriteForbidden (controller renders 403); everything else → 503.
  func mapThreadWriteErr(err error) error {
      switch {
      case err == nil:
          return nil
      case errors.Is(err, ports.ErrSCMNotFound):
          return apierr.NotFound("THREAD_NOT_FOUND", "Review thread not found")
      case errors.Is(err, ports.ErrSCMForbidden):
          return ErrSCMWriteForbidden
      default:
          return fmt.Errorf("%w: %w", ErrSCMUnavailable, err)
      }
  }

  func (s *Service) ReplyToThread(ctx context.Context, id domain.SessionID, prURL, threadID, body string) (PRThreadComment, error) {
      ref, err := s.resolveThreadRef(ctx, id, prURL)
      if err != nil {
          return PRThreadComment{}, err
      }
      obs, err := s.scm.ReplyToThread(ctx, ref, threadID, body)
      if err != nil {
          return PRThreadComment{}, mapThreadWriteErr(err)
      }
      return PRThreadComment{
          ID:        obs.ID,
          Author:    obs.Author,
          Body:      obs.Body,
          URL:       obs.URL,
          Resolved:  false,
          IsBot:     obs.IsBot,
          CreatedAt: s.clock().UTC(),
      }, nil
  }

  func (s *Service) ResolveThread(ctx context.Context, id domain.SessionID, prURL, threadID string) error {
      ref, err := s.resolveThreadRef(ctx, id, prURL)
      if err != nil {
          return err
      }
      return mapThreadWriteErr(s.scm.ResolveThread(ctx, ref, threadID))
  }
  ```
  Confirm `PRThreadComment` field names against `pr_comments.go` (`CreatedAt` may be `time.Time`; if the type uses different casing, match it). If `Service` has no `clock` when built in a test, the existing tests set it — verify `s.clock` is non-nil in the test service construction (claim_pr tests set a clock; reuse that).

- [ ] **Step 6: Run to verify pass** — `go test ./internal/service/session/ -run 'ReplyToThread|ResolveThread'` → PASS, then the whole package `go test ./internal/service/session/`. `go build ./... && gofmt -l internal/service/session/`.

- [ ] **Step 7: Commit** — `git commit -am "feat(session): ReplyToThread + ResolveThread write path"`

---

### Task 5: HTTP endpoints + DTOs + OpenAPI

**Files:**
- Modify: `backend/internal/httpd/controllers/dto.go` (request/response DTOs)
- Modify: `backend/internal/httpd/controllers/sessions.go` (routes, handlers, `SessionService` interface, error mapping)
- Modify: `backend/internal/httpd/apispec/specgen/build.go` (operation registry + schemaNames)
- Modify: `frontend/src/renderer/lib/api-client.ts` (ROUTE_TEMPLATES)
- Regenerate: `openapi.yaml`, `frontend/src/api/schema.ts` (via `npm run api`)
- Test: `backend/internal/httpd/controllers/sessions_test.go` (or the existing controller test file) + the fake SessionService used there

**Interfaces:**
- Consumes: `sessionsvc.Service.ReplyToThread`/`ResolveThread` (Task 4); `sessionsvc.ErrSCMWriteForbidden`, `sessionsvc.ErrSCMUnavailable`; `domain.SanitizeControlChars`; `envelope.WriteError`/`WriteAPIError`.
- Produces (consumed by Task 6): OpenAPI schemas `ReplyCommentRequest`, `ReplyCommentResponse`, `ResolveThreadRequest`, `ResolveThreadResponse`; routes `/api/v1/sessions/{sessionId}/comment-reply`, `/comment-resolve`.

- [ ] **Step 1: Add DTOs** to dto.go (mirror `DispatchCommentRequest`/`DispatchCommentResponse` — same file, same json-tag style):
  ```go
  type ReplyCommentRequest struct {
      PrURL    string `json:"prUrl"`
      ThreadID string `json:"threadId"`
      Body     string `json:"body"`
  }
  type ReplyCommentResponse struct {
      OK      bool                   `json:"ok"`
      Comment SessionPRThreadComment `json:"comment"`
  }
  type ResolveThreadRequest struct {
      PrURL    string `json:"prUrl"`
      ThreadID string `json:"threadId"`
  }
  type ResolveThreadResponse struct {
      OK        bool             `json:"ok"`
      SessionID domain.SessionID `json:"sessionId"`
      Resolved  bool             `json:"resolved"`
  }
  ```
  (`SessionPRThreadComment` already exists from Phase 2 — reuse it; find its constructor/mapper `sessionPRThreadComment(...)` and reuse to map the service `PRThreadComment`.)

- [ ] **Step 2: Extend `SessionService` interface** (sessions.go:34) with:
  ```go
  ReplyToThread(ctx context.Context, id domain.SessionID, prURL, threadID, body string) (sessionsvc.PRThreadComment, error)
  ResolveThread(ctx context.Context, id domain.SessionID, prURL, threadID string) error
  ```

- [ ] **Step 3: Register routes** in `Register` (after the `comment-dispatch` line, :92):
  ```go
  r.Post("/sessions/{sessionId}/comment-reply", c.commentReply)
  r.Post("/sessions/{sessionId}/comment-resolve", c.commentResolve)
  ```

- [ ] **Step 4: Write failing controller tests** first (mirror the `comment-dispatch` controller test). Build the router with a fake `SessionService` whose `ReplyToThread`/`ResolveThread` are stubbable. Cases: 200 reply returns the comment JSON; 400 when `prUrl`/`threadId` missing; 400 when body empty for reply; 403 when service returns `ErrSCMWriteForbidden`; 503 when `ErrSCMUnavailable`; 404 when service returns `apierr.NotFound("THREAD_NOT_FOUND",…)`. Update the fake SessionService in the test file to implement the two new interface methods. Run → FAIL.

- [ ] **Step 5: Implement handlers** in sessions.go:
  ```go
  func (c *SessionsController) commentReply(w http.ResponseWriter, r *http.Request) {
      if c.Svc == nil {
          apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/comment-reply")
          return
      }
      var in ReplyCommentRequest
      if err := decodeJSON(r, &in); err != nil {
          envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
          return
      }
      if in.PrURL == "" || in.ThreadID == "" {
          envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "THREAD_REQUIRED", "prUrl and threadId are required", nil)
          return
      }
      body := domain.SanitizeControlChars(in.Body)
      if strings.TrimSpace(body) == "" {
          envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "BODY_REQUIRED", "Reply body is required", nil)
          return
      }
      if len(body) > maxMessageLen {
          envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "MESSAGE_TOO_LONG", "Reply body is too long", nil)
          return
      }
      comment, err := c.Svc.ReplyToThread(r.Context(), sessionID(r), in.PrURL, in.ThreadID, body)
      if err != nil {
          writeThreadWriteError(w, r, err)
          return
      }
      envelope.WriteJSON(w, http.StatusOK, ReplyCommentResponse{OK: true, Comment: newSessionPRThreadComment(comment)})
  }

  func (c *SessionsController) commentResolve(w http.ResponseWriter, r *http.Request) {
      if c.Svc == nil {
          apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/comment-resolve")
          return
      }
      var in ResolveThreadRequest
      if err := decodeJSON(r, &in); err != nil {
          envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
          return
      }
      if in.PrURL == "" || in.ThreadID == "" {
          envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "THREAD_REQUIRED", "prUrl and threadId are required", nil)
          return
      }
      if err := c.Svc.ResolveThread(r.Context(), sessionID(r), in.PrURL, in.ThreadID); err != nil {
          writeThreadWriteError(w, r, err)
          return
      }
      envelope.WriteJSON(w, http.StatusOK, ResolveThreadResponse{OK: true, SessionID: sessionID(r), Resolved: true})
  }

  // writeThreadWriteError maps thread-write sentinels to explicit statuses,
  // delegating apierr-coded errors (SESSION_NOT_FOUND / PR_NOT_FOUND /
  // THREAD_NOT_FOUND) to envelope.WriteError.
  func writeThreadWriteError(w http.ResponseWriter, r *http.Request, err error) {
      switch {
      case errors.Is(err, sessionsvc.ErrSCMWriteForbidden):
          envelope.WriteAPIError(w, r, http.StatusForbidden, "forbidden", "SCM_WRITE_FORBIDDEN", "The configured token cannot write to this thread (missing write scope)", nil)
      case errors.Is(err, sessionsvc.ErrSCMUnavailable):
          envelope.WriteAPIError(w, r, http.StatusServiceUnavailable, "unavailable", "SCM_UNAVAILABLE", "SCM unavailable", nil)
      default:
          envelope.WriteError(w, r, err)
      }
  }
  ```
  Find the Phase-2 mapper that converts `sessionsvc.PRThreadComment` → `SessionPRThreadComment` (grep `SessionPRThreadComment` in dto.go/sessions.go) and use it as `newSessionPRThreadComment`; if none exists, add a small mapper.

- [ ] **Step 6: Register OpenAPI operations** in specgen/build.go. Find `sessionOperations()` and the `comment-dispatch` operation registration; add two entries for the new POST routes with their request/response bodies, and add the four new DTO type names to the `schemaNames` map (keys prefixed `Controllers…`, e.g. `ControllersReplyCommentRequest`). Mirror the exact shape of the `comment-dispatch` operation entry.

- [ ] **Step 7: Regenerate + verify drift** — from repo root run `npm run api`. Then `go test ./internal/httpd/...` (runs `TestBuild_MatchesEmbedded` + `parity_test`) → PASS. If drift fails, re-run `npm run api` and re-check; never hand-edit generated files.

- [ ] **Step 8: Add routes to `ROUTE_TEMPLATES`** in api-client.ts (next to `/comment-dispatch`, :68):
  ```ts
  "/api/v1/sessions/{sessionId}/comment-reply",
  "/api/v1/sessions/{sessionId}/comment-resolve",
  ```

- [ ] **Step 9: Run controller tests to verify pass** — `go test ./internal/httpd/controllers/ -run 'CommentReply|CommentResolve|commentReply|commentResolve'` → PASS. `go build ./... && gofmt -l internal/httpd/`.

- [ ] **Step 10: Commit** — `git commit -am "feat(httpd): comment-reply + comment-resolve endpoints"`

---

### Task 6: Frontend mutation hooks with optimistic cache update

**Files:**
- Create: `frontend/src/renderer/hooks/useThreadActions.ts`
- Test: `frontend/src/renderer/hooks/useThreadActions.test.ts`

**Interfaces:**
- Consumes: `apiClient.POST("/api/v1/sessions/{sessionId}/comment-reply" | "/comment-resolve")`; `useQueryClient`; the `["session-pr-comments", sessionId]` cache shape = `PRCommentGroup[]` (each group `{prUrl, threads:[{threadId, resolved, comments:[…]}]}`).
- Produces (consumed by Task 7): `useReplyToThread(sessionId)` and `useResolveThread(sessionId)` returning TanStack mutations.

- [ ] **Step 1: Write failing tests.** Using a `QueryClient` seeded with a `["session-pr-comments", sessionId]` value of one group/one thread/one comment: 
  - reply mutation success appends the returned comment to the matching thread's `comments` (patch via `setQueryData`, do NOT invalidate);
  - resolve mutation success sets the matching thread's `resolved` to `true`. 
  Mock `apiClient.POST` (mirror how existing hook tests mock it). Run → FAIL.

- [ ] **Step 2: Implement `useThreadActions.ts`:**
  ```ts
  import { useMutation, useQueryClient } from "@tanstack/react-query";
  import type { components } from "../../api/schema";
  import { apiClient, apiErrorMessage } from "../lib/api-client";
  import type { PRCommentGroup } from "./useSessionPRComments";

  type ThreadComment = PRCommentGroup["threads"][number]["comments"][number];

  function patchGroups(
      groups: PRCommentGroup[] | undefined,
      prUrl: string,
      threadId: string,
      fn: (thread: PRCommentGroup["threads"][number]) => PRCommentGroup["threads"][number],
  ): PRCommentGroup[] {
      return (groups ?? []).map((g) =>
          g.prUrl !== prUrl
              ? g
              : { ...g, threads: g.threads.map((t) => (t.threadId === threadId ? fn(t) : t)) },
      );
  }

  export function useReplyToThread(sessionId: string) {
      const qc = useQueryClient();
      return useMutation({
          mutationFn: async (vars: { prUrl: string; threadId: string; body: string }) => {
              const { data, error } = await apiClient.POST("/api/v1/sessions/{sessionId}/comment-reply", {
                  params: { path: { sessionId } },
                  body: vars,
              });
              if (error) throw new Error(apiErrorMessage(error, "Unable to reply"));
              return data!;
          },
          onSuccess: (data, vars) => {
              qc.setQueryData<PRCommentGroup[]>(["session-pr-comments", sessionId], (groups) =>
                  patchGroups(groups, vars.prUrl, vars.threadId, (t) => ({
                      ...t,
                      comments: [...t.comments, data.comment as ThreadComment],
                  })),
              );
          },
      });
  }

  export function useResolveThread(sessionId: string) {
      const qc = useQueryClient();
      return useMutation({
          mutationFn: async (vars: { prUrl: string; threadId: string }) => {
              const { error } = await apiClient.POST("/api/v1/sessions/{sessionId}/comment-resolve", {
                  params: { path: { sessionId } },
                  body: vars,
              });
              if (error) throw new Error(apiErrorMessage(error, "Unable to resolve"));
          },
          onSuccess: (_data, vars) => {
              qc.setQueryData<PRCommentGroup[]>(["session-pr-comments", sessionId], (groups) =>
                  patchGroups(groups, vars.prUrl, vars.threadId, (t) => ({ ...t, resolved: true })),
              );
          },
      });
  }
  ```
  Confirm the cache key `["session-pr-comments", sessionId]` matches `useSessionPRComments.ts` exactly.

- [ ] **Step 3: Run tests to verify pass** — `cd frontend && npx vitest run src/renderer/hooks/useThreadActions.test.ts`.

- [ ] **Step 4: Commit** — `git commit -am "feat(web): reply + resolve thread mutation hooks"`

---

### Task 7: Reply box + Resolve button in `ThreadCard`

**Files:**
- Modify: `frontend/src/renderer/components/CommentsView.tsx` (extend `ThreadCard`)
- Create: `frontend/src/renderer/components/ThreadActions.tsx`
- Test: `frontend/src/renderer/components/ThreadActions.test.tsx`

**Interfaces:**
- Consumes: `useReplyToThread`, `useResolveThread` (Task 6); `Button`, `Textarea` (`components/ui/*`); `Loader2` from lucide-react; `Thread` type from CommentsView.

- [ ] **Step 1: Write failing tests** (`ThreadActions.test.tsx`, render within a `QueryClientProvider`): typing in the reply textarea + clicking "Reply" calls the reply mutation with `{prUrl, threadId, body}`; the Resolve button is rendered when `!thread.resolved` and hidden/disabled when `thread.resolved`; clicking Resolve calls the resolve mutation. Mock the hooks. Run → FAIL.

- [ ] **Step 2: Implement `ThreadActions.tsx`** — a reply `Textarea` + "Reply" `Button` (disabled while pending or empty) and, when `!resolved`, a "Resolve" `Button` (`variant="outline"`, disabled while pending). Show `apiErrorMessage` on error in a `role="alert"` line (hoisted, not nested in any toggle — the Phase-4a lesson). Clear the textarea on reply success.
  ```tsx
  export function ThreadActions({ sessionId, prUrl, thread }: { sessionId: string; prUrl: string; thread: Thread }) {
      const [body, setBody] = useState("");
      const reply = useReplyToThread(sessionId);
      const resolve = useResolveThread(sessionId);
      const busy = reply.isPending || resolve.isPending;
      // textarea + Reply button (mutate {prUrl, threadId: thread.threadId, body}, clear on success)
      // Resolve button only when !thread.resolved
      // error line for reply.isError / resolve.isError
  }
  ```

- [ ] **Step 3: Wire into `ThreadCard`** (CommentsView.tsx:66) — replace the footer row that currently holds only `SendToWorkerButton` with a footer containing both `SendToWorkerButton` and `ThreadActions` (keep Send-to-worker; add reply/resolve). Match spacing to the existing `border-t border-border px-3 py-2` footer.

- [ ] **Step 4: Run tests** — `cd frontend && npx vitest run src/renderer/components/ThreadActions.test.tsx`.

- [ ] **Step 5: Full frontend check** — `cd frontend && npm run typecheck && npx vitest run`. Revert any `routeTree.gen.ts` / `pnpm-lock.yaml` churn (do not commit it).

- [ ] **Step 6: Commit** — `git commit -am "feat(web): reply box + resolve button on review threads"`

---

## Final verification (whole-branch)

- [ ] Backend: `go build ./... && go test ./internal/adapters/scm/... ./internal/service/session/... ./internal/httpd/... ./internal/observe/... && go vet ./... && gofmt -l internal/`
- [ ] Frontend: `cd frontend && npm run typecheck && npx vitest run`
- [ ] Confirm no auto-nudge reintroduced (Phase 4 removed the human-review nudge): `git grep -n NameReviewCommentDispatch internal/lifecycle` returns nothing.
- [ ] Working tree clean apart from intended changes; `routeTree.gen.ts`/lockfile churn reverted.

## Self-Review notes

- **Spec coverage:** Reply (§ goals) → Tasks 1/2/4/5/6/7. Resolve (§ goals) → same. The spec's `/prs/{id}` shape and `resolve-comments` stub are intentionally superseded by session-scoped endpoints (documented in Global Constraints) — flag to the human at execution start per SDD pre-flight.
- **Type consistency:** `ReviewThreadWriter` signature is identical across Tasks 1–4. `PRThreadComment` (service) vs `SessionPRThreadComment` (wire) are mapped in Task 5 — verify the Phase-2 mapper name before use.
- **Auth risk (runtime, not a code gap):** reply/resolve need write-scoped tokens. `gh`/`glab` logged-in tokens usually carry write scope; a read-only token passes every current read but 401s on write → surfaced distinctly as 403 `SCM_WRITE_FORBIDDEN`.
