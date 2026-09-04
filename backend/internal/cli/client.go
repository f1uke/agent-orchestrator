package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/runfile"
)

// sessionResolvedHeader mirrors controllers.SessionResolvedHeader. It is
// duplicated rather than imported because the CLI is a thin HTTP client and
// does not depend on the daemon's controller package.
const sessionResolvedHeader = "X-AO-Session-Resolved"

// commandTimeout bounds a mutating daemon call. Spawns do real work (git
// worktree add, tmux launch, hook install), so it is generous compared to the
// status probe timeout.
const commandTimeout = 2 * time.Minute

// apiError is the subset of the daemon's JSON error envelope the CLI surfaces.
// RequestID is surfaced so a failed command can be correlated with daemon logs.
type apiError struct {
	Message   string `json:"message"`
	Code      string `json:"code"`
	RequestID string `json:"requestId"`
	// Details carries the envelope's structured payload (e.g. which session
	// holds a leased simulator) so a command can render a better message than
	// the generic one without a second, racy call.
	Details map[string]any `json:"details"`
}

type apiResponseError struct {
	StatusCode int
	ErrorBody  apiError
}

func (e apiResponseError) Error() string {
	if e.ErrorBody.Message == "" {
		return fmt.Sprintf("daemon returned HTTP %d", e.StatusCode)
	}
	return e.ErrorBody.String()
}

// String renders the envelope for the user: "<message> (<code>) [request <id>]",
// omitting whichever parts the daemon left empty.
func (e apiError) String() string {
	msg := e.Message
	if e.Code != "" {
		msg = fmt.Sprintf("%s (%s)", msg, e.Code)
	}
	if e.RequestID != "" {
		msg = fmt.Sprintf("%s [request %s]", msg, e.RequestID)
	}
	return msg
}

// getJSON sends GET /api/v1/<path> to the running daemon and decodes a 2xx
// response into out. A missing daemon or non-2xx API envelope is rendered the
// same way as mutating calls.
func (c *commandContext) getJSON(ctx context.Context, path string, out any) error {
	return c.doJSON(ctx, http.MethodGet, path, nil, out)
}

// postJSON sends body as JSON to POST /api/v1/<path> on the running daemon and
// decodes a 2xx response into out (out may be nil). A non-2xx response becomes
// an error built from the API error envelope. A missing run-file or a stale one
// (dead PID) yields a clear "not running" message rather than a
// connection-refused dump.
func (c *commandContext) postJSON(ctx context.Context, path string, body, out any) error {
	return c.doJSON(ctx, http.MethodPost, path, body, out)
}

// patchJSON sends body as JSON to PATCH /api/v1/<path> on the running daemon
// and decodes a 2xx response into out.
func (c *commandContext) patchJSON(ctx context.Context, path string, body, out any) error {
	return c.doJSON(ctx, http.MethodPatch, path, body, out)
}

// putJSON sends body as JSON to PUT /api/v1/<path> on the running daemon and
// decodes a 2xx response into out.
func (c *commandContext) putJSON(ctx context.Context, path string, body, out any) error {
	return c.doJSON(ctx, http.MethodPut, path, body, out)
}

// deleteJSON sends DELETE /api/v1/<path> to the running daemon and decodes a
// 2xx response into out.
func (c *commandContext) deleteJSON(ctx context.Context, path string, out any) error {
	return c.doJSON(ctx, http.MethodDelete, path, nil, out)
}

func (c *commandContext) doJSON(ctx context.Context, method, path string, body, out any) error {
	return c.doJSONPath(ctx, method, "/api/v1/"+path, body, out)
}

func (c *commandContext) postLoopbackJSON(ctx context.Context, path string, body any) error {
	return c.doJSONPath(ctx, http.MethodPost, path, body, nil)
}

func (c *commandContext) doJSONPath(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader = http.NoBody
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(payload)
	}
	req, err := c.newDaemonRequest(ctx, method, path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.sendDaemonRequest(req, out)
}

// postBytes streams a raw body to POST /api/v1/<path> with an explicit content
// type - the daemon's non-multipart upload path (see readEvidenceUpload). The
// CLI uses it rather than multipart because it has one file and no form.
func (c *commandContext) postBytes(ctx context.Context, path, contentType, filename string, body io.Reader, out any) error {
	req, err := c.newDaemonRequest(ctx, http.MethodPost, "/api/v1/"+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", contentType)
	if filename != "" {
		req.Header.Set("X-Filename", filename)
	}
	return c.sendDaemonRequest(req, out)
}

// newDaemonRequest resolves the running daemon and builds a request against it,
// failing with the "not running" guidance rather than a connection dump.
func (c *commandContext) newDaemonRequest(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	info, err := runfile.Read(cfg.RunFilePath)
	if err != nil {
		return nil, err
	}
	if info == nil {
		return nil, fmt.Errorf("AO daemon is not running — start it with `ao start`")
	}
	if !c.deps.ProcessAlive(info.PID) {
		return nil, fmt.Errorf("AO daemon is not running (stale run-file at %s) — start it with `ao start`", cfg.RunFilePath)
	}
	url := fmt.Sprintf("http://%s:%d%s", config.LoopbackHost, info.Port, path)
	return http.NewRequestWithContext(ctx, method, url, body) // #nosec G704 -- daemon host is fixed loopback; path is an internal API route.
}

func (c *commandContext) sendDaemonRequest(req *http.Request, out any) error {
	// Reuse the injected client's transport (keeps it stubbable in tests) but
	// give daemon API calls far more headroom than the 2s status-probe timeout.
	client := *c.deps.HTTPClient
	client.Timeout = commandTimeout
	resp, err := client.Do(req) // #nosec G704 -- request target is the fixed loopback daemon URL above.
	if err != nil {
		return fmt.Errorf("call daemon: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	c.noteResolvedSession(resp)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var e apiError
		_ = json.NewDecoder(resp.Body).Decode(&e)
		return apiResponseError{StatusCode: resp.StatusCode, ErrorBody: e}
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}

// noteResolvedSession tells the user when the daemon read the session id they
// typed as a Claude Code session name and answered for a different AO session.
//
// It prints once per distinct substitution: a command that makes several calls
// about one session would otherwise repeat itself. It goes to stderr so it
// never lands in the output of a command being piped somewhere, and it names
// the AO id so the next command can use it directly.
func (c *commandContext) noteResolvedSession(resp *http.Response) {
	note := resp.Header.Get(sessionResolvedHeader)
	if note == "" || c.deps.Err == nil {
		return
	}
	if c.notedSessions == nil {
		c.notedSessions = map[string]bool{}
	}
	if c.notedSessions[note] {
		return
	}
	c.notedSessions[note] = true
	_, _ = fmt.Fprintf(c.deps.Err, "note: session resolved %s\n", note)
}
