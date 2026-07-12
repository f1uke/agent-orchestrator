package controllers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	jiraadapter "github.com/aoagents/agent-orchestrator/backend/internal/adapters/jira"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/jira/adf"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	jirasvc "github.com/aoagents/agent-orchestrator/backend/internal/service/jira"
)

type stubJira struct {
	res jirasvc.Result
	err error

	transitions []jiraadapter.Transition
	transErr    error
	gotTransKey string
	moveRes     jirasvc.MoveResult
	moveErr     error
	gotMoveKey  string
	gotMoveID   string

	searchRes  []jiraadapter.IssueSummary
	searchErr  error
	gotProject string
	gotText    string
	projectRes []jiraadapter.ProjectRef
	projectErr error
	bindRes    jiraadapter.IssueSummary
	bindErr    error
	gotBindKey string
	unlinkErr  error
}

func (s *stubJira) Context(context.Context, domain.SessionID) (jirasvc.Result, error) {
	return s.res, s.err
}

func (s *stubJira) Transitions(_ context.Context, _ domain.SessionID, key string) ([]jiraadapter.Transition, error) {
	s.gotTransKey = key
	return s.transitions, s.transErr
}

func (s *stubJira) Move(_ context.Context, _ domain.SessionID, key, transitionID string) (jirasvc.MoveResult, error) {
	s.gotMoveKey = key
	s.gotMoveID = transitionID
	return s.moveRes, s.moveErr
}

func (s *stubJira) Search(_ context.Context, project, text string) ([]jiraadapter.IssueSummary, error) {
	s.gotProject, s.gotText = project, text
	return s.searchRes, s.searchErr
}

func (s *stubJira) Projects(context.Context, string) ([]jiraadapter.ProjectRef, error) {
	return s.projectRes, s.projectErr
}

func (s *stubJira) SetBinding(_ context.Context, _ domain.SessionID, key string) (jiraadapter.IssueSummary, error) {
	s.gotBindKey = key
	return s.bindRes, s.bindErr
}

func (s *stubJira) Unlink(context.Context, domain.SessionID) (domain.Session, error) {
	return domain.Session{}, s.unlinkErr
}

func serveJira(t *testing.T, svc JiraService) *httptest.ResponseRecorder {
	t.Helper()
	return serveJiraReq(t, svc, http.MethodGet, "/sessions/s1/jira", nil)
}

