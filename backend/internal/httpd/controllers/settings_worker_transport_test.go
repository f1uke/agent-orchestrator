package controllers_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/workertransport"
)

type workerTransportBody struct {
	WorkerTransport string `json:"workerTransport"`
}

func newWorkerTransportServer(t *testing.T, dir string) *httptest.Server {
	t.Helper()
	store, err := workertransport.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{WorkerTransport: store}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	return srv
}

func TestWorkerTransportRoutes_DefaultToStubsWithoutService(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)

	for _, method := range []string{"GET", "PUT"} {
		body, status, _ := doRequest(t, srv, method, "/api/v1/settings/worker-transport", `{"workerTransport":"terminal"}`)
		assertErrorCode(t, body, status, http.StatusNotImplemented, "NOT_IMPLEMENTED")
	}
}

func TestWorkerTransportSetting_ShipsChatAndSurvivesARestart(t *testing.T) {
	dir := t.TempDir()
	srv := newWorkerTransportServer(t, dir)

	body, status, _ := doRequest(t, srv, "GET", "/api/v1/settings/worker-transport", "")
	var got workerTransportBody
	mustJSON(t, body, &got)
	if status != http.StatusOK || got.WorkerTransport != "chat" {
		t.Fatalf("fresh data dir: %d %s, want 200 chat", status, body)
	}

	body, status, _ = doRequest(t, srv, "PUT", "/api/v1/settings/worker-transport", `{"workerTransport":"terminal"}`)
	mustJSON(t, body, &got)
	if status != http.StatusOK || got.WorkerTransport != "terminal" {
		t.Fatalf("PUT terminal: %d %s", status, body)
	}

	restarted := newWorkerTransportServer(t, dir)
	body, status, _ = doRequest(t, restarted, "GET", "/api/v1/settings/worker-transport", "")
	mustJSON(t, body, &got)
	if status != http.StatusOK || got.WorkerTransport != "terminal" {
		t.Fatalf("after a restart on the same data dir: %d %s, want 200 terminal", status, body)
	}
}

func TestWorkerTransportSetting_RefusesWhatIsNotATransportAndKeepsTheValue(t *testing.T) {
	srv := newWorkerTransportServer(t, t.TempDir())
	if _, status, _ := doRequest(t, srv, "PUT", "/api/v1/settings/worker-transport", `{"workerTransport":"terminal"}`); status != http.StatusOK {
		t.Fatalf("PUT terminal = %d", status)
	}

	for name, tc := range map[string]struct{ body, code string }{
		"unknown":     {`{"workerTransport":"pigeon"}`, "TRANSPORT_INVALID"},
		"empty":       {`{"workerTransport":""}`, "INVALID_SETTINGS"},
		"key omitted": {`{}`, "INVALID_SETTINGS"},
		"not json":    {`{`, "INVALID_JSON"},
	} {
		t.Run(name, func(t *testing.T) {
			body, status, _ := doRequest(t, srv, "PUT", "/api/v1/settings/worker-transport", tc.body)
			assertErrorCode(t, body, status, http.StatusBadRequest, tc.code)
		})
	}

	body, _, _ := doRequest(t, srv, "GET", "/api/v1/settings/worker-transport", "")
	var got workerTransportBody
	mustJSON(t, body, &got)
	if got.WorkerTransport != "terminal" {
		t.Fatalf("a refused PUT changed the setting to %q", got.WorkerTransport)
	}
}
