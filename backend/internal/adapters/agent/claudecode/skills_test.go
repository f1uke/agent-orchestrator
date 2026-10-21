package claudecode

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// installPstack lays out a config dir the way Claude Code leaves it after
// `/plugin install pstack@pstack-claude`, and returns the install path.
func installPstack(t *testing.T, dir string) string {
	t.Helper()
	install := filepath.Join(dir, "plugins", "cache", "pstack-claude", "pstack", "1.0.0")
	writeFile(t, filepath.Join(install, "skills", "poteto-mode", "SKILL.md"), "---\nname: poteto-mode\n---\n")
	writeFile(t, filepath.Join(dir, "plugins", "installed_plugins.json"),
		`{"version":2,"plugins":{"pstack@pstack-claude":[{"scope":"user","installPath":"`+install+`"}]}}`)
	return install
}

func TestSkillCommand(t *testing.T) {
	for _, tc := range []struct {
		name   string
		setup  func(t *testing.T, dir string)
		skill  string
		wantOK bool
	}{
		{"installed plugin skill", func(t *testing.T, dir string) { installPstack(t, dir) }, "pstack:poteto-mode", true},
		{"enabled in settings", func(t *testing.T, dir string) {
			installPstack(t, dir)
			writeFile(t, filepath.Join(dir, "settings.json"), `{"enabledPlugins":{"pstack@pstack-claude":true}}`)
		}, "pstack:poteto-mode", true},
		{"plugin disabled in settings", func(t *testing.T, dir string) {
			installPstack(t, dir)
			writeFile(t, filepath.Join(dir, "settings.json"), `{"enabledPlugins":{"pstack@pstack-claude":false}}`)
		}, "pstack:poteto-mode", false},
		{"plugin lacks that skill", func(t *testing.T, dir string) { installPstack(t, dir) }, "pstack:no-such-skill", false},
		{"install path gone", func(t *testing.T, dir string) {
			if err := os.RemoveAll(installPstack(t, dir)); err != nil {
				t.Fatal(err)
			}
		}, "pstack:poteto-mode", false},
		{"no plugins installed", func(*testing.T, string) {}, "pstack:poteto-mode", false},
		{"user skill", func(t *testing.T, dir string) {
			writeFile(t, filepath.Join(dir, "skills", "mine", "SKILL.md"), "---\nname: mine\n---\n")
		}, "mine", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("CLAUDE_CONFIG_DIR", dir)
			tc.setup(t, dir)
			cmd, ok := New().SkillCommand(context.Background(), tc.skill)
			if ok != tc.wantOK {
				t.Fatalf("SkillCommand(%q) ok = %v, want %v", tc.skill, ok, tc.wantOK)
			}
			if ok && cmd != "/"+tc.skill {
				t.Fatalf("SkillCommand(%q) = %q", tc.skill, cmd)
			}
		})
	}
}
