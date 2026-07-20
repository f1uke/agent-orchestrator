package jira

import (
	"context"
	"errors"
	"io"
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

	dlBody  string
	dlCtype string
	dlErr   error
	gotDlID string
}

func (f *fakeIssues) Get(_ context.Context, key string) (jiraadapter.Issue, error) {
	f.got = key
	return f.iss, f.err
}

func (f *fakeIssues) DownloadAttachment(_ context.Context, attachmentID string) (io.ReadCloser, string, error) {
	f.gotDlID = attachmentID
	if f.dlErr != nil {
		return nil, "", f.dlErr
	}
	return io.NopCloser(strings.NewReader(f.dlBody)), f.dlCtype, nil
}

func TestDownloadAttachment_StreamsForLinkedSession(t *testing.T) {
	issues := &fakeIssues{dlBody: "BYTES", dlCtype: "image/png"}
	svc := New(fakeSessions{sess: sessionWith("jira:DEMO-1")}, issues, nil, nil)
	rc, ctype, err := svc.DownloadAttachment(context.Background(), "s1", "173517")
	if err != nil {
		t.Fatalf("DownloadAttachment: %v", err)
	}
	defer func() { _ = rc.Close() }()
	b, _ := io.ReadAll(rc)
	if string(b) != "BYTES" || ctype != "image/png" {
		t.Fatalf("got %q %q", b, ctype)
	}
	if issues.gotDlID != "173517" {
		t.Errorf("attachment id = %q", issues.gotDlID)
	}
}

func TestDownloadAttachment_UnlinkedSessionErrors(t *testing.T) {
	svc := New(fakeSessions{sess: sessionWith("")}, &fakeIssues{}, nil, nil)
	if _, _, err := svc.DownloadAttachment(context.Background(), "s1", "1"); err == nil {
		t.Fatal("want error for unlinked session")
	}
}

