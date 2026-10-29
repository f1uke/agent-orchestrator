package releasetrain

import (
	"fmt"
	"sort"
)

var ruleB1 = rule{
	ID: "B1", Group: "B", Severity: SevRisk, DefaultOn: true, Scope: scopePerApp,
	Subjects: []SubjectKind{SubjectVersion},
	Mine:     MineOwner,
	Reads:    []KindName{KindMilestones.Name, KindMRs.Name},
	Title:    "Freeze day passed and no release MR is open",
	Check: func(b *Board) []Hit {
		return forEachVersion(b, func(t *Train, v *AppVersion) *Hit {
			s := freezeStage(t, v, b.Today)
			if s.Status != StatusLate && s.Status != StatusWaiting {
				return nil
			}
			freeze := "Freeze was " + s.Date.label() + "."
			if *s.Date == b.Today {
				freeze = "Freeze is today."
			}
			return versionHit(t, v, Copy{
				What: fmt.Sprintf("Train %s has no release MR yet", t.ID.short()),
				Do:   "Open the release MR. " + freeze,
			}, Evidence{"freeze": s.Date, "status": s.Status}).open(milestoneLink(&v.Milestone)).dueAt(b.Today, s.Date, DueFreeze)
		})
	},
}

var devStatuses = map[string]bool{"Ready to Dev": true, "In Progress": true, "Pull Request": true}

var ruleB3 = rule{
	ID: "B3", Group: "B", Severity: SevRisk, DefaultOn: true, Scope: scopePerApp,
	Subjects: []SubjectKind{SubjectVersion},
	Mine:     MineListedCards,
	Reads:    []KindName{KindMilestones.Name, KindJiraVersions.Name, KindCards.Name},
	Title:    "Cards still in development after freeze",
	Check: func(b *Board) []Hit {
		return forEachVersion(b, func(t *Train, v *AppVersion) *Hit {
			freeze := freezeOf(t, v)
			if freeze == nil || b.Today.Before(*freeze) || (t.Publish != nil && t.Publish.Before(b.Today)) {
				return nil
			}
			var keys []string
			statuses := map[string]bool{}
			for _, is := range v.Issues {
				if devStatuses[is.Status] {
					keys = append(keys, is.Key)
					statuses[is.Status] = true
				}
			}
			if len(keys) == 0 {
				return nil
			}
			h := versionHit(t, v, Copy{
				What: fmt.Sprintf("%s in train %s %s still in development", plural(len(keys), "card"), t.ID.short(), isAre(len(keys))),
				Do:   fmt.Sprintf("Finish %s or take %s out of the train %s.", itThem(len(keys)), itThem(len(keys)), before("publish", dueOf(t, v))),
			}, Evidence{"cards": keys}).open(jiraLink(v.Jira)).dueAt(b.Today, dueOf(t, v), DuePublish)
			h.Cards = keys
			return h
		})
	},
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
