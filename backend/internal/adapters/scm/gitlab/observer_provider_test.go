package gitlab

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
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

// TestApprovalDecision locks in the new no-rule-defers-to-AO semantics:
// approvalDecision only ever says "approved" when GitLab itself enforces an
// approval rule; otherwise it returns "" and leaves the call to the project's
// ApprovalRule (via ApprovalsCount).
func TestApprovalDecision(t *testing.T) {
	approver := func(n int) []struct {
		User struct {
			Username string `json:"username"`
		} `json:"user"`
	} {
		out := make([]struct {
			User struct {
				Username string `json:"username"`
			} `json:"user"`
		}, n)
		for i := range out {
			out[i].User.Username = "u" + strconv.Itoa(i)
		}
		return out
	}

	// No rule → always "" regardless of count (threshold decides later).
	noRule := restApprovals{ApprovalsLeft: 0, ApprovalsRequired: 0, HasApprovalRules: false, ApprovedBy: approver(3)}
	if got := approvalDecision(noRule); got != "" {
		t.Fatalf("no-rule: got %q, want \"\"", got)
	}
	if got := approvalRuleConfigured(noRule); got {
		t.Fatalf("no-rule: ruleConfigured got true, want false")
	}

	// Rule present and satisfied → "approved" (unchanged behaviour).
	satisfied := restApprovals{ApprovalsLeft: 0, ApprovalsRequired: 2, HasApprovalRules: true, ApprovedBy: approver(2)}
	if got := approvalDecision(satisfied); got != "approved" {
		t.Fatalf("rule satisfied: got %q, want approved", got)
	}

	// Rule present, unsatisfied → "".
	pending := restApprovals{ApprovalsLeft: 1, ApprovalsRequired: 2, HasApprovalRules: true, ApprovedBy: approver(1)}
	if got := approvalDecision(pending); got != "" {
		t.Fatalf("rule pending: got %q, want \"\"", got)
	}
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

// TestListOpenPRsByRepoCarriesHeadRepo locks in the branch-prefix attribution
// contract: the observer's discoverNewPRs drops any open PR whose HeadRepo does
// not match a session's push origin (see candidatesForHeadRepo in
// internal/observe/scm/observer.go), so a GitLab same-project MR must report
// HeadRepo == repo.Repo or it is never attributed and its card stays in WORKING.
// A fork MR (source_project_id != target_project_id) reports an empty HeadRepo,
// so it is left unattributed rather than misattributed to an origin session.
func TestListOpenPRsByRepoCarriesHeadRepo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[
			{"iid":7,"state":"opened","source_branch":"feat","target_branch":"main","sha":"s1","web_url":"https://gl/mr/7","project_id":99,"source_project_id":99,"target_project_id":99},
			{"iid":8,"state":"opened","source_branch":"forkfeat","target_branch":"main","sha":"s2","web_url":"https://gl/mr/8","project_id":99,"source_project_id":42,"target_project_id":99}
		]`))
	}))
	defer srv.Close()
	p := newTestProvider(t, srv.URL)
	repo := ports.SCMRepo{Provider: "gitlab", Host: "gitlab.finnomena.com", Owner: "group/sub", Name: "proj", Repo: "group/sub/proj"}
	prs, err := p.ListOpenPRsByRepo(context.Background(), repo)
	if err != nil {
		t.Fatalf("ListOpenPRsByRepo: %v", err)
	}
	if len(prs) != 2 {
		t.Fatalf("want 2 PRs, got %d: %+v", len(prs), prs)
	}
	if prs[0].HeadRepo != "group/sub/proj" {
		t.Fatalf("same-project MR HeadRepo = %q, want group/sub/proj", prs[0].HeadRepo)
	}
	if prs[1].HeadRepo != "" {
		t.Fatalf("fork MR HeadRepo = %q, want empty", prs[1].HeadRepo)
	}
}

// TestBaseBranchGuardETag304 verifies the base-branch guard hits the
// repository-branches endpoint, surfaces the branch ETag, sends If-None-Match on
// the next call, and reports NotModified on a 304 — the signal the observer uses
// to re-read an MR whose target branch advanced (a sibling merged into the base).
func TestBaseBranchGuardETag304(t *testing.T) {
	var hits int
	var lastPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		lastPath = r.URL.EscapedPath()
		if r.Header.Get("If-None-Match") == `"base1"` {
			w.Header().Set("ETag", `"base1"`)
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"base1"`)
		_, _ = w.Write([]byte(`{"name":"main","commit":{"id":"abc123"}}`))
	}))
	defer srv.Close()
	p := newTestProvider(t, srv.URL)
	repo := ports.SCMRepo{Provider: "gitlab", Host: "gitlab.finnomena.com", Owner: "group/sub", Name: "proj", Repo: "group/sub/proj"}

	first, err := p.BaseBranchGuard(context.Background(), repo, "main", "")
	if err != nil {
		t.Fatalf("first BaseBranchGuard: %v", err)
	}
	if first.NotModified || first.ETag != `"base1"` {
		t.Fatalf("first guard = %+v, want ETag \"base1\" NotModified=false", first)
	}
	if !strings.Contains(lastPath, "repository/branches") {
		t.Fatalf("guard hit %q, want the repository-branches endpoint", lastPath)
	}
	second, err := p.BaseBranchGuard(context.Background(), repo, "main", first.ETag)
	if err != nil {
		t.Fatalf("second BaseBranchGuard: %v", err)
	}
	if !second.NotModified {
		t.Fatalf("second guard NotModified=false; want true (branch head unchanged)")
	}
	if hits != 2 {
		t.Fatalf("branch endpoint hits = %d, want 2", hits)
	}
	if _, err := p.BaseBranchGuard(context.Background(), repo, "  ", ""); err == nil {
		t.Fatalf("BaseBranchGuard with an empty branch must error, got nil")
	}
}

