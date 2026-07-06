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

// claudeStartupMatcher is referenced by pointer so SessionStart serializes with
// its required "startup" matcher.
var claudeStartupMatcher = "startup"

// claudeManagedHooks is the source of truth for the hooks AO installs.
var claudeManagedHooks = []hooksjson.HookSpec{
	{Event: "SessionStart", Matcher: &claudeStartupMatcher, Command: claudeHookCommandPrefix + "session-start"},
	{Event: "UserPromptSubmit", Command: claudeHookCommandPrefix + "user-prompt-submit"},
	// Pre/PostToolUse install without a matcher so they fire for every tool,
	// including tool calls inside Task sub-agents (verified: those fire the
	// session's hooks with the same session_id). They report "active", keeping
	// the session working during long in-turn stretches — a sub-agent run, a
	// permission approved in the TUI — that emit none of the other hooks.
	{Event: "PreToolUse", Command: claudeHookCommandPrefix + "pre-tool-use"},
	{Event: "PostToolUse", Command: claudeHookCommandPrefix + "post-tool-use"},
	{Event: "Stop", Command: claudeHookCommandPrefix + "stop"},
	{Event: "Notification", Command: claudeHookCommandPrefix + "notification"},
	{Event: "SessionEnd", Command: claudeHookCommandPrefix + "session-end"},
}

// claudeHooks manages AO's hooks in the workspace-local
// .claude/settings.local.json file.
var claudeHooks = hooksjson.Manager{
	Label:         "claude-code",
	CommandPrefix: claudeHookCommandPrefix,
	Timeout:       claudeHookTimeout,
	Path:          claudeSettingsPath,
	Managed:       claudeManagedHooks,
}

func claudeSettingsPath(workspacePath string) string {
	return filepath.Join(workspacePath, claudeSettingsDirName, claudeSettingsFileName)
}

// GetAgentHooks installs AO's Claude Code hooks, preserving user-defined hooks and unrelated settings.
func (p *Plugin) GetAgentHooks(ctx context.Context, cfg ports.WorkspaceHookConfig) error {
	return claudeHooks.Install(ctx, cfg.WorkspacePath)
}

// UninstallHooks removes AO's Claude Code hooks, leaving user-defined hooks untouched.
func (p *Plugin) UninstallHooks(ctx context.Context, workspacePath string) error {
	return claudeHooks.Uninstall(ctx, workspacePath)
}

// AreHooksInstalled reports whether any AO Claude Code hook is present.
func (p *Plugin) AreHooksInstalled(ctx context.Context, workspacePath string) (bool, error) {
	return claudeHooks.AreInstalled(ctx, workspacePath)
}
