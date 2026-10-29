package releasetrain

import "fmt"

var ruleA1 = rule{
	ID: "A1", Group: "A", Severity: SevData, DefaultOn: true, Scope: scopePerApp, Reads: readsJoin,
	Subjects: []SubjectKind{SubjectVersion},
	Mine:     MineNever,
	Title:    "Milestone and Jira release name different versions",
	Check: func(b *Board) []Hit {
		return mismatchHits(b, MismatchVersion, func(m Mismatch) (Subject, Copy, *Day) {
			return SubjVersion{Train: *m.Train, App: m.App},
				Copy{
					What: fmt.Sprintf("Train %s: milestone is %s, Jira release is %s", m.Train.short(), versionOf(m.Milestone), m.Version),
					Do:   "Rename one so both name the same version.",
				}, m.Milestone.Due
		})
	},
}

var ruleA2 = rule{
	ID: "A2", Group: "A", Severity: SevData, DefaultOn: true, Scope: scopePerApp, Reads: readsJoin,
	Subjects: []SubjectKind{SubjectJiraVersion},
	Mine:     MineNever,
	Title:    "Jira release has a train label but no milestone",
	Check: func(b *Board) []Hit {
		return mismatchHits(b, MismatchNoMilestone, func(m Mismatch) (Subject, Copy, *Day) {
			return SubjJiraVersion{ID: m.Jira.ID, App: m.App, Name: m.Jira.Name},
				Copy{
					What: fmt.Sprintf("Jira release %s is in train %s, but no milestone is", m.Jira.Name, m.Train.short()),
					Do:   fmt.Sprintf("Add the train %s label to its milestone, or fix the label on the Jira release.", m.Train.short()),
				}, m.Jira.ReleaseDate
		})
	},
}

var ruleA3 = rule{
	ID: "A3", Group: "A", Severity: SevData, DefaultOn: true, Scope: scopePerApp, Reads: readsJoin,
	Subjects: []SubjectKind{SubjectMilestone},
	Mine:     MineNever,
	Title:    "Milestone has a train label but no Jira release",
	Check: func(b *Board) []Hit {
		return mismatchHits(b, MismatchNoJira, func(m Mismatch) (Subject, Copy, *Day) {
			return SubjMilestone{App: m.App, ID: m.Milestone.ID, Title: m.Milestone.Title},
				Copy{
					What: fmt.Sprintf("Milestone %s has no Jira release for train %s", m.Milestone.Title, m.Train.short()),
					Do:   fmt.Sprintf("Create the Jira release, or add the train %s label to it.", m.Train.short()),
				}, m.Milestone.Due
		})
	},
}

var ruleA4 = rule{
	ID: "A4", Group: "A", Severity: SevData, DefaultOn: true, Scope: scopePerApp, Reads: readsJoin,
	Subjects: []SubjectKind{SubjectJiraVersion},
	Mine:     MineNever,
	Title:    "Jira release has no train label and no hotfix milestone",
	Check: func(b *Board) []Hit {
		return mismatchHits(b, MismatchNoLabel, func(m Mismatch) (Subject, Copy, *Day) {
			return SubjJiraVersion{ID: m.Jira.ID, App: m.App, Name: m.Jira.Name},
				Copy{
					What: fmt.Sprintf("Jira release %s has no train label", m.Jira.Name),
					Do:   "Add its train label, or match it to a hotfix milestone.",
				}, m.Jira.ReleaseDate
		})
	},
}

var ruleA5 = rule{
	ID: "A5", Group: "A", Severity: SevData, DefaultOn: true, Scope: scopePerApp, Reads: readsJoin,
	Subjects: []SubjectKind{SubjectVersion},
	Mine:     MineNever,
	Title:    "Jira release date differs from milestone due date",
	Check: func(b *Board) []Hit {
		return forEachVersion(b, func(t *Train, v *AppVersion) *Hit {
			if v.Jira == nil || v.Jira.ReleaseDate == nil || v.Milestone.Due == nil || *v.Jira.ReleaseDate == *v.Milestone.Due {
				return nil
			}
			return versionHit(t, v, Copy{
				What: fmt.Sprintf("Train %s release dates differ: Jira %s, milestone %s", t.ID.short(), v.Jira.ReleaseDate.label(), v.Milestone.Due.label()),
				Do:   "Pick the right date. The fix sets it on the other one.",
			}, Evidence{"releaseDate": *v.Jira.ReleaseDate, "due": *v.Milestone.Due}).fix("Fix the date", jiraLink(v.Jira), milestoneLink(&v.Milestone))
		})
	},
}

