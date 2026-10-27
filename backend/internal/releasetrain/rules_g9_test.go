package releasetrain

import (
	"strconv"
	"testing"
)

func TestG9MergedCardMRIntoASprintBranchWithoutAMilestone(t *testing.T) {
	f := newFixture(t, today)
	v := f.version("di", "22", "3.6.0", "2026-10-30", "2026-11-06")
	iid := v.cardMR("DEMO-31 limit", "feat/DEMO-31", "feature/sprint-2026-21", MRMerged, "2026-10-12", "2026-10-13")

	hits := firedHits(f.reconcile(), "G9")

	want := "!" + strconv.Itoa(iid) + " has no milestone; it rides #22, set Release 3.6.0"
	if len(hits) != 1 || hits[0].Text != want {
		t.Fatalf("G9 = %+v; want one hit %q", hits, want)
	}
	h := hits[0]
	if h.Subject != (SubjMR{App: "di", IID: iid, Source: "feat/DEMO-31", Target: "feature/sprint-2026-21"}) || h.Severity != SevHygiene {
		t.Fatalf("G9 subject %+v severity %v; want the MR as a hygiene hit", h.Subject, h.Severity)
	}
	if h.App != "di" || h.Train == nil || *h.Train != (TrainID{Number: 22, Year: 2026}) || h.Evidence["card"] != "DEMO-31" {
		t.Fatalf("G9 app %q train %v card %v; want di, #22 and DEMO-31 so the release and card drawers show it", h.App, h.Train, h.Evidence["card"])
	}
}
