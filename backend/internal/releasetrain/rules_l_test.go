package releasetrain

import (
	"reflect"
	"strconv"
	"testing"
)

func TestL1OpenMRFromALongLivedBranchIntoTrunkAfterCutover(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(f *fixture) []*MRFact
		plan  func(mrs []*MRFact) *LlbPlan
		want  func(mrs []*MRFact) []string
	}{
		{"open into develop after the cutover", func(f *fixture) []*MRFact {
			return []*MRFact{f.mr("di", "wallet", "feature/demo-wallet", "develop", MROpened, "2026-10-12", "")}
		}, nil, func(mrs []*MRFact) []string { return []string{"L1~mr/di/" + strconv.Itoa(mrs[0].IID)} }},
		{"open into main counts too, one hit per MR", func(f *fixture) []*MRFact {
			return []*MRFact{
				f.mr("di", "wallet", "feature/demo-wallet", "develop", MROpened, "2026-10-12", ""),
				f.mr("di", "wallet", "feature/demo-wallet", "main", MROpened, "2026-10-13", ""),
			}
		}, nil, func(mrs []*MRFact) []string {
			return []string{"L1~mr/di/" + strconv.Itoa(mrs[0].IID), "L1~mr/di/" + strconv.Itoa(mrs[1].IID)}
		}},
		{"open into the sprint branch is right", func(f *fixture) []*MRFact {
			return []*MRFact{f.mr("di", "wallet", "feature/demo-wallet", "feature/sprint-21-2026", MROpened, "2026-10-12", "")}
		}, nil, func([]*MRFact) []string { return nil }},
		{"merged into develop is L2, not built", func(f *fixture) []*MRFact {
			return []*MRFact{f.mr("di", "wallet", "feature/demo-wallet", "develop", MRMerged, "2026-10-12", "2026-10-12")}
		}, nil, func([]*MRFact) []string { return nil }},
		{"an accepted MR raises nothing", func(f *fixture) []*MRFact {
			return []*MRFact{f.mr("di", "wallet", "feature/demo-wallet", "develop", MROpened, "2026-10-12", "")}
		}, func(mrs []*MRFact) *LlbPlan {
			return &LlbPlan{ID: "demo-wallet", Name: "Wallet", TargetKind: TargetUnplanned,
				Accepted: []PlanAccepted{{MRRef: "di!" + strconv.Itoa(mrs[0].IID), Reason: "ships in #21 as intended"}}}
		}, func([]*MRFact) []string { return nil }},
		{"no cutover yet in another app", func(f *fixture) []*MRFact {
			f.mr("da", "DEMO-1 wallet", "feat/DEMO-1", "feature/demo-wallet", MRMerged, "2026-09-01", "2026-09-02")
			return []*MRFact{f.mr("da", "wallet", "feature/demo-wallet", "develop", MROpened, "2026-10-12", "")}
		}, nil, func([]*MRFact) []string { return nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, today)
			f.mr("di", "DEMO-50 other", "feat/DEMO-50", "feature/sprint-20-2026", MRMerged, "2026-10-01", "2026-10-01")
			f.mr("di", "DEMO-1 wallet", "feat/DEMO-1", "feature/demo-wallet", MRMerged, "2026-09-01", "2026-09-02")
			mrs := tc.setup(f)
			if tc.plan != nil {
				f.human.Plans = []LlbPlan{*tc.plan(mrs)}
			}
			b := f.reconcile()
			if got, want := firedIDs(b, "L1"), tc.want(mrs); !reflect.DeepEqual(got, want) {
				t.Fatalf("L1 hits = %v, want %v", got, want)
			}
			if _, ok := outcomeOf(b, "L1", "di").(Unknown); ok {
				t.Fatalf("L1 is unknown on fresh MRs")
			}
		})
	}
}

