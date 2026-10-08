package controllers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

func TestSessionsAPI_SpawnForwardsTheClaudeProfile(t *testing.T) {
	svc := newFakeSessionService()
	srv := newSessionTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions", `{"projectId":"ao","kind":"worker","claudeProfile":"OmniRoute"}`)
	if status != http.StatusCreated {
		t.Fatalf("POST session = %d; body=%s", status, body)
	}
	if svc.lastSpawnCfg.ClaudeProfile != "OmniRoute" {
		t.Fatalf("SpawnConfig.ClaudeProfile = %q, want OmniRoute", svc.lastSpawnCfg.ClaudeProfile)
	}
}

func TestSessionsAPI_SpawnSurfacesAnUnknownClaudeProfile(t *testing.T) {
	svc := newFakeSessionService()
	svc.spawnErr = apierr.Invalid("UNKNOWN_CLAUDE_PROFILE", `unknown Claude profile "nope" (known: Subscription, OmniRoute)`, map[string]any{"known": []string{"Subscription", "OmniRoute"}})
	srv := newSessionTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions", `{"projectId":"ao","kind":"worker","claudeProfile":"nope"}`)
	assertErrorCode(t, body, status, http.StatusBadRequest, "UNKNOWN_CLAUDE_PROFILE")
}

func TestSessionsAPI_UpdateSpecForwardsTheClaudeProfile(t *testing.T) {
	svc := newFakeSessionService()
	srv := newSessionTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "PATCH", "/api/v1/sessions/ao-1/spec", `{"claudeProfile":"OmniRoute"}`)
	if status != http.StatusOK {
		t.Fatalf("PATCH spec = %d; body=%s", status, body)
	}
	if svc.updatedPatch.ClaudeProfile == nil || *svc.updatedPatch.ClaudeProfile != "OmniRoute" {
		t.Fatalf("patch ClaudeProfile = %v, want OmniRoute", svc.updatedPatch.ClaudeProfile)
	}
}

func TestSetClaudeProfile_ReturnsTheSessionAndWhatHappenedToTheAgent(t *testing.T) {
	svc := newFakeSessionService()
	srv := newSessionTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "PUT", "/api/v1/sessions/ao-1/claude-profile", `{"profile":"OmniRoute","restart":true}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d; body=%s", status, body)
	}
	if svc.claudeProfileReq != (claudeProfileCall{id: "ao-1", profile: "OmniRoute", restart: true}) {
		t.Fatalf("service got %+v", svc.claudeProfileReq)
	}
	var got struct {
		Session struct {
			ClaudeProfile  string `json:"claudeProfile"`
			RestartPending bool   `json:"restartPending"`
		} `json:"session"`
		Restart string `json:"restart"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if got.Session.ClaudeProfile != "OmniRoute" || !got.Session.RestartPending || got.Restart != "pending" {
		t.Fatalf("response = %+v, want OmniRoute, pending", got)
	}
}

func TestSetClaudeProfile_RequiresAProfile(t *testing.T) {
	srv := newSessionTestServer(t, newFakeSessionService())
	body, status, _ := doRequest(t, srv, "PUT", "/api/v1/sessions/ao-1/claude-profile", `{"profile":" "}`)
	assertErrorCode(t, body, status, http.StatusBadRequest, "CLAUDE_PROFILE_REQUIRED")
}

func TestSetClaudeProfile_NonClaudeSessionIsAConflict(t *testing.T) {
	svc := newFakeSessionService()
	svc.claudeProfileErr = apierr.Conflict("CLAUDE_PROFILE_UNSUPPORTED", "Claude profiles apply only to claude-code sessions", nil)
	srv := newSessionTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "PUT", "/api/v1/sessions/ao-1/claude-profile", `{"profile":"OmniRoute"}`)
	assertErrorCode(t, body, status, http.StatusConflict, "CLAUDE_PROFILE_UNSUPPORTED")
}

type claudeProfileCall struct {
	id      domain.SessionID
	profile string
	restart bool
}

func (f *fakeSessionService) SetClaudeProfile(_ context.Context, id domain.SessionID, profile string, restart bool) (domain.Session, domain.ClaudeProfileRestart, error) {
	f.claudeProfileReq = claudeProfileCall{id: id, profile: profile, restart: restart}
	if f.claudeProfileErr != nil {
		return domain.Session{}, "", f.claudeProfileErr
	}
	s := f.sessions[id]
	s.ClaudeProfile = profile
	s.RestartPending = restart
	f.sessions[id] = s
	return s, domain.ClaudeProfileRestartPending, nil
}
