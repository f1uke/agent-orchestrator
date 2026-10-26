package releasetrain

import (
	"reflect"
	"slices"
	"strconv"
	"testing"
)

func movedTo(key, to string) *IssueFact { return &IssueFact{Key: key, MovedTo: to} }

func TestASprintBranchMRWithAMovedKeyGivesItsTrainOnlyTheValidCard(t *testing.T) {
	f := newFixture(t, today)
	v := f.version("di", "21", "3.5.0", liveFreeze, liveDue)
	iid := v.cardMR("OLD-1 DEMO-31 limit", "feat/OLD-1", "feature/sprint-20-2026", MRMerged, "2026-10-01", "2026-10-02")
	f.branchIss = append(f.branchIss, &IssueFact{Key: "DEMO-31", Status: "Done", StatusCategory: StatusCategoryDone}, movedTo("OLD-1", "NEW-9"))

	b := f.reconcile()

	if cardOn(b, "di", "OLD-1") != nil || cardOn(b, "di", "NEW-9") != nil || cardOn(b, "di", "DEMO-31") == nil {
		t.Fatalf("train 21 cards %v; want DEMO-31 and neither the moved key nor the card it now names", cardKeysOn(b, "di"))
	}
	hits := firedHits(b, "G6")
	want := "!" + strconv.Itoa(iid) + " uses a card key that Jira moved: OLD-1 → NEW-9; fix the MR title"
	if len(hits) != 1 || hits[0].Text != want || hits[0].Subject != (SubjMR{App: "di", IID: iid, Source: "feat/OLD-1", Target: "feature/sprint-20-2026"}) {
		t.Fatalf("G6 = %+v; want one hit on the MR: %q", hits, want)
	}
}

func TestTheSprintOwnerPullsInNoOffBoardCardForAMovedKey(t *testing.T) {
	f := newFixture(t, today)
	own := f.sprint(42, 7, "2026-10-06", "2026-10-20")
	f.sprintCard(own, "DEMO-1", "In Progress", true)
	f.mr("di", "OLD-1 coupon", "feat/OLD-1", "feature/sprint-2026-7", MROpened, "2026-10-07", "")
	f.branchIss = append(f.branchIss, movedTo("OLD-1", "NEW-9"))

	b := f.reconcile()

	if got := cardKeys(rowOf(t, b, "DEMO")); !reflect.DeepEqual(got, []string{"DEMO-1"}) {
		t.Fatalf("DEMO cards = %v; want only DEMO-1", got)
	}
	if hits := firedHits(b, "G6"); len(hits) != 1 {
		t.Fatalf("G6 = %+v; want one hit on the open MR", hits)
	}
}

func TestAClosedMRWithAMovedKeyIsQuiet(t *testing.T) {
	f := newFixture(t, today)
	f.mr("di", "OLD-1 coupon", "feat/OLD-1", "feature/sprint-2026-7", MRClosed, "2026-10-07", "")
	f.branchIss = append(f.branchIss, movedTo("OLD-1", "NEW-9"))

	if hits := firedHits(f.reconcile(), "G6"); len(hits) != 0 {
		t.Fatalf("G6 = %+v; want none for a closed MR", hits)
	}
}

func TestThePlanKeepsReadingAMovedLongLivedKeyButWatchesNoSubtasksUnderIt(t *testing.T) {
	f := newFixture(t, today)
	f.me()
	f.mr("di", "DEMO-2 wallet", "feat/DEMO-2", "feature/demo-wallet", MRMerged, "2026-10-01", "2026-10-02")
	f.mr("di", "OLD-1 limit", "feat/OLD-1", "feature/demo-wallet", MRMerged, "2026-10-03", "2026-10-04")
	f.llbCards = append(f.llbCards, &IssueFact{Key: "DEMO-2", Status: "In Progress"}, movedTo("OLD-1", "NEW-9"))

	pv := NewPlanView(f.cfg, f.snapshot(), f.env())

	if want := []string{"DEMO-2", "OLD-1"}; !reflect.DeepEqual(pv.LLBCardKeys, want) {
		t.Fatalf("long-lived card keys = %v, want %v: dropping OLD-1 would lose the fact that it moved", pv.LLBCardKeys, want)
	}
	if slices.Contains(pv.WatchedCardKeys, "OLD-1") || !slices.Contains(pv.WatchedCardKeys, "DEMO-2") {
		t.Fatalf("watched = %v; want DEMO-2 and not OLD-1, whose subtasks are the unrelated card's", pv.WatchedCardKeys)
	}
}

func cardKeysOn(b Board, app AppID) []string {
	var out []string
	for _, tr := range b.Trains {
		if v := tr.version(app); v != nil {
			for _, c := range v.Cards {
				out = append(out, c.Key)
			}
		}
	}
	return out
}
