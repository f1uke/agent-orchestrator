package controllers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/simctl"
	"github.com/aoagents/agent-orchestrator/backend/internal/simhealth"
)

const doctorUDID = "AAAAAAAA-0000-0000-0000-000000000001"

type doctorSessions struct{ known domain.SessionID }

func (s doctorSessions) Get(_ context.Context, id domain.SessionID) (domain.Session, error) {
	if id != s.known {
		return domain.Session{}, apierr.NotFound("SESSION_NOT_FOUND", "no session "+string(id))
	}
	return domain.Session{}, nil
}

// doctorReaders is a machine with one booted simulator that session doc-1
// holds, and asks which session the lease and CA reads were made for.
func doctorReaders(t *testing.T, asked *[]domain.SessionID) *simhealth.Readers {
	t.Helper()
	dataPath := t.TempDir()
	return &simhealth.Readers{
		Devices: func(context.Context) ([]simctl.Device, error) {
			return []simctl.Device{{UDID: doctorUDID, Name: "iPhone 17", State: simctl.BootedState, DataPath: dataPath}}, nil
		},
		Assigned: func(_ context.Context, id domain.SessionID) (string, error) {
			*asked = append(*asked, id)
			return doctorUDID, nil
		},
		Leases: func(context.Context) ([]domain.SimLease, error) {
			return []domain.SimLease{{UDID: doctorUDID, SessionID: "doc-1", ExpiresAt: time.Now().Add(time.Minute)}}, nil
		},
		CAFiles: func(_ context.Context, id domain.SessionID) ([]string, error) {
			*asked = append(*asked, id)
			return nil, nil
		},
		Trusted: simhealth.TrustStoreHas,
	}
}

func serveDoctor(t *testing.T, c *controllers.SimDoctorController, target string) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	c.Register(r)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	return rec
}

func TestSimDoctor_ReportsEveryLineForTheSessionInThePath(t *testing.T) {
	var asked []domain.SessionID
	c := &controllers.SimDoctorController{Sessions: doctorSessions{known: "doc-1"}, Readers: doctorReaders(t, &asked)}

	rec := serveDoctor(t, c, "/sessions/doc-1/sim-doctor")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Checks []struct{ Name, Status, Message string } `json:"checks"`
		OK     bool                                     `json:"ok"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}
	want := []struct{ name, status string }{
		{"device", "OK"}, {"lease", "OK"}, {"app", "WARN"}, {"proxy CA", "WARN"},
	}
	if len(got.Checks) != len(want) {
		t.Fatalf("checks = %+v, want %d lines", got.Checks, len(want))
	}
	for i, w := range want {
		if got.Checks[i].Name != w.name || got.Checks[i].Status != w.status {
			t.Errorf("line %d = %s %s (%q), want %s %s", i, got.Checks[i].Status, got.Checks[i].Name, got.Checks[i].Message, w.status, w.name)
		}
	}
	if !got.OK {
		t.Error("a report with no FAIL line must be ok")
	}
	for _, id := range asked {
		if id != "doc-1" {
			t.Errorf("a read was made for session %q, not the one in the path", id)
		}
	}
}

func TestSimDoctor_TheQueryReachesTheChecks(t *testing.T) {
	var asked []domain.SessionID
	c := &controllers.SimDoctorController{Sessions: doctorSessions{known: "doc-2"}, Readers: doctorReaders(t, &asked)}

	// doc-1 holds the device, and nothing is installed on it.
	rec := serveDoctor(t, c, "/sessions/doc-2/sim-doctor?udid="+doctorUDID+"&app=com.example.app")
	var got controllers.SimDoctorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}
	if got.OK || len(got.Checks) != 4 || got.Checks[1].Status != "FAIL" || got.Checks[2].Status != "FAIL" {
		t.Fatalf("want the lease (another session's) and app (not installed) lines failing, got %+v", got)
	}
}

func TestSimDoctor_RefusesWhatItCannotAnswer(t *testing.T) {
	var asked []domain.SessionID
	readers := doctorReaders(t, &asked)
	cases := []struct {
		name   string
		c      *controllers.SimDoctorController
		target string
		status int
	}{
		{"unknown session", &controllers.SimDoctorController{Sessions: doctorSessions{known: "doc-1"}, Readers: readers}, "/sessions/nope-9/sim-doctor", http.StatusNotFound},
		{"expect without app", &controllers.SimDoctorController{Sessions: doctorSessions{known: "doc-1"}, Readers: readers}, "/sessions/doc-1/sim-doctor?expect=/tmp/App.app", http.StatusBadRequest},
		{"relative expect", &controllers.SimDoctorController{Sessions: doctorSessions{known: "doc-1"}, Readers: readers}, "/sessions/doc-1/sim-doctor?app=com.example.app&expect=build/App.app", http.StatusBadRequest},
		{"no lease service", &controllers.SimDoctorController{Sessions: doctorSessions{known: "doc-1"}}, "/sessions/doc-1/sim-doctor", http.StatusNotImplemented},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if rec := serveDoctor(t, tc.c, tc.target); rec.Code != tc.status {
				t.Errorf("status = %d, want %d (body %s)", rec.Code, tc.status, rec.Body.String())
			}
		})
	}
	if len(asked) != 0 {
		t.Errorf("a refused request still read the machine for %v", asked)
	}
}
