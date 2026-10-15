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
	// PersonIDs are the Jira accounts the Board shows a face for: approvers, assignees and the identity.
	PersonIDs []string
	// LLBs are the long-lived branches not yet landed.
	LLBs []PlannedLLB
	// LLBCardKeys are the card keys every long-lived branch carries.
	LLBCardKeys []string
	// MyAccountID is the Jira account jira.myself read; empty until it is read.
	MyAccountID string
	// WatchedCardKeys are the cards whose subtasks may be mine.
	WatchedCardKeys []string
	// Sprints are the active sprints jira.board_sprints read, one per board at most.
	Sprints []PlannedSprint
	// BranchCardKeys are the cards with an MR into a sprint branch the Board follows.
	BranchCardKeys []string
}

// PlannedSprint is one active sprint whose cards are read.
type PlannedSprint struct {
	Board int
	ID    int
}

// PlannedLLB is one long-lived branch whose ahead count is read.
type PlannedLLB struct {
	App     AppID
	Project int
	Branch  string
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

// peopleKinds are the issue kinds whose assignees the page draws a face for.
var peopleKinds = []Kind[IssueFact]{KindCards, KindPageCards, KindLLBCards, KindMySubtasks, KindSprintCards, KindBranchCards}

// NewPlanView joins the snapshot as Reconcile does and lists the versions and people to fetch.
func NewPlanView(cfg Config, snap Snapshot, env Env) PlanView {
	b := Board{Today: DayOf(env.Now, env.Loc)}
	joinTrains(&b, cfg, snap)
	rs := readSet{today: b.Today, ageOut: env.AgeOut}
	pv := PlanView{Config: cfg, Loc: env.Loc, HistoryFrom: cfg.HistoryFrom, Apps: cfg.Apps}
	people := map[string]bool{}
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
					people[a.AccountID] = true
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
	pv.LLBs, pv.LLBCardKeys = plannedLLBs(cfg, snap)
	for _, m := range AllItems(snap, KindJiraMyself) {
		pv.MyAccountID = m.AccountID
		people[m.AccountID] = true
	}
	for _, k := range peopleKinds {
		for _, is := range AllItems(snap, k) {
			if is.Assignee != nil && is.Assignee.AccountID != "" {
				people[is.Assignee.AccountID] = true
			}
		}
	}
	for id := range people {
		pv.PersonIDs = append(pv.PersonIDs, id)
	}
	sort.Strings(pv.PersonIDs)
	for _, sp := range activeSprints(cfg, snap) {
		pv.Sprints = append(pv.Sprints, PlannedSprint{Board: sp.Board, ID: sp.ID})
	}
	live := make([]TrainID, 0, len(b.Trains))
	for id := range liveTrains(b.Trains, rs) {
		live = append(live, id)
	}
	pv.BranchCardKeys = sprintBranchKeys(cfg, snap, live)
	pv.WatchedCardKeys = watchedCards(b.Trains, rs, append(append([]string{}, pv.LLBCardKeys...), pv.BranchCardKeys...))
	return pv
}

func watchedCards(trains []Train, rs readSet, llbKeys []string) []string {
	keys := map[string]bool{}
	for _, k := range llbKeys {
		keys[k] = true
	}
	for i := range trains {
		t := &trains[i]
		live := false
		for _, v := range t.Versions {
			if !rs.live(dueOf(t, v)) {
				continue
			}
			live = true
			for _, is := range v.Issues {
				keys[is.Key] = true
			}
		}
		for _, p := range t.Pages {
			for _, k := range p.Keys {
				if live {
					keys[k] = true
				}
			}
		}
	}
	out := make([]string, 0, len(keys))
	for k := range keys {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func plannedLLBs(cfg Config, snap Snapshot) ([]PlannedLLB, []string) {
	branches, _ := detectLLBs(cfg, snap)
	var out []PlannedLLB
	keys := map[string]bool{}
	for _, b := range branches {
		if b.Landing == nil || b.Landing.MergedOn == nil {
			out = append(out, PlannedLLB{App: b.App, Project: cfg.app(b.App).GitLabProject, Branch: b.Branch})
		}
		for _, c := range b.Cards {
			keys[c.Key] = true
		}
	}
	sorted := make([]string, 0, len(keys))
	for k := range keys {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)
	return out, sorted
}
