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
	// This fixture carries no approval-rule fields (approvals_required=0,
	// has_approval_rules=false), so per the new no-rule-defers-to-AO
	// semantics, Decision is "" and the project's ApprovalRule is expected to
	// decide from ApprovalsCount instead.
	if rev.Decision != "" {
		t.Fatalf("decision=%q want \"\" (no approval rule configured)", rev.Decision)
	}
	if rev.ApprovalsCount != 1 {
		t.Fatalf("ApprovalsCount=%d want 1", rev.ApprovalsCount)
	}
	if rev.ApprovalRuleConfigured {
		t.Fatalf("ApprovalRuleConfigured=true want false (fixture has no rule fields)")
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

// TestFetchReviewThreads_MarksSystemNotes verifies that GitLab auto-generated
// system notes (system:true — e.g. "changed this line in version 6 of the diff"
// appended when a thread goes outdated) are flagged System on the observation so
// downstream rendering can de-emphasize them instead of treating them as a second
// user comment. The note stays in the thread (nothing dropped) and does not change
// the thread's resolved/inclusion outcome, which is driven by the resolvable note.
func TestFetchReviewThreads_MarksSystemNotes(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/projects/group%2Fproj/merge_requests/7/discussions", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"id":"disc1","notes":[
			{"id":101,"body":"please fix","resolvable":true,"resolved":false,"system":false,"author":{"username":"rev"},"position":{"new_path":"main.go","new_line":42}},
			{"id":102,"body":"changed this line in [version 6 of the diff](/g/p/-/merge_requests/7/diffs?diff_id=1)","resolvable":false,"resolved":false,"system":true,"author":{"username":"rev"}}
		]}]`))
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
		t.Fatalf("Threads = %+v, want exactly 1", rev.Threads)
	}
	cs := rev.Threads[0].Comments
	if len(cs) != 2 {
		t.Fatalf("Comments = %+v, want 2 (system note retained, not dropped)", cs)
	}
	if cs[0].System {
		t.Errorf("comments[0].System = true, want false (real user note)")
	}
	if !cs[1].System {
		t.Errorf("comments[1].System = false, want true (GitLab system note)")
	}
	// The system note is non-resolvable, so it must not flip the thread's
	// resolved state — resolution is driven solely by the resolvable user note.
	if rev.Threads[0].Resolved {
		t.Errorf("threads[0].Resolved = true, want false (driven by the unresolved user note)")
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
