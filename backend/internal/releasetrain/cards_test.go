package releasetrain

import (
	"reflect"
	"testing"
)

func (v *fv) cardMR(title, source, target, state, created, merged string) int {
	iid := v.f.id()
	mr := &MRFact{
		Project: v.app.GitLabProject, IID: iid, Title: title, SourceBranch: source, TargetBranch: target,
		State: state, Author: "Dee", CreatedOn: day(v.f.t, created),
	}
	if merged != "" {
		mr.MergedOn = dayPtr(v.f.t, merged)
	}
	v.f.mrs = append(v.f.mrs, mr)
	return iid
}

func TestCardCodeState(t *testing.T) {
	const cut = "2026-10-09"
	for _, tc := range []struct {
		name  string
		train string
		noCut bool
		mrs   func(v *fv) []int
		want  func(ids []int) CodeState
	}{
		{"no MR", "21", false, func(*fv) []int { return nil },
			func([]int) CodeState { return CodeNone{} }},
		{"merged into develop before the cut", "21", false, func(v *fv) []int {
			return []int{v.cardMR("DEMO-1 login", "feat/login", "develop", MRMerged, "2026-10-01", "2026-10-02")}
		}, func(ids []int) CodeState { return CodeMerged{MRs: ids} }},
		{"merged into the source sprint branch", "21", false, func(v *fv) []int {
			return []int{v.cardMR("DEMO-1 login", "feat/login", "sprint-20-2026", MRMerged, "2026-10-01", "2026-10-08")}
		}, func(ids []int) CodeState { return CodeMerged{MRs: ids} }},
		{"merged into develop after the cut", "21", false, func(v *fv) []int {
			v.cardMR("DEMO-1 login", "feat/login", "develop", MRMerged, "2026-10-08", "2026-10-11")
			return nil
		}, func([]int) CodeState { return CodeElsewhere{Branch: "develop", On: Day{2026, 10, 11}} }},
		{"open MR into develop", "21", false, func(v *fv) []int {
			return []int{v.cardMR("DEMO-1 login", "feat/login", "develop", MROpened, "2026-10-08", "")}
		}, func(ids []int) CodeState { return CodeOpen{MRs: ids} }},
		{"merged into a feature branch only", "21", false, func(v *fv) []int {
			v.cardMR("DEMO-1 login", "feat/login", "feature/wallet", MRMerged, "2026-10-01", "2026-10-03")
			return nil
		}, func([]int) CodeState { return CodeElsewhere{Branch: "feature/wallet", On: Day{2026, 10, 3}} }},
		{"MR into an older sprint branch is ignored", "21", false, func(v *fv) []int {
			v.cardMR("DEMO-1 login", "feat/login", "sprint-19-2026", MRMerged, "2026-09-20", "2026-09-22")
			return nil
		}, func([]int) CodeState { return CodeNone{} }},
		{"extra train takes no sprint branch", "20-1", false, func(v *fv) []int {
			v.cardMR("DEMO-1 login", "feat/login", "sprint-20-2026", MRMerged, "2026-10-01", "2026-10-02")
			return nil
		}, func([]int) CodeState { return CodeNone{} }},
		{"title key wins over branch key", "21", false, func(v *fv) []int {
			v.cardMR("DEMO-2 other", "feat/DEMO-1-login", "develop", MRMerged, "2026-10-01", "2026-10-02")
			return nil
		}, func([]int) CodeState { return CodeNone{} }},
		{"branch key when the title has none", "21", false, func(v *fv) []int {
			return []int{v.cardMR("login screen", "feat/DEMO-1-login", "develop", MRMerged, "2026-10-01", "2026-10-02")}
		}, func(ids []int) CodeState { return CodeMerged{MRs: ids} }},
		{"no release MR means no cut", "21", true, func(v *fv) []int {
			return []int{v.cardMR("DEMO-1 login", "feat/login", "develop", MRMerged, "2026-10-12", "2026-10-13")}
		}, func(ids []int) CodeState { return CodeMerged{MRs: ids} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, today)
			v := f.version("di", tc.train, "3.5.0", liveFreeze, liveDue)
			if !tc.noCut {
				v.releaseMR(cut, MROpened)
			}
			v.issue("DEMO-1", "In Progress", "indeterminate")
			ids := tc.mrs(v)

			b := f.reconcile()

			cards := b.Trains[0].version("di").Cards
			if len(cards) != 1 {
				t.Fatalf("cards = %+v, want one", cards)
			}
			if want := tc.want(ids); !reflect.DeepEqual(cards[0].Code, want) {
				t.Fatalf("code = %#v, want %#v", cards[0].Code, want)
			}
		})
	}
}

func TestJiraVersionJoinsTheLongestPrefix(t *testing.T) {
	cfg := fixtureConfig()
	for name, want := range map[string]AppID{"iOS Pilot 1.0.0": "pi", "iOS 1.0.0": "di", "Android Pilot 2.0.0": "pa"} {
		if got, ok := appForJiraName(cfg, name); !ok || got.ID != want {
			t.Fatalf("appForJiraName(%q) = %s, %v; want %s", name, got.ID, ok, want)
		}
	}
	if _, ok := appForJiraName(cfg, "Web 1.0.0"); ok {
		t.Fatalf("appForJiraName matched a name with no configured prefix")
	}
}
