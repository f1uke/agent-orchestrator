package releasetrain

import (
	"reflect"
	"testing"
)

func routeTrain(n int) TrainID { return TrainID{Number: n, Year: 2026} }

func trainOn(t *testing.T, n int, freeze, publish string, apps ...AppID) Train {
	t.Helper()
	tr := Train{ID: routeTrain(n), Freeze: dayPtr(t, freeze), Publish: dayPtr(t, publish)}
	for _, a := range apps {
		tr.Versions = append(tr.Versions, &AppVersion{Train: tr.ID, App: a, Version: "3.5." + string(rune('0'+n%10))})
	}
	return tr
}

func smr(t *testing.T, iid int, target, state, created, merged string) SprintMR {
	t.Helper()
	m := SprintMR{App: "di", IID: iid, Source: "feat/DEMO-1", Target: target, State: state, Created: day(t, created)}
	if merged != "" {
		m.Merged = dayPtr(t, merged)
	}
	return m
}

func goldenRouting(t *testing.T) routing {
	t.Helper()
	landed := &LongLivedBranch{App: "di", Branch: "feature/demo-wallet",
		Landing: &MRFact{TargetBranch: "feature/sprint-2026-6", State: MRMerged, MergedOn: dayPtr(t, "2026-09-30")}}
	parked := &LongLivedBranch{App: "di", Branch: "feature/demo-chat"}
	return routing{
		today: day(t, "2026-10-14"), sprint: 7, year: 2026, start: day(t, "2026-10-06"),
		cutover: map[AppID]Day{"di": day(t, "2026-09-01")},
		llbs:    map[AppID]map[string]*LongLivedBranch{"di": {landed.Branch: landed, parked.Branch: parked}},
		groups: map[AppID]map[string]*LLBGroup{"di": {
			parked.Branch: {ID: "demo-chat", Target: LLBPlanned{Train: routeTrain(9)}},
			landed.Branch: {ID: "demo-wallet", Target: LLBLandedIn{Train: routeTrain(7)}},
		}},
		trains: []Train{
			trainOn(t, 6, "2026-09-28", "2026-10-05", "di"),
			trainOn(t, 7, "2026-10-12", "2026-10-19", "di"),
			trainOn(t, 8, "2026-10-26", "2026-11-02", "di"),
		},
		plans: map[string]TrainID{},
	}
}

