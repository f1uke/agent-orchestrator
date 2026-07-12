package jira

import (
	"context"
	"errors"
	"strings"
	"testing"

	jiraadapter "github.com/aoagents/agent-orchestrator/backend/internal/adapters/jira"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// bindRec records the last SetIssueBinding call. A pointer field on the (value)
// fakeSessions so a copied value still records into the same struct.
type bindRec struct {
	issueID string
	display string
	called  bool
}

type fakeSessions struct {
	sess domain.Session
	err  error
	rec  *bindRec
}

func (f fakeSessions) Get(context.Context, domain.SessionID) (domain.Session, error) {
	return f.sess, f.err
}

func (f fakeSessions) SetIssueBinding(_ context.Context, _ domain.SessionID, issueID, displayName string) (domain.Session, error) {
	if f.rec != nil {
		f.rec.issueID = issueID
		f.rec.display = displayName
		f.rec.called = true
	}
	s := f.sess
	s.IssueID = domain.IssueID(issueID)
	s.DisplayName = displayName
	return s, nil
}

type fakeIssues struct {
	iss jiraadapter.Issue
	err error
	got string
}

func (f *fakeIssues) Get(_ context.Context, key string) (jiraadapter.Issue, error) {
	f.got = key
	return f.iss, f.err
}

type fakeMover struct {
	transitions []jiraadapter.Transition
	listErr     error
	moveErr     error
	gotKey      string
	gotID       string
}

func (f *fakeMover) Transitions(_ context.Context, key string) ([]jiraadapter.Transition, error) {
	f.gotKey = key
	return f.transitions, f.listErr
}

func (f *fakeMover) Move(_ context.Context, key, transitionID string) error {
	f.gotKey = key
	f.gotID = transitionID
	return f.moveErr
}

type fakeSearcher struct {
	issues   []jiraadapter.IssueSummary
	projects []jiraadapter.ProjectRef
	err      error
	gotJQL   string
	gotMax   int
}

func (f *fakeSearcher) SearchIssues(_ context.Context, jql string, limit int) ([]jiraadapter.IssueSummary, error) {
	f.gotJQL = jql
	f.gotMax = limit
	return f.issues, f.err
}

func (f *fakeSearcher) ListProjects(_ context.Context, _ string) ([]jiraadapter.ProjectRef, error) {
	return f.projects, f.err
}

func sessionWith(issueID string) domain.Session {
	return domain.Session{SessionRecord: domain.SessionRecord{ID: "s1", IssueID: domain.IssueID(issueID)}}
}

func TestContext_NotLinked(t *testing.T) {
	for _, id := range []string{"", "Fix the thing", "github:acme/repo#1", "gitlab:grp/proj#2", "jira:"} {
		issues := &fakeIssues{}
		svc := New(fakeSessions{sess: sessionWith(id)}, issues, nil, nil)
		res, err := svc.Context(context.Background(), "s1")
		if err != nil {
			t.Fatalf("id=%q err %v", id, err)
		}
		if res.Linked {
			t.Errorf("id=%q should be unlinked", id)
		}
		if issues.got != "" {
			t.Errorf("id=%q must not call the issue reader", id)
		}
	}
}

func TestContext_LinkedSuccess(t *testing.T) {
	issues := &fakeIssues{iss: jiraadapter.Issue{Key: "DEMO-101", Title: "T"}}
	svc := New(fakeSessions{sess: sessionWith("jira:DEMO-101")}, issues, nil, nil)
	res, err := svc.Context(context.Background(), "s1")
	if err != nil {
		t.Fatalf("err %v", err)
	}
	if !res.Linked || res.Issue == nil || res.Issue.Key != "DEMO-101" {
		t.Fatalf("result = %+v", res)
	}
	if res.FetchError != "" {
		t.Errorf("unexpected fetch error %q", res.FetchError)
	}
	if issues.got != "DEMO-101" {
		t.Errorf("reader got key %q", issues.got)
	}
}

func TestContext_LinkedFetchError(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{jiraadapter.ErrNotFound, "not found"},
		{jiraadapter.ErrAuthFailed, "authentication"},
		{jiraadapter.ErrUnavailable, "unavailable"},
	}
	for _, tc := range cases {
		issues := &fakeIssues{err: tc.err}
		svc := New(fakeSessions{sess: sessionWith("jira:DEMO-1")}, issues, nil, nil)
		res, err := svc.Context(context.Background(), "s1")
		if err != nil {
			t.Fatalf("err %v", err)
		}
		if !res.Linked || res.Issue != nil || res.FetchError == "" {
			t.Fatalf("expected linked with fetch error, got %+v", res)
		}
	}
}

