package releasetrain

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
)

type trainWindow int

const nearFreezeWorkDays = 3

const (
	windowEarly trainWindow = iota + 1
	windowNear
	windowMissed
)

type aim struct {
	group          *LLBGroup
	branch         *LongLivedBranch
	train          *Train
	version        *AppVersion
	freeze         *Day
	window         trainWindow
	openIntoSprint []MRFact
}

func aims(b *Board) []aim {
	var out []aim
	for gi := range b.LLBs {
		g := &b.LLBs[gi]
		planned, ok := g.Target.(LLBPlanned)
		if !ok {
			continue
		}
		t := b.train(planned.Train)
		if t == nil || !t.ID.regular() {
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
					a.openIntoSprint = append(a.openIntoSprint, mr)
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
	case freeze != nil && workDaysLeft(today, *freeze) <= nearFreezeWorkDays:
		return windowNear
	}
	return windowEarly
}

func (a aim) hit(subject Subject, c Copy) Hit {
	train := a.train.ID
	return Hit{
		Subject: subject, Train: &train, App: a.branch.App,
		Evidence: Evidence{"group": a.group.ID, "branch": a.branch.Branch, "train": a.train.ID, "freeze": a.freeze},
		Copy:     c, Cards: a.branch.cardKeys(),
	}
}

func (a aim) branchSubject() SubjLLBBranch {
	return SubjLLBBranch{Train: a.train.ID, App: a.branch.App, Branch: a.branch.Branch, Group: a.group.ID}
}

func severityIn(w trainWindow) Severity {
	if w == windowNear {
		return SevRisk
	}
	return SevInfo
}

// newMRURL is the GitLab form that opens an MR from the branch into the sprint branch of its train.
// It is empty when either name is unknown: the sprint branch is never guessed from a pattern.
func (b *Board) newMRURL(a aim) string {
	app := a.branch.App
	into, web := b.fix.sprintBranchFor(app, a.train.ID), b.projectWebURL(app)
	if into == "" || web == "" {
		return ""
	}
	return fmt.Sprintf("%s/-/merge_requests/new?merge_request[source_branch]=%s&merge_request[target_branch]=%s",
		web, url.QueryEscape(a.branch.Branch), url.QueryEscape(into))
}

// projectWebURL is the app's GitLab project page, read off an MR the app has: the config holds only the host.
func (b *Board) projectWebURL(app AppID) string {
	for _, mr := range b.mrs[app] {
		if web, _, ok := strings.Cut(mr.URL, "/-/merge_requests/"); ok && web != "" {
			return web
		}
	}
	return ""
}

var readsAim = []KindName{KindMilestones.Name, KindMRs.Name}

type trunkKey struct {
	app    AppID
	branch string
}

var ruleL10 = rule{
	ID: "L10", Group: "G", Severity: SevInfo, DefaultOn: true, Scope: scopePerApp,
	Subjects: []SubjectKind{SubjectLLBBranch},
	Mine:     MineListedCards,
	Reads:    readsAim,
	Title:    "Long-lived branch targets a train but has no MR into its sprint branch",
	Hint:     "Per app in the group. Info until 3 working days before freeze, then risk. After the freeze it is L13.",
	Check: func(b *Board) []Hit {
		var out []Hit
		onTrunk := map[trunkKey]bool{}
		for _, t := range openTrunkMRs(b) {
			onTrunk[trunkKey{t.branch.App, t.branch.Branch}] = true
		}
		for _, a := range aims(b) {
			if a.window == windowMissed || len(a.openIntoSprint) > 0 || onTrunk[trunkKey{a.branch.App, a.branch.Branch}] {
				continue
			}
			h := a.hit(a.branchSubject(), Copy{
				What: fmt.Sprintf("%s is not in sprint %d yet", a.branch.Branch, a.train.ID.Number-1),
				Do:   "Open an MR into the sprint branch " + before("freeze", a.freeze) + ".",
			})
			h.Severity = severityIn(a.window)
			if newMR := b.newMRURL(a); newMR != "" {
				h.Primary = &Action{Label: "New MR", URL: newMR}
			}
			out = append(out, *h.dueAt(b.Today, a.freeze, DueFreeze))
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
				h := a.hit(SubjCardOnTrain{Train: a.train.ID, App: a.branch.App, Key: key}, Copy{
					What: fmt.Sprintf("%s is missing from the train %s page and fixVersion", key, a.train.ID.short()),
					Do:   fmt.Sprintf("Add it to both %s. Its code ships in %s.", before("freeze", a.freeze), a.branch.Branch),
				})
				h.Evidence["card"] = key
				h.Severity, h.Cards = severityIn(a.window), []string{key}
				out = append(out, *h.open(links...).dueAt(b.Today, a.freeze, DueFreeze))
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
			for _, mr := range a.openIntoSprint {
				h := a.hit(SubjMR{App: a.branch.App, IID: mr.IID, Source: mr.SourceBranch, Target: mr.TargetBranch}, Copy{
					What: fmt.Sprintf("MR !%d from %s into the sprint branch is still open", mr.IID, a.branch.Branch),
					Do:   "Merge it " + before("freeze", a.freeze) + ".",
				})
				h.Evidence["mr"] = mr.IID
				out = append(out, *h.open(Link{Label: "Open MR", URL: mr.URL}).dueAt(b.Today, a.freeze, DueFreeze))
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
			next := TrainID{Number: a.train.ID.Number + 1, Year: a.train.ID.Year}
			h := a.hit(a.branchSubject(), Copy{
				What: fmt.Sprintf("%s missed train %s", a.branch.Branch, a.train.ID.short()),
				Do:   fmt.Sprintf("Retarget its plan to train %s. It never merged into the sprint %d branch.", next.short(), a.train.ID.Number-1),
			})
			h.Primary = &Action{Label: "Retarget plan"}
			h.dueAt(b.Today, a.freeze, DueFreeze)
			if h.Due != nil {
				h.Due.Missed = true
			}
			out = append(out, h)
		}
		return out
	},
}
