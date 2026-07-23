package tmux

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// isolatedTmux returns a path to a wrapper that execs the real tmux on a private
// socket, so the test never touches the developer's default tmux server (which
// hosts their live AO sessions). Skips when tmux is unavailable.
func isolatedTmux(t *testing.T) string {
	t.Helper()
	tmuxPath, err := exec.LookPath("tmux")
	if err != nil {
		t.Skipf("tmux not on PATH: %v", err)
	}
	socket := fmt.Sprintf("ao-send-e2e-%d", os.Getpid())
	wrapper := filepath.Join(t.TempDir(), "tmux")
	script := fmt.Sprintf("#!/bin/sh\nexec %q -L %q \"$@\"\n", tmuxPath, socket)
	if err := os.WriteFile(wrapper, []byte(script), 0o755); err != nil { //nolint:gosec // G306: an executable wrapper must be executable
		t.Fatalf("write tmux wrapper: %v", err)
	}
	t.Cleanup(func() { _ = exec.Command(tmuxPath, "-L", socket, "kill-server").Run() })
	return wrapper
}

// TestSendMessageDeliversLargeMixedMessageThroughRealTmux is the end-to-end
// proof that a long message survives the real transport: it drives the
// production SendMessage (production chunking and delays) against a real tmux
// server and compares the bytes that landed in the pane against what was sent.
//
// It covers exactly what unit tests with a fake runner cannot:
//   - tmux really accepts every command the chunker produces (before the fix,
//     anything from 16 KiB up died with "command too long" -> 500 INTERNAL_ERROR);
//   - the message arrives intact, in order, once — no truncation, no reordering,
//     no duplication across the chunk boundaries;
//   - multi-byte text survives: the payload mixes ASCII with Thai (3 bytes per
//     character), so chunk boundaries land inside characters.
//
// The pane runs `cat` with the tty in raw mode so the line discipline neither
// caps a line at MAX_CANON nor rewrites the bytes on their way to the file.
func TestSendMessageDeliversLargeMixedMessageThroughRealTmux(t *testing.T) {
	tmuxBin := isolatedTmux(t)
	const sess = "ao-send-e2e"
	sink := filepath.Join(t.TempDir(), "sink.txt")

	run := func(args ...string) ([]byte, error) {
		return exec.Command(tmuxBin, args...).CombinedOutput()
	}
	if out, err := run("new-session", "-d", "-s", sess,
		"sh -c 'stty raw -echo; cat > "+sink+"'"); err != nil {
		t.Fatalf("new-session: %v: %s", err, out)
	}
	t.Cleanup(func() { _, _ = run("kill-session", "-t", "="+sess) })
	waitForFile(t, sink) // the pane's redirect has created the file: cat is up

	// ~20 KB of mixed ASCII + Thai. Ends on a Thai character so the trailing-CR
	// trim below cannot eat message content.
	msg := strings.Repeat("brief line: สวัสดีครับ ทดสอบข้อความยาว 0123456789 ", 320)
	if len(msg) < 20*1024 {
		t.Fatalf("fixture is %d bytes, want >= 20 KiB to span many chunks", len(msg))
	}

	r := New(Options{Binary: tmuxBin}) // production chunk size and delays
	if err := r.SendMessage(context.Background(), ports.RuntimeHandle{ID: sess}, msg); err != nil {
		t.Fatalf("SendMessage of %d bytes: %v", len(msg), err)
	}

	got := waitForContent(t, sink, len(msg))
	// SendMessage submits with Enter, which reaches the pane as a CR.
	if trimmed := strings.TrimRight(got, "\r\n"); trimmed != msg {
		t.Fatalf("delivered %d bytes, sent %d; first difference at byte %d",
			len(trimmed), len(msg), firstDiff(trimmed, msg))
	}
}

// waitForFile blocks until path exists, so the test does not send into a pane
// whose `cat` has not started yet.
func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("pane never created %s", path)
}

// waitForContent polls until the sink holds at least want bytes and has stopped
// growing, so the assertion sees the whole delivery rather than a partial read.
func waitForContent(t *testing.T, path string, want int) string {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	last := -1
	for time.Now().Before(deadline) {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read sink: %v", err)
		}
		if len(raw) >= want && len(raw) == last {
			return string(raw)
		}
		last = len(raw)
		time.Sleep(100 * time.Millisecond)
	}
	raw, _ := os.ReadFile(path)
	t.Fatalf("sink settled at %d bytes, want at least %d — message was truncated in transit", len(raw), want)
	return ""
}

// firstDiff reports the byte offset where a and b diverge, for a failure message
// that points at the corruption instead of dumping 20 KB.
func firstDiff(a, b string) int {
	n := min(len(a), len(b))
	for i := range n {
		if a[i] != b[i] {
			return i
		}
	}
	return n
}