func TestRouteOfGoldenCases(t *testing.T) {
	for _, tc := range []struct {
		name      string
		plan      int
		noCutover bool
		mrs       func(t *testing.T) []SprintMR
		want      Route
	}{
		{"merged into this sprint's branch rides the next train", 0, false, func(t *testing.T) []SprintMR {
			return []SprintMR{smr(t, 1, "feature/sprint-7-2026", MRMerged, "2026-10-07", "2026-10-08")}
		}, Route{Kind: DestSprint, To: ToTrain{Train: routeTrain(8)}, MR: 1}},
		{"in review into the sprint branch, year-first name", 0, false, func(t *testing.T) []SprintMR {
			return []SprintMR{smr(t, 2, "feature/sprint-2026-7", MROpened, "2026-10-09", "")}
		}, Route{Kind: DestSprintOpen, To: ToTrain{Train: routeTrain(8)}, MR: 2}},
		{"a merged MR beats a better-ranked open one", 0, false, func(t *testing.T) []SprintMR {
			return []SprintMR{smr(t, 3, "feature/sprint-7-2026", MROpened, "2026-10-09", ""), smr(t, 4, "develop", MRMerged, "2026-10-07", "2026-10-08")}
		}, Route{Kind: DestDevelop, To: OffFlow{}, MR: 4}},
		{"via a long-lived branch that landed", 0, false, func(t *testing.T) []SprintMR {
			return []SprintMR{smr(t, 5, "feature/demo-wallet", MRMerged, "2026-09-20", "2026-09-21")}
		}, Route{Kind: DestBefore, To: ToTrain{Train: routeTrain(7)}, ViaLLB: "feature/demo-wallet", MR: 5}},
		{"via a landed long-lived branch this sprint", 0, false, func(t *testing.T) []SprintMR {
			return []SprintMR{smr(t, 15, "feature/demo-wallet", MRMerged, "2026-10-07", "2026-10-07")}
		}, Route{Kind: DestLLB, To: ToTrain{Train: routeTrain(7)}, ViaLLB: "feature/demo-wallet", MR: 15}},
		{"parked in a long-lived branch names the group and its plan", 0, false, func(t *testing.T) []SprintMR {
			return []SprintMR{smr(t, 6, "feature/demo-chat", MRMerged, "2026-10-07", "2026-10-08")}
		}, Route{Kind: DestLLB, To: ToLLB{Group: "demo-chat", Branch: "feature/demo-chat", Via: ptrTrain(9)}, MR: 6}},
		{"merged into develop after cutover goes off flow", 0, false, func(t *testing.T) []SprintMR {
			return []SprintMR{smr(t, 7, "develop", MRMerged, "2026-10-07", "2026-10-08")}
		}, Route{Kind: DestDevelop, To: OffFlow{}, MR: 7}},
		{"open into develop after cutover goes off flow", 0, false, func(t *testing.T) []SprintMR {
			return []SprintMR{smr(t, 8, "develop", MROpened, "2026-10-07", "")}
		}, Route{Kind: DestDevelopOpen, To: OffFlow{}, MR: 8}},
		{"merged before this sprint rides the next train frozen after", 0, false, func(t *testing.T) []SprintMR {
			return []SprintMR{smr(t, 9, "develop", MRMerged, "2026-09-20", "2026-09-22")}
		}, Route{Kind: DestBefore, To: ToTrain{Train: routeTrain(6)}, MR: 9}},
		{"merged long before the first train", 0, false, func(t *testing.T) []SprintMR {
			return []SprintMR{smr(t, 10, "develop", MRMerged, "2026-08-01", "2026-08-02")}
		}, Route{Kind: DestBefore, To: BeforeFirstTrain{Train: routeTrain(6)}, MR: 10}},
		{"the previous sprint's branch rides this sprint's train", 0, false, func(t *testing.T) []SprintMR {
			return []SprintMR{smr(t, 11, "feature/sprint-6-2026", MROpened, "2026-10-07", "")}
		}, Route{Kind: DestPrev, To: ToTrain{Train: routeTrain(7)}, MR: 11}},
		{"page says another train than the sprint branch", 7, false, func(t *testing.T) []SprintMR {
			return []SprintMR{smr(t, 12, "feature/sprint-7-2026", MRMerged, "2026-10-07", "2026-10-08")}
		}, Route{Kind: DestSprint, To: ToTrain{Train: routeTrain(8)}, Plan: ptrTrain(7), Mismatch: true, MR: 12}},
		{"page agrees with the sprint branch", 8, false, func(t *testing.T) []SprintMR {
			return []SprintMR{smr(t, 13, "feature/sprint-7-2026", MRMerged, "2026-10-07", "2026-10-08")}
		}, Route{Kind: DestSprint, To: ToTrain{Train: routeTrain(8)}, Plan: ptrTrain(8), MR: 13}},
		{"page names another train than the long-lived plan", 8, false, func(t *testing.T) []SprintMR {
			return []SprintMR{smr(t, 14, "feature/demo-chat", MRMerged, "2026-10-07", "2026-10-08")}
		}, Route{Kind: DestLLB, To: ToLLB{Group: "demo-chat", Branch: "feature/demo-chat", Via: ptrTrain(9)}, Plan: ptrTrain(8), Mismatch: true, MR: 14}},
		{"merged before this sprint, its page names the train", 6, false, func(t *testing.T) []SprintMR {
			return []SprintMR{smr(t, 16, "develop", MRMerged, "2026-09-02", "2026-09-03")}
		}, Route{Kind: DestBefore, To: ToTrain{Train: routeTrain(6)}, Plan: ptrTrain(6), MR: 16}},
		{"with no cutover, develop after the sprint start goes off flow", 0, true, func(t *testing.T) []SprintMR {
			return []SprintMR{smr(t, 17, "develop", MRMerged, "2026-10-07", "2026-10-08")}
		}, Route{Kind: DestDevelop, To: OffFlow{}, MR: 17}},
		{"a sprint branch merged back rides this sprint's train", 0, false, func(t *testing.T) []SprintMR {
			m := smr(t, 18, "develop", MROpened, "2026-10-07", "")
			m.Source = "feature/sprint-2026-6"
			return []SprintMR{m}
		}, Route{Kind: DestBack, To: ToTrain{Train: routeTrain(7)}, MR: 18}},
		{"no MR is not routed", 0, false, func(*testing.T) []SprintMR { return nil }, Route{Kind: DestNone, To: NotRouted{}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := goldenRouting(t)
			if tc.noCutover {
				r.cutover = map[AppID]Day{}
			}
			if tc.plan != 0 {
				r.plans[planKey("DEMO-1", "di")] = routeTrain(tc.plan)
			}
			got := r.RouteOf("DEMO-1", "di", tc.mrs(t))
			tc.want.App = "di"
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("RouteOf = %+v\nwant      %+v", got, tc.want)
			}
		})
	}
}

func ptrTrain(n int) *TrainID { id := routeTrain(n); return &id }

func TestSprintNumberReadsBothNameForms(t *testing.T) {
	for name, want := range map[string][2]int{"DEMO 2026-7": {7, 2026}, "DEMO 7-2026": {7, 2026}, "Sprint 7": {7, 2025}} {
		n, y, ok := sprintNumber(SprintFact{Name: name, Start: &Day{2025, 12, 29}})
		if !ok || n != want[0] || y != want[1] {
			t.Errorf("sprintNumber(%q) = %d, %d, %v; want %v", name, n, y, ok, want)
		}
	}
	for branch, want := range map[string]int{"feature/sprint-7-2026": 7, "feature/sprint-2026-7": 7} {
		if n, ok := sprintOf(branch); !ok || n != want {
			t.Errorf("sprintOf(%q) = %d, %v; want %d", branch, n, ok, want)
		}
	}
}
