package releasetrain

import (
	"sort"
	"time"
)

// ResourceSpec is one resource a source wants observed.
type ResourceSpec struct {
	Kind    KindName
	Scope   Scope
	App     AppID
	Mode    SyncMode
	Version int
	Budget  time.Duration
}

// SpecFor is the spec of one resource of kind k, with the kind's mode, version and default budget.
func SpecFor[T any](k Kind[T], scope Scope, app AppID) ResourceSpec {
	return ResourceSpec{Kind: k.Name, Scope: scope, App: app, Mode: k.Mode, Version: k.Version, Budget: k.Budget}
}

// PlanView is what the observed facts say the sources should fetch next.
type PlanView struct {
	Config      Config
	Loc         *time.Location
	HistoryFrom Day
	Apps        []App
	Versions    []PlannedVersion
	Pages       []PlannedPage
	ApproverIDs []string
}

// PlannedPage is a train page attached to a train, with the card keys it lists.
type PlannedPage struct {
	ID   string
	Keys []string
	Live bool
}

// PlannedVersion is one app version on a train with the entity ids its per-entity reads need.
type PlannedVersion struct {
	App           AppID
	Project       int
	Live          bool
	JiraVersionID string
	ReleaseMR     int
	Pipeline      int
	Tag           string
}

// NewPlanView joins the snapshot as Reconcile does and lists the versions and people to fetch.
func NewPlanView(cfg Config, snap Snapshot, env Env) PlanView {
	b := Board{Today: DayOf(env.Now, env.Loc)}
	joinTrains(&b, cfg, snap)
	rs := readSet{today: b.Today, ageOut: env.AgeOut}
	pv := PlanView{Config: cfg, Loc: env.Loc, HistoryFrom: cfg.HistoryFrom, Apps: cfg.Apps}
	approvers := map[string]bool{}
	for i := range b.Trains {
		t := &b.Trains[i]
		for _, page := range t.Pages {
			pp := PlannedPage{ID: page.ID, Keys: page.Keys}
			for _, v := range t.Versions {
				if v.page != nil && v.page.ID == page.ID && rs.live(dueOf(t, v)) {
					pp.Live = true
				}
			}
			pv.Pages = append(pv.Pages, pp)
		}
		for _, v := range t.Versions {
			p := PlannedVersion{App: v.App, Project: v.Milestone.Project, Live: rs.live(dueOf(t, v))}
			if v.Jira != nil {
				p.JiraVersionID = v.Jira.ID
				for _, a := range v.Jira.Approvers {
					approvers[a.AccountID] = true
				}
			}
			if r := v.ReleaseMR; r != nil {
				p.ReleaseMR = r.MR.IID
				if r.Detail != nil && r.Detail.Pipeline != nil {
					p.Pipeline = r.Detail.Pipeline.ID
				}
			}
			if v.Tag != nil {
				p.Tag = v.Tag.Name
			}
			pv.Versions = append(pv.Versions, p)
		}
	}
	for id := range approvers {
		pv.ApproverIDs = append(pv.ApproverIDs, id)
	}
	sort.Strings(pv.ApproverIDs)
	return pv
}
