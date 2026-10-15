package releasetrain

import (
	"errors"
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
	if ids := pv.PersonIDs; !reflect.DeepEqual(ids, []string{"acc-Ann", "acc-Bob"}) {
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

func TestPlanViewReadsEveryPersonTheBoardShowsAFaceFor(t *testing.T) {
	f := newFixture(t, "2026-10-15")
	f.me()
	live := f.version("di", "21", "3.5.0", "2026-10-12", "2026-10-19")
	live.approvers("Ann:APPROVED")
	live.issue("DEMO-1", "In Progress", "indeterminate")
	f.issues[live.jira.ID][0].Assignee = &Person{AccountID: "acc-cara", Name: "Cara"}
	p := f.page("demo", "21", "2026-09-20", "2026-10-12", "2026-10-19")
	f.card(p, "DEMO-5").Assignee = &Person{AccountID: "acc-finn", Name: "Finn"}
	s := demoSprint(f)
	f.sprintCard(s, "DEMO-7", "In Progress", true).Assignee = &Person{AccountID: "acc-eve", Name: "Eve"}
	f.subtask(s, "DEMO-7", "DEMO-8", "iOS build", "indeterminate").Assignee = &Person{AccountID: "acc-gus", Name: "Gus"}
	f.llbCards = append(f.llbCards, &IssueFact{Key: "DEMO-2", Status: "In Progress", Assignee: &Person{AccountID: "acc-hal", Name: "Hal"}})
	f.branchIss = append(f.branchIss, &IssueFact{Key: "DEMO-9", Status: "In Progress", Assignee: &Person{AccountID: "acc-ivy", Name: "Ivy"}})
	f.subtasks = append(f.subtasks, &IssueFact{Key: "DEMO-6", Subtask: true, Parent: "DEMO-5", Assignee: dee})

	pv := NewPlanView(f.cfg, f.snapshot(), f.env())

	want := []string{"acc-Ann", "acc-cara", "acc-dee", "acc-eve", "acc-finn", "acc-gus", "acc-hal", "acc-ivy"}
	if !reflect.DeepEqual(pv.PersonIDs, want) {
		t.Fatalf("people read = %v, want every approver, assignee and the identity once: %v", pv.PersonIDs, want)
	}
}

func TestEveryIssueKindIsScannedForPeople(t *testing.T) {
	body, err := KindCards.Encode(IssueFact{Key: "DEMO-1"})
	if err != nil {
		t.Fatal(err)
	}
	var issueKinds []KindName
	for name, m := range kinds {
		if v, err := m.decode(body); err == nil {
			if _, ok := v.(IssueFact); ok {
				issueKinds = append(issueKinds, name)
			}
		}
	}
	sort.Slice(issueKinds, func(i, j int) bool { return issueKinds[i] < issueKinds[j] })
	var scanned []KindName
	for _, k := range peopleKinds {
		scanned = append(scanned, k.Name)
	}
	sort.Slice(scanned, func(i, j int) bool { return scanned[i] < scanned[j] })
	if !reflect.DeepEqual(issueKinds, scanned) {
		t.Fatalf("issue kinds %v, scanned for people %v: a new issue kind needs adding to peopleKinds", issueKinds, scanned)
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
	if err := s.PutEncoded(ResourceStatus{Kind: "gone.kind"}, nil); !errors.Is(err, ErrUnknownKind) {
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
