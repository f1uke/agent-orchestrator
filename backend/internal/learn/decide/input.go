package decide

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/learn/rules"
	"github.com/aoagents/agent-orchestrator/backend/internal/learn/skills"
)

// Bounds on what one decide call is given.
const (
	MaxInputBytes   = 40 << 10
	relatedDrafts   = 15
	relatedRules    = 25
	verifierRules   = 50
	skillBodies     = 3
	planBytes       = 4 << 10
	skillBodyBytes  = 6 << 10
	descriptionCap  = 200
	maxIndexSkills  = 120
	minIndexSkills  = 20
	minRelatedRules = 8
)

// Plan is a knowledge-store plan of the task's branch.
type Plan struct {
	Path string `json:"path"`
	Text string `json:"text"`
}

// RuleFile is a rule file a lesson may be added to, with its headings.
type RuleFile struct {
	Path     string   `json:"path"`
	Scope    string   `json:"scope"`
	Headings []string `json:"headings"`
}

// Context is everything decide is given about one task, before it is shaped
// into the model's input.
type Context struct {
	TaskKey   string
	ProjectID domain.ProjectID
	Outcome   domain.LearnOutcome
	PRs       []string
	Plans     []Plan
	// Drafts are the task's open drafts.
	Drafts []domain.LearnDraft
	// Others are every other draft (any project, any status but reversed),
	// from which the related ones are picked.
	Others    []domain.LearnDraft
	Corpus    []domain.LearnRule
	Protected []domain.LearnProtectedRule
	Skills    []skills.Skill
	RuleFiles []RuleFile
	// Proposals are the existing proposals, for amending and for rejection
	// memory.
	Proposals []domain.LearnProposal
}

// DraftID names a draft in the model's input.
func DraftID(id int64) string { return "d" + strconv.FormatInt(id, 10) }

// ParseDraftID reverses DraftID.
func ParseDraftID(s string) (int64, bool) {
	n, err := strconv.ParseInt(strings.TrimPrefix(strings.TrimSpace(s), "d"), 10, 64)
	return n, err == nil && strings.HasPrefix(strings.TrimSpace(s), "d")
}

type draftIn struct {
	ID          string  `json:"id"`
	Task        string  `json:"task"`
	Project     string  `json:"project"`
	Kind        string  `json:"kind"`
	About       string  `json:"about,omitempty"`
	Statement   string  `json:"statement"`
	AppliesWhen string  `json:"applies_when,omitempty"`
	Quote       string  `json:"human_words"`
	AgentBefore string  `json:"agent_before,omitempty"`
	HowSent     string  `json:"how_sent,omitempty"`
	Confidence  float64 `json:"confidence"`
	Weak        bool    `json:"weak,omitempty"`
	Status      string  `json:"status,omitempty"`
}

type ruleIn struct {
	ID        string   `json:"id"`
	Text      string   `json:"text"`
	Source    string   `json:"source"`
	Heading   string   `json:"heading,omitempty"`
	Protected bool     `json:"protected,omitempty"`
	Forbids   []string `json:"forbids,omitempty"`
}

type skillIn struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Source      string `json:"source"`
	Scope       string `json:"scope"`
	Path        string `json:"path"`
}

type skillBody struct {
	Path string `json:"path"`
	Body string `json:"body"`
}

type proposalIn struct {
	ID           string `json:"id"`
	Status       string `json:"status"`
	Action       string `json:"action"`
	Target       string `json:"target"`
	Title        string `json:"title"`
	RejectReason string `json:"reject_reason,omitempty"`
}

type input struct {
	Task struct {
		Key     string   `json:"key"`
		Kind    string   `json:"kind"`
		Project string   `json:"project"`
		Outcome string   `json:"outcome"`
		PRs     []string `json:"pull_requests,omitempty"`
	} `json:"task"`
	Drafts      []draftIn    `json:"drafts"`
	Related     []draftIn    `json:"related_drafts"`
	Rules       []ruleIn     `json:"standing_rules"`
	RuleFiles   []RuleFile   `json:"rule_files"`
	Skills      []skillIn    `json:"skills"`
	SkillBodies []skillBody  `json:"skill_bodies"`
	Proposals   []proposalIn `json:"proposals"`
	Plans       []Plan       `json:"plans,omitempty"`
}

