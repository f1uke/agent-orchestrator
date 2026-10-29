package releasetrain

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
)

const (
	today      = "2026-10-14"
	liveFreeze = "2026-10-09"
	liveDue    = "2026-10-16"
)

func healthy(f *fixture) *fv {
	v := f.version("di", "21", "3.5.0", liveFreeze, liveDue)
	v.approvers("Ann:APPROVED", "Bob:APPROVED")
	v.relatedWork(CategoryScanPage, CategoryReleaseNote)
	v.releaseMR(liveFreeze, MROpened)
	v.issue("DEMO-1", "Ready for QA", "indeterminate")
	v.forceUpdate("REQ-1", "To Do", "new", "2026-10-20")
	return v
}

func shipped(f *fixture) *fv {
	v := f.published("di", "20", "3.4.0", "2026-10-01", "2026-10-08")
	v.issue("DEMO-2", "Done", StatusCategoryDone)
	return v
}

func firedHits(b Board, rule RuleID) []Hit {
	var out []Hit
	for _, o := range b.Alerts.Rules {
		if fired, ok := o.Outcome.(Fired); ok && o.Rule == rule {
			out = append(out, fired.Hits...)
		}
	}
	return out
}

func firedIDs(b Board, rule RuleID) []string {
	var out []string
	for _, h := range firedHits(b, rule) {
		out = append(out, string(h.ID))
	}
	sort.Strings(out)
	return out
}

func TestRuleTableHasExactlyTheBuiltIDs(t *testing.T) {
	want := strings.Fields("A1 A2 A3 A4 A5 A6 A7 B1 B3 C1 C2 C3 C5 C6 C7 D1 D2 D3 E1 E2 E4 E5 F1 F2 G1 G2 G3 G4 G5 G6 G9 L1 L3 L8 L9 L10 L11 L12 L13")
	var got []string
	for _, r := range ruleTable() {
		got = append(got, string(r.ID))
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rule ids = %v, want %v", got, want)
	}
}

type ruleCase struct {
	name  string
	rule  RuleID
	today string
	setup func(f *fixture)
	want  []string
	text  string
}

