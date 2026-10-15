package releasetrain

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
)

type fixPlanner struct {
	kind FixKind
	plan func(b *Board, h *AlertHit) FixPlan
}

// fixPlanners is the one list of fixable rules; the catalogue reads each
// rule's fix kind from it.
var fixPlanners = map[RuleID]fixPlanner{
	"A5": {FixChoose, planA5},
	"C1": {FixQueue, planRetry},
	"C6": {FixQueue, planRetry},
	"D1": {FixQueue, planD1},
	"D2": {FixQueue, planD2},
	"E1": {FixQueue, planE1},
	"G1": {FixQueue, planG1},
	"G5": {FixCopy, planG5},
	"L1": {FixQueue, planRetarget},
	"L8": {FixQueue, planRetarget},
	"L3": {FixApply, planL3},
}

func fixKindOf(rule RuleID) FixKind { return fixPlanners[rule].kind }

// fixContext is what a plan's requests need beyond the Board's wire shape.
type fixContext struct {
	gitlabHost string
	projects   map[AppID]int
	// branches is every sprint branch an MR of the app came from or went to.
	branches map[AppID][]string
}

func newFixContext(cfg Config, snap Snapshot) fixContext {
	c := fixContext{gitlabHost: cfg.GitLabHost, projects: map[AppID]int{}, branches: map[AppID][]string{}}
	for _, a := range cfg.Apps {
		c.projects[a.ID] = a.GitLabProject
		seen := map[string]bool{}
		for _, m := range Items(snap, KindMRs, ProjectScope(a.GitLabProject)) {
			for _, br := range []string{m.SourceBranch, m.TargetBranch} {
				if _, ok := sprintOf(br); ok && !seen[br] {
					seen[br] = true
					c.branches[a.ID] = append(c.branches[a.ID], br)
				}
			}
		}
		sort.Strings(c.branches[a.ID])
	}
	return c
}

// sprintBranchFor is the observed branch whose sprint ships in train t.
func (c fixContext) sprintBranchFor(app AppID, t TrainID) string {
	for _, br := range c.branches[app] {
		if id, ok := sprintTrain(br); ok && id == t {
			return br
		}
	}
	return ""
}

// PlanFix plans the fix of hit id on b; choice picks a side of a choose plan.
// It writes nothing and reads nothing but b.
func PlanFix(b Board, id HitID, choice string) (FixPlan, error) {
	var h *AlertHit
	for i := range b.Alerts.Hits {
		if b.Alerts.Hits[i].ID == id {
			h = &b.Alerts.Hits[i]
		}
	}
	if h == nil {
		return FixPlan{}, fmt.Errorf("%w: %q", ErrNoSuchHit, id)
	}
	p, ok := fixPlanners[h.Rule]
	if !ok {
		return FixPlan{}, fmt.Errorf("%w: %s", ErrNotFixable, h.Rule)
	}
	plan := p.plan(&b, h)
	plan.Hit, plan.Rule = h.ID, h.Rule
	for _, r := range ruleTable() {
		if r.ID == h.Rule && r.Scope == scopePerApp {
			plan.App = h.App
		}
	}
	if plan.Steps == nil {
		plan.Steps = []FixStep{}
	}
	if plan.Mode == ModeChoose && choice != "" {
		found := false
		for _, c := range plan.Choices {
			if c.ID == choice {
				plan.Choice, plan.Steps, found = c.ID, c.Steps, true
			}
		}
		if !found {
			return FixPlan{}, fmt.Errorf("%w: %q", ErrUnknownChoice, choice)
		}
	}
	plan.Fingerprint = fingerprint(plan.Hit, plan.Choice, plan.Steps)
	return plan, nil
}

func blocked(title, note string, need *FixNeed) FixPlan {
	return FixPlan{Title: title, Note: note, Mode: ModeBlocked, Need: need}
}

func siteOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

func (b *Board) hitVersion(h *AlertHit) (*Train, *AppVersion) {
	s, ok := h.Subject.(SubjVersion)
	if !ok {
		return nil, nil
	}
	for i := range b.Trains {
		if t := &b.Trains[i]; t.ID == s.Train {
			v := t.version(s.App)
			return t, v
		}
	}
	return nil, nil
}

