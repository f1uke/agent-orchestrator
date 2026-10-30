package sessionhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/chatproto"
)

func TestRealClaudeSmoke(t *testing.T) {
	if os.Getenv("AO_SESSIONHOST_REAL_CLAUDE") != "1" {
		t.Skip("set AO_SESSIONHOST_REAL_CLAUDE=1 to run the real claude")
	}
	claude, err := exec.LookPath("claude")
	if err != nil {
		t.Fatal(err)
	}
	dir := shortDir(t)
	work := filepath.Join(dir, "work")
	if err := os.Mkdir(work, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := Config{
		Command:    []string{claude, "--model", "haiku", "--setting-sources", "project", "--strict-mcp-config"},
		Dir:        work,
		LogPath:    filepath.Join(dir, "events.jsonl"),
		SocketPath: filepath.Join(dir, "h.sock"),
	}
	r := startHost(t, cfg)
	callOK(t, cfg.SocketPath, map[string]any{"op": "send", "text": "Reply with just the word ok. Do not use tools."})

	deadline := time.Now().Add(2 * time.Minute)
	for {
		tr, err := chatproto.Fold(loadEntries(t, cfg.LogPath))
		if err != nil {
			t.Fatalf("log does not fold: %v", err)
		}
		if turnEnded(tr) {
			t.Logf("turn ended: session %v, cost $%.4f, %d items", tr.ClaudeSessionIDs, tr.CostUSD, len(tr.Items))
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no turn end\npane:\n%s", r.pane)
		}
		time.Sleep(200 * time.Millisecond)
	}
	r.cancel()
	res := r.wait(t)
	tr, err := chatproto.Fold(loadEntries(t, cfg.LogPath))
	if err != nil {
		t.Fatalf("final log does not fold: %v", err)
	}
	t.Logf("Run = %d, %v; items %d\npane:\n%s", res.code, res.err, len(tr.Items), r.pane)
	if res.err != nil {
		t.Fatal(res.err)
	}
}

func turnEnded(tr chatproto.Transcript) bool {
	for _, it := range tr.Items {
		if _, ok := it.(chatproto.TurnEnd); ok {
			return true
		}
	}
	return false
}
