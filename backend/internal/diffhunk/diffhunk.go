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
	// KindHunk is not a line of the file at all: it marks a seam where the diff
	// SKIPS lines, so a renderer can show a boundary instead of butting two
	// distant regions together. OldLine/NewLine are the starts of the hunk that
	// follows and Text is the verbatim `@@ -a,b +c,d @@ section` header.
	KindHunk Kind = "hunk"
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
	var found []Line
	eachHunk(diff, func(_ hunk, body []Line) bool {
		for _, l := range body {
			// Only context and add lines exist on the new side; a del line
			// carries NewLine == 0, so it can never match a 1-based target.
			if l.NewLine == newLine {
				found = body
				return false // stop
			}
		}
		return true // keep scanning
	})
	if found == nil {
		return nil, false
	}
	return found, true
}

// AllLines parses a single file's unified diff and returns every hunk's lines in
// order. Used by the Files panel, which shows a whole file's diff rather than
// the one hunk around a review-comment anchor.
//
// Wherever the diff skips lines — between two hunks, or before the first hunk
// when it does not start at line 1 — a KindHunk marker is emitted. Without it
// the caller would render two distant regions of the file as if they were
// consecutive code. Hunks that continue each other with nothing skipped (which
// `-U0` can produce) get no marker, and neither does a whole-file addition or
// deletion, whose single hunk covers everything there is.
func AllLines(diff string) []Line {
	var out []Line
	// Highest old/new line number emitted so far; 0 before the first hunk. A hunk
	// body can end on a deletion (NewLine == 0), so track the max per side rather
	// than reading the last line.
	oldEnd, newEnd := 0, 0
	eachHunk(diff, func(h hunk, body []Line) bool {
		if h.skipsLinesAfter(oldEnd, newEnd) {
			out = append(out, Line{Kind: KindHunk, OldLine: h.OldStart, NewLine: h.NewStart, Text: h.Header})
		}
		out = append(out, body...)
		for _, l := range body {
			oldEnd = max(oldEnd, l.OldLine)
			newEnd = max(newEnd, l.NewLine)
		}
		return true
	})
	return out
}

// hunk is one parsed `@@` header: where its body starts on each side, and the
// header line verbatim (including git's trailing section heading).
type hunk struct {
	OldStart int
	NewStart int
	Header   string
}

// skipsLinesAfter reports whether any line of the file sits between the region
// already emitted (ending at oldEnd/newEnd) and this hunk's start. A side whose
// start is 0 does not exist in this diff — a new file has no old side, a deleted
// file no new side — so it cannot skip anything.
func (h hunk) skipsLinesAfter(oldEnd, newEnd int) bool {
	return (h.OldStart > 0 && h.OldStart > oldEnd+1) || (h.NewStart > 0 && h.NewStart > newEnd+1)
}

// eachHunk parses each hunk of a unified diff and hands its header and body to
// visit, which returns false to stop scanning.
func eachHunk(diff string, visit func(h hunk, body []Line) bool) {
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
		head := hunk{OldStart: oldCur, NewStart: newCur, Header: rows[i]}
		i++
		body := make([]Line, 0, 16)
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
				oldCur++
				newCur++
			case '+':
				body = append(body, Line{Kind: KindAdd, NewLine: newCur, Text: r[1:]})
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
		if !visit(head, body) {
			return
		}
	}
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
	if tok == "" || tok[0] != sign {
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
