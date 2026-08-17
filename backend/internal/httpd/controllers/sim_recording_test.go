package controllers_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	simsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/sim"
)

// fakeSimServiceWithRecording adds the recording surface on top of
// fakeSimService, so lease/hold tests are not forced to grow methods they
// never exercise while the recording routes still get a full simsvc.Manager +
// simRecordingService double to talk to.
type fakeSimServiceWithRecording struct {
	*fakeSimService

	startRec domain.SimRecording
	startErr error
	gotName  string

	stopRec   domain.SimRecording
	stopSteps []domain.SimRecordingStep
	stopErr   error

	getRec   domain.SimRecording
	getSteps []domain.SimRecordingStep
	getFound bool
	getErr   error
}

func newFakeSimServiceWithRecording() *fakeSimServiceWithRecording {
	return &fakeSimServiceWithRecording{fakeSimService: &fakeSimService{}}
}

func (f *fakeSimServiceWithRecording) StartRecording(_ context.Context, sessionID domain.SessionID, udid, name string) (domain.SimRecording, error) {
	f.gotSession, f.gotUDID, f.gotName = sessionID, udid, name
	if f.startErr != nil {
		return domain.SimRecording{}, f.startErr
	}
	return f.startRec, nil
}

func (f *fakeSimServiceWithRecording) StopRecording(_ context.Context, sessionID domain.SessionID, udid string) (domain.SimRecording, []domain.SimRecordingStep, error) {
	f.gotSession, f.gotUDID = sessionID, udid
	if f.stopErr != nil {
		return domain.SimRecording{}, nil, f.stopErr
	}
	return f.stopRec, f.stopSteps, nil
}

func (f *fakeSimServiceWithRecording) GetRecording(_ context.Context, udid string) (domain.SimRecording, []domain.SimRecordingStep, bool, error) {
	f.gotUDID = udid
	if f.getErr != nil {
		return domain.SimRecording{}, nil, false, f.getErr
	}
	return f.getRec, f.getSteps, f.getFound, nil
}

// intentCapturingSimService wraps fakeSimService to remember the
// simsvc.GestureIntent an AcquireHold call actually received (which is what
// proves the gesture route stopped forwarding a zero value) and the
// `performed` bool a ReleaseHold call actually received (which is what proves
// the gesture route stopped hardcoding true).
type intentCapturingSimService struct {
	*fakeSimService
	gotIntent             simsvc.GestureIntent
	releaseHoldCalls      int
	lastReleasedPerformed bool
}

func (f *intentCapturingSimService) AcquireHold(ctx context.Context, sessionID domain.SessionID, udid string, ttl time.Duration, intent simsvc.GestureIntent) (domain.SimHold, error) {
	f.gotIntent = intent
	return f.fakeSimService.AcquireHold(ctx, sessionID, udid, ttl, intent)
}

func (f *intentCapturingSimService) ReleaseHold(ctx context.Context, udid, token string, performed bool) error {
	f.releaseHoldCalls++
	f.lastReleasedPerformed = performed
	return f.fakeSimService.ReleaseHold(ctx, udid, token, performed)
}

