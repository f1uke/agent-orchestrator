package releasetrain

import (
	"strings"
	"testing"
)

func TestStageStatuses(t *testing.T) {
	for _, tc := range []struct {
		name   string
		today  string
		setup  func(f *fixture) *fv
		key    StageKey
		status StageStatus
		date   string
		detail string
	}{
		{"freeze todo before freeze", "2026-10-08", bare, StageFreeze, StatusTodo, liveFreeze, ""},
		{"freeze waiting on the day", liveFreeze, bare, StageFreeze, StatusWaiting, liveFreeze, "Freeze is today"},
		{"freeze late after", today, bare, StageFreeze, StatusLate, liveFreeze, "5 day(s) ago"},
		{"freeze done with a release MR", today, healthy, StageFreeze, StatusDone, liveFreeze, "Release MR !"},

		{"scan todo without MR", today, bare, StageScan, StatusTodo, "2026-10-10", "Waits for the release MR"},
		{"scan done when passed", today, healthy, StageScan, StatusDone, "2026-10-10", "Pipeline passed"},
		{"scan running", today, func(f *fixture) *fv {
			v := healthy(f)
			v.detail().Pipeline.Status = "running"
			return v
		}, StageScan, StatusRunning, "2026-10-10", "Pipeline running"},
		{"scan failed names required jobs", today, func(f *fixture) *fv {
			v := healthy(f)
			v.detail().Pipeline.Status = "failed"
			v.jobList().Jobs = append(v.jobList().Jobs, JobFact{Name: "build_test", Status: "failed"})
			return v
		}, StageScan, StatusFailed, "2026-10-10", "Pipeline failed: build_test"},
		{"scan done once merged", today, func(f *fixture) *fv { return shipped(f) }, StageScan, StatusDone, "2026-10-02", "Release MR merged"},

		{"approve waiting", today, func(f *fixture) *fv {
			v := healthy(f)
			v.approvers("Ann:APPROVED", "Bob:PENDING")
			return v
		}, StageApprove, StatusWaiting, "2026-10-11", "1 of 2 approved"},
		{"approve done", today, healthy, StageApprove, StatusDone, "2026-10-11", "2 of 2 approved"},
		{"approve done when released with pending", today, func(f *fixture) *fv {
			v := shipped(f)
			v.approvers("Ann:APPROVED", "Bob:PENDING")
			return v
		}, StageApprove, StatusDone, "2026-10-03", "Released with 1 of 2 approved"},

		{"tag waiting once merged", today, func(f *fixture) *fv {
			v := healthy(f)
			v.mr.State = MRMerged
			return v
		}, StageTag, StatusWaiting, "2026-10-15", "Merged, no version tag yet"},
		{"tag late after due", "2026-10-17", healthy, StageTag, StatusLate, "2026-10-15", ""},
		{"tag done", today, func(f *fixture) *fv { return shipped(f) }, StageTag, StatusDone, "2026-10-07", "Tag 3.4.0"},

		{"publish todo", today, healthy, StagePublish, StatusTodo, liveDue, "Jira release NOT marked released"},
		{"publish done with tag on the day", "2026-10-08", func(f *fixture) *fv { return shipped(f) }, StagePublish, StatusDone, "2026-10-08", "Tag 3.4.0 exists"},
		{"publish late without tag", "2026-10-17", healthy, StagePublish, StatusLate, liveDue, "no tag 3.5.0"},

		{"force waiting", today, healthy, StageForce, StatusWaiting, "2026-10-20", "REQ-1 To Do"},
		{"force done", today, func(f *fixture) *fv {
			v := bare(f)
			v.forceUpdate("REQ-9", "Closed", StatusCategoryDone, "")
			return v
		}, StageForce, StatusDone, liveFreeze, "REQ-9 Closed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, tc.today)
			v := tc.setup(f)
			b := f.reconcile()
			tr := b.train(TrainID{Number: v.trainNumber(), Year: fixtureYear})
			var got Stage
			for _, s := range tr.version(v.app.ID).Stages {
				if s.Key == tc.key {
					got = s
				}
			}
			if got.Status != tc.status || got.Date == nil || got.Date.String() != tc.date || !strings.Contains(got.Detail, tc.detail) {
				t.Fatalf("%s = %+v, want %s on %s with %q", tc.key, got, tc.status, tc.date, tc.detail)
			}
		})
	}
}

func bare(f *fixture) *fv { return f.version("di", "21", "3.5.0", liveFreeze, liveDue) }

func (v *fv) trainNumber() int {
	_, id, _ := ParseTrainLabel(v.ms.Description)
	return id.Number
}
