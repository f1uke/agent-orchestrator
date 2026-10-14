package releasetrain

import (
	"reflect"
	"sort"
	"strconv"
	"testing"
)

func (f *fixture) mr(app AppID, title, source, target, state, created, merged string) *MRFact {
	iid := f.id()
	mr := &MRFact{
		Project: f.app(app).GitLabProject, IID: iid, Title: title, SourceBranch: source, TargetBranch: target,
		State: state, Author: "Dee", AuthorUsername: "dee", CreatedOn: day(f.t, created),
		URL: "https://gitlab.example.com/mr/" + strconv.Itoa(iid),
	}
	if merged != "" {
		mr.MergedOn = dayPtr(f.t, merged)
	}
	f.mrs = append(f.mrs, mr)
	return mr
}

func (f *fixture) ahead(app AppID, branch string, commits int) {
	c := &CompareFact{Project: f.app(app).GitLabProject, From: "develop", To: branch, Commits: []CommitFact{}}
	for i := 0; i < commits; i++ {
		c.Commits = append(c.Commits, CommitFact{Title: "work"})
	}
	f.compares = append(f.compares, c)
}

func llbBranches(b Board) []string {
	var out []string
	for _, g := range b.LLBs {
		for _, br := range g.Branches {
			out = append(out, string(br.App)+":"+br.Branch)
		}
	}
	sort.Strings(out)
	return out
}

func llbBranch(t *testing.T, b Board, app AppID, branch string) (LLBGroup, LongLivedBranch) {
	t.Helper()
	for _, g := range b.LLBs {
		for _, br := range g.Branches {
			if br.App == app && br.Branch == branch {
				return g, br
			}
		}
	}
	t.Fatalf("no long-lived branch %s:%s in %v", app, branch, llbBranches(b))
	return LLBGroup{}, LongLivedBranch{}
}

