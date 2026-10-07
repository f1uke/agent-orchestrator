package cli

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

const testinyEvidenceJSON = `{"folder":"/Users/me/Desktop/QA Evidence/MOBILITY/2026/Sprint 2026-20/TP-193 - Chat session logout/TR-625 - Chat logout - iOS",
"drive":"finnomena:QA/MOBILITY/2026/Sprint 2026-20/TP-193 - Chat session logout/TR-625 - Chat logout - iOS",
"uploaded":["README.md","TC-3819 FAIL MOBILITY-1 - Pixel.mp4"],
"cases":[{"caseId":3818,"linked":[],"commentId":0,"alreadyLinked":["TC-3818 pass.png"]},
{"caseId":3819,"linked":["TC-3819 FAIL MOBILITY-1 - Pixel.mp4"],"commentId":9002,"alreadyLinked":["TC-3819 FAIL MOBILITY-1 - iPhone.mp4"]}],
"run":{"link":{"sessionId":"app-1","runId":625,"linkedBy":"","createdAt":"2026-10-07T09:00:00Z"},"title":"Chat logout - iOS","url":"","closed":false,
"counts":{"PASSED":1},"cases":[],"evidenceDir":"","fetchedAt":"2026-10-07T09:00:00Z"}}`

func TestTestinyEvidenceUploadsAndPrintsWhatItLinked(t *testing.T) {
	cfg := setConfigEnv(t)
	t.Setenv("AO_SESSION_ID", "app-2")
	srv, capture := reviewServer(t, http.StatusOK, testinyEvidenceJSON)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, aliveDeps(), "testiny", "evidence", "app-1", "TR-625")
	if err != nil {
		t.Fatalf("evidence: %v\nstderr=%s", err, errOut)
	}
	if capture.method != http.MethodPost || capture.path != "/api/v1/sessions/app-1/testiny/runs/TR-625/evidence" {
		t.Fatalf("request = %s %s", capture.method, capture.path)
	}
	if capture.body != `{"from":"app-2"}` {
		t.Fatalf("body = %s", capture.body)
	}
	want := `uploaded the evidence of TR-625 "Chat logout - iOS"
  folder: /Users/me/Desktop/QA Evidence/MOBILITY/2026/Sprint 2026-20/TP-193 - Chat session logout/TR-625 - Chat logout - iOS
  drive:  finnomena:QA/MOBILITY/2026/Sprint 2026-20/TP-193 - Chat session logout/TR-625 - Chat logout - iOS
  sent:   README.md, TC-3819 FAIL MOBILITY-1 - Pixel.mp4
  TC-3818 already linked TC-3818 pass.png
  TC-3819 linked TC-3819 FAIL MOBILITY-1 - Pixel.mp4; already linked TC-3819 FAIL MOBILITY-1 - iPhone.mp4
`
	if out != want {
		t.Fatalf("output =\n%s\nwant\n%s", out, want)
	}
}

func TestTestinyEvidenceWithNothingNew(t *testing.T) {
	cfg := setConfigEnv(t)
	t.Setenv("AO_SESSION_ID", "")
	srv, capture := reviewServer(t, http.StatusOK, `{"folder":"/f","drive":"finnomena:QA/x","uploaded":[],"cases":[],"run":{"link":{"runId":625},"title":"r","counts":{},"cases":[]}}`)
	writeRunFileFor(t, cfg, srv)
	out, _, err := executeCLI(t, aliveDeps(), "testiny", "evidence", "app-1", "625")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "sent:   nothing new, Drive had every file\n") || !strings.Contains(out, "no evidence files to link, only README.md\n") {
		t.Fatalf("output = %q", out)
	}
	if capture.body != `{}` {
		t.Fatalf("body = %s, want no from from a person's shell", capture.body)
	}
}

func TestTestinyEvidenceErrorsExitByKind(t *testing.T) {
	for _, tc := range []struct {
		status int
		code   string
		exit   int
	}{
		{http.StatusUnprocessableEntity, "TESTINY_EVIDENCE_INVALID", 2},
		{http.StatusConflict, "TESTINY_RUN_CLOSED", 2},
		{http.StatusNotFound, "TESTINY_RUN_NOT_LINKED", 2},
		{http.StatusForbidden, "TESTINY_WRITE_NOT_YOURS", 2},
		{http.StatusConflict, "TESTINY_EVIDENCE_OFF", 1},
		{http.StatusBadGateway, "DRIVE_AUTH", 1},
		{http.StatusServiceUnavailable, "DRIVE_RCLONE_MISSING", 1},
		{http.StatusUnprocessableEntity, "DRIVE_REMOTE_MISSING", 1},
		{http.StatusConflict, "DRIVE_DUPLICATE", 1},
		{http.StatusBadGateway, "DRIVE_UNAVAILABLE", 1},
	} {
		cfg := setConfigEnv(t)
		srv, _ := reviewServer(t, tc.status, `{"code":"`+tc.code+`","message":"the daemon's words"}`)
		writeRunFileFor(t, cfg, srv)
		_, _, err := executeCLI(t, aliveDeps(), "testiny", "evidence", "app-1", "625")
		if err == nil || ExitCode(err) != tc.exit || !strings.Contains(err.Error(), "the daemon's words") {
			t.Errorf("%s: err = %v (exit %d), want exit %d with the daemon's message", tc.code, err, ExitCode(err), tc.exit)
		}
	}
}

// deadlineTransport answers every request and keeps the deadline its context
// carried, which is what bounds the call.
type deadlineTransport struct {
	deadline time.Time
	has      bool
}

func (d *deadlineTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	d.deadline, d.has = r.Context().Deadline()
	body := `{"project":"MOB","runs":[],"cases":[],"uploaded":[],"run":{"link":{"runId":625},"counts":{},"cases":[]}}`
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
}

// An upload sends screen recordings for minutes, longer than the two minutes
// any other daemon call gets.
func TestTestinyEvidenceWaitsLongerThanOtherCalls(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, _ := reviewServer(t, http.StatusOK, `{}`)
	writeRunFileFor(t, cfg, srv)
	for _, tc := range []struct {
		args    []string
		atLeast time.Duration
		atMost  time.Duration
	}{
		{[]string{"testiny", "evidence", "app-1", "625"}, 30 * time.Minute, testinyEvidenceTimeout},
		{[]string{"testiny", "runs", "app-1", "--json"}, time.Second, commandTimeout},
	} {
		tr := &deadlineTransport{}
		deps := aliveDeps()
		deps.HTTPClient = &http.Client{Transport: tr}
		if _, _, err := executeCLI(t, deps, tc.args...); err != nil {
			t.Fatalf("%v: %v", tc.args, err)
		}
		left := time.Until(tr.deadline)
		if !tr.has || left < tc.atLeast || left > tc.atMost {
			t.Errorf("%v: the call may take %s (deadline set: %v), want between %s and %s", tc.args, left, tr.has, tc.atLeast, tc.atMost)
		}
	}
}
