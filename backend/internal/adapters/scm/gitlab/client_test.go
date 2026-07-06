package gitlab

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// invalidatingTokenSource is a fake TokenSource that also implements
// tokenInvalidator, recording how many times InvalidateToken is called so
// tests can assert whether the Client invalidated on a given response.
type invalidatingTokenSource struct {
	token           string
	invalidateCalls int
}

func (s *invalidatingTokenSource) Token(context.Context) (string, error) {
	return s.token, nil
}

func (s *invalidatingTokenSource) InvalidateToken() {
	s.invalidateCalls++
}

func TestClientDoRESTInvalidatesTokenOnAuthFailure(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		wantCalls  int
	}{
		{name: "401 unauthorized invalidates token", statusCode: http.StatusUnauthorized, wantCalls: 1},
		{name: "403 forbidden invalidates token", statusCode: http.StatusForbidden, wantCalls: 1},
		{name: "404 not found does not invalidate token", statusCode: http.StatusNotFound, wantCalls: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.statusCode)
				_, _ = w.Write([]byte(`{"message":"nope"}`))
			}))
			defer srv.Close()

			src := &invalidatingTokenSource{token: "tok-123"}
			c := NewClient(ClientOptions{APIBase: srv.URL, Token: src})
			_, err := c.doRESTWithETag(context.Background(), "projects/x/merge_requests", nil, "")
			if err == nil {
				t.Fatalf("expected error for status %d, got nil", tt.statusCode)
			}
			if src.invalidateCalls != tt.wantCalls {
				t.Fatalf("InvalidateToken called %d times; want %d", src.invalidateCalls, tt.wantCalls)
			}
		})
	}
}

func TestClientDoRESTSendsPrivateToken(t *testing.T) {
	var gotToken string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotToken = r.Header.Get("PRIVATE-TOKEN")
		w.Header().Set("ETag", `"abc"`)
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	c := NewClient(ClientOptions{APIBase: srv.URL, Token: StaticTokenSource("tok-123")})
	resp, err := c.doRESTWithETag(context.Background(), "projects/x/merge_requests", nil, "")
	if err != nil {
		t.Fatalf("doRESTWithETag: %v", err)
	}
	if gotToken != "tok-123" {
		t.Fatalf("PRIVATE-TOKEN header = %q, want tok-123", gotToken)
	}
	if resp.ETag != `"abc"` {
		t.Fatalf("ETag = %q", resp.ETag)
	}
}

func TestClientDoRESTMaps304(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") == `"abc"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()
	c := NewClient(ClientOptions{APIBase: srv.URL, Token: StaticTokenSource("t")})
	resp, err := c.doRESTWithETag(context.Background(), "p", nil, `"abc"`)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !resp.NotModified {
		t.Fatalf("expected NotModified")
	}
}
