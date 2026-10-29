package releasetrain

import (
	"fmt"
	"sort"
	"strings"
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
			return versionHit(t, v, s.Detail, Evidence{"freeze": s.Date, "status": s.Status},
				milestoneLink(&v.Milestone))
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
			h := versionHit(t, v,
				fmt.Sprintf("%d card(s) still %s: %s", len(keys), strings.Join(sortedKeys(statuses), " / "), joinNames(keys)),
				Evidence{"cards": keys}, jiraLink(v.Jira))
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
