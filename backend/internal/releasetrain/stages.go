package releasetrain

import (
	"fmt"
	"strings"
)

// StageKey names one of the six release stages.
type StageKey string

// The six release stages in order.
const (
	StageFreeze  StageKey = "freeze"
	StageScan    StageKey = "scan"
	StageApprove StageKey = "approve"
	StageTag     StageKey = "tag"
	StagePublish StageKey = "publish"
	StageForce   StageKey = "force"
)

// StageStatus is where a stage stands.
type StageStatus string

// Stage statuses.
const (
	StatusDone          StageStatus = "done"
	StatusRunning       StageStatus = "running"
	StatusWaiting       StageStatus = "waiting"
	StatusFailed        StageStatus = "failed"
	StatusLate          StageStatus = "late"
	StatusTodo          StageStatus = "todo"
	StatusUnknown       StageStatus = "unknown"
	StatusNotObservable StageStatus = "not_observable"
)

// Stage is one stage row of one app version.
type Stage struct {
	Key          StageKey         `json:"key"`
	Date         *Day             `json:"date,omitempty"`
	Status       StageStatus      `json:"status"`
	Detail       string           `json:"detail"`
	Conventional bool             `json:"conventional,omitempty"`
	Reads        []KindName       `json:"reads"`
	Missing      []ResourceStatus `json:"missing,omitempty"`
}

type stageRow struct {
	key   StageKey
	reads []KindName
	eval  func(t *Train, v *AppVersion, today Day) Stage
	// byConvention rows date by freeze + n, a date no system records.
	byConvention bool
}

var stageTable = []stageRow{
	{StageFreeze, []KindName{KindMilestones.Name, KindMRs.Name}, freezeStage, false},
	{StageScan, []KindName{KindMilestones.Name, KindMRs.Name, KindMRDetail.Name, KindJobs.Name}, scanStage, true},
	{StageApprove, []KindName{KindMilestones.Name, KindJiraVersions.Name}, approveStage, true},
	{StageTag, []KindName{KindMilestones.Name, KindMRs.Name, KindTags.Name}, tagStage, false},
	{StagePublish, []KindName{KindMilestones.Name, KindJiraVersions.Name, KindTags.Name}, publishStage, false},
	{StageForce, []KindName{KindMilestones.Name, KindTechReq.Name}, forceStage, false},
}

func stages(t *Train, v *AppVersion, today Day, rs *readSet) []Stage {
	out := make([]Stage, 0, len(stageTable))
	for _, row := range stageTable {
		s := row.eval(t, v, today)
		s.Key = row.key
		s.Reads = row.reads
		if row.byConvention && s.Date != nil {
			s.Conventional = true
		}
		if cov := rs.coverageFor(row.reads, v.App, []*AppVersion{v}); !cov.fresh() {
			s.Status = StatusUnknown
			s.Missing = cov.missing()
		}
		out = append(out, s)
	}
	return out
}

func freezeOf(t *Train, v *AppVersion) *Day {
	if v.Milestone.Start != nil {
		return v.Milestone.Start
	}
	return t.Freeze
}

func dueOf(t *Train, v *AppVersion) *Day {
	if v.Milestone.Due != nil {
		return v.Milestone.Due
	}
	return t.Publish
}

func plus(d *Day, n int) *Day {
	if d == nil {
		return nil
	}
	x := d.Add(n)
	return &x
}

func freezeStage(t *Train, v *AppVersion, today Day) Stage {
	date := freezeOf(t, v)
	switch {
	case v.ReleaseMR != nil:
		mr := v.ReleaseMR.MR
		return Stage{Date: date, Status: StatusDone, Detail: fmt.Sprintf("Release MR !%d by %s, opened %s", mr.IID, mr.Author, mr.CreatedOn.label())}
	case date == nil:
		return Stage{Status: StatusTodo, Detail: "No freeze date on the milestone"}
	case today.After(*date):
		return Stage{Date: date, Status: StatusLate, Detail: fmt.Sprintf("Freeze was %d day(s) ago and no release MR is open", today.Sub(*date))}
	case today == *date:
		return Stage{Date: date, Status: StatusWaiting, Detail: "Freeze is today. Release MR not open yet"}
	}
	return Stage{Date: date, Status: StatusTodo}
}

