package releasetrain

import (
	"fmt"
	"sort"
)

// trainWindow is where today sits against a train's freeze for one app.
type trainWindow int

const (
	windowEarly trainWindow = iota + 1
	// windowNear is 3 working days or fewer before the freeze, freeze day included.
	windowNear
	// windowMissed is after the freeze day, or once the release MR is open: the train is cut.
	windowMissed
)

// aim is one branch of a group whose plan sends it to a train, while the branch has not landed.
type aim struct {
	group   *LLBGroup
	branch  *LongLivedBranch
	train   *Train
	version *AppVersion
	freeze  *Day
	window  trainWindow
	// open is the branch's open MRs into the sprint branch the train takes.
	open []MRFact
}

// aims skips a group whose plan is unplanned or dropped, whose target is only
// guessed from an epic, or that landed; and a train with no sprint branch of its own.
func aims(b *Board) []aim {
	var out []aim
	for gi := range b.LLBs {
		g := &b.LLBs[gi]
		planned, ok := g.Target.(LLBPlanned)
		if !ok {
			continue
		}
		t := b.train(planned.Train)
		if t == nil || t.ID.Extra != 0 {
			continue
		}
		for bi := range g.Branches {
			br := &g.Branches[bi]
			if br.Gone || landedAnywhere(br) {
				continue
			}
			a := aim{group: g, branch: br, train: t, version: t.version(br.App), freeze: t.Freeze}
			if a.version != nil {
				a.freeze = freezeOf(t, a.version)
			}
			a.window = windowOf(a.freeze, a.version, b.Today)
			for _, mr := range br.outs {
				if into, ok := sprintTrain(mr.TargetBranch); ok && into == t.ID && mr.State == MROpened {
					a.open = append(a.open, mr)
				}
			}
			out = append(out, a)
		}
	}
	return out
}

func landedAnywhere(br *LongLivedBranch) bool {
	for _, mr := range br.outs {
		if mr.MergedOn != nil {
			return true
		}
	}
	return false
}

func windowOf(freeze *Day, v *AppVersion, today Day) trainWindow {
	switch {
	case v != nil && v.ReleaseMR != nil, freeze != nil && today.After(*freeze):
		return windowMissed
	case freeze != nil && workDaysLeft(today, *freeze) <= 3:
		return windowNear
	}
	return windowEarly
}

func (a aim) hit(subject Subject, text string, links ...Link) Hit {
	train := a.train.ID
	return Hit{
		Subject: subject, Train: &train, App: a.branch.App,
		Evidence: Evidence{"group": a.group.ID, "branch": a.branch.Branch, "train": a.train.ID, "freeze": a.freeze},
		Text:     text, Links: append([]Link{}, links...), cards: a.branch.cardKeys(),
	}
}

func (a aim) branchSubject() SubjLLBBranch {
	return SubjLLBBranch{Train: a.train.ID, App: a.branch.App, Branch: a.branch.Branch, Group: a.group.ID}
}

func (a aim) freezeText(today Day) string {
	if a.freeze == nil {
		return "no freeze date yet"
	}
	return fmt.Sprintf("freeze is %s, %d working day(s) left", a.freeze.label(), workDaysLeft(today, *a.freeze))
}

func severityIn(w trainWindow) Severity {
	if w == windowNear {
		return SevRisk
	}
	return SevInfo
}

var readsAim = []KindName{KindMilestones.Name, KindMRs.Name}

var ruleL10 = rule{
	ID: "L10", Group: "G", Severity: SevInfo, DefaultOn: true, Scope: scopePerApp,
	Subjects: []SubjectKind{SubjectLLBBranch},
	Mine:     MineListedCards,
	Reads:    readsAim,
	Title:    "Long-lived branch targets a train but has no MR into its sprint branch",
	Hint:     "Per app in the group. Info until 3 working days before freeze, then risk. After the freeze it is L13.",
	Check: func(b *Board) []Hit {
		var out []Hit
		for _, a := range aims(b) {
			if a.window == windowMissed || len(a.open) > 0 {
				continue
			}
			h := a.hit(a.branchSubject(), fmt.Sprintf("%s has no MR into the sprint %d branch for %s; %s",
				a.branch.Branch, a.train.ID.Number-1, a.train.ID, a.freezeText(b.Today)))
			h.Severity = severityIn(a.window)
			out = append(out, h)
		}
		return out
	},
}

