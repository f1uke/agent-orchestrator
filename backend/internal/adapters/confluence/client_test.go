package confluence

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/atlassian"
)

type fakeWiki struct {
	*httptest.Server
	mu       sync.Mutex
	pages    int
	perPage  int
	fail     map[int]int
	header   map[string]string
	body     string
	methods  []string
	requests []string
}

func newFakeWiki(t *testing.T, pages int) *fakeWiki {
	t.Helper()
	f := &fakeWiki{pages: pages, perPage: 2, fail: map[int]int{}}
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeWiki) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.methods = append(f.methods, r.Method)
	f.requests = append(f.requests, r.URL.RequestURI())
	page := 1
	if c := r.URL.Query().Get("cursor"); c != "" {
		_, _ = fmt.Sscanf(c, "c%d", &page)
	}
	if r.URL.Path != "/wiki/rest/api/content/search" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if status, ok := f.fail[page]; ok {
		for k, v := range f.header {
			w.Header().Set(k, v)
		}
		w.WriteHeader(status)
		_, _ = fmt.Fprintf(w, `{"statusCode":%d,"message":"fake %d"}`, status, status)
		return
	}
	if f.body != "" {
		_, _ = w.Write([]byte(f.body))
		return
	}
	var results []string
	for i := 0; i < f.perPage; i++ {
		id := (page-1)*f.perPage + i + 1
		results = append(results, fmt.Sprintf(`{"id":"%d","title":"2026.10.13 - Release train #%d","body":{"storage":{"value":"<p>DEMO-%d</p>"}},"history":{"createdDate":"2026-09-16T06:30:40.949Z"},"_links":{"webui":"/spaces/DEMOSPACE/pages/%d"}}`, id, id, id, id))
	}
	next := ""
	if page < f.pages {
		next = fmt.Sprintf(`,"next":"/rest/api/content/search?next=true&cursor=c%d&limit=25&cql=x"`, page+1)
	}
	_, _ = fmt.Fprintf(w, `{"results":[%s],"start":0,"limit":25,"size":%d,"_links":{"base":"x"%s}}`, strings.Join(results, ","), f.perPage, next)
}

func (f *fakeWiki) client() *Client {
	cfg := atlassian.Config{BaseURL: f.URL, Email: "dee@example.com", Token: "t"}
	return NewClient(WithHTTPDoer(f.Client().Do), WithConfigSource(func() (atlassian.Config, error) { return cfg, nil }))
}

func TestSearchPagesFollowsTheNextLinkToTheEnd(t *testing.T) {
	f := newFakeWiki(t, 3)
	pages, err := f.client().SearchPages(context.Background(), `space = DEMOSPACE AND title ~ "Release train"`, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 6 || pages[5].ID != "6" {
		t.Fatalf("pages = %+v, want 6 across 3 result pages", pages)
	}
	p := pages[0]
	want := Page{ID: "1", Title: "2026.10.13 - Release train #1", URL: f.URL + "/wiki/spaces/DEMOSPACE/pages/1",
		Created: time.Date(2026, 9, 16, 6, 30, 40, 949_000_000, time.UTC), Body: "<p>DEMO-1</p>"}
	if p.ID != want.ID || p.Title != want.Title || p.URL != want.URL || !p.Created.Equal(want.Created) || p.Body != want.Body {
		t.Fatalf("page = %+v, want %+v", p, want)
	}
	first := f.requests[0]
	for _, q := range []string{"limit=25", "expand=body.storage%2Chistory", "cql=space"} {
		if !strings.Contains(first, q) {
			t.Fatalf("first request %s lacks %s", first, q)
		}
	}
	if !strings.Contains(f.requests[1], "cursor=c2") || !strings.Contains(f.requests[2], "cursor=c3") {
		t.Fatalf("requests = %v, want the next links followed", f.requests)
	}
}

func TestSearchPagesOnlyEverSendsGET(t *testing.T) {
	f := newFakeWiki(t, 2)
	if _, err := f.client().SearchPages(context.Background(), "type = page", 0); err != nil {
		t.Fatal(err)
	}
	for _, m := range f.methods {
		if m != http.MethodGet {
			t.Fatalf("methods = %v, want GET only", f.methods)
		}
	}
}

func TestSearchPagesPastTheCapIsIncomplete(t *testing.T) {
	f := newFakeWiki(t, 9)
	pages, err := f.client().SearchPages(context.Background(), "type = page", 8)
	if !errors.Is(err, ErrIncomplete) || pages != nil {
		t.Fatalf("pages %d, err = %v; want ErrIncomplete and no pages", len(pages), err)
	}
	if len(f.requests) != 8 {
		t.Fatalf("sent %d requests, want the 8-page cap", len(f.requests))
	}
}

func TestSearchPagesExactlyAtTheCapIsComplete(t *testing.T) {
	f := newFakeWiki(t, 8)
	pages, err := f.client().SearchPages(context.Background(), "type = page", 8)
	if err != nil || len(pages) != 16 {
		t.Fatalf("pages %d, err %v; want 16 and no error", len(pages), err)
	}
}

func TestSearchPagesLaterPageFailureIsIncompleteNotShort(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		header map[string]string
		class  error
	}{
		{"429", http.StatusTooManyRequests, map[string]string{"Retry-After": "40"}, atlassian.ErrRateLimited},
		{"5xx", http.StatusBadGateway, nil, atlassian.ErrUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeWiki(t, 3)
			f.fail[2] = tc.status
			f.header = tc.header
			pages, err := f.client().SearchPages(context.Background(), "type = page", 0)
			if pages != nil || !errors.Is(err, ErrIncomplete) || !errors.Is(err, tc.class) {
				t.Fatalf("pages %d, err = %v; want ErrIncomplete wrapping %v", len(pages), err, tc.class)
			}
			var rl *atlassian.RateLimitError
			if tc.status == http.StatusTooManyRequests && (!errors.As(err, &rl) || rl.RetryAfter != 40*time.Second) {
				t.Fatalf("err = %v, want a RateLimitError waiting 40s", err)
			}
		})
	}
}

