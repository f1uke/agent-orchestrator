package releasetrain

import (
	"sort"
	"strings"
	"time"
)

// Env is everything Reconcile needs from the outside world besides facts.
type Env struct {
	Now    time.Time
	Loc    *time.Location
	AgeOut int
}

// Inputs is the observed state Reconcile joins.
type Inputs struct {
	Config Config
	Snap   Snapshot
	Human  HumanState
}

// HumanState is what people set: rule toggles, hides and long-lived-branch plans.
type HumanState struct {
	RuleSettings map[RuleID]bool
	Hides        map[HitID]Hide
	Plans        []LlbPlan
}

// Hide records a hidden hit's evidence and severity at hide time.
type Hide struct {
	Fingerprint string
	Severity    Severity
	Reason      string
	HiddenAt    time.Time
}

// Board is the read model every surface renders.
type Board struct {
	Today      Day               `json:"today"`
	Apps       []BoardApp        `json:"apps"`
	Sources    []SourceFreshness `json:"sources"`
	Trains     []Train           `json:"trains"`
	Hotfixes   []Hotfix          `json:"hotfixes"`
	Mismatches []Mismatch        `json:"mismatches"`
	LLBs       []LLBGroup        `json:"llbs"`
	// Sprints is one row per configured board with an active sprint.
	Sprints []SprintRow `json:"sprints"`
	// Cutover is each app's first merge into a sprint branch; apps with none are absent.
	Cutover map[AppID]Day `json:"cutover"`
	// Identity is who "me" is for every Mine on the Board.
	Identity  IdentityState `json:"identity"`
	Alerts    Alerts        `json:"alerts"`
	Catalogue []RuleInfo    `json:"catalogue"`
}

// BoardApp is one configured app as every surface names it.
type BoardApp struct {
	ID       AppID    `json:"id"`
	Name     string   `json:"name"`
	Family   FamilyID `json:"family"`
	Platform Platform `json:"platform"`
}

// TrainPhase is where a train stands relative to today.
type TrainPhase string

// Train phases: the current train is the first one not yet published.
const (
	PhasePast    TrainPhase = "past"
	PhaseCurrent TrainPhase = "current"
	PhaseNext    TrainPhase = "next"
)

// Train is one release train across the apps that ride it.
type Train struct {
	ID       TrainID       `json:"id"`
	Label    string        `json:"label"`
	Phase    TrainPhase    `json:"phase"`
	Families []FamilyID    `json:"families"`
	Freeze   *Day          `json:"freeze,omitempty"`
	Publish  *Day          `json:"publish,omitempty"`
	Versions []*AppVersion `json:"versions"`
	// DatesSplit is the apps rule A7 flags, so a surface marks them without running rules.
	DatesSplit []AppID `json:"datesSplit"`
	// Pages is the release train page of each family that has one, in config order.
	Pages []PageFact `json:"pages"`
}

// SourceFreshness is one system's resources at a glance: the oldest complete
// read of live data, and every resource that is stale, never complete, or failing.
type SourceFreshness struct {
	System    string           `json:"system"`
	Resources int              `json:"resources"`
	AsOf      *time.Time       `json:"asOf,omitempty"`
	Issues    []ResourceStatus `json:"issues"`
}

// AppVersion is one app's version on one train.
type AppVersion struct {
	Train       TrainID          `json:"train"`
	App         AppID            `json:"app"`
	Version     string           `json:"version"`
	Milestone   MilestoneFact    `json:"milestone"`
	Jira        *JiraVersionFact `json:"jira,omitempty"`
	RelatedWork *RelatedWorkFact `json:"relatedWork,omitempty"`
	ReleaseMR   *ReleaseMR       `json:"releaseMr,omitempty"`
	Ticket      *IssueFact       `json:"ticket,omitempty"`
	Tag         *Tag             `json:"tag,omitempty"`
	ForceUpdate *ForceUpdateFact `json:"forceUpdate,omitempty"`
	Issues      []IssueFact      `json:"issues"`
	Stages      []Stage          `json:"stages"`
	Cards       []CardUnit       `json:"cards"`
	Mine        []MineReason     `json:"mine,omitempty"`
	page        *PageFact
}

// Hotfix is a milestone with no train label.
type Hotfix struct {
	App         AppID            `json:"app"`
	Version     string           `json:"version"`
	Milestone   MilestoneFact    `json:"milestone"`
	Jira        *JiraVersionFact `json:"jira,omitempty"`
	Tag         *Tag             `json:"tag,omitempty"`
	ForceUpdate *ForceUpdateFact `json:"forceUpdate,omitempty"`
	Mine        []MineReason     `json:"mine,omitempty"`
}

