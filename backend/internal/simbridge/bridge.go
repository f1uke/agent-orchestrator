package simbridge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// Event is one thing the bridge does to a device, in order. Composing gestures
// out of these in Go (see gesture.go) keeps every decision testable without a
// simulator, and keeps the bridge script free of policy.
type Event struct {
	Kind string `json:"kind"`
	// Touch: Type is begin|move|end and X/Y are normalized 0..1 of the screen.
	// Normalized coordinates are what the HID layer takes directly, and what
	// `ao sim ax` hands out per element, so nothing in between has to know the
	// device's pixel size.
	Type string  `json:"type,omitempty"`
	X    float64 `json:"x,omitempty"`
	Y    float64 `json:"y,omitempty"`
	// Key: Usage is a USB HID keyboard usage id.
	Usage int `json:"usage,omitempty"`
	// Button: a name the addon knows (see gesture.go for why the list is short).
	Name string `json:"name,omitempty"`
	// Sleep.
	MS int `json:"ms,omitempty"`
}

// Driver is the whole surface AO uses to see and touch a simulator screen.
// Everything above it is mechanism-agnostic on purpose: this is the seam a
// future Apple-supported implementation replaces.
type Driver interface {
	// AX reads the accessibility tree and which app is frontmost.
	AX(ctx context.Context, udid string) (Snapshot, error)
	// Perform runs one gesture to completion. It reports whether the bridge had
	// to lift a finger the gesture left down, because that is the difference
	// between "nothing happened" and "the device was rescued".
	Perform(ctx context.Context, udid string, events []Event) (PerformResult, error)
}

// PerformResult is what a gesture did.
type PerformResult struct {
	// Lifted: the bridge had to release a touch the gesture did not release
	// itself. Never silent - a caller reports it.
	Lifted     bool   `json:"lifted"`
	LiftReason string `json:"liftReason,omitempty"`
}

// Runner executes the bridge process. Injectable so everything above can be
// tested without Node, a simulator or a mac.
type Runner func(ctx context.Context, name string, args []string, stdin []byte) (stdout []byte, stderr []byte, err error)

// NodeDriver runs our bridge script under the machine's own `node`.
type NodeDriver struct {
	Toolchain Toolchain
	// NodePath is the resolved `node` binary.
	NodePath string
	Run      Runner
}

// Error codes the bridge reports. They exist so a failure can be turned into
// advice a human can act on rather than a stack trace.
const (
	errCodeAddon = "addon_load_failed"
	errCodeAX    = "ax_unavailable"
)

// Error is a failure the bridge itself reported (as opposed to Node failing to
// start, which is an ordinary exec error).
type Error struct {
	Code    string
	Message string
	// Advice is what the caller should do about it.
	Advice string
}

func (e *Error) Error() string {
	if e.Advice == "" {
		return e.Message
	}
	return e.Message + "\n" + e.Advice
}

type bridgeRequest struct {
	AddonPath string  `json:"addonPath"`
	Op        string  `json:"op"`
	UDID      string  `json:"udid"`
	Events    []Event `json:"events,omitempty"`
}

