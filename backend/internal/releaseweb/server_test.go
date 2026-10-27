package releaseweb

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	rtsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/releasetrain"
)

const (
	internetPeer = "198.51.100.7:40000"
	loopbackPeer = "127.0.0.1:50000"
	boardBody    = `{"trains":[]}`
	boardETag    = `"b1"`
)

var pngBytes = append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 32)...)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

type fakeBoard struct {
	mu      sync.Mutex
	persons []string
}

func (f *fakeBoard) Board(_ context.Context, ifNoneMatch, person string) ([]byte, string, error) {
	f.mu.Lock()
	f.persons = append(f.persons, person)
	f.mu.Unlock()
	if ifNoneMatch == boardETag {
		return nil, boardETag, rtsvc.ErrNotModified
	}
	return []byte(boardBody), boardETag, nil
}

func (f *fakeBoard) Avatar(id string) ([]byte, error) {
	switch id {
	case "557058:face":
		return pngBytes, nil
	case "557058:html":
		return []byte("<html><script>alert(1)</script></html>"), nil
	case "bad id":
		return nil, rtsvc.ErrBadAccountID
	}
	return nil, rtsvc.ErrNoAvatar
}

type harness struct {
	t     *testing.T
	p     Paths
	srv   *Server
	clock *fakeClock
	board *fakeBoard
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	p := Paths{Dir: t.TempDir()}
	if err := RotateKey(p); err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, p: p, clock: &fakeClock{now: t0}, board: &fakeBoard{}}
	srv, err := NewServer(Options{
		Paths: p,
		Board: h.board,
		Assets: fstest.MapFS{
			"index.html":    {Data: []byte("<!doctype html><title>Release Train board</title>")},
			"assets/app.js": {Data: []byte("console.log('release train board')")},
		},
		Now:    h.clock.Now,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	h.srv = srv
	return h
}

type reqOpt func(*http.Request)

func from(peer string) reqOpt { return func(r *http.Request) { r.RemoteAddr = peer } }

func header(k, v string) reqOpt { return func(r *http.Request) { r.Header.Add(k, v) } }

func withCookie(c *http.Cookie) reqOpt { return func(r *http.Request) { r.AddCookie(c) } }

func (h *harness) do(method, target string, body io.Reader, opts ...reqOpt) *http.Response {
	h.t.Helper()
	r := httptest.NewRequest(method, target, body)
	r.RemoteAddr = internetPeer
	for _, o := range opts {
		o(r)
	}
	w := httptest.NewRecorder()
	h.srv.ServeHTTP(w, r)
	return w.Result()
}

func (h *harness) signin(key string, opts ...reqOpt) *http.Response {
	h.t.Helper()
	form := url.Values{"key": {key}}.Encode()
	opts = append([]reqOpt{header("Content-Type", "application/x-www-form-urlencoded")}, opts...)
	return h.do(http.MethodPost, "/auth/signin", strings.NewReader(form), opts...)
}

func (h *harness) secret() string { return secretOf(h.t, h.p) }

// session signs in from peer and returns the session cookie.
func (h *harness) session(opts ...reqOpt) *http.Cookie {
	h.t.Helper()
	res := h.signin(h.secret(), opts...)
	if res.StatusCode != http.StatusSeeOther {
		h.t.Fatalf("sign-in with the right key = %d, want 303", res.StatusCode)
	}
	return sessionCookie(h.t, res)
}

func sessionCookie(t *testing.T, res *http.Response) *http.Cookie {
	t.Helper()
	for _, c := range res.Cookies() {
		if c.Name == CookieName && c.Value != "" {
			return c
		}
	}
	t.Fatal("no session cookie set")
	return nil
}

func bodyOf(t *testing.T, res *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func (h *harness) events() []accessLine {
	h.t.Helper()
	f, err := os.Open(h.p.AccessLog())
	if err != nil {
		h.t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	var lines []accessLine
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var l accessLine
		if err := json.Unmarshal(sc.Bytes(), &l); err != nil {
			h.t.Fatalf("access log line is not JSON: %v", err)
		}
		lines = append(lines, l)
	}
	return lines
}

func (h *harness) hasEvent(ip string, ev accessEvent) bool {
	for _, l := range h.events() {
		if l.IP == ip && l.Event == ev {
			return true
		}
	}
	return false
}

var apiMethods = []string{http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete}

func TestWithoutASessionOnlyTheKeyPageAndRobotsArePublic(t *testing.T) {
	h := newHarness(t)

	res := h.do(http.MethodGet, "/", nil)
	page := bodyOf(t, res)
	if res.StatusCode != http.StatusOK || !strings.Contains(page, `type="password"`) {
		t.Fatalf("GET / = %d, want the key page", res.StatusCode)
	}
	for _, word := range []string{"release", "train", "board", "orchestrator", "jira", "<script", "http://", "https://"} {
		if strings.Contains(strings.ToLower(page), word) {
			t.Errorf("key page contains %q", word)
		}
	}

	for _, target := range []string{"/assets/app.js", "/index.html", "/some/route", "/assets/../index.html"} {
		res := h.do(http.MethodGet, target, nil)
		body := bodyOf(t, res)
		if strings.Contains(body, "release") || !strings.Contains(body, `type="password"`) {
			t.Errorf("GET %s without a session served %d %q, want the key page", target, res.StatusCode, body)
		}
	}

	res = h.do(http.MethodGet, "/robots.txt", nil)
	if got := bodyOf(t, res); got != "User-agent: *\nDisallow: /\n" {
		t.Errorf("robots.txt = %q", got)
	}

	for _, target := range []string{"/api", "/api/v1/release-trains/board", "/api/v1/release-trains/avatars/557058:face", "/api/v1/release-trains/hides/x", "/api/nope"} {
		for _, m := range apiMethods {
			res := h.do(m, target, nil)
			body := bodyOf(t, res)
			if res.StatusCode != http.StatusUnauthorized || (m != http.MethodHead && body != errorUnauthorized) {
				t.Errorf("%s %s without a session = %d %q, want 401 unauthorized", m, target, res.StatusCode, body)
			}
		}
	}
}

func TestSignInGrantsTheBoardAndAssets(t *testing.T) {
	h := newHarness(t)
	const wrong = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	res := h.signin(wrong)
	if res.StatusCode != http.StatusUnauthorized || len(res.Cookies()) != 0 {
		t.Fatalf("wrong key = %d with %d cookies, want 401 and none", res.StatusCode, len(res.Cookies()))
	}
	if !h.hasEvent("198.51.100.7", eventSigninFailed) {
		t.Error("access log has no signin_failed for the client")
	}

	res = h.signin(h.secret())
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/" {
		t.Fatalf("right key = %d to %q, want 303 to /", res.StatusCode, res.Header.Get("Location"))
	}
	c := sessionCookie(t, res)
	if !c.HttpOnly || c.SameSite != http.SameSiteStrictMode || c.Path != "/" || c.Secure || c.MaxAge != int(SessionAbsoluteTTL/time.Second) {
		t.Errorf("cookie HttpOnly=%v SameSite=%v Path=%q Secure=%v MaxAge=%d", c.HttpOnly, c.SameSite, c.Path, c.Secure, c.MaxAge)
	}
	if !h.hasEvent("198.51.100.7", eventSigninOK) {
		t.Error("access log has no signin_ok")
	}
	raw, err := os.ReadFile(h.p.AccessLog())
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(h.secret())) || bytes.Contains(raw, []byte(wrong)) || bytes.Contains(raw, []byte(c.Value)) {
		t.Error("access log contains a key or a session token")
	}

	res = h.do(http.MethodGet, "/api/v1/release-trains/board?person=alice", nil, withCookie(c))
	if body := bodyOf(t, res); res.StatusCode != http.StatusOK || body != boardBody || res.Header.Get("ETag") != boardETag {
		t.Errorf("board = %d %q etag %q", res.StatusCode, body, res.Header.Get("ETag"))
	}
	if got := h.board.persons; len(got) != 1 || got[0] != "alice" {
		t.Errorf("board source got persons %q, want [alice]", got)
	}
	res = h.do(http.MethodGet, "/api/v1/release-trains/board", nil, withCookie(c), header("If-None-Match", boardETag))
	if res.StatusCode != http.StatusNotModified || res.Header.Get("ETag") != boardETag {
		t.Errorf("board with matching If-None-Match = %d etag %q, want 304", res.StatusCode, res.Header.Get("ETag"))
	}

	avatars := []struct {
		id     string
		status int
		ctype  string
	}{
		{"557058:face", http.StatusOK, "image/png"},
		{"557058%3Aface", http.StatusOK, "image/png"},
		{"557058:html", http.StatusNotFound, "application/json; charset=utf-8"},
		{"557058:none", http.StatusNotFound, "application/json; charset=utf-8"},
		{"bad%20id", http.StatusBadRequest, "application/json; charset=utf-8"},
	}
	for _, a := range avatars {
		res := h.do(http.MethodGet, "/api/v1/release-trains/avatars/"+a.id, nil, withCookie(c))
		if res.StatusCode != a.status || res.Header.Get("Content-Type") != a.ctype {
			t.Errorf("avatar %s = %d %q, want %d %q", a.id, res.StatusCode, res.Header.Get("Content-Type"), a.status, a.ctype)
		}
	}

	pages := []struct{ target, want, ctype string }{
		{"/", "Release Train board", "text/html"},
		{"/index.html", "Release Train board", "text/html"},
		{"/cards/ABC-1", "Release Train board", "text/html"},
		{"/assets/app.js", "console.log", "text/javascript"},
	}
	for _, p := range pages {
		res := h.do(http.MethodGet, p.target, nil, withCookie(c))
		body := bodyOf(t, res)
		if res.StatusCode != http.StatusOK || !strings.Contains(body, p.want) || !strings.HasPrefix(res.Header.Get("Content-Type"), p.ctype) {
			t.Errorf("GET %s signed in = %d %q (%s)", p.target, res.StatusCode, body, res.Header.Get("Content-Type"))
		}
	}
}

