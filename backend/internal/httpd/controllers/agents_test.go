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

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	agentsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/agent"
)

type fakeAgentCatalog struct {
	inventory    agentsvc.Inventory
	refreshed    agentsvc.Inventory
	probed       agentsvc.ProbeResult
	err          error
	listCalls    int
	refreshCalls int
	probeCalls   int
	probeAgent   string
}

func (f *fakeAgentCatalog) List(context.Context) (agentsvc.Inventory, error) {
	f.listCalls++
	return f.inventory, f.err
}

func (f *fakeAgentCatalog) Refresh(context.Context) (agentsvc.Inventory, error) {
	f.refreshCalls++
	if f.refreshed.Supported != nil {
		return f.refreshed, f.err
	}
	return f.inventory, f.err
}

func (f *fakeAgentCatalog) Probe(_ context.Context, agentID string) (agentsvc.ProbeResult, error) {
	f.probeCalls++
	f.probeAgent = agentID
	return f.probed, f.err
}

func TestListAgents(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	catalog := &fakeAgentCatalog{inventory: agentsvc.Inventory{
		Supported:  []agentsvc.Info{{ID: "claude-code", Label: "Claude Code"}, {ID: "codex", Label: "Codex"}},
		Installed:  []agentsvc.Info{{ID: "codex", Label: "Codex"}},
		Authorized: []agentsvc.Info{{ID: "codex", Label: "Codex"}},
	}}
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{
		Agents: catalog,
	}, httpd.ControlDeps{}))
	defer srv.Close()

	body, status, _ := doRequest(t, srv, http.MethodGet, "/api/v1/agents", "")
	if status != http.StatusOK {
		t.Fatalf("GET /agents = %d, body=%s", status, body)
	}
	for _, want := range []string{`"supported"`, `"installed"`, `"authorized"`, `"id":"codex"`} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("body missing %s: %s", want, body)
		}
	}
	if strings.Contains(string(body), `"counts"`) {
		t.Fatalf("body includes removed counts field: %s", body)
	}
	if catalog.listCalls != 1 || catalog.refreshCalls != 0 {
		t.Fatalf("calls: list=%d refresh=%d, want list=1 refresh=0", catalog.listCalls, catalog.refreshCalls)
	}
}

// TestListAgentsExposesRealModelCatalogAfterRefresh wires the REAL agent service
// (backed by the shipped adapter registry) through the HTTP handler and drives the
// exact live flow — the frontend/daemon refresh the catalog, then read it back. It
// guards the #128 regression where a refresh dropped every agent's model tiers, so
// an empty catalog can never ship green again.
func TestListAgentsExposesRealModelCatalogAfterRefresh(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{
		Agents: agentsvc.New(),
	}, httpd.ControlDeps{}))
	defer srv.Close()

	// Refresh first (what daemon startup and the desktop shell both do), then read.
	if _, status, _ := doRequest(t, srv, http.MethodPost, "/api/v1/agents/refresh", ""); status != http.StatusOK {
		t.Fatalf("POST /agents/refresh = %d", status)
	}
	body, status, _ := doRequest(t, srv, http.MethodGet, "/api/v1/agents", "")
	if status != http.StatusOK {
		t.Fatalf("GET /agents = %d, body=%s", status, body)
	}

	var inv agentsvc.Inventory
	if err := json.Unmarshal(body, &inv); err != nil {
		t.Fatalf("decode inventory: %v, body=%s", err, body)
	}
	models := map[string][]string{}
	for _, info := range inv.Supported {
		ids := make([]string, 0, len(info.Models))
		for _, m := range info.Models {
			ids = append(ids, m.ID)
		}
		models[info.ID] = ids
	}
	wantCatalogs := map[string][]string{
		"claude-code": {"opus", "sonnet", "haiku", "claude-fable-5"},
		"codex":       {"gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna", "gpt-5.5"},
		"opencode":    {"anthropic/claude-opus-4-8", "anthropic/claude-sonnet-5", "anthropic/claude-haiku-4-5", "openai/gpt-5.6"},
	}
	for agentID, want := range wantCatalogs {
		got := models[agentID]
		if len(got) != len(want) {
			t.Fatalf("%s models = %v, want %v", agentID, got, want)
		}
		for i, id := range want {
			if got[i] != id {
				t.Fatalf("%s models = %v, want %v", agentID, got, want)
			}
		}
	}
}

