package releasetrain

import (
	"fmt"
	"sort"
)

var ruleF1 = rule{
	ID: "F1", Group: "F", Severity: SevInfo, DefaultOn: false, Scope: scopeGlobal,
	Subjects: []SubjectKind{SubjectTrainPair},
	Mine:     MineNever,
	Reads:    []KindName{KindMilestones.Name},
	Title:    "Two trains overlap",
	Check: func(b *Board) []Hit {
		var trains []*Train
		for i := range b.Trains {
			if b.Trains[i].Freeze != nil && b.Trains[i].Publish != nil {
				trains = append(trains, &b.Trains[i])
			}
		}
		sort.SliceStable(trains, func(i, j int) bool { return trains[i].Freeze.Before(*trains[j].Freeze) })
		var out []Hit
		for i := 0; i+1 < len(trains); i++ {
			x, y := trains[i], trains[i+1]
			if y.Freeze.After(*x.Publish) {
				continue
			}
			train := y.ID
			out = append(out, Hit{
				Subject: SubjTrainPair{A: x.ID, B: y.ID}, Train: &train, AgesOn: y.Publish,
				Evidence: Evidence{"freeze": *y.Freeze, "publish": *x.Publish},
				Copy: Copy{
					What: fmt.Sprintf("Train %s freezes %s, before train %s publishes %s", y.ID.short(), y.Freeze.label(), x.ID.short(), x.Publish.label()),
					Do:   "Move a date if the overlap is not planned.",
				},
				Links: []Link{},
			})
		}
		return out
	},
}

var ruleF2 = rule{
	ID: "F2", Group: "F", Severity: SevRisk, DefaultOn: true, Scope: scopePerApp,
	Subjects: []SubjectKind{SubjectHotfixTrain},
	Mine:     MineOwner,
	Reads:    []KindName{KindMilestones.Name},
	Title:    "A hotfix overlaps a train window",
	Check: func(b *Board) []Hit {
		var out []Hit
		for _, h := range b.Hotfixes {
			start, due := h.Milestone.Start, h.Milestone.Due
			if start == nil || due == nil {
				continue
			}
			for i := range b.Trains {
				t := &b.Trains[i]
				if t.Freeze == nil || t.Publish == nil || start.After(*t.Publish) || due.Before(*t.Freeze) {
					continue
				}
				train := t.ID
				hit := Hit{
					Subject: SubjHotfixTrain{App: h.App, Version: h.Version, Train: t.ID}, Train: &train, App: h.App, AgesOn: due,
					Evidence: Evidence{"start": *start, "due": *due, "freeze": *t.Freeze, "publish": *t.Publish},
					Copy: Copy{
						What: fmt.Sprintf("Hotfix %s (%s to %s) overlaps train %s (%s to %s)",
							h.Version, start.label(), due.label(), t.ID.short(), t.Freeze.label(), t.Publish.label()),
						Do: "Agree which ships first, or move the hotfix dates.",
					},
				}
				out = append(out, *hit.open(hotfixLinks(h, t)...))
			}
		}
		return out
	},
}

func hotfixLinks(h Hotfix, t *Train) []Link {
	links := []Link{{Label: "Open hotfix", URL: h.Milestone.URL}}
	for _, v := range t.Versions {
		if v.App == h.App {
			links = append(links, Link{Label: "Open train " + t.ID.short() + " milestone", URL: v.Milestone.URL})
		}
	}
	return links
}
