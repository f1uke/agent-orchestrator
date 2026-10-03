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

// rig builds an Env over a temporary home with a user skill, a CLAUDE.md and
// a project memory directory holding one memory file.
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
	memDir := filepath.Join(home, ".claude", "projects", "-repo-nter", "memory")
	write(filepath.Join(memDir, "MEMORY.md"), "- [QA handoff](feedback_qa.md) - hand finished work to qa\n")
	write(filepath.Join(memDir, "feedback_qa.md"), "---\nname: feedback-qa\ndescription: \"Hand finished work to qa\"\nmetadata:\n  type: feedback\n---\n\nHand finished work to qa.\n")
	env := Env{
		ProjectID: "nter", TaskKey: "solo:s1", Outcome: domain.LearnOutcomeMerged, Home: home,
		KnowledgeDir: filepath.Join(home, ".ao", "knowledge"), MemoryDir: memDir,
		Now: func() time.Time { return now },
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

func memory(ev ...string) Proposed {
	return Proposed{Action: "create_memory", MemoryType: "feedback", Name: "Verify on device", Title: "Verify on device",
		Description: "Check a UI change on the simulator through its script", Content: "Run the Maestro script for the screen.\n\n**Why:** the person said so.",
		Scope: "project", Evidence: ev, Confidence: 0.8}
}

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
	cases := map[string]struct {
		p    Proposed
		drop string
	}{
		"strong own draft":           {memory("d1"), ""},
		"low confidence alone":       {memory("d2"), "no draft reaches confidence"},
		"corroborated by other task": {memory("d2", "d3"), ""},
		"product decision only":      {memory("d4"), "product decisions"},
		"accepted suggestion only":   {memory("d5"), "typed"},
		"other task only":            {memory("d6"), "no draft of this task"},
		"invented id":                {memory("d99"), "cites no draft"},
	}
	for name, c := range cases {
		got, _ := Prepare(env, []Proposed{c.p})
		if (c.drop == "") != (got[0].Drop == "") || !strings.Contains(got[0].Drop, c.drop) {
			t.Errorf("%s: drop = %q, want %q", name, got[0].Drop, c.drop)
		}
	}
	got, noAction := Prepare(env, []Proposed{memory("d1"), {Action: "no_action"}})
	if noAction != 1 || len(got) != 1 {
		t.Fatalf("no_action is counted, not returned: %d %d", noAction, len(got))
	}
}

func TestCreateMemory_WritesClaudeCodesFormatAndTheIndexLine(t *testing.T) {
	env := rig(t, draft(1, "solo:s1", 0.9, domain.LearnAboutAgentPractice))
	got, _ := Prepare(env, []Proposed{memory("d1")})
	c := got[0]
	if c.Drop != "" {
		t.Fatal(c.Drop)
	}
	p := c.Proposal
	if p.Action != domain.LearnProposeCreateMemory || p.TargetPath != filepath.Join(env.MemoryDir, "feedback_verify_on_device.md") || p.Scope != "project:nter" {
		t.Errorf("proposal = %+v", p)
	}
	want := "---\nname: feedback-verify-on-device\ndescription: \"Check a UI change on the simulator through its script\"\nmetadata:\n  node_type: memory\n  type: feedback\n  modified: 2026-10-04T12:00:00Z\n---\n\nRun the Maestro script for the screen.\n\n**Why:** the person said so.\n"
	if p.NewContent != want {
		t.Errorf("file =\n%s\nwant\n%s", p.NewContent, want)
	}
	if p.IndexLine != "- [Verify on device](feedback_verify_on_device.md) - Check a UI change on the simulator through its script" {
		t.Errorf("index line = %q", p.IndexLine)
	}
	if !strings.Contains(p.Diff, "+++ b/"+strings.TrimPrefix(filepath.Join(env.MemoryDir, "MEMORY.md"), "/")) || !strings.Contains(p.Diff, "+"+p.IndexLine) {
		t.Errorf("the diff must show the memory file and its MEMORY.md line:\n%s", p.Diff)
	}

	dup := memory("d1")
	dup.Name = "qa"
	if got, _ := Prepare(env, []Proposed{dup}); !strings.Contains(got[0].Drop, "update it instead") {
		t.Errorf("an existing memory file is updated, not created again: %q", got[0].Drop)
	}
	noDesc := memory("d1")
	noDesc.Description = ""
	if got, _ := Prepare(env, []Proposed{noDesc}); !strings.Contains(got[0].Drop, "description") {
		t.Errorf("drop = %q", got[0].Drop)
	}
	env.MemoryDir = ""
	if got, _ := Prepare(env, []Proposed{memory("d1")}); !strings.Contains(got[0].Drop, "no Claude Code memory") {
		t.Errorf("drop = %q", got[0].Drop)
	}
}