func TestDownloadAttachment_UnconfiguredErrors(t *testing.T) {
	svc := New(fakeSessions{sess: sessionWith("jira:DEMO-1")}, nil, nil, nil)
	if _, _, err := svc.DownloadAttachment(context.Background(), "s1", "1"); !errors.Is(err, jiraadapter.ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
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
	me       jiraadapter.CurrentUser
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

func (f *fakeSearcher) Myself(_ context.Context) (jiraadapter.CurrentUser, error) {
	return f.me, f.err
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

func TestCurrentUser(t *testing.T) {
	s := &fakeSearcher{me: jiraadapter.CurrentUser{AccountID: "acc-42", DisplayName: "Fluke Sattra"}}
	me, err := newSearchSvc(s).CurrentUser(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if me.AccountID != "acc-42" || me.DisplayName != "Fluke Sattra" {
		t.Errorf("CurrentUser = %+v, want acc-42/Fluke Sattra", me)
	}
}

func TestCurrentUser_Unconfigured(t *testing.T) {
	svc := New(fakeSessions{sess: sessionWith("")}, &fakeIssues{}, nil, nil)
	if _, err := svc.CurrentUser(context.Background()); !errors.Is(err, jiraadapter.ErrUnavailable) {
		t.Errorf("err = %v, want ErrUnavailable when searcher is nil", err)
	}
}

func TestBuildJQL_ExactKey(t *testing.T) {
	s := &fakeSearcher{issues: []jiraadapter.IssueSummary{{Key: "DEMO-101"}}}
	if _, err := newSearchSvc(s).Search(context.Background(), SearchParams{Text: "demo-101"}); err != nil {
		t.Fatal(err)
	}
	if s.gotJQL != `key = "DEMO-101"` {
		t.Errorf("jql = %q, want exact-key resolve", s.gotJQL)
	}
}

func TestBuildJQL_TextSearch(t *testing.T) {
	s := &fakeSearcher{}
	if _, err := newSearchSvc(s).Search(context.Background(), SearchParams{Text: "eligible"}); err != nil {
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
	if _, err := newSearchSvc(s).Search(context.Background(), SearchParams{Text: "demo"}); err != nil {
		t.Fatal(err)
	}
	if s.gotJQL != `project = "DEMO" ORDER BY updated DESC` {
		t.Errorf("jql = %q, want project-scoped", s.gotJQL)
	}
}

func TestBuildJQL_BareTokenNotAProjectFallsBackToText(t *testing.T) {
	// No project matches "demo" → do NOT emit `project = "DEMO"` (a 400); text search.
	s := &fakeSearcher{projects: nil}
	if _, err := newSearchSvc(s).Search(context.Background(), SearchParams{Text: "demo"}); err != nil {
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
	if _, err := newSearchSvc(s).Search(context.Background(), SearchParams{Project: "DEMO", Text: "coupon"}); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(s.gotJQL, `project = "DEMO" AND (summary ~ "coupon*"`) {
		t.Errorf("jql = %q, want project-scoped text search", s.gotJQL)
	}
}

// --- search-that-actually-finds-things (verified against real Jira) ---------
//
// The JQL shapes asserted below were each run against the live Finnomena Jira
// (project STAR) before being encoded here; see
// ~/.ao/knowledge/agent-orchestrator/plans/fix-jira-search-partial-match--plan.md
// for the measured row counts behind every choice.

func TestBuildJQL_BareNumberWithProjectResolvesKey(t *testing.T) {
	// A bare number can never match prose — an issue's KEY is not part of its
	// summary text (live: `summary ~ "2271*"` in STAR = 0 rows, while STAR-2271
	// exists). With a project selected the number is unambiguous, so resolve it.
	s := &fakeSearcher{issues: []jiraadapter.IssueSummary{{Key: "STAR-2271"}}}
	if _, err := newSearchSvc(s).Search(context.Background(), SearchParams{Project: "STAR", Text: "2271"}); err != nil {
		t.Fatal(err)
	}
	if s.gotJQL != `key = "STAR-2271"` {
		t.Errorf("jql = %q, want an exact key lookup from project + number", s.gotJQL)
	}
}

func TestBuildJQL_BareNumberWithoutProjectStaysTextSearch(t *testing.T) {
	// No project selected → nothing to build a key from; keep it a text search
	// rather than inventing a project.
	s := &fakeSearcher{}
	if _, err := newSearchSvc(s).Search(context.Background(), SearchParams{Text: "2271"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(s.gotJQL, "key =") {
		t.Errorf("jql = %q, must not guess a key without a project", s.gotJQL)
	}
	if !strings.Contains(s.gotJQL, `summary ~ "2271*"`) {
		t.Errorf("jql = %q, want a text search", s.gotJQL)
	}
}

func TestBuildJQL_HyphenatedTextSplitsIntoTerms(t *testing.T) {
	// The `~` operand goes to Jira's Lucene-style text parser, where `-` means NOT:
	// `summary ~ "e-coupon*"` is live-confirmed 0 rows against STAR even though
	// "App - E-Coupon 3.0 …" exists. Backslash-escaping alone does NOT fix it —
	// a wildcard term bypasses the analyzer, and the index holds `e` + `coupon`,
	// never the single token `e-coupon`. Split into terms, wildcard the last.
	s := &fakeSearcher{}
	if _, err := newSearchSvc(s).Search(context.Background(), SearchParams{Project: "STAR", Text: "e-coupon"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s.gotJQL, `summary ~ "e coupon*"`) {
		t.Errorf("jql = %q, want the hyphen split into ANDed terms with a trailing wildcard", s.gotJQL)
	}
	if strings.Contains(s.gotJQL, "-") {
		t.Errorf("jql = %q, must not leave a bare hyphen in the text operand", s.gotJQL)
	}
}

func TestBuildJQL_OperatorCharactersNeutralised(t *testing.T) {
	// Real titles are full of Lucene operators. Every one of
	// `+ - && || ! ( ) { } [ ] ^ " ~ * ? : \ /` must stop being an operator.
	s := &fakeSearcher{}
	if _, err := newSearchSvc(s).Search(context.Background(), SearchParams{Text: `(E-Coupon) 3.0 ~ "x" +y !z`}); err != nil {
		t.Fatal(err)
	}
	for _, op := range []string{"(", ")", "~ \"x", "+", "!", "-", "?", "^", "[", "]", "{", "}", ":", "/", `\`} {
		operand := s.gotJQL
		if i := strings.Index(operand, `summary ~ "`); i >= 0 {
			operand = operand[i+len(`summary ~ "`):]
			if j := strings.Index(operand, `"`); j >= 0 {
				operand = operand[:j]
			}
		}
		if strings.Contains(operand, op) {
			t.Errorf("operand %q still contains the operator %q", operand, op)
		}
	}
	if !strings.Contains(s.gotJQL, `summary ~ "e coupon 3 0 x y z*"`) {
		t.Errorf("jql = %q, want operators reduced to ANDed terms", s.gotJQL)
	}
}

func TestBuildJQL_UppercaseBooleanWordsAreNotOperators(t *testing.T) {
	// Live: `summary ~ "NOT coupon*"` returns rows that do NOT contain "coupon" —
	// Jira honours the uppercase word as a negation. Lowercasing neutralises it,
	// and costs nothing because text matching is case-insensitive.
	s := &fakeSearcher{}
	if _, err := newSearchSvc(s).Search(context.Background(), SearchParams{Text: "NOT coupon"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s.gotJQL, `summary ~ "not coupon*"`) {
		t.Errorf("jql = %q, want the boolean word lowercased into a plain term", s.gotJQL)
	}
}

func TestBuildJQL_AllOperatorTextDropsTextClause(t *testing.T) {
	// Input with no searchable characters must not emit a bare `~ "*"`.
	s := &fakeSearcher{}
	if _, err := newSearchSvc(s).Search(context.Background(), SearchParams{Project: "STAR", Text: "---"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(s.gotJQL, "summary ~") {
		t.Errorf("jql = %q, want the text clause dropped entirely", s.gotJQL)
	}
	if s.gotJQL != `project = "STAR" ORDER BY updated DESC` {
		t.Errorf("jql = %q, want a plain project scope", s.gotJQL)
	}
}

func TestBuildJQL_QuoteAndBackslashCannotBreakOutOfTheLiteral(t *testing.T) {
	// A quote or backslash in the query must not break out of the JQL string
	// literal. Term splitting is what achieves this today (they are not letters or
	// digits, so they become separators); escapeJQL is the belt-and-braces layer
	// behind it. This asserts the guarantee, not which layer provides it.
	s := &fakeSearcher{}
	if _, err := newSearchSvc(s).Search(context.Background(), SearchParams{Text: `a"b\c`}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(s.gotJQL, `"a"b`) {
		t.Errorf("jql = %q, quote broke out of the literal", s.gotJQL)
	}
	if !strings.Contains(s.gotJQL, `summary ~ "a b c*"`) {
		t.Errorf("jql = %q, want quote/backslash split into terms", s.gotJQL)
	}
}

func TestBuildJQL_AssigneeFilterServerSide(t *testing.T) {
	// The assignee (an accountId) is pushed into the JQL so Jira returns all of that
	// person's issues — not just those in the most-recent page the client can pare.
	s := &fakeSearcher{}
	if _, err := newSearchSvc(s).Search(context.Background(), SearchParams{Project: "STAR", Assignee: "6192fbf4d2e64c00718e026d"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s.gotJQL, `assignee = "6192fbf4d2e64c00718e026d"`) {
		t.Errorf("jql = %q, want a server-side assignee clause", s.gotJQL)
	}
	if !strings.HasPrefix(s.gotJQL, `project = "STAR" AND assignee =`) {
		t.Errorf("jql = %q, want project AND assignee", s.gotJQL)
	}
}

func TestBuildJQL_UnassignedSentinel(t *testing.T) {
	s := &fakeSearcher{}
	if _, err := newSearchSvc(s).Search(context.Background(), SearchParams{Project: "STAR", Assignee: "unassigned"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s.gotJQL, "assignee is EMPTY") {
		t.Errorf("jql = %q, want `assignee is EMPTY` for the unassigned sentinel", s.gotJQL)
	}
	if strings.Contains(s.gotJQL, `assignee = "unassigned"`) {
		t.Errorf("jql = %q, the sentinel must not become a literal assignee", s.gotJQL)
	}
}

func TestBuildJQL_TypeFilterServerSide(t *testing.T) {
	s := &fakeSearcher{}
	if _, err := newSearchSvc(s).Search(context.Background(), SearchParams{Project: "STAR", Types: []string{"Sub-task", "Subtask"}}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s.gotJQL, `issuetype in ("Sub-task", "Subtask")`) {
		t.Errorf("jql = %q, want an issuetype IN clause covering the type variants", s.gotJQL)
	}
}

func TestBuildJQL_AssigneeAndTypeCombine(t *testing.T) {
	s := &fakeSearcher{}
	if _, err := newSearchSvc(s).Search(context.Background(), SearchParams{Project: "STAR", Assignee: "acc-9", Types: []string{"Bug"}}); err != nil {
		t.Fatal(err)
	}
	// project + assignee + issuetype are all ANDed, newest-first.
	if !strings.HasPrefix(s.gotJQL, `project = "STAR" AND assignee = "acc-9" AND issuetype in ("Bug")`) {
		t.Errorf("jql = %q, want project AND assignee AND issuetype", s.gotJQL)
	}
	if !strings.HasSuffix(s.gotJQL, "ORDER BY updated DESC") {
		t.Errorf("jql = %q, want newest-first", s.gotJQL)
	}
}

func TestBuildJQL_EmptyTypesAllTypes(t *testing.T) {
	// "All types" (no names) must not emit an issuetype clause.
	s := &fakeSearcher{}
	if _, err := newSearchSvc(s).Search(context.Background(), SearchParams{Project: "STAR", Types: []string{"", "  "}}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(s.gotJQL, "issuetype") {
		t.Errorf("jql = %q, want no issuetype clause for All types", s.gotJQL)
	}
}

func TestBuildJQL_ExactKeyIgnoresFilters(t *testing.T) {
	// An exact key is an unambiguous lookup — assignee/type must not narrow it.
	s := &fakeSearcher{issues: []jiraadapter.IssueSummary{{Key: "DEMO-101"}}}
	if _, err := newSearchSvc(s).Search(context.Background(), SearchParams{Text: "DEMO-101", Assignee: "acc-9", Types: []string{"Bug"}}); err != nil {
		t.Fatal(err)
	}
	if s.gotJQL != `key = "DEMO-101"` {
		t.Errorf("jql = %q, want a bare exact-key lookup", s.gotJQL)
	}
}

func TestBuildJQL_HideDone(t *testing.T) {
	s := &fakeSearcher{}
	if _, err := newSearchSvc(s).Search(context.Background(), SearchParams{Project: "STAR", HideDone: true}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s.gotJQL, "statusCategory != Done") {
		t.Errorf("jql = %q, want a category-based hide-done clause", s.gotJQL)
	}
	// Robust across custom statuses — never a hardcoded status name.
	if strings.Contains(strings.ToLower(s.gotJQL), `status = "done"`) {
		t.Errorf("jql = %q, must not hardcode a status name", s.gotJQL)
	}
}

func TestBuildJQL_ActiveSprintOnly(t *testing.T) {
	s := &fakeSearcher{}
	if _, err := newSearchSvc(s).Search(context.Background(), SearchParams{Project: "STAR", ActiveSprint: true}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s.gotJQL, "sprint in openSprints()") {
		t.Errorf("jql = %q, want an open-sprints clause", s.gotJQL)
	}
}

func TestBuildJQL_HideDoneAndActiveSprintCombine(t *testing.T) {
	s := &fakeSearcher{}
	if _, err := newSearchSvc(s).Search(context.Background(), SearchParams{
		Project: "STAR", Assignee: "acc-9", HideDone: true, ActiveSprint: true,
	}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`project = "STAR"`, `assignee = "acc-9"`, "statusCategory != Done", "sprint in openSprints()"} {
		if !strings.Contains(s.gotJQL, want) {
			t.Errorf("jql = %q, missing %q", s.gotJQL, want)
		}
	}
	if !strings.HasSuffix(s.gotJQL, "ORDER BY updated DESC") {
		t.Errorf("jql = %q, want newest-first", s.gotJQL)
	}
}

func TestBuildJQL_AdvancedJQLReplacesEverything(t *testing.T) {
	// Advanced JQL drives the search verbatim; the structured fields are ignored.
	s := &fakeSearcher{}
	raw := `project = STAR AND labels = urgent ORDER BY created ASC`
	if _, err := newSearchSvc(s).Search(context.Background(), SearchParams{
		Project: "DEMO", Text: "ignored", Assignee: "acc-9", Types: []string{"Bug"}, HideDone: true, JQL: raw,
	}); err != nil {
		t.Fatal(err)
	}
	if s.gotJQL != raw {
		t.Errorf("jql = %q, want the raw advanced JQL verbatim", s.gotJQL)
	}
	if strings.Contains(s.gotJQL, "DEMO") || strings.Contains(s.gotJQL, "assignee") || strings.Contains(s.gotJQL, "statusCategory") {
		t.Errorf("jql = %q, structured fields must not leak into advanced mode", s.gotJQL)
	}
}

func TestBuildJQL_BlankAdvancedJQLFallsBackToStructured(t *testing.T) {
	// Whitespace-only advanced JQL is not "advanced" — use the structured query.
	s := &fakeSearcher{}
	if _, err := newSearchSvc(s).Search(context.Background(), SearchParams{Project: "STAR", JQL: "   "}); err != nil {
		t.Fatal(err)
	}
	if s.gotJQL != `project = "STAR" ORDER BY updated DESC` {
		t.Errorf("jql = %q, want the structured project scope", s.gotJQL)
	}
}

func TestSearch_NilSearcher(t *testing.T) {
	svc := New(fakeSessions{}, &fakeIssues{}, nil, nil)
	if _, err := svc.Search(context.Background(), SearchParams{Text: "x"}); !errors.Is(err, jiraadapter.ErrUnavailable) {
		t.Errorf("err = %v, want ErrUnavailable", err)
	}
}

func TestGetIssue_ValidatesKeyThenReads(t *testing.T) {
	issues := &fakeIssues{iss: jiraadapter.Issue{Key: "DEMO-101", Title: "hi"}}
	svc := New(fakeSessions{}, issues, nil, nil)

	// Malformed key never hits the adapter.
	if _, err := svc.GetIssue(context.Background(), "not a key"); !errors.Is(err, jiraadapter.ErrBadKey) {
		t.Errorf("err = %v, want ErrBadKey", err)
	}
	// A valid key (case-normalized) reads via the adapter.
	iss, err := svc.GetIssue(context.Background(), "demo-101")
	if err != nil {
		t.Fatal(err)
	}
	if iss.Key != "DEMO-101" || issues.got != "DEMO-101" {
		t.Errorf("got issue %+v, adapter key %q", iss, issues.got)
	}
}

func TestMoveIssue_MovesByKeyAndReReadsStatus(t *testing.T) {
	mover := &fakeMover{}
	issues := &fakeIssues{iss: jiraadapter.Issue{Key: "DEMO-101", Status: "In Progress", StatusCategory: "indeterminate"}}
	svc := New(fakeSessions{}, issues, mover, nil)

	res, err := svc.MoveIssue(context.Background(), "DEMO-101", "31")
	if err != nil {
		t.Fatal(err)
	}
	if mover.gotKey != "DEMO-101" || mover.gotID != "31" {
		t.Errorf("mover got key=%q id=%q", mover.gotKey, mover.gotID)
	}
	// Best-effort re-read carries the new status back (no session/tree scope).
	if res.Key != "DEMO-101" || res.Status != "In Progress" || res.StatusCategory != "indeterminate" {
		t.Errorf("move result = %+v", res)
	}
}

func TestIssueTransitions_ValidatesKey(t *testing.T) {
	svc := New(fakeSessions{}, &fakeIssues{}, &fakeMover{}, nil)
	if _, err := svc.IssueTransitions(context.Background(), "nope"); !errors.Is(err, jiraadapter.ErrBadKey) {
		t.Errorf("err = %v, want ErrBadKey", err)
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

// Unlink must genuinely CLEAR issue_id. It previously wrote the session's display
// name into issue_id, so a session that reported {"linked":false} still carried
// "My label" as its issue id - data corruption that survived because the old test
// asserted exactly that. The display name is preserved so the card still reads well.
func TestUnlink_Success(t *testing.T) {
	rec := &bindRec{}
	sess := domain.Session{SessionRecord: domain.SessionRecord{ID: "s1", IssueID: "jira:DEMO-2272", DisplayName: "My label"}}
	svc := New(fakeSessions{sess: sess, rec: rec}, &fakeIssues{}, nil, &fakeSearcher{})
	got, err := svc.Unlink(context.Background(), "s1")
	if err != nil {
		t.Fatal(err)
	}
	if !rec.called {
		t.Fatal("unlink must write the cleared binding")
	}
	if rec.issueID != "" {
		t.Errorf("issue_id written = %q, want %q (unlink must clear it, never store a label)", rec.issueID, "")
	}
	if rec.display != "My label" {
		t.Errorf("display_name written = %q, want %q (unlink must preserve the label)", rec.display, "My label")
	}
	if got.IssueID != "" {
		t.Errorf("returned session IssueID = %q, want empty", got.IssueID)
	}
}

// A session with no display name still must not get a label parked in issue_id:
// the key it was unlinked from becomes the display name, and issue_id clears.
func TestUnlink_NoDisplayNameFallsBackToKeyAsLabelOnly(t *testing.T) {
	rec := &bindRec{}
	sess := domain.Session{SessionRecord: domain.SessionRecord{ID: "s1", IssueID: "jira:DEMO-2272", DisplayName: "  "}}
	svc := New(fakeSessions{sess: sess, rec: rec}, &fakeIssues{}, nil, &fakeSearcher{})
	if _, err := svc.Unlink(context.Background(), "s1"); err != nil {
		t.Fatal(err)
	}
	if rec.issueID != "" {
		t.Errorf("issue_id written = %q, want empty", rec.issueID)
	}
	if rec.display != "DEMO-2272" {
		t.Errorf("display_name written = %q, want the unlinked key as a readable fallback", rec.display)
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