type bridgeResponse struct {
	OK    bool            `json:"ok"`
	Tree  []rawAXNode     `json:"tree"`
	Front json.RawMessage `json:"frontmost"`
	PerformResult
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// NewNodeDriver builds a driver, installing the bridge under dataDir. lookPath
// resolves `node`; a machine without it gets an error that says so rather than
// an exec failure nobody can read.
func NewNodeDriver(dataDir string, lookPath func(string) (string, error), run Runner) (*NodeDriver, error) {
	if runtime.GOOS != "darwin" {
		return nil, &Error{
			Message: "reading and touching an iOS Simulator screen only works on macOS",
			Advice:  "This machine is not a mac, so there is no simulator to talk to.",
		}
	}
	node, err := lookPath("node")
	if err != nil {
		return nil, &Error{
			Message: "node was not found on PATH",
			Advice: "`ao sim ax` and the touch commands run a small bridge script under Node.js (20 or newer).\n" +
				"Install Node (for example `brew install node`) and retry. `ao sim shot` and `ao sim list` do not need it.",
		}
	}
	tc, err := Install(dataDir)
	if err != nil {
		return nil, err
	}
	if run == nil {
		run = execRunner
	}
	return &NodeDriver{Toolchain: tc, NodePath: node, Run: run}, nil
}

// AX reads the accessibility tree.
func (d *NodeDriver) AX(ctx context.Context, udid string) (Snapshot, error) {
	res, err := d.call(ctx, bridgeRequest{Op: "ax", UDID: udid})
	if err != nil {
		return Snapshot{}, err
	}
	var front Frontmost
	if len(res.Front) > 0 {
		_ = json.Unmarshal(res.Front, &front)
	}
	return newSnapshot(res.Tree, front), nil
}

// Perform runs a gesture.
func (d *NodeDriver) Perform(ctx context.Context, udid string, events []Event) (PerformResult, error) {
	res, err := d.call(ctx, bridgeRequest{Op: "perform", UDID: udid, Events: events})
	if err != nil {
		return PerformResult{}, err
	}
	return res.PerformResult, nil
}

func (d *NodeDriver) call(ctx context.Context, req bridgeRequest) (bridgeResponse, error) {
	req.AddonPath = d.Toolchain.Addon
	body, err := json.Marshal(req)
	if err != nil {
		return bridgeResponse{}, err
	}
	// The bridge answers in a file rather than on stdout. The native addon
	// prints to stdout itself - relaunching SpringBoard for `button home` logs
	// its pid there - and a single stray line on a shared channel would turn a
	// gesture that completed into an unreadable answer.
	reply, err := os.CreateTemp("", "ao-sim-bridge-*.json")
	if err != nil {
		return bridgeResponse{}, fmt.Errorf("prepare the simulator bridge reply: %w", err)
	}
	replyPath := reply.Name()
	_ = reply.Close()
	defer func() { _ = os.Remove(replyPath) }()

	stdout, stderr, err := d.Run(ctx, d.NodePath, []string{d.Toolchain.Script, replyPath}, body)
	if err != nil {
		return bridgeResponse{}, fmt.Errorf("the simulator bridge did not run: %w: %s", err, firstLines(stderr, stdout))
	}
	answer, readErr := os.ReadFile(replyPath)
	if readErr != nil {
		return bridgeResponse{}, fmt.Errorf("the simulator bridge left no answer: %w: %s", readErr, firstLines(stderr, stdout))
	}
	var res bridgeResponse
	if err := json.Unmarshal(bytes.TrimSpace(answer), &res); err != nil {
		// A bridge that produced no parsable answer must never read as success.
		return bridgeResponse{}, fmt.Errorf("the simulator bridge returned no usable answer: %w: %s", err, firstLines(answer, stderr, stdout))
	}
	if res.Error != nil {
		return bridgeResponse{}, describe(res.Error.Code, res.Error.Message)
	}
	if !res.OK {
		return bridgeResponse{}, errors.New("the simulator bridge reported failure without saying why")
	}
	return res, nil
}

// describe turns a bridge error code into something actionable. The addon calls
// private Apple frameworks, so "it stopped loading" is a real, expected outcome
// of upgrading Xcode - and the one failure a human most needs named.
func describe(code, message string) error {
	switch code {
	case errCodeAddon:
		return &Error{
			Code:    code,
			Message: "the simulator bridge could not load: " + message,
			Advice: "This bridge (vendored serve-sim " + Version + ") calls private Apple frameworks, so an Xcode or macOS\n" +
				"upgrade can break it. `ao sim shot` and `ao sim list` still work - they only use `xcrun simctl`.\n" +
				"Report the Xcode version with this message so the bridge can be re-vendored.",
		}
	case errCodeAX:
		return &Error{
			Code:    code,
			Message: "the simulator's accessibility service did not answer: " + message,
			Advice: "The device may still be booting, or SpringBoard may be restarting. Retry in a few seconds;\n" +
				"`ao sim shot` will show you what is actually on screen.",
		}
	default:
		return &Error{Code: code, Message: message}
	}
}

func execRunner(ctx context.Context, name string, args []string, stdin []byte) ([]byte, []byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = bytes.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

// firstLines keeps a diagnostic short enough to read and never empty enough to
// be useless.
func firstLines(streams ...[]byte) string {
	for _, s := range streams {
		trimmed := strings.TrimSpace(string(s))
		if trimmed == "" {
			continue
		}
		const limit = 400
		if len(trimmed) > limit {
			return trimmed[:limit] + "…"
		}
		return trimmed
	}
	return "(no output)"
}
