package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	rt "github.com/aoagents/agent-orchestrator/backend/internal/releasetrain"
	rtsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/releasetrain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

type fakeReleaseTrains struct {
	*rtsvc.BoardService
	avatars   *rtsvc.AvatarStore
	report    rtsvc.Report
	initErr   error
	inits     int
	refreshed []string
}

func (f *fakeReleaseTrains) Report(context.Context) (rtsvc.Report, error) { return f.report, nil }

func (f *fakeReleaseTrains) Avatar(accountID string) ([]byte, error) { return f.avatars.Get(accountID) }

func (f *fakeReleaseTrains) Refresh(source string) error {
	if source != "" && source != "gitlab" && source != "jira" {
		return rtsvc.ErrUnknownSource
	}
	f.refreshed = append(f.refreshed, source)
	return nil
}

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
	for _, route := range [][2]string{
		{http.MethodGet, "/release-trains/sources"}, {http.MethodPost, "/release-trains/config"},
		{http.MethodGet, "/release-trains/board"}, {http.MethodPost, "/release-trains/refresh"},
	} {
		if w := serveReleaseTrains(t, nil, route[0], route[1]); w.Code != http.StatusNotImplemented {
			t.Fatalf("%s %s = %d", route[0], route[1], w.Code)
		}
	}
}

type boardFixture struct {
	t     *testing.T
	svc   *fakeReleaseTrains
	store *sqlite.Store
	now   time.Time
	tags  rt.ResourceSpec
}

func newBoardFixture(t *testing.T) *boardFixture {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, rtsvc.ConfigFileName)
	if err := os.WriteFile(path, []byte(rt.ExampleConfigJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	f := &boardFixture{t: t, store: st, now: time.Date(2026, 10, 14, 3, 0, 0, 0, time.UTC), tags: rt.SpecFor(rt.KindTags, rt.ProjectScope(1001), "di")}
	f.svc = &fakeReleaseTrains{BoardService: rtsvc.NewBoardService(rtsvc.BoardDeps{
		Config: rtsvc.NewConfigLoader(path), Store: st, Clock: func() time.Time { return f.now }})}
	ctx := context.Background()
	if err := st.PlanReleaseResources(ctx, []rt.ResourceSpec{f.tags}, f.now); err != nil {
		t.Fatal(err)
	}
	if err := st.WriteReleaseSnapshot(ctx, f.tags, nil, f.now); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *boardFixture) get(ifNoneMatch string) *httptest.ResponseRecorder {
	r := chi.NewRouter()
	(&ReleaseTrainsController{Svc: f.svc}).Register(r)
	req := httptest.NewRequest(http.MethodGet, "/release-trains/board", nil)
	if ifNoneMatch != "" {
		req.Header.Set("If-None-Match", ifNoneMatch)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestReleaseTrainBoardAnswers304WhileNothingChanges(t *testing.T) {
	f := newBoardFixture(t)
	first := f.get("")
	etag := first.Header().Get("ETag")
	if first.Code != http.StatusOK || etag == "" || first.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("first = %d etag %q cache %q", first.Code, etag, first.Header().Get("Cache-Control"))
	}
	var body struct {
		Config struct{ State string } `json:"config"`
		Board  *struct {
			Apps []struct{ ID string } `json:"apps"`
		} `json:"board"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &body); err != nil || body.Config.State != "ok" || body.Board == nil || len(body.Board.Apps) != 2 {
		t.Fatalf("body = %s (%v)", first.Body, err)
	}

	again := f.get(etag)
	if again.Code != http.StatusNotModified || again.Header().Get("ETag") != etag || again.Body.Len() != 0 {
		t.Fatalf("matching If-None-Match = %d etag %q body %q", again.Code, again.Header().Get("ETag"), again.Body)
	}
	if stale := f.get(`"something-else"`); stale.Code != http.StatusOK || stale.Header().Get("ETag") != etag {
		t.Fatalf("an old ETag = %d", stale.Code)
	}
}

func TestReleaseTrainBoardETagChangesWhenASourceFails(t *testing.T) {
	f := newBoardFixture(t)
	etag := f.get("").Header().Get("ETag")
	err := f.store.MarkReleaseFailure(context.Background(), f.tags, rt.Failure{At: f.now, Failures: 1, Class: rt.ErrUnavailable,
		Message: "gitlab scm: 503", NextAttemptAt: f.now.Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	w := f.get(etag)
	if w.Code != http.StatusOK || w.Header().Get("ETag") == etag || !strings.Contains(w.Body.String(), `"errorClass":"unavailable"`) {
		t.Fatalf("after a failed fetch = %d, etag changed %v, body %s", w.Code, w.Header().Get("ETag") != etag, w.Body)
	}
}

func TestReleaseTrainRefreshAccepts(t *testing.T) {
	svc := &fakeReleaseTrains{}
	r := chi.NewRouter()
	(&ReleaseTrainsController{Svc: svc}).Register(r)
	post := func(body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/release-trains/refresh", strings.NewReader(body)))
		return w
	}
	if w := post(`{"source":"gitlab"}`); w.Code != http.StatusAccepted || !strings.Contains(w.Body.String(), `"source":"gitlab"`) {
		t.Fatalf("refresh gitlab = %d %s", w.Code, w.Body)
	}
	if w := post(``); w.Code != http.StatusAccepted {
		t.Fatalf("refresh with no body = %d %s", w.Code, w.Body)
	}
	if w := post(`{"source":"confluence"}`); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), `"code":"UNKNOWN_SOURCE"`) {
		t.Fatalf("refresh confluence = %d %s", w.Code, w.Body)
	}
	if w := post(`{"source":`); w.Code != http.StatusBadRequest {
		t.Fatalf("refresh with a broken body = %d", w.Code)
	}
	if len(svc.refreshed) != 2 || svc.refreshed[0] != "gitlab" || svc.refreshed[1] != "" {
		t.Fatalf("refreshed = %q", svc.refreshed)
	}
}
