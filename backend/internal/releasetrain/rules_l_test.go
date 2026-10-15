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
	if h.Text != want || h.Severity != SevBlock || h.App != "di" || h.Train != nil || h.AgesOn != nil || h.Link.URL != mr.URL {
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
			if link := firedHits(b, "L3")[0].Link; link.URL != "https://jira.example.com/browse/DEMO-7" {
				t.Fatalf("L3 link = %+v, want the card's browse URL", link)
			}
			h := firedHits(b, "L3")[0]
			if h.Text != tc.text || h.Evidence["train"] != (TrainID{Number: 21, Year: fixtureYear}) || h.Evidence["group"] != PlanID("demo-wallet") || h.AgesOn == nil {
				t.Fatalf("L3 hit = %+v; want text %q, evidence naming train #21 and group demo-wallet, ageing with the version", h, tc.text)
			}
		})
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
