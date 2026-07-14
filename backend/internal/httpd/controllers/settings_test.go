package controllers_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/autonudge"
	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/evidenceretention"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/promptoverrides"
	"github.com/aoagents/agent-orchestrator/backend/internal/prompts"
	"github.com/aoagents/agent-orchestrator/backend/internal/reclaimsettings"
	"github.com/aoagents/agent-orchestrator/backend/internal/spawnconfirm"
)

type fakeSettingsSvc struct {
	cur   reclaimsettings.Settings
	saved reclaimsettings.Settings
	err   error
}

func (f *fakeSettingsSvc) Get() reclaimsettings.Settings { return f.cur }

func (f *fakeSettingsSvc) Set(s reclaimsettings.Settings) error {
	if f.err != nil {
		return f.err
	}
	f.saved = s
	f.cur = s
	return nil
}

func newSettingsTestServer(t *testing.T, svc *fakeSettingsSvc) *httptest.Server {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{Settings: svc}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	return srv
}

func TestSettingsRoutes_DefaultToStubsWithoutService(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)

	body, status, headers := doRequest(t, srv, "GET", "/api/v1/settings/reclaim", "")
	assertJSON(t, headers)
	assertErrorCode(t, body, status, http.StatusNotImplemented, "NOT_IMPLEMENTED")
}

func TestSettingsController_GetReturnsCurrent(t *testing.T) {
	svc := &fakeSettingsSvc{cur: reclaimsettings.Settings{Enabled: true, GraceMinutes: 15}}
	srv := newSettingsTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "GET", "/api/v1/settings/reclaim", "")
	if status != http.StatusOK {
		t.Fatalf("code=%d body=%s", status, body)
	}
	var got reclaimSettingsBody
	mustJSON(t, body, &got)
	if !got.Enabled || got.GraceMinutes != 15 {
		t.Fatalf("got = %#v", got)
	}
}

func TestSettingsController_PutValidatesAndSaves(t *testing.T) {
	svc := &fakeSettingsSvc{}
	srv := newSettingsTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "PUT", "/api/v1/settings/reclaim", `{"enabled":false,"graceMinutes":30}`)
	if status != http.StatusOK {
		t.Fatalf("code=%d body=%s", status, body)
	}
	var got reclaimSettingsBody
	mustJSON(t, body, &got)
	if got.Enabled || got.GraceMinutes != 30 {
		t.Fatalf("response = %#v", got)
	}
	if svc.saved.GraceMinutes != 30 || svc.saved.Enabled {
		t.Fatalf("saved=%+v", svc.saved)
	}
}

func TestSettingsController_PutInvalidJSON(t *testing.T) {
	srv := newSettingsTestServer(t, &fakeSettingsSvc{})

	body, status, _ := doRequest(t, srv, "PUT", "/api/v1/settings/reclaim", `{`)
	assertErrorCode(t, body, status, http.StatusBadRequest, "INVALID_JSON")
}

func TestSettingsController_PutServiceRejectsInvalidSettings(t *testing.T) {
	svc := &fakeSettingsSvc{err: errors.New("reclaimsettings: graceMinutes must be >= 0, got -1")}
	srv := newSettingsTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "PUT", "/api/v1/settings/reclaim", `{"enabled":true,"graceMinutes":-1}`)
	assertErrorCode(t, body, status, http.StatusBadRequest, "INVALID_SETTINGS")
}

type reclaimSettingsBody struct {
	Enabled      bool `json:"enabled"`
	GraceMinutes int  `json:"graceMinutes"`
}

// --- evidence retention settings + manual sweep ----------------------------

type fakeEvidenceRetentionSvc struct {
	cur   evidenceretention.Settings
	saved evidenceretention.Settings
	err   error
}

func (f *fakeEvidenceRetentionSvc) Get() evidenceretention.Settings { return f.cur }

func (f *fakeEvidenceRetentionSvc) Set(s evidenceretention.Settings) error {
	if f.err != nil {
		return f.err
	}
	f.saved = s
	f.cur = s
	return nil
}

type fakeEvidenceSweeper struct {
	purged int
	freed  int64
	called bool
}

func (f *fakeEvidenceSweeper) SweepEvidenceNow(context.Context) (int, int64, error) {
	f.called = true
	return f.purged, f.freed, nil
}

