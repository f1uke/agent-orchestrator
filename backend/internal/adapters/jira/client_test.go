package jira

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// fixtureRunner returns a Runner that serves testdata/<lowercased-key>.json.
func fixtureRunner(t *testing.T) Runner {
	t.Helper()
	return func(_ context.Context, key string) ([]byte, error) {
		b, err := os.ReadFile(filepath.Join("testdata", toLowerKey(key)+".json"))
		if err != nil {
			return nil, err
		}
		return b, nil
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

func TestGet_Star2272_MapsStructuredFields(t *testing.T) {
	c := NewClient(WithRunner(fixtureRunner(t)))
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

func TestGet_Star2312_BugNoSprint(t *testing.T) {
	c := NewClient(WithRunner(fixtureRunner(t)))
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

func TestGet_InvalidKeyRejectedBeforeShellOut(t *testing.T) {
	called := false
	c := NewClient(WithRunner(func(context.Context, string) ([]byte, error) {
		called = true
		return nil, nil
	}))
	for _, bad := range []string{"", "nope", "demo-101", "STAR 2272", "STAR-", "-1", "DROP;TABLE-1"} {
		if _, err := c.Get(context.Background(), bad); !errors.Is(err, ErrBadKey) {
			t.Errorf("Get(%q) err = %v, want ErrBadKey", bad, err)
		}
	}
	if called {
		t.Errorf("runner must not be invoked for invalid keys")
	}
}

func TestGet_ClassifiesRunnerErrors(t *testing.T) {
	cases := []struct {
		name   string
		runErr error
		want   error
	}{
		{"not found", errors.New("jira issue view X: exit status 1: 404 Not Found"), ErrNotFound},
		{"permission", errors.New("Issue does not exist or you do not have permission"), ErrNotFound},
		{"auth", errors.New("401 Unauthorized: invalid token"), ErrAuthFailed},
		{"other", errors.New("dial tcp: connection refused"), ErrUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := NewClient(WithRunner(func(context.Context, string) ([]byte, error) {
				return nil, tc.runErr
			}))
			_, err := c.Get(context.Background(), "DEMO-1")
			if !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestGet_EmptyOutputIsUnavailable(t *testing.T) {
	c := NewClient(WithRunner(func(context.Context, string) ([]byte, error) { return []byte("  \n"), nil }))
	if _, err := c.Get(context.Background(), "DEMO-1"); !errors.Is(err, ErrUnavailable) {
		t.Errorf("empty output err = %v, want ErrUnavailable", err)
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
