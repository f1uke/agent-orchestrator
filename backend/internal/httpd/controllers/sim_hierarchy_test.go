package controllers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/simbridge"
	"github.com/aoagents/agent-orchestrator/backend/internal/simrunner"
)

const hierarchyUDID = "087DF306-1FC9-4E5A-B9ED-AD36D6A1A0F1"

type fakeSimRunner struct {
	udid   string
	wait   time.Duration
	tree   simbridge.XCTestHierarchy
	status simrunner.Status
	err    error
}

func (f *fakeSimRunner) Read(_ context.Context, udid string, wait time.Duration) (simbridge.XCTestHierarchy, simrunner.Status, error) {
	f.udid, f.wait = udid, wait
	return f.tree, f.status, f.err
}

func hierarchyServer(t *testing.T, runner SimRunner) *httptest.Server {
	t.Helper()
	r := chi.NewRouter()
	(&SimHierarchyController{Runner: runner}).Register(r)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv
}

func getHierarchy(t *testing.T, url string) (SimHierarchyResponse, int) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var out SimHierarchyResponse
	if resp.StatusCode == http.StatusOK {
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatal(err)
		}
	}
	return out, resp.StatusCode
}

func TestSimHierarchy_AReadyRunnerAnswersWithTheTree(t *testing.T) {
	runner := &fakeSimRunner{
		tree:   simbridge.XCTestHierarchy{Version: "1", Apps: []simbridge.XCTestApp{{BundleID: "com.example.app"}}},
		status: simrunner.Status{State: simrunner.StateReady},
	}
	srv := hierarchyServer(t, runner)
	out, code := getHierarchy(t, srv.URL+"/sim/devices/"+hierarchyUDID+"/hierarchy?waitMs=1500")
	if code != http.StatusOK || out.Runner.State != "ready" || out.Hierarchy == nil || out.Hierarchy.Apps[0].BundleID != "com.example.app" {
		t.Fatalf("code %d, %+v", code, out)
	}
	if runner.udid != hierarchyUDID || runner.wait != 1500*time.Millisecond {
		t.Fatalf("asked for %q waiting %s", runner.udid, runner.wait)
	}
}

// Not ready is an ANSWER, not an error: the caller falls back to the bridge
// and needs the reason to say why.
func TestSimHierarchy_NotReadyCarriesTheReason(t *testing.T) {
	runner := &fakeSimRunner{
		status: simrunner.Status{State: simrunner.StateOff, Reason: "nobody holds it"},
		err:    simrunner.ErrNotReady,
	}
	out, code := getHierarchy(t, hierarchyServer(t, runner).URL+"/sim/devices/"+hierarchyUDID+"/hierarchy")
	if code != http.StatusOK || out.Runner.State != "off" || out.Runner.Reason != "nobody holds it" || out.Hierarchy != nil {
		t.Fatalf("code %d, %+v", code, out)
	}
	if runner.wait != 0 {
		t.Fatalf("waited %s without being asked to", runner.wait)
	}
}

func TestSimHierarchy_WithoutARunnerIsUnavailable(t *testing.T) {
	out, code := getHierarchy(t, hierarchyServer(t, nil).URL+"/sim/devices/"+hierarchyUDID+"/hierarchy")
	if code != http.StatusOK || out.Runner.State != "unavailable" || out.Runner.Reason == "" {
		t.Fatalf("code %d, %+v", code, out)
	}
}

func TestSimHierarchy_TheWaitIsValidatedAndCapped(t *testing.T) {
	runner := &fakeSimRunner{status: simrunner.Status{State: simrunner.StateStarting}, err: simrunner.ErrNotReady}
	srv := hierarchyServer(t, runner)
	for _, bad := range []string{"-1", "soon"} {
		if _, code := getHierarchy(t, srv.URL+"/sim/devices/"+hierarchyUDID+"/hierarchy?waitMs="+bad); code != http.StatusBadRequest {
			t.Errorf("waitMs=%s answered %d, want 400", bad, code)
		}
	}
	if _, code := getHierarchy(t, srv.URL+"/sim/devices/"+hierarchyUDID+"/hierarchy?waitMs=999999"); code != http.StatusOK || runner.wait != maxHierarchyWait {
		t.Fatalf("code %d, waited %s, want the cap", code, runner.wait)
	}
}
