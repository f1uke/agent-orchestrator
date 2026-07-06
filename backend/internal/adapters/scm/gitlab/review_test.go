package gitlab

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

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
