package controllers_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/chatproto"
	"github.com/aoagents/agent-orchestrator/backend/internal/chatusage"
	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
)

func newUsageTestServer(t *testing.T, store *chatusage.Store) *httptest.Server {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{ChatUsage: store}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	return srv
}

func TestChatUsage(t *testing.T) {
	store := &chatusage.Store{}
	srv := newUsageTestServer(t, store)

	body, status, _ := doRequest(t, srv, "GET", "/api/v1/chat/usage", "")
	if status != http.StatusNoContent || len(body) != 0 {
		t.Fatalf("before any event: status %d body %q, want 204 and no body", status, body)
	}

	at := time.Date(2026, 10, 31, 4, 36, 16, 0, time.UTC)
	store.Record(chatproto.RateLimit{
		FiveHour: &chatproto.RateLimitWindow{Utilization: 0.32, ResetsAt: time.Unix(1793428800, 0).UTC(), Status: "allowed"},
		SevenDay: &chatproto.RateLimitWindow{Utilization: 0.53, ResetsAt: time.Unix(1793692800, 0).UTC(), Status: "allowed_warning"},
	}, at)
	body, status, _ = doRequest(t, srv, "GET", "/api/v1/chat/usage", "")
	if status != http.StatusOK {
		t.Fatalf("status %d, body %s", status, body)
	}
	var got controllers.ChatUsageResponse
	mustJSON(t, body, &got)
	if got.FiveHour == nil || got.FiveHour.Utilization != 0.32 || got.FiveHour.ResetsAt != "2026-10-31T06:40:00Z" || got.FiveHour.Status != "allowed" {
		t.Errorf("fiveHour = %+v", got.FiveHour)
	}
	if got.SevenDay == nil || got.SevenDay.Utilization != 0.53 || got.SevenDay.Status != "allowed_warning" {
		t.Errorf("sevenDay = %+v", got.SevenDay)
	}
	if got.UpdatedAt != "2026-10-31T04:36:16Z" {
		t.Errorf("updatedAt = %q, want the time the event was logged", got.UpdatedAt)
	}
}

func TestChatUsageOmitsAWindowClaudeDidNotReport(t *testing.T) {
	store := &chatusage.Store{}
	store.Record(chatproto.RateLimit{SevenDay: &chatproto.RateLimitWindow{Utilization: 0.5, ResetsAt: time.Unix(0, 0), Status: "allowed"}}, time.Unix(1, 0))
	body, _, _ := doRequest(t, newUsageTestServer(t, store), "GET", "/api/v1/chat/usage", "")
	var got controllers.ChatUsageResponse
	mustJSON(t, body, &got)
	if got.FiveHour != nil || got.SevenDay == nil {
		t.Errorf("got %+v, want only the weekly window", got)
	}
}
