package simbridge

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
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

// BridgeSession is one running bridge process: requests in, answers out, and a
// Close that stops it. Injectable so everything above can be tested without
// Node, a simulator or a mac.
type BridgeSession interface {
	// Request sends one request line and returns the answer line. It is called
	// one at a time; the device has one finger, so there is nothing to overlap.
	Request(ctx context.Context, line []byte) ([]byte, error)
	// Diagnostics is what the process said on stdout and stderr, for a failure
	// that needs quoting back to a human.
	Diagnostics() string
	Close() error
}

// BridgeStarter launches the bridge process.
type BridgeStarter func(ctx context.Context, node string, args []string) (BridgeSession, error)

// NodeDriver runs our bridge script under the machine's own `node`, once,
// and keeps it.
//
// Keeping it is the whole difference between a touch and a request. Measured on
// the machine this was built for: loading the addon costs 80 ms and the first
// HID event a further 290 ms while the injector attaches, after which each
// event costs under half a millisecond. A process per gesture paid that 370 ms
// floor every time, and a tap through the daemon took about 950 ms end to end -
// which a person clicking a live screen feels as lag, and reported as exactly
// that.
//
// The lifetime is still owned by whoever started it: the child's stdin is its
// heartbeat, so it exits when this process does, and Close stops it sooner. It
// holds no port, is addressed by the pid we started, and lifts any finger it
// left down before it goes.
type NodeDriver struct {
	Toolchain Toolchain
	// NodePath is the resolved `node` binary.
	NodePath string
	Start    BridgeStarter

	mu      sync.Mutex
	session BridgeSession
	closed  bool
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
func NewNodeDriver(dataDir string, lookPath func(string) (string, error), start BridgeStarter) (*NodeDriver, error) {
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
	if start == nil {
		start = startBridgeProcess
	}
	return &NodeDriver{Toolchain: tc, NodePath: node, Start: start}, nil
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

	answer, session, err := d.exchange(ctx, body)
	if err != nil {
		// A transport that failed has left this process in an unknown state -
		// possibly mid-gesture - so it is dropped rather than reused. The next
		// call starts a fresh one, which starts with no finger down.
		d.discard(session)
		return bridgeResponse{}, err
	}

	var res bridgeResponse
	if unmarshalErr := json.Unmarshal(bytes.TrimSpace(answer), &res); unmarshalErr != nil {
		// A bridge that produced no parsable answer must never read as success.
		d.discard(session)
		return bridgeResponse{}, fmt.Errorf("the simulator bridge returned no usable answer: %w: %s",
			unmarshalErr, firstLines(answer, []byte(session.Diagnostics())))
	}
	if res.Error != nil {
		return bridgeResponse{}, describe(res.Error.Code, res.Error.Message)
	}
	if !res.OK {
		return bridgeResponse{}, errors.New("the simulator bridge reported failure without saying why")
	}
	return res, nil
}

// exchange runs one request against the resident bridge, starting it if this is
// the first call. Requests are serialized: the protocol is one answer per
// request on one pipe, and the device has one finger.
func (d *NodeDriver) exchange(ctx context.Context, body []byte) ([]byte, BridgeSession, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil, nil, errors.New("the simulator bridge has been shut down")
	}
	if d.session == nil {
		// Started without the caller's context on purpose: the process outlives
		// the request that needed it, exactly as the capture child does.
		session, err := d.Start(context.WithoutCancel(ctx), d.NodePath, []string{d.Toolchain.Script})
		if err != nil {
			return nil, nil, fmt.Errorf("the simulator bridge did not run: %w", err)
		}
		d.session = session
	}
	session := d.session
	answer, err := session.Request(ctx, body)
	if err != nil {
		return nil, session, fmt.Errorf("the simulator bridge stopped answering: %w: %s", err, firstLines([]byte(session.Diagnostics())))
	}
	return answer, session, nil
}

// discard stops a session that can no longer be trusted, unless it has already
// been replaced by another caller.
func (d *NodeDriver) discard(session BridgeSession) {
	if session == nil {
		return
	}
	d.mu.Lock()
	if d.session == session {
		d.session = nil
	}
	d.mu.Unlock()
	_ = session.Close()
}

// Close stops the bridge process. The daemon calls it on the way out; a
// short-lived caller can skip it, because the child exits when its stdin closes.
func (d *NodeDriver) Close() error {
	d.mu.Lock()
	session := d.session
	d.session = nil
	d.closed = true
	d.mu.Unlock()
	if session == nil {
		return nil
	}
	return session.Close()
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

// bridgeProcess is the production BridgeSession: one child process, its
// answers on descriptor 3, and its stdin held open as both the request channel
// and the heartbeat that kills it if we die.
type bridgeProcess struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	answers *bufio.Reader
	replies *os.File
	diag    *diagnosticBuffer
}

// maxAnswerBytes bounds one answer. An accessibility tree of the addon's 500-node
// cap measures well under a megabyte; 16 MiB is far above any real answer and
// stops a runaway line from becoming an allocation.
const maxAnswerBytes = 16 << 20

func (s *bridgeProcess) Request(ctx context.Context, line []byte) ([]byte, error) {
	if _, err := s.stdin.Write(append(append([]byte(nil), line...), '\n')); err != nil {
		return nil, err
	}
	type answer struct {
		line []byte
		err  error
	}
	done := make(chan answer, 1)
	go func() {
		out, err := s.answers.ReadBytes('\n')
		done <- answer{line: out, err: err}
	}()
	select {
	case <-ctx.Done():
		// The reader is parked on a pipe that only produces when the child does,
		// so the context cannot interrupt it - closing the process is what does.
		return nil, ctx.Err()
	case got := <-done:
		if got.err != nil {
			return nil, got.err
		}
		if len(got.line) > maxAnswerBytes {
			return nil, errors.New("the simulator bridge sent an impossible answer size")
		}
		return got.line, nil
	}
}

func (s *bridgeProcess) Diagnostics() string { return s.diag.String() }

func (s *bridgeProcess) Close() error {
	// Closing stdin asks the child to lift any finger it left down and exit on
	// its own, which is the only orderly way a touch comes back up.
	_ = s.stdin.Close()
	_ = s.replies.Close()
	if s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
	_ = s.cmd.Wait()
	return nil
}

func startBridgeProcess(ctx context.Context, node string, args []string) (BridgeSession, error) {
	readFrom, writeTo, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, node, args...)
	// Descriptor 3 in the child: stdout stays a diagnostic channel because the
	// addon writes to it unbidden.
	cmd.ExtraFiles = []*os.File{writeTo}
	diag := &diagnosticBuffer{}
	cmd.Stdout = diag
	cmd.Stderr = diag
	stdin, err := cmd.StdinPipe()
	if err != nil {
		_ = readFrom.Close()
		_ = writeTo.Close()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		_ = readFrom.Close()
		_ = writeTo.Close()
		return nil, err
	}
	// The parent must not keep the write end open, or reads never see EOF when
	// the child exits.
	_ = writeTo.Close()
	return &bridgeProcess{
		cmd:     cmd,
		stdin:   stdin,
		answers: bufio.NewReaderSize(readFrom, 64<<10),
		replies: readFrom,
		diag:    diag,
	}, nil
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
