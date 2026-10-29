package releasetrain

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

var trunkRE = regexp.MustCompile(`^(develop|main|master)$`)

var ruleL1 = rule{
	ID: "L1", Group: "G", Severity: SevBlock, DefaultOn: true, Scope: scopePerApp,
	Subjects: []SubjectKind{SubjectMR},
	Mine:     MineMR,
	Reads:    []KindName{KindMRs.Name},
	Title:    "Long-lived branch has an open MR into develop or main after cutover",
	Hint:     "Catches the wrong landing before it merges; it should go into the sprint branch.",
	Check: func(b *Board) []Hit {
		trunk := openTrunkMRs(b)
		out := make([]Hit, 0, len(trunk))
		for _, t := range trunk {
			do := fmt.Sprintf("Retarget it to the sprint branch. %s takes no feature work after the cutover.", upperFirst(t.mr.TargetBranch))
			if planned, ok := t.group.Target.(LLBPlanned); ok {
				do += fmt.Sprintf(" This also puts it in sprint %d.", planned.Train.Number-1)
			}
			h := Hit{
				Subject: SubjMR{App: t.branch.App, IID: t.mr.IID, Source: t.mr.SourceBranch, Target: t.mr.TargetBranch}, App: t.branch.App,
				Evidence: Evidence{"group": t.group.ID, "branch": t.branch.Branch, "mr": t.mr.IID, "target": t.mr.TargetBranch, "cutover": t.cutover},
				Copy: Copy{
					What: fmt.Sprintf("MR !%d takes %s into %s", t.mr.IID, t.branch.Branch, t.mr.TargetBranch),
					Do:   do,
				},
				Cards: t.branch.cardKeys(),
			}
			out = append(out, *h.fix("Retarget MR", Link{Label: "Open MR", URL: t.mr.URL}))
		}
		return out
	},
}

type trunkMR struct {
	group   *LLBGroup
	branch  *LongLivedBranch
	mr      MRFact
	cutover Day
}

func openTrunkMRs(b *Board) []trunkMR {
	var out []trunkMR
	for gi := range b.LLBs {
		g := &b.LLBs[gi]
		for bi := range g.Branches {
			br := &g.Branches[bi]
			cutover, ok := b.Cutover[br.App]
			if !ok || !b.Today.After(cutover) {
				continue
			}
			for _, mr := range br.outs {
				if mr.State == MROpened && trunkRE.MatchString(mr.TargetBranch) && !accepted(g.Plan, br.App, mr.IID) {
					out = append(out, trunkMR{group: g, branch: br, mr: mr, cutover: cutover})
				}
			}
		}
	}
	return out
}

func upperFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func accepted(p *LlbPlan, app AppID, iid int) bool {
	if p == nil {
		return false
	}
	ref := fmt.Sprintf("%s!%d", app, iid)
	for _, a := range p.Accepted {
		if a.MRRef == ref {
			return true
		}
	}
	return false
}

var ruleL3 = rule{
	ID: "L3", Group: "G", Severity: SevRisk, DefaultOn: true, Scope: scopePerApp,
	Subjects: []SubjectKind{SubjectCardOnTrain},
	Mine:     MineListedCards,
	Reads:    []KindName{KindMilestones.Name, KindJiraVersions.Name, KindCards.Name, KindMRs.Name, KindPages.Name, KindPageCards.Name, KindLLBCards.Name},
	Title:    "Card is on a train page but parked in a long-lived branch that does not target it",
	Hint:     "Its code will not ride this train unless the branch lands in the sprint branch before the cut.",
	Check: func(b *Board) []Hit {
		groups := groupsByBranch(b.LLBs)
		var out []Hit
		for i := range b.Trains {
			t := &b.Trains[i]
			for _, v := range t.Versions {
				for _, c := range v.Cards {
					parked, ok := c.Code.(CodeParked)
					if !ok {
						continue
					}
					g := groups[v.App][parked.Branch]
					if g == nil {
						continue
					}
					if aimed, ok := aimedTrain(g.Target); ok && aimed == t.ID {
						continue
					}
					train := t.ID
					h := Hit{
						Subject: SubjCardOnTrain{Train: t.ID, App: v.App, Key: c.Key}, Train: &train, App: v.App, AgesOn: dueOf(t, v),
						Evidence: Evidence{"card": c.Key, "train": t.ID, "group": g.ID, "branch": parked.Branch, "target": targetText(g.Target)},
						Copy: Copy{
							What: fmt.Sprintf("%s is on train %s, but its code is parked in %s", c.Key, t.ID.short(), parked.Branch),
							Do:   fmt.Sprintf("Set %s to target train %s, or take the card off the train. %s", parked.Branch, t.ID.short(), targetSentence(g.Target)),
						},
						Cards: []string{c.Key},
					}
					out = append(out, *h.fix("Set target train", Link{Label: "Open card", URL: c.URL}).dueAt(b.Today, freezeOf(t, v), DueFreeze))
				}
			}
		}
		sort.SliceStable(out, func(i, j int) bool {
			return out[i].Subject.subjectKey() < out[j].Subject.subjectKey()
		})
		return out
	},
}

