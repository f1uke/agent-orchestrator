package releasetrain

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	rt "github.com/aoagents/agent-orchestrator/backend/internal/releasetrain"
)

const b1Hit = rt.HitID("B1~v/21-2026/da")

func (w *boardWorld) seedLateAndroid() {
	t := w.t
	start, due := rt.Day{Y: 2026, M: 10, D: 12}, rt.Day{Y: 2026, M: 10, D: 19}
	w.write(rt.KindMilestones.Name, rt.ProjectScope(1002), item(t, rt.KindMilestones, rt.MilestoneFact{
		Project: 1002, ID: 21, Title: "Release 3.5.0", Description: "Release Train #21/2026", State: "active", Start: &start, Due: &due}))
	w.write(rt.KindMRs.Name, rt.ProjectScope(1002))
}

type boardProbe struct {
	Board struct {
		Catalogue []rt.RuleInfo `json:"catalogue"`
		Alerts    struct {
			Hits []struct {
				ID                 rt.HitID `json:"id"`
				Bucket             string   `json:"bucket"`
				ChangedSinceHidden bool     `json:"changedSinceHidden"`
			} `json:"hits"`
			StaleHides []rt.StaleHide `json:"staleHides"`
		} `json:"alerts"`
	} `json:"board"`
}

func probe(t *testing.T, body []byte) boardProbe {
	t.Helper()
	var p boardProbe
	if err := json.Unmarshal(body, &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func (p boardProbe) bucket(id rt.HitID) string {
	for _, h := range p.Board.Alerts.Hits {
		if h.ID == id {
			return h.Bucket
		}
	}
	return ""
}

func (p boardProbe) rule(id rt.RuleID) rt.RuleInfo {
	for _, r := range p.Board.Catalogue {
		if r.ID == id {
			return r
		}
	}
	return rt.RuleInfo{}
}

func TestARuleToggleChangesTheETagAndTheNextBoard(t *testing.T) {
	w := newBoardWorld(t)
	w.seed()
	ctx := context.Background()
	body, etag := w.mustBoard()
	if probe(t, body).rule("C2").On {
		t.Fatalf("C2 is on before any toggle")
	}

	on := true
	info, err := w.svc.SetRule(ctx, "C2", &on)
	if err != nil || !info.On || info.DefaultOn {
		t.Fatalf("SetRule C2 on = %+v, %v", info, err)
	}
	body, after := w.mustBoard()
	if after == etag || !probe(t, body).rule("C2").On {
		t.Fatalf("after enabling C2: etag same=%v catalogue C2 on=%v", after == etag, probe(t, body).rule("C2").On)
	}

	info, err = w.svc.SetRule(ctx, "C2", nil)
	if err != nil || info.On {
		t.Fatalf("reset C2 = %+v, %v; want back to its default off", info, err)
	}
	if _, reset := w.mustBoard(); reset == after {
		t.Fatalf("reset kept the ETag")
	}

	if _, err := w.svc.SetRule(ctx, "Z9", &on); !errors.Is(err, ErrUnknownRule) {
		t.Fatalf("SetRule Z9 = %v, want ErrUnknownRule", err)
	}
}

func TestAHideChangesTheETagAndTheNextBoardHidesTheHit(t *testing.T) {
	w := newBoardWorld(t)
	w.seed()
	w.seedLateAndroid()
	ctx := context.Background()
	body, etag := w.mustBoard()
	if got := probe(t, body).bucket(b1Hit); got != "badge" {
		t.Fatalf("B1 for da before hiding = %q, want badge", got)
	}

	hide, err := w.svc.Hide(ctx, b1Hit, "known, release MR tomorrow")
	if err != nil || hide.Severity != rt.SevRisk || hide.Fingerprint == "" {
		t.Fatalf("Hide = %+v, %v", hide, err)
	}
	body, after := w.mustBoard()
	if after == etag || probe(t, body).bucket(b1Hit) != "hidden" {
		t.Fatalf("after hiding: etag same=%v bucket=%q", after == etag, probe(t, body).bucket(b1Hit))
	}

	if _, err := w.svc.Hide(ctx, "B1~v/99-2026/da", ""); !errors.Is(err, ErrUnknownHit) {
		t.Fatalf("Hide of a hit not on the board = %v, want ErrUnknownHit", err)
	}

	if err := w.svc.Unhide(ctx, b1Hit); err != nil {
		t.Fatal(err)
	}
	if body, _ := w.mustBoard(); probe(t, body).bucket(b1Hit) != "badge" {
		t.Fatalf("after unhide B1 is not back on the badge")
	}
	if err := w.svc.Unhide(ctx, b1Hit); !errors.Is(err, ErrUnknownHit) {
		t.Fatalf("second unhide = %v, want ErrUnknownHit", err)
	}
}

func TestUnhideStaleClearsOnlyHidesWhoseHitIsGone(t *testing.T) {
	w := newBoardWorld(t)
	w.seed()
	w.seedLateAndroid()
	ctx := context.Background()
	w.mustBoard()
	if _, err := w.svc.Hide(ctx, b1Hit, ""); err != nil {
		t.Fatal(err)
	}
	if err := w.store.HideReleaseHit(ctx, "C5~v/21-2026/di", rt.Hide{Fingerprint: "old", Severity: rt.SevBlock, HiddenAt: w.now}); err != nil {
		t.Fatal(err)
	}
	body, _ := w.mustBoard()
	stale := probe(t, body).Board.Alerts.StaleHides
	if len(stale) != 1 || stale[0].ID != "C5~v/21-2026/di" {
		t.Fatalf("stale hides = %+v, want only the C5 hide whose hit is gone", stale)
	}

	removed, err := w.svc.UnhideStale(ctx)
	if err != nil || len(removed) != 1 || removed[0] != "C5~v/21-2026/di" {
		t.Fatalf("UnhideStale = %v, %v", removed, err)
	}
	human, _ := w.store.LoadReleaseHumanState(ctx)
	if _, ok := human.Hides[b1Hit]; !ok || len(human.Hides) != 1 {
		t.Fatalf("hides after UnhideStale = %v, want the live B1 hide kept", human.Hides)
	}
}

func TestPutPlanStampsTheTimeAndValidates(t *testing.T) {
	w := newBoardWorld(t)
	ctx := context.Background()
	train := rt.TrainID{Number: 22, Year: 2026}
	p := rt.LlbPlan{ID: "demo-llb", Name: "Demo", TargetKind: rt.TargetTrain, TargetTrain: &train, UpdatedBy: "cli",
		Branches: []rt.PlanBranch{{App: "di", Branch: "feature/demo-llb"}}}
	got, err := w.svc.PutPlan(ctx, p, 0)
	if err != nil || got.Version != 1 || !got.UpdatedAt.Equal(w.now) {
		t.Fatalf("PutPlan = %+v, %v", got, err)
	}
	p.TargetTrain = nil
	if _, err := w.svc.PutPlan(ctx, p, 1); !errors.Is(err, rt.ErrInvalidPlan) {
		t.Fatalf("train target without a train = %v, want ErrInvalidPlan", err)
	}
	plans, err := w.svc.Plans(ctx)
	if err != nil || len(plans) != 1 || plans[0].Version != 1 {
		t.Fatalf("Plans = %+v, %v", plans, err)
	}
}

const g2Hit = rt.HitID("G2~c/DEMO-7")

func (w *boardWorld) seedConfigCard() {
	t := w.t
	start, end := rt.Day{Y: 2026, M: 10, D: 6}, rt.Day{Y: 2026, M: 10, D: 16}
	w.write(rt.KindBoardSprints.Name, rt.BoardScope(42), item(t, rt.KindBoardSprints, rt.SprintFact{
		Board: 42, ID: 7021, Name: "DEMO 2026-21", Start: &start, End: &end}))
	w.write(rt.KindSprintCards.Name, rt.SprintScope(7021), item(t, rt.KindSprintCards, rt.IssueFact{
		Key: "DEMO-7", Summary: "Config flag", Status: "Ready for UAT", StatusCategory: "indeterminate"}))
	w.write(rt.KindThisSprint.Name, rt.SprintScope(7021), item(t, rt.KindThisSprint, rt.SprintMemberFact{Key: "DEMO-7"}))
	w.write(rt.KindBoardSprints.Name, rt.BoardScope(43))
	w.write(rt.KindBranchCards.Name, rt.BranchCardsScope)
	w.write(rt.KindMRs.Name, rt.ProjectScope(1002))
}

func TestMarkNoMRChangesTheETagAndTakesG2OffTheBoardUntilUnmarked(t *testing.T) {
	w := newBoardWorld(t)
	w.seed()
	w.seedConfigCard()
	ctx := context.Background()
	body, etag := w.mustBoard()
	if got := probe(t, body).bucket(g2Hit); got != "badge" {
		t.Fatalf("G2 for DEMO-7 before marking = %q, want badge", got)
	}

	mark, err := w.svc.MarkNoMR(ctx, "DEMO-7", "config only", "app")
	if err != nil || mark.MarkedBy != "app" || mark.Note != "config only" || !mark.MarkedAt.Equal(w.now) {
		t.Fatalf("MarkNoMR = %+v, %v", mark, err)
	}
	body, marked := w.mustBoard()
	if marked == etag || probe(t, body).bucket(g2Hit) != "" {
		t.Fatalf("after marking: etag same=%v G2 bucket=%q, want a new ETag and no G2", marked == etag, probe(t, body).bucket(g2Hit))
	}

	if _, err := w.svc.MarkNoMR(ctx, "NOPE-1", "", "cli"); !errors.Is(err, ErrUnknownCard) {
		t.Fatalf("MarkNoMR of a card not on the board = %v, want ErrUnknownCard", err)
	}

	if err := w.svc.UnmarkNoMR(ctx, "DEMO-7"); err != nil {
		t.Fatal(err)
	}
	body, unmarked := w.mustBoard()
	if unmarked == marked || probe(t, body).bucket(g2Hit) != "badge" {
		t.Fatalf("after unmarking: etag same=%v G2 bucket=%q, want G2 back", unmarked == marked, probe(t, body).bucket(g2Hit))
	}
	if err := w.svc.UnmarkNoMR(ctx, "DEMO-7"); !errors.Is(err, ErrUnknownCard) {
		t.Fatalf("second unmark = %v, want ErrUnknownCard", err)
	}
	marks, err := w.svc.NoMRMarks(ctx)
	if err != nil || len(marks) != 0 {
		t.Fatalf("NoMRMarks = %+v, %v; want none", marks, err)
	}
}
