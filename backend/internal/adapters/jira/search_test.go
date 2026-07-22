package jira

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// issuesJSON builds a search-response body: one minimal issue per key, plus an
// optional nextPageToken (empty = last page) so paging tests can chain pages.
func issuesJSON(keys []string, nextToken string) string {
	rows := make([]string, 0, len(keys))
	for _, k := range keys {
		rows = append(rows, fmt.Sprintf(
			`{"key":%q,"fields":{"summary":"row %s","issuetype":{"name":"Task"},"status":{"name":"To Do","statusCategory":{"key":"new"}}}}`,
			k, k))
	}
	body := `{"issues":[` + strings.Join(rows, ",") + `]`
	if nextToken != "" {
		body += `,"nextPageToken":` + strconv.Quote(nextToken)
	}
	return body + `}`
}

// keysN makes n synthetic issue keys (DEMO-<i>) for filling a full page.
func keysN(prefix string, n int) []string {
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, fmt.Sprintf("%s-%d", prefix, i))
	}
	return out
}

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
			{"key":"DEMO-88","fields":{"summary":"Item empty state","issuetype":{"name":"Bug"},
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

func TestSearchIssues_DecodesSprintPerRow(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// A sprint custom-field (id varies per instance) alongside the known fields;
		// detectSprint should pick the active sprint. The second row has none.
		_, _ = io.WriteString(w, `{"issues":[
			{"key":"DEMO-101","fields":{"summary":"Has a sprint","issuetype":{"name":"Story"},
				"status":{"name":"To Do","statusCategory":{"key":"new"}},
				"assignee":{"displayName":"Alex Rivera"},
				"customfield_10020":[{"name":"Sprint 2026-14","state":"active","boardId":5,"startDate":"2026-06-29T00:00:00Z","endDate":"2026-07-10T00:00:00Z"}]}},
			{"key":"DEMO-88","fields":{"summary":"No sprint","issuetype":{"name":"Bug"},
				"status":{"name":"Backlog","statusCategory":{"key":"new"}},
				"assignee":{"displayName":"Sam Chen"}}}
		]}`)
	}))
	defer srv.Close()

	c := NewClient(WithHTTPDoer(srv.Client().Do), WithConfigSource(staticConfig(srv.URL)))
	out, err := c.SearchIssues(context.Background(), `project = "DEMO"`, 25)
	if err != nil {
		t.Fatalf("SearchIssues: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("rows = %+v", out)
	}
	if out[0].Sprint == nil {
		t.Fatalf("row[0] should carry a sprint")
	}
	if out[0].Sprint.Name != "Sprint 2026-14" || out[0].Sprint.State != "active" {
		t.Errorf("row[0].Sprint = %+v", out[0].Sprint)
	}
	// The known fields still decode correctly under the map-based path.
	if out[0].Title != "Has a sprint" || out[0].Type != "Story" || out[0].Status != "To Do" ||
		out[0].Assignee != "Alex Rivera" {
		t.Errorf("row[0] = %+v", out[0])
	}
	if out[1].Sprint != nil {
		t.Errorf("row[1] has no sprint, got %+v", out[1].Sprint)
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

func TestSearchIssues_DecodesAssigneeAccountId(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"issues":[
			{"key":"DEMO-101","fields":{"summary":"x","issuetype":{"name":"Story"},
				"status":{"name":"To Do","statusCategory":{"key":"new"}},
				"assignee":{"displayName":"Alex Rivera","accountId":"acc-alex"}}}
		]}`)
	}))
	defer srv.Close()

	c := NewClient(WithHTTPDoer(srv.Client().Do), WithConfigSource(staticConfig(srv.URL)))
	out, err := c.SearchIssues(context.Background(), `project = "DEMO"`, 25)
	if err != nil {
		t.Fatalf("SearchIssues: %v", err)
	}
	if len(out) != 1 || out[0].Assignee != "Alex Rivera" || out[0].AssigneeAccountId != "acc-alex" {
		t.Errorf("row = %+v, want assignee Alex Rivera / accountId acc-alex", out[0])
	}
}

func TestSearchIssues_DecodesParent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"issues":[
			{"key":"DEMO-102","fields":{"summary":"a subtask","issuetype":{"name":"Sub-task"},
				"status":{"name":"To Do","statusCategory":{"key":"new"}},
				"parent":{"key":"DEMO-101","fields":{"summary":"the parent story"}}}},
			{"key":"DEMO-101","fields":{"summary":"the parent story","issuetype":{"name":"Story"},
				"status":{"name":"To Do","statusCategory":{"key":"new"}}}}
		]}`)
	}))
	defer srv.Close()

	c := NewClient(WithHTTPDoer(srv.Client().Do), WithConfigSource(staticConfig(srv.URL)))
	out, err := c.SearchIssues(context.Background(), `project = "DEMO"`, 25)
	if err != nil {
		t.Fatalf("SearchIssues: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("rows = %+v", out)
	}
	if out[0].Parent == nil || out[0].Parent.Key != "DEMO-101" || out[0].Parent.Title != "the parent story" {
		t.Errorf("row[0].Parent = %+v, want DEMO-101 / the parent story", out[0].Parent)
	}
	if out[1].Parent != nil {
		t.Errorf("row[1] (a story) should have no parent, got %+v", out[1].Parent)
	}
}