func planE1(b *Board, h *AlertHit) FixPlan {
	var jira *JiraVersionFact
	var due *Day
	switch s := h.Subject.(type) {
	case SubjVersion:
		if t, v := b.hitVersion(h); v != nil {
			jira, due = v.Jira, dueOf(t, v)
		}
	case SubjHotfix:
		for i := range b.Hotfixes {
			if hf := &b.Hotfixes[i]; hf.App == s.App && hf.Version == s.Version {
				jira, due = hf.Jira, hf.Milestone.Due
			}
		}
	}
	if jira == nil || due == nil {
		return blocked("Mark the Jira release released", "The Jira release or the milestone due date is no longer on the Board.", nil)
	}
	e := EffJiraVersionUpdate{Site: siteOf(jira.URL), VersionID: jira.ID,
		Before: VersionFields{Released: jira.Released, ReleaseDate: jira.ReleaseDate},
		After:  VersionFields{Released: true, ReleaseDate: due}}
	before := "unreleased, release date " + dayOrDash(jira.ReleaseDate)
	plan := FixPlan{Title: "Mark " + jira.Name + " released", Mode: ModeQueue,
		Steps: []FixStep{newStep("Jira release "+jira.Name, before, "released, release date "+due.String(), e)}}
	if jira.ReleaseDate != nil && *jira.ReleaseDate != *due {
		plan.Note = fmt.Sprintf("The Jira date (%s) differs from the milestone due (%s). This uses the milestone, which is when the build shipped.", jira.ReleaseDate, due)
	}
	return plan
}

func dayOrDash(d *Day) string {
	if d == nil {
		return "-"
	}
	return d.String()
}

func planA5(b *Board, h *AlertHit) FixPlan {
	_, v := b.hitVersion(h)
	if v == nil || v.Jira == nil || v.Jira.ReleaseDate == nil || v.Milestone.Due == nil {
		return blocked("Make the dates match", "The Jira release or the milestone is no longer on the Board.", nil)
	}
	j, ms := v.Jira, v.Milestone
	toJira := EffJiraVersionUpdate{Site: siteOf(j.URL), VersionID: j.ID,
		Before: VersionFields{Released: j.Released, ReleaseDate: j.ReleaseDate},
		After:  VersionFields{Released: j.Released, ReleaseDate: ms.Due}}
	toGitLab := EffGitLabMilestoneDue{Host: b.fix.gitlabHost, App: v.App, Project: ms.Project, MilestoneID: ms.ID, Before: *ms.Due, After: *j.ReleaseDate}
	return FixPlan{Title: "Make " + j.Name + "'s date match the milestone", Mode: ModeChoose,
		Note: "Which side is right is a team call, so neither is picked for you.",
		Choices: []FixChoice{
			{ID: "jira", Label: "Jira release date ← milestone due (" + ms.Due.String() + ")",
				Steps: []FixStep{newStep(j.Name, j.ReleaseDate.String(), ms.Due.String(), toJira)}},
			{ID: "gitlab", Label: "Milestone due ← Jira release date (" + j.ReleaseDate.String() + ")",
				Steps: []FixStep{newStep(ms.Title, ms.Due.String(), j.ReleaseDate.String(), toGitLab)}},
		}}
}

func planRetry(b *Board, h *AlertHit) FixPlan {
	_, v := b.hitVersion(h)
	if v == nil || v.ReleaseMR == nil || v.ReleaseMR.Jobs == nil {
		return blocked("Retry the failed job", "The release MR's jobs are no longer on the Board.", nil)
	}
	r := v.ReleaseMR
	title := fmt.Sprintf("Retry the failed job on !%d", r.MR.IID)
	var steps []FixStep
	for _, j := range r.Jobs.Jobs {
		retry := j.Status == "failed" && !j.AllowFailure
		if h.Rule == "C6" && j.Name == "semgrep-sast" && (j.Status == "failed" || j.Status == "canceled") {
			retry = true
		}
		if !retry {
			continue
		}
		if j.ID == 0 {
			return blocked(title, "The job ids are not read yet; they come with the next jobs read.", nil)
		}
		e := EffGitLabJobRetry{Host: b.fix.gitlabHost, App: v.App, Project: r.MR.Project, JobID: j.ID, Name: j.Name}
		steps = append(steps, newStep(b.appName(v.App)+" pipeline", j.Name+" "+j.Status, j.Name+" retried once", e))
	}
	if len(steps) == 0 {
		return blocked(title, "No failed job to retry: semgrep-sast is missing from the pipeline.", nil)
	}
	note := "Retry once only. If it fails again it is a real failure, not a flaky one."
	if h.Rule == "C6" {
		note = "semgrep-sast is skipped when the build fails first; retrying the build lets it run. One retry covers C1 and C6."
	}
	return FixPlan{Title: title, Mode: ModeQueue, Note: note, Steps: steps}
}

