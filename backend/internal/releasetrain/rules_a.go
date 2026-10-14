package releasetrain

import "fmt"

var ruleA1 = rule{
	ID: "A1", Group: "A", Severity: SevData, DefaultOn: true, Scope: scopePerApp, Reads: readsJoin,
	Title: "Milestone and Jira release name different versions",
	Check: func(b *Board) []Hit {
		return mismatchHits(b, MismatchVersion, func(m Mismatch) (Subject, string, *Day) {
			return SubjVersion{Train: *m.Train, App: m.App},
				fmt.Sprintf("Milestone says %s, Jira release says %s", versionOf(m.Milestone), m.Version), m.Milestone.Due
		})
	},
}

var ruleA2 = rule{
	ID: "A2", Group: "A", Severity: SevData, DefaultOn: true, Scope: scopePerApp, Reads: readsJoin,
	Title: "Jira release has a train label but no milestone",
	Check: func(b *Board) []Hit {
		return mismatchHits(b, MismatchNoMilestone, func(m Mismatch) (Subject, string, *Day) {
			return SubjJiraVersion{ID: m.Jira.ID},
				fmt.Sprintf("Jira release %s says train %s, but no GitLab milestone has that label for this app", m.Jira.Name, m.Train), m.Jira.ReleaseDate
		})
	},
}

var ruleA3 = rule{
	ID: "A3", Group: "A", Severity: SevData, DefaultOn: true, Scope: scopePerApp, Reads: readsJoin,
	Title: "Milestone has a train label but no Jira release",
	Check: func(b *Board) []Hit {
		return mismatchHits(b, MismatchNoJira, func(m Mismatch) (Subject, string, *Day) {
			return SubjMilestone{App: m.App, ID: m.Milestone.ID},
				fmt.Sprintf("Milestone %s has no Jira release with the train %s label", m.Milestone.Title, m.Train), m.Milestone.Due
		})
	},
}

var ruleA4 = rule{
	ID: "A4", Group: "A", Severity: SevData, DefaultOn: true, Scope: scopePerApp, Reads: readsJoin,
	Title: "Jira release has no train label and no hotfix milestone",
	Check: func(b *Board) []Hit {
		return mismatchHits(b, MismatchNoLabel, func(m Mismatch) (Subject, string, *Day) {
			return SubjJiraVersion{ID: m.Jira.ID},
				fmt.Sprintf("Jira release %s has no train label and matches no hotfix milestone", m.Jira.Name), m.Jira.ReleaseDate
		})
	},
}

var ruleA5 = rule{
	ID: "A5", Group: "A", Severity: SevData, DefaultOn: true, Scope: scopePerApp, Reads: readsJoin,
	Title: "Jira release date differs from milestone due date",
	Check: func(b *Board) []Hit {
		return forEachVersion(b, func(t *Train, v *AppVersion) *Hit {
			if v.Jira == nil || v.Jira.ReleaseDate == nil || v.Milestone.Due == nil || *v.Jira.ReleaseDate == *v.Milestone.Due {
				return nil
			}
			return versionHit(t, v,
				fmt.Sprintf("Jira release date %s, milestone due %s", v.Jira.ReleaseDate.label(), v.Milestone.Due.label()),
				Evidence{"releaseDate": *v.Jira.ReleaseDate, "due": *v.Milestone.Due}, jiraLink(v.Jira))
		})
	},
}

func versionOf(ms *MilestoneFact) string {
	if v, ok := ParseVersion(ms.Title); ok {
		return v.String()
	}
	return ms.Title
}

func mismatchHits(b *Board, kind MismatchKind, describe func(Mismatch) (Subject, string, *Day)) []Hit {
	var out []Hit
	for _, m := range b.Mismatches {
		if m.Kind != kind {
			continue
		}
		subject, text, ages := describe(m)
		ev := Evidence{"kind": m.Kind, "version": m.Version}
		link := Link{Label: "Open Jira release"}
		if m.Jira != nil {
			ev["jira"] = m.Jira.Name
			link.URL = m.Jira.URL
		}
		if m.Milestone != nil {
			ev["milestone"] = m.Milestone.Title
			if m.Jira == nil {
				link = Link{Label: "Open milestone", URL: m.Milestone.URL}
			}
		}
		out = append(out, Hit{Subject: subject, Train: m.Train, App: m.App, AgesOn: ages, Evidence: ev, Text: text, Link: link})
	}
	return out
}
