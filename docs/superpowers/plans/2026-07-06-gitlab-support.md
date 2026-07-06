# GitLab Support Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add GitLab as a first-class SCM + issue-tracker provider that runs alongside the existing GitHub provider in one daemon, selected per-repository.

**Architecture:** New `internal/adapters/scm/gitlab` and `internal/adapters/tracker/gitlab` adapters implement the existing provider-neutral ports (`internal/observe/scm.Provider`, `internal/ports.Tracker`) against GitLab REST v4. A new `internal/adapters/scm/composite` dispatcher routes each SCM call to the right adapter by repo host/provider. Daemon wiring adds GitLab when `AO_GITLAB_HOST` is set. Central interfaces and DTOs do not change.

**Tech Stack:** Go 1.x, standard `net/http` + `httptest`, existing `internal/process` exec wrapper for `glab`, sqlc/SQLite (no new migrations), code-first OpenAPI (`npm run api`).

## Global Constraints

- All app state under `~/.ao` only; never read/write OS-default app-data. (Reading `glab`'s own config via the `glab` binary is fine; do not parse glab's config files directly.)
- CLI is a thin client; do not bypass daemon HTTP. This plan touches daemon/adapters, not CLI.
- Daemon startup must stay lazy on credentials: `SkipTokenPreflight: true`; never shell out to `glab` on the readiness path.
- No network in tests — use `httptest`, fakes, injected exec hooks.
- Do not hand-edit `backend/internal/storage/sqlite/gen/*`. No new SQLite migration is needed.
- OpenAPI/DTO are generated: edit source then run `npm run api`.
- Keep changes surgical; mirror the existing `github` adapter's structure and file boundaries one-to-one.
- Provider-neutral DTOs in `internal/ports/scm_observations.go` are the boundary — adapters normalize into them; do not leak raw GitLab payloads past the adapter.
- Config surface: `AO_GITLAB_HOST` (enables GitLab, sets composite matcher + API base `https://<host>/api/v4`), `AO_GITLAB_TOKEN`/`GITLAB_TOKEN` (token; else `glab` login).
- Commit after each task on branch `feat/gitlab-support`. Run `cd backend && go build ./... && go test ./...` before each commit.

---

### Task 1: Domain — register the `gitlab` tracker provider

**Files:**
- Modify: `backend/internal/domain/tracker.go`
- Modify: `backend/internal/domain/projectconfig.go` (only if it re-validates the provider; see step 3)
- Test: `backend/internal/domain/tracker_test.go` (create if absent), `backend/internal/domain/projectconfig_test.go`

**Interfaces:**
- Produces: `domain.TrackerProviderGitLab domain.TrackerProvider = "gitlab"`; `TrackerIntakeConfig.Validate()` accepts `github` and `gitlab`.

- [ ] **Step 1: Write the failing test** — add to `backend/internal/domain/projectconfig_test.go` the table cases:

```go
{"tracker intake explicit gitlab", ProjectConfig{TrackerIntake: TrackerIntakeConfig{Enabled: true, Provider: TrackerProviderGitLab, Assignee: "alice"}}, false},
```

And in `tracker_test.go` (create):

```go
package domain

import "testing"

func TestTrackerProviderGitLabConstant(t *testing.T) {
	if TrackerProviderGitLab != "gitlab" {
		t.Fatalf("TrackerProviderGitLab = %q, want gitlab", TrackerProviderGitLab)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd backend && go test ./internal/domain/ -run 'GitLab|gitlab' -v`
Expected: FAIL — `TrackerProviderGitLab` undefined; the projectconfig case errors because validation rejects non-github.

- [ ] **Step 3: Implement**

In `tracker.go`, below the existing github constant:

```go
// TrackerProviderGitHub and TrackerProviderGitLab are the supported issue-tracker providers.
const TrackerProviderGitHub TrackerProvider = "github"
const TrackerProviderGitLab TrackerProvider = "gitlab"
```

In `tracker.go` update the `Provider` enum doc tag:

```go
	Provider TrackerProvider `json:"provider,omitempty" enum:"github,gitlab"`
```

In `projectconfig.go`, replace the single-provider check in `TrackerIntakeConfig.Validate()`:

```go
	if c.Enabled && c.Provider != TrackerProviderGitHub && c.Provider != TrackerProviderGitLab {
		return fmt.Errorf("trackerIntake.provider: unsupported provider %q", c.Provider)
	}
```

`WithDefaults()` still defaults to `TrackerProviderGitHub` — leave it; GitLab must be explicit.

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd backend && go test ./internal/domain/ -v`
Expected: PASS.

- [ ] **Step 5: Regenerate OpenAPI + frontend types**

Run: `npm run api`
Expected: `enum` for the tracker provider widens to include `gitlab` in the generated spec/TS. Review the diff is limited to the enum widening.

- [ ] **Step 6: Commit**

```bash
git add backend/internal/domain frontend/src backend/internal/httpd
git commit -m "feat(domain): register gitlab tracker provider"
```

---

### Task 2: GitLab REST v4 client

**Files:**
- Create: `backend/internal/adapters/scm/gitlab/client.go`
- Create: `backend/internal/adapters/scm/gitlab/doc.go`
- Test: `backend/internal/adapters/scm/gitlab/client_test.go`

**Interfaces:**
- Produces:
  - `type ClientOptions struct { HTTPClient *http.Client; Token TokenSource; APIBase string; UserAgent string }`
  - `func NewClient(opts ClientOptions) *Client`
  - `func (c *Client) doREST(ctx context.Context, method, path string, query url.Values, body any) (restResponse, error)`
  - `func (c *Client) doRESTWithETag(ctx context.Context, path string, query url.Values, etag string) (restResponse, error)`
  - `type restResponse struct { Body []byte; ETag string; NotModified bool; Status int }`
  - Auth header set as `PRIVATE-TOKEN: <token>`; base URL default from `APIBase` (must end without trailing slash; requests join `APIBase + "/" + path`).
- Consumes: `TokenSource` from Task 3 (define the interface here so the client compiles; Task 3 fills implementations).

Mirror the structure of `backend/internal/adapters/scm/github/client.go` (ETag handling, 304 mapping, auth-failure token invalidation via the `tokenInvalidator` interface, `context.Context` first arg). Differences from GitHub: header is `PRIVATE-TOKEN` not `Authorization: Bearer`; there is no GraphQL client; base URL is `https://<host>/api/v4` supplied via `APIBase`.