// ReleaseMR is a version's release MR with what its detail and jobs reads say.
type ReleaseMR struct {
	MR     MRFact        `json:"mr"`
	Detail *MRDetailFact `json:"detail,omitempty"`
	Jobs   *JobsFact     `json:"jobs,omitempty"`
}

// Tag is a version tag with the merges that landed on main after it.
type Tag struct {
	TagFact
	Newest      bool         `json:"newest"`
	MergesAfter []CommitFact `json:"mergesAfter"`
	CompareURL  string       `json:"compareUrl,omitempty"`
}

// MismatchKind says how a train label failed to join.
type MismatchKind string

// Mismatch kinds, one per rule A1-A4.
const (
	MismatchVersion     MismatchKind = "version"
	MismatchNoMilestone MismatchKind = "no-milestone"
	MismatchNoJira      MismatchKind = "no-jira"
	MismatchNoLabel     MismatchKind = "no-label"
)

// Mismatch is a join gap between GitLab milestones and Jira versions.
type Mismatch struct {
	Kind      MismatchKind     `json:"kind"`
	App       AppID            `json:"app"`
	Train     *TrainID         `json:"train,omitempty"`
	Milestone *MilestoneFact   `json:"milestone,omitempty"`
	Jira      *JiraVersionFact `json:"jira,omitempty"`
	Version   string           `json:"version,omitempty"`
}

func boardApps(cfg Config) []BoardApp {
	out := make([]BoardApp, 0, len(cfg.Apps))
	for _, a := range cfg.Apps {
		out = append(out, BoardApp{ID: a.ID, Name: a.Name, Family: cfg.familyOf(a.ID).ID, Platform: a.Platform})
	}
	return out
}

func phases(trains []Train, today Day) {
	var current *Train
	for i := range trains {
		t := &trains[i]
		t.Label = t.ID.String()
		if t.Publish != nil && !t.Publish.Before(today) && (current == nil || t.Publish.Before(*current.Publish)) {
			current = t
		}
	}
	for i := range trains {
		t := &trains[i]
		switch {
		case t.Publish != nil && t.Publish.Before(today):
			t.Phase = PhasePast
		case t == current, t.Freeze == nil || !t.Freeze.After(today):
			t.Phase = PhaseCurrent
		default:
			t.Phase = PhaseNext
		}
	}
}

// sources skips what the latest pass did not plan (never fetched again, it lingers
// until the unplanned sweep) and a gone branch's compare, which can never complete.
func (r *readSet) sources(trains []Train, llbs []LLBGroup) []SourceFreshness {
	live, gone := r.liveEntities(trains), goneCompares(llbs, r.cfg)
	all := r.snap.all()
	var lastPlan time.Time
	for _, st := range all {
		if st.PlannedAt.After(lastPlan) {
			lastPlan = st.PlannedAt
		}
	}
	bySystem := map[string]*SourceFreshness{}
	system := func(k KindName) *SourceFreshness {
		name, _, _ := strings.Cut(string(k), ".")
		s := bySystem[name]
		if s == nil {
			s = &SourceFreshness{System: name, Issues: []ResourceStatus{}}
			bySystem[name] = s
		}
		return s
	}
	for _, k := range ReadKinds() {
		system(k)
	}
	for _, st := range all {
		if gone[resKey{st.Kind, st.Scope}] || st.PlannedAt.Before(lastPlan) {
			continue
		}
		s := system(st.Kind)
		s.Resources++
		budget, settled := st.budget(), kinds[st.Kind].cover == perEntity && !live[resKey{st.Kind, st.Scope}]
		if settled {
			budget = SettledBudget
		}
		if st.ErrorClass != "" || st.CompleteAt == nil || r.now.Sub(*st.CompleteAt) > budget {
			s.Issues = append(s.Issues, st)
		}
		if !settled && st.CompleteAt != nil && (s.AsOf == nil || st.CompleteAt.Before(*s.AsOf)) {
			at := *st.CompleteAt
			s.AsOf = &at
		}
	}
	out := make([]SourceFreshness, 0, len(bySystem))
	for _, s := range bySystem {
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].System < out[j].System })
	return out
}

func goneCompares(llbs []LLBGroup, cfg Config) map[resKey]bool {
	out := map[resKey]bool{}
	for _, g := range llbs {
		for _, b := range g.Branches {
			if b.Gone {
				out[resKey{KindCompare.Name, CompareScope(cfg.app(b.App).GitLabProject, "develop", b.Branch)}] = true
			}
		}
	}
	return out
}

func (b *Board) familyOf(app AppID) FamilyID {
	for _, a := range b.Apps {
		if a.ID == app {
			return a.Family
		}
	}
	return ""
}

func containsString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
