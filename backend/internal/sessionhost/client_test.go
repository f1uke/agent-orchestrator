package sessionhost

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/chatproto"
	"github.com/aoagents/agent-orchestrator/backend/internal/sockpath"
)

func TestClient(t *testing.T) {
	dir := shortDir(t)
	cfg, _ := fakeConfig(t, dir, waitingFixture(t, dir))
	startHost(t, cfg)
	c := NewClient(cfg.SocketPath)
	ctx := context.Background()

	st, err := c.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.PID == 0 {
		t.Errorf("state = %+v, want the pid of the hosted claude", st)
	}

	origin := &chatproto.Origin{Via: "ao_send", From: "mer-2"}
	id, err := c.Send(ctx, "[from @mer-2] hello", origin)
	if err != nil {
		t.Fatal(err)
	}
	if id == "" {
		t.Fatal("Send returned no uuid")
	}
	var noted, sent bool
	for _, e := range loadEntries(t, cfg.LogPath) {
		switch e.Dir {
		case chatproto.DirHost:
			if note, err := chatproto.ParseHostNote(e.Payload); err == nil && note.Kind == chatproto.NoteUserOrigin {
				noted = note.UUID == id && note.Origin != nil && *note.Origin == *origin
			}
		case chatproto.DirIn:
			if strings.Contains(string(e.Payload), id) && strings.Contains(string(e.Payload), "[from @mer-2] hello") {
				sent = true
			}
		}
	}
	if !noted || !sent {
		t.Errorf("log: origin noted %v, message sent %v; want both under uuid %s", noted, sent, id)
	}

	if _, err := c.Send(ctx, "", nil); err == nil || !strings.Contains(err.Error(), "send needs text or images") {
		t.Errorf("empty Send err = %v, want the host's refusal", err)
	}
}

func TestClientWithNoHost(t *testing.T) {
	c := NewClient(filepath.Join(shortDir(t), "none.sock"))
	if _, err := c.Send(context.Background(), "hi", nil); err == nil {
		t.Fatal("Send to a socket nobody serves succeeded")
	}
	if _, err := c.State(context.Background()); err == nil {
		t.Fatal("State from a socket nobody serves succeeded")
	}
}

func TestPaths(t *testing.T) {
	logPath, socketPath := Paths("/Users/me/.ao/data", "mer-1")
	if logPath != "/Users/me/.ao/data/sessions/mer-1/events.jsonl" || socketPath != "/Users/me/.ao/data/hosts/mer-1.sock" {
		t.Fatalf("Paths = %q, %q", logPath, socketPath)
	}

	deep := filepath.Join(t.TempDir(), strings.Repeat("d", 120))
	logPath, socketPath, err := PreparePaths(deep, "mer-1")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(socketPath)) })
	if len(socketPath) > sockpath.MaxLen {
		t.Errorf("socket %q is %d bytes, over %d", socketPath, len(socketPath), sockpath.MaxLen)
	}
	if strings.HasPrefix(socketPath, deep) {
		t.Errorf("socket %q did not fall back from the deep data dir", socketPath)
	}
	if !strings.HasPrefix(logPath, deep) {
		t.Errorf("log %q left the data dir", logPath)
	}
	if _, other := Paths(deep, "mer-2"); other == socketPath {
		t.Error("two sessions share a socket")
	}
	if runtime.GOOS == "windows" {
		return
	}
	for _, d := range []string{filepath.Dir(logPath), filepath.Dir(socketPath)} {
		info, err := os.Stat(d)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o700 {
			t.Errorf("%s mode = %o, want 700", d, got)
		}
	}
}
