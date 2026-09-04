package claudepeer

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/runtime/tmux"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// TestAgainstRealClaude is the manual end-to-end check behind this adapter's
// whole reason for existing: a message must reach a live Claude Code session
// WITHOUT touching the pane's input line, so it cannot fight the human for the
// keyboard. It drives the production Runtime (real tmux delegate, real
// registry, real socket) and only manages the session lifecycle itself.
//
// Opt-in (spawns a real `claude` and consumes a few requests):
//
//	AO_CLAUDEPEER_E2E=1 go test ./internal/adapters/runtime/claudepeer/ \
//	    -run TestAgainstRealClaude -v -timeout 5m
func TestAgainstRealClaude(t *testing.T) {
	if os.Getenv("AO_CLAUDEPEER_E2E") == "" {
		t.Skip("set AO_CLAUDEPEER_E2E=1 to run the live-claude delivery check")
	}
	tmuxBin, err := exec.LookPath("tmux")
	if err != nil {
		t.Skipf("tmux not on PATH: %v", err)
	}
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skipf("claude not on PATH: %v", err)
	}

	// Run claude in the repo checkout, not a scratch directory: Claude Code
	// refuses to start in a folder the user has not trusted, and a fresh
	// mktemp dir is never trusted.
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	dir := t.TempDir()
	// Unique per run: a session name reused across runs can match a descriptor
	// a previous run's claude left behind, and the test would then message a
	// pane that no longer exists.
	sess := fmt.Sprintf("ao-claudepeer-e2e-%d", os.Getpid())
	tmuxRun := func(args ...string) ([]byte, error) {
		return exec.Command(tmuxBin, args...).CombinedOutput()
	}
	_, _ = tmuxRun("kill-session", "-t", "="+sess)
	// Strip the CLAUDE_* env a session inherits when this test is itself run
	// from inside Claude Code: a child that sees them refuses to start its own
	// session, so it would never register an inbox to message.
	launch := "env" +
		" -u CLAUDECODE -u CLAUDE_CODE_CHILD_SESSION -u CLAUDE_CODE_SESSION_ID" +
		" -u CLAUDE_CODE_MESSAGING_SOCKET -u CLAUDE_CODE_MESSAGING_TOKEN" +
		" -u CLAUDE_PID -u CLAUDE_CODE_ENTRYPOINT -u AI_AGENT claude"
	if out, err := tmuxRun("new-session", "-d", "-s", sess, "-x", "200", "-y", "50", "-c", cwd, launch); err != nil {
		t.Fatalf("start claude: %v: %s", err, out)
	}
	t.Cleanup(func() { _, _ = tmuxRun("kill-session", "-t", "="+sess) })
	if out, err := tmuxRun("has-session", "-t", "="+sess); err != nil {
		t.Fatalf("claude exited before the pane settled: %v: %s", err, out)
	}

	// Pane targets take a plain name: tmux's "=" exact-match prefix is only
	// accepted where a SESSION is expected, and a pane target carrying it
	// fails with "can't find pane".
	capture := func() string {
		out, err := tmuxRun("capture-pane", "-p", "-S", "-200", "-t", sess)
		if err != nil {
			t.Fatalf("capture-pane: %v: %s", err, out)
		}
		return string(out)
	}
	waitFor := func(what string, contains string, timeout time.Duration) string {
		deadline := time.Now().Add(timeout)
		var pane string
		for time.Now().Before(deadline) {
			pane = capture()
			if strings.Contains(pane, contains) {
				return pane
			}
			time.Sleep(300 * time.Millisecond)
		}
		t.Fatalf("timed out waiting for %s (%q). Pane:\n%s", what, contains, pane)
		return ""
	}

	handle := ports.RuntimeHandle{ID: sess}
	rt := New(tmux.New(tmux.Options{}), Options{})
	ctx := context.Background()

	// The session must register itself before the adapter can find it.
	registry := NewFileRegistry(FileRegistryOptions{})
	deadline := time.Now().Add(60 * time.Second)
	for {
		if _, err := registry.Lookup(ctx, sess); err == nil {
			break
		} else if time.Now().After(deadline) {
			t.Fatalf("claude never registered a messageable session for tmux %q: %v", sess, err)
		}
		time.Sleep(500 * time.Millisecond)
	}

	// --- the headline: deliver while the human is mid-keystroke -----------
	const draft = "half-typed draft the human is still writing"
	if out, err := tmuxRun("send-keys", "-t", sess, "-l", draft); err != nil {
		t.Fatalf("type into the pane: %v: %s", err, out)
	}
	time.Sleep(500 * time.Millisecond)
	if err := rt.SendMessage(ctx, handle, "E2E-INTERLEAVE probe. Do not reply and take no action."); err != nil {
		t.Fatalf("SendMessage while typing: %v", err)
	}
	pane := waitFor("the socket-delivered message", "E2E-INTERLEAVE", 60*time.Second)
	if !strings.Contains(pane, draft) {
		t.Fatalf("the human's half-typed line was lost or corrupted. Pane:\n%s", pane)
	}
	if strings.Contains(pane, draft+"E2E-INTERLEAVE") || strings.Contains(pane, "E2E-INTERLEAVE"+draft) {
		t.Fatalf("the message merged into the human's input line. Pane:\n%s", pane)
	}

	// --- bigger than one send-keys chunk, in one frame --------------------
	big := "E2E-BIG-START " + strings.Repeat("x", 40_000) + " E2E-BIG-END. Do not reply."
	if err := rt.SendMessage(ctx, handle, big); err != nil {
		t.Fatalf("SendMessage (large): %v", err)
	}
	pane = waitFor("the large message's tail", "E2E-BIG-END", 90*time.Second)
	if !strings.Contains(pane, "E2E-BIG-START") {
		t.Fatalf("the large message arrived without its head. Pane:\n%s", pane)
	}

	// --- a dead socket still delivers, through the pane -------------------
	fallbackRT := New(tmux.New(tmux.Options{}), Options{
		Registry: fakeRegistry{session: Session{PID: 1, SessionID: "gone", SocketPath: dir + "/not-a-socket.sock"}},
	})
	if err := fallbackRT.SendMessage(ctx, handle, "E2E-FALLBACK probe. Do not reply."); err != nil {
		t.Fatalf("SendMessage (dead socket): %v", err)
	}
	pane = waitFor("the fallback message", "E2E-FALLBACK", 60*time.Second)
	// The fallback types into the composer, so it lands ON the human's
	// half-typed line - which is exactly the behaviour the socket path exists
	// to avoid, and the clearest possible proof that the two paths differ.
	if !strings.Contains(pane, draft+"E2E-FALLBACK") {
		t.Fatalf("the fallback message did not go through the pane's input line. Pane:\n%s", pane)
	}
	fmt.Println(pane)
}
