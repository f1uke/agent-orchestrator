package releasetrain

import (
	"strconv"
	"time"
)

// SprintState is a sprint's state in Jira; the Board reads the active and future ones.
type SprintState string

// Sprint states.
const (
	SprintActive SprintState = "active"
	SprintFuture SprintState = "future"
)

// Enum lists the sprint states for the API schema.
func (SprintState) Enum() []any { return []any{SprintActive, SprintFuture} }

// SprintFact is one sprint of a configured agile board; a stored fact with no State is active.
type SprintFact struct {
	Board int         `json:"board"`
	ID    int         `json:"id"`
	Name  string      `json:"name"`
	State SprintState `json:"state,omitempty"`
	Start *Day        `json:"start,omitempty"`
	End   *Day        `json:"end,omitempty"`
	URL   string      `json:"url"`
}

// SprintMemberFact is one issue Jira's "This Sprint" filter lists: in the sprint and in no closed one.
type SprintMemberFact struct {
	Key string `json:"key"`
}

// Sprint fact kinds. jira.sprint_cards holds a sprint's cards and their subtasks;
// jira.branch_cards holds the cards with an MR into a sprint branch the Board follows.
var (
	KindBoardSprints = atVersion(newKind[SprintFact]("jira.board_sprints", "active and next sprints", Snapshotted, 30*time.Minute, perBoard), 2)
	KindSprintCards  = newKind[IssueFact]("jira.sprint_cards", "sprint cards", Snapshotted, 5*time.Minute, perSprint)
	KindThisSprint   = newKind[SprintMemberFact]("jira.this_sprint", "This Sprint set", Snapshotted, 10*time.Minute, perSprint)
	KindBranchCards  = newKind[IssueFact]("jira.branch_cards", "sprint branch cards", Snapshotted, 5*time.Minute, global)
)

// BranchCardsScope is the scope of the Jira read of every card with an MR into a followed sprint branch.
const BranchCardsScope Scope = "sprint-branches"

// ItemKey keys a SprintFact within its resource.
func (f SprintFact) ItemKey() string { return strconv.Itoa(f.ID) }

// ItemKey keys a SprintMemberFact within its resource.
func (f SprintMemberFact) ItemKey() string { return f.Key }

// BoardScope is the scope of one board's active and future sprints.
func BoardScope(id int) Scope { return Scope("board:" + strconv.Itoa(id)) }

// SprintScope is the scope of one sprint's issue reads.
func SprintScope(id int) Scope { return Scope("sprint:" + strconv.Itoa(id)) }

// activeSprint is the board's active sprint as the snapshot has it: the lowest
// id when Jira reports more than one.
func activeSprint(snap Snapshot, board int) (SprintFact, bool) {
	var best SprintFact
	found := false
	for _, s := range Items(snap, KindBoardSprints, BoardScope(board)) {
		if s.State != SprintFuture && (!found || s.ID < best.ID) {
			best, found = s, true
		}
	}
	return best, found
}

func (s SprintFact) dated() bool { return s.Start != nil && s.End != nil }

// nextSprint is the board's next sprint with its dates as the Board shows them:
// the earliest-starting future sprint with both dates, else the undated one
// with the lowest id, whose dates are estimated from the active sprint's length
// and flagged. With no dates on the active sprint there is nothing to estimate from.
func nextSprint(snap Snapshot, board int) (sprint SprintFact, estimated, ok bool) {
	active, hasActive := activeSprint(snap, board)
	var dated, undated *SprintFact
	items := Items(snap, KindBoardSprints, BoardScope(board))
	for i := range items {
		s := &items[i]
		switch {
		case s.State != SprintFuture:
		case s.dated():
			if dated == nil || s.Start.Before(*dated.Start) || *s.Start == *dated.Start && s.ID < dated.ID {
				dated = s
			}
		case undated == nil || s.ID < undated.ID:
			undated = s
		}
	}
	switch {
	case dated != nil:
		return *dated, false, true
	case undated != nil && hasActive && active.dated():
		est := *undated
		length := active.End.Sub(*active.Start)
		start := active.End.Add(1)
		end := start.Add(length)
		est.Start, est.End = &start, &end
		return est, true, true
	}
	return SprintFact{}, false, false
}

func readSprints(cfg Config, snap Snapshot) []SprintFact {
	out := activeSprints(cfg, snap)
	for _, b := range cfg.Boards {
		if s, _, ok := nextSprint(snap, b.ID); ok {
			out = append(out, s)
		}
	}
	return out
}

func activeSprints(cfg Config, snap Snapshot) []SprintFact {
	var out []SprintFact
	for _, b := range cfg.Boards {
		if s, ok := activeSprint(snap, b.ID); ok {
			out = append(out, s)
		}
	}
	return out
}
