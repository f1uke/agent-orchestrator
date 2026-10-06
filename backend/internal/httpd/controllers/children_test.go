package controllers_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	childrensvc "github.com/aoagents/agent-orchestrator/backend/internal/service/children"
)

type fakeChildrenService struct {
	created   childrensvc.CreateInput
	createErr error
	stop      childrensvc.StopOutcome
	notes     []string
}

func (f *fakeChildrenService) List(context.Context, domain.SessionID) ([]domain.SessionChild, error) {
	return nil, nil
}

func (f *fakeChildrenService) Create(_ context.Context, id domain.SessionID, in childrensvc.CreateInput) (domain.SessionChild, error) {
	f.created = in
	return domain.SessionChild{SessionID: id, AgentID: "a1", State: domain.ChildRunning, WorktreePath: "/children/a1"}, f.createErr
}

func (f *fakeChildrenService) Brief(context.Context, domain.SessionID, string) (string, bool, error) {
	return "brief", true, nil
}

func (f *fakeChildrenService) Stop(context.Context, domain.SessionID, string) (childrensvc.StopOutcome, error) {
	return f.stop, nil
}

func (f *fakeChildrenService) Describe(context.Context, domain.SessionID, string, string, string) error {
	return nil
}

func (f *fakeChildrenService) Notes(context.Context, domain.SessionID) ([]string, error) {
	return f.notes, nil
}

func newChildrenTestServer(t *testing.T, svc *fakeChildrenService) *httptest.Server {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{Children: svc}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	return srv
}

func TestCreateChildMapsRefusalsToStatuses(t *testing.T) {
	cases := []struct {
		err    error
		status int
		code   string
	}{
		{nil, http.StatusCreated, ""},
		{fmt.Errorf("%w: %q", childrensvc.ErrBadName, "manual"), http.StatusUnprocessableEntity, "NOT_A_SUBAGENT_WORKTREE"},
		{childrensvc.ErrNested, http.StatusUnprocessableEntity, "NESTED_CHILD_REFUSED"},
		{childrensvc.ErrNotWorker, http.StatusConflict, "NOT_A_LIVE_WORKER"},
		{childrensvc.ErrSessionNotFound, http.StatusNotFound, "SESSION_NOT_FOUND"},
	}
	for _, tc := range cases {
		svc := &fakeChildrenService{createErr: tc.err}
		srv := newChildrenTestServer(t, svc)
		resp, err := http.Post(srv.URL+"/api/v1/sessions/w1/children", "application/json", strings.NewReader(`{"name":"agent-a1","cwd":"/ws"}`))
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != tc.status || !strings.Contains(string(body), tc.code) {
			t.Errorf("err %v: status %d body %s, want %d %s", tc.err, resp.StatusCode, body, tc.status, tc.code)
		}
		if svc.created.Name != "agent-a1" || svc.created.Cwd != "/ws" {
			t.Errorf("service got %+v", svc.created)
		}
	}
}

func TestStopChildCarriesTheBlockForTheHook(t *testing.T) {
	svc := &fakeChildrenService{stop: childrensvc.StopOutcome{Known: true, Block: true, Reason: "commit first", Child: domain.SessionChild{AgentID: "a1", State: domain.ChildRunning}}}
	srv := newChildrenTestServer(t, svc)
	resp, err := http.Post(srv.URL+"/api/v1/sessions/w1/children/a1/stop", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var got struct {
		Known  bool   `json:"known"`
		Block  bool   `json:"block"`
		Reason string `json:"reason"`
		Child  *struct {
			State string `json:"state"`
		} `json:"child"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || !got.Known || !got.Block || got.Reason != "commit first" || got.Child == nil || got.Child.State != "running" {
		t.Fatalf("stop = %d %+v", resp.StatusCode, got)
	}
}

func TestChildNotesAnswersAnEmptyListNotNull(t *testing.T) {
	srv := newChildrenTestServer(t, &fakeChildrenService{})
	resp, err := http.Post(srv.URL+"/api/v1/sessions/w1/children/notes", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || strings.TrimSpace(string(body)) != `{"notes":[]}` {
		t.Fatalf("notes = %d %s", resp.StatusCode, body)
	}
}
