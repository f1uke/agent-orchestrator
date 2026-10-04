package decide

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Version names this prompt and schema in the job record, so proposals made
// by different instructions can be told apart.
const Version = "decide-v5"

// SystemPrompt is the decide instruction.
const SystemPrompt = `You decide what AI coding agents should durably learn from one finished task, run by Agent Orchestrator (AO) on one person's machine. Agents read the project's memory (Claude Code memory files), skills and rule files; your proposals change those files, and the person approves or rejects every one.

The input is JSON:
- task: its key, whether it was a worker task or an orchestrator's day, the project, and how it ended;
- drafts: candidate lessons a cheaper model found in what the person typed during this task. human_words are the person's own words (redacted); agent_before is what the agent had just done; about is the cheaper model's tag (agent_practice = how agents should work; product_decision, one_off and question are usually not lessons); how_sent says whether the person typed it or accepted a suggestion;
- related_drafts: similar drafts from other tasks and projects - a lesson taught again elsewhere;
- standing_rules: what agents are already told, including the project's existing memory files (source ending in memory/<file>.md); protected ones were pinned by the person, with patterns nothing may contain;
- rule_files: the global rule file a lesson for every project may be added to, with its headings;
- skills: every skill agents already have (name, description, where it lives), and skill_bodies: the full text of the ones most related;
- proposals: open and rejected proposals; plans: the task's own planning notes.

Propose only what changes how agents should work from now on and holds beyond this task.

Write only what the person's words in the evidence say. Do not add steps, examples, precautions, tools or reasons they did not state, however sensible: a reviewer refuses any proposal with a single line the person's words do not support, and the whole lesson is lost. A one-line rule in their words is better than a fuller one in yours. A decision about the product being built, a one-off direction, a question, or a preference the person decides case by case is not a lesson. Prefer no proposal over a weak one; most tasks teach nothing durable.

For each proposal choose an action:
- create_memory: a lesson of this project that no existing memory holds - this is where a project's lesson goes. memory_type is feedback (how to work: a correction or a confirmed approach), project (a fact about the work or its environment) or reference (where something lives); name is a few lowercase words; description is one line of at most 300 bytes that says what the memory is, so a reader can tell when it applies; title is a few words for the index; content is the body: the rule in the person's words, then a "**Why:**" line and a "**How to apply:**" line when their words give them. AO writes the file in Claude Code's memory format and its line in MEMORY.md. Leave target and under_heading empty.
- update_memory: an existing memory file (a standing rule whose source ends in memory/<file>.md) should change - it holds the lesson already but the person refined it. target is that file's path; content is the whole new file, frontmatter included, keeping its name and everything still true. If a memory already says the same thing, propose nothing.
- update_skill: the lesson belongs in an existing skill of the person's about its topic. target is that skill's path from skills (only source user); content is the whole new SKILL.md, keeping everything else as it is.
- edit_rule_file: a standing rule for every project, for the person's ~/.claude/CLAUDE.md (the only entry in rule_files). target is its path, under_heading the heading it goes under (prefer an existing one), content the lines to add, in the format of the lines already there.
- conflict: the person's words contradict a standing rule or an existing memory (not refine it - contradict it). target is the rule's id; content states the person's new rule. The person decides which wins; never resolve it yourself.
- no_action: the drafts teach nothing durable; say why in rationale.

If an open proposal already targets the same file, it will be amended, not duplicated: for update_memory, update_skill and create_memory your content must keep every line of the open proposal's content (given in proposals) and add yours. Do not re-propose a rejected one unless the drafts add new evidence.

For every proposal:
- scope: "project" unless the lesson clearly holds in every project (the person said so, or it was taught in two projects); then "global";
- title: a few words; rationale: at most three sentences on why, citing the person's words;
- evidence: the ids of the drafts (from drafts or related_drafts) it rests on - only drafts whose words actually say it;
- rule_verdicts: for each standing rule it touches, its id and whether the proposal is consistent with it, refines it, or contradicts it;
- confidence: 0 to 1 that the person will approve it as written.

Never contradict or work around a standing rule outside a conflict. Never copy a secret, email, account name or customer data into any field; write placeholders. Never write the em dash character. Memory and skill text is in English (quote the person's Thai words where they matter); do not instruct agents to reply to the person in English.`

