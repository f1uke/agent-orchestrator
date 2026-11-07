// Package chatcontext reads what a claude-code agent was given, from the CLI's
// own transcript: the system prompt and tools of each distinct request shape,
// and the context blocks (system reminders) the CLI injected into the
// conversation. The transcript is the CLI's internal format, not a public API,
// so anything this package does not recognise is skipped rather than failed.
package chatcontext

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"
	"unicode/utf8"
)

// Past MaxInjections the newest are kept, because the latest turns are the ones a person reads.
const (
	MaxShapes     = 20
	MaxInjections = 2000
	// MaxTextBytes caps one system block, tool description, or injection text.
	MaxTextBytes = 256 << 10
)

// maxLineBytes bounds one transcript line. A request-shape line carries every
// tool's schema, so lines run to hundreds of KiB.
var maxLineBytes = 64 << 20

// Tool is one tool definition sent with a request.
type Tool struct {
	Name        string
	Description string
	InputSchema json.RawMessage
}

// Shape is the system prompt and tools of one request shape. The first shape
// is the initial system prompt; a later one means they changed mid-session.
type Shape struct {
	TS     time.Time
	System []string
	Tools  []Tool
}

// Injection is one context block the CLI added to the conversation.
type Injection struct {
	TS        time.Time
	Source    string
	Role      string
	Text      string
	Truncated bool
}

// Context is what Read found in a transcript, oldest first.
type Context struct {
	Shapes     []Shape
	Injections []Injection
	// Truncated is true when a cap dropped shapes or injections, or cut a text.
	Truncated bool
}

var (
	shapeMarker      = []byte(`"type":"api-request-shape"`)
	attachmentMarker = []byte(`"type":"attachment"`)
)

type textBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type record struct {
	Type        string `json:"type"`
	Timestamp   string `json:"timestamp"`
	IsSidechain bool   `json:"isSidechain"`
	ShapeHash   string `json:"shapeHash"`
	Shape       struct {
		System []textBlock `json:"system"`
		Tools  []struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			InputSchema json.RawMessage `json:"input_schema"`
		} `json:"tools"`
	} `json:"shape"`
	Attachment struct {
		Type string `json:"type"`
	} `json:"attachment"`
	Rendered []struct {
		Content json.RawMessage `json:"content"`
	} `json:"rendered"`
	RenderedRole string `json:"renderedRole"`
}

// Read scans a transcript.
func Read(r io.Reader) (Context, error) {
	out := Context{Shapes: []Shape{}, Injections: []Injection{}}
	seen := map[string]bool{}
	lines := lineReader{r: bufio.NewReaderSize(r, 64<<10)}
	for {
		line, err := lines.next()
		if line != nil && (bytes.Contains(line, shapeMarker) || bytes.Contains(line, attachmentMarker)) {
			out.add(line, seen)
		}
		if errors.Is(err, io.EOF) {
			if n := len(out.Injections); n > MaxInjections {
				out.Injections = out.Injections[n-MaxInjections:]
				out.Truncated = true
			}
			out.Truncated = out.Truncated || lines.skipped
			return out, nil
		}
		if err != nil {
			return out, err
		}
	}
}

func (c *Context) add(line []byte, seen map[string]bool) {
	var rec record
	if json.Unmarshal(line, &rec) != nil || rec.IsSidechain {
		return
	}
	switch rec.Type {
	case "api-request-shape":
		c.addShape(rec, seen)
	case "attachment":
		c.addInjection(rec)
	}
}

type lineReader struct {
	r       *bufio.Reader
	buf     []byte
	skipped bool
}

func (l *lineReader) next() ([]byte, error) {
	l.buf = l.buf[:0]
	over := false
	for {
		frag, err := l.r.ReadSlice('\n')
		if !over {
			if len(l.buf)+len(frag) > maxLineBytes {
				over, l.skipped, l.buf = true, true, l.buf[:0]
			} else {
				l.buf = append(l.buf, frag...)
			}
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if over || len(l.buf) == 0 {
			return nil, err
		}
		return l.buf, err
	}
}

func (c *Context) addShape(rec record, seen map[string]bool) {
	if rec.ShapeHash != "" {
		if seen[rec.ShapeHash] {
			return
		}
		seen[rec.ShapeHash] = true
	}
	if len(c.Shapes) >= MaxShapes {
		c.Truncated = true
		return
	}
	shape := Shape{TS: parseTS(rec.Timestamp), System: []string{}, Tools: []Tool{}}
	for _, b := range rec.Shape.System {
		if b.Text == "" {
			continue
		}
		shape.System = append(shape.System, c.capped(b.Text))
	}
	for _, t := range rec.Shape.Tools {
		shape.Tools = append(shape.Tools, Tool{Name: t.Name, Description: c.capped(t.Description), InputSchema: t.InputSchema})
	}
	c.Shapes = append(c.Shapes, shape)
}

func (c *Context) addInjection(rec record) {
	parts := make([]string, 0, len(rec.Rendered))
	for _, r := range rec.Rendered {
		if text := contentText(r.Content); text != "" {
			parts = append(parts, text)
		}
	}
	if len(parts) == 0 {
		return
	}
	if len(c.Injections) >= 2*MaxInjections {
		c.Injections = append(c.Injections[:0], c.Injections[len(c.Injections)-MaxInjections:]...)
		c.Truncated = true
	}
	text, cut := capText(strings.Join(parts, "\n"))
	c.Truncated = c.Truncated || cut
	c.Injections = append(c.Injections, Injection{
		TS:        parseTS(rec.Timestamp),
		Source:    rec.Attachment.Type,
		Role:      rec.RenderedRole,
		Text:      text,
		Truncated: cut,
	})
}

func (c *Context) capped(s string) string {
	text, cut := capText(s)
	c.Truncated = c.Truncated || cut
	return text
}

// A rendered block's content is a string or an array of content blocks; only text blocks reach the model.
func contentText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []textBlock
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	texts := make([]string, 0, len(blocks))
	for _, b := range blocks {
		if b.Type == "text" && b.Text != "" {
			texts = append(texts, b.Text)
		}
	}
	return strings.Join(texts, "\n")
}

func capText(s string) (string, bool) {
	if len(s) <= MaxTextBytes {
		return s, false
	}
	end := MaxTextBytes
	for end > 0 && !utf8.RuneStart(s[end]) {
		end--
	}
	return s[:end], true
}

func parseTS(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t
}
