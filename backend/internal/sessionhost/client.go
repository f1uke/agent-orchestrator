package sessionhost

import (
	"bufio"
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
	Origin *chatproto.Origin `json:"origin,omitempty"`
}

// Send hands text to claude as the next user message and returns the uuid the
// host gave it. origin, when set, says the message did not come from the chat.
func (c Client) Send(ctx context.Context, text string, origin *chatproto.Origin) (string, error) {
	var resp sendResponse
	if err := c.call(ctx, sendWire{Op: "send", Text: text, Origin: origin}, &resp); err != nil {
		return "", err
	}
	return resp.UUID, nil
}

// State reports what the host knows about its session right now.
func (c Client) State(ctx context.Context) (State, error) {
	var resp stateResponse
	if err := c.call(ctx, map[string]string{"op": "state"}, &resp); err != nil {
		return State{}, err
	}
	return resp.State, nil
}

func (c Client) call(ctx context.Context, req, out any) error {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", c.socketPath)
	if err != nil {
		return fmt.Errorf("sessionhost: dial %s: %w", c.socketPath, err)
	}
	defer func() { _ = conn.Close() }()
	defer context.AfterFunc(ctx, func() { _ = conn.Close() })()
	deadline := time.Now().Add(requestTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = conn.SetDeadline(deadline)

	b, err := marshal(req)
	if err != nil {
		return fmt.Errorf("sessionhost: %w", err)
	}
	if _, err := conn.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("sessionhost: write request: %w", ctxErrOr(ctx, err))
	}
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil && (!errors.Is(err, io.EOF) || len(line) == 0) {
		return fmt.Errorf("sessionhost: read response: %w", ctxErrOr(ctx, err))
	}
	var head errorResponse
	if err := json.Unmarshal(line, &head); err != nil {
		return fmt.Errorf("sessionhost: bad response %q: %w", line, err)
	}
	if !head.OK {
		return fmt.Errorf("sessionhost: %s", head.Error)
	}
	if err := json.Unmarshal(line, out); err != nil {
		return fmt.Errorf("sessionhost: bad response %q: %w", line, err)
	}
	return nil
}

func ctxErrOr(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	return err
}
