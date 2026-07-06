package gitlab

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// newTestProvider builds a Provider whose Client points at an httptest fake
// server, bypassing token preflight so tests don't need a real token source.
// apiBase gets "/api/v4" appended, mirroring production APIBase values (e.g.
// "https://gitlab.example.com/api/v4") so fake servers that register REST v4
// paths (as GitLab actually serves them) match real request paths.
func newTestProvider(t *testing.T, apiBase string) *Provider {
	t.Helper()
	p, err := NewProvider(ProviderOptions{
		Client:             NewClient(ClientOptions{APIBase: apiBase + "/api/v4", Token: StaticTokenSource("t")}),
		Host:               "gitlab.finnomena.com",
		SkipTokenPreflight: true,
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	return p
}

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
	if len(prs) != 1 || prs[0].Number != 7 || prs[0].State != "draft" || !prs[0].Draft {
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

func TestCommitChecksGuard304(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/projects/") || !strings.HasSuffix(r.URL.Path, "/pipelines") {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if r.URL.Query().Get("sha") != "deadbeef" {
			t.Fatalf("expected sha query param, got %q", r.URL.Query().Get("sha"))
		}
		if r.Header.Get("If-None-Match") == `"pipe-1"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"pipe-1"`)
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()
	p := newTestProvider(t, srv.URL)
	repo := ports.SCMRepo{Repo: "group/sub/proj"}
	res, err := p.CommitChecksGuard(context.Background(), repo, "deadbeef", `"pipe-1"`)
	if err != nil || !res.NotModified {
		t.Fatalf("guard=%+v err=%v", res, err)
	}
}
