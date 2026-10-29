package sessionhost

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/chatproto"
)

const testTimeout = 20 * time.Second

func fixturePath(t *testing.T, name string) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("..", "chatproto", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func loadEntries(t *testing.T, path string) []chatproto.Entry {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	entries, err := chatproto.DecodeLog(f)
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

func shortDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "sh")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func fakeConfig(t *testing.T, dir, fixture string) (Config, string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	argv := filepath.Join(dir, "argv.json")
	t.Setenv(envFakeFixture, fixture)
	t.Setenv(envFakeArgv, argv)
	return Config{
		Command:    []string{exe, "--model", "sonnet"},
		LogPath:    filepath.Join(dir, "log", "events.jsonl"),
		SocketPath: filepath.Join(dir, "h.sock"),
	}, argv
}

type runResult struct {
	code int
	err  error
}

type running struct {
	cfg    Config
	pane   *syncBuffer
	cancel context.CancelFunc
	done   chan runResult
}

func startHost(t *testing.T, cfg Config) *running {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	r := &running{cfg: cfg, pane: &syncBuffer{}, cancel: cancel, done: make(chan runResult, 1)}
	go func() {
		code, err := Run(ctx, cfg, r.pane)
		r.done <- runResult{code, err}
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case res := <-r.done:
			r.done <- res
		case <-time.After(testTimeout):
			t.Errorf("Run did not return after cancel")
		}
	})
	deadline := time.Now().Add(testTimeout)
	for {
		if c, err := net.Dial("unix", cfg.SocketPath); err == nil {
			_ = c.Close()
			return r
		}
		select {
		case res := <-r.done:
			t.Fatalf("Run returned before listening: code %d, err %v\npane:\n%s", res.code, res.err, r.pane)
		case <-time.After(5 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			t.Fatal("host socket never answered")
		}
	}
}

func (r *running) wait(t *testing.T) runResult {
	t.Helper()
	select {
	case res := <-r.done:
		r.done <- res
		return res
	case <-time.After(testTimeout):
		t.Fatalf("Run did not return\npane:\n%s", r.pane)
		return runResult{}
	}
}

func roundTrip(t *testing.T, sock string, req any) map[string]json.RawMessage {
	t.Helper()
	c, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	_ = c.SetDeadline(time.Now().Add(testTimeout))
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Write(append(b, '\n')); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(c).ReadBytes('\n')
	if err != nil {
		t.Fatalf("%s: read response: %v", b, err)
	}
	var resp map[string]json.RawMessage
	if err := json.Unmarshal(line, &resp); err != nil {
		t.Fatalf("%s: response %s: %v", b, line, err)
	}
	return resp
}

func callOK(t *testing.T, sock string, req any) map[string]json.RawMessage {
	t.Helper()
	resp := roundTrip(t, sock, req)
	if string(resp["ok"]) != "true" {
		t.Fatalf("%v: response ok=%s error=%s", req, resp["ok"], resp["error"])
	}
	return resp
}

func callErr(t *testing.T, sock string, req any) string {
	t.Helper()
	resp := roundTrip(t, sock, req)
	if string(resp["ok"]) != "false" {
		t.Fatalf("%v: want an error, got %v", req, resp)
	}
	var msg string
	_ = json.Unmarshal(resp["error"], &msg)
	return msg
}

func state(t *testing.T, sock string) stateResponse {
	t.Helper()
	resp := callOK(t, sock, map[string]any{"op": "state"})
	b, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}
	var s stateResponse
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatal(err)
	}
	return s
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(testTimeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

type subFrame struct {
	Entry *chatproto.Entry `json:"entry"`
	Delta json.RawMessage  `json:"delta"`
}

type subscription struct {
	frames chan subFrame
	conn   net.Conn
}

func subscribeTo(t *testing.T, sock string, from uint64) *subscription {
	t.Helper()
	c, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	if _, err := fmt.Fprintf(c, `{"op":"subscribe","from_seq":%d}`+"\n", from); err != nil {
		t.Fatal(err)
	}
	br := bufio.NewReader(c)
	line, err := br.ReadBytes('\n')
	if err != nil || !bytes.Equal(bytes.TrimSpace(line), []byte(`{"ok":true}`)) {
		t.Fatalf("subscribe answered %q, %v", line, err)
	}
	s := &subscription{frames: make(chan subFrame, 4096), conn: c}
	go func() {
		defer close(s.frames)
		for {
			line, err := br.ReadBytes('\n')
			if err != nil {
				return
			}
			var f subFrame
			if json.Unmarshal(line, &f) != nil {
				return
			}
			s.frames <- f
		}
	}()
	return s
}

func (s *subscription) next(t *testing.T) (subFrame, bool) {
	t.Helper()
	select {
	case f, ok := <-s.frames:
		return f, ok
	case <-time.After(testTimeout):
		t.Fatal("no frame from subscription")
		return subFrame{}, false
	}
}

func outCounter(t *testing.T, sub *subscription) *atomic.Int64 {
	var n atomic.Int64
	go func() {
		for f := range sub.frames {
			if f.Entry != nil && f.Entry.Dir == chatproto.DirOut {
				n.Add(1)
			}
		}
	}()
	return &n
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func writeFixture(t *testing.T, path string, payloads []fixtureLine) {
	t.Helper()
	var buf bytes.Buffer
	for i, p := range payloads {
		b, err := json.Marshal(chatproto.Entry{Seq: uint64(i + 1), Dir: p.dir, Payload: json.RawMessage(p.payload)})
		if err != nil {
			t.Fatal(err)
		}
		buf.Write(append(b, '\n'))
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

type fixtureLine struct {
	dir     chatproto.Direction
	payload string
}

func readArgv(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var argv []string
	if err := json.Unmarshal(b, &argv); err != nil {
		t.Fatal(err)
	}
	return argv
}

func socketGone(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("socket %s still there after Run returned (stat err %v)", path, err)
	}
}
