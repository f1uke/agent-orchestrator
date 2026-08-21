package controllers_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	crewrunsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/crewrun"
)

type fakeCrewRunService struct {
	started  crewrunsvc.StartInput
	ended    crewrunsvc.EndInput
	startRes crewrunsvc.StartResult
	endRes   crewrunsvc.EndResult
	runs     []domain.CrewRun
	err      error
}

func (f *fakeCrewRunService) Start(_ context.Context, _ domain.SessionID, in crewrunsvc.StartInput) (crewrunsvc.StartResult, error) {
	f.started = in
	return f.startRes, f.err
}

func (f *fakeCrewRunService) End(_ context.Context, _ domain.SessionID, in crewrunsvc.EndInput) (crewrunsvc.EndResult, error) {
	f.ended = in
	return f.endRes, f.err
}

func (f *fakeCrewRunService) List(context.Context, domain.SessionID) ([]domain.CrewRun, error) {
	return f.runs, f.err
}

func newCrewRunTestServer(t *testing.T, svc crewrunsvc.Manager) *httptest.Server {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{CrewRuns: svc}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	return srv
}

// A daemon with no detector wired answers 501 rather than a quiet success. A
// bracket that silently does nothing is a detector that misses.
func TestCrewRunNilServiceReturns501(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	for _, req := range [][2]string{
		{"GET", "/api/v1/sessions/w1/crew/runs"},
		{"POST", "/api/v1/sessions/w1/crew/runs"},
		{"POST", "/api/v1/sessions/w1/crew/runs/end"},
	} {
		_, status, _ := doRequest(t, srv, req[0], req[1], `{}`)
		if status != http.StatusNotImplemented {
			t.Fatalf("%s %s status = %d, want 501", req[0], req[1], status)
		}
	}
}

func TestCrewRunStartMapsKindAndReportsTheDetector(t *testing.T) {
	svc := &fakeCrewRunService{startRes: crewrunsvc.StartResult{
		Run:       domain.CrewRun{ID: "r1", Kind: domain.CrewRunBuild, Detector: domain.CrewRunDetectorDown, DetectorReason: "no watcher"},
		Certified: false,
	}}
	srv := newCrewRunTestServer(t, svc)
	body, status, headers := doRequest(t, srv, "POST", "/api/v1/sessions/w1/crew/runs", `{"kind":"build","label":"npm run build"}`)
	assertJSON(t, headers)
	if status != http.StatusCreated {
		t.Fatalf("status = %d body=%s", status, body)
	}
	if svc.started.Kind != domain.CrewRunBuild || svc.started.Label != "npm run build" {
		t.Fatalf("start input = %+v", svc.started)
	}
	for _, want := range []string{`"certified":false`, `"detectorReason":"no watcher"`} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("body missing %s: %s", want, body)
		}
	}
}

func TestCrewRunEndReportsTheThirdStateAndTheCap(t *testing.T) {
	ended := time.Now().UTC()
	svc := &fakeCrewRunService{endRes: crewrunsvc.EndResult{
		Run: domain.CrewRun{
			ID: "r1", Outcome: domain.CrewRunDiscarded, Result: domain.CrewRunResultPass,
			EndedAt: &ended, ChangedPaths: []string{"a.go"},
		},
		Retry: true, Attempt: 1, MaxAttempts: domain.CappedRepeat,
	}}
	srv := newCrewRunTestServer(t, svc)
	body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions/w1/crew/runs/end", `{"result":"pass"}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d body=%s", status, body)
	}
	if svc.ended.Result != domain.CrewRunResultPass {
		t.Fatalf("end input = %+v", svc.ended)
	}
	for _, want := range []string{`"outcome":"discarded"`, `"retry":true`, `"maxAttempts":3`, `"a.go"`} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("body missing %s: %s", want, body)
		}
	}
}

// `ao crew run --end` with no flags sends no body; that must not be a 400.
func TestCrewRunEndAcceptsAnEmptyBody(t *testing.T) {
	svc := &fakeCrewRunService{}
	srv := newCrewRunTestServer(t, svc)
	_, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions/w1/crew/runs/end", "")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
}

func TestCrewRunListReturnsRunsNewestFirst(t *testing.T) {
	svc := &fakeCrewRunService{runs: []domain.CrewRun{{ID: "r2", Kind: domain.CrewRunTest}, {ID: "r1", Kind: domain.CrewRunBuild}}}
	srv := newCrewRunTestServer(t, svc)
	body, status, _ := doRequest(t, srv, "GET", "/api/v1/sessions/w1/crew/runs", "")
	if status != http.StatusOK {
		t.Fatalf("status = %d body=%s", status, body)
	}
	if !strings.Contains(string(body), `"r2"`) || !strings.Contains(string(body), `"r1"`) {
		t.Fatalf("body missing the runs: %s", body)
	}
}

func TestCrewRunServiceErrorsMapToStatuses(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
	}{
		{crewrunsvc.ErrInvalid, http.StatusUnprocessableEntity},
		{crewrunsvc.ErrNotFound, http.StatusNotFound},
	} {
		svc := &fakeCrewRunService{err: tc.err}
		srv := newCrewRunTestServer(t, svc)
		_, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions/w1/crew/runs", `{"kind":"test"}`)
		if status != tc.status {
			t.Fatalf("%v mapped to %d, want %d", tc.err, status, tc.status)
		}
	}
}
