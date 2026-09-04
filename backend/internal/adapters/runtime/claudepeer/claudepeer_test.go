package claudepeer

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// ---- doubles -------------------------------------------------------------

// fakeDelegate records what the tmux path was asked to type.
type fakeDelegate struct {
	mu   sync.Mutex
	sent []string
	err  error
}

func (f *fakeDelegate) SendMessage(_ context.Context, handle ports.RuntimeHandle, message string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, handle.ID+"\x00"+message)
	return f.err
}

func (f *fakeDelegate) messages() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.sent))
	for _, s := range f.sent {
		out = append(out, s[strings.Index(s, "\x00")+1:])
	}
	return out
}

func (f *fakeDelegate) Create(context.Context, ports.RuntimeConfig) (ports.RuntimeHandle, error) {
	return ports.RuntimeHandle{}, nil
}
func (f *fakeDelegate) Destroy(context.Context, ports.RuntimeHandle) error         { return nil }
func (f *fakeDelegate) IsAlive(context.Context, ports.RuntimeHandle) (bool, error) { return true, nil }
func (f *fakeDelegate) AgentAlive(context.Context, ports.RuntimeHandle) (bool, error) {
	return true, nil
}
func (f *fakeDelegate) Attach(context.Context, ports.RuntimeHandle, uint16, uint16) (ports.Stream, error) {
	return nil, errors.New("not used")
}
func (f *fakeDelegate) GetOutput(context.Context, ports.RuntimeHandle, int) (string, error) {
	return "", nil
}

var _ Delegate = (*fakeDelegate)(nil)

// fakeRegistry answers with one canned session, or a canned rejection.
type fakeRegistry struct {
	session Session
	err     error
}

func (f fakeRegistry) Lookup(context.Context, string) (Session, error) {
	if f.err != nil {
		return Session{}, f.err
	}
	return f.session, nil
}

// inbox is a stand-in for a claude-code session's messaging socket. It records
// only COMPLETE newline-terminated JSON lines, exactly as the real receiver
// does - a trailing fragment is discarded there and must be discarded here, or
// the test cannot tell a delivery from a half-written one.
type inbox struct {
	path string

	mu    sync.Mutex
	lines [][]byte
	done  chan struct{}
}

func newInbox(t *testing.T) *inbox {
	t.Helper()
	// Unix socket paths are capped near 104 bytes, so keep the name short
	// rather than nesting under the (long) default t.TempDir() path.
	dir, err := os.MkdirTemp("", "ao-cp")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	box := &inbox{path: filepath.Join(dir, "s.sock"), done: make(chan struct{})}
	listener, err := net.Listen("unix", box.path)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	go func() {
		defer close(box.done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go box.read(conn)
		}
	}()
	return box
}

func (b *inbox) read(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 0, 64*1024), maxFrameBytes)
	for scanner.Scan() {
		line := append([]byte(nil), scanner.Bytes()...)
		b.mu.Lock()
		b.lines = append(b.lines, line)
		b.mu.Unlock()
	}
}

// userMessages returns the content of every complete `type:"user"` line seen,
// after waiting for at least want of them so the reader goroutine is not
// raced. want may be 0, in which case it only settles briefly - long enough to
// catch a message that should not be there.
func (b *inbox) userMessages(t *testing.T, want int) []string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for want > 0 && time.Now().Before(deadline) {
		b.mu.Lock()
		n := len(b.lines)
		b.mu.Unlock()
		if n >= want {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)

	b.mu.Lock()
	defer b.mu.Unlock()
	var out []string
	for _, line := range b.lines {
		var frame struct {
			Type    string `json:"type"`
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		}
		if err := json.Unmarshal(line, &frame); err != nil {
			t.Fatalf("inbox received a line that is not JSON: %q", line)
		}
		if frame.Type == "user" {
			out = append(out, frame.Message.Content)
		}
	}
	return out
}

func newTestRuntime(t *testing.T, delegate Delegate, registry Registry) *Runtime {
	t.Helper()
	t.Setenv(disableEnv, "")
	return New(delegate, Options{Registry: registry})
}

// ---- delivery happens exactly once, on exactly one path ------------------