- [ ] **Step 1: Write the failing test**

```go
package gitlab

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientDoRESTSendsPrivateToken(t *testing.T) {
	var gotToken string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotToken = r.Header.Get("PRIVATE-TOKEN")
		w.Header().Set("ETag", `"abc"`)
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	c := NewClient(ClientOptions{APIBase: srv.URL, Token: StaticTokenSource("tok-123")})
	resp, err := c.doRESTWithETag(context.Background(), "projects/x/merge_requests", nil, "")
	if err != nil {
		t.Fatalf("doRESTWithETag: %v", err)
	}
	if gotToken != "tok-123" {
		t.Fatalf("PRIVATE-TOKEN header = %q, want tok-123", gotToken)
	}
	if resp.ETag != `"abc"` {
		t.Fatalf("ETag = %q", resp.ETag)
	}
}

func TestClientDoRESTMaps304(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") == `"abc"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()
	c := NewClient(ClientOptions{APIBase: srv.URL, Token: StaticTokenSource("t")})
	resp, err := c.doRESTWithETag(context.Background(), "p", nil, `"abc"`)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !resp.NotModified {
		t.Fatalf("expected NotModified")
	}
}
```

(`StaticTokenSource` is defined in Task 3; if implementing Task 2 first, add a temporary `type StaticTokenSource string` with a `Token` method in a scratch, or implement Task 3's `auth.go` first. Recommended order: do Task 3 before Task 2's test run.)

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/adapters/scm/gitlab/ -run TestClient -v`
Expected: FAIL — package/type undefined.

- [ ] **Step 3: Implement `client.go`**

Write a `Client` with `httpClient`, `apiBase` (trailing slash trimmed), `tokens TokenSource`, `userAgent`. `doRESTWithETag` builds `req` to `apiBase + "/" + path` with query, sets `PRIVATE-TOKEN` from `tokens.Token(ctx)`, sets `If-None-Match` when `etag != ""`, sets `Accept: application/json` and `User-Agent`. Map `304` → `restResponse{NotModified: true, ETag: reqETag, Status: 304}`; read body otherwise; capture `ETag` response header. On `401` call `tokens` invalidation if it implements `tokenInvalidator` (mirror github). `doREST` is `doRESTWithETag` with `etag=""` plus optional JSON body encode for non-GET. Add `doc.go` with a one-line package comment mirroring `github/doc.go`.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/adapters/scm/gitlab/ -run TestClient -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/adapters/scm/gitlab
git commit -m "feat(scm/gitlab): REST v4 client with ETag + private-token auth"
```

---

### Task 3: GitLab token sources (env + `glab` fallback)

**Files:**
- Create: `backend/internal/adapters/scm/gitlab/auth.go`
- Test: `backend/internal/adapters/scm/gitlab/auth_test.go`

**Interfaces:**
- Produces (mirror `github/auth.go` names/semantics):
  - `type TokenSource interface { Token(ctx context.Context) (string, error) }`
  - `type tokenInvalidator interface { InvalidateToken() }`
  - `var ErrNoToken = errors.New("gitlab scm: no token configured")`
  - `type StaticTokenSource string`
  - `type EnvTokenSource struct { EnvVars []string }` — falls back to `GITLAB_TOKEN`
  - `type FallbackTokenSource []TokenSource`
  - `type GlabTokenSource struct { Host string; Glab func(ctx context.Context, host string) (string, error); TokenTTL time.Duration; Clock func() time.Time }` with `Token`/`InvalidateToken`
  - `func parseGlabToken(out string) (string, error)` — extracts the token from `glab auth status --show-token` output

- [ ] **Step 1: Write the failing test**

```go
package gitlab

import (
	"context"
	"testing"
)

func TestParseGlabToken(t *testing.T) {
	sample := `gitlab.finnomena.com
  ✓ Logged in to gitlab.finnomena.com as fluke.s (config.yml)
  ✓ Git operations for gitlab.finnomena.com configured to use https protocol.
  ✓ Token: glpat-abc123DEF
`
	got, err := parseGlabToken(sample)
	if err != nil {
		t.Fatalf("parseGlabToken: %v", err)
	}
	if got != "glpat-abc123DEF" {
		t.Fatalf("token = %q, want glpat-abc123DEF", got)
	}
}

func TestEnvTokenSourcePrecedence(t *testing.T) {
	t.Setenv("AO_GITLAB_TOKEN", "ao-tok")
	t.Setenv("GITLAB_TOKEN", "generic-tok")
	tok, err := EnvTokenSource{EnvVars: []string{"AO_GITLAB_TOKEN"}}.Token(context.Background())
	if err != nil || tok != "ao-tok" {
		t.Fatalf("token=%q err=%v, want ao-tok", tok, err)
	}
}

