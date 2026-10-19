package releasetrain

import (
	"reflect"
	"testing"
	"time"
)

var markedAt = time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)

func (f *fixture) markNoMR(key string) NoMRMark {
	if f.human.NoMR == nil {
		f.human.NoMR = map[string]NoMRMark{}
	}
	m := NoMRMark{Note: "config only", MarkedBy: "app", MarkedAt: markedAt}
	f.human.NoMR[key] = m
	return m
}

func sprintCardOf(t *testing.T, r SprintRow, key string) SprintCard {
	t.Helper()
	for _, c := range r.Cards {
		if c.Key == key {
			return c
		}
	}
	t.Fatalf("no %s in row %s", key, r.Board.Key)
	return SprintCard{}
}

func TestAMarkedSprintCardLeavesNotRoutedForItsOwnBlock(t *testing.T) {
	f := newFixture(t, today)
	s := demoSprint(f)
	f.sprintCard(s, "DEMO-1", "Ready for UAT", true)
	f.sprintCard(s, "DEMO-2", "Ready for UAT", true)
	mark := f.markNoMR("DEMO-1")

	row := rowOf(t, f.reconcile(), "DEMO")

	want := []SprintDest{
		{To: NotRouted{}, Count: 1, Keys: []string{"DEMO-2"}},
		{To: NoMRNeeded{}, Count: 1, Keys: []string{"DEMO-1"}},
	}
	if !reflect.DeepEqual(row.Dests, want) {
		t.Fatalf("dests = %+v, want the marked card in its own block after not routed", row.Dests)
	}
	if c := sprintCardOf(t, row, "DEMO-1"); c.NoMR == nil || *c.NoMR != mark {
		t.Fatalf("DEMO-1 noMr = %+v, want the mark", c.NoMR)
	}
	if c := sprintCardOf(t, row, "DEMO-2"); c.NoMR != nil {
		t.Fatalf("DEMO-2 noMr = %+v, want none", c.NoMR)
	}
	if row.Counts != (SprintCounts{New: 2}) {
		t.Fatalf("counts = %+v, want both cards still new this sprint", row.Counts)
	}
}

func TestAMarkedSprintCardWithAnMRKeepsItsRoute(t *testing.T) {
	f := newFixture(t, today)
	s := demoSprint(f)
	f.sprintCard(s, "DEMO-1", "In Progress", true)
	f.mr("di", "DEMO-1 a", "feat/DEMO-1", "feature/sprint-7-2026", MROpened, "2026-10-07", "")
	f.markNoMR("DEMO-1")

	row := rowOf(t, f.reconcile(), "DEMO")

	if len(row.Dests) != 1 || row.Dests[0].To.destKey() == (NoMRNeeded{}).destKey() {
		t.Fatalf("dests = %+v, want the MR's route, not the mark", row.Dests)
	}
	if c := sprintCardOf(t, row, "DEMO-1"); c.NoMR == nil {
		t.Fatal("the mark is gone from a card that has an MR; the sheet must still offer its undo")
	}
}

func TestAMarkedCardOnATrainIsInTheNoCodeState(t *testing.T) {
	f := newFixture(t, today)
	v := f.version("di", "7", "3.5.0", "2026-10-12", "2026-10-19")
	v.issue("DEMO-1", "Ready for UAT", "indeterminate")
	v.issue("DEMO-2", "Ready for UAT", "indeterminate")
	v.issue("DEMO-3", "In Progress", "indeterminate")
	f.mr("di", "DEMO-3 a", "feat/DEMO-3", "develop", MROpened, "2026-10-07", "")
	mark := f.markNoMR("DEMO-1")
	f.markNoMR("DEMO-3")

	codes := map[string]CodeState{}
	for _, c := range f.reconcile().Trains[0].Versions[0].Cards {
		codes[c.Key] = c.Code
	}
	want := map[string]CodeState{
		"DEMO-1": CodeNoMR{Note: mark.Note, MarkedBy: mark.MarkedBy, MarkedAt: mark.MarkedAt},
		"DEMO-2": CodeNone{},
		"DEMO-3": CodeOpen{MRs: []int{f.mrs[len(f.mrs)-1].IID}},
	}
	if !reflect.DeepEqual(codes, want) {
		t.Fatalf("codes = %+v, want %+v", codes, want)
	}
}

func TestG2SkipsAMarkedCardAndStillFiresForItsTwin(t *testing.T) {
	runG(t, "G2", []gCase{
		{"marked and unmarked twins", "2026-10-15", func(f *fixture) []string {
			s := demoSprint(f)
			f.sprintCard(s, "DEMO-1", "Ready for UAT", true)
			f.sprintCard(s, "DEMO-2", "Ready for UAT", true)
			f.markNoMR("DEMO-1")
			return []string{"G2~c/DEMO-2"}
		}, ""},
	})
}

func TestG5SkipsAMarkedCardAsItSkipsOneWithNoCode(t *testing.T) {
	runG(t, "G5", []gCase{
		{"DEMO-3 has no code and is marked", "", func(f *fixture) []string {
			pagedTrain(f, "2026-10-19")
			f.markNoMR("DEMO-3")
			return []string{"G5~pf/21-2026/demo/merged", "G5~pf/21-2026/demo/open"}
		}, ""},
	})
	f := newFixture(t, today)
	pagedTrain(f, "2026-10-19")
	f.markNoMR("DEMO-3")
	for _, h := range firedHits(f.reconcile(), "G5") {
		for _, k := range h.Subject.(SubjPageFamily).Keys {
			if k == "DEMO-3" {
				t.Fatalf("%s lists the marked DEMO-3", h.ID)
			}
		}
	}
}

func TestAMarkIsMineAsMarkedByYou(t *testing.T) {
	f := newFixture(t, today)
	f.me()
	s := demoSprint(f)
	f.sprintCard(s, "DEMO-1", "Ready for UAT", true)
	f.sprintCard(s, "DEMO-2", "Ready for UAT", true)
	v := f.version("di", "7", "3.5.0", "2026-10-12", "2026-10-19")
	v.issue("DEMO-1", "Ready for UAT", "indeterminate")
	f.markNoMR("DEMO-1")

	b := f.reconcile()
	want := []MineReason{{Why: MineNoMR}}
	row := rowOf(t, b, "DEMO")
	if got := sprintCardOf(t, row, "DEMO-1").Mine; !reflect.DeepEqual(got, want) {
		t.Fatalf("sprint DEMO-1 mine = %+v, want %+v", got, want)
	}
	if got := sprintCardOf(t, row, "DEMO-2").Mine; got != nil {
		t.Fatalf("sprint DEMO-2 mine = %+v, want none", got)
	}
	if got := b.Trains[0].Versions[0].Cards[0].Mine; !reflect.DeepEqual(got, want) {
		t.Fatalf("train DEMO-1 mine = %+v, want %+v", got, want)
	}
}
