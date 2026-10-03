package decide

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/learn/redact"
	"github.com/aoagents/agent-orchestrator/backend/internal/learn/rules"
	"github.com/aoagents/agent-orchestrator/backend/internal/learn/skills"
)

// StandAlone is the draft confidence that may support a proposal on its own
// (decision 8): 15 of 15 labelled drafts at or above it were real lessons.
const StandAlone = 0.65

// everywhere matches a person saying a rule holds in every project.
var everywhere = regexp.MustCompile(`(?i)\b(always|every project|all projects|everywhere|any project)\b|ทุกโปรเจ|ทุกโปรเจค|ทุกโปรเจกต์|ทุก ?repo`)

// Env is what the gates check a proposal against.
type Env struct {
	ProjectID domain.ProjectID
	TaskKey   string
	Outcome   domain.LearnOutcome
	// Home, Learned (~/.ao/learned) and KnowledgeDir (~/.ao/knowledge) are
	// the roots targets resolve against.
	Home         string
	Learned      string
	KnowledgeDir string
	Skills       []skills.Skill
	Shaped       Shaped
	Protected    []domain.LearnProtectedRule
	Proposals    []domain.LearnProposal
	Redactor     *redact.Redactor
	// Read returns a file's content, and false when it does not exist.
	Read func(path string) (string, bool, error)
	// Resolve follows symlinks (filepath.EvalSymlinks in production).
	Resolve func(path string) (string, error)
}

// Candidate is a proposal on its way through the gates. Drop is set when one
// refused it. Added is the text the proposal adds, which the content gates and
// the verifier look at.
type Candidate struct {
	Ref      string
	Proposal domain.LearnProposal
	Added    string
	Drop     string
	// base is the target's content the diff is computed against.
	base string
}

// Prepare turns decide's answer into candidates: it resolves each target,
// renders the content AO would write, computes the diff, clamps the scope and
// applies every gate that needs no model. no_action answers are counted, not
// returned.
func Prepare(env Env, answer []Proposed) (out []Candidate, noAction int) {
	ruleEdit := map[string]int{}
	for i, p := range answer {
		if p.Action == "no_action" {
			noAction++
			continue
		}
		c := Candidate{Ref: "p" + strconv.Itoa(i+1), Proposal: domain.LearnProposal{
			ProjectID: env.ProjectID, TaskKey: env.TaskKey, Action: domain.LearnProposalAction(p.Action),
			Title: strings.TrimSpace(p.Title), Rationale: strings.TrimSpace(p.Rationale), Outcome: env.Outcome,
			Confidence: clamp01(p.Confidence) * env.Outcome.Weight(), Status: domain.LearnProposalPending,
		}}
		for _, v := range p.RuleVerdicts {
			c.Proposal.RuleVerdicts = append(c.Proposal.RuleVerdicts, domain.LearnRuleVerdict{RuleID: v.Rule, Verdict: v.Verdict, Note: v.Note})
		}
		c.Drop = env.prepare(&c, p)
		if c.Drop == "" && c.Proposal.Action == domain.LearnProposeEditRuleFile {
			if j, ok := ruleEdit[c.Proposal.TargetPath]; ok {
				// Lines one task adds to one file are one proposal: the
				// person approves the file's change as a whole.
				mergeRuleEdit(&out[j], c, p.UnderHeading)
				continue
			}
			ruleEdit[c.Proposal.TargetPath] = len(out)
		}
		out = append(out, c)
	}
	return out, noAction
}

// mergeRuleEdit folds a second edit of the same rule file into the first:
// its lines are inserted into the first's new content, the diff recomputed
// against the same base, and evidence, titles and rationales joined.
func mergeRuleEdit(into *Candidate, c Candidate, underHeading string) {
	p := &into.Proposal
	p.NewContent = InsertUnder(p.NewContent, underHeading, c.Added)
	p.Diff = Diff(p.TargetPath, into.base, p.NewContent)
	into.Added += "\n" + c.Added
	p.EvidenceIDs = union(p.EvidenceIDs, c.Proposal.EvidenceIDs)
	p.Title += "; " + c.Proposal.Title
	p.Rationale += " " + c.Proposal.Rationale
	p.RuleVerdicts = append(p.RuleVerdicts, c.Proposal.RuleVerdicts...)
	p.Confidence = max(p.Confidence, c.Proposal.Confidence)
}

