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

// HumanState is what people set: rule toggles, hides, long-lived-branch plans
// and the cards marked "No MR needed".
type HumanState struct {
	RuleSettings map[RuleID]bool
	Hides        map[HitID]Hide
	Plans        []LlbPlan
	// OpenFixes is each hit's live fix, so the Board shows it without a second request.
	OpenFixes map[HitID]FixRef
	NoMR      map[string]NoMRMark
}

// NoMRMark says a person decided a card needs no MR; it lives in AO only.
type NoMRMark struct {
	Note     string    `json:"note,omitempty"`
	MarkedBy string    `json:"markedBy"`
	MarkedAt time.Time `json:"markedAt"`
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
	Today   Day               `json:"today"`
	Apps    []BoardApp        `json:"apps"`
	Sources []SourceFreshness `json:"sources"`
	// Kinds is every fact kind with its system, label and budget, sorted by kind.
	Kinds      []KindFreshness `json:"kinds"`
	Trains     []Train         `json:"trains"`
	Hotfixes   []Hotfix        `json:"hotfixes"`
	Mismatches []Mismatch      `json:"mismatches"`
	LLBs       []LLBGroup      `json:"llbs"`
	// Sprints is one row per configured board with an active sprint.
	Sprints []SprintRow `json:"sprints"`
	// Cutover is each app's first merge into a sprint branch; apps with none are absent.
	Cutover map[AppID]Day `json:"cutover"`
	// Identity is who "me" is for every Mine on the Board.
	Identity  IdentityState `json:"identity"`
	Alerts    Alerts        `json:"alerts"`
	Catalogue []RuleInfo    `json:"catalogue"`
	fix       fixContext
	mrs       map[AppID][]MRFact
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

// KindFreshness is one fact kind as a source tag names it, with the same
// freshness SourceFreshness gives its system, judged on this kind's reads alone.
type KindFreshness struct {
	Kind          KindName         `json:"kind"`
	System        string           `json:"system"`
	Label         string           `json:"label"`
	BudgetSeconds int              `json:"budgetSeconds"`
	Resources     int              `json:"resources"`
	AsOf          *time.Time       `json:"asOf,omitempty"`
	Issues        []ResourceStatus `json:"issues"`
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

// freshness skips what the latest pass did not plan (never fetched again, it lingers
// until the unplanned sweep) and a gone branch's compare, which can never complete.
// A settled version's per-entity read is judged by SettledBudget and does not age its kind.
func (r *readSet) freshness(trains []Train, llbs []LLBGroup) ([]SourceFreshness, []KindFreshness) {
	live, gone := r.liveEntities(trains), goneCompares(llbs, r.cfg)
	all := r.snap.all()
	var lastPlan time.Time
	for _, st := range all {
		if st.PlannedAt.After(lastPlan) {
			lastPlan = st.PlannedAt
		}
	}
	byKind := map[KindName]*KindFreshness{}
	for name, meta := range kinds {
		byKind[name] = &KindFreshness{Kind: name, System: systemOf(name), Label: meta.Label,
			BudgetSeconds: int(meta.Budget.Seconds()), Issues: []ResourceStatus{}}
	}
	for _, st := range all {
		k := byKind[st.Kind]
		if k == nil || gone[resKey{st.Kind, st.Scope}] || st.PlannedAt.Before(lastPlan) {
			continue
		}
		k.Resources++
		budget, settled := st.budget(), kinds[st.Kind].cover == perEntity && !live[resKey{st.Kind, st.Scope}]
		if settled {
			budget = SettledBudget
		}
		if st.ErrorClass != "" || st.CompleteAt == nil || r.now.Sub(*st.CompleteAt) > budget {
			k.Issues = append(k.Issues, st)
		}
		if !settled && st.CompleteAt != nil {
			k.AsOf = older(k.AsOf, *st.CompleteAt)
		}
	}
	kindList := make([]KindFreshness, 0, len(byKind))
	for _, k := range byKind {
		kindList = append(kindList, *k)
	}
	sort.Slice(kindList, func(i, j int) bool { return kindList[i].Kind < kindList[j].Kind })
	return systemsOf(kindList), kindList
}

// systemsOf rolls kinds up by system; only systems a rule or a stage reads are listed.
func systemsOf(kindList []KindFreshness) []SourceFreshness {
	bySystem := map[string]*SourceFreshness{}
	for _, k := range ReadKinds() {
		bySystem[systemOf(k)] = &SourceFreshness{System: systemOf(k), Issues: []ResourceStatus{}}
	}
	for _, k := range kindList {
		s := bySystem[k.System]
		if s == nil {
			s = &SourceFreshness{System: k.System, Issues: []ResourceStatus{}}
			bySystem[k.System] = s
		}
		s.Resources += k.Resources
		s.Issues = append(s.Issues, k.Issues...)
		if k.AsOf != nil {
			s.AsOf = older(s.AsOf, *k.AsOf)
		}
	}
	out := make([]SourceFreshness, 0, len(bySystem))
	for _, s := range bySystem {
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].System < out[j].System })
	return out
}

func older(cur *time.Time, at time.Time) *time.Time {
	if cur != nil && !at.Before(*cur) {
		return cur
	}
	return &at
}

func systemOf(k KindName) string {
	name, _, _ := strings.Cut(string(k), ".")
	return name
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
