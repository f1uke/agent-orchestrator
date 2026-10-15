package controllers

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	rtsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/releasetrain"
)

var pngHeader = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")

func getAvatar(t *testing.T, store *rtsvc.AvatarStore, rawID string) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	(&ReleaseTrainsController{Svc: &fakeReleaseTrains{avatars: store}}).Register(r)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/release-trains/avatars/"+rawID, nil))
	return w
}

func TestReleaseTrainAvatarServesTheCachedBytes(t *testing.T) {
	store := rtsvc.NewAvatarStore(t.TempDir())
	if err := store.Put("712020:acc-ann", pngHeader); err != nil {
		t.Fatal(err)
	}
	w := getAvatar(t, store, url.PathEscape("712020:acc-ann"))
	if w.Code != http.StatusOK || w.Body.String() != string(pngHeader) {
		t.Fatalf("avatar = %d %q", w.Code, w.Body)
	}
	if ct := w.Header().Get("Content-Type"); ct != "image/png" {
		t.Fatalf("Content-Type = %q", ct)
	}
	if cc := w.Header().Get("Cache-Control"); !strings.Contains(cc, "max-age=") || !strings.Contains(cc, "private") {
		t.Fatalf("Cache-Control = %q", cc)
	}
	if w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q", w.Header().Get("X-Content-Type-Options"))
	}
}

func TestReleaseTrainAvatarIs404UntilDownloaded(t *testing.T) {
	w := getAvatar(t, rtsvc.NewAvatarStore(t.TempDir()), "acc-bob")
	if w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), `"code":"RELEASE_AVATAR_NOT_FOUND"`) {
		t.Fatalf("absent avatar = %d %s", w.Code, w.Body)
	}
}

func TestReleaseTrainAvatarRefusesIdsThatLeaveTheCache(t *testing.T) {
	for _, raw := range []string{"..", "%2E%2E", "..%2Fsecret", "a%2Fb", ".hidden"} {
		w := getAvatar(t, rtsvc.NewAvatarStore(t.TempDir()), raw)
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), `"code":"BAD_ACCOUNT_ID"`) {
			t.Errorf("avatar %q = %d %s", raw, w.Code, w.Body)
		}
	}
}

func TestReleaseTrainAvatarRefusesAnSVGItCannotTrust(t *testing.T) {
	store := rtsvc.NewAvatarStore(t.TempDir())
	if err := store.Put("acc-eve", []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>x</script></svg>`)); err != nil {
		t.Fatal(err)
	}
	w := getAvatar(t, store, "acc-eve")
	if w.Code != http.StatusNotFound || strings.Contains(w.Header().Get("Content-Type"), "svg") {
		t.Fatalf("an SVG avatar = %d as %q", w.Code, w.Header().Get("Content-Type"))
	}
}