func TestGlabTokenSourceUsesInjectedHook(t *testing.T) {
	src := &GlabTokenSource{Host: "gitlab.finnomena.com", Glab: func(ctx context.Context, host string) (string, error) {
		return "  ✓ Token: glpat-XYZ\n", nil
	}}
	tok, err := src.Token(context.Background())
	if err != nil || tok != "glpat-XYZ" {
		t.Fatalf("token=%q err=%v, want glpat-XYZ", tok, err)
	}
}
```

Note: `glab auth status --show-token` prints the token on a line matching `Token: <tok>` (real output uses `Token found:`/`Token:` depending on version — `parseGlabToken` must accept a line containing `Token` and a `glpat-`/token value after the last `:`; match the last colon-separated field, trimming the leading check glyph and spaces).

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/adapters/scm/gitlab/ -run 'Token|Glab' -v`
Expected: FAIL — undefined identifiers.

- [ ] **Step 3: Implement `auth.go`**

Copy the shape of `github/auth.go`. For `GlabTokenSource.Token`: memoize with TTL (default 5m) like `GHTokenSource`; when cache empty run `Glab` hook (production default shells `glab auth status --show-token --hostname <host>` via `aoprocess.CommandContext`), then `parseGlabToken`. `parseGlabToken` scans lines for one containing `Token`, splits on `:`, trims spaces and any leading `✓`/`*`, returns the last field; error `ErrNoToken` if none non-empty. `InvalidateToken` clears the memo.

```go
func parseGlabToken(out string) (string, error) {
	for _, line := range strings.Split(out, "\n") {
		if !strings.Contains(line, "Token") {
			continue
		}
		idx := strings.LastIndex(line, ":")
		if idx < 0 {
			continue
		}
		tok := strings.TrimSpace(line[idx+1:])
		if tok != "" && !strings.Contains(tok, "*") { // masked tokens contain asterisks
			return tok, nil
		}
	}
	return "", ErrNoToken
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/adapters/scm/gitlab/ -run 'Token|Glab' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/adapters/scm/gitlab/auth.go backend/internal/adapters/scm/gitlab/auth_test.go
git commit -m "feat(scm/gitlab): env + glab token sources"
```

---

### Task 4: Provider constructor + `ParseRepository` (nested groups)

**Files:**
- Create: `backend/internal/adapters/scm/gitlab/provider.go`
- Create: `backend/internal/adapters/scm/gitlab/observer_provider.go` (ParseRepository only in this task)
- Test: `backend/internal/adapters/scm/gitlab/provider_test.go`

**Interfaces:**
- Produces:
  - `type ProviderOptions struct { Client *Client; HTTPClient *http.Client; Token TokenSource; SkipTokenPreflight bool; APIBase string; Host string; UserAgent string; Logger *slog.Logger }`
  - `func NewProvider(opts ProviderOptions) (*Provider, error)`
  - `func (p *Provider) SCMCredentialsAvailable(ctx context.Context) (bool, error)`
  - `func (p *Provider) ParseRepository(remote string) (ports.SCMRepo, bool)` — sets `Provider: "gitlab"`, `Host`, full-path `Repo`, `Owner`=all-but-last segment, `Name`=last segment. Returns `ok=false` when the remote host is not this provider's `Host`.

- [ ] **Step 1: Write the failing test**

```go
func TestParseRepositoryNestedGroup(t *testing.T) {
	p, _ := NewProvider(ProviderOptions{Host: "gitlab.finnomena.com", APIBase: "https://gitlab.finnomena.com/api/v4", Token: StaticTokenSource("t"), SkipTokenPreflight: true})
	cases := []string{
		"git@gitlab.finnomena.com:group/sub/proj.git",
		"https://gitlab.finnomena.com/group/sub/proj.git",
		"https://gitlab.finnomena.com/group/sub/proj",
	}
	for _, remote := range cases {
		repo, ok := p.ParseRepository(remote)
		if !ok {
			t.Fatalf("%s: not parsed", remote)
		}
		if repo.Provider != "gitlab" || repo.Host != "gitlab.finnomena.com" {
			t.Fatalf("%s: provider/host = %q/%q", remote, repo.Provider, repo.Host)
		}
		if repo.Repo != "group/sub/proj" || repo.Owner != "group/sub" || repo.Name != "proj" {
			t.Fatalf("%s: repo=%q owner=%q name=%q", remote, repo.Repo, repo.Owner, repo.Name)
		}
	}
}

func TestParseRepositoryRejectsOtherHost(t *testing.T) {
	p, _ := NewProvider(ProviderOptions{Host: "gitlab.finnomena.com", Token: StaticTokenSource("t"), SkipTokenPreflight: true})
	if _, ok := p.ParseRepository("git@github.com:acme/demo.git"); ok {
		t.Fatalf("github.com remote should not be claimed by gitlab provider")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/adapters/scm/gitlab/ -run ParseRepository -v`
Expected: FAIL.

- [ ] **Step 3: Implement**

`provider.go`: mirror `github/provider.go` `NewProvider` (build `Client` from opts when `opts.Client==nil`; preflight token unless `SkipTokenPreflight`). `SCMCredentialsAvailable` mirrors github. Store `host` on the Provider.

