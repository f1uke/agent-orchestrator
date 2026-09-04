package claudepeer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/google/uuid"
)

const (
	// maxFrameBytes is the receiver's per-connection buffer ceiling: it drops
	// the connection once an unterminated line passes 1 MiB. Staying under it
	// keeps a message that would be refused on the pane path, where it is
	// chunked and has no such ceiling.
	maxFrameBytes = 1 << 20

	// dialTimeout and writeTimeout bound the whole attempt. A socket that does
	// not answer promptly is a socket we stop waiting on: the pane is right
	// there and always works.
	dialTimeout  = 2 * time.Second
	writeTimeout = 5 * time.Second
)

// Dialer opens a connection to a session's messaging socket.
type Dialer func(ctx context.Context, socketPath string) (net.Conn, error)

func dialUnix(ctx context.Context, socketPath string) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, "unix", socketPath)
}

// authFrame is the optional first line: it presents the token Claude Code
// published in the session's key file. The receiver only REQUIRES it on
// Windows; elsewhere it authenticates the connection by peer credentials and
// the 0700 socket directory. We send it whenever the key is readable because
// that is what a first-party sender does, and because a future release could
// start requiring it.
type authFrame struct {
	Type  string `json:"type"`
	Token string `json:"token"`
}

// userFrame is the message itself. msgV and msg_id mirror what a first-party
// sender puts on the wire; priority "next" means "deliver at the next turn
// boundary", which is the closest match to typing into the pane.
//
// Deliberately absent:
//   - from: an address the receiver could reply to. AO's daemon does not listen
//     on a socket in the receiver's namespace, so a from would be unreachable;
//     omitting it makes the receiver record the sender as "unknown", which is
//     true.
//   - session_id: the receiver drops a frame whose session_id does not match its
//     CURRENT conversation, and that id changes on /clear before the descriptor
//     catches up. Setting it would turn a harmless race into silently lost mail;
//     the tmux-pane match already pins the destination.
type userFrame struct {
	MsgV     int              `json:"msgV"`
	MsgID    string           `json:"msg_id"`
	Type     string           `json:"type"`
	Message  userFrameMessage `json:"message"`
	Priority string           `json:"priority"`
}

type userFrameMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// buildFrame renders the newline-delimited JSON the receiver reads. The message
// is the LAST line and ends in a newline, which is what makes a short write
// safe: the receiver enqueues only whole parseable lines and discards a
// trailing fragment, so an interrupted write delivers nothing at all.
func buildFrame(session Session, message string) ([]byte, error) {
	if message == "" {
		// The receiver ignores an empty-content user frame outright, so sending
		// one would look like delivery and be nothing of the kind.
		return nil, errors.New("claudepeer: empty message")
	}
	var buf []byte
	if session.PeerToken != "" {
		line, err := json.Marshal(authFrame{Type: "auth", Token: session.PeerToken})
		if err != nil {
			return nil, fmt.Errorf("claudepeer: encode auth frame: %w", err)
		}
		buf = append(buf, line...)
		buf = append(buf, '\n')
	}
	line, err := json.Marshal(userFrame{
		MsgV:     1,
		MsgID:    uuid.NewString(),
		Type:     "user",
		Message:  userFrameMessage{Role: "user", Content: message},
		Priority: "next",
	})
	if err != nil {
		return nil, fmt.Errorf("claudepeer: encode user frame: %w", err)
	}
	buf = append(buf, line...)
	buf = append(buf, '\n')
	if len(buf) > maxFrameBytes {
		return nil, fmt.Errorf("claudepeer: framed message is %d bytes, over the receiver's %d-byte line cap", len(buf), maxFrameBytes)
	}
	return buf, nil
}

// writeFrame is the commit point. It returns nil only when every byte of frame
// reached the socket; any other outcome means the receiver saw, at most, an
// incomplete line it will discard, so the caller is free to deliver through the
// pane instead without risking a double delivery.
func writeFrame(ctx context.Context, dial Dialer, socketPath string, frame []byte) error {
	dialCtx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()
	conn, err := dial(dialCtx, socketPath)
	if err != nil {
		return fmt.Errorf("claudepeer: dial %s: %w", socketPath, err)
	}
	defer func() { _ = conn.Close() }()

	deadline := time.Now().Add(writeTimeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	_ = conn.SetWriteDeadline(deadline)

	n, err := conn.Write(frame)
	if err != nil {
		return fmt.Errorf("claudepeer: wrote %d of %d bytes: %w", n, len(frame), err)
	}
	if n != len(frame) {
		return fmt.Errorf("claudepeer: short write, %d of %d bytes", n, len(frame))
	}
	// Half-close so the receiver sees end-of-input and stops waiting for more
	// lines. The bytes are already in its buffer by now, so a failure here does
	// not un-deliver the message and must not trigger a fallback.
	if halfCloser, ok := conn.(interface{ CloseWrite() error }); ok {
		_ = halfCloser.CloseWrite()
	}
	return nil
}