func TestL1EvidenceAndText(t *testing.T) {
	f := newFixture(t, today)
	f.mr("di", "DEMO-50 other", "feat/DEMO-50", "feature/sprint-20-2026", MRMerged, "2026-10-01", "2026-10-01")
	f.mr("di", "DEMO-1 wallet", "feat/DEMO-1", "feature/demo-wallet", MRMerged, "2026-09-01", "2026-09-02")
	mr := f.mr("di", "wallet", "feature/demo-wallet", "develop", MROpened, "2026-10-12", "")
	hits := firedHits(f.reconcile(), "L1")
	if len(hits) != 1 {
		t.Fatalf("L1 hits = %v", hits)
	}
	h := hits[0]
	want := "!" + strconv.Itoa(mr.IID) + " feature/demo-wallet → develop is open; land it in the sprint branch instead"
	if h.Text != want || h.Severity != SevBlock || h.App != "di" || h.Train != nil || h.AgesOn != nil || len(h.Links) != 1 || h.Links[0].URL != mr.URL {
		t.Fatalf("L1 hit = %+v, want text %q, block, app di, no train, never ages, the MR link", h, want)
	}
}

func TestL3CardOnATrainParkedInALongLivedBranchThatDoesNotTargetIt(t *testing.T) {
	train22 := &TrainID{Number: 22, Year: fixtureYear}
	for _, tc := range []struct {
		name string
		plan *LlbPlan
		why  string
		want []string
		text string
	}{
		{"no plan", nil, "page", []string{"L3~ct/21-2026/di/DEMO-7"}, "DEMO-7 is on train #21, but its code sits in feature/demo-wallet (no target train)"},
		{"planned for another train", &LlbPlan{TargetKind: TargetTrain, TargetTrain: train22}, "page", []string{"L3~ct/21-2026/di/DEMO-7"},
			"DEMO-7 is on train #21, but its code sits in feature/demo-wallet (targets #22)"},
		{"dropped", &LlbPlan{TargetKind: TargetDropped}, "page", []string{"L3~ct/21-2026/di/DEMO-7"},
			"DEMO-7 is on train #21, but its code sits in feature/demo-wallet (dropped)"},
		{"planned for this train", &LlbPlan{TargetKind: TargetTrain, TargetTrain: &TrainID{Number: 21, Year: fixtureYear}}, "page", nil, ""},
		{"by fixVersion only", nil, "fixVersion", []string{"L3~ct/21-2026/di/DEMO-7"}, "DEMO-7 is on train #21, but its code sits in feature/demo-wallet (no target train)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, today)
			v := f.version("di", "21", "3.5.0", liveFreeze, liveDue)
			v.releaseMR(liveFreeze, MROpened)
			f.version("di", "22", "3.6.0", "2026-10-20", "2026-10-27")
			if tc.why == "page" {
				p := f.page("demo", "21", "2026-09-20", liveFreeze, liveDue)
				f.card(p, "DEMO-7")
			} else {
				v.issue("DEMO-7", "In Progress", "indeterminate")
			}
			f.mr("di", "DEMO-7 wallet", "feat/DEMO-7", "feature/demo-wallet", MRMerged, "2026-09-01", "2026-09-02")
			if tc.plan != nil {
				p := *tc.plan
				p.ID, p.Name = "demo-wallet", "Wallet"
				f.human.Plans = []LlbPlan{p}
			}
			b := f.reconcile()
			if got := firedIDs(b, "L3"); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("L3 hits = %v, want %v", got, tc.want)
			}
			if tc.text == "" {
				return
			}
			if links := firedHits(b, "L3")[0].Links; len(links) != 1 || links[0].URL != "https://jira.example.com/browse/DEMO-7" {
				t.Fatalf("L3 links = %+v, want the card's browse URL", links)
			}
			h := firedHits(b, "L3")[0]
			if h.Text != tc.text || h.Evidence["train"] != (TrainID{Number: 21, Year: fixtureYear}) || h.Evidence["group"] != PlanID("demo-wallet") || h.AgesOn == nil {
				t.Fatalf("L3 hit = %+v; want text %q, evidence naming train #21 and group demo-wallet, ageing with the version", h, tc.text)
			}
		})
	}
}

