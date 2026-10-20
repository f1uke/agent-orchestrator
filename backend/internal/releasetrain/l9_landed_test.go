package releasetrain

import (
	"reflect"
	"strconv"
	"testing"
)

func l9Fixture(t *testing.T) *fixture {
	f := newFixture(t, today)
	f.mr("di", "DEMO-50 other", "feat/DEMO-50", "feature/sprint-20-2026", MRMerged, "2026-10-01", "2026-10-01")
	return f
}

func l9Fired(f *fixture) map[string]string {
	got := map[string]string{}
	for _, h := range firedHits(f.reconcile(), "L9") {
		got[string(h.ID)] = h.Text
	}
	return got
}

func l9ID(app string, mr *MRFact) string { return "L9~mr/" + app + "/" + strconv.Itoa(mr.IID) }

func TestL9OpenMRIntoAFeatureBranchSaysItIsOpen(t *testing.T) {
	f := l9Fixture(t)
	mr := f.mr("di", "DEMO-2 part", "feat/DEMO-2", "feat/DEMO-1-base", MROpened, "2026-10-12", "")
	want := map[string]string{l9ID("di", mr): "!" + strconv.Itoa(mr.IID) + " feat/DEMO-2 → feat/DEMO-1-base: open into a branch no train takes"}
	if got := l9Fired(f); !reflect.DeepEqual(got, want) {
		t.Fatalf("L9 = %v, want %v", got, want)
	}
}

func TestL9MergedMRWhoseTargetBranchLandedIsQuiet(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(f *fixture)
	}{
		{"landed in the sprint branch", func(f *fixture) {
			f.mr("di", "DEMO-1 base", "feat/DEMO-1-base", "feature/sprint-21-2026", MRMerged, "2026-10-10", "2026-10-13")
		}},
		{"landed in develop", func(f *fixture) {
			f.mr("di", "DEMO-1 base", "feat/DEMO-1-base", "develop", MRMerged, "2026-10-10", "2026-10-13")
		}},
		{"landed in a long-lived branch", func(f *fixture) {
			f.mr("di", "DEMO-1 base", "feat/DEMO-1-base", "feature/demo-wallet", MRMerged, "2026-10-10", "2026-10-13")
			f.mr("di", "DEMO-1 wallet", "feat/DEMO-1", "feature/demo-wallet", MRMerged, "2026-09-01", "2026-09-02")
		}},
		{"landed through a chain of branches", func(f *fixture) {
			f.mr("di", "base into mid", "feat/DEMO-1-base", "feat/DEMO-1-mid", MRMerged, "2026-10-10", "2026-10-12")
			f.mr("di", "mid into sprint", "feat/DEMO-1-mid", "feature/sprint-21-2026", MRMerged, "2026-10-10", "2026-10-13")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := l9Fixture(t)
			f.mr("di", "DEMO-2 part", "feat/DEMO-2", "feat/DEMO-1-base", MRMerged, "2026-10-11", "2026-10-12")
			tc.setup(f)
			for id := range l9Fired(f) {
				t.Errorf("L9 fired %s; the code of the merged MR reached a flow branch", id)
			}
		})
	}
}

func TestL9MergedMRStaysStrandedUntilItsTargetBranchLands(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(f *fixture)
	}{
		{"the target branch has no MR out", func(f *fixture) {}},
		{"the target branch has only an open MR into the sprint branch", func(f *fixture) {
			f.mr("di", "DEMO-1 base", "feat/DEMO-1-base", "feature/sprint-21-2026", MROpened, "2026-10-10", "")
		}},
		{"the target branch has only a closed MR into the sprint branch", func(f *fixture) {
			f.mr("di", "DEMO-1 base", "feat/DEMO-1-base", "feature/sprint-21-2026", MRClosed, "2026-10-10", "")
		}},
		{"the chain ends in another stranded branch", func(f *fixture) {
			f.mr("di", "base into mid", "feat/DEMO-1-base", "feat/DEMO-1-mid", MRMerged, "2026-10-10", "2026-10-13")
		}},
		{"another app's MR from a branch of the same name does not count", func(f *fixture) {
			f.mr("da", "DEMO-1 base", "feat/DEMO-1-base", "develop", MRMerged, "2026-10-10", "2026-10-13")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := l9Fixture(t)
			stranded := f.mr("di", "DEMO-2 part", "feat/DEMO-2", "feat/DEMO-1-base", MRMerged, "2026-10-11", "2026-10-12")
			tc.setup(f)
			want := "!" + strconv.Itoa(stranded.IID) + " feat/DEMO-2 → feat/DEMO-1-base: merged, but feat/DEMO-1-base has not landed in a sprint branch or develop"
			if got := l9Fired(f); got[l9ID("di", stranded)] != want {
				t.Fatalf("L9 = %v, want the stranded MR with %q", got, want)
			}
		})
	}
}

func TestL9ChainWalkStopsOnACycle(t *testing.T) {
	f := l9Fixture(t)
	a := f.mr("di", "a into b", "feat/DEMO-8-a", "feat/DEMO-9-b", MRMerged, "2026-10-11", "2026-10-12")
	f.mr("di", "b into a", "feat/DEMO-9-b", "feat/DEMO-8-a", MRMerged, "2026-10-11", "2026-10-12")
	if _, ok := l9Fired(f)[l9ID("di", a)]; !ok {
		t.Fatalf("a cycle of merged MRs never reaches a flow branch, so it stays stranded")
	}
}

func TestL9ClosedAndOldMergedMRsStayQuiet(t *testing.T) {
	f := l9Fixture(t)
	f.mr("di", "closed", "feat/DEMO-2", "feat/DEMO-1-base", MRClosed, "2026-10-12", "")
	f.mr("di", "before cutover", "feat/DEMO-3", "feat/DEMO-1-base", MRMerged, "2026-09-01", "2026-09-02")
	if got := l9Fired(f); len(got) != 0 {
		t.Fatalf("L9 = %v, want none", got)
	}
}
