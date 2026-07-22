package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
)

// ActivityStream is the live activity feed used by SSE clients.
type ActivityStream interface {
	Subscribe(sessionID domain.SessionID) (<-chan domain.ActivityEvent, func())
}

// ActivityFeed publishes curated activity events to live subscribers. It is
// deliberately separate from ActivityRecorder: recording is a durable lifecycle
// reduction, publishing is ephemeral fan-out that must never fail a request.
type ActivityFeed interface {
	Publish(ctx context.Context, ev domain.ActivityEvent) error
}

// ActivityController owns the activity feed stream route.
type ActivityController struct {
	Stream ActivityStream
}

// RegisterStream mounts the long-lived activity stream route. It stays outside
// the REST timeout group, like the notification stream.
func (c *ActivityController) RegisterStream(r chi.Router) {
	r.Get("/activity/stream", c.stream)
}

// stream is the overlay's subscription. An absent sessionId subscribes to every
// session, which is what a desktop companion watching all sessions wants.
func (c *ActivityController) stream(w http.ResponseWriter, r *http.Request) {
	if c.Stream == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/activity/stream")
		return
	}
	streamSSE(w, r,
		func() (<-chan domain.ActivityEvent, func()) {
			return c.Stream.Subscribe(domain.SessionID(r.URL.Query().Get("sessionId")))
		},
		writeActivitySSE,
	)
}

func writeActivitySSE(w http.ResponseWriter, flusher http.Flusher, ev domain.ActivityEvent) error {
	data, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "event: activity\ndata: %s\n\n", data); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}

// activityEventFromSignal builds the feed frame for an accepted activity signal.
//
// The whole truth contract lives in this function. Every frame carries the
// coarse level the signal implies AND how long that level stays true; a frame
// that also carries curated detail carries the (much shorter) window in which
// that detail may be shown as currently happening. A consumer therefore cannot
// render a finished action as live: the detail expires and it falls back to the
// coarse level, then to "unknown".
//
// A detail whose kind AO does not recognise, or that arrives over-long or
// secret-shaped, is clamped or dropped here — the per-tool whitelist in the hook
// process is the primary guard, this is the backstop.
func activityEventFromSignal(id domain.SessionID, state domain.ActivityState, detail *domain.ActivityDetail) domain.ActivityEvent {
	coarse, coarseTTL := domain.CoarseFromActivityState(state)
	ev := domain.ActivityEvent{
		SessionID:   id,
		Kind:        domain.ActivityEventActivity,
		At:          time.Now().UTC(),
		Coarse:      coarse,
		CoarseTTLMs: domain.DurationMs(coarseTTL),
	}
	if detail == nil {
		return ev
	}
	safe, ok := detail.SanitizedForFeed()
	if !ok {
		return ev
	}
	ev.Kind = safe.Kind
	ev.Tool = safe.Tool
	ev.Target = safe.Target
	ev.Text = safe.Text
	ev.TTLMs = domain.DurationMs(domain.DetailTTL(safe.Kind))
	return ev
}

// activityEventFromMessage builds the feed frame for an accepted ao send.
//
// It carries NO coarse level on purpose: a message tells you something flew by,
// not how busy the agent is — and delivery is gated on a typing gap (inputgate
// defers up to 8s), so "sent" is true immediately while "the agent has it" is
// not. Only the truncated, redacted first line is emitted; briefs are long and
// can carry paths and credentials.
func activityEventFromMessage(id domain.SessionID, message string) domain.ActivityEvent {
	return domain.ActivityEvent{
		SessionID: id,
		Kind:      domain.ActivityEventMessage,
		At:        time.Now().UTC(),
		Text:      domain.ActivityLine(message),
		TTLMs:     domain.DurationMs(domain.MessageDetailTTL),
	}
}
