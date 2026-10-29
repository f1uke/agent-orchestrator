package releasetrain

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

type copyCase struct {
	rule  RuleID
	today string
	setup func(f *fixture)
}

var chipRules = map[RuleID]DueEvent{
	"B1": DueFreeze, "B3": DuePublish, "C1": DuePublish, "C5": DuePublish, "C6": DuePublish,
	"D1": DuePublish, "D2": DuePublish, "D3": DuePublish,
	"G2": DueSprintEnd, "G5": DueFreeze, "L3": DueFreeze, "L10": DueFreeze, "L11": DueFreeze, "L12": DueFreeze, "L13": DueFreeze,
}

func copyCases() []copyCase {
	var out []copyCase
	for _, tc := range ruleCases() {
		if tc.want != nil {
			out = append(out, copyCase{tc.rule, tc.today, tc.setup})
		}
	}
	return append(out,
		copyCase{"A6", today, func(f *fixture) {
			healthy(f)
			f.page("demo", "21", "2026-09-20", "2026-10-08", liveDue)
		}},
		copyCase{"G1", today, func(f *fixture) {
			f.sprintCard(demoSprint(f), "DEMO-1", "In Progress", true)
			f.mr("di", "DEMO-1 a", "feat/DEMO-1", "feature/sprint-7-2026", MRMerged, "2026-10-07", "2026-10-08")
		}},
		copyCase{"G2", "2026-10-15", func(f *fixture) { f.sprintCard(demoSprint(f), "DEMO-1", "In Progress", true) }},
		copyCase{"G3", today, routedToNext},
		copyCase{"G4", today, func(f *fixture) {
			f.sprintCard(demoSprint(f), "DEMO-1", "In Progress", true)
			f.mr("di", "DEMO-1 a", "feat/DEMO-1", "feature/sprint-7-2026", MRMerged, "2026-10-07", "2026-10-08")
			f.mr("da", "DEMO-1 b", "feat/DEMO-1", "develop", MROpened, "2026-10-07", "")
		}},
		copyCase{"G5", today, func(f *fixture) { pagedTrain(f, "2026-10-19") }},
		copyCase{"G6", today, func(f *fixture) {
			f.sprintCard(f.sprint(42, 7, "2026-10-06", "2026-10-20"), "DEMO-1", "In Progress", true)
			f.mr("di", "OLD-1 coupon", "feat/OLD-1", "feature/sprint-2026-7", MROpened, "2026-10-07", "")
			f.branchIss = append(f.branchIss, movedTo("OLD-1", "NEW-9"))
		}},
		copyCase{"G9", today, func(f *fixture) {
			v := f.version("di", "22", "3.6.0", "2026-10-30", "2026-11-06")
			v.cardMR("DEMO-31 limit", "feat/DEMO-31", "feature/sprint-2026-21", MRMerged, "2026-10-12", "2026-10-13")
		}},
		copyCase{"G9", today, func(f *fixture) {
			v := f.version("di", "22", "3.6.0", "2026-10-19", "2026-11-06")
			v.cardMR("DEMO-31 limit", "feat/DEMO-31", "feature/sprint-2026-21", MROpened, "2026-10-12", "")
		}},
		copyCase{"L1", today, func(f *fixture) {
			f.mr("di", "DEMO-50 other", "feat/DEMO-50", "feature/sprint-20-2026", MRMerged, "2026-10-01", "2026-10-01")
			f.mr("di", "DEMO-1 wallet", "feat/DEMO-1", "feature/demo-wallet", MRMerged, "2026-09-01", "2026-09-02")
			f.mr("di", "wallet", "feature/demo-wallet", "develop", MROpened, "2026-10-12", "")
		}},
		copyCase{"L3", today, func(f *fixture) {
			v := f.version("di", "21", "3.5.0", liveFreeze, liveDue)
			v.releaseMR(liveFreeze, MROpened)
			f.version("di", "22", "3.6.0", "2026-10-20", "2026-10-27")
			f.card(f.page("demo", "21", "2026-09-20", liveFreeze, liveDue), "DEMO-7")
			f.mr("di", "DEMO-7 wallet", "feat/DEMO-7", "feature/demo-wallet", MRMerged, "2026-09-01", "2026-09-02")
			f.human.Plans = []LlbPlan{{ID: "demo-wallet", Name: "Wallet", TargetKind: TargetUnplanned}}
		}},
		copyCase{"L8", today, func(f *fixture) {
			f.sprintCard(demoSprint(f), "DEMO-1", "In Progress", true)
			f.mr("da", "DEMO-1 b", "feat/DEMO-1", "develop", MROpened, "2026-10-07", "")
		}},
		copyCase{"L8", today, func(f *fixture) {
			f.mr("di", "DEMO-50 other", "feat/DEMO-50", "feature/sprint-6-2026", MRMerged, "2026-09-01", "2026-09-01")
			f.sprintCard(demoSprint(f), "DEMO-1", "In Progress", true)
			f.mr("di", "DEMO-1 a", "feat/DEMO-1", "develop", MRMerged, "2026-10-07", "2026-10-08")
		}},
		copyCase{"L9", today, func(f *fixture) {
			f.mr("di", "DEMO-50 other", "feat/DEMO-50", "feature/sprint-20-2026", MRMerged, "2026-10-01", "2026-10-01")
			f.mr("di", "DEMO-2 part", "feat/DEMO-2", "feat/DEMO-1-base", MROpened, "2026-10-12", "")
		}},
		copyCase{"L10", targetToday, func(f *fixture) {
			fillWallet(f, targetFreeze, TargetTrain)
			sprintBranchWithProjectURL(f)
		}},
		copyCase{"L11", targetToday, func(f *fixture) { fillWallet(f, targetFreeze, TargetTrain) }},
		copyCase{"L12", "2026-10-27", func(f *fixture) {
			fillWallet(f, targetFreeze, TargetTrain)
			f.mr("di", "wallet", "feature/demo-wallet", "feature/sprint-21-2026", MROpened, "2026-10-22", "")
		}},
		copyCase{"L13", "2026-10-31", func(f *fixture) { fillWallet(f, targetFreeze, TargetTrain) }},
	)
}

