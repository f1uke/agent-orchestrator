package releasetrain

import (
	"sort"
	"strings"
)

type ruleScope int

const (
	scopePerApp ruleScope = iota + 1
	scopeGlobal
)

type rule struct {
	ID        RuleID
	Group     string
	Severity  Severity
	DefaultOn bool
	Title     string
	Hint      string
	Reads     []KindName
	Scope     ruleScope
	Check     func(b *Board) []Hit
}

var (
	readsJoin        = []KindName{KindMilestones.Name, KindJiraVersions.Name}
	readsReleaseMR   = []KindName{KindMilestones.Name, KindMRs.Name, KindMRDetail.Name}
	readsReleaseJobs = []KindName{KindMilestones.Name, KindMRs.Name, KindMRDetail.Name, KindJobs.Name}
)

func ruleTable() []rule {
	return []rule{
		ruleA1, ruleA2, ruleA3, ruleA4, ruleA5, ruleA6, ruleA7,
		ruleB1, ruleB3,
		ruleC1, ruleC2, ruleC3, ruleC5, ruleC6, ruleC7,
		ruleD1, ruleD2, ruleD3,
		ruleE1, ruleE2, ruleE4, ruleE5,
		ruleF1, ruleF2,
		ruleG1, ruleG2, ruleG3, ruleG4, ruleG5,
		ruleL1, ruleL3, ruleL8, ruleL9,
	}
}

// RuleInfo is one rule as the catalogue serves it, with its effective on/off.
type RuleInfo struct {
	ID        RuleID   `json:"id"`
	Group     string   `json:"group"`
	Severity  Severity `json:"severity"`
	DefaultOn bool     `json:"defaultOn"`
	On        bool     `json:"on"`
	Title     string   `json:"title"`
	Hint      string   `json:"hint,omitempty"`
	Fix       FixKind  `json:"fix,omitempty"`
}

// LookupRule returns the catalogue row of id under settings, or false when no rule has that id.
func LookupRule(id RuleID, settings map[RuleID]bool) (RuleInfo, bool) {
	for _, r := range catalogue(settings) {
		if r.ID == id {
			return r, true
		}
	}
	return RuleInfo{}, false
}

func catalogue(settings map[RuleID]bool) []RuleInfo {
	table := ruleTable()
	out := make([]RuleInfo, len(table))
	for i, r := range table {
		out[i] = RuleInfo{ID: r.ID, Group: r.Group, Severity: r.Severity, DefaultOn: r.DefaultOn,
			On: r.effectiveOn(settings), Title: r.Title, Hint: r.Hint, Fix: fixKindOf(r.ID)}
	}
	return out
}

func (r rule) effectiveOn(settings map[RuleID]bool) bool {
	if on, set := settings[r.ID]; set {
		return on
	}
	return r.DefaultOn
}

func forEachVersion(b *Board, fn func(t *Train, v *AppVersion) *Hit) []Hit {
	var out []Hit
	for i := range b.Trains {
		t := &b.Trains[i]
		for _, v := range t.Versions {
			if h := fn(t, v); h != nil {
				out = append(out, *h)
			}
		}
	}
	return out
}

func versionHit(t *Train, v *AppVersion, text string, ev Evidence, links ...Link) *Hit {
	train := t.ID
	return &Hit{
		Subject: SubjVersion{Train: t.ID, App: v.App}, Train: &train, App: v.App,
		AgesOn: dueOf(t, v), Evidence: ev, Text: text, Links: links,
	}
}

func jiraLink(j *JiraVersionFact) Link {
	if j == nil {
		return Link{Label: "Open Jira release"}
	}
	return Link{Label: "Open Jira release", URL: j.URL}
}

func milestoneLink(m *MilestoneFact) Link { return Link{Label: "Open milestone", URL: m.URL} }

func pendingApprovers(j *JiraVersionFact) []string {
	var out []string
	for _, a := range j.Approvers {
		if a.Status != ApproverApproved {
			out = append(out, a.Name)
		}
	}
	return out
}

func published(t *Train, v *AppVersion, today Day) bool {
	due := dueOf(t, v)
	return v.Tag != nil && due != nil && due.Before(today)
}

