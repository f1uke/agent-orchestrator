package gitlab

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func fullPageJSON(n int) string {
	rows := make([]string, n)
	for i := range rows {
		rows[i] = fmt.Sprintf(`{"id":%d}`, i)
	}
	return "[" + strings.Join(rows, ",") + "]"
}

func TestFetchAllPagesStopsSilentlyAtTheCap(t *testing.T) {
	var queries []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries = append(queries, r.URL.RawQuery)
		_, _ = w.Write([]byte(fullPageJSON(100)))
	}))
	defer srv.Close()
	c := NewClient(ClientOptions{APIBase: srv.URL, Token: StaticTokenSource("t")})

	rows, err := fetchAllPages[struct{ ID int }](context.Background(), c, "projects/1/things", 2, "things")
	if err != nil {
		t.Fatalf("err = %v, want nil at the cap", err)
	}
	if len(rows) != 200 {
		t.Errorf("rows = %d, want 200", len(rows))
	}
	want := []string{"page=1&per_page=100", "page=2&per_page=100"}
	if strings.Join(queries, " ") != strings.Join(want, " ") {
		t.Errorf("queries = %q, want %q", queries, want)
	}
}

func TestFetchAllPagesLaterPageFailureReturnsTheErrorAndNoRows(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") == "2" {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"boom"}`))
			return
		}
		_, _ = w.Write([]byte(fullPageJSON(100)))
	}))
	defer srv.Close()
	c := NewClient(ClientOptions{APIBase: srv.URL, Token: StaticTokenSource("t")})

	rows, err := fetchAllPages[struct{ ID int }](context.Background(), c, "projects/1/things", 5, "things")
	if rows != nil {
		t.Errorf("rows = %d, want nil", len(rows))
	}
	if err == nil || err.Error() != "gitlab scm: 500 boom" {
		t.Errorf("err = %v, want %q", err, "gitlab scm: 500 boom")
	}
}

func TestExistingReadsKeepThe429ErrorText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"message":"429 Too Many Requests"}`))
	}))
	defer srv.Close()
	p := newTestProvider(t, srv.URL)
	ref := ports.SCMPRRef{Repo: ports.SCMRepo{Repo: "group/proj"}, Number: 7}

	_, err := p.FetchReviewThreads(context.Background(), ref)
	if err == nil || err.Error() != "gitlab scm: 429 429 Too Many Requests" {
		t.Errorf("FetchReviewThreads err = %v", err)
	}
	if errors.Is(err, ErrAuthFailed) {
		t.Errorf("a 429 must not read as an auth failure")
	}
	obs, err := p.FetchPullRequests(context.Background(), []ports.SCMPRRef{ref})
	if err != nil || len(obs) != 1 || obs[0].Fetched {
		t.Errorf("FetchPullRequests = %+v, %v, want one unfetched observation and no error", obs, err)
	}
}
