package adf

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// collect walks the normalized tree and records which node types, marks, and
// link hrefs appear, so fixture assertions can check faithful coverage without
// pinning the exact tree shape.
type collected struct {
	types map[string]int
	marks map[string]int
	hrefs []string
	texts []string
}

func walk(nodes []Node, c *collected) {
	for _, n := range nodes {
		c.types[n.Type]++
		if n.Text != "" {
			c.texts = append(c.texts, n.Text)
		}
		for _, m := range n.Marks {
			c.marks[m.Type]++
			if m.Href != "" {
				c.hrefs = append(c.hrefs, m.Href)
			}
		}
		walk(n.Content, c)
	}
}

func parseFixture(t *testing.T, name string) []Node {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return Parse(json.RawMessage(raw))
}

func collectFixture(t *testing.T, name string) *collected {
	t.Helper()
	c := &collected{types: map[string]int{}, marks: map[string]int{}}
	walk(parseFixture(t, name), c)
	return c
}

func TestParse_Star2272_Story(t *testing.T) {
	c := collectFixture(t, "demo-101-desc.json")
	// Real Story body: bold "headings" are bold paragraphs (not heading nodes),
	// with bullet lists, an attachment, smart links, and an AC checklist.
	for _, want := range []string{"paragraph", "text", "bulletList", "listItem", "media", "inlineCard", "taskList", "taskItem"} {
		if c.types[want] == 0 {
			t.Errorf("expected node type %q in DEMO-101, got types=%v", want, c.types)
		}
	}
	if c.types["heading"] != 0 {
		t.Errorf("DEMO-101 must render bold paragraphs, not synthesized headings; got %d heading nodes", c.types["heading"])
	}
	if c.marks["strong"] == 0 {
		t.Errorf("expected strong marks (the bold 'headings'), got marks=%v", c.marks)
	}
	if len(c.hrefs) == 0 && c.types["inlineCard"] == 0 {
		t.Errorf("expected smart-link URLs to survive")
	}
}

func TestParse_Star2272_MediaAndTasksCarryAttrs(t *testing.T) {
	nodes := parseFixture(t, "demo-101-desc.json")
	var media, task *Node
	var find func([]Node)
	find = func(ns []Node) {
		for i := range ns {
			switch ns[i].Type {
			case "media":
				if media == nil {
					media = &ns[i]
				}
			case "taskItem":
				if task == nil {
					task = &ns[i]
				}
			}
			find(ns[i].Content)
		}
	}
	find(nodes)
	if media == nil || media.Attrs == nil || media.Attrs.Filename == "" {
		t.Errorf("media node must carry a filename, got %+v", media)
	}
	if task == nil || task.Attrs == nil || task.Attrs.State == "" {
		t.Errorf("taskItem must carry a state (TODO/DONE), got %+v", task)
	}
	var inline *Node
	var findCard func([]Node)
	findCard = func(ns []Node) {
		for i := range ns {
			if ns[i].Type == "inlineCard" && inline == nil {
				inline = &ns[i]
			}
			findCard(ns[i].Content)
		}
	}
	findCard(nodes)
	if inline == nil || inline.Attrs == nil || inline.Attrs.URL == "" {
		t.Errorf("inlineCard must carry a url, got %+v", inline)
	}
}

func TestParse_Star2312_BugTable(t *testing.T) {
	c := collectFixture(t, "demo-201-desc.json")
	for _, want := range []string{"table", "tableRow", "tableHeader", "tableCell", "status"} {
		if c.types[want] == 0 {
			t.Errorf("expected node type %q in DEMO-201 (Bug w/ table), got types=%v", want, c.types)
		}
	}
}

func TestParse_Team4532_PanelOrderedRule(t *testing.T) {
	c := collectFixture(t, "demo-301-desc.json")
	for _, want := range []string{"panel", "orderedList", "rule"} {
		if c.types[want] == 0 {
			t.Errorf("expected node type %q in DEMO-301, got types=%v", want, c.types)
		}
	}
	// The panel must carry its panelType so the renderer can colour it.
	var panelType string
	var find func([]Node)
	find = func(ns []Node) {
		for _, n := range ns {
			if n.Type == "panel" && n.Attrs != nil {
				panelType = n.Attrs.PanelType
			}
			find(n.Content)
		}
	}
	find(parseFixture(t, "demo-301-desc.json"))
	if panelType == "" {
		t.Errorf("panel node should carry a panelType")
	}
}

func TestParse_Empty(t *testing.T) {
	if got := Parse(nil); got != nil {
		t.Errorf("nil ADF should parse to nil, got %v", got)
	}
	if got := Parse(json.RawMessage(`null`)); got != nil {
		t.Errorf("null ADF should parse to nil, got %v", got)
	}
	if got := Parse(json.RawMessage(`{"type":"doc","version":1,"content":[]}`)); got != nil {
		t.Errorf("empty doc should parse to nil, got %v", got)
	}
	if got := Parse(json.RawMessage(`{bad json`)); got != nil {
		t.Errorf("bad JSON should parse to nil, got %v", got)
	}
}

func TestParse_HeadingLevelAndLinkHref(t *testing.T) {
	doc := `{"type":"doc","content":[
	  {"type":"heading","attrs":{"level":3},"content":[{"type":"text","text":"Title"}]},
	  {"type":"paragraph","content":[
	    {"type":"text","text":"see ","marks":[]},
	    {"type":"text","text":"here","marks":[{"type":"link","attrs":{"href":"https://x.test/y"}},{"type":"strong"}]}
	  ]}
	]}`
	nodes := Parse(json.RawMessage(doc))
	if len(nodes) != 2 || nodes[0].Type != "heading" || nodes[0].Attrs == nil || nodes[0].Attrs.Level != 3 {
		t.Fatalf("heading level not captured: %+v", nodes)
	}
	link := nodes[1].Content[1]
	if len(link.Marks) != 2 {
		t.Fatalf("expected link+strong marks, got %+v", link.Marks)
	}
	var href string
	for _, m := range link.Marks {
		if m.Type == "link" {
			href = m.Href
		}
	}
	if href != "https://x.test/y" {
		t.Errorf("link href = %q, want https://x.test/y", href)
	}
}

func TestParse_UnknownWrapperUnwrapsUnknownMarkDropped(t *testing.T) {
	// An unknown block ("expand") should surface its children; an unknown mark
	// ("textColor") is dropped while its text survives.
	doc := `{"type":"doc","content":[
	  {"type":"expand","attrs":{"title":"more"},"content":[
	    {"type":"paragraph","content":[{"type":"text","text":"kept","marks":[{"type":"textColor","attrs":{"color":"#ff0000"}},{"type":"em"}]}]}
	  ]},
	  {"type":"unknownLeaf"}
	]}`
	nodes := Parse(json.RawMessage(doc))
	if len(nodes) != 1 || nodes[0].Type != "paragraph" {
		t.Fatalf("unknown wrapper should unwrap to its paragraph, got %+v", nodes)
	}
	txt := nodes[0].Content[0]
	if txt.Text != "kept" {
		t.Fatalf("text lost: %+v", txt)
	}
	if len(txt.Marks) != 1 || txt.Marks[0].Type != "em" {
		t.Errorf("unknown mark should be dropped, em kept; got %+v", txt.Marks)
	}
}
