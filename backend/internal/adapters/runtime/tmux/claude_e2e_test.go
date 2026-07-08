package tmux

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// TestSendMessageAutoSubmitsAgainstRealClaude is a manual end-to-end check that
// the real SendMessage (with its production delays) reliably auto-submits a
// multi-line message to a live Claude Code TUI — no leftover un-submitted paste
// in the composer. It drives the actual production code path, only managing the
// tmux session lifecycle and pane capture itself.
//
// Opt-in (spawns a real `claude` and consumes a request):
//
//	AO_TMUX_CLAUDE_E2E=1 go test ./internal/adapters/runtime/tmux/ \
//	    -run TestSendMessageAutoSubmitsAgainstRealClaude -v
func TestSendMessageAutoSubmitsAgainstRealClaude(t *testing.T) {
	if os.Getenv("AO_TMUX_CLAUDE_E2E") == "" {
		t.Skip("set AO_TMUX_CLAUDE_E2E=1 to run the live-claude auto-submit check")
	}
	tmuxBin, err := exec.LookPath("tmux")
	if err != nil {
		t.Skipf("tmux not on PATH: %v", err)
	}
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skipf("claude not on PATH: %v", err)
	}

	dir := t.TempDir()
	const sess = "ao-tmux-claude-e2e"
	tmuxRun := func(args ...string) ([]byte, error) {
		return exec.Command(tmuxBin, args...).CombinedOutput()
	}
	_, _ = tmuxRun("kill-session", "-t", "="+sess)
	if out, err := tmuxRun("new-session", "-d", "-s", sess, "-x", "200", "-y", "50", "-c", dir, "claude"); err != nil {
		t.Fatalf("start claude: %v: %s", err, out)
	}
	t.Cleanup(func() { _, _ = tmuxRun("kill-session", "-t", "="+sess) })

	time.Sleep(6 * time.Second)
	// Accept the "trust this folder" prompt if shown, then let the composer settle.
	_, _ = tmuxRun("send-keys", "-t", sess, "Enter")
	time.Sleep(5 * time.Second)

	// Deliver a multi-line message via the REAL production path (real delays).
	msg := "multi-line probe:\nalpha line\nbeta line\ngamma line\nplease acknowledge"
	r := New(Options{}) // production defaults: 15ms chunk delay, 300ms enter delay
	if err := r.SendMessage(context.Background(), ports.RuntimeHandle{ID: sess}, msg); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	// After submit the composer clears: the last "❯" line holds no message text.
	// Poll briefly to allow the TUI to repaint.
	var lastComposer string
	submitted := false
	for i := 0; i < 20; i++ {
		time.Sleep(200 * time.Millisecond)
		out, err := tmuxRun("capture-pane", "-t", sess, "-p")
		if err != nil {
			t.Fatalf("capture-pane: %v: %s", err, out)
		}
		lastComposer = composerLine(string(out))
		if lastComposer == "❯" {
			submitted = true
			break
		}
	}
	if !submitted {
		t.Fatalf("message was NOT submitted: composer still shows %q (expected empty \"❯\")", lastComposer)
	}
}

// composerLine returns the trimmed content of the TUI's input line — the last
// line beginning with the "❯" prompt marker. Empty composer => just "❯".
func composerLine(pane string) string {
	last := ""
	for _, line := range strings.Split(pane, "\n") {
		trimmed := strings.TrimRight(line, " ")
		if strings.HasPrefix(strings.TrimSpace(trimmed), "❯") {
			last = strings.TrimSpace(trimmed)
		}
	}
	return last
}
