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
	if rev.Threads[0].Resolved {
		t.Fatalf("threads[0].Resolved = true, want false (fixture note has resolved:false)")
	}
	if rev.Threads[0].IsBot {
		t.Fatalf("threads[0].IsBot = true, want false (fixture author %q is not a bot)", "rev")
	}
	if len(rev.Threads[0].Comments) != 1 || rev.Threads[0].Comments[0].Body != "please fix" {
		t.Fatalf("comments wrong: %+v", rev.Threads[0].Comments)
	}
	if rev.Threads[0].Comments[0].IsBot {
		t.Fatalf("comments[0].IsBot = true, want false")
	}
}

// TestFetchReviewThreads_DropsNonResolvableDiscussions verifies the adapter's
// thread-inclusion gate: a discussion is only surfaced as a review Thread
// when at least one of its notes is Resolvable (diff-anchored review
// comments), per discussionToThread's contract. Plain MR conversation
// (a discussion whose notes are all non-resolvable) must be dropped.
func TestFetchReviewThreads_DropsNonResolvableDiscussions(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/projects/group%2Fproj/merge_requests/7/discussions", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[
			{"id":"disc-resolvable","notes":[{"id":101,"body":"please fix","resolvable":true,"resolved":false,"author":{"username":"rev"},"position":{"new_path":"main.go","new_line":42}}]},
			{"id":"disc-plain","notes":[{"id":102,"body":"nice work","resolvable":false,"resolved":false,"author":{"username":"rev"}}]}
		]`))
	})
	mux.HandleFunc("/api/v4/projects/group%2Fproj/merge_requests/7/approvals", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"approvals_left":0,"approved_by":[]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	p := newTestProvider(t, srv.URL)
	ref := ports.SCMPRRef{Repo: ports.SCMRepo{Repo: "group/proj"}, Number: 7}
	rev, err := p.FetchReviewThreads(context.Background(), ref)
	if err != nil {
		t.Fatalf("FetchReviewThreads: %v", err)
	}
	if len(rev.Threads) != 1 {
		t.Fatalf("Threads = %+v, want exactly 1 (non-resolvable discussion should be dropped)", rev.Threads)
	}
	if rev.Threads[0].ID != "disc-resolvable" {
		t.Fatalf("Threads[0].ID = %q, want %q", rev.Threads[0].ID, "disc-resolvable")
	}
}

// TestIsBotUsername guards against the raw strings.Contains(login, "bot")
// false-positive magnet that was deliberately removed from the GitHub
// adapter (see scm/github/provider.go's isBotAuthor doc: logins like
// "robothon"/"lambot123" tripped it). GitLab's UserBasic payload has no
// typed bot field, so isBotUsername matches GitLab's underscore-delimited
// bot-account naming convention instead.
func TestIsBotUsername(t *testing.T) {
	cases := []struct {
		username string
		want     bool
	}{
		{"robothon", false},
		{"lambot123", false},
		{"alice", false},
		{"project_12_bot_a1b2", true},
		{"support_bot", true},
		{"group_9_bot_ff", true},
	}
	for _, tc := range cases {
		if got := isBotUsername(tc.username); got != tc.want {
			t.Errorf("isBotUsername(%q) = %v, want %v", tc.username, got, tc.want)
		}
	}
}
