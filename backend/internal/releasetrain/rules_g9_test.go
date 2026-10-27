package releasetrain

import (
	"strconv"
	"testing"
)

func TestG9MergedCardMRIntoASprintBranchWithoutAMilestone(t *testing.T) {
	f := newFixture(t, today)
	v := f.version("di", "22", "3.6.0", "2026-10-30", "2026-11-06")
	iid := v.cardMR("DEMO-31 limit", "feat/DEMO-31", "feature/sprint-2026-21", MRMerged, "2026-10-12", "2026-10-13")

	hits := firedHits(f.reconcile(), "G9")

	want := "!" + strconv.Itoa(iid) + " has no milestone; it rides #22, set Release 3.6.0"
	if len(hits) != 1 || hits[0].Text != want {
		t.Fatalf("G9 = %+v; want one hit %q", hits, want)
	}
	h := hits[0]
	if h.Subject != (SubjMR{App: "di", IID: iid, Source: "feat/DEMO-31", Target: "feature/sprint-2026-21"}) || h.Severity != SevHygiene {
		t.Fatalf("G9 subject %+v severity %v; want the MR as a hygiene hit", h.Subject, h.Severity)
	}
	if h.App != "di" || h.Train == nil || *h.Train != (TrainID{Number: 22, Year: 2026}) || h.Evidence["card"] != "DEMO-31" {
		t.Fatalf("G9 app %q train %v card %v; want di, #22 and DEMO-31 so the release and card drawers show it", h.App, h.Train, h.Evidence["card"])
	}
}

func (v *fv) cardMRWith(ms *MilestoneFact, title, source, target, state string) int {
	iid := v.cardMR(title, source, target, state, "2026-10-12", map[string]string{MRMerged: "2026-10-13"}[state])
	if ms != nil {
		id := ms.ID
		v.f.mrs[len(v.f.mrs)-1].MilestoneID = &id
	}
	return iid
}

func TestG9MilestoneOfAnotherTrain(t *testing.T) {
	f := newFixture(t, today)
	v22 := f.version("di", "22", "3.6.0", "2026-10-30", "2026-11-06")
	v23 := f.version("di", "23", "3.7.0", "2026-11-13", "2026-11-20")
	iid := v22.cardMRWith(v23.ms, "DEMO-31 limit", "feat/DEMO-31", "feature/sprint-2026-21", MROpened)

	hits := firedHits(f.reconcile(), "G9")

	want := "!" + strconv.Itoa(iid) + " milestone says #23, code goes to #22"
	if len(hits) != 1 || hits[0].Text != want || *hits[0].Train != (TrainID{Number: 22, Year: 2026}) {
		t.Fatalf("G9 = %+v; want one hit %q on #22", hits, want)
	}
}

func TestG9HotfixMilestoneOnASprintMR(t *testing.T) {
	f := newFixture(t, today)
	v := f.version("di", "22", "3.6.0", "2026-10-30", "2026-11-06")
	hf := f.milestone("di", "Hotfix 3.5.1", "", "2026-10-13", "2026-10-15")
	iid := v.cardMRWith(hf, "DEMO-31 limit", "feat/DEMO-31", "feature/sprint-2026-21", MRMerged)

	want := "!" + strconv.Itoa(iid) + " milestone says Hotfix 3.5.1, code goes to #22"
	if hits := firedHits(f.reconcile(), "G9"); len(hits) != 1 || hits[0].Text != want {
		t.Fatalf("G9 = %+v; want one hit %q", hits, want)
	}
}

