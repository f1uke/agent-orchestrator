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
	moveRes     jirasvc.MoveResult
	moveErr     error
}

func (s stubJira) Context(context.Context, domain.SessionID) (jirasvc.Result, error) {
	return s.res, s.err
}

func (s stubJira) Transitions(context.Context, domain.SessionID) ([]jiraadapter.Transition, error) {
	return s.transitions, s.transErr
}

func (s stubJira) Move(context.Context, domain.SessionID, string) (jirasvc.MoveResult, error) {
	return s.moveRes, s.moveErr
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
		Type: "Story", Title: "Order Eligible UI",
		Status: "Ready for QA", StatusCategory: "new", StatusColor: "blue-gray",
		Priority: "Medium", Assignee: "Alex", Reporter: "Sam",
		Sprint:      &jiraadapter.Sprint{Name: "Sprint 2026-14", State: "active"},
		Description: []adf.Node{{Type: "paragraph", Content: []adf.Node{{Type: "text", Text: "hi"}}}},
		Subtasks:    []jiraadapter.Subtask{{Key: "DEMO-102", Type: "Sub-task", Status: "Pull Request"}},
	}
	rec := serveJira(t, stubJira{res: jirasvc.Result{Linked: true, Issue: &iss}})
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
	rec := serveJira(t, stubJira{res: jirasvc.Result{Linked: false}})
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
	rec := serveJira(t, stubJira{res: jirasvc.Result{Linked: true, FetchError: "Couldn't reach Jira."}})
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
	rec := serveJira(t, stubJira{err: apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")})
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
	rec := getTransitions(t, stubJira{transitions: []jiraadapter.Transition{
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
	rec := getTransitions(t, stubJira{transErr: jirasvc.ErrNotLinked})
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
	rec := postMove(t, stubJira{moveRes: jirasvc.MoveResult{Key: "DEMO-101", Status: "In Progress", StatusCategory: "indeterminate"}}, `{"transitionId":"11"}`)
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
	rec := postMove(t, stubJira{}, `{"transitionId":""}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestJiraMove_MalformedBodyIs400(t *testing.T) {
	rec := postMove(t, stubJira{}, `not json`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestJiraMove_RejectionIs400(t *testing.T) {
	rec := postMove(t, stubJira{moveErr: jiraadapter.ErrBadTransition}, `{"transitionId":"99"}`)
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
