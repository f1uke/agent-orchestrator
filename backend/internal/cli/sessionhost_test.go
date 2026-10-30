package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSessionHostUsage(t *testing.T) {
	setConfigEnv(t)
	for name, args := range map[string][]string{
		"no agent command":           {"session-host", "--log", "l", "--socket", "s"},
		"agent command not after --": {"session-host", "--log", "l", "--socket", "s", "claude"},
		"no log":                     {"session-host", "--socket", "s", "--", "claude"},
		"no socket":                  {"session-host", "--log", "l", "--", "claude"},
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := executeCLI(t, Deps{}, args...)
			if got := ExitCode(err); got != 2 {
				t.Fatalf("ExitCode(%v) = %d, want 2", err, got)
			}
		})
	}
}

func TestSessionHostIsHidden(t *testing.T) {
	out, _, err := executeCLI(t, Deps{}, "--help")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "session-host") {
		t.Fatalf("hidden session-host command leaked into help:\n%s", out)
	}
}

func TestSessionHostPassesClaudesExitCodeThrough(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in agent is a POSIX shell script")
	}
	setConfigEnv(t)
	dir, err := os.MkdirTemp("", "sh")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	log := filepath.Join(dir, "events.jsonl")
	out, _, err := executeCLI(t, Deps{}, "session-host", "--log", log, "--socket", filepath.Join(dir, "h.sock"), "--", "sh", "-c", "exit 3")
	if got := ExitCode(err); got != 3 {
		t.Fatalf("ExitCode(%v) = %d, want 3", err, got)
	}
	for _, want := range []string{"── claude started", "── claude exited 3"} {
		if !strings.Contains(out, want) {
			t.Errorf("pane missing %q:\n%s", want, out)
		}
	}
	if b, err := os.ReadFile(log); err != nil || !strings.Contains(string(b), `"kind":"exit","code":3`) {
		t.Errorf("log %q (%v), want an exit note with code 3", b, err)
	}
}
