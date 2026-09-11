package controllers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/simvideo"
)

// fakeSimVideo answers whatever the test decided, and remembers what it was
// asked, so the controller's own job - routing, the body, and turning the
// recorder's sentinels into codes a CLI can act on - is what is under test.
type fakeSimVideo struct {
	rec simvideo.Recording
	err error

	gotSession     domain.SessionID
	gotUDID        string
	gotMaxDuration time.Duration
}

func (f *fakeSimVideo) Start(_ context.Context, sessionID domain.SessionID, udid string, maxDuration time.Duration) (simvideo.Recording, error) {
	f.gotSession, f.gotUDID, f.gotMaxDuration = sessionID, udid, maxDuration
	return f.rec, f.err
}

func (f *fakeSimVideo) Status(udid string) (simvideo.Recording, error) {
	f.gotUDID = udid
	return f.rec, f.err
}

func (f *fakeSimVideo) Stop(_ context.Context, sessionID domain.SessionID, udid string) (simvideo.Recording, error) {
	f.gotSession, f.gotUDID = sessionID, udid
	return f.rec, f.err
}

func simVideoRouter(svc SimVideoService) chi.Router {
	r := chi.NewRouter()
	(&SimVideoController{Svc: svc}).Register(r)
	return r
}

func simVideoRequest(t *testing.T, r chi.Router, method, body string) *httptest.ResponseRecorder {
	t.Helper()
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, "/sessions/sess-1/sim-videos/"+testSimVideoUDID, reader)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

const testSimVideoUDID = "11111111-2222-3333-4444-555555555555"

func openTestRecording() simvideo.Recording {
	return simvideo.Recording{
		UDID:      testSimVideoUDID,
		SessionID: "sess-1",
		Path:      "/data/sim/sess-1/videos/20260101-120000.000Z-" + testSimVideoUDID + ".mov",
		StartedAt: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC),
		// 10 minutes, so the seconds conversion is visible rather than zero.
		MaxDuration: 10 * time.Minute,
	}
}

func TestStartSimVideo_PassesTheSessionTheDeviceAndTheCap(t *testing.T) {
	svc := &fakeSimVideo{rec: openTestRecording()}
	res := simVideoRequest(t, simVideoRouter(svc), http.MethodPost, `{"maxDurationSeconds":120}`)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", res.Code, res.Body.String())
	}
	if svc.gotSession != "sess-1" {
		t.Errorf("the session in the path must own the recording: %q", svc.gotSession)
	}
	if svc.gotUDID != testSimVideoUDID {
		t.Errorf("udid = %q", svc.gotUDID)
	}
	if svc.gotMaxDuration != 2*time.Minute {
		t.Errorf("maxDurationSeconds must reach the recorder as a duration: %s", svc.gotMaxDuration)
	}

	var body SimVideoResponse
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Video.Path == "" || body.Video.StartedAt == "" {
		t.Errorf("a start must report where the video will be and when it began: %+v", body.Video)
	}
	if body.Video.MaxDurationSeconds != 600 {
		t.Errorf("the cap must be echoed back in seconds so a caller never has to guess the default: %d", body.Video.MaxDurationSeconds)
	}
	if body.Video.StoppedAt != "" || body.Video.StopReason != "" {
		t.Errorf("an open recording must not claim to have stopped: %+v", body.Video)
	}
}

func TestStartSimVideo_AnEmptyBodyIsAStartWithDefaults(t *testing.T) {
	svc := &fakeSimVideo{rec: openTestRecording()}
	res := simVideoRequest(t, simVideoRouter(svc), http.MethodPost, "")

	if res.Code != http.StatusOK {
		t.Fatalf("the common call - start with every default - must not need a body: %d %s", res.Code, res.Body.String())
	}
	if svc.gotMaxDuration != 0 {
		t.Errorf("an omitted cap must reach the recorder as zero, which is what it reads as `the default`: %s", svc.gotMaxDuration)
	}
}

