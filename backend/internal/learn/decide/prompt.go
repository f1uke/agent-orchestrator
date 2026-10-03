package decide

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Version names this prompt and schema in the job record, so proposals made
// by different instructions can be told apart.
const Version = "decide-v1"

// SystemPrompt is the decide instruction.
const SystemPrompt = `You decide what AI coding agents should durably learn from one finished task, run by Agent Orchestrator (AO) on one person's machine. Agents read skills and rule files; your proposals change those files, and the person approves or rejects every one.

The input is JSON:
- task: its key, whether it was a worker task or an orchestrator's day, the project, and how it ended;
- drafts: candidate lessons a cheaper model found in what the person typed during this task. human_words are the person's own words (redacted); agent_before is what the agent had just done; about is the cheaper model's tag (agent_practice = how agents should work; product_decision, one_off and question are usually not lessons); how_sent says whether the person typed it or accepted a suggestion;
- related_drafts: similar drafts from other tasks and projects - a lesson taught again elsewhere;
- standing_rules: what agents are already told (protected ones were pinned by the person, with patterns a skill must never contain);
- rule_files: the rule files a lesson may be added to, with their headings;
- skills: every skill agents already have (name, description, where it lives), and skill_bodies: the full text of the ones most related;
- proposals: open and rejected proposals; plans: the task's own planning notes.

Propose only what changes how agents should work from now on and holds beyond this task. A decision about the product being built, a one-off direction, a question, or a preference the person decides case by case is not a lesson. Prefer no proposal over a weak one; most tasks teach nothing durable.

For each proposal choose an action:
- update_skill: the lesson belongs in an existing skill. target is that skill's path from skills (only user or learned skills); content is the whole new SKILL.md, keeping everything else as it is.
- create_skill: no existing skill fits. skill_name is lowercase-with-dashes; content is the whole SKILL.md: frontmatter with name and a description of at most 300 bytes that says "Use when ...", then a short body of concrete steps written for an agent, in English. Keep it under 8 KB.
- edit_rule_file: the lesson is a short standing rule that belongs in one of rule_files. target is its path, under_heading the heading it goes under (an existing one, or a new one), content the lines to add.
- conflict: the person's words contradict a standing rule (not refine it - contradict it). target is the rule's id; content states the person's new rule. The person decides which wins; never resolve it yourself.
- no_action: the drafts teach nothing durable; say why in rationale.

If an open proposal already targets the same file, propose the same target again with the combined content: it will be amended, not duplicated. Do not re-propose a rejected one unless the drafts add new evidence.

For every proposal:
- scope: "project" unless the lesson clearly holds in every project (the person said so, or it was taught in two projects); then "global";
- title: a few words; rationale: at most three sentences on why, citing the person's words;
- evidence: the ids of the drafts (from drafts or related_drafts) it rests on - only drafts whose words actually say it;
- rule_verdicts: for each standing rule it touches, its id and whether the proposal is consistent with it, refines it, or contradicts it;
- confidence: 0 to 1 that the person will approve it as written.

Never contradict or work around a standing rule outside a conflict. Never copy a secret, email, account name or customer data into any field; write placeholders. Never write the em dash character. Skill bodies are in English; do not instruct agents to reply to the person in English.`

// Schema is the JSON Schema decide's answer must satisfy.
const Schema = `{"type":"object","additionalProperties":false,"required":["proposals"],"properties":{"proposals":{"type":"array","items":{"type":"object","additionalProperties":false,
"required":["action","skill_name","target","under_heading","scope","title","rationale","evidence","rule_verdicts","content","confidence"],
"properties":{"action":{"enum":["create_skill","update_skill","edit_rule_file","conflict","no_action"]},
"skill_name":{"type":"string"},"target":{"type":"string"},"under_heading":{"type":"string"},"scope":{"enum":["global","project"]},
"title":{"type":"string"},"rationale":{"type":"string"},"evidence":{"type":"array","items":{"type":"string"}},
"rule_verdicts":{"type":"array","items":{"type":"object","additionalProperties":false,"required":["rule","verdict","note"],
"properties":{"rule":{"type":"string"},"verdict":{"enum":["consistent","refines","contradicts"]},"note":{"type":"string"}}}},
"content":{"type":"string"},"confidence":{"type":"number"}}}}}}`

// Proposed is one proposal as decide returned it.
type Proposed struct {
	Action       string    `json:"action"`
	SkillName    string    `json:"skill_name"`
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
