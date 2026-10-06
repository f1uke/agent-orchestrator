package claudecode

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hooksjson"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// The fixtures are real Claude Code 2.1.291 payloads captured on 2026-10-06
// (paths scrubbed). If an upgrade changes these shapes, the version gate in
// minChildWorktreeVersion is what has to move, after re-verifying.
func readChildHookFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "childhooks", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParseChildHookReadsRealPayloads(t *testing.T) {
	cases := []struct {
		event   string
		fixture string
		want    ports.ChildHook
	}{
		{"worktree-create", "worktree-create.json", ports.ChildHook{
			Kind: ports.ChildHookCreate, NativeEvent: "WorktreeCreate", Name: "agent-ad4368059f77a5382", Cwd: "/tmp/sandbox/wt/worker",
		}},
		{"subagent-start", "subagent-start.json", ports.ChildHook{
			Kind: ports.ChildHookStart, NativeEvent: "SubagentStart", AgentID: "ad4368059f77a5382", AgentType: "general-purpose",
		}},
		{"subagent-stop", "subagent-stop.json", ports.ChildHook{
			Kind: ports.ChildHookStop, NativeEvent: "SubagentStop", AgentID: "ad4368059f77a5382", AgentType: "general-purpose",
		}},
		{"post-tool-use", "post-tool-use-agent-async.json", ports.ChildHook{
			Kind: ports.ChildHookWorkerTurn, NativeEvent: "PostToolUse", AgentID: "a37b1b24e43f16f13", AgentType: "general-purpose", Description: "Create b.md",
		}},
	}
	for _, tc := range cases {
		got, ok := ParseChildHook(tc.event, readChildHookFixture(t, tc.fixture))
		if !ok || got != tc.want {
			t.Errorf("ParseChildHook(%s) = %+v, %v; want %+v", tc.fixture, got, ok, tc.want)
		}
	}
}

func TestParseChildHookIgnoresASubagentsOwnToolCalls(t *testing.T) {
	payload := []byte(`{"hook_event_name":"PostToolUse","agent_id":"a1","tool_name":"Write","cwd":"/tmp/children/a1"}`)
	if got, ok := ParseChildHook("post-tool-use", payload); ok {
		t.Fatalf("a subagent's tool call parsed as %+v; it must not drain the worker's notes", got)
	}
	if _, ok := ParseChildHook("pre-tool-use", []byte(`{"tool_name":"Agent"}`)); ok {
		t.Fatal("pre-tool-use is not a child lifecycle callback")
	}
}

func TestAgentIDFromAForegroundAgentResult(t *testing.T) {
	raw := json.RawMessage(`[{"type":"text","text":"done\nagentId: a42ce0bff37724a7f (use SendMessage with to: 'a42ce0bff37724a7f')\nworktreePath: /x"}]`)
	if got := agentIDFromToolResponse(raw); got != "a42ce0bff37724a7f" {
		t.Fatalf("agent id = %q", got)
	}
}

func TestClaudeVersionGate(t *testing.T) {
	cases := map[string]bool{
		"2.1.291 (Claude Code)": true,
		"2.1.300 (Claude Code)": true,
		"2.2.0 (Claude Code)":   true,
		"3.0.0":                 true,
		"2.1.290 (Claude Code)": false,
		"1.9.999":               false,
		"garbage":               false,
		"":                      false,
	}
	for out, want := range cases {
		v, ok := parseClaudeVersion(out)
		if got := ok && versionAtLeast(v, minChildWorktreeVersion); got != want {
			t.Errorf("%q supported = %v, want %v", out, got, want)
		}
	}
}

func TestChildHooksInstallOnlyWhenEnabled(t *testing.T) {
	p := &Plugin{resolvedBinary: "claude"}
	workspace := t.TempDir()
	cfg := ports.WorkspaceHookConfig{SessionID: "s1", WorkspacePath: workspace, ChildWorktrees: true}
	if err := p.GetAgentHooks(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	hooks := readInstalledHooks(t, workspace)
	for _, spec := range claudeChildHooks {
		if got := countClaudeHookCommand(hooks[spec.Event], spec.Command); got != 1 {
			t.Fatalf("%s count = %d, want 1", spec.Event, got)
		}
	}
	if timeout := hooks["WorktreeCreate"][0].Hooks[0].Timeout; timeout != claudeChildHookTimeout {
		t.Fatalf("WorktreeCreate timeout = %d, want %d (it waits on git)", timeout, claudeChildHookTimeout)
	}

	cfg.ChildWorktrees = false
	if err := p.GetAgentHooks(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	hooks = readInstalledHooks(t, workspace)
	for _, spec := range claudeChildHooks {
		if got := countClaudeHookCommand(hooks[spec.Event], spec.Command); got != 0 {
			t.Fatalf("%s still installed after the feature turned off: once a WorktreeCreate hook exists Claude Code creates no worktree itself", spec.Event)
		}
	}
	if got := countClaudeHookCommand(hooks["PreToolUse"], "ao hooks claude-code pre-tool-use"); got != 1 {
		t.Fatalf("base hooks lost when child hooks were removed: PreToolUse count %d", got)
	}
}

func readInstalledHooks(t *testing.T, workspace string) map[string][]hooksjson.MatcherGroup {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(workspace, ".claude", "settings.local.json"))
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Hooks map[string][]hooksjson.MatcherGroup `json:"hooks"`
	}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	return config.Hooks
}

// Found in the sandbox run: a subagent that started a background shell and
// ended its turn fired SubagentStop while it still meant to come back, and AO
// removed its worktree from under it. Its stop is a pause when a shell it
// started is still running; a shell some other agent started does not count.
func TestParseChildHookSeesASubagentPausedOnItsOwnBackgroundShell(t *testing.T) {
	dir := t.TempDir()
	own := filepath.Join(dir, "agent-a1.jsonl")
	if err := os.WriteFile(own, []byte(`{"type":"tool_result","content":"Command running in background with ID: byz8ktpua. Output is being written to: /tmp/x"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stop := func(transcript, tasks string) []byte {
		return []byte(`{"hook_event_name":"SubagentStop","agent_id":"a1","agent_type":"general-purpose","agent_transcript_path":"` + transcript + `","background_tasks":` + tasks + `}`)
	}
	runningShell := `[{"id":"byz8ktpua","type":"shell","status":"running","command":"sleep 20"}]`
	cases := []struct {
		name    string
		payload []byte
		paused  bool
	}{
		{"its own shell still running", stop(own, runningShell), true},
		{"its own shell finished", stop(own, `[{"id":"byz8ktpua","type":"shell","status":"completed"}]`), false},
		{"a shell another agent started", stop(own, `[{"id":"other123","type":"shell","status":"running"}]`), false},
		{"sibling subagents still running", stop(own, `[{"id":"a2","type":"subagent","status":"running"}]`), false},
		{"no background work", stop(own, `[]`), false},
		{"transcript unreadable with a live shell", stop(filepath.Join(dir, "missing.jsonl"), runningShell), true},
	}
	for _, tc := range cases {
		got, ok := ParseChildHook("subagent-stop", tc.payload)
		if !ok || got.Paused != tc.paused {
			t.Errorf("%s: paused = %v (ok %v), want %v", tc.name, got.Paused, ok, tc.paused)
		}
	}
}