func TestStartSimRecording_ReturnsTheRow(t *testing.T) {
	now := time.Date(2026, 8, 17, 9, 0, 0, 0, time.UTC)
	svc := newFakeSimServiceWithRecording()
	svc.startRec = domain.SimRecording{UDID: testSimUDID, SessionID: "mer-1", Name: "checkout", StartedAt: now, UpdatedAt: now}
	srv := newSimTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions/mer-1/sim-recordings/"+testSimUDID, `{"name":"checkout"}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", status, body)
	}
	var res struct {
		Recording domain.SimRecording `json:"recording"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if res.Recording.Name != "checkout" || res.Recording.UDID != testSimUDID {
		t.Fatalf("recording = %+v, want name checkout on %s", res.Recording, testSimUDID)
	}
	if svc.gotSession != "mer-1" || svc.gotUDID != testSimUDID || svc.gotName != "checkout" {
		t.Fatalf("service saw session %q udid %q name %q", svc.gotSession, svc.gotUDID, svc.gotName)
	}
}

func TestStartSimRecording_OmittedNameIsLeftToTheService(t *testing.T) {
	svc := newFakeSimServiceWithRecording()
	srv := newSimTestServer(t, svc)

	if _, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions/mer-1/sim-recordings/"+testSimUDID, `{}`); status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if svc.gotName != "" {
		t.Fatalf("name = %q, want empty", svc.gotName)
	}
}

func TestStartSimRecording_RefusalIs409WithAnActionableReason(t *testing.T) {
	now := time.Date(2026, 8, 17, 9, 0, 0, 0, time.UTC)
	cases := []struct {
		name   string
		err    error
		reason simsvc.RecordingRefusedReason
	}{
		{"unclaimed", &simsvc.RecordingRefusedError{UDID: testSimUDID, Reason: simsvc.RecordingRefusedNotLeased}, simsvc.RecordingRefusedNotLeased},
		{"someone else", &simsvc.RecordingRefusedError{
			UDID:   testSimUDID,
			Reason: simsvc.RecordingRefusedLeasedByOther,
			Lease:  domain.SimLease{UDID: testSimUDID, SessionID: "mer-9", ExpiresAt: now.Add(7 * time.Minute)},
		}, simsvc.RecordingRefusedLeasedByOther},
		{"already open", &simsvc.RecordingRefusedError{UDID: testSimUDID, Reason: simsvc.RecordingRefusedAlreadyOpen}, simsvc.RecordingRefusedAlreadyOpen},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := newFakeSimServiceWithRecording()
			svc.startErr = tc.err
			srv := newSimTestServer(t, svc)

			body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions/mer-1/sim-recordings/"+testSimUDID, `{}`)
			if status != http.StatusConflict {
				t.Fatalf("status = %d, want 409: %s", status, body)
			}
			var res struct {
				Code    string         `json:"code"`
				Details map[string]any `json:"details"`
			}
			if err := json.Unmarshal(body, &res); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if res.Code != "SIM_RECORDING_REFUSED" {
				t.Fatalf("code = %q, want SIM_RECORDING_REFUSED: %s", res.Code, body)
			}
			if got, _ := res.Details["reason"].(string); got != string(tc.reason) {
				t.Fatalf("details.reason = %q, want %q", got, tc.reason)
			}
		})
	}
}

func TestGetSimRecording_ReturnsRecordingAndSteps(t *testing.T) {
	now := time.Date(2026, 8, 17, 9, 0, 0, 0, time.UTC)
	svc := newFakeSimServiceWithRecording()
	svc.getFound = true
	svc.getRec = domain.SimRecording{UDID: testSimUDID, SessionID: "mer-1", StartedAt: now}
	svc.getSteps = []domain.SimRecordingStep{{Seq: 1, Kind: "tap", X: 0.5, Y: 0.5}}
	srv := newSimTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "GET", "/api/v1/sessions/mer-1/sim-recordings/"+testSimUDID, "")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", status, body)
	}
	var res struct {
		Recording domain.SimRecording       `json:"recording"`
		Steps     []domain.SimRecordingStep `json:"steps"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(res.Steps) != 1 || res.Steps[0].Kind != "tap" {
		t.Fatalf("steps = %+v, want one tap step", res.Steps)
	}
	if res.Recording.UDID != testSimUDID {
		t.Fatalf("recording udid = %q, want %q", res.Recording.UDID, testSimUDID)
	}
}

func TestGetSimRecording_NeverStartedIs404(t *testing.T) {
	svc := newFakeSimServiceWithRecording()
	srv := newSimTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "GET", "/api/v1/sessions/mer-1/sim-recordings/"+testSimUDID, "")
	if status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", status, body)
	}
}

