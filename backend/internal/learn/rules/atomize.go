package rules

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Version is folded into every model-atomized chunk's hash: changing the
// prompt or the schema below must change it, so the corpus is rebuilt rather
// than mixing rules cut two different ways.
const Version = "atomize-v1"

// BlocksVersion is the same for the deterministic split (Blocks).
const BlocksVersion = "blocks-v1"

// Atom is one standing rule.
type Atom struct {
	// Text is the rule, self-contained.
	Text string `json:"text"`
	// Quote is the verbatim span of the source that states it.
	Quote string `json:"quote"`
	// Tags are keywords for retrieval.
	Tags []string `json:"tags,omitempty"`
	// Heading is the source heading the quote sits under.
	Heading string `json:"heading,omitempty"`
}

// SystemPrompt is the atomize instruction.
const SystemPrompt = `You turn an instruction document that AI coding agents are given into a list of atomic standing rules, so that a later check can tell whether a new lesson repeats or contradicts something the agents are already told.

The input is JSON: where the text comes from (source), and the text.

A rule is one thing an agent must do, must not do, or must know: a directive, a constraint, a convention, a step that is always required, or a fact about the environment the agent has to respect. Leave out headings, background, descriptions of what a tool is, examples that add no rule, and anything addressed to people rather than to the agent.

For each rule give:
- text: the rule in one or two self-contained sentences, in the document's own words where possible, with the context a reader would otherwise need from the heading or the surrounding text (what it applies to, when);
- quote: a short span copied exactly, character for character, from the text that states the rule (at most about 200 characters);
- tags: 2 to 8 lowercase keywords naming the tools, commands, files and topics the rule is about.

Split a sentence that carries several rules into several rules. Do not merge rules, do not invent a rule the text does not state, and keep every rule the text states, even an obvious one. Never copy a secret, token or password into any field. Return an empty list when the text states no rule.`

// Schema is the JSON Schema the model's answer must satisfy.
const Schema = `{"type":"object","additionalProperties":false,"required":["rules"],"properties":{"rules":{"type":"array","items":{"type":"object","additionalProperties":false,
"required":["text","quote","tags"],
"properties":{"text":{"type":"string"},"quote":{"type":"string"},"tags":{"type":"array","items":{"type":"string"}}}}}}}`

// Input is the user message for one chunk.
func Input(source string, c Chunk) (string, error) {
	b, err := json.Marshal(struct {
		Source string `json:"source"`
		Text   string `json:"text"`
	}{source, c.Text()})
	return string(b), err
}

// Rejection is a rule the grounding check refused.
type Rejection struct {
	Atom   Atom
	Reason string
}

// maxTags bounds what one rule may carry into the index.
const maxTags = 8

// Atoms parses the model's answer for chunk c and keeps the rules whose quote
// is in the chunk. The quote is the grounding: a rule the source does not state
// would later read as a conflict that is not there.
func Atoms(output json.RawMessage, c Chunk) ([]Atom, []Rejection, error) {
	var ans struct {
		Rules []Atom `json:"rules"`
	}
	if err := json.Unmarshal(output, &ans); err != nil {
		return nil, nil, fmt.Errorf("rules: parse answer: %w", err)
	}
	if ans.Rules == nil && !strings.Contains(string(output), `"rules"`) {
		return nil, nil, errors.New("rules: answer has no rules list")
	}
	whole := normalize(c.Text())
	var out []Atom
	var rejected []Rejection
	for _, a := range ans.Rules {
		a.Text = strings.TrimSpace(a.Text)
		a.Quote = strings.TrimSpace(a.Quote)
		q := normalize(a.Quote)
		switch {
		case a.Text == "":
			rejected = append(rejected, Rejection{a, "empty rule"})
			continue
		case q == "":
			rejected = append(rejected, Rejection{a, "empty quote"})
			continue
		case !strings.Contains(whole, q):
			rejected = append(rejected, Rejection{a, "quote is not in the source"})
			continue
		}
		a.Heading = headingFor(c, q)
		a.Tags = cleanTags(a.Tags)
		out = append(out, a)
	}
	return out, rejected, nil
}

// headingFor is the heading of the section that holds the quote; a quote that
// spans two sections takes the chunk's first heading.
func headingFor(c Chunk, q string) string {
	for _, s := range c.Sections {
		if strings.Contains(normalize(s.Text), q) {
			return s.Heading
		}
	}
	return c.Heading()
}

func cleanTags(tags []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range tags {
		t = strings.ToLower(strings.TrimSpace(t))
		if t == "" || seen[t] || len(out) == maxTags {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return out
}

// normalize makes a quote comparable to its source despite what a model
// changes when it copies: whitespace, case, markdown emphasis and dashes.
func normalize(s string) string {
	s = strings.NewReplacer("*", "", "`", "", "\u2014", "-", "\u2013", "-", "\u2019", "'", "\u2018", "'", "\u201c", `"`, "\u201d", `"`).Replace(s)
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}