func TestContext_NilIssueReader(t *testing.T) {
	svc := New(fakeSessions{sess: sessionWith("jira:DEMO-1")}, nil, nil, nil)
	res, err := svc.Context(context.Background(), "s1")
	if err != nil {
		t.Fatalf("err %v", err)
	}
	if !res.Linked || res.Issue != nil || res.FetchError == "" {
		t.Errorf("nil reader should report a fetch error, got %+v", res)
	}
}

func TestContext_SessionErrorPropagates(t *testing.T) {
	sentinel := errors.New("boom")
	svc := New(fakeSessions{err: sentinel}, &fakeIssues{}, nil, nil)
	if _, err := svc.Context(context.Background(), "s1"); !errors.Is(err, sentinel) {
		t.Errorf("err = %v, want propagated session error", err)
	}
}

func TestTransitions_Success(t *testing.T) {
	mover := &fakeMover{transitions: []jiraadapter.Transition{{ID: "11", Name: "Start Testing", To: "In Progress"}}}
	svc := New(fakeSessions{sess: sessionWith("jira:DEMO-101")}, &fakeIssues{}, mover, nil)
	ts, err := svc.Transitions(context.Background(), "s1", "")
	if err != nil {
		t.Fatalf("err %v", err)
	}
	if len(ts) != 1 || ts[0].Name != "Start Testing" {
		t.Fatalf("transitions = %+v", ts)
	}
	if mover.gotKey != "DEMO-101" {
		t.Errorf("mover got key %q", mover.gotKey)
	}
}

func TestTransitions_NotLinked(t *testing.T) {
	mover := &fakeMover{}
	svc := New(fakeSessions{sess: sessionWith("github:acme/repo#1")}, &fakeIssues{}, mover, nil)
	if _, err := svc.Transitions(context.Background(), "s1", ""); !errors.Is(err, ErrNotLinked) {
		t.Errorf("err = %v, want ErrNotLinked", err)
	}
	if mover.gotKey != "" {
		t.Errorf("must not call the mover for an unlinked session")
	}
}

func TestTransitions_NilMover(t *testing.T) {
	svc := New(fakeSessions{sess: sessionWith("jira:DEMO-1")}, &fakeIssues{}, nil, nil)
	if _, err := svc.Transitions(context.Background(), "s1", ""); !errors.Is(err, jiraadapter.ErrUnavailable) {
		t.Errorf("err = %v, want ErrUnavailable", err)
	}
}

func TestMove_SuccessReReadsStatus(t *testing.T) {
	issues := &fakeIssues{iss: jiraadapter.Issue{Key: "DEMO-101", Status: "In Progress", StatusCategory: "indeterminate"}}
	mover := &fakeMover{}
	svc := New(fakeSessions{sess: sessionWith("jira:DEMO-101")}, issues, mover, nil)
	res, err := svc.Move(context.Background(), "s1", "", "11")
	if err != nil {
		t.Fatalf("err %v", err)
	}
	if mover.gotKey != "DEMO-101" || mover.gotID != "11" {
		t.Errorf("mover got key=%q id=%q", mover.gotKey, mover.gotID)
	}
	if res.Key != "DEMO-101" || res.Status != "In Progress" || res.StatusCategory != "indeterminate" {
		t.Errorf("result = %+v, want the re-read status", res)
	}
}

