package jira

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSearchIssuesLaterPageFailureKeepsTheRowsAlreadyRead(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("nextPageToken") == "" {
			_, _ = io.WriteString(w, issuesJSON(keysN("DEMO", searchPageSize), "TOK2"))
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := NewClient(WithHTTPDoer(srv.Client().Do), WithConfigSource(staticConfig(srv.URL)))
	out, err := c.SearchIssues(context.Background(), `project = "DEMO"`, SearchMaxResults)
	if err != nil {
		t.Fatalf("err = %v, want nil (best effort)", err)
	}
	if len(out) != searchPageSize {
		t.Errorf("rows = %d, want the %d from page 1", len(out), searchPageSize)
	}
}

func TestExistingReadsKeepThe429Classification(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"errorMessages":["Rate limit exceeded"]}`)
	}))
	defer srv.Close()
	c := NewClient(WithHTTPDoer(srv.Client().Do), WithConfigSource(staticConfig(srv.URL)))
	ctx := context.Background()

	_, searchErr := c.SearchIssues(ctx, `project = "DEMO"`, 25)
	_, getErr := c.Get(ctx, "DEMO-1")
	_, transErr := c.Transitions(ctx, "DEMO-1")
	_, myselfErr := c.Myself(ctx)
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"SearchIssues", searchErr, "jira: unavailable: search: HTTP 429: Rate limit exceeded"},
		{"Get", getErr, "jira: unavailable: DEMO-1: HTTP 429: Rate limit exceeded"},
		{"Transitions", transErr, "jira: unavailable: DEMO-1: HTTP 429: Rate limit exceeded"},
		{"Myself", myselfErr, "jira: unavailable: myself: HTTP 429: Rate limit exceeded"},
	}
	for _, tc := range cases {
		if !errors.Is(tc.err, ErrUnavailable) {
			t.Errorf("%s: err = %v, want ErrUnavailable", tc.name, tc.err)
		}
		if tc.err == nil || tc.err.Error() != tc.want {
			t.Errorf("%s: err = %v, want %q", tc.name, tc.err, tc.want)
		}
	}
}
