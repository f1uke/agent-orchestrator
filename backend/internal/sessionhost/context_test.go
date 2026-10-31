package sessionhost

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/chatproto"
)

const contextBody = `{"categories":[{"id":"messages","name":"Messages","tokens":10,"kind":"used"}],"totalTokens":10,"maxTokens":1000,"percentage":1,"gridRows":[[{"color":"inactive","isFilled":true}]]}`

func contextRequests(entries []chatproto.Entry) []int {
	var at []int
	for i, e := range entries {
		if e.Dir == chatproto.DirIn && messageKind(e.Payload) == "control_request/get_context_usage" {
			at = append(at, i)
		}
	}
	return at
}

func answeringContext(t *testing.T, dir string) {
	t.Helper()
	p := filepath.Join(dir, "context.json")
	if err := os.WriteFile(p, []byte(contextBody), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(envFakeContext, p)
}

func TestContextUsageAskedAfterInitializeAndEachTurn(t *testing.T) {
	dir := shortDir(t)
	answeringContext(t, dir)
	cfg, _, _ := runFirstSession(t, dir)
	got := loadEntries(t, cfg.LogPath)

	asks := contextRequests(got)
	if len(asks) < 3 {
		t.Fatalf("asked for context usage %d times, want after initialize and after the turns", len(asks))
	}
	for _, i := range asks {
		prev := got[i-1]
		afterInit := prev.Dir == chatproto.DirOut && messageKind(prev.Payload) == "control_response/success" && i == 3
		afterResult := prev.Dir == chatproto.DirOut && messageKind(prev.Payload) == "result/"
		if !afterInit && !afterResult {
			t.Errorf("entry %d asks for context usage after %s, want only after initialize or a result", got[i].Seq, messageKind(prev.Payload))
		}
		if id := messageID(got[i].Payload); !strings.HasPrefix(id, "ctx-") {
			t.Errorf("context request id %q lacks the ctx- prefix", id)
		}
	}

	answered := 0
	for _, e := range got {
		if e.Dir != chatproto.DirOut || !strings.HasPrefix(messageID(e.Payload), "ctx-") {
			continue
		}
		answered++
		if bytes.Contains(e.Payload, []byte("gridRows")) {
			t.Errorf("seq %d logs the CLI's gridRows, which only repeat the categories", e.Seq)
		}
		if !bytes.Contains(e.Payload, []byte(`"categories"`)) {
			t.Errorf("seq %d lost the categories: %s", e.Seq, e.Payload)
		}
	}
	if answered != len(asks)-1 {
		t.Errorf("logged %d context answers, want %d: all but the ask after the last turn", answered, len(asks)-1)
	}
}

func unansweredContextHost(t *testing.T, timeout time.Duration) Config {
	t.Helper()
	dir := shortDir(t)
	first := loadEntries(t, fixturePath(t, "first_session.jsonl"))
	turn := func() []fixtureLine {
		return []fixtureLine{{chatproto.DirIn, string(first[3].Payload)}, {chatproto.DirOut, string(first[18].Payload)}}
	}
	cfg, _ := fakeConfig(t, dir, waitingFixture(t, dir, append(turn(), turn()...)...))
	cfg.contextTimeout = timeout
	startHost(t, cfg)
	eventually(t, "initialize answered", func() bool { return state(t, cfg.SocketPath).LastSeq >= 3 })
	return cfg
}

func asksIn(t *testing.T, cfg Config) int {
	t.Helper()
	return len(contextRequests(loadEntries(t, cfg.LogPath)))
}

func resultsIn(t *testing.T, cfg Config) int {
	t.Helper()
	n := 0
	for _, e := range loadEntries(t, cfg.LogPath) {
		if messageKind(e.Payload) == "result/" {
			n++
		}
	}
	return n
}

func TestUnansweredContextRequestIsNotRepeated(t *testing.T) {
	cfg := unansweredContextHost(t, time.Hour)
	eventually(t, "the first context request", func() bool { return asksIn(t, cfg) == 1 })

	callOK(t, cfg.SocketPath, map[string]any{"op": "send", "text": "one"})
	eventually(t, "the first turn", func() bool { return resultsIn(t, cfg) == 1 })
	callOK(t, cfg.SocketPath, map[string]any{"op": "send", "text": "two"})
	eventually(t, "the second turn", func() bool { return resultsIn(t, cfg) == 2 })
	if n := asksIn(t, cfg); n != 1 {
		t.Errorf("asked %d times while the first request was unanswered, want 1", n)
	}
}

func TestUnansweredContextRequestIsDroppedAfterTimeout(t *testing.T) {
	const timeout = 20 * time.Millisecond
	cfg := unansweredContextHost(t, timeout)
	eventually(t, "the first context request", func() bool { return asksIn(t, cfg) == 1 })
	time.Sleep(10 * timeout)
	if n := asksIn(t, cfg); n != 1 {
		t.Fatalf("asked %d times with no new turn, want 1: a timed-out request is not retried", n)
	}

	callOK(t, cfg.SocketPath, map[string]any{"op": "send", "text": "one"})
	eventually(t, "the next turn's context request", func() bool { return asksIn(t, cfg) == 2 })
}
