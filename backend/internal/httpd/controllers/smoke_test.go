package controllers_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	smokesvc "github.com/aoagents/agent-orchestrator/backend/internal/service/smoke"
)

type fakeSmokeService struct {
	list        smokesvc.SessionSmoke
	authored    []domain.SmokeAuthoredCase
	verdictErr  error
	lastUpload  smokesvc.EvidenceUpload
	uploadBytes []byte
	blob        smokesvc.EvidenceBlob
	reported    smokesvc.ReportOutcome
	jiraOutcome smokesvc.JiraPostOutcome
	jiraErr     error

	removedEvidenceID string
	removeErr         error

	exportPath       string
	exportErr        error
	exportedEvidence string
	purgeResult      smokesvc.EvidencePurgeResult
	purgeCutoff      time.Time
}

func (f *fakeSmokeService) List(context.Context, domain.SessionID) (smokesvc.SessionSmoke, error) {
	return f.list, nil
}

func (f *fakeSmokeService) Author(_ context.Context, _ domain.SessionID, cases []domain.SmokeAuthoredCase) (smokesvc.SessionSmoke, error) {
	f.authored = cases
	return f.list, nil
}

func (f *fakeSmokeService) SetVerdict(_ context.Context, _ domain.SessionID, checkID string, verdict domain.SmokeVerdict, note string) (domain.SmokeCheck, error) {
	if f.verdictErr != nil {
		return domain.SmokeCheck{}, f.verdictErr
	}
	return domain.SmokeCheck{ID: checkID, Verdict: verdict, Note: note}, nil
}

func (f *fakeSmokeService) Reset(_ context.Context, _ domain.SessionID, checkID string) (domain.SmokeCheck, error) {
	return domain.SmokeCheck{ID: checkID, Verdict: domain.SmokePending}, nil
}

func (f *fakeSmokeService) AttachEvidence(_ context.Context, _ domain.SessionID, checkID string, upload smokesvc.EvidenceUpload) (domain.SmokeEvidence, error) {
	f.lastUpload = upload
	f.uploadBytes, _ = io.ReadAll(upload.Reader)
	return domain.SmokeEvidence{ID: "ev1", CheckID: checkID, Kind: "image", Filename: upload.Filename, Mime: upload.Mime, SizeBytes: int64(len(f.uploadBytes))}, nil
}

func (f *fakeSmokeService) OpenEvidence(context.Context, domain.SessionID, string, string) (smokesvc.EvidenceBlob, error) {
	return f.blob, nil
}

func (f *fakeSmokeService) RemoveEvidence(_ context.Context, _ domain.SessionID, checkID, evidenceID string) (domain.SmokeCheck, error) {
	if f.removeErr != nil {
		return domain.SmokeCheck{}, f.removeErr
	}
	f.removedEvidenceID = evidenceID
	return domain.SmokeCheck{ID: checkID, Verdict: domain.SmokePending, Evidence: []domain.SmokeEvidence{}}, nil
}

func (f *fakeSmokeService) Report(context.Context, domain.SessionID) (smokesvc.ReportOutcome, error) {
	return f.reported, nil
}

func (f *fakeSmokeService) PostToJira(context.Context, domain.SessionID) (smokesvc.JiraPostOutcome, error) {
	if f.jiraErr != nil {
		return smokesvc.JiraPostOutcome{}, f.jiraErr
	}
	return f.jiraOutcome, nil
}

func (f *fakeSmokeService) ExportEvidence(_ context.Context, _ domain.SessionID, _, evidenceID string) (string, error) {
	if f.exportErr != nil {
		return "", f.exportErr
	}
	f.exportedEvidence = evidenceID
	return f.exportPath, nil
}

func (f *fakeSmokeService) PurgeSessionEvidence(context.Context, domain.SessionID) error { return nil }

func (f *fakeSmokeService) PurgeEvidenceOlderThan(_ context.Context, cutoff time.Time) (smokesvc.EvidencePurgeResult, error) {
	f.purgeCutoff = cutoff
	return f.purgeResult, nil
}

func newSmokeTestServer(t *testing.T, svc smokesvc.Manager) *httptest.Server {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{Smoke: svc}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	return srv
}

func TestSmokeNilServiceReturns501(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	_, status, _ := doRequest(t, srv, "GET", "/api/v1/sessions/w1/smoke-checks", "")
	if status != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", status)
	}
}

