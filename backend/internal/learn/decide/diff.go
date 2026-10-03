package decide

import (
	"fmt"
	"strings"
)

// diffContext is how many unchanged lines surround a change.
const diffContext = 3

// Diff is a unified diff of old to new for path, computed by AO so the human
// reviews exactly what would be written - never a model's account of it. It
// is "" when nothing changes.
func Diff(path, old, next string) string {
	if old == next {
		return ""
	}
	a, b := splitLines(old), splitLines(next)
	ops := lcsOps(a, b)
	var out strings.Builder
	fromName := "a/" + strings.TrimPrefix(path, "/")
	if old == "" {
		fromName = "/dev/null"
	}
	fmt.Fprintf(&out, "--- %s\n+++ b/%s\n", fromName, strings.TrimPrefix(path, "/"))
	for _, h := range hunks(ops) {
		out.WriteString(h)
	}
	return out.String()
}

type op struct {
	kind byte // ' ', '-', '+'
	text string
	ai   int // line number in a (1-based) for ' ' and '-'
	bi   int // line number in b (1-based) for ' ' and '+'
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	lines := strings.SplitAfter(s, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// lcsOps aligns a and b by their longest common subsequence. The files are
// instruction files and skills - hundreds of lines at most - so the quadratic
// table is cheap.
func lcsOps(a, b []string) []op {
	n, m := len(a), len(b)
	t := make([][]int32, n+1)
	for i := range t {
		t[i] = make([]int32, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				t[i][j] = t[i+1][j+1] + 1
			} else {
				t[i][j] = max(t[i+1][j], t[i][j+1])
			}
		}
	}
	var ops []op
	i, j := 0, 0
	for i < n || j < m {
		switch {
		case i < n && j < m && a[i] == b[j]:
			ops = append(ops, op{' ', a[i], i + 1, j + 1})
			i++
			j++
		case j < m && (i == n || t[i][j+1] >= t[i+1][j]):
			ops = append(ops, op{'+', b[j], i, j + 1})
			j++
		default:
			ops = append(ops, op{'-', a[i], i + 1, j})
			i++
		}
	}
	return ops
}

// hunks groups changes with their context into @@ hunks.
func hunks(ops []op) []string {
	var out []string
	for i := 0; i < len(ops); {
		if ops[i].kind == ' ' {
			i++
			continue
		}
		start := max(i-diffContext, 0)
		end := i
		for end < len(ops) {
			if ops[end].kind != ' ' {
				end++
				continue
			}
			run := end
			for run < len(ops) && ops[run].kind == ' ' {
				run++
			}
			if run == len(ops) || run-end > 2*diffContext {
				end = min(end+diffContext, len(ops))
				break
			}
			end = run
		}
		var body strings.Builder
		aStart, bStart, aLen, bLen := 0, 0, 0, 0
		for _, o := range ops[start:end] {
			if aStart == 0 && o.kind != '+' {
				aStart = o.ai
			}
			if bStart == 0 && o.kind != '-' {
				bStart = o.bi
			}
			if o.kind != '+' {
				aLen++
			}
			if o.kind != '-' {
				bLen++
			}
			text := o.text
			if !strings.HasSuffix(text, "\n") {
				text += "\n\\ No newline at end of file\n"
			}
			body.WriteByte(o.kind)
			body.WriteString(text)
		}
		if aStart == 0 {
			aStart = ops[start].ai
		}
		if bStart == 0 {
			bStart = ops[start].bi
		}
		out = append(out, fmt.Sprintf("@@ -%d,%d +%d,%d @@\n", aStart, aLen, bStart, bLen)+body.String())
		i = end
	}
	return out
}

// InsertUnder adds text at the end of the section under heading (any level,
// matched case-insensitively), or appends a new "## heading" section when the
// file has none. It is how a rule-file edit is applied: the model names where a
// rule belongs, AO writes it, so the rest of the file can never be rewritten.
func InsertUnder(content, heading, text string) string {
	text = strings.TrimRight(text, "\n") + "\n"
	lines := strings.SplitAfter(content, "\n")
	want := strings.ToLower(strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(heading), "#")))
	found, level := -1, 0
	for i, l := range lines {
		t := strings.TrimSpace(l)
		lv := len(t) - len(strings.TrimLeft(t, "#"))
		if lv > 0 && lv < len(t) && t[lv] == ' ' && strings.ToLower(strings.TrimSpace(t[lv:])) == want && want != "" {
			found, level = i, lv
			break
		}
	}
	if found < 0 {
		sep := ""
		if content != "" && !strings.HasSuffix(content, "\n") {
			sep = "\n"
		}
		if content != "" {
			sep += "\n"
		}
		h := strings.TrimSpace(heading)
		if h == "" {
			return content + sep + text
		}
		return content + sep + "## " + strings.TrimLeft(h, "# ") + "\n\n" + text
	}
	end := len(lines)
	fence := false
	for i := found + 1; i < len(lines); i++ {
		t := strings.TrimSpace(lines[i])
		if strings.HasPrefix(t, "```") {
			fence = !fence
		}
		if fence {
			continue
		}
		lv := len(t) - len(strings.TrimLeft(t, "#"))
		if lv > 0 && lv <= level && lv < len(t) && t[lv] == ' ' {
			end = i
			break
		}
	}
	// Insert after the section's last non-blank line.
	at := end
	for at > found+1 && strings.TrimSpace(lines[at-1]) == "" {
		at--
	}
	if at > 0 && !strings.HasSuffix(lines[at-1], "\n") {
		lines[at-1] += "\n"
	}
	out := append([]string{}, lines[:at]...)
	out = append(out, text)
	out = append(out, lines[at:]...)
	return strings.Join(out, "")
}
