package releasetrain

import (
	"fmt"
	"strconv"
	"time"
)

// MilestoneFact is a GitLab milestone of one app's project.
type MilestoneFact struct {
	Project     int    `json:"project"`
	ID          int    `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	State       string `json:"state"`
	Start       *Day   `json:"start,omitempty"`
	Due         *Day   `json:"due,omitempty"`
	URL         string `json:"url"`
}

// MRFact is one merge request from the project's MR list.
type MRFact struct {
	Project        int    `json:"project"`
	IID            int    `json:"iid"`
	Title          string `json:"title"`
	SourceBranch   string `json:"sourceBranch"`
	TargetBranch   string `json:"targetBranch"`
	State          string `json:"state"`
	Author         string `json:"author"`
	AuthorUsername string `json:"authorUsername,omitempty"`
	CreatedOn      Day    `json:"createdOn"`
	MergedOn       *Day   `json:"mergedOn,omitempty"`
	MilestoneID    *int   `json:"milestoneId,omitempty"`
	URL            string `json:"url"`
}

// MR states as GitLab reports them.
const (
	MROpened = "opened"
	MRMerged = "merged"
)

// MRDetailFact is the single-MR read that carries the head pipeline and conflicts.
type MRDetailFact struct {
	Project   int           `json:"project"`
	IID       int           `json:"iid"`
	Conflicts bool          `json:"conflicts"`
	Pipeline  *PipelineFact `json:"pipeline,omitempty"`
}

// PipelineFact is an MR's head pipeline.
type PipelineFact struct {
	ID       int    `json:"id"`
	Status   string `json:"status"`
	URL      string `json:"url"`
	Duration *int   `json:"durationSeconds,omitempty"`
}

// JobsFact holds every job of one pipeline; its absence means the jobs were not fetched.
type JobsFact struct {
	Project  int       `json:"project"`
	Pipeline int       `json:"pipeline"`
	Jobs     []JobFact `json:"jobs"`
}

// JobFact is one pipeline job.
type JobFact struct {
	Name         string `json:"name"`
	Status       string `json:"status"`
	AllowFailure bool   `json:"allowFailure"`
}

// TagFact is a repository tag.
type TagFact struct {
	Project  int    `json:"project"`
	Name     string `json:"name"`
	CommitOn Day    `json:"commitOn"`
	URL      string `json:"url"`
}

// CompareFact is the commit list of From..To.
type CompareFact struct {
	Project int          `json:"project"`
	From    string       `json:"from"`
	To      string       `json:"to"`
	Commits []CommitFact `json:"commits"`
	URL     string       `json:"url"`
}

// CommitFact is one commit of a compare.
type CommitFact struct {
	Title     string `json:"title"`
	CreatedOn Day    `json:"createdOn"`
	URL       string `json:"url"`
}

// GitLab fact kinds.
var (
	KindMilestones = newKind[MilestoneFact]("gitlab.milestones", Snapshotted, 10*time.Minute, perApp)
	KindMRs        = newKind[MRFact]("gitlab.mrs", Incremental, 2*time.Minute, perApp)
	KindMRDetail   = newKind[MRDetailFact]("gitlab.mr_detail", Snapshotted, time.Minute, perEntity)
	KindJobs       = newKind[JobsFact]("gitlab.jobs", Snapshotted, time.Minute, perEntity)
	KindTags       = newKind[TagFact]("gitlab.tags", Snapshotted, 10*time.Minute, perApp)
	KindCompare    = newKind[CompareFact]("gitlab.compare", Snapshotted, 30*time.Minute, perEntity)
)

// ItemKey keys a MilestoneFact within its resource.
func (f MilestoneFact) ItemKey() string { return strconv.Itoa(f.ID) }

// ItemKey keys an MRFact within its resource.
func (f MRFact) ItemKey() string { return strconv.Itoa(f.IID) }

// ItemKey keys an MRDetailFact within its resource.
func (f MRDetailFact) ItemKey() string { return strconv.Itoa(f.IID) }

// ItemKey keys a JobsFact within its resource.
func (f JobsFact) ItemKey() string { return strconv.Itoa(f.Pipeline) }

// ItemKey keys a TagFact within its resource.
func (f TagFact) ItemKey() string { return f.Name }

// ItemKey keys a CompareFact within its resource.
func (f CompareFact) ItemKey() string { return f.From + ".." + f.To }

// ProjectScope is the scope of a per-project resource.
func ProjectScope(project int) Scope { return Scope(fmt.Sprintf("p:%d", project)) }

// MRScope is the scope of one MR's detail.
func MRScope(project, iid int) Scope { return Scope(fmt.Sprintf("p:%d!%d", project, iid)) }

// PipelineScope is the scope of one pipeline's jobs.
func PipelineScope(project, pipeline int) Scope {
	return Scope(fmt.Sprintf("p:%d:pipeline:%d", project, pipeline))
}

// CompareScope is the scope of one compare.
func CompareScope(project int, from, to string) Scope {
	return Scope(fmt.Sprintf("p:%d:%s..%s", project, from, to))
}
