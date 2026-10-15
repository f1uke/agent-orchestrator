package releasetrain

import (
	"reflect"
	"strconv"
	"testing"
)

type gCase struct {
	name  string
	today string
	setup func(f *fixture) []string
	text  string
}

func runG(t *testing.T, rule RuleID, cases []gCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			now := tc.today
			if now == "" {
				now = today
			}
			f := newFixture(t, now)
			want := tc.setup(f)
			b := f.reconcile()
			if got := firedIDs(b, rule); !reflect.DeepEqual(got, want) {
				t.Fatalf("%s hits = %v, want %v", rule, got, want)
			}
			if tc.text != "" && firedHits(b, rule)[0].Text != tc.text {
				t.Fatalf("%s text = %q, want %q", rule, firedHits(b, rule)[0].Text, tc.text)
			}
		})
	}
}

func TestG1MergedCodeWhileJiraSaysInDevelopment(t *testing.T) {
	runG(t, "G1", []gCase{
		{"every MR merged, Jira in progress", "", func(f *fixture) []string {
			f.sprintCard(demoSprint(f), "DEMO-1", "In Progress", true)
			f.mr("di", "DEMO-1 a", "feat/DEMO-1", "feature/sprint-7-2026", MRMerged, "2026-10-07", "2026-10-08")
			return []string{"G1~c/DEMO-1"}
		}, "Jira says In Progress, all 1 MR(s) merged (Demo iOS !102)"},
		{"one MR still open", "", func(f *fixture) []string {
			f.sprintCard(demoSprint(f), "DEMO-1", "In Progress", true)
			f.mr("di", "DEMO-1 a", "feat/DEMO-1", "feature/sprint-7-2026", MRMerged, "2026-10-07", "2026-10-08")
			f.mr("da", "DEMO-1 b", "feat/DEMO-1", "feature/sprint-7-2026", MROpened, "2026-10-07", "")
			return nil
		}, ""},
		{"Jira moved on", "", func(f *fixture) []string {
			f.sprintCard(demoSprint(f), "DEMO-1", "Ready for QA", true)
			f.mr("di", "DEMO-1 a", "feat/DEMO-1", "feature/sprint-7-2026", MRMerged, "2026-10-07", "2026-10-08")
			return nil
		}, ""},
		{"no MR", "", func(f *fixture) []string {
			f.sprintCard(demoSprint(f), "DEMO-1", "In Progress", true)
			return nil
		}, ""},
	})
}

func TestG2NoMRWithinThreeWorkingDaysOfTheSprintEnd(t *testing.T) {
	runG(t, "G2", []gCase{
		{"Thursday before a Tuesday end: 3 working days", "2026-10-15", func(f *fixture) []string {
			f.sprintCard(demoSprint(f), "DEMO-1", "In Progress", true)
			return []string{"G2~c/DEMO-1"}
		}, "No MR yet, DEMO 7 ends Tue 20 Oct (3 working days left) · Jira In Progress"},
		{"Wednesday before a Tuesday end: 4 working days", "2026-10-14", func(f *fixture) []string {
			f.sprintCard(demoSprint(f), "DEMO-1", "In Progress", true)
			return nil
		}, ""},
		{"Sunday before a Monday end: 1 working day", "2026-10-18", func(f *fixture) []string {
			f.sprintCard(f.sprint(42, 7, "2026-10-06", "2026-10-19"), "DEMO-1", "In Progress", true)
			return []string{"G2~c/DEMO-1"}
		}, "No MR yet, DEMO 7 ends Mon 19 Oct (1 working day left) · Jira In Progress"},
		{"carried over, abandoned or with an MR stay quiet", "2026-10-15", func(f *fixture) []string {
			s := demoSprint(f)
			f.sprintCard(s, "DEMO-1", "Done", false)
			f.sprintCard(s, "DEMO-2", StatusAbandoned, true)
			f.sprintCard(s, "DEMO-3", "In Progress", true)
			f.mr("di", "DEMO-3", "feat/DEMO-3", "feature/sprint-7-2026", MROpened, "2026-10-07", "")
			return nil
		}, ""},
	})
}

func routedToNext(f *fixture) {
	v := f.version("di", "7", "3.5.0", "2026-10-12", "2026-10-19")
	v.issue("DEMO-1", "In Progress", "indeterminate")
	f.sprintCard(demoSprint(f), "DEMO-1", "In Progress", true)
	f.mr("di", "DEMO-1 a", "feat/DEMO-1", "feature/sprint-7-2026", MRMerged, "2026-10-07", "2026-10-08")
}

