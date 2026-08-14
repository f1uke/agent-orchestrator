package httpd_test

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/simbridge"
	"github.com/aoagents/agent-orchestrator/backend/internal/simctl"
	"github.com/aoagents/agent-orchestrator/backend/internal/simkeyboard"
	"github.com/aoagents/agent-orchestrator/backend/internal/simstream"
)

// streamScreen is a screen whose subscription the test drives, and which
// records whether the subscription's context was cancelled - the signal the hub
// uses to stop a capture.
type streamScreen struct {
	events chan simstream.Event
	subErr error

	mu        sync.Mutex
	udid      string
	subCtx    context.Context
	subscribe chan struct{}
}

func newStreamScreen() *streamScreen {
	return &streamScreen{events: make(chan simstream.Event, 4), subscribe: make(chan struct{}, 1)}
}

func (s *streamScreen) Devices(context.Context) (simctl.Listing, error) {
	return simctl.Listing{}, nil
}

func (s *streamScreen) Subscribe(ctx context.Context, udid string) (<-chan simstream.Event, error) {
	if s.subErr != nil {
		return nil, s.subErr
	}
	s.mu.Lock()
	s.udid, s.subCtx = udid, ctx
	s.mu.Unlock()
	select {
	case s.subscribe <- struct{}{}:
	default:
	}
	// A real hub closes the viewer's channel when its context ends. Mirroring
	// that here is what makes this a test of the handler and not of the hub.
	go func() {
		<-ctx.Done()
		close(s.events)
	}()
	return s.events, nil
}

func (s *streamScreen) Driver(context.Context) (simbridge.Driver, error) {
	return nil, errors.New("not used")
}

func (s *streamScreen) Keyboard(context.Context, string) (simkeyboard.Mode, error) {
	return simkeyboard.Mode{}, errors.New("not used")
}

func (s *streamScreen) subscribedUDID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.udid
}

func (s *streamScreen) subscriptionEnded() bool {
	s.mu.Lock()
	ctx := s.subCtx
	s.mu.Unlock()
	if ctx == nil {
		return false
	}
	select {
	case <-ctx.Done():
		return true
	default:
		return false
	}
}

func newStreamServer(t *testing.T, screen httpd.SimScreen) *httptest.Server {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil,
		httpd.APIDeps{SimScreen: screen}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	return srv
}

func dialStream(t *testing.T, srv *httptest.Server, udid string) (*websocket.Conn, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/sim-stream/"+udid, nil)
	if err != nil {
		cancel()
		t.Fatalf("dial: %v", err)
	}
	return conn, cancel
}

func TestSimStream_ForwardsFramesAsBinaryMessages(t *testing.T) {
	screen := newStreamScreen()
	srv := newStreamServer(t, screen)
	conn, cancel := dialStream(t, srv, "UDID-A")
	defer cancel()
	defer func() { _ = conn.CloseNow() }()

	<-screen.subscribe
	if got := screen.subscribedUDID(); got != "UDID-A" {
		t.Fatalf("subscribed to %q", got)
	}
	screen.events <- simstream.Event{Frame: &simbridge.Frame{
		Data: []byte("nal-bytes"), Kind: simbridge.FrameKeyframe, Width: 1320, Height: 2868,
	}}

	ctx, done := context.WithTimeout(context.Background(), 5*time.Second)
	defer done()
	kind, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if kind != websocket.MessageBinary {
		t.Fatalf("frames must be binary, got %v", kind)
	}
	// A viewer that cannot tell a keyframe from a delta cannot decode the
	// stream at all, so the kind and the framebuffer size lead every message.
	if len(data) < 5 {
		t.Fatalf("frame message is too short to carry its header: %d bytes", len(data))
	}
	if data[0] != byte(simbridge.FrameKeyframe) {
		t.Fatalf("frame kind byte %d, want %d", data[0], simbridge.FrameKeyframe)
	}
	if w := binary.BigEndian.Uint16(data[1:3]); w != 1320 {
		t.Fatalf("frame width %d, want 1320", w)
	}
	if h := binary.BigEndian.Uint16(data[3:5]); h != 2868 {
		t.Fatalf("frame height %d, want 2868", h)
	}
	if string(data[5:]) != "nal-bytes" {
		t.Fatalf("frame body %q", data[5:])
	}
}

// This is the whole no-polling-when-nobody-looks guarantee at the transport
// level: the socket closing IS the viewer leaving. If this ever stops holding,
// a hidden tab keeps a capture process running.
func TestSimStream_ClosingTheSocketEndsTheSubscription(t *testing.T) {
	screen := newStreamScreen()
	srv := newStreamServer(t, screen)
	conn, cancel := dialStream(t, srv, "UDID-A")
	defer cancel()
	<-screen.subscribe

	if screen.subscriptionEnded() {
		t.Fatal("the subscription ended before the socket closed")
	}
	_ = conn.Close(websocket.StatusNormalClosure, "tab hidden")

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if screen.subscriptionEnded() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("closing the socket did not end the subscription: a hidden tab would keep capturing")
}

// A device that goes away mid-view has to say so in words rather than just
// dropping the connection.
func TestSimStream_StreamFailureIsReportedBeforeTheSocketCloses(t *testing.T) {
	screen := newStreamScreen()
	srv := newStreamServer(t, screen)
	conn, cancel := dialStream(t, srv, "UDID-A")
	defer cancel()
	defer func() { _ = conn.CloseNow() }()
	<-screen.subscribe

	screen.events <- simstream.Event{Err: errors.New("the device is gone")}

	ctx, done := context.WithTimeout(context.Background(), 5*time.Second)
	defer done()
	kind, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if kind != websocket.MessageText {
		t.Fatalf("a status must be text, got %v", kind)
	}
	var status struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(data, &status); err != nil {
		t.Fatalf("status body %q: %v", data, err)
	}
	if status.Type != "ended" || !strings.Contains(status.Message, "device is gone") {
		t.Fatalf("status was %+v", status)
	}
}

func TestSimStream_UnsubscribableDeviceGetsAReasonNotSilence(t *testing.T) {
	screen := newStreamScreen()
	screen.subErr = errors.New("no capture mechanism on this machine")
	srv := newStreamServer(t, screen)
	conn, cancel := dialStream(t, srv, "UDID-A")
	defer cancel()
	defer func() { _ = conn.CloseNow() }()

	ctx, done := context.WithTimeout(context.Background(), 5*time.Second)
	defer done()
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(data), "no capture mechanism") {
		t.Fatalf("body %q", data)
	}
}

// A daemon with no simulator surface must not offer a route that can only ever
// fail.
func TestSimStream_NotMountedWithoutAScreen(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	res, err := http.Get(srv.URL + "/sim-stream/UDID-A") //nolint:noctx // test helper
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("status %d, want 404", res.StatusCode)
	}
}
