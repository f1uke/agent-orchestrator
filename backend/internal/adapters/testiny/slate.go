package testiny

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Testiny stores rich text as a JSON-encoded Slate document:
// {"t":"slate","v":1,"c":[...blocks]}. The blocks seen in real cases are p,
// ul and ol (of li), code, and t (a table of tr of td). Inline nodes are text
// leaves, which may carry marks such as bold or code, and a (a link).

type slateNode struct {
	T        string      `json:"t"`
	Text     *string     `json:"text"`
	Children []slateNode `json:"children"`
	URL      string      `json:"url"`
	Start    int         `json:"start"`
}

type slateDoc struct {
	T string      `json:"t"`
	C []slateNode `json:"c"`
}

// parseSlate reads a Slate document, and reports false for anything else.
func parseSlate(raw string) ([]slateNode, bool) {
	var doc slateDoc
	if err := json.Unmarshal([]byte(raw), &doc); err != nil || doc.T != "slate" {
		return nil, false
	}
	return doc.C, true
}

// richText renders a rich-text field as plain text that keeps its structure:
// blocks apart by a blank line, list items as "- item" or "1. item" with
// nested lists indented, and table rows as "cell | cell". Marks are dropped. A
// value that is not a Slate document is plain text already.
func richText(raw string) string {
	raw = strings.TrimSpace(raw)
	blocks, ok := parseSlate(raw)
	if !ok {
		return raw
	}
	return strings.Join(renderBlocks(blocks), "\n\n")
}

// stepsTable reads a STEPS case's table: each row is an action and its
// expected result. A row with both cells empty is not a step.
func stepsTable(raw string) []domain.TestinyCaseStep {
	blocks, _ := parseSlate(strings.TrimSpace(raw))
	steps := []domain.TestinyCaseStep{}
	for _, b := range blocks {
		if b.T != "t" {
			continue
		}
		for _, row := range b.Children {
			var cells [2]string
			for i, cell := range row.Children {
				if i < len(cells) {
					cells[i] = strings.Join(renderBlocks(cell.Children), "\n")
				}
			}
			if cells[0] == "" && cells[1] == "" {
				continue
			}
			steps = append(steps, domain.TestinyCaseStep{N: len(steps) + 1, Action: cells[0], Expected: cells[1]})
		}
	}
	return steps
}

// renderBlocks renders each block that has any text.
func renderBlocks(nodes []slateNode) []string {
	var out []string
	for _, n := range nodes {
		if s := renderBlock(n); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func renderBlock(n slateNode) string {
	switch {
	case n.T == "ul" || n.T == "ol":
		return renderList(n)
	case n.T == "t":
		return renderTable(n)
	case n.T == "tr":
		return renderRow(n)
	case allInline(n.Children):
		return strings.TrimSpace(inlineText(n.Children))
	default:
		return strings.Join(renderBlocks(n.Children), "\n")
	}
}

func renderList(n slateNode) string {
	next := 1
	if n.T == "ol" && n.Start > 0 {
		next = n.Start
	}
	var lines []string
	for _, item := range n.Children {
		marker := "- "
		if n.T == "ol" {
			marker = strconv.Itoa(next) + ". "
			next++
		}
		body := strings.Join(renderBlocks(item.Children), "\n")
		if body == "" {
			continue
		}
		indent := strings.Repeat(" ", len(marker))
		lines = append(lines, marker+strings.ReplaceAll(body, "\n", "\n"+indent))
	}
	return strings.Join(lines, "\n")
}

func renderTable(n slateNode) string {
	var rows []string
	for _, r := range n.Children {
		if s := renderRow(r); s != "" {
			rows = append(rows, s)
		}
	}
	return strings.Join(rows, "\n")
}

// renderRow keeps a row on one line: a cell's own lines are joined by " / ".
func renderRow(r slateNode) string {
	cells := make([]string, len(r.Children))
	empty := true
	for i, cell := range r.Children {
		cells[i] = strings.Join(renderBlocks(cell.Children), " / ")
		cells[i] = strings.ReplaceAll(cells[i], "\n", " / ")
		empty = empty && cells[i] == ""
	}
	if empty {
		return ""
	}
	return strings.Join(cells, " | ")
}

func allInline(nodes []slateNode) bool {
	for _, n := range nodes {
		if n.Text == nil && n.T != "a" {
			return false
		}
	}
	return true
}

// inlineText is a paragraph's text. A link whose text is not its address
// shows the address after it.
func inlineText(nodes []slateNode) string {
	var b strings.Builder
	for _, n := range nodes {
		switch {
		case n.Text != nil:
			b.WriteString(*n.Text)
		case n.T == "a":
			text := inlineText(n.Children)
			b.WriteString(text)
			if n.URL != "" && n.URL != text {
				b.WriteString(" (" + n.URL + ")")
			}
		}
	}
	return b.String()
}
