// Package rules builds and searches the standing-rules corpus: the rules AI
// agents are already told - the human's CLAUDE.md, each project's CLAUDE.md
// and AGENTS.md, skills, AO's own standing prompt, the knowledge INDEX - split
// into atomic statements, so a candidate lesson can be checked against them
// before it is ever proposed.
//
// Everything here is pure: the refresh loop (observe/learnrules) reads the
// files, calls the model and stores the result.
package rules

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"unicode/utf8"
)

// ChunkLimit is the most text one atomize call is given. A whole instruction
// file usually fits; a larger one is cut at its headings so an edit
// re-atomizes only the part it touched.
const ChunkLimit = 24 << 10

// Section is a run of markdown under one heading. The text includes the
// heading line itself.
type Section struct {
	Heading string
	Text    string
}

// Chunk is the unit the corpus caches: consecutive sections whose text is
// atomized together. Hash addresses the cache, so identical text in two files
// is atomized once and an unchanged chunk is never atomized again.
type Chunk struct {
	Sections []Section
	Hash     string
}

// Text is the chunk's whole text.
func (c Chunk) Text() string {
	var b strings.Builder
	for _, s := range c.Sections {
		b.WriteString(s.Text)
	}
	return b.String()
}

// Heading is the first heading in the chunk, for display.
func (c Chunk) Heading() string {
	for _, s := range c.Sections {
		if s.Heading != "" {
			return s.Heading
		}
	}
	return ""
}

// Chunks splits a markdown document into chunks of at most ChunkLimit bytes,
// cutting only between H1/H2 sections (outside code fences), and inside an
// oversized section only between paragraphs. version is folded into each hash
// so a change to how chunks are atomized invalidates the cache.
func Chunks(text, version string) []Chunk {
	var out []Chunk
	var cur []Section
	size := 0
	flush := func() {
		if len(cur) == 0 {
			return
		}
		out = append(out, newChunk(cur, version))
		cur, size = nil, 0
	}
	for _, s := range splitSections(text, 2) {
		if strings.TrimSpace(s.Text) == "" {
			continue
		}
		if len(s.Text) > ChunkLimit {
			flush()
			for _, piece := range splitOversized(s.Text) {
				out = append(out, newChunk([]Section{{Heading: s.Heading, Text: piece}}, version))
			}
			continue
		}
		if size+len(s.Text) > ChunkLimit {
			flush()
		}
		cur = append(cur, s)
		size += len(s.Text)
	}
	flush()
	return out
}

func newChunk(sections []Section, version string) Chunk {
	c := Chunk{Sections: sections}
	sum := sha256.Sum256([]byte(version + "\x00" + c.Text()))
	c.Hash = hex.EncodeToString(sum[:])
	return c
}

// splitSections cuts text before every heading of level 1..maxLevel that is
// not inside a fenced code block. Text before the first heading is a section
// with no heading.
func splitSections(text string, maxLevel int) []Section {
	var out []Section
	var b strings.Builder
	heading := ""
	fence := ""
	for _, line := range strings.SplitAfter(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if fence == "" {
			if h, ok := headingOf(trimmed, maxLevel); ok {
				if b.Len() > 0 {
					out = append(out, Section{Heading: heading, Text: b.String()})
					b.Reset()
				}
				heading = h
			}
		}
		fence = nextFence(fence, trimmed)
		b.WriteString(line)
	}
	if b.Len() > 0 {
		out = append(out, Section{Heading: heading, Text: b.String()})
	}
	return out
}

func headingOf(trimmed string, maxLevel int) (string, bool) {
	level := 0
	for level < len(trimmed) && trimmed[level] == '#' {
		level++
	}
	if level == 0 || level > maxLevel || level >= len(trimmed) || trimmed[level] != ' ' {
		return "", false
	}
	return strings.TrimSpace(trimmed[level:]), true
}

// nextFence tracks ``` and ~~~ fences: it returns the fence that is open after
// this line ("" when none).
func nextFence(open, trimmed string) string {
	for _, f := range []string{"```", "~~~"} {
		if !strings.HasPrefix(trimmed, f) {
			continue
		}
		switch open {
		case "":
			return f
		case f:
			return ""
		}
	}
	return open
}

// splitOversized cuts one section into pieces of at most ChunkLimit bytes at
// blank lines outside code fences; a single paragraph longer than that is cut
// at line, then rune, boundaries.
func splitOversized(text string) []string {
	var paras []string
	var b strings.Builder
	fence := ""
	for _, line := range strings.SplitAfter(text, "\n") {
		trimmed := strings.TrimSpace(line)
		b.WriteString(line)
		fence = nextFence(fence, trimmed)
		if trimmed == "" && fence == "" {
			paras = append(paras, b.String())
			b.Reset()
		}
	}
	if b.Len() > 0 {
		paras = append(paras, b.String())
	}
	var out []string
	var cur strings.Builder
	for _, p := range paras {
		if cur.Len()+len(p) > ChunkLimit && cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
		for len(p) > ChunkLimit {
			cut := strings.LastIndex(p[:ChunkLimit], "\n") + 1
			if cut <= 0 {
				cut = ChunkLimit
				for cut > 0 && !utf8.RuneStart(p[cut]) {
					cut--
				}
			}
			out = append(out, p[:cut])
			p = p[cut:]
		}
		cur.WriteString(p)
	}
	if strings.TrimSpace(cur.String()) != "" {
		out = append(out, cur.String())
	}
	return out
}

// Blocks splits a document deterministically, with no model, into one atom per
// top-level list item or paragraph under its heading. It is for the knowledge
// INDEX, where every entry is already one line that stands on its own and the
// file is too large to send to a model every time it changes.
func Blocks(text string) []Atom {
	var out []Atom
	for _, s := range splitSections(text, 6) {
		var b strings.Builder
		fence := ""
		flush := func() {
			block := strings.TrimSpace(b.String())
			b.Reset()
			if len([]rune(block)) < minBlock {
				return
			}
			out = append(out, Atom{Text: block, Quote: block, Heading: s.Heading})
		}
		for _, line := range strings.Split(s.Text, "\n") {
			trimmed := strings.TrimSpace(line)
			if fence == "" {
				if _, ok := headingOf(trimmed, 6); ok {
					continue
				}
				if trimmed == "---" || trimmed == "***" || trimmed == "___" {
					flush()
					continue
				}
				if trimmed == "" || isTopLevelItem(line) {
					flush()
				}
			}
			fence = nextFence(fence, trimmed)
			b.WriteString(line)
			b.WriteString("\n")
		}
		flush()
	}
	return out
}

// minBlock drops fragments too short to be a rule (a lone "---", a stray link).
const minBlock = 12

func isTopLevelItem(line string) bool {
	if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") {
		return true
	}
	i := 0
	for i < len(line) && line[i] >= '0' && line[i] <= '9' {
		i++
	}
	return i > 0 && strings.HasPrefix(line[i:], ". ")
}