func TestLLBDetection(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(f *fixture)
		want  []string
	}{
		{"a branch with no card key counts from one MR in", func(f *fixture) {
			f.mr("di", "DEMO-1 wallet", "feat/DEMO-1", "feature/demo-wallet", MRMerged, "2026-09-01", "2026-09-02")
		}, []string{"di:feature/demo-wallet"}},
		{"a branch named after a card needs two MRs in", func(f *fixture) {
			f.mr("di", "DEMO-1 part", "feat/a", "feature/DEMO-7-chat", MRMerged, "2026-09-01", "2026-09-02")
		}, nil},
		{"a branch named after a card counts with two MRs in", func(f *fixture) {
			f.mr("di", "DEMO-1 part", "feat/a", "feature/DEMO-7-chat", MRMerged, "2026-09-01", "2026-09-02")
			f.mr("di", "DEMO-2 part", "feat/b", "feature/DEMO-7-chat", MROpened, "2026-09-03", "")
		}, []string{"di:feature/DEMO-7-chat"}},
		{"a branch named after a card counts once it merges out", func(f *fixture) {
			f.mr("di", "DEMO-1 part", "feat/a", "feature/DEMO-7-chat", MRMerged, "2026-09-01", "2026-09-02")
			f.mr("di", "DEMO-7 chat", "feature/DEMO-7-chat", "develop", MRMerged, "2026-09-05", "2026-09-06")
		}, []string{"di:feature/DEMO-7-chat"}},
		{"closed MRs are ignored", func(f *fixture) {
			f.mr("di", "DEMO-1 wallet", "feat/DEMO-1", "feature/demo-wallet", "closed", "2026-09-01", "")
		}, nil},
		{"role targets are not long-lived", func(f *fixture) {
			f.mr("di", "DEMO-1 a", "feat/a", "develop", MRMerged, "2026-09-01", "2026-09-02")
			f.mr("di", "DEMO-2 b", "feat/b", "feature/sprint-20-2026", MRMerged, "2026-09-01", "2026-09-02")
			f.mr("di", "DEMO-3 c", "feat/c", "feature/sprint-2026-21", MRMerged, "2026-09-01", "2026-09-02")
			f.mr("di", "DEMO-4 d", "feat/d", "release/3.5.0", MRMerged, "2026-09-01", "2026-09-02")
			f.mr("di", "DEMO-5 e", "feat/e", "hotfix/3.4.1", MRMerged, "2026-09-01", "2026-09-02")
			f.mr("di", "Release 3.5.0", "release/3.5.0", "main", MRMerged, "2026-09-01", "2026-09-02")
		}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, today)
			tc.setup(f)
			if got := llbBranches(f.reconcile()); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("long-lived branches = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestLLBBranchFacts(t *testing.T) {
	f := newFixture(t, today)
	a := f.mr("di", "DEMO-1 wallet", "feat/DEMO-1", "feature/demo-wallet", MRMerged, "2026-09-01", "2026-09-02")
	f.mr("di", "DEMO-2 top-up", "feat/DEMO-2", "feature/demo-wallet", MROpened, "2026-09-10", "")
	f.mr("di", "wallet into develop", "feature/demo-wallet", "develop", "closed", "2026-09-20", "")
	f.ahead("di", "feature/demo-wallet", 3)
	f.mr("di", "DEMO-3 bond", "feat/DEMO-3", "feature/demo-bond", MRMerged, "2026-08-01", "2026-08-02")
	f.mr("di", "bond, first try", "feature/demo-bond", "develop", MRMerged, "2026-08-10", "2026-08-11")
	last := f.mr("di", "bond", "feature/demo-bond", "develop", MRMerged, "2026-08-20", "2026-08-21")
	f.ahead("di", "feature/demo-bond", 9)
	b := f.reconcile()

	_, wallet := llbBranch(t, b, "di", "feature/demo-wallet")
	if wallet.OpenedOn != a.CreatedOn || wallet.State != LLBOpen || wallet.Landing != nil {
		t.Fatalf("wallet = opened %s state %s landing %v; want opened %s, open, no landing (the closed MR out is ignored)",
			wallet.OpenedOn, wallet.State, wallet.Landing, a.CreatedOn)
	}
	if wallet.Ahead == nil || *wallet.Ahead != 3 {
		t.Fatalf("wallet ahead = %v, want 3 from compare develop..branch", wallet.Ahead)
	}
	var keys []string
	for _, c := range wallet.Cards {
		keys = append(keys, c.Key+"!"+strconv.Itoa(c.MR.IID))
	}
	if want := []string{"DEMO-1!" + strconv.Itoa(a.IID), "DEMO-2!" + strconv.Itoa(a.IID+1)}; !reflect.DeepEqual(keys, want) {
		t.Fatalf("wallet cards = %v, want %v", keys, want)
	}

	_, bond := llbBranch(t, b, "di", "feature/demo-bond")
	if bond.Landing == nil || bond.Landing.IID != last.IID {
		t.Fatalf("bond landing = %+v, want the latest MR out !%d", bond.Landing, last.IID)
	}
	if bond.Ahead != nil {
		t.Fatalf("bond ahead = %d, want none once landed", *bond.Ahead)
	}
}

func TestLLBStates(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(f *fixture)
		want  LLBState
	}{
		{"no MR out", func(*fixture) {}, LLBOpen},
		{"open MR into the sprint branch", func(f *fixture) {
			f.mr("di", "wallet", "feature/demo-wallet", "feature/sprint-21-2026", MROpened, "2026-10-12", "")
		}, LLBLanding},
		{"open MR into develop", func(f *fixture) {
			f.mr("di", "wallet", "feature/demo-wallet", "develop", MROpened, "2026-10-12", "")
		}, LLBLandingWrong},
		{"merged into the sprint branch", func(f *fixture) {
			f.mr("di", "wallet", "feature/demo-wallet", "feature/sprint-21-2026", MRMerged, "2026-10-12", "2026-10-12")
		}, LLBLanded},
		{"merged into develop before the cutover", func(f *fixture) {
			f.mr("di", "wallet", "feature/demo-wallet", "develop", MRMerged, "2026-09-20", "2026-09-21")
		}, LLBLanded},
		{"merged into develop after the cutover", func(f *fixture) {
			f.mr("di", "wallet", "feature/demo-wallet", "develop", MRMerged, "2026-10-12", "2026-10-12")
		}, LLBLandedWrong},
		{"a card MR in after the landing", func(f *fixture) {
			f.mr("di", "wallet", "feature/demo-wallet", "feature/sprint-21-2026", MRMerged, "2026-10-08", "2026-10-08")
			f.mr("di", "DEMO-9 late", "feat/DEMO-9", "feature/demo-wallet", MRMerged, "2026-10-09", "2026-10-10")
		}, LLBStranded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, today)
			f.mr("di", "DEMO-50 other", "feat/DEMO-50", "feature/sprint-20-2026", MRMerged, "2026-10-01", "2026-10-01")
			f.mr("di", "DEMO-1 wallet", "feat/DEMO-1", "feature/demo-wallet", MRMerged, "2026-09-01", "2026-09-02")
			tc.setup(f)
			_, br := llbBranch(t, f.reconcile(), "di", "feature/demo-wallet")
			if br.State != tc.want {
				t.Fatalf("state = %s, want %s", br.State, tc.want)
			}
		})
	}
}

