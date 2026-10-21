package claudecode

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// shipLog builds a transcript as Claude Code 2.1.306 writes it; a hook-denied
// call's result is "PreToolUse:Bash hook error: " + reason with is_error set.
type shipLog struct {
	t     *testing.T
	repo  string
	lines []string
	n     int
}

func newShipLog(t *testing.T) *shipLog {
	return &shipLog{t: t, repo: t.TempDir()}
}

func (l *shipLog) add(e map[string]any) *shipLog {
	b, err := json.Marshal(e)
	if err != nil {
		l.t.Fatal(err)
	}
	l.lines = append(l.lines, string(b))
	return l
}

func (l *shipLog) toolResult(name string, input map[string]any, result string, isError bool) *shipLog {
	l.n++
	id := fmt.Sprintf("toolu_%d", l.n)
	l.add(map[string]any{"type": "assistant", "message": map[string]any{"content": []any{map[string]any{"type": "tool_use", "id": id, "name": name, "input": input}}}})
	return l.add(map[string]any{"type": "user", "message": map[string]any{"content": []any{map[string]any{"type": "tool_result", "tool_use_id": id, "content": result, "is_error": isError}}}})
}

func (l *shipLog) tool(name string, input map[string]any) *shipLog {
	return l.toolResult(name, input, "ok", false)
}

func (l *shipLog) skill(name string) *shipLog {
	return l.tool("Skill", map[string]any{"skill": "pstack:" + name})
}

func (l *shipLog) bash(cmd string) *shipLog { return l.tool("Bash", map[string]any{"command": cmd}) }

func (l *shipLog) edit(rel string) *shipLog {
	return l.tool("Edit", map[string]any{"file_path": filepath.Join(l.repo, rel), "old_string": "a", "new_string": "b"})
}

func (l *shipLog) commit() *shipLog { return l.bash("git add -A && git commit -m wip") }

func (l *shipLog) readFeature(skill string) *shipLog {
	return l.tool("Read", map[string]any{"file_path": filepath.Join(skill, "playbooks", "feature.md")})
}

func (l *shipLog) todos(status string, steps ...string) *shipLog {
	todos := make([]any, 0, len(steps))
	for _, s := range steps {
		todos = append(todos, map[string]any{"content": s, "status": status})
	}
	return l.tool("TodoWrite", map[string]any{"todos": todos})
}

func (l *shipLog) shipped(skill string) *shipLog {
	return l.readFeature(skill).todos("completed", allFeatureSteps...).
		edit("main.go").commit().
		skill("deslop").skill("no-comments").skill("unslop").commit()
}

func (l *shipLog) refusedOpen(cmd string) *shipLog {
	return l.toolResult("Bash", map[string]any{"command": cmd}, "PreToolUse:Bash hook error: "+prOpenRefusalPrefix+"Before it opens...", true)
}

const openCmd = "gh pr create --base main-fluke --title 'feat: x' --body 'Adds x - plainly.'"

func (l *shipLog) open(cmd string) (bool, string) {
	path := filepath.Join(l.t.TempDir(), "session.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(l.lines, "\n")+"\n"), 0o644); err != nil {
		l.t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]any{
		"transcript_path": path, "cwd": l.repo, "hook_event_name": "PreToolUse",
		"tool_name": "Bash", "tool_input": map[string]any{"command": cmd},
	})
	return PROpenRefusal("pre-tool-use", payload)
}

