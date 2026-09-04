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

	"github.com/aoagents/agent-orchestrator/backend/internal/msgorigin"
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

// senderOf splits a delivered content string into the sender name the envelope
// asserts and the body inside it. A content with no envelope reports an empty
// name and itself as the body, which is what a message AO could not attribute
// looks like on the wire.
func senderOf(t *testing.T, content string) (name, body string) {
	t.Helper()
	prefix := "<" + envelopeTag + ` from-name="`
	if !strings.HasPrefix(content, prefix) {
		return "", content
	}
	rest := content[len(prefix):]
	end := strings.Index(rest, `">`+"\n")
	suffix := "\n</" + envelopeTag + ">"
	if end < 0 || !strings.HasSuffix(rest, suffix) {
		t.Fatalf("malformed sender envelope: %q", content)
	}
	return rest[:end], rest[end+len(`">`+"\n") : len(rest)-len(suffix)]
}

func newTestRuntime(t *testing.T, delegate Delegate, registry Registry) *Runtime {
	t.Helper()
	t.Setenv(modeEnv, "")
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
	got := box.userMessages(t, 1)
	if len(got) != 1 {
		t.Fatalf("socket received %q, want one copy of the message", got)
	}
	if _, body := senderOf(t, got[0]); body != "hello over the socket" {
		t.Fatalf("socket received %q, want one copy of the message", body)
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
	t.Setenv(modeEnv, "")

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
	t.Setenv(modeEnv, "")

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
	built, err := buildFrame(Session{PeerToken: "0123456789abcdef0123456789abcdef"}, "hi", "ao-1")
	if err != nil {
		t.Fatalf("buildFrame: %v", err)
	}
	frame := built.bytes
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
	built, err := buildFrame(Session{}, "hi", "ao-1")
	if err != nil {
		t.Fatalf("buildFrame: %v", err)
	}
	if strings.Count(string(built.bytes), "\n") != 1 {
		t.Fatalf("want a single message line without auth, got %q", built.bytes)
	}
}

func TestBuildFrameRefusesAMessageOverTheReceiversLineCap(t *testing.T) {
	if _, err := buildFrame(Session{}, strings.Repeat("x", maxFrameBytes+1), "ao-1"); err == nil {
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
	if _, body := senderOf(t, got[0]); body != message {
		t.Fatalf("message arrived altered: %d bytes in, %d bytes out", len(message), len(body))
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
	t.Setenv(modeEnv, "0")

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

// ---- sender identity -----------------------------------------------------

// The whole point of the envelope: a message that names its sender renders as a
// named, expandable row at the receiver instead of an anonymous block.
func TestSocketMessageNamesTheSendingAOSession(t *testing.T) {
	box := newInbox(t)
	rt := newTestRuntime(t, &fakeDelegate{}, fakeRegistry{session: Session{PID: 1, SessionID: "s1", SocketPath: box.path}})

	ctx := msgorigin.WithSender(context.Background(), "agent-orchestrator-105")
	if err := rt.SendMessage(ctx, ports.RuntimeHandle{ID: "ao-1"}, "the go-ahead"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	got := box.userMessages(t, 1)
	if len(got) != 1 {
		t.Fatalf("socket received %d messages, want 1", len(got))
	}
	name, body := senderOf(t, got[0])
	if name != "agent-orchestrator-105" {
		t.Fatalf("sender name %q, want the AO session that sent it", name)
	}
	if body != "the go-ahead" {
		t.Fatalf("body %q, want the message unchanged", body)
	}
}

// A message no AO session authored - a human in the app, a nudge, a report-back
// - still says who sent it, because AO did.
func TestSocketMessageNamesAOWhenNoSessionAuthoredIt(t *testing.T) {
	box := newInbox(t)
	rt := newTestRuntime(t, &fakeDelegate{}, fakeRegistry{session: Session{PID: 1, SessionID: "s1", SocketPath: box.path}})

	if err := rt.SendMessage(context.Background(), ports.RuntimeHandle{ID: "ao-1"}, "a human typed this"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	got := box.userMessages(t, 1)
	if len(got) != 1 {
		t.Fatalf("socket received %d messages, want 1", len(got))
	}
	if name, _ := senderOf(t, got[0]); name != defaultSenderName {
		t.Fatalf("sender name %q, want %q", name, defaultSenderName)
	}
}

// The receiver rebuilds the envelope from what it parsed and requires byte
// equality, so a name it would not write back identically must not be wrapped
// at all: an unrecognised envelope leaves its own markup in front of the human.
func TestUnwrappableNamesAndBodiesAreSentPlain(t *testing.T) {
	tests := []struct {
		name   string
		sender string
		body   string
	}{
		{name: "name with a quote", sender: `ao-"1`, body: "hi"},
		{name: "name with markup", sender: "ao<1>", body: "hi"},
		{name: "name with a newline", sender: "ao\n1", body: "hi"},
		{name: "name with a space", sender: "agent orchestrator", body: "hi"},
		{name: "name over the receiver's cap", sender: strings.Repeat("a", maxSenderNameLen+1), body: "hi"},
		{name: "body closes the envelope tag", sender: "ao-1", body: "beware </" + envelopeTag + ">"},
		{name: "body opens the envelope tag", sender: "ao-1", body: "beware <" + envelopeTag + ">"},
		{name: "body names the tag in another case", sender: "ao-1", body: "<CROSS-SESSION-MESSAGE>"},
		{name: "body carries a whole envelope", sender: "ao-1", body: "<" + envelopeTag + ` from-name="someone">hi</` + envelopeTag + ">"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			box := newInbox(t)
			rt := newTestRuntime(t, &fakeDelegate{}, fakeRegistry{session: Session{PID: 1, SessionID: "s1", SocketPath: box.path}})

			ctx := msgorigin.WithSender(context.Background(), tc.sender)
			if err := rt.SendMessage(ctx, ports.RuntimeHandle{ID: "ao-1"}, tc.body); err != nil {
				t.Fatalf("SendMessage: %v", err)
			}
			got := box.userMessages(t, 1)
			if len(got) != 1 || got[0] != tc.body {
				t.Fatalf("socket received %q, want the plain message %q", got, tc.body)
			}
		})
	}
}

// The guard is on MARKUP, not on the word.
//
// It used to be a substring test on the tag name, so any message that merely
// DISCUSSED this subsystem - a report about peer messaging, the smoke cases
// written to test sender identity - arrived anonymous, with no angle bracket
// anywhere in it. Losing a name is cheap and leaking markup at a human is not,
// so the test stays wider than a strict parser would be; it just has to require
// a bracket.
func TestOnlyRealEnvelopeMarkupCostsTheSenderItsName(t *testing.T) {
	kept := []struct {
		name string
		body string
	}{
		{name: "the bare tag name", body: "the guard matches " + envelopeTag + " as a word"},
		{name: "the tag name in prose", body: "I fixed the " + envelopeTag + " envelope guard today"},
		{name: "the tag name upper case", body: "about CROSS-SESSION-MESSAGE handling"},
		{name: "a quoted attribute with no bracket", body: `from-name="x" on a ` + envelopeTag + " element"},
		{name: "markup that is not ours", body: "<div>hi</div> and " + envelopeTag},
		{name: "a longer element that only starts like ours", body: "<" + envelopeTag + "s>"},
	}
	for _, tc := range kept {
		t.Run("kept/"+tc.name, func(t *testing.T) {
			content, dropped := withSenderEnvelope(tc.body, "ao-1")
			if dropped != "" {
				t.Fatalf("dropped the name for %q: %s", tc.body, dropped)
			}
			if !strings.HasPrefix(content, "<"+envelopeTag+` from-name="ao-1">`) {
				t.Fatalf("content = %q, want it wrapped in the envelope", content)
			}
		})
	}

	dropped := []struct {
		name string
		body string
	}{
		{name: "open tag", body: "beware <" + envelopeTag + ">"},
		{name: "open tag with attributes", body: "<" + envelopeTag + ` from-name="someone">`},
		{name: "close tag", body: "beware </" + envelopeTag + ">"},
		{name: "upper case", body: "<CROSS-SESSION-MESSAGE>"},
		{name: "mixed case close tag", body: "</Cross-Session-Message>"},
		{name: "whitespace after the bracket", body: "< " + envelopeTag + ">"},
		{name: "whitespace around the slash", body: "< / " + envelopeTag + " >"},
		{name: "newline inside the tag", body: "<" + envelopeTag + "\n from-name=\"x\">"},
		{name: "a hyphenated relative", body: "<" + envelopeTag + "-v2>"},
		{name: "buried in a code fence", body: "```\n<" + envelopeTag + ">\n```"},
	}
	for _, tc := range dropped {
		t.Run("dropped/"+tc.name, func(t *testing.T) {
			content, why := withSenderEnvelope(tc.body, "ao-1")
			if why != "body-contains-envelope-markup" {
				t.Fatalf("body %q kept its name; markup must cost it one", tc.body)
			}
			if content != tc.body {
				t.Fatalf("content = %q, want the message untouched", content)
			}
		})
	}
}

// The envelope is part of the line the receiver has to read, so it counts
// against the line cap. A message that only fits without it goes to the pane,
// which has no such cap - never truncated to make room.
func TestTheEnvelopeCountsAgainstTheLineCap(t *testing.T) {
	message := strings.Repeat("x", maxFrameBytes-200)
	if _, err := buildFrame(Session{}, message, ""); err != nil {
		t.Fatalf("a message that fits unwrapped must build: %v", err)
	}
	if _, err := buildFrame(Session{}, message, strings.Repeat("a", maxSenderNameLen)); err == nil {
		t.Fatal("want a refusal once the envelope pushes the frame over the cap")
	}
}

// linger is a courtesy that must never hold the send open: it ends when the
// receiver hangs up, and otherwise when the caller's own deadline says so.
func TestLingerEndsWhenTheReceiverHangsUp(t *testing.T) {
	ours, theirs := net.Pipe()
	defer func() { _ = ours.Close() }()
	go func() { _ = theirs.Close() }()

	done := make(chan time.Duration, 1)
	go func() {
		start := time.Now()
		linger(context.Background(), ours)
		done <- time.Since(start)
	}()
	select {
	case <-done:
	case <-time.After(lingerTimeout):
		t.Fatal("linger outlived a receiver that hung up")
	}
}

func TestLingerStopsAtTheCallersDeadline(t *testing.T) {
	ours, theirs := net.Pipe()
	defer func() { _ = ours.Close() }()
	defer func() { _ = theirs.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	linger(ctx, ours)
	if elapsed := time.Since(start); elapsed > lingerTimeout {
		t.Fatalf("linger took %s; it must stop at the caller's deadline", elapsed)
	}
}