func TestLLBCutoverIsTheFirstMergeIntoASprintBranch(t *testing.T) {
	f := newFixture(t, today)
	f.mr("di", "DEMO-1", "feat/a", "feature/sprint-2026-20", MRMerged, "2026-10-04", "2026-10-05")
	f.mr("di", "DEMO-2", "feat/b", "feature/sprint-20-2026", MRMerged, "2026-10-01", "2026-10-07")
	f.mr("di", "DEMO-3", "feat/c", "feature/sprint-20-2026", MROpened, "2026-09-01", "")
	f.mr("da", "DEMO-4", "feat/d", "develop", MRMerged, "2026-10-01", "2026-10-02")
	b := f.reconcile()
	if got, want := b.Cutover, map[AppID]Day{"di": day(t, "2026-10-05")}; !reflect.DeepEqual(got, want) {
		t.Fatalf("cutover = %v, want %v", got, want)
	}
}

func TestLLBTwinsGroupBySharedCardKeysAndAPlanOverrides(t *testing.T) {
	f := newFixture(t, today)
	f.mr("di", "DEMO-1 call", "feat/a", "feature/demo-call-2.0", MRMerged, "2026-09-01", "2026-09-02")
	f.mr("da", "DEMO-1 call", "feat/a", "feature/demo-call-2", MRMerged, "2026-09-01", "2026-09-02")
	f.mr("da", "DEMO-5 wallet", "feat/b", "feature/demo-wallet", MRMerged, "2026-09-01", "2026-09-02")
	f.mr("di", "DEMO-6 coupon", "feat/c", "feature/demo-coupon-3.0", MRMerged, "2026-09-01", "2026-09-02")
	f.mr("di", "DEMO-7 coupon", "feat/d", "feature/demo-coupon-3.1", MRMerged, "2026-09-01", "2026-09-02")

	groups := func(b Board) map[PlanID][]string {
		out := map[PlanID][]string{}
		for _, g := range b.LLBs {
			for _, br := range g.Branches {
				out[g.ID] = append(out[g.ID], string(br.App)+":"+br.Branch)
			}
		}
		return out
	}
	want := map[PlanID][]string{
		"demo-call":       {"di:feature/demo-call-2.0", "da:feature/demo-call-2"},
		"demo-coupon":     {"di:feature/demo-coupon-3.0"},
		"demo-coupon-3.1": {"di:feature/demo-coupon-3.1"},
		"demo-wallet":     {"da:feature/demo-wallet"},
	}
	if got := groups(f.reconcile()); !reflect.DeepEqual(got, want) {
		t.Fatalf("derived groups = %v, want %v", got, want)
	}

	f.human.Plans = []LlbPlan{{
		ID: "wallet-call", Name: "Wallet call", TargetKind: TargetUnplanned, Version: 2,
		Branches: []PlanBranch{{App: "da", Branch: "feature/demo-wallet"}, {App: "di", Branch: "feature/demo-call-2.0"}},
	}}
	want = map[PlanID][]string{
		"wallet-call":     {"di:feature/demo-call-2.0", "da:feature/demo-wallet"},
		"demo-call":       {"da:feature/demo-call-2"},
		"demo-coupon":     {"di:feature/demo-coupon-3.0"},
		"demo-coupon-3.1": {"di:feature/demo-coupon-3.1"},
	}
	b := f.reconcile()
	if got := groups(b); !reflect.DeepEqual(got, want) {
		t.Fatalf("groups with a plan = %v, want %v", got, want)
	}
	g, _ := llbBranch(t, b, "da", "feature/demo-wallet")
	if g.Name != "Wallet call" || g.Plan == nil || g.Plan.Version != 2 {
		t.Fatalf("planned group = %+v, want the plan's name and the plan", g)
	}

	f.human.Plans = []LlbPlan{{ID: "demo-coupon-3.1", Name: "Coupon next", TargetKind: TargetDropped}}
	g, _ = llbBranch(t, f.reconcile(), "di", "feature/demo-coupon-3.1")
	if g.ID != "demo-coupon-3.1" || g.Plan == nil || g.Name != "Coupon next" {
		t.Fatalf("a plan with no branches attaches to the group with its id; got %+v", g)
	}
}

