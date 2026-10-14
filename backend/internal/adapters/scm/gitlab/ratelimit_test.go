package gitlab

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func rateLimitedServer(t *testing.T, retryAfter string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", retryAfter)
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"message":"429 Too Many Requests"}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestDoRESTClassifies429AsRateLimitedWithRetryAfterSeconds(t *testing.T) {
	srv := rateLimitedServer(t, "30")
	c := NewClient(ClientOptions{APIBase: srv.URL, Token: StaticTokenSource("t")})

	_, err := c.doREST(context.Background(), "user", nil)
	var rl *RateLimitError
	if !errors.As(err, &rl) {
		t.Fatalf("err = %v, want *RateLimitError", err)
	}
	if !errors.Is(err, ErrRateLimited) {
		t.Errorf("errors.Is(err, ErrRateLimited) = false")
	}
	if rl.RetryAfter != 30*time.Second {
		t.Errorf("RetryAfter = %v, want 30s", rl.RetryAfter)
	}
	if rl.Message != "429 Too Many Requests" {
		t.Errorf("Message = %q", rl.Message)
	}
}

func TestDoRESTClassifies429WithRetryAfterAsHTTPDate(t *testing.T) {
	srv := rateLimitedServer(t, time.Now().Add(2*time.Minute).UTC().Format(http.TimeFormat))
	c := NewClient(ClientOptions{APIBase: srv.URL, Token: StaticTokenSource("t")})

	_, err := c.doREST(context.Background(), "user", nil)
	var rl *RateLimitError
	if !errors.As(err, &rl) {
		t.Fatalf("err = %v, want *RateLimitError", err)
	}
	if rl.RetryAfter < 100*time.Second || rl.RetryAfter > 2*time.Minute {
		t.Errorf("RetryAfter = %v, want about 2m", rl.RetryAfter)
	}
}

func TestDoRESTTypes404AsNotFoundAndKeepsItsText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"404 Project Not Found"}`))
	}))
	t.Cleanup(srv.Close)
	c := NewClient(ClientOptions{APIBase: srv.URL, Token: StaticTokenSource("t")})

	_, err := c.doREST(context.Background(), "projects/1/milestones", nil)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if err.Error() != "gitlab scm: 404 404 Project Not Found" {
		t.Errorf("text = %q, want the pre-typing text", err.Error())
	}
}

func TestDoRESTLeaves5xxOutOfNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	t.Cleanup(srv.Close)
	c := NewClient(ClientOptions{APIBase: srv.URL, Token: StaticTokenSource("t")})

	_, err := c.doREST(context.Background(), "user", nil)
	if err == nil || errors.Is(err, ErrNotFound) || err.Error() != "gitlab scm: 502 " {
		t.Fatalf("err = %v", err)
	}
}

func TestRetryAfterInThePastOrUnparseableIsZero(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	for _, h := range []string{"", "soon", "-5", now.Add(-time.Minute).Format(http.TimeFormat)} {
		if got := parseRetryAfter(h, now); got != 0 {
			t.Errorf("parseRetryAfter(%q) = %v, want 0", h, got)
		}
	}
}
