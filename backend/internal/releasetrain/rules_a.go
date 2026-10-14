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

var ruleA7 = rule{
	ID: "A7", Group: "A", Severity: SevData, DefaultOn: true, Scope: scopePerApp, Reads: []KindName{KindMilestones.Name},
	Title: "An app's milestone dates differ from the rest of its train",
	Check: func(b *Board) []Hit {
		var out []Hit
		for i := range b.Trains {
			t := &b.Trains[i]
			off, usual := trainDateSplit(t)
			for _, v := range off {
				start, due := *v.Milestone.Start, *v.Milestone.Due
				ev := Evidence{"start": start, "due": due}
				text := fmt.Sprintf("Apps in %s disagree on dates: %s → %s here", t.ID, start.label(), due.label())
				if usual != nil {
					ev["usualStart"], ev["usualDue"] = usual.start, usual.due
					text = fmt.Sprintf("Milestone %s → %s, the rest of %s runs %s → %s. Give it its own train or move the milestone",
						start.label(), due.label(), t.ID, usual.start.label(), usual.due.label())
				}
				out = append(out, *versionHit(t, v, text, ev, Link{Label: "Open milestone", URL: v.Milestone.URL}))
			}
		}
		return out
	},
}

type datePair struct{ start, due Day }

// trainDateSplit finds the start+due pair most of a train's dated apps share.
// A strict majority gives the apps off that pair and the pair; no majority
// gives every dated app and nil; fewer than two dated apps, or all agreeing, gives nothing.
func trainDateSplit(t *Train) ([]*AppVersion, *datePair) {
	var dated []*AppVersion
	counts := map[datePair]int{}
	for _, v := range t.Versions {
		if v.Milestone.Start != nil && v.Milestone.Due != nil {
			dated = append(dated, v)
			counts[datePair{*v.Milestone.Start, *v.Milestone.Due}]++
		}
	}
	var top datePair
	for p, n := range counts {
		if n > counts[top] {
			top = p
		}
	}
	switch n := counts[top]; {
	case len(dated) < 2 || n == len(dated):
		return nil, nil
	case n*2 <= len(dated):
		return dated, nil
	}
	var off []*AppVersion
	for _, v := range dated {
		if (datePair{*v.Milestone.Start, *v.Milestone.Due}) != top {
			off = append(off, v)
		}
	}
	return off, &top
}

func datesSplit(t *Train) []AppID {
	off, _ := trainDateSplit(t)
	out := make([]AppID, len(off))
	for i, v := range off {
		out[i] = v.App
	}
	return out
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
