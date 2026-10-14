package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	rt "github.com/aoagents/agent-orchestrator/backend/internal/releasetrain"
	rtsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/releasetrain"
)

type fakeReleaseTrains struct {
	report  rtsvc.Report
	initErr error
	inits   int
}

func (f *fakeReleaseTrains) Report(context.Context) (rtsvc.Report, error) { return f.report, nil }

func (f *fakeReleaseTrains) InitConfig() (string, error) {
	f.inits++
	return "/data/release-train.json", f.initErr
}

func serveReleaseTrains(t *testing.T, svc ReleaseTrainsService, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	(&ReleaseTrainsController{Svc: svc}).Register(r)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(method, path, nil))
	return w
}

func TestReleaseTrainSourcesListsConfigResourcesAndLastPass(t *testing.T) {
	done := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	next := done.Add(time.Minute)
	svc := &fakeReleaseTrains{report: rtsvc.Report{
		Config: rtsvc.ConfigReport{State: rtsvc.ConfigOK, Path: "/data/release-train.json"},
		Resources: []rtsvc.ResourceReport{
			{Kind: "gitlab.mrs", Scope: "p:1001", App: "di", Mode: rt.Incremental, LastAttemptAt: &done, Backfilling: true},
			{Kind: "gitlab.tags", Scope: "p:1001", App: "di", Mode: rt.Snapshotted, CompleteAt: &done, LastAttemptAt: &done,
				ErrorClass: rt.ErrRateLimited, ErrorMessage: "gitlab scm: 429", NextAttemptAt: &next},
		},
		LastPass: rtsvc.PassStats{At: &done, Requests: map[string]int{"gitlab": 7, "jira": 3}},
	}}
	w := serveReleaseTrains(t, svc, http.MethodGet, "/release-trains/sources")
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	var got ReleaseTrainSourcesResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Config.State != "ok" || got.Config.Path != "/data/release-train.json" || len(got.Resources) != 2 {
		t.Fatalf("response = %+v", got)
	}
	mrs, tags := got.Resources[0], got.Resources[1]
	if !mrs.Backfilling || mrs.Mode != "incremental" || mrs.CompleteAt != nil || mrs.App != "di" {
		t.Fatalf("mrs = %+v", mrs)
	}
	if tags.ErrorClass != "rate_limited" || tags.ErrorMessage != "gitlab scm: 429" || !tags.NextAttemptAt.Equal(next) || !tags.CompleteAt.Equal(done) {
		t.Fatalf("tags = %+v", tags)
	}
	if got.LastPass.At == nil || got.LastPass.Requests["gitlab"] != 7 || got.LastPass.Requests["jira"] != 3 {
		t.Fatalf("last pass = %+v", got.LastPass)
	}
	if !strings.Contains(w.Body.String(), `"resources":[`) {
		t.Fatalf("body = %s", w.Body)
	}
}

func TestReleaseTrainSourcesWithoutConfigListsNoResources(t *testing.T) {
	svc := &fakeReleaseTrains{report: rtsvc.Report{Config: rtsvc.ConfigReport{State: rtsvc.ConfigAbsent, Message: "no config; run `ao train config init`"}}}
	w := serveReleaseTrains(t, svc, http.MethodGet, "/release-trains/sources")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"resources":[]`) || !strings.Contains(w.Body.String(), `"state":"absent"`) {
		t.Fatalf("status %d body %s", w.Code, w.Body)
	}
}

func TestReleaseTrainConfigInitWritesOnceThenConflicts(t *testing.T) {
	svc := &fakeReleaseTrains{}
	w := serveReleaseTrains(t, svc, http.MethodPost, "/release-trains/config")
	if w.Code != http.StatusCreated || !strings.Contains(w.Body.String(), `"path":"/data/release-train.json"`) {
		t.Fatalf("first: %d %s", w.Code, w.Body)
	}
	svc.initErr = rtsvc.ErrConfigExists
	w = serveReleaseTrains(t, svc, http.MethodPost, "/release-trains/config")
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), `"code":"RELEASE_TRAIN_CONFIG_EXISTS"`) {
		t.Fatalf("second: %d %s", w.Code, w.Body)
	}
	svc.initErr = errors.New("disk full")
	if w = serveReleaseTrains(t, svc, http.MethodPost, "/release-trains/config"); w.Code != http.StatusInternalServerError {
		t.Fatalf("other error: %d %s", w.Code, w.Body)
	}
}

func TestReleaseTrainRoutesWithoutAServiceAreNotImplemented(t *testing.T) {
	for _, m := range []string{http.MethodGet, http.MethodPost} {
		path := "/release-trains/sources"
		if m == http.MethodPost {
			path = "/release-trains/config"
		}
		if w := serveReleaseTrains(t, nil, m, path); w.Code != http.StatusNotImplemented {
			t.Fatalf("%s %s = %d", m, path, w.Code)
		}
	}
}
