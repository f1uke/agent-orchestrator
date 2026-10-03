package decide

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/learn/redact"
	"github.com/aoagents/agent-orchestrator/backend/internal/learn/skills"
)

var now = time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)

func ended(reason string, at time.Time) SessionFacts {
	return SessionFacts{Session: domain.SessionRecord{IsTerminated: true, Termination: domain.Termination{Reason: reason, At: at}}}
}

func TestAssess(t *testing.T) {
	old := now.Add(-72 * time.Hour)
	cases := []struct {
		name     string
		key      string
		sessions []SessionFacts
		newest   time.Time
		ready    bool
		outcome  domain.LearnOutcome
	}{
		{"orchestrator day over", "orch:p:2026-10-03", nil, now, true, domain.LearnOutcomeDay},
		{"orchestrator today", "orch:p:2026-10-04", nil, now, false, domain.LearnOutcomeDay},
		{"merged PR wins over a kill", "solo:s", []SessionFacts{{Session: domain.SessionRecord{IsTerminated: true, Termination: domain.Termination{Reason: domain.TerminationCauseKill, At: now}}, PRs: []domain.PullRequest{{Merged: true}}}}, now, true, domain.LearnOutcomeMerged},
		{"asleep as merged", "solo:s", []SessionFacts{{Session: domain.SessionRecord{SleepReason: domain.SleepReasonMerged}}}, now, true, domain.LearnOutcomeMerged},
		{"killed", "crew:c", []SessionFacts{ended(domain.TerminationCauseKill, now), ended(domain.TerminationCauseDevExited, now)}, now, true, domain.LearnOutcomeAbandoned},
		{"closed unmerged", "solo:s", []SessionFacts{{Session: domain.SessionRecord{IsTerminated: true}, PRs: []domain.PullRequest{{Closed: true}}}}, now, true, domain.LearnOutcomeAbandoned},
		{"unknown, recent", "solo:s", []SessionFacts{ended(domain.TerminationCauseRuntimeMissing, now.Add(-time.Hour))}, now, false, domain.LearnOutcomeUnknown},
		{"unknown, waited", "solo:s", []SessionFacts{ended(domain.TerminationCauseRuntimeMissing, old)}, old, true, domain.LearnOutcomeUnknown},
		{"still running", "solo:s", []SessionFacts{{}}, now.Add(-time.Hour), false, domain.LearnOutcomeOngoing},
		{"still running, daily cut", "solo:s", []SessionFacts{{}}, now.Add(-25 * time.Hour), true, domain.LearnOutcomeOngoing},
		{"rows gone", "solo:s", nil, old, true, domain.LearnOutcomeUnknown},
	}
	for _, c := range cases {
		got := Assess(c.key, c.sessions, c.newest, now)
		if got.Ready != c.ready || got.Outcome != c.outcome {
			t.Errorf("%s: %+v", c.name, got)
		}
	}
}

func TestDiffAndInsertUnder(t *testing.T) {
	old := "# Rules\n\n## Git\n\n- Squash before merge.\n\n## Simulators\n\n- Use scripts.\n"
	got := InsertUnder(old, "git", "- Never force-push main.")
	want := "# Rules\n\n## Git\n\n- Squash before merge.\n- Never force-push main.\n\n## Simulators\n\n- Use scripts.\n"
	if got != want {
		t.Fatalf("insert =\n%q\nwant\n%q", got, want)
	}
	if got := InsertUnder("x\n", "New place", "- rule"); got != "x\n\n## New place\n\n- rule\n" {
		t.Errorf("a missing heading becomes a new section: %q", got)
	}
	d := Diff("/h/.claude/CLAUDE.md", old, want)
	if !strings.Contains(d, "--- a/h/.claude/CLAUDE.md\n+++ b/h/.claude/CLAUDE.md\n@@ -3,6 +3,7 @@\n") || !strings.Contains(d, "\n+- Never force-push main.\n") {
		t.Errorf("diff =\n%s", d)
	}
	if Diff("p", "a\n", "a\n") != "" {
		t.Error("no change, no diff")
	}
	if d := Diff("p", "", "new\n"); !strings.HasPrefix(d, "--- /dev/null\n+++ b/p\n@@ -0,0 +1,1 @@\n+new\n") {
		t.Errorf("a new file diffs from /dev/null: %q", d)
	}
}