func planD1(b *Board, h *AlertHit) FixPlan {
	t, v := b.hitVersion(h)
	if v == nil || v.Jira == nil {
		return blocked("Add approvers", "The Jira release is no longer on the Board.", nil)
	}
	var prev *AppVersion
	var prevTrain TrainID
	for i := range b.Trains {
		o := &b.Trains[i]
		pv := o.version(v.App)
		if pv == nil || pv.Jira == nil || len(pv.Jira.Approvers) == 0 || !o.ID.less(t.ID) {
			continue
		}
		if prev == nil || prevTrain.less(o.ID) {
			prev, prevTrain = pv, o.ID
		}
	}
	title := "Add approvers to " + v.Jira.Name
	if prev == nil {
		return blocked(title, "No earlier release of this app has approvers to copy.", nil)
	}
	people := make([]Person, len(prev.Jira.Approvers))
	names := make([]string, len(prev.Jira.Approvers))
	for i, a := range prev.Jira.Approvers {
		people[i], names[i] = Person{AccountID: a.AccountID, Name: a.Name}, a.Name
	}
	e := EffJiraApprovers{Site: siteOf(v.Jira.URL), VersionID: v.Jira.ID, After: people}
	return FixPlan{Title: fmt.Sprintf("Add %d approvers to %s", len(people), v.Jira.Name), Mode: ModeQueue,
		Note:  "The bot normally sets these. Copying the last train's list stands in when the bot missed this release.",
		Steps: []FixStep{newStep(v.Jira.Name, "no approvers", fmt.Sprintf("%s (copied from %s)", strings.Join(names, ", "), prevTrain), e)}}
}

func planD2(b *Board, h *AlertHit) FixPlan {
	_, v := b.hitVersion(h)
	if v == nil || v.Jira == nil || v.RelatedWork == nil {
		return blocked("Attach the missing pages", "The Jira release is no longer on the Board.", nil)
	}
	have := map[string]bool{}
	for _, l := range v.RelatedWork.Links {
		have[l.Category] = true
	}
	var steps []FixStep
	for _, need := range []struct{ category, title string }{
		{CategoryScanPage, "Source code scan"}, {CategoryReleaseNote, "Release note"},
	} {
		if have[need.category] {
			continue
		}
		title := need.title + " " + v.Version
		e := EffJiraRelatedWork{Site: siteOf(v.Jira.URL), VersionID: v.Jira.ID, Category: need.category, Title: title}
		steps = append(steps, newStep(v.Jira.Name+" · "+need.category, "not attached", fmt.Sprintf("%q page", title), e))
	}
	return FixPlan{Title: "Attach the missing pages to " + v.Jira.Name, Mode: ModeQueue, Steps: steps,
		Note: "The page link is found at run time by a title search, and attached only when exactly one page matches."}
}

func planG1(b *Board, h *AlertHit) FixPlan {
	s, _ := h.Subject.(SubjCard)
	c := b.sprintCard(s.Key)
	if c == nil {
		return blocked("Move the card", "The card is no longer in a sprint row.", nil)
	}
	const to = "Ready for QA"
	e := EffJiraTransition{Site: siteOf(c.URL), Key: c.Key, From: c.Status, To: to}
	return FixPlan{Title: "Move " + c.Key + " to " + to, Mode: ModeQueue,
		Note:  "The transition id is looked up on the card at run time. QA may prefer to move it themselves.",
		Steps: []FixStep{newStep(c.Key, c.Status, to, e)}}
}

func planG5(b *Board, h *AlertHit) FixPlan {
	s, _ := h.Subject.(SubjPageFamily)
	for i := range b.Trains {
		if b.Trains[i].ID != s.Train {
			continue
		}
		for _, pg := range b.Trains[i].Pages {
			if pg.Family != s.Family {
				continue
			}
			e := EffCopyText{Target: pg.URL, Text: strings.Join(s.Keys, "\n")}
			return FixPlan{Title: fmt.Sprintf("Add %d card(s) to %s", len(s.Keys), pg.Title), Mode: ModeCopy,
				Note:  "Release train pages are edited by people, so this copies the keys to paste into the page's card table.",
				Steps: []FixStep{newStep(pg.Title, "missing", strings.Join(s.Keys, ", "), e)}}
		}
	}
	return blocked("Add the cards to the page", "The release train page is no longer on the Board.", nil)
}

type retargetMR struct {
	app            AppID
	iid            int
	source, target string
	state          string
}

