package releasetrain

import (
	"reflect"
	"strconv"
	"testing"
)

func TestEveryRuleDeclaresTheApprovedMinePolicy(t *testing.T) {
	want := map[RuleID]MinePolicy{
		"A1": MineNever, "A2": MineNever, "A3": MineNever, "A4": MineNever,
		"A5": MineNever, "A6": MineNever, "A7": MineNever, "F1": MineNever,
		"B1": MineOwner, "C1": MineOwner, "C2": MineOwner, "C3": MineOwner, "C5": MineOwner,
		"C6": MineOwner, "C7": MineOwner, "D1": MineOwner, "D2": MineOwner, "E4": MineOwner, "E5": MineOwner,
		"E1": MineOwner, "F2": MineOwner,
		"D3": MinePendingApprover,
		"B3": MineListedCards, "E2": MineListedCards, "G5": MineListedCards,
		"G1": MineListedCards, "G2": MineListedCards, "G3": MineListedCards, "G4": MineListedCards, "L3": MineListedCards,
		"L1": MineMR, "L8": MineMR, "L9": MineMR,
	}
	table := ruleTable()
	if len(table) != len(want) {
		t.Errorf("%d rules, the approved table has %d: give a new rule its mine policy here", len(table), len(want))
	}
	for _, r := range table {
		if r.Mine != want[r.ID] {
			t.Errorf("rule %s mine policy = %q, want %q", r.ID, r.Mine, want[r.ID])
		}
	}
}

func TestAnOwnerPolicyRuleIsAboutAVersionOrAHotfix(t *testing.T) {
	owned := map[SubjectKind]bool{SubjectVersion: true, SubjectHotfix: true, SubjectHotfixTrain: true}
	for _, r := range ruleTable() {
		if r.Mine != MineOwner {
			continue
		}
		for _, k := range r.Subjects {
			if !owned[k] {
				t.Errorf("rule %s is mine by owner but emits %s subjects, which have no owner", r.ID, k)
			}
		}
	}
}

func TestEveryHitOfAListedCardsRuleListsItsCards(t *testing.T) {
	f := everyRuleFires(t)
	in := f.inputs()
	for _, r := range ruleTable() {
		in.Human.RuleSettings[r.ID] = true
	}
	policy := map[RuleID]MinePolicy{}
	for _, r := range ruleTable() {
		policy[r.ID] = r.Mine
	}
	for _, h := range Reconcile(in, f.env()).Alerts.Hits {
		if policy[h.Rule] == MineListedCards && len(h.cards) == 0 {
			t.Errorf("%s lists no cards, so no card of mine can make it mine", h.ID)
		}
	}
}

func hitMine(t *testing.T, b Board, id HitID) ([]MineReason, bool) {
	t.Helper()
	for _, h := range b.Alerts.Hits {
		if h.ID == id {
			return h.Mine, true
		}
	}
	return nil, false
}

func expectMine(t *testing.T, b Board, id HitID, want []MineReason) {
	t.Helper()
	got, ok := hitMine(t, b, id)
	if !ok {
		var ids []HitID
		for _, h := range b.Alerts.Hits {
			ids = append(ids, h.ID)
		}
		t.Fatalf("no hit %s; hits %v", id, ids)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s mine = %+v, want %+v", id, got, want)
	}
}

func approvalTrain(t *testing.T, approvers ...string) (*fixture, *fv) {
	f := newFixture(t, liveFreeze)
	f.me()
	v := f.version("di", "21", "3.5.0", "2026-10-01", "2026-10-09")
	v.approvers(approvers...)
	v.releaseMR("2026-10-01", MROpened)
	return f, v
}

func TestD3IsMineOnlyWhileMyApprovalIsPending(t *testing.T) {
	const id = "D3~v/21-2026/di"
	t.Run("pending", func(t *testing.T) {
		f, _ := approvalTrain(t, "Ann:APPROVED", "dee:PENDING")
		expectMine(t, f.reconcile(), id, []MineReason{{Why: MineApprover}})
	})
	t.Run("approved, another pending, and I own the release", func(t *testing.T) {
		f, v := approvalTrain(t, "dee:APPROVED", "Bob:PENDING")
		v.mr.AuthorUsername = "dee"
		f.issues[v.jira.ID] = append(f.issues[v.jira.ID], &IssueFact{Key: "DEMO-9", ReleaseTicket: true, Assignee: dee, FixVersions: []string{v.jira.ID}})
		v.issue("DEMO-1", "Ready for UAT", "indeterminate")
		f.issues[v.jira.ID][len(f.issues[v.jira.ID])-1].Assignee = dee
		b := f.reconcile()
		if len(b.Trains[0].version("di").Mine) == 0 {
			t.Fatal("the version itself stays mine for the sticky label")
		}
		expectMine(t, b, id, nil)
	})
	t.Run("not an approver", func(t *testing.T) {
		f, _ := approvalTrain(t, "Ann:APPROVED", "Bob:PENDING")
		expectMine(t, f.reconcile(), id, nil)
	})
}