func serveJiraReq(t *testing.T, svc JiraService, method, path string, body io.Reader) *httptest.ResponseRecorder {
	t.Helper()
	c := &JiraController{Svc: svc}
	r := chi.NewRouter()
	c.Register(r)
	req := httptest.NewRequest(method, path, body)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestJiraGet_LinkedIssue(t *testing.T) {
	iss := jiraadapter.Issue{
		Key: "DEMO-101", URL: "https://x.atlassian.net/browse/DEMO-101",
		Type: "Story", Title: "Example issue summary",
		Status: "Ready for QA", StatusCategory: "new", StatusColor: "blue-gray",
		Priority: "Medium", Assignee: "Alex", Reporter: "Sam",
		Sprint:      &jiraadapter.Sprint{Name: "Sprint 2026-14", State: "active"},
		Description: []adf.Node{{Type: "paragraph", Content: []adf.Node{{Type: "text", Text: "hi"}}}},
		Subtasks:    []jiraadapter.Subtask{{Key: "DEMO-102", Type: "Sub-task", Status: "Pull Request"}},
	}
	rec := serveJira(t, &stubJira{res: jirasvc.Result{Linked: true, Issue: &iss}})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var body JiraContextResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !body.Linked || body.Issue == nil {
		t.Fatalf("body = %+v", body)
	}
	if body.Issue.Key != "DEMO-101" || body.Issue.StatusCategory != "new" || body.Issue.Sprint == nil {
		t.Errorf("issue mapped wrong: %+v", body.Issue)
	}
	if len(body.Issue.Description) == 0 || len(body.Issue.Subtasks) != 1 {
		t.Errorf("description/subtasks not mapped: %+v", body.Issue)
	}
	if body.SessionID != "s1" {
		t.Errorf("sessionId = %q", body.SessionID)
	}
}

func TestJiraGet_NotLinked(t *testing.T) {
	rec := serveJira(t, &stubJira{res: jirasvc.Result{Linked: false}})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var body JiraContextResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body.Linked || body.Issue != nil {
		t.Errorf("expected unlinked empty, got %+v", body)
	}
}

func TestJiraGet_FetchErrorIs200(t *testing.T) {
	rec := serveJira(t, &stubJira{res: jirasvc.Result{Linked: true, FetchError: "Couldn't reach Jira."}})
	if rec.Code != http.StatusOK {
		t.Fatalf("a Jira fetch failure must still be 200, got %d", rec.Code)
	}
	var body JiraContextResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if !body.Linked || body.Issue != nil || body.FetchError == "" {
		t.Errorf("expected linked with fetchError, got %+v", body)
	}
}

func TestJiraGet_SessionNotFoundIs404(t *testing.T) {
	rec := serveJira(t, &stubJira{err: apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestJiraGet_NilServiceNotImplemented(t *testing.T) {
	rec := serveJira(t, nil)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", rec.Code)
	}
}

func getTransitions(t *testing.T, svc JiraService) *httptest.ResponseRecorder {
	t.Helper()
	return serveJiraReq(t, svc, http.MethodGet, "/sessions/s1/jira/transitions", nil)
}

func postMove(t *testing.T, svc JiraService, body string) *httptest.ResponseRecorder {
	t.Helper()
	return serveJiraReq(t, svc, http.MethodPost, "/sessions/s1/jira/move", strings.NewReader(body))
}

func TestJiraTransitions_ListsLive(t *testing.T) {
	rec := getTransitions(t, &stubJira{transitions: []jiraadapter.Transition{
		{ID: "11", Name: "Start Testing", To: "In Progress", ToCategory: "indeterminate"},
		{ID: "21", Name: "Abandoned", To: "Abandoned", ToCategory: "done"},
	}})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var body JiraTransitionsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.SessionID != "s1" || len(body.Transitions) != 2 {
		t.Fatalf("body = %+v", body)
	}
	if body.Transitions[0].ID != "11" || body.Transitions[0].Name != "Start Testing" || body.Transitions[0].To != "In Progress" {
		t.Errorf("transition mapped wrong: %+v", body.Transitions[0])
	}
}

func TestJiraTransitions_NotLinkedIs400(t *testing.T) {
	rec := getTransitions(t, &stubJira{transErr: jirasvc.ErrNotLinked})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestJiraTransitions_NilServiceNotImplemented(t *testing.T) {
	rec := getTransitions(t, nil)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", rec.Code)
	}
}

func TestJiraMove_AppliesAndReturnsNewStatus(t *testing.T) {
	rec := postMove(t, &stubJira{moveRes: jirasvc.MoveResult{Key: "DEMO-101", Status: "In Progress", StatusCategory: "indeterminate"}}, `{"transitionId":"11"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var body JiraMoveResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Key != "DEMO-101" || body.Status != "In Progress" || body.StatusCategory != "indeterminate" {
		t.Errorf("move response = %+v", body)
	}
}

func TestJiraMove_MissingTransitionIs400(t *testing.T) {
	rec := postMove(t, &stubJira{}, `{"transitionId":""}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestJiraMove_MalformedBodyIs400(t *testing.T) {
	rec := postMove(t, &stubJira{}, `not json`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// The ?key= query on transitions and the issueKey body field on move must reach
// the service so a subtask can be targeted.
func TestJiraTransitions_PassesSubtaskKey(t *testing.T) {
	stub := &stubJira{}
	serveJiraReq(t, stub, http.MethodGet, "/sessions/s1/jira/transitions?key=DEMO-102", nil)
	if stub.gotTransKey != "DEMO-102" {
		t.Errorf("transitions got key %q, want DEMO-102", stub.gotTransKey)
	}
}

func TestJiraMove_PassesSubtaskKey(t *testing.T) {
	stub := &stubJira{moveRes: jirasvc.MoveResult{Key: "DEMO-102"}}
	postMove(t, stub, `{"transitionId":"21","issueKey":"DEMO-102"}`)
	if stub.gotMoveKey != "DEMO-102" || stub.gotMoveID != "21" {
		t.Errorf("move got key=%q id=%q, want DEMO-102/21", stub.gotMoveKey, stub.gotMoveID)
	}
}

func TestJiraMove_KeyNotInTreeIs400(t *testing.T) {
	rec := postMove(t, &stubJira{moveErr: jirasvc.ErrKeyNotInIssueTree}, `{"transitionId":"21","issueKey":"OTHER-9"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestJiraMove_RejectionIs400(t *testing.T) {
	rec := postMove(t, &stubJira{moveErr: jiraadapter.ErrBadTransition}, `{"transitionId":"99"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("a workflow rejection must be 4xx, got %d", rec.Code)
	}
}

func TestJiraMove_NilServiceNotImplemented(t *testing.T) {
	rec := postMove(t, nil, `{"transitionId":"11"}`)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", rec.Code)
	}
}

// ---- search / projects / link / unlink ----

func TestJiraSearch_ReturnsRowsAndPassesQuery(t *testing.T) {
	stub := &stubJira{searchRes: []jiraadapter.IssueSummary{
		{Key: "DEMO-2272", Type: "Story", Title: "Example issue summary", Status: "Ready for QA", StatusCategory: "new"},
	}}
	rec := serveJiraReq(t, stub, http.MethodGet, "/jira/search?q=eligible&project=DEMO", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if stub.gotText != "eligible" || stub.gotProject != "DEMO" {
		t.Errorf("service got text=%q project=%q", stub.gotText, stub.gotProject)
	}
	var body JiraSearchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Issues) != 1 || body.Issues[0].Key != "DEMO-2272" || body.Issues[0].Type != "Story" {
		t.Errorf("issues = %+v", body.Issues)
	}
}

func TestJiraSearch_BadQueryIs400(t *testing.T) {
	rec := serveJiraReq(t, &stubJira{searchErr: jiraadapter.ErrBadQuery}, http.MethodGet, "/jira/search?q=%22", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestJiraSearch_NilServiceNotImplemented(t *testing.T) {
	rec := serveJiraReq(t, nil, http.MethodGet, "/jira/search?q=x", nil)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", rec.Code)
	}
}

func TestJiraProjects_ReturnsList(t *testing.T) {
	rec := serveJiraReq(t, &stubJira{projectRes: []jiraadapter.ProjectRef{{Key: "DEMO", Name: "DEMO project"}}}, http.MethodGet, "/jira/projects?q=demo", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var body JiraProjectsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Projects) != 1 || body.Projects[0].Key != "DEMO" || body.Projects[0].Name != "DEMO project" {
		t.Errorf("projects = %+v", body.Projects)
	}
}

func putLink(t *testing.T, svc JiraService, body string) *httptest.ResponseRecorder {
	t.Helper()
	return serveJiraReq(t, svc, http.MethodPut, "/sessions/s1/jira", strings.NewReader(body))
}

func TestJiraLink_BindsAndReturnsIssue(t *testing.T) {
	stub := &stubJira{bindRes: jiraadapter.IssueSummary{Key: "DEMO-2272", Title: "Example issue summary", Status: "Ready for QA", StatusCategory: "new"}}
	rec := putLink(t, stub, `{"issueKey":"DEMO-2272"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if stub.gotBindKey != "DEMO-2272" {
		t.Errorf("service got key %q", stub.gotBindKey)
	}
	var body JiraLinkResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !body.Linked || body.Issue == nil || body.Issue.Key != "DEMO-2272" || body.SessionID != "s1" {
		t.Errorf("link response = %+v", body)
	}
}

func TestJiraLink_MissingKeyIs400(t *testing.T) {
	rec := putLink(t, &stubJira{}, `{"issueKey":"  "}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestJiraLink_MalformedBodyIs400(t *testing.T) {
	rec := putLink(t, &stubJira{}, `nope`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestJiraLink_UnknownKeyIs404(t *testing.T) {
	rec := putLink(t, &stubJira{bindErr: jiraadapter.ErrNotFound}, `{"issueKey":"DEMO-9"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestJiraLink_NilServiceNotImplemented(t *testing.T) {
	rec := putLink(t, nil, `{"issueKey":"DEMO-1"}`)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", rec.Code)
	}
}

func TestJiraUnlink_OK(t *testing.T) {
	rec := serveJiraReq(t, &stubJira{}, http.MethodDelete, "/sessions/s1/jira", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var body JiraLinkResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Linked || body.SessionID != "s1" {
		t.Errorf("unlink response = %+v, want linked=false", body)
	}
}

func TestJiraUnlink_NotLinkedIs400(t *testing.T) {
	rec := serveJiraReq(t, &stubJira{unlinkErr: jirasvc.ErrNotLinked}, http.MethodDelete, "/sessions/s1/jira", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestJiraUnlink_NilServiceNotImplemented(t *testing.T) {
	rec := serveJiraReq(t, nil, http.MethodDelete, "/sessions/s1/jira", nil)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", rec.Code)
	}
}