func TestL3FollowsAnEpicTarget(t *testing.T) {
	for _, tc := range []struct {
		name   string
		epicOn string
		want   []string
		text   string
	}{
		{"the epic's page is the card's train", "21", nil, ""},
		{"the epic's page is another train", "22", []string{"L3~ct/21-2026/di/DEMO-7"},
			"DEMO-7 is on train #21, but its code sits in feature/demo-wallet (targets #22 from epic DEMO-500, 2 of 2 cards)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, today)
			v := f.version("di", "21", "3.5.0", liveFreeze, liveDue)
			v.releaseMR(liveFreeze, MROpened)
			f.version("di", "22", "3.6.0", "2026-10-20", "2026-10-27")
			p21 := f.page("demo", "21", "2026-09-20", liveFreeze, liveDue)
			p22 := f.page("demo", "22", "2026-10-01", "2026-10-20", "2026-10-27")
			f.card(p21, "DEMO-7")
			if tc.epicOn == "21" {
				f.epic(p21, "DEMO-500")
			} else {
				f.epic(p22, "DEMO-500")
			}
			f.mr("di", "DEMO-7 wallet", "feat/DEMO-7", "feature/demo-wallet", MRMerged, "2026-09-01", "2026-09-02")
			f.mr("di", "DEMO-8 wallet", "feat/DEMO-8", "feature/demo-wallet", MRMerged, "2026-09-03", "2026-09-04")
			f.parent("DEMO-7", "DEMO-500", IssueTypeEpic)
			f.parent("DEMO-8", "DEMO-500", IssueTypeEpic)
			b := f.reconcile()
			if got := firedIDs(b, "L3"); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("L3 hits = %v, want %v", got, tc.want)
			}
			if tc.text != "" && firedHits(b, "L3")[0].Text != tc.text {
				t.Fatalf("L3 text = %q, want %q", firedHits(b, "L3")[0].Text, tc.text)
			}
		})
	}
}

func TestTargetTextNamesTheLaterPagesThatListTheSameEpic(t *testing.T) {
	t21, t22, t23 := TrainID{Number: 21, Year: fixtureYear}, TrainID{Number: 22, Year: fixtureYear}, TrainID{Number: 23, Year: fixtureYear}
	got := targetText(LLBFromEpic{Train: t21, Epic: "DEMO-500", Covered: 2, Total: 3, Also: []TrainID{t22, t23}})
	if want := "targets #21 from epic DEMO-500, 2 of 3 cards; #22 and #23 pages list it too"; got != want {
		t.Fatalf("targetText = %q, want %q", got, want)
	}
}

func TestL3QuietOnceTheBranchLandsInTheSourceSprint(t *testing.T) {
	f := newFixture(t, today)
	v := f.version("di", "21", "3.5.0", liveFreeze, liveDue)
	v.releaseMR(liveFreeze, MROpened)
	v.issue("DEMO-7", "In Progress", "indeterminate")
	in := f.mr("di", "DEMO-7 wallet", "feat/DEMO-7", "feature/demo-wallet", MRMerged, "2026-09-01", "2026-09-02")
	f.mr("di", "wallet", "feature/demo-wallet", "feature/sprint-20-2026", MRMerged, "2026-10-05", "2026-10-05")
	b := f.reconcile()
	if got := firedIDs(b, "L3"); got != nil {
		t.Fatalf("L3 hits = %v, want none: the card merged via the branch", got)
	}
	if code := b.Trains[0].version("di").Cards[0].Code; !reflect.DeepEqual(code, CodeMerged{MRs: []int{in.IID}, Via: "feature/demo-wallet"}) {
		t.Fatalf("code = %#v, want merged via feature/demo-wallet", code)
	}
}

