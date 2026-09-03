package gitlab

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// GitLab's analogue of the GitHub superseded-run defect (fix/ci-status-latest-run):
// a job that failed and was RETRIED to green must not leave the MR red. GitLab is
// structurally safe here and this pins the two reasons why, so neither is lost:
//
//   - the CI summary is the PIPELINE's own status, which GitLab recomputes on a
//     retry (success), rather than an "any failed job?" scan; and
//   - /pipelines/:id/jobs returns only the LATEST attempt of each job unless the
//     caller asks for include_retried=true, which AO must never do.
func TestFetchPullRequests_RetriedJobLeavesMRPassing(t *testing.T) {
	const proj = "/api/v4/projects/group%2Fproj"
	mux := http.NewServeMux()
	mux.HandleFunc(proj+"/merge_requests/7", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"iid":7,"state":"opened","title":"t","source_branch":"feat","target_branch":"main","sha":"sha1","web_url":"https://gl/7","author":{"username":"fluke"}}`))
	})
	mux.HandleFunc(proj+"/merge_requests/7/pipelines", func(w http.ResponseWriter, _ *http.Request) {
		// The retry runs in the SAME pipeline, whose status is now success.
		_, _ = w.Write([]byte(`[{"id":900,"sha":"sha1","status":"success"}]`))
	})
	mux.HandleFunc(proj+"/pipelines/900/jobs", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("include_retried"); got != "" {
			t.Errorf("jobs include_retried = %q, want unset: retried attempts would be listed as failed checks", got)
		}
		_, _ = w.Write([]byte(`[{"id":11,"name":"format","status":"success","web_url":"https://gl/j/11"}]`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	p := newTestProvider(t, srv.URL)
	ref := ports.SCMPRRef{Repo: ports.SCMRepo{Repo: "group/proj", Host: "gitlab.example.com", Provider: "gitlab"}, Number: 7}
	obs, err := p.FetchPullRequests(context.Background(), []ports.SCMPRRef{ref})
	if err != nil {
		t.Fatalf("FetchPullRequests: %v", err)
	}
	if obs[0].CI.Summary != "passing" {
		t.Fatalf("CI summary = %q, want passing", obs[0].CI.Summary)
	}
	if len(obs[0].CI.FailedChecks) != 0 {
		t.Fatalf("failed checks = %+v, want none", obs[0].CI.FailedChecks)
	}
}

// A re-run that lands in a NEW pipeline for the same head must win over the one
// that failed: latestPipelineForSHA takes the newest id, so the summary follows
// the retry rather than the attempt it replaced.
func TestFetchPullRequests_NewerPipelineForSameSHAWins(t *testing.T) {
	const proj = "/api/v4/projects/group%2Fproj"
	mux := http.NewServeMux()
	mux.HandleFunc(proj+"/merge_requests/7", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"iid":7,"state":"opened","title":"t","source_branch":"feat","target_branch":"main","sha":"sha1","web_url":"https://gl/7","author":{"username":"fluke"}}`))
	})
	mux.HandleFunc(proj+"/merge_requests/7/pipelines", func(w http.ResponseWriter, _ *http.Request) {
		// Oldest-first on purpose: the list order must not decide this.
		_, _ = w.Write([]byte(`[{"id":900,"sha":"sha1","status":"failed"},{"id":901,"sha":"sha1","status":"success"}]`))
	})
	mux.HandleFunc(proj+"/pipelines/901/jobs", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"id":12,"name":"format","status":"success","web_url":"https://gl/j/12"}]`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	p := newTestProvider(t, srv.URL)
	ref := ports.SCMPRRef{Repo: ports.SCMRepo{Repo: "group/proj", Host: "gitlab.example.com", Provider: "gitlab"}, Number: 7}
	obs, err := p.FetchPullRequests(context.Background(), []ports.SCMPRRef{ref})
	if err != nil {
		t.Fatalf("FetchPullRequests: %v", err)
	}
	if obs[0].CI.Summary != "passing" {
		t.Fatalf("CI summary = %q, want passing from the newer pipeline", obs[0].CI.Summary)
	}
}
