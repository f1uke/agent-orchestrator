package releaseweb

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	rtsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/releasetrain"
)

const (
	// CookieName names the session cookie.
	CookieName = "rtw_session"
	// maxSigninBody caps the sign-in form; a real one is under 100 bytes.
	maxSigninBody = 4 << 10
	// avatarCacheSeconds matches the daemon's avatar route.
	avatarCacheSeconds = 24 * 60 * 60
	// maxPersonBytes bounds the board's person query, as the daemon's board route does.
	maxPersonBytes = 256
)

// BoardSource is what the release web reads the board from.
type BoardSource interface {
	// Board returns the serialized board and its ETag, or rtsvc.ErrNotModified
	// with the ETag when ifNoneMatch already names it.
	Board(ctx context.Context, ifNoneMatch, person string) ([]byte, string, error)
	// Avatar returns rtsvc.ErrNoAvatar or rtsvc.ErrBadAccountID when it has no image.
	Avatar(accountID string) ([]byte, error)
}

// Options configures a Server and Serve.
type Options struct {
	Paths  Paths
	Board  BoardSource
	Assets fs.FS
	// Addr is where Serve listens: DefaultAddr when empty, and always on
	// 127.0.0.1. Port 0 picks a free one.
	Addr string
	// Now is the clock; time.Now when nil.
	Now func() time.Time
	// Logger receives operational errors, never secrets; stderr when nil.
	Logger *slog.Logger
}

// Server is the release web's HTTP handler.
type Server struct {
	board    BoardSource
	assets   fs.FS
	now      func() time.Time
	log      *slog.Logger
	keys     *keyWatcher
	sessions *sessionStore
	limiter  *signinLimiter
	access   *accessLog
	// changed receives a value whenever the session count may have moved.
	changed chan struct{}
}

// NewServer loads the key and opens the access log. It refuses to build a
// server without a valid key.
func NewServer(o Options) (*Server, error) {
	if o.Board == nil || o.Assets == nil {
		return nil, errors.New("release web: Board and Assets are required")
	}
	if err := o.Paths.Ensure(); err != nil {
		return nil, err
	}
	s := &Server{
		board:    o.Board,
		assets:   o.Assets,
		now:      o.Now,
		log:      o.Logger,
		sessions: newSessionStore(),
		limiter:  newSigninLimiter(),
		changed:  make(chan struct{}, 1),
	}
	if s.now == nil {
		s.now = time.Now
	}
	if s.log == nil {
		s.log = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	keys, err := newKeyWatcher(o.Paths, s.log, func() {
		s.sessions.rotate()
		s.notifyChanged()
	})
	if err != nil {
		return nil, err
	}
	s.keys = keys
	if s.access, err = openAccessLog(o.Paths.AccessLog()); err != nil {
		return nil, err
	}
	return s, nil
}

// Sessions counts the live sessions, after following a key rotation that no
// request has noticed yet.
func (s *Server) Sessions() int {
	s.keys.current()
	return s.sessions.count(s.now())
}

// Close closes the access log.
func (s *Server) Close() error { return s.access.close() }

func (s *Server) notifyChanged() {
	select {
	case s.changed <- struct{}{}:
	default:
	}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	c := clientOf(r)
	setSecurityHeaders(w.Header(), c.https)
	p := path.Clean("/" + r.URL.Path)
	switch {
	case p == "/robots.txt" && isRead(r):
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("User-agent: *\nDisallow: /\n"))
	case p == "/auth" || strings.HasPrefix(p, "/auth/"):
		w.Header().Set("Cache-Control", "no-store")
		s.auth(w, r, c, p)
	case p == "/api" || strings.HasPrefix(p, "/api/"):
		w.Header().Set("Cache-Control", "no-store")
		s.api(w, r, c, p)
	case !isRead(r):
		http.Error(w, "Forbidden", http.StatusForbidden)
	case s.signedIn(w, r, c):
		s.asset(w, r, p)
	case p == "/":
		s.keyPage(w, http.StatusOK, "")
	default:
		s.keyPage(w, http.StatusUnauthorized, "")
	}
}

func isRead(r *http.Request) bool { return r.Method == http.MethodGet || r.Method == http.MethodHead }

// setSecurityHeaders runs before every response, errors included.
func setSecurityHeaders(h http.Header, https bool) {
	h.Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; "+
		"img-src 'self' data:; connect-src 'self'; font-src 'self' data:; frame-ancestors 'none'; "+
		"base-uri 'none'; form-action 'self'; object-src 'none'")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("X-Robots-Tag", "noindex, nofollow, noarchive")
	h.Set("Cross-Origin-Opener-Policy", "same-origin")
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
	h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=()")
	if https {
		h.Set("Strict-Transport-Security", "max-age=31536000")
	}
}

