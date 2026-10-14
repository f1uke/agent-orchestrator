package releasetrain

import "time"

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

// HumanState is what people set: rule toggles and hides.
type HumanState struct {
	RuleSettings map[RuleID]bool
	Hides        map[HitID]Hide
}

// Hide records a hidden hit's evidence and severity at hide time.
type Hide struct {
	Fingerprint string
	Severity    Severity
}

// Board is the read model every surface renders.
type Board struct {
	Today      Day        `json:"today"`
	Trains     []Train    `json:"trains"`
	Hotfixes   []Hotfix   `json:"hotfixes"`
	Mismatches []Mismatch `json:"mismatches"`
	Alerts     Alerts     `json:"alerts"`
}

// Train is one release train across the apps that ride it.
type Train struct {
	ID       TrainID       `json:"id"`
	Families []FamilyID    `json:"families"`
	Freeze   *Day          `json:"freeze,omitempty"`
	Publish  *Day          `json:"publish,omitempty"`
	Versions []*AppVersion `json:"versions"`
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
}

// Hotfix is a milestone with no train label.
type Hotfix struct {
	App         AppID            `json:"app"`
	Version     string           `json:"version"`
	Milestone   MilestoneFact    `json:"milestone"`
	Jira        *JiraVersionFact `json:"jira,omitempty"`
	Tag         *Tag             `json:"tag,omitempty"`
	ForceUpdate *ForceUpdateFact `json:"forceUpdate,omitempty"`
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
