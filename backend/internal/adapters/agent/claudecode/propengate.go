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

// PROpenMaxRefusals bounds how often a session's PR or MR open is refused. The
// next attempt goes through, so the check can never keep a worker from
// shipping.
const PROpenMaxRefusals = 3

// prOpenRefusalPrefix opens every refusal reason. Earlier refusals are counted
// from the transcript by finding it in a PR-open call's tool result.
const prOpenRefusalPrefix = "AO refused this PR/MR open. "

var (
	prOpenRE    = regexp.MustCompile(`(?m)(?:^|[;&|(])\s*(?:\w+=\S*\s+)*(?:gh|glab)\s[^|;&\n]*?\b(?:pr|mr)\s+create\b`)
	bodyFileRE  = regexp.MustCompile(`(?:--body-file|-F)[=\s]+("[^"]+"|'[^']+'|\S+)`)
	gitCommitRE = regexp.MustCompile(`\bgit\s+(-C\s+\S+\s+)?commit\b`)
	commandRE   = regexp.MustCompile(`<command-name>/(?:pstack:)?([a-z-]+)</command-name>`)
	todoPathRE  = regexp.MustCompile(`[^\s'"<>|;&()]*todo\.md`)
	taskIDRE    = regexp.MustCompile(`Task #(\d+)`)
	checkedRE   = regexp.MustCompile(`\[[xX]\]`)
	skippedRE   = regexp.MustCompile(`(?i)\b(skip|n/a):`)
	codeSpanRE  = regexp.MustCompile("`[^`]*`")
	listItemRE  = regexp.MustCompile(`^\s*([-*]|\d+\.)\s`)
)

// preShipSkills are the reviews poteto-mode runs before a PR opens, each with
// the runs that count for it.
var preShipSkills = []struct {
	name string
	runs func(skill, subagentType string) bool
}{
	{"deslop", func(s, _ string) bool { return s == "deslop" }},
	{"no-comments", func(s, a string) bool { return s == "no-comments" || strings.HasSuffix(a, "comment-sicko") }},
	{"unslop (or technical-writing)", func(s, _ string) bool { return s == "unslop" || s == "technical-writing" }},
}

// PROpenRefusal decides whether a Bash call that opens a GitHub PR or a GitLab
// MR must be refused, and the reason handed back to the agent. The open may go
// ahead once the session's own transcript shows deslop, no-comments and unslop
// run over the work that ships, every step of the playbook it read closed in
// its todolist, and the PR body carries no em dash.
func PROpenRefusal(event string, payload []byte) (bool, string) {
	if event != "pre-tool-use" {
		return false, ""
	}
	var p struct {
		TranscriptPath string `json:"transcript_path"`
		Cwd            string `json:"cwd"`
		ToolName       string `json:"tool_name"`
		ToolInput      struct {
			Command string `json:"command"`
		} `json:"tool_input"`
	}
	if json.Unmarshal(payload, &p) != nil || p.ToolName != "Bash" || !prOpenRE.MatchString(p.ToolInput.Command) || p.TranscriptPath == "" {
		return false, ""
	}
	t, err := readShipTranscript(p.TranscriptPath)
	if err != nil || t.refusals() >= PROpenMaxRefusals {
		return false, ""
	}
	missing := staleReviews(t.events, p.Cwd)
	if step := openSteps(t, p.Cwd, playbookStepsFromInstall); step != "" {
		missing = append(missing, step)
	}
	if emDashInBody(p.ToolInput.Command, p.Cwd) {
		missing = append(missing, "the PR/MR title or body has an em dash (U+2014); write a plain dash or rephrase.")
	}
	if len(missing) == 0 {
		return false, ""
	}
	return true, prOpenRefusalPrefix + "Before it opens, poteto-mode still needs:\n- " + strings.Join(missing, "\n- ") +
		"\nDo what is missing, commit any fixes, then run the open again. Run each fix as its own command: a fix chained with the open is refused with it and never runs."
}

// shipEvent is one thing the agent did, in transcript order: a tool call (with
// its result) or a skill a person ran as a slash command.
type shipEvent struct {
	call    toolCall
	id      string
	result  string
	command string
}

type shipTranscript struct {
	events []*shipEvent
}