// signedIn reports whether the request carries a live session cookie, and
// clears a cookie that no longer names one.
func (s *Server) signedIn(w http.ResponseWriter, r *http.Request, c client) bool {
	cookie, err := r.Cookie(CookieName)
	if err != nil {
		return false
	}
	key, ok := s.keys.current()
	result := sessionMissing
	if ok {
		result = s.sessions.lookup(cookie.Value, key.id, s.now())
	}
	if result == sessionLive {
		return true
	}
	if result == sessionRotated {
		s.access.record(s.now(), c.ip, eventSessionRejectedRotate)
	}
	clearCookie(w, c)
	return false
}

func (s *Server) auth(w http.ResponseWriter, r *http.Request, c client, p string) {
	if isRead(r) {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	if r.Method != http.MethodPost || (p != "/auth/signin" && p != "/auth/signout") {
		http.NotFound(w, r)
		return
	}
	if !sameOrigin(r, c) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if p == "/auth/signout" {
		s.signout(w, r, c)
		return
	}
	s.signin(w, r, c)
}

// sameOrigin refuses a cross-site form post. A browser's Sec-Fetch-Site
// settles it: under Referrer-Policy no-referrer the same browser sends
// "Origin: null" on its own form post. Without it, Funnel may rewrite Host to
// the loopback address, so a loopback peer's X-Forwarded-Host also counts.
func sameOrigin(r *http.Request, c client) bool {
	switch r.Header.Get("Sec-Fetch-Site") {
	case "same-origin", "none":
		return true
	case "":
	default:
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	return u.Host == r.Host || (c.viaLoopback && u.Host == r.Header.Get("X-Forwarded-Host"))
}

const (
	msgWrongKey    = "That key did not work."
	msgLocked      = "Too many attempts, try again later."
	msgBadRequest  = "Something went wrong, try again."
	msgUnavailable = "Sign-in is unavailable right now."
)

func (s *Server) signin(w http.ResponseWriter, r *http.Request, c client) {
	r.Body = http.MaxBytesReader(w, r.Body, maxSigninBody)
	if err := r.ParseForm(); err != nil {
		s.keyPage(w, http.StatusBadRequest, msgBadRequest)
		return
	}
	submitted := strings.TrimSpace(r.PostForm.Get("key"))
	key, haveKey := s.keys.current()
	if !haveKey {
		s.keyPage(w, http.StatusServiceUnavailable, msgUnavailable)
		return
	}
	now := s.now()
	ok, retryAfter := s.limiter.attempt(c.bucket, now, func() bool { return key.Matches(submitted) })
	switch {
	case retryAfter > 0:
		s.access.record(now, c.ip, eventSigninLocked)
		w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(retryAfter.Seconds()))))
		s.keyPage(w, http.StatusTooManyRequests, msgLocked)
		return
	case !ok:
		s.access.record(now, c.ip, eventSigninFailed)
		s.keyPage(w, http.StatusUnauthorized, msgWrongKey)
		return
	}
	token, err := s.sessions.create(key.id, now)
	if err != nil {
		s.log.Error("release web: create session", "err", err)
		s.keyPage(w, http.StatusInternalServerError, msgBadRequest)
		return
	}
	s.notifyChanged()
	setSessionCookie(w, c, token, int(SessionAbsoluteTTL/time.Second))
	s.access.record(now, c.ip, eventSigninOK)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) signout(w http.ResponseWriter, r *http.Request, c client) {
	if cookie, err := r.Cookie(CookieName); err == nil {
		s.sessions.remove(cookie.Value)
		s.notifyChanged()
	}
	clearCookie(w, c)
	s.access.record(s.now(), c.ip, eventSignout)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func clearCookie(w http.ResponseWriter, c client) { setSessionCookie(w, c, "", -1) }

func setSessionCookie(w http.ResponseWriter, c client, value string, maxAge int) {
	//nolint:gosec // G124: Secure follows the request, so a direct local browser on plain http can still sign in
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   c.https,
		SameSite: http.SameSiteStrictMode,
	})
}

const (
	boardPath         = "/api/v1/release-trains/board"
	avatarPrefix      = "/api/v1/release-trains/avatars/"
	errorUnauthorized = `{"error":"unauthorized"}`
	errorReadOnly     = `{"error":"read_only"}`
	errorNotFound     = `{"error":"not_found"}`
	errorBadRequest   = `{"error":"bad_request"}`
	errorInternal     = `{"error":"internal"}`
)