func TestSecureCookieAndHSTSOnlyForHTTPSFromTheLocalProxy(t *testing.T) {
	cases := []struct {
		name   string
		opts   []reqOpt
		secure bool
	}{
		{"direct local browser", []reqOpt{from(loopbackPeer)}, false},
		{"funnel over https", []reqOpt{from(loopbackPeer), header("X-Forwarded-For", "203.0.113.5"), header("X-Forwarded-Proto", "https")}, true},
		{"internet peer claiming https", []reqOpt{from(internetPeer), header("X-Forwarded-Proto", "https")}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			res := h.signin(h.secret(), tc.opts...)
			if c := sessionCookie(t, res); c.Secure != tc.secure {
				t.Errorf("cookie Secure = %v, want %v", c.Secure, tc.secure)
			}
			if got := res.Header.Get("Strict-Transport-Security") != ""; got != tc.secure {
				t.Errorf("HSTS present = %v, want %v", got, tc.secure)
			}
		})
	}
}

func TestSignedInClientsCannotWrite(t *testing.T) {
	h := newHarness(t)
	c := h.session()
	paths := []string{"/hides/x", "/llb-plans/x", "/no-mr/x", "/fixes", "/fixes/plan", "/refresh", "/rules/x", "/board"}
	for _, p := range paths {
		for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
			res := h.do(m, "/api/v1/release-trains"+p, strings.NewReader("{}"), withCookie(c))
			if body := bodyOf(t, res); res.StatusCode != http.StatusForbidden || body != errorReadOnly {
				t.Errorf("%s %s = %d %q, want 403 read_only", m, p, res.StatusCode, body)
			}
		}
	}
	for _, target := range []string{"/", "/assets/app.js", "/anything"} {
		if res := h.do(http.MethodPost, target, nil, withCookie(c)); res.StatusCode != http.StatusForbidden {
			t.Errorf("POST %s = %d, want 403", target, res.StatusCode)
		}
	}
	for _, target := range []string{"/api/v1/release-trains/sources", "/api/v1/release-trains/fixes", "/api/v1/release-trains/avatars/a/b", "/api/v2/x"} {
		res := h.do(http.MethodGet, target, nil, withCookie(c))
		if body := bodyOf(t, res); res.StatusCode != http.StatusNotFound || body != errorNotFound {
			t.Errorf("GET %s = %d %q, want 404 not_found", target, res.StatusCode, body)
		}
	}
}

