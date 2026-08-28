package domain

import (
	"testing"
	"time"
)

// WHAT A HANDBACK MAY NOT LEAVE BEHIND.
//
// "not driven yet" and "cannot be driven" are the same empty case on screen, so
// a qa that neglected the most direct part of its job was invisible to the human
// and to itself. These say which cases count as left behind, and - just as
// importantly - which do not, because a gate that fires on finished work is one
// people learn to satisfy by lying.

func at(minute int) *time.Time {
	t := time.Date(2026, 8, 28, 4, minute, 0, 0, time.UTC)
	return &t
}

func recordedRun(v SmokeVerdict, note string) SmokeRun {
	return SmokeRun{ID: "r1", Seq: 1, Verdict: v, Note: note, RecordedAt: at(10)}
}

func TestSmokeHandbackGap_NamesOnlyTheCasesNobodyDrove(t *testing.T) {
	checks := []SmokeCheck{
		// Driven: a machine verdict.
		{ID: "passed", Runs: []SmokeRun{recordedRun(SmokePass, "3 runs, listed each time")}},
		// Driven: evidence with no verdict is a complete answer (#268), not a gap.
		{ID: "captured", Runs: []SmokeRun{recordedRun("", "captured the screen; the lag is not mine to call")}},
		// Driven: declared undriveable, which IS a recorded run.
		{ID: "press-hold", Runs: []SmokeRun{recordedRun(SmokeSkip, "tried a 1.2s ao sim drag; the menu never opened")}},
		// The human already played it, so it is not qa's to answer.
		{ID: "played-by-hand", Verdict: SmokePass, DecidedAt: at(5)},
		// Off the list entirely.
		{ID: "retired", RetiredAt: at(6), RetiredReason: "now covered by a Go test"},
		// A round the machine opened, captured into and never concluded. It is not
		// a result, so it is not an answer either.
		{ID: "abandoned", Runs: []SmokeRun{{ID: "r9", Seq: 1, CreatedAt: time.Now()}}},
		// Nothing at all.
		{ID: "untouched"},
	}

	gap := SmokeHandbackGap(checks)
	want := []string{"abandoned", "untouched"}
	if len(gap) != len(want) {
		t.Fatalf("gap = %v, want %v", gap, want)
	}
	for i, id := range want {
		if gap[i] != id {
			t.Fatalf("gap = %v, want %v", gap, want)
		}
	}
}

// A reasonless skip is exactly the state this whole change exists to end, but it
// is not the DOMAIN's job to refuse it - the service is. What the domain must not
// do is quietly re-open the gap for it: a skip is a recorded run whether or not
// it carries its reason, and a rule that said otherwise would let a case slide
// between two guards with neither owning it.
func TestSmokeCheck_MachineDroveCountsEveryRecordedRun(t *testing.T) {
	for _, tc := range []struct {
		name  string
		check SmokeCheck
		want  bool
	}{
		{"nothing", SmokeCheck{ID: "a"}, false},
		{"open run only", SmokeCheck{ID: "a", Runs: []SmokeRun{{ID: "r1"}}}, false},
		{"skip with a reason", SmokeCheck{ID: "a", Runs: []SmokeRun{recordedRun(SmokeSkip, "why")}}, true},
		{"skip with none", SmokeCheck{ID: "a", Runs: []SmokeRun{recordedRun(SmokeSkip, "")}}, true},
		{"evidence only", SmokeCheck{ID: "a", Runs: []SmokeRun{recordedRun("", "")}}, true},
	} {
		if got := tc.check.MachineDrove(); got != tc.want {
			t.Errorf("%s: MachineDrove() = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestSmokeHandbackGap_EmptyChecklistLeavesNothingBehind(t *testing.T) {
	if gap := SmokeHandbackGap(nil); len(gap) != 0 {
		t.Fatalf("an empty checklist produced a gap: %v", gap)
	}
}
