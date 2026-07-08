package controllers_test

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
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
