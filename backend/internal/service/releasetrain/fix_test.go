package releasetrain

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	rt "github.com/aoagents/agent-orchestrator/backend/internal/releasetrain"
)

const e1Hit = rt.HitID("E1~v/20-2026/di")

type refuseAll struct{ t *testing.T }

func (r refuseAll) RoundTrip(req *http.Request) (*http.Response, error) {
	r.t.Errorf("a fix sent %s %s; every fix but L3 is a dry run", req.Method, req.URL)
	return nil, errors.New("no requests from a fix")
}

func newFixWorld(t *testing.T) *boardWorld {
	w := newBoardWorld(t)
	w.svc.d.Fixes = w.store
	w.svc.UsePlanTargetExecutor()
	prev := http.DefaultTransport
	http.DefaultTransport = refuseAll{t}
	t.Cleanup(func() { http.DefaultTransport = prev })
	return w
}

// seedShipped is #20 on Demo iOS: tagged, its due date passed, its Jira release not released (E1).
func (w *boardWorld) seedShipped() {
	t := w.t
	start, due := rt.Day{Y: 2026, M: 9, D: 28}, rt.Day{Y: 2026, M: 10, D: 5}
	w.write(rt.KindMilestones.Name, rt.ProjectScope(1001), item(t, rt.KindMilestones, rt.MilestoneFact{
		Project: 1001, ID: 10, Title: "Release 3.4.0", Description: "Release Train #20/2026", State: "active", Start: &start, Due: &due}))
	w.write(rt.KindTags.Name, rt.ProjectScope(1001), item(t, rt.KindTags, rt.TagFact{Project: 1001, Name: "3.4.0", CommitOn: due}))
	w.writeJira(rt.JiraVersionFact{ID: "5001", Name: "iOS 3.4.0", Description: "Release Train #20/2026", URL: "https://jira.example.com/versions/5001", Approvers: []rt.Approver{}})
}

func (w *boardWorld) writeJira(versions ...rt.JiraVersionFact) {
	items := make([]rt.Item, len(versions))
	for i, v := range versions {
		items[i] = item(w.t, rt.KindJiraVersions, v)
	}
	w.write(rt.KindJiraVersions.Name, "project", items...)
}

func TestConfirmRecordsADryRunWithTheRequestAndSendsNothing(t *testing.T) {
	w := newFixWorld(t)
	w.seedShipped()
	ctx := context.Background()
	plan, err := w.svc.PlanFix(ctx, e1Hit, "")
	if err != nil || plan.Mode != rt.ModeQueue {
		t.Fatalf("plan = %+v, %v", plan, err)
	}
	f, err := w.svc.ConfirmFix(ctx, e1Hit, "", plan.Fingerprint, "cli:dee")
	if err != nil || f.State != rt.FixDryRun || len(f.Events) != 1 {
		t.Fatalf("confirm = %+v, %v", f, err)
	}
	ev := f.Events[0]
	want := `"request":{"method":"PUT","url":"https://jira.example.com/rest/api/3/version/5001","body":{"released":true,"releaseDate":"2026-10-05"}}`
	if ev.Kind != rt.EventDryRunRecorded || ev.Actor != "cli:dee" || !strings.Contains(string(ev.Detail), want) || !strings.Contains(string(ev.Detail), `"dryRun":true`) {
		t.Fatalf("event = %s %s %s; want the dry run with %s", ev.Kind, ev.Actor, ev.Detail, want)
	}
	body, _ := w.mustBoard()
	var b struct {
		Board struct {
			Alerts struct {
				Hits []struct {
					ID      rt.HitID   `json:"id"`
					Fix     string     `json:"fix"`
					OpenFix *rt.FixRef `json:"openFix"`
				} `json:"hits"`
			} `json:"alerts"`
		} `json:"board"`
	}
	if err := json.Unmarshal(body, &b); err != nil {
		t.Fatal(err)
	}
	for _, h := range b.Board.Alerts.Hits {
		if h.ID == e1Hit && (h.Fix != "queue" || h.OpenFix == nil || h.OpenFix.ID != f.ID || h.OpenFix.State != rt.FixDryRun) {
			t.Fatalf("board hit = %+v; want fix queue and the open dry run %s right after confirm", h, f.ID)
		}
	}
}

