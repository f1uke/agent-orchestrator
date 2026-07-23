package tmux

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// A realistic AO session id: `ao-<project>-<num>` plus a branch-derived suffix.
// Length matters here — it is charged against tmux's per-command argv budget,
// so the longer the target id, the less room the literal payload has.
const longSessionID = "ao-agent-orchestrator-105-fix-ao-send-message-length"

// assertChunksFitBudget asserts every `tmux send-keys` the runtime emitted packs
// into a single tmux command message. tmux ships one command to its server in
// one libimsg message, so an over-budget argv is rejected outright with
// "command too long" — the failure that surfaced as a 500 INTERNAL_ERROR.
func assertChunksFitBudget(t *testing.T, calls []runnerCall) {
	t.Helper()
	for i, c := range calls {
		if got := packedArgvBytes(c.args); got > tmuxCommandArgvBudget {
			t.Fatalf("call %d packs %d argv bytes, over tmux's %d budget (tmux would reject it with %q)",
				i, got, tmuxCommandArgvBudget, "command too long")
		}
	}
}

// literalChunks returns just the payloads of the `send-keys -l` calls, in order.
func literalChunks(calls []runnerCall) []string {
	var out []string
	for _, c := range calls {
		if len(c.args) == 5 && c.args[0] == "send-keys" && c.args[3] == "-l" {
			out = append(out, c.args[4])
		}
	}
	return out
}

// TestSendMessageChunksFitTmuxBudgetAtDefaults is the core transport guard: with
// production defaults, a message far larger than any single tmux command must
// still be delivered as commands tmux will accept. Before the fix the default
// chunk size (16 KiB) was itself above tmux's argv budget, so every message of
// 16 KiB or more died with "command too long".
func TestSendMessageChunksFitTmuxBudgetAtDefaults(t *testing.T) {
	r, fr := newTestRuntime(0) // 0 => production default chunk size

	msg := strings.Repeat("a", 128*1024)
	if err := r.SendMessage(context.Background(), ports.RuntimeHandle{ID: longSessionID}, msg); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	assertChunksFitBudget(t, fr.calls)
	if got := strings.Join(literalChunks(fr.calls), ""); got != msg {
		t.Fatalf("reassembled %d bytes, want the original %d", len(got), len(msg))
	}
}

// TestSendMessageClampsOversizedChunkToTmuxBudget asserts an explicitly
// configured chunk size cannot push a command past what tmux accepts: the
// runtime clamps to the real per-command budget rather than emitting a doomed
// send-keys.
func TestSendMessageClampsOversizedChunkToTmuxBudget(t *testing.T) {
	r, fr := newTestRuntime(64 * 1024) // deliberately way over tmux's ceiling

	msg := strings.Repeat("b", 100*1024)
	if err := r.SendMessage(context.Background(), ports.RuntimeHandle{ID: longSessionID}, msg); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	assertChunksFitBudget(t, fr.calls)
	if got := strings.Join(literalChunks(fr.calls), ""); got != msg {
		t.Fatalf("reassembled %d bytes, want the original %d", len(got), len(msg))
	}
}

// TestSendKeysLiteralBudgetShrinksWithSessionID pins the derivation: the target
// id is packed into the same command as the payload, so a longer id leaves less
// room. Measured against tmux 3.6a, `send-keys -t <id> -l <chunk>` accepts
// exactly 16346-len(id) payload bytes and rejects one more.
func TestSendKeysLiteralBudgetShrinksWithSessionID(t *testing.T) {
	for _, id := range []string{"probe", longSessionID} {
		if got, want := sendKeysLiteralBudget(id), 16346-len(id); got != want {
			t.Fatalf("sendKeysLiteralBudget(%q) = %d, want %d", id, got, want)
		}
		// The whole command, filled to the budget, must fit — and one byte more
		// must not. This is the invariant the measured tmux boundary encodes.
		full := sendKeysLiteralArgs(id, strings.Repeat("x", sendKeysLiteralBudget(id)))
		if got := packedArgvBytes(full); got != tmuxCommandArgvBudget {
			t.Fatalf("budget-filled command packs %d bytes, want exactly %d", got, tmuxCommandArgvBudget)
		}
	}
}