func TestListAgentsMarksOpenEndedModels(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{
		Agents: agentsvc.New(),
	}, httpd.ControlDeps{}))
	defer srv.Close()

	if _, status, _ := doRequest(t, srv, http.MethodPost, "/api/v1/agents/refresh", ""); status != http.StatusOK {
		t.Fatalf("POST /agents/refresh = %d", status)
	}
	body, status, _ := doRequest(t, srv, http.MethodGet, "/api/v1/agents", "")
	if status != http.StatusOK {
		t.Fatalf("GET /agents = %d, body=%s", status, body)
	}
	var inv agentsvc.Inventory
	if err := json.Unmarshal(body, &inv); err != nil {
		t.Fatalf("decode inventory: %v, body=%s", err, body)
	}
	openEnded := map[string]bool{}
	for _, info := range inv.Supported {
		openEnded[info.ID] = info.ModelsOpenEnded
	}
	// opencode's --model is a free-form provider/model string: open-ended.
	// claude-code and codex are fixed-tier and must NOT be open-ended.
	want := map[string]bool{"opencode": true, "claude-code": false, "codex": false}
	for agentID, wantFlag := range want {
		if openEnded[agentID] != wantFlag {
			t.Fatalf("%s modelsOpenEnded = %v, want %v", agentID, openEnded[agentID], wantFlag)
		}
	}
}

func TestRefreshAgents(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	catalog := &fakeAgentCatalog{
		inventory: agentsvc.Inventory{Supported: []agentsvc.Info{{ID: "codex", Label: "Codex"}}},
		refreshed: agentsvc.Inventory{
			Supported:  []agentsvc.Info{{ID: "codex", Label: "Codex"}},
			Installed:  []agentsvc.Info{{ID: "codex", Label: "Codex"}},
			Authorized: []agentsvc.Info{{ID: "codex", Label: "Codex"}},
		},
	}
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{
		Agents: catalog,
	}, httpd.ControlDeps{}))
	defer srv.Close()

	body, status, _ := doRequest(t, srv, http.MethodPost, "/api/v1/agents/refresh", "")
	if status != http.StatusOK {
		t.Fatalf("POST /agents/refresh = %d, body=%s", status, body)
	}
	for _, want := range []string{`"supported"`, `"installed"`, `"authorized"`, `"id":"codex"`} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("body missing %s: %s", want, body)
		}
	}
	if catalog.listCalls != 0 || catalog.refreshCalls != 1 {
		t.Fatalf("calls: list=%d refresh=%d, want list=0 refresh=1", catalog.listCalls, catalog.refreshCalls)
	}
}

func TestProbeAgent(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	catalog := &fakeAgentCatalog{
		probed: agentsvc.ProbeResult{
			Agent:     agentsvc.Info{ID: "codex", Label: "Codex", AuthStatus: "authorized"},
			Supported: true,
			Installed: true,
		},
	}
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{
		Agents: catalog,
	}, httpd.ControlDeps{}))
	defer srv.Close()

	body, status, _ := doRequest(t, srv, http.MethodPost, "/api/v1/agents/codex/probe", "")
	if status != http.StatusOK {
		t.Fatalf("POST /agents/codex/probe = %d, body=%s", status, body)
	}
	for _, want := range []string{`"supported":true`, `"installed":true`, `"id":"codex"`, `"authStatus":"authorized"`} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("body missing %s: %s", want, body)
		}
	}
	if catalog.probeCalls != 1 || catalog.probeAgent != "codex" {
		t.Fatalf("probe calls=%d agent=%q, want one codex probe", catalog.probeCalls, catalog.probeAgent)
	}
}