func TestUpdateMemory_OnlyThisProjectsMemoryAndKeepsItsName(t *testing.T) {
	env := rig(t, draft(1, "solo:s1", 0.9, domain.LearnAboutAgentPractice))
	file := filepath.Join(env.MemoryDir, "feedback_qa.md")
	upd := func(target, content string) Candidate {
		got, _ := Prepare(env, []Proposed{{Action: "update_memory", Target: target, Content: content, Scope: "project", Evidence: []string{"d1"}, Confidence: 0.9}})
		return got[0]
	}
	ok := upd(file, "---\nname: feedback-qa\ndescription: \"Hand finished work to qa\"\nmetadata:\n  type: feedback\n---\n\nHand finished work to qa right away, without being asked.\n")
	if ok.Drop != "" || !strings.Contains(ok.Proposal.Diff, "+Hand finished work to qa right away") || ok.Proposal.BaseSHA256 == "" {
		t.Fatalf("update = %+v", ok)
	}
	bare := upd(file, "---\nname: feedback-qa\ndescription: MR rule: hand work to qa\nmetadata:\n  type: feedback\n---\n\nHand finished work to qa within the day.\n")
	if bare.Drop != "" || !strings.Contains(bare.Proposal.NewContent, `description: "MR rule: hand work to qa"`) {
		t.Errorf("a bare description with a colon is quoted, not refused: %q\n%s", bare.Drop, bare.Proposal.NewContent)
	}
	if c := upd(file, "---\nname: renamed\n---\n\nx\n"); !strings.Contains(c.Drop, "name must stay") {
		t.Errorf("drop = %q", c.Drop)
	}
	if c := upd(filepath.Join(env.MemoryDir, "MEMORY.md"), "x"); !strings.Contains(c.Drop, "MEMORY.md is the index") {
		t.Errorf("drop = %q", c.Drop)
	}
	if c := upd(filepath.Join(env.Home, ".claude", "projects", "-repo-other", "memory", "x.md"), "x"); !strings.Contains(c.Drop, "not a memory file of this project") {
		t.Errorf("drop = %q", c.Drop)
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
	tapper := memory("d7")
	tapper.Name, tapper.Content = "tapper", "Then ao sim tap the button."
	email := memory("d7")
	email.Name, email.Content = "mail", "Mail me at someone@example.com"
	got, _ := Prepare(env, []Proposed{
		global(memory("d1"), "d1"),
		global(Proposed{Action: "edit_rule_file", Target: "~/.claude/CLAUDE.md", UnderHeading: "Git", Content: "- Paste passwords."}, "d7"),
		global(Proposed{Action: "update_skill", Target: "/plugins/pdf/SKILL.md", Content: "x"}, "d7"),
		global(Proposed{Action: "edit_rule_file", Target: "/etc/hosts", Content: "x"}, "d7"),
		global(Proposed{Action: "edit_rule_file", Target: filepath.Join(env.KnowledgeDir, "nter", "INDEX.md"), Content: "- x"}, "d7"),
		tapper,
		email,
	})
	if got[0].Drop != "" || got[0].Proposal.Scope != "project:nter" {
		t.Errorf("a memory is always the project's: %+v", got[0])
	}
	if got[1].Drop != "" || got[1].Proposal.Scope != "global" || !strings.Contains(got[1].Proposal.NewContent, "- Squash.\n- Paste passwords.\n") {
		t.Errorf("'always' allows a global rule, inserted under its heading: %q %q", got[1].Drop, got[1].Proposal.NewContent)
	}
	for i, want := range map[int]string{2: "plugin skill", 3: "only ~/.claude/CLAUDE.md", 4: "live in its memory", 6: "sensitive (email)"} {
		if !strings.Contains(got[i].Drop, want) {
			t.Errorf("proposal %d: drop = %q, want %q", i, got[i].Drop, want)
		}
	}
	if got[5].Drop != "" || got[5].Proposal.Action != domain.LearnProposeConflict || got[5].Proposal.TargetPath != "rule:protected-1" || got[5].Proposal.IndexLine != "" {
		t.Errorf("a forbidden pattern makes a conflict card, never a memory: %+v", got[5])
	}
}

func TestReviewsAndFinalize(t *testing.T) {
	env := rig(t, draft(1, "solo:s1", 0.9, domain.LearnAboutAgentPractice), draft(2, "solo:s1", 0.9, domain.LearnAboutAgentPractice))
	target := filepath.Join(env.MemoryDir, "feedback_verify_on_device.md")
	first, _ := Prepare(env, []Proposed{memory("d1")})
	pendingContent := first[0].Proposal.NewContent
	env.Proposals = []domain.LearnProposal{{ID: 40, TargetPath: target, Status: domain.LearnProposalPending, EvidenceIDs: []int64{9}, NewContent: pendingContent}}
	p := memory("d1")
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
	env.Shaped.Rules["abc-0"] = domain.LearnRule{ID: "abc-0", LearnRuleAtom: domain.LearnRuleAtom{Text: "Never do X."}, SourceLabel: "~/.claude/CLAUDE.md"}
	cands[0].Proposal.NewContent = strings.Replace(cands[0].Proposal.NewContent, "Run the Maestro script for the screen.", "Run the Maestro script for the screen.\nKeep the run's screenshot.", 1)
	Finalize(env, cands)
	if cands[0].Proposal.ID != 40 || len(cands[0].Proposal.EvidenceIDs) != 2 {
		t.Errorf("a pending proposal on the same target is amended with merged evidence: %+v", cands[0].Proposal)
	}
	if got := cands[0].Proposal.NewContent; !strings.HasPrefix(got, pendingContent) || !strings.HasSuffix(got, "\nKeep the run's screenshot.\n") {
		t.Errorf("the same memory taught again keeps the open file and adds only its new lines:\n%s", got)
	}
	if v := cands[2].Proposal.RuleVerdicts; len(v) == 0 || v[0].RuleText != "Never do X." || v[0].RuleSource != "~/.claude/CLAUDE.md" {
		t.Errorf("the contradicted rule is snapshotted with the proposal: %+v", v)
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

	// A full-file update that would drop a line an open proposal adds is refused.
	env.Proposals = []domain.LearnProposal{{ID: 8, TargetPath: env.Skills[0].Path, Status: domain.LearnProposalPending,
		NewContent: "---\nname: release\ndescription: Cut a release. Use when releasing.\n---\n\n1. Bump the version.\n2. Write the changelog.\n"}}
	d3 := draft(3, "solo:s1", 0.9, domain.LearnAboutAgentPractice)
	d3.Quote = "always tag releases"
	env.Shaped.Drafts[3] = d3
	upd, _ := Prepare(env, []Proposed{{Action: "update_skill", Target: env.Skills[0].Path, Scope: "global", Evidence: []string{"d3"}, Confidence: 0.9,
		Content: "---\nname: release\ndescription: Cut a release. Use when releasing.\n---\n\n1. Bump the version.\n2. Tag it.\n"}})
	Finalize(env, upd)
	if !strings.Contains(upd[0].Drop, "would drop lines open proposal #8 adds") {
		t.Errorf("drop = %q", upd[0].Drop)
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