`observer_provider.go`: `ParseRepository` normalizes SSH (`git@host:path.git`), HTTPS (`https://host/path(.git)`), trimming `.git`. Extract host; if host != `p.host` return `ok=false`. Split remaining path: `Repo` = full path, `Name` = last `/` segment, `Owner` = the rest. Build `ports.SCMRepo{Provider: "gitlab", Host: host, Owner: owner, Name: name, Repo: fullPath}`.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/adapters/scm/gitlab/ -run ParseRepository -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/adapters/scm/gitlab/provider.go backend/internal/adapters/scm/gitlab/observer_provider.go backend/internal/adapters/scm/gitlab/provider_test.go
git commit -m "feat(scm/gitlab): provider constructor + nested-group ParseRepository"
```

---

### Task 5: MR listing + guards (`ListOpenPRsByRepo`, `RepoPRListGuard`, `CommitChecksGuard`)

**Files:**
- Modify: `backend/internal/adapters/scm/gitlab/observer_provider.go`
- Test: `backend/internal/adapters/scm/gitlab/observer_provider_test.go`

**Interfaces:**
- Produces on `*Provider`:
  - `ListOpenPRsByRepo(ctx, repo ports.SCMRepo) ([]ports.SCMPRObservation, error)`
  - `RepoPRListGuard(ctx, repo ports.SCMRepo, etag string) (ports.SCMGuardResult, error)`
  - `CommitChecksGuard(ctx, repo ports.SCMRepo, headSHA, etag string) (ports.SCMGuardResult, error)`
  - helper `projectID(repo ports.SCMRepo) string` = `url.PathEscape(repo.Repo)`
  - helper `normalizeMRState(state string, draft bool) (stateStr string, draftB, mergedB, closedB bool)`: `opened`→open, `merged`→merged, `locked`/`closed`→closed; draft passthrough.

- [ ] **Step 1: Write the failing test** — fake server returns one opened MR:

```go
func TestListOpenPRsByRepo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// GitLab escapes the project path: projects/group%2Fsub%2Fproj/merge_requests
		if !strings.Contains(r.URL.Path, "/projects/") || !strings.HasSuffix(r.URL.Path, "/merge_requests") {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("ETag", `"list-1"`)
		_, _ = w.Write([]byte(`[{"iid":7,"state":"opened","draft":true,"title":"Add x","source_branch":"feat","target_branch":"main","sha":"deadbeef","web_url":"https://gl/mr/7","author":{"username":"fluke"}}]`))
	}))
	defer srv.Close()
	p := newTestProvider(t, srv.URL) // helper builds Provider with APIBase=srv.URL, Host set
	repo := ports.SCMRepo{Provider: "gitlab", Host: "gitlab.finnomena.com", Owner: "group/sub", Name: "proj", Repo: "group/sub/proj"}
	prs, err := p.ListOpenPRsByRepo(context.Background(), repo)
	if err != nil {
		t.Fatalf("ListOpenPRsByRepo: %v", err)
	}
	if len(prs) != 1 || prs[0].Number != 7 || prs[0].State != "open" || !prs[0].Draft {
		t.Fatalf("got %+v", prs)
	}
	if prs[0].SourceBranch != "feat" || prs[0].TargetBranch != "main" || prs[0].HeadSHA != "deadbeef" {
		t.Fatalf("branches/sha wrong: %+v", prs[0])
	}
}

func TestRepoPRListGuard304(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") == `"list-1"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"list-1"`)
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()
	p := newTestProvider(t, srv.URL)
	repo := ports.SCMRepo{Repo: "group/sub/proj"}
	res, err := p.RepoPRListGuard(context.Background(), repo, `"list-1"`)
	if err != nil || !res.NotModified {
		t.Fatalf("guard=%+v err=%v", res, err)
	}
}
```

Add a `newTestProvider(t, apiBase string) *Provider` helper in the test file (builds `ProviderOptions{Client: NewClient(ClientOptions{APIBase: apiBase, Token: StaticTokenSource("t")}), Host: "gitlab.finnomena.com", SkipTokenPreflight: true}`).

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/adapters/scm/gitlab/ -run 'ListOpenPRs|Guard' -v`
Expected: FAIL — methods undefined.

- [ ] **Step 3: Implement**

