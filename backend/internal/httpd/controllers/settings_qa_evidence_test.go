package controllers_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/qaevidence"
)

func newQAEvidenceTestServer(t *testing.T) (*httptest.Server, *qaevidence.Store) {
	t.Helper()
	store, err := qaevidence.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{QAEvidence: store}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	return srv, store
}

func TestQAEvidenceRoutesDefaultToStubsWithoutService(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	body, status, _ := doRequest(t, srv, "GET", "/api/v1/settings/qa-evidence", "")
	assertErrorCode(t, body, status, http.StatusNotImplemented, "NOT_IMPLEMENTED")
}

func TestQAEvidenceControllerSavesAndReadsTheFolder(t *testing.T) {
	srv, store := newQAEvidenceTestServer(t)

	body, status, _ := doRequest(t, srv, "GET", "/api/v1/settings/qa-evidence", "")
	if status != http.StatusOK || string(body) != `{"driveFolder":""}`+"\n" {
		t.Fatalf("GET = %d %s, want an empty folder", status, body)
	}
	body, status, _ = doRequest(t, srv, "PUT", "/api/v1/settings/qa-evidence", `{"driveFolder":" finnomena:QA/ "}`)
	if status != http.StatusOK {
		t.Fatalf("PUT = %d %s", status, body)
	}
	var got struct {
		DriveFolder string `json:"driveFolder"`
	}
	mustJSON(t, body, &got)
	if got.DriveFolder != "finnomena:QA" || store.Get().DriveFolder != "finnomena:QA" {
		t.Fatalf("response %q, stored %q; want finnomena:QA", got.DriveFolder, store.Get().DriveFolder)
	}
}

func TestQAEvidenceControllerRefusesALocalPath(t *testing.T) {
	srv, store := newQAEvidenceTestServer(t)
	body, status, _ := doRequest(t, srv, "PUT", "/api/v1/settings/qa-evidence", `{"driveFolder":"/Users/me/QA"}`)
	assertErrorCode(t, body, status, http.StatusBadRequest, "INVALID_SETTINGS")
	if store.Get().DriveFolder != "" {
		t.Fatalf("stored %q after a refused PUT", store.Get().DriveFolder)
	}
}