type evidenceRetentionBody struct {
	Enabled    bool `json:"enabled"`
	MaxAgeDays int  `json:"maxAgeDays"`
}

func TestEvidenceRetentionRoutes_DefaultToStubsWithoutService(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)

	body, status, headers := doRequest(t, srv, "GET", "/api/v1/settings/evidence-retention", "")
	assertJSON(t, headers)
	assertErrorCode(t, body, status, http.StatusNotImplemented, "NOT_IMPLEMENTED")

	body, status, _ = doRequest(t, srv, "POST", "/api/v1/settings/evidence-retention/sweep", "")
	assertErrorCode(t, body, status, http.StatusNotImplemented, "NOT_IMPLEMENTED")
}

func TestEvidenceRetentionController_GetReturnsCurrent(t *testing.T) {
	svc := &fakeEvidenceRetentionSvc{cur: evidenceretention.Settings{Enabled: true, MaxAgeDays: 30}}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{EvidenceRetention: svc}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)

	body, status, _ := doRequest(t, srv, "GET", "/api/v1/settings/evidence-retention", "")
	if status != http.StatusOK {
		t.Fatalf("code=%d body=%s", status, body)
	}
	var got evidenceRetentionBody
	mustJSON(t, body, &got)
	if !got.Enabled || got.MaxAgeDays != 30 {
		t.Fatalf("got = %#v", got)
	}
}

func TestEvidenceRetentionController_PutSaves(t *testing.T) {
	svc := &fakeEvidenceRetentionSvc{}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{EvidenceRetention: svc}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)

	body, status, _ := doRequest(t, srv, "PUT", "/api/v1/settings/evidence-retention", `{"enabled":false,"maxAgeDays":7}`)
	if status != http.StatusOK {
		t.Fatalf("code=%d body=%s", status, body)
	}
	var got evidenceRetentionBody
	mustJSON(t, body, &got)
	if got.Enabled || got.MaxAgeDays != 7 {
		t.Fatalf("response = %#v", got)
	}
	if svc.saved.MaxAgeDays != 7 || svc.saved.Enabled {
		t.Fatalf("saved=%+v", svc.saved)
	}
}

func TestEvidenceRetentionController_PutRejectsInvalid(t *testing.T) {
	svc := &fakeEvidenceRetentionSvc{err: errors.New("evidenceretention: maxAgeDays must be >= 0, got -1")}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{EvidenceRetention: svc}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)

	body, status, _ := doRequest(t, srv, "PUT", "/api/v1/settings/evidence-retention", `{"enabled":true,"maxAgeDays":-1}`)
	assertErrorCode(t, body, status, http.StatusBadRequest, "INVALID_SETTINGS")
}

func TestEvidenceRetentionController_SweepReturnsSummary(t *testing.T) {
	sweeper := &fakeEvidenceSweeper{purged: 3, freed: 4096}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{EvidenceSweeper: sweeper}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)

	body, status, _ := doRequest(t, srv, "POST", "/api/v1/settings/evidence-retention/sweep", "")
	if status != http.StatusOK {
		t.Fatalf("code=%d body=%s", status, body)
	}
	var got struct {
		Purged     int   `json:"purged"`
		FreedBytes int64 `json:"freedBytes"`
	}
	mustJSON(t, body, &got)
	if got.Purged != 3 || got.FreedBytes != 4096 {
		t.Fatalf("sweep response = %#v", got)
	}
	if !sweeper.called {
		t.Fatal("sweeper was not invoked")
	}
}

type fakeSpawnConfirmSvc struct {
	cur   spawnconfirm.Settings
	saved spawnconfirm.Settings
	err   error
}

func (f *fakeSpawnConfirmSvc) Get() spawnconfirm.Settings { return f.cur }

func (f *fakeSpawnConfirmSvc) Set(s spawnconfirm.Settings) error {
	if f.err != nil {
		return f.err
	}
	f.saved = s
	f.cur = s
	return nil
}

func newSpawnConfirmTestServer(t *testing.T, svc *fakeSpawnConfirmSvc) *httptest.Server {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{SpawnConfirm: svc}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	return srv
}

