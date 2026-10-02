package simrunner

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/simbridge"
)

// WireVersion is the runner protocol this AO speaks. A runner answering any
// other version is a different build left on the port, and is not trusted.
const WireVersion = "1"

// runnerStatus is GET /status.
type runnerStatus struct {
	Version string `json:"version"`
	UDID    string `json:"udid"`
	PID     int    `json:"pid"`
}

// client talks to one runner on the loopback interface.
type client struct {
	port int
	http *http.Client
}

func (c client) url(path string) string {
	return "http://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(c.port)) + path
}

func (c client) get(ctx context.Context, path string, into any) error {
	return c.do(ctx, http.MethodGet, path, into)
}

func (c client) do(ctx context.Context, method, path string, into any) error {
	req, err := http.NewRequestWithContext(ctx, method, c.url(path), http.NoBody)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("runner answered %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if into == nil {
		return nil
	}
	return json.Unmarshal(body, into)
}

// status asks the runner who it is, and refuses an answer from anybody else:
// the port is only ours while our runner holds it.
func (c client) status(ctx context.Context, udid string) error {
	var st runnerStatus
	if err := c.get(ctx, "/status", &st); err != nil {
		return err
	}
	if st.Version != WireVersion {
		return fmt.Errorf("the runner on port %d speaks version %q, not %q", c.port, st.Version, WireVersion)
	}
	if domain.NormalizeSimUDID(st.UDID) != domain.NormalizeSimUDID(udid) {
		return fmt.Errorf("the runner on port %d belongs to %s, not %s", c.port, st.UDID, udid)
	}
	return nil
}

func (c client) hierarchy(ctx context.Context) (simbridge.XCTestHierarchy, error) {
	var h simbridge.XCTestHierarchy
	if err := c.get(ctx, "/hierarchy", &h); err != nil {
		return simbridge.XCTestHierarchy{}, err
	}
	if h.Version != WireVersion {
		return simbridge.XCTestHierarchy{}, fmt.Errorf("the runner answered wire version %q, not %q", h.Version, WireVersion)
	}
	return h, nil
}

func (c client) stop(ctx context.Context) error {
	return c.do(ctx, http.MethodPost, "/stop", nil)
}
