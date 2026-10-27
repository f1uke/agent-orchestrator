package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/releaseweb"
)

type fakeReleaseWeb struct {
	status  releaseweb.Status
	calls   []string
	err     error
	copyErr error
}

func (f *fakeReleaseWeb) do(name string) (releaseweb.Status, error) {
	f.calls = append(f.calls, name)
	return f.status, f.err
}

func (f *fakeReleaseWeb) Status(context.Context) (releaseweb.Status, error) { return f.do("status") }
func (f *fakeReleaseWeb) Start(context.Context) (releaseweb.Status, error)  { return f.do("start") }
func (f *fakeReleaseWeb) Stop(context.Context) (releaseweb.Status, error)   { return f.do("stop") }
func (f *fakeReleaseWeb) RotateKey(context.Context) (releaseweb.Status, error) {
	return f.do("rotate-key")
}
func (f *fakeReleaseWeb) CopyKey(context.Context) error {
	f.calls = append(f.calls, "copy-key")
	return f.copyErr
}

func serveReleaseWeb(t *testing.T, svc ReleaseWebService, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	(&ReleaseWebController{Svc: svc}).Register(r)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(method, path, nil))
	return w
}

func runningStatus() releaseweb.Status {
	return releaseweb.Status{
		Running: true, PID: 4242, URL: "http://127.0.0.1:7420/", Port: 7420, Sessions: 3,
		StartedAt: time.Date(2026, 10, 27, 8, 0, 0, 0, time.UTC), KeyPresent: true, KeyGeneration: 2,
	}
}

func TestReleaseWebStatusReportsTheWebAndHasNoKeyField(t *testing.T) {
	svc := &fakeReleaseWeb{status: runningStatus()}
	w := serveReleaseWeb(t, svc, http.MethodGet, "/release-web")
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	var got ReleaseWebStatusResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.Running || got.URL != "http://127.0.0.1:7420/" || got.Sessions != 3 || got.KeyGeneration != 2 || !got.KeyPresent || got.StartedAt == nil {
		t.Fatalf("response = %+v", got)
	}
	var raw map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"key", "secret", "token", "value"} {
		if _, ok := raw[name]; ok {
			t.Errorf("the status carries a %q field", name)
		}
	}
	if len(svc.calls) != 1 || svc.calls[0] != "status" {
		t.Fatalf("calls = %v", svc.calls)
	}
}

func TestReleaseWebStoppedStatusOmitsTheServerFields(t *testing.T) {
	svc := &fakeReleaseWeb{status: releaseweb.Status{KeyPresent: true, KeyGeneration: 1}}
	w := serveReleaseWeb(t, svc, http.MethodGet, "/release-web")
	var raw map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"pid", "url", "port", "startedAt"} {
		if _, ok := raw[name]; ok {
			t.Errorf("a stopped web reports %q", name)
		}
	}
	if raw["running"] != false || raw["sessions"] != float64(0) {
		t.Fatalf("response = %v", raw)
	}
}

func TestReleaseWebEachPostCallsTheServiceOnce(t *testing.T) {
	for path, want := range map[string]string{
		"/release-web/start":      "start",
		"/release-web/stop":       "stop",
		"/release-web/rotate-key": "rotate-key",
	} {
		svc := &fakeReleaseWeb{status: runningStatus()}
		w := serveReleaseWeb(t, svc, http.MethodPost, path)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: status %d: %s", path, w.Code, w.Body)
		}
		var got ReleaseWebStatusResponse
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || got.KeyGeneration != 2 {
			t.Fatalf("%s: body = %s (%v)", path, w.Body, err)
		}
		if len(svc.calls) != 1 || svc.calls[0] != want {
			t.Fatalf("%s: calls = %v, want [%s]", path, svc.calls, want)
		}
	}
}

func TestReleaseWebCopyKeyAnswersCopiedWithoutTheKey(t *testing.T) {
	svc := &fakeReleaseWeb{}
	w := serveReleaseWeb(t, svc, http.MethodPost, "/release-web/copy-key")
	if w.Code != http.StatusOK || w.Body.String() != "{\"copied\":true}\n" {
		t.Fatalf("status %d body %q", w.Code, w.Body)
	}
	if len(svc.calls) != 1 || svc.calls[0] != "copy-key" {
		t.Fatalf("calls = %v", svc.calls)
	}
}

func TestReleaseWebCopyKeyMapsItsRefusals(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code int
		want string
	}{
		{releaseweb.ErrNoKey, http.StatusConflict, "RELEASE_WEB_NO_KEY"},
		{releaseweb.ErrNoClipboard, http.StatusConflict, "RELEASE_WEB_NO_CLIPBOARD"},
		{errors.New("pbcopy exploded"), http.StatusInternalServerError, "RELEASE_WEB_FAILED"},
	} {
		w := serveReleaseWeb(t, &fakeReleaseWeb{copyErr: tc.err}, http.MethodPost, "/release-web/copy-key")
		var env struct {
			Code string `json:"code"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil || w.Code != tc.code || env.Code != tc.want {
			t.Errorf("%v: status %d body %s, want %d %s", tc.err, w.Code, w.Body, tc.code, tc.want)
		}
	}
}

func TestReleaseWebFailuresReachThePersonWhoClicked(t *testing.T) {
	svc := &fakeReleaseWeb{err: errors.New("release web: exited before listening, see /x/web.log")}
	w := serveReleaseWeb(t, svc, http.MethodPost, "/release-web/start")
	var env struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusInternalServerError || env.Code != "RELEASE_WEB_FAILED" || env.Message != svc.err.Error() {
		t.Fatalf("status %d body %s", w.Code, w.Body)
	}
}

func TestReleaseWebWithoutAServiceIsNotImplemented(t *testing.T) {
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/release-web"},
		{http.MethodPost, "/release-web/start"},
		{http.MethodPost, "/release-web/stop"},
		{http.MethodPost, "/release-web/rotate-key"},
		{http.MethodPost, "/release-web/copy-key"},
	} {
		if w := serveReleaseWeb(t, nil, route.method, route.path); w.Code != http.StatusNotImplemented {
			t.Errorf("%s %s = %d, want 501", route.method, route.path, w.Code)
		}
	}
}