// Shaped is the model's input and what it may refer to.
type Shaped struct {
	JSON string
	// Drafts are every draft the input names, by id: the task's and the
	// related ones. Evidence may cite only these.
	Drafts map[int64]domain.LearnDraft
	// Rules are the rules the input names, by id.
	Rules map[string]domain.LearnRule
}

// Query is the text the task's drafts are retrieved by.
func Query(drafts []domain.LearnDraft) string {
	var b strings.Builder
	for _, d := range drafts {
		b.WriteString(d.Statement)
		b.WriteString(" ")
		b.WriteString(d.AppliesWhen)
		b.WriteString(" ")
		b.WriteString(d.Quote)
		b.WriteString("\n")
	}
	return b.String()
}

// Build shapes c into the model's input, trimming the least important parts
// until it fits MaxInputBytes: related drafts, then skill bodies, then rules,
// then the skill index.
func Build(c Context) (Shaped, error) {
	q := Query(c.Drafts)
	out := Shaped{Drafts: map[int64]domain.LearnDraft{}, Rules: map[string]domain.LearnRule{}}
	var in input
	in.Task.Key, in.Task.Project, in.Task.Outcome, in.Task.PRs = c.TaskKey, string(c.ProjectID), string(c.Outcome), c.PRs
	in.Task.Kind = "worker"
	if IsOrchestrator(c.TaskKey) {
		in.Task.Kind = "orchestrator"
	}
	for _, d := range c.Drafts {
		in.Drafts = append(in.Drafts, toDraftIn(d))
		out.Drafts[d.ID] = d
	}
	related := relatedDraftsFor(c, q)
	scored := rules.Search(c.Corpus, q, relatedRules)
	protected := protectedFor(c)
	bodies := bestSkills(c.Skills, q, skillBodies)
	index := c.Skills
	if len(index) > maxIndexSkills {
		index = bestSkills(index, q, maxIndexSkills)
	}
	in.RuleFiles = c.RuleFiles
	in.Plans = c.Plans
	in.Proposals = proposalsFor(c)
	for {
		in.Related = in.Related[:0]
		for _, d := range related {
			in.Related = append(in.Related, toDraftIn(d))
		}
		in.Rules = in.Rules[:0]
		for _, r := range scored {
			in.Rules = append(in.Rules, ruleIn{ID: r.ID, Text: r.Text, Source: r.SourceLabel, Heading: r.Heading})
		}
		in.Rules = append(in.Rules, protected...)
		in.Skills = in.Skills[:0]
		for _, s := range index {
			in.Skills = append(in.Skills, skillIn{Name: s.Name, Description: clip(s.Description, descriptionCap), Source: string(s.Source), Scope: s.Scope, Path: s.Path})
		}
		in.SkillBodies = in.SkillBodies[:0]
		for _, s := range bodies {
			in.SkillBodies = append(in.SkillBodies, skillBody{Path: s.Path, Body: clip(s.Body, skillBodyBytes)})
		}
		b, err := json.Marshal(in)
		if err != nil {
			return Shaped{}, err
		}
		if len(b) <= MaxInputBytes {
			for _, d := range related {
				out.Drafts[d.ID] = d
			}
			for _, r := range scored {
				out.Rules[r.ID] = r.LearnRule
			}
			out.JSON = string(b)
			return out, nil
		}
		switch {
		case len(related) > 0:
			related = related[:len(related)/2]
		case len(bodies) > 0:
			bodies = bodies[:len(bodies)-1]
		case len(in.Plans) > 0:
			in.Plans = nil
		case len(scored) > minRelatedRules:
			scored = scored[:len(scored)/2]
		case len(index) > minIndexSkills:
			index = index[:len(index)/2]
		default:
			return Shaped{}, fmt.Errorf("decide input is %d bytes even trimmed; the task's drafts alone are too large", len(b))
		}
	}
}

