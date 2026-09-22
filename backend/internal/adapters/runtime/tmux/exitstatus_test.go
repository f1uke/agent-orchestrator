package tmux

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/endingslog"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// The pane's exit line is written by a shell and read by endingslog, two ends
// of one contract that share no code. This runs the real snippet in every shell
// AO may launch through, and reads the result back with the real reader.
func TestExitStatusRecorder_RealShellsWriteWhatTheJournalReads(t *testing.T) {
	cases := []struct {
		agent  string
		code   int
		signal string
	}{
		{agent: "exit 0", code: 0},
		{agent: "exit 3", code: 3},
		{agent: "kill -TERM $$", code: 143, signal: "SIGTERM"},
		{agent: "kill -HUP $$", code: 129, signal: "SIGHUP"},
		{agent: "kill -KILL $$", code: 137, signal: "SIGKILL"},
	}
	for _, shell := range []string{"/bin/sh", "/bin/bash", "/bin/zsh"} {
		if _, err := os.Stat(shell); err != nil {
			continue
		}
		t.Run(filepath.Base(shell), func(t *testing.T) {
			dir := t.TempDir()
			journal := endingslog.JournalPath(dir)
			for i, c := range cases {
				// The agent is a child shell, exactly as the real agent is a child
				// of the pane's leader; the recorder then runs in the leader.
				cmd := shellQuote("/bin/sh") + " -c " + shellQuote(c.agent) + "; " + exitStatusRecorder(journal, domain.SessionID(fmt.Sprintf("s-%d", i)))
				out, err := exec.Command(shell, "-c", cmd).CombinedOutput()
				if err != nil {
					t.Fatalf("%s: %v: %s", c.agent, err, out)
				}
				// bash reports a child killed by a signal ("Terminated: 15") with
				// or without the recorder; what must never appear is the
				// recorder's own noise - a shell without zmodload, a failed write.
				for _, noise := range []string{"zmodload", "not found", "printf", "endings.jsonl"} {
					if strings.Contains(string(out), noise) {
						t.Errorf("%s: the recorder printed into the pane: %q", c.agent, out)
					}
				}
			}
			entries, err := endingslog.Read(dir)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != len(cases) {
				t.Fatalf("read %d exits, want %d", len(entries), len(cases))
			}
			byID := map[string]*endingslog.AgentExit{}
			for _, e := range entries {
				byID[e.SessionID] = e.Exit
			}
			for i, c := range cases {
				x := byID[fmt.Sprintf("s-%d", i)]
				if x == nil || x.Code != c.code || x.Signal != c.signal {
					t.Errorf("%s: exit = %+v, want code %d signal %q", c.agent, x, c.code, c.signal)
					continue
				}
				if x.At.IsZero() {
					t.Errorf("%s: exit carries no time", c.agent)
				}
			}
		})
	}
}

// A recorder that cannot write must stay silent: the pane is the human's view
// of the agent, and an error there after every exit would be noise.
func TestExitStatusRecorder_FailsSilently(t *testing.T) {
	rec := exitStatusRecorder("/nonexistent-dir/for-sure/endings.jsonl", "s")
	out, err := exec.Command("/bin/sh", "-c", "false; "+rec+"; echo after").CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if string(out) != "after\n" {
		t.Errorf("output = %q, want only what follows the recorder", out)
	}
}

// Only a launch that asks gets the recorder, and never one that would write into
// the worktree.
func TestBuildLaunchCommand_RecordsTheExitOnlyWhenAsked(t *testing.T) {
	base := ports.RuntimeConfig{SessionID: "s-1", Argv: []string{"claude"}}
	if got := buildLaunchCommand(base); strings.Contains(got, "agentExit") {
		t.Errorf("a launch that did not ask records exits: %q", got)
	}

	rel := base
	rel.ExitStatusFile = "endings.jsonl"
	if got := buildLaunchCommand(rel); strings.Contains(got, "agentExit") {
		t.Errorf("a relative journal would land in the worktree: %q", got)
	}

	asked := base
	asked.ExitStatusFile = "/data/endings.jsonl"
	got := buildLaunchCommand(asked)
	claude := strings.Index(got, "'claude'")
	rec := strings.Index(got, "__ao_rc=$?")
	keep := strings.Index(got, `exec "${SHELL:-/bin/sh}" -i`)
	if claude < 0 || rec < 0 || keep < 0 || claude >= rec || rec >= keep {
		t.Errorf("want agent, then recorder, then keep-alive shell: %q", got)
	}
	if !strings.Contains(got, `'"s-1"'`) || !strings.Contains(got, "'/data/endings.jsonl'") {
		t.Errorf("recorder does not name the session and the journal: %q", got)
	}
}
