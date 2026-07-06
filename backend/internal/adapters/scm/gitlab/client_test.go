package gitlab

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

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
