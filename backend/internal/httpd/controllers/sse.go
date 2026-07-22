package controllers

import (
	"net/http"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
)

// streamSSE is the shared shape behind every server-sent-events route here: it
// subscribes, opens a text/event-stream response, and pumps frames until the
// client disconnects or the publisher closes the channel.
//
// Both feeds it serves are backed by lossy in-memory hubs, so this loop never
// needs to apply back-pressure: a subscriber that cannot keep up simply misses
// frames rather than stalling the publisher.
func streamSSE[T any](
	w http.ResponseWriter,
	r *http.Request,
	subscribe func() (<-chan T, func()),
	write func(http.ResponseWriter, http.Flusher, T) error,
) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "SSE_UNSUPPORTED", "Streaming is not supported by this server", nil)
		return
	}
	ch, unsubscribe := subscribe()
	defer unsubscribe()

	h := w.Header()
	h.Set("Content-Type", "text/event-stream; charset=utf-8")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	for {
		select {
		case <-r.Context().Done():
			return
		case frame, open := <-ch:
			if !open {
				return
			}
			if err := write(w, flusher, frame); err != nil {
				return
			}
		}
	}
}
