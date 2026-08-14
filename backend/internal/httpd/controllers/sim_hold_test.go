package controllers_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	simsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/sim"
)

func TestAcquireSimHold_ReturnsTheToken(t *testing.T) {
	svc := &fakeSimService{}
	srv := newSimTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions/mer-1/sim-leases/"+testSimUDID+"/hold", `{"holdSeconds":20}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", status, body)
	}
	var res struct {
		Hold domain.SimHold `json:"hold"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if res.Hold.Token == "" {
		t.Fatalf("hold token missing from %s", body)
	}
	if svc.gotTTL != 20*time.Second {
		t.Fatalf("ttl = %s, want 20s", svc.gotTTL)
	}
	if svc.gotUDID != testSimUDID || svc.gotSession != "mer-1" {
		t.Fatalf("service saw session %q udid %q", svc.gotSession, svc.gotUDID)
	}
}

func TestAcquireSimHold_OmittedTTLIsLeftToTheService(t *testing.T) {
	svc := &fakeSimService{}
	srv := newSimTestServer(t, svc)

	if _, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions/mer-1/sim-leases/"+testSimUDID+"/hold", `{}`); status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if svc.gotTTL != 0 {
		t.Fatalf("ttl = %s, want 0 so the default lives in one place", svc.gotTTL)
	}
}

func TestAcquireSimHold_RefusalIs409WithAnActionableReason(t *testing.T) {
	now := time.Date(2026, 8, 13, 7, 41, 2, 0, time.UTC)
	cases := []struct {
		name   string
		err    error
		reason simsvc.HoldRefusedReason
	}{
		{"mid-gesture", &simsvc.HoldRefusedError{UDID: testSimUDID, Reason: simsvc.HoldRefusedBusy, Now: now}, simsvc.HoldRefusedBusy},
		{"unclaimed", &simsvc.HoldRefusedError{UDID: testSimUDID, Reason: simsvc.HoldRefusedNotLeased, Now: now}, simsvc.HoldRefusedNotLeased},
		{"someone else", &simsvc.HoldRefusedError{
			UDID:   testSimUDID,
			Reason: simsvc.HoldRefusedLeasedByOther,
			Lease:  domain.SimLease{UDID: testSimUDID, SessionID: "mer-9", ExpiresAt: now.Add(7 * time.Minute)},
			Now:    now,
		}, simsvc.HoldRefusedLeasedByOther},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := &fakeSimService{holdErr: tc.err}
			srv := newSimTestServer(t, svc)

			body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions/mer-1/sim-leases/"+testSimUDID+"/hold", `{}`)
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
			if res.Code != "SIM_DEVICE_BUSY" {
				t.Fatalf("code = %q, want SIM_DEVICE_BUSY: %s", res.Code, body)
			}
			if got, _ := res.Details["reason"].(string); got != string(tc.reason) {
				t.Fatalf("details.reason = %q, want %q", got, tc.reason)
			}
		})
	}
}

func TestAcquireSimHold_LeasedByAnotherSessionNamesTheHolder(t *testing.T) {
	now := time.Date(2026, 8, 13, 7, 41, 2, 0, time.UTC)
	svc := &fakeSimService{holdErr: &simsvc.HoldRefusedError{
		UDID:   testSimUDID,
		Reason: simsvc.HoldRefusedLeasedByOther,
		Lease:  domain.SimLease{UDID: testSimUDID, SessionID: "mer-9", ExpiresAt: now.Add(7 * time.Minute)},
		Now:    now,
	}}
	srv := newSimTestServer(t, svc)

	body, _, _ := doRequest(t, srv, "POST", "/api/v1/sessions/mer-1/sim-leases/"+testSimUDID+"/hold", `{}`)
	if !strings.Contains(string(body), "mer-9") || !strings.Contains(string(body), "expiresAt") {
		t.Fatalf("a refusal must carry the holder and when it lapses: %s", body)
	}
}

func TestReleaseSimHold_HappyPathAndUnknownToken(t *testing.T) {
	svc := &fakeSimService{}
	srv := newSimTestServer(t, svc)

	if _, status, _ := doRequest(t, srv, "DELETE", "/api/v1/sessions/mer-1/sim-leases/"+testSimUDID+"/hold/tok-1", ""); status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if svc.gotToken != "tok-1" {
		t.Fatalf("token = %q, want tok-1", svc.gotToken)
	}

	svc.releaseHoldErr = simsvc.ErrNotFound
	if _, status, _ := doRequest(t, srv, "DELETE", "/api/v1/sessions/mer-1/sim-leases/"+testSimUDID+"/hold/tok-2", ""); status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", status)
	}
}

func TestSimHoldNilServiceReturns501(t *testing.T) {
	srv := newSimTestServer(t, nil)
	if _, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions/mer-1/sim-leases/"+testSimUDID+"/hold", `{}`); status != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", status)
	}
	if _, status, _ := doRequest(t, srv, "DELETE", "/api/v1/sessions/mer-1/sim-leases/"+testSimUDID+"/hold/tok", ""); status != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", status)
	}
}
