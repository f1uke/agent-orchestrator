package releasetrain

import (
	"reflect"
	"sort"
	"testing"
	"time"
)

func TestPlanViewListsWhatLiveVersionsNeedFetched(t *testing.T) {
	f := newFixture(t, "2026-10-15")
	live := f.version("di", "21", "3.5.0", "2026-10-12", "2026-10-19")
	live.approvers("Ann:APPROVED", "Bob:PENDING")
	live.releaseMR("2026-10-12", MROpened)
	old := f.published("da", "19", "3.3.0", "2026-09-01", "2026-09-08")
	noJira := f.milestone("pi", "Release 1.4.0", f.label("pi", "21"), "2026-10-12", "2026-10-19")

	pv := NewPlanView(f.cfg, f.snapshot(), f.env())

	got := map[AppID]PlannedVersion{}
	for _, v := range pv.Versions {
		got[v.App] = v
	}
	want := map[AppID]PlannedVersion{
		"di": {App: "di", Project: 1001, Live: true, JiraVersionID: live.jira.ID, ReleaseMR: live.mr.IID, Pipeline: live.detail().Pipeline.ID},
		"da": {App: "da", Project: 1002, Live: false, JiraVersionID: old.jira.ID, ReleaseMR: old.mr.IID, Pipeline: old.detail().Pipeline.ID, Tag: "3.3.0"},
		"pi": {App: "pi", Project: noJira.Project, Live: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("planned versions =\n%+v\nwant\n%+v", got, want)
	}
	if ids := pv.ApproverIDs; !reflect.DeepEqual(ids, []string{"acc-Ann", "acc-Bob"}) {
		t.Fatalf("approver ids = %v", ids)
	}
	if len(pv.Apps) != 4 || pv.HistoryFrom != f.cfg.HistoryFrom {
		t.Fatalf("plan view apps %d history %v", len(pv.Apps), pv.HistoryFrom)
	}
}

func TestApproverNamesComeFromJiraUsers(t *testing.T) {
	f := newFixture(t, "2026-10-14")
	v := f.published("di", "20", "3.4.0", "2026-10-05", "2026-10-12")
	v.jira.Released = false
	v.jira.Approvers = []Approver{{AccountID: "acc-1", Status: "APPROVED"}, {AccountID: "acc-2", Status: "PENDING"}, {AccountID: "acc-3", Status: "PENDING"}}
	snap := f.snapshot()
	at := f.now
	Put(&snap, KindJiraUsers, ResourceStatus{Scope: UserScope("acc-2"), CompleteAt: &at}, []JiraUserFact{{AccountID: "acc-2", DisplayName: "Dee Example"}})

	b := Reconcile(Inputs{Config: f.cfg, Snap: snap, Human: f.human}, f.env())

	hits := activeHits(b)
	if len(hits) != 1 || hits[0].Text != "Tag 3.4.0 and due Mon 12 Oct passed, Jira release still unreleased; 2 of 3 approvers pending: Dee Example, acc-3" {
		t.Fatalf("hits = %+v", hits)
	}
	if name := Items(snap, KindJiraVersions, "project")[0].Approvers[1].Name; name != "" {
		t.Fatalf("Reconcile wrote %q into the snapshot's version fact", name)
	}
}

func TestPutEncodedDecodesThroughTheKind(t *testing.T) {
	info, ok := LookupKind(KindMRs.Name)
	if !ok || info.Mode != Incremental || info.Version != 1 || info.Budget != 2*time.Minute {
		t.Fatalf("LookupKind = %+v, %v", info, ok)
	}
	it, err := EncodeItem(KindMRs, MRFact{Project: 7, IID: 3, Title: "A change", CreatedOn: day(t, "2026-10-01")}, nil)
	if err != nil || it.Key != "3" {
		t.Fatalf("EncodeItem = %+v, %v", it, err)
	}
	var s Snapshot
	if err := s.PutEncoded(ResourceStatus{Kind: KindMRs.Name, Scope: ProjectScope(7)}, [][]byte{it.Body}); err != nil {
		t.Fatal(err)
	}
	if got := Items(s, KindMRs, ProjectScope(7)); len(got) != 1 || got[0].Title != "A change" {
		t.Fatalf("items = %+v", got)
	}
	if err := s.PutEncoded(ResourceStatus{Kind: KindMRs.Name, Scope: ProjectScope(7)}, [][]byte{[]byte("{")}); err == nil {
		t.Fatal("a broken body decoded")
	}
	if err := s.PutEncoded(ResourceStatus{Kind: "gone.kind"}, nil); err != ErrUnknownKind {
		t.Fatalf("unknown kind err = %v", err)
	}
}

func TestReadKindsCoverEveryRuleAndStage(t *testing.T) {
	got := ReadKinds()
	if !sort.SliceIsSorted(got, func(i, j int) bool { return got[i] < got[j] }) {
		t.Fatalf("ReadKinds not sorted: %v", got)
	}
	for _, r := range ruleTable() {
		for _, k := range r.Reads {
			if !reads(got, k) {
				t.Fatalf("ReadKinds lacks %s read by %s", k, r.ID)
			}
		}
	}
	for _, row := range stageTable {
		for _, k := range row.reads {
			if !reads(got, k) {
				t.Fatalf("ReadKinds lacks %s read by stage %s", k, row.key)
			}
		}
	}
}