func scanStage(t *Train, v *AppVersion, _ Day) Stage {
	date := plus(freezeOf(t, v), 1)
	r := v.ReleaseMR
	if r == nil {
		return Stage{Date: date, Status: StatusTodo, Detail: "Waits for the release MR"}
	}
	if r.MR.State == MRMerged {
		detail := "Release MR merged"
		if r.MR.MergedOn != nil {
			detail += " " + r.MR.MergedOn.label()
		}
		return Stage{Date: date, Status: StatusDone, Detail: detail}
	}
	if r.Detail == nil || r.Detail.Pipeline == nil {
		return Stage{Date: date, Status: StatusTodo, Detail: "No pipeline yet"}
	}
	switch p := r.Detail.Pipeline; p.Status {
	case "failed":
		failed := r.failedRequired()
		detail := "Pipeline failed: see pipeline"
		if len(failed) > 0 {
			detail = "Pipeline failed: " + strings.Join(failed, ", ")
		}
		return Stage{Date: date, Status: StatusFailed, Detail: detail}
	case "running", "pending":
		return Stage{Date: date, Status: StatusRunning, Detail: "Pipeline running"}
	case "success":
		return Stage{Date: date, Status: StatusDone, Detail: "Pipeline passed"}
	default:
		return Stage{Date: date, Status: StatusTodo, Detail: "Pipeline " + p.Status}
	}
}

func approveStage(t *Train, v *AppVersion, today Day) Stage {
	date := plus(freezeOf(t, v), 2)
	j := v.Jira
	if j == nil {
		return Stage{Date: date, Status: StatusTodo, Detail: "No Jira release found"}
	}
	ok, total := approvals(j)
	switch {
	case total == 0:
		status := StatusTodo
		if date != nil && today.After(*date) && t.Publish != nil && t.Publish.Before(today) {
			status = StatusWaiting
		}
		return Stage{Date: date, Status: status, Detail: "No approvers set on the Jira release"}
	case ok == total:
		return Stage{Date: date, Status: StatusDone, Detail: fmt.Sprintf("%d of %d approved", ok, total)}
	case j.Released:
		return Stage{Date: date, Status: StatusDone, Detail: fmt.Sprintf("Released with %d of %d approved", ok, total)}
	}
	return Stage{Date: date, Status: StatusWaiting, Detail: fmt.Sprintf("%d of %d approved", ok, total)}
}

func tagStage(t *Train, v *AppVersion, today Day) Stage {
	if v.Tag != nil {
		on := v.Tag.CommitOn
		return Stage{Date: &on, Status: StatusDone, Detail: fmt.Sprintf("Tag %s on %s", v.Tag.Name, on.label())}
	}
	due := dueOf(t, v)
	date := plus(due, -1)
	if v.ReleaseMR != nil && v.ReleaseMR.MR.State == MRMerged {
		return Stage{Date: date, Status: StatusWaiting, Detail: "Merged, no version tag yet", Conventional: true}
	}
	if due != nil && today.After(*due) {
		return Stage{Date: date, Status: StatusLate, Conventional: true}
	}
	return Stage{Date: date, Status: StatusTodo, Conventional: true}
}

func publishStage(t *Train, v *AppVersion, today Day) Stage {
	date := dueOf(t, v)
	jiraText := "No Jira release"
	if v.Jira != nil {
		jiraText = "Jira release NOT marked released"
		if v.Jira.Released {
			jiraText = "Jira release marked released"
		}
	}
	switch {
	case date == nil:
		return Stage{Status: StatusTodo, Detail: "No due date on the milestone"}
	case v.Tag != nil && !today.Before(*date):
		return Stage{Date: date, Status: StatusDone, Detail: fmt.Sprintf("Tag %s exists. %s", v.Tag.Name, jiraText)}
	case today.After(*date):
		return Stage{Date: date, Status: StatusLate, Detail: "Past the due date and no tag " + v.Version}
	}
	return Stage{Date: date, Status: StatusTodo, Detail: jiraText}
}

func forceStage(_ *Train, v *AppVersion, _ Day) Stage {
	fu := v.ForceUpdate
	if fu == nil {
		return Stage{Status: StatusNotObservable, Detail: "No force-update ticket matches this version"}
	}
	date := fu.CreatedOn
	detail := fu.Key + " " + fu.Status
	if fu.Due != nil {
		date = *fu.Due
		detail += ", due " + fu.Due.label()
	}
	status := StatusWaiting
	if fu.StatusCategory == StatusCategoryDone {
		status = StatusDone
	}
	return Stage{Date: &date, Status: status, Detail: detail}
}

func approvals(j *JiraVersionFact) (ok, total int) {
	for _, a := range j.Approvers {
		if a.Status == ApproverApproved {
			ok++
		}
	}
	return ok, len(j.Approvers)
}

func (r *ReleaseMR) failedRequired() []string { return r.failedJobs(false) }

func (r *ReleaseMR) failedAllowed() []string { return r.failedJobs(true) }

func (r *ReleaseMR) failedJobs(allowed bool) []string {
	if r.Jobs == nil {
		return nil
	}
	var out []string
	for _, j := range r.Jobs.Jobs {
		if j.Status == "failed" && j.AllowFailure == allowed {
			out = append(out, j.Name)
		}
	}
	return out
}
