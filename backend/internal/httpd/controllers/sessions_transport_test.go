package controllers_test

import (
	"net/http"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestSessionsAPI_SpawnTransport(t *testing.T) {
	for _, tc := range []struct {
		body string
		want domain.Transport
	}{
		{`{"projectId":"ao","kind":"worker","transport":"chat"}`, domain.TransportChat},
		{`{"projectId":"ao","kind":"worker","transport":"terminal"}`, domain.TransportTerminal},
		{`{"projectId":"ao","kind":"worker"}`, domain.TransportTerminal},
	} {
		svc := newFakeSessionService()
		srv := newSessionTestServer(t, svc)
		body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions", tc.body)
		if status != http.StatusCreated {
			t.Fatalf("%s: POST session = %d, want 201; body=%s", tc.body, status, body)
		}
		if svc.lastSpawnCfg.Transport != tc.want {
			t.Fatalf("%s: SpawnConfig.Transport = %q, want %q", tc.body, svc.lastSpawnCfg.Transport, tc.want)
		}
	}
}

func TestSessionsAPI_SpawnRejectsUnknownTransport(t *testing.T) {
	svc := newFakeSessionService()
	srv := newSessionTestServer(t, svc)
	body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions", `{"projectId":"ao","kind":"worker","transport":"pigeon"}`)
	assertErrorCode(t, body, status, http.StatusBadRequest, "TRANSPORT_INVALID")
}
