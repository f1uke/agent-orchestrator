package tmux

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/locale"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// TestPaneGetsUTF8LocaleFromLocalelessServer reproduces the Finder-launched
// app: a tmux server whose environment has no locale. A pane takes its
// environment from that server, not from the client that asked for it, so the
// launch script is the only place that can give the agent a locale.
//
// It runs on its own tmux server (a private TMUX_TMPDIR, TMUX unset so the
// client does not follow a surrounding session's socket), never the user's.
func TestPaneGetsUTF8LocaleFromLocalelessServer(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux unavailable")
	}
	// A short socket dir: tmux socket paths are capped near 104 bytes.
	sockDir, err := os.MkdirTemp("", "aoloc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })
	for _, k := range []string{"TMUX", "LC_ALL", "LC_CTYPE", "LANG"} {
		t.Setenv(k, "") // registers the restore
		if err := os.Unsetenv(k); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("TMUX_TMPDIR", sockDir)

	r := New(Options{Timeout: 5 * time.Second, Shell: "/bin/sh"})
	t.Cleanup(func() {
		// Socket-targeted: kills only the private server this test started.
		_, _ = r.runner.Run(context.Background(), nil, r.binary, "kill-server")
	})

	for _, tc := range []struct {
		name string
		env  map[string]string
		want string // LC_ALL|LC_CTYPE|LANG as the agent sees them
	}{
		{"no locale anywhere gets a UTF-8 LC_CTYPE", nil, "||" + locale.CType + "|"},
		{"a project-configured LANG is left alone", map[string]string{"LANG": "th_TH.UTF-8"}, "|||th_TH.UTF-8"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ws := t.TempDir()
			out := filepath.Join(ws, "locale.txt")
			id := strings.ReplaceAll(t.Name(), "/", "_")
			h, err := r.Create(context.Background(), ports.RuntimeConfig{
				SessionID:     domain.SessionID(id),
				WorkspacePath: ws,
				Argv:          []string{"sh", "-c", `printf '|%s|%s|%s' "$LC_ALL" "$LC_CTYPE" "$LANG" > locale.txt`},
				Env:           tc.env,
			})
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			t.Cleanup(func() { _ = r.Destroy(context.Background(), h) })

			deadline := time.Now().Add(5 * time.Second)
			for {
				got, err := os.ReadFile(out)
				if err == nil && len(got) > 0 {
					if string(got) != tc.want {
						t.Fatalf("agent locale = %q, want %q", got, tc.want)
					}
					return
				}
				if time.Now().After(deadline) {
					t.Fatalf("agent never wrote %s", out)
				}
				time.Sleep(50 * time.Millisecond)
			}
		})
	}
}