func TestSocketDeliveryBypassesThePane(t *testing.T) {
	box := newInbox(t)
	delegate := &fakeDelegate{}
	rt := newTestRuntime(t, delegate, fakeRegistry{session: Session{PID: 1, SessionID: "s1", SocketPath: box.path}})

	if err := rt.SendMessage(context.Background(), ports.RuntimeHandle{ID: "ao-1"}, "hello over the socket"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if got := box.userMessages(t, 1); len(got) != 1 || got[0] != "hello over the socket" {
		t.Fatalf("socket received %q, want one copy of the message", got)
	}
	if got := delegate.messages(); len(got) != 0 {
		t.Fatalf("tmux path also typed %q; the message was delivered twice", got)
	}
}

func TestFallbackDeliversExactlyOnceThroughThePane(t *testing.T) {
	tests := []struct {
		name     string
		registry Registry
		// socketPath, when set, replaces the registry session's path.
		deadSocket bool
	}{
		{name: "not a claude-code session", registry: fakeRegistry{err: reject("no-descriptor")}},
		{name: "unsupported peer protocol", registry: fakeRegistry{err: reject("unsupported-peer-protocol")}},
		{name: "bypass permissions session", registry: fakeRegistry{err: reject("bypass-permissions-session")}},
		{name: "socket refuses the connection", deadSocket: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			box := newInbox(t)
			registry := tc.registry
			if tc.deadSocket {
				registry = fakeRegistry{session: Session{PID: 1, SessionID: "s1", SocketPath: box.path + ".gone"}}
			}
			delegate := &fakeDelegate{}
			rt := newTestRuntime(t, delegate, registry)

			if err := rt.SendMessage(context.Background(), ports.RuntimeHandle{ID: "ao-1"}, "fall back to the pane"); err != nil {
				t.Fatalf("SendMessage: %v", err)
			}
			if got := delegate.messages(); len(got) != 1 || got[0] != "fall back to the pane" {
				t.Fatalf("tmux path got %q, want exactly one copy", got)
			}
			if got := box.userMessages(t, 0); len(got) != 0 {
				t.Fatalf("socket also received %q; the message was delivered twice", got)
			}
		})
	}
}

// shortWriteConn accepts only the first half of a frame and then fails, which
// is the one case where "did we already deliver it?" is not obvious. The
// receiver never sees a terminating newline, so nothing was delivered and the
// pane must carry the message.
type shortWriteConn struct {
	net.Conn
	sink *strings.Builder
}

func (c *shortWriteConn) Write(p []byte) (int, error) {
	n := len(p) / 2
	c.sink.Write(p[:n])
	return n, errors.New("connection reset by peer")
}
func (c *shortWriteConn) Close() error                     { return nil }
func (c *shortWriteConn) SetWriteDeadline(time.Time) error { return nil }

