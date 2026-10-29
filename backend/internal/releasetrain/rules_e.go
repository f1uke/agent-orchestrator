package releasetrain

import "fmt"

var doneStatuses = map[string]bool{"Done": true, "Deployed": true, "UAT Approved": true, "Abandoned": true}

var ruleE1 = rule{
	ID: "E1", Group: "E", Severity: SevHygiene, DefaultOn: true, Scope: scopePerApp,
	Subjects: []SubjectKind{SubjectVersion, SubjectHotfix},
	Mine:     MineOwner,
	Reads:    []KindName{KindMilestones.Name, KindJiraVersions.Name, KindTags.Name},
	Title:    "Published but the Jira release is not marked released",
	Check: func(b *Board) []Hit {
		hits := forEachVersion(b, func(t *Train, v *AppVersion) *Hit {
			if v.Jira == nil || v.Jira.Released || !published(t, v, b.Today) {
				return nil
			}
			pending := pendingApprovers(v.Jira)
			do := "Mark the Jira release released."
			if len(pending) > 0 {
				do += fmt.Sprintf(" %s %s not approved yet: %s.", plural(len(pending), "approver"), hasHave(len(pending)), joinNames(pending))
			}
			return versionHit(t, v, Copy{
				What: fmt.Sprintf("Train %s shipped, but its Jira release is not marked released", t.ID.short()),
				Do:   do,
			}, Evidence{"tag": v.Tag.Name, "pending": pending}).fix("Mark released", jiraLink(v.Jira), Link{Label: "Open tag", URL: v.Tag.URL})
		})
		for _, h := range b.Hotfixes {
			due := h.Milestone.Due
			if h.Jira == nil || h.Jira.Released || due == nil || !due.Before(b.Today) {
				continue
			}
			hit := Hit{
				Subject: SubjHotfix{App: h.App, Version: h.Version}, App: h.App, AgesOn: due,
				Evidence: Evidence{"hotfix": h.Version},
				Copy: Copy{
					What: fmt.Sprintf("Hotfix %s shipped, but its Jira release is not marked released", h.Version),
					Do:   "Mark the Jira release released.",
				},
			}
			hits = append(hits, *hit.fix("Mark released", jiraLink(h.Jira), Link{Label: "Open hotfix", URL: h.Milestone.URL}))
		}
		return hits
	},
}

var ruleE2 = rule{
	ID: "E2", Group: "E", Severity: SevHygiene, DefaultOn: true, Scope: scopePerApp,
	Subjects: []SubjectKind{SubjectVersion},
	Mine:     MineListedCards,
	Reads:    []KindName{KindMilestones.Name, KindJiraVersions.Name, KindCards.Name},
	Title:    "Cards not Done after publish",
	Hint:     "Team decision: every card should be moved to Done after publish, including Ready for UAT and Ready to Deliver.",
	Check: func(b *Board) []Hit {
		return forEachVersion(b, func(t *Train, v *AppVersion) *Hit {
			due := dueOf(t, v)
			if due == nil || !due.Before(b.Today) {
				return nil
			}
			var left, keys []string
			for _, is := range v.Issues {
				if !doneStatuses[is.Status] {
					left = append(left, is.Key+" "+is.Status)
					keys = append(keys, is.Key)
				}
			}
			if len(left) == 0 {
				return nil
			}
			h := versionHit(t, v, Copy{
				What: fmt.Sprintf("%s in train %s %s not Done after publish", plural(len(left), "card"), t.ID.short(), isAre(len(left))),
				Do:   fmt.Sprintf("Move %s to Done.", itThem(len(left))),
			}, Evidence{"cards": left}).open(jiraLink(v.Jira))
			h.Cards = keys
			return h
		})
	},
}

var ruleE4 = rule{
	ID: "E4", Group: "E", Severity: SevHygiene, DefaultOn: true, Scope: scopePerApp,
	Subjects: []SubjectKind{SubjectVersion},
	Mine:     MineOwner,
	Reads:    []KindName{KindMilestones.Name, KindTechReq.Name},
	Title:    "Force-update ticket open after its due date",
	Check: func(b *Board) []Hit {
		return forEachVersion(b, func(t *Train, v *AppVersion) *Hit {
			fu := v.ForceUpdate
			if fu == nil || fu.StatusCategory == StatusCategoryDone || fu.Due == nil || !fu.Due.Before(b.Today) {
				return nil
			}
			return versionHit(t, v, Copy{
				What: fmt.Sprintf("Force-update ticket %s is past its due date (%s)", fu.Key, fu.Status),
				Do:   fmt.Sprintf("Close it, or move its due date. It was due %s.", fu.Due.label()),
			}, Evidence{"ticket": fu.Key, "status": fu.Status, "due": *fu.Due}).open(Link{Label: "Open " + fu.Key, URL: fu.URL})
		})
	},
}

var ruleE5 = rule{
	ID: "E5", Group: "E", Severity: SevInfo, DefaultOn: false, Scope: scopePerApp,
	Subjects: []SubjectKind{SubjectVersion},
	Mine:     MineOwner,
	Reads:    []KindName{KindMilestones.Name, KindTechReq.Name},
	Title:    "Force-update ticket has no due date",
	Check: func(b *Board) []Hit {
		return forEachVersion(b, func(t *Train, v *AppVersion) *Hit {
			fu := v.ForceUpdate
			if fu == nil || fu.Due != nil {
				return nil
			}
			return versionHit(t, v, Copy{
				What: fmt.Sprintf("Force-update ticket %s has no due date", fu.Key),
				Do:   "Set its due date.",
			}, Evidence{"ticket": fu.Key}).open(Link{Label: "Open " + fu.Key, URL: fu.URL})
		})
	},
}
