package sessionhost

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/chatproto"
	"github.com/aoagents/agent-orchestrator/backend/internal/sockpath"
)

// Paths returns the event log and socket paths of one session's host.
func Paths(dataDir, sessionID string) (logPath, socketPath string) {
	logPath = filepath.Join(dataDir, "sessions", sessionID, "events.jsonl")
	socketPath = sockpath.In(sockpath.Dir(filepath.Join(dataDir, "hosts"), "ao-hosts-"), sessionID+".sock")
	return logPath, socketPath
}

// PreparePaths is Paths with both parent directories created, private to the
// user, so a host can be launched on them.
func PreparePaths(dataDir, sessionID string) (logPath, socketPath string, err error) {
	logPath, socketPath = Paths(dataDir, sessionID)
	for _, dir := range []string{filepath.Dir(logPath), filepath.Dir(socketPath)} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return "", "", fmt.Errorf("sessionhost: %w", err)
		}
	}
	return logPath, socketPath, nil
}

// ErrNotReachable means no host answered on the session's socket: the session
// is asleep, restarting, or its host has gone.
var ErrNotReachable = errors.New("session host is not reachable")

// ErrRefused means the host got the request and turned it down, for example
// answering a request that is no longer pending. The host's reason is in the
// *RefusedError that wraps it.
var ErrRefused = errors.New("session host refused the request")

// RefusedError carries the reason a host gave for turning a request down.
type RefusedError struct{ Reason string }

func (e *RefusedError) Error() string { return "sessionhost: " + e.Reason }

func (e *RefusedError) Unwrap() error { return ErrRefused }

// Client talks to one session host. Every call is its own connection, because
// the host answers one request per connection.
type Client struct {
	socketPath string
}

// NewClient returns a client for the host serving socketPath. It does not
// connect until a call is made.
func NewClient(socketPath string) Client {
	return Client{socketPath: socketPath}
}

type sendWire struct {
	Op     string            `json:"op"`
	Text   string            `json:"text"`
	Images []imageWire       `json:"images,omitempty"`
	Origin *chatproto.Origin `json:"origin,omitempty"`
}

type imageWire struct {
	MediaType string `json:"media_type"`
	Base64    string `json:"base64"`
}

// Send hands in to claude as the next user message and returns the uuid the
// host gave it; in.UUID is ignored. origin, when set, says the message did not
// come from the chat.
func (c Client) Send(ctx context.Context, in chatproto.UserInput, origin *chatproto.Origin) (string, error) {
	req := sendWire{Op: "send", Text: in.Text, Origin: origin}
	for _, img := range in.Images {
		req.Images = append(req.Images, imageWire{MediaType: img.MediaType, Base64: img.Base64})
	}
	var resp sendResponse
	if err := c.call(ctx, req, &resp); err != nil {
		return "", err
	}
	return resp.UUID, nil
}

type answerWire struct {
	Op                 string            `json:"op"`
	RequestID          string            `json:"request_id"`
	Behavior           string            `json:"behavior"`
	UpdatedInput       json.RawMessage   `json:"updated_input,omitempty"`
	UpdatedPermissions []json.RawMessage `json:"updated_permissions,omitempty"`
	Message            string            `json:"message,omitempty"`
	Interrupt          bool              `json:"interrupt,omitempty"`
}

// Answer settles the pending request requestID with d. The host refuses a
// request that is not pending, which includes one already answered.
func (c Client) Answer(ctx context.Context, requestID string, d chatproto.Decision) error {
	req := answerWire{Op: "answer", RequestID: requestID}
	switch d := d.(type) {
	case chatproto.Allow:
		req.Behavior, req.UpdatedInput, req.UpdatedPermissions = "allow", d.UpdatedInput, d.UpdatedPermissions
	case chatproto.Deny:
		req.Behavior, req.Message, req.Interrupt = "deny", d.Message, d.Interrupt
	default:
		return fmt.Errorf("sessionhost: unknown decision %T", d)
	}
	return c.call(ctx, req, &okResponse{})
}

// Interrupt stops the turn claude is running.
func (c Client) Interrupt(ctx context.Context) error {
	return c.call(ctx, map[string]string{"op": "interrupt"}, &okResponse{})
}

// SetMode changes the permission mode of the running claude.
func (c Client) SetMode(ctx context.Context, mode chatproto.PermissionMode) error {
	return c.call(ctx, map[string]string{"op": "set_mode", "mode": string(mode)}, &okResponse{})
}