func TestSignOutEndsTheSession(t *testing.T) {
	h := newHarness(t)
	c := h.session()
	if h.srv.Sessions() != 1 {
		t.Fatalf("Sessions() = %d after sign-in, want 1", h.srv.Sessions())
	}
	res := h.do(http.MethodPost, "/auth/signout", nil, withCookie(c))
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("sign-out = %d, want 303", res.StatusCode)
	}
	cleared := false
	for _, k := range res.Cookies() {
		cleared = cleared || (k.Name == CookieName && k.MaxAge < 0)
	}
	if !cleared {
		t.Error("sign-out did not clear the cookie")
	}
	if res := h.do(http.MethodGet, "/api/v1/release-trains/board", nil, withCookie(c)); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("board after sign-out = %d, want 401", res.StatusCode)
	}
	if h.srv.Sessions() != 0 || !h.hasEvent("198.51.100.7", eventSignout) {
		t.Errorf("after sign-out: %d sessions, signout logged = %v", h.srv.Sessions(), h.hasEvent("198.51.100.7", eventSignout))
	}
}

func TestSessionExpiresAfterIdleTTL(t *testing.T) {
	h := newHarness(t)
	c := h.session()
	h.clock.Advance(SessionIdleTTL)
	if res := h.do(http.MethodGet, "/api/v1/release-trains/board", nil, withCookie(c)); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("board after %v idle = %d, want 401", SessionIdleTTL, res.StatusCode)
	}
}

