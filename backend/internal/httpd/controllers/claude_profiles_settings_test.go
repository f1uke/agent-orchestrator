package controllers_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/claudeprofile"
	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
)

type claudeProfilesBody struct {
	Profiles []struct {
		Name         string `json:"name"`
		SettingsFile string `json:"settingsFile"`
		Builtin      bool   `json:"builtin"`
	} `json:"profiles"`
}

func newClaudeProfilesTestServer(t *testing.T) (*httptest.Server, *claudeprofile.Store) {
	t.Helper()
	store, err := claudeprofile.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{ClaudeProfiles: store}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	return srv, store
}

func TestClaudeProfilesRoutes_DefaultToStubsWithoutService(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)

	body, status, _ := doRequest(t, srv, "GET", "/api/v1/settings/claude-profiles", "")
	assertErrorCode(t, body, status, http.StatusNotImplemented, "NOT_IMPLEMENTED")
}

func TestClaudeProfiles_PutThenGetRoundTripsUserProfilesAfterBuiltins(t *testing.T) {
	srv, store := newClaudeProfilesTestServer(t)

	body, status, _ := doRequest(t, srv, "PUT", "/api/v1/settings/claude-profiles",
		`{"profiles":[{"name":"Work","settingsFile":"~/.claude/settings-work.json"}]}`)
	if status != http.StatusOK {
		t.Fatalf("PUT code=%d body=%s", status, body)
	}
	if _, ok := store.Resolve("work"); !ok {
		t.Fatal("PUT did not reach the registry")
	}

	body, status, _ = doRequest(t, srv, "GET", "/api/v1/settings/claude-profiles", "")
	if status != http.StatusOK {
		t.Fatalf("GET code=%d body=%s", status, body)
	}
	var got claudeProfilesBody
	mustJSON(t, body, &got)
	if len(got.Profiles) != 3 {
		t.Fatalf("profiles = %+v, want Subscription, OmniRoute, Work", got.Profiles)
	}
	if got.Profiles[0].Name != "Subscription" || !got.Profiles[0].Builtin || got.Profiles[0].SettingsFile != "" {
		t.Errorf("profiles[0] = %+v, want builtin Subscription with no file", got.Profiles[0])
	}
	if got.Profiles[1].Name != "OmniRoute" || !got.Profiles[1].Builtin || got.Profiles[1].SettingsFile != "~/.claude/settings-omniroute.json" {
		t.Errorf("profiles[1] = %+v, want builtin OmniRoute", got.Profiles[1])
	}
	if got.Profiles[2].Name != "Work" || got.Profiles[2].Builtin || got.Profiles[2].SettingsFile != "~/.claude/settings-work.json" {
		t.Errorf("profiles[2] = %+v, want user profile Work", got.Profiles[2])
	}
}

func TestClaudeProfiles_PutRejectsAnInvalidListAndKeepsTheStoredOne(t *testing.T) {
	srv, store := newClaudeProfilesTestServer(t)
	if err := store.SetUser([]claudeprofile.Profile{{Name: "Kept", SettingsFile: "/k.json"}}); err != nil {
		t.Fatal(err)
	}
	for _, payload := range []string{
		`{"profiles":[{"name":"OmniRoute","settingsFile":"/x.json"}]}`,
		`{"profiles":[{"name":"A","settingsFile":"relative.json"}]}`,
		`{"profiles":[{"name":"","settingsFile":"/x.json"}]}`,
	} {
		body, status, _ := doRequest(t, srv, "PUT", "/api/v1/settings/claude-profiles", payload)
		assertErrorCode(t, body, status, http.StatusBadRequest, "INVALID_CLAUDE_PROFILES")
	}
	if _, ok := store.Resolve("Kept"); !ok {
		t.Fatal("a rejected PUT dropped the stored profiles")
	}
}
