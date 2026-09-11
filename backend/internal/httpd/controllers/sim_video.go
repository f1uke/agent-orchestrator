package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/simvideo"
)

// The screen-recording routes, behind `ao sim record`.
//
// They hang off the session for the same reason the gesture-recording routes
// do: a recording belongs to the session that started it, and only that session
// may stop it. They are a SEPARATE controller from SimController because they
// are a separate concern - SimController arbitrates a device between sessions
// and never touches a process, while this one owns a process that outlives
// every request it serves.

// StartSimVideoInput is the body of POST .../sim-videos/{udid}.
type StartSimVideoInput struct {
	// MaxDurationSeconds bounds a recording nobody stops. Omit for the
	// default; the service refuses anything outside its own bounds rather than
	// silently clamping, because a caller who asked for an hour and got ten
	// minutes would find that out from the file.
	MaxDurationSeconds int `json:"maxDurationSeconds,omitempty" description:"Stop the recording automatically after this many seconds. Omit for the 10 minute default; at least 5 seconds and at most 30 minutes."`
}

// SimVideoResponse carries one screen recording, open or finished.
type SimVideoResponse struct {
	Video SimVideoView `json:"video"`
}

// SimVideoView is simvideo.Recording on the wire.
type SimVideoView struct {
	UDID      string           `json:"udid" description:"The simulator being recorded."`
	SessionID domain.SessionID `json:"sessionId" description:"The session that started it; only that session may stop it."`
	Path      string           `json:"path" description:"Absolute path of the video file, under the session's own artifact directory."`
	StartedAt string           `json:"startedAt" description:"RFC3339 timestamp of when the first frame was recorded."`
	// MaxDurationSeconds is echoed back so a caller never has to remember
	// whether its own omitted value became the default.
	MaxDurationSeconds int `json:"maxDurationSeconds" description:"When this recording stops itself, in seconds from startedAt."`
	// StoppedAt and StopReason are absent while it is still recording.
	StoppedAt  string `json:"stoppedAt,omitempty" description:"RFC3339 timestamp of when it stopped. Absent while it is still recording."`
	StopReason string `json:"stopReason,omitempty" description:"What ended it: requested, max-duration, session-ended or daemon-stopped."`
	// Bytes is zero until it has stopped: simctl finalizes the file on exit, so
	// the size on disk says nothing about an open recording.
	Bytes int64 `json:"bytes" description:"Size of the finished file. Zero while it is still recording."`
}

func simVideoView(rec simvideo.Recording) SimVideoView {
	view := SimVideoView{
		UDID:               rec.UDID,
		SessionID:          rec.SessionID,
		Path:               rec.Path,
		StartedAt:          rec.StartedAt.UTC().Format(time.RFC3339),
		MaxDurationSeconds: int(rec.MaxDuration.Seconds()),
		StopReason:         string(rec.StopReason),
		Bytes:              rec.Bytes,
	}
	if rec.StoppedAt != nil {
		view.StoppedAt = rec.StoppedAt.UTC().Format(time.RFC3339)
	}
	return view
}

// SimVideoService is the recorder surface this controller needs. It is an
// interface, and exported, so the daemon can wire a recorder in and a test can
// wire a fake in without spawning anything; *simvideo.Recorder satisfies it.
type SimVideoService interface {
	Start(ctx context.Context, sessionID domain.SessionID, udid string, maxDuration time.Duration) (simvideo.Recording, error)
	Status(udid string) (simvideo.Recording, error)
	Stop(ctx context.Context, sessionID domain.SessionID, udid string) (simvideo.Recording, error)
}

// SimVideoController owns the screen-recording routes. A nil Svc returns 501,
// mirroring the other optional-service controllers - a machine with no Xcode
// has no recorder, and saying "not implemented here" is more honest than
// failing every call with a spawn error.
type SimVideoController struct {
	Svc SimVideoService
}

// Register mounts the screen-recording routes.
func (c *SimVideoController) Register(r chi.Router) {
	r.Post("/sessions/{sessionId}/sim-videos/{udid}", c.start)
	r.Get("/sessions/{sessionId}/sim-videos/{udid}", c.status)
	r.Delete("/sessions/{sessionId}/sim-videos/{udid}", c.stop)
}

func (c *SimVideoController) start(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/sim-videos/{udid}")
		return
	}
	var in StartSimVideoInput
	// An empty body is a start with every default, which is the common call.
	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_BODY", "Invalid request body", nil)
			return
		}
	}
	rec, err := c.Svc.Start(r.Context(), sessionID(r), chi.URLParam(r, "udid"),
		time.Duration(in.MaxDurationSeconds)*time.Second)
	if err != nil {
		writeSimVideoError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, SimVideoResponse{Video: simVideoView(rec)})
}

func (c *SimVideoController) status(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/sessions/{sessionId}/sim-videos/{udid}")
		return
	}
	rec, err := c.Svc.Status(chi.URLParam(r, "udid"))
	if err != nil {
		writeSimVideoError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, SimVideoResponse{Video: simVideoView(rec)})
}

func (c *SimVideoController) stop(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "DELETE", "/api/v1/sessions/{sessionId}/sim-videos/{udid}")
		return
	}
	rec, err := c.Svc.Stop(r.Context(), sessionID(r), chi.URLParam(r, "udid"))
	if err != nil {
		writeSimVideoError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, SimVideoResponse{Video: simVideoView(rec)})
}

// writeSimVideoError maps the recorder's sentinels. Contention gets its own 409
// carrying the holder, the same shape the lease and gesture-recording refusals
// use, so a caller can tell "somebody else is recording this device" apart from
// a transient failure and never read the refusal as permission to proceed.
func writeSimVideoError(w http.ResponseWriter, r *http.Request, err error) {
	var held *simvideo.HeldError
	switch {
	case errors.As(err, &held):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "SIM_VIDEO_HELD", err.Error(), map[string]any{
			"udid":      held.Recording.UDID,
			"holder":    string(held.Recording.SessionID),
			"path":      held.Recording.Path,
			"startedAt": held.Recording.StartedAt.UTC().Format(time.RFC3339),
		})
	case errors.Is(err, simvideo.ErrNotFound):
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "SIM_VIDEO_NOT_FOUND", err.Error(), nil)
	case errors.Is(err, simvideo.ErrInvalid):
		envelope.WriteAPIError(w, r, http.StatusUnprocessableEntity, "unprocessable", "SIM_VIDEO_INVALID", err.Error(), nil)
	case errors.Is(err, simvideo.ErrUnavailable):
		// 501, not 500: the machine cannot do this at all, and a caller that
		// reads it as a transient failure would retry forever.
		envelope.WriteAPIError(w, r, http.StatusNotImplemented, "not_implemented", "SIM_VIDEO_UNAVAILABLE", err.Error(), nil)
	default:
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "SIM_VIDEO_FAILED",
			"Simulator screen recording failed: "+err.Error(), nil)
	}
}