// rig builds an Env over a temporary home with one user skill, one learned
// skill and a CLAUDE.md.
func rig(t *testing.T, drafts ...domain.LearnDraft) Env {
	t.Helper()
	home := t.TempDir()
	write := func(p, s string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	userSkill := filepath.Join(home, ".claude", "skills", "release", "SKILL.md")
	write(userSkill, "---\nname: release\ndescription: Cut a release. Use when releasing.\n---\n\n1. Bump the version.\n")
	write(filepath.Join(home, ".claude", "CLAUDE.md"), "# Global\n\n## Git\n\n- Squash.\n")
	learned := filepath.Join(home, ".ao", "learned")
	env := Env{
		ProjectID: "nter", TaskKey: "solo:s1", Outcome: domain.LearnOutcomeMerged, Home: home, Learned: learned,
		KnowledgeDir: filepath.Join(home, ".ao", "knowledge"),
		Skills: []skills.Skill{
			{Name: "release", Description: "Cut a release. Use when releasing.", Source: skills.SourceUser, Scope: "global", Path: userSkill},
			{Name: "pdf", Source: skills.SourcePlugin, Scope: "global", Path: "/plugins/pdf/SKILL.md"},
		},
		Shaped:    Shaped{Drafts: map[int64]domain.LearnDraft{}, Rules: map[string]domain.LearnRule{"abc-0": {ID: "abc-0"}}},
		Protected: []domain.LearnProtectedRule{{ID: 1, Text: "Drive simulators through scripts.", Patterns: []string{`\bao sim tap\b`}}},
		Redactor:  redact.New(nil),
		Read: func(p string) (string, bool, error) {
			b, err := os.ReadFile(p)
			if os.IsNotExist(err) {
				return "", false, nil
			}
			return string(b), err == nil, err
		},
	}
	for _, d := range drafts {
		env.Shaped.Drafts[d.ID] = d
	}
	return env
}

func draft(id int64, task string, conf float64, about domain.LearnDraftAbout) domain.LearnDraft {
	return domain.LearnDraft{ID: id, ProjectID: "nter", TaskKey: task, Confidence: conf, About: about,
		AnchorSourceClass: domain.LearnSourceTyped, Statement: "s", Quote: "q"}
}

const goodSkill = "---\nname: verify-on-device\ndescription: Check a change on the simulator. Use when verifying a UI change.\n---\n\n1. Run the Maestro script for the screen.\n"

func TestPrepare_EvidenceGates(t *testing.T) {
	weak := draft(5, "solo:s1", 0.9, domain.LearnAboutAgentPractice)
	weak.Weak, weak.AnchorSourceClass = true, domain.LearnSourceSuggestionAccepted
	env := rig(t,
		draft(1, "solo:s1", 0.8, domain.LearnAboutAgentPractice),
		draft(2, "solo:s1", 0.5, domain.LearnAboutAgentPractice),
		draft(3, "solo:other", 0.5, domain.LearnAboutAgentPractice),
		draft(4, "solo:s1", 0.9, domain.LearnAboutProductDecision),
		weak,
		draft(6, "solo:other", 0.9, domain.LearnAboutAgentPractice),
	)
	create := func(ev ...string) Proposed {
		return Proposed{Action: "create_skill", SkillName: "verify-on-device", Scope: "project", Title: "t", Evidence: ev, Content: goodSkill, Confidence: 0.8}
	}
	cases := map[string]struct {
		p    Proposed
		drop string
	}{
		"strong own draft":           {create("d1"), ""},
		"low confidence alone":       {create("d2"), "no draft reaches confidence"},
		"corroborated by other task": {create("d2", "d3"), ""},
		"product decision only":      {create("d4"), "product decisions"},
		"accepted suggestion only":   {create("d5"), "typed"},
		"other task only":            {create("d6"), "no draft of this task"},
		"invented id":                {create("d99"), "cites no draft"},
	}
	for name, c := range cases {
		got, _ := Prepare(env, []Proposed{c.p})
		if (c.drop == "") != (got[0].Drop == "") || !strings.Contains(got[0].Drop, c.drop) {
			t.Errorf("%s: drop = %q, want %q", name, got[0].Drop, c.drop)
		}
	}
	got, noAction := Prepare(env, []Proposed{create("d1"), {Action: "no_action"}})
	if noAction != 1 || len(got) != 1 {
		t.Fatalf("no_action is counted, not returned: %d %d", noAction, len(got))
	}
	p := got[0].Proposal
	if p.TargetPath != filepath.Join(env.Learned, "projects", "nter", "skills", "verify-on-device", "SKILL.md") || p.Scope != "project:nter" {
		t.Errorf("target %s scope %s", p.TargetPath, p.Scope)
	}
	if !strings.HasPrefix(p.Diff, "--- /dev/null") || p.Confidence != 0.8 {
		t.Errorf("diff %q confidence %v", p.Diff, p.Confidence)
	}
}

func TestPrepare_ScopeTargetsAndContent(t *testing.T) {
	always := draft(7, "solo:s1", 0.9, domain.LearnAboutAgentPractice)
	always.Quote = "always paste passwords"
	env := rig(t, draft(1, "solo:s1", 0.9, domain.LearnAboutAgentPractice), always)
	global := func(p Proposed, ev string) Proposed {
		p.Scope, p.Evidence, p.Confidence = "global", []string{ev}, 1
		return p
	}

	got, _ := Prepare(env, []Proposed{
		global(Proposed{Action: "create_skill", SkillName: "verify-on-device", Content: goodSkill}, "d1"),
		global(Proposed{Action: "edit_rule_file", Target: "~/.claude/CLAUDE.md", UnderHeading: "Git", Content: "- Paste passwords."}, "d7"),
		global(Proposed{Action: "update_skill", Target: "/plugins/pdf/SKILL.md", Content: goodSkill}, "d7"),
		global(Proposed{Action: "edit_rule_file", Target: "/etc/hosts", Content: "x"}, "d7"),
		global(Proposed{Action: "create_skill", SkillName: "release", Content: goodSkill}, "d7"),
		global(Proposed{Action: "create_skill", SkillName: "tapper", Content: "---\nname: tapper\ndescription: Tap. Use when testing.\n---\n\nThen ao sim tap the button.\n"}, "d7"),
		global(Proposed{Action: "create_skill", SkillName: "bad", Content: "---\nname: bad\ndescription: No trigger.\n---\n\nBody.\n"}, "d7"),
		global(Proposed{Action: "edit_rule_file", Target: "~/.claude/CLAUDE.md", UnderHeading: "Git", Content: "- Mail me at someone@example.com"}, "d7"),
	})
	if got[0].Drop != "" || got[0].Proposal.Scope != "project:nter" {
		t.Errorf("global without two projects or 'always' is clamped to project: %+v", got[0])
	}
	if got[1].Drop != "" || got[1].Proposal.Scope != "global" || !strings.Contains(got[1].Proposal.NewContent, "- Squash.\n- Paste passwords.\n") {
		t.Errorf("'always' allows global; the rule is inserted under its heading: %q %q", got[1].Drop, got[1].Proposal.NewContent)
	}
	wants := []string{"", "", "plugin skill", "only ~/.claude/CLAUDE.md", "already exists", "", "use the skill", "sensitive (email)"}
	for i := 2; i < len(wants); i++ {
		if wants[i] == "" {
			continue
		}
		if !strings.Contains(got[i].Drop, wants[i]) {
			t.Errorf("proposal %d: drop = %q, want %q", i, got[i].Drop, wants[i])
		}
	}
	if got[5].Drop != "" || got[5].Proposal.Action != domain.LearnProposeConflict || got[5].Proposal.TargetPath != "rule:protected-1" {
		t.Errorf("a forbidden pattern makes a conflict card, never a skill: %+v", got[5])
	}
}

func TestReviewsAndFinalize(t *testing.T) {
	env := rig(t, draft(1, "solo:s1", 0.9, domain.LearnAboutAgentPractice), draft(2, "solo:s1", 0.9, domain.LearnAboutAgentPractice))
	target := filepath.Join(env.Learned, "projects", "nter", "skills", "verify-on-device", "SKILL.md")
	env.Proposals = []domain.LearnProposal{{ID: 40, TargetPath: target, Status: domain.LearnProposalPending, EvidenceIDs: []int64{9}}}
	p := Proposed{Action: "create_skill", SkillName: "verify-on-device", Scope: "project", Evidence: []string{"d1"}, Content: goodSkill, Confidence: 1}
	cands, _ := Prepare(env, []Proposed{p, p, p, p})
	ApplyReviews(env, cands, map[string]Review{
		"p1": {Grounded: true},
		"p2": {Grounded: false, Notes: "agent's idea"},
		"p3": {Grounded: true, ContradictsRule: true, ContradictedRules: []string{"abc-0"}},
	})
	if cands[1].Drop == "" || cands[3].Drop == "" {
		t.Errorf("ungrounded and unanswered must drop: %q / %q", cands[1].Drop, cands[3].Drop)
	}
	if cands[2].Proposal.Action != domain.LearnProposeConflict || cands[2].Proposal.TargetPath != "rule:abc-0" {
		t.Errorf("a contradiction becomes a conflict card: %+v", cands[2].Proposal)
	}
	Finalize(env, cands)
	if cands[0].Proposal.ID != 40 || len(cands[0].Proposal.EvidenceIDs) != 2 {
		t.Errorf("a pending proposal on the same target is amended with merged evidence: %+v", cands[0].Proposal)
	}
	if cands[1].Proposal.Status != domain.LearnProposalDropped || cands[1].Proposal.DropReason == "" {
		t.Errorf("dropped candidates keep their reason: %+v", cands[1].Proposal)
	}

	env.Proposals = []domain.LearnProposal{{ID: 41, TargetPath: target, Status: domain.LearnProposalRejected, EvidenceIDs: []int64{1, 2}}}
	again, _ := Prepare(env, []Proposed{p})
	ApplyReviews(env, again, map[string]Review{"p1": {Grounded: true}})
	Finalize(env, again)
	if !strings.Contains(again[0].Drop, "rejected") {
		t.Errorf("a rejection is not re-proposed on the same evidence: %q", again[0].Drop)
	}
}

func TestPrepare_MergesOneTasksEditsOfTheSameFile(t *testing.T) {
	a, b := draft(1, "solo:s1", 0.9, domain.LearnAboutAgentPractice), draft(2, "solo:s1", 0.9, domain.LearnAboutAgentPractice)
	a.Quote, b.Quote = "always squash", "always paste passwords"
	env := rig(t, a, b)
	edit := func(ev, heading, text string) Proposed {
		return Proposed{Action: "edit_rule_file", Target: "~/.claude/CLAUDE.md", UnderHeading: heading, Content: text,
			Scope: "global", Title: text, Evidence: []string{ev}, Confidence: 0.8}
	}
	got, _ := Prepare(env, []Proposed{edit("d1", "Git", "- one"), edit("d2", "Secrets", "- two")})
	if len(got) != 1 || got[0].Drop != "" {
		t.Fatalf("candidates = %+v", got)
	}
	p := got[0].Proposal
	if len(p.EvidenceIDs) != 2 || !strings.Contains(p.NewContent, "- Squash.\n- one\n") || !strings.Contains(p.NewContent, "## Secrets\n\n- two\n") ||
		!strings.Contains(p.Diff, "+- one") || !strings.Contains(p.Diff, "+- two") {
		t.Errorf("merged = %+v", p)
	}
}

func TestPrepare_ProjectRulesNeverGoToTheKnowledgeStore(t *testing.T) {
	env := rig(t, draft(1, "solo:s1", 0.9, domain.LearnAboutAgentPractice))
	got, _ := Prepare(env, []Proposed{{Action: "edit_rule_file", Target: filepath.Join(env.KnowledgeDir, "nter", "INDEX.md"),
		Content: "- rule", Scope: "project", Evidence: []string{"d1"}, Confidence: 0.8}})
	if !strings.Contains(got[0].Drop, "learned skill of the project") {
		t.Errorf("drop = %q", got[0].Drop)
	}
}

func TestAddProjectRule_BuildsTheWorkingRulesSkillAndNeverLosesLines(t *testing.T) {
	env := rig(t, draft(1, "solo:s1", 0.9, domain.LearnAboutAgentPractice), draft(2, "solo:s1", 0.9, domain.LearnAboutAgentPractice))
	rule := func(ev, text string) Proposed {
		return Proposed{Action: "add_project_rule", Content: text, Scope: "project", Title: text, Evidence: []string{ev}, Confidence: 0.8}
	}
	got, _ := Prepare(env, []Proposed{rule("d1", "- Open finished diagrams in Chrome."), rule("d2", "- Hand finished work to QA.")})
	if len(got) != 1 || got[0].Drop != "" {
		t.Fatalf("one task's project rules are one proposal: %+v", got)
	}
	first := got[0].Proposal
	path := filepath.Join(env.Learned, "projects", "nter", "skills", "nter-working-rules", "SKILL.md")
	if first.TargetPath != path || first.Action != domain.LearnProposeCreateSkill || first.Scope != "project:nter" {
		t.Fatalf("first = %+v", first)
	}
	if _, err := skills.Check(first.NewContent); err != nil || !strings.Contains(first.NewContent, "# nter working rules\n\n- Open finished diagrams in Chrome.\n- Hand finished work to QA.\n") {
		t.Fatalf("skill =\n%s\n%v", first.NewContent, err)
	}

	// Another task adds a rule while the first proposal is pending; a third,
	// decided in parallel, sees the pending one only at commit time.
	pending := first
	pending.ID, pending.Status = 7, domain.LearnProposalPending
	env.TaskKey = "solo:s2"
	d3 := draft(3, "solo:s2", 0.9, domain.LearnAboutAgentPractice)
	env.Shaped.Drafts[3] = d3
	stale, _ := Prepare(env, []Proposed{rule("d3", "- Never run ao session cleanup.")})
	env.Proposals = []domain.LearnProposal{pending}
	Finalize(env, stale)
	got3 := stale[0].Proposal
	if got3.ID != 7 || !strings.Contains(got3.NewContent, "- Open finished diagrams in Chrome.\n- Hand finished work to QA.\n- Never run ao session cleanup.\n") ||
		!strings.HasPrefix(got3.Diff, "--- /dev/null") {
		t.Errorf("amend must keep the pending lines and add its own:\n%s", got3.NewContent)
	}

	// A full-file skill update that would drop a pending proposal's lines is refused.
	full := Proposed{Action: "update_skill", Target: env.Skills[0].Path, Scope: "global", Evidence: []string{"d3"}, Confidence: 0.9,
		Content: "---\nname: release\ndescription: Cut a release. Use when releasing.\n---\n\n1. Bump the version.\n2. Tag it.\n"}
	d3.Quote = "always tag releases"
	env.Shaped.Drafts[3] = d3
	upd, _ := Prepare(env, []Proposed{full})
	env.Proposals = []domain.LearnProposal{{ID: 8, TargetPath: env.Skills[0].Path, Status: domain.LearnProposalPending,
		NewContent: "---\nname: release\ndescription: Cut a release. Use when releasing.\n---\n\n1. Bump the version.\n2. Write the changelog.\n"}}
	Finalize(env, upd)
	if !strings.Contains(upd[0].Drop, "would drop lines open proposal #8 adds") {
		t.Errorf("drop = %q", upd[0].Drop)
	}
}

func TestAddProjectRule_RefusedEarlyIsStillAStorableAction(t *testing.T) {
	env := rig(t, draft(1, "solo:s1", 0.5, domain.LearnAboutAgentPractice))
	got, _ := Prepare(env, []Proposed{{Action: "add_project_rule", Content: "- x", Evidence: []string{"d1"}, Confidence: 0.9}})
	if got[0].Drop == "" || got[0].Proposal.Action != domain.LearnProposeUpdateSkill {
		t.Errorf("a refused project rule must keep a storable action: %+v", got[0].Proposal)
	}
}