func TestG3CodeGoesToAnotherTrainThanThePageOrFixVersion(t *testing.T) {
	runG(t, "G3", []gCase{
		{"fixVersion says 7, code in sprint 7's branch rides 8", "", func(f *fixture) []string {
			routedToNext(f)
			return []string{"G3~c/DEMO-1"}
		}, "Page or fixVersion says #7, code goes to #8 (Demo iOS)"},
		{"fixVersion agrees", "", func(f *fixture) []string {
			v := f.version("di", "8", "3.6.0", "2026-10-26", "2026-11-02")
			v.issue("DEMO-1", "In Progress", "indeterminate")
			f.sprintCard(demoSprint(f), "DEMO-1", "In Progress", true)
			f.mr("di", "DEMO-1 a", "feat/DEMO-1", "feature/sprint-7-2026", MRMerged, "2026-10-07", "2026-10-08")
			return nil
		}, ""},
		{"parked in a long-lived branch with no target", "", func(f *fixture) []string {
			v := f.version("di", "7", "3.5.0", "2026-10-12", "2026-10-19")
			v.issue("DEMO-1", "In Progress", "indeterminate")
			f.sprintCard(demoSprint(f), "DEMO-1", "In Progress", true)
			f.mr("di", "DEMO-1 a", "feat/DEMO-1", "feature/demo-chat", MRMerged, "2026-09-07", "2026-09-08")
			return []string{"G3~c/DEMO-1"}
		}, "Page or fixVersion says #7, code goes to feature/demo-chat (Demo iOS)"},
		{"carried over cards are not checked", "", func(f *fixture) []string {
			v := f.version("di", "7", "3.5.0", "2026-10-12", "2026-10-19")
			v.issue("DEMO-1", "In Progress", "indeterminate")
			f.sprintCard(demoSprint(f), "DEMO-1", "In Progress", false)
			f.mr("di", "DEMO-1 a", "feat/DEMO-1", "feature/demo-chat", MRMerged, "2026-09-07", "2026-09-08")
			return nil
		}, ""},
	})
}

func TestG4AppsGoToDifferentPlaces(t *testing.T) {
	runG(t, "G4", []gCase{
		{"iOS in the sprint branch, Android straight into develop", "", func(f *fixture) []string {
			f.sprintCard(demoSprint(f), "DEMO-1", "In Progress", true)
			f.mr("di", "DEMO-1 a", "feat/DEMO-1", "feature/sprint-7-2026", MRMerged, "2026-10-07", "2026-10-08")
			f.mr("da", "DEMO-1 b", "feat/DEMO-1", "develop", MROpened, "2026-10-07", "")
			return []string{"G4~c/DEMO-1"}
		}, "Demo iOS → #8, Demo Android → off flow"},
		{"both apps in the sprint branch", "", func(f *fixture) []string {
			f.sprintCard(demoSprint(f), "DEMO-1", "In Progress", true)
			f.mr("di", "DEMO-1 a", "feat/DEMO-1", "feature/sprint-7-2026", MRMerged, "2026-10-07", "2026-10-08")
			f.mr("da", "DEMO-1 b", "feat/DEMO-1", "feature/sprint-2026-7", MROpened, "2026-10-07", "")
			return nil
		}, ""},
	})
}

func pagedTrain(f *fixture, freeze string) *PageFact {
	v := f.version("di", "21", "3.5.0", freeze, "2026-10-26")
	v.releaseMR("2026-10-12", MROpened)
	v.issue("DEMO-1", "In Progress", "indeterminate")
	f.mr("di", "DEMO-1 a", "feat/DEMO-1", "develop", MRMerged, "2026-10-01", "2026-10-02")
	v.issue("DEMO-2", "In Progress", "indeterminate")
	f.mr("di", "DEMO-2 a", "feat/DEMO-2", "develop", MROpened, "2026-10-01", "")
	v.issue("DEMO-3", "In Progress", "indeterminate")
	return f.page("demo", "21", "2026-09-20", freeze, "2026-10-26")
}

