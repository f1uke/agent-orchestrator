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
	simsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/sim"
	"github.com/aoagents/agent-orchestrator/backend/internal/simbridge"
)

const testSimUDID = "087DF306-1FC9-4E5A-B9ED-AD36D6A1A0F1"

type fakeSimService struct {
	leases     []domain.SimLease
	acquireErr error
	releaseErr error

	holdErr        error
	releaseHoldErr error
	takeOverErr    error
	tookOver       bool

	gotSession domain.SessionID
	gotUDID    string
	gotTTL     time.Duration
	gotToken   string
	// gotPerformed is what the last ReleaseHold call actually received - see
	// TestReleaseSimHold_PerformedDefaultsTrueWhenQueryParamIsAbsent.
	gotPerformed bool
	// gotEnd is where the last ReleaseHold call said the gesture ended, which
	// only a drag reports - see TestSimGesture_DragReleasesWithItsRealEndPoint.
	gotEnd *simbridge.Point
	// holds counts grants. A drag that took one per move would still look right
	// on the device and be wrong about arbitration, so it is counted.
	holds int
	// gotIntent is what the last AcquireHold was told the caller is about to
	// do. It is what a recording is built from, so a gesture that reaches the
	// device correctly and describes itself wrongly is a bug this catches - see
	// TestSimGesture_APinchIsRecordedAtThePointBetweenItsFingers.
	gotIntent simsvc.GestureIntent
}

func (f *fakeSimService) AcquireHold(_ context.Context, sessionID domain.SessionID, udid string, ttl time.Duration, intent simsvc.GestureIntent) (domain.SimHold, error) {
	f.gotSession, f.gotUDID, f.gotTTL, f.gotIntent = sessionID, udid, ttl, intent
	if f.holdErr != nil {
		return domain.SimHold{}, f.holdErr
	}
	f.holds++
	now := time.Date(2026, 8, 13, 7, 41, 2, 0, time.UTC)
	return domain.SimHold{UDID: udid, SessionID: sessionID, Token: "tok-fake", ExpiresAt: now.Add(ttl)}, nil
}

func (f *fakeSimService) ReleaseHold(_ context.Context, udid, token string, outcome simsvc.GestureOutcome) error {
	f.gotUDID, f.gotToken, f.gotPerformed = udid, token, outcome.Performed
	f.gotEnd = outcome.End
	return f.releaseHoldErr
}

func (f *fakeSimService) Acquire(_ context.Context, sessionID domain.SessionID, udid string, ttl time.Duration) (domain.SimLease, error) {
	f.gotSession, f.gotUDID, f.gotTTL = sessionID, udid, ttl
	if f.acquireErr != nil {
		return domain.SimLease{}, f.acquireErr
	}
	now := time.Date(2026, 8, 13, 7, 41, 2, 0, time.UTC)
	return domain.SimLease{UDID: udid, SessionID: sessionID, AcquiredAt: now, ExpiresAt: now.Add(ttl)}, nil
}

// TakeOver records that the takeover path was the one taken, which is what the
// route has to get right: the two refuse for different reasons.
func (f *fakeSimService) TakeOver(_ context.Context, sessionID domain.SessionID, udid string, ttl time.Duration) (domain.SimLease, error) {
	f.gotSession, f.gotUDID, f.gotTTL = sessionID, udid, ttl
	f.tookOver = true
	if f.takeOverErr != nil {
		return domain.SimLease{}, f.takeOverErr
	}
	now := time.Date(2026, 8, 13, 7, 41, 2, 0, time.UTC)
	return domain.SimLease{UDID: udid, SessionID: sessionID, AcquiredAt: now, ExpiresAt: now.Add(ttl)}, nil
}

func (f *fakeSimService) Release(_ context.Context, sessionID domain.SessionID, udid string) error {
	f.gotSession, f.gotUDID = sessionID, udid
	return f.releaseErr
}

func (f *fakeSimService) List(context.Context) ([]domain.SimLease, error) { return f.leases, nil }

func newSimTestServer(t *testing.T, svc simsvc.Manager) *httptest.Server {
	t.Helper()
	return newSimTestServerIn(t, svc, "")
}

// newSimTestServerIn is the same server with a data directory, which is what
// turns stopping a recording into a written flow and gives the sim-flows
// routes somewhere to read.
func newSimTestServerIn(t *testing.T, svc simsvc.Manager, dataDir string) *httptest.Server {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(
		config.Config{DataDir: dataDir}, log, nil, httpd.APIDeps{Sim: svc}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	return srv
}

func TestSimNilServiceReturns501(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	if _, status, _ := doRequest(t, srv, "GET", "/api/v1/sim/leases", ""); status != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", status)
	}
}

func TestSimListLeases(t *testing.T) {
	now := time.Date(2026, 8, 13, 7, 41, 2, 0, time.UTC)
	svc := &fakeSimService{leases: []domain.SimLease{
		{UDID: testSimUDID, SessionID: "mer-7", AcquiredAt: now, ExpiresAt: now.Add(10 * time.Minute)},
	}}
	srv := newSimTestServer(t, svc)
	body, status, headers := doRequest(t, srv, "GET", "/api/v1/sim/leases", "")
	assertJSON(t, headers)
	if status != http.StatusOK {
		t.Fatalf("status = %d body=%s", status, body)
	}
	for _, want := range []string{`"leases"`, testSimUDID, `"mer-7"`, `"expiresAt"`} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("body missing %s: %s", want, body)
		}
	}
}