var ruleL11 = rule{
	ID: "L11", Group: "G", Severity: SevInfo, DefaultOn: true, Scope: scopePerApp,
	Subjects: []SubjectKind{SubjectCardOnTrain},
	Mine:     MineListedCards,
	Reads:    []KindName{KindMilestones.Name, KindJiraVersions.Name, KindCards.Name, KindMRs.Name, KindPages.Name, KindPageCards.Name},
	Title:    "Card in a long-lived branch that targets a train is not on its train page or in its fixVersion",
	Hint:     "Info until 3 working days before freeze, then risk. Only while the train has a version for the app.",
	Check: func(b *Board) []Hit {
		var out []Hit
		for _, a := range aims(b) {
			if a.window == windowMissed || a.version == nil {
				continue
			}
			listed := map[string]bool{}
			for _, c := range a.version.Cards {
				listed[c.Key] = containsWhy(c.Why, WhyPage) || containsWhy(c.Why, WhyFixVersion)
			}
			var links []Link
			if p := a.version.page; p != nil {
				links = append(links, Link{Label: "Open page", URL: p.URL})
			}
			if a.version.Jira != nil {
				links = append(links, jiraLink(a.version.Jira))
			}
			seen := map[string]bool{}
			for _, key := range a.branch.cardKeys() {
				if listed[key] || seen[key] {
					continue
				}
				seen[key] = true
				h := a.hit(SubjCardOnTrain{Train: a.train.ID, App: a.branch.App, Key: key},
					fmt.Sprintf("%s rides %s in %s but is on neither its train page nor its fixVersion", key, a.train.ID, a.branch.Branch), links...)
				h.Evidence["card"] = key
				h.Severity, h.cards = severityIn(a.window), []string{key}
				out = append(out, h)
			}
		}
		sort.SliceStable(out, func(i, j int) bool { return out[i].Subject.subjectKey() < out[j].Subject.subjectKey() })
		return out
	},
}

var ruleL12 = rule{
	ID: "L12", Group: "G", Severity: SevRisk, DefaultOn: true, Scope: scopePerApp,
	Subjects: []SubjectKind{SubjectMR},
	Mine:     MineMR,
	Reads:    readsAim,
	Title:    "Long-lived branch's MR into the sprint branch is still open near freeze",
	Hint:     "3 working days or fewer before the freeze of the train its plan targets. Conflicts and pipeline are not read for these MRs.",
	Check: func(b *Board) []Hit {
		var out []Hit
		for _, a := range aims(b) {
			if a.window != windowNear {
				continue
			}
			for _, mr := range a.open {
				h := a.hit(SubjMR{App: a.branch.App, IID: mr.IID, Source: mr.SourceBranch, Target: mr.TargetBranch},
					fmt.Sprintf("!%d %s → %s is still open for %s; %s", mr.IID, mr.SourceBranch, mr.TargetBranch, a.train.ID, a.freezeText(b.Today)),
					Link{Label: "Open MR", URL: mr.URL})
				h.Evidence["mr"] = mr.IID
				out = append(out, h)
			}
		}
		return out
	},
}

var ruleL13 = rule{
	ID: "L13", Group: "G", Severity: SevBlock, DefaultOn: true, Scope: scopePerApp,
	Subjects: []SubjectKind{SubjectLLBBranch},
	Mine:     MineListedCards,
	Reads:    readsAim,
	Title:    "Long-lived branch missed the train its plan targets",
	Hint:     "The freeze passed or the release MR is open, and the branch has not merged into the sprint branch. Retarget the plan.",
	Check: func(b *Board) []Hit {
		var out []Hit
		for _, a := range aims(b) {
			if a.window != windowMissed {
				continue
			}
			why := "the release MR is open"
			if a.freeze != nil && b.Today.After(*a.freeze) {
				why = "freeze was " + a.freeze.label()
			}
			out = append(out, a.hit(a.branchSubject(), fmt.Sprintf("%s missed %s (%s) without merging into the sprint %d branch; retarget the plan",
				a.branch.Branch, a.train.ID, why, a.train.ID.Number-1)))
		}
		return out
	},
}