func ruleCases() []ruleCase {
	return []ruleCase{
		{"A1 quiet", "A1", today, func(f *fixture) { healthy(f) }, nil, ""},
		{"A1 milestone and Jira disagree", "A1", today, func(f *fixture) {
			healthy(f).jira.Name = "iOS 3.5.1"
		}, []string{"A1~v/21-2026/di"}, "Train 21: milestone is 3.5.0, Jira release is 3.5.1"},

		{"A2 quiet", "A2", today, func(f *fixture) { healthy(f) }, nil, ""},
		{"A2 Jira label with no milestone", "A2", today, func(f *fixture) {
			healthy(f)
			jv := f.jiraVersion("da", "3.6.0", f.label("da", "22"), "2026-10-30")
			jv.ID = "7001"
		}, []string{"A2~jv/7001"}, "is in train 22, but no milestone is"},

		{"A3 quiet", "A3", today, func(f *fixture) { healthy(f) }, nil, ""},
		{"A3 milestone label with no Jira", "A3", today, func(f *fixture) {
			healthy(f)
			f.milestone("pi", "Release 1.2.0", f.label("pi", "21"), liveFreeze, liveDue).ID = 9001
		}, []string{"A3~m/pi/9001"}, "has no Jira release for train 21"},

		{"A4 quiet: unlabelled Jira matches a hotfix", "A4", today, func(f *fixture) {
			healthy(f)
			f.milestone("di", "Hotfix 3.4.1", "crash on launch", "2026-10-01", "2026-10-03")
			f.jiraVersion("di", "3.4.1", "", "2026-10-03")
		}, nil, ""},
		{"A4 unlabelled Jira with no hotfix", "A4", today, func(f *fixture) {
			healthy(f)
			f.jiraVersion("di", "3.4.2", "", "2026-10-03").ID = "7002"
		}, []string{"A4~jv/7002"}, "has no train label"},

		{"A5 quiet", "A5", today, func(f *fixture) { healthy(f) }, nil, ""},
		{"A5 dates differ", "A5", today, func(f *fixture) {
			healthy(f).jira.ReleaseDate = dayPtr(f.t, "2026-10-15")
		}, []string{"A5~v/21-2026/di"}, "Train 21 release dates differ: Jira Thu 15 Oct, milestone Fri 16 Oct"},

		{"A7 quiet: one app only", "A7", today, func(f *fixture) { healthy(f) }, nil, ""},
		{"A7 quiet: every app agrees", "A7", today, func(f *fixture) {
			healthy(f)
			f.version("da", "21", "3.5.0", liveFreeze, liveDue)
			f.version("pi", "21", "1.2.0", liveFreeze, liveDue)
		}, nil, ""},
		{"A7 quiet: an app without a start date is not counted", "A7", today, func(f *fixture) {
			healthy(f)
			f.version("da", "21", "3.5.0", liveFreeze, liveDue)
			f.version("pi", "21", "1.2.0", liveFreeze, "2026-10-20").ms.Start = nil
		}, nil, ""},
		{"A7 one app off the majority", "A7", today, func(f *fixture) {
			healthy(f)
			f.version("da", "21", "3.5.0", liveFreeze, liveDue)
			f.version("pi", "21", "1.2.0", liveFreeze, liveDue)
			f.version("pa", "21", "1.2.0", "2026-10-12", "2026-10-20")
		}, []string{"A7~v/21-2026/pa"}, "Train 21 dates differ here: Mon 12 Oct → Tue 20 Oct, the other apps run Fri 9 Oct → Fri 16 Oct"},
		{"A7 two against two flags every app", "A7", today, func(f *fixture) {
			healthy(f)
			f.version("da", "21", "3.5.0", liveFreeze, liveDue)
			f.version("pi", "21", "1.2.0", "2026-10-12", "2026-10-20")
			f.version("pa", "21", "1.2.0", "2026-10-12", "2026-10-20")
		}, []string{"A7~v/21-2026/da", "A7~v/21-2026/di", "A7~v/21-2026/pa", "A7~v/21-2026/pi"}, "The apps in train 21 have different dates"},

		{"B1 quiet", "B1", today, func(f *fixture) { healthy(f) }, nil, ""},
		{"B1 freeze passed with no release MR", "B1", today, func(f *fixture) {
			f.version("da", "21", "3.5.0", liveFreeze, liveDue)
		}, []string{"B1~v/21-2026/da"}, "Open the release MR. Freeze was Fri 9 Oct."},

		{"B3 quiet", "B3", today, func(f *fixture) { healthy(f) }, nil, ""},
		{"B3 card in progress after freeze", "B3", today, func(f *fixture) {
			healthy(f).issue("DEMO-3", "In Progress", "indeterminate")
		}, []string{"B3~v/21-2026/di"}, "1 card in train 21 is still in development"},

		{"C1 quiet", "C1", today, func(f *fixture) { healthy(f) }, nil, ""},
		{"C1 required job failed", "C1", today, func(f *fixture) {
			j := healthy(f).jobList()
			j.Jobs = append(j.Jobs, JobFact{Name: "build_test", Status: "failed"})
		}, []string{"C1~v/21-2026/di"}, "Train 21 release pipeline failed: build_test"},

		{"C2 quiet: required failure is C1's", "C2", today, func(f *fixture) {
			j := healthy(f).jobList()
			j.Jobs = append(j.Jobs, JobFact{Name: "build_test", Status: "failed"})
		}, nil, ""},
		{"C2 allowed job failed", "C2", today, func(f *fixture) {
			j := healthy(f).jobList()
			j.Jobs = append(j.Jobs, JobFact{Name: "lint_report", Status: "failed", AllowFailure: true})
		}, []string{"C2~v/21-2026/di"}, "lint_report failed, allowed to fail"},

		{"C3 quiet at 60 minutes 20 seconds", "C3", today, func(f *fixture) {
			d := 60*60 + 20
			healthy(f).detail().Pipeline.Duration = &d
		}, nil, ""},
		{"C3 slow pipeline", "C3", today, func(f *fixture) {
			d := 71 * 60
			healthy(f).detail().Pipeline.Duration = &d
		}, []string{"C3~v/21-2026/di"}, "took 71 minutes"},

		{"C5 quiet", "C5", today, func(f *fixture) { healthy(f) }, nil, ""},
		{"C5 conflicts", "C5", today, func(f *fixture) {
			healthy(f).detail().Conflicts = true
		}, []string{"C5~v/21-2026/di"}, "has merge conflicts"},

		{"C6 quiet: jobs not fetched yet", "C6", today, func(f *fixture) {
			v := healthy(f)
			v.detail().Pipeline.ID = 99999
		}, nil, ""},
		{"C6 semgrep skipped", "C6", today, func(f *fixture) {
			j := healthy(f).jobList()
			j.Jobs[1].Status = "skipped"
		}, []string{"C6~v/21-2026/di"}, "semgrep-sast was skipped"},
		{"C6 semgrep missing", "C6", today, func(f *fixture) {
			j := healthy(f).jobList()
			j.Jobs = j.Jobs[:1]
		}, []string{"C6~v/21-2026/di"}, "semgrep-sast is missing"},

		{"C7 quiet: a newer version tag exists", "C7", today, func(f *fixture) {
			v := shipped(f)
			v.compare().Commits = []CommitFact{{Title: "Merge branch 'fix/crash' into 'main'", CreatedOn: day(f.t, "2026-10-10")}}
			f.tags = append(f.tags, &TagFact{Project: 1001, Name: "3.4.1", CommitOn: day(f.t, "2026-10-11")})
		}, nil, ""},
		{"C7 merges after the newest tag", "C7", today, func(f *fixture) {
			v := shipped(f)
			v.compare().Commits = []CommitFact{
				{Title: "Merge branch 'release/release-3.4.0' into 'main'", CreatedOn: day(f.t, "2026-10-08")},
				{Title: "Merge branch 'fix/crash' into 'main'", CreatedOn: day(f.t, "2026-10-10")},
			}
			f.tags = append(f.tags, &TagFact{Project: 1001, Name: "3.4.1-rc1", CommitOn: day(f.t, "2026-10-11")})
		}, []string{"C7~v/20-2026/di"}, "1 branch merged into main after tag 3.4.0"},

		{"D1 quiet on freeze plus two", "D1", "2026-10-11", func(f *fixture) {
			healthy(f).approvers()
		}, nil, ""},
		{"D1 no approvers", "D1", today, func(f *fixture) {
			healthy(f).approvers()
		}, []string{"D1~v/21-2026/di"}, "Train 21 Jira release has no approvers"},

		{"D2 quiet: related work not fetched", "D2", today, func(f *fixture) {
			healthy(f)
			f.related = nil
		}, nil, ""},
		{"D2 release note missing", "D2", today, func(f *fixture) {
			healthy(f)
			f.related[0].Links = f.related[0].Links[:1]
		}, []string{"D2~v/21-2026/di"}, "Train 21 Jira release has no release note"},

		{"D3 quiet before the day before publish", "D3", today, func(f *fixture) {
			healthy(f).approvers("Ann:APPROVED", "Bob:PENDING")
		}, nil, ""},
		{"D3 pending the day before publish", "D3", "2026-10-15", func(f *fixture) {
			healthy(f).approvers("Ann:APPROVED", "Bob:PENDING")
		}, []string{"D3~v/21-2026/di"}, "1 of 2 approvers has not approved train 21"},

		{"E1 quiet", "E1", today, func(f *fixture) { shipped(f) }, nil, ""},
		{"E1 train and hotfix unreleased", "E1", today, func(f *fixture) {
			shipped(f).jira.Released = false
			f.milestone("da", "Hotfix 3.3.1", "crash on launch", "2026-10-06", "2026-10-08")
			f.jiraVersion("da", "3.3.1", "", "2026-10-08")
		}, []string{"E1~h/da/3.3.1", "E1~v/20-2026/di"}, "shipped, but its Jira release is not marked released"},

		{"E2 quiet", "E2", today, func(f *fixture) { shipped(f) }, nil, ""},
		{"E2 card not done after publish", "E2", today, func(f *fixture) {
			shipped(f).issue("DEMO-4", "Ready for UAT", "indeterminate")
		}, []string{"E2~v/20-2026/di"}, "1 card in train 20 is not Done after publish"},

		{"E4 quiet when done", "E4", today, func(f *fixture) {
			shipped(f).forceUpdate("REQ-2", "Closed", StatusCategoryDone, "2026-10-12")
		}, nil, ""},
		{"E4 open after due", "E4", today, func(f *fixture) {
			shipped(f).forceUpdate("REQ-2", "In Progress", "indeterminate", "2026-10-12")
		}, []string{"E4~v/20-2026/di"}, "Force-update ticket REQ-2 is past its due date (In Progress)"},

		{"E5 quiet", "E5", today, func(f *fixture) { healthy(f) }, nil, ""},
		{"E5 no due date", "E5", today, func(f *fixture) {
			shipped(f).forceUpdate("REQ-3", "To Do", "new", "")
		}, []string{"E5~v/20-2026/di"}, "Force-update ticket REQ-3 has no due date"},

		{"F1 quiet", "F1", today, func(f *fixture) {
			shipped(f)
			healthy(f)
		}, nil, ""},
		{"F1 trains overlap", "F1", today, func(f *fixture) {
			healthy(f)
			f.version("di", "22", "3.6.0", "2026-10-15", "2026-10-23")
		}, []string{"F1~tt/21-2026/22-2026"}, "Train 22 freezes Thu 15 Oct, before train 21 publishes Fri 16 Oct"},

		{"F2 quiet", "F2", today, func(f *fixture) {
			healthy(f)
			f.milestone("di", "Hotfix 3.4.1", "crash", "2026-10-01", "2026-10-03")
		}, nil, ""},
		{"F2 hotfix inside a train", "F2", today, func(f *fixture) {
			healthy(f)
			f.milestone("di", "Hotfix 3.4.1", "crash", "2026-10-10", "2026-10-13")
		}, []string{"F2~ht/di/3.4.1/21-2026"}, "overlaps train 21"},
	}
}