func TestSimVideo_ADeviceSomebodyElseIsRecordingIsA409ThatNamesThem(t *testing.T) {
	held := openTestRecording()
	held.SessionID = "sess-other"
	svc := &fakeSimVideo{err: &simvideo.HeldError{Recording: held}}

	for _, method := range []string{http.MethodPost, http.MethodDelete} {
		res := simVideoRequest(t, simVideoRouter(svc), method, "")
		if res.Code != http.StatusConflict {
			t.Fatalf("%s: status = %d, want 409", method, res.Code)
		}
		var body envelope.APIError
		if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
			t.Fatalf("%s: decode: %v", method, err)
		}
		if body.Code != "SIM_VIDEO_HELD" {
			t.Errorf("%s: code = %q", method, body.Code)
		}
		if body.Details["holder"] != "sess-other" {
			t.Errorf("%s: a refusal must name the holder so the caller never has to ask twice: %v", method, body.Details)
		}
	}
}

func TestGetSimVideo_NothingRecordingIsA404WithItsOwnCode(t *testing.T) {
	// Its own code, because the CLI reads it as an ANSWER for `status` and as a
	// refusal for `stop`: a generic not-found would make the two indistinguishable.
	svc := &fakeSimVideo{err: simvideo.ErrNotFound}
	res := simVideoRequest(t, simVideoRouter(svc), http.MethodGet, "")

	if res.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", res.Code)
	}
	var body envelope.APIError
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Code != "SIM_VIDEO_NOT_FOUND" {
		t.Errorf("code = %q", body.Code)
	}
}

func TestStopSimVideo_ReportsTheFinishedFileAndWhatEndedIt(t *testing.T) {
	stoppedAt := time.Date(2026, 1, 1, 12, 5, 0, 0, time.UTC)
	rec := openTestRecording()
	rec.StoppedAt = &stoppedAt
	rec.StopReason = simvideo.StopReasonMaxDuration
	rec.Bytes = 1234
	svc := &fakeSimVideo{rec: rec}

	res := simVideoRequest(t, simVideoRouter(svc), http.MethodDelete, "")
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", res.Code, res.Body.String())
	}
	var body SimVideoResponse
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Video.Bytes != 1234 {
		t.Errorf("a stop must report the finished file's size: %d", body.Video.Bytes)
	}
	if body.Video.StopReason != string(simvideo.StopReasonMaxDuration) {
		t.Errorf("a recording that hit its cap looks exactly like one you stopped, so the reason must survive the wire: %q", body.Video.StopReason)
	}
	if body.Video.StoppedAt != "2026-01-01T12:05:00Z" {
		t.Errorf("stoppedAt = %q", body.Video.StoppedAt)
	}
}

func TestSimVideo_ADaemonWithNoRecorderSays501RatherThanFailingEveryCall(t *testing.T) {
	r := simVideoRouter(nil)
	for _, method := range []string{http.MethodPost, http.MethodGet, http.MethodDelete} {
		if res := simVideoRequest(t, r, method, ""); res.Code != http.StatusNotImplemented {
			t.Errorf("%s: status = %d, want 501", method, res.Code)
		}
	}
}

func TestSimVideo_AMachineThatCannotRecordAtAllIs501(t *testing.T) {
	// 501 and not 500: a caller that read "no Xcode here" as a transient
	// failure would retry it forever.
	svc := &fakeSimVideo{err: simvideo.ErrUnavailable}
	if res := simVideoRequest(t, simVideoRouter(svc), http.MethodPost, ""); res.Code != http.StatusNotImplemented {
		t.Errorf("status = %d, want 501", res.Code)
	}
}

func TestSimVideo_ARefusedDurationIs422(t *testing.T) {
	svc := &fakeSimVideo{err: simvideo.ErrInvalid}
	if res := simVideoRequest(t, simVideoRouter(svc), http.MethodPost, `{"maxDurationSeconds":99999}`); res.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", res.Code)
	}
}
