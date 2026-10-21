package claudecode

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var _ ports.SkillInvoker = (*Plugin)(nil)

// SkillCommand returns the slash command that loads skill, or ok=false when the
// skill is not installed for this user. A turn that opens with the command makes
// Claude Code load the skill and hand it the rest of the turn as its arguments.
//
// A plugin skill ("plugin:skill") is installed when installed_plugins.json lists
// the plugin, its install path holds skills/<skill>/SKILL.md, and settings.json
// does not disable it. A bare name is a user skill under skills/<name>/.
func (p *Plugin) SkillCommand(_ context.Context, skill string) (string, bool) {
	dir, err := claudeConfigDir()
	if err != nil || !skillInstalled(dir, skill) {
		return "", false
	}
	return "/" + skill, true
}

func skillInstalled(configDir, skill string) bool {
	plugin, name, isPlugin := strings.Cut(skill, ":")
	if !isPlugin {
		return fileExists(filepath.Join(configDir, "skills", skill, "SKILL.md"))
	}
	_, ok := pluginSkillDir(configDir, plugin+":"+name)
	return ok
}

// pluginSkillDir is the directory of an installed, enabled plugin skill
// ("plugin:skill"), the one holding its SKILL.md.
func pluginSkillDir(configDir, skill string) (string, bool) {
	plugin, name, _ := strings.Cut(skill, ":")
	if plugin == "" || name == "" {
		return "", false
	}
	var manifest struct {
		Plugins map[string][]struct {
			InstallPath string `json:"installPath"`
		} `json:"plugins"`
	}
	if !decodeJSONFile(filepath.Join(configDir, "plugins", "installed_plugins.json"), &manifest) {
		return "", false
	}
	var settings struct {
		EnabledPlugins map[string]bool `json:"enabledPlugins"`
	}
	decodeJSONFile(filepath.Join(configDir, "settings.json"), &settings)
	for key, installs := range manifest.Plugins {
		if pluginName, _, _ := strings.Cut(key, "@"); pluginName != plugin {
			continue
		}
		if enabled, set := settings.EnabledPlugins[key]; set && !enabled {
			continue
		}
		for _, i := range installs {
			dir := filepath.Join(i.InstallPath, "skills", name)
			if i.InstallPath != "" && fileExists(filepath.Join(dir, "SKILL.md")) {
				return dir, true
			}
		}
	}
	return "", false
}

// claudeConfigDir is Claude Code's config directory: CLAUDE_CONFIG_DIR, else
// ~/.claude.
func claudeConfigDir() (string, error) {
	if dir := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); dir != "" {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude"), nil
}

func decodeJSONFile(path string, v any) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return json.Unmarshal(b, v) == nil
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