var ruleL9 = rule{
	ID: "L9", Group: "G", Severity: SevRisk, DefaultOn: true, Scope: scopePerApp,
	Subjects: []SubjectKind{SubjectMR},
	Mine:     MineMR,
	Reads:    []KindName{KindMRs.Name},
	Title:    "Code stranded in a branch that is not a sprint branch, a long-lived branch or develop",
	Hint:     "An open MR into such a branch, or a merged one whose target branch has not itself landed in a sprint branch, a long-lived branch or develop. No train takes code from that branch until someone moves it on.",
	Check: func(b *Board) []Hit {
		llbs := groupsByBranch(b.LLBs)
		var out []Hit
		for _, app := range b.Apps {
			cutover, hasCutover := b.Cutover[app.ID]
			landed := landedBranches(b.mrs[app.ID], func(branch string) bool { return roleRE.MatchString(branch) || llbs[app.ID][branch] != nil })
			for _, mr := range b.mrs[app.ID] {
				switch {
				case mr.State == MRClosed,
					mr.State == MRMerged && (!hasCutover || mr.MergedOn == nil || !mr.MergedOn.After(cutover)),
					roleRE.MatchString(mr.TargetBranch),
					llbs[app.ID][mr.TargetBranch] != nil,
					llbs[app.ID][mr.SourceBranch] != nil,
					sprintBranchRE.MatchString(mr.SourceBranch),
					mr.State == MRMerged && landed[mr.TargetBranch]:
					continue
				}
				c := Copy{
					What: fmt.Sprintf("MR !%d goes into %s, which no train takes", mr.IID, mr.TargetBranch),
					Do:   "Retarget it to the sprint branch or a long-lived branch.",
				}
				if mr.State == MRMerged {
					c = Copy{
						What: fmt.Sprintf("MR !%d is merged into %s, which never reached a sprint branch", mr.IID, mr.TargetBranch),
						Do:   fmt.Sprintf("Merge %s into the sprint branch, or move the change.", mr.TargetBranch),
					}
				}
				h := Hit{
					Subject: SubjMR{App: app.ID, IID: mr.IID, Source: mr.SourceBranch, Target: mr.TargetBranch}, App: app.ID,
					Evidence: Evidence{"mr": mr.IID, "source": mr.SourceBranch, "target": mr.TargetBranch, "state": mr.State},
					Copy:     c,
					Cards:    mrKeys(mr),
				}
				out = append(out, *h.open(Link{Label: "Open MR", URL: mr.URL}))
			}
		}
		return out
	},
}

// landedBranches grows to a fixpoint, so a cycle of merged MRs never lands.
func landedBranches(mrs []MRFact, flow func(branch string) bool) map[string]bool {
	landed := map[string]bool{}
	for changed := true; changed; {
		changed = false
		for _, mr := range mrs {
			if mr.State == MRMerged && !landed[mr.SourceBranch] && (flow(mr.TargetBranch) || landed[mr.TargetBranch]) {
				landed[mr.SourceBranch], changed = true, true
			}
		}
	}
	return landed
}

func mrsByApp(cfg Config, snap Snapshot) map[AppID][]MRFact {
	out := map[AppID][]MRFact{}
	for _, app := range cfg.Apps {
		mrs := Items(snap, KindMRs, ProjectScope(app.GitLabProject))
		sort.Slice(mrs, func(i, j int) bool { return mrs[i].IID < mrs[j].IID })
		out[app.ID] = mrs
	}
	return out
}

func aimedTrain(t LLBTarget) (TrainID, bool) {
	switch t := t.(type) {
	case LLBPlanned:
		return t.Train, true
	case LLBFromEpic:
		return t.Train, true
	}
	return TrainID{}, false
}

func targetSentence(t LLBTarget) string {
	switch t := t.(type) {
	case LLBPlanned:
		return fmt.Sprintf("It targets train %s now.", t.Train.short())
	case LLBFromEpic:
		return fmt.Sprintf("An epic suggests train %s for it.", t.Train.short())
	case LLBLandedIn:
		return fmt.Sprintf("It already landed for train %s.", t.Train.short())
	case LLBDropped:
		return "Its plan was dropped."
	}
	return "It has no target train now."
}

func targetText(t LLBTarget) string {
	switch t := t.(type) {
	case LLBPlanned:
		return "targets " + t.Train.String()
	case LLBFromEpic:
		text := fmt.Sprintf("targets %s from epic %s, %d of %d cards", t.Train, t.Epic, t.Covered, t.Total)
		if len(t.Also) > 0 {
			also := make([]string, len(t.Also))
			for i, a := range t.Also {
				also[i] = a.String()
			}
			text += fmt.Sprintf("; %s pages list it too", strings.Join(also, " and "))
		}
		return text
	case LLBLandedIn:
		return "landed for " + t.Train.String()
	case LLBDropped:
		return "dropped"
	}
	return "no target train"
}
