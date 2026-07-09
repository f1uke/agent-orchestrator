// Package diffhunk parses `git diff` unified output into classified lines and
// extracts the single hunk that covers a given new-side line number. It is pure
// (no I/O) so the SCM comment code-context feature can render a diff hunk around
// a review comment's anchor line.
package diffhunk

import (
	"strconv"
	"strings"
)

// Kind classifies one diff line.
type Kind string

const (
	// KindContext is an unchanged line present on both sides.
	KindContext Kind = "context"
	// KindAdd is a line added on the new side (OldLine == 0).
	KindAdd Kind = "add"
	// KindDel is a line removed from the old side (NewLine == 0).
	KindDel Kind = "del"
)

// Line is one classified diff line with 1-based old/new line numbers (0 where
// the line does not exist on that side). Text excludes the leading +/-/space.
type Line struct {
	Kind    Kind
	OldLine int
	NewLine int
	Text    string
}

// HunkForLine parses the unified diff for a single file (the output of
// `git diff <base>..<head> -- <path>`) and returns the lines of the one hunk
// whose body covers newLine on the new side. found is false when no hunk covers
// newLine (e.g. the anchor is in an unchanged region far from any change).
func HunkForLine(diff string, newLine int) ([]Line, bool) {
	rows := strings.Split(diff, "\n")
	i := 0
	for i < len(rows) {
		if !strings.HasPrefix(rows[i], "@@") {
			i++
			continue
		}
		oldCur, newCur, ok := parseHunkHeader(rows[i])
		if !ok {
			i++
			continue
		}
		i++
		body := make([]Line, 0, 16)
		covers := false
		for i < len(rows) {
			r := rows[i]
			if strings.HasPrefix(r, "@@") || strings.HasPrefix(r, "diff ") ||
				strings.HasPrefix(r, "--- ") || strings.HasPrefix(r, "+++ ") ||
				strings.HasPrefix(r, "index ") {
				break // next hunk or next file header — end this hunk body
			}
			if r == "" {
				i++
				continue // trailing blank from the split
			}
			switch r[0] {
			case ' ':
				body = append(body, Line{Kind: KindContext, OldLine: oldCur, NewLine: newCur, Text: r[1:]})
				if newCur == newLine {
					covers = true
				}
				oldCur++
				newCur++
			case '+':
				body = append(body, Line{Kind: KindAdd, NewLine: newCur, Text: r[1:]})
				if newCur == newLine {
					covers = true
				}
				newCur++
			case '-':
				body = append(body, Line{Kind: KindDel, OldLine: oldCur, Text: r[1:]})
				oldCur++
			case '\\':
				// "\ No newline at end of file" — metadata, ignore.
			default:
				// Unexpected content: abandon the whole parse defensively
				// rather than risk returning a corrupt hunk. Real `git diff`
				// bodies only ever start with ' '/'+'/'-'/'\', so this is
				// unreachable for well-formed input.
				i = len(rows)
			}
			i++
		}
		if covers {
			return body, true
		}
	}
	return nil, false
}

// parseHunkHeader reads "@@ -oldStart[,oldCount] +newStart[,newCount] @@ ..."
// and returns the 1-based old/new start line numbers.
func parseHunkHeader(h string) (oldStart, newStart int, ok bool) {
	if !strings.HasPrefix(h, "@@ ") {
		return 0, 0, false
	}
	rest := h[3:]
	end := strings.Index(rest, " @@")
	if end < 0 {
		return 0, 0, false
	}
	parts := strings.Fields(rest[:end]) // ["-10,6", "+10,7"]
	if len(parts) != 2 {
		return 0, 0, false
	}
	o, ok1 := parseStart(parts[0], '-')
	n, ok2 := parseStart(parts[1], '+')
	if !ok1 || !ok2 {
		return 0, 0, false
	}
	return o, n, true
}

// parseStart reads a "<sign><start>[,<count>]" token, returning start.
func parseStart(tok string, sign byte) (int, bool) {
	if len(tok) == 0 || tok[0] != sign {
		return 0, false
	}
	tok = tok[1:]
	if c := strings.IndexByte(tok, ','); c >= 0 {
		tok = tok[:c]
	}
	n, err := strconv.Atoi(tok)
	if err != nil {
		return 0, false
	}
	return n, true
}