func TestDoubleConfirmReturnsTheSameFix(t *testing.T) {
	w := newFixWorld(t)
	w.seedShipped()
	ctx := context.Background()
	plan, _ := w.svc.PlanFix(ctx, e1Hit, "")
	a, err := w.svc.ConfirmFix(ctx, e1Hit, "", plan.Fingerprint, "gui:dee")
	if err != nil {
		t.Fatal(err)
	}
	b, err := w.svc.ConfirmFix(ctx, e1Hit, "", plan.Fingerprint, "gui:dee")
	if err != nil || b.ID != a.ID || len(b.Events) != 1 {
		t.Fatalf("second confirm = %+v, %v; want %s with one event", b, err, a.ID)
	}
	all, _ := w.svc.Fixes(ctx)
	if len(all) != 1 {
		t.Fatalf("fixes = %d, want 1", len(all))
	}
}

func TestConfirmAgainstAChangedPlanIs409(t *testing.T) {
	w := newFixWorld(t)
	w.seedShipped()
	ctx := context.Background()
	plan, _ := w.svc.PlanFix(ctx, e1Hit, "")
	due := rt.Day{Y: 2026, M: 10, D: 3}
	w.writeJira(rt.JiraVersionFact{ID: "5001", Name: "iOS 3.4.0", Description: "Release Train #20/2026", ReleaseDate: &due,
		URL: "https://jira.example.com/versions/5001", Approvers: []rt.Approver{}})
	_, err := w.svc.ConfirmFix(ctx, e1Hit, "", plan.Fingerprint, "cli")
	var changed *PlanChangedError
	if !errors.As(err, &changed) || changed.Plan.Fingerprint == plan.Fingerprint {
		t.Fatalf("confirm after the Jira date moved = %v; want PlanChangedError with the new plan", err)
	}
	if all, _ := w.svc.Fixes(ctx); len(all) != 0 {
		t.Fatalf("a refused confirm stored %d fixes", len(all))
	}
}

func TestUndoOfADryRunWithdrawsIt(t *testing.T) {
	w := newFixWorld(t)
	w.seedShipped()
	ctx := context.Background()
	plan, _ := w.svc.PlanFix(ctx, e1Hit, "")
	f, _ := w.svc.ConfirmFix(ctx, e1Hit, "", plan.Fingerprint, "cli")
	undone, err := w.svc.UndoFix(ctx, f.ID, "gui")
	if err != nil || undone.State != rt.FixWithdrawn || undone.Events[1].Kind != rt.EventWithdrawn || undone.Events[1].Actor != "gui" {
		t.Fatalf("undo = %+v, %v", undone, err)
	}
	if _, err := w.svc.UndoFix(ctx, f.ID, "gui"); !errors.Is(err, ErrNoUndo) {
		t.Fatalf("second undo err = %v, want ErrNoUndo", err)
	}
}

func TestConfirmOfAChooseWithoutAChoiceRecordsNothing(t *testing.T) {
	w := newFixWorld(t)
	w.seedShipped()
	early := rt.Day{Y: 2026, M: 10, D: 3}
	w.writeJira(rt.JiraVersionFact{ID: "5001", Name: "iOS 3.4.0", Description: "Release Train #20/2026", ReleaseDate: &early,
		URL: "https://jira.example.com/versions/5001", Approvers: []rt.Approver{}})
	ctx := context.Background()
	const a5 = rt.HitID("A5~v/20-2026/di")
	plan, err := w.svc.PlanFix(ctx, a5, "")
	if err != nil || plan.Mode != rt.ModeChoose {
		t.Fatalf("A5 plan = %+v, %v", plan, err)
	}
	if _, err := w.svc.ConfirmFix(ctx, a5, "", plan.Fingerprint, "cli"); !errors.Is(err, ErrNothingToRecord) {
		t.Fatalf("confirm without a choice err = %v, want ErrNothingToRecord", err)
	}
	picked, _ := w.svc.PlanFix(ctx, a5, "gitlab")
	f, err := w.svc.ConfirmFix(ctx, a5, "gitlab", picked.Fingerprint, "cli")
	if err != nil || f.State != rt.FixDryRun || f.Choice != "gitlab" {
		t.Fatalf("confirm with a choice = %+v, %v", f, err)
	}
	if _, err := w.svc.ConfirmFix(ctx, "C5~v/20-2026/di", "", "x", "cli"); !errors.Is(err, rt.ErrNoSuchHit) {
		t.Fatalf("confirm of a hit not on the board err = %v", err)
	}
}

