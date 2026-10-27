package httpd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/releaseweb"
)

type recordingReleaseWeb struct{ calls []string }

func (f *recordingReleaseWeb) do(name string) (releaseweb.Status, error) {
	f.calls = append(f.calls, name)
	return releaseweb.Status{}, nil
}

func (f *recordingReleaseWeb) Status(context.Context) (releaseweb.Status, error) {
	return f.do("status")
}
func (f *recordingReleaseWeb) Start(context.Context) (releaseweb.Status, error) { return f.do("start") }
func (f *recordingReleaseWeb) Stop(context.Context) (releaseweb.Status, error)  { return f.do("stop") }
func (f *recordingReleaseWeb) RotateKey(context.Context) (releaseweb.Status, error) {
	return f.do("rotate-key")
}
func (f *recordingReleaseWeb) CopyKey(context.Context) error {
	_, err := f.do("copy-key")
	return err
}

func send(t *testing.T, srv *httptest.Server, method, path, origin string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, srv.URL+path, strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	return resp
}

func TestTheReleaseWebsOwnOriginIsNotTrustedAsLoopback(t *testing.T) {
	t.Setenv(releaseweb.DirEnv, "")
	dataDir := filepath.Join(t.TempDir(), "data")
	webDir := releaseweb.DirFor(dataDir)
	if err := os.MkdirAll(webDir, 0o700); err != nil {
		t.Fatal(err)
	}
	run, _ := json.Marshal(map[string]any{"pid": 1, "port": 7555})
	if err := os.WriteFile(filepath.Join(webDir, "run.json"), run, 0o600); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(newTestRouter(config.Config{AllowedOrigins: []string{"app://renderer"}, DataDir: dataDir}, discardLogger(), nil))
	defer srv.Close()

	for _, origin := range []string{"http://127.0.0.1:7420", "http://localhost:7420", "https://localhost:7420", "http://127.0.0.1:7555", "http://[::1]:7555"} {
		resp := send(t, srv, http.MethodGet, "/healthz", origin)
		if resp.StatusCode != http.StatusForbidden || resp.Header.Get("Access-Control-Allow-Origin") != "" {
			t.Errorf("GET from the release web's origin %s = %d ACAO %q, want 403 and no grant",
				origin, resp.StatusCode, resp.Header.Get("Access-Control-Allow-Origin"))
		}
	}
	if resp := send(t, srv, http.MethodGet, "/healthz", "http://127.0.0.1:5181"); resp.StatusCode != http.StatusOK {
		t.Errorf("GET from a dev server's loopback origin = %d, want 200", resp.StatusCode)
	}
}

func TestReleaseWebControlsAcceptOnlyTheAppsOwnOrigin(t *testing.T) {
	svc := &recordingReleaseWeb{}
	cfg := config.Config{AllowedOrigins: []string{"app://renderer"}, DataDir: t.TempDir()}
	srv := httptest.NewServer(NewRouterWithControl(cfg, discardLogger(), nil, APIDeps{ReleaseWeb: svc}, ControlDeps{}))
	defer srv.Close()

	for _, action := range []string{"start", "stop", "rotate-key", "copy-key"} {
		path := "/api/v1/release-web/" + action
		for _, origin := range []string{"http://localhost:5181", "http://127.0.0.1:8080", "http://evil.example"} {
			if resp := send(t, srv, http.MethodPost, path, origin); resp.StatusCode != http.StatusForbidden {
				t.Errorf("POST %s from %s = %d, want 403", path, origin, resp.StatusCode)
			}
		}
		if len(svc.calls) != 0 {
			t.Fatalf("a refused origin reached the release web control: %v", svc.calls)
		}
		for _, origin := range []string{"app://renderer", ""} {
			if resp := send(t, srv, http.MethodPost, path, origin); resp.StatusCode != http.StatusOK {
				t.Errorf("POST %s from origin %q = %d, want 200", path, origin, resp.StatusCode)
			}
		}
		svc.calls = nil
	}
	if resp := send(t, srv, http.MethodGet, "/api/v1/release-web", "http://localhost:5181"); resp.StatusCode != http.StatusOK {
		t.Errorf("GET the status from a loopback dev origin = %d, want 200", resp.StatusCode)
	}
}