func TestSmokeListReturnsChecks(t *testing.T) {
	svc := &fakeSmokeService{list: smokesvc.SessionSmoke{
		Worker: "fix gl note",
		Checks: []domain.SmokeCheck{{ID: "a", Seq: 1, Name: "A fresh MR shows up", Verdict: domain.SmokePending, Evidence: []domain.SmokeEvidence{}}},
	}}
	srv := newSmokeTestServer(t, svc)
	body, status, headers := doRequest(t, srv, "GET", "/api/v1/sessions/w1/smoke-checks", "")
	assertJSON(t, headers)
	if status != http.StatusOK {
		t.Fatalf("status = %d body=%s", status, body)
	}
	for _, want := range []string{`"worker":"fix gl note"`, `"checks"`, `"A fresh MR shows up"`, `"pending"`} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("body missing %s: %s", want, body)
		}
	}
}

func TestSmokeAuthorMapsCases(t *testing.T) {
	svc := &fakeSmokeService{}
	srv := newSmokeTestServer(t, svc)
	payload := `{"cases":[{"name":"Case one","why":"because","steps":["do x"],"expected":"y","prNum":36,"fileRef":"a.go:1"}]}`
	body, status, _ := doRequest(t, srv, "PUT", "/api/v1/sessions/w1/smoke-checks", payload)
	if status != http.StatusOK {
		t.Fatalf("status = %d body=%s", status, body)
	}
	if len(svc.authored) != 1 || svc.authored[0].Name != "Case one" || svc.authored[0].PRNum != 36 || svc.authored[0].FileRef != "a.go:1" {
		t.Fatalf("authored cases not mapped: %+v", svc.authored)
	}
	if len(svc.authored[0].Steps) != 1 || svc.authored[0].Steps[0] != "do x" {
		t.Fatalf("steps not mapped: %+v", svc.authored[0].Steps)
	}
}

func TestSmokeVerdictMapsNotFound(t *testing.T) {
	svc := &fakeSmokeService{verdictErr: smokesvc.ErrNotFound}
	srv := newSmokeTestServer(t, svc)
	body, status, headers := doRequest(t, srv, "POST", "/api/v1/sessions/w1/smoke-checks/a/verdict", `{"verdict":"pass"}`)
	assertJSON(t, headers)
	assertErrorCode(t, body, status, http.StatusNotFound, "SMOKE_NOT_FOUND")
}

func TestSmokeEvidenceMultipartRoundTrip(t *testing.T) {
	svc := &fakeSmokeService{}
	srv := newSmokeTestServer(t, svc)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	hdr := make(map[string][]string)
	hdr["Content-Disposition"] = []string{`form-data; name="file"; filename="shot.png"`}
	hdr["Content-Type"] = []string{"image/png"}
	part, err := mw.CreatePart(hdr)
	if err != nil {
		t.Fatalf("create part: %v", err)
	}
	if _, err := part.Write([]byte("PNGBYTES")); err != nil {
		t.Fatalf("write part: %v", err)
	}
	_ = mw.Close()

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/sessions/w1/smoke-checks/a/evidence", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d body=%s", resp.StatusCode, body)
	}
	if svc.lastUpload.Mime != "image/png" || svc.lastUpload.Filename != "shot.png" {
		t.Fatalf("upload metadata wrong: %+v", svc.lastUpload)
	}
	if string(svc.uploadBytes) != "PNGBYTES" {
		t.Fatalf("upload bytes = %q, want PNGBYTES", svc.uploadBytes)
	}
	if !strings.Contains(string(body), `"evidence"`) {
		t.Fatalf("response missing evidence: %s", body)
	}
}

func TestSmokeEvidenceServeSetsContentType(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ev1")
	if err := os.WriteFile(path, []byte("PNGBYTES"), 0o644); err != nil {
		t.Fatalf("write blob: %v", err)
	}
	svc := &fakeSmokeService{blob: smokesvc.EvidenceBlob{Path: path, Mime: "image/png", Filename: "shot.png"}}
	srv := newSmokeTestServer(t, svc)

	resp, err := http.Get(srv.URL + "/api/v1/sessions/w1/smoke-checks/a/evidence/ev1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); got != "image/png" {
		t.Fatalf("content-type = %q, want image/png", got)
	}
	if string(body) != "PNGBYTES" {
		t.Fatalf("served bytes = %q", body)
	}
}

