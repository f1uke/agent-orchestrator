package releasetrain

import "testing"

func TestAGoneLongLivedBranchesCompareRaisesNoAlertAndNoUnknownCell(t *testing.T) {
	f := newFixture(t, today)
	healthy(f)
	f.mr("di", "DEMO-1 wallet", "feat/DEMO-1", "feature/demo-gone", MRMerged, "2026-09-01", "2026-09-02")
	f.mr("di", "DEMO-2 wallet", "feat/DEMO-2", "feature/demo-gone", MRMerged, "2026-09-03", "2026-09-04")
	f.ahead("di", "feature/demo-gone", 0)
	base := f.reconcile()
	f.statuses[KindCompare.Name] = func(st *ResourceStatus) { st.CompleteAt, st.ErrorClass = nil, ErrNotFound }
	b := f.reconcile()

	if _, br := llbBranch(t, b, "di", "feature/demo-gone"); !br.Gone {
		t.Fatalf("demo-gone gone = false; the test needs a branch GitLab answers 404 for")
	}
	if len(b.Alerts.Hits) != len(base.Alerts.Hits) {
		var ids []HitID
		for _, h := range b.Alerts.Hits {
			ids = append(ids, h.ID)
		}
		t.Fatalf("hits with a 404 compare = %v; want the %d hits without it: a gone branch is data, not an alert", ids, len(base.Alerts.Hits))
	}
	if b.Alerts.Badge.Unknown != base.Alerts.Badge.Unknown {
		t.Fatalf("unknown cells = %d, want %d: a 404 on one branch must not blank a rule", b.Alerts.Badge.Unknown, base.Alerts.Badge.Unknown)
	}
}