func TestSpawnConfirmRoutes_DefaultToStubsWithoutService(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)

	body, status, headers := doRequest(t, srv, "GET", "/api/v1/settings/spawn-confirm", "")
	assertJSON(t, headers)
	assertErrorCode(t, body, status, http.StatusNotImplemented, "NOT_IMPLEMENTED")
}

func TestSpawnConfirmController_GetReturnsCurrent(t *testing.T) {
	svc := &fakeSpawnConfirmSvc{cur: spawnconfirm.Settings{Enabled: true}}
	srv := newSpawnConfirmTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "GET", "/api/v1/settings/spawn-confirm", "")
	if status != http.StatusOK {
		t.Fatalf("code=%d body=%s", status, body)
	}
	var got spawnConfirmSettingsBody
	mustJSON(t, body, &got)
	if !got.Enabled {
		t.Fatalf("got = %#v", got)
	}
}

func TestSpawnConfirmController_PutSaves(t *testing.T) {
	svc := &fakeSpawnConfirmSvc{cur: spawnconfirm.Settings{Enabled: true}}
	srv := newSpawnConfirmTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "PUT", "/api/v1/settings/spawn-confirm", `{"enabled":false}`)
	if status != http.StatusOK {
		t.Fatalf("code=%d body=%s", status, body)
	}
	var got spawnConfirmSettingsBody
	mustJSON(t, body, &got)
	if got.Enabled {
		t.Fatalf("response = %#v", got)
	}
	if svc.saved.Enabled {
		t.Fatalf("saved=%+v, want disabled", svc.saved)
	}
}

func TestSpawnConfirmController_PutInvalidJSON(t *testing.T) {
	srv := newSpawnConfirmTestServer(t, &fakeSpawnConfirmSvc{})

	body, status, _ := doRequest(t, srv, "PUT", "/api/v1/settings/spawn-confirm", `{`)
	assertErrorCode(t, body, status, http.StatusBadRequest, "INVALID_JSON")
}

type spawnConfirmSettingsBody struct {
	Enabled bool `json:"enabled"`
}

type fakeAutoNudgeSvc struct {
	cur   autonudge.Settings
	saved autonudge.Settings
	err   error
}

func (f *fakeAutoNudgeSvc) Get() autonudge.Settings { return f.cur }

func (f *fakeAutoNudgeSvc) Set(s autonudge.Settings) error {
	if f.err != nil {
		return f.err
	}
	f.saved = s
	f.cur = s
	return nil
}

func newAutoNudgeTestServer(t *testing.T, svc *fakeAutoNudgeSvc) *httptest.Server {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{AutoNudge: svc}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	return srv
}

func TestAutoNudgeRoutes_DefaultToStubsWithoutService(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)

	body, status, headers := doRequest(t, srv, "GET", "/api/v1/settings/auto-nudge", "")
	assertJSON(t, headers)
	assertErrorCode(t, body, status, http.StatusNotImplemented, "NOT_IMPLEMENTED")
}

func TestAutoNudgeController_GetReturnsCurrent(t *testing.T) {
	svc := &fakeAutoNudgeSvc{cur: autonudge.Settings{Enabled: false}}
	srv := newAutoNudgeTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "GET", "/api/v1/settings/auto-nudge", "")
	if status != http.StatusOK {
		t.Fatalf("code=%d body=%s", status, body)
	}
	var got autoNudgeSettingsBody
	mustJSON(t, body, &got)
	if got.Enabled {
		t.Fatalf("got = %#v", got)
	}
}

func TestAutoNudgeController_PutSaves(t *testing.T) {
	svc := &fakeAutoNudgeSvc{cur: autonudge.Settings{Enabled: false}}
	srv := newAutoNudgeTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "PUT", "/api/v1/settings/auto-nudge", `{"enabled":true}`)
	if status != http.StatusOK {
		t.Fatalf("code=%d body=%s", status, body)
	}
	var got autoNudgeSettingsBody
	mustJSON(t, body, &got)
	if !got.Enabled {
		t.Fatalf("response = %#v", got)
	}
	if !svc.saved.Enabled {
		t.Fatalf("saved=%+v, want enabled", svc.saved)
	}
}

