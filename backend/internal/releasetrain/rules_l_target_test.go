package releasetrain

import (
	"reflect"
	"testing"
)

const (
	targetToday  = "2026-10-26"
	targetFreeze = "2026-10-30"
	targetDue    = "2026-11-06"
)

var train22 = TrainID{Number: 22, Year: fixtureYear}

func walletPlan(t *testing.T, today, freeze string, target TargetKind) (*fixture, map[AppID]*fv) {
	f := newFixture(t, today)
	return f, fillWallet(f, freeze, target)
}

func fillWallet(f *fixture, freeze string, target TargetKind) map[AppID]*fv {
	versions := map[AppID]*fv{}
	for _, app := range []AppID{"di", "da"} {
		versions[app] = f.version(app, "22", "3.6.0", freeze, targetDue)
		f.mr(app, "DEMO-1 wallet", "feat/DEMO-1", "feature/demo-wallet", MRMerged, "2026-09-01", "2026-09-02")
		f.mr(app, "DEMO-2 wallet", "feat/DEMO-2", "feature/demo-wallet", MRMerged, "2026-09-03", "2026-09-04")
	}
	p := LlbPlan{ID: "demo-wallet", Name: "Wallet", TargetKind: target,
		Branches: []PlanBranch{{App: "di", Branch: "feature/demo-wallet"}, {App: "da", Branch: "feature/demo-wallet"}}}
	if target == TargetTrain {
		tr := train22
		p.TargetTrain = &tr
	}
	f.human.Plans = []LlbPlan{p}
	return versions
}

func TestL10PlanTargetsATrainAndNoAppHasAnMRIntoItsSprintBranch(t *testing.T) {
	f, _ := walletPlan(t, targetToday, targetFreeze, TargetTrain)
	b := f.reconcile()
	want := []string{"L10~llb/22-2026/da/feature/demo-wallet", "L10~llb/22-2026/di/feature/demo-wallet"}
	if got := firedIDs(b, "L10"); !reflect.DeepEqual(got, want) {
		t.Fatalf("L10 hits = %v, want one per app %v", got, want)
	}
	h := firedHits(b, "L10")[0]
	copy := Copy{What: "feature/demo-wallet is not in sprint 21 yet", Do: "Open an MR into the sprint branch before freeze Fri 30 Oct."}
	if h.Severity != SevInfo || h.Train == nil || *h.Train != train22 || h.Evidence["group"] != PlanID("demo-wallet") || h.Copy != copy {
		t.Fatalf("L10 hit = %+v; want info on train 22, group demo-wallet, copy %+v", h, copy)
	}
	if h.Due == nil || h.Due.Event != DueFreeze || h.Due.WorkDays != 4 || h.Due.Missed {
		t.Fatalf("L10 due = %+v; want freeze in 4 working days", h.Due)
	}
}

func TestL10TurnsRiskThreeWorkingDaysBeforeFreeze(t *testing.T) {
	for _, tc := range []struct {
		today string
		want  Severity
	}{
		{"2026-10-26", SevInfo},
		{"2026-10-27", SevRisk},
		{"2026-10-24", SevInfo},
		{"2026-10-30", SevRisk},
	} {
		f, _ := walletPlan(t, tc.today, targetFreeze, TargetTrain)
		hits := firedHits(f.reconcile(), "L10")
		if len(hits) != 2 || hits[0].Severity != tc.want || hits[1].Severity != tc.want {
			t.Fatalf("today %s: L10 hits = %+v, want two at %v", tc.today, hits, tc.want)
		}
	}
}

