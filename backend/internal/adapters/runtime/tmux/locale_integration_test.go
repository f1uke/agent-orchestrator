package tmux

import (
	"context"
	"os"
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
// Each session gets its own tmux server, started by the runtime from this
// process's (locale-less) environment, on a private socket dir - never the
// user's server.
func TestPaneGetsUTF8LocaleFromLocalelessServer(t *testing.T) {
	sockDir := privateSocketDir(t)
	for _, k := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		t.Setenv(k, "") // registers the restore
		if err := os.Unsetenv(k); err != nil {
			t.Fatal(err)
		}
	}

	r := New(Options{Timeout: 5 * time.Second, Shell: "/bin/sh", SocketDir: sockDir})

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
