package releasetrain

import (
	"reflect"
	"strconv"
	"testing"
)

func developWallet(t *testing.T, todayStr string) *fixture {
	f := newFixture(t, todayStr)
	for _, app := range []AppID{"di", "da"} {
		f.version(app, "22", "3.6.0", "2026-10-20", "2026-10-27")
		f.version(app, "23", "3.7.0", "2026-11-03", "2026-11-10")
		f.mr(app, "DEMO-50 other", "feat/DEMO-50", "feature/sprint-20-2026", MRMerged, "2026-10-01", "2026-10-01")
		f.mr(app, "DEMO-1 wallet", "feat/DEMO-1", "feature/demo-wallet", MRMerged, "2026-09-01", "2026-09-02")
	}
	tr := train22
	f.human.Plans = []LlbPlan{{ID: "demo-wallet", Name: "Wallet", TargetKind: TargetTrain, TargetTrain: &tr,
		Branches: []PlanBranch{{App: "di", Branch: "feature/demo-wallet"}, {App: "da", Branch: "feature/demo-wallet"}}}}
	return f
}

func llbRules(b Board) map[RuleID][]string {
	out := map[RuleID][]string{}
	for _, r := range []RuleID{"L1", "L8", "L10", "L11", "L12", "L13"} {
		if ids := firedIDs(b, r); ids != nil {
			out[r] = ids
		}
	}
	return out
}

func TestALongLivedBranchMergedIntoDevelopOrItsSprintBranchIsOnFlow(t *testing.T) {
	for _, tc := range []struct {
		name   string
		target string
	}{
		{"develop", "develop"},
		{"the sprint branch", "feature/sprint-21-2026"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := developWallet(t, today)
			f.sprintCard(demoSprint(f), "DEMO-1", "In Progress", true)
			for _, app := range []AppID{"di", "da"} {
				f.mr(app, "DEMO-1 wallet landing", "feature/demo-wallet", tc.target, MRMerged, "2026-10-11", "2026-10-12")
			}
			b := f.reconcile()
			for _, app := range []AppID{"di", "da"} {
				g, br := llbBranch(t, b, app, "feature/demo-wallet")
				if br.State != LLBLanded || !reflect.DeepEqual(g.Target, LLBLandedIn{Train: train22, App: "di", Branch: "feature/demo-wallet"}) {
					t.Fatalf("%s: state %s target %#v, want landed for train 22", app, br.State, g.Target)
				}
			}
			if got := llbRules(b); len(got) != 0 {
				t.Fatalf("hits = %v, want none: the landing is on flow", got)
			}
			if tc.target != "develop" {
				return
			}
			routes := b.sprintCard("DEMO-1").Routes
			if len(routes) != 2 {
				t.Fatalf("routes = %+v, want one per app", routes)
			}
			for _, r := range routes {
				if r.Kind != DestLLB || r.To != (ToTrain{Train: train22}) || r.ViaLLB != "feature/demo-wallet" {
					t.Fatalf("%s route = %+v, want the card to ride train 22 through feature/demo-wallet", r.App, r)
				}
			}
		})
	}
}

func TestAnOpenLongLivedBranchMRIntoDevelopIsALandingNotAWrongOne(t *testing.T) {
	f := developWallet(t, today)
	f.sprintCard(demoSprint(f), "DEMO-9", "In Progress", true)
	f.mr("di", "DEMO-9 wallet landing", "feature/demo-wallet", "develop", MROpened, "2026-10-13", "")
	b := f.reconcile()
	if _, br := llbBranch(t, b, "di", "feature/demo-wallet"); br.State != LLBLanding {
		t.Fatalf("state = %s, want landing", br.State)
	}
	if got := firedIDs(b, "L1"); got != nil {
		t.Fatalf("L1 = %v, want none for an MR into develop", got)
	}
	routes := b.sprintCard("DEMO-9").Routes
	if len(routes) != 1 {
		t.Fatalf("routes = %+v, want one", routes)
	}
	for _, r := range routes {
		if r.Kind != DestLLBOpen {
			t.Fatalf("route = %+v, want the open landing routed through its long-lived branch", r)
		}
		if to, ok := r.To.(ToLLB); !ok || to.Group != "demo-wallet" || to.Branch != "feature/demo-wallet" || to.Via == nil || *to.Via != train22 {
			t.Fatalf("route to = %+v, want ToLLB through demo-wallet for train 22", r.To)
		}
	}
	if got := firedIDs(b, "L8"); got != nil {
		t.Fatalf("L8 = %v, want none for a long-lived branch landing in develop", got)
	}
}

func TestOpenMRIntoDevelopNearFreezeFiresL12AndLeavesL10AndL1Quiet(t *testing.T) {
	f := developWallet(t, "2026-10-16")
	mr := f.mr("di", "wallet", "feature/demo-wallet", "develop", MROpened, "2026-10-14", "")
	b := f.reconcile()
	if got, want := firedIDs(b, "L12"), []string{"L12~mr/di/" + strconv.Itoa(mr.IID)}; !reflect.DeepEqual(got, want) {
		t.Fatalf("L12 = %v, want %v", got, want)
	}
	h := firedHits(b, "L12")[0]
	want := Copy{
		What: "MR !" + strconv.Itoa(mr.IID) + " from feature/demo-wallet into develop is still open",
		Do:   "Merge it before freeze Tue 20 Oct.",
	}
	if h.Copy != want || h.Severity != SevRisk {
		t.Fatalf("L12 hit = %+v, want risk with copy %+v", h, want)
	}
	if got, want := firedIDs(b, "L10"), []string{"L10~llb/22-2026/da/feature/demo-wallet"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("L10 = %v, want only da, which has no MR into develop or the sprint branch", got)
	}
	if got := firedIDs(b, "L1"); got != nil {
		t.Fatalf("L1 = %v, want none", got)
	}
}

