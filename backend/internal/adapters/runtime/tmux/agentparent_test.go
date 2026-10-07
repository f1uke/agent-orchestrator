package tmux

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// A hook tells the session's own agent from a claude nested inside it by asking
// whether the agent's parent is the pane's leader shell. This runs the real launch
// command and checks that the pid it exports is the agent's parent.
func TestBuildLaunchCommand_ExportsTheAgentsParentPID(t *testing.T) {
	for _, shell := range []string{"/bin/sh", "/bin/bash", "/bin/zsh"} {
		if _, err := os.Stat(shell); err != nil {
			continue
		}
		t.Run(filepath.Base(shell), func(t *testing.T) {
			cmd := buildLaunchCommand(ports.RuntimeConfig{
				SessionID: "s-1",
				Argv:      []string{"/bin/sh", "-c", `echo "$` + ports.EnvAgentParentPID + ` $PPID"`},
			})
			run := exec.Command(shell, "-c", cmd)
			run.Env = append(os.Environ(), "SHELL=/usr/bin/true")
			out, err := run.CombinedOutput()
			if err != nil {
				t.Fatalf("%v: %s", err, out)
			}
			fields := strings.Fields(strings.TrimSpace(string(out)))
			if len(fields) != 2 || fields[0] == "" || fields[0] != fields[1] {
				t.Errorf("agent printed %q, want %s equal to its parent pid", out, ports.EnvAgentParentPID)
			}
		})
	}
}