const walletProject = "https://gitlab.example.com/demo/"

func sprintBranchWithProjectURL(f *fixture) {
	for _, app := range []AppID{"di", "da"} {
		mr := f.mr(app, "unrelated", "feat/other", "feature/sprint-21-2026", MRClosed, "2026-10-20", "")
		mr.URL = walletProject + string(app) + "/-/merge_requests/" + strconv.Itoa(mr.IID)
	}
}

var hashNumber = regexp.MustCompile(`#\d`)

func TestEveryRuleSaysWhatIsWrongAndWhatToDoInPlainWords(t *testing.T) {
	fired := map[RuleID]bool{}
	for _, tc := range copyCases() {
		t.Run(string(tc.rule), func(t *testing.T) {
			f := newFixture(t, tc.today)
			tc.setup(f)
			b := f.reconcile()
			hits := firedHits(b, tc.rule)
			if len(hits) == 0 {
				t.Fatalf("%s did not fire", tc.rule)
			}
			fired[tc.rule] = true
			for _, h := range hits {
				checkCopy(t, b, h)
				checkDue(t, b, h)
				checkPrimary(t, h)
				checkCards(t, h)
			}
		})
	}
	for _, r := range ruleTable() {
		if !fired[r.ID] {
			t.Errorf("no case fires %s, so its copy is unchecked", r.ID)
		}
	}
}