var ruleA6 = rule{
	ID: "A6", Group: "A", Severity: SevData, DefaultOn: true, Scope: scopePerApp, Reads: []KindName{KindMilestones.Name, KindPages.Name},
	Subjects: []SubjectKind{SubjectVersionDate},
	Mine:     MineNever,
	Title:    "Release train page dates differ from the milestone",
	Check: func(b *Board) []Hit {
		var out []Hit
		for i := range b.Trains {
			t := &b.Trains[i]
			for _, v := range t.Versions {
				out = append(out, pageDateHits(t, v)...)
			}
		}
		return out
	},
}

func pageDateHits(t *Train, v *AppVersion) []Hit {
	p := v.page
	if p == nil {
		return nil
	}
	hit := func(field DateField, c Copy, ev Evidence, links ...Link) Hit {
		h := versionHit(t, v, c, ev).open(append([]Link{{Label: "Open page", URL: p.URL}}, links...)...)
		h.Subject = SubjVersionDate{Train: t.ID, App: v.App, Field: field}
		return *h
	}
	if p.ParseError != "" {
		return []Hit{hit(DatePage, Copy{
			What: fmt.Sprintf("The train %s page cannot be read", t.ID.short()),
			Do:   fmt.Sprintf("Check the page's date table (%s).", p.ParseError),
		}, Evidence{"page": p.ID, "parseError": p.ParseError})}
	}
	var out []Hit
	for _, d := range []struct {
		field           DateField
		page, milestone *Day
	}{
		{DateFreeze, p.Freeze, v.Milestone.Start},
		{DatePublish, p.Publish, v.Milestone.Due},
	} {
		if d.page == nil || d.milestone == nil || *d.page == *d.milestone {
			continue
		}
		out = append(out, hit(d.field, Copy{
			What: fmt.Sprintf("Train %s %s dates differ: page %s, milestone %s", t.ID.short(), d.field, d.page.label(), d.milestone.label()),
			Do:   "Correct the page or the milestone.",
		}, Evidence{"page": *d.page, "milestone": *d.milestone}, milestoneLink(&v.Milestone)))
	}
	return out
}

var ruleA7 = rule{
	ID: "A7", Group: "A", Severity: SevData, DefaultOn: true, Scope: scopePerApp, Reads: []KindName{KindMilestones.Name},
	Subjects: []SubjectKind{SubjectVersion},
	Mine:     MineNever,
	Title:    "An app's milestone dates differ from the rest of its train",
	Check: func(b *Board) []Hit {
		var out []Hit
		for i := range b.Trains {
			t := &b.Trains[i]
			off, usual := trainDateSplit(t)
			for _, v := range off {
				links := []Link{milestoneLink(&v.Milestone)}
				start, due := *v.Milestone.Start, *v.Milestone.Due
				ev := Evidence{"start": start, "due": due}
				what := fmt.Sprintf("The apps in train %s have different dates", t.ID.short())
				if usual != nil {
					ev["usualStart"], ev["usualDue"] = usual.start, usual.due
					what = fmt.Sprintf("Train %s dates differ here: %s → %s, the other apps run %s → %s",
						t.ID.short(), start.label(), due.label(), usual.start.label(), usual.due.label())
					if other := usualApp(t, *usual); other != nil {
						links = append(links, Link{Label: "Open " + b.appName(other.App) + " milestone", URL: other.Milestone.URL})
					}
				}
				out = append(out, *versionHit(t, v, Copy{What: what, Do: "Move this milestone, or give it its own train."}, ev).open(links...))
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

func usualApp(t *Train, usual datePair) *AppVersion {
	for _, v := range t.Versions {
		if v.Milestone.Start != nil && v.Milestone.Due != nil && (datePair{*v.Milestone.Start, *v.Milestone.Due}) == usual {
			return v
		}
	}
	return nil
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

func mismatchHits(b *Board, kind MismatchKind, describe func(Mismatch) (Subject, Copy, *Day)) []Hit {
	var out []Hit
	for _, m := range b.Mismatches {
		if m.Kind != kind {
			continue
		}
		subject, c, ages := describe(m)
		ev := Evidence{"kind": m.Kind, "version": m.Version}
		var links []Link
		if m.Jira != nil {
			ev["jira"] = m.Jira.Name
			links = append(links, jiraLink(m.Jira))
		}
		if m.Milestone != nil {
			ev["milestone"] = m.Milestone.Title
			links = append(links, milestoneLink(m.Milestone))
		}
		h := Hit{Subject: subject, Train: m.Train, App: m.App, AgesOn: ages, Evidence: ev, Copy: c}
		out = append(out, *h.open(links...))
	}
	return out
}