Define a `restMR` struct with json tags (`iid`, `state`, `draft`, `title`, `source_branch`, `target_branch`, `sha`, `web_url`, `merge_status`, `has_conflicts`, `changes_count`, `additions`?/use diffstats via `changes`, `author.username`, timestamps `created_at`/`updated_at`/`merged_at`/`closed_at`). `ListOpenPRsByRepo` GETs `projects/<esc>/merge_requests?state=opened&per_page=100`, maps each via a shared `mrToObservation(restMR) ports.SCMPRObservation` using `normalizeMRState`. Guards do a cheap GET with `per_page=1` and `doRESTWithETag`, returning `ports.SCMGuardResult{ETag: firstNonEmpty(resp.ETag, etag), NotModified: resp.NotModified}`. `CommitChecksGuard` targets `projects/<esc>/pipelines?sha=<headSHA>&per_page=1`.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/adapters/scm/gitlab/ -run 'ListOpenPRs|Guard' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/adapters/scm/gitlab/observer_provider.go backend/internal/adapters/scm/gitlab/observer_provider_test.go
git commit -m "feat(scm/gitlab): MR listing + ETag guards"
```

---

### Task 6: `FetchPullRequests` — MR detail + pipeline → mergeability + CI summary

**Files:**
- Modify: `backend/internal/adapters/scm/gitlab/observer_provider.go`
- Test: `backend/internal/adapters/scm/gitlab/fetch_test.go`

**Interfaces:**
- Produces: `FetchPullRequests(ctx, refs []ports.SCMPRRef) ([]ports.SCMObservation, error)`; helpers `normalizeCIStatus(pipelineStatus string) string` (`success`→passing, `failed`→failing, `running`/`pending`/`created`/`scheduled`→pending, else unknown) and `mergeability(mr restMR) ports.SCMMergeabilityObservation` (`merge_status=="can_be_merged"` → Mergeable; `has_conflicts` → Conflict + blocker "merge conflict").

- [ ] **Step 1: Write the failing test** — fake server serves MR detail + its pipelines + jobs:

```go
func TestFetchPullRequestsMergeabilityAndCI(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/projects/group%2Fproj/merge_requests/7", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"iid":7,"state":"opened","draft":false,"title":"t","source_branch":"feat","target_branch":"main","sha":"sha1","merge_status":"cannot_be_merged","has_conflicts":true,"changes_count":"3","web_url":"https://gl/7","author":{"username":"fluke"}}`))
	})
	mux.HandleFunc("/api/v4/projects/group%2Fproj/merge_requests/7/pipelines", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"id":900,"sha":"sha1","status":"failed"}]`))
	})
	mux.HandleFunc("/api/v4/projects/group%2Fproj/pipelines/900/jobs", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"id":11,"name":"test","status":"failed","web_url":"https://gl/j/11"}]`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	p := newTestProvider(t, srv.URL)
	ref := ports.SCMPRRef{Repo: ports.SCMRepo{Repo: "group/proj", Host: "gitlab.finnomena.com", Provider: "gitlab"}, Number: 7}
	obs, err := p.FetchPullRequests(context.Background(), []ports.SCMPRRef{ref})
	if err != nil {
		t.Fatalf("FetchPullRequests: %v", err)
	}
	if len(obs) != 1 {
		t.Fatalf("want 1 obs, got %d", len(obs))
	}
	o := obs[0]
	if !o.Fetched || o.PR.Number != 7 {
		t.Fatalf("pr wrong: %+v", o.PR)
	}
	if o.Mergeability.Mergeable || !o.Mergeability.Conflict {
		t.Fatalf("mergeability wrong: %+v", o.Mergeability)
	}
	if o.CI.Summary != "failing" {
		t.Fatalf("CI summary = %q, want failing", o.CI.Summary)
	}
	if len(o.CI.FailedChecks) != 1 || o.CI.FailedChecks[0].ProviderID != "11" {
		t.Fatalf("failed checks wrong: %+v", o.CI.FailedChecks)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/adapters/scm/gitlab/ -run FetchPullRequests -v`
Expected: FAIL.

- [ ] **Step 3: Implement**

For each ref: GET MR detail → `restMR`; GET `.../merge_requests/<iid>/pipelines` (take newest by id/sha match to `mr.sha`); GET `.../pipelines/<id>/jobs`. Build `ports.SCMObservation{Fetched: true, ObservedAt: now, Provider: "gitlab", Host: ref.Repo.Host, Repo: ref.Repo.Repo, PR: mrToObservation(mr), CI: ...}`. CI: `Summary = normalizeCIStatus(pipeline.status)`, `Checks` = all jobs normalized to `SCMCheckObservation{Name, Status, Conclusion: job.status, URL: job.web_url, ProviderID: strconv job id}`, `FailedChecks` = jobs with status in {failed, canceled}. `Mergeability` from helper. On any fetch error for a ref, append `ports.SCMObservation{Fetched: false}` (never infer closed). Use `time.Now()` — inject a clock only if a test needs determinism; here `ObservedAt` is not asserted.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/adapters/scm/gitlab/ -run FetchPullRequests -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/adapters/scm/gitlab/observer_provider.go backend/internal/adapters/scm/gitlab/fetch_test.go
git commit -m "feat(scm/gitlab): FetchPullRequests with mergeability + CI"
```

---

### Task 7: `FetchFailedCheckLogTail` — job trace tail

**Files:**
- Modify: `backend/internal/adapters/scm/gitlab/observer_provider.go`
- Test: `backend/internal/adapters/scm/gitlab/logtail_test.go`

**Interfaces:**
- Produces: `FetchFailedCheckLogTail(ctx, repo ports.SCMRepo, check ports.SCMCheckObservation) (string, error)` — GETs `projects/<esc>/jobs/<check.ProviderID>/trace` (plain text), returns last 20 lines. Reuse a `lastNLines(s string, n int) string` helper (create in this package; 20 = `ciFailureLogTailLines` const mirroring github).

- [ ] **Step 1: Write the failing test**

```go
func TestFetchFailedCheckLogTail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/jobs/11/trace") {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		lines := make([]string, 0, 30)
		for i := 1; i <= 30; i++ {
			lines = append(lines, "line"+strconv.Itoa(i))
		}
		_, _ = w.Write([]byte(strings.Join(lines, "\n")))
	}))
	defer srv.Close()
	p := newTestProvider(t, srv.URL)
	tail, err := p.FetchFailedCheckLogTail(context.Background(), ports.SCMRepo{Repo: "group/proj"}, ports.SCMCheckObservation{ProviderID: "11"})
	if err != nil {
		t.Fatalf("log tail: %v", err)
	}
	if strings.Contains(tail, "line10") || !strings.Contains(tail, "line30") || !strings.Contains(tail, "line11") {
		t.Fatalf("tail should be last 20 lines: %q", tail)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/adapters/scm/gitlab/ -run LogTail -v`
Expected: FAIL.

- [ ] **Step 3: Implement** — GET trace via a raw-text client call (add `doRESTText` or reuse `doREST` and return `resp.Body` as string), return `lastNLines(string(body), ciFailureLogTailLines)`. If `check.ProviderID` empty, return `"", nil`.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/adapters/scm/gitlab/ -run LogTail -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/adapters/scm/gitlab
git commit -m "feat(scm/gitlab): failed job log tail"
```

---

### Task 8: `FetchReviewThreads` — discussions + approvals

**Files:**
- Modify: `backend/internal/adapters/scm/gitlab/observer_provider.go`
- Test: `backend/internal/adapters/scm/gitlab/review_test.go`

**Interfaces:**
- Produces: `FetchReviewThreads(ctx, ref ports.SCMPRRef) (ports.SCMReviewObservation, error)` — GETs `.../merge_requests/<iid>/discussions` and `.../merge_requests/<iid>/approvals`. Maps each discussion → `ports.SCMReviewThreadObservation` (ID=discussion id, Path/Line from note `position.new_path`/`new_line`, Resolved from note `resolved`, `IsBot` from author). Notes → `SCMReviewCommentObservation`. Decision from approvals: `approved` when `approvals_left == 0` and `approved_by` non-empty, else empty.

- [ ] **Step 1: Write the failing test**

```go
func TestFetchReviewThreads(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/projects/group%2Fproj/merge_requests/7/discussions", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"id":"disc1","notes":[{"id":101,"body":"please fix","resolvable":true,"resolved":false,"author":{"username":"rev"},"position":{"new_path":"main.go","new_line":42}}]}]`))
	})
	mux.HandleFunc("/api/v4/projects/group%2Fproj/merge_requests/7/approvals", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"approvals_left":0,"approved_by":[{"user":{"username":"lead"}}]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	p := newTestProvider(t, srv.URL)
	ref := ports.SCMPRRef{Repo: ports.SCMRepo{Repo: "group/proj"}, Number: 7}
	rev, err := p.FetchReviewThreads(context.Background(), ref)
	if err != nil {
		t.Fatalf("FetchReviewThreads: %v", err)
	}
	if rev.Decision != "approved" {
		t.Fatalf("decision=%q want approved", rev.Decision)
	}
	if len(rev.Threads) != 1 || rev.Threads[0].Path != "main.go" || rev.Threads[0].Line != 42 {
		t.Fatalf("threads wrong: %+v", rev.Threads)
	}
	if len(rev.Threads[0].Comments) != 1 || rev.Threads[0].Comments[0].Body != "please fix" {
		t.Fatalf("comments wrong: %+v", rev.Threads[0].Comments)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/adapters/scm/gitlab/ -run FetchReviewThreads -v`
Expected: FAIL.

- [ ] **Step 3: Implement** — define `restDiscussion`/`restNote`/`restApprovals` structs; iterate discussions, take the first note's `position` for Path/Line and `resolvable` for thread inclusion, set `Resolved` if all notes resolved. Map approvals to decision. Set `ports.SCMReviewObservation{Decision, Threads, Partial: false}` (full snapshot per poll in round 1).

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/adapters/scm/gitlab/ -run FetchReviewThreads -v`
Expected: PASS.

- [ ] **Step 5: Verify the full `observe/scm.Provider` interface is satisfied** — add a compile-time assertion in `provider.go`:

```go
var _ scmobserve.Provider = (*Provider)(nil)
```

(import `scmobserve "github.com/aoagents/agent-orchestrator/backend/internal/observe/scm"`). Run `cd backend && go build ./...` — Expected: PASS (all 7 methods present).

- [ ] **Step 6: Commit**

```bash
git add backend/internal/adapters/scm/gitlab
git commit -m "feat(scm/gitlab): review discussions + approvals decision"
```

---

### Task 9: Composite SCM dispatcher

**Files:**
- Create: `backend/internal/adapters/scm/composite/provider.go`
- Create: `backend/internal/adapters/scm/composite/doc.go`
- Test: `backend/internal/adapters/scm/composite/provider_test.go`

**Interfaces:**
- Consumes: `scmobserve.Provider` (the 7-method interface).
- Produces:
  - `func New(providers ...scmobserve.Provider) *Provider` — ordered; first whose `ParseRepository` returns ok claims a remote.
  - `*Provider` implements `scmobserve.Provider`. Routing: `ParseRepository` tries each in order; all repo-taking methods route by matching `repo.Provider` against each child's probe (store a `map[string]scmobserve.Provider` keyed by the provider name each child stamps, discovered lazily, plus fall back to trying `ParseRepository` on a synthesized remote is unnecessary — instead each child exposes its provider name).

To route deterministically, wrap each child with its provider name. Add an internal interface the adapters already satisfy via the `Provider` field on parsed repos: since `ParseRepository` stamps `repo.Provider`, the composite keeps children in order and routes repo/ref calls to the child whose parsed provider name matches. Implement by having `New` accept entries:

```go
type Entry struct {
	Name     string // "github" | "gitlab"
	Provider scmobserve.Provider
}
func New(entries ...Entry) *Provider
```

Routing methods look up `entries` by `repo.Provider` / `ref.Repo.Provider`; `ParseRepository` iterates entries in order returning the first ok.

- [ ] **Step 1: Write the failing test** using two fakes:

```go
package composite

import (
	"context"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type fakeProvider struct {
	name    string
	host    string
	listErr error
	listN   int
}

func (f *fakeProvider) ParseRepository(remote string) (ports.SCMRepo, bool) {
	if f.host == "" || !contains(remote, f.host) {
		return ports.SCMRepo{}, false
	}
	return ports.SCMRepo{Provider: f.name, Host: f.host, Repo: "o/n"}, true
}
func (f *fakeProvider) RepoPRListGuard(context.Context, ports.SCMRepo, string) (ports.SCMGuardResult, error) { return ports.SCMGuardResult{}, nil }
func (f *fakeProvider) ListOpenPRsByRepo(context.Context, ports.SCMRepo) ([]ports.SCMPRObservation, error) {
	return make([]ports.SCMPRObservation, f.listN), f.listErr
}
func (f *fakeProvider) CommitChecksGuard(context.Context, ports.SCMRepo, string, string) (ports.SCMGuardResult, error) { return ports.SCMGuardResult{}, nil }
func (f *fakeProvider) FetchPullRequests(context.Context, []ports.SCMPRRef) ([]ports.SCMObservation, error) { return nil, nil }
func (f *fakeProvider) FetchFailedCheckLogTail(context.Context, ports.SCMRepo, ports.SCMCheckObservation) (string, error) { return "", nil }
func (f *fakeProvider) FetchReviewThreads(context.Context, ports.SCMPRRef) (ports.SCMReviewObservation, error) { return ports.SCMReviewObservation{}, nil }

func TestParseRoutesByHost(t *testing.T) {
	gl := &fakeProvider{name: "gitlab", host: "gitlab.finnomena.com", listN: 3}
	gh := &fakeProvider{name: "github", host: "github.com", listN: 1}
	c := New(Entry{"gitlab", gl}, Entry{"github", gh})

	repo, ok := c.ParseRepository("https://gitlab.finnomena.com/o/n.git")
	if !ok || repo.Provider != "gitlab" {
		t.Fatalf("parse => %+v ok=%v", repo, ok)
	}
	prs, _ := c.ListOpenPRsByRepo(context.Background(), repo)
	if len(prs) != 3 {
		t.Fatalf("routed to wrong provider, len=%d", len(prs))
	}
	ghRepo, _ := c.ParseRepository("https://github.com/o/n")
	prs2, _ := c.ListOpenPRsByRepo(context.Background(), ghRepo)
	if len(prs2) != 1 {
		t.Fatalf("github route len=%d", len(prs2))
	}
}
```

(add a small `contains` helper or use `strings.Contains` directly.)

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/adapters/scm/composite/ -v`
Expected: FAIL — package undefined.

- [ ] **Step 3: Implement** — `Provider{ordered []Entry; byName map[string]scmobserve.Provider}`. `ParseRepository` loops `ordered`, returns first ok. Repo/ref methods look up `byName[repo.Provider]`; if missing, return a clear error `fmt.Errorf("composite scm: no provider %q", name)`. Add compile assertion `var _ scmobserve.Provider = (*Provider)(nil)`.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/adapters/scm/composite/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/adapters/scm/composite
git commit -m "feat(scm/composite): deterministic per-repo provider dispatcher"
```

---

### Task 10: GitLab issue tracker adapter

**Files:**
- Create: `backend/internal/adapters/tracker/gitlab/tracker.go`
- Create: `backend/internal/adapters/tracker/gitlab/auth.go` (reuse the same token-source pattern; may import the scm/gitlab auth or duplicate the small pattern — prefer a local `Options{Token, APIBase, HTTPClient}` mirroring `tracker/github/Options`)
- Create: `backend/internal/adapters/tracker/gitlab/doc.go`
- Test: `backend/internal/adapters/tracker/gitlab/tracker_test.go`

**Interfaces:**
- Produces: `func New(opts Options) (*Tracker, error)`; `*Tracker` implements `ports.Tracker` (`Get`, `List`, `Preflight`). `TrackerID.Native` form for GitLab = `"group/sub/proj#<iid>"`; `parseID` splits on last `#`. Issue state map: `opened`→`IssueOpen`, `closed`→`IssueDone` (round 1; GitLab has no native in-progress/review without board columns).

- [ ] **Step 1: Write the failing test**

```go
func TestTrackerGet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/projects/group%2Fproj/issues/5") {
			t.Fatalf("path %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"iid":5,"title":"Bug","description":"desc","state":"opened","web_url":"https://gl/5","labels":["bug"],"assignees":[{"username":"fluke"}]}`))
	}))
	defer srv.Close()
	tr, _ := New(Options{APIBase: srv.URL, Token: staticToken("t")})
	iss, err := tr.Get(context.Background(), domain.TrackerID{Provider: domain.TrackerProviderGitLab, Native: "group/proj#5"})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if iss.Title != "Bug" || iss.State != domain.IssueOpen || iss.ID.Provider != domain.TrackerProviderGitLab {
		t.Fatalf("issue wrong: %+v", iss)
	}
	if len(iss.Labels) != 1 || iss.Labels[0] != "bug" || len(iss.Assignees) != 1 {
		t.Fatalf("labels/assignees wrong: %+v", iss)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/adapters/tracker/gitlab/ -run TrackerGet -v`
Expected: FAIL.

- [ ] **Step 3: Implement** — mirror `tracker/github/tracker.go` structure. `Get` parses native → project path + iid, GETs `projects/<esc>/issues/<iid>`, maps to `domain.Issue` with `ID: domain.TrackerID{Provider: domain.TrackerProviderGitLab, Native: native}`. `List` GETs `projects/<esc>/issues` with query from `domain.ListFilter` (`state=opened|closed`, `labels=`, `assignee_username=`, `per_page`/limit). `Preflight` GETs `user`. Add compile assertion `var _ ports.Tracker = (*Tracker)(nil)`.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/adapters/tracker/gitlab/ -run TrackerGet -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/adapters/tracker/gitlab
git commit -m "feat(tracker/gitlab): GitLab issue adapter"
```

---

### Task 11: Multi-provider tracker resolver

**Files:**
- Modify: `backend/internal/observe/trackerintake/observer.go` (add `MultiTrackerResolver`; keep `SingleTrackerResolver` for existing tests)
- Test: `backend/internal/observe/trackerintake/observer_test.go`

**Interfaces:**
- Produces: `type MultiTrackerResolver struct { Adapters map[domain.TrackerProvider]ports.Tracker }` with `Resolve(provider domain.TrackerProvider) (ports.Tracker, error)` — returns the mapped adapter or `fmt.Errorf("tracker intake: no adapter for provider %q", provider)`. Empty provider defaults to `domain.TrackerProviderGitHub`.

- [ ] **Step 1: Write the failing test**

```go
func TestMultiTrackerResolver(t *testing.T) {
	gh := fakeTracker{}
	gl := fakeTracker{}
	r := MultiTrackerResolver{Adapters: map[domain.TrackerProvider]ports.Tracker{
		domain.TrackerProviderGitHub: gh,
		domain.TrackerProviderGitLab: gl,
	}}
	got, err := r.Resolve(domain.TrackerProviderGitLab)
	if err != nil || got != ports.Tracker(gl) {
		t.Fatalf("resolve gitlab => %v err=%v", got, err)
	}
	if _, err := r.Resolve("linear"); err == nil {
		t.Fatalf("expected error for unknown provider")
	}
}
```

(reuse or add a `fakeTracker` implementing `ports.Tracker` in the test file.)

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/observe/trackerintake/ -run MultiTracker -v`
Expected: FAIL.

- [ ] **Step 3: Implement** the resolver in `observer.go` next to `SingleTrackerResolver`.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/observe/trackerintake/ -v`
Expected: PASS (existing SingleTrackerResolver tests still pass).

- [ ] **Step 5: Commit**

```bash
git add backend/internal/observe/trackerintake
git commit -m "feat(trackerintake): multi-provider tracker resolver"
```

---

### Task 12: Daemon wiring — SCM composite + tracker multi-resolver

**Files:**
- Modify: `backend/internal/daemon/scm_wiring.go`
- Modify: `backend/internal/daemon/tracker_intake_wiring.go`
- Test: `backend/internal/daemon/scm_wiring_test.go` (create), extend existing tracker wiring test if present

**Interfaces:**
- Consumes: `composite.New`, `composite.Entry`, `scmgitlab.NewProvider`, `trackergitlab.New`, `MultiTrackerResolver`.
- Produces: `func gitlabHosts() []string` (reads `AO_GITLAB_HOST`, comma-split, trims); GitLab wired only when non-empty.

- [ ] **Step 1: Write the failing test** — assert host parsing + that no GitLab host ⇒ github-only composite:

```go
func TestGitlabHostsParsing(t *testing.T) {
	t.Setenv("AO_GITLAB_HOST", " gitlab.finnomena.com , gl.example.com ")
	got := gitlabHosts()
	if len(got) != 2 || got[0] != "gitlab.finnomena.com" || got[1] != "gl.example.com" {
		t.Fatalf("hosts=%v", got)
	}
	t.Setenv("AO_GITLAB_HOST", "")
	if len(gitlabHosts()) != 0 {
		t.Fatalf("empty env should yield no hosts")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/daemon/ -run GitlabHosts -v`
Expected: FAIL.

- [ ] **Step 3: Implement**

In `scm_wiring.go`: add `gitlabHosts()`. Build `entries := []composite.Entry{}`; always append `{Name:"github", Provider: newGitHubSCMProvider(...)}`. If `hosts := gitlabHosts(); len(hosts) > 0`, for the first host (round 1 single host) build `scmgitlab.NewProvider(ProviderOptions{Host: hosts[0], APIBase: "https://"+hosts[0]+"/api/v4", Token: gitlabTokenSource(hosts[0]), SkipTokenPreflight: true, Logger: logger})` and prepend the gitlab entry (gitlab before github so it claims its host first). Wrap `provider := composite.New(entries...)`; pass to `scmobserve.New`. `gitlabTokenSource(host)` = `scmgitlab.FallbackTokenSource{ EnvTokenSource{[]string{"AO_GITLAB_TOKEN"}}, &GlabTokenSource{Host: host} }`.

In `tracker_intake_wiring.go`: build a lazy GitLab tracker mirroring `lazyGitHubTracker`, and switch the resolver to `MultiTrackerResolver{Adapters: map[...]{github: lazyGH, gitlab: lazyGL}}`. Only add the gitlab entry when `len(gitlabHosts())>0`.

- [ ] **Step 4: Run test to verify it passes + full build/test**

Run: `cd backend && go test ./internal/daemon/ -run GitlabHosts -v && go build ./... && go test ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/daemon
git commit -m "feat(daemon): wire GitLab SCM + tracker behind AO_GITLAB_HOST"
```

---

### Task 13: Docs + full verification

**Files:**
- Modify: `README.md` (config section) and/or `docs/` — document `AO_GITLAB_HOST`, `AO_GITLAB_TOKEN`, glab-login fallback, and that absence of `AO_GITLAB_HOST` keeps behavior unchanged.

- [ ] **Step 1: Add a short "GitLab (self-hosted)" subsection** to the config docs listing the three env behaviors and the single-host round-1 limitation.

- [ ] **Step 2: Run the full local check suite**

Run: `npm run lint && npm run frontend:typecheck`
Also: `cd backend && go test -race ./internal/adapters/... ./internal/observe/... ./internal/daemon/... ./internal/domain/...`
Expected: PASS.

- [ ] **Step 3: Manual smoke (optional, requires glab login)** — with `AO_GITLAB_HOST=gitlab.finnomena.com`, start the daemon and confirm the SCM observer logs no credential warning and picks up a GitLab MR for a session whose origin is on that host. Do not commit any run state.

- [ ] **Step 4: Commit**

```bash
git add README.md docs
git commit -m "docs: document GitLab self-hosted config"
```

---

## Self-Review Notes

- **Spec coverage:** MR observation (T5,T6) · CI/pipeline + failed logs (T6,T7) · review threads/comments + decision (T8) · issue intake (T10,T11) · composite selection (T9) · self-hosted host/auth (T3,T12) · domain/config/OpenAPI (T1) · no migration (confirmed) · deferred items (review-posting, multi-host, OAuth) explicitly out of scope. All covered.
- **Type consistency:** `ProviderOptions`/`ClientOptions`/`TokenSource`/`ports.SCMRepo`/`ports.SCMObservation` names match the existing github adapter and `internal/ports`. Composite `Entry{Name, Provider}` used consistently in T9 and T12. `MultiTrackerResolver.Adapters` map type consistent T11/T12.
- **Round-1 simplifications flagged in-code:** GitLab issue state maps only open/closed; review decision from approvals only; single GitLab host wired (list modeled for later).
