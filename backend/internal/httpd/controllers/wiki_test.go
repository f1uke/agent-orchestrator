package controllers_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
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
	wrote     []wikisvc.WriteNoteInput
	writeRes  wikisvc.WriteNoteResult
	writeErr  error
	tasks     wikisvc.Tasks
	tasksErr  error
	completed []wikisvc.CompleteTaskInput
	completeR wikisvc.CompleteTaskResult
	completeE error
}

func (f *fakeWikiSvc) ListTasks(context.Context) (wikisvc.Tasks, error) {
	return f.tasks, f.tasksErr
}

func (f *fakeWikiSvc) CompleteTask(_ context.Context, in wikisvc.CompleteTaskInput) (wikisvc.CompleteTaskResult, error) {
	f.completed = append(f.completed, in)
	if f.completeE != nil {
		return wikisvc.CompleteTaskResult{}, f.completeE
	}
	return f.completeR, nil
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

func (f *fakeWikiSvc) WriteNote(_ context.Context, in wikisvc.WriteNoteInput) (wikisvc.WriteNoteResult, error) {
	f.wrote = append(f.wrote, in)
	if f.writeErr != nil {
		return wikisvc.WriteNoteResult{}, f.writeErr
	}
	return f.writeRes, nil
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
		{"PUT", "/api/v1/wiki/file"},
		{"GET", "/api/v1/wiki/tasks"},
		{"POST", "/api/v1/wiki/tasks/complete"},
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

func TestWriteWikiNote_PassesThePreconditionThrough(t *testing.T) {
	svc := &fakeWikiSvc{writeRes: wikisvc.WriteNoteResult{
		Path:        "notes/tasks.md",
		ContentHash: "sha256:after",
		Size:        12,
		ModifiedAt:  time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC),
	}}
	srv := newWikiTestServer(t, httpd.APIDeps{Wiki: svc})

	body, status, headers := doRequest(t, srv, "PUT", "/api/v1/wiki/file",
		`{"path":"notes/tasks.md","content":"- [x] one\n","baseHash":" sha256:before "}`)
	assertJSON(t, headers)
	if status != http.StatusOK {
		t.Fatalf("status = %d body = %s", status, body)
	}
	if len(svc.wrote) != 1 {
		t.Fatalf("wrote = %+v", svc.wrote)
	}
	if svc.wrote[0].Content != "- [x] one\n" || svc.wrote[0].BaseHash != "sha256:before" {
		t.Fatalf("input = %+v", svc.wrote[0])
	}
	var got struct {
		Path        string `json:"path"`
		ContentHash string `json:"contentHash"`
		Size        int64  `json:"size"`
		ModifiedAt  string `json:"modifiedAt"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if got.ContentHash != "sha256:after" || got.Size != 12 || got.ModifiedAt != "2026-09-02T10:00:00Z" {
		t.Fatalf("response = %+v", got)
	}
}

// A `content` key that never arrived is not an empty note: as a plain string
// the two collapse, and a client that forgot the field would empty somebody's
// note and get a 200 for it. The base hash cannot catch that — it is still
// correct — so the route has to.
func TestWriteWikiNote_RefusesAnAbsentContentButAllowsAnEmptyOne(t *testing.T) {
	svc := &fakeWikiSvc{}
	srv := newWikiTestServer(t, httpd.APIDeps{Wiki: svc})

	body, status, headers := doRequest(t, srv, "PUT", "/api/v1/wiki/file", `{"path":"a.md","baseHash":"sha256:x"}`)
	assertJSON(t, headers)
	assertErrorCode(t, body, status, http.StatusBadRequest, "WIKI_NOTE_CONTENT_REQUIRED")
	if len(svc.wrote) != 0 {
		t.Fatalf("an absent content reached the service: %+v", svc.wrote)
	}

	if _, status, _ := doRequest(t, srv, "PUT", "/api/v1/wiki/file",
		`{"path":"a.md","content":"","baseHash":"sha256:x"}`); status != http.StatusOK {
		t.Fatalf("emptying a note explicitly must be allowed: status = %d", status)
	}
}

func TestWriteWikiNote_RequiresAPath(t *testing.T) {
	svc := &fakeWikiSvc{}
	srv := newWikiTestServer(t, httpd.APIDeps{Wiki: svc})

	body, status, headers := doRequest(t, srv, "PUT", "/api/v1/wiki/file", `{"content":"x","baseHash":"sha256:x"}`)
	assertJSON(t, headers)
	assertErrorCode(t, body, status, http.StatusBadRequest, "PATH_REQUIRED")
}

func TestWriteWikiNote_SurfacesTheConflictEnvelope(t *testing.T) {
	svc := &fakeWikiSvc{writeErr: apierr.Conflict("WIKI_NOTE_CONFLICT", "changed", map[string]any{"currentHash": "sha256:now"})}
	srv := newWikiTestServer(t, httpd.APIDeps{Wiki: svc})

	body, status, headers := doRequest(t, srv, "PUT", "/api/v1/wiki/file",
		`{"path":"a.md","content":"x","baseHash":"sha256:stale"}`)
	assertJSON(t, headers)
	assertErrorCode(t, body, status, http.StatusConflict, "WIKI_NOTE_CONFLICT")
}

func TestWikiTasks_ShapesTheRowsAndNeverNullsAList(t *testing.T) {
	svc := &fakeWikiSvc{tasks: wikisvc.Tasks{
		Configured: true,
		Folders:    []string{"Areas"},
		Rows: []wikisvc.Task{{
			ID: "abc", Path: "Areas/a.md", Line: 7, Raw: "- [ ] [@Someone] a row (from: 2026-05-07 standup) due:2026-05-09",
			Text: "a row (from: 2026-05-07 standup)", Section: "Mine", Owner: "Someone", Due: "2026-05-09",
			Created: "2026-05-06", FromDate: "2026-05-07",
		}},
	}}
	srv := newWikiTestServer(t, httpd.APIDeps{Wiki: svc})

	body, status, headers := doRequest(t, srv, "GET", "/api/v1/wiki/tasks", "")
	assertJSON(t, headers)
	if status != http.StatusOK {
		t.Fatalf("status = %d: %s", status, body)
	}
	var got struct {
		Configured bool     `json:"configured"`
		Folders    []string `json:"folders"`
		Tasks      []struct {
			ID       string `json:"id"`
			Path     string `json:"path"`
			Line     int    `json:"line"`
			Raw      string `json:"raw"`
			Text     string `json:"text"`
			Owner    string `json:"owner"`
			Due      string `json:"due"`
			Created  string `json:"created"`
			FromDate string `json:"fromDate"`
		} `json:"tasks"`
		Sections     []string `json:"sections"`
		Owners       []string `json:"owners"`
		OwnerAliases []string `json:"ownerAliases"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if !got.Configured || len(got.Folders) != 1 || got.Folders[0] != "Areas" || len(got.Tasks) != 1 {
		t.Fatalf("body = %+v", got)
	}
	row := got.Tasks[0]
	if row.Line != 7 || row.Raw != "- [ ] [@Someone] a row (from: 2026-05-07 standup) due:2026-05-09" {
		t.Fatalf("row = %+v", row)
	}
	// The dates that describe the ROW travel with it; the note's mtime does not
	// travel at all, so nothing downstream can date a row by its file again.
	if row.Owner != "Someone" || row.Due != "2026-05-09" || row.Created != "2026-05-06" || row.FromDate != "2026-05-07" {
		t.Fatalf("row = %+v", row)
	}
	if strings.Contains(string(body), "noteModifiedAt") {
		t.Fatalf("a task row still carries the note's mtime: %s", body)
	}
	// A renderer maps over these, so they must be [] and never null.
	if got.Sections == nil || got.Owners == nil || got.OwnerAliases == nil || got.Folders == nil {
		t.Fatalf("a list came back null: %+v", got)
	}
}

// The row's exact text is its identity, so the route must hand it through
// untouched — trimming it would make a row with trailing whitespace
// unmatchable and the tick would be refused for no reason the reader can see.
func TestWikiCompleteTask_PassesTheRawRowThroughVerbatim(t *testing.T) {
	raw := "  - [ ] a row with trailing space  "
	svc := &fakeWikiSvc{completeR: wikisvc.CompleteTaskResult{Path: "Areas/a.md", Line: 4, Raw: "  - [x] a row with trailing space  "}}
	srv := newWikiTestServer(t, httpd.APIDeps{Wiki: svc})

	payload, err := json.Marshal(map[string]any{"path": "Areas/a.md", "line": 4, "raw": raw})
	if err != nil {
		t.Fatal(err)
	}
	body, status, _ := doRequest(t, srv, "POST", "/api/v1/wiki/tasks/complete", string(payload))
	if status != http.StatusOK {
		t.Fatalf("status = %d: %s", status, body)
	}
	if len(svc.completed) != 1 {
		t.Fatalf("completed = %v", svc.completed)
	}
	if svc.completed[0].Raw != raw {
		t.Fatalf("Raw = %q, want it verbatim (%q)", svc.completed[0].Raw, raw)
	}
	if svc.completed[0].Line != 4 || svc.completed[0].Path != "Areas/a.md" {
		t.Fatalf("input = %+v", svc.completed[0])
	}
}

func TestWikiCompleteTask_RequiresAPath(t *testing.T) {
	svc := &fakeWikiSvc{}
	srv := newWikiTestServer(t, httpd.APIDeps{Wiki: svc})

	body, status, headers := doRequest(t, srv, "POST", "/api/v1/wiki/tasks/complete", `{"raw":"- [ ] x"}`)
	assertJSON(t, headers)
	assertErrorCode(t, body, status, http.StatusBadRequest, "PATH_REQUIRED")
	if len(svc.completed) != 0 {
		t.Fatal("a pathless tick reached the service")
	}
}

// A refusal must arrive as the service's own code, so the tab can explain
// exactly which of the three "we did not write anything" cases happened.
func TestWikiCompleteTask_SurfacesTheRefusalCode(t *testing.T) {
	svc := &fakeWikiSvc{completeE: apierr.Conflict("WIKI_TASK_AMBIGUOUS", "two rows match", nil)}
	srv := newWikiTestServer(t, httpd.APIDeps{Wiki: svc})

	body, status, _ := doRequest(t, srv, "POST", "/api/v1/wiki/tasks/complete", `{"path":"a.md","line":1,"raw":"- [ ] x"}`)
	assertErrorCode(t, body, status, http.StatusConflict, "WIKI_TASK_AMBIGUOUS")
}

func TestWikiCompleteTask_ReportsAMovedRow(t *testing.T) {
	svc := &fakeWikiSvc{completeR: wikisvc.CompleteTaskResult{Path: "a.md", Line: 9, Raw: "- [x] x", Moved: true}}
	srv := newWikiTestServer(t, httpd.APIDeps{Wiki: svc})

	body, status, _ := doRequest(t, srv, "POST", "/api/v1/wiki/tasks/complete", `{"path":"a.md","line":2,"raw":"- [ ] x"}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d: %s", status, body)
	}
	var got struct {
		Line  int  `json:"line"`
		Moved bool `json:"moved"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if !got.Moved || got.Line != 9 {
		t.Fatalf("body = %+v, want moved to line 9", got)
	}
}