func TestL9MRIntoABranchNoTrainTakes(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(f *fixture) *MRFact
		fires bool
	}{
		{"open into another card's branch", func(f *fixture) *MRFact {
			return f.mr("di", "DEMO-2 part", "feat/DEMO-2", "feat/DEMO-1-base", MROpened, "2026-10-12", "")
		}, true},
		{"merged into another card's branch after the cutover", func(f *fixture) *MRFact {
			return f.mr("di", "DEMO-2 part", "feat/DEMO-2", "feat/DEMO-1-base", MRMerged, "2026-10-11", "2026-10-12")
		}, true},
		{"merged on the cutover day", func(f *fixture) *MRFact {
			return f.mr("di", "DEMO-2 part", "feat/DEMO-2", "feat/DEMO-1-base", MRMerged, "2026-09-30", "2026-10-01")
		}, false},
		{"merged before the cutover", func(f *fixture) *MRFact {
			return f.mr("di", "DEMO-2 part", "feat/DEMO-2", "feat/DEMO-1-base", MRMerged, "2026-09-01", "2026-09-02")
		}, false},
		{"closed", func(f *fixture) *MRFact {
			return f.mr("di", "DEMO-2 part", "feat/DEMO-2", "feat/DEMO-1-base", MRClosed, "2026-10-12", "")
		}, false},
		{"into the sprint branch", func(f *fixture) *MRFact {
			return f.mr("di", "DEMO-2 part", "feat/DEMO-2", "feature/sprint-21-2026", MROpened, "2026-10-12", "")
		}, false},
		{"into develop", func(f *fixture) *MRFact {
			return f.mr("di", "DEMO-2 part", "feat/DEMO-2", "develop", MROpened, "2026-10-12", "")
		}, false},
		{"into main", func(f *fixture) *MRFact {
			return f.mr("di", "DEMO-2 part", "feat/DEMO-2", "main", MROpened, "2026-10-12", "")
		}, false},
		{"into master", func(f *fixture) *MRFact {
			return f.mr("di", "DEMO-2 part", "feat/DEMO-2", "master", MROpened, "2026-10-12", "")
		}, false},
		{"into a release branch", func(f *fixture) *MRFact {
			return f.mr("di", "DEMO-2 part", "feat/DEMO-2", "release/3.5.0", MROpened, "2026-10-12", "")
		}, false},
		{"into a hotfix branch", func(f *fixture) *MRFact {
			return f.mr("di", "DEMO-2 part", "feat/DEMO-2", "hotfix/3.4.1", MROpened, "2026-10-12", "")
		}, false},
		{"into a long-lived branch the Board found", func(f *fixture) *MRFact {
			return f.mr("di", "DEMO-2 part", "feat/DEMO-2", "feature/demo-wallet", MROpened, "2026-10-12", "")
		}, false},
		{"out of a long-lived branch", func(f *fixture) *MRFact {
			return f.mr("di", "wallet", "feature/demo-wallet", "feat/DEMO-1-base", MROpened, "2026-10-12", "")
		}, false},
		{"back out of a sprint branch", func(f *fixture) *MRFact {
			return f.mr("di", "sprint 20", "feature/sprint-20-2026", "feat/DEMO-1-base", MROpened, "2026-10-12", "")
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, today)
			f.mr("di", "DEMO-50 other", "feat/DEMO-50", "feature/sprint-20-2026", MRMerged, "2026-10-01", "2026-10-01")
			f.mr("di", "DEMO-1 wallet", "feat/DEMO-1", "feature/demo-wallet", MRMerged, "2026-09-01", "2026-09-02")
			mr := tc.setup(f)
			var want []string
			if tc.fires {
				want = []string{"L9~mr/di/" + strconv.Itoa(mr.IID)}
			}
			b := f.reconcile()
			if got := firedIDs(b, "L9"); !reflect.DeepEqual(got, want) {
				t.Fatalf("L9 hits = %v, want %v", got, want)
			}
			if _, ok := outcomeOf(b, "L9", "di").(Unknown); ok {
				t.Fatalf("L9 is unknown on fresh MRs")
			}
		})
	}
}

