// Package vibe implements the Mistral Vibe agent adapter: launching new
// non-interactive Vibe sessions and resuming sessions when a native Vibe
// session id is known.
//
// Mistral Vibe (binary "vibe", https://github.com/mistralai/mistral-vibe) is a
// Python CLI installed via `uv tool install mistral-vibe`, pip, or its install
// script. AO drives it in programmatic/headless mode with `-p <prompt>`, which
// auto-approves tools, prints the final response, and exits. `--trust` skips
// the working-directory trust prompt for non-interactive automation, and
// `--output text` pins the human-readable output format.
//
// Permission modes map onto Vibe's builtin agent profiles via `--agent`:
// accept-edits ("auto-approves file edits only") and auto-approve
// ("auto-approves all tool executions"). PermissionModeDefault emits no flag so
// Vibe resolves its starting agent from the user's `default_agent` config.
//
// Vibe has no usable lifecycle-hook surface for AO activity: its only hook type
// is an experimental, off-by-default POST_AGENT_TURN hook with no
// session-start/user-prompt-submit/stop/permission-request taxonomy, and it is
// not Claude-Code compatible. Hook installation and SessionInfo are therefore
// intentionally no-ops (Tier C).
//
// Restore uses `--resume <session id>` (Vibe matches by partial/short id) when
// a native session id is available in metadata.
package vibe

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hookutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const adapterID = "vibe"

// Plugin is the Mistral Vibe agent adapter. It is safe for concurrent use; the
// binary path is resolved once and cached under binaryMu.
type Plugin struct {
	binaryMu       sync.Mutex
	resolvedBinary string
}

// New returns a ready-to-register Mistral Vibe adapter.
func New() *Plugin {
	return &Plugin{}
}

var _ adapters.Adapter = (*Plugin)(nil)
var _ ports.Agent = (*Plugin)(nil)

// Manifest returns the adapter's static self-description.
func (p *Plugin) Manifest() adapters.Manifest {
	return adapters.Manifest{
		ID:          adapterID,
		Name:        "Mistral Vibe",
		Description: "Run Mistral Vibe worker sessions.",
		Version:     "0.0.1",
		Capabilities: []adapters.Capability{
			adapters.CapabilityAgent,
		},
	}
}

// GetConfigSpec reports no agent-specific config keys yet.
func (p *Plugin) GetConfigSpec(ctx context.Context) (ports.ConfigSpec, error) {
	if err := ctx.Err(); err != nil {
		return ports.ConfigSpec{}, err
	}
	return ports.ConfigSpec{}, nil
}

// GetLaunchCommand builds the argv to start a new non-interactive Vibe session:
//
//	vibe --trust --output text [--workdir <path>] [--agent <profile-or-ao-agent>] [--auto-approve] -p <prompt>
//
// The prompt is delivered through `-p` (programmatic mode), so AO uses
// in-command delivery. `--trust` skips the trust prompt for automation and
// `--output text` pins the output format. `--workdir` is passed explicitly
// because Vibe validates its own working directory in addition to the process
// cwd AO sets through the runtime. Vibe exposes no CLI system-prompt flag, so
// AO writes a workspace-local custom agent and selects it with --agent when
// standing instructions are present.
func (p *Plugin) GetLaunchCommand(ctx context.Context, cfg ports.LaunchConfig) (cmd []string, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	binary, err := p.vibeBinary(ctx)
	if err != nil {
		return nil, err
	}

	agentName, err := vibeAgentFlag(cfg.Permissions, cfg.SystemPrompt, cfg.SystemPromptFile, cfg.WorkspacePath)
	if err != nil {
		return nil, err
	}
	cmd = []string{binary, "--trust", "--output", "text"}
	appendWorkdirFlag(&cmd, cfg.WorkspacePath)
	if agentName != "" {
		cmd = append(cmd, "--agent", agentName)
		appendCustomAgentApprovalFlags(&cmd, cfg.Permissions)
	} else {
		appendAgentFlags(&cmd, cfg.Permissions)
	}
	if cfg.Prompt != "" {
		cmd = append(cmd, "-p", cfg.Prompt)
	}
	return cmd, nil
}

