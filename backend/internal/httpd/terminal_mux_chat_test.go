package httpd

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/sessionhost"
	"github.com/aoagents/agent-orchestrator/backend/internal/terminal"
)

type chatSessionStub struct{}

func (chatSessionStub) GetSession(_ context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	return domain.SessionRecord{ID: id, Transport: domain.TransportChat}, true, nil
}

func TestMuxChatChannelWireShape(t *testing.T) {
	dataDir, err := os.MkdirTemp("", "mux")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dataDir) })
	logPath, _, err := sessionhost.PreparePaths(dataDir, "s1")
	if err != nil {
		t.Fatal(err)
	}
	log := `{"seq":1,"ts":"2026-10-30T01:02:03Z","dir":"host","payload":{"kind":"spawn"}}` + "\n" +
		`{"seq":2,"ts":"2026-10-30T01:02:04.5Z","dir":"out","payload":{"type":"system","subtype":"init"}}` + "\n"
	if err := os.WriteFile(logPath, []byte(log), 0o600); err != nil {
		t.Fatal(err)
	}
	mgr := terminal.NewManager(&stubSource{}, nil, discardLogger(), terminal.WithChat(chatSessionStub{}, dataDir))
	defer mgr.Close()
	c, done := dialMux(t, mgr)
	defer done()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := c.Write(ctx, websocket.MessageText, []byte(`{"ch":"chat","type":"open","id":"s1","fromSeq":1}`)); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`{"ch":"chat","id":"s1","type":"entry","entry":{"seq":2,"ts":"2026-10-30T01:02:04.5Z","dir":"out","payload":{"type":"system","subtype":"init"}}}`,
		`{"ch":"chat","id":"s1","type":"offline"}`,
	} {
		_, got, err := c.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want+"\n" {
			t.Fatalf("frame:\n got %s\nwant %s", got, want)
		}
	}
}