func TestShortWriteFallsBackAndDeliversNothingOnTheSocket(t *testing.T) {
	var wire strings.Builder
	delegate := &fakeDelegate{}
	rt := New(delegate, Options{
		Registry: fakeRegistry{session: Session{PID: 1, SessionID: "s1", SocketPath: "/unused.sock"}},
		Dial: func(context.Context, string) (net.Conn, error) {
			return &shortWriteConn{sink: &wire}, nil
		},
	})
	t.Setenv(disableEnv, "")

	if err := rt.SendMessage(context.Background(), ports.RuntimeHandle{ID: "ao-1"}, "half a message is no message"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if got := delegate.messages(); len(got) != 1 {
		t.Fatalf("tmux path got %q, want exactly one copy", got)
	}
	if strings.Contains(wire.String(), "\n") {
		t.Fatalf("a partial write left a complete line on the wire: %q", wire.String())
	}
}

// ---- the receiver's own guards are mirrored, not tripped -----------------

func TestDuplicateWithinTheDedupeWindowGoesThroughThePane(t *testing.T) {
	box := newInbox(t)
	delegate := &fakeDelegate{}
	clock := time.Now()
	rt := New(delegate, Options{
		Registry: fakeRegistry{session: Session{PID: 1, SessionID: "s1", SocketPath: box.path}},
		Now:      func() time.Time { return clock },
	})
	t.Setenv(disableEnv, "")

	handle := ports.RuntimeHandle{ID: "ao-1"}
	for range 2 {
		if err := rt.SendMessage(context.Background(), handle, "continue"); err != nil {
			t.Fatalf("SendMessage: %v", err)
		}
	}
	if got := box.userMessages(t, 1); len(got) != 1 {
		t.Fatalf("socket received %d copies, want 1 (the receiver would have dropped the second)", len(got))
	}
	if got := delegate.messages(); len(got) != 1 || got[0] != "continue" {
		t.Fatalf("tmux path got %q, want the duplicate exactly once", got)
	}

	// Past the mirrored window the socket is fine again.
	clock = clock.Add(mirrorDedupeWindow + time.Second)
	if err := rt.SendMessage(context.Background(), handle, "continue"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if got := box.userMessages(t, 2); len(got) != 2 {
		t.Fatalf("socket received %d copies after the window, want 2", len(got))
	}
}

func TestGuardIsNotSpentByASendThatFellBack(t *testing.T) {
	clock := time.Now()
	g := newGuard(func() time.Time { return clock })

	release, ok := g.admit("s1", "same body")
	if !ok {
		t.Fatal("first admit refused")
	}
	release(false) // the socket failed; the receiver never saw this message

	release, ok = g.admit("s1", "same body")
	if !ok {
		t.Fatal("a body the receiver never saw was treated as a duplicate")
	}
	release(true)

	if _, ok := g.admit("s1", "same body"); ok {
		t.Fatal("a body the receiver did see was not treated as a duplicate")
	}
}

func TestGuardRateLimitFallsBackBeforeTheReceiverWouldDrop(t *testing.T) {
	clock := time.Now()
	g := newGuard(func() time.Time { return clock })

	admitted := 0
	for i := range 40 {
		// Distinct bodies, so only the bucket can refuse.
		if release, ok := g.admit("s1", string(rune('a'+i%26))+strings.Repeat("x", i)); ok {
			admitted++
			release(true)
		}
	}
	if admitted != int(mirrorBucketSize) {
		t.Fatalf("admitted %d in a burst, want the mirrored bucket size %v", admitted, mirrorBucketSize)
	}
	clock = clock.Add(10 * time.Second) // 10s * 0.4/s = 4 tokens
	admitted = 0
	for i := range 10 {
		if release, ok := g.admit("s1", "refilled "+strings.Repeat("y", i)); ok {
			admitted++
			release(true)
		}
	}
	if admitted != 4 {
		t.Fatalf("admitted %d after a 10s refill, want 4", admitted)
	}
}

// ---- framing -------------------------------------------------------------

func TestBuildFrameShape(t *testing.T) {
	frame, err := buildFrame(Session{PeerToken: "0123456789abcdef0123456789abcdef"}, "hi")
	if err != nil {
		t.Fatalf("buildFrame: %v", err)
	}
	lines := strings.Split(strings.TrimSuffix(string(frame), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("frame has %d lines, want auth + message: %q", len(lines), frame)
	}
	if frame[len(frame)-1] != '\n' {
		t.Fatal("frame does not end in a newline; the receiver would never see a complete line")
	}
	var auth authFrame
	if err := json.Unmarshal([]byte(lines[0]), &auth); err != nil || auth.Type != "auth" {
		t.Fatalf("first line is not an auth frame: %q", lines[0])
	}
	var msg map[string]any
	if err := json.Unmarshal([]byte(lines[1]), &msg); err != nil {
		t.Fatalf("second line is not JSON: %q", lines[1])
	}
	if msg["type"] != "user" || msg["priority"] != "next" || msg["msgV"] != float64(1) {
		t.Fatalf("unexpected user frame: %v", msg)
	}
	if _, ok := msg["session_id"]; ok {
		t.Fatal("session_id must not be sent: it goes stale on /clear and would lose the message silently")
	}
	if _, ok := msg["from"]; ok {
		t.Fatal("from must not be sent: AO has no address in the receiver's namespace")
	}
}

func TestBuildFrameOmitsAuthWhenTheKeyIsUnreadable(t *testing.T) {
	frame, err := buildFrame(Session{}, "hi")
	if err != nil {
		t.Fatalf("buildFrame: %v", err)
	}
	if strings.Count(string(frame), "\n") != 1 {
		t.Fatalf("want a single message line without auth, got %q", frame)
	}
}

func TestBuildFrameRefusesAMessageOverTheReceiversLineCap(t *testing.T) {
	if _, err := buildFrame(Session{}, strings.Repeat("x", maxFrameBytes+1)); err == nil {
		t.Fatal("want a refusal for a message over the receiver's line cap")
	}
}

// A message far larger than one tmux send-keys chunk must cross the socket in
// a single frame, which is the whole point of not typing it.
func TestLargeMessageArrivesWholeInOneFrame(t *testing.T) {
	box := newInbox(t)
	delegate := &fakeDelegate{}
	rt := newTestRuntime(t, delegate, fakeRegistry{session: Session{PID: 1, SessionID: "s1", SocketPath: box.path}})

	message := "start " + strings.Repeat("ก", 60_000) + " end"
	if err := rt.SendMessage(context.Background(), ports.RuntimeHandle{ID: "ao-1"}, message); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	got := box.userMessages(t, 1)
	if len(got) != 1 {
		t.Fatalf("socket received %d messages, want 1", len(got))
	}
	if got[0] != message {
		t.Fatalf("message arrived altered: %d bytes in, %d bytes out", len(message), len(got[0]))
	}
	if len(delegate.messages()) != 0 {
		t.Fatal("the pane also received the message")
	}
}

// ---- kill switch ---------------------------------------------------------

func TestDisableEnvPinsDeliveryToThePane(t *testing.T) {
	box := newInbox(t)
	delegate := &fakeDelegate{}
	rt := New(delegate, Options{Registry: fakeRegistry{session: Session{PID: 1, SessionID: "s1", SocketPath: box.path}}})
	t.Setenv(disableEnv, "0")

	if err := rt.SendMessage(context.Background(), ports.RuntimeHandle{ID: "ao-1"}, "typed, not sent"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if got := delegate.messages(); len(got) != 1 {
		t.Fatalf("tmux path got %q, want exactly one copy", got)
	}
	if got := box.userMessages(t, 0); len(got) != 0 {
		t.Fatalf("socket received %q with the kill switch on", got)
	}
}

// A delegate failure is the caller's error, unchanged from before this adapter
// existed.
func TestDelegateErrorSurfaces(t *testing.T) {
	delegate := &fakeDelegate{err: errors.New("tmux is gone")}
	rt := newTestRuntime(t, delegate, fakeRegistry{err: reject("no-descriptor")})
	if err := rt.SendMessage(context.Background(), ports.RuntimeHandle{ID: "ao-1"}, "x"); err == nil {
		t.Fatal("want the delegate's error")
	}
}
