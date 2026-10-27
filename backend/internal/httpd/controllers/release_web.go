package controllers

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/releaseweb"
)

// ReleaseWebService backs ReleaseWebController. The team web runs as its own
// process; this controls it. Nothing it returns carries the key.
type ReleaseWebService interface {
	Status(ctx context.Context) (releaseweb.Status, error)
	// Start creates a key when none exists, and reports the web already running.
	Start(ctx context.Context) (releaseweb.Status, error)
	Stop(ctx context.Context) (releaseweb.Status, error)
	// RotateKey signs every session out.
	RotateKey(ctx context.Context) (releaseweb.Status, error)
	// CopyKey puts the key on the clipboard of the machine the daemon runs on.
	// It returns releaseweb.ErrNoKey or releaseweb.ErrNoClipboard.
	CopyKey(ctx context.Context) error
}

// ReleaseWebController owns the /release-web routes.
type ReleaseWebController struct {
	Svc ReleaseWebService
}

// ReleaseWebStatusResponse is the body of the /release-web routes. It never
// carries the key.
type ReleaseWebStatusResponse struct {
	Running       bool       `json:"running"`
	PID           int        `json:"pid,omitempty"`
	URL           string     `json:"url,omitempty" description:"Where the web listens, on 127.0.0.1; absent while stopped."`
	Port          int        `json:"port,omitempty"`
	Sessions      int        `json:"sessions" description:"People signed in right now."`
	StartedAt     *time.Time `json:"startedAt,omitempty"`
	KeyPresent    bool       `json:"keyPresent"`
	KeyGeneration int        `json:"keyGeneration,omitempty" description:"Counts up on every key rotation."`
	Hosts         []string   `json:"hosts" description:"Public names the web answers besides 127.0.0.1 and localhost: the running web's, or while it is stopped the configured ones."`
}

// ReleaseWebCopyKeyResponse is the body of POST /api/v1/release-web/copy-key.
type ReleaseWebCopyKeyResponse struct {
	Copied bool `json:"copied"`
}

// Register mounts the release-web routes.
func (c *ReleaseWebController) Register(r chi.Router) {
	r.Get("/release-web", c.status)
	r.Post("/release-web/start", c.start)
	r.Post("/release-web/stop", c.stop)
	r.Post("/release-web/rotate-key", c.rotateKey)
	r.Post("/release-web/copy-key", c.copyKey)
}

func releaseWebStatus(st releaseweb.Status) ReleaseWebStatusResponse {
	out := ReleaseWebStatusResponse{
		Running: st.Running, PID: st.PID, URL: st.URL, Port: st.Port, Sessions: st.Sessions,
		KeyPresent: st.KeyPresent, KeyGeneration: st.KeyGeneration, Hosts: append([]string{}, st.Hosts...),
	}
	if !st.StartedAt.IsZero() {
		started := st.StartedAt
		out.StartedAt = &started
	}
	return out
}

// releaseWebCall runs one status-returning call. The service's errors are
// written to be read by the person who clicked, and never hold the key, so
// their text goes to the wire where WriteError would hide it.
func (c *ReleaseWebController) releaseWebCall(w http.ResponseWriter, r *http.Request, method, path string,
	call func(context.Context) (releaseweb.Status, error)) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, method, path)
		return
	}
	st, err := call(r.Context())
	if err != nil {
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "RELEASE_WEB_FAILED", err.Error(), nil)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, releaseWebStatus(st))
}

func (c *ReleaseWebController) status(w http.ResponseWriter, r *http.Request) {
	c.releaseWebCall(w, r, "GET", "/api/v1/release-web", func(ctx context.Context) (releaseweb.Status, error) { return c.Svc.Status(ctx) })
}

func (c *ReleaseWebController) start(w http.ResponseWriter, r *http.Request) {
	c.releaseWebCall(w, r, "POST", "/api/v1/release-web/start", func(ctx context.Context) (releaseweb.Status, error) { return c.Svc.Start(ctx) })
}

func (c *ReleaseWebController) stop(w http.ResponseWriter, r *http.Request) {
	c.releaseWebCall(w, r, "POST", "/api/v1/release-web/stop", func(ctx context.Context) (releaseweb.Status, error) { return c.Svc.Stop(ctx) })
}

func (c *ReleaseWebController) rotateKey(w http.ResponseWriter, r *http.Request) {
	c.releaseWebCall(w, r, "POST", "/api/v1/release-web/rotate-key", func(ctx context.Context) (releaseweb.Status, error) { return c.Svc.RotateKey(ctx) })
}

func (c *ReleaseWebController) copyKey(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/release-web/copy-key")
		return
	}
	err := c.Svc.CopyKey(r.Context())
	switch {
	case errors.Is(err, releaseweb.ErrNoKey):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "RELEASE_WEB_NO_KEY", err.Error(), nil)
	case errors.Is(err, releaseweb.ErrNoClipboard):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "RELEASE_WEB_NO_CLIPBOARD", err.Error(), nil)
	case err != nil:
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "RELEASE_WEB_FAILED", err.Error(), nil)
	default:
		envelope.WriteJSON(w, http.StatusOK, ReleaseWebCopyKeyResponse{Copied: true})
	}
}