func retryAfterSeconds(t *testing.T, res *http.Response) int {
	t.Helper()
	n, err := strconv.Atoi(res.Header.Get("Retry-After"))
	if err != nil {
		t.Fatalf("Retry-After %q is not seconds", res.Header.Get("Retry-After"))
	}
	return n
}

func TestClientLockoutRefusesEvenTheRightKey(t *testing.T) {
	h := newHarness(t)
	for range SigninFailuresPerClient {
		if res := h.signin("wrong"); res.StatusCode != http.StatusUnauthorized {
			t.Fatalf("failure before the limit = %d, want 401", res.StatusCode)
		}
	}
	res := h.signin(h.secret())
	if res.StatusCode != http.StatusTooManyRequests || len(res.Cookies()) != 0 {
		t.Fatalf("right key while locked = %d, want 429 and no cookie", res.StatusCode)
	}
	if got := retryAfterSeconds(t, res); got != int(SigninClientLockout/time.Second) {
		t.Errorf("Retry-After = %d, want %d", got, int(SigninClientLockout/time.Second))
	}
	if !strings.Contains(bodyOf(t, res), "Too many attempts, try again later") {
		t.Error("429 page lacks the generic message")
	}
	if !h.hasEvent("198.51.100.7", eventSigninLocked) {
		t.Error("access log has no signin_locked")
	}
	if res := h.signin(h.secret(), from("192.0.2.44:1")); res.StatusCode != http.StatusSeeOther {
		t.Errorf("another client during the lockout = %d, want 303", res.StatusCode)
	}

	h.clock.Advance(SigninClientLockout - time.Second)
	if res := h.signin(h.secret()); res.StatusCode != http.StatusTooManyRequests {
		t.Errorf("one second before unlock = %d, want 429", res.StatusCode)
	}
	h.clock.Advance(time.Second)
	if res := h.signin(h.secret()); res.StatusCode != http.StatusSeeOther {
		t.Errorf("after the lockout = %d, want 303", res.StatusCode)
	}
}

