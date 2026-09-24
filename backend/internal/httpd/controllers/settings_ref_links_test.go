package controllers_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/reflinks"
)

type refLinksSettingsBody struct {
	JiraBaseURL       string            `json:"jiraBaseUrl"`
	GitLabBaseURL     string            `json:"gitlabBaseUrl"`
	GitLabDefaultRepo string            `json:"gitlabDefaultRepo"`
	GitLabRepoAliases map[string]string `json:"gitlabRepoAliases"`
}

// The real store, not a fake: validation lives in it, and the controller's job
// is to surface it as a 400.
func newRefLinksTestServer(t *testing.T) (*httptest.Server, *reflinks.Store) {
	t.Helper()
	store, err := reflinks.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{RefLinks: store}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	return srv, store
}

func TestRefLinksRoutes_DefaultToStubsWithoutService(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)

	body, status, headers := doRequest(t, srv, "GET", "/api/v1/settings/ref-links", "")
	assertJSON(t, headers)
	assertErrorCode(t, body, status, http.StatusNotImplemented, "NOT_IMPLEMENTED")
}

func TestRefLinksController_GetDefaultsEmpty(t *testing.T) {
	srv, _ := newRefLinksTestServer(t)

	body, status, _ := doRequest(t, srv, "GET", "/api/v1/settings/ref-links", "")
	if status != http.StatusOK {
		t.Fatalf("code=%d body=%s", status, body)
	}
	var got refLinksSettingsBody
	mustJSON(t, body, &got)
	if got.JiraBaseURL != "" || got.GitLabBaseURL != "" || got.GitLabDefaultRepo != "" || len(got.GitLabRepoAliases) != 0 {
		t.Fatalf("got = %#v, want everything empty", got)
	}
	if got.GitLabRepoAliases == nil {
		t.Fatalf("gitlabRepoAliases decoded as null, want {}: %s", body)
	}
}

func TestRefLinksController_PutSavesNormalized(t *testing.T) {
	srv, store := newRefLinksTestServer(t)

	body, status, _ := doRequest(t, srv, "PUT", "/api/v1/settings/ref-links",
		`{"jiraBaseUrl":"https://jira.example.com/","gitlabBaseUrl":"https://gitlab.example.com","gitlabDefaultRepo":"group/project","gitlabRepoAliases":{"XYZ":"group/other/"}}`)
	if status != http.StatusOK {
		t.Fatalf("code=%d body=%s", status, body)
	}
	var got refLinksSettingsBody
	mustJSON(t, body, &got)
	if got.JiraBaseURL != "https://jira.example.com" || got.GitLabRepoAliases["XYZ"] != "group/other" {
		t.Fatalf("response = %#v, want the normalized values", got)
	}
	if store.Get().GitLabDefaultRepo != "group/project" {
		t.Fatalf("stored = %+v", store.Get())
	}
}

func TestRefLinksController_PutRejectsInvalid(t *testing.T) {
	srv, store := newRefLinksTestServer(t)

	body, status, _ := doRequest(t, srv, "PUT", "/api/v1/settings/ref-links", `{"jiraBaseUrl":"javascript:alert(1)"}`)
	assertErrorCode(t, body, status, http.StatusBadRequest, "INVALID_SETTINGS")
	if store.Get().JiraBaseURL != "" {
		t.Fatalf("an invalid value was stored: %+v", store.Get())
	}
}

func TestRefLinksController_PutInvalidJSON(t *testing.T) {
	srv, _ := newRefLinksTestServer(t)

	body, status, _ := doRequest(t, srv, "PUT", "/api/v1/settings/ref-links", `{`)
	assertErrorCode(t, body, status, http.StatusBadRequest, "INVALID_JSON")
}
