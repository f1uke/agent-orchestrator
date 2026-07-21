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

// GitLab's /merge_requests/:iid/pipelines is NOT one globally newest-first list.
// It is a UNION of two id-descending partitions — the MR's own pipelines
// (merge_request_event/detached/merged_result) first, then the source branch's
// `push` pipelines — so a HIGHER-id push pipeline sorts BELOW every lower-id
// merge_request_event row. Verified against the real API (gitlab.finnomena.com,
// MR !2986: push pipelines 177164/177162/177160 appear after merge_request_event
// pipelines 176858/176825/176804, and MR !1 of kratos-ui: 15 merge_request_event
// rows then 15 push rows, the head SHA's failed push pipeline landing at index
// 15 — off the default page).
//
// So "the head-SHA pipeline is newest, therefore always on page 1" does not
// hold: when the head SHA's only pipeline is a `push` one, it sits in the second
// partition behind every MR-event row. Reading only page 1 then matches no
// pipeline for the head SHA at all, and the MR reads ci=unknown with no checks.
// FetchPullRequests must page through the whole list.
func TestFetchPullRequests_PaginatesMRPipelinesToFindHeadSHAPipelineBeyondFirstPage(t *testing.T) {
	const proj = "/api/v4/projects/group%2Fproj"
	mux := http.NewServeMux()
	mux.HandleFunc(proj+"/merge_requests/7", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"iid":7,"state":"opened","title":"t","source_branch":"feat","target_branch":"main","sha":"sha-head","web_url":"https://gl/7","author":{"username":"fluke"}}`))
	})

	var pagesRequested []string
	mux.HandleFunc(proj+"/merge_requests/7/pipelines", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("per_page"); got != "100" {
			t.Errorf("pipelines per_page = %q, want 100", got)
		}
		page := r.URL.Query().Get("page")
		pagesRequested = append(pagesRequested, page)
		switch page {
		case "1":
			// A FULL page of the MR's own pipelines — the first UNION partition.
			// None of these is for the MR's head SHA.
			var b strings.Builder
			b.WriteByte('[')
			for i := 0; i < 100; i++ {
				if i > 0 {
					b.WriteByte(',')
				}
				fmt.Fprintf(&b, `{"id":%d,"sha":"sha-old-%d","status":"success","source":"merge_request_event"}`, 5000+i, i)
			}
			b.WriteByte(']')
			_, _ = w.Write([]byte(b.String()))
		case "2":
			// Second partition: the head SHA's `push` pipeline, which failed.
			// Note its id (900) is LOWER than page 1's ids — that is exactly the
			// real ordering, and why sorting cannot rescue a page-1-only read.
			_, _ = w.Write([]byte(`[{"id":900,"sha":"sha-head","status":"failed","source":"push"}]`))
		default:
			_, _ = w.Write([]byte(`[]`))
		}
	})

	mux.HandleFunc(proj+"/pipelines/900/jobs", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"id":11,"name":"lint","status":"failed","web_url":"https://gl/j/11"}]`))
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	p := newTestProvider(t, srv.URL)
	ref := ports.SCMPRRef{Repo: ports.SCMRepo{Repo: "group/proj", Host: "gitlab.finnomena.com", Provider: "gitlab"}, Number: 7}
	obs, err := p.FetchPullRequests(context.Background(), []ports.SCMPRRef{ref})
	if err != nil {
		t.Fatalf("FetchPullRequests: %v", err)
	}
	o := obs[0]

	// Page-1-only leaves the head SHA unmatched: Summary "unknown", HeadSHA "".
	if o.CI.Summary != "failing" {
		t.Fatalf("CI summary = %q, want failing (head-SHA pipeline on page 2 was missed)", o.CI.Summary)
	}
	if o.CI.HeadSHA != "sha-head" {
		t.Fatalf("CI head sha = %q, want sha-head", o.CI.HeadSHA)
	}
	found := false
	for _, c := range o.CI.FailedChecks {
		if c.Name == "lint" && c.Status == "failed" {
			found = true
		}
	}
	if !found {
		t.Fatalf("failed check from the head-SHA pipeline not captured; FailedChecks=%+v", o.CI.FailedChecks)
	}
	if len(pagesRequested) < 2 || pagesRequested[1] != "2" {
		t.Fatalf("pipelines pages requested = %v, want page 2 to be fetched", pagesRequested)
	}
}

// A single short page must not trigger a second request: the common case (an MR
// with a handful of pipelines) stays one API call, as offset pagination
// guarantees a page shorter than per_page is the last.
func TestFetchPullRequests_MRPipelinesStopsAfterShortPage(t *testing.T) {
	const proj = "/api/v4/projects/group%2Fproj"
	mux := http.NewServeMux()
	mux.HandleFunc(proj+"/merge_requests/7", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"iid":7,"state":"opened","title":"t","source_branch":"feat","target_branch":"main","sha":"sha-head","web_url":"https://gl/7","author":{"username":"fluke"}}`))
	})
	var pages int
	mux.HandleFunc(proj+"/merge_requests/7/pipelines", func(w http.ResponseWriter, _ *http.Request) {
		pages++
		_, _ = w.Write([]byte(`[{"id":900,"sha":"sha-head","status":"success","source":"merge_request_event"}]`))
	})
	mux.HandleFunc(proj+"/pipelines/900/jobs", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[]`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	p := newTestProvider(t, srv.URL)
	ref := ports.SCMPRRef{Repo: ports.SCMRepo{Repo: "group/proj", Host: "gitlab.finnomena.com", Provider: "gitlab"}, Number: 7}
	obs, err := p.FetchPullRequests(context.Background(), []ports.SCMPRRef{ref})
	if err != nil {
		t.Fatalf("FetchPullRequests: %v", err)
	}
	if obs[0].CI.Summary != "passing" {
		t.Fatalf("CI summary = %q, want passing", obs[0].CI.Summary)
	}
	if pages != 1 {
		t.Fatalf("pipelines pages requested = %d, want 1 (short page ends pagination)", pages)
	}
}