const l3Hit = rt.HitID("L3~ct/21-2026/di/DEMO-7")

// seedParked is DEMO-7 on #21's train page while its code sits in
// feature/demo-wallet, whose plan targets #22 (L3).
func (w *boardWorld) seedParked() {
	t := w.t
	w.seed()
	w.writeJira()
	created := rt.Day{Y: 2026, M: 9, D: 20}
	w.write(rt.KindPages.Name, rt.SpaceScope("DEMOSPACE"), item(t, rt.KindPages, rt.PageFact{
		ID: "8001", Title: "Release train #21", URL: "https://wiki.example.com/pages/8001", Created: created, Family: "demo", Number: 21, Keys: []string{"DEMO-7"}}))
	w.write(rt.KindPageCards.Name, rt.PageScope("8001"), item(t, rt.KindPageCards, rt.IssueFact{
		Key: "DEMO-7", Summary: "Wallet", Status: "In Progress", StatusCategory: "indeterminate", URL: "https://jira.example.com/browse/DEMO-7"}))
	merged := rt.Day{Y: 2026, M: 9, D: 2}
	ms := 11
	w.write(rt.KindMRs.Name, rt.ProjectScope(1001),
		item(t, rt.KindMRs, rt.MRFact{Project: 1001, IID: 301, Title: "Release 3.5.0", SourceBranch: "release/3.5.0", TargetBranch: "main",
			State: rt.MROpened, Author: "Rae Example", CreatedOn: rt.Day{Y: 2026, M: 10, D: 12}, MilestoneID: &ms}),
		item(t, rt.KindMRs, rt.MRFact{Project: 1001, IID: 310, Title: "DEMO-7 wallet", SourceBranch: "feat/DEMO-7", TargetBranch: "feature/demo-wallet",
			State: rt.MRMerged, Author: "Kai", CreatedOn: merged, MergedOn: &merged}))
	train22 := rt.TrainID{Number: 22, Year: 2026}
	if _, err := w.svc.PutPlan(context.Background(), rt.LlbPlan{ID: "demo-wallet", Name: "Wallet", TargetKind: rt.TargetTrain, TargetTrain: &train22,
		Branches: []rt.PlanBranch{{App: "di", Branch: "feature/demo-wallet"}}, Accepted: []rt.PlanAccepted{}, UpdatedBy: "cli"}, 0); err != nil {
		t.Fatal(err)
	}
}

func planOf(t *testing.T, w *boardWorld) rt.LlbPlan {
	t.Helper()
	plans, err := w.svc.Plans(context.Background())
	if err != nil || len(plans) != 1 {
		t.Fatalf("plans = %+v, %v", plans, err)
	}
	return plans[0]
}

func TestL3AppliesThroughPutPlanAndUndoWritesBeforeBack(t *testing.T) {
	w := newFixWorld(t)
	w.seedParked()
	ctx := context.Background()
	plan, err := w.svc.PlanFix(ctx, l3Hit, "")
	if err != nil || plan.Mode != rt.ModeApply {
		t.Fatalf("L3 plan = %+v, %v", plan, err)
	}
	f, err := w.svc.ConfirmFix(ctx, l3Hit, "", plan.Fingerprint, "gui:dee")
	if err != nil || f.State != rt.FixApplied {
		t.Fatalf("confirm L3 = %+v, %v", f, err)
	}
	if kinds := eventKinds(f); kinds != "confirmed applied" {
		t.Fatalf("events = %s", kinds)
	}
	p := planOf(t, w)
	if p.Version != 2 || p.TargetTrain == nil || p.TargetTrain.Number != 21 || p.UpdatedBy != "gui:dee" {
		t.Fatalf("plan after apply = %+v; want version 2 targeting #21 by gui:dee", p)
	}
	again, err := w.svc.ConfirmFix(ctx, l3Hit, "", plan.Fingerprint, "gui:dee")
	if err != nil || again.ID != f.ID {
		t.Fatalf("double confirm after apply = %+v, %v; want %s back", again, err, f.ID)
	}

	undone, err := w.svc.UndoFix(ctx, f.ID, "cli")
	if err != nil || undone.State != rt.FixUndone {
		t.Fatalf("undo = %+v, %v", undone, err)
	}
	p = planOf(t, w)
	if p.Version != 3 || p.TargetTrain == nil || p.TargetTrain.Number != 22 {
		t.Fatalf("plan after undo = %+v; want #22 back at version 3", p)
	}
}

