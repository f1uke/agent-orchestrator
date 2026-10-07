package claudecode

import (
	"context"
	"path/filepath"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hooksjson"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	claudeSettingsDirName   = ".claude"
	claudeSettingsFileName  = "settings.local.json"
	claudeHookCommandPrefix = "ao hooks claude-code "
	claudeHookTimeout       = 30
)

// HookAgentPIDEnv is set by Claude Code in a hook's environment to the pid of
// the claude process running the hook. Measured on 2.1.292: a hook fired by a
// `claude mcp list` run from the agent's Bash tool carries that nested
// process's pid, not the agent's.
const HookAgentPIDEnv = "CLAUDE_PID"

// claudeStartupMatcher is referenced by pointer so SessionStart serializes with
// its required "startup" matcher.
var claudeStartupMatcher = "startup"

// claudeManagedHooks is the source of truth for the hooks AO installs.
var claudeManagedHooks = []hooksjson.HookSpec{
	{Event: "SessionStart", Matcher: &claudeStartupMatcher, Command: claudeHookCommandPrefix + "session-start"},
	{Event: "UserPromptSubmit", Command: claudeHookCommandPrefix + "user-prompt-submit"},
	// Tool-use hooks install without a matcher so they fire for every tool,
	// including tool calls inside Task sub-agents (verified: those fire the
	// session's hooks with the same session_id). They report "active", keeping
	// the session working during long in-turn stretches — a sub-agent run, a
	// permission approved in the TUI — that emit none of the other hooks.
	// PostToolUseFailure is required alongside PostToolUse: a failing tool
	// (e.g. a nonzero bash exit) fires the failure variant INSTEAD.
	{Event: "PreToolUse", Command: claudeHookCommandPrefix + "pre-tool-use"},
	{Event: "PostToolUse", Command: claudeHookCommandPrefix + "post-tool-use"},
	{Event: "PostToolUseFailure", Command: claudeHookCommandPrefix + "post-tool-use-failure"},
	{Event: "Stop", Command: claudeHookCommandPrefix + "stop"},
	{Event: "Notification", Command: claudeHookCommandPrefix + "notification"},
	{Event: "SessionEnd", Command: claudeHookCommandPrefix + "session-end"},
}

// claudeChildHookTimeout is longer than the activity hooks' because these
// callbacks wait on git: creating a child worktree, and merging a stopping
// child into the worker's branch.
const claudeChildHookTimeout = 120

// claudeChildHooks hand an isolated subagent's worktree to AO. Installed only
// for a worker whose Claude Code supports the contract (see
// SupportsChildWorktrees): once a WorktreeCreate hook exists, Claude Code
// creates no worktree of its own, so these must never be present where AO
// cannot honour them.
var claudeChildHooks = []hooksjson.HookSpec{
	{Event: "WorktreeCreate", Command: claudeHookCommandPrefix + "worktree-create", Timeout: claudeChildHookTimeout},
	{Event: "SubagentStart", Command: claudeHookCommandPrefix + "subagent-start"},
	{Event: "SubagentStop", Command: claudeHookCommandPrefix + "subagent-stop", Timeout: claudeChildHookTimeout},
}

func claudeHookManager(managed []hooksjson.HookSpec) hooksjson.Manager {
	return hooksjson.Manager{
		Label:         "claude-code",
		CommandPrefix: claudeHookCommandPrefix,
		Timeout:       claudeHookTimeout,
		Path:          claudeSettingsPath,
		Managed:       managed,
	}
}

// claudeHooks manages AO's hooks in the workspace-local
// .claude/settings.local.json file. Removal and detection cover every hook AO
// may have installed, the child set included.
var (
	claudeHooks            = claudeHookManager(claudeManagedHooks)
	claudeChildHookManager = claudeHookManager(claudeChildHooks)
	claudeAllHooks         = claudeHookManager(append(append([]hooksjson.HookSpec{}, claudeManagedHooks...), claudeChildHooks...))
)

func claudeSettingsPath(workspacePath string) string {
	return filepath.Join(workspacePath, claudeSettingsDirName, claudeSettingsFileName)
}

// GetAgentHooks installs AO's Claude Code hooks, preserving user-defined hooks and unrelated settings.
func (p *Plugin) GetAgentHooks(ctx context.Context, cfg ports.WorkspaceHookConfig) error {
	if err := claudeHooks.Install(ctx, cfg.WorkspacePath); err != nil {
		return err
	}
	if cfg.ChildWorktrees {
		return claudeChildHookManager.Install(ctx, cfg.WorkspacePath)
	}
	return claudeChildHookManager.Uninstall(ctx, cfg.WorkspacePath)
}

// UninstallHooks removes AO's Claude Code hooks, leaving user-defined hooks untouched.
func (p *Plugin) UninstallHooks(ctx context.Context, workspacePath string) error {
	return claudeAllHooks.Uninstall(ctx, workspacePath)
}

// AreHooksInstalled reports whether any AO Claude Code hook is present.
func (p *Plugin) AreHooksInstalled(ctx context.Context, workspacePath string) (bool, error) {
	return claudeAllHooks.AreInstalled(ctx, workspacePath)
}
