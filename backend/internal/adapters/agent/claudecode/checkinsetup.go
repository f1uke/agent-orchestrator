package claudecode

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// CheckInSetupMaxRefusals bounds how often one check-in is refused. A worker
// that still has no set-up after that many refusals is let through, so the
// check can never trap a worker in a turn it cannot end.
const CheckInSetupMaxRefusals = 3

// checkInSetupRefusalPrefix opens every refusal reason. Claude Code records a
// blocked Stop in the transcript as a meta user entry "Stop hook feedback:\n" +
// reason (measured on 2.1.306), which is how earlier refusals are counted.
const checkInSetupRefusalPrefix = "AO refused this check-in: "

const stopFeedbackPrefix = "Stop hook feedback:\n"

// CheckInSetupRefusal decides whether the Stop that would end a gated dev's
// first turn (its check-in) must be refused, and the reason handed back to the
// agent. The turn may end once the session's own transcript shows a pstack
// playbook file read and a todolist that holds every step of a playbook it
// read.
//
// Only the first turn is checked: once a person has replied, the check-in is
// over. The caller decides which sessions are checked at all; this reads only
// the hook payload and the transcript it names, and answers false whenever the
// transcript cannot be read.
func CheckInSetupRefusal(event string, payload []byte) (bool, string) {
	if event != "stop" {
		return false, ""
	}
	var p struct {
		TranscriptPath string `json:"transcript_path"`
	}
	if json.Unmarshal(payload, &p) != nil || p.TranscriptPath == "" {
		return false, ""
	}
	t, err := readSetupTranscript(p.TranscriptPath)
	if err != nil || t.prompts != 1 || t.refusals >= CheckInSetupMaxRefusals {
		return false, ""
	}
	missing := missingSetup(t.calls, playbookStepsFromInstall)
	if missing == "" {
		return false, ""
	}
	return true, checkInSetupRefusalPrefix + missing + " Match the task to the playbook your loaded skill names, open that playbook's file, copy its steps into your todolist verbatim (a step you will not do stays with `skip: <reason>`), then post the check-in again. A todolist is not a forbidden edit; a todo.md goes outside the repository. Do not edit repository files yet."
}

type toolCall struct {
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

type setupTranscript struct {
	prompts  int
	refusals int
	calls    []toolCall
}

// readSetupTranscript walks the main thread of a Claude Code session .jsonl:
// how many prompts a person (or AO) submitted, how many of AO's refusals the
// agent was given, and every tool call the agent made.
func readSetupTranscript(path string) (setupTranscript, error) {
	f, err := os.Open(path) //nolint:gosec // the path comes from Claude Code's own hook envelope
	if err != nil {
		return setupTranscript{}, err
	}
	defer func() { _ = f.Close() }()
	var t setupTranscript
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 64<<20)
	for sc.Scan() {
		var e struct {
			Type        string `json:"type"`
			IsMeta      bool   `json:"isMeta"`
			IsSidechain bool   `json:"isSidechain"`
			Message     struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(sc.Bytes(), &e) != nil || e.IsSidechain {
			continue
		}
		switch e.Type {
		case "user":
			var text string
			if json.Unmarshal(e.Message.Content, &text) != nil {
				// A block list: tool results, or a prompt's text blocks.
				var blocks []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				}
				_ = json.Unmarshal(e.Message.Content, &blocks)
				for _, b := range blocks {
					if b.Type == "text" {
						text += b.Text
					}
				}
				if text == "" {
					continue
				}
			}
			switch {
			case strings.HasPrefix(text, stopFeedbackPrefix+checkInSetupRefusalPrefix):
				t.refusals++
			case !e.IsMeta && !strings.HasPrefix(text, stopFeedbackPrefix):
				t.prompts++
				if t.prompts > 1 {
					// Past the check-in: the rest of a long session is not read.
					return t, nil
				}
			}
		case "assistant":
			var blocks []struct {
				Type string `json:"type"`
				toolCall
			}
			_ = json.Unmarshal(e.Message.Content, &blocks)
			for _, b := range blocks {
				if b.Type == "tool_use" {
					t.calls = append(t.calls, b.toolCall)
				}
			}
		}
	}
	return t, sc.Err()
}

var (
	playbookPathRE = regexp.MustCompile(`poteto-mode/playbooks/([a-z-]+)\.md`)
	shellReadRE    = regexp.MustCompile(`\b(cat|sed|head|less|bat|awk|grep)\b`)
	shellWriteRE   = regexp.MustCompile(`<<|>|\btee\b`)
	todoFileRE     = regexp.MustCompile(`(?i)todo\.md`)
	stepLineRE     = regexp.MustCompile(`(?m)^\d+\.\s+(.+)$`)
	nonWordRE      = regexp.MustCompile(`[^a-z0-9]+`)
)

