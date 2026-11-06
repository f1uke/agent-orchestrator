package controllers_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
)

type orchestratorTransportBody struct {
	OrchestratorTransport string `json:"orchestratorTransport"`
}

func TestOrchestratorTransportRoutes_DefaultToStubsWithoutService(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)

	for _, method := range []string{"GET", "PUT"} {
		body, status, _ := doRequest(t, srv, method, "/api/v1/settings/orchestrator-transport", `{"orchestratorTransport":"terminal"}`)
		assertErrorCode(t, body, status, http.StatusNotImplemented, "NOT_IMPLEMENTED")
	}
}

func TestOrchestratorTransportSetting_ShipsChatAndSurvivesARestart(t *testing.T) {
	dir := t.TempDir()
	srv := newTransportSettingsServer(t, dir)

	body, status, _ := doRequest(t, srv, "GET", "/api/v1/settings/orchestrator-transport", "")
	var got orchestratorTransportBody
	mustJSON(t, body, &got)
	if status != http.StatusOK || got.OrchestratorTransport != "chat" {
		t.Fatalf("fresh data dir: %d %s, want 200 chat", status, body)
	}

	body, status, _ = doRequest(t, srv, "PUT", "/api/v1/settings/orchestrator-transport", `{"orchestratorTransport":"terminal"}`)
	mustJSON(t, body, &got)
	if status != http.StatusOK || got.OrchestratorTransport != "terminal" {
		t.Fatalf("PUT terminal: %d %s", status, body)
	}

	restarted := newTransportSettingsServer(t, dir)
	body, status, _ = doRequest(t, restarted, "GET", "/api/v1/settings/orchestrator-transport", "")
	mustJSON(t, body, &got)
	if status != http.StatusOK || got.OrchestratorTransport != "terminal" {
		t.Fatalf("after a restart on the same data dir: %d %s, want 200 terminal", status, body)
	}
}

func TestOrchestratorAndWorkerTransportSettingsDoNotAffectEachOther(t *testing.T) {
	srv := newTransportSettingsServer(t, t.TempDir())
	if _, status, _ := doRequest(t, srv, "PUT", "/api/v1/settings/orchestrator-transport", `{"orchestratorTransport":"terminal"}`); status != http.StatusOK {
		t.Fatalf("PUT orchestrator terminal = %d", status)
	}
	body, _, _ := doRequest(t, srv, "GET", "/api/v1/settings/worker-transport", "")
	var worker workerTransportBody
	mustJSON(t, body, &worker)
	if worker.WorkerTransport != "chat" {
		t.Fatalf("setting the orchestrator moved the worker to %q", worker.WorkerTransport)
	}

	if _, status, _ := doRequest(t, srv, "PUT", "/api/v1/settings/worker-transport", `{"workerTransport":"terminal"}`); status != http.StatusOK {
		t.Fatalf("PUT worker terminal = %d", status)
	}
	body, _, _ = doRequest(t, srv, "GET", "/api/v1/settings/orchestrator-transport", "")
	var orch orchestratorTransportBody
	mustJSON(t, body, &orch)
	if orch.OrchestratorTransport != "terminal" {
		t.Fatalf("setting the worker moved the orchestrator to %q", orch.OrchestratorTransport)
	}
}

func TestOrchestratorTransportSetting_RefusesWhatIsNotATransportAndKeepsTheValue(t *testing.T) {
	srv := newTransportSettingsServer(t, t.TempDir())
	if _, status, _ := doRequest(t, srv, "PUT", "/api/v1/settings/orchestrator-transport", `{"orchestratorTransport":"terminal"}`); status != http.StatusOK {
		t.Fatalf("PUT terminal = %d", status)
	}

	for name, tc := range map[string]struct{ body, code string }{
		"unknown":     {`{"orchestratorTransport":"pigeon"}`, "TRANSPORT_INVALID"},
		"empty":       {`{"orchestratorTransport":""}`, "INVALID_SETTINGS"},
		"key omitted": {`{}`, "INVALID_SETTINGS"},
		"not json":    {`{`, "INVALID_JSON"},
	} {
		t.Run(name, func(t *testing.T) {
			body, status, _ := doRequest(t, srv, "PUT", "/api/v1/settings/orchestrator-transport", tc.body)
			assertErrorCode(t, body, status, http.StatusBadRequest, tc.code)
		})
	}

	body, _, _ := doRequest(t, srv, "GET", "/api/v1/settings/orchestrator-transport", "")
	var got orchestratorTransportBody
	mustJSON(t, body, &got)
	if got.OrchestratorTransport != "terminal" {
		t.Fatalf("a refused PUT changed the setting to %q", got.OrchestratorTransport)
	}
}