func TestG5CardShipsButIsNotOnThePage(t *testing.T) {
	runG(t, "G5", []gCase{
		{"merged and open cards missing from the page", "", func(f *fixture) []string {
			pagedTrain(f, "2026-10-19")
			return []string{"G5~pf/21-2026/demo/merged", "G5~pf/21-2026/demo/open"}
		}, ""},
		{"listed cards and cards with no code stay quiet", "", func(f *fixture) []string {
			p := pagedTrain(f, "2026-10-19")
			f.card(p, "DEMO-1")
			f.card(p, "DEMO-2")
			return nil
		}, ""},
		{"a published train is not checked", "2026-10-27", func(f *fixture) []string {
			pagedTrain(f, "2026-10-19")
			return nil
		}, ""},
		{"no page, no hit", "", func(f *fixture) []string {
			v := f.version("di", "21", "3.5.0", "2026-10-19", "2026-10-26")
			v.issue("DEMO-1", "In Progress", "indeterminate")
			f.mr("di", "DEMO-1 a", "feat/DEMO-1", "develop", MRMerged, "2026-10-01", "2026-10-02")
			return nil
		}, ""},
	})
	for _, tc := range []struct {
		freeze string
		want   Severity
	}{{"2026-10-19", SevInfo}, {"2026-10-17", SevRisk}} {
		f := newFixture(t, today)
		pagedTrain(f, tc.freeze)
		b := f.reconcile()
		for _, h := range firedHits(b, "G5") {
			want := tc.want
			if h.Subject.(SubjPageFamily).Bucket == "merged" {
				want = SevRisk
			}
			if h.Severity != want {
				t.Errorf("freeze %s: %s severity %s, want %s", tc.freeze, h.ID, h.Severity, want)
			}
		}
	}
}

func TestL8CardMRIntoDevelopAfterCutover(t *testing.T) {
	runG(t, "L8", []gCase{
		{"merged into develop after cutover", "", func(f *fixture) []string {
			f.mr("di", "DEMO-50 other", "feat/DEMO-50", "feature/sprint-6-2026", MRMerged, "2026-09-01", "2026-09-01")
			f.sprintCard(demoSprint(f), "DEMO-1", "In Progress", true)
			iid := f.mr("di", "DEMO-1 a", "feat/DEMO-1", "develop", MRMerged, "2026-10-07", "2026-10-08").IID
			return []string{"L8~mr/di/" + strconv.Itoa(iid)}
		}, ""},
		{"open into develop is caught before it merges", "", func(f *fixture) []string {
			f.sprintCard(demoSprint(f), "DEMO-1", "In Progress", true)
			iid := f.mr("da", "DEMO-1 b", "feat/DEMO-1", "develop", MROpened, "2026-10-07", "").IID
			return []string{"L8~mr/da/" + strconv.Itoa(iid)}
		}, ""},
		{"into develop before cutover, or into the sprint branch, is fine", "", func(f *fixture) []string {
			f.mr("di", "DEMO-50 other", "feat/DEMO-50", "feature/sprint-7-2026", MRMerged, "2026-10-09", "2026-10-09")
			s := demoSprint(f)
			f.sprintCard(s, "DEMO-1", "In Progress", true)
			f.mr("di", "DEMO-1 a", "feat/DEMO-1", "develop", MRMerged, "2026-10-07", "2026-10-08")
			f.sprintCard(s, "DEMO-2", "In Progress", true)
			f.mr("di", "DEMO-2 a", "feat/DEMO-2", "feature/sprint-7-2026", MROpened, "2026-10-09", "")
			return nil
		}, ""},
	})
}

func TestSprintHitsTakeMineFromTheirCard(t *testing.T) {
	f := newFixture(t, "2026-10-15")
	f.me()
	s := demoSprint(f)
	mine := f.sprintCard(s, "DEMO-1", "In Progress", true)
	mine.Assignee = dee
	f.sprintCard(s, "DEMO-2", "In Progress", true)
	f.sprintCard(s, "DEMO-3", "In Progress", true)
	f.subtask(s, "DEMO-3", "DEMO-4", "iOS part", "new").Assignee = dee
	b := f.reconcile()
	got := map[HitID]int{}
	for _, h := range firedHits(b, "G2") {
		got[h.ID] = len(h.Mine)
	}
	if !reflect.DeepEqual(got, map[HitID]int{"G2~c/DEMO-1": 1, "G2~c/DEMO-2": 0, "G2~c/DEMO-3": 1}) {
		t.Fatalf("G2 mine counts = %v; want DEMO-1 (assignee) and DEMO-3 (subtask) mine", got)
	}
}

func TestWorkDaysLeftSkipsTheWeekend(t *testing.T) {
	for _, tc := range []struct {
		today, end string
		want       int
	}{
		{"2026-10-15", "2026-10-20", 3},
		{"2026-10-16", "2026-10-20", 2},
		{"2026-10-17", "2026-10-19", 1},
		{"2026-10-20", "2026-10-20", 0},
	} {
		if got := workDaysLeft(day(t, tc.today), day(t, tc.end)); got != tc.want {
			t.Errorf("workDaysLeft(%s, %s) = %d, want %d", tc.today, tc.end, got, tc.want)
		}
	}
}
