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
		State: state, Author: "Kai", AuthorUsername: "kai", CreatedOn: day(f.t, created),
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

// epic lists key on page p as an epic, as Jira reads it.
func (f *fixture) epic(p *PageFact, key string) {
	f.card(p, key).IssueType = IssueTypeEpic
}

// parent makes the long-lived card key a child of parent, of type kind.
func (f *fixture) parent(key, parent, kind string) {
	f.llbCards = append(f.llbCards, &IssueFact{Key: key, Summary: "Card " + key, IssueType: "Story", Parent: parent, ParentType: kind})
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
		{"a branch named after a card counts with two other cards' MRs in", func(f *fixture) {
			f.mr("di", "DEMO-1 part", "feat/a", "feature/DEMO-7-chat", MRMerged, "2026-09-01", "2026-09-02")
			f.mr("di", "DEMO-2 part", "feat/b", "feature/DEMO-7-chat", MROpened, "2026-09-03", "")
		}, []string{"di:feature/DEMO-7-chat"}},
		{"a stacked MR into a card's branch that merges out is not long-lived", func(f *fixture) {
			f.mr("di", "DEMO-1 part", "feat/a", "feature/DEMO-7-chat", MRMerged, "2026-09-01", "2026-09-02")
			f.mr("di", "DEMO-7 chat", "feature/DEMO-7-chat", "feature/sprint-20-2026", MRMerged, "2026-09-05", "2026-09-06")
		}, nil},
		{"two MRs of one other card are one card", func(f *fixture) {
			f.mr("di", "DEMO-1 part", "feat/a", "feature/DEMO-7-chat", MRMerged, "2026-09-01", "2026-09-02")
			f.mr("di", "DEMO-1 more", "feat/a2", "feature/DEMO-7-chat", MROpened, "2026-09-03", "")
		}, nil},
		{"the branch's own card coming back in does not count", func(f *fixture) {
			f.mr("di", "DEMO-1 part", "feat/a", "feature/DEMO-7-chat", MRMerged, "2026-09-01", "2026-09-02")
			f.mr("di", "DEMO-7 fixup", "feat/DEMO-7-fix", "feature/DEMO-7-chat", MROpened, "2026-09-03", "")
		}, nil},
		{"a branch with no card key still counts once it merges out", func(f *fixture) {
			f.mr("di", "wallet", "feature/demo-wallet", "develop", MRMerged, "2026-09-05", "2026-09-06")
			f.mr("di", "DEMO-1 wallet", "feat/DEMO-1", "feature/demo-wallet", MRMerged, "2026-09-01", "2026-09-02")
		}, []string{"di:feature/demo-wallet"}},
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

func TestLLBIsGoneWhenItsCompareFindsNoBranch(t *testing.T) {
	f := newFixture(t, today)
	f.mr("di", "DEMO-1 wallet", "feat/DEMO-1", "feature/demo-wallet", MRMerged, "2026-09-01", "2026-09-02")
	f.mr("di", "DEMO-2 bond", "feat/DEMO-2", "feature/demo-bond", MRMerged, "2026-09-01", "2026-09-02")
	f.ahead("di", "feature/demo-wallet", 0)
	f.ahead("di", "feature/demo-bond", 0)
	f.statuses[KindCompare.Name] = func(st *ResourceStatus) {
		if st.Scope == CompareScope(1001, "develop", "feature/demo-wallet") {
			st.CompleteAt, st.ErrorClass = nil, ErrNotFound
		}
	}
	b := f.reconcile()
	_, wallet := llbBranch(t, b, "di", "feature/demo-wallet")
	_, bond := llbBranch(t, b, "di", "feature/demo-bond")
	if !wallet.Gone || bond.Gone {
		t.Fatalf("gone: wallet %v bond %v; want only the branch GitLab cannot find", wallet.Gone, bond.Gone)
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

func llbGroups(b Board) map[PlanID][]string {
	out := map[PlanID][]string{}
	for _, g := range b.LLBs {
		for _, br := range g.Branches {
			out[g.ID] = append(out[g.ID], string(br.App)+":"+br.Branch)
		}
	}
	return out
}

func TestLLBTwinsGroupBySharedCardKeysAndAPlanOverrides(t *testing.T) {
	f := newFixture(t, today)
	f.mr("di", "DEMO-1 call", "feat/a", "feature/demo-call-2.0", MRMerged, "2026-09-01", "2026-09-02")
	f.mr("da", "DEMO-1 call", "feat/a", "feature/demo-call-2", MRMerged, "2026-09-01", "2026-09-02")
	f.mr("da", "DEMO-5 wallet", "feat/b", "feature/demo-wallet", MRMerged, "2026-09-01", "2026-09-02")
	f.mr("di", "DEMO-6 coupon", "feat/c", "feature/demo-coupon-3.0", MRMerged, "2026-09-01", "2026-09-02")
	f.mr("di", "DEMO-7 coupon", "feat/d", "feature/demo-coupon-3.1", MRMerged, "2026-09-01", "2026-09-02")

	want := map[PlanID][]string{
		"demo-call":       {"di:feature/demo-call-2.0", "da:feature/demo-call-2"},
		"demo-coupon":     {"di:feature/demo-coupon-3.0"},
		"demo-coupon-3.1": {"di:feature/demo-coupon-3.1"},
		"demo-wallet":     {"da:feature/demo-wallet"},
	}
	if got := llbGroups(f.reconcile()); !reflect.DeepEqual(got, want) {
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
	if got := llbGroups(b); !reflect.DeepEqual(got, want) {
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

func TestLLBPlanTakesALaterSameNameBranchOfAnotherApp(t *testing.T) {
	f := newFixture(t, today)
	f.sprintCard(demoSprint(f), "DEMO-1", "In Progress", true)
	f.mr("da", "DEMO-1 list", "feat/DEMO-1", "feature/demo-history-list", MRMerged, "2026-10-07", "2026-10-08")
	f.mr("di", "DEMO-1 list", "feat/DEMO-1", "feature/demo-history-list", MROpened, "2026-10-09", "")
	f.human.Plans = []LlbPlan{{
		ID: "demo-history-list", Name: "History list", Version: 1,
		TargetKind: TargetTrain, TargetTrain: &TrainID{Number: 23, Year: fixtureYear},
		Branches: []PlanBranch{{App: "da", Branch: "feature/demo-history-list"}},
	}}
	b := f.reconcile()
	want := map[PlanID][]string{"demo-history-list": {"di:feature/demo-history-list", "da:feature/demo-history-list"}}
	if got := llbGroups(b); !reflect.DeepEqual(got, want) {
		t.Fatalf("groups = %v, want %v", got, want)
	}
	g, _ := llbBranch(t, b, "di", "feature/demo-history-list")
	if want := (LLBPlanned{Train: TrainID{Number: 23, Year: fixtureYear}}); !reflect.DeepEqual(g.Target, want) {
		t.Fatalf("target = %#v, want %#v", g.Target, want)
	}
	if got := firedIDs(b, "G4"); got != nil {
		t.Fatalf("G4 hits = %v, want none", got)
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
		}, nil, LLBLandedIn{Train: train(22), App: "di", Branch: "feature/demo-wallet"}, LLBLanded},
		{"landed into develop rides the first train frozen after it", func(f *fixture) {
			f.mr("di", "wallet", "feature/demo-wallet", "develop", MRMerged, "2026-09-20", "2026-09-21")
		}, nil, LLBLandedIn{Train: train(21), App: "di", Branch: "feature/demo-wallet"}, LLBLanded},
		{"landed into develop after the cutover is still landed, in the wrong place", func(f *fixture) {
			f.mr("di", "wallet", "feature/demo-wallet", "develop", MRMerged, "2026-10-12", "2026-10-12")
		}, nil, LLBLandedIn{Train: train(22), App: "di", Branch: "feature/demo-wallet"}, LLBLandedWrong},
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

func TestLLBTargetFromEpic(t *testing.T) {
	train := func(n int) TrainID { return TrainID{Number: n, Year: fixtureYear} }
	wallet := func(f *fixture, keys ...string) {
		for _, k := range keys {
			f.mr("di", k+" wallet", "feat/"+k, "feature/demo-wallet", MRMerged, "2026-09-01", "2026-09-02")
		}
	}
	under := func(f *fixture, epic string, keys ...string) {
		for _, k := range keys {
			f.parent(k, epic, IssueTypeEpic)
		}
	}
	for _, tc := range []struct {
		name  string
		setup func(f *fixture, p20, p21, p22 *PageFact)
		plan  *LlbPlan
		want  LLBTarget
	}{
		{"an epic listed on a train page covering most cards sets the target", func(f *fixture, _, _, p22 *PageFact) {
			wallet(f, "DEMO-1", "DEMO-2", "DEMO-3")
			under(f, "DEMO-500", "DEMO-1", "DEMO-2")
			f.epic(p22, "DEMO-500")
		}, nil, LLBFromEpic{Train: train(22), Epic: "DEMO-500", Covered: 2, Total: 3}},
		{"an unplanned plan still takes the epic's train", func(f *fixture, _, _, p22 *PageFact) {
			wallet(f, "DEMO-1", "DEMO-2", "DEMO-3")
			under(f, "DEMO-500", "DEMO-1", "DEMO-2")
			f.epic(p22, "DEMO-500")
		}, &LlbPlan{TargetKind: TargetUnplanned}, LLBFromEpic{Train: train(22), Epic: "DEMO-500", Covered: 2, Total: 3}},
		{"a plan naming a train beats the epic", func(f *fixture, _, _, p22 *PageFact) {
			wallet(f, "DEMO-1", "DEMO-2")
			under(f, "DEMO-500", "DEMO-1", "DEMO-2")
			f.epic(p22, "DEMO-500")
		}, &LlbPlan{TargetKind: TargetTrain, TargetTrain: &TrainID{Number: 21, Year: fixtureYear}}, LLBPlanned{Train: train(21)}},
		{"a dropped plan beats the epic", func(f *fixture, _, _, p22 *PageFact) {
			wallet(f, "DEMO-1", "DEMO-2")
			under(f, "DEMO-500", "DEMO-1", "DEMO-2")
			f.epic(p22, "DEMO-500")
		}, &LlbPlan{TargetKind: TargetDropped}, LLBDropped{}},
		{"the observed landing beats the epic", func(f *fixture, _, _, p22 *PageFact) {
			wallet(f, "DEMO-1", "DEMO-2")
			under(f, "DEMO-500", "DEMO-1", "DEMO-2")
			f.epic(p22, "DEMO-500")
			f.mr("di", "wallet", "feature/demo-wallet", "feature/sprint-20-2026", MRMerged, "2026-10-05", "2026-10-05")
		}, nil, LLBLandedIn{Train: train(21), App: "di", Branch: "feature/demo-wallet"}},
		{"exactly half is not a majority", func(f *fixture, _, _, p22 *PageFact) {
			wallet(f, "DEMO-1", "DEMO-2", "DEMO-3", "DEMO-4")
			under(f, "DEMO-500", "DEMO-1", "DEMO-2")
			under(f, "DEMO-600", "DEMO-3", "DEMO-4")
			f.epic(p22, "DEMO-500")
		}, nil, LLBNoTarget{}},
		{"cards with no epic count against the coverage", func(f *fixture, _, _, p22 *PageFact) {
			wallet(f, "DEMO-1", "DEMO-2", "DEMO-3", "DEMO-4")
			under(f, "DEMO-500", "DEMO-1", "DEMO-2")
			f.parent("DEMO-3", "DEMO-2", "Story")
			f.epic(p22, "DEMO-500")
		}, nil, LLBNoTarget{}},
		{"a parent that is not an epic is no epic, even when a page lists it", func(f *fixture, _, _, p22 *PageFact) {
			wallet(f, "DEMO-1", "DEMO-2")
			f.parent("DEMO-1", "DEMO-500", "Story")
			f.parent("DEMO-2", "DEMO-500", "Story")
			f.card(p22, "DEMO-500")
		}, nil, LLBNoTarget{}},
		{"an epic no page lists gives none", func(f *fixture, _, _, _ *PageFact) {
			wallet(f, "DEMO-1", "DEMO-2")
			under(f, "DEMO-500", "DEMO-1", "DEMO-2")
		}, nil, LLBNoTarget{}},
		{"a card on two platforms counts once", func(f *fixture, _, _, p22 *PageFact) {
			wallet(f, "DEMO-1", "DEMO-1", "DEMO-2", "DEMO-3")
			f.mr("da", "DEMO-1 wallet", "feat/DEMO-1", "feature/demo-wallet", MRMerged, "2026-09-01", "2026-09-02")
			under(f, "DEMO-500", "DEMO-1")
			f.epic(p22, "DEMO-500")
		}, nil, LLBNoTarget{}},
		{"two pages: the earliest unpublished train wins and the other is named", func(f *fixture, _, p21, p22 *PageFact) {
			wallet(f, "DEMO-1", "DEMO-2")
			under(f, "DEMO-500", "DEMO-1", "DEMO-2")
			f.epic(p22, "DEMO-500")
			f.epic(p21, "DEMO-500")
		}, nil, LLBFromEpic{Train: train(21), Epic: "DEMO-500", Covered: 2, Total: 2, Also: []TrainID{train(22)}}},
		{"a published train's epic is ignored", func(f *fixture, p20, _, _ *PageFact) {
			wallet(f, "DEMO-1", "DEMO-2")
			under(f, "DEMO-500", "DEMO-1", "DEMO-2")
			f.epic(p20, "DEMO-500")
		}, nil, LLBNoTarget{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, today)
			f.version("di", "20", "3.4.0", "2026-10-01", "2026-10-09")
			f.version("di", "21", "3.5.0", liveFreeze, liveDue)
			f.version("di", "22", "3.6.0", "2026-10-20", "2026-10-27")
			f.mr("di", "DEMO-50 other", "feat/DEMO-50", "feature/sprint-19-2026", MRMerged, "2026-09-20", "2026-09-20")
			p20 := f.page("demo", "20", "2026-09-10", "2026-10-01", "2026-10-09")
			p21 := f.page("demo", "21", "2026-09-20", liveFreeze, liveDue)
			p22 := f.page("demo", "22", "2026-10-01", "2026-10-20", "2026-10-27")
			tc.setup(f, p20, p21, p22)
			if tc.plan != nil {
				p := *tc.plan
				p.ID, p.Name = "demo-wallet", "Wallet"
				f.human.Plans = []LlbPlan{p}
			}
			g, _ := llbBranch(t, f.reconcile(), "di", "feature/demo-wallet")
			if !reflect.DeepEqual(g.Target, tc.want) {
				t.Fatalf("target = %#v, want %#v", g.Target, tc.want)
			}
		})
	}
}

func TestLLBCardsNameTheirEpicOnlyWhenTheParentIsAnEpic(t *testing.T) {
	f := newFixture(t, today)
	f.version("di", "21", "3.5.0", liveFreeze, liveDue)
	for _, k := range []string{"DEMO-1", "DEMO-2", "DEMO-3"} {
		f.mr("di", k+" wallet", "feat/"+k, "feature/demo-wallet", MRMerged, "2026-09-01", "2026-09-02")
	}
	f.parent("DEMO-1", "DEMO-500", IssueTypeEpic)
	f.parent("DEMO-2", "DEMO-1", "Story")
	_, br := llbBranch(t, f.reconcile(), "di", "feature/demo-wallet")
	got := map[string]string{}
	for _, c := range br.Cards {
		got[c.Key] = c.Epic
	}
	if want := map[string]string{"DEMO-1": "DEMO-500", "DEMO-2": "", "DEMO-3": ""}; !reflect.DeepEqual(got, want) {
		t.Fatalf("card epics = %v, want %v", got, want)
	}
}