func TestAutoNudgeController_PutInvalidJSON(t *testing.T) {
	srv := newAutoNudgeTestServer(t, &fakeAutoNudgeSvc{})

	body, status, _ := doRequest(t, srv, "PUT", "/api/v1/settings/auto-nudge", `{`)
	assertErrorCode(t, body, status, http.StatusBadRequest, "INVALID_JSON")
}

type autoNudgeSettingsBody struct {
	Enabled bool `json:"enabled"`
}

type fakeSystemPromptsSvc struct {
	ov      promptoverrides.Overrides
	setKind prompts.Kind
	setVal  string
	cleared prompts.Kind
	setErr  error
}

func (f *fakeSystemPromptsSvc) Get() promptoverrides.Overrides { return f.ov }
func (f *fakeSystemPromptsSvc) SetBase(k prompts.Kind, v string) error {
	if f.setErr != nil {
		return f.setErr
	}
	f.setKind, f.setVal = k, v
	if f.ov.Base == nil {
		f.ov.Base = map[prompts.Kind]string{}
	}
	f.ov.Base[k] = v
	return nil
}
func (f *fakeSystemPromptsSvc) ClearBase(k prompts.Kind) error {
	f.cleared = k
	delete(f.ov.Base, k)
	return nil
}

func newPromptsTestServer(t *testing.T, svc *fakeSystemPromptsSvc) *httptest.Server {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{SystemPrompts: svc}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	return srv
}

func TestSystemPrompts_GetReturnsDefaultAndOverride(t *testing.T) {
	svc := &fakeSystemPromptsSvc{ov: promptoverrides.Overrides{Base: map[prompts.Kind]string{prompts.KindWorker: "custom"}}}
	srv := newPromptsTestServer(t, svc)
	body, status, _ := doRequest(t, srv, "GET", "/api/v1/settings/prompts", "")
	if status != http.StatusOK {
		t.Fatalf("code=%d body=%s", status, body)
	}
	// worker item has override "custom"; orchestrator item has nil override and a
	// non-empty default carrying the placeholder.
	if !strings.Contains(string(body), `"custom"`) || !strings.Contains(string(body), prompts.ProjectIDPlaceholder) {
		t.Fatalf("body missing expected content: %s", body)
	}
}

func TestSystemPrompts_PutSetsOverride(t *testing.T) {
	svc := &fakeSystemPromptsSvc{}
	srv := newPromptsTestServer(t, svc)
	_, status, _ := doRequest(t, srv, "PUT", "/api/v1/settings/prompts/worker", `{"base":"new base"}`)
	if status != http.StatusOK || svc.setKind != prompts.KindWorker || svc.setVal != "new base" {
		t.Fatalf("status=%d set=%q/%q", status, svc.setKind, svc.setVal)
	}
}

func TestSystemPrompts_PutUnknownKind400(t *testing.T) {
	srv := newPromptsTestServer(t, &fakeSystemPromptsSvc{})
	body, status, _ := doRequest(t, srv, "PUT", "/api/v1/settings/prompts/bogus", `{"base":"x"}`)
	assertErrorCode(t, body, status, http.StatusBadRequest, "INVALID_SETTINGS")
}

func TestSystemPrompts_DeleteClears(t *testing.T) {
	svc := &fakeSystemPromptsSvc{ov: promptoverrides.Overrides{Base: map[prompts.Kind]string{prompts.KindReviewer: "x"}}}
	srv := newPromptsTestServer(t, svc)
	_, status, _ := doRequest(t, srv, "DELETE", "/api/v1/settings/prompts/reviewer", "")
	if status != http.StatusOK || svc.cleared != prompts.KindReviewer {
		t.Fatalf("status=%d cleared=%q", status, svc.cleared)
	}
}

func TestSystemPrompts_StubbedWithoutService501(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	body, status, _ := doRequest(t, srv, "GET", "/api/v1/settings/prompts", "")
	assertErrorCode(t, body, status, http.StatusNotImplemented, "NOT_IMPLEMENTED")
}

type fakeMessageTemplates struct {
	overrides map[string]string
	setErr    error
}

