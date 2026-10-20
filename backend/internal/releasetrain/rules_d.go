package releasetrain

import (
	"fmt"
	"strings"
)

var ruleD1 = rule{
	ID: "D1", Group: "D", Severity: SevRisk, DefaultOn: true, Scope: scopePerApp, Reads: readsJoin,
	Subjects: []SubjectKind{SubjectVersion},
	Mine:     MineOwner,
	Title:    "No approvers set on the Jira release",
	Check: func(b *Board) []Hit {
		return forEachVersion(b, func(t *Train, v *AppVersion) *Hit {
			freeze := freezeOf(t, v)
			if v.Jira == nil || len(v.Jira.Approvers) > 0 || freeze == nil || !b.Today.After(freeze.Add(2)) {
				return nil
			}
			return versionHit(t, v, "The bot did not set approvers on this Jira release",
				Evidence{"jira": v.Jira.ID}, jiraLink(v.Jira))
		})
	},
}

var ruleD2 = rule{
	ID: "D2", Group: "D", Severity: SevRisk, DefaultOn: true, Scope: scopePerApp,
	Subjects: []SubjectKind{SubjectVersion},
	Mine:     MineOwner,
	Reads:    []KindName{KindMilestones.Name, KindJiraVersions.Name, KindRelatedWork.Name},
	Title:    "Scan page or release note not attached",
	Check: func(b *Board) []Hit {
		return forEachVersion(b, func(t *Train, v *AppVersion) *Hit {
			freeze := freezeOf(t, v)
			if v.Jira == nil || v.RelatedWork == nil || freeze == nil || !b.Today.After(freeze.Add(2)) {
				return nil
			}
			cats := map[string]bool{}
			for _, l := range v.RelatedWork.Links {
				cats[l.Category] = true
			}
			var missing []string
			if !cats[CategoryScanPage] {
				missing = append(missing, "scan page")
			}
			if !cats[CategoryReleaseNote] {
				missing = append(missing, "release note")
			}
			if len(missing) == 0 {
				return nil
			}
			return versionHit(t, v, "Missing on the Jira release: "+strings.Join(missing, " and "),
				Evidence{"missing": missing}, jiraLink(v.Jira))
		})
	},
}

var ruleD3 = rule{
	ID: "D3", Group: "D", Severity: SevRisk, DefaultOn: true, Scope: scopePerApp,
	Subjects: []SubjectKind{SubjectVersion},
	Mine:     MinePendingApprover,
	Reads:    []KindName{KindMilestones.Name, KindJiraVersions.Name, KindTags.Name},
	Title:    "Not every approver approved before publish",
	Hint:     "The team has no fixed rule yet for which approvals are required, so this checks all of them.",
	Check: func(b *Board) []Hit {
		return forEachVersion(b, func(t *Train, v *AppVersion) *Hit {
			due := dueOf(t, v)
			if v.Jira == nil || len(v.Jira.Approvers) == 0 || v.Jira.Released || due == nil ||
				b.Today.Before(due.Add(-1)) || published(t, v, b.Today) {
				return nil
			}
			pending := pendingApprovers(v.Jira)
			if len(pending) == 0 {
				return nil
			}
			return versionHit(t, v,
				fmt.Sprintf("%d of %d not approved: %s", len(pending), len(v.Jira.Approvers), joinNames(pending)),
				Evidence{"pending": pending, "total": len(v.Jira.Approvers)}, jiraLink(v.Jira))
		})
	},
}
