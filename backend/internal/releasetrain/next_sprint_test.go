package releasetrain

import (
	"reflect"
	"testing"
)

func (f *fixture) futureSprint(board, n int, start, end string) *SprintFact {
	s := f.sprint(board, n, "2026-01-01", "2026-01-02")
	s.State, s.Start, s.End = SprintFuture, nil, nil
	if start != "" {
		s.Start = dayPtr(f.t, start)
	}
	if end != "" {
		s.End = dayPtr(f.t, end)
	}
	return s
}

func rowsOf(b Board, key string) []SprintRow {
	var out []SprintRow
	for _, r := range b.Sprints {
		if r.Board.Key == key {
			out = append(out, r)
		}
	}
	return out
}

func (f *fixture) plannedSprintIDs() []int {
	var ids []int
	for _, sp := range NewPlanView(f.cfg, f.snapshot(), f.env()).Sprints {
		ids = append(ids, sp.ID)
	}
	return ids
}

func TestNextSprintGetsItsOwnRowWithTheSameDataAndIsFlagged(t *testing.T) {
	f := newFixture(t, today)
	active := f.sprint(42, 7, "2026-10-06", "2026-10-20")
	f.sprintCard(active, "DEMO-1", "In Progress", true)
	f.mr("di", "DEMO-1 a", "feat/DEMO-1", "feature/sprint-7-2026", MROpened, "2026-10-07", "")
	next := f.futureSprint(42, 8, "2026-10-20", "2026-11-03")
	f.sprintCard(next, "DEMO-2", "To Do", true)
	f.mr("da", "DEMO-2 a", "feat/DEMO-2", "feature/sprint-7-2026", MROpened, "2026-10-12", "")
	f.sprintCard(next, "DEMO-3", "To Do", false)
	f.sprintCard(next, "DEMO-4", "To Do", true)

	rows := rowsOf(f.reconcile(), "DEMO")

	if len(rows) != 2 || rows[0].Next || !rows[1].Next {
		t.Fatalf("DEMO rows = %+v; want the active row, then the next one flagged", rows)
	}
	r := rows[1]
	if r.Number != 8 || r.Sprint.ID != next.ID || r.Sprint.Start.String() != "2026-10-20" || r.Sprint.End.String() != "2026-11-03" || r.Total != 3 {
		t.Fatalf("next row = number %d sprint %d total %d; want DEMO 8 on its own dates with its 3 cards", r.Number, r.Sprint.ID, r.Total)
	}
	if got := cardKeys(r); !reflect.DeepEqual(got, []string{"DEMO-2", "DEMO-3", "DEMO-4"}) {
		t.Fatalf("next cards = %v; want its three cards", got)
	}
	if r.Counts != (SprintCounts{New: 2, CarriedDone: 1}) {
		t.Fatalf("next counts = %+v; want the tallies the active row computes: 2 new, 1 carried (in no This Sprint set)", r.Counts)
	}
	if c := r.Cards[0]; len(c.Routes) != 1 || c.Routes[0].To != (ToTrain{Train: TrainID{Number: 8, Year: 2026}}) {
		t.Fatalf("DEMO-2 routes = %+v; want its open MR on its way to train 8, as for an active card", c.Routes)
	}
	if len(r.Dests) != 2 || r.Dests[0].Count != 1 || r.Dests[1].To != (NotRouted{}) {
		t.Fatalf("next dests = %+v; want train 8 then not routed, one card each", r.Dests)
	}
}

func TestABoardWithoutADatedNextSprintShowsOnlyItsActiveRow(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(f *fixture)
	}{
		{"no future sprint", func(f *fixture) {}},
		{"future sprint without dates", func(f *fixture) { f.futureSprint(42, 8, "", "") }},
		{"future sprint without an end", func(f *fixture) { f.futureSprint(42, 8, "2026-10-20", "") }},
		{"future sprint without a start", func(f *fixture) { f.futureSprint(42, 8, "", "2026-11-03") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, today)
			active := f.sprint(42, 7, "2026-10-06", "2026-10-20")
			f.sprintCard(active, "DEMO-1", "To Do", true)
			tc.setup(f)

			b := f.reconcile()

			if rows := rowsOf(b, "DEMO"); len(rows) != 1 || rows[0].Next {
				t.Fatalf("DEMO rows = %+v; want the active row alone", rows)
			}
			if b.Alerts.Badge.Unknown != 0 {
				t.Fatalf("unknown cells = %d; a missing next sprint is no coverage gap", b.Alerts.Badge.Unknown)
			}
			if got := f.plannedSprintIDs(); !reflect.DeepEqual(got, []int{active.ID}) {
				t.Fatalf("planned sprints = %v; want only the active one read", got)
			}
		})
	}
}

