package sessionhost

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/chatproto"
)

const effortAccepted = `{"type":"control_response","response":{"subtype":"success","request_id":"ef"}}`

func effortHost(t *testing.T, extra ...fixtureLine) Config {
	t.Helper()
	dir := shortDir(t)
	cfg, _ := fakeConfig(t, dir, waitingFixture(t, dir, extra...))
	startHost(t, cfg)
	eventually(t, "initialize answered", func() bool { return state(t, cfg.SocketPath).LastSeq >= 3 })
	return cfg
}

func TestClientSetEffortAppliesTheLevelFromTheNextRequest(t *testing.T) {
	cfg := effortHost(t,
		fixtureLine{chatproto.DirIn, encoded(t, chatproto.SetEffort{RequestID: "ef", Level: "low"})},
		fixtureLine{chatproto.DirOut, effortAccepted},
	)
	if err := NewClient(cfg.SocketPath).SetEffort(context.Background(), "low"); err != nil {
		t.Fatalf("SetEffort(low) = %v, want claude's success", err)
	}
	got := loggedInputs(t, cfg.LogPath, "control_request/apply_flag_settings")
	if len(got) != 1 || !strings.Contains(got[0], `"settings":{"effortLevel":"low"}`) {
		t.Errorf("logged flag settings = %v, want one setting effortLevel low", got)
	}
}

func TestSetEffortRefusesALevelNoModelOffers(t *testing.T) {
	cfg := effortHost(t)
	err := NewClient(cfg.SocketPath).SetEffort(context.Background(), "turbo")
	var refused *RefusedError
	if !errors.As(err, &refused) || refused.Reason != `unknown effort level "turbo"` {
		t.Fatalf("SetEffort(turbo) err = %v, want a refusal naming the level", err)
	}
	if got := loggedInputs(t, cfg.LogPath, "control_request/apply_flag_settings"); len(got) != 0 {
		t.Errorf("a refused level reached claude: %v", got)
	}
}

func TestSetEffortEndsWhenClaudeExits(t *testing.T) {
	cfg := effortHost(t,
		fixtureLine{chatproto.DirIn, encoded(t, chatproto.SetEffort{RequestID: "ef", Level: "low"})},
		fixtureLine{chatproto.DirHost, `{"kind":"exit","code":0}`},
	)
	err := NewClient(cfg.SocketPath).SetEffort(context.Background(), "low")
	var refused *RefusedError
	if !errors.As(err, &refused) || refused.Reason != errNotRunning.Error() {
		t.Fatalf("err = %v, want a refusal saying claude is not running", err)
	}
}

func TestHostAsksClaudeForItsSettingsAfterInitialize(t *testing.T) {
	dir := shortDir(t)
	body := filepath.Join(dir, "settings.json")
	const answer = `{"effective":{"env":{"TOKEN":"hunter2"}},"sources":{},"applied":{"model":"claude-sonnet-5-5","effort":"medium"}}`
	if err := os.WriteFile(body, []byte(answer), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(envFakeSettings, body)
	cfg, _ := fakeConfig(t, dir, waitingFixture(t, dir))
	startHost(t, cfg)

	eventually(t, "the starting effort is folded", func() bool {
		tr, err := chatproto.Fold(loadEntries(t, cfg.LogPath))
		return err == nil && tr.Effort == "medium"
	})
	if got := loggedInputs(t, cfg.LogPath, "control_request/get_settings"); len(got) != 1 {
		t.Errorf("asked for settings %d times, want once after initialize", len(got))
	}
	raw, err := os.ReadFile(cfg.LogPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "hunter2") {
		t.Error("the log keeps the effective settings, which can hold secrets")
	}
}

func TestRestartKeepsTheChosenEffort(t *testing.T) {
	dir := shortDir(t)
	cfg, _, _ := runFirstSession(t, dir)
	before := loadEntries(t, cfg.LogPath)
	last := before[len(before)-1].Seq
	appendEntries(t, cfg.LogPath,
		chatproto.Entry{Seq: last + 1, Dir: chatproto.DirIn, Payload: json.RawMessage(
			`{"type":"control_request","request_id":"ef-1","request":{"subtype":"apply_flag_settings","settings":{"effortLevel":"low"}}}`)},
		chatproto.Entry{Seq: last + 2, Dir: chatproto.DirOut, Payload: json.RawMessage(
			`{"type":"control_response","response":{"subtype":"success","request_id":"ef-1"}}`)},
	)

	resumed := loadEntries(t, fixturePath(t, "resumed_session.jsonl"))
	cfg2, argvFile := fakeConfig(t, dir, fixturePath(t, "resumed_session.jsonl"))
	cfg2.Command = append(cfg2.Command, "--effort", "high")
	cfg2.PromptFile = cfg.PromptFile
	r := startHost(t, cfg2)
	replayInputs(t, cfg2.SocketPath, last+2, resumed, 0)
	if res := r.wait(t); res.err != nil || res.code != 0 {
		t.Fatalf("Run = %d, %v; want 0\npane:\n%s", res.code, res.err, r.pane)
	}

	argv := readArgv(t, argvFile)
	i := slices.Index(argv, "--effort")
	if i < 0 || i+1 >= len(argv) || argv[i+1] != "low" || slices.Contains(argv, "high") {
		t.Errorf("argv = %v, want the one --effort low chosen before the restart", argv)
	}
}

func TestSetEffortNeedsALevel(t *testing.T) {
	if _, err := parseRequest([]byte(`{"op":"set_effort"}`)); err == nil || err.Error() != "set_effort needs level" {
		t.Errorf("parseRequest(set_effort) err = %v, want %q", err, "set_effort needs level")
	}
}
