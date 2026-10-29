package sessionhost

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/chatproto"
)

const (
	ansiReset  = "\x1b[0m"
	ansiDim    = "\x1b[2m"
	ansiRed    = "\x1b[31m"
	ansiYellow = "\x1b[33m"
	ansiCyan   = "\x1b[36m"

	maxArgRunes    = 60
	maxResultRunes = 120
)

func dim(s string) string    { return ansiDim + s + ansiReset }
func red(s string) string    { return ansiRed + s + ansiReset }
func yellow(s string) string { return ansiYellow + s + ansiReset }
func cyan(s string) string   { return ansiCyan + s + ansiReset }

// RenderPane renders one log entry as the lines the pane shows, each with at
// most one ANSI style. Entries with nothing to show render as no lines.
func RenderPane(e chatproto.Entry) []string {
	switch e.Dir {
	case chatproto.DirHost:
		note, err := chatproto.ParseHostNote(e.Payload)
		if err != nil {
			return nil
		}
		return renderNote(note)
	case chatproto.DirIn:
		ev, err := chatproto.ParseLine(e.Payload)
		if err != nil {
			return nil
		}
		return renderIn(ev)
	case chatproto.DirOut:
		ev, err := chatproto.ParseLine(e.Payload)
		if err != nil {
			return nil
		}
		return renderOut(ev)
	}
	return nil
}

func renderNote(n chatproto.HostNote) []string {
	switch n.Kind {
	case chatproto.NoteSpawn:
		if n.Resume != "" {
			return []string{dim("── claude resumed " + n.Resume)}
		}
		return []string{dim("── claude started")}
	case chatproto.NoteExit:
		if n.Code != nil {
			return []string{dim(fmt.Sprintf("── claude exited %d", *n.Code))}
		}
		var w struct {
			Signal string `json:"signal"`
		}
		_ = json.Unmarshal(n.Raw, &w)
		return []string{dim("── claude exited on " + w.Signal)}
	case noteNonJSON:
		var w struct {
			Text string `json:"text"`
		}
		_ = json.Unmarshal(n.Raw, &w)
		return []string{dim(w.Text)}
	}
	return nil
}

func renderIn(ev chatproto.Event) []string {
	switch ev := ev.(type) {
	case chatproto.User:
		var text strings.Builder
		images := 0
		for _, b := range ev.Blocks {
			switch b := b.(type) {
			case chatproto.TextBlock:
				text.WriteString(b.Text)
			case chatproto.ImageBlock:
				images++
			}
		}
		lines := splitLines(text.String())
		if images > 0 {
			lines = append(lines, fmt.Sprintf("[%d image(s)]", images))
		}
		out := make([]string, 0, len(lines))
		for i, l := range lines {
			prefix := "  "
			if i == 0 {
				prefix = "> "
			}
			out = append(out, cyan(prefix+l))
		}
		return out
	case chatproto.ControlResponse:
		var body struct {
			Behavior string `json:"behavior"`
			Message  string `json:"message"`
		}
		if json.Unmarshal(ev.Response, &body) != nil {
			return nil
		}
		switch body.Behavior {
		case "allow":
			return []string{dim("  → allowed")}
		case "deny":
			if body.Message == "" {
				return []string{dim("  → denied")}
			}
			return []string{dim("  → denied: " + firstLine(body.Message, maxResultRunes))}
		}
	}
	return nil
}

func renderOut(ev chatproto.Event) []string {
	switch ev := ev.(type) {
	case chatproto.Assistant:
		var out []string
		for _, b := range ev.Blocks {
			switch b := b.(type) {
			case chatproto.TextBlock:
				out = append(out, splitLines(b.Text)...)
			case chatproto.ToolUseBlock:
				out = append(out, toolLabel("● ", b.Name, b.Input))
			}
		}
		return out
	case chatproto.User:
		if ev.IsReplay {
			return nil
		}
		var out []string
		for _, b := range ev.Blocks {
			switch b := b.(type) {
			case chatproto.ToolResultBlock:
				if b.IsError {
					out = append(out, red("  ⎿ error: "+resultLine(b.Content)))
				} else {
					out = append(out, dim("  ⎿ ok"))
				}
			case chatproto.TextBlock:
				if strings.HasPrefix(b.Text, "[Request interrupted by user") {
					out = append(out, red("! interrupted"))
				}
			}
		}
		return out
	case chatproto.ControlRequest:
		if c, ok := ev.Body.(chatproto.CanUseTool); ok {
			return []string{yellow(toolLabel("? Permission: ", c.ToolName, c.Input) + " waiting")}
		}
	case chatproto.Result:
		what := "done"
		if ev.Subtype != "success" {
			what = ev.Subtype
		}
		return []string{dim(fmt.Sprintf("· %s, $%.2f", what, ev.TotalCostUSD))}
	case chatproto.System:
		if ev.Subtype == "compact_boundary" {
			return []string{dim("· compacted")}
		}
	}
	return nil
}

func toolLabel(prefix, name string, input json.RawMessage) string {
	if arg := shortArg(input); arg != "" {
		return prefix + name + "(" + arg + ")"
	}
	return prefix + name
}

var argKeys = []string{"file_path", "command", "pattern", "path", "url", "query", "description", "prompt", "plan"}

func shortArg(input json.RawMessage) string {
	var fields map[string]json.RawMessage
	if json.Unmarshal(input, &fields) != nil {
		return ""
	}
	str := func(k string) string {
		var s string
		if json.Unmarshal(fields[k], &s) != nil {
			return ""
		}
		return s
	}
	for _, k := range argKeys {
		if s := str(k); s != "" {
			return firstLine(s, maxArgRunes)
		}
	}
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if s := str(k); s != "" {
			return firstLine(s, maxArgRunes)
		}
	}
	return ""
}

func resultLine(content json.RawMessage) string {
	var s string
	if json.Unmarshal(content, &s) == nil {
		return firstLine(s, maxResultRunes)
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(content, &blocks) == nil {
		for _, b := range blocks {
			if b.Type == "text" && b.Text != "" {
				return firstLine(b.Text, maxResultRunes)
			}
		}
	}
	return ""
}

func firstLine(s string, limit int) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = strings.TrimSpace(s[:i]) + "…"
	}
	if r := []rune(s); len(r) > limit {
		return string(r[:limit]) + "…"
	}
	return s
}

func splitLines(s string) []string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}