func TestPROpenRefusal(t *testing.T) {
	skill := installPoteto(t)
	cases := []struct {
		name  string
		build func(*shipLog) *shipLog
		cmd   string
		want  []string
	}{
		{"every pre-ship step done", func(l *shipLog) *shipLog { return l.shipped(skill) }, openCmd, nil},
		{"glab mr create is checked the same way", func(l *shipLog) *shipLog { return l.readFeature(skill) },
			`glab mr create --title "x" --target-branch develop --description "$(cat <<'EOF'` + "\nbody\nEOF\n)\"", []string{"deslop has not run"}},
		{"gh with a repo flag before the subcommand", func(l *shipLog) *shipLog { return l.readFeature(skill) },
			"gh -R o/r pr create --fill", []string{"deslop has not run"}},
		{"an open after cd and an env assignment", func(l *shipLog) *shipLog { return l.readFeature(skill) },
			"cd app && GH_HOST=x gh pr create --fill", []string{"deslop has not run"}},
		{"nothing done", func(l *shipLog) *shipLog { return l.edit("main.go").commit() }, openCmd,
			[]string{"deslop has not run", "no-comments has not run", "unslop (or technical-writing) has not run", "no poteto-mode playbook was read"}},
		{"deslop missing", func(l *shipLog) *shipLog {
			return l.readFeature(skill).todos("completed", allFeatureSteps...).edit("main.go").skill("no-comments").skill("unslop").commit()
		}, openCmd, []string{"deslop has not run"}},
		{"no-comments missing", func(l *shipLog) *shipLog {
			return l.readFeature(skill).todos("completed", allFeatureSteps...).edit("main.go").skill("deslop").skill("unslop").commit()
		}, openCmd, []string{"no-comments has not run"}},
		{"unslop missing", func(l *shipLog) *shipLog {
			return l.readFeature(skill).todos("completed", allFeatureSteps...).edit("main.go").skill("deslop").skill("no-comments").commit()
		}, openCmd, []string{"unslop (or technical-writing) has not run"}},
		{"technical-writing stands in for unslop", func(l *shipLog) *shipLog {
			return l.readFeature(skill).todos("completed", allFeatureSteps...).edit("main.go").skill("deslop").skill("no-comments").skill("technical-writing").commit()
		}, openCmd, nil},
		{"a comment-sicko subagent stands in for no-comments", func(l *shipLog) *shipLog {
			return l.readFeature(skill).todos("completed", allFeatureSteps...).edit("main.go").skill("deslop").
				tool("Agent", map[string]any{"subagent_type": "pstack:comment-sicko", "prompt": "review the diff"}).skill("unslop").commit()
		}, openCmd, nil},
		{"a person's slash command counts as a run", func(l *shipLog) *shipLog {
			l.readFeature(skill).todos("completed", allFeatureSteps...).edit("main.go").skill("deslop").skill("no-comments")
			return l.add(map[string]any{"type": "user", "message": map[string]any{"content": "<command-name>/pstack:unslop</command-name>"}}).commit()
		}, openCmd, nil},
		{"a subagent's skill run does not count", func(l *shipLog) *shipLog {
			l.readFeature(skill).todos("completed", allFeatureSteps...).edit("main.go").skill("no-comments").skill("unslop")
			return l.add(map[string]any{"type": "assistant", "isSidechain": true, "message": map[string]any{"content": []any{map[string]any{"type": "tool_use", "id": "x", "name": "Skill", "input": map[string]any{"skill": "pstack:deslop"}}}}}).commit()
		}, openCmd, []string{"deslop has not run"}},
		{"reviews ran before a second commit of new work", func(l *shipLog) *shipLog {
			return l.shipped(skill).edit("more.go").commit()
		}, openCmd, []string{"deslop ran before later work", "no-comments ran before later work", "unslop (or technical-writing) ran before later work"}},
		{"reviews ran, fixes committed, then an uncommitted edit", func(l *shipLog) *shipLog {
			return l.shipped(skill).edit("more.go")
		}, openCmd, []string{"deslop ran before later work"}},
		{"reviews ran after the last commit, fixes not committed yet", func(l *shipLog) *shipLog {
			return l.readFeature(skill).todos("completed", allFeatureSteps...).edit("main.go").commit().
				skill("deslop").skill("no-comments").skill("unslop")
		}, openCmd, nil},
		{"a rerun after the new work is fresh again", func(l *shipLog) *shipLog {
			return l.shipped(skill).edit("more.go").commit().skill("deslop").skill("no-comments").skill("unslop")
		}, openCmd, nil},
		{"an edit outside the repository is not new work", func(l *shipLog) *shipLog {
			return l.shipped(skill).tool("Write", map[string]any{"file_path": "/elsewhere/notes.md", "content": "x"}).bash("go test ./...")
		}, openCmd, nil},
		{"a step still open", func(l *shipLog) *shipLog {
			l.readFeature(skill).todos("completed", allFeatureSteps[:2]...)
			l.tool("TodoWrite", map[string]any{"todos": []any{
				map[string]any{"content": allFeatureSteps[0], "status": "completed"},
				map[string]any{"content": allFeatureSteps[1], "status": "completed"},
				map[string]any{"content": allFeatureSteps[2], "status": "in_progress"},
			}})
			return l.edit("main.go").skill("deslop").skill("no-comments").skill("unslop").commit()
		}, openCmd, []string{"not completed or marked `skip: <reason>`", `"write the throughput checkpoint"`}},
		{"a step quoting `skip: <reason>` in its own text is not skipped", func(l *shipLog) *shipLog {
			return l.readFeature(skill).todos("pending", "`how` over the affected subsystem. Mark `skip: <reason>` if not.").
				todos("completed", allFeatureSteps[1:]...).edit("main.go").skill("deslop").skill("no-comments").skill("unslop").commit()
		}, openCmd, []string{`"how over the affected"`}},
		{"tasks closed through TaskUpdate", func(l *shipLog) *shipLog {
			l.readFeature(skill)
			for i, s := range allFeatureSteps {
				l.toolResult("TaskCreate", map[string]any{"subject": s}, fmt.Sprintf("Task #%d created successfully: %s", i+1, s), false)
			}
			for i := range allFeatureSteps {
				l.tool("TaskUpdate", map[string]any{"taskId": fmt.Sprint(i + 1), "status": "completed"})
			}
			return l.edit("main.go").skill("deslop").skill("no-comments").skill("unslop").commit()
		}, openCmd, nil},
		{"em dash in the inline body", func(l *shipLog) *shipLog { return l.shipped(skill) },
			"gh pr create --title 'feat: x' --body 'Adds x — plainly.'", []string{"em dash"}},
		{"em dash in the title", func(l *shipLog) *shipLog { return l.shipped(skill) },
			"gh pr create --title 'feat: x — y' --body 'ok'", []string{"em dash"}},
		{"an en dash is allowed", func(l *shipLog) *shipLog { return l.shipped(skill) },
			"gh pr create --title 'feat: x' --body 'pages 1–2'", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			refuse, reason := tc.build(newShipLog(t)).open(tc.cmd)
			if refuse != (len(tc.want) > 0) {
				t.Fatalf("refuse = %v, want %v (reason %q)", refuse, len(tc.want) > 0, reason)
			}
			for _, w := range tc.want {
				if !strings.Contains(reason, w) {
					t.Errorf("reason %q lacks %q", reason, w)
				}
			}
			if refuse && !strings.HasPrefix(reason, prOpenRefusalPrefix) {
				t.Errorf("reason %q must open with %q so later refusals are counted", reason, prOpenRefusalPrefix)
			}
		})
	}
}

