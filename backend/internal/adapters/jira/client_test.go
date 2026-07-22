package jira

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
)

// restClient wires a Client to an httptest server + a static REST identity so the
// display Get() runs entirely in-process (no jira binary, no network, no keychain).
func restClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return NewClient(
		WithHTTPDoer(srv.Client().Do),
		WithConfigSource(func() (restConfig, error) {
			return restConfig{baseURL: srv.URL, email: "e@example.com", token: "tok"}, nil
		}),
	)
}

// issueFixtureHandler serves testdata/<lowercased-key>.json for a
// GET /rest/api/3/issue/{key} request; an unknown key 404s like Jira.
func issueFixtureHandler(t *testing.T) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		key := path.Base(r.URL.Path)
		b, err := os.ReadFile(filepath.Join("testdata", toLowerKey(key)+".json"))
		if err != nil {
			http.Error(w, `{"errorMessages":["Issue does not exist"]}`, http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(b)
	}
}

func toLowerKey(key string) string {
	out := make([]byte, len(key))
	for i := 0; i < len(key); i++ {
		c := key[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		out[i] = c
	}
	return string(out)
}

func TestGet_MapsStructuredFields(t *testing.T) {
	c := restClient(t, issueFixtureHandler(t))
	iss, err := c.Get(context.Background(), "DEMO-101")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if iss.Key != "DEMO-101" {
		t.Errorf("key = %q", iss.Key)
	}
	if iss.URL != "https://example.atlassian.net/browse/DEMO-101" {
		t.Errorf("browse URL = %q", iss.URL)
	}
	if iss.Type != "Story" {
		t.Errorf("type = %q, want Story", iss.Type)
	}
	if iss.Status != "Ready for QA" || iss.StatusCategory != "new" {
		t.Errorf("status = %q / %q", iss.Status, iss.StatusCategory)
	}
	if iss.Priority != "Medium" {
		t.Errorf("priority = %q", iss.Priority)
	}
	if iss.Assignee != "Alex Rivera" || iss.Reporter != "Sam Chen" {
		t.Errorf("assignee/reporter = %q / %q", iss.Assignee, iss.Reporter)
	}
	if iss.Title == "" {
		t.Errorf("title empty")
	}
	if len(iss.Description) == 0 {
		t.Errorf("description not rendered")
	}
	if iss.Sprint == nil || iss.Sprint.Name != "Sprint 2026-14" || iss.Sprint.State != "active" {
		t.Errorf("sprint = %+v, want active Sprint 2026-14", iss.Sprint)
	}
	if iss.Sprint != nil && (iss.Sprint.StartDate == "" || iss.Sprint.EndDate == "") {
		t.Errorf("sprint dates missing: %+v", iss.Sprint)
	}
	if len(iss.Subtasks) != 2 {
		t.Fatalf("subtasks = %d, want 2", len(iss.Subtasks))
	}
	if iss.Subtasks[0].Key != "DEMO-102" || iss.Subtasks[0].Type != "Sub-task" || iss.Subtasks[0].Status == "" {
		t.Errorf("subtask[0] = %+v", iss.Subtasks[0])
	}
}

func TestGet_BugNoSprint(t *testing.T) {
	c := restClient(t, issueFixtureHandler(t))
	iss, err := c.Get(context.Background(), "DEMO-201")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if iss.Type != "Bug" {
		t.Errorf("type = %q, want Bug", iss.Type)
	}
	if len(iss.Description) == 0 {
		t.Errorf("bug description not rendered")
	}
}

func TestGet_DecodesAttachments(t *testing.T) {
	const body = `{"key":"DEMO-1","self":"https://example.atlassian.net/rest/api/3/issue/10001","fields":{
		"summary":"With media",
		"attachment":[
			{"id":"173517","filename":"image-20260708-040128.png","mimeType":"image/png","content":"https://x/rest/api/3/attachment/content/173517"},
			{"id":"173520","filename":"clip.mp4","mimeType":"video/mp4"},
			{"id":"","filename":"skip-me.png","mimeType":"image/png"}
		]}}`
	c := restClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	})
	iss, err := c.Get(context.Background(), "DEMO-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(iss.Attachments) != 2 {
		t.Fatalf("attachments = %d, want 2 (blank-id row dropped)", len(iss.Attachments))
	}
	if iss.Attachments[0].ID != "173517" || iss.Attachments[0].Filename != "image-20260708-040128.png" || iss.Attachments[0].MimeType != "image/png" {
		t.Errorf("attachment[0] = %+v", iss.Attachments[0])
	}
	if iss.Attachments[1].MimeType != "video/mp4" {
		t.Errorf("attachment[1] mime = %q, want video/mp4", iss.Attachments[1].MimeType)
	}
}

