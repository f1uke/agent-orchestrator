package httpd

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"
)

// The live simulator screen, as binary WebSocket frames.
//
// It is a socket and not a REST route for two reasons that both come back to
// the same rule. First, the renderer runs on the app:// scheme, where a
// loopback <img src="http://127.0.0.1…"> subresource is CSP-blocked; only
// connect-src reaches the daemon, so the bytes have to arrive over a connection
// the page opened. Second, and more important: the socket IS the subscription.
// A capture cannot exist without a viewer, and there is no clearer definition of
// "a viewer is here" than an open connection. When the tab is hidden the page
// closes the socket, this handler returns, the hub's last subscriber goes away
// and the capture process is gone - no timer, no heartbeat, nothing to forget.
//
// Frames go out as binary messages; anything the client needs to be told in
// words goes out as one JSON text message. Nothing is ever written to disk.

// simStreamWriteTimeout bounds one frame write. A viewer whose socket has
// stopped draining must not pin a frame - and the drop-not-queue rule in the
// hub already means the next frame is the one worth having.
const simStreamWriteTimeout = 5 * time.Second

// simStreamStatus is the one JSON message this socket sends: why the stream is
// ending, in words a person can read.
type simStreamStatus struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// mountSimStream registers the live simulator frame socket. It sits outside the
// per-request timeout middleware, like the terminal mux: the connection is
// long-lived by design.
func mountSimStream(r chi.Router, screen SimScreen, log *slog.Logger) {
	if screen == nil {
		// A machine with no simulator surface has no stream to offer, and the
		// route's absence is the honest answer: the devices route already says
		// 501 with the reason.
		return
	}
	r.Get("/sim-stream/{udid}", simStreamHandler(screen, log))
}

func simStreamHandler(screen SimScreen, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		udid := chi.URLParam(r, "udid")
		// InsecureSkipVerify disables the same-origin check for the same reason
		// the terminal mux does: the daemon binds loopback only and the desktop
		// renderer's app:// origin never matches the loopback host.
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			log.Warn("sim stream: websocket upgrade failed", "err", err, "udid", udid)
			return
		}
		defer func() { _ = conn.CloseNow() }()

		// The subscription's lifetime is this connection's lifetime, and nothing
		// else. Cancelling here is what stops the capture.
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()

		frames, err := screen.Subscribe(ctx, udid)
		if err != nil {
			writeSimStreamStatus(ctx, conn, "unavailable", err.Error())
			_ = conn.Close(websocket.StatusNormalClosure, "unavailable")
			return
		}

		// A client that goes away without closing cleanly is noticed by reading:
		// this socket expects no inbound messages, so the read only ever ends.
		go func() {
			defer cancel()
			for {
				if _, _, readErr := conn.Read(ctx); readErr != nil {
					return
				}
			}
		}()

		for event := range frames {
			if event.Err != nil {
				writeSimStreamStatus(ctx, conn, "ended", event.Err.Error())
				break
			}
			if event.Frame == nil {
				continue
			}
			writeCtx, done := context.WithTimeout(ctx, simStreamWriteTimeout)
			writeErr := conn.Write(writeCtx, websocket.MessageBinary, event.Frame.JPEG)
			done()
			if writeErr != nil {
				return
			}
		}
		_ = conn.Close(websocket.StatusNormalClosure, "stream ended")
	}
}

func writeSimStreamStatus(ctx context.Context, conn *websocket.Conn, kind, message string) {
	body, err := json.Marshal(simStreamStatus{Type: kind, Message: message})
	if err != nil {
		return
	}
	writeCtx, done := context.WithTimeout(ctx, simStreamWriteTimeout)
	defer done()
	_ = conn.Write(writeCtx, websocket.MessageText, body)
}