func TestGlobalLockoutStopsNewSignInsButNotSessions(t *testing.T) {
	h := newHarness(t)
	c := h.session(from("192.0.2.1:1"))
	for i := range SigninGlobalFailures {
		peer := "203.0.113." + strconv.Itoa(10+i/(SigninFailuresPerClient-1)) + ":1"
		if res := h.signin("wrong", from(peer)); res.StatusCode != http.StatusUnauthorized {
			t.Fatalf("failure %d = %d, want 401", i+1, res.StatusCode)
		}
	}
	res := h.signin(h.secret(), from("192.0.2.200:1"))
	if res.StatusCode != http.StatusTooManyRequests || retryAfterSeconds(t, res) != int(SigninGlobalLockout/time.Second) {
		t.Fatalf("fresh client under the global lock = %d Retry-After %q, want 429", res.StatusCode, res.Header.Get("Retry-After"))
	}
	if res := h.do(http.MethodGet, "/api/v1/release-trains/board", nil, withCookie(c)); res.StatusCode != http.StatusOK {
		t.Errorf("existing session under the global lock = %d, want 200", res.StatusCode)
	}
	h.clock.Advance(SigninGlobalLockout)
	if res := h.signin(h.secret(), from("192.0.2.200:1")); res.StatusCode != http.StatusSeeOther {
		t.Errorf("after the global lock = %d, want 303", res.StatusCode)
	}
}

func TestForwardedForCountsOnlyFromTheLocalProxy(t *testing.T) {
	t.Run("internet peer cannot spread guesses over forged addresses", func(t *testing.T) {
		h := newHarness(t)
		for i := range SigninFailuresPerClient {
			h.signin("wrong", header("X-Forwarded-For", "203.0.113."+strconv.Itoa(i)))
		}
		res := h.signin(h.secret(), header("X-Forwarded-For", "203.0.113.99"))
		if res.StatusCode != http.StatusTooManyRequests {
			t.Errorf("right key after forged-XFF failures = %d, want 429", res.StatusCode)
		}
		if h.hasEvent("203.0.113.99", eventSigninLocked) || !h.hasEvent("198.51.100.7", eventSigninLocked) {
			t.Error("access log trusted XFF from an internet peer")
		}
	})
	t.Run("local proxy clients have separate buckets", func(t *testing.T) {
		h := newHarness(t)
		for range SigninFailuresPerClient {
			h.signin("wrong", from(loopbackPeer), header("X-Forwarded-For", "10.9.9.9, 203.0.113.1"))
		}
		if res := h.signin(h.secret(), from(loopbackPeer), header("X-Forwarded-For", "203.0.113.1")); res.StatusCode != http.StatusTooManyRequests {
			t.Errorf("locked forwarded client = %d, want 429", res.StatusCode)
		}
		if res := h.signin(h.secret(), from(loopbackPeer), header("X-Forwarded-For", "203.0.113.2")); res.StatusCode != http.StatusSeeOther {
			t.Errorf("other forwarded client = %d, want 303", res.StatusCode)
		}
		if res := h.signin(h.secret(), from(loopbackPeer)); res.StatusCode != http.StatusSeeOther {
			t.Errorf("direct local browser = %d, want 303", res.StatusCode)
		}
		if !h.hasEvent("203.0.113.1", eventSigninFailed) {
			t.Error("access log lacks the forwarded client address")
		}
	})
}