func TestAReleaseOwnerOwnsThePipelineButNotTheCompletenessChecks(t *testing.T) {
	for _, tc := range []struct {
		name  string
		owner func(f *fixture, v *fv) MineReason
	}{
		{"release MR author", func(f *fixture, v *fv) MineReason {
			v.mr.AuthorUsername = "dee"
			return MineReason{Why: MineReleaseMR, Ref: mrRef(*v.mr)}
		}},
		{"release ticket assignee", func(f *fixture, v *fv) MineReason {
			f.issues[v.jira.ID] = append(f.issues[v.jira.ID], &IssueFact{Key: "DEMO-9", ReleaseTicket: true, Assignee: dee, FixVersions: []string{v.jira.ID}})
			return MineReason{Why: MineReleaseTicket, Ref: "DEMO-9"}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, today)
			f.me()
			v := f.version("di", "21", "3.5.0", liveFreeze, liveDue)
			v.jira.Name = "iOS 3.5.1"
			v.approvers("dee:PENDING")
			v.issue("DEMO-1", "In Progress", "indeterminate")
			f.issues[v.jira.ID][0].Assignee = dee
			v.releaseMR(liveFreeze, MROpened)
			v.jobList().Jobs = []JobFact{{Name: "build_test", Status: "failed"}}
			reason := tc.owner(f, v)
			b := f.reconcile()
			expectMine(t, b, "A1~v/21-2026/di", nil)
			expectMine(t, b, "C1~v/21-2026/di", []MineReason{reason})
		})
	}
}

func TestAHotfixIsMineThroughMyMROnItsMilestone(t *testing.T) {
	f := newFixture(t, "2026-10-15")
	f.me()
	f.version("di", "21", "3.5.0", liveFreeze, liveDue)
	ms := f.milestone("di", "Hotfix 3.4.1", "crash", "2026-10-10", "2026-10-13")
	f.jiraVersion("di", "3.4.1", "", "2026-10-13")
	mr := f.mr("di", "fix crash", "hotfix/3.4.1", "main", MRMerged, "2026-10-10", "2026-10-11")
	mr.MilestoneID = &ms.ID
	other := f.mr("di", "fix crash too", "hotfix/3.4.1-b", "main", MRMerged, "2026-10-10", "2026-10-11")
	other.MilestoneID = &ms.ID

	b := f.reconcile()
	expectMine(t, b, "F2~ht/di/3.4.1/21-2026", nil)

	mr.AuthorUsername = "dee"
	b = f.reconcile()
	want := []MineReason{{Why: MineMRAuthor, Ref: mrRef(*mr)}}
	expectMine(t, b, "F2~ht/di/3.4.1/21-2026", want)
	expectMine(t, b, "E1~h/di/3.4.1", want)
}

func familyTrain(t *testing.T) (*fixture, map[string]*MRFact) {
	f := newFixture(t, today)
	f.me()
	di := f.version("di", "21", "3.5.0", "2026-10-19", "2026-10-26")
	da := f.version("da", "21", "2.5.0", "2026-10-19", "2026-10-26")
	mrs := map[string]*MRFact{}
	for _, key := range []string{"DEMO-1", "DEMO-2", "DEMO-3", "DEMO-4"} {
		for _, v := range []*fv{di, da} {
			v.issue(key, "In Progress", "indeterminate")
			mr := f.mr(v.app.ID, key+" part", "feat/"+key, "develop", MRMerged, "2026-10-01", "2026-10-02")
			mrs[string(v.app.ID)+key] = mr
		}
	}
	for _, v := range []*fv{di, da} {
		for _, is := range f.issues[v.jira.ID] {
			if is.Key == "DEMO-1" {
				is.Assignee = dee
			}
		}
	}
	mrs["daDEMO-3"].AuthorUsername = "dee"
	f.page("demo", "21", "2026-09-20", "2026-10-19", "2026-10-26")
	return f, mrs
}

func TestAPageFamilyAlertIsMineThroughMyListedCardsOnceEach(t *testing.T) {
	f, _ := familyTrain(t)
	expectMine(t, f.reconcile(), "G5~pf/21-2026/demo/merged", []MineReason{
		{Why: MineCard, Ref: "DEMO-1"},
		{Why: MineCard, Ref: "DEMO-3"},
	})
}