func TestG9StaysQuiet(t *testing.T) {
	for _, tc := range []struct {
		name string
		mr   func(f *fixture)
	}{
		{"right milestone", func(f *fixture) {
			v := f.version("di", "22", "3.6.0", "2026-10-30", "2026-11-06")
			v.cardMRWith(v.ms, "DEMO-31 limit", "feat/DEMO-31", "feature/sprint-2026-21", MRMerged)
		}},
		{"split train of the same number", func(f *fixture) {
			f.version("di", "22", "3.6.0", "2026-10-30", "2026-11-06")
			split := f.version("di", "22-1", "3.6.1", "2026-11-02", "2026-11-09")
			split.cardMRWith(split.ms, "DEMO-31 limit", "feat/DEMO-31", "feature/sprint-2026-21", MRMerged)
		}},
		{"MR into a long-lived branch", func(f *fixture) {
			v := f.version("di", "22", "3.6.0", "2026-10-30", "2026-11-06")
			v.cardMRWith(nil, "DEMO-31 limit", "feat/DEMO-31", "feature/demo-wallet", MRMerged)
		}},
		{"develop, release and back-merge sources", func(f *fixture) {
			v := f.version("di", "22", "3.6.0", "2026-10-30", "2026-11-06")
			for _, src := range []string{"develop", "main", "release/release-3.5.0", "hotfix/3.5.1", "feature/sprint-2026-20"} {
				v.cardMRWith(nil, "sync", src, "feature/sprint-2026-21", MRMerged)
			}
		}},
		{"closed MR", func(f *fixture) {
			v := f.version("di", "22", "3.6.0", "2026-10-30", "2026-11-06")
			v.cardMRWith(nil, "DEMO-31 limit", "feat/DEMO-31", "feature/sprint-2026-21", MRClosed)
		}},
		{"released version", func(f *fixture) {
			v := f.published("di", "22", "3.6.0", "2026-10-10", "2026-10-20")
			v.cardMRWith(nil, "DEMO-31 limit", "feat/DEMO-31", "feature/sprint-2026-21", MRMerged)
		}},
		{"past train", func(f *fixture) {
			v := f.version("di", "20", "3.4.0", "2026-10-01", "2026-10-08")
			v.cardMRWith(nil, "DEMO-31 limit", "feat/DEMO-31", "feature/sprint-2026-19", MRMerged)
		}},
		{"sprint older than every train on the board", func(f *fixture) {
			v := f.version("di", "22", "3.6.0", "2026-10-30", "2026-11-06")
			v.cardMRWith(nil, "DEMO-31 limit", "feat/DEMO-31", "feature/sprint-2026-15", MRMerged)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, today)
			tc.mr(f)
			if hits := firedHits(f.reconcile(), "G9"); len(hits) != 0 {
				t.Fatalf("G9 = %+v; want none", hits)
			}
		})
	}
}

func TestG9TurnsRiskThreeWorkingDaysBeforeFreeze(t *testing.T) {
	for _, tc := range []struct {
		freeze string
		want   Severity
	}{{"2026-10-20", SevHygiene}, {"2026-10-19", SevRisk}, {"2026-10-12", SevRisk}} {
		f := newFixture(t, today)
		v := f.version("di", "22", "3.6.0", tc.freeze, "2026-11-06")
		v.cardMRWith(nil, "DEMO-31 limit", "feat/DEMO-31", "feature/sprint-2026-21", MROpened)
		if hits := firedHits(f.reconcile(), "G9"); len(hits) != 1 || hits[0].Severity != tc.want {
			t.Fatalf("freeze %s: G9 = %+v; want one %v hit", tc.freeze, hits, tc.want)
		}
	}
}

func TestG9UnknownTrainAheadOfTheBoardStillNamesIt(t *testing.T) {
	f := newFixture(t, today)
	f.version("di", "22", "3.6.0", "2026-10-30", "2026-11-06")
	iid := f.mr("di", "DEMO-31 limit", "feat/DEMO-31", "feature/sprint-2026-24", MROpened, "2026-10-12", "").IID

	want := "!" + strconv.Itoa(iid) + " has no milestone; it rides #25"
	if hits := firedHits(f.reconcile(), "G9"); len(hits) != 1 || hits[0].Text != want || hits[0].Severity != SevHygiene {
		t.Fatalf("G9 = %+v; want one hygiene hit %q", hits, want)
	}
}