func TestRotationSignsEveryoneOut(t *testing.T) {
	h := newHarness(t)
	c := h.session()
	old := h.secret()
	if err := RotateKey(h.p); err != nil {
		t.Fatal(err)
	}
	if n := h.srv.Sessions(); n != 0 {
		t.Errorf("Sessions() = %d right after rotation, want 0", n)
	}
	if res := h.do(http.MethodGet, "/api/v1/release-trains/board", nil, withCookie(c)); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("old cookie after rotation = %d, want 401", res.StatusCode)
	}
	if !h.hasEvent("198.51.100.7", eventSessionRejectedRotate) {
		t.Error("access log has no session_rejected_rotated")
	}
	if res := h.signin(old); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("old key after rotation = %d, want 401", res.StatusCode)
	}
	if res := h.signin(h.secret()); res.StatusCode != http.StatusSeeOther {
		t.Errorf("new key after rotation = %d, want 303", res.StatusCode)
	}
}

func TestHandEditedKeyWithTheSameGenerationSignsEveryoneOut(t *testing.T) {
	h := newHarness(t)
	c := h.session()
	writeKeyFile(t, h.p, base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)))
	if k, err := ReadKey(h.p); err != nil || k.Generation != 1 {
		t.Fatalf("hand-edited key: generation %d, err %v", k.Generation, err)
	}
	if res := h.do(http.MethodGet, "/api/v1/release-trains/board", nil, withCookie(c)); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("old cookie after the key changed = %d, want 401", res.StatusCode)
	}
}

func TestDeletingTheKeyFileStopsEverything(t *testing.T) {
	h := newHarness(t)
	c := h.session()
	secret := h.secret()
	if err := os.Remove(h.p.KeyFile()); err != nil {
		t.Fatal(err)
	}
	if res := h.do(http.MethodGet, "/api/v1/release-trains/board", nil, withCookie(c)); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("session after the key file went = %d, want 401", res.StatusCode)
	}
	if res := h.signin(secret); res.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("sign-in without a key file = %d, want 503", res.StatusCode)
	}
}

func TestSignInRefusesCrossSitePostsAndOversizedBodies(t *testing.T) {
	cases := []struct {
		name string
		opts []reqOpt
		want int
	}{
		{"cross-site fetch metadata", []reqOpt{header("Sec-Fetch-Site", "cross-site")}, http.StatusForbidden},
		{"same-site fetch metadata", []reqOpt{header("Sec-Fetch-Site", "same-site")}, http.StatusForbidden},
		{"foreign origin", []reqOpt{header("Origin", "https://evil.example")}, http.StatusForbidden},
		{"null origin", []reqOpt{header("Origin", "null")}, http.StatusForbidden},
		{"forwarded host from an internet peer", []reqOpt{header("Origin", "https://box.tail.ts.net"), header("X-Forwarded-Host", "box.tail.ts.net")}, http.StatusForbidden},
		{"same origin", []reqOpt{header("Origin", "http://example.com"), header("Sec-Fetch-Site", "same-origin")}, http.StatusSeeOther},
		{"browser form under no-referrer", []reqOpt{header("Origin", "null"), header("Sec-Fetch-Site", "same-origin")}, http.StatusSeeOther},
		{"null origin from another site", []reqOpt{header("Origin", "null"), header("Sec-Fetch-Site", "cross-site")}, http.StatusForbidden},
		{"funnel forwarded host", []reqOpt{from(loopbackPeer), header("Origin", "https://box.tail.ts.net"), header("X-Forwarded-Host", "box.tail.ts.net")}, http.StatusSeeOther},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			if res := h.signin(h.secret(), tc.opts...); res.StatusCode != tc.want {
				t.Errorf("sign-in = %d, want %d", res.StatusCode, tc.want)
			}
		})
	}

	h := newHarness(t)
	big := "key=" + strings.Repeat("A", maxSigninBody)
	for range SigninFailuresPerClient + 1 {
		res := h.do(http.MethodPost, "/auth/signin", strings.NewReader(big), header("Content-Type", "application/x-www-form-urlencoded"))
		if res.StatusCode != http.StatusBadRequest {
			t.Fatalf("oversized body = %d, want 400", res.StatusCode)
		}
	}
	if res := h.signin(h.secret()); res.StatusCode != http.StatusSeeOther {
		t.Errorf("right key after oversized bodies = %d, want 303 (they must not count)", res.StatusCode)
	}
	if res := h.do(http.MethodPost, "/auth/signin?key="+url.QueryEscape(h.secret()), nil, header("Content-Type", "application/x-www-form-urlencoded")); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("key in the query string = %d, want 401", res.StatusCode)
	}
}