func TestB3AndE2AreMineOnlyThroughTheCardsTheyList(t *testing.T) {
	for _, tc := range []struct {
		rule  RuleID
		today string
	}{{"B3", "2026-10-21"}, {"E2", "2026-10-28"}} {
		t.Run(string(tc.rule), func(t *testing.T) {
			setup := func(mineListed bool) Board {
				f := newFixture(t, tc.today)
				f.me()
				v := f.version("di", "21", "3.5.0", "2026-10-19", "2026-10-26")
				v.releaseMR("2026-10-19", MROpened)
				v.mr.AuthorUsername = "dee"
				v.issue("DEMO-1", "Done", "done")
				v.issue("DEMO-2", "In Progress", "indeterminate")
				v.issue("DEMO-3", "In Progress", "indeterminate")
				f.issues[v.jira.ID][0].Assignee = dee
				if mineListed {
					f.issues[v.jira.ID][2].Assignee = dee
				}
				f.markNoMR("DEMO-2")
				return f.reconcile()
			}
			id := HitID(string(tc.rule) + "~v/21-2026/di")
			expectMine(t, setup(false), id, nil)
			expectMine(t, setup(true), id, []MineReason{{Why: MineCard, Ref: "DEMO-3"}})
		})
	}
}

func TestACardAlertIsMineThroughItsCard(t *testing.T) {
	f := newFixture(t, "2026-10-15")
	f.me()
	s := demoSprint(f)
	f.sprintCard(s, "DEMO-1", "In Progress", true)
	f.sprintCard(s, "DEMO-2", "In Progress", true).Assignee = dee
	b := f.reconcile()
	expectMine(t, b, "G2~c/DEMO-1", nil)
	expectMine(t, b, "G2~c/DEMO-2", []MineReason{{Why: MineCard, Ref: "DEMO-2"}})
}

func TestALandingAlertIsMineThroughACardOnTheBranch(t *testing.T) {
	f := newFixture(t, today)
	f.me()
	f.mr("di", "DEMO-50", "feat/DEMO-50", "feature/sprint-20-2026", MRMerged, "2026-10-01", "2026-10-01")
	f.mr("di", "DEMO-5 wallet", "feat/DEMO-5", "feature/demo-wallet", MRMerged, "2026-09-01", "2026-09-02")
	f.mr("di", "DEMO-5 wallet again", "feat/DEMO-5-b", "feature/demo-wallet", MRMerged, "2026-09-03", "2026-09-04")
	f.mr("di", "DEMO-6 wallet", "feat/DEMO-6", "feature/demo-wallet", MRMerged, "2026-09-03", "2026-09-04")
	f.llbCards = append(f.llbCards, &IssueFact{Key: "DEMO-5", Assignee: dee})
	land := f.mr("di", "wallet", "feature/demo-wallet", "develop", MROpened, "2026-10-12", "")
	expectMine(t, f.reconcile(), HitID("L1~mr/di/"+strconv.Itoa(land.IID)), []MineReason{{Why: MineCard, Ref: "DEMO-5"}})
}

func TestAnMRAlertIsMineThroughTheMROrItsCard(t *testing.T) {
	f := newFixture(t, today)
	f.me()
	f.mr("di", "DEMO-50", "feat/DEMO-50", "feature/sprint-20-2026", MRMerged, "2026-10-01", "2026-10-01")
	mine := f.mr("di", "DEMO-61 part", "feat/DEMO-61", "feat/DEMO-60-base", MROpened, "2026-10-13", "")
	mine.AuthorUsername = "dee"
	viaCard := f.mr("di", "DEMO-62 part", "feat/DEMO-62", "feat/DEMO-70-base", MROpened, "2026-10-13", "")
	f.sprintCard(demoSprint(f), "DEMO-62", "In Progress", true).Assignee = dee
	theirs := f.mr("di", "DEMO-63 part", "feat/DEMO-63", "feat/DEMO-80-base", MROpened, "2026-10-13", "")
	b := f.reconcile()
	expectMine(t, b, HitID("L9~mr/di/"+strconv.Itoa(mine.IID)), []MineReason{{Why: MineMRAuthor, Ref: mrRef(*mine)}})
	expectMine(t, b, HitID("L9~mr/di/"+strconv.Itoa(viaCard.IID)), []MineReason{{Why: MineCard, Ref: "DEMO-62"}})
	expectMine(t, b, HitID("L9~mr/di/"+strconv.Itoa(theirs.IID)), nil)
}
