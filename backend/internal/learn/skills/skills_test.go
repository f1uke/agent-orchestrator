package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheck(t *testing.T) {
	ok := "---\nname: verify-ui\ndescription: Verify a UI change. Use when a screen changed.\n---\n\nRun the script.\n"
	if fm, err := Check(ok); err != nil || fm.Name != "verify-ui" {
		t.Fatalf("valid skill: %+v %v", fm, err)
	}
	for name, bad := range map[string]string{
		"no frontmatter":   "name: x\n",
		"unclosed":         "---\nname: x\n",
		"bad yaml":         "---\nname: [x\n---\nbody\n",
		"bad name":         "---\nname: Verify UI\ndescription: Use when x.\n---\nbody\n",
		"no trigger":       "---\nname: x\ndescription: Does things.\n---\nbody\n",
		"long description": "---\nname: x\ndescription: Use when " + strings.Repeat("a", 300) + "\n---\nbody\n",
		"empty body":       "---\nname: x\ndescription: Use when x.\n---\n\n",
		"huge body":        "---\nname: x\ndescription: Use when x.\n---\n" + strings.Repeat("a", MaxBodyBytes+1),
	} {
		if _, err := Check(bad); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestIndex_EverySource(t *testing.T) {
	home, data, repo := t.TempDir(), t.TempDir(), t.TempDir()
	write := func(p, s string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	skill := func(name string) string {
		return "---\nname: " + name + "\ndescription: Use when " + name + ".\n---\nbody\n"
	}
	write(filepath.Join(home, ".claude", "skills", "release", "SKILL.md"), skill("release"))
	write(filepath.Join(home, ".claude", "skills", "broken", "SKILL.md"), "no frontmatter")
	plugin := filepath.Join(home, ".claude", "plugins", "cache", "m", "figma", "1.0")
	write(filepath.Join(plugin, "skills", "figma-use", "SKILL.md"), skill("figma-use"))
	write(filepath.Join(home, ".claude", "plugins", "cache", "m", "figma", "0.9", "skills", "old", "SKILL.md"), skill("old"))
	write(filepath.Join(home, ".claude", "plugins", "installed_plugins.json"), `{"version":2,"plugins":{"figma@m":[{"installPath":"`+plugin+`"}]}}`)
	write(filepath.Join(data, "skills", "using-ao", "SKILL.md"), skill("using-ao"))
	learned := filepath.Join(home, ".ao", "learned")
	write(filepath.Join(learned, "global", "skills", "g", "SKILL.md"), skill("g"))
	write(filepath.Join(learned, "projects", "nter", "skills", "p", "SKILL.md"), skill("p"))
	write(filepath.Join(repo, ".claude", "skills", "team", "SKILL.md"), skill("team"))

	var got []string
	for _, s := range Index(Dirs{Home: home, DataDir: data, Learned: learned, Repos: map[string]string{"nter": repo}}) {
		got = append(got, string(s.Source)+":"+s.Scope+":"+s.Name)
	}
	want := "user:global:broken user:global:release plugin:global:figma-use ao:global:using-ao learned:global:g learned:project:nter:p repo:project:nter:team"
	if strings.Join(got, " ") != want {
		t.Errorf("index =\n%s\nwant\n%s", strings.Join(got, " "), want)
	}
}
