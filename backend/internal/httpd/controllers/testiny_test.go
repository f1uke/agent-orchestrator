package controllers_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	rcloneadapter "github.com/aoagents/agent-orchestrator/backend/internal/adapters/rclone"
	testinyadapter "github.com/aoagents/agent-orchestrator/backend/internal/adapters/testiny"
	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	testinysvc "github.com/aoagents/agent-orchestrator/backend/internal/service/testiny"
)

// fakeTestiny records which task each call reached and what it was given.
type fakeTestiny struct {
	err      error
	runs     testinysvc.Runs
	asked    []string
	linkRef  string
	linkBy   string
	refresh  bool
	unlinked domain.TestinyRunID
	recorded recordCall
	caseID   int64
	// evidenceBy is who UploadEvidence was asked by; evidenceTook is how long
	// it takes, and evidenceCtxErr what its context said when it finished.
	evidenceBy     string
	evidenceTook   time.Duration
	evidenceCtxErr error
}

func (f *fakeTestiny) UploadEvidence(ctx context.Context, task domain.SessionID, id domain.TestinyRunID, by string) (domain.TestinyEvidenceReport, error) {
	f.asked = append(f.asked, "evidence "+string(task))
	f.evidenceBy = by
	time.Sleep(f.evidenceTook)
	f.evidenceCtxErr = ctx.Err()
	if f.err != nil {
		return domain.TestinyEvidenceReport{}, f.err
	}
	return domain.TestinyEvidenceReport{
		Folder: "/Users/me/Desktop/QA Evidence/MOBILITY/2026/S/TP-1 - p/TR-632 - r", Drive: "finnomena:QA/MOBILITY/2026/S/TP-1 - p/TR-632 - r",
		Uploaded: []string{"README.md", "TC-7166 pass.png"},
		Cases:    []domain.TestinyEvidenceCaseLinks{{CaseID: 7166, Linked: []string{"TC-7166 pass.png"}, CommentID: 2601, AlreadyLinked: []string{}}},
		Run:      domain.TestinyRunView{Link: domain.TestinyRunLink{SessionID: task, RunID: id}, Counts: map[domain.TestinyCaseStatus]int{}, Cases: []domain.TestinyCaseResult{}},
	}, nil
}

// recordCall is what one RecordResults call was given.
type recordCall struct {
	run     domain.TestinyRunID
	results []domain.TestinyResult
	by, sha string
}

func (f *fakeTestiny) Link(_ context.Context, task domain.SessionID, ref, by string) (domain.TestinyRunView, error) {
	f.asked = append(f.asked, "link "+string(task))
	f.linkRef, f.linkBy = ref, by
	if f.err != nil {
		return domain.TestinyRunView{}, f.err
	}
	return domain.TestinyRunView{Link: domain.TestinyRunLink{SessionID: task, RunID: 632, LinkedBy: by}, Title: "Chat notice", Counts: map[domain.TestinyCaseStatus]int{}, Cases: []domain.TestinyCaseResult{}}, nil
}

func (f *fakeTestiny) Unlink(_ context.Context, task domain.SessionID, id domain.TestinyRunID) error {
	f.asked = append(f.asked, "unlink "+string(task))
	f.unlinked = id
	return f.err
}

func (f *fakeTestiny) Runs(_ context.Context, task domain.SessionID, refresh bool) (testinysvc.Runs, error) {
	f.asked = append(f.asked, "runs "+string(task))
	f.refresh = refresh
	return f.runs, f.err
}

func (f *fakeTestiny) RecordResults(_ context.Context, task domain.SessionID, id domain.TestinyRunID, results []domain.TestinyResult, by, sha string) (domain.TestinyRunView, error) {
	f.asked = append(f.asked, "record "+string(task))
	f.recorded = recordCall{run: id, results: results, by: by, sha: sha}
	if f.err != nil {
		return domain.TestinyRunView{}, f.err
	}
	return domain.TestinyRunView{Link: domain.TestinyRunLink{SessionID: task, RunID: id}, Title: "Chat notice",
		Counts: map[domain.TestinyCaseStatus]int{"FAILED": 1}, Cases: []domain.TestinyCaseResult{{ID: 7166, Status: "FAILED"}}}, nil
}