func TestL10CountsAnMRIntoDevelopAsBeingInTheTrain(t *testing.T) {
	f := developWallet(t, "2026-10-12")
	f.mr("di", "wallet", "feature/demo-wallet", "develop", MROpened, "2026-10-12", "")
	b := f.reconcile()
	if got, want := firedIDs(b, "L10"), []string{"L10~llb/22-2026/da/feature/demo-wallet"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("L10 = %v, want only da %v", got, want)
	}
	if h := firedHits(b, "L10")[0]; h.Copy.What != "feature/demo-wallet is not in develop or sprint 21 yet" || h.Copy.Do != "Open an MR into develop or the sprint branch before freeze Tue 20 Oct." {
		t.Fatalf("L10 copy = %+v", h.Copy)
	}
}

func TestLandingIntoDevelopAfterFreezeMissesTheTrainEvenWhenEveryBranchMerged(t *testing.T) {
	f := developWallet(t, "2026-10-22")
	for _, app := range []AppID{"di", "da"} {
		f.mr(app, "wallet", "feature/demo-wallet", "develop", MRMerged, "2026-10-20", "2026-10-21")
	}
	b := f.reconcile()
	want := []string{"L13~llb/22-2026/da/feature/demo-wallet", "L13~llb/22-2026/di/feature/demo-wallet"}
	if got := firedIDs(b, "L13"); !reflect.DeepEqual(got, want) {
		t.Fatalf("L13 = %v, want %v", got, want)
	}
	for _, h := range firedHits(b, "L13") {
		if h.Due == nil || !h.Due.Missed {
			t.Fatalf("L13 due = %+v, want missed", h.Due)
		}
	}
	for _, r := range []RuleID{"L10", "L11", "L12", "L1"} {
		if got := firedIDs(b, r); got != nil {
			t.Fatalf("%s = %v, want none", r, got)
		}
	}
	if g, br := llbBranch(t, b, "di", "feature/demo-wallet"); br.State != LLBLanded || !reflect.DeepEqual(g.Target, LLBLandedIn{Train: TrainID{Number: 23, Year: fixtureYear}, App: "di", Branch: "feature/demo-wallet"}) {
		t.Fatalf("state %s target %#v, want landed, riding train 23", br.State, g.Target)
	}
}

func TestLandingIntoDevelopBeforeFreezeSatisfiesThePlan(t *testing.T) {
	f := developWallet(t, "2026-10-22")
	for _, app := range []AppID{"di", "da"} {
		f.mr(app, "wallet", "feature/demo-wallet", "develop", MRMerged, "2026-10-18", "2026-10-19")
	}
	if got := llbRules(f.reconcile()); len(got) != 0 {
		t.Fatalf("hits = %v, want none: merged into develop before the freeze", got)
	}
}

func TestAPlainCardMRIntoDevelopAfterCutoverStaysOffFlow(t *testing.T) {
	f := developWallet(t, today)
	f.sprintCard(demoSprint(f), "DEMO-2", "In Progress", true)
	iid := f.mr("di", "DEMO-2 part", "feat/DEMO-2", "develop", MROpened, "2026-10-12", "").IID
	f.mr("di", "wallet", "feature/demo-wallet", "develop", MROpened, "2026-10-12", "")
	b := f.reconcile()
	routes := b.sprintCard("DEMO-2").Routes
	if len(routes) != 1 || routes[0].Kind != DestDevelopOpen || routes[0].To != (OffFlow{}) {
		t.Fatalf("routes = %+v, want one off-flow route into develop", routes)
	}
	if got, want := firedIDs(b, "L8"), []string{"L8~mr/di/" + strconv.Itoa(iid)}; !reflect.DeepEqual(got, want) {
		t.Fatalf("L8 = %v, want %v", got, want)
	}
}

func TestAnOpenLongLivedBranchMRIntoMainAfterCutoverIsAWrongLanding(t *testing.T) {
	f := developWallet(t, today)
	mr := f.mr("di", "wallet", "feature/demo-wallet", "main", MROpened, "2026-10-12", "")
	b := f.reconcile()
	if got, want := firedIDs(b, "L1"), []string{"L1~mr/di/" + strconv.Itoa(mr.IID)}; !reflect.DeepEqual(got, want) {
		t.Fatalf("L1 = %v, want %v", got, want)
	}
	if _, br := llbBranch(t, b, "di", "feature/demo-wallet"); br.State != LLBLandingWrong {
		t.Fatalf("state = %s, want landing_wrong", br.State)
	}
	if h := firedHits(b, "L1")[0]; h.Copy.What != "MR !"+strconv.Itoa(mr.IID)+" takes feature/demo-wallet into main" ||
		h.Copy.Do != "Retarget it to develop or the sprint branch. Main takes no feature work after the cutover. The sprint branch puts it in sprint 21." {
		t.Fatalf("L1 copy = %+v", h.Copy)
	}
}

func TestAMergedLongLivedBranchLandingIntoMainAfterCutoverIsWrong(t *testing.T) {
	f := developWallet(t, today)
	f.mr("di", "wallet", "feature/demo-wallet", "main", MRMerged, "2026-10-11", "2026-10-12")
	if _, br := llbBranch(t, f.reconcile(), "di", "feature/demo-wallet"); br.State != LLBLandedWrong {
		t.Fatalf("state = %s, want landed_wrong", br.State)
	}
}
