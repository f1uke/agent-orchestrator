package releasetrain

import (
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func planHit(t *testing.T, b Board, rule RuleID, choice string) FixPlan {
	t.Helper()
	hits := firedHits(b, rule)
	if len(hits) != 1 {
		t.Fatalf("%s hits = %v, want exactly one", rule, firedIDs(b, rule))
	}
	p, err := PlanFix(b, hits[0].ID, choice)
	if err != nil {
		t.Fatalf("PlanFix(%s): %v", hits[0].ID, err)
	}
	return p
}

func wantRequests(t *testing.T, p FixPlan, want ...string) {
	t.Helper()
	got := make([]string, len(p.Steps))
	for i, s := range p.Steps {
		got[i] = s.Request.Method + " " + s.Request.URL + " " + string(s.Request.Body)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s requests =\n  %s\nwant\n  %s", p.Hit, strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
}

func wantMode(t *testing.T, p FixPlan, mode FixMode) {
	t.Helper()
	if p.Mode != mode {
		t.Fatalf("%s mode = %s (%s), want %s", p.Hit, p.Mode, p.Note, mode)
	}
}

func TestPlanFixE1MarksTheJiraReleaseReleasedOnTheMilestoneDue(t *testing.T) {
	f := newFixture(t, today)
	v := f.published("di", "20", "3.4.0", "2026-10-01", "2026-10-08")
	v.jira.Released = false
	p := planHit(t, f.reconcile(), "E1", "")
	wantMode(t, p, ModeQueue)
	wantRequests(t, p, "PUT https://jira.example.com/rest/api/3/version/"+v.jira.ID+` {"released":true}`)
	if p.Note != "" {
		t.Fatalf("note = %q, want none while the dates agree", p.Note)
	}

	v.jira.ReleaseDate = dayPtr(t, "2026-10-10")
	p = planHit(t, f.reconcile(), "E1", "")
	wantRequests(t, p, "PUT https://jira.example.com/rest/api/3/version/"+v.jira.ID+` {"released":true,"releaseDate":"2026-10-08"}`)
	if !strings.Contains(p.Note, "2026-10-10") {
		t.Fatalf("note = %q, want it to name the differing Jira date", p.Note)
	}
}

func TestPlanFixE1OnAHotfix(t *testing.T) {
	f := newFixture(t, today)
	f.milestone("di", "Hotfix 3.4.1", "crash", "2026-10-10", "2026-10-13")
	j := f.jiraVersion("di", "3.4.1", "", "2026-10-13")
	p := planHit(t, f.reconcile(), "E1", "")
	wantRequests(t, p, "PUT https://jira.example.com/rest/api/3/version/"+j.ID+` {"released":true}`)
}

func TestPlanFixA5OffersBothSidesAndPicksNeither(t *testing.T) {
	f := newFixture(t, today)
	v := f.version("di", "21", "3.5.0", liveFreeze, liveDue)
	v.jira.ReleaseDate = dayPtr(t, "2026-10-17")
	b := f.reconcile()

	p := planHit(t, b, "A5", "")
	wantMode(t, p, ModeChoose)
	if len(p.Steps) != 0 || p.Recordable() || len(p.Choices) != 2 {
		t.Fatalf("A5 without a choice = %+v; want two choices, no steps, nothing to record", p)
	}
	jira := planHit(t, b, "A5", "jira")
	wantRequests(t, jira, "PUT https://jira.example.com/rest/api/3/version/"+v.jira.ID+` {"releaseDate":"2026-10-16"}`)
	gitlab := planHit(t, b, "A5", "gitlab")
	wantRequests(t, gitlab, "PUT https://gitlab.example.com/api/v4/projects/1001/milestones/"+strconv.Itoa(v.ms.ID)+` {"due_date":"2026-10-17"}`)
	if !jira.Recordable() || jira.Fingerprint == gitlab.Fingerprint || jira.Choice != "jira" {
		t.Fatalf("choices must be recordable with their own fingerprints: %+v / %+v", jira, gitlab)
	}
	if _, err := PlanFix(b, jira.Hit, "both"); !errors.Is(err, ErrUnknownChoice) {
		t.Fatalf("unknown choice err = %v, want ErrUnknownChoice", err)
	}
}

func TestPlanFixC1RetriesEachRequiredFailedJobByID(t *testing.T) {
	f := newFixture(t, today)
	v := f.version("di", "21", "3.5.0", liveFreeze, liveDue)
	v.releaseMR(liveFreeze, MROpened)
	v.detail().Pipeline.Status = "failed"
	v.jobList().Jobs = []JobFact{
		{ID: 901, Name: "build_test", Status: "failed"},
		{ID: 902, Name: "unit", Status: "failed"},
		{ID: 903, Name: "lint_report", Status: "failed", AllowFailure: true},
		{ID: 904, Name: "semgrep-sast", Status: "success"},
	}
	p := planHit(t, f.reconcile(), "C1", "")
	wantMode(t, p, ModeQueue)
	wantRequests(t, p,
		"POST https://gitlab.example.com/api/v4/projects/1001/jobs/901/retry ",
		"POST https://gitlab.example.com/api/v4/projects/1001/jobs/902/retry ")
}

func TestPlanFixC1IsBlockedUntilTheJobIDsAreRead(t *testing.T) {
	f := newFixture(t, today)
	v := f.version("di", "21", "3.5.0", liveFreeze, liveDue)
	v.releaseMR(liveFreeze, MROpened)
	v.jobList().Jobs = []JobFact{{Name: "build_test", Status: "failed"}}
	wantMode(t, planHit(t, f.reconcile(), "C1", ""), ModeBlocked)
}

func TestPlanFixC6RetriesTheBuildThatSkippedSemgrep(t *testing.T) {
	f := newFixture(t, today)
	v := f.version("di", "21", "3.5.0", liveFreeze, liveDue)
	v.releaseMR(liveFreeze, MROpened)
	v.jobList().Jobs = []JobFact{{ID: 901, Name: "build_test", Status: "failed"}, {ID: 904, Name: "semgrep-sast", Status: "skipped"}}
	p := planHit(t, f.reconcile(), "C6", "")
	wantRequests(t, p, "POST https://gitlab.example.com/api/v4/projects/1001/jobs/901/retry ")
	if !strings.Contains(p.Note, "skipped") {
		t.Fatalf("C6 note = %q, want it to explain the skip", p.Note)
	}

	v.jobList().Jobs = []JobFact{{ID: 905, Name: "semgrep-sast", Status: "failed"}}
	wantRequests(t, planHit(t, f.reconcile(), "C6", ""), "POST https://gitlab.example.com/api/v4/projects/1001/jobs/905/retry ")
}

func TestPlanFixD1CopiesThePreviousReleasesApprovers(t *testing.T) {
	f := newFixture(t, today)
	f.published("di", "20", "3.4.0", "2026-10-01", "2026-10-08")
	v := f.version("di", "21", "3.5.0", liveFreeze, liveDue)
	p := planHit(t, f.reconcile(), "D1", "")
	wantRequests(t, p, "PUT https://jira.example.com/rest/api/3/version/"+v.jira.ID+` {"approvers":[{"accountId":"acc-Ann"},{"accountId":"acc-Bob"}]}`)
	if p.Steps[0].After != "Ann, Bob (copied from #20)" {
		t.Fatalf("D1 after = %q", p.Steps[0].After)
	}

	g := newFixture(t, today)
	g.version("di", "21", "3.5.0", liveFreeze, liveDue)
	wantMode(t, planHit(t, g.reconcile(), "D1", ""), ModeBlocked)
}

func TestPlanFixD2AttachesEachMissingCategory(t *testing.T) {
	f := newFixture(t, today)
	v := f.version("di", "21", "3.5.0", liveFreeze, liveDue)
	v.relatedWork()
	base := "POST https://jira.example.com/rest/api/3/version/" + v.jira.ID + "/relatedwork "
	wantRequests(t, planHit(t, f.reconcile(), "D2", ""),
		base+`{"category":"Development","title":"Source code scan 3.5.0"}`,
		base+`{"category":"Communication","title":"Release note 3.5.0"}`)
}

func TestPlanFixG1MovesTheCardToReadyForQA(t *testing.T) {
	f := newFixture(t, today)
	f.sprintCard(demoSprint(f), "DEMO-1", "Pull Request", true)
	f.mr("di", "DEMO-1 a", "feat/DEMO-1", "feature/sprint-7-2026", MRMerged, "2026-10-07", "2026-10-08")
	p := planHit(t, f.reconcile(), "G1", "")
	wantRequests(t, p, `POST https://jira.example.com/rest/api/3/issue/DEMO-1/transitions {"transition":{"name":"Ready for QA"}}`)
	if p.Steps[0].Before != "Pull Request" || p.Steps[0].After != "Ready for QA" {
		t.Fatalf("G1 step = %+v", p.Steps[0])
	}
}

func TestPlanFixG5CopiesTheMissingKeysAndRecordsNothing(t *testing.T) {
	f := newFixture(t, today)
	page := pagedTrain(f, "2026-10-19")
	b := f.reconcile()
	p, err := PlanFix(b, "G5~pf/21-2026/demo/merged", "")
	if err != nil {
		t.Fatal(err)
	}
	wantMode(t, p, ModeCopy)
	wantRequests(t, p, "COPY "+page.URL+` "DEMO-1"`)
	if p.Recordable() {
		t.Fatal("a copy plan has nothing to record")
	}
}

func llbFixture(t *testing.T, plan *LlbPlan) (*fixture, *MRFact) {
	f := newFixture(t, today)
	f.mr("di", "DEMO-50 other", "feat/DEMO-50", "feature/sprint-2026-20", MRMerged, "2026-10-01", "2026-10-01")
	f.mr("di", "DEMO-1 wallet", "feat/DEMO-1", "feature/demo-wallet", MRMerged, "2026-09-01", "2026-09-02")
	mr := f.mr("di", "wallet", "feature/demo-wallet", "develop", MROpened, "2026-10-12", "")
	if plan != nil {
		f.human.Plans = []LlbPlan{*plan}
	}
	return f, mr
}

func TestPlanFixL1IsBlockedUntilTheGroupHasATarget(t *testing.T) {
	f, _ := llbFixture(t, nil)
	p := planHit(t, f.reconcile(), "L1", "")
	wantMode(t, p, ModeBlocked)
	if p.Need == nil || p.Need.LLB != "demo-wallet" {
		t.Fatalf("L1 need = %+v, want the demo-wallet sheet", p.Need)
	}
}

func TestPlanFixL1RetargetsToTheObservedSprintBranchOfTheTarget(t *testing.T) {
	f, mr := llbFixture(t, &LlbPlan{ID: "demo-wallet", Name: "Wallet", TargetKind: TargetTrain, TargetTrain: &TrainID{Number: 21, Year: fixtureYear}, Version: 1})
	p := planHit(t, f.reconcile(), "L1", "")
	wantMode(t, p, ModeQueue)
	wantRequests(t, p, "PUT https://gitlab.example.com/api/v4/projects/1001/merge_requests/"+strconv.Itoa(mr.IID)+` {"target_branch":"feature/sprint-2026-20"}`)

	g, _ := llbFixture(t, &LlbPlan{ID: "demo-wallet", Name: "Wallet", TargetKind: TargetTrain, TargetTrain: &TrainID{Number: 23, Year: fixtureYear}, Version: 1})
	p = planHit(t, g.reconcile(), "L1", "")
	wantMode(t, p, ModeBlocked)
	if !strings.Contains(p.Note, "never guessed") {
		t.Fatalf("L1 for an unobserved branch = %q, want it to refuse to guess", p.Note)
	}
}

func TestPlanFixL8RetargetsToTheOwningBoardsSprintBranch(t *testing.T) {
	f := newFixture(t, today)
	f.mr("di", "DEMO-50 other", "feat/DEMO-50", "feature/sprint-2026-7", MRMerged, "2026-09-01", "2026-09-01")
	f.sprintCard(demoSprint(f), "DEMO-1", "In Progress", true)
	mr := f.mr("di", "DEMO-1 a", "feat/DEMO-1", "develop", MROpened, "2026-10-07", "")
	p := planHit(t, f.reconcile(), "L8", "")
	wantRequests(t, p, "PUT https://gitlab.example.com/api/v4/projects/1001/merge_requests/"+strconv.Itoa(mr.IID)+` {"target_branch":"feature/sprint-2026-7"}`)

	mr.State, mr.MergedOn = MRMerged, dayPtr(t, "2026-10-08")
	p = planHit(t, f.reconcile(), "L8", "")
	wantMode(t, p, ModeBlocked)
	if !strings.Contains(p.Note, "already merged") {
		t.Fatalf("merged L8 note = %q", p.Note)
	}
}

func l3Fixture(t *testing.T, plan *LlbPlan) *fixture {
	f := newFixture(t, today)
	v := f.version("di", "21", "3.5.0", liveFreeze, liveDue)
	v.releaseMR(liveFreeze, MROpened)
	f.version("di", "22", "3.6.0", "2026-10-20", "2026-10-27")
	f.card(f.page("demo", "21", "2026-09-20", liveFreeze, liveDue), "DEMO-7")
	f.mr("di", "DEMO-7 wallet", "feat/DEMO-7", "feature/demo-wallet", MRMerged, "2026-09-01", "2026-09-02")
	if plan != nil {
		f.human.Plans = []LlbPlan{*plan}
	}
	return f
}

func TestPlanFixL3AppliesToTheLongLivedPlan(t *testing.T) {
	f := l3Fixture(t, &LlbPlan{ID: "demo-wallet", Name: "Wallet", TargetKind: TargetTrain, TargetTrain: &TrainID{Number: 22, Year: fixtureYear}, Version: 4,
		Branches: []PlanBranch{{App: "di", Branch: "feature/demo-wallet"}}})
	p := planHit(t, f.reconcile(), "L3", "")
	wantMode(t, p, ModeApply)
	wantRequests(t, p, `PUT /api/v1/release-trains/llb-plans/demo-wallet {"targetKind":"train","targetTrain":"21-2026","ifVersion":4}`)
	e := p.Steps[0].Effect.(EffAOPlanTarget)
	if e.Before.Kind != TargetTrain || *e.Before.Train != (TrainID{Number: 22, Year: fixtureYear}) || e.PlanVersion != 4 {
		t.Fatalf("L3 effect = %+v; want before #22 at version 4", e)
	}
}

func TestPlanFixL3IsBlockedWithoutAPlan(t *testing.T) {
	p := planHit(t, l3Fixture(t, nil).reconcile(), "L3", "")
	wantMode(t, p, ModeBlocked)
	if p.Need == nil || p.Need.LLB != "demo-wallet" || p.Recordable() {
		t.Fatalf("L3 without a plan = %+v; want blocked on the demo-wallet sheet", p)
	}
}

func TestFixFingerprintIsStableAndFollowsBeforeValues(t *testing.T) {
	plan := &LlbPlan{ID: "demo-wallet", Name: "Wallet", TargetKind: TargetUnplanned, Version: 1}
	f := l3Fixture(t, plan)
	a, b := planHit(t, f.reconcile(), "L3", ""), planHit(t, f.reconcile(), "L3", "")
	if a.Fingerprint == "" || a.Fingerprint != b.Fingerprint {
		t.Fatalf("fingerprints %q and %q differ for the same Board", a.Fingerprint, b.Fingerprint)
	}
	f.human.Plans[0].Version = 2
	if c := planHit(t, f.reconcile(), "L3", ""); c.Fingerprint == a.Fingerprint {
		t.Fatal("the fingerprint ignores the plan version the write is guarded by")
	}
	f.human.Plans[0].Version, f.human.Plans[0].TargetKind = 1, TargetDropped
	if c := planHit(t, f.reconcile(), "L3", ""); c.Fingerprint == a.Fingerprint {
		t.Fatal("the fingerprint ignores the Before value")
	}
}

func TestFixTransitionTable(t *testing.T) {
	allowed := map[[2]string]string{
		{"", "record_dry_run"}:     "dry_run",
		{"", "start_apply"}:        "applying",
		{"applying", "apply_ok"}:   "applied",
		{"applying", "apply_fail"}: "failed",
		{"applied", "undo_ok"}:     "undone",
		{"applied", "undo_fail"}:   "applied",
		{"dry_run", "withdraw"}:    "withdrawn",
		{"dry_run", "supersede"}:   "superseded",
	}
	states := []FixState{"", FixDryRun, FixApplying, FixApplied, FixFailed, FixUndone, FixWithdrawn, FixSuperseded}
	acts := []FixAction{ActRecordDryRun, ActStartApply, ActApplyOK, ActApplyFail, ActUndoOK, ActUndoFail, ActWithdraw, ActSupersede}
	for _, s := range states {
		for _, a := range acts {
			to, ev, err := Transition(s, a)
			want, ok := allowed[[2]string{string(s), string(a)}]
			switch {
			case ok && (err != nil || string(to) != want || ev == ""):
				t.Errorf("%q --%s--> %q %q %v, want %q", s, a, to, ev, err, want)
			case !ok && err == nil:
				t.Errorf("%q --%s--> %q is allowed; the table refuses it", s, a, to)
			}
		}
	}
	if _, ev, _ := Transition(FixApplied, ActUndoFail); ev != EventUndoFailed {
		t.Errorf("a failed undo appends %q, want undo_failed", ev)
	}
}

func TestCatalogueFixKindsAgreeWithPlanFix(t *testing.T) {
	want := []string{"A5:choose", "C1:queue", "C6:queue", "D1:queue", "D2:queue", "E1:queue", "G1:queue", "G5:copy", "L1:queue", "L3:apply", "L8:queue"}
	f := everyRuleFires(t)
	for _, r := range ruleTable() {
		f.human.RuleSettings[r.ID] = true
	}
	b := f.reconcile()
	var got []string
	for _, r := range b.Catalogue {
		if r.Fix != FixNone {
			got = append(got, string(r.ID)+":"+string(r.Fix))
		}
	}
	sort.Strings(got)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("catalogue fix kinds = %v, want %v", got, want)
	}
	modes := map[FixKind][]FixMode{FixApply: {ModeApply, ModeBlocked}, FixQueue: {ModeQueue, ModeBlocked}, FixChoose: {ModeChoose}, FixCopy: {ModeCopy}}
	for _, h := range b.Alerts.Hits {
		p, err := PlanFix(b, h.ID, "")
		if h.Fix == FixNone {
			if !errors.Is(err, ErrNotFixable) {
				t.Errorf("%s: PlanFix err = %v, want ErrNotFixable", h.ID, err)
			}
			continue
		}
		if err != nil || !containsMode(modes[h.Fix], p.Mode) {
			t.Errorf("%s (%s): plan mode %s, err %v", h.ID, h.Fix, p.Mode, err)
		}
	}
	if _, err := PlanFix(b, "E1~v/99-2026/di", ""); !errors.Is(err, ErrNoSuchHit) {
		t.Fatalf("missing hit err = %v", err)
	}
}

func containsMode(list []FixMode, m FixMode) bool {
	for _, x := range list {
		if x == m {
			return true
		}
	}
	return false
}

func TestFixPlanRoundTripsThroughJSON(t *testing.T) {
	f := newFixture(t, today)
	v := f.version("di", "21", "3.5.0", liveFreeze, liveDue)
	v.jira.ReleaseDate = dayPtr(t, "2026-10-17")
	p := planHit(t, f.reconcile(), "A5", "gitlab")
	body, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var back FixPlan
	if err := json.Unmarshal(body, &back); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(back.Steps, p.Steps) || back.Fingerprint != fingerprint(back.Hit, back.Choice, back.Steps) {
		t.Fatalf("round trip lost the steps or the fingerprint: %s", body)
	}
}

func TestBoardHitsCarryTheirFixKindAndOpenFix(t *testing.T) {
	f := newFixture(t, today)
	v := f.published("di", "20", "3.4.0", "2026-10-01", "2026-10-08")
	v.jira.Released = false
	id := HitID("E1~v/20-2026/di")
	f.human.OpenFixes = map[HitID]FixRef{id: {ID: "fx-1", State: FixDryRun}}
	for _, h := range f.reconcile().Alerts.Hits {
		if h.ID == id {
			if h.Fix != FixQueue || h.OpenFix == nil || *h.OpenFix != (FixRef{ID: "fx-1", State: FixDryRun}) {
				t.Fatalf("E1 hit fix = %q, open %+v", h.Fix, h.OpenFix)
			}
			return
		}
	}
	t.Fatalf("no %s on the Board", id)
}
