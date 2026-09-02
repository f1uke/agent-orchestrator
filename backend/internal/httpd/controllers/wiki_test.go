package controllers_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	wikisvc "github.com/aoagents/agent-orchestrator/backend/internal/service/wiki"
	"github.com/aoagents/agent-orchestrator/backend/internal/wikisettings"
)

type fakeWikiSvc struct {
	status    wikisvc.Status
	files     wikisvc.Files
	note      wikisvc.NoteContent
	err       error
	started   []domain.AgentHarness
	restarted int
	stopped   int
	readPath  string
}

func (f *fakeWikiSvc) Status(context.Context) (wikisvc.Status, error) {
	return f.status, f.err
}

func (f *fakeWikiSvc) Start(_ context.Context, h domain.AgentHarness) (wikisvc.Status, error) {
	if f.err != nil {
		return wikisvc.Status{}, f.err
	}
	f.started = append(f.started, h)
	f.status.Running = true
	f.status.Harness = string(h)
	f.status.HandleID = wikisvc.HandleID
	return f.status, nil
}

func (f *fakeWikiSvc) Restart(context.Context) (wikisvc.Status, error) {
	if f.err != nil {
		return wikisvc.Status{}, f.err
	}
	f.restarted++
	return f.status, nil
}

func (f *fakeWikiSvc) Stop(context.Context) (wikisvc.Status, error) {
	if f.err != nil {
		return wikisvc.Status{}, f.err
	}
	f.stopped++
	f.status.Running = false
	f.status.HandleID = ""
	return f.status, nil
}

func (f *fakeWikiSvc) ListFiles(context.Context) (wikisvc.Files, error) {
	return f.files, f.err
}

func (f *fakeWikiSvc) ReadNote(_ context.Context, path string) (wikisvc.NoteContent, error) {
	f.readPath = path
	return f.note, f.err
}

type fakeWikiSettingsSvc struct {
	cur wikisettings.Settings
}

func (f *fakeWikiSettingsSvc) Get() wikisettings.Settings { return f.cur }

func (f *fakeWikiSettingsSvc) Set(s wikisettings.Settings) error {
	f.cur = s
	return nil
}

func newWikiTestServer(t *testing.T, deps httpd.APIDeps) *httptest.Server {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, deps, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	return srv
}

func TestWikiRoutes_WithoutServiceAreNotImplemented(t *testing.T) {
	srv := newWikiTestServer(t, httpd.APIDeps{})
	for _, route := range []struct {
		method string
		path   string
	}{
		{"GET", "/api/v1/wiki"},
		{"POST", "/api/v1/wiki/agent"},
		{"POST", "/api/v1/wiki/agent/restart"},
		{"DELETE", "/api/v1/wiki/agent"},
		{"GET", "/api/v1/wiki/files"},
		{"GET", "/api/v1/wiki/file?path=a.md"},
	} {
		body, status, headers := doRequest(t, srv, route.method, route.path, "")
		assertJSON(t, headers)
		assertErrorCode(t, body, status, http.StatusNotImplemented, "NOT_IMPLEMENTED")
	}
}

