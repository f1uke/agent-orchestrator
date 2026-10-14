package releasetrain

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// PlanID names a long-lived-branch plan.
type PlanID string

// TargetKind is where a plan sends its branches.
type TargetKind string

// Plan targets: a train, no train yet, or dropped.
const (
	TargetTrain     TargetKind = "train"
	TargetUnplanned TargetKind = "unplanned"
	TargetDropped   TargetKind = "dropped"
)

// Enum lists the target kinds for the API schema.
func (TargetKind) Enum() []any { return []any{TargetTrain, TargetUnplanned, TargetDropped} }

// PlanBranch is one app's long-lived branch in a plan.
type PlanBranch struct {
	App    AppID  `json:"app"`
	Branch string `json:"branch"`
}

// PlanAccepted is an MR into the long-lived branch a person accepted, with why.
type PlanAccepted struct {
	MRRef  string `json:"mrRef"`
	Reason string `json:"reason"`
}

// LlbPlan is what people decided about a long-lived branch group.
type LlbPlan struct {
	ID          PlanID         `json:"id"`
	Name        string         `json:"name"`
	TargetKind  TargetKind     `json:"targetKind"`
	TargetTrain *TrainID       `json:"targetTrain,omitempty"`
	Note        string         `json:"note"`
	Branches    []PlanBranch   `json:"branches"`
	Accepted    []PlanAccepted `json:"accepted"`
	Version     int            `json:"version"`
	UpdatedBy   string         `json:"updatedBy"`
	UpdatedAt   time.Time      `json:"updatedAt"`
}

// ErrInvalidPlan wraps every reason a plan input is refused before it is stored.
var ErrInvalidPlan = errors.New("invalid long-lived branch plan")

// PlanVersionError is a plan write whose ifVersion is not the stored version;
// Current is 0 when the plan does not exist.
type PlanVersionError struct {
	Plan    PlanID
	Current int
}

func (e *PlanVersionError) Error() string {
	return fmt.Sprintf("plan %s is at version %d; reload it and try again", e.Plan, e.Current)
}

// BranchTakenError is a plan write naming a branch another plan already holds.
type BranchTakenError struct {
	Branch PlanBranch
	Owner  PlanID
}

func (e *BranchTakenError) Error() string {
	return fmt.Sprintf("branch %s:%s is already in plan %s", e.Branch.App, e.Branch.Branch, e.Owner)
}

var planIDRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// Validate checks a plan before it is stored: an id fit for a URL, a name,
// a known target with a train exactly when the target is a train, and no
// branch or MR twice.
func (p LlbPlan) Validate() error {
	var problems []string
	if !planIDRE.MatchString(string(p.ID)) {
		problems = append(problems, "id must be 1-64 of a-z, 0-9, '.', '_', '-', starting with a letter or digit")
	}
	if strings.TrimSpace(p.Name) == "" {
		problems = append(problems, "name is required")
	}
	switch p.TargetKind {
	case TargetTrain:
		if p.TargetTrain == nil {
			problems = append(problems, "target train is required when the target is a train")
		}
	case TargetUnplanned, TargetDropped:
		if p.TargetTrain != nil {
			problems = append(problems, "target train is only for a train target")
		}
	default:
		problems = append(problems, fmt.Sprintf("target must be train, unplanned or dropped, not %q", p.TargetKind))
	}
	seen := map[PlanBranch]bool{}
	for _, b := range p.Branches {
		if b.App == "" || strings.TrimSpace(b.Branch) == "" {
			problems = append(problems, "every branch needs an app and a branch name")
		}
		if seen[b] {
			problems = append(problems, fmt.Sprintf("branch %s:%s is listed twice", b.App, b.Branch))
		}
		seen[b] = true
	}
	mrs := map[string]bool{}
	for _, a := range p.Accepted {
		if strings.TrimSpace(a.MRRef) == "" || strings.TrimSpace(a.Reason) == "" {
			problems = append(problems, "every accepted MR needs a ref and a reason")
		}
		if mrs[a.MRRef] {
			problems = append(problems, fmt.Sprintf("MR %s is accepted twice", a.MRRef))
		}
		mrs[a.MRRef] = true
	}
	if len(problems) > 0 {
		return fmt.Errorf("%w: %s", ErrInvalidPlan, strings.Join(problems, "; "))
	}
	return nil
}

var trainKeyRE = regexp.MustCompile(`^(\d+)(?:-(\d+))?-(\d{4})$`)

// ParseTrainKey reads a train key, "21-2026" or "20-1-2026", back into a TrainID.
func ParseTrainKey(s string) (TrainID, bool) {
	m := trainKeyRE.FindStringSubmatch(s)
	if m == nil {
		return TrainID{}, false
	}
	var id TrainID
	id.Number, _ = strconv.Atoi(m[1])
	if m[2] != "" {
		id.Extra, _ = strconv.Atoi(m[2])
	}
	id.Year, _ = strconv.Atoi(m[3])
	return id, true
}
