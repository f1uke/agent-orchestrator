package controllers_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
)

const modelContextTranscript = `{"type":"api-request-shape","shapeHash":"h1","timestamp":"2026-11-07T10:00:00Z","shape":{"system":[{"type":"text","text":"You are a coding agent."}],"tools":[{"name":"Read","description":"Reads a file","input_schema":{"type":"object"}}]}}
{"type":"attachment","timestamp":"2026-11-07T10:00:01Z","attachment":{"type":"date"},"rendered":[{"content":"Today's date is 2026-11-07."}],"renderedRole":"system"}
`

func modelContextServer(t *testing.T, harness domain.AgentHarness, transcript string) *httptest.Server {
	t.Helper()
	svc := newFakeSessionService()
	s := svc.sessions["ao-1"]
	s.Harness = harness
	svc.sessions["ao-1"] = s
	locate := func(rec domain.SessionRecord) string {
		if rec.Harness != domain.HarnessClaudeCode {
			return ""
		}
		return transcript
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil,
		httpd.APIDeps{Sessions: svc, LocateTranscript: locate}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	return srv
}

func getModelContext(t *testing.T, srv *httptest.Server, session string) (*http.Response, []byte) {
	t.Helper()
	resp, err := srv.Client().Get(srv.URL + "/api/v1/sessions/" + session + "/chat/model-context")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	return resp, body
}

func TestModelContextIsEmptyForAHarnessWithNoTranscript(t *testing.T) {
	srv := modelContextServer(t, domain.HarnessCodex, "")
	resp, body := getModelContext(t, srv, "ao-1")
	if resp.StatusCode != http.StatusNoContent || len(body) != 0 {
		t.Errorf("status %d body %q, want 204 and no body", resp.StatusCode, body)
	}
}

func TestModelContextIsEmptyWhenTheTranscriptIsGone(t *testing.T) {
	srv := modelContextServer(t, domain.HarnessClaudeCode, filepath.Join(t.TempDir(), "missing.jsonl"))
	if resp, _ := getModelContext(t, srv, "ao-1"); resp.StatusCode != http.StatusNoContent {
		t.Errorf("status %d, want 204", resp.StatusCode)
	}
}

func TestModelContextReadsTheTranscript(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	writeFile(t, path, modelContextTranscript)
	srv := modelContextServer(t, domain.HarnessClaudeCode, path)
	resp, body := getModelContext(t, srv, "ao-1")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	var got struct {
		Shapes []struct {
			TS     string   `json:"ts"`
			System []string `json:"system"`
			Tools  []struct {
				Name        string          `json:"name"`
				InputSchema json.RawMessage `json:"inputSchema"`
			} `json:"tools"`
		} `json:"shapes"`
		Injections []struct {
			TS     string `json:"ts"`
			Source string `json:"source"`
			Role   string `json:"role"`
			Text   string `json:"text"`
		} `json:"injections"`
		Truncated bool `json:"truncated"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Shapes) != 1 || got.Shapes[0].System[0] != "You are a coding agent." || got.Shapes[0].Tools[0].Name != "Read" ||
		string(got.Shapes[0].Tools[0].InputSchema) != `{"type":"object"}` || got.Shapes[0].TS != "2026-11-07T10:00:00Z" {
		t.Errorf("shapes = %+v", got.Shapes)
	}
	if len(got.Injections) != 1 || got.Injections[0].Source != "date" || got.Injections[0].Role != "system" ||
		got.Injections[0].Text != "Today's date is 2026-11-07." || got.Injections[0].TS != "2026-11-07T10:00:01Z" {
		t.Errorf("injections = %+v", got.Injections)
	}
}

func TestModelContextIs404ForAnUnknownSession(t *testing.T) {
	srv := modelContextServer(t, domain.HarnessClaudeCode, "")
	if resp, _ := getModelContext(t, srv, "nope"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("status %d, want 404", resp.StatusCode)
	}
}
