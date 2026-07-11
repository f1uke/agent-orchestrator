package jira

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSearchIssues_ParsesRows(t *testing.T) {
	var gotPath, gotJQL, gotFields string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotJQL = r.URL.Query().Get("jql")
		gotFields = r.URL.Query().Get("fields")
		_, _ = io.WriteString(w, `{"issues":[
			{"key":"DEMO-101","fields":{"summary":"Example issue summary","issuetype":{"name":"Story"},
				"status":{"name":"Ready for QA","statusCategory":{"key":"new","colorName":"blue-gray"}},
				"assignee":{"displayName":"Alex Rivera"}}},
			{"key":"DEMO-88","fields":{"summary":"Coupon empty state","issuetype":{"name":"Bug"},
				"status":{"name":"In Progress","statusCategory":{"key":"indeterminate","colorName":"yellow"}},
				"assignee":{"displayName":""}}}
		]}`)
	}))
	defer srv.Close()

	c := NewClient(WithHTTPDoer(srv.Client().Do), WithConfigSource(staticConfig(srv.URL)))
	out, err := c.SearchIssues(context.Background(), `project = "DEMO" ORDER BY updated DESC`, 25)
	if err != nil {
		t.Fatalf("SearchIssues: %v", err)
	}
	if gotPath != "/rest/api/3/search/jql" {
		t.Errorf("path = %q, want the enhanced search endpoint", gotPath)
	}
	if !strings.Contains(gotJQL, "DEMO") {
		t.Errorf("jql = %q", gotJQL)
	}
	if gotFields != searchFields {
		t.Errorf("fields = %q, want %q", gotFields, searchFields)
	}
	if len(out) != 2 {
		t.Fatalf("rows = %+v", out)
	}
	if out[0].Key != "DEMO-101" || out[0].Type != "Story" || out[0].Title != "Example issue summary" ||
		out[0].Status != "Ready for QA" || out[0].StatusCategory != "new" || out[0].Assignee != "Alex Rivera" {
		t.Errorf("row[0] = %+v", out[0])
	}
	if out[0].URL != srv.URL+"/browse/DEMO-101" {
		t.Errorf("row[0].URL = %q", out[0].URL)
	}
}

func TestSearchIssues_FallsBackToClassicEndpoint(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.URL.Path == "/rest/api/3/search/jql" {
			// Enhanced endpoint absent on this (older) instance.
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = io.WriteString(w, `{"issues":[{"key":"DEMO-1","fields":{"summary":"x","issuetype":{"name":"Task"},"status":{"name":"To Do","statusCategory":{"key":"new"}}}}]}`)
	}))
	defer srv.Close()

	c := NewClient(WithHTTPDoer(srv.Client().Do), WithConfigSource(staticConfig(srv.URL)))
	out, err := c.SearchIssues(context.Background(), "text ~ \"x\"", 10)
	if err != nil {
		t.Fatalf("SearchIssues: %v", err)
	}
	if len(paths) != 2 || paths[0] != "/rest/api/3/search/jql" || paths[1] != "/rest/api/3/search" {
		t.Errorf("paths = %v, want enhanced then classic", paths)
	}
	if len(out) != 1 || out[0].Key != "DEMO-1" {
		t.Errorf("rows = %+v", out)
	}
}

func TestSearchIssues_BadQueryMapsToErrBadQuery(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"errorMessages":["The value 'NOPE' does not exist for the field 'project'."]}`)
	}))
	defer srv.Close()

	c := NewClient(WithHTTPDoer(srv.Client().Do), WithConfigSource(staticConfig(srv.URL)))
	_, err := c.SearchIssues(context.Background(), `project = "NOPE"`, 25)
	if !errors.Is(err, ErrBadQuery) {
		t.Errorf("err = %v, want ErrBadQuery", err)
	}
	if err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("err should surface the Jira message, got %v", err)
	}
}

func TestSearchIssues_EmptyQueryNeverCallsHTTP(t *testing.T) {
	c := NewClient(WithHTTPDoer(func(*http.Request) (*http.Response, error) {
		t.Fatal("HTTP must not be called for an empty query")
		return nil, nil
	}), WithConfigSource(staticConfig("http://x")))
	if _, err := c.SearchIssues(context.Background(), "   ", 25); !errors.Is(err, ErrBadQuery) {
		t.Errorf("err = %v, want ErrBadQuery", err)
	}
}

func TestSearchIssues_AuthFailureSurfaces(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"errorMessages":["Client must be authenticated"]}`)
	}))
	defer srv.Close()
	c := NewClient(WithHTTPDoer(srv.Client().Do), WithConfigSource(staticConfig(srv.URL)))
	if _, err := c.SearchIssues(context.Background(), "text ~ \"x\"", 25); !errors.Is(err, ErrAuthFailed) {
		t.Errorf("err = %v, want ErrAuthFailed", err)
	}
}

func TestListProjects_ParsesAndFilters(t *testing.T) {
	var gotQuery, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.Query().Get("query")
		_, _ = io.WriteString(w, `{"values":[
			{"key":"DEMO","name":"DEMO project"},
			{"key":"ACME","name":"[Squad] Acme"}
		]}`)
	}))
	defer srv.Close()

	c := NewClient(WithHTTPDoer(srv.Client().Do), WithConfigSource(staticConfig(srv.URL)))
	out, err := c.ListProjects(context.Background(), "dem")
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if gotPath != "/rest/api/3/project/search" || gotQuery != "dem" {
		t.Errorf("path=%q query=%q", gotPath, gotQuery)
	}
	if len(out) != 2 || out[0].Key != "DEMO" || out[0].Name != "DEMO project" {
		t.Errorf("projects = %+v", out)
	}
}
