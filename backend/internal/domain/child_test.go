package domain

import "testing"

func TestDecideChildStop(t *testing.T) {
	cases := []struct {
		name     string
		facts    ChildStopFacts
		mayBlock bool
		want     ChildStopAction
	}{
		{"dirty child is asked to commit", ChildStopFacts{Dirty: true}, true, ChildStopBlockCommit},
		{"dirty child asked once is asked again", ChildStopFacts{Dirty: true, Blocks: 1}, true, ChildStopBlockCommit},
		{"dirty child past the block limit is committed by AO", ChildStopFacts{Dirty: true, Blocks: ChildStopBlockLimit}, true, ChildStopAutoCommit},
		{"dirty orphan is committed by AO", ChildStopFacts{Dirty: true}, false, ChildStopAutoCommit},
		{"clean child with no commits is removed", ChildStopFacts{}, true, ChildStopRemove},
		{"conflicting child is asked to rebase", ChildStopFacts{Commits: 2, Conflicts: []string{"a.go"}}, true, ChildStopBlockRebase},
		{"conflicting child past the block limit is parked", ChildStopFacts{Commits: 2, Conflicts: []string{"a.go"}, Blocks: ChildStopBlockLimit}, true, ChildStopPark},
		{"conflicting orphan is parked", ChildStopFacts{Commits: 1, Conflicts: []string{"a.go"}}, false, ChildStopPark},
		{"clean child with commits merges", ChildStopFacts{Commits: 3}, true, ChildStopMerge},
		{"clean orphan with commits merges", ChildStopFacts{Commits: 1}, false, ChildStopMerge},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := DecideChildStop(tc.facts, tc.mayBlock); got != tc.want {
				t.Fatalf("DecideChildStop(%+v, %v) = %q, want %q", tc.facts, tc.mayBlock, got, tc.want)
			}
		})
	}
}

func TestChildStateHoldsUndeliveredWork(t *testing.T) {
	undelivered := map[ChildState]bool{
		ChildRunning:   true,
		ChildMerging:   true,
		ChildHeld:      true,
		ChildConflict:  true,
		ChildMerged:    false,
		ChildRemoved:   false,
		ChildPreserved: false,
	}
	for state, want := range undelivered {
		if got := state.Undelivered(); got != want {
			t.Errorf("%s.Undelivered() = %v, want %v", state, got, want)
		}
		if !state.Valid() {
			t.Errorf("%s.Valid() = false", state)
		}
	}
	if ChildState("bogus").Valid() {
		t.Error(`ChildState("bogus").Valid() = true`)
	}
}

func TestParseChildAgentName(t *testing.T) {
	cases := map[string]struct {
		id string
		ok bool
	}{
		"agent-a1469c9b92bb394ab": {"a1469c9b92bb394ab", true},
		"agent-":                  {"", false},
		"manual-test":             {"", false},
		"agent-../../etc":         {"", false},
		"agent-ABC_def":           {"", false},
	}
	for name, want := range cases {
		id, ok := ParseChildAgentName(name)
		if id != want.id || ok != want.ok {
			t.Errorf("ParseChildAgentName(%q) = (%q, %v), want (%q, %v)", name, id, ok, want.id, want.ok)
		}
	}
}
