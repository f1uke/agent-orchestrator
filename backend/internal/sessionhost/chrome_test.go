package sessionhost

import (
	"encoding/json"
	"os"
	"slices"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/chatproto"
)

const chromeInit = `{"type":"system","subtype":"init","session_id":"cs-1","model":"sonnet","permissionMode":"default","tools":[]}`

func TestSetChromeRelaunchesClaudeInPlace(t *testing.T) {
	dir := shortDir(t)
	cfg, argvFile := fakeConfig(t, dir, waitingFixture(t, dir, fixtureLine{chatproto.DirOut, chromeInit}))
	startHost(t, cfg)
	eventually(t, "claude's init is logged", func() bool { return state(t, cfg.SocketPath).ClaudeSessionID == "cs-1" })
	if argv := readArgv(t, argvFile); slices.Contains(argv, "--chrome") {
		t.Fatalf("first launch argv = %v, want no --chrome", argv)
	}
	firstPID := state(t, cfg.SocketPath).PID

	callOK(t, cfg.SocketPath, map[string]any{"op": "set_chrome", "enabled": true})
	eventually(t, "claude is relaunched", func() bool {
		s := state(t, cfg.SocketPath)
		return s.PID != 0 && s.PID != firstPID
	})
	var argv []string
	eventually(t, "the relaunch argv has --chrome", func() bool {
		b, err := os.ReadFile(argvFile)
		return err == nil && json.Unmarshal(b, &argv) == nil && slices.Contains(argv, "--chrome")
	})
	if i := slices.Index(argv, "--resume"); i < 0 || argv[i+1] != "cs-1" {
		t.Errorf("relaunch argv = %v, want --resume cs-1", argv)
	}
	if !state(t, cfg.SocketPath).Chrome {
		t.Error("state after the relaunch says chrome is off")
	}

	callOK(t, cfg.SocketPath, map[string]any{"op": "set_chrome", "enabled": true})
	if pid := state(t, cfg.SocketPath).PID; pid == firstPID {
		t.Errorf("pid went back to the first claude")
	}
}

func TestSetChromeWaitsForTheTurnToEnd(t *testing.T) {
	dir := shortDir(t)
	first := loadEntries(t, fixturePath(t, "first_session.jsonl"))
	cfg, _ := fakeConfig(t, dir, waitingFixture(t, dir,
		fixtureLine{chatproto.DirOut, chromeInit},
		fixtureLine{chatproto.DirOut, string(first[4].Payload)},
		fixtureLine{chatproto.DirOut, string(first[5].Payload)},
	))
	startHost(t, cfg)
	eventually(t, "the turn is running", func() bool { return state(t, cfg.SocketPath).Busy })
	pid := state(t, cfg.SocketPath).PID
	if got := callErr(t, cfg.SocketPath, map[string]any{"op": "set_chrome", "enabled": true}); got != errChromeBusy.Error() {
		t.Errorf("set_chrome while busy: error %q", got)
	}
	if s := state(t, cfg.SocketPath); s.PID != pid || s.Chrome {
		t.Errorf("a refused set_chrome changed the process: pid %d -> %d, chrome %v", pid, s.PID, s.Chrome)
	}
}

func TestSetChromeWaitsForBackgroundTasks(t *testing.T) {
	dir := shortDir(t)
	cfg, _ := fakeConfig(t, dir, waitingFixture(t, dir,
		fixtureLine{chatproto.DirOut, chromeInit},
		fixtureLine{chatproto.DirOut, `{"type":"system","subtype":"task_started","task_id":"bq1","task_type":"local_bash","description":"x"}`},
	))
	cfg.NoIdleCompact = true
	startHost(t, cfg)
	eventually(t, "claude's init is logged", func() bool { return state(t, cfg.SocketPath).ClaudeSessionID == "cs-1" })
	eventually(t, "the task start is logged", func() bool {
		return callErr(t, cfg.SocketPath, map[string]any{"op": "set_chrome", "enabled": true}) == errChromeBusy.Error()
	})
}