func TestEveryRuleFiresAndStaysQuiet(t *testing.T) {
	for _, tc := range ruleCases() {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, tc.today)
			tc.setup(f)
			b := f.reconcile()
			got := firedIDs(b, tc.rule)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("%s hits = %v, want %v", tc.rule, got, tc.want)
			}
			for _, h := range firedHits(b, tc.rule) {
				if said := h.Copy.What + " " + h.Copy.Do; !strings.Contains(said, tc.text) {
					t.Fatalf("%s copy = %q, want it to contain %q", h.ID, said, tc.text)
				}
			}
		})
	}
}

func TestD3EndsAtPublishAndE1TakesOver(t *testing.T) {
	for _, tc := range []struct {
		today  string
		tagged bool
		want   []string
	}{
		{"2026-10-15", true, []string{"D3~v/21-2026/di"}},
		{"2026-10-16", true, []string{"D3~v/21-2026/di"}},
		{"2026-10-17", true, []string{"E1~v/21-2026/di"}},
		{"2026-10-17", false, []string{"D3~v/21-2026/di"}},
	} {
		t.Run(fmt.Sprintf("%s tagged=%v", tc.today, tc.tagged), func(t *testing.T) {
			f := newFixture(t, tc.today)
			v := healthy(f)
			v.approvers("Ann:APPROVED", "Bob:PENDING")
			if tc.tagged {
				v.tagOn("2026-10-15")
			}
			b := f.reconcile()
			got := append(firedIDs(b, "D3"), firedIDs(b, "E1")...)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("D3+E1 hits = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestDatesSplitIsTheAppsA7Flags(t *testing.T) {
	f := newFixture(t, today)
	healthy(f)
	f.version("da", "21", "3.5.0", liveFreeze, liveDue)
	f.version("pi", "21", "1.2.0", liveFreeze, liveDue)
	f.version("pa", "21", "1.2.0", "2026-10-12", "2026-10-20")
	f.version("di", "22", "3.6.0", "2026-10-20", "2026-10-27")
	f.version("da", "22", "3.6.0", "2026-10-21", "2026-10-28")
	f.version("di", "23", "3.7.0", "2026-11-02", "2026-11-09")
	b := f.reconcile()

	flagged := map[string][]string{}
	for _, h := range firedHits(b, "A7") {
		flagged[h.Train.Key()] = append(flagged[h.Train.Key()], string(h.App))
	}
	for _, tr := range b.Trains {
		got := []string{}
		for _, a := range tr.DatesSplit {
			got = append(got, string(a))
		}
		want := append([]string{}, flagged[tr.ID.Key()]...)
		sort.Strings(got)
		sort.Strings(want)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("train %s datesSplit = %v, A7 flags %v", tr.ID, got, want)
		}
	}
	if len(flagged) != 2 {
		t.Fatalf("A7 flagged trains %v, want #21 and #22", flagged)
	}
}