// GetPromptDeliveryStrategy reports that Vibe receives its prompt in the launch
// command itself.
func (p *Plugin) GetPromptDeliveryStrategy(ctx context.Context, cfg ports.LaunchConfig) (ports.PromptDeliveryStrategy, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return ports.PromptDeliveryInCommand, nil
}

// GetAgentHooks is intentionally a no-op: Vibe has no usable lifecycle-hook
// surface for AO activity reporting (Tier C).
func (p *Plugin) GetAgentHooks(ctx context.Context, cfg ports.WorkspaceHookConfig) error {
	return ctx.Err()
}

// GetRestoreCommand rebuilds the argv that continues an existing Vibe session
// when a native session id is available in metadata. Without it, ok is false
// and callers fall back to fresh launch behavior.
func (p *Plugin) GetRestoreCommand(ctx context.Context, cfg ports.RestoreConfig) (cmd []string, ok bool, err error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	agentSessionID := strings.TrimSpace(cfg.Session.Metadata[ports.MetadataKeyAgentSessionID])
	if agentSessionID == "" {
		return nil, false, nil
	}

	binary, err := p.vibeBinary(ctx)
	if err != nil {
		return nil, false, err
	}
	agentName, err := vibeAgentFlag(cfg.Permissions, cfg.SystemPrompt, cfg.SystemPromptFile, cfg.Session.WorkspacePath)
	if err != nil {
		return nil, false, err
	}
	cmd = []string{binary, "--trust", "--output", "text"}
	appendWorkdirFlag(&cmd, cfg.Session.WorkspacePath)
	if agentName != "" {
		cmd = append(cmd, "--agent", agentName)
		appendCustomAgentApprovalFlags(&cmd, cfg.Permissions)
	} else {
		appendAgentFlags(&cmd, cfg.Permissions)
	}
	cmd = append(cmd, "--resume", agentSessionID)
	return cmd, true, nil
}

// SessionInfo is intentionally a no-op until Vibe can surface native session
// metadata to AO.
func (p *Plugin) SessionInfo(ctx context.Context, session ports.SessionRef) (ports.SessionInfo, bool, error) {
	if err := ctx.Err(); err != nil {
		return ports.SessionInfo{}, false, err
	}
	return ports.SessionInfo{}, false, nil
}

func appendWorkdirFlag(cmd *[]string, workspacePath string) {
	if workspacePath != "" {
		*cmd = append(*cmd, "--workdir", workspacePath)
	}
}

// appendAgentFlags maps AO permission modes onto Vibe's builtin `--agent`
// profiles. PermissionModeDefault (and the empty mode) emit no flag so Vibe
// resolves its starting agent from the user's `default_agent` config.
func appendAgentFlags(cmd *[]string, mode ports.PermissionMode) {
	switch mode {
	case ports.PermissionModeAcceptEdits:
		*cmd = append(*cmd, "--agent", "accept-edits")
	case ports.PermissionModeAuto:
		*cmd = append(*cmd, "--agent", "auto-approve")
	case ports.PermissionModeBypassPermissions:
		*cmd = append(*cmd, "--agent", "auto-approve")
	}
}

func appendCustomAgentApprovalFlags(cmd *[]string, mode ports.PermissionMode) {
	switch mode {
	case ports.PermissionModeAuto, ports.PermissionModeBypassPermissions:
		*cmd = append(*cmd, "--auto-approve")
	}
}

const vibePromptAgentName = "ao-system-prompt"

