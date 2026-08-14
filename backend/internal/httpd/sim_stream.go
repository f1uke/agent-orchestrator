package httpd

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"log/slog"
	"math"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/simbridge"
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
//
// Each binary message carries a five-byte header before the encoded bytes:
//
//	[u8 kind][u16 width][u16 height] payload
//
// The kind is what tells a viewer whether the payload configures its decoder,
// starts a picture group, or continues one - an H.264 stream is unreadable
// without it. The size is the device's own framebuffer size, which lets the
// pane hold the right aspect ratio from the first message rather than after the
// first frame decodes.

// simStreamWriteTimeout bounds one frame write. A viewer whose socket has
// stopped draining must not pin this handler; giving up on it ends the
// subscription, which is also what stops the capture when it was the last one.
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
			writeErr := conn.Write(writeCtx, websocket.MessageBinary, simStreamMessage(*event.Frame))
			done()
			if writeErr != nil {
				return
			}
		}
		_ = conn.Close(websocket.StatusNormalClosure, "stream ended")
	}
}

// simStreamHeaderSize is the kind byte plus the framebuffer's width and height.
const simStreamHeaderSize = 5

// simStreamMessage puts one frame on the wire. The header is built into the
// same allocation as the payload so a 60 fps stream does not spend a second
// buffer per frame.
func simStreamMessage(frame simbridge.Frame) []byte {
	out := make([]byte, simStreamHeaderSize+len(frame.Data))
	out[0] = byte(frame.Kind)
	binary.BigEndian.PutUint16(out[1:3], pixels(frame.Width))
	binary.BigEndian.PutUint16(out[3:5], pixels(frame.Height))
	copy(out[simStreamHeaderSize:], frame.Data)
	return out
}

// pixels narrows a framebuffer dimension to the two bytes the header has for
// it. No simulator is anywhere near the limit; clamping is only here so a
// nonsense size cannot become a different nonsense size.
func pixels(n int) uint16 {
	if n < 0 {
		return 0
	}
	if n > math.MaxUint16 {
		return math.MaxUint16
	}
	return uint16(n)
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