func checkCopy(t *testing.T, b Board, h Hit) {
	t.Helper()
	c := h.Copy
	if c.What == "" || c.Do == "" {
		t.Errorf("%s copy = %+v, want both lines", h.ID, c)
	}
	for _, line := range []string{c.What, c.Do} {
		if strings.Contains(line, "(s)") || hashNumber.MatchString(line) || strings.Contains(line, "MOBILITY") {
			t.Errorf("%s says %q, want no \"(s)\", no \"#22\" and no board key for a sprint", h.ID, line)
		}
	}
	if h.Train != nil && strings.HasPrefix(c.What, h.Train.String()) {
		t.Errorf("%s What %q opens with the train subject line", h.ID, c.What)
	}
	if h.App != "" && strings.HasPrefix(c.What, b.appName(h.App)+" "+"1") {
		t.Errorf("%s What %q opens with the app and version subject line", h.ID, c.What)
	}
	if first := strings.Fields(c.Do)[0]; strings.ToLower(first) == first && !slices.Contains([]string{"iOS"}, first) {
		t.Errorf("%s Do %q should start with a capital letter", h.ID, c.Do)
	}
}

func checkDue(t *testing.T, b Board, h Hit) {
	t.Helper()
	event, chip := chipRules[h.Rule]
	if h.Rule == "G9" {
		event, chip = DueFreeze, h.Severity == SevRisk
	}
	switch {
	case !chip && h.Due != nil:
		t.Errorf("%s has due %+v, but the rule has no chip", h.ID, h.Due)
	case chip && h.Due == nil:
		t.Errorf("%s has no due, but the rule counts down to its %s", h.ID, event)
	case chip:
		if h.Due.Event != event || h.Due.WorkDays != workDaysTo(b.Today, h.Due.On) {
			t.Errorf("%s due = %+v, want %s and %d working days to %s", h.ID, h.Due, event, workDaysTo(b.Today, h.Due.On), h.Due.On)
		}
		if h.Due.Missed != (h.Rule == "L13") {
			t.Errorf("%s due.missed = %v", h.ID, h.Due.Missed)
		}
	}
}

func checkPrimary(t *testing.T, h Hit) {
	t.Helper()
	if h.Primary == nil {
		if h.Rule != "F1" && slices.ContainsFunc(h.Links, func(l Link) bool { return l.URL != "" }) {
			t.Errorf("%s has no primary action", h.ID)
		}
		return
	}
	if h.Rule == "F1" {
		t.Errorf("F1 has primary %+v, want Show only", h.Primary)
	}
	if n := btoi(h.Primary.URL != "") + btoi(h.Primary.Fix) + btoi(h.Primary.Details); n != 1 {
		t.Errorf("%s primary %+v must do exactly one thing: open a URL, the Fix or the details", h.ID, h.Primary)
	}
	for _, l := range h.Links {
		if l.Label == h.Primary.Label || (h.Primary.URL != "" && l.URL == h.Primary.URL) {
			t.Errorf("%s repeats its primary %+v in links %+v", h.ID, h.Primary, h.Links)
		}
	}
}

func TestWorkDaysToIsSignedAndSkipsWeekends(t *testing.T) {
	for _, tc := range []struct {
		from, to string
		want     int
	}{
		{"2026-10-15", "2026-10-20", 3},
		{"2026-10-20", "2026-10-20", 0},
		{"2026-10-17", "2026-10-17", 0},
		{"2026-10-20", "2026-10-15", -3},
		{"2026-10-12", "2026-10-09", -1},
		{"2026-10-18", "2026-10-17", 0},
	} {
		if got := workDaysTo(day(t, tc.from), day(t, tc.to)); got != tc.want {
			t.Errorf("workDaysTo(%s, %s) = %d, want %d", tc.from, tc.to, got, tc.want)
		}
	}
}

func TestPluralAddsTheRightEnding(t *testing.T) {
	for _, tc := range []struct {
		n    int
		noun string
		want string
	}{
		{0, "card", "0 cards"},
		{1, "card", "1 card"},
		{2, "card", "2 cards"},
		{1, "branch", "1 branch"},
		{3, "branch", "3 branches"},
		{2, "approver", "2 approvers"},
		{2, "merged card", "2 merged cards"},
	} {
		if got := plural(tc.n, tc.noun); got != tc.want {
			t.Errorf("plural(%d, %q) = %q, want %q", tc.n, tc.noun, got, tc.want)
		}
	}
}