func vibeAgentFlag(mode ports.PermissionMode, inlinePrompt, promptFile, workspacePath string) (string, error) {
	if inlinePrompt == "" && promptFile == "" {
		return "", nil
	}
	if strings.TrimSpace(workspacePath) == "" {
		return "", fmt.Errorf("vibe: workspace path required to build agent config")
	}
	promptsDir := filepath.Join(workspacePath, ".vibe", "prompts")
	agentsDir := filepath.Join(workspacePath, ".vibe", "agents")
	promptText := inlinePrompt
	if promptText == "" {
		data, err := os.ReadFile(promptFile) //nolint:gosec // path is AO-owned launch config
		if err != nil {
			return "", fmt.Errorf("vibe: read system prompt file: %w", err)
		}
		promptText = string(data)
	}
	if err := os.MkdirAll(promptsDir, 0o700); err != nil {
		return "", fmt.Errorf("vibe: create prompts dir: %w", err)
	}
	if err := os.MkdirAll(agentsDir, 0o700); err != nil {
		return "", fmt.Errorf("vibe: create agents dir: %w", err)
	}
	if err := hookutil.AtomicWriteFile(filepath.Join(promptsDir, vibePromptAgentName+".md"), []byte(strings.TrimRight(promptText, "\n")+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("vibe: write prompt: %w", err)
	}
	agentConfig := vibeAgentTOML(vibePromptAgentName, mode)
	if err := hookutil.AtomicWriteFile(filepath.Join(agentsDir, vibePromptAgentName+".toml"), []byte(agentConfig), 0o600); err != nil {
		return "", fmt.Errorf("vibe: write agent config: %w", err)
	}
	if err := hookutil.EnsureWorkspaceGitignore(promptsDir, vibePromptAgentName+".md"); err != nil {
		return "", fmt.Errorf("vibe: prompt gitignore: %w", err)
	}
	if err := hookutil.EnsureWorkspaceGitignore(agentsDir, vibePromptAgentName+".toml"); err != nil {
		return "", fmt.Errorf("vibe: agent gitignore: %w", err)
	}
	return vibePromptAgentName, nil
}

func vibeAgentTOML(agentName string, mode ports.PermissionMode) string {
	var b strings.Builder
	b.WriteString(`agent_type = "agent"` + "\n")
	b.WriteString(`display_name = "AO Session"` + "\n")
	b.WriteString(`description = "AO session standing instructions."` + "\n")
	b.WriteString(`safety = "neutral"` + "\n")
	b.WriteString("system_prompt_id = ")
	b.WriteString(strconv.Quote(agentName))
	b.WriteString("\n")
	if mode == ports.PermissionModeAcceptEdits {
		b.WriteString("\n[tools.write_file]\npermission = \"always\"\n")
		b.WriteString("\n[tools.search_replace]\npermission = \"always\"\n")
	}
	return b.String()
}

// ResolveVibeBinary finds the `vibe` binary, searching PATH then common install
// locations. It returns "vibe" as a last resort so callers get the shell's
// normal command-not-found behavior if Vibe is absent.
func ResolveVibeBinary(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	if runtime.GOOS == "windows" {
		for _, name := range []string{"vibe.exe", "vibe.cmd", "vibe"} {
			if path, err := exec.LookPath(name); err == nil && path != "" {
				return path, nil
			}
			if err := ctx.Err(); err != nil {
				return "", err
			}
		}
		candidates := []string{}
		if appData := os.Getenv("APPDATA"); appData != "" {
			candidates = append(candidates,
				filepath.Join(appData, "Python", "Scripts", "vibe.exe"),
			)
		}
		if localAppData := os.Getenv("LOCALAPPDATA"); localAppData != "" {
			candidates = append(candidates,
				filepath.Join(localAppData, "uv", "tools", "mistral-vibe", "Scripts", "vibe.exe"),
			)
		}
		for _, candidate := range candidates {
			if fileExists(candidate) {
				return candidate, nil
			}
			if err := ctx.Err(); err != nil {
				return "", err
			}
		}
		return "", fmt.Errorf("vibe: %w", ports.ErrAgentBinaryNotFound)
	}

	if path, err := exec.LookPath("vibe"); err == nil && path != "" {
		return path, nil
	}

	candidates := []string{
		"/usr/local/bin/vibe",
		"/opt/homebrew/bin/vibe",
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates,
			filepath.Join(home, ".local", "bin", "vibe"),
			filepath.Join(home, ".local", "share", "uv", "tools", "mistral-vibe", "bin", "vibe"),
		)
	}

	for _, candidate := range candidates {
		if fileExists(candidate) {
			return candidate, nil
		}
		if err := ctx.Err(); err != nil {
			return "", err
		}
	}

	return "", fmt.Errorf("vibe: %w", ports.ErrAgentBinaryNotFound)
}

func (p *Plugin) vibeBinary(ctx context.Context) (string, error) {
	p.binaryMu.Lock()
	defer p.binaryMu.Unlock()

	if p.resolvedBinary != "" {
		return p.resolvedBinary, nil
	}

	binary, err := ResolveVibeBinary(ctx)
	if err != nil {
		return "", err
	}
	p.resolvedBinary = binary
	return binary, nil
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