func TestTheNextSprintIsTheFutureOneThatStartsFirst(t *testing.T) {
	f := newFixture(t, today)
	active := f.sprint(42, 7, "2026-10-06", "2026-10-20")
	f.sprintCard(active, "DEMO-1", "To Do", true)
	later := f.futureSprint(42, 9, "2026-11-03", "2026-11-17")
	f.sprintCard(later, "DEMO-9", "To Do", true)
	sooner := f.futureSprint(42, 8, "2026-10-20", "2026-11-03")
	f.sprintCard(sooner, "DEMO-8", "To Do", true)
	f.futureSprint(42, 10, "", "")

	rows := rowsOf(f.reconcile(), "DEMO")

	if len(rows) != 2 || rows[1].Number != 8 || rows[1].Sprint.ID != sooner.ID {
		t.Fatalf("DEMO rows = %+v; want the active row and sprint 8, the earliest-starting future sprint", rows)
	}
	if got, want := f.plannedSprintIDs(), []int{active.ID, sooner.ID}; !reflect.DeepEqual(got, want) {
		t.Fatalf("planned sprints = %v; want the active and the next one only: %v", got, want)
	}
}

func TestTheActiveSprintStaysTheOneWhenFutureOnesHaveLowerIDs(t *testing.T) {
	f := newFixture(t, today)
	future := f.futureSprint(42, 8, "2026-10-20", "2026-11-03")
	active := f.sprint(42, 7, "2026-10-06", "2026-10-20")
	if future.ID >= active.ID {
		t.Fatalf("setup: future %d must have the lower id than active %d", future.ID, active.ID)
	}
	f.sprintCard(active, "DEMO-1", "To Do", true)

	rows := rowsOf(f.reconcile(), "DEMO")

	if len(rows) != 2 || rows[0].Sprint.ID != active.ID || rows[1].Sprint.ID != future.ID {
		t.Fatalf("DEMO rows = %+v; want active first whatever the ids", rows)
	}
}

func TestACardOfTheNextSprintRaisesNoAlertAndAFailingReadIsNoCoverageGap(t *testing.T) {
	f := newFixture(t, today)
	active := f.sprint(42, 7, "2026-10-06", "2026-10-20")
	f.sprintCard(active, "DEMO-0", "To Do", true)
	next := f.futureSprint(42, 8, "2026-10-20", "2026-11-03")
	f.sprintCard(next, "DEMO-1", "In Progress", true)
	f.mr("di", "DEMO-1 a", "feat/DEMO-1", "feature/sprint-7-2026", MRMerged, "2026-10-07", "2026-10-08")
	f.sprintCard(next, "DEMO-2", "In Progress", true)
	f.sprintCard(next, "DEMO-3", "In Progress", true)
	f.mr("da", "DEMO-3 a", "feat/DEMO-3", "feature/DEMO-9-base", MROpened, "2026-10-07", "")
	in := f.inputs()
	st, _ := in.Snap.Status(KindSprintCards.Name, SprintScope(next.ID))
	st.ErrorClass, st.Failures = ErrUnavailable, 3
	Put(&in.Snap, KindSprintCards, st, Items(in.Snap, KindSprintCards, SprintScope(next.ID)))

	b := Reconcile(in, f.env())

	if got := cardKeys(rowsOf(b, "DEMO")[1]); !reflect.DeepEqual(got, []string{"DEMO-1", "DEMO-2", "DEMO-3"}) {
		t.Fatalf("next cards = %v; want the three of the next sprint", got)
	}
	for _, rule := range []RuleID{"G1", "G2", "G3", "G4", "G5"} {
		if got := firedIDs(b, rule); got != nil {
			t.Errorf("%s hits = %v; a card of the next sprint raises none", rule, got)
		}
		if _, ok := outcomeOf(b, rule, "").(Unknown); ok {
			t.Errorf("%s is unknown; a failing read of the next sprint is no coverage gap", rule)
		}
	}
	if b.Alerts.Badge.Unknown != 0 {
		t.Fatalf("unknown cells = %d; want none", b.Alerts.Badge.Unknown)
	}
}

func TestACardOfTheActiveSprintStillRaisesG1BesideANextSprint(t *testing.T) {
	f := newFixture(t, today)
	f.sprintCard(f.sprint(42, 7, "2026-10-06", "2026-10-20"), "DEMO-1", "In Progress", true)
	f.mr("di", "DEMO-1 a", "feat/DEMO-1", "feature/sprint-7-2026", MRMerged, "2026-10-07", "2026-10-08")
	f.futureSprint(42, 8, "2026-10-20", "2026-11-03")

	if got := firedIDs(f.reconcile(), "G1"); !reflect.DeepEqual(got, []string{"G1~c/DEMO-1"}) {
		t.Fatalf("G1 hits = %v; want the active sprint's card still read", got)
	}
}

func TestTheNextSprintDoesNotMoveTheSprintBranchOwnersNumber(t *testing.T) {
	f := newFixture(t, today)
	f.sprintCard(f.sprint(42, 7, "2026-10-06", "2026-10-20"), "DEMO-1", "In Progress", true)
	f.mr("di", "DEMO-1 a", "feat/DEMO-1", "feature/sprint-7-2026", MROpened, "2026-10-07", "")
	f.futureSprint(42, 8, "2026-10-20", "2026-11-03")

	b := f.reconcile()

	if n, _, ok := b.branchSprint(); !ok || n != 7 {
		t.Fatalf("branch sprint = %d %v; want the active sprint 7", n, ok)
	}
	if c := b.sprintCard("DEMO-1"); c == nil || len(c.Routes) != 1 || c.Routes[0].To != (ToTrain{Train: TrainID{Number: 8, Year: 2026}}) {
		t.Fatalf("DEMO-1 = %+v; want its route to train 8 unchanged", c)
	}
}
