package claudecode

import (
	"context"
	"encoding/json"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// minChildWorktreeVersion is the oldest Claude Code verified to hand an
// isolated subagent's worktree to a WorktreeCreate hook with the payload AO
// reads ({"cwd": <session cwd>, "name": "agent-<id>"}), to fail the Agent call
// when the hook fails, and never to remove a hook-made worktree itself.
// Verified against 2.1.291 on 2026-10-06. An older binary keeps the
// shared-worktree rules, because with no hook it cuts the child from the
// default branch inside the primary checkout, where the work is lost.
var minChildWorktreeVersion = [3]int{2, 1, 291}

// childSupportTTL bounds how long a version probe is trusted. Claude Code
// updates itself in place, so the answer can change under a running daemon.
const childSupportTTL = 10 * time.Minute

var _ ports.ChildWorktreeAgent = (*Plugin)(nil)

// SupportsChildWorktrees implements ports.ChildWorktreeAgent.
func (p *Plugin) SupportsChildWorktrees(ctx context.Context) bool {
	p.childMu.Lock()
	defer p.childMu.Unlock()
	if !p.childCheckedAt.IsZero() && time.Since(p.childCheckedAt) < childSupportTTL {
		return p.childSupported
	}
	binary, err := p.claudeBinary(ctx)
	if err != nil {
		return false
	}
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(probeCtx, binary, "--version").Output() // #nosec G204 -- binary is the resolved claude executable.
	version, ok := parseClaudeVersion(string(out))
	p.childSupported = err == nil && ok && versionAtLeast(version, minChildWorktreeVersion)
	p.childCheckedAt = time.Now()
	return p.childSupported
}

// parseClaudeVersion reads "2.1.291 (Claude Code)".
func parseClaudeVersion(out string) ([3]int, bool) {
	fields := strings.Fields(out)
	if len(fields) == 0 {
		return [3]int{}, false
	}
	parts := strings.Split(fields[0], ".")
	if len(parts) != 3 {
		return [3]int{}, false
	}
	var v [3]int
	for i, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil {
			return [3]int{}, false
		}
		v[i] = n
	}
	return v, true
}

func versionAtLeast(v, min [3]int) bool {
	for i := range v {
		if v[i] != min[i] {
			return v[i] > min[i]
		}
	}
	return true
}

// ParseChildHook reads the child-lifecycle part of a Claude Code hook payload.
// event is the AO hook sub-command name. ok=false for a callback the child
// lifecycle does not act on, including every callback a subagent makes about
// its own tool calls.
func ParseChildHook(event string, payload []byte) (ports.ChildHook, bool) {
	var p struct {
		Cwd       string `json:"cwd"`
		Name      string `json:"name"`
		AgentID   string `json:"agent_id"`
		AgentType string `json:"agent_type"`
		ToolName  string `json:"tool_name"`
		ToolInput struct {
			Description  string `json:"description"`
			SubagentType string `json:"subagent_type"`
		} `json:"tool_input"`
		ToolResponse json.RawMessage `json:"tool_response"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return ports.ChildHook{}, false
	}
	switch event {
	case "worktree-create":
		return ports.ChildHook{Kind: ports.ChildHookCreate, NativeEvent: "WorktreeCreate", Name: p.Name, Cwd: p.Cwd}, true
	case "subagent-start":
		return ports.ChildHook{Kind: ports.ChildHookStart, NativeEvent: "SubagentStart", AgentID: p.AgentID, AgentType: p.AgentType}, p.AgentID != ""
	case "subagent-stop":
		return ports.ChildHook{Kind: ports.ChildHookStop, NativeEvent: "SubagentStop", AgentID: p.AgentID, AgentType: p.AgentType}, p.AgentID != ""
	case "user-prompt-submit":
		return ports.ChildHook{Kind: ports.ChildHookWorkerTurn, NativeEvent: "UserPromptSubmit"}, true
	case "post-tool-use":
		if p.AgentID != "" {
			return ports.ChildHook{}, false
		}
		hook := ports.ChildHook{Kind: ports.ChildHookWorkerTurn, NativeEvent: "PostToolUse"}
		if p.ToolName == "Agent" {
			hook.AgentID = agentIDFromToolResponse(p.ToolResponse)
			hook.AgentType = p.ToolInput.SubagentType
			hook.Description = p.ToolInput.Description
		}
		return hook, true
	}
	return ports.ChildHook{}, false
}

// agentIDFromToolResponse finds the subagent id in an Agent tool result. A
// background launch answers {"agentId": ...}; a foreground one ends its text
// with an "agentId: <id>" line.
func agentIDFromToolResponse(raw json.RawMessage) string {
	var obj struct {
		AgentID string `json:"agentId"`
	}
	if json.Unmarshal(raw, &obj) == nil && obj.AgentID != "" {
		return obj.AgentID
	}
	text := string(raw)
	if i := strings.Index(text, "agentId: "); i >= 0 {
		rest := text[i+len("agentId: "):]
		end := strings.IndexFunc(rest, func(r rune) bool {
			return (r < 'a' || r > 'z') && (r < '0' || r > '9')
		})
		if end < 0 {
			end = len(rest)
		}
		return rest[:end]
	}
	return ""
}