func TestSecurityHeadersOnEveryResponse(t *testing.T) {
	h := newHarness(t)
	c := h.session(from("192.0.2.1:1"))
	for range SigninFailuresPerClient {
		h.signin("wrong", from("192.0.2.66:1"))
	}
	https := []reqOpt{from(loopbackPeer), header("X-Forwarded-For", "192.0.2.9"), header("X-Forwarded-Proto", "https")}
	cases := []struct {
		name   string
		res    func() *http.Response
		status int
		https  bool
	}{
		{"key page", func() *http.Response { return h.do(http.MethodGet, "/", nil) }, 200, false},
		{"board", func() *http.Response {
			return h.do(http.MethodGet, "/api/v1/release-trains/board", nil, withCookie(c))
		}, 200, false},
		{"asset", func() *http.Response { return h.do(http.MethodGet, "/assets/app.js", nil, withCookie(c)) }, 200, false},
		{"robots", func() *http.Response { return h.do(http.MethodGet, "/robots.txt", nil) }, 200, false},
		{"401", func() *http.Response { return h.do(http.MethodGet, "/api/v1/release-trains/board", nil) }, 401, false},
		{"403", func() *http.Response { return h.do(http.MethodDelete, "/", nil) }, 403, false},
		{"404", func() *http.Response { return h.do(http.MethodGet, "/api/x", nil, withCookie(c)) }, 404, false},
		{"429", func() *http.Response { return h.signin("wrong", from("192.0.2.66:1")) }, 429, false},
		{"key page over https", func() *http.Response { return h.do(http.MethodGet, "/", nil, https...) }, 200, true},
		{"401 over https", func() *http.Response {
			return h.do(http.MethodGet, "/api/v1/release-trains/board", nil, https...)
		}, 401, true},
	}
	want := map[string]string{
		"Content-Security-Policy":      "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; font-src 'self' data:; frame-ancestors 'none'; base-uri 'none'; form-action 'self'; object-src 'none'",
		"X-Frame-Options":              "DENY",
		"Referrer-Policy":              "no-referrer",
		"X-Content-Type-Options":       "nosniff",
		"X-Robots-Tag":                 "noindex, nofollow, noarchive",
		"Cross-Origin-Opener-Policy":   "same-origin",
		"Cross-Origin-Resource-Policy": "same-origin",
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := tc.res()
			if res.StatusCode != tc.status {
				t.Fatalf("status %d, want %d", res.StatusCode, tc.status)
			}
			for k, v := range want {
				if got := res.Header.Get(k); got != v {
					t.Errorf("%s = %q, want %q", k, got, v)
				}
			}
			if res.Header.Get("Permissions-Policy") == "" {
				t.Error("no Permissions-Policy")
			}
			if got := res.Header.Get("Strict-Transport-Security"); (got == "max-age=31536000") != tc.https || (got != "" && !tc.https) {
				t.Errorf("Strict-Transport-Security = %q, https %v", got, tc.https)
			}
		})
	}
	for _, target := range []string{"/", "/api/v1/release-trains/board", "/api/x"} {
		if got := h.do(http.MethodGet, target, nil).Header.Get("Cache-Control"); got != "no-store" {
			t.Errorf("GET %s without a session: Cache-Control %q, want no-store", target, got)
		}
	}
	if got := h.signin("wrong", from("192.0.2.77:1")).Header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("sign-in Cache-Control %q, want no-store", got)
	}
}