func TestTrainShortReadsIrregularTrainsAsTheyAreNumbered(t *testing.T) {
	for id, want := range map[TrainID]string{
		{Number: 22, Year: 2026}:           "22",
		{Number: 20, Extra: 1, Year: 2026}: "20-1",
	} {
		if got := id.short(); got != want {
			t.Errorf("%+v.short() = %q, want %q", id, got, want)
		}
	}
}

func TestSingularCardsReadAsOne(t *testing.T) {
	f := newFixture(t, today)
	healthy(f).issue("DEMO-3", "In Progress", "indeterminate")
	hits := firedHits(f.reconcile(), "B3")
	want := Copy{What: "1 card in train 21 is still in development", Do: "Finish it or take it out of the train before publish Fri 16 Oct."}
	if len(hits) != 1 || hits[0].Copy != want {
		t.Fatalf("B3 = %+v, want %+v", hits, want)
	}
}

func trunkMRInWallet(t *testing.T, accept bool) (*fixture, *MRFact) {
	f, _ := walletPlan(t, targetToday, targetFreeze, TargetTrain)
	f.mr("di", "DEMO-50 other", "feat/DEMO-50", "feature/sprint-20-2026", MRMerged, "2026-10-01", "2026-10-01")
	mr := f.mr("di", "wallet", "feature/demo-wallet", "develop", MROpened, "2026-10-12", "")
	if accept {
		f.human.Plans[0].Accepted = []PlanAccepted{{MRRef: "di!" + strconv.Itoa(mr.IID), Reason: "meant for develop"}}
	}
	return f, mr
}

func TestL10StaysQuietWhileL1FiresForTheSameBranchAndApp(t *testing.T) {
	f, mr := trunkMRInWallet(t, false)
	b := f.reconcile()

	l1 := firedHits(b, "L1")
	if len(l1) != 1 || l1[0].Subject.(SubjMR).IID != mr.IID {
		t.Fatalf("L1 = %v, want the develop MR", firedIDs(b, "L1"))
	}
	if want := "Retarget it to the sprint branch. Develop takes no feature work after the cutover. This also puts it in sprint 21."; l1[0].Copy.Do != want {
		t.Fatalf("L1 do = %q, want %q", l1[0].Copy.Do, want)
	}
	if got, want := firedIDs(b, "L10"), []string{"L10~llb/22-2026/da/feature/demo-wallet"}; !slices.Equal(got, want) {
		t.Fatalf("L10 = %v, want only the app with no MR into develop %v", got, want)
	}
}

func TestL10FiresAgainOnceL1HasNothingToSay(t *testing.T) {
	f, _ := trunkMRInWallet(t, true)
	b := f.reconcile()
	if got := firedIDs(b, "L1"); got != nil {
		t.Fatalf("L1 = %v, want none: the plan accepted the MR", got)
	}
	if got := len(firedIDs(b, "L10")); got != 2 {
		t.Fatalf("L10 = %v, want one hit per app", firedIDs(b, "L10"))
	}
}

func TestL1DoesNotPromiseASprintWhenThePlanTargetsNoTrain(t *testing.T) {
	f, _ := trunkMRInWallet(t, false)
	f.human.Plans[0].TargetKind, f.human.Plans[0].TargetTrain = TargetUnplanned, nil
	hits := firedHits(f.reconcile(), "L1")
	if len(hits) != 1 || strings.Contains(hits[0].Copy.Do, "sprint 21") {
		t.Fatalf("L1 = %+v, want no sprint promise without a target train", hits)
	}
}