func TestL3UndoAfterThePlanMovedOnIsRefusedAndStaysApplied(t *testing.T) {
	w := newFixWorld(t)
	w.seedParked()
	ctx := context.Background()
	plan, _ := w.svc.PlanFix(ctx, l3Hit, "")
	f, err := w.svc.ConfirmFix(ctx, l3Hit, "", plan.Fingerprint, "gui")
	if err != nil {
		t.Fatal(err)
	}
	edited := planOf(t, w)
	edited.Note = "someone edited it"
	if _, err := w.svc.PutPlan(ctx, edited, edited.Version); err != nil {
		t.Fatal(err)
	}
	_, err = w.svc.UndoFix(ctx, f.ID, "cli")
	var verr *rt.PlanVersionError
	if !errors.As(err, &verr) {
		t.Fatalf("undo over a newer plan err = %v, want PlanVersionError", err)
	}
	got, _ := w.store.GetReleaseFix(ctx, f.ID)
	if got.State != rt.FixApplied || eventKinds(got) != "confirmed applied undo_failed" {
		t.Fatalf("fix = %s %s; want applied with an undo_failed event", got.State, eventKinds(got))
	}
	if p := planOf(t, w); p.Version != 3 || p.TargetTrain.Number != 21 {
		t.Fatalf("plan = %+v; a refused undo writes nothing", p)
	}
}

func TestL3ApplyAgainstAStalePlanVersionFails(t *testing.T) {
	w := newFixWorld(t)
	w.seedParked()
	ctx := context.Background()
	plan, _ := w.svc.PlanFix(ctx, l3Hit, "")
	x := w.svc.d.Executors[rt.EffectAOPlanTarget]
	stale := plan.Steps[0].Effect.(rt.EffAOPlanTarget)
	stale.PlanVersion = 7
	var verr *rt.PlanVersionError
	if _, err := x(ctx, stale, "cli"); !errors.As(err, &verr) || verr.Current != 1 {
		t.Fatalf("apply at a stale version err = %v, want PlanVersionError at 1", err)
	}
	if p := planOf(t, w); p.Version != 1 || p.TargetTrain.Number != 22 {
		t.Fatalf("plan = %+v; a refused apply must write nothing", p)
	}
}

func eventKinds(f rt.Fix) string {
	out := make([]string, len(f.Events))
	for i, e := range f.Events {
		out[i] = string(e.Kind)
	}
	return strings.Join(out, " ")
}

func TestSupersededSweepSkipsHitsStillThereAndUnknownRules(t *testing.T) {
	w := newFixWorld(t)
	w.seedShipped()
	ctx := context.Background()
	plan, _ := w.svc.PlanFix(ctx, e1Hit, "")
	f, _ := w.svc.ConfirmFix(ctx, e1Hit, "", plan.Fingerprint, "cli")
	if err := w.svc.SupersedeFixes(ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ := w.store.GetReleaseFix(ctx, f.ID); got.State != rt.FixDryRun {
		t.Fatalf("state = %s while the hit is still there", got.State)
	}

	w.writeJira(rt.JiraVersionFact{ID: "5001", Name: "iOS 3.4.0", Description: "Release Train #20/2026", Released: true,
		URL: "https://jira.example.com/versions/5001", Approvers: []rt.Approver{}})
	if err := w.svc.SupersedeFixes(ctx); err != nil {
		t.Fatal(err)
	}
	got, _ := w.store.GetReleaseFix(ctx, f.ID)
	if got.State != rt.FixSuperseded || got.Events[len(got.Events)-1].Actor != "poller" {
		t.Fatalf("fix = %+v; want superseded by the poller once someone released it by hand", got)
	}
}

func TestSupersededSweepWaitsOnAColdStart(t *testing.T) {
	w := newFixWorld(t)
	w.seedShipped()
	ctx := context.Background()
	plan, _ := w.svc.PlanFix(ctx, e1Hit, "")
	f, _ := w.svc.ConfirmFix(ctx, e1Hit, "", plan.Fingerprint, "cli")
	if _, err := w.store.DeleteUnplannedReleaseResources(ctx, w.now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := w.svc.SupersedeFixes(ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ := w.store.GetReleaseFix(ctx, f.ID); got.State != rt.FixDryRun {
		t.Fatalf("state = %s after a cold start read nothing; an unknown rule must not supersede", got.State)
	}
}