// TestCIObservationNormalizesJobStatus locks in that every emitted check status
// is one of AO's normalized domain.PRCheckStatus values. The pr_checks.status
// column CHECK-constrains writes to that vocabulary, so emitting GitLab's raw job
// statuses (success/canceled/running/manual/…) makes the whole PR observation
// write fail atomically — stranding the MR with no title/CI/mergeability.
func TestCIObservationNormalizesJobStatus(t *testing.T) {
	jobs := []restJob{
		{ID: 1, Name: "build", Status: "success"},
		{ID: 2, Name: "test", Status: "running"},
		{ID: 3, Name: "deploy", Status: "canceled"},
		{ID: 4, Name: "lint", Status: "failed"},
		{ID: 5, Name: "manual-job", Status: "manual"},
		{ID: 6, Name: "created-job", Status: "created"},
		{ID: 7, Name: "skip", Status: "skipped"},
	}
	ci := ciObservation(restPipeline{SHA: "s1", Status: "running"}, jobs)

	allowed := map[string]bool{"unknown": true, "queued": true, "in_progress": true, "passed": true, "failed": true, "skipped": true, "cancelled": true}
	byName := map[string]ports.SCMCheckObservation{}
	for _, c := range ci.Checks {
		if !allowed[c.Status] {
			t.Fatalf("check %q has non-domain status %q (would fail the pr_checks CHECK constraint)", c.Name, c.Status)
		}
		byName[c.Name] = c
	}
	if got := byName["build"].Status; got != "passed" {
		t.Fatalf("success -> %q, want passed", got)
	}
	if got := byName["test"].Status; got != "in_progress" {
		t.Fatalf("running -> %q, want in_progress", got)
	}
	if got := byName["deploy"].Status; got != "cancelled" {
		t.Fatalf("canceled -> %q, want cancelled", got)
	}
	if got := byName["manual-job"].Status; got != "queued" {
		t.Fatalf("manual -> %q, want queued", got)
	}
	// Raw provider status is preserved in Conclusion for detail.
	if got := byName["deploy"].Conclusion; got != "canceled" {
		t.Fatalf("Conclusion = %q, want raw 'canceled'", got)
	}
	if len(ci.FailedChecks) != 2 {
		t.Fatalf("FailedChecks = %d, want 2 (failed + canceled): %+v", len(ci.FailedChecks), ci.FailedChecks)
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