// TestSendMessagePreservesThaiAcrossChunkBoundaries is the multi-byte-safety
// test the byte-denominated limit demands: Thai is 3 bytes per character, so a
// byte-aligned chunk boundary lands *inside* a character unless the splitter
// backs off to a rune boundary. A split character would reach the agent as
// mojibake, so every chunk must be valid UTF-8 on its own and the concatenation
// must be byte-identical to the original.
func TestSendMessagePreservesThaiAcrossChunkBoundaries(t *testing.T) {
	// 3 bytes per rune: no chunk size that is not a multiple of 3 can split this
	// text on a rune boundary by luck.
	msg := strings.Repeat("สวัสดีครับนี่คือข้อความภาษาไทยที่ยาวมาก", 400)
	if len(msg)%3 != 0 {
		t.Fatalf("fixture is not pure 3-byte runes (len=%d)", len(msg))
	}

	// 3070 % 3 == 1, so every boundary would fall mid-character without backoff.
	r, fr := newTestRuntime(3070)
	if err := r.SendMessage(context.Background(), ports.RuntimeHandle{ID: "sess-thai"}, msg); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	parts := literalChunks(fr.calls)
	if len(parts) < 2 {
		t.Fatalf("got %d chunks, want the message split so a boundary is exercised", len(parts))
	}
	for i, c := range parts {
		if !utf8.ValidString(c) {
			t.Fatalf("chunk %d is not valid UTF-8 — a multi-byte character was split", i)
		}
	}
	if got := strings.Join(parts, ""); got != msg {
		t.Fatalf("reassembled message differs from the original (%d vs %d bytes)", len(got), len(msg))
	}
}

// TestSendMessageRejectsSessionIDThatLeavesNoRoom asserts a pathologically long
// target id fails with a clear error instead of looping forever on a zero-width
// chunk or shipping a command tmux will refuse.
func TestSendMessageRejectsSessionIDThatLeavesNoRoom(t *testing.T) {
	r, fr := newTestRuntime(0)

	err := r.SendMessage(context.Background(), ports.RuntimeHandle{ID: strings.Repeat("z", 20000)}, "hi")
	if err == nil {
		t.Fatal("SendMessage: got nil, want an error for a session id that exhausts tmux's command budget")
	}
	if len(fr.calls) != 0 {
		t.Fatalf("emitted %d tmux calls, want none — the command could not have been accepted", len(fr.calls))
	}
}

// serialRunner records an ordered tag per tmux call and yields inside each one,
// so two concurrent sends interleave unless the runtime serializes them.
type serialRunner struct {
	mu   sync.Mutex
	tags []byte
}

func (s *serialRunner) Run(_ context.Context, _ []string, _ string, args ...string) ([]byte, error) {
	tag := byte('E') // the submitting Enter
	if len(args) == 5 && args[3] == "-l" && len(args[4]) > 0 {
		tag = args[4][0] // 'a' or 'b' — which message this chunk belongs to
	}
	s.mu.Lock()
	s.tags = append(s.tags, tag)
	s.mu.Unlock()
	time.Sleep(time.Millisecond) // widen the interleaving window
	return nil, nil
}

// TestSendMessageSerializesConcurrentSendsToSameSession asserts two sends racing
// on one session cannot interleave. Every message is now potentially many
// send-keys calls, so without serialization two senders would shred each other's
// text in the pane — and a stray Enter mid-message would submit half of one.
// The recorded call order must be one message's chunks then its Enter, then the
// other's.
func TestSendMessageSerializesConcurrentSendsToSameSession(t *testing.T) {
	sr := &serialRunner{}
	r := New(Options{Binary: "tmux-test", Timeout: time.Second, Shell: "/bin/sh", ChunkSize: 4})
	r.runner = sr
	r.sleep = func(time.Duration) {}

	const chunksEach = 6
	msgA := strings.Repeat("a", 4*chunksEach)
	msgB := strings.Repeat("b", 4*chunksEach)

	var wg sync.WaitGroup
	wg.Add(2)
	for _, m := range []string{msgA, msgB} {
		go func(msg string) {
			defer wg.Done()
			if err := r.SendMessage(context.Background(), ports.RuntimeHandle{ID: "race-1"}, msg); err != nil {
				t.Errorf("SendMessage: %v", err)
			}
		}(m)
	}
	wg.Wait()

	runA := strings.Repeat("a", chunksEach) + "E"
	runB := strings.Repeat("b", chunksEach) + "E"
	got := string(sr.tags)
	if got != runA+runB && got != runB+runA {
		t.Fatalf("call order = %q, want two uninterleaved runs (%q then %q, in either order)", got, runA, runB)
	}
}
