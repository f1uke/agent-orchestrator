// Package adf converts an Atlassian Document Format (ADF) document — the JSON
// tree Jira Cloud returns in an issue's `fields.description` — into AO's compact,
// whitelisted render tree ([]Node).
//
// This is deliberately a NORMALIZE step, not an HTML renderer: the frontend maps
// the node tree onto safe React elements, so no HTML string is ever built or
// injected. The whitelist below is the faithful set of block/inline kinds real
// Jira issues use (verified across a Story, a Bug-with-table, and a
// panel/ordered-list Story). The rule for anything outside the whitelist is
// "recurse or drop": an unknown block with children surfaces its children
// (unwrapped) so content is never silently lost, and an unknown leaf/mark is
// dropped while its text survives. So a newer ADF node type degrades to its text
// rather than crashing or vanishing.
package adf

import (
	"encoding/json"
)

// Node is one normalized ADF node. It is also the wire shape sent to the
// renderer, so the json tags are load-bearing — keep them stable.
type Node struct {
	// Type is the normalized node kind (paragraph, heading, bulletList, text, …).
	Type string `json:"type"`
	// Text is the literal text for a "text" node; empty otherwise.
	Text string `json:"text,omitempty"`
	// Marks are the inline styles on a text node (strong, em, code, link, …).
	Marks []Mark `json:"marks,omitempty"`
	// Attrs carries the typed subset of node attributes the renderer needs.
	Attrs *Attrs `json:"attrs,omitempty"`
	// Content are the child nodes (already normalized).
	Content []Node `json:"content,omitempty"`
}

// Mark is one inline style on a text node.
type Mark struct {
	// Type is one of: strong, em, code, strike, underline, link, subsup.
	Type string `json:"type"`
	// Href is the target for a link mark; empty otherwise.
	Href string `json:"href,omitempty"`
}

// Attrs is the typed, whitelisted subset of ADF node attributes. Every field is
// optional and only meaningful for specific node types (documented inline). A
// node with no meaningful attribute carries a nil *Attrs.
type Attrs struct {
	Level     int    `json:"level,omitempty"`     // heading: 1..6
	PanelType string `json:"panelType,omitempty"` // panel: info|note|warning|success|error
	Language  string `json:"language,omitempty"`  // codeBlock
	State     string `json:"state,omitempty"`     // taskItem: TODO|DONE
	Color     string `json:"color,omitempty"`     // status lozenge colour
	Text      string `json:"text,omitempty"`      // status|mention|emoji|date display text
	URL       string `json:"url,omitempty"`       // inlineCard|blockCard target
	Filename  string `json:"filename,omitempty"`  // media attachment display name
	Layout    string `json:"layout,omitempty"`    // mediaSingle|table layout hint
}

func (a Attrs) isEmpty() bool { return a == Attrs{} }

// blockTypes and inlineTypes are the whitelisted node kinds carried through
// verbatim (their content is recursed into). Anything not listed is treated by
// the recurse-or-drop rule.
var passthroughTypes = map[string]bool{
	// block
	"paragraph": true, "heading": true, "blockquote": true,
	"bulletList": true, "orderedList": true, "listItem": true,
	"codeBlock": true, "panel": true, "rule": true,
	"table": true, "tableRow": true, "tableCell": true, "tableHeader": true,
	"mediaSingle": true, "mediaGroup": true,
	"taskList": true, "taskItem": true,
	// inline / leaf
	"hardBreak": true, "media": true, "mediaInline": true,
	"inlineCard": true, "blockCard": true,
	"mention": true, "emoji": true, "date": true, "status": true,
}

// keptMarks is the whitelist of inline marks. Decorative marks (textColor,
// backgroundColor, …) are dropped while the text they wrapped is preserved.
var keptMarks = map[string]bool{
	"strong": true, "em": true, "code": true,
	"strike": true, "underline": true, "link": true, "subsup": true,
}

type rawNode struct {
	Type    string         `json:"type"`
	Text    string         `json:"text"`
	Marks   []rawMark      `json:"marks"`
	Attrs   map[string]any `json:"attrs"`
	Content []rawNode      `json:"content"`
}

type rawMark struct {
	Type  string         `json:"type"`
	Attrs map[string]any `json:"attrs"`
}

// Parse converts a raw ADF description document into AO's normalized top-level
// node list. A null / empty / unparseable document yields nil (callers treat
// that as "no description"). The returned slice is the doc's children — the
// `doc` wrapper itself is not emitted.
func Parse(raw json.RawMessage) []Node {
	if len(raw) == 0 {
		return nil
	}
	var doc rawNode
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil
	}
	// The top level is a `doc` node; if we somehow got a bare node, still try
	// to normalize it so we never lose content.
	if doc.Type == "doc" || doc.Type == "" {
		return normalizeNodes(doc.Content)
	}
	return normalizeNode(doc)
}

func normalizeNodes(raws []rawNode) []Node {
	if len(raws) == 0 {
		return nil
	}
	out := make([]Node, 0, len(raws))
	for _, r := range raws {
		out = append(out, normalizeNode(r)...)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// normalizeNode maps one raw node onto zero or more normalized nodes. A
// whitelisted type maps 1:1; a "text" node maps to itself with kept marks; an
// unknown type surfaces its normalized children (unwrapped) or drops entirely.
func normalizeNode(r rawNode) []Node {
	switch {
	case r.Type == "text":
		return []Node{{Type: "text", Text: r.Text, Marks: normalizeMarks(r.Marks)}}
	case passthroughTypes[r.Type]:
		n := Node{Type: r.Type, Content: normalizeNodes(r.Content)}
		if attrs := normalizeAttrs(r.Type, r.Attrs); attrs != nil {
			n.Attrs = attrs
		}
		return []Node{n}
	default:
		// Unknown wrapper: keep its content so nothing is silently lost; a leaf
		// with no content is dropped.
		return normalizeNodes(r.Content)
	}
}

func normalizeMarks(raws []rawMark) []Mark {
	if len(raws) == 0 {
		return nil
	}
	out := make([]Mark, 0, len(raws))
	for _, m := range raws {
		if !keptMarks[m.Type] {
			continue
		}
		mark := Mark{Type: m.Type}
		if m.Type == "link" {
			mark.Href = attrString(m.Attrs, "href")
		}
		out = append(out, mark)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func normalizeAttrs(nodeType string, m map[string]any) *Attrs {
	if m == nil {
		return nil
	}
	var a Attrs
	switch nodeType {
	case "heading":
		a.Level = attrInt(m, "level")
	case "panel":
		a.PanelType = attrString(m, "panelType")
	case "codeBlock":
		a.Language = attrString(m, "language")
	case "taskItem":
		a.State = attrString(m, "state")
	case "status":
		a.Text = attrString(m, "text")
		a.Color = attrString(m, "color")
	case "inlineCard", "blockCard":
		a.URL = attrString(m, "url")
	case "media", "mediaInline":
		// Jira sets the attachment's display name on `alt`; fall back to `title`.
		a.Filename = firstNonEmpty(attrString(m, "alt"), attrString(m, "title"))
	case "mediaSingle", "table":
		a.Layout = attrString(m, "layout")
	case "mention":
		a.Text = attrString(m, "text")
	case "emoji":
		a.Text = firstNonEmpty(attrString(m, "text"), attrString(m, "shortName"))
	case "date":
		a.Text = attrString(m, "timestamp")
	}
	if a.isEmpty() {
		return nil
	}
	return &a
}

func attrString(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func attrInt(m map[string]any, key string) int {
	if m == nil {
		return 0
	}
	switch v := m[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	}
	return 0
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