func TestLLBTarget(t *testing.T) {
	train := func(n int) TrainID { return TrainID{Number: n, Year: fixtureYear} }
	for _, tc := range []struct {
		name  string
		setup func(f *fixture)
		plan  *LlbPlan
		want  LLBTarget
		state LLBState
	}{
		{"landed into a sprint branch rides the next train", func(f *fixture) {
			f.mr("di", "wallet", "feature/demo-wallet", "feature/sprint-21-2026", MRMerged, "2026-10-12", "2026-10-12")
		}, nil, LLBLandedIn{Train: train(22)}, LLBLanded},
		{"landed into develop rides the first train frozen after it", func(f *fixture) {
			f.mr("di", "wallet", "feature/demo-wallet", "develop", MRMerged, "2026-09-20", "2026-09-21")
		}, nil, LLBLandedIn{Train: train(21)}, LLBLanded},
		{"landed into develop after the cutover is still landed, in the wrong place", func(f *fixture) {
			f.mr("di", "wallet", "feature/demo-wallet", "develop", MRMerged, "2026-10-12", "2026-10-12")
		}, nil, LLBLandedIn{Train: train(22)}, LLBLandedWrong},
		{"planned", func(*fixture) {}, &LlbPlan{TargetKind: TargetTrain, TargetTrain: &TrainID{Number: 22, Year: fixtureYear}},
			LLBPlanned{Train: train(22)}, LLBOpen},
		{"no plan", func(*fixture) {}, nil, LLBNoTarget{}, LLBOpen},
		{"unplanned", func(*fixture) {}, &LlbPlan{TargetKind: TargetUnplanned}, LLBNoTarget{}, LLBOpen},
		{"dropped", func(*fixture) {}, &LlbPlan{TargetKind: TargetDropped}, LLBDropped{}, LLBOpen},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, today)
			f.version("di", "21", "3.5.0", liveFreeze, liveDue)
			f.version("di", "22", "3.6.0", "2026-10-20", "2026-10-27")
			f.mr("di", "DEMO-50 other", "feat/DEMO-50", "feature/sprint-20-2026", MRMerged, "2026-10-01", "2026-10-01")
			f.mr("di", "DEMO-1 wallet", "feat/DEMO-1", "feature/demo-wallet", MRMerged, "2026-09-01", "2026-09-02")
			tc.setup(f)
			if tc.plan != nil {
				p := *tc.plan
				p.ID, p.Name = "demo-wallet", "Wallet"
				f.human.Plans = []LlbPlan{p}
			}
			g, br := llbBranch(t, f.reconcile(), "di", "feature/demo-wallet")
			if !reflect.DeepEqual(g.Target, tc.want) || br.State != tc.state {
				t.Fatalf("target %#v state %s, want %#v %s", g.Target, br.State, tc.want, tc.state)
			}
		})
	}
}