func TestL9CatchesAStackedMRIntoACardBranchThatMergesOut(t *testing.T) {
	f := newFixture(t, today)
	f.sprintCard(demoSprint(f), "DEMO-2", "In Progress", true)
	f.mr("di", "DEMO-50 other", "feat/DEMO-50", "feature/sprint-20-2026", MRMerged, "2026-10-01", "2026-10-01")
	stacked := f.mr("di", "DEMO-2 part", "feat/DEMO-2", "feature/DEMO-1-base", MROpened, "2026-10-12", "")
	f.mr("di", "DEMO-1 base", "feature/DEMO-1-base", "feature/sprint-21-2026", MROpened, "2026-10-12", "")
	b := f.reconcile()
	if got := llbBranches(b); len(got) != 0 {
		t.Fatalf("long-lived branches = %v, want none: one other card's MR into a card's branch is a stacked MR", got)
	}
	if got, want := firedIDs(b, "L9"), []string{"L9~mr/di/" + strconv.Itoa(stacked.IID)}; !reflect.DeepEqual(got, want) {
		t.Fatalf("L9 hits = %v, want the stacked MR %v", got, want)
	}
	f.human.Plans = []LlbPlan{{ID: "demo-1-base", Name: "base", TargetKind: TargetDropped, Version: 1,
		Branches: []PlanBranch{{App: "di", Branch: "feature/DEMO-1-base"}}}}
	planned := f.reconcile()
	if got, want := firedIDs(planned, "L9"), []string{"L9~mr/di/" + strconv.Itoa(stacked.IID)}; !reflect.DeepEqual(got, want) {
		t.Fatalf("L9 hits with a plan naming the branch = %v, want %v: a plan must not make a stacked branch long-lived", got, want)
	}
	if got := llbBranches(planned); len(got) != 0 {
		t.Fatalf("long-lived branches with a plan naming the branch = %v, want none", got)
	}
	card := rowOf(t, b, "DEMO").Cards[0]
	if len(card.Routes) != 1 || card.Routes[0].Kind != DestOther {
		t.Fatalf("DEMO-2 routes = %+v, want one off-flow route like any sprint card with an MR into an odd branch", card.Routes)
	}
}

func TestL9WithNoCutoverCatchesOpenMRsOnly(t *testing.T) {
	f := newFixture(t, today)
	open := f.mr("da", "DEMO-2 part", "feat/DEMO-2", "feat/DEMO-1-base", MROpened, "2026-10-12", "")
	f.mr("da", "DEMO-3 part", "feat/DEMO-3", "feat/DEMO-1-other", MRMerged, "2026-10-11", "2026-10-12")
	if got, want := firedIDs(f.reconcile(), "L9"), []string{"L9~mr/da/" + strconv.Itoa(open.IID)}; !reflect.DeepEqual(got, want) {
		t.Fatalf("L9 hits = %v, want %v", got, want)
	}
}

func TestL9EvidenceAndText(t *testing.T) {
	f := newFixture(t, today)
	f.mr("di", "DEMO-50 other", "feat/DEMO-50", "feature/sprint-20-2026", MRMerged, "2026-10-01", "2026-10-01")
	mr := f.mr("di", "DEMO-2 part", "feat/DEMO-2", "feat/DEMO-1-base", MROpened, "2026-10-12", "")
	hits := firedHits(f.reconcile(), "L9")
	if len(hits) != 1 {
		t.Fatalf("L9 hits = %v", hits)
	}
	h := hits[0]
	want := "!" + strconv.Itoa(mr.IID) + " feat/DEMO-2 → feat/DEMO-1-base: not a sprint branch, a long-lived branch or develop"
	if h.Text != want || h.Severity != SevRisk || h.App != "di" || h.Train != nil || h.AgesOn != nil || len(h.Links) != 1 || h.Links[0].URL != mr.URL {
		t.Fatalf("L9 hit = %+v, want text %q, risk, app di, no train, never ages, the MR link", h, want)
	}
	if h.Evidence["source"] != "feat/DEMO-2" || h.Evidence["target"] != "feat/DEMO-1-base" || h.Evidence["state"] != MROpened {
		t.Fatalf("L9 evidence = %v, want source, target and state", h.Evidence)
	}
}

func TestL9HitIsMineWhenIAuthoredTheMR(t *testing.T) {
	f := newFixture(t, today)
	f.me()
	mine := f.mr("di", "DEMO-2 part", "feat/DEMO-2", "feat/DEMO-1-base", MROpened, "2026-10-12", "")
	mine.AuthorUsername = "dee"
	theirs := f.mr("di", "DEMO-3 part", "feat/DEMO-3", "feat/DEMO-4-base", MROpened, "2026-10-12", "")
	got := map[int]bool{}
	for _, h := range firedHits(f.reconcile(), "L9") {
		got[h.Subject.(SubjMR).IID] = len(h.Mine) > 0
	}
	if want := map[int]bool{mine.IID: true, theirs.IID: false}; !reflect.DeepEqual(got, want) {
		t.Fatalf("L9 mine by MR = %v, want %v", got, want)
	}
}
