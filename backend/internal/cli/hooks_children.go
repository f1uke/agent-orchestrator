package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// envChildWorktrees is set to "1" in a worker whose agent hands isolated
// subagents' worktrees to AO. Mirrors session_manager.EnvChildWorktrees.
const envChildWorktrees = "AO_CHILD_WORKTREES"

// createChildAPIRequest mirrors the daemon's CreateChildInput.
type createChildAPIRequest struct {
	Name string `json:"name"`
	Cwd  string `json:"cwd"`
}

type childAPIResponse struct {
	Child domain.SessionChild `json:"child"`
}

type childBriefAPIResponse struct {
	Known bool   `json:"known"`
	Brief string `json:"brief"`
}

type stopChildAPIResponse struct {
	Known  bool   `json:"known"`
	Block  bool   `json:"block"`
	Reason string `json:"reason"`
}

type describeChildAPIRequest struct {
	AgentType   string `json:"agentType,omitempty"`
	Description string `json:"description,omitempty"`
}

type childNotesAPIResponse struct {
	Notes []string `json:"notes"`
}

// hookContext is the native response that adds text to the agent's context.
type hookContext struct {
	HookSpecificOutput struct {
		HookEventName     string `json:"hookEventName"`
		AdditionalContext string `json:"additionalContext"`
	} `json:"hookSpecificOutput"`
}

// stopBlock is the native SubagentStop response that keeps the subagent going.
type stopBlock struct {
	Decision string `json:"decision"`
	Reason   string `json:"reason"`
}

// createChildWorktree answers WorktreeCreate. It is the one hook that fails
// CLOSED: once the hook exists Claude Code creates no worktree of its own, so
// the only honest answers are a real path or a failed Agent call that says why.
// The path is the only thing written to stdout.
func (c *commandContext) createChildWorktree(ctx context.Context, sessionID string, hook ports.ChildHook) error {
	var res childAPIResponse
	err := c.postJSON(ctx, "sessions/"+url.PathEscape(sessionID)+"/children", createChildAPIRequest{Name: hook.Name, Cwd: hook.Cwd}, &res)
	if err != nil {
		c.reportHookFailure("claude-code", "worktree-create", sessionID, err)
		return fmt.Errorf("AO could not create a worktree for this subagent: %v. Launch it without isolation to work in the worker's own worktree", err)
	}
	_, err = fmt.Fprintln(c.deps.Out, res.Child.WorktreePath)
	return err
}

// reportChildHook runs the best-effort child callbacks: briefing a starting
// child, settling a stopping one, and telling the worker what happened to its
// children. A failure is logged and never reaches the agent.
func (c *commandContext) reportChildHook(ctx context.Context, agent, event, sessionID string, hook ports.ChildHook) {
	base := "sessions/" + url.PathEscape(sessionID) + "/children"
	switch hook.Kind {
	case ports.ChildHookStart:
		var res childBriefAPIResponse
		if err := c.postJSON(ctx, base+"/"+url.PathEscape(hook.AgentID)+"/start", nil, &res); err != nil {
			c.reportHookFailure(agent, event, sessionID, err)
			return
		}
		if res.Known && res.Brief != "" {
			c.writeHookContext(hook.NativeEvent, res.Brief)
		}
	case ports.ChildHookStop:
		var res stopChildAPIResponse
		if err := c.postJSON(ctx, base+"/"+url.PathEscape(hook.AgentID)+"/stop", nil, &res); err != nil {
			c.reportHookFailure(agent, event, sessionID, err)
			return
		}
		if res.Block {
			_ = json.NewEncoder(c.deps.Out).Encode(stopBlock{Decision: "block", Reason: res.Reason})
		}
	case ports.ChildHookWorkerTurn:
		if hook.AgentID != "" {
			body := describeChildAPIRequest{AgentType: hook.AgentType, Description: hook.Description}
			if err := c.postJSON(ctx, base+"/"+url.PathEscape(hook.AgentID)+"/describe", body, nil); err != nil {
				c.reportHookFailure(agent, event, sessionID, err)
			}
		}
		var res childNotesAPIResponse
		if err := c.postJSON(ctx, base+"/notes", nil, &res); err != nil {
			c.reportHookFailure(agent, event, sessionID, err)
			return
		}
		if len(res.Notes) > 0 {
			c.writeHookContext(hook.NativeEvent, "AO child worktrees:\n- "+strings.Join(res.Notes, "\n- "))
		}
	}
}

func (c *commandContext) writeHookContext(nativeEvent, text string) {
	var out hookContext
	out.HookSpecificOutput.HookEventName = nativeEvent
	out.HookSpecificOutput.AdditionalContext = text
	_ = json.NewEncoder(c.deps.Out).Encode(out)
}
