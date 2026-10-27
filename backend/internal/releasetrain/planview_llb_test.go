package releasetrain

import (
	"reflect"
	"testing"
)

func TestPlanViewListsLongLivedReadsAndMySubtaskCards(t *testing.T) {
	f := newFixture(t, "2026-10-15")
	live := f.version("di", "21", "3.5.0", "2026-10-12", "2026-10-19")
	live.issue("DEMO-1", "In Progress", "indeterminate")
	p := f.page("demo", "21", "2026-09-20", "2026-10-12", "2026-10-19")
	f.card(p, "DEMO-5")
	f.published("da", "18", "3.2.0", "2026-08-01", "2026-08-08").issue("DEMO-9", "Done", StatusCategoryDone)
	f.mr("di", "DEMO-2 wallet", "feat/DEMO-2", "feature/demo-wallet", MRMerged, "2026-09-01", "2026-09-02")
	f.mr("di", "DEMO-3 bond", "feat/DEMO-3", "feature/demo-bond", MRMerged, "2026-09-01", "2026-09-02")
	f.mr("di", "bond", "feature/demo-bond", "develop", MRMerged, "2026-09-10", "2026-09-11")

	pv := NewPlanView(f.cfg, f.snapshot(), f.env())
	if want := []PlannedLLB{{App: "di", Project: 1001, Branch: "feature/demo-wallet"}}; !reflect.DeepEqual(pv.LLBs, want) {
		t.Fatalf("planned long-lived = %+v, want only the branch not landed: %+v", pv.LLBs, want)
	}
	if want := []string{"DEMO-2", "DEMO-3"}; !reflect.DeepEqual(pv.LLBCardKeys, want) {
		t.Fatalf("long-lived card keys = %v, want %v", pv.LLBCardKeys, want)
	}
	if want := []string{"DEMO-1", "DEMO-2", "DEMO-3", "DEMO-5"}; !reflect.DeepEqual(pv.WatchedCardKeys, want) {
		t.Fatalf("watched %v, want %v (the settled #18 card left out)", pv.WatchedCardKeys, want)
	}
}