func TestL10NewMRLinkOpensGitLabWithBothBranchesFilled(t *testing.T) {
	f, _ := walletPlan(t, targetToday, targetFreeze, TargetTrain)
	sprintBranchWithProjectURL(f)

	b := f.reconcile()

	hits := firedHits(b, "L10")
	if len(hits) != 2 {
		t.Fatalf("L10 = %v, want one per app", firedIDs(b, "L10"))
	}
	for _, h := range hits {
		want := walletProject + string(h.App) + "/-/merge_requests/new?merge_request[source_branch]=feature%2Fdemo-wallet&merge_request[target_branch]=feature%2Fsprint-21-2026"
		if h.Primary == nil || h.Primary.Label != "New MR" || h.Primary.URL != want || h.Primary.Fix {
			t.Fatalf("L10 %s primary = %+v, want New MR to %s", h.App, h.Primary, want)
		}
	}
}

func TestL10NewMRLinkIsLeftOutWhenTheSprintBranchOrProjectIsUnknown(t *testing.T) {
	t.Run("no MR has used the sprint branch", func(t *testing.T) {
		f, _ := walletPlan(t, targetToday, targetFreeze, TargetTrain)
		for _, h := range firedHits(f.reconcile(), "L10") {
			if h.Primary != nil {
				t.Fatalf("L10 %s primary = %+v, want none: the sprint branch is never guessed", h.App, h.Primary)
			}
		}
	})
	t.Run("no MR URL names the project", func(t *testing.T) {
		f, _ := walletPlan(t, targetToday, targetFreeze, TargetTrain)
		f.mr("di", "unrelated", "feat/other", "feature/sprint-21-2026", MRClosed, "2026-10-20", "")
		for _, h := range firedHits(f.reconcile(), "L10") {
			if h.Primary != nil {
				t.Fatalf("L10 %s primary = %+v, want none: the project page is unknown", h.App, h.Primary)
			}
		}
	})
}

func TestL8RetargetsAnOpenMRAndOnlyOpensAMergedOne(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(f *fixture)
		want  Action
	}{
		{"open", func(f *fixture) {
			f.sprintCard(demoSprint(f), "DEMO-1", "In Progress", true)
			f.mr("da", "DEMO-1 b", "feat/DEMO-1", "develop", MROpened, "2026-10-07", "")
		}, Action{Label: "Retarget MR", Fix: true}},
		{"merged", func(f *fixture) {
			f.mr("di", "DEMO-50 other", "feat/DEMO-50", "feature/sprint-6-2026", MRMerged, "2026-09-01", "2026-09-01")
			f.sprintCard(demoSprint(f), "DEMO-1", "In Progress", true)
			f.mr("di", "DEMO-1 a", "feat/DEMO-1", "develop", MRMerged, "2026-10-07", "2026-10-08")
		}, Action{Label: "Open MR"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, today)
			tc.setup(f)
			hits := firedHits(f.reconcile(), "L8")
			if len(hits) != 1 || hits[0].Primary == nil || hits[0].Primary.Label != tc.want.Label || hits[0].Primary.Fix != tc.want.Fix {
				t.Fatalf("L8 = %+v, want primary %+v", hits, tc.want)
			}
		})
	}
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

func checkCards(t *testing.T, h Hit) {
	t.Helper()
	if h.Cards == nil {
		t.Errorf("%s cards is nil; the wire promises a list", h.ID)
	}
	seen := map[string]bool{}
	for _, k := range h.Cards {
		if seen[k] {
			t.Errorf("%s lists card %s twice: %v", h.ID, k, h.Cards)
		}
		seen[k] = true
	}
}

func TestBranchCardKeysNameACardOnceThoughItHasSeveralMRs(t *testing.T) {
	br := LongLivedBranch{Cards: []LLBCard{{Key: "DEMO-1"}, {Key: "DEMO-2"}, {Key: "DEMO-1"}}}
	if got := br.cardKeys(); !slices.Equal(got, []string{"DEMO-1", "DEMO-2"}) {
		t.Fatalf("cardKeys = %v, want each card once in first-seen order", got)
	}
}