// readShipTranscript walks the main thread of a Claude Code session .jsonl.
func readShipTranscript(path string) (shipTranscript, error) {
	f, err := os.Open(path) //nolint:gosec // the path comes from Claude Code's own hook envelope
	if err != nil {
		return shipTranscript{}, err
	}
	defer func() { _ = f.Close() }()
	var t shipTranscript
	byID := map[string]*shipEvent{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 64<<20)
	for sc.Scan() {
		var e struct {
			Type        string `json:"type"`
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
			if json.Unmarshal(e.Message.Content, &text) == nil {
				if m := commandRE.FindStringSubmatch(text); m != nil {
					t.events = append(t.events, &shipEvent{command: m[1]})
				}
				continue
			}
			var blocks []struct {
				Type      string          `json:"type"`
				ToolUseID string          `json:"tool_use_id"`
				Content   json.RawMessage `json:"content"`
				Text      string          `json:"text"`
			}
			_ = json.Unmarshal(e.Message.Content, &blocks)
			for _, b := range blocks {
				switch b.Type {
				case "tool_result":
					if ev, ok := byID[b.ToolUseID]; ok {
						ev.result = resultText(b.Content)
					}
				case "text":
					if m := commandRE.FindStringSubmatch(b.Text); m != nil {
						t.events = append(t.events, &shipEvent{command: m[1]})
					}
				}
			}
		case "assistant":
			var blocks []struct {
				Type string `json:"type"`
				ID   string `json:"id"`
				toolCall
			}
			_ = json.Unmarshal(e.Message.Content, &blocks)
			for _, b := range blocks {
				if b.Type == "tool_use" {
					ev := &shipEvent{call: b.toolCall, id: b.ID}
					byID[b.ID] = ev
					t.events = append(t.events, ev)
				}
			}
		}
	}
	return t, sc.Err()
}

func resultText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []struct {
		Text string `json:"text"`
	}
	_ = json.Unmarshal(raw, &blocks)
	parts := make([]string, 0, len(blocks))
	for _, b := range blocks {
		parts = append(parts, b.Text)
	}
	return strings.Join(parts, "\n")
}

// refusals counts the PR-open calls this check already refused.
func (t shipTranscript) refusals() int {
	n := 0
	for _, ev := range t.events {
		if ev.call.Name == "Bash" && prOpenRE.MatchString(ev.call.str("command")) && strings.Contains(ev.result, prOpenRefusalPrefix) {
			n++
		}
	}
	return n
}

func (ev *shipEvent) skill() string {
	if ev.command != "" {
		return ev.command
	}
	if ev.call.Name == "Skill" {
		return strings.TrimPrefix(ev.call.str("skill"), "pstack:")
	}
	return ""
}

func (ev *shipEvent) isCommit() bool {
	return ev.call.Name == "Bash" && gitCommitRE.MatchString(ev.call.str("command"))
}

// isRepoEdit is a file tool writing inside the worktree, other than a todolist.
func (ev *shipEvent) isRepoEdit(cwd string) bool {
	switch ev.call.Name {
	case "Edit", "Write", "MultiEdit", "NotebookEdit":
	default:
		return false
	}
	path := ev.call.str("file_path") + ev.call.str("notebook_path")
	if cwd == "" || todoFileRE.MatchString(filepath.Base(path)) {
		return false
	}
	rel, err := filepath.Rel(cwd, path)
	return err == nil && !strings.HasPrefix(rel, "..")
}

// staleReviews names each pre-ship review that did not see the work that
// ships. A review is fresh when, after its last run, the work changed by at
// most the one commit that records the review's own fixes: a second commit,
// or a repository edit after that commit, means code it never saw.
func staleReviews(events []*shipEvent, cwd string) []string {
	var stale []string
	for _, s := range preShipSkills {
		last := -1
		for i, ev := range events {
			if s.runs(ev.skill(), ev.call.str("subagent_type")) {
				last = i
			}
		}
		if last < 0 {
			stale = append(stale, s.name+" has not run in this session.")
			continue
		}
		commits := 0
		for _, ev := range events[last+1:] {
			if ev.isCommit() {
				commits++
			}
			if commits > 1 || (commits == 1 && ev.isRepoEdit(cwd)) {
				stale = append(stale, s.name+" ran before later work; run it again over the branch diff.")
				break
			}
		}
	}
	return stale
}

type todoItem struct {
	text   string
	closed bool
}