func TestSearchPagesClassifiesAFirstPageFailure(t *testing.T) {
	for _, tc := range []struct {
		status int
		want   error
	}{
		{http.StatusUnauthorized, atlassian.ErrAuthFailed},
		{http.StatusForbidden, atlassian.ErrAuthFailed},
		{http.StatusNotFound, ErrNotFound},
		{http.StatusBadRequest, ErrBadQuery},
		{http.StatusTooManyRequests, atlassian.ErrRateLimited},
		{http.StatusServiceUnavailable, atlassian.ErrUnavailable},
	} {
		f := newFakeWiki(t, 1)
		f.fail[1] = tc.status
		_, err := f.client().SearchPages(context.Background(), "type = page", 0)
		if !errors.Is(err, tc.want) || errors.Is(err, ErrIncomplete) {
			t.Errorf("HTTP %d: err = %v, want %v and not incomplete", tc.status, err, tc.want)
		}
	}
}

func TestSearchPagesBadJSONIsUnavailable(t *testing.T) {
	f := newFakeWiki(t, 1)
	f.body = `{"results":[{"id":`
	_, err := f.client().SearchPages(context.Background(), "type = page", 0)
	if !errors.Is(err, atlassian.ErrUnavailable) {
		t.Fatalf("err = %v, want unavailable", err)
	}
}

func TestSearchPagesRefusesANextLinkOffTheSearchEndpoint(t *testing.T) {
	f := newFakeWiki(t, 1)
	f.body = `{"results":[],"_links":{"next":"https://elsewhere.example.com/steal"}}`
	_, err := f.client().SearchPages(context.Background(), "type = page", 0)
	if !errors.Is(err, atlassian.ErrUnavailable) || len(f.requests) != 1 {
		t.Fatalf("err = %v after %d requests, want unavailable after one", err, len(f.requests))
	}
}

func TestErrorTextIsConfluencesOwnAndKeepsTheSharedClass(t *testing.T) {
	for _, tc := range []struct {
		err   error
		text  string
		class error
	}{
		{ErrAuthFailed, "confluence: authentication failed", atlassian.ErrAuthFailed},
		{ErrUnavailable, "confluence: unavailable", atlassian.ErrUnavailable},
	} {
		wrapped := fmt.Errorf("%w: search", tc.err)
		if tc.err.Error() != tc.text || strings.Contains(wrapped.Error(), "jira") || !errors.Is(wrapped, tc.class) || !errors.Is(wrapped, tc.err) {
			t.Errorf("%q: text or class wrong (is shared class %v)", wrapped, errors.Is(wrapped, tc.class))
		}
	}
	if errors.Is(ErrAuthFailed, atlassian.ErrUnavailable) {
		t.Error("auth must not match unavailable")
	}
}
