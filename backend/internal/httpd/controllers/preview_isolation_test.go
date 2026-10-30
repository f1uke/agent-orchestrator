package controllers_test

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// previewWorkspace points the fake session ao-1 at a fresh workspace and
// returns it with a request helper in front of the real router. host, when
// set, is the Host header, so a test can address the preview origin without
// DNS.
func previewWorkspace(t *testing.T) (string, func(method, host, path, origin string) ([]byte, int, http.Header)) {
	t.Helper()
	svc := newFakeSessionService()
	workspace := t.TempDir()
	s := svc.sessions["ao-1"]
	s.Metadata = domain.SessionMetadata{WorkspacePath: workspace}
	svc.sessions["ao-1"] = s
	srv := newSessionTestServer(t, svc)
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	do := func(method, host, path, origin string) ([]byte, int, http.Header) {
		t.Helper()
		var body io.Reader
		if method == http.MethodPost {
			body = strings.NewReader(`{"url":"http://example.com"}`)
		}
		req, err := http.NewRequest(method, srv.URL+path, body)
		if err != nil {
			t.Fatal(err)
		}
		if host != "" {
			req.Host = host
		}
		if method == http.MethodPost {
			req.Header.Set("Content-Type", "text/plain")
		}
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		out, _ := io.ReadAll(resp.Body)
		return out, resp.StatusCode, resp.Header
	}
	return workspace, do
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

const previewHost = "ao-1.preview.localhost"

// A preview page is agent-made HTML, and the daemon has no auth. Served on the
// daemon's own origin, its scripts could call the whole API: send messages,
// answer permission cards, kill sessions. Preview files therefore live on a
// per-session origin, which serves nothing else and whose cross-origin calls
// the daemon refuses.
func TestPreviewPageCannotCallTheDaemonAPI(t *testing.T) {
	workspace, do := previewWorkspace(t)
	writeFile(t, filepath.Join(workspace, "page.html"), `<script>fetch("/api/v1/sessions")</script>`)

	_, status, headers := do(http.MethodGet, "", "/api/v1/sessions/ao-1/preview/files/page.html?v=2", "")
	want := "http://" + previewHost + ":"
	if loc := headers.Get("Location"); status != http.StatusTemporaryRedirect ||
		!strings.HasPrefix(loc, want) || !strings.HasSuffix(loc, "/api/v1/sessions/ao-1/preview/files/page.html?v=2") {
		t.Fatalf("preview file on the daemon origin = %d Location %q, want a redirect to %s...", status, loc, want)
	}

	body, status, headers := do(http.MethodGet, previewHost, "/api/v1/sessions/ao-1/preview/files/page.html", "")
	if status != http.StatusOK || !strings.Contains(string(body), "fetch(") {
		t.Fatalf("preview file on its origin = %d %q, want the page", status, body)
	}
	if got := headers.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q, want nosniff", got)
	}

	// A module script is fetched in CORS mode, so the browser sends the page's
	// own origin; the page must still load it.
	writeFile(t, filepath.Join(workspace, "app.js"), "export {}")
	if body, status, _ := do(http.MethodGet, previewHost, "/api/v1/sessions/ao-1/preview/files/app.js", "http://"+previewHost); status != http.StatusOK {
		t.Fatalf("module script on the preview origin = %d, want 200; body=%s", status, body)
	}

	sameOrigin := []struct{ method, path string }{
		{http.MethodGet, "/api/v1/sessions"},
		{http.MethodPost, "/api/v1/sessions/ao-1/preview"},
		{http.MethodPost, "/shutdown"},
		{http.MethodGet, "/mux"},
		{http.MethodGet, "/api/v1/sessions/ao-2/preview/files/index.html"},
	}
	for _, call := range sameOrigin {
		body, status, _ := do(call.method, previewHost, call.path, "http://"+previewHost)
		if status != http.StatusNotFound {
			t.Fatalf("%s %s on the preview origin = %d, want 404; body=%s", call.method, call.path, status, body)
		}
	}

	for _, call := range sameOrigin[:2] {
		body, status, _ := do(call.method, "", call.path, "http://"+previewHost+":3001")
		if status != http.StatusForbidden {
			t.Fatalf("%s %s from the preview origin = %d, want 403; body=%s", call.method, call.path, status, body)
		}
	}
}

// A symlink inside the workspace must not serve a file outside it.
func TestPreviewFilesRefusesSymlinksThatLeaveTheWorkspace(t *testing.T) {
	workspace, do := previewWorkspace(t)
	outside := t.TempDir()
	writeFile(t, filepath.Join(outside, "secret.txt"), "OUTSIDE-SECRET")
	writeFile(t, filepath.Join(outside, "dir", "page.html"), "OUTSIDE-SECRET")
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(workspace, "leak.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "dir"), filepath.Join(workspace, "linked")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(workspace, "docs", "real.html"), "INSIDE")
	if err := os.Symlink(filepath.Join(workspace, "docs", "real.html"), filepath.Join(workspace, "alias.html")); err != nil {
		t.Fatal(err)
	}

	for _, file := range []string{"leak.txt", "linked/page.html"} {
		body, status, _ := do(http.MethodGet, previewHost, "/api/v1/sessions/ao-1/preview/files/"+file, "")
		if status != http.StatusNotFound || strings.Contains(string(body), "OUTSIDE-SECRET") {
			t.Fatalf("GET %s = %d %q, want 404 without the outside file", file, status, body)
		}
	}
	body, status, _ := do(http.MethodGet, previewHost, "/api/v1/sessions/ao-1/preview/files/alias.html", "")
	if status != http.StatusOK || !strings.Contains(string(body), "INSIDE") {
		t.Fatalf("GET alias.html = %d %q, want the in-workspace target", status, body)
	}
}