func (env Env) prepare(c *Candidate, p Proposed) string {
	scope, why := env.evidence(c, p)
	if why != "" {
		c.Proposal.Scope = p.Scope
		c.Proposal.TargetPath = p.Target
		return why
	}
	c.Proposal.Scope = scope
	switch c.Proposal.Action {
	case domain.LearnProposeCreateSkill:
		return env.createSkill(c, p)
	case domain.LearnProposeUpdateSkill:
		return env.updateSkill(c, p)
	case domain.LearnProposeEditRuleFile:
		return env.editRuleFile(c, p)
	case domain.LearnProposeConflict:
		return env.conflict(c, p.Target, p.Content)
	default:
		return fmt.Sprintf("unknown action %q", p.Action)
	}
}

// evidence checks what the proposal rests on (decisions 6, 8, 9) and returns
// the scope it may have.
func (env Env) evidence(c *Candidate, p Proposed) (string, string) {
	var ev []domain.LearnDraft
	seen := map[int64]bool{}
	for _, ref := range p.Evidence {
		id, ok := ParseDraftID(ref)
		if !ok || seen[id] {
			continue
		}
		d, ok := env.Shaped.Drafts[id]
		if !ok {
			continue
		}
		seen[id] = true
		ev = append(ev, d)
		c.Proposal.EvidenceIDs = append(c.Proposal.EvidenceIDs, id)
	}
	if len(ev) == 0 {
		return "", "cites no draft it was given"
	}
	own, strong := false, false
	tasks, projects := map[string]bool{}, map[domain.ProjectID]bool{}
	primary, saidEverywhere := false, false
	for _, d := range ev {
		if d.TaskKey == env.TaskKey {
			own = true
		}
		if !d.Weak && (d.AnchorSourceClass == domain.LearnSourceTyped || d.AnchorSourceClass == domain.LearnSourceQueued) {
			strong = true
		}
		// A product decision, a one-off or a question is never a lesson's
		// basis, and an accepted suggestion never stands alone.
		if d.Weak || (d.About != "" && d.About != domain.LearnAboutAgentPractice) {
			continue
		}
		tasks[d.TaskKey] = true
		projects[d.ProjectID] = true
		if d.Confidence >= StandAlone {
			primary = true
		}
		if everywhere.MatchString(d.Quote) {
			saidEverywhere = true
		}
	}
	switch {
	case !own:
		return "", "rests on no draft of this task"
	case !strong:
		return "", "needs at least one turn the person typed (an accepted suggestion is weak evidence)"
	case len(tasks) == 0:
		return "", "rests only on drafts tagged as product decisions, one-offs or questions, or on accepted suggestions"
	case !primary && len(tasks) < 2:
		return "", fmt.Sprintf("no draft reaches confidence %.2f and no other task corroborates it", StandAlone)
	}
	if p.Scope == "global" && (len(projects) >= 2 || saidEverywhere) {
		return "global", ""
	}
	return "project:" + string(env.ProjectID), ""
}

func (env Env) learnedSkillPath(scope, name string) string {
	if scope == "global" {
		return filepath.Join(env.Learned, "global", "skills", name, "SKILL.md")
	}
	return filepath.Join(env.Learned, "projects", string(env.ProjectID), "skills", name, "SKILL.md")
}

