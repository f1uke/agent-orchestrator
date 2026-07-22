package gitlab

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// A pipeline reports status=failed, but its failing job (a lint gate) sorts
// past the first page of GitLab's /pipelines/:id/jobs endpoint (GitLab orders
// jobs newest-id-first, so early-stage jobs land on later pages). Fetching only
// the first page left CI.FailedChecks empty while CI.Summary was "failing", so
// the lifecycle CI-fail nudge — which requires a failed check row — never fired.
// FetchPullRequests must page through all jobs so the failed one is captured.
func TestFetchPullRequests_PaginatesPipelineJobsToFindFailedJobBeyondFirstPage(t *testing.T) {
	const proj = "/api/v4/projects/group%2Fproj"
	mux := http.NewServeMux()
	mux.HandleFunc(proj+"/merge_requests/7", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"iid":7,"state":"opened","title":"t","source_branch":"feat","target_branch":"main","sha":"sha1","web_url":"https://gl/7","author":{"username":"fluke"}}`))
	})
	mux.HandleFunc(proj+"/merge_requests/7/pipelines", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"id":900,"sha":"sha1","status":"failed"}]`))
	})
	var pagesRequested []string
	mux.HandleFunc(proj+"/pipelines/900/jobs", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("per_page"); got != "100" {
			t.Errorf("jobs per_page = %q, want 100", got)
		}
		page := r.URL.Query().Get("page")
		pagesRequested = append(pagesRequested, page)
		switch page {
		case "1":
			// A FULL page (100) of skipped jobs — the failing lint gate is not here.
			var b strings.Builder
			b.WriteByte('[')
			for i := 0; i < 100; i++ {
				if i > 0 {
					b.WriteByte(',')
				}
				fmt.Fprintf(&b, `{"id":%d,"name":"run_uitest_%d","status":"skipped"}`, 2000+i, i)
			}
			b.WriteByte(']')
			_, _ = w.Write([]byte(b.String()))
		case "2":
			// The failed lint gate lands on page 2. A short page (< per_page) ends
			// the pagination loop.
			_, _ = w.Write([]byte(`[{"id":11,"name":"swiftlint_report","status":"failed","web_url":"https://gl/j/11"}]`))
		default:
			_, _ = w.Write([]byte(`[]`))
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	p := newTestProvider(t, srv.URL)
	ref := ports.SCMPRRef{Repo: ports.SCMRepo{Repo: "group/proj", Host: "gitlab.example.com", Provider: "gitlab"}, Number: 7}
	obs, err := p.FetchPullRequests(context.Background(), []ports.SCMPRRef{ref})
	if err != nil {
		t.Fatalf("FetchPullRequests: %v", err)
	}
	o := obs[0]
	if o.CI.Summary != "failing" {
		t.Fatalf("CI summary = %q, want failing", o.CI.Summary)
	}
	// The point of the fix: the failed job on page 2 must be captured so the
	// CI-fail nudge (which reads FailedChecks) has a check to fire on.
	found := false
	for _, c := range o.CI.FailedChecks {
		if c.Name == "swiftlint_report" && c.Status == "failed" {
			found = true
		}
	}
	if !found {
		t.Fatalf("failed job on page 2 not captured; FailedChecks=%+v (CI-fail nudge would never fire)", o.CI.FailedChecks)
	}
	if len(pagesRequested) < 2 {
		t.Fatalf("jobs endpoint was not paginated past page 1; pages requested = %v", pagesRequested)
	}
}
