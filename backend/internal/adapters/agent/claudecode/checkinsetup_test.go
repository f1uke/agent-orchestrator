package claudecode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const featurePlaybook = `# Feature

1. ` + "`how`" + ` over the affected subsystem.
2. ` + "`architect`" + ` for parallel design exploration, only when asked.
3. Write the throughput checkpoint as four todo items.
`

// installPoteto fakes a Claude Code config dir with pstack's poteto-mode
// installed and its feature playbook, and points CLAUDE_CONFIG_DIR at it.
func installPoteto(t *testing.T) string {
	t.Helper()
	config := t.TempDir()
	skill := filepath.Join(config, "plugins", "cache", "pstack", "skills", "poteto-mode")
	if err := os.MkdirAll(filepath.Join(skill, "playbooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"SKILL.md": "# poteto", "playbooks/feature.md": featurePlaybook} {
		if err := os.WriteFile(filepath.Join(skill, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	manifest := `{"plugins":{"pstack@pstack-claude":[{"installPath":"` + filepath.Join(config, "plugins", "cache", "pstack") + `"}]}}`
	if err := os.WriteFile(filepath.Join(config, "plugins", "installed_plugins.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", config)
	return skill
}

// transcript writes a session .jsonl the way Claude Code 2.1.306 does: the
// preloaded command as the first prompt, the skill body as a meta entry, tool
// calls as assistant blocks, results as user block lists.
type setupLog struct {
	t     *testing.T
	lines []string
}

func newTranscript(t *testing.T) *setupLog {
	tr := &setupLog{t: t}
	tr.add(map[string]any{"type": "user", "message": map[string]any{"content": "<command-name>/pstack:poteto-mode</command-name><command-args>add --by project</command-args>"}})
	tr.add(map[string]any{"type": "user", "isMeta": true, "message": map[string]any{"content": []any{map[string]any{"type": "text", "text": "Base directory for this skill: ..."}}}})
	return tr
}

func (tr *setupLog) add(e map[string]any) *setupLog {
	b, err := json.Marshal(e)
	if err != nil {
		tr.t.Fatal(err)
	}
	tr.lines = append(tr.lines, string(b))
	return tr
}

func (tr *setupLog) tool(name string, input map[string]any) *setupLog {
	tr.add(map[string]any{"type": "assistant", "message": map[string]any{"content": []any{map[string]any{"type": "tool_use", "name": name, "input": input}}}})
	return tr.add(map[string]any{"type": "user", "message": map[string]any{"content": []any{map[string]any{"type": "tool_result", "content": "ok"}}}})
}

func (tr *setupLog) subagentTool(name string, input map[string]any) *setupLog {
	return tr.add(map[string]any{"type": "assistant", "isSidechain": true, "message": map[string]any{"content": []any{map[string]any{"type": "tool_use", "name": name, "input": input}}}})
}

func (tr *setupLog) refused(reason string) *setupLog {
	return tr.add(map[string]any{"type": "user", "isMeta": true, "message": map[string]any{"content": stopFeedbackPrefix + reason}})
}

func (tr *setupLog) prompt(text string) *setupLog {
	return tr.add(map[string]any{"type": "user", "message": map[string]any{"content": text}})
}

func (tr *setupLog) readPlaybook(skill string) *setupLog {
	return tr.tool("Read", map[string]any{"file_path": filepath.Join(skill, "playbooks", "feature.md")})
}

func (tr *setupLog) todos(steps ...string) *setupLog {
	var todos []any
	for _, s := range steps {
		todos = append(todos, map[string]any{"content": s, "status": "pending"})
	}
	return tr.tool("TodoWrite", map[string]any{"todos": todos})
}

var allFeatureSteps = []string{
	"`how` over the affected subsystem.",
	"`architect` for parallel design exploration. skip: not requested",
	"Write the throughput checkpoint as four todo items.",
}

func (tr *setupLog) stop() (bool, string) {
	path := filepath.Join(tr.t.TempDir(), "session.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(tr.lines, "\n")+"\n"), 0o644); err != nil {
		tr.t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]any{"session_id": "s", "transcript_path": path, "stop_hook_active": false})
	return CheckInSetupRefusal("stop", payload)
}

func TestCheckInSetupRefusal(t *testing.T) {
	skill := installPoteto(t)
	grep := func(s string) map[string]any { return map[string]any{"command": "grep -rn parse " + s} }
	cases := []struct {
		name       string
		build      func(*setupLog) *setupLog
		wantRefuse bool
		wantReason string
	}{
		{"set up with TodoWrite", func(tr *setupLog) *setupLog {
			return tr.readPlaybook(skill).todos(allFeatureSteps...).tool("Bash", grep("src"))
		}, false, ""},
		{"set up with TaskCreate, one task per step", func(tr *setupLog) *setupLog {
			tr.readPlaybook(skill)
			for _, s := range allFeatureSteps {
				tr.tool("TaskCreate", map[string]any{"subject": s, "description": "playbook step"})
			}
			return tr
		}, false, ""},
		{"set up with a todo.md in the knowledge store", func(tr *setupLog) *setupLog {
			return tr.tool("Bash", map[string]any{"command": "cat " + skill + "/playbooks/feature.md"}).
				tool("Write", map[string]any{"file_path": "/h/.ao/knowledge/p/plans/x--todo.md", "content": "- [ ] " + strings.Join(allFeatureSteps, "\n- [ ] ")})
		}, false, ""},
		{"set up with a todo.md generated from the playbook file", func(tr *setupLog) *setupLog {
			return tr.readPlaybook(skill).tool("Bash", map[string]any{"command": "grep -E '^[0-9]+\\.' " + skill + "/playbooks/feature.md > /h/.ao/knowledge/p/todo.md"})
		}, false, ""},
		{"nothing set up", func(tr *setupLog) *setupLog {
			return tr.tool("Bash", grep("src")).tool("Read", map[string]any{"file_path": "/repo/src/report.js"})
		}, true, "no playbook file has been read and there is no todolist"},
		{"todolist but no playbook read", func(tr *setupLog) *setupLog {
			return tr.todos(allFeatureSteps...)
		}, true, "no playbook file has been read"},
		{"playbook read but no todolist", func(tr *setupLog) *setupLog {
			return tr.readPlaybook(skill).tool("Bash", grep("src"))
		}, true, "you read playbooks/feature.md but there is no todolist"},
		{"todolist summarises the playbook instead of copying it", func(tr *setupLog) *setupLog {
			return tr.readPlaybook(skill).todos("Implement --by project", "Open the PR")
		}, true, `missing steps start with "how over the affected"`},
		{"todolist drops one step", func(tr *setupLog) *setupLog {
			return tr.readPlaybook(skill).todos(allFeatureSteps[0], allFeatureSteps[2])
		}, true, `"architect for parallel design"`},
		{"a subagent's set-up is not the worker's", func(tr *setupLog) *setupLog {
			return tr.subagentTool("Read", map[string]any{"file_path": skill + "/playbooks/feature.md"}).
				subagentTool("TodoWrite", map[string]any{"todos": []any{map[string]any{"content": strings.Join(allFeatureSteps, " ")}}})
		}, true, "no playbook file has been read"},
		{"refused once, then set up", func(tr *setupLog) *setupLog {
			return tr.refused(checkInSetupRefusalPrefix + "x").readPlaybook(skill).todos(allFeatureSteps...)
		}, false, ""},
		{"refused twice, still nothing: refused again", func(tr *setupLog) *setupLog {
			return tr.refused(checkInSetupRefusalPrefix + "x").refused(checkInSetupRefusalPrefix + "x")
		}, true, checkInSetupRefusalPrefix},
		{"refused three times: let through", func(tr *setupLog) *setupLog {
			return tr.refused(checkInSetupRefusalPrefix + "x").refused(checkInSetupRefusalPrefix + "x").refused(checkInSetupRefusalPrefix + "x")
		}, false, ""},
		{"another hook's refusal does not count toward the cap", func(tr *setupLog) *setupLog {
			return tr.refused("AO: commit first").refused("AO: commit first").refused("AO: commit first")
		}, true, checkInSetupRefusalPrefix},
		{"past the check-in: a person replied", func(tr *setupLog) *setupLog {
			return tr.prompt("go ahead").tool("Bash", grep("src"))
		}, false, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			refuse, reason := c.build(newTranscript(t)).stop()
			if refuse != c.wantRefuse || !strings.Contains(reason, c.wantReason) {
				t.Fatalf("CheckInSetupRefusal = (%v, %q), want (%v, containing %q)", refuse, reason, c.wantRefuse, c.wantReason)
			}
			if refuse && !strings.HasPrefix(reason, checkInSetupRefusalPrefix) {
				t.Fatalf("a refusal must start with %q so the cap can count it: %q", checkInSetupRefusalPrefix, reason)
			}
		})
	}
}

func TestCheckInSetupRefusal_OnlyAReadableStop(t *testing.T) {
	installPoteto(t)
	if refuse, _ := CheckInSetupRefusal("pre-tool-use", []byte(`{"transcript_path":"/x"}`)); refuse {
		t.Fatal("only a Stop can be refused")
	}
	if refuse, _ := CheckInSetupRefusal("stop", []byte(`{"transcript_path":"/no/such/file.jsonl"}`)); refuse {
		t.Fatal("an unreadable transcript must let the turn end")
	}
	if refuse, _ := CheckInSetupRefusal("stop", []byte(`not json`)); refuse {
		t.Fatal("a malformed payload must let the turn end")
	}
}
