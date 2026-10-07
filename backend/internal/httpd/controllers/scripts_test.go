package controllers_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/scriptstore"
)

type fakeScriptsService struct {
	asked   domain.SessionID
	publish ports.ScriptsPublishResult
	err     error
}

var scriptsRow = domain.ScriptsStoreWorktree{
	SessionID: "nter-1", Store: "/store", Path: "/data/store-worktrees/store/nter-1", Branch: "ao/nter-1",
	BaseBranch: "main", State: domain.ScriptsStoreActive, Uncommitted: []string{"projects/nter/draft.yaml"}, Unpublished: 2,
}

func (f *fakeScriptsService) Status(_ context.Context, session domain.SessionID) (scriptstore.Status, error) {
	f.asked = session
	return scriptstore.Status{Worktree: scriptsRow, StoreDirty: []string{"README.md"}}, f.err
}

func (f *fakeScriptsService) Publish(_ context.Context, session domain.SessionID) (scriptstore.Published, error) {
	f.asked = session
	return scriptstore.Published{Worktree: scriptsRow, Result: f.publish}, f.err
}

func newScriptsTestServer(t *testing.T, svc controllers.ScriptsService) *httptest.Server {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{Scripts: svc}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	return srv
}

func TestScriptsStatusAndPublish(t *testing.T) {
	svc := &fakeScriptsService{publish: ports.ScriptsPublishResult{Outcome: ports.PublishRefused, Hold: domain.HoldPublishConflict, Detail: "ao/nter-1 conflicts with main", Files: []string{"projects/nter/login.yaml"}}}
	srv := newScriptsTestServer(t, svc)

	resp, err := http.Get(srv.URL + "/api/v1/sessions/nter-2/scripts")
	if err != nil {
		t.Fatal(err)
	}
	var st controllers.ScriptsStatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || svc.asked != "nter-2" || st.Worktree.Owner != "nter-1" || st.Worktree.Unpublished != 2 || len(st.StoreDirty) != 1 {
		t.Fatalf("status %d %+v (asked %s)", resp.StatusCode, st, svc.asked)
	}

	resp, err = http.Post(srv.URL+"/api/v1/sessions/nter-2/scripts/publish", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	var pub controllers.ScriptsPublishResponse
	if err := json.NewDecoder(resp.Body).Decode(&pub); err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || pub.Outcome != ports.PublishRefused || pub.Hold != domain.HoldPublishConflict || len(pub.Files) != 1 {
		t.Fatalf("publish %d %+v; a refusal is an answer carrying its files", resp.StatusCode, pub)
	}
}

func TestScriptsErrorsMapToStatuses(t *testing.T) {
	cases := map[error]struct {
		status int
		code   string
	}{
		fmt.Errorf("%w: nter-9", scriptstore.ErrSessionNotFound): {http.StatusNotFound, "SESSION_NOT_FOUND"},
		scriptstore.ErrNoWorktree:                                {http.StatusNotFound, "SCRIPTS_STORE_WORKTREE_NOT_FOUND"},
	}
	for err, want := range cases {
		srv := newScriptsTestServer(t, &fakeScriptsService{err: err})
		resp, getErr := http.Get(srv.URL + "/api/v1/sessions/nter-9/scripts")
		if getErr != nil {
			t.Fatal(getErr)
		}
		var body struct {
			Code string `json:"code"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&body)
		_ = resp.Body.Close()
		if resp.StatusCode != want.status || body.Code != want.code {
			t.Errorf("%v: %d %s, want %d %s", err, resp.StatusCode, body.Code, want.status, want.code)
		}
	}

	srv := newScriptsTestServer(t, nil)
	resp, err := http.Post(srv.URL+"/api/v1/sessions/nter-9/scripts/publish", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotImplemented {
		t.Fatalf("unwired publish = %d, want 501", resp.StatusCode)
	}
}