func (env Env) createSkill(c *Candidate, p Proposed) string {
	name := strings.TrimSpace(p.SkillName)
	path := env.learnedSkillPath(c.Proposal.Scope, name)
	c.Proposal.TargetPath = path
	for _, s := range env.Skills {
		if s.Name != name {
			continue
		}
		if s.Path == path {
			c.Proposal.Action = domain.LearnProposeUpdateSkill
			return env.updateSkill(c, Proposed{Target: path, Content: p.Content})
		}
		return fmt.Sprintf("a skill named %q already exists (%s, %s); a new one must not shadow it", name, s.Source, s.Path)
	}
	learned := 0
	for _, s := range env.Skills {
		if s.Source == skills.SourceLearned && s.Scope == c.Proposal.Scope {
			learned++
		}
	}
	prefix := filepath.Dir(filepath.Dir(path))
	for _, pr := range env.Proposals {
		if pr.Status == domain.LearnProposalPending && pr.Action == domain.LearnProposeCreateSkill && strings.HasPrefix(pr.TargetPath, prefix+string(filepath.Separator)) && pr.TargetPath != path {
			learned++
		}
	}
	limit := skills.MaxProjectLearned
	if c.Proposal.Scope == "global" {
		limit = skills.MaxGlobalLearned
	}
	if learned >= limit {
		return fmt.Sprintf("%s already has %d learned skills, the cap; only updates are allowed", c.Proposal.Scope, learned)
	}
	return env.skillContent(c, name, "", p.Content)
}

func (env Env) updateSkill(c *Candidate, p Proposed) string {
	target := env.expand(p.Target)
	c.Proposal.TargetPath = target
	var skill *skills.Skill
	for i := range env.Skills {
		if env.Skills[i].Path == target {
			skill = &env.Skills[i]
			break
		}
	}
	switch {
	case skill == nil:
		return "the target is not a known skill file"
	case skill.Source != skills.SourceUser && skill.Source != skills.SourceLearned:
		return fmt.Sprintf("a %s skill is not the person's to change; only user and learned skills are", skill.Source)
	case skill.Scope == "global" && c.Proposal.Scope != "global":
		return "a lesson from one project cannot change a skill every project loads"
	case skill.Scope != "global" && skill.Scope != "project:"+string(env.ProjectID):
		return "the skill belongs to another project"
	}
	c.Proposal.Scope = skill.Scope
	if why := env.confined(target, filepath.Join(env.Home, ".claude", "skills"), env.Learned); why != "" {
		return why
	}
	old, ok, err := env.Read(target)
	if err != nil || !ok {
		return "the skill file cannot be read"
	}
	return env.skillContent(c, skill.Name, old, p.Content)
}

// skillContent checks a skill file and computes what it adds.
func (env Env) skillContent(c *Candidate, name, old, content string) string {
	content = noEmDash(strings.TrimSpace(content)) + "\n"
	fm, err := skills.Check(content)
	if err != nil {
		return "skill file: " + err.Error()
	}
	if fm.Name != name {
		return fmt.Sprintf("the frontmatter name %q does not match the skill %q", fm.Name, name)
	}
	c.Proposal.NewContent = content
	c.Proposal.BaseSHA256 = sha(old)
	c.Proposal.Diff = Diff(c.Proposal.TargetPath, old, content)
	c.Added = added(old, content)
	if c.Proposal.Diff == "" {
		return "it changes nothing"
	}
	return env.contentGates(c)
}

func (env Env) editRuleFile(c *Candidate, p Proposed) string {
	target := env.expand(p.Target)
	c.Proposal.TargetPath = target
	claudeMD := filepath.Join(env.Home, ".claude", "CLAUDE.md")
	index := filepath.Join(env.KnowledgeDir, string(env.ProjectID), "INDEX.md")
	switch target {
	case claudeMD:
		if IsOrchestrator(env.TaskKey) {
			// Decision 5: an orchestrator's day may not edit what every
			// session is told; the person decides it as a conflict card.
			return env.conflict(c, "file:"+claudeMD, p.Content)
		}
		if c.Proposal.Scope != "global" {
			return "a lesson from one project cannot change the global CLAUDE.md"
		}
	case index:
		c.Proposal.Scope = "project:" + string(env.ProjectID)
	default:
		return "a rule file edit may target only ~/.claude/CLAUDE.md or this project's knowledge INDEX.md"
	}
	if why := env.confined(target, filepath.Dir(claudeMD), env.KnowledgeDir); why != "" {
		return why
	}
	old, _, err := env.Read(target)
	if err != nil {
		return "the rule file cannot be read"
	}
	text := noEmDash(strings.TrimSpace(p.Content))
	if text == "" {
		return "it adds nothing"
	}
	c.Proposal.NewContent = InsertUnder(old, p.UnderHeading, text)
	c.base = old
	c.Proposal.BaseSHA256 = sha(old)
	c.Proposal.Diff = Diff(target, old, c.Proposal.NewContent)
	c.Added = text
	return env.contentGates(c)
}

