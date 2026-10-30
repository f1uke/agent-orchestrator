package sessionhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
		if turnEnds(tr) > 0 {
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

func TestRealClaudeKeepsTheChosenModelAcrossARestart(t *testing.T) {
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
	turn := func(r *running, ended int) chatproto.Transcript {
		t.Helper()
		callOK(t, cfg.SocketPath, map[string]any{"op": "send", "text": "Reply with just the word ok. Do not use tools."})
		deadline := time.Now().Add(2 * time.Minute)
		for {
			tr, err := chatproto.Fold(loadEntries(t, cfg.LogPath))
			if err != nil {
				t.Fatalf("log does not fold: %v", err)
			}
			if turnEnds(tr) >= ended {
				return tr
			}
			if time.Now().After(deadline) {
				t.Fatalf("no turn end\npane:\n%s", r.pane)
			}
			time.Sleep(200 * time.Millisecond)
		}
	}

	first := startHost(t, cfg)
	if tr := turn(first, 1); !strings.Contains(tr.Model, "haiku") {
		t.Fatalf("launch model = %q, want haiku", tr.Model)
	}
	callOK(t, cfg.SocketPath, map[string]any{"op": "set_model", "model": "sonnet"})
	first.cancel()
	_ = first.wait(t)

	second := startHost(t, cfg)
	tr := turn(second, 2)
	second.cancel()
	_ = second.wait(t)
	t.Logf("after restart: model %q, chosen %q, sessions %v", tr.Model, tr.ChosenModel, tr.ClaudeSessionIDs)
	if !strings.Contains(tr.Model, "sonnet") {
		t.Fatalf("model after restart = %q, want the sonnet chosen before it", tr.Model)
	}
}

func turnEnds(tr chatproto.Transcript) int {
	n := 0
	for _, it := range tr.Items {
		if _, ok := it.(chatproto.TurnEnd); ok {
			n++
		}
	}
	return n
}