func TestMove_NotLinked(t *testing.T) {
	mover := &fakeMover{}
	svc := New(fakeSessions{sess: sessionWith("")}, &fakeIssues{}, mover, nil)
	if _, err := svc.Move(context.Background(), "s1", "", "11"); !errors.Is(err, ErrNotLinked) {
		t.Errorf("err = %v, want ErrNotLinked", err)
	}
	if mover.gotID != "" {
		t.Errorf("must not apply a move for an unlinked session")
	}
}

func TestMove_RejectionPropagates(t *testing.T) {
	mover := &fakeMover{moveErr: jiraadapter.ErrBadTransition}
	svc := New(fakeSessions{sess: sessionWith("jira:DEMO-1")}, &fakeIssues{}, mover, nil)
	if _, err := svc.Move(context.Background(), "s1", "", "99"); !errors.Is(err, jiraadapter.ErrBadTransition) {
		t.Errorf("err = %v, want ErrBadTransition", err)
	}
}

func TestMove_SucceedsEvenIfReReadFails(t *testing.T) {
	// A successful move must not be reported as a failure just because the
	// best-effort status re-read errors.
	issues := &fakeIssues{err: jiraadapter.ErrUnavailable}
	svc := New(fakeSessions{sess: sessionWith("jira:DEMO-1")}, issues, &fakeMover{}, nil)
	res, err := svc.Move(context.Background(), "s1", "", "11")
	if err != nil {
		t.Fatalf("err %v", err)
	}
	if res.Key != "DEMO-1" || res.Status != "" {
		t.Errorf("result = %+v, want key set and empty status", res)
	}
}

// A subtask of the bound issue can be listed + moved by naming its key.
func TestTransitions_SubtaskOfBound(t *testing.T) {
	issues := &fakeIssues{iss: jiraadapter.Issue{Key: "DEMO-101", Subtasks: []jiraadapter.Subtask{{Key: "DEMO-102"}}}}
	mover := &fakeMover{transitions: []jiraadapter.Transition{{ID: "21", Name: "Ship"}}}
	svc := New(fakeSessions{sess: sessionWith("jira:DEMO-101")}, issues, mover, nil)
	if _, err := svc.Transitions(context.Background(), "s1", "DEMO-102"); err != nil {
		t.Fatalf("err %v", err)
	}
	if mover.gotKey != "DEMO-102" {
		t.Errorf("mover got key %q, want the subtask DEMO-102", mover.gotKey)
	}
}

func TestMove_SubtaskOfBound(t *testing.T) {
	issues := &fakeIssues{iss: jiraadapter.Issue{Key: "DEMO-101", Subtasks: []jiraadapter.Subtask{{Key: "DEMO-102"}}}}
	mover := &fakeMover{}
	svc := New(fakeSessions{sess: sessionWith("jira:DEMO-101")}, issues, mover, nil)
	res, err := svc.Move(context.Background(), "s1", "demo-102", "21") // lower-cased on purpose
	if err != nil {
		t.Fatalf("err %v", err)
	}
	if mover.gotKey != "DEMO-102" || mover.gotID != "21" {
		t.Errorf("mover got key=%q id=%q, want DEMO-102/21", mover.gotKey, mover.gotID)
	}
	if res.Key != "DEMO-102" {
		t.Errorf("result key = %q, want the subtask key", res.Key)
	}
}

// A key that is neither the bound issue nor one of its subtasks is refused — the
// move stays scoped to the session's own issue tree.
func TestMove_ForeignKeyRejected(t *testing.T) {
	issues := &fakeIssues{iss: jiraadapter.Issue{Key: "DEMO-101", Subtasks: []jiraadapter.Subtask{{Key: "DEMO-102"}}}}
	mover := &fakeMover{}
	svc := New(fakeSessions{sess: sessionWith("jira:DEMO-101")}, issues, mover, nil)
	if _, err := svc.Move(context.Background(), "s1", "OTHER-9", "21"); !errors.Is(err, ErrKeyNotInIssueTree) {
		t.Errorf("err = %v, want ErrKeyNotInIssueTree", err)
	}
	if mover.gotID != "" {
		t.Errorf("must not apply a move for a foreign key")
	}
}