func TestLLBTargetRulesStayQuiet(t *testing.T) {
	for _, tc := range []struct {
		name   string
		target TargetKind
		setup  func(f *fixture)
	}{
		{"plan is unplanned", TargetUnplanned, func(*fixture) {}},
		{"plan is dropped", TargetDropped, func(*fixture) {}},
		{"both branches landed in the sprint branch", TargetTrain, func(f *fixture) {
			for _, app := range []AppID{"di", "da"} {
				f.mr(app, "wallet", "feature/demo-wallet", "feature/sprint-21-2026", MRMerged, "2026-10-20", "2026-10-21")
			}
		}},
		{"both branches landed in develop before any cutover", TargetTrain, func(f *fixture) {
			for _, app := range []AppID{"di", "da"} {
				f.mr(app, "wallet", "feature/demo-wallet", "develop", MRMerged, "2026-10-20", "2026-10-21")
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, _ := walletPlan(t, targetToday, targetFreeze, tc.target)
			tc.setup(f)
			b := f.reconcile()
			for _, r := range []RuleID{"L10", "L11", "L12", "L13"} {
				if got := firedIDs(b, r); got != nil {
					t.Fatalf("%s hits = %v, want none", r, got)
				}
			}
		})
	}
}

func TestL10OnlyTheAppStillWithoutAnMR(t *testing.T) {
	f, _ := walletPlan(t, targetToday, targetFreeze, TargetTrain)
	f.mr("di", "wallet", "feature/demo-wallet", "feature/sprint-21-2026", MRMerged, "2026-10-20", "2026-10-21")
	if got, want := firedIDs(f.reconcile(), "L10"), []string{"L10~llb/22-2026/da/feature/demo-wallet"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("L10 hits = %v, want only the app that has not landed %v", got, want)
	}
}

func TestL10AnMRIntoAnotherSprintBranchDoesNotCount(t *testing.T) {
	f, _ := walletPlan(t, targetToday, targetFreeze, TargetTrain)
	f.mr("di", "wallet", "feature/demo-wallet", "feature/sprint-22-2026", MROpened, "2026-10-20", "")
	if got := firedIDs(f.reconcile(), "L10"); len(got) != 2 {
		t.Fatalf("L10 hits = %v, want both apps: #23's sprint branch is not #22's", got)
	}
}

func TestL11CardOfATargetingGroupMissingFromTheTrain(t *testing.T) {
	for _, tc := range []struct {
		name  string
		today string
		setup func(f *fixture, vs map[AppID]*fv)
		want  []string
		sev   Severity
	}{
		{"on neither page nor fixVersion", targetToday, func(*fixture, map[AppID]*fv) {}, []string{
			"L11~ct/22-2026/da/DEMO-1", "L11~ct/22-2026/da/DEMO-2", "L11~ct/22-2026/di/DEMO-1", "L11~ct/22-2026/di/DEMO-2",
		}, SevInfo},
		{"near freeze turns risk", "2026-10-28", func(*fixture, map[AppID]*fv) {}, []string{
			"L11~ct/22-2026/da/DEMO-1", "L11~ct/22-2026/da/DEMO-2", "L11~ct/22-2026/di/DEMO-1", "L11~ct/22-2026/di/DEMO-2",
		}, SevRisk},
		{"on the page or in the fixVersion is quiet", targetToday, func(f *fixture, vs map[AppID]*fv) {
			p := f.page("demo", "22", "2026-10-01", targetFreeze, targetDue)
			f.card(p, "DEMO-1")
			vs["di"].issue("DEMO-2", "In Progress", "indeterminate")
		}, []string{"L11~ct/22-2026/da/DEMO-2"}, SevInfo},
		{"after freeze L13 owns it", "2026-11-02", func(*fixture, map[AppID]*fv) {}, nil, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, vs := walletPlan(t, tc.today, targetFreeze, TargetTrain)
			tc.setup(f, vs)
			b := f.reconcile()
			if got := firedIDs(b, "L11"); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("L11 hits = %v, want %v", got, tc.want)
			}
			for _, h := range firedHits(b, "L11") {
				if h.Severity != tc.sev || h.Evidence["group"] != PlanID("demo-wallet") {
					t.Fatalf("L11 hit = %+v, want severity %v and group demo-wallet", h, tc.sev)
				}
			}
		})
	}
}

func TestL12OpenMRIntoTheSprintBranchNearFreeze(t *testing.T) {
	for _, tc := range []struct {
		today string
		want  int
	}{
		{"2026-10-26", 0},
		{"2026-10-27", 1},
		{"2026-10-30", 1},
	} {
		f, _ := walletPlan(t, tc.today, targetFreeze, TargetTrain)
		mr := f.mr("di", "wallet", "feature/demo-wallet", "feature/sprint-21-2026", MROpened, "2026-10-22", "")
		b := f.reconcile()
		hits := firedHits(b, "L12")
		if len(hits) != tc.want {
			t.Fatalf("today %s: L12 hits = %+v, want %d", tc.today, hits, tc.want)
		}
		if got := firedIDs(b, "L10"); !reflect.DeepEqual(got, []string{"L10~llb/22-2026/da/feature/demo-wallet"}) {
			t.Fatalf("today %s: L10 hits = %v, want only da: di has an MR", tc.today, got)
		}
		if tc.want == 0 {
			continue
		}
		if h := hits[0]; h.Severity != SevRisk || h.Subject != (SubjMR{App: "di", IID: mr.IID, Source: mr.SourceBranch, Target: mr.TargetBranch}) || h.Evidence["group"] != PlanID("demo-wallet") {
			t.Fatalf("L12 hit = %+v, want risk on the open MR, group demo-wallet", h)
		}
	}
}

func TestL13MissedTheTrain(t *testing.T) {
	for _, tc := range []struct {
		name  string
		today string
		setup func(f *fixture, vs map[AppID]*fv)
		want  []string
	}{
		{"freeze day is not missed yet", targetFreeze, func(*fixture, map[AppID]*fv) {}, nil},
		{"freeze passed, no MR", "2026-10-31", func(*fixture, map[AppID]*fv) {}, []string{
			"L13~llb/22-2026/da/feature/demo-wallet", "L13~llb/22-2026/di/feature/demo-wallet",
		}},
		{"freeze passed, MR still open", "2026-11-02", func(f *fixture, _ map[AppID]*fv) {
			f.mr("di", "wallet", "feature/demo-wallet", "feature/sprint-21-2026", MROpened, "2026-10-22", "")
		}, []string{"L13~llb/22-2026/da/feature/demo-wallet", "L13~llb/22-2026/di/feature/demo-wallet"}},
		{"release cut before freeze in one app", targetToday, func(_ *fixture, vs map[AppID]*fv) {
			vs["di"].releaseMR(targetToday, MROpened)
		}, []string{"L13~llb/22-2026/di/feature/demo-wallet"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, vs := walletPlan(t, tc.today, targetFreeze, TargetTrain)
			tc.setup(f, vs)
			b := f.reconcile()
			if got := firedIDs(b, "L13"); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("L13 hits = %v, want %v", got, tc.want)
			}
			for _, h := range firedHits(b, "L13") {
				if h.Severity != SevBlock {
					t.Fatalf("L13 hit = %+v, want block", h)
				}
				if got := firedIDs(b, "L10"); contains(got, "L10~llb/22-2026/"+string(h.App)+"/feature/demo-wallet") {
					t.Fatalf("L10 and L13 both fire for %s", h.App)
				}
				if got := firedIDs(b, "L12"); len(got) > 0 {
					t.Fatalf("L12 = %v after the train was missed, want L13 alone", got)
				}
			}
		})
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
