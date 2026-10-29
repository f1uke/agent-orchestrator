package releasetrain

import (
	"slices"
	"strconv"
	"testing"
)

type linkCase struct {
	name  string
	rule  RuleID
	today string
	setup func(f *fixture) []Link
	id    HitID
}

func linkCases() []linkCase {
	return []linkCase{
		{"A1 opens the Jira release and the milestone", "A1", today, func(f *fixture) []Link {
			v := healthy(f)
			v.jira.Name = "iOS 3.5.1"
			return []Link{{"Open Jira release", v.jira.URL}, {"Open milestone", v.ms.URL}}
		}, "A1~v/21-2026/di"},
		{"A2 has no milestone to open", "A2", today, func(f *fixture) []Link {
			healthy(f)
			jv := f.jiraVersion("da", "3.6.0", f.label("da", "22"), "2026-10-30")
			jv.ID = "7001"
			return []Link{{"Open Jira release", jv.URL}}
		}, "A2~jv/7001"},
		{"A3 has no Jira release to open", "A3", today, func(f *fixture) []Link {
			healthy(f)
			m := f.milestone("pi", "Release 1.2.0", f.label("pi", "21"), liveFreeze, liveDue)
			m.ID = 9001
			return []Link{{"Open milestone", m.URL}}
		}, "A3~m/pi/9001"},
		{"A5 opens the Jira release and the milestone", "A5", today, func(f *fixture) []Link {
			v := healthy(f)
			v.jira.ReleaseDate = dayPtr(f.t, "2026-10-15")
			return []Link{{"Open Jira release", v.jira.URL}, {"Open milestone", v.ms.URL}}
		}, "A5~v/21-2026/di"},
		{"A6 opens the page and the milestone", "A6", today, func(f *fixture) []Link {
			v := healthy(f)
			p := f.page("demo", "21", "2026-09-20", "2026-10-08", liveDue)
			return []Link{{"Open page", p.URL}, {"Open milestone", v.ms.URL}}
		}, "A6~vd/21-2026/di/freeze"},
		{"A6 on an unreadable page opens only the page", "A6", today, func(f *fixture) []Link {
			healthy(f)
			p := f.page("demo", "21", "2026-09-20", "", "")
			p.ParseError = "no table"
			return []Link{{"Open page", p.URL}}
		}, "A6~vd/21-2026/di/page"},
		{"A7 opens its milestone and one the rest of the train runs on", "A7", today, func(f *fixture) []Link {
			di := healthy(f)
			f.version("da", "21", "3.5.0", liveFreeze, liveDue)
			f.version("pi", "21", "1.2.0", liveFreeze, liveDue)
			pa := f.version("pa", "21", "1.2.0", "2026-10-12", "2026-10-20")
			return []Link{{"Open milestone", pa.ms.URL}, {"Open Demo iOS milestone", di.ms.URL}}
		}, "A7~v/21-2026/pa"},
		{"E1 opens the Jira release and the tag", "E1", today, func(f *fixture) []Link {
			v := shipped(f)
			v.jira.Released = false
			return []Link{{"Open Jira release", v.jira.URL}, {"Open tag", v.tag.URL}}
		}, "E1~v/20-2026/di"},
		{"G1 opens the card and each merged MR", "G1", today, func(f *fixture) []Link {
			f.sprintCard(demoSprint(f), "DEMO-1", "In Progress", true)
			a := f.mr("di", "DEMO-1 a", "feat/DEMO-1", "feature/sprint-7-2026", MRMerged, "2026-10-07", "2026-10-08")
			b := f.mr("da", "DEMO-1 b", "feat/DEMO-1", "feature/sprint-7-2026", MRMerged, "2026-10-07", "2026-10-08")
			return []Link{{"Open card", "https://jira.example.com/browse/DEMO-1"}, {"Open !" + strconv.Itoa(a.IID), a.URL}, {"Open !" + strconv.Itoa(b.IID), b.URL}}
		}, "G1~c/DEMO-1"},
		{"G3 opens the card and the MR the code went by", "G3", today, func(f *fixture) []Link {
			v := f.version("di", "7", "3.5.0", "2026-10-12", "2026-10-19")
			v.issue("DEMO-1", "In Progress", "indeterminate")
			f.sprintCard(demoSprint(f), "DEMO-1", "In Progress", true)
			m := f.mr("di", "DEMO-1 a", "feat/DEMO-1", "feature/sprint-7-2026", MRMerged, "2026-10-07", "2026-10-08")
			return []Link{{"Open card", "https://jira.example.com/browse/DEMO-1"}, {"Open !" + strconv.Itoa(m.IID), m.URL}}
		}, "G3~c/DEMO-1"},
	}
}

func TestEachMismatchHitOpensBothSources(t *testing.T) {
	for _, tc := range linkCases() {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, tc.today)
			want := tc.setup(f)
			h, ok := alertHit(f.reconcile(), tc.id)
			if !ok {
				t.Fatalf("no hit %s", tc.id)
			}
			got := h.Links
			if h.Primary != nil && !h.Primary.Fix {
				got = append([]Link{{Label: h.Primary.Label, URL: h.Primary.URL}}, got...)
			}
			if len(got) != len(want) {
				t.Fatalf("primary %+v and links %+v, want the links %+v", h.Primary, h.Links, want)
			}
			for _, w := range want {
				if !slices.Contains(got, w) {
					t.Fatalf("links %+v miss %+v", got, w)
				}
			}
		})
	}
}

func TestLinksLeaveHitIDsAndFingerprintsAlone(t *testing.T) {
	for _, tc := range linkCases() {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, tc.today)
			tc.setup(f)
			h, ok := alertHit(f.reconcile(), tc.id)
			if !ok {
				t.Fatalf("no hit %s", tc.id)
			}
			if got, want := h.Fingerprint(), linkPrints[tc.id]; got != want {
				t.Fatalf("%s fingerprint = %s, want %s from before hits carried links", tc.id, got, want)
			}
		})
	}
}

var linkPrints = map[HitID]string{
	"A1~v/21-2026/di":         "f80647d0ef7981e0",
	"A2~jv/7001":              "4b87b4204daad1fc",
	"A3~m/pi/9001":            "75e5accfd830a583",
	"A5~v/21-2026/di":         "ebacf559b04a9cc9",
	"A6~vd/21-2026/di/freeze": "0897f61eaa655a33",
	"A6~vd/21-2026/di/page":   "40af27216af0538d",
	"A7~v/21-2026/pa":         "737173fa1c53581c",
	"E1~v/20-2026/di":         "d70bfaecf65e3c20",
	"G1~c/DEMO-1":             "8728231d82378993",
	"G3~c/DEMO-1":             "d2b9b13dcf575439",
}