func TestMove_MalformedTargetKeyRejected(t *testing.T) {
	issues := &fakeIssues{iss: jiraadapter.Issue{Key: "DEMO-101"}}
	svc := New(fakeSessions{sess: sessionWith("jira:DEMO-101")}, issues, &fakeMover{}, nil)
	if _, err := svc.Move(context.Background(), "s1", "not-a-key", "21"); !errors.Is(err, jiraadapter.ErrBadKey) {
		t.Errorf("err = %v, want ErrBadKey", err)
	}
}

// ---- search / resolve / bind ----

func newSearchSvc(searcher IssueSearcher) *Service {
	return New(fakeSessions{sess: sessionWith("")}, &fakeIssues{}, nil, searcher)
}

func TestBuildJQL_ExactKey(t *testing.T) {
	s := &fakeSearcher{issues: []jiraadapter.IssueSummary{{Key: "DEMO-101"}}}
	if _, err := newSearchSvc(s).Search(context.Background(), "", "demo-101"); err != nil {
		t.Fatal(err)
	}
	if s.gotJQL != `key = "DEMO-101"` {
		t.Errorf("jql = %q, want exact-key resolve", s.gotJQL)
	}
}

func TestBuildJQL_TextSearch(t *testing.T) {
	s := &fakeSearcher{}
	if _, err := newSearchSvc(s).Search(context.Background(), "", "eligible"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s.gotJQL, `summary ~ "eligible*"`) || !strings.Contains(s.gotJQL, `text ~ "eligible*"`) {
		t.Errorf("jql = %q, want a summary/text contains-search", s.gotJQL)
	}
	if !strings.HasSuffix(s.gotJQL, "ORDER BY updated DESC") {
		t.Errorf("jql = %q, want newest-first", s.gotJQL)
	}
}

func TestBuildJQL_BareProjectKeyScopes(t *testing.T) {
	// "demo" is confirmed to be a real project key → scope to it (so DEMO-* show,
	// which a text match never would).
	s := &fakeSearcher{projects: []jiraadapter.ProjectRef{{Key: "DEMO", Name: "DEMO project"}}}
	if _, err := newSearchSvc(s).Search(context.Background(), "", "demo"); err != nil {
		t.Fatal(err)
	}
	if s.gotJQL != `project = "DEMO" ORDER BY updated DESC` {
		t.Errorf("jql = %q, want project-scoped", s.gotJQL)
	}
}