func TestSimAcquirePassesSessionAndTTL(t *testing.T) {
	svc := &fakeSimService{}
	srv := newSimTestServer(t, svc)
	body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions/mer-7/sim-leases",
		`{"udid":"`+testSimUDID+`","ttlSeconds":45}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d body=%s", status, body)
	}
	if svc.gotSession != "mer-7" || svc.gotUDID != testSimUDID {
		t.Fatalf("service got session=%q udid=%q", svc.gotSession, svc.gotUDID)
	}
	if svc.gotTTL != 45*time.Second {
		t.Fatalf("ttl = %s, want 45s", svc.gotTTL)
	}
	if !strings.Contains(string(body), `"lease"`) {
		t.Fatalf("body missing lease: %s", body)
	}
}

func TestSimAcquireOmittedTTLIsTheServiceDefault(t *testing.T) {
	svc := &fakeSimService{}
	srv := newSimTestServer(t, svc)
	if _, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions/mer-7/sim-leases", `{"udid":"`+testSimUDID+`"}`); status != http.StatusOK {
		t.Fatalf("status = %d", status)
	}
	if svc.gotTTL != 0 {
		t.Fatalf("ttl = %s, want 0 so the service picks its default", svc.gotTTL)
	}
}

// Contention must be a distinct, machine-readable outcome: a 409 whose details
// name the holder, so a caller can never mistake it for a transient failure and
// proceed anyway.
func TestSimAcquireConflictNamesTheHolder(t *testing.T) {
	now := time.Date(2026, 8, 13, 7, 41, 2, 0, time.UTC)
	svc := &fakeSimService{acquireErr: &simsvc.HeldError{
		Lease: domain.SimLease{UDID: testSimUDID, SessionID: "mer-3", AcquiredAt: now, ExpiresAt: now.Add(7 * time.Minute)},
		Now:   now,
	}}
	srv := newSimTestServer(t, svc)
	body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions/mer-7/sim-leases", `{"udid":"`+testSimUDID+`"}`)
	assertErrorCode(t, body, status, http.StatusConflict, "SIM_DEVICE_LEASED")
	for _, want := range []string{"mer-3", `"holder"`, `"expiresAt"`} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("409 body missing %s: %s", want, body)
		}
	}
}

func TestSimAcquireInvalidIs422(t *testing.T) {
	svc := &fakeSimService{acquireErr: simsvc.ErrInvalid}
	srv := newSimTestServer(t, svc)
	body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions/mer-7/sim-leases", `{"udid":"x"}`)
	assertErrorCode(t, body, status, http.StatusUnprocessableEntity, "SIM_INVALID")
}

func TestSimAcquireUnknownSessionIs404(t *testing.T) {
	svc := &fakeSimService{acquireErr: simsvc.ErrNotFound}
	srv := newSimTestServer(t, svc)
	body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions/nope/sim-leases", `{"udid":"`+testSimUDID+`"}`)
	assertErrorCode(t, body, status, http.StatusNotFound, "SIM_NOT_FOUND")
}

func TestSimReleaseTakesTheUDIDFromThePath(t *testing.T) {
	svc := &fakeSimService{}
	srv := newSimTestServer(t, svc)
	body, status, _ := doRequest(t, srv, "DELETE", "/api/v1/sessions/mer-7/sim-leases/"+testSimUDID, "")
	if status != http.StatusOK {
		t.Fatalf("status = %d body=%s", status, body)
	}
	if svc.gotSession != "mer-7" || svc.gotUDID != testSimUDID {
		t.Fatalf("service got session=%q udid=%q", svc.gotSession, svc.gotUDID)
	}
	if !strings.Contains(string(body), `"released":true`) {
		t.Fatalf("body = %s, want released:true", body)
	}
}

func TestSimReleaseByNonHolderIs409(t *testing.T) {
	now := time.Date(2026, 8, 13, 7, 41, 2, 0, time.UTC)
	svc := &fakeSimService{releaseErr: &simsvc.HeldError{
		Lease: domain.SimLease{UDID: testSimUDID, SessionID: "mer-3", ExpiresAt: now.Add(time.Minute)},
		Now:   now,
	}}
	srv := newSimTestServer(t, svc)
	body, status, _ := doRequest(t, srv, "DELETE", "/api/v1/sessions/mer-7/sim-leases/"+testSimUDID, "")
	assertErrorCode(t, body, status, http.StatusConflict, "SIM_DEVICE_LEASED")
}

// The route has to take the right path: an ordinary claim and a takeover refuse
// for different reasons, so which one ran is not a detail.
func TestAcquireSimLease_TakeOverGoesThroughTheTakeoverPath(t *testing.T) {
	svc := &fakeSimService{}
	srv := newSimTestServer(t, svc)

	code, out := postJSON(t, srv.URL+"/api/v1/sessions/p-1/sim-leases",
		map[string]any{"udid": testSimUDID, "takeOver": true})
	if code != http.StatusOK {
		t.Fatalf("status %d: %v", code, out)
	}
	if !svc.tookOver {
		t.Fatal("takeOver must claim a device another session holds, not make an ordinary claim that is refused")
	}
}

func TestAcquireSimLease_WithoutTakeOverMakesAnOrdinaryClaim(t *testing.T) {
	svc := &fakeSimService{}
	srv := newSimTestServer(t, svc)

	if code, out := postJSON(t, srv.URL+"/api/v1/sessions/p-1/sim-leases",
		map[string]any{"udid": testSimUDID}); code != http.StatusOK {
		t.Fatalf("status %d: %v", code, out)
	}
	if svc.tookOver {
		t.Fatal("a claim that did not ask to take over must not take one")
	}
}