func writeJSON(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

func (s *Server) api(w http.ResponseWriter, r *http.Request, c client, p string) {
	switch {
	case !s.signedIn(w, r, c):
		writeJSON(w, http.StatusUnauthorized, errorUnauthorized)
	case !isRead(r):
		writeJSON(w, http.StatusForbidden, errorReadOnly)
	case p == boardPath:
		s.serveBoard(w, r)
	case strings.HasPrefix(p, avatarPrefix) && !strings.Contains(p[len(avatarPrefix):], "/"):
		s.serveAvatar(w, p[len(avatarPrefix):])
	default:
		writeJSON(w, http.StatusNotFound, errorNotFound)
	}
}

func (s *Server) serveBoard(w http.ResponseWriter, r *http.Request) {
	person := r.URL.Query().Get("person")
	if len(person) > maxPersonBytes {
		writeJSON(w, http.StatusBadRequest, errorBadRequest)
		return
	}
	body, etag, err := s.board.Board(r.Context(), r.Header.Get("If-None-Match"), person)
	switch {
	case errors.Is(err, rtsvc.ErrNotModified):
		w.Header().Set("ETag", etag)
		w.Header().Set("Cache-Control", "private, no-cache")
		w.WriteHeader(http.StatusNotModified)
	case err != nil:
		s.log.Error("release web: board", "err", err)
		writeJSON(w, http.StatusInternalServerError, errorInternal)
	default:
		// private, no-cache instead of no-store: the browser must keep the
		// body for If-None-Match to save the next download.
		w.Header().Set("ETag", etag)
		w.Header().Set("Cache-Control", "private, no-cache")
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body) //nolint:gosec // G705: the board service's own json.Marshal output, served as application/json
	}
}

func (s *Server) serveAvatar(w http.ResponseWriter, accountID string) {
	b, err := s.board.Avatar(accountID)
	if errors.Is(err, rtsvc.ErrBadAccountID) {
		writeJSON(w, http.StatusBadRequest, errorBadRequest)
		return
	}
	if err != nil && !errors.Is(err, rtsvc.ErrNoAvatar) {
		s.log.Error("release web: avatar", "err", err)
		writeJSON(w, http.StatusInternalServerError, errorInternal)
		return
	}
	ct := http.DetectContentType(b)
	if err != nil || !strings.HasPrefix(ct, "image/") {
		writeJSON(w, http.StatusNotFound, errorNotFound)
		return
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", fmt.Sprintf("private, max-age=%d", avatarCacheSeconds))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(b) //nolint:gosec // G705: sniffed as a raster image above and served with nosniff
}

// asset serves the web build. Any path that is not a file gets index.html, so
// a hash-router app loads from every URL.
func (s *Server) asset(w http.ResponseWriter, r *http.Request, p string) {
	name := strings.TrimPrefix(p, "/")
	if st, err := fs.Stat(s.assets, name); name == "" || err != nil || !st.Mode().IsRegular() {
		name = "index.html"
	}
	w.Header().Set("Cache-Control", "private, no-cache")
	if name != "index.html" {
		http.ServeFileFS(w, r, s.assets, name)
		return
	}
	// Written directly: ServeFileFS redirects /index.html to ./.
	b, err := fs.ReadFile(s.assets, name)
	if err != nil {
		http.Error(w, "Not Found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(b) //nolint:gosec // G705: the web build's own index.html
}

var keyPageTemplate = template.Must(template.New("key").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex, nofollow, noarchive">
<title>Sign in</title>
<style>
body{margin:0;min-height:100vh;display:flex;align-items:center;justify-content:center;background:#fff;color:#1b2333;font:16px/1.5 system-ui,-apple-system,"Segoe UI",sans-serif}
form{width:min(320px,calc(100vw - 32px));display:flex;flex-direction:column;gap:12px;padding:24px;border-radius:12px;background:#f4f5f7}
h1{margin:0 0 4px;font-size:20px;font-weight:600}
label{font-size:14px}
input{font:inherit;padding:8px 12px;border:1px solid #c9ced6;border-radius:8px;background:#fff}
button{font:inherit;padding:8px 12px;border:0;border-radius:8px;background:#1b2333;color:#fff;cursor:pointer}
p{margin:0;font-size:14px;color:#b42318}
</style>
</head>
<body>
<form method="post" action="/auth/signin">
<h1>Sign in</h1>
{{if .}}<p role="alert">{{.}}</p>{{end}}
<label for="key">Access key</label>
<input id="key" name="key" type="password" autocomplete="current-password" required autofocus>
<button type="submit">Sign in</button>
</form>
</body>
</html>
`))

func (s *Server) keyPage(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := keyPageTemplate.Execute(w, message); err != nil {
		s.log.Error("release web: render key page", "err", err)
	}
}