func TestWikiStatus_ReportsTheHandleAndVault(t *testing.T) {
	svc := &fakeWikiSvc{status: wikisvc.Status{
		Configured: true,
		VaultPath:  "/vault",
		Harness:    "claude-code",
		Running:    true,
		HandleID:   wikisvc.HandleID,
		StartedAt:  time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC),
	}}
	srv := newWikiTestServer(t, httpd.APIDeps{Wiki: svc})

	body, status, headers := doRequest(t, srv, "GET", "/api/v1/wiki", "")
	assertJSON(t, headers)
	if status != http.StatusOK {
		t.Fatalf("status = %d: %s", status, body)
	}
	var got struct {
		Configured bool   `json:"configured"`
		VaultPath  string `json:"vaultPath"`
		Harness    string `json:"harness"`
		Running    bool   `json:"running"`
		HandleID   string `json:"handleId"`
		StartedAt  string `json:"startedAt"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if !got.Configured || got.VaultPath != "/vault" || got.HandleID != wikisvc.HandleID {
		t.Fatalf("body = %+v", got)
	}
	if got.StartedAt != "2026-09-02T10:00:00Z" {
		t.Fatalf("startedAt = %q", got.StartedAt)
	}
}

func TestWikiStartAgent_PassesTheHarnessThrough(t *testing.T) {
	svc := &fakeWikiSvc{status: wikisvc.Status{Configured: true, VaultPath: "/vault"}}
	srv := newWikiTestServer(t, httpd.APIDeps{Wiki: svc})

	_, status, _ := doRequest(t, srv, "POST", "/api/v1/wiki/agent", `{"harness":"codex"}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d", status)
	}
	if len(svc.started) != 1 || svc.started[0] != domain.HarnessCodex {
		t.Fatalf("started = %v", svc.started)
	}
}

func TestWikiStartAgent_EmptyBodyReusesTheRememberedAgent(t *testing.T) {
	svc := &fakeWikiSvc{status: wikisvc.Status{Configured: true}}
	srv := newWikiTestServer(t, httpd.APIDeps{Wiki: svc})

	_, status, _ := doRequest(t, srv, "POST", "/api/v1/wiki/agent", "")
	if status != http.StatusOK {
		t.Fatalf("status = %d", status)
	}
	if len(svc.started) != 1 || svc.started[0] != "" {
		t.Fatalf("started = %v, want one empty harness", svc.started)
	}
}

func TestWikiStartAgent_SurfacesTheServiceErrorEnvelope(t *testing.T) {
	svc := &fakeWikiSvc{err: apierr.Invalid("WIKI_NOT_CONFIGURED", "No wiki vault is configured", nil)}
	srv := newWikiTestServer(t, httpd.APIDeps{Wiki: svc})

	body, status, headers := doRequest(t, srv, "POST", "/api/v1/wiki/agent", `{"harness":"codex"}`)
	assertJSON(t, headers)
	assertErrorCode(t, body, status, http.StatusBadRequest, "WIKI_NOT_CONFIGURED")
}

func TestWikiStopAgent_TearsDownThePane(t *testing.T) {
	svc := &fakeWikiSvc{status: wikisvc.Status{Configured: true, Running: true, HandleID: wikisvc.HandleID}}
	srv := newWikiTestServer(t, httpd.APIDeps{Wiki: svc})

	body, status, _ := doRequest(t, srv, "DELETE", "/api/v1/wiki/agent", "")
	if status != http.StatusOK {
		t.Fatalf("status = %d: %s", status, body)
	}
	if svc.stopped != 1 {
		t.Fatalf("stopped %d times", svc.stopped)
	}
}

func TestWikiRestartAgent(t *testing.T) {
	svc := &fakeWikiSvc{status: wikisvc.Status{Configured: true}}
	srv := newWikiTestServer(t, httpd.APIDeps{Wiki: svc})

	_, status, _ := doRequest(t, srv, "POST", "/api/v1/wiki/agent/restart", "")
	if status != http.StatusOK || svc.restarted != 1 {
		t.Fatalf("status = %d, restarted = %d", status, svc.restarted)
	}
}

