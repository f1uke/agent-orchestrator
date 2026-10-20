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
		var out []Hit
		for _, g := range b.LLBs {
			for _, br := range g.Branches {
				cutover, ok := b.Cutover[br.App]
				if !ok || !b.Today.After(cutover) {
					continue
				}
				for _, mr := range br.outs {
					if mr.State != MROpened || !trunkRE.MatchString(mr.TargetBranch) || accepted(g.Plan, br.App, mr.IID) {
						continue
					}
					out = append(out, Hit{
						Subject: SubjMR{App: br.App, IID: mr.IID, Source: mr.SourceBranch, Target: mr.TargetBranch}, App: br.App,
						Evidence: Evidence{"group": g.ID, "branch": br.Branch, "mr": mr.IID, "target": mr.TargetBranch, "cutover": cutover},
						Text:     fmt.Sprintf("!%d %s → %s is open; land it in the sprint branch instead", mr.IID, br.Branch, mr.TargetBranch),
						Links:    []Link{{Label: "Open MR", URL: mr.URL}},
						cards:    br.cardKeys(),
					})
				}
			}
		}
		return out
	},
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
					out = append(out, Hit{
						Subject: SubjCardOnTrain{Train: t.ID, App: v.App, Key: c.Key}, Train: &train, App: v.App, AgesOn: dueOf(t, v),
						Evidence: Evidence{"card": c.Key, "train": t.ID, "group": g.ID, "branch": parked.Branch, "target": targetText(g.Target)},
						Text:     fmt.Sprintf("%s is on train %s, but its code sits in %s (%s)", c.Key, t.ID, parked.Branch, targetText(g.Target)),
						Links:    []Link{{Label: "Open card", URL: c.URL}},
						cards:    []string{c.Key},
					})
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
				why := "open into a branch no train takes"
				if mr.State == MRMerged {
					why = fmt.Sprintf("merged, but %s has not landed in a sprint branch or develop", mr.TargetBranch)
				}
				out = append(out, Hit{
					Subject: SubjMR{App: app.ID, IID: mr.IID, Source: mr.SourceBranch, Target: mr.TargetBranch}, App: app.ID,
					Evidence: Evidence{"mr": mr.IID, "source": mr.SourceBranch, "target": mr.TargetBranch, "state": mr.State},
					Text:     fmt.Sprintf("!%d %s → %s: %s", mr.IID, mr.SourceBranch, mr.TargetBranch, why),
					Links:    []Link{{Label: "Open MR", URL: mr.URL}},
					cards:    mrKeys(mr),
				})
			}
		}
		return out
	},
}

// landedBranches is the branches whose code reached a flow branch: one with a
// merged MR into a flow branch, or into another branch that landed. It grows to
// a fixpoint, so a cycle of merged MRs never lands.
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
