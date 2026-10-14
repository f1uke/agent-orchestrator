package releasetrain

import (
	"strings"
	"testing"
)

func TestShippedNotReleasedGivesExactlyOneE1NamingPendingApprovers(t *testing.T) {
	f := newFixture(t, "2026-10-14")
	v := f.published("di", "20", "3.4.0", "2026-10-05", "2026-10-12")
	v.jira.Released = false
	v.approvers("Ann:APPROVED", "Bob:PENDING", "Cid:PENDING")

	b := f.reconcile()

	active := activeHits(b)
	if len(active) != 1 || active[0].Rule != "E1" {
		t.Fatalf("active hits = %v, want exactly one E1", hitIDs(active))
	}
	if want := HitID("E1~v/20-2026/di"); active[0].ID != want {
		t.Fatalf("hit id = %q, want %q", active[0].ID, want)
	}
	for _, name := range []string{"Bob", "Cid"} {
		if !strings.Contains(active[0].Text, name) {
			t.Fatalf("E1 text %q does not name pending approver %s", active[0].Text, name)
		}
	}
	if strings.Contains(active[0].Text, "Ann") {
		t.Fatalf("E1 text %q names Ann, who approved", active[0].Text)
	}
	if _, fired := outcomeOf(b, "D3", "di").(Fired); fired {
		t.Fatalf("D3 fired after publish; its window ends at publish")
	}
}