func (c toolCall) str(key string) string {
	var m map[string]any
	_ = json.Unmarshal(c.Input, &m)
	s, _ := m[key].(string)
	return s
}

// playbookRead names the playbook a call opens, or "".
func playbookRead(c toolCall) string {
	var target string
	switch c.Name {
	case "Read":
		target = c.str("file_path")
	case "Bash":
		target = c.str("command")
		if !shellReadRE.MatchString(target) {
			return ""
		}
	default:
		return ""
	}
	if m := playbookPathRE.FindStringSubmatch(target); m != nil {
		return m[1]
	}
	return ""
}

// todoText is the text a call adds to a todolist; ok=false when the call is not
// a todolist write. A shell write that extracts the steps from a playbook file
// adds that playbook's steps.
func todoText(c toolCall, steps func(string) []string) (string, bool) {
	switch c.Name {
	case "TaskCreate":
		return c.str("subject") + "\n" + c.str("description"), true
	case "TodoWrite":
		var in struct {
			Todos []struct {
				Content string `json:"content"`
			} `json:"todos"`
		}
		_ = json.Unmarshal(c.Input, &in)
		var parts []string
		for _, t := range in.Todos {
			parts = append(parts, t.Content)
		}
		return strings.Join(parts, "\n"), true
	case "Write", "Edit":
		if todoFileRE.MatchString(filepath.Base(c.str("file_path"))) {
			return c.str("content") + c.str("new_string"), true
		}
	case "Bash":
		cmd := c.str("command")
		if !todoFileRE.MatchString(cmd) || !shellWriteRE.MatchString(cmd) {
			return "", false
		}
		if m := playbookPathRE.FindStringSubmatch(cmd); m != nil && shellReadRE.MatchString(cmd) {
			return cmd + "\n" + strings.Join(steps(m[1]), "\n"), true
		}
		return cmd, true
	}
	return "", false
}

// missingSetup says what the turn still lacks, or "" when a playbook was read
// and a todolist holds every step of one the agent read. A step counts as
// copied when its first four words appear in the todolist, as the eval's
// grader (scripts/playbook-adherence) counts it. A playbook whose steps cannot
// be found is satisfied by any todolist.
func missingSetup(calls []toolCall, steps func(string) []string) string {
	var read []string
	var todo strings.Builder
	hasTodo := false
	for _, c := range calls {
		if pb := playbookRead(c); pb != "" {
			read = append(read, pb)
		}
		if text, ok := todoText(c, steps); ok {
			hasTodo = true
			todo.WriteString(" " + normalizeWords(text) + " ")
		}
	}
	switch {
	case len(read) == 0 && !hasTodo:
		return "no playbook file has been read and there is no todolist yet."
	case len(read) == 0:
		return "no playbook file has been read yet."
	case !hasTodo:
		return fmt.Sprintf("you read playbooks/%s.md but there is no todolist holding its steps yet.", read[len(read)-1])
	}
	var best string
	var bestMissing []string
	for _, pb := range read {
		var missing []string
		for _, s := range steps(pb) {
			if !strings.Contains(todo.String(), " "+s+" ") {
				missing = append(missing, s)
			}
		}
		if len(missing) == 0 {
			return ""
		}
		if best == "" || len(missing) < len(bestMissing) {
			best, bestMissing = pb, missing
		}
	}
	return fmt.Sprintf(`your todolist does not hold every step of playbooks/%s.md verbatim; missing steps start with "%s".`, best, strings.Join(bestMissing, `", "`))
}

func normalizeWords(s string) string {
	return strings.TrimSpace(nonWordRE.ReplaceAllString(strings.ToLower(s), " "))
}

// stepPrefixes are the first four normalized words of each numbered step.
func stepPrefixes(playbook string) []string {
	var out []string
	for _, m := range stepLineRE.FindAllStringSubmatch(playbook, -1) {
		words := strings.Fields(normalizeWords(m[1]))
		if len(words) > 4 {
			words = words[:4]
		}
		out = append(out, strings.Join(words, " "))
	}
	return out
}

// playbookStepsFromInstall reads a playbook from the installed pstack plugin.
func playbookStepsFromInstall(name string) []string {
	dir, err := claudeConfigDir()
	if err != nil {
		return nil
	}
	root, ok := pluginSkillDir(dir, "pstack:poteto-mode")
	if !ok {
		return nil
	}
	b, err := os.ReadFile(filepath.Join(root, "playbooks", name+".md")) //nolint:gosec // name matches [a-z-]+
	if err != nil {
		return nil
	}
	return stepPrefixes(string(b))
}
