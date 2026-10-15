package releasetrain

import "fmt"

var doneStatuses = map[string]bool{"Done": true, "Deployed": true, "UAT Approved": true, "Abandoned": true}

var ruleE1 = rule{
	ID: "E1", Group: "E", Severity: SevHygiene, DefaultOn: true, Scope: scopePerApp,
	Reads: []KindName{KindMilestones.Name, KindJiraVersions.Name, KindTags.Name},
	Title: "Published but the Jira release is not marked released",
	Check: func(b *Board) []Hit {
		hits := forEachVersion(b, func(t *Train, v *AppVersion) *Hit {
			if v.Jira == nil || v.Jira.Released || !published(t, v, b.Today) {
				return nil
			}
			pending := pendingApprovers(v.Jira)
			text := fmt.Sprintf("Tag %s and due %s passed, Jira release still unreleased", v.Tag.Name, dueOf(t, v).label())
			if len(pending) > 0 {
				text += fmt.Sprintf("; %d of %d approvers pending: %s", len(pending), len(v.Jira.Approvers), joinNames(pending))
			}
			return versionHit(t, v, text, Evidence{"tag": v.Tag.Name, "pending": pending}, jiraLink(v.Jira), Link{Label: "Open tag", URL: v.Tag.URL})
		})
		for _, h := range b.Hotfixes {
			due := h.Milestone.Due
			if h.Jira == nil || h.Jira.Released || due == nil || !due.Before(b.Today) {
				continue
			}
			hits = append(hits, Hit{
				Subject: SubjHotfix{App: h.App, Version: h.Version}, App: h.App, AgesOn: due,
				Evidence: Evidence{"hotfix": h.Version}, Text: "Hotfix shipped, Jira release still unreleased", Links: []Link{jiraLink(h.Jira), {Label: "Open hotfix", URL: h.Milestone.URL}},
			})
		}
		return hits
	},
}

var ruleE2 = rule{
	ID: "E2", Group: "E", Severity: SevHygiene, DefaultOn: true, Scope: scopePerApp,
	Reads: []KindName{KindMilestones.Name, KindJiraVersions.Name, KindCards.Name},
	Title: "Cards not Done after publish",
	Hint:  "Team decision: every card should be moved to Done after publish, including Ready for UAT and Ready to Deliver.",
	Check: func(b *Board) []Hit {
		return forEachVersion(b, func(t *Train, v *AppVersion) *Hit {
			due := dueOf(t, v)
			if due == nil || !due.Before(b.Today) {
				return nil
			}
			var left []string
			for _, is := range v.Issues {
				if !doneStatuses[is.Status] {
					left = append(left, is.Key+" "+is.Status)
				}
			}
			if len(left) == 0 {
				return nil
			}
			return versionHit(t, v, fmt.Sprintf("%d card(s) not Done: %s", len(left), joinNames(left)),
				Evidence{"cards": left}, jiraLink(v.Jira))
		})
	},
}

var ruleE4 = rule{
	ID: "E4", Group: "E", Severity: SevHygiene, DefaultOn: true, Scope: scopePerApp,
	Reads: []KindName{KindMilestones.Name, KindTechReq.Name},
	Title: "Force-update ticket open after its due date",
	Check: func(b *Board) []Hit {
		return forEachVersion(b, func(t *Train, v *AppVersion) *Hit {
			fu := v.ForceUpdate
			if fu == nil || fu.StatusCategory == StatusCategoryDone || fu.Due == nil || !fu.Due.Before(b.Today) {
				return nil
			}
			return versionHit(t, v, fmt.Sprintf("%s still %s, due %s", fu.Key, fu.Status, fu.Due.label()),
				Evidence{"ticket": fu.Key, "status": fu.Status, "due": *fu.Due}, Link{Label: "Open " + fu.Key, URL: fu.URL})
		})
	},
}

var ruleE5 = rule{
	ID: "E5", Group: "E", Severity: SevInfo, DefaultOn: false, Scope: scopePerApp,
	Reads: []KindName{KindMilestones.Name, KindTechReq.Name},
	Title: "Force-update ticket has no due date",
	Check: func(b *Board) []Hit {
		return forEachVersion(b, func(t *Train, v *AppVersion) *Hit {
			fu := v.ForceUpdate
			if fu == nil || fu.Due != nil {
				return nil
			}
			return versionHit(t, v, fu.Key+" has no due date", Evidence{"ticket": fu.Key}, Link{Label: "Open " + fu.Key, URL: fu.URL})
		})
	},
}