func toDraftIn(d domain.LearnDraft) draftIn {
	return draftIn{
		ID: DraftID(d.ID), Task: d.TaskKey, Project: string(d.ProjectID), Kind: string(d.Kind), About: string(d.About),
		Statement: d.Statement, AppliesWhen: d.AppliesWhen, Quote: d.Quote, AgentBefore: clip(d.AgentBefore, 600),
		HowSent: string(d.AnchorSourceClass), Confidence: d.Confidence, Weak: d.Weak, Status: string(d.Status),
	}
}

// relatedDraftsFor picks the drafts of other tasks most like this task's: the
// only way decide can see a lesson taught twice.
func relatedDraftsFor(c Context, q string) []domain.LearnDraft {
	var docs []rules.Doc
	byID := map[string]domain.LearnDraft{}
	for _, d := range c.Others {
		if d.TaskKey == c.TaskKey || d.Status == domain.LearnDraftReversed {
			continue
		}
		id := DraftID(d.ID)
		docs = append(docs, rules.Doc{ID: id, Text: d.Statement + " " + d.AppliesWhen})
		byID[id] = d
	}
	hits := rules.NewIndex(docs).Search(q, relatedDrafts)
	out := make([]domain.LearnDraft, 0, len(hits))
	for _, h := range hits {
		out = append(out, byID[h.ID])
	}
	return out
}

func protectedFor(c Context) []ruleIn {
	var out []ruleIn
	for _, p := range c.Protected {
		if p.ProjectID != "" && p.ProjectID != c.ProjectID {
			continue
		}
		out = append(out, ruleIn{ID: "protected-" + strconv.FormatInt(p.ID, 10), Text: p.Text, Source: "protected rule", Protected: true, Forbids: p.Patterns})
	}
	return out
}

// bestSkills ranks skills by their name and description against q; with no
// match the first n are returned in index order.
func bestSkills(all []skills.Skill, q string, n int) []skills.Skill {
	docs := make([]rules.Doc, len(all))
	for i, s := range all {
		docs[i] = rules.Doc{ID: strconv.Itoa(i), Text: strings.ReplaceAll(s.Name, "-", " ") + " " + s.Description}
	}
	hits := rules.NewIndex(docs).Search(q, n)
	out := make([]skills.Skill, 0, len(hits))
	for _, h := range hits {
		i, _ := strconv.Atoi(h.ID)
		out = append(out, all[i])
	}
	return out
}

// proposalsFor lists the pending and rejected proposals of the task's project
// and every global one, newest first, so decide amends instead of duplicating
// and does not re-propose a rejection without new evidence.
func proposalsFor(c Context) []proposalIn {
	var out []proposalIn
	ps := append([]domain.LearnProposal(nil), c.Proposals...)
	sort.SliceStable(ps, func(i, j int) bool { return ps[i].ID > ps[j].ID })
	for _, p := range ps {
		if p.Status != domain.LearnProposalPending && p.Status != domain.LearnProposalRejected {
			continue
		}
		if p.ProjectID != c.ProjectID && p.Scope != "global" {
			continue
		}
		out = append(out, proposalIn{ID: "p" + strconv.FormatInt(p.ID, 10), Status: string(p.Status), Action: string(p.Action),
			Target: p.TargetPath, Title: p.Title, RejectReason: p.DropReason})
		if len(out) == 30 {
			break
		}
	}
	return out
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && cut < len(s) && (s[cut]&0xC0) == 0x80 {
		cut--
	}
	return s[:cut] + "..."
}

// Headings lists a markdown file's headings, for a rule file's entry.
func Headings(content string) []string {
	var out []string
	fence := false
	for _, l := range strings.Split(content, "\n") {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "```") {
			fence = !fence
		}
		if fence {
			continue
		}
		lv := len(t) - len(strings.TrimLeft(t, "#"))
		if lv > 0 && lv <= 3 && lv < len(t) && t[lv] == ' ' {
			out = append(out, strings.TrimSpace(t[lv:]))
		}
	}
	return out
}