func TestGet_NoAttachmentsIsNil(t *testing.T) {
	c := restClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"key":"DEMO-2","self":"https://example.atlassian.net/rest/api/3/issue/2","fields":{"summary":"none"}}`))
	})
	iss, err := c.Get(context.Background(), "DEMO-2")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if iss.Attachments != nil {
		t.Errorf("attachments = %+v, want nil", iss.Attachments)
	}
}

// The display Get() hits the REST v3 issue endpoint with a basic-auth header and a
// fields param — the same seam search/transitions use.
func TestGet_RequestsIssueEndpointWithAuth(t *testing.T) {
	var gotMethod, gotPath, gotAuth, gotFields string
	c := restClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotFields = r.URL.Query().Get("fields")
		b, _ := os.ReadFile(filepath.Join("testdata", "demo-101.json"))
		_, _ = w.Write(b)
	})
	if _, err := c.Get(context.Background(), "DEMO-101"); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if gotMethod != http.MethodGet {
		t.Errorf("method = %q, want GET", gotMethod)
	}
	if gotPath != "/rest/api/3/issue/DEMO-101" {
		t.Errorf("path = %q", gotPath)
	}
	if !strings.HasPrefix(gotAuth, "Basic ") {
		t.Errorf("auth = %q, want Basic …", gotAuth)
	}
	if gotFields == "" {
		t.Errorf("fields query param missing")
	}
}

func TestGet_InvalidKeyRejectedBeforeRequest(t *testing.T) {
	called := false
	c := restClient(t, func(w http.ResponseWriter, _ *http.Request) {
		called = true
		_, _ = w.Write([]byte("{}"))
	})
	for _, bad := range []string{"", "nope", "demo-101", "PROJ 2272", "PROJ-", "-1", "DROP;TABLE-1"} {
		if _, err := c.Get(context.Background(), bad); !errors.Is(err, ErrBadKey) {
			t.Errorf("Get(%q) err = %v, want ErrBadKey", bad, err)
		}
	}
	if called {
		t.Errorf("no HTTP request must be made for invalid keys")
	}
}

func TestGet_ClassifiesHTTPStatus(t *testing.T) {
	cases := []struct {
		name   string
		status int
		want   error
	}{
		{"not found", http.StatusNotFound, ErrNotFound},
		{"no permission (404)", http.StatusNotFound, ErrNotFound},
		{"unauthorized", http.StatusUnauthorized, ErrAuthFailed},
		{"forbidden", http.StatusForbidden, ErrAuthFailed},
		{"server error", http.StatusInternalServerError, ErrUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := restClient(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(`{"errorMessages":["boom"]}`))
			})
			if _, err := c.Get(context.Background(), "DEMO-1"); !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

// A missing/invalid credential resolves to a config error the caller surfaces
// (degrade to an inline error, never a crash) — the same as the other REST paths.
func TestGet_ConfigErrorPropagates(t *testing.T) {
	c := NewClient(WithConfigSource(func() (restConfig, error) {
		return restConfig{}, fmt.Errorf("%w: no Jira API token", ErrAuthFailed)
	}))
	if _, err := c.Get(context.Background(), "DEMO-1"); !errors.Is(err, ErrAuthFailed) {
		t.Errorf("config err = %v, want ErrAuthFailed", err)
	}
}

func TestGet_TransportErrorIsUnavailable(t *testing.T) {
	// Point the client at a server we immediately close so the request cannot
	// connect; the dial failure must classify as ErrUnavailable.
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	base := srv.URL
	srv.Close()
	c := NewClient(WithConfigSource(func() (restConfig, error) {
		return restConfig{baseURL: base, email: "e@example.com", token: "tok"}, nil
	}))
	if _, err := c.Get(context.Background(), "DEMO-1"); !errors.Is(err, ErrUnavailable) {
		t.Errorf("transport err = %v, want ErrUnavailable", err)
	}
}

func TestGet_MalformedBodyIsUnavailable(t *testing.T) {
	c := restClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("not json"))
	})
	if _, err := c.Get(context.Background(), "DEMO-1"); !errors.Is(err, ErrUnavailable) {
		t.Errorf("malformed body err = %v, want ErrUnavailable", err)
	}
}

func TestBrowseURL(t *testing.T) {
	got := browseURL("https://x.atlassian.net/rest/api/3/issue/10001", "DEMO-9")
	if got != "https://x.atlassian.net/browse/DEMO-9" {
		t.Errorf("browseURL = %q", got)
	}
	if browseURL("garbage", "DEMO-9") != "" {
		t.Errorf("browseURL with no /rest/ should be empty")
	}
}