// conflict makes the candidate a conflict card about rule (a rule id, a
// "protected-<n>" id, or "file:<path>"), carrying the person's new rule.
func (env Env) conflict(c *Candidate, rule, content string) string {
	rule = strings.TrimSpace(rule)
	switch {
	case strings.HasPrefix(rule, "file:"):
	case strings.HasPrefix(rule, "protected-"):
		ok := false
		for _, p := range env.Protected {
			if "protected-"+strconv.FormatInt(p.ID, 10) == rule {
				ok = true
			}
		}
		if !ok {
			return "the conflict names a protected rule that does not exist"
		}
	default:
		if _, ok := env.Shaped.Rules[rule]; !ok {
			return "the conflict names a rule it was not given"
		}
	}
	c.Proposal.Action = domain.LearnProposeConflict
	c.Proposal.TargetPath = "rule:" + rule
	c.Proposal.NewContent = noEmDash(strings.TrimSpace(content))
	c.Proposal.Diff, c.Proposal.BaseSHA256 = "", ""
	c.Added = c.Proposal.NewContent
	if c.Added == "" {
		return "the conflict states no new rule"
	}
	return env.sensitive(c)
}

// contentGates are the deterministic checks on what a proposal adds.
func (env Env) contentGates(c *Candidate) string {
	if hits := rules.Forbidden(c.Added, env.ProjectID, env.Protected); len(hits) > 0 {
		// The person's words contradict a rule they pinned: that is theirs to
		// decide, never something to write.
		c.Proposal.RuleVerdicts = append(c.Proposal.RuleVerdicts, domain.LearnRuleVerdict{
			RuleID: "protected-" + strconv.FormatInt(hits[0].RuleID, 10), Verdict: "contradicts",
			Note: fmt.Sprintf("adds %q, which the pattern %s forbids", hits[0].Match, hits[0].Pattern)})
		return env.conflict(c, "protected-"+strconv.FormatInt(hits[0].RuleID, 10), c.Added)
	}
	return env.sensitive(c)
}

func (env Env) sensitive(c *Candidate) string {
	if env.Redactor == nil {
		return ""
	}
	for _, s := range []string{c.Proposal.Title, c.Proposal.Rationale, c.Added} {
		if _, counts := env.Redactor.Redact(s); len(counts) > 0 {
			kinds := make([]string, 0, len(counts))
			for k := range counts {
				kinds = append(kinds, k)
			}
			sort.Strings(kinds)
			return "contains a value that looks sensitive (" + strings.Join(kinds, ", ") + ")"
		}
	}
	return ""
}

// confined resolves symlinks and requires the result inside one of roots.
func (env Env) confined(path string, roots ...string) string {
	resolve := env.Resolve
	if resolve == nil {
		resolve = filepath.EvalSymlinks
	}
	resolved, err := resolve(path)
	if err != nil {
		// A file that does not exist yet resolves through its directory.
		dir, derr := resolve(filepath.Dir(path))
		if derr != nil {
			return ""
		}
		resolved = filepath.Join(dir, filepath.Base(path))
	}
	for _, r := range roots {
		rr, err := resolve(r)
		if err != nil {
			rr = r
		}
		if rel, err := filepath.Rel(rr, resolved); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return ""
		}
	}
	return "the target resolves to " + resolved + ", outside the directories learning may change"
}

func (env Env) expand(p string) string {
	p = strings.TrimSpace(p)
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		return filepath.Join(env.Home, rest)
	}
	return filepath.Clean(p)
}

