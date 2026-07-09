package gitlab

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestFetchPullRequestsMergeabilityAndCI(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/projects/group%2Fproj/merge_requests/7", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"iid":7,"state":"opened","draft":false,"title":"t","source_branch":"feat","target_branch":"main","sha":"sha1","merge_status":"cannot_be_merged","has_conflicts":true,"changes_count":"3","web_url":"https://gl/7","author":{"username":"fluke"},"diff_refs":{"base_sha":"base1","head_sha":"sha1","start_sha":"start1"}}`))
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
	// BaseSHA must come from GitLab's diff_refs.base_sha so diff-context can
	// run `git diff base..head`; without it GitLab MRs never render a hunk.
	if o.PR.HeadSHA != "sha1" || o.PR.BaseSHA != "base1" {
		t.Fatalf("SHAs wrong: head=%q base=%q, want head=sha1 base=base1", o.PR.HeadSHA, o.PR.BaseSHA)
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