func (f *fakeTestiny) Case(_ context.Context, task domain.SessionID, id int64) (domain.TestinyCaseDetail, error) {
	f.asked = append(f.asked, "case "+string(task))
	f.caseID = id
	if f.err != nil {
		return domain.TestinyCaseDetail{}, f.err
	}
	return domain.TestinyCaseDetail{ID: id, Title: "Fund disclaimer", Template: domain.TestinyTemplateSteps,
		Priority: &domain.TestinyCasePriority{Level: 1, Label: "High"}, Platforms: []string{"iOS"}, Automation: []string{},
		TestData: "qa@example.com / fake-password", Steps: []domain.TestinyCaseStep{{N: 1, Action: "Open chat", Expected: "Chat list shows"}}}, nil
}

func newTestinyServer(t *testing.T, svc *fakeTestiny, crew map[domain.SessionID]domain.SessionID) *httptest.Server {
	t.Helper()
	sessions := &scopeSessions{fakeSessionService: newFakeSessionService(), devOf: crew}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{
		Sessions: sessions,
		Testiny:  svc,
	}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	return srv
}

func TestTestinyRunsList(t *testing.T) {
	at := time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)
	svc := &fakeTestiny{runs: testinysvc.Runs{Project: "MOB", Runs: []domain.TestinyRunView{{
		Link:       domain.TestinyRunLink{SessionID: "solo-1", RunID: 632, CreatedAt: at},
		Title:      "Chat notice",
		URL:        "https://app.testiny.io/MOB/testruns/tr/632",
		Counts:     map[domain.TestinyCaseStatus]int{"PASSED": 6},
		Cases:      []domain.TestinyCaseResult{{ID: 7166, Title: "Fund disclaimer", Status: "PASSED", Script: "projects/nter/cases/chat/fund.yaml"}},
		FetchedAt:  &at,
		FetchError: &domain.TestinyFetchError{Kind: domain.TestinyErrAuth, Message: "Unauthenticated user"},
	}}}}
	srv := newTestinyServer(t, svc, nil)

	body, status, _ := doRequest(t, srv, "GET", "/api/v1/sessions/solo-1/testiny/runs?refresh=1", "")
	if status != http.StatusOK {
		t.Fatalf("status = %d body=%s", status, body)
	}
	if !svc.refresh {
		t.Fatal("refresh=1 did not reach the service")
	}
	var got struct {
		Project string `json:"project"`
		Runs    []struct {
			Link struct {
				RunID int64 `json:"runId"`
			} `json:"link"`
			URL        string         `json:"url"`
			Counts     map[string]int `json:"counts"`
			FetchError struct {
				Kind string `json:"kind"`
			} `json:"fetchError"`
			Cases []struct {
				Script string `json:"script"`
			} `json:"cases"`
		} `json:"runs"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	r := got.Runs[0]
	if got.Project != "MOB" || r.Link.RunID != 632 || r.Counts["PASSED"] != 6 || r.FetchError.Kind != "auth" ||
		r.Cases[0].Script != "projects/nter/cases/chat/fund.yaml" || r.URL == "" {
		t.Fatalf("body = %s", body)
	}

	if _, status, _ := doRequest(t, srv, "GET", "/api/v1/sessions/solo-1/testiny/runs", ""); status != http.StatusOK || svc.refresh {
		t.Fatalf("plain GET: status %d, refresh %v", status, svc.refresh)
	}
}

func TestTestinyEmptyListIsAnArray(t *testing.T) {
	srv := newTestinyServer(t, &fakeTestiny{runs: testinysvc.Runs{Project: "MOB"}}, nil)
	body, status, _ := doRequest(t, srv, "GET", "/api/v1/sessions/solo-1/testiny/runs", "")
	if status != http.StatusOK || !strings.Contains(string(body), `"runs":[]`) {
		t.Fatalf("status %d body %s", status, body)
	}
}

func TestTestinyLinkAndUnlink(t *testing.T) {
	svc := &fakeTestiny{}
	srv := newTestinyServer(t, svc, nil)
	body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions/solo-1/testiny/runs", `{"ref":"https://app.testiny.io/MOB/testruns/tr/632","from":"solo-2"}`)
	if status != http.StatusOK {
		t.Fatalf("link: status %d body %s", status, body)
	}
	if svc.linkRef != "https://app.testiny.io/MOB/testruns/tr/632" || svc.linkBy != "solo-2" {
		t.Fatalf("link got ref %q by %q", svc.linkRef, svc.linkBy)
	}
	if !strings.Contains(string(body), `"title":"Chat notice"`) {
		t.Fatalf("link body = %s", body)
	}
	if _, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions/solo-1/testiny/runs", `{"ref":"632"}`); status != http.StatusOK || svc.linkBy != "" {
		t.Fatalf("link from the app: status %d, by %q", status, svc.linkBy)
	}

	if body, status, _ := doRequest(t, srv, "DELETE", "/api/v1/sessions/solo-1/testiny/runs/TR-632", ""); status != http.StatusNoContent {
		t.Fatalf("unlink: status %d body %s", status, body)
	}
	if svc.unlinked != 632 {
		t.Fatalf("unlinked %d", svc.unlinked)
	}
	body, status, _ = doRequest(t, srv, "DELETE", "/api/v1/sessions/solo-1/testiny/runs/abc", "")
	if status != http.StatusBadRequest || !strings.Contains(string(body), "TESTINY_BAD_RUN_REF") {
		t.Fatalf("unlink abc: status %d body %s", status, body)
	}
	body, status, _ = doRequest(t, srv, "POST", "/api/v1/sessions/solo-1/testiny/runs", `{"ref":`)
	if status != http.StatusBadRequest || !strings.Contains(string(body), "INVALID_BODY") {
		t.Fatalf("bad body: status %d body %s", status, body)
	}
}

func TestTestinyErrorsMapToCodes(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{domain.ErrBadRunRef, http.StatusBadRequest, "TESTINY_BAD_RUN_REF"},
		{fmt.Errorf("%w: TR-999999", testinysvc.ErrRunNotFound), http.StatusNotFound, "TESTINY_RUN_NOT_FOUND"},
		{fmt.Errorf("%w: TR-900 belongs to KERN", testinysvc.ErrWrongProject), http.StatusUnprocessableEntity, "TESTINY_RUN_WRONG_PROJECT"},
		{testinysvc.ErrProjectNotFound, http.StatusUnprocessableEntity, "TESTINY_PROJECT_NOT_FOUND"},
		{testinysvc.ErrOff, http.StatusConflict, "TESTINY_OFF"},
		{testinysvc.ErrSessionNotFound, http.StatusNotFound, "SESSION_NOT_FOUND"},
		{fmt.Errorf("%w: Unauthenticated user", testinyadapter.ErrAuth), http.StatusBadGateway, "TESTINY_AUTH"},
		{testinyadapter.ErrUnavailable, http.StatusBadGateway, "TESTINY_UNAVAILABLE"},
		{testinyadapter.ErrRejected, http.StatusBadGateway, "TESTINY_UNAVAILABLE"},
		{testinyadapter.ErrBinaryMissing, http.StatusBadGateway, "TESTINY_CLI_MISSING"},
		{testinyadapter.ErrCLITooOld, http.StatusBadGateway, "TESTINY_CLI_TOO_OLD"},
		{testinyadapter.ErrNotFound, http.StatusBadGateway, "TESTINY_UNAVAILABLE"},
	} {
		srv := newTestinyServer(t, &fakeTestiny{err: tc.err}, nil)
		body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions/solo-1/testiny/runs", `{"ref":"632"}`)
		if status != tc.status || !strings.Contains(string(body), `"code":"`+tc.code+`"`) {
			t.Errorf("%v: status %d body %s, want %d %s", tc.err, status, body, tc.status, tc.code)
		}
	}
	srv := newTestinyServer(t, &fakeTestiny{err: testinysvc.ErrOff}, nil)
	if _, status, _ := doRequest(t, srv, "GET", "/api/v1/sessions/solo-1/testiny/runs", ""); status != http.StatusConflict {
		t.Fatalf("GET on an off project: status %d, want 409", status)
	}
}

// A run links to the TASK. qa's link, list and unlink must all reach dev's
// task, or the tab dev and the person look at never shows qa's runs.
func TestTestinyRoutesResolveToTheTasksDev(t *testing.T) {
	svc := &fakeTestiny{runs: testinysvc.Runs{Project: "MOB"}}
	srv := newTestinyServer(t, svc, map[domain.SessionID]domain.SessionID{scopeQA: scopeDev})
	qa := "/api/v1/sessions/" + string(scopeQA) + "/testiny/runs"
	for _, req := range [][3]string{
		{"POST", qa, `{"ref":"632","from":"task-qa"}`},
		{"GET", qa, ""},
		{"DELETE", qa + "/632", ""},
		{"POST", qa + "/632/results", `{"results":[{"caseId":7166,"status":"PASSED"}],"from":"task-qa"}`},
		{"GET", "/api/v1/sessions/" + string(scopeQA) + "/testiny/cases/7166", ""},
		{"POST", qa + "/632/evidence", `{"from":"task-qa"}`},
	} {
		if body, status, _ := doRequest(t, srv, req[0], req[1], req[2]); status >= 300 {
			t.Fatalf("%s %s: status %d body %s", req[0], req[1], status, body)
		}
	}
	want := []string{"link task-dev", "runs task-dev", "unlink task-dev", "record task-dev", "case task-dev", "evidence task-dev"}
	if strings.Join(svc.asked, ",") != strings.Join(want, ",") {
		t.Fatalf("service asked %v, want %v", svc.asked, want)
	}
	if svc.linkBy != "task-qa" || svc.recorded.by != "task-qa" || svc.evidenceBy != "task-qa" {
		t.Fatalf("linked by %q, recorded by %q, uploaded by %q; want qa's own id", svc.linkBy, svc.recorded.by, svc.evidenceBy)
	}
}

func TestTestinyRecordResults(t *testing.T) {
	svc := &fakeTestiny{}
	srv := newTestinyServer(t, svc, nil)
	body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions/solo-1/testiny/runs/TR-632/results",
		`{"results":[{"caseId":7166,"status":"FAILED","comment":"ปุ่มไม่แสดง"},{"caseId":7167,"status":"PASSED"}],"from":" solo-1 ","sha":"4f2c9e1"}`)
	if status != http.StatusOK {
		t.Fatalf("status %d body %s", status, body)
	}
	want := recordCall{run: 632, by: "solo-1", sha: "4f2c9e1", results: []domain.TestinyResult{
		{CaseID: 7166, Status: "FAILED", Comment: "ปุ่มไม่แสดง"},
		{CaseID: 7167, Status: "PASSED"},
	}}
	if fmt.Sprint(svc.recorded) != fmt.Sprint(want) {
		t.Fatalf("service got %+v, want %+v", svc.recorded, want)
	}
	if !strings.Contains(string(body), `"counts":{"FAILED":1}`) {
		t.Fatalf("body = %s, want the fresh run view", body)
	}

	// A step result rides on its case; with no status, the case keeps its own.
	if body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions/solo-1/testiny/runs/632/results",
		`{"results":[{"caseId":7166,"steps":[{"n":2,"status":"FAILED"},{"n":3,"status":"PASSED"}]}]}`); status != http.StatusOK {
		t.Fatalf("steps: status %d body %s", status, body)
	}
	wantSteps := []domain.TestinyResult{{CaseID: 7166, Steps: []domain.TestinyStepResult{{N: 2, Status: "FAILED"}, {N: 3, Status: "PASSED"}}}}
	if !reflect.DeepEqual(svc.recorded.results, wantSteps) {
		t.Fatalf("service got %+v, want %+v", svc.recorded.results, wantSteps)
	}

	if _, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions/solo-1/testiny/runs/632/results", `{"results":[{"caseId":7166,"status":"PASSED"}]}`); status != http.StatusOK || svc.recorded.by != "" {
		t.Fatalf("from the app: status %d, by %q", status, svc.recorded.by)
	}
	for _, bad := range [][2]string{
		{"/api/v1/sessions/solo-1/testiny/runs/abc/results", `{"results":[]}`},
		{"/api/v1/sessions/solo-1/testiny/runs/632/results", `{"results":`},
	} {
		if body, status, _ := doRequest(t, srv, "POST", bad[0], bad[1]); status != http.StatusBadRequest {
			t.Fatalf("%s %s: status %d body %s, want 400", bad[0], bad[1], status, body)
		}
	}
}

func TestTestinyRecordResultsErrorsMapToCodes(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{fmt.Errorf("%w: TC-7166: FAILED needs a comment", domain.ErrBadTestinyResult), http.StatusBadRequest, "TESTINY_RESULT_INVALID"},
		{fmt.Errorf("%w: TR-632 is not linked", testinysvc.ErrRunNotLinked), http.StatusNotFound, "TESTINY_RUN_NOT_LINKED"},
		{fmt.Errorf("%w: TC-7166 (PASSED)", testinysvc.ErrSetByPerson), http.StatusConflict, "TESTINY_RESULT_SET_BY_PERSON"},
		{fmt.Errorf("%w: task has a qa", testinysvc.ErrWriteNotYours), http.StatusForbidden, "TESTINY_WRITE_NOT_YOURS"},
		{fmt.Errorf("%w: Unauthenticated user", testinyadapter.ErrAuth), http.StatusBadGateway, "TESTINY_AUTH"},
		{testinysvc.ErrOff, http.StatusConflict, "TESTINY_OFF"},
	} {
		srv := newTestinyServer(t, &fakeTestiny{err: tc.err}, nil)
		body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions/solo-1/testiny/runs/632/results", `{"results":[{"caseId":7166,"status":"PASSED"}]}`)
		if status != tc.status || !strings.Contains(string(body), `"code":"`+tc.code+`"`) {
			t.Errorf("%v: status %d body %s, want %d %s", tc.err, status, body, tc.status, tc.code)
		}
	}
}

func TestTestinyCase(t *testing.T) {
	svc := &fakeTestiny{}
	srv := newTestinyServer(t, svc, nil)
	body, status, _ := doRequest(t, srv, "GET", "/api/v1/sessions/solo-1/testiny/cases/TC-7166", "")
	if status != http.StatusOK {
		t.Fatalf("status %d body %s", status, body)
	}
	if svc.caseID != 7166 {
		t.Fatalf("service got case %d, want 7166", svc.caseID)
	}
	var got domain.TestinyCaseDetail
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if got.ID != 7166 || got.Priority == nil || got.Priority.Label != "High" || got.TestData != "qa@example.com / fake-password" ||
		len(got.Steps) != 1 || got.Steps[0].Expected != "Chat list shows" {
		t.Fatalf("body = %s", body)
	}
	if !strings.Contains(string(body), `"automation":[]`) {
		t.Fatalf("an empty list must be [], body = %s", body)
	}

	body, status, _ = doRequest(t, srv, "GET", "/api/v1/sessions/solo-1/testiny/cases/TR-7166", "")
	if status != http.StatusBadRequest || !strings.Contains(string(body), `"code":"TESTINY_BAD_CASE_REF"`) {
		t.Fatalf("bad case ref: status %d body %s", status, body)
	}
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{fmt.Errorf("%w: TC-7166 is in none of the runs", testinysvc.ErrCaseNotInTask), http.StatusNotFound, "TESTINY_CASE_NOT_IN_TASK"},
		{testinysvc.ErrOff, http.StatusConflict, "TESTINY_OFF"},
		{fmt.Errorf("%w: Unauthenticated user", testinyadapter.ErrAuth), http.StatusBadGateway, "TESTINY_AUTH"},
		{testinyadapter.ErrNotFound, http.StatusBadGateway, "TESTINY_UNAVAILABLE"},
	} {
		srv := newTestinyServer(t, &fakeTestiny{err: tc.err}, nil)
		body, status, _ := doRequest(t, srv, "GET", "/api/v1/sessions/solo-1/testiny/cases/7166", "")
		if status != tc.status || !strings.Contains(string(body), `"code":"`+tc.code+`"`) {
			t.Errorf("%v: status %d body %s, want %d %s", tc.err, status, body, tc.status, tc.code)
		}
	}
}

func TestTestinyWithoutAServiceIsNotImplemented(t *testing.T) {
	sessions := &scopeSessions{fakeSessionService: newFakeSessionService()}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{Sessions: sessions}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	if _, status, _ := doRequest(t, srv, "GET", "/api/v1/sessions/solo-1/testiny/runs", ""); status != http.StatusNotImplemented {
		t.Fatalf("status %d, want 501", status)
	}
}

func TestTestinyUploadEvidence(t *testing.T) {
	svc := &fakeTestiny{}
	srv := newTestinyServer(t, svc, nil)
	body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions/solo-1/testiny/runs/TR-632/evidence", "")
	if status != http.StatusOK {
		t.Fatalf("status %d body %s", status, body)
	}
	var got struct {
		Folder   string   `json:"folder"`
		Drive    string   `json:"drive"`
		Uploaded []string `json:"uploaded"`
		Cases    []struct {
			CaseID        int64    `json:"caseId"`
			Linked        []string `json:"linked"`
			CommentID     int64    `json:"commentId"`
			AlreadyLinked []string `json:"alreadyLinked"`
		} `json:"cases"`
		Run struct {
			Link struct {
				RunID int64 `json:"runId"`
			} `json:"link"`
		} `json:"run"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if got.Drive == "" || len(got.Uploaded) != 2 || got.Cases[0].CommentID != 2601 || got.Cases[0].AlreadyLinked == nil || got.Run.Link.RunID != 632 {
		t.Fatalf("report = %s", body)
	}
	if svc.evidenceBy != "" {
		t.Fatalf("an empty body uploaded as %q, want a person", svc.evidenceBy)
	}
}

// An upload sends screen recordings to Drive, which takes longer than the
// REST timeout allows any other route.
func TestTestinyUploadEvidenceOutlivesTheRequestTimeout(t *testing.T) {
	svc := &fakeTestiny{evidenceTook: 300 * time.Millisecond}
	sessions := &scopeSessions{fakeSessionService: newFakeSessionService()}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{RequestTimeout: 50 * time.Millisecond}, log, nil,
		httpd.APIDeps{Sessions: sessions, Testiny: svc}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions/solo-1/testiny/runs/632/evidence", `{}`)
	if status != http.StatusOK || svc.evidenceCtxErr != nil {
		t.Fatalf("status %d (ctx %v) body %s, want the upload to finish", status, svc.evidenceCtxErr, body)
	}
}

func TestTestinyUploadEvidenceErrorsMapToCodes(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{fmt.Errorf("%w: README.md is missing", domain.ErrBadTestinyEvidence), http.StatusUnprocessableEntity, "TESTINY_EVIDENCE_INVALID"},
		{testinysvc.ErrEvidenceOff, http.StatusConflict, "TESTINY_EVIDENCE_OFF"},
		{fmt.Errorf("%w: TR-632 is closed", testinysvc.ErrRunClosed), http.StatusConflict, "TESTINY_RUN_CLOSED"},
		{fmt.Errorf("%w: TR-632 is not linked", testinysvc.ErrRunNotLinked), http.StatusNotFound, "TESTINY_RUN_NOT_LINKED"},
		{fmt.Errorf("%w: app-9 is not on the task", testinysvc.ErrWriteNotYours), http.StatusForbidden, "TESTINY_WRITE_NOT_YOURS"},
		{fmt.Errorf("%w: two files", testinysvc.ErrDriveDuplicate), http.StatusConflict, "DRIVE_DUPLICATE"},
		{rcloneadapter.ErrBinaryMissing, http.StatusServiceUnavailable, "DRIVE_RCLONE_MISSING"},
		{rcloneadapter.ErrRemoteMissing, http.StatusUnprocessableEntity, "DRIVE_REMOTE_MISSING"},
		{fmt.Errorf("%w finnomena: run `rclone config reconnect finnomena:`", rcloneadapter.ErrAuth), http.StatusBadGateway, "DRIVE_AUTH"},
		{rcloneadapter.ErrUnavailable, http.StatusBadGateway, "DRIVE_UNAVAILABLE"},
		{testinyadapter.ErrUnavailable, http.StatusBadGateway, "TESTINY_UNAVAILABLE"},
	} {
		srv := newTestinyServer(t, &fakeTestiny{err: tc.err}, nil)
		body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions/solo-1/testiny/runs/632/evidence", `{"from":"solo-1"}`)
		if status != tc.status || !strings.Contains(string(body), `"code":"`+tc.code+`"`) {
			t.Errorf("%v: status %d body %s, want %d %s", tc.err, status, body, tc.status, tc.code)
		}
	}
}
