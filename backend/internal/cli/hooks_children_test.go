package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
)

// childServer stands in for the daemon's child routes: it answers each path
// with a canned body and records what was called, in order.
type childServer struct {
	mu     sync.Mutex
	calls  []string
	bodies map[string]string
}

func newChildServer(t *testing.T, responses map[string]string) (*httptest.Server, *childServer) {
	t.Helper()
	cs := &childServer{bodies: map[string]string{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		cs.mu.Lock()
		cs.calls = append(cs.calls, r.URL.Path)
		cs.bodies[r.URL.Path] = string(body)
		cs.mu.Unlock()
		resp, ok := responses[r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{}`)
			return
		}
		status := http.StatusOK
		if strings.HasPrefix(resp, "!") {
			status = http.StatusUnprocessableEntity
			resp = resp[1:]
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, resp)
	}))
	t.Cleanup(srv.Close)
	return srv, cs
}

func (cs *childServer) called(path string) bool {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	for _, c := range cs.calls {
		if c == path {
			return true
		}
	}
	return false
}

func childHookEnv(t *testing.T, responses map[string]string) *childServer {
	t.Helper()
	t.Setenv("AO_SESSION_ID", "ao-7")
	t.Setenv("AO_SESSION_KIND", "worker")
	t.Setenv(envChildWorktrees, "1")
	cfg := setConfigEnv(t)
	srv, cs := newChildServer(t, responses)
	writeRunFileFor(t, cfg, srv)
	return cs
}

func TestHooks_WorktreeCreatePrintsOnlyThePath(t *testing.T) {
	cs := childHookEnv(t, map[string]string{
		"/api/v1/sessions/ao-7/children": `{"child":{"worktreePath":"/data/child-worktrees/p/ao-7/a1","state":"running"}}`,
	})
	payload := `{"hook_event_name":"WorktreeCreate","cwd":"/ws/ao-7","name":"agent-a1"}`
	out, errOut, err := executeCLI(t, Deps{In: strings.NewReader(payload), ProcessAlive: func(int) bool { return true }}, "hooks", "claude-code", "worktree-create")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	if out != "/data/child-worktrees/p/ao-7/a1\n" {
		t.Fatalf("stdout = %q, want the path alone: Claude Code reads it as the worktree", out)
	}
	if got := cs.bodies["/api/v1/sessions/ao-7/children"]; !strings.Contains(got, `"name":"agent-a1"`) || !strings.Contains(got, `"cwd":"/ws/ao-7"`) {
		t.Fatalf("create body = %s", got)
	}
}

func TestHooks_WorktreeCreateFailsClosed(t *testing.T) {
	childHookEnv(t, map[string]string{
		"/api/v1/sessions/ao-7/children": `!{"error":"unprocessable","code":"NESTED_CHILD_REFUSED","message":"a child cannot have child worktrees of its own"}`,
	})
	payload := `{"hook_event_name":"WorktreeCreate","cwd":"/data/child-worktrees/p/ao-7/a1","name":"agent-b1"}`
	out, _, err := executeCLI(t, Deps{In: strings.NewReader(payload), ProcessAlive: func(int) bool { return true }}, "hooks", "claude-code", "worktree-create")
	if err == nil {
		t.Fatal("a refused creation exited 0: Claude Code would then run the subagent with no worktree")
	}
	if out != "" || !strings.Contains(err.Error(), "child worktrees of its own") || !strings.Contains(err.Error(), "without isolation") {
		t.Fatalf("stdout %q err %v, want no path and a reason with the way forward", out, err)
	}
}

func TestHooks_SubagentStopRelaysTheBlock(t *testing.T) {
	childHookEnv(t, map[string]string{
		"/api/v1/sessions/ao-7/children/a1/stop": `{"known":true,"block":true,"reason":"AO: commit first"}`,
	})
	payload := `{"hook_event_name":"SubagentStop","agent_id":"a1","agent_type":"general-purpose","cwd":"/data/child-worktrees/p/ao-7/a1"}`
	out, errOut, err := executeCLI(t, Deps{In: strings.NewReader(payload), ProcessAlive: func(int) bool { return true }}, "hooks", "claude-code", "subagent-stop")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	var got struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil || got.Decision != "block" || got.Reason != "AO: commit first" {
		t.Fatalf("stdout = %q (%v), want the native block", out, err)
	}
}

func TestHooks_SubagentStartBriefsAChild(t *testing.T) {
	childHookEnv(t, map[string]string{
		"/api/v1/sessions/ao-7/children/a1/start": `{"known":true,"brief":"## AO child worktree"}`,
	})
	payload := `{"hook_event_name":"SubagentStart","agent_id":"a1","agent_type":"general-purpose"}`
	out, _, err := executeCLI(t, Deps{In: strings.NewReader(payload), ProcessAlive: func(int) bool { return true }}, "hooks", "claude-code", "subagent-start")
	if err != nil {
		t.Fatal(err)
	}
	var got hookContext
	if err := json.Unmarshal([]byte(out), &got); err != nil || got.HookSpecificOutput.HookEventName != "SubagentStart" || got.HookSpecificOutput.AdditionalContext != "## AO child worktree" {
		t.Fatalf("stdout = %q (%v)", out, err)
	}
}

func TestHooks_WorkersAgentCallDescribesTheChildAndCarriesNotes(t *testing.T) {
	cs := childHookEnv(t, map[string]string{
		"/api/v1/sessions/ao-7/children/notes": `{"notes":["AO merged subagent a0 into feature/w"]}`,
	})
	payload := `{"hook_event_name":"PostToolUse","tool_name":"Agent","tool_input":{"description":"Write B","subagent_type":"general-purpose","isolation":"worktree"},"tool_response":{"isAsync":true,"status":"async_launched","agentId":"a1"}}`
	out, _, err := executeCLI(t, Deps{In: strings.NewReader(payload), ProcessAlive: func(int) bool { return true }}, "hooks", "claude-code", "post-tool-use")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cs.bodies["/api/v1/sessions/ao-7/children/a1/describe"], `"description":"Write B"`) {
		t.Fatalf("describe body = %q", cs.bodies["/api/v1/sessions/ao-7/children/a1/describe"])
	}
	var got hookContext
	if err := json.Unmarshal([]byte(out), &got); err != nil || got.HookSpecificOutput.HookEventName != "PostToolUse" ||
		!strings.Contains(got.HookSpecificOutput.AdditionalContext, "AO merged subagent a0") {
		t.Fatalf("stdout = %q (%v), want the note as PostToolUse context", out, err)
	}
	if !cs.called("/api/v1/sessions/ao-7/activity") {
		t.Fatal("the activity report was lost to the child handling")
	}
}

func TestHooks_ASubagentsToolCallDoesNotDrainTheWorkersNotes(t *testing.T) {
	cs := childHookEnv(t, nil)
	payload := `{"hook_event_name":"PostToolUse","agent_id":"a1","tool_name":"Write","tool_input":{"file_path":"/x"}}`
	if _, _, err := executeCLI(t, Deps{In: strings.NewReader(payload), ProcessAlive: func(int) bool { return true }}, "hooks", "claude-code", "post-tool-use"); err != nil {
		t.Fatal(err)
	}
	if cs.called("/api/v1/sessions/ao-7/children/notes") {
		t.Fatal("a subagent's own tool call asked for the worker's notes")
	}
}

func TestHooks_IsolatedChildIsAllowedWhenAOOwnsChildWorktrees(t *testing.T) {
	childHookEnv(t, nil)
	payload := `{"hook_event_name":"PreToolUse","tool_name":"Agent","tool_input":{"description":"implement task","isolation":"worktree"}}`
	out, _, err := executeCLI(t, Deps{In: strings.NewReader(payload), ProcessAlive: func(int) bool { return true }}, "hooks", "claude-code", "pre-tool-use")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "deny") {
		t.Fatalf("stdout = %q, want no denial", out)
	}
}

func TestHooks_APausedSubagentIsLeftAlone(t *testing.T) {
	cs := childHookEnv(t, map[string]string{
		"/api/v1/sessions/ao-7/children/a1/stop": `{"known":true}`,
	})
	transcript := t.TempDir() + "/agent-a1.jsonl"
	if err := os.WriteFile(transcript, []byte(`Command running in background with ID: sh1. Output is being written to: /x`), 0o600); err != nil {
		t.Fatal(err)
	}
	payload := `{"hook_event_name":"SubagentStop","agent_id":"a1","agent_transcript_path":"` + transcript + `","background_tasks":[{"id":"sh1","type":"shell","status":"running"}]}`
	out, _, err := executeCLI(t, Deps{In: strings.NewReader(payload), ProcessAlive: func(int) bool { return true }}, "hooks", "claude-code", "subagent-stop")
	if err != nil {
		t.Fatal(err)
	}
	if out != "" || cs.called("/api/v1/sessions/ao-7/children/a1/stop") {
		t.Fatalf("a subagent waiting on its own background shell was settled (stdout %q)", out)
	}
}
