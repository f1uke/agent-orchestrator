package sessionmanager

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// A project's Testiny conventions skill reaches the prompt only while its
// folder holds a SKILL.md: a prompt that sends an agent to a skill that is not
// there leaves it with neither the skill nor the fallback of asking the human.
// The case language follows the project's human-facing language.
func TestBuildSystemPrompt_TestinySkillOnlyWhenItExists(t *testing.T) {
	present := t.TempDir()
	if err := os.WriteFile(filepath.Join(present, "SKILL.md"), []byte("---\nname: testiny\n---\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	missing := t.TempDir()
	for name, tc := range map[string]struct {
		skill string
		want  string
	}{
		"present": {present, "Follow the skill at `" + present + "/SKILL.md`"},
		"missing": {missing, "names no skill for its Testiny conventions"},
		"unset":   {"", "names no skill for its Testiny conventions"},
	} {
		t.Run(name, func(t *testing.T) {
			st := crewPromptStore(t)
			st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: domain.ProjectConfig{UsesTestiny: true, TestinySkill: tc.skill, ResponseLanguage: "Thai"}}
			got, err := layeredManager(st, nil).buildSystemPrompt(ctx, systemPromptSpec{Kind: domain.KindWorker, ProjectID: "mer", CrewRole: domain.CrewRoleQA})
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{tc.want, "in plain Thai"} {
				if !strings.Contains(got, want) {
					t.Fatalf("qa prompt missing %q:\n%s", want, got)
				}
			}
		})
	}
}

func TestTestinySkillDir_ExpandsHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, "skills", "testiny")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := testinySkillDir("~/skills/testiny"); got != dir {
		t.Fatalf("testinySkillDir(~/skills/testiny) = %q, want %q", got, dir)
	}
}