func TestPROpenRefusal_TodoMdOnDiskIsTheTodolist(t *testing.T) {
	skill := installPoteto(t)
	l := newShipLog(t)
	todo := filepath.Join(t.TempDir(), "todo.md")
	write := func(body string) {
		if err := os.WriteFile(todo, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("- [ ] 1. `how` over the affected subsystem.\n- [ ] 2. `architect` for parallel design exploration.\n- [ ] 3. Write the throughput checkpoint as four todo items.\n")
	l.readFeature(skill).tool("Write", map[string]any{"file_path": todo, "content": "..."}).
		edit("main.go").skill("deslop").skill("no-comments").skill("unslop").commit()
	if refuse, reason := l.open(openCmd); !refuse || !strings.Contains(reason, `"how over the affected"`) {
		t.Fatalf("open steps in todo.md: refuse=%v reason=%q", refuse, reason)
	}
	write("- [x] 1. `how` over the affected subsystem.\n- [ ] 2. `architect` for parallel design exploration. skip: not requested\n- [x] 3. Write the throughput checkpoint as four todo items.\n")
	l.bash("sed -i '' 's/- \\[ \\] 1/- [x] 1/' " + todo)
	if refuse, reason := l.open(openCmd); refuse {
		t.Fatalf("closed todo.md still refused: %q", reason)
	}
}

func TestPROpenRefusal_BodyFileWithEmDash(t *testing.T) {
	skill := installPoteto(t)
	l := newShipLog(t).shipped(skill)
	body := filepath.Join(l.repo, "body.md")
	if err := os.WriteFile(body, []byte("## Why\nBecause — reasons.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if refuse, reason := l.open("gh pr create --title 'feat: x' --body-file body.md"); !refuse || !strings.Contains(reason, "em dash") {
		t.Fatalf("relative --body-file with an em dash: refuse=%v reason=%q", refuse, reason)
	}
	if refuse, reason := l.open("gh pr create --title 'feat: x' -F " + body); !refuse || !strings.Contains(reason, "em dash") {
		t.Fatalf("absolute -F with an em dash: refuse=%v reason=%q", refuse, reason)
	}
}

func TestPROpenRefusal_CappedAtThreeRefusals(t *testing.T) {
	installPoteto(t)
	l := newShipLog(t)
	for i := range PROpenMaxRefusals {
		if refuse, _ := l.open(openCmd); !refuse {
			t.Fatalf("attempt %d went through, want refused", i+1)
		}
		l.refusedOpen(openCmd)
	}
	if refuse, reason := l.open(openCmd); refuse {
		t.Fatalf("attempt %d refused, want it through: %q", PROpenMaxRefusals+1, reason)
	}
}

func TestPROpenRefusal_OnlyAPROpenIsChecked(t *testing.T) {
	installPoteto(t)
	l := newShipLog(t)
	for _, cmd := range []string{"gh pr view 12", "gh pr list", "glab mr view 3", "git push -u origin HEAD", "echo gh pr create later", "grep -n 'glab mr create' ~/.claude/commands/open-mr.md"} {
		if refuse, reason := l.open(cmd); refuse {
			t.Errorf("%q refused: %q", cmd, reason)
		}
	}
	payload, _ := json.Marshal(map[string]any{"transcript_path": "/x", "tool_name": "Bash", "tool_input": map[string]any{"command": openCmd}})
	if refuse, _ := PROpenRefusal("post-tool-use", payload); refuse {
		t.Error("only pre-tool-use can refuse")
	}
	if refuse, _ := PROpenRefusal("pre-tool-use", payload); refuse {
		t.Error("an unreadable transcript must let the open through")
	}
}