func alerts(b *Board, in Inputs, rs *readSet) Alerts {
	a := Alerts{StaleHides: []StaleHide{}}
	matched := map[HitID]bool{}
	unsure := map[RuleID]bool{}
	for _, r := range ruleTable() {
		on := r.effectiveOn(in.Human.RuleSettings)
		hits := r.Check(b)
		for i := range hits {
			hits[i].Rule = r.ID
			hits[i].ID = hitID(r.ID, hits[i].Subject)
			if hits[i].Links == nil {
				hits[i].Links = []Link{}
			}
			if hits[i].Severity == 0 {
				hits[i].Severity = r.Severity
			}
			hits[i].Mine = mineOfSubject(b, hits[i].Subject)
			matched[hits[i].ID] = true
		}
		for _, cell := range cells(b, in.Config, r, hits) {
			o := evaluate(cell.hits, rs.coverageFor(r.Reads, cell.app, cell.versions))
			a.Rules = append(a.Rules, RuleOutcome{Rule: r.ID, App: cell.app, On: on, Outcome: o})
			if _, ok := o.(Unknown); ok {
				unsure[r.ID] = true
			}
			if !on {
				continue
			}
			switch o := o.(type) {
			case Unknown:
				a.Badge.Unknown++
			case Fired:
				for _, h := range o.Hits {
					a.Hits = append(a.Hits, bucket(h, rs, in.Human))
				}
			}
		}
	}
	sort.SliceStable(a.Hits, func(i, j int) bool {
		if a.Hits[i].Severity != a.Hits[j].Severity {
			return a.Hits[i].Severity < a.Hits[j].Severity
		}
		return a.Hits[i].ID < a.Hits[j].ID
	})
	for _, h := range a.Hits {
		if h.Bucket != BucketBadge {
			continue
		}
		a.Badge.Count++
		if a.Badge.Worst == nil || h.Severity.MoreSevereThan(*a.Badge.Worst) {
			s := h.Severity
			a.Badge.Worst = &s
		}
	}
	for id, hide := range in.Human.Hides {
		if !matched[id] && !unsure[id.Rule()] {
			a.StaleHides = append(a.StaleHides, StaleHide{ID: id, Reason: hide.Reason, HiddenAt: hide.HiddenAt})
		}
	}
	sort.Slice(a.StaleHides, func(i, j int) bool { return a.StaleHides[i].ID < a.StaleHides[j].ID })
	return a
}

type cell struct {
	app      AppID
	hits     []Hit
	versions []*AppVersion
}

func cells(b *Board, cfg Config, r rule, hits []Hit) []cell {
	if r.Scope == scopeGlobal {
		var all []*AppVersion
		for i := range b.Trains {
			all = append(all, b.Trains[i].Versions...)
		}
		return []cell{{hits: hits, versions: all}}
	}
	out := make([]cell, 0, len(cfg.Apps))
	for _, app := range cfg.Apps {
		c := cell{app: app.ID}
		for _, h := range hits {
			if h.App == app.ID {
				c.hits = append(c.hits, h)
			}
		}
		for i := range b.Trains {
			if v := b.Trains[i].version(app.ID); v != nil {
				c.versions = append(c.versions, v)
			}
		}
		out = append(out, c)
	}
	return out
}

func bucket(h Hit, rs *readSet, human HumanState) AlertHit {
	ah := AlertHit{Hit: h, Bucket: BucketBadge, Fix: fixKindOf(h.Rule)}
	if f, ok := human.OpenFixes[h.ID]; ok {
		ah.OpenFix = &f
	}
	if hide, ok := human.Hides[h.ID]; ok && !h.Severity.MoreSevereThan(hide.Severity) {
		ah.Bucket = BucketHidden
		ah.ChangedSinceHidden = hide.Fingerprint != h.Fingerprint()
		return ah
	}
	if h.AgesOn != nil && !rs.live(h.AgesOn) {
		ah.Bucket = BucketHistory
	}
	return ah
}

func joinNames(names []string) string { return strings.Join(names, ", ") }