// SetModel switches the running claude to model and returns once claude has
// accepted it. Claude's refusal, such as an unknown model, is a RefusedError.
func (c Client) SetModel(ctx context.Context, model string) error {
	return c.call(ctx, map[string]string{"op": "set_model", "model": model}, &okResponse{})
}

// State reports what the host knows about its session right now.
func (c Client) State(ctx context.Context) (State, error) {
	var resp stateResponse
	if err := c.call(ctx, map[string]string{"op": "state"}, &resp); err != nil {
		return State{}, err
	}
	return resp.State, nil
}

// Frame is one frame of a subscription: a log entry, or a partial-message
// delta, which the host streams but never logs.
type Frame struct {
	Entry *chatproto.Entry
	Delta json.RawMessage
}

// Subscribe passes fn every log entry after fromSeq, then every entry and
// delta as the host makes it. attached, when set, is called once the host has
// accepted the subscription, before the first frame. It returns nil when the
// host ends the stream, ctx's error when ctx ends, and fn's error when fn
// returns one.
func (c Client) Subscribe(ctx context.Context, fromSeq uint64, attached func(), fn func(Frame) error) error {
	conn, br, done, err := c.open(ctx, map[string]any{"op": "subscribe", "from_seq": fromSeq})
	if err != nil {
		return err
	}
	defer done()
	if _, err := readHead(ctx, br); err != nil {
		return err
	}
	_ = conn.SetDeadline(time.Time{})
	if attached != nil {
		attached()
	}
	for {
		line, err := br.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			var w struct {
				Entry *chatproto.Entry `json:"entry"`
				Delta json.RawMessage  `json:"delta"`
			}
			if err := json.Unmarshal(line, &w); err != nil {
				return fmt.Errorf("sessionhost: bad frame %q: %w", line, err)
			}
			if w.Entry == nil && w.Delta == nil {
				return fmt.Errorf("sessionhost: bad frame %q", line)
			}
			if err := fn(Frame(w)); err != nil {
				return err
			}
		}
		switch {
		case ctx.Err() != nil:
			return ctx.Err()
		case errors.Is(err, io.EOF):
			return nil
		case err != nil:
			return fmt.Errorf("sessionhost: read frame: %w", err)
		}
	}
}

func (c Client) call(ctx context.Context, req, out any) error {
	_, br, done, err := c.open(ctx, req)
	if err != nil {
		return err
	}
	defer done()
	line, err := readHead(ctx, br)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(line, out); err != nil {
		return fmt.Errorf("sessionhost: bad response %q: %w", line, err)
	}
	return nil
}

func (c Client) open(ctx context.Context, req any) (conn net.Conn, br *bufio.Reader, done func(), err error) {
	var d net.Dialer
	conn, err = d.DialContext(ctx, "unix", c.socketPath)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, nil, nil, fmt.Errorf("sessionhost: dial %s: %w", c.socketPath, ctxErr)
		}
		return nil, nil, nil, fmt.Errorf("sessionhost: dial %s: %w: %w", c.socketPath, ErrNotReachable, err)
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	done = func() {
		stop()
		_ = conn.Close()
	}
	deadline := time.Now().Add(requestTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = conn.SetDeadline(deadline)
	b, err := marshal(req)
	if err != nil {
		done()
		return nil, nil, nil, fmt.Errorf("sessionhost: %w", err)
	}
	if _, err := conn.Write(append(b, '\n')); err != nil {
		done()
		return nil, nil, nil, fmt.Errorf("sessionhost: write request: %w", ctxErrOr(ctx, err))
	}
	return conn, bufio.NewReader(conn), done, nil
}

func readHead(ctx context.Context, br *bufio.Reader) ([]byte, error) {
	line, err := br.ReadBytes('\n')
	if err != nil && (!errors.Is(err, io.EOF) || len(line) == 0) {
		return nil, fmt.Errorf("sessionhost: read response: %w", ctxErrOr(ctx, err))
	}
	var head errorResponse
	if err := json.Unmarshal(line, &head); err != nil {
		return nil, fmt.Errorf("sessionhost: bad response %q: %w", line, err)
	}
	if !head.OK {
		return nil, &RefusedError{Reason: head.Error}
	}
	return line, nil
}

func ctxErrOr(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	return err
}