func TestStopSimRecording_ReturnsRecordingAndSteps(t *testing.T) {
	now := time.Date(2026, 8, 17, 9, 0, 0, 0, time.UTC)
	svc := newFakeSimServiceWithRecording()
	svc.stopRec = domain.SimRecording{UDID: testSimUDID, SessionID: "mer-1", StartedAt: now, StoppedAt: &now}
	svc.stopSteps = []domain.SimRecordingStep{{Seq: 1, Kind: "tap"}, {Seq: 2, Kind: "swipe"}}
	srv := newSimTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "DELETE", "/api/v1/sessions/mer-1/sim-recordings/"+testSimUDID, "")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", status, body)
	}
	var res struct {
		Recording domain.SimRecording       `json:"recording"`
		Steps     []domain.SimRecordingStep `json:"steps"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(res.Steps) != 2 {
		t.Fatalf("steps = %+v, want 2", res.Steps)
	}
	if svc.gotSession != "mer-1" || svc.gotUDID != testSimUDID {
		t.Fatalf("service saw session %q udid %q", svc.gotSession, svc.gotUDID)
	}
}

func TestStopSimRecording_NoOpenRecordingIs404(t *testing.T) {
	svc := newFakeSimServiceWithRecording()
	svc.stopErr = simsvc.ErrNotFound
	srv := newSimTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "DELETE", "/api/v1/sessions/mer-1/sim-recordings/"+testSimUDID, "")
	if status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", status, body)
	}
}

func TestSimRecordingNilServiceReturns501(t *testing.T) {
	srv := newSimTestServer(t, nil)
	if _, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions/mer-1/sim-recordings/"+testSimUDID, `{}`); status != http.StatusNotImplemented {
		t.Fatalf("POST status = %d, want 501", status)
	}
	if _, status, _ := doRequest(t, srv, "GET", "/api/v1/sessions/mer-1/sim-recordings/"+testSimUDID, ""); status != http.StatusNotImplemented {
		t.Fatalf("GET status = %d, want 501", status)
	}
	if _, status, _ := doRequest(t, srv, "DELETE", "/api/v1/sessions/mer-1/sim-recordings/"+testSimUDID, ""); status != http.StatusNotImplemented {
		t.Fatalf("DELETE status = %d, want 501", status)
	}
}

// The recording routes' whole point is exposing Task 2's recorder: a Svc that
// satisfies simsvc.Manager but not the recording methods (any ordinary
// fakeSimService) must answer 501 too, the same as a nil one.
func TestSimRecordingServiceWithoutRecordingSupportReturns501(t *testing.T) {
	srv := newSimTestServer(t, &fakeSimService{})
	if _, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions/mer-1/sim-recordings/"+testSimUDID, `{}`); status != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", status)
	}
}

// Without a real intent, AcquireHold sees a zero GestureIntent and the
// recorder captures a step with no kind and no coordinates - a recording that
// "records nothing useful". This is the regression Task 3 exists to close.
func TestSimGesture_ForwardsARealIntentToTheHold(t *testing.T) {
	svc := &intentCapturingSimService{fakeSimService: &fakeSimService{}}
	driver := &fakeDriver{}
	srv := newScreenTestServer(t, svc, &fakeScreen{listing: oneBooted(), driver: driver})

	code, _ := postJSON(t, srv.URL+"/api/v1/sessions/p-1/sim-devices/"+testSimUDID+"/gesture",
		map[string]any{"kind": "tap", "x": 0.25, "y": 0.75})
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if svc.gotIntent.Kind != "tap" || svc.gotIntent.X != 0.25 || svc.gotIntent.Y != 0.75 {
		t.Fatalf("gesture route must forward a real intent, not a zero value: %+v", svc.gotIntent)
	}
}

func TestSimGesture_ForwardsTypedTextInTheIntent(t *testing.T) {
	svc := &intentCapturingSimService{fakeSimService: &fakeSimService{}}
	driver := &fakeDriver{}
	srv := newScreenTestServer(t, svc, &fakeScreen{listing: oneBooted(), driver: driver})

	code, _ := postJSON(t, srv.URL+"/api/v1/sessions/p-1/sim-devices/"+testSimUDID+"/gesture",
		map[string]any{"kind": "type", "text": "hello", "rawKeys": true})
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if svc.gotIntent.Kind != "type" || svc.gotIntent.Text != "hello" {
		t.Fatalf("gesture route must forward the typed text in the intent: %+v", svc.gotIntent)
	}
}

// A successful gesture must be released as performed - the hold must still be
// released the way it always was, but the recorder now has to be told the
// touch actually happened.
func TestSimGesture_SuccessfulGestureReleasesTheHoldAsPerformed(t *testing.T) {
	svc := &intentCapturingSimService{fakeSimService: &fakeSimService{}}
	driver := &fakeDriver{}
	srv := newScreenTestServer(t, svc, &fakeScreen{listing: oneBooted(), driver: driver})

	code, _ := postJSON(t, srv.URL+"/api/v1/sessions/p-1/sim-devices/"+testSimUDID+"/gesture",
		map[string]any{"kind": "tap", "x": 0.25, "y": 0.75})
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if svc.releaseHoldCalls != 1 || !svc.lastReleasedPerformed {
		t.Fatalf("a gesture that reached the device must release the hold as performed: calls=%d performed=%v",
			svc.releaseHoldCalls, svc.lastReleasedPerformed)
	}
}

// This is the defect Fix round 1 closes: a gesture whose perform failed was
// still being released as performed=true, so a recording open on this device
// would keep a step for a touch that never actually reached the screen. The
// hold must still always come back - only what gets recorded changes.
func TestSimGesture_FailedGestureReleasesTheHoldAsNotPerformed(t *testing.T) {
	svc := &intentCapturingSimService{fakeSimService: &fakeSimService{}}
	driver := &fakeDriver{err: errors.New("bridge exploded")}
	srv := newScreenTestServer(t, svc, &fakeScreen{listing: oneBooted(), driver: driver})

	code, _ := postJSON(t, srv.URL+"/api/v1/sessions/p-1/sim-devices/"+testSimUDID+"/gesture",
		map[string]any{"kind": "tap", "x": 0.25, "y": 0.75})
	if code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500 (the gesture must still fail)", code)
	}
	if svc.releaseHoldCalls != 1 {
		t.Fatalf("the hold must still be released even though the gesture failed, got %d calls", svc.releaseHoldCalls)
	}
	if svc.lastReleasedPerformed {
		t.Fatal("a gesture whose perform failed must be released as not performed, so an open recording writes nothing down for it")
	}
}
