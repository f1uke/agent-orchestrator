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

// TestFetchReviewThreads_PaginatesDiscussions proves the adapter pages through
// GitLab's /discussions endpoint instead of reading only the first page. GitLab
// paginates discussions (default 20, max 100 per page) and system notes count
// toward the total, so on an active MR the newest review threads land on page 2+.
// Fetching only page 1 silently drops them, freezing AO's resolved/unresolved
// view (see fix/reviews-gitlab-resolved-refresh: MR !3028's newest unresolved
// thread lived on page 2 and never reached AO). A resolvable thread on page 2
// must still be returned, and the request must ask for the max page size.
func TestFetchReviewThreads_PaginatesDiscussions(t *testing.T) {
	var page1PerPage string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/projects/group%2Fproj/merge_requests/7/discussions", func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		if page == "1" || page == "" {
			page1PerPage = r.URL.Query().Get("per_page")
		}
		if page == "2" {
			// The newest resolvable thread — only reachable if the adapter
			// requests page 2.
			_, _ = w.Write([]byte(`[{"id":"disc-p2","notes":[{"id":902,"body":"newest thread","resolvable":true,"resolved":false,"author":{"username":"rev"},"position":{"new_path":"b.go","new_line":7}}]}]`))
			return
		}
		// Page 1 (or an unpaginated request): a full page of 100 discussions —
		// one resolvable thread plus 99 non-resolvable system notes — so the
		// pagination loop must go on to page 2 to find disc-p2.
		var b strings.Builder
		b.WriteByte('[')
		b.WriteString(`{"id":"disc-p1","notes":[{"id":901,"body":"first thread","resolvable":true,"resolved":true,"author":{"username":"rev"},"position":{"new_path":"a.go","new_line":3}}]}`)
		for i := 0; i < 99; i++ {
			fmt.Fprintf(&b, `,{"id":"sys-%d","notes":[{"id":%d,"body":"system","resolvable":false,"system":true,"author":{"username":"rev"}}]}`, i, 1000+i)
		}
		b.WriteByte(']')
		_, _ = w.Write([]byte(b.String()))
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
	byID := map[string]ports.SCMReviewThreadObservation{}
	for _, th := range rev.Threads {
		byID[th.ID] = th
	}
	if _, ok := byID["disc-p1"]; !ok {
		t.Errorf("missing page-1 thread disc-p1; got %+v", rev.Threads)
	}
	p2, ok := byID["disc-p2"]
	if !ok {
		t.Fatalf("missing page-2 thread disc-p2 (pagination not applied); got %d threads: %+v", len(rev.Threads), rev.Threads)
	}
	if p2.Resolved {
		t.Errorf("disc-p2.Resolved = true, want false (fixture note has resolved:false)")
	}
	if page1PerPage != "100" {
		t.Errorf("discussions request per_page=%q, want 100 (max page size)", page1PerPage)
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
