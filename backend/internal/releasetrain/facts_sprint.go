package releasetrain

import (
	"strconv"
	"time"
)

// SprintState is where a sprint stands in Jira; the Board follows each board's active sprint and its next future one.
type SprintState string

// Sprint states the Board reads.
const (
	SprintActive SprintState = "active"
	SprintFuture SprintState = "future"
)

// Enum lists the states for the API schema.
func (SprintState) Enum() []any { return []any{SprintActive, SprintFuture} }

// SprintFact is one active or future sprint of a configured agile board; a stored fact with no State is active.
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
	KindBoardSprints = newKind[SprintFact]("jira.board_sprints", "active and next sprints", Snapshotted, 30*time.Minute, perBoard)
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

// nextSprint is the board's future sprint that starts first, when it has both
// dates; a board with none, or whose next one is undated, has no next sprint.
func nextSprint(snap Snapshot, board int) (SprintFact, bool) {
	var best SprintFact
	found := false
	for _, s := range Items(snap, KindBoardSprints, BoardScope(board)) {
		if s.State != SprintFuture || s.Start == nil {
			continue
		}
		if !found || s.Start.Before(*best.Start) || *s.Start == *best.Start && s.ID < best.ID {
			best, found = s, true
		}
	}
	if found && best.End == nil {
		return SprintFact{}, false
	}
	return best, found
}

// readSprints is every sprint whose cards the Board reads: each board's active sprint and its next one.
func readSprints(cfg Config, snap Snapshot) []SprintFact {
	out := activeSprints(cfg, snap)
	for _, b := range cfg.Boards {
		if s, ok := nextSprint(snap, b.ID); ok {
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