// ApplyReviews folds the verifier's answers in: a proposal that contradicts a
// rule becomes a conflict card, one that is not grounded in the person's words
// or carries sensitive data is dropped, and one the verifier did not answer for
// is dropped too.
func ApplyReviews(env Env, cands []Candidate, reviews map[string]Review) {
	for i := range cands {
		c := &cands[i]
		if c.Drop != "" {
			continue
		}
		r, ok := reviews[c.Ref]
		c.Proposal.Verifier = domain.LearnVerifierResult{ContradictsRule: r.ContradictsRule, Grounded: r.Grounded, SensitiveData: r.Sensitive, Notes: r.Notes}
		switch {
		case !ok:
			c.Drop = "the verifier gave no answer for it"
		case r.Sensitive:
			c.Drop = "verifier: carries sensitive data. " + r.Notes
		case !r.Grounded:
			c.Drop = "verifier: not grounded in the person's own words. " + r.Notes
		case r.ContradictsRule && c.Proposal.Action != domain.LearnProposeConflict:
			rule := ""
			if len(r.ContradictedRules) > 0 {
				rule = strings.TrimSpace(r.ContradictedRules[0])
			}
			for _, id := range r.ContradictedRules {
				c.Proposal.RuleVerdicts = append(c.Proposal.RuleVerdicts, domain.LearnRuleVerdict{RuleID: id, Verdict: "contradicts", Note: r.Notes})
			}
			if why := env.conflict(c, rule, c.Added); why != "" {
				c.Drop = "verifier found a contradiction, but " + why
			}
		}
	}
}

// Finalize settles each kept candidate against the existing proposals: one
// on a target a pending proposal already holds amends it (evidence merged),
// and one whose evidence a rejected proposal on the same target already had is
// dropped - a rejection is not re-proposed without new evidence.
func Finalize(env Env, cands []Candidate) {
	claimed := map[string]int{}
	for i := range cands {
		c := &cands[i]
		if c.Drop != "" {
			c.Proposal.Status = domain.LearnProposalDropped
			c.Proposal.DropReason = c.Drop
			continue
		}
		if j, ok := claimed[c.Proposal.TargetPath]; ok {
			c.Drop = fmt.Sprintf("another proposal of this task (%s) already targets the same file", cands[j].Ref)
			c.Proposal.Status, c.Proposal.DropReason = domain.LearnProposalDropped, c.Drop
			continue
		}
		claimed[c.Proposal.TargetPath] = i
		for _, p := range env.Proposals {
			if p.TargetPath != c.Proposal.TargetPath {
				continue
			}
			switch p.Status {
			case domain.LearnProposalRejected:
				if subset(c.Proposal.EvidenceIDs, p.EvidenceIDs) {
					c.Drop = fmt.Sprintf("proposal #%d on the same target was rejected with this evidence", p.ID)
				}
			case domain.LearnProposalPending:
				c.Proposal.ID = p.ID
				c.Proposal.EvidenceIDs = union(p.EvidenceIDs, c.Proposal.EvidenceIDs)
			}
		}
		if c.Drop != "" {
			c.Proposal.ID = 0
			c.Proposal.Status, c.Proposal.DropReason = domain.LearnProposalDropped, c.Drop
		}
	}
}

func subset(a, b []int64) bool {
	in := map[int64]bool{}
	for _, x := range b {
		in[x] = true
	}
	for _, x := range a {
		if !in[x] {
			return false
		}
	}
	return len(a) > 0
}

func union(a, b []int64) []int64 {
	seen := map[int64]bool{}
	var out []int64
	for _, x := range append(append([]int64{}, a...), b...) {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}

// added is the text next adds to old: the lines the diff marks +.
func added(old, next string) string {
	var b strings.Builder
	for _, o := range lcsOps(splitLines(old), splitLines(next)) {
		if o.kind == '+' {
			b.WriteString(o.text)
		}
	}
	return b.String()
}

// noEmDash applies decision 4 to everything learning writes.
func noEmDash(s string) string {
	return strings.NewReplacer(" — ", " - ", "—", "-").Replace(s)
}

func sha(s string) string {
	if s == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func clamp01(v float64) float64 {
	switch {
	case v < 0:
		return 0
	case v > 1:
		return 1
	}
	return v
}
