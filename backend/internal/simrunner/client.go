package simrunner

import (
	"bytes"
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
const WireVersion = "2"

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
	status, body, err := c.exchange(ctx, method, path, nil)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("runner answered %d: %s", status, strings.TrimSpace(string(body)))
	}
	if into == nil {
		return nil
	}
	return json.Unmarshal(body, into)
}

// exchange sends one request, with a JSON body when payload is not nil, and
// returns the status and body whatever the status is.
func (c client) exchange(ctx context.Context, method, path string, payload any) (int, []byte, error) {
	var reqBody io.Reader = http.NoBody
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return 0, nil, err
		}
		reqBody = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.url(path), reqBody)
	if err != nil {
		return 0, nil, err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return 0, nil, err
	}
	return resp.StatusCode, body, nil
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

// TypeAnswer is the runner's answer to POST /type (and GET /focus, which looks
// without typing): what it typed into, or why it typed nothing. It never says whether the text ARRIVED - that is decided
// by reading the screen (internal/simtype), the same way for every route.
type TypeAnswer struct {
	Version string `json:"version"`
	// Typed: XCTest typed the text without recording a failure.
	Typed bool `json:"typed"`
	// App is the bundle id of the application holding the focused element.
	App   string     `json:"app,omitempty"`
	Field *TypeField `json:"field,omitempty"`
	// Keyboard: the software keyboard was on screen before typing.
	Keyboard bool `json:"keyboard"`
	// Checked is every application looked in for keyboard focus.
	Checked []string `json:"checked,omitempty"`
	// KeyboardSwitchedTo is the layout the globe key switched to before
	// typing, when a layout was asked for and the keyboard was another;
	// KeyboardRestored says it was switched back afterwards.
	KeyboardSwitchedTo string     `json:"keyboardSwitchedTo,omitempty"`
	KeyboardRestored   bool       `json:"keyboardRestored,omitempty"`
	TypingMs           int        `json:"typingMs,omitempty"`
	ElapsedMs          int        `json:"elapsedMs"`
	Error              *TypeError `json:"error,omitempty"`
}

// TypeField is the element that had keyboard focus.
type TypeField struct {
	Type        string `json:"type"`
	ID          string `json:"id,omitempty"`
	Label       string `json:"label,omitempty"`
	Placeholder string `json:"placeholder,omitempty"`
}

// TypeError is why the runner typed nothing, or what XCTest said while typing.
type TypeError struct {
	// Code is "no_focus" (nothing was typed: no element has keyboard focus),
	// "type_failed" (XCTest recorded a failure while typing; some of the text
	// may have arrived), "no_layout" (nothing was typed) or
	// "bad_request".
	Code    string `json:"code"`
	Message string `json:"message"`
}

// The runner's TypeError codes.
const (
	TypeNoFocus    = "no_focus"
	TypeFailed     = "type_failed"
	TypeBadRequest = "bad_request"
	// TypeNoLayout: a keyboard layout was asked for and the keyboard could not
	// be switched to it. Nothing was typed.
	TypeNoLayout = "no_layout"
)

func (c client) typeText(ctx context.Context, text string, opts TypeOptions) (TypeAnswer, error) {
	body := map[string]any{"text": text}
	if opts.Layout != "" {
		body["layout"] = string(opts.Layout)
	}
	return c.typeAnswer(ctx, http.MethodPost, "/type", body)
}

// focus asks what a type would go into, without typing.
func (c client) focus(ctx context.Context) (TypeAnswer, error) {
	return c.typeAnswer(ctx, http.MethodGet, "/focus", nil)
}

// typeAnswer reads /type and /focus, which answer in the same shape whatever
// the status: the status says what happened, the body says why.
func (c client) typeAnswer(ctx context.Context, method, path string, payload any) (TypeAnswer, error) {
	status, body, err := c.exchange(ctx, method, path, payload)
	if err != nil {
		return TypeAnswer{}, err
	}
	var answer TypeAnswer
	if jsonErr := json.Unmarshal(body, &answer); jsonErr != nil || answer.Version == "" {
		return TypeAnswer{}, fmt.Errorf("runner answered %d: %s", status, strings.TrimSpace(string(body)))
	}
	if answer.Version != WireVersion {
		return TypeAnswer{}, fmt.Errorf("the runner answered wire version %q, not %q", answer.Version, WireVersion)
	}
	return answer, nil
}