func planRetarget(b *Board, h *AlertHit) FixPlan {
	s, _ := h.Subject.(SubjMR)
	title := fmt.Sprintf("Retarget !%d", s.IID)
	var mr *retargetMR
	var to string
	var why string
	if h.Rule == "L1" {
		g, br := b.llbOfMR(s)
		if br == nil {
			return blocked(title, "The MR is no longer on the Board.", nil)
		}
		for _, o := range br.outs {
			if o.IID == s.IID {
				mr = &retargetMR{s.App, o.IID, o.SourceBranch, o.TargetBranch, o.State}
			}
		}
		var train *TrainID
		switch t := g.Target.(type) {
		case LLBPlanned:
			train = &t.Train
		case LLBLandedIn:
			train = &t.Train
		}
		if e, ok := g.Target.(LLBFromEpic); ok {
			return blocked(title, fmt.Sprintf("Only an epic suggests %s for %s; confirm it as a plan first, then this fix retargets the MR.", e.Train, br.Branch),
				&FixNeed{Label: "Confirm the target train", LLB: g.ID})
		}
		if train == nil {
			return blocked(title, br.Branch+" has no target train yet, so the sprint branch to merge into is unknown.",
				&FixNeed{Label: "Set a target train first", LLB: g.ID})
		}
		to = b.fix.sprintBranchFor(s.App, *train)
		why = "the sprint branch for " + train.String()
	} else {
		mr = b.sprintMR(s)
		if n, year, ok := b.branchSprint(); ok {
			to = b.fix.sprintBranchFor(s.App, TrainID{Number: n + 1, Year: year})
			why = fmt.Sprintf("the branch of sprint %d", n)
		}
	}
	if mr == nil {
		return blocked(title, "The MR is no longer on the Board.", nil)
	}
	if mr.state == MRMerged {
		return blocked(title, "This MR is already merged, so it cannot be retargeted. Merge its change into the sprint branch by hand.", nil)
	}
	if to == "" {
		return blocked(title, "No MR has used "+why+" yet, so its name is unknown; it is never guessed from a pattern.", nil)
	}
	e := EffGitLabRetarget{Host: b.fix.gitlabHost, App: s.App, Project: b.fix.projects[s.App], IID: s.IID, From: mr.target, To: to}
	return FixPlan{Title: fmt.Sprintf("Retarget !%d to %s", s.IID, to), Mode: ModeQueue,
		Steps: []FixStep{newStep(fmt.Sprintf("%s !%d", b.appName(s.App), s.IID), mr.source+" → "+mr.target, mr.source+" → "+to, e)}}
}

func (b *Board) llbOfMR(s SubjMR) (*LLBGroup, *LongLivedBranch) {
	for gi := range b.LLBs {
		for bi := range b.LLBs[gi].Branches {
			br := &b.LLBs[gi].Branches[bi]
			if br.App != s.App {
				continue
			}
			for _, o := range br.outs {
				if o.IID == s.IID {
					return &b.LLBs[gi], br
				}
			}
		}
	}
	return nil, nil
}

func (b *Board) sprintMR(s SubjMR) *retargetMR {
	for _, row := range b.Sprints {
		for _, c := range row.Cards {
			for _, m := range c.MRs {
				if m.App == s.App && m.IID == s.IID {
					return &retargetMR{m.App, m.IID, m.Source, m.Target, m.State}
				}
			}
		}
	}
	return nil
}

// branchSprint is the sprint the sprint branch belongs to: the owning
// board's active sprint, as sprint routing reads it.
func (b *Board) branchSprint() (int, int, bool) {
	for _, row := range b.Sprints {
		if row.Board.OwnsSprintBranch {
			return sprintNumber(row.Sprint)
		}
	}
	return 0, 0, false
}

func planL3(b *Board, h *AlertHit) FixPlan {
	s, _ := h.Subject.(SubjCardOnTrain)
	group, _ := h.Evidence["group"].(PlanID)
	title := fmt.Sprintf("Set %s to target %s", group, s.Train)
	var g *LLBGroup
	for i := range b.LLBs {
		if b.LLBs[i].ID == group {
			g = &b.LLBs[i]
		}
	}
	if g == nil {
		return blocked(title, "The long-lived branch group is no longer on the Board.", nil)
	}
	if g.Plan == nil {
		return blocked(title, g.Name+" has no plan yet; save one with its branches, then this fix retargets it.",
			&FixNeed{Label: "Make a plan first", LLB: g.ID})
	}
	p := g.Plan
	train := s.Train
	e := EffAOPlanTarget{Plan: p.ID, Before: LLBTargetInput{Kind: p.TargetKind, Train: p.TargetTrain},
		After: LLBTargetInput{Kind: TargetTrain, Train: &train}, PlanVersion: p.Version}
	return FixPlan{Title: fmt.Sprintf("Set %s to target %s", p.Name, s.Train), Mode: ModeApply,
		Note:  "This is AO's own plan, so it applies now. Nothing is written to GitLab, Jira or Confluence.",
		Steps: []FixStep{newStep("Long-lived plan "+string(p.ID), targetLabel(e.Before), targetLabel(e.After), e)}}
}

func targetLabel(t LLBTargetInput) string {
	if t.Kind == TargetTrain && t.Train != nil {
		return "targets " + t.Train.String()
	}
	return string(t.Kind)
}