func TestWikiFiles_ReturnsAnEmptyArrayNotNull(t *testing.T) {
	svc := &fakeWikiSvc{}
	srv := newWikiTestServer(t, httpd.APIDeps{Wiki: svc})

	body, status, _ := doRequest(t, srv, "GET", "/api/v1/wiki/files", "")
	if status != http.StatusOK {
		t.Fatalf("status = %d", status)
	}
	var got struct {
		Notes []map[string]any `json:"notes"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if got.Notes == nil {
		t.Fatalf("notes must serialise as [], got null: %s", body)
	}
}

func TestWikiNote_RequiresAPath(t *testing.T) {
	svc := &fakeWikiSvc{}
	srv := newWikiTestServer(t, httpd.APIDeps{Wiki: svc})

	body, status, headers := doRequest(t, srv, "GET", "/api/v1/wiki/file", "")
	assertJSON(t, headers)
	assertErrorCode(t, body, status, http.StatusBadRequest, "PATH_REQUIRED")
}

func TestWikiNote_ReturnsRawMarkdown(t *testing.T) {
	svc := &fakeWikiSvc{note: wikisvc.NoteContent{Path: "llm/note.md", Content: "# Title", Size: 7}}
	srv := newWikiTestServer(t, httpd.APIDeps{Wiki: svc})

	body, status, _ := doRequest(t, srv, "GET", "/api/v1/wiki/file?path=llm%2Fnote.md", "")
	if status != http.StatusOK {
		t.Fatalf("status = %d: %s", status, body)
	}
	if svc.readPath != "llm/note.md" {
		t.Fatalf("readPath = %q", svc.readPath)
	}
	var got struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if got.Content != "# Title" {
		t.Fatalf("content = %q", got.Content)
	}
}

func TestWikiNote_NotFoundKeepsTheEnvelope(t *testing.T) {
	svc := &fakeWikiSvc{err: apierr.NotFound("WIKI_NOTE_NOT_FOUND", "Note not found")}
	srv := newWikiTestServer(t, httpd.APIDeps{Wiki: svc})

	body, status, headers := doRequest(t, srv, "GET", "/api/v1/wiki/file?path=../secret.md", "")
	assertJSON(t, headers)
	assertErrorCode(t, body, status, http.StatusNotFound, "WIKI_NOTE_NOT_FOUND")
}

func TestWikiSettings_RoundTripsTheVaultPath(t *testing.T) {
	vault := t.TempDir()
	set := &fakeWikiSettingsSvc{}
	srv := newWikiTestServer(t, httpd.APIDeps{WikiSettings: set})

	body, status, _ := doRequest(t, srv, "PUT", "/api/v1/settings/wiki", `{"vaultPath":"`+vault+`"}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d: %s", status, body)
	}
	if set.cur.VaultPath != vault {
		t.Fatalf("saved = %+v", set.cur)
	}

	body, status, _ = doRequest(t, srv, "GET", "/api/v1/settings/wiki", "")
	if status != http.StatusOK {
		t.Fatalf("status = %d: %s", status, body)
	}
}

func TestWikiSettings_RejectsAPathThatIsNotADirectory(t *testing.T) {
	set := &fakeWikiSettingsSvc{}
	srv := newWikiTestServer(t, httpd.APIDeps{WikiSettings: set})

	body, status, headers := doRequest(t, srv, "PUT", "/api/v1/settings/wiki", `{"vaultPath":"/definitely/not/here"}`)
	assertJSON(t, headers)
	assertErrorCode(t, body, status, http.StatusBadRequest, "WIKI_VAULT_NOT_A_DIRECTORY")
	if set.cur.VaultPath != "" {
		t.Fatalf("a bad path must not be saved: %+v", set.cur)
	}
}

func TestWikiSettings_EmptyPathTurnsTheWikiOff(t *testing.T) {
	set := &fakeWikiSettingsSvc{cur: wikisettings.Settings{VaultPath: "/vault", Harness: "claude-code"}}
	srv := newWikiTestServer(t, httpd.APIDeps{WikiSettings: set})

	_, status, _ := doRequest(t, srv, "PUT", "/api/v1/settings/wiki", `{"vaultPath":""}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d", status)
	}
	if set.cur.VaultPath != "" {
		t.Fatalf("vault not cleared: %+v", set.cur)
	}
	// Clearing the path must not forget which agent the user prefers.
	if set.cur.Harness != "claude-code" {
		t.Fatalf("remembered harness lost: %+v", set.cur)
	}
}
