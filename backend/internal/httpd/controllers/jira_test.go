package controllers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
}

func (s stubJira) Context(context.Context, domain.SessionID) (jirasvc.Result, error) {
	return s.res, s.err
}

func serveJira(t *testing.T, svc JiraService) *httptest.ResponseRecorder {
	t.Helper()
	c := &JiraController{Svc: svc}
	r := chi.NewRouter()
	c.Register(r)
	req := httptest.NewRequest(http.MethodGet, "/sessions/s1/jira", nil)
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