// todolist is the session's todolist as it stands: tasks replayed from
// TaskCreate/TaskUpdate, the last TodoWrite, and every todo.md the session
// wrote, read from disk so a shell edit counts as much as an Edit.
func todolist(events []*shipEvent, cwd string) []todoItem {
	tasks := map[string]*todoItem{}
	var order []string
	var written []todoItem
	files := map[string]bool{}
	for _, ev := range events {
		c := ev.call
		switch c.Name {
		case "TaskCreate":
			id := fmt.Sprint(len(order) + 1)
			if m := taskIDRE.FindStringSubmatch(ev.result); m != nil {
				id = m[1]
			}
			tasks[id] = &todoItem{text: c.str("subject") + " " + c.str("description")}
			order = append(order, id)
		case "TaskUpdate":
			var in struct {
				TaskID any    `json:"taskId"`
				Status string `json:"status"`
				Subj   string `json:"subject"`
				Desc   string `json:"description"`
			}
			_ = json.Unmarshal(c.Input, &in)
			if item, ok := tasks[fmt.Sprint(in.TaskID)]; ok {
				item.text += " " + in.Subj + " " + in.Desc
				if in.Status != "" {
					item.closed = in.Status == "completed" || in.Status == "deleted"
				}
			}
		case "TodoWrite":
			var in struct {
				Todos []struct {
					Content string `json:"content"`
					Status  string `json:"status"`
				} `json:"todos"`
			}
			_ = json.Unmarshal(c.Input, &in)
			written = written[:0]
			for _, t := range in.Todos {
				written = append(written, todoItem{text: t.Content, closed: t.Status == "completed"})
			}
		case "Write", "Edit", "MultiEdit":
			if path := c.str("file_path"); todoFileRE.MatchString(filepath.Base(path)) {
				files[path] = true
			}
		case "Bash":
			for _, path := range todoPathRE.FindAllString(c.str("command"), -1) {
				if strings.HasPrefix(path, "~/") {
					home, _ := os.UserHomeDir()
					path = filepath.Join(home, path[2:])
				} else if !filepath.IsAbs(path) {
					path = filepath.Join(cwd, path)
				}
				files[path] = true
			}
		}
	}
	items := append([]todoItem{}, written...)
	for _, id := range order {
		items = append(items, *tasks[id])
	}
	for path := range files {
		b, err := os.ReadFile(path) //nolint:gosec // a todo.md the session itself wrote
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(b), "\n") {
			if listItemRE.MatchString(line) {
				items = append(items, todoItem{text: line, closed: checkedRE.MatchString(line)})
			}
		}
	}
	for i := range items {
		items[i].closed = items[i].closed || skippedRE.MatchString(codeSpanRE.ReplaceAllString(items[i].text, ""))
		items[i].text = " " + normalizeWords(items[i].text) + " "
	}
	return items
}

// openSteps says which steps of the playbook the session follows are not yet
// done or skipped, or "" when none. The playbook it follows is the one read in
// the main thread whose steps the todolist best covers; a playbook whose steps
// cannot be found is satisfied.
func openSteps(t shipTranscript, cwd string, steps func(string) []string) string {
	var read []string
	for _, ev := range t.events {
		if pb := playbookRead(ev.call); pb != "" && pb != "opening-a-pr" {
			read = append(read, pb)
		}
	}
	if len(read) == 0 {
		return "no poteto-mode playbook was read in this session, so there are no steps to close; open the playbook that fits the task and track its steps."
	}
	items := todolist(t.events, cwd)
	best, bestOpen := "", []string(nil)
	for _, pb := range read {
		var open []string
		for _, s := range steps(pb) {
			done := false
			for _, it := range items {
				if it.closed && strings.Contains(it.text, " "+s+" ") {
					done = true
					break
				}
			}
			if !done {
				open = append(open, s)
			}
		}
		if len(open) == 0 {
			return ""
		}
		if best == "" || len(open) < len(bestOpen) {
			best, bestOpen = pb, open
		}
	}
	quoted := make([]string, len(bestOpen))
	for i, s := range bestOpen {
		quoted[i] = fmt.Sprintf("%q", s)
	}
	return fmt.Sprintf("these steps of playbooks/%s.md are not completed or marked `skip: <reason>` in your todolist: %s.", best, strings.Join(quoted, ", "))
}

// emDashInBody reports an em dash in the open command (its title and inline
// body) or in a body file it names.
func emDashInBody(command, cwd string) bool {
	if strings.ContainsRune(command, '\u2014') {
		return true
	}
	for _, m := range bodyFileRE.FindAllStringSubmatch(command, -1) {
		path := strings.Trim(m[1], `"'`)
		if path == "-" {
			continue
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(cwd, path)
		}
		if b, err := os.ReadFile(path); err == nil && strings.ContainsRune(string(b), '\u2014') { //nolint:gosec // a body file the agent named
			return true
		}
	}
	return false
}
