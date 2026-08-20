package controllers_test

import (
	"bytes"
	"context"
	"encoding/json"
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
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
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

	agentResult   domain.SmokeAgentResult
	agentErr      error
	retiredCaseID string
	retiredReason string
	retireErr     error

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

func (f *fakeSmokeService) RecordAgentResult(_ context.Context, _ domain.SessionID, checkID string, res domain.SmokeAgentResult) (domain.SmokeCheck, error) {
	if f.agentErr != nil {
		return domain.SmokeCheck{}, f.agentErr
	}
	f.agentResult = res
	return domain.SmokeCheck{ID: checkID, AgentVerdict: res.Verdict, AgentNote: res.Note, AgentSHA: res.SHA}, nil
}

func (f *fakeSmokeService) Retire(_ context.Context, _ domain.SessionID, checkID, reason string) (domain.SmokeCheck, error) {
	if f.retireErr != nil {
		return domain.SmokeCheck{}, f.retireErr
	}
	f.retiredCaseID, f.retiredReason = checkID, reason
	retiredAt := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)
	return domain.SmokeCheck{ID: checkID, RetiredAt: &retiredAt, RetiredReason: reason}, nil
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

// TestSmokeAuthorNonASCIINamesNeverReturn500 drives the REAL service over a
// REAL store, because the crash it guards lives below the controller: case ids
// are derived from the name, and smoke_check.id is a global primary key, so two
// sessions whose names derive the same id used to fail the insert and surface
// as a 500. A Thai name slugged to nothing, making every such checklist derive
// the same constant id, so the second session to author one always crashed.
func TestSmokeAuthorNonASCIINamesNeverReturn500(t *testing.T) {
	st, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	if err := st.UpsertProject(ctx, domain.ProjectRecord{ID: "proj", Path: "/tmp/proj", RegisteredAt: now}); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	var ids []domain.SessionID
	for i := 0; i < 2; i++ {
		rec, err := st.CreateSession(ctx, domain.SessionRecord{
			ProjectID: "proj", Kind: domain.KindWorker,
			Activity:  domain.Activity{State: domain.ActivityIdle, LastActivityAt: now},
			CreatedAt: now, UpdatedAt: now,
		})
		if err != nil {
			t.Fatalf("seed session %d: %v", i, err)
		}
		ids = append(ids, rec.ID)
	}
	w1, w2 := string(ids[0]), string(ids[1])
	srv := newSmokeTestServer(t, smokesvc.New(st, t.TempDir(), nil))

	for _, tc := range []struct {
		session string
		payload string
	}{
		// Thai names, no explicit id - the path workers now take by default.
		{w1, `{"cases":[{"name":"เปิดแอปแล้วเห็นหน้าแรก","why":"ก","steps":["เปิดแอป"],"expected":"เห็นหน้าแรก"},{"name":"กดปุ่มบันทึกแล้วขึ้นข้อความสำเร็จ","why":"ข","steps":["กดบันทึก"],"expected":"สำเร็จ"}]}`},
		// A second session authoring its own Thai checklist: used to 500.
		{w2, `{"cases":[{"name":"ลบรายการแล้วหายจากลิสต์","why":"ค","steps":["กดลบ"],"expected":"หาย"}]}`},
		// Other scripts and punctuation-only names slug to nothing too.
		{w2, `{"cases":[{"name":"日本語のケース名"},{"name":"---"},{"name":"мой случай"}]}`},
		// Re-author must stay accepted and keep ids stable.
		{w2, `{"cases":[{"name":"日本語のケース名"},{"name":"---"},{"name":"мой случай"}]}`},
	} {
		body, status, _ := doRequest(t, srv, "PUT", "/api/v1/sessions/"+tc.session+"/smoke-checks", tc.payload)
		if status != http.StatusOK {
			t.Fatalf("session %s: status = %d, want 200; body=%s", tc.session, status, body)
		}
		if strings.Contains(string(body), `"id":""`) {
			t.Fatalf("session %s: a case got an empty id: %s", tc.session, body)
		}
	}

	byName := func(session domain.SessionID) map[string]string {
		t.Helper()
		checks, err := st.ListSmokeChecksBySession(ctx, session)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		out := map[string]string{}
		for _, c := range checks {
			if c.ID == "" {
				t.Fatal("stored an empty id")
			}
			if prior, dup := out[c.Name]; dup {
				t.Fatalf("duplicate name %q at ids %q and %q", c.Name, prior, c.ID)
			}
			out[c.Name] = c.ID
		}
		return out
	}

	before := byName(ids[1])
	if len(before) != 3 {
		t.Fatalf("checks = %d, want 3", len(before))
	}

	// Drop the FIRST case and re-author. An id derived from the name survives
	// this; a positional one silently slides onto the wrong case, handing the
	// dropped case's verdict, note and evidence to its neighbour.
	body, status, _ := doRequest(t, srv, "PUT", "/api/v1/sessions/"+w2+"/smoke-checks",
		`{"cases":[{"name":"---"},{"name":"мой случай"}]}`)
	if status != http.StatusOK {
		t.Fatalf("re-author after drop: status = %d body=%s", status, body)
	}
	after := byName(ids[1])
	for _, name := range []string{"---", "мой случай"} {
		if after[name] != before[name] {
			t.Errorf("id for %q shifted when another case was dropped: %q then %q", name, before[name], after[name])
		}
	}

	// The underlying mechanism, independent of the script: two sessions that
	// pick the SAME case name derive the same id, and the id column is global.
	// Both must be accepted, on distinct ids.
	shared := `{"cases":[{"name":"แอปเปิดได้ไม่ค้าง"},{"name":"Build passes"}]}`
	for _, session := range []string{w1, w2} {
		body, status, _ := doRequest(t, srv, "PUT", "/api/v1/sessions/"+session+"/smoke-checks", shared)
		if status != http.StatusOK {
			t.Fatalf("session %s shared names: status = %d, want 200; body=%s", session, status, body)
		}
	}
	first, second := byName(ids[0]), byName(ids[1])
	for _, name := range []string{"แอปเปิดได้ไม่ค้าง", "Build passes"} {
		if first[name] == "" || second[name] == "" {
			t.Fatalf("case %q missing an id: %q / %q", name, first[name], second[name])
		}
		if first[name] == second[name] {
			t.Errorf("both sessions got id %q for %q; the id column is global", first[name], name)
		}
	}
	// The first session keeps the historical slug; only the loser moves.
	if first["Build passes"] != "build-passes" {
		t.Errorf("first session id = %q, want the unchanged slug", first["Build passes"])
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

// TestSmokeReauthorRewordedNameKeepsHumanResults drives the REAL service over a
// REAL store because the loss it guards lives below the controller: a case id is
// derived from the case NAME, so an agent that merely rewords a name produces a
// different id, the old case falls out of the payload, and the author call used
// to delete it - taking the verdict, the note and the evidence blob the user
// recorded while playing it.
func TestSmokeReauthorRewordedNameKeepsHumanResults(t *testing.T) {
	st, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	if err := st.UpsertProject(ctx, domain.ProjectRecord{ID: "proj", Path: "/tmp/proj", RegisteredAt: now}); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	rec, err := st.CreateSession(ctx, domain.SessionRecord{
		ProjectID: "proj", Kind: domain.KindWorker,
		Activity:  domain.Activity{State: domain.ActivityIdle, LastActivityAt: now},
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("seed session: %v", err)
	}
	session := string(rec.ID)
	dataDir := t.TempDir()
	srv := newSmokeTestServer(t, smokesvc.New(st, dataDir, nil))
	base := "/api/v1/sessions/" + session + "/smoke-checks"

	// 1. The worker authors the checklist.
	body, status, _ := doRequest(t, srv, "PUT", base,
		`{"cases":[{"name":"A fresh MR shows up in Reviews","why":"w","steps":["open"],"expected":"e"},{"name":"Second case","why":"w2","steps":["x"],"expected":"e2"}]}`)
	if status != http.StatusOK {
		t.Fatalf("author: status = %d body=%s", status, body)
	}

	// 2. The user plays the first case: verdict + note + a screenshot.
	checkID := "a-fresh-mr-shows-up-in-reviews"
	body, status, _ = doRequest(t, srv, "POST", base+"/"+checkID+"/verdict", `{"verdict":"pass","note":"looked right"}`)
	if status != http.StatusOK {
		t.Fatalf("verdict: status = %d body=%s", status, body)
	}
	evidenceID := uploadSmokeEvidence(t, srv, session, checkID)
	blob := filepath.Join(dataDir, "evidence", session, checkID, evidenceID)
	if _, err := os.Stat(blob); err != nil {
		t.Fatalf("evidence blob not written: %v", err)
	}

	// 3. The worker re-authors and merely REWORDS the played case's name.
	body, status, _ = doRequest(t, srv, "PUT", base,
		`{"cases":[{"name":"A fresh MR shows up in the Reviews tab","why":"w","steps":["open"],"expected":"e"},{"name":"Second case","why":"w2","steps":["x"],"expected":"e2"}]}`)
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("re-author with a reworded played case: status = %d, want 422; body=%s", status, body)
	}
	for _, want := range []string{"A fresh MR shows up in Reviews", checkID} {
		if !strings.Contains(string(body), want) {
			t.Errorf("refusal does not name the case at risk (%q): %s", want, body)
		}
	}

	// 4. Nothing the user recorded was touched.
	got, ok, err := st.GetSmokeCheck(ctx, checkID)
	if err != nil || !ok {
		t.Fatalf("played case gone: ok=%v err=%v", ok, err)
	}
	if got.Verdict != domain.SmokePass || got.Note != "looked right" || len(got.Evidence) != 1 {
		t.Fatalf("results lost: verdict=%q note=%q evidence=%d", got.Verdict, got.Note, len(got.Evidence))
	}
	if _, err := os.Stat(blob); err != nil {
		t.Fatalf("evidence blob deleted: %v", err)
	}
}

// uploadSmokeEvidence posts one screenshot the way the Tests tab does and
// returns the stored evidence id.
func uploadSmokeEvidence(t *testing.T, srv *httptest.Server, session, checkID string) string {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	hdr := map[string][]string{
		"Content-Disposition": {`form-data; name="file"; filename="shot.png"`},
		"Content-Type":        {"image/png"},
	}
	part, err := mw.CreatePart(hdr)
	if err != nil {
		t.Fatalf("create part: %v", err)
	}
	if _, err := part.Write([]byte("PNGBYTES")); err != nil {
		t.Fatalf("write part: %v", err)
	}
	_ = mw.Close()
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/sessions/"+session+"/smoke-checks/"+checkID+"/evidence", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("upload evidence: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("upload evidence: status = %d body=%s", resp.StatusCode, body)
	}
	var out struct {
		Evidence domain.SmokeEvidence `json:"evidence"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode evidence: %v", err)
	}
	return out.Evidence.ID
}

// TestSmokeRecordAgentResultRoute: the machine's result travels its own endpoint
// and its own body, so nothing on the user's verdict route can reach it or be
// reached by it.
func TestSmokeRecordAgentResultRoute(t *testing.T) {
	svc := &fakeSmokeService{}
	srv := newSmokeTestServer(t, svc)
	body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions/w1/smoke-checks/a/agent-result",
		`{"verdict":"pass","note":"ran clean","sha":"abc123"}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d body=%s", status, body)
	}
	if svc.agentResult.Verdict != domain.SmokePass || svc.agentResult.Note != "ran clean" || svc.agentResult.SHA != "abc123" {
		t.Fatalf("agent result not mapped: %+v", svc.agentResult)
	}
	for _, want := range []string{`"agentVerdict":"pass"`, `"agentSha":"abc123"`} {
		if !strings.Contains(string(body), want) {
			t.Errorf("body missing %s: %s", want, body)
		}
	}
}

func TestSmokeRetireRoute(t *testing.T) {
	svc := &fakeSmokeService{}
	srv := newSmokeTestServer(t, svc)
	body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions/w1/smoke-checks/a/retire",
		`{"reason":"now covered by TestA"}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d body=%s", status, body)
	}
	if svc.retiredCaseID != "a" || svc.retiredReason != "now covered by TestA" {
		t.Fatalf("retire not mapped: %q/%q", svc.retiredCaseID, svc.retiredReason)
	}
	for _, want := range []string{`"retiredAt"`, `"retiredReason":"now covered by TestA"`} {
		if !strings.Contains(string(body), want) {
			t.Errorf("body missing %s: %s", want, body)
		}
	}
}

// TestSmokeRetiredCaseMapsTo422: "this case is frozen, and here is why it went"
// is a different answer from "no such case", and the caller has to be able to
// tell them apart.
func TestSmokeRetiredCaseMapsTo422(t *testing.T) {
	svc := &fakeSmokeService{agentErr: fmt.Errorf("%w: %q was retired: now covered by TestA", smokesvc.ErrCaseRetired, "case a")}
	srv := newSmokeTestServer(t, svc)
	body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions/w1/smoke-checks/a/agent-result", `{"verdict":"pass"}`)
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body=%s", status, body)
	}
	for _, want := range []string{`"SMOKE_CASE_RETIRED"`, "now covered by TestA"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("body missing %s: %s", want, body)
		}
	}
}

// TestSmokeEvidenceSourceParam: provenance rides on the query string so it works
// for both upload body shapes, and defaults to the user for every caller that
// does not send it - which is every caller that exists today.
func TestSmokeEvidenceSourceParam(t *testing.T) {
	for _, tc := range []struct {
		path string
		want domain.SmokeEvidenceSource
	}{
		{"/api/v1/sessions/w1/smoke-checks/a/evidence", domain.SmokeEvidenceUser},
		{"/api/v1/sessions/w1/smoke-checks/a/evidence?source=agent", domain.SmokeEvidenceAgent},
		{"/api/v1/sessions/w1/smoke-checks/a/evidence?source=nonsense", domain.SmokeEvidenceUser},
	} {
		svc := &fakeSmokeService{}
		srv := newSmokeTestServer(t, svc)
		req, err := http.NewRequest(http.MethodPost, srv.URL+tc.path, strings.NewReader("PNG"))
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		req.Header.Set("Content-Type", "image/png")
		req.Header.Set("X-Filename", "shot.png")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("upload: %v", err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d for %s", resp.StatusCode, tc.path)
		}
		if svc.lastUpload.Source != tc.want {
			t.Errorf("%s: source = %q, want %q", tc.path, svc.lastUpload.Source, tc.want)
		}
	}
}