// Schema is the JSON Schema decide's answer must satisfy.
const Schema = `{"type":"object","additionalProperties":false,"required":["proposals"],"properties":{"proposals":{"type":"array","items":{"type":"object","additionalProperties":false,
"required":["action","memory_type","name","description","target","under_heading","scope","title","rationale","evidence","rule_verdicts","content","confidence"],
"properties":{"action":{"enum":["create_memory","update_memory","update_skill","edit_rule_file","conflict","no_action"]},
"memory_type":{"enum":["feedback","project","reference","user",""]},"name":{"type":"string"},"description":{"type":"string"},"target":{"type":"string"},"under_heading":{"type":"string"},"scope":{"enum":["global","project"]},
"title":{"type":"string"},"rationale":{"type":"string"},"evidence":{"type":"array","items":{"type":"string"}},
"rule_verdicts":{"type":"array","items":{"type":"object","additionalProperties":false,"required":["rule","verdict","note"],
"properties":{"rule":{"type":"string"},"verdict":{"enum":["consistent","refines","contradicts"]},"note":{"type":"string"}}}},
"content":{"type":"string"},"confidence":{"type":"number"}}}}}}`

// Proposed is one proposal as decide returned it.
type Proposed struct {
	Action       string    `json:"action"`
	MemoryType   string    `json:"memory_type"`
	Name         string    `json:"name"`
	Description  string    `json:"description"`
	Target       string    `json:"target"`
	UnderHeading string    `json:"under_heading"`
	Scope        string    `json:"scope"`
	Title        string    `json:"title"`
	Rationale    string    `json:"rationale"`
	Evidence     []string  `json:"evidence"`
	RuleVerdicts []Verdict `json:"rule_verdicts"`
	Content      string    `json:"content"`
	Confidence   float64   `json:"confidence"`
}

// Verdict is a proposal's relation to one rule.
type Verdict struct {
	Rule    string `json:"rule"`
	Verdict string `json:"verdict"`
	Note    string `json:"note"`
}

// ParseDecide reads decide's answer.
func ParseDecide(output json.RawMessage) ([]Proposed, error) {
	var ans struct {
		Proposals *[]Proposed `json:"proposals"`
	}
	if err := json.Unmarshal(output, &ans); err != nil {
		return nil, fmt.Errorf("decide: parse answer: %w", err)
	}
	if ans.Proposals == nil {
		return nil, errors.New("decide: answer has no proposals list")
	}
	return *ans.Proposals, nil
}

// VerifierPrompt is the adversarial check of decide's proposals.
const VerifierPrompt = `You are the adversarial reviewer of proposals another model made to change the skills and rule files AI coding agents read. A wrong proposal teaches every future agent something the person never said, or undoes a rule they set. Look for reasons to refuse.

The input is JSON: proposals (each with an id, its action, target, content and the evidence ids it cites), evidence (the cited drafts: the person's own words in human_words, and what the agent had just done in agent_before), and standing_rules (what agents are already told; protected ones were pinned by the person).

For each proposal answer:
- contradicts_rule: does any step contradict a standing rule, or work around one (for example doing by another route what a rule forbids)? A proposal whose action is already conflict exists to show a contradiction, so answer false for it unless it also breaks another rule. List the rule ids in contradicted_rules.
- grounded: is every instruction in the content supported by the person's own words in the cited evidence - not by what the agent did or said, and not by the reviewer's own idea of good practice? Generic filler that the evidence does not support makes it ungrounded.
- sensitive: does any value look like a secret, a token, a password, an account name, an email address, a phone number or a customer's data?
- notes: one or two sentences on what you found.

Answer for every proposal id, in order.`

// VerifierSchema is the JSON Schema of the verifier's answer.
const VerifierSchema = `{"type":"object","additionalProperties":false,"required":["reviews"],"properties":{"reviews":{"type":"array","items":{"type":"object","additionalProperties":false,
"required":["proposal","contradicts_rule","contradicted_rules","grounded","sensitive","notes"],
"properties":{"proposal":{"type":"string"},"contradicts_rule":{"type":"boolean"},"contradicted_rules":{"type":"array","items":{"type":"string"}},
"grounded":{"type":"boolean"},"sensitive":{"type":"boolean"},"notes":{"type":"string"}}}}}}`

// Review is the verifier's answer for one proposal.
type Review struct {
	Proposal          string   `json:"proposal"`
	ContradictsRule   bool     `json:"contradicts_rule"`
	ContradictedRules []string `json:"contradicted_rules"`
	Grounded          bool     `json:"grounded"`
	Sensitive         bool     `json:"sensitive"`
	Notes             string   `json:"notes"`
}

// ParseVerifier reads the verifier's answer, keyed by proposal id.
func ParseVerifier(output json.RawMessage) (map[string]Review, error) {
	var ans struct {
		Reviews []Review `json:"reviews"`
	}
	if err := json.Unmarshal(output, &ans); err != nil {
		return nil, fmt.Errorf("verifier: parse answer: %w", err)
	}
	out := make(map[string]Review, len(ans.Reviews))
	for _, r := range ans.Reviews {
		out[strings.TrimSpace(r.Proposal)] = r
	}
	return out, nil
}