func TestSearchIssues_PaginatesEnhancedByToken(t *testing.T) {
	var tokens []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok := r.URL.Query().Get("nextPageToken")
		tokens = append(tokens, tok)
		switch tok {
		case "":
			// First page: a short page that STILL carries a token — the loop must
			// follow the token, not stop at the short page.
			_, _ = io.WriteString(w, issuesJSON([]string{"DEMO-1", "DEMO-2"}, "TOK2"))
		case "TOK2":
			_, _ = io.WriteString(w, issuesJSON([]string{"DEMO-3"}, "")) // last page: no token
		default:
			t.Errorf("unexpected nextPageToken %q", tok)
		}
	}))
	defer srv.Close()

	c := NewClient(WithHTTPDoer(srv.Client().Do), WithConfigSource(staticConfig(srv.URL)))
	out, err := c.SearchIssues(context.Background(), `project = "DEMO"`, SearchMaxResults)
	if err != nil {
		t.Fatalf("SearchIssues: %v", err)
	}
	if len(out) != 3 || out[0].Key != "DEMO-1" || out[2].Key != "DEMO-3" {
		t.Fatalf("rows = %+v, want 3 across two pages", out)
	}
	if len(tokens) != 2 || tokens[0] != "" || tokens[1] != "TOK2" {
		t.Errorf("tokens = %v, want [\"\", TOK2]", tokens)
	}
}

func TestSearchIssues_PaginatesClassicByStartAt(t *testing.T) {
	var starts []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/rest/api/3/search/jql" {
			w.WriteHeader(http.StatusNotFound) // force the classic (startAt) path
			return
		}
		starts = append(starts, r.URL.Query().Get("startAt"))
		if r.URL.Query().Get("startAt") == "0" {
			_, _ = io.WriteString(w, issuesJSON(keysN("DEMO", searchPageSize), "")) // a FULL page → keep going
			return
		}
		_, _ = io.WriteString(w, issuesJSON([]string{"DEMO-last"}, "")) // short page → stop
	}))
	defer srv.Close()

	c := NewClient(WithHTTPDoer(srv.Client().Do), WithConfigSource(staticConfig(srv.URL)))
	out, err := c.SearchIssues(context.Background(), `project = "DEMO"`, SearchMaxResults)
	if err != nil {
		t.Fatalf("SearchIssues: %v", err)
	}
	if len(out) != searchPageSize+1 {
		t.Fatalf("rows = %d, want %d across two pages", len(out), searchPageSize+1)
	}
	// startAt advanced by the first page's length.
	if len(starts) != 2 || starts[0] != "0" || starts[1] != strconv.Itoa(searchPageSize) {
		t.Errorf("startAt sequence = %v, want [0 %d]", starts, searchPageSize)
	}
}

func TestSearchIssues_CapsAtMaxResults(t *testing.T) {
	var requests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		// Always a full page WITH a token — an unbounded result set. The cap must
		// stop paging so this can't loop forever.
		_, _ = io.WriteString(w, issuesJSON(keysN("DEMO", searchPageSize), "MORE"))
	}))
	defer srv.Close()

	c := NewClient(WithHTTPDoer(srv.Client().Do), WithConfigSource(staticConfig(srv.URL)))
	out, err := c.SearchIssues(context.Background(), `project = "DEMO"`, SearchMaxResults)
	if err != nil {
		t.Fatalf("SearchIssues: %v", err)
	}
	if len(out) != SearchMaxResults {
		t.Errorf("rows = %d, want the cap %d", len(out), SearchMaxResults)
	}
	if requests != SearchMaxResults/searchPageSize {
		t.Errorf("requests = %d, want %d (cap / page size)", requests, SearchMaxResults/searchPageSize)
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