func TestSmokeEvidenceDeleteReturnsCheck(t *testing.T) {
	svc := &fakeSmokeService{}
	srv := newSmokeTestServer(t, svc)

	body, status, headers := doRequest(t, srv, "DELETE", "/api/v1/sessions/w1/smoke-checks/a/evidence/ev1", "")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if ct := headers.Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("content-type = %q", ct)
	}
	if svc.removedEvidenceID != "ev1" {
		t.Fatalf("removed evidence id = %q, want ev1", svc.removedEvidenceID)
	}
	if !strings.Contains(string(body), `"check"`) {
		t.Fatalf("response missing check: %s", body)
	}
}

func TestSmokeEvidenceDeleteMapsNotFound(t *testing.T) {
	svc := &fakeSmokeService{removeErr: fmt.Errorf("%w: evidence %q", smokesvc.ErrNotFound, "ev1")}
	srv := newSmokeTestServer(t, svc)

	_, status, _ := doRequest(t, srv, "DELETE", "/api/v1/sessions/w1/smoke-checks/a/evidence/ev1", "")
	if status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", status)
	}
}

func TestSmokeEvidenceExportReturnsPath(t *testing.T) {
	svc := &fakeSmokeService{exportPath: "/Users/x/.ao/data/evidence/w1/a/_open/a-shot.png"}
	srv := newSmokeTestServer(t, svc)

	body, status, headers := doRequest(t, srv, "POST", "/api/v1/sessions/w1/smoke-checks/a/evidence/ev1/export", "")
	if status != http.StatusOK {
		t.Fatalf("status = %d body=%s", status, body)
	}
	if ct := headers.Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("content-type = %q", ct)
	}
	if svc.exportedEvidence != "ev1" {
		t.Fatalf("exported evidence id = %q, want ev1", svc.exportedEvidence)
	}
	if !strings.Contains(string(body), `_open/a-shot.png`) {
		t.Fatalf("response missing exported path: %s", body)
	}
}

func TestSmokeEvidenceExportMapsNotFound(t *testing.T) {
	svc := &fakeSmokeService{exportErr: fmt.Errorf("%w: evidence %q", smokesvc.ErrNotFound, "ev1")}
	srv := newSmokeTestServer(t, svc)

	_, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions/w1/smoke-checks/a/evidence/ev1/export", "")
	if status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", status)
	}
}

func TestSmokePostJiraReturnsOutcome(t *testing.T) {
	svc := &fakeSmokeService{jiraOutcome: smokesvc.JiraPostOutcome{
		Key: "DEMO-101", CommentURL: "https://acme.atlassian.net/browse/DEMO-101?focusedCommentId=10101",
		AttachmentsUploaded: 2, RowsPosted: 3, EmbeddedMedia: true,
	}}
	srv := newSmokeTestServer(t, svc)
	body, status, headers := doRequest(t, srv, "POST", "/api/v1/sessions/w1/smoke-checks/jira", "")
	assertJSON(t, headers)
	if status != http.StatusOK {
		t.Fatalf("status = %d body=%s", status, body)
	}
	for _, want := range []string{`"key":"DEMO-101"`, `"attachmentsUploaded":2`, `"rowsPosted":3`, `"embeddedMedia":true`, `focusedCommentId=10101`} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("body missing %s: %s", want, body)
		}
	}
}

func TestSmokePostJiraNotLinkedMapsCode(t *testing.T) {
	svc := &fakeSmokeService{jiraErr: smokesvc.ErrNotLinked}
	srv := newSmokeTestServer(t, svc)
	body, status, headers := doRequest(t, srv, "POST", "/api/v1/sessions/w1/smoke-checks/jira", "")
	assertJSON(t, headers)
	assertErrorCode(t, body, status, http.StatusUnprocessableEntity, "SMOKE_JIRA_NOT_LINKED")
}

func TestSmokeReportReturnsOutcome(t *testing.T) {
	svc := &fakeSmokeService{reported: smokesvc.ReportOutcome{Delivered: true, Target: "worker", Summary: "2 pass"}}
	srv := newSmokeTestServer(t, svc)
	body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions/w1/smoke-checks/report", "")
	if status != http.StatusOK {
		t.Fatalf("status = %d body=%s", status, body)
	}
	for _, want := range []string{`"delivered":true`, `"target":"worker"`, `"summary":"2 pass"`} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("body missing %s: %s", want, body)
		}
	}
}
