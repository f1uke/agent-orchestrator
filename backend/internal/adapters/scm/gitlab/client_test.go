package gitlab

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
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
		w.WriteHeader(http.StatusOK)
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

// TestClientRESTURLSingleEscapesProjectPath guards against double
// URL-encoding of a pre-escaped path segment. observer_provider.go's
// projectID() builds "group%2Fsub%2Fproj" via url.PathEscape before handing
// it to restURL; restURL must not re-escape the '%' into "%25" or the
// resulting wire path 404s against a real GitLab server for any project
// whose path contains '/'. httptest's r.URL.Path single-decodes, which is
// why this must assert on r.URL.EscapedPath() instead.
func TestClientRESTURLSingleEscapesProjectPath(t *testing.T) {
	var gotEscapedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotEscapedPath = r.URL.EscapedPath()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	c := NewClient(ClientOptions{APIBase: srv.URL, Token: StaticTokenSource("t")})
	// Mirrors how observer_provider.go's projectID/mrListPath build the path
	// for repo "group/sub/proj": url.PathEscape the project id, then join it
	// into the merge_requests list path.
	path := "projects/" + url.PathEscape("group/sub/proj") + "/merge_requests"
	if _, err := c.doREST(context.Background(), path, nil); err != nil {
		t.Fatalf("doREST: %v", err)
	}
	if !strings.Contains(gotEscapedPath, "group%2Fsub%2Fproj") {
		t.Fatalf("EscapedPath() = %q, want it to contain group%%2Fsub%%2Fproj", gotEscapedPath)
	}
	if strings.Contains(gotEscapedPath, "group%252Fsub%252Fproj") {
		t.Fatalf("EscapedPath() = %q, project path was double-escaped", gotEscapedPath)
	}
}

// TestClientRESTURLPlainASCIIPathsUnaffected confirms the RawPath fix does
// not alter behavior for paths that need no escaping.
func TestClientRESTURLPlainASCIIPathsUnaffected(t *testing.T) {
	tests := []string{"user", "projects/x/jobs/11/trace"}
	for _, path := range tests {
		t.Run(path, func(t *testing.T) {
			var gotPath string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{}`))
			}))
			defer srv.Close()

			c := NewClient(ClientOptions{APIBase: srv.URL, Token: StaticTokenSource("t")})
			if _, err := c.doREST(context.Background(), path, nil); err != nil {
				t.Fatalf("doREST: %v", err)
			}
			want := "/" + path
			if gotPath != want {
				t.Fatalf("Path = %q, want %q", gotPath, want)
			}
		})
	}
}

func TestClientDoRESTMaps304(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") == `"abc"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.WriteHeader(http.StatusOK)
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
