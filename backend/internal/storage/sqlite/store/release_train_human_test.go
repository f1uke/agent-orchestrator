package store_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	rt "github.com/aoagents/agent-orchestrator/backend/internal/releasetrain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

var humanAt = time.Date(2026, 10, 15, 3, 0, 0, 0, time.UTC)

func TestReleaseRuleSettingsRoundTripAndResetDeletes(t *testing.T) {
	s, ctx := newTestStore(t), context.Background()
	on, off := true, false
	for _, step := range []struct {
		id rt.RuleID
		v  *bool
	}{{"C2", &on}, {"A7", &off}, {"C2", &off}} {
		if err := s.SetReleaseRuleSetting(ctx, step.id, step.v, humanAt); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.LoadReleaseHumanState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if want := map[rt.RuleID]bool{"A7": false, "C2": false}; !reflect.DeepEqual(got.RuleSettings, want) {
		t.Fatalf("settings = %v, want %v", got.RuleSettings, want)
	}

	if err := s.SetReleaseRuleSetting(ctx, "A7", nil, humanAt); err != nil {
		t.Fatal(err)
	}
	got, _ = s.LoadReleaseHumanState(ctx)
	if _, ok := got.RuleSettings["A7"]; ok || len(got.RuleSettings) != 1 {
		t.Fatalf("settings after reset = %v, want A7 gone so its default applies", got.RuleSettings)
	}
}

func TestReleaseHidesRoundTripAndUnhideReportsWhatItRemoved(t *testing.T) {
	s, ctx := newTestStore(t), context.Background()
	hide := rt.Hide{Fingerprint: "a1b2c3", Severity: rt.SevRisk, Reason: "waiting on the approver", HiddenAt: humanAt}
	if err := s.HideReleaseHit(ctx, "D3~v/21-2026/di", hide); err != nil {
		t.Fatal(err)
	}
	if err := s.HideReleaseHit(ctx, "C2~v/21-2026/da", rt.Hide{Fingerprint: "ffff", Severity: rt.SevInfo, HiddenAt: humanAt}); err != nil {
		t.Fatal(err)
	}
	got, err := s.LoadReleaseHumanState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.Hides["D3~v/21-2026/di"] != hide || len(got.Hides) != 2 {
		t.Fatalf("hides = %+v, want the D3 hide back as written", got.Hides)
	}

	removed, err := s.UnhideReleaseHits(ctx, []rt.HitID{"D3~v/21-2026/di", "Z9~nothing"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(removed, []rt.HitID{"D3~v/21-2026/di"}) {
		t.Fatalf("removed = %v, want only the hide that existed", removed)
	}
	got, _ = s.LoadReleaseHumanState(ctx)
	if _, ok := got.Hides["D3~v/21-2026/di"]; ok || len(got.Hides) != 1 {
		t.Fatalf("hides after unhide = %v", got.Hides)
	}
}

func demoPlan() rt.LlbPlan {
	train := rt.TrainID{Number: 22, Year: 2026}
	return rt.LlbPlan{
		ID: "demo-llb", Name: "Demo cards", TargetKind: rt.TargetTrain, TargetTrain: &train, Note: "lands with #22",
		Branches:  []rt.PlanBranch{{App: "da", Branch: "feature/demo-llb"}, {App: "di", Branch: "feature/demo-llb"}},
		Accepted:  []rt.PlanAccepted{{MRRef: "di!52", Reason: "reviewed together"}},
		UpdatedBy: "cli", UpdatedAt: humanAt,
	}
}

func TestReleasePlanRoundTripsAndBumpsItsVersion(t *testing.T) {
	s, ctx := newTestStore(t), context.Background()
	p := demoPlan()
	put, err := s.PutReleaseLlbPlan(ctx, p, 0)
	if err != nil {
		t.Fatal(err)
	}
	if put.Version != 1 {
		t.Fatalf("created version = %d, want 1", put.Version)
	}
	plans, err := s.ListReleaseLlbPlans(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != 1 || !reflect.DeepEqual(plans[0], put) {
		t.Fatalf("plans = %+v, want %+v", plans, put)
	}

	p.TargetKind, p.TargetTrain = rt.TargetUnplanned, nil
	p.Branches = p.Branches[:1]
	p.Accepted = []rt.PlanAccepted{}
	put, err = s.PutReleaseLlbPlan(ctx, p, 1)
	if err != nil {
		t.Fatal(err)
	}
	plans, _ = s.ListReleaseLlbPlans(ctx)
	if put.Version != 2 || !reflect.DeepEqual(plans[0], put) {
		t.Fatalf("after edit = %+v, want %+v at version 2", plans, put)
	}
}

func TestReleasePlanRefusesAStaleVersionAndWritesNothing(t *testing.T) {
	s, ctx := newTestStore(t), context.Background()
	if _, err := s.PutReleaseLlbPlan(ctx, demoPlan(), 0); err != nil {
		t.Fatal(err)
	}
	for _, ifVersion := range []int{0, 2} {
		edit := demoPlan()
		edit.Name = "someone else's edit"
		_, err := s.PutReleaseLlbPlan(ctx, edit, ifVersion)
		var verr *rt.PlanVersionError
		if !errors.As(err, &verr) || verr.Current != 1 {
			t.Fatalf("put with ifVersion %d = %v, want a version error naming version 1", ifVersion, err)
		}
	}
	_, err := s.PutReleaseLlbPlan(ctx, rt.LlbPlan{ID: "ghost", Name: "x", TargetKind: rt.TargetDropped, UpdatedBy: "cli", UpdatedAt: humanAt}, 3)
	var verr *rt.PlanVersionError
	if !errors.As(err, &verr) || verr.Current != 0 {
		t.Fatalf("put of a missing plan at version 3 = %v, want a version error naming 0", err)
	}
	plans, _ := s.ListReleaseLlbPlans(ctx)
	if len(plans) != 1 || plans[0].Name != "Demo cards" || plans[0].Version != 1 {
		t.Fatalf("plans = %+v, want the first write untouched", plans)
	}
}

func TestReleasePlanRefusesABranchAnotherPlanHolds(t *testing.T) {
	s, ctx := newTestStore(t), context.Background()
	if _, err := s.PutReleaseLlbPlan(ctx, demoPlan(), 0); err != nil {
		t.Fatal(err)
	}
	other := rt.LlbPlan{ID: "other-llb", Name: "Other", TargetKind: rt.TargetDropped, UpdatedBy: "cli", UpdatedAt: humanAt,
		Branches: []rt.PlanBranch{{App: "pi", Branch: "feature/other"}, {App: "di", Branch: "feature/demo-llb"}}}
	_, err := s.PutReleaseLlbPlan(ctx, other, 0)
	var taken *rt.BranchTakenError
	if !errors.As(err, &taken) || taken.Owner != "demo-llb" || taken.Branch.App != "di" {
		t.Fatalf("put of a held branch = %v, want it owned by demo-llb", err)
	}
	plans, _ := s.ListReleaseLlbPlans(ctx)
	if len(plans) != 1 {
		t.Fatalf("plans = %+v, want the refused plan not written", plans)
	}
	if _, err := s.PutReleaseLlbPlan(ctx, demoPlan(), 1); err != nil {
		t.Fatalf("a plan rewriting its own branches = %v, want no conflict with itself", err)
	}
}

func TestReleaseNoMRMarksRoundTripAndUnmarkReportsWhetherOneExisted(t *testing.T) {
	s, ctx := newTestStore(t), context.Background()
	mark := rt.NoMRMark{Note: "config only", MarkedBy: "app", MarkedAt: humanAt}
	if err := s.MarkReleaseCardNoMR(ctx, "DEMO-7", mark); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkReleaseCardNoMR(ctx, "DEMO-8", rt.NoMRMark{MarkedBy: "cli", MarkedAt: humanAt}); err != nil {
		t.Fatal(err)
	}
	again := rt.NoMRMark{Note: "still config", MarkedBy: "cli", MarkedAt: humanAt.Add(time.Hour)}
	if err := s.MarkReleaseCardNoMR(ctx, "DEMO-7", again); err != nil {
		t.Fatal(err)
	}
	got, err := s.LoadReleaseHumanState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]rt.NoMRMark{"DEMO-7": again, "DEMO-8": {MarkedBy: "cli", MarkedAt: humanAt}}; !reflect.DeepEqual(got.NoMR, want) {
		t.Fatalf("marks = %+v, want %+v", got.NoMR, want)
	}

	for _, step := range []struct {
		key  string
		want bool
	}{{"DEMO-7", true}, {"DEMO-7", false}, {"DEMO-404", false}} {
		removed, err := s.UnmarkReleaseCardNoMR(ctx, step.key)
		if err != nil || removed != step.want {
			t.Fatalf("unmark %s = %v, %v; want %v", step.key, removed, err, step.want)
		}
	}
	got, _ = s.LoadReleaseHumanState(ctx)
	if _, ok := got.NoMR["DEMO-7"]; ok || len(got.NoMR) != 1 {
		t.Fatalf("marks after unmark = %+v, want only DEMO-8", got.NoMR)
	}
}

func TestReleaseNoMRMarkSurvivesAReopen(t *testing.T) {
	dir, ctx := t.TempDir(), context.Background()
	first, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	mark := rt.NoMRMark{Note: "config only", MarkedBy: "app", MarkedAt: humanAt}
	if err := first.MarkReleaseCardNoMR(ctx, "DEMO-7", mark); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })
	got, err := second.LoadReleaseHumanState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.NoMR["DEMO-7"] != mark {
		t.Fatalf("mark after reopen = %+v, want %+v", got.NoMR, mark)
	}
}