func (f *fakeMessageTemplates) Get() promptoverrides.Overrides {
	cp := map[string]string{}
	for k, v := range f.overrides {
		cp[k] = v
	}
	return promptoverrides.Overrides{Templates: cp}
}
func (f *fakeMessageTemplates) SetTemplate(name, text string) error {
	if f.setErr != nil {
		return f.setErr
	}
	if f.overrides == nil {
		f.overrides = map[string]string{}
	}
	f.overrides[name] = text
	return nil
}
func (f *fakeMessageTemplates) ClearTemplate(name string) error {
	delete(f.overrides, name)
	return nil
}

func newMessageTemplatesTestServer(t *testing.T, svc *fakeMessageTemplates) *httptest.Server {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{MessageTemplates: svc}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	return srv
}

func TestMessageTemplatesAPI_GetListsAllWithDefaults(t *testing.T) {
	svc := &fakeMessageTemplates{overrides: map[string]string{"ci-failing": "custom"}}
	srv := newMessageTemplatesTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "GET", "/api/v1/settings/message-templates", "")
	if status != http.StatusOK {
		t.Fatalf("status %d: %s", status, body)
	}
	if !strings.Contains(string(body), `"name":"review-comment-dispatch"`) {
		t.Fatalf("missing review-comment-dispatch: %s", body)
	}
	if !strings.Contains(string(body), `"override":"custom"`) {
		t.Fatalf("ci-failing override not surfaced: %s", body)
	}

	var got controllers.MessageTemplatesResponse
	mustJSON(t, body, &got)
	wantNames := []string{
		"review-comment-dispatch", "ci-failing", "merge-conflict",
		"tracker-bot-comment", "ao-reviewer-batch", "ao-reviewer-single",
	}
	if len(got.Templates) != len(wantNames) {
		t.Fatalf("want %d templates, got %d: %+v", len(wantNames), len(got.Templates), got.Templates)
	}
	byName := make(map[string]controllers.MessageTemplateItem, len(got.Templates))
	for _, item := range got.Templates {
		byName[item.Name] = item
	}
	for _, name := range wantNames {
		if _, ok := byName[name]; !ok {
			t.Fatalf("missing template %q in response: %+v", name, got.Templates)
		}
	}

	// merge-conflict now documents the PR-context placeholders (#2492 enrichment:
	// {{.PRIdentity}} / {{.PRURL}}).
	mc, ok := byName["merge-conflict"]
	if !ok {
		t.Fatalf("missing merge-conflict template: %+v", got.Templates)
	}
	if len(mc.Placeholders) != 2 {
		t.Fatalf("merge-conflict should document the PR-context placeholders, got %v", mc.Placeholders)
	}
	// Placeholders must always serialize as a JSON array, never null: a nil slice
	// violates the OpenAPI schema's required non-nullable array and previously
	// crashed the frontend's Global Settings renderer (t.placeholders.length threw
	// on null) every time the section opened.
	if strings.Contains(string(body), `"placeholders":null`) {
		t.Fatalf("placeholders must never serialize as null: %s", body)
	}
}

func TestMessageTemplatesAPI_SetAndClear(t *testing.T) {
	fake := &fakeMessageTemplates{}
	srv := newMessageTemplatesTestServer(t, fake)

	_, status, _ := doRequest(t, srv, "PUT", "/api/v1/settings/message-templates/ci-failing", `{"template":"hi"}`)
	if status != http.StatusOK {
		t.Fatalf("PUT status %d", status)
	}
	if fake.overrides["ci-failing"] != "hi" {
		t.Fatalf("override not stored: %v", fake.overrides)
	}

	_, status, _ = doRequest(t, srv, "PUT", "/api/v1/settings/message-templates/bogus", `{"template":"x"}`)
	if status != http.StatusBadRequest {
		t.Fatalf("unknown name should be 400, got %d", status)
	}

	_, status, _ = doRequest(t, srv, "DELETE", "/api/v1/settings/message-templates/ci-failing", "")
	if status != http.StatusOK {
		t.Fatalf("DELETE status %d", status)
	}
	if _, ok := fake.overrides["ci-failing"]; ok {
		t.Fatalf("override not cleared: %v", fake.overrides)
	}
}

func TestMessageTemplates_StubbedWithoutService501(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	body, status, _ := doRequest(t, srv, "GET", "/api/v1/settings/message-templates", "")
	assertErrorCode(t, body, status, http.StatusNotImplemented, "NOT_IMPLEMENTED")
}