func TestBuildJQL_BareTokenNotAProjectFallsBackToText(t *testing.T) {
	// No project matches "demo" → do NOT emit `project = "DEMO"` (a 400); text search.
	s := &fakeSearcher{projects: nil}
	if _, err := newSearchSvc(s).Search(context.Background(), "", "demo"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(s.gotJQL, "project =") {
		t.Errorf("jql = %q, must not scope to an unconfirmed project", s.gotJQL)
	}
	if !strings.Contains(s.gotJQL, `summary ~ "demo*"`) {
		t.Errorf("jql = %q, want text fallback", s.gotJQL)
	}
}

func TestBuildJQL_ExplicitProjectAndText(t *testing.T) {
	s := &fakeSearcher{}
	if _, err := newSearchSvc(s).Search(context.Background(), "DEMO", "coupon"); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(s.gotJQL, `project = "DEMO" AND (summary ~ "coupon*"`) {
		t.Errorf("jql = %q, want project-scoped text search", s.gotJQL)
	}
}

func TestSearch_NilSearcher(t *testing.T) {
	svc := New(fakeSessions{}, &fakeIssues{}, nil, nil)
	if _, err := svc.Search(context.Background(), "", "x"); !errors.Is(err, jiraadapter.ErrUnavailable) {
		t.Errorf("err = %v, want ErrUnavailable", err)
	}
}

func TestResolve_BadKey(t *testing.T) {
	if _, err := newSearchSvc(&fakeSearcher{}).Resolve(context.Background(), "not a key"); !errors.Is(err, jiraadapter.ErrBadKey) {
		t.Errorf("err = %v, want ErrBadKey", err)
	}
}

func TestResolve_NotFound(t *testing.T) {
	s := &fakeSearcher{issues: nil}
	if _, err := newSearchSvc(s).Resolve(context.Background(), "demo-9"); !errors.Is(err, jiraadapter.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
	if s.gotJQL != `key = "DEMO-9"` {
		t.Errorf("jql = %q", s.gotJQL)
	}
}

func TestResolve_Success(t *testing.T) {
	s := &fakeSearcher{issues: []jiraadapter.IssueSummary{{Key: "DEMO-9", Title: "T"}}}
	iss, err := newSearchSvc(s).Resolve(context.Background(), "DEMO-9")
	if err != nil {
		t.Fatal(err)
	}
	if iss.Key != "DEMO-9" {
		t.Errorf("iss = %+v", iss)
	}
}

func TestSetBinding_ResolvesAndBinds(t *testing.T) {
	rec := &bindRec{}
	searcher := &fakeSearcher{issues: []jiraadapter.IssueSummary{{Key: "DEMO-2272", Title: "Example story"}}}
	svc := New(fakeSessions{sess: sessionWith("old title"), rec: rec}, &fakeIssues{}, nil, searcher)
	iss, err := svc.SetBinding(context.Background(), "s1", "demo-2272")
	if err != nil {
		t.Fatal(err)
	}
	if iss.Key != "DEMO-2272" {
		t.Errorf("returned issue = %+v", iss)
	}
	if !rec.called || rec.issueID != "jira:DEMO-2272" || rec.display != "Example story" {
		t.Errorf("binding rec = %+v, want jira:DEMO-2272 + issue title", rec)
	}
}

func TestSetBinding_PreservesExistingDisplayName(t *testing.T) {
	rec := &bindRec{}
	sess := domain.Session{SessionRecord: domain.SessionRecord{ID: "s1", IssueID: "old", DisplayName: "My label"}}
	searcher := &fakeSearcher{issues: []jiraadapter.IssueSummary{{Key: "DEMO-1", Title: "Some title"}}}
	svc := New(fakeSessions{sess: sess, rec: rec}, &fakeIssues{}, nil, searcher)
	if _, err := svc.SetBinding(context.Background(), "s1", "DEMO-1"); err != nil {
		t.Fatal(err)
	}
	if rec.display != "My label" {
		t.Errorf("display = %q, want the existing label preserved", rec.display)
	}
}

func TestSetBinding_UnknownKeyDoesNotBind(t *testing.T) {
	rec := &bindRec{}
	svc := New(fakeSessions{sess: sessionWith("old"), rec: rec}, &fakeIssues{}, nil, &fakeSearcher{issues: nil})
	if _, err := svc.SetBinding(context.Background(), "s1", "DEMO-9"); !errors.Is(err, jiraadapter.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
	if rec.called {
		t.Error("must not bind when the key does not resolve")
	}
}

func TestUnlink_Success(t *testing.T) {
	rec := &bindRec{}
	sess := domain.Session{SessionRecord: domain.SessionRecord{ID: "s1", IssueID: "jira:DEMO-2272", DisplayName: "My label"}}
	svc := New(fakeSessions{sess: sess, rec: rec}, &fakeIssues{}, nil, &fakeSearcher{})
	if _, err := svc.Unlink(context.Background(), "s1"); err != nil {
		t.Fatal(err)
	}
	if !rec.called || rec.issueID != "My label" || rec.display != "My label" {
		t.Errorf("unlink rec = %+v, want issue_id reset to the plain label", rec)
	}
}

func TestUnlink_NotLinked(t *testing.T) {
	rec := &bindRec{}
	svc := New(fakeSessions{sess: sessionWith("plain title"), rec: rec}, &fakeIssues{}, nil, &fakeSearcher{})
	if _, err := svc.Unlink(context.Background(), "s1"); !errors.Is(err, ErrNotLinked) {
		t.Errorf("err = %v, want ErrNotLinked", err)
	}
	if rec.called {
		t.Error("must not write when there is nothing to unlink")
	}
}
