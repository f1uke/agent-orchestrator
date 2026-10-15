package gitlab

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"time"
)

// ErrIncomplete is a list read that stopped at its page cap on a full page,
// so the rows it read may not be all of them.
var ErrIncomplete = errors.New("gitlab scm: incomplete read")

// Page caps used when a read's MaxPages is zero.
const (
	defaultMilestonePages = 5
	defaultTagPages       = 3
	defaultJobPages       = 5
)

// Milestone is one project milestone. StartDate and DueDate are GitLab's
// YYYY-MM-DD strings, empty when unset.
type Milestone struct {
	ID          int    `json:"id"`
	IID         int    `json:"iid"`
	Title       string `json:"title"`
	Description string `json:"description"`
	State       string `json:"state"`
	StartDate   string `json:"start_date"`
	DueDate     string `json:"due_date"`
	WebURL      string `json:"web_url"`
}

// ListMilestonesOptions filters ListMilestones. Zero values send no filter.
type ListMilestonesOptions struct {
	UpdatedAfter time.Time
	State        string
	MaxPages     int
}

// ListMilestones reads every milestone of a project, or ErrIncomplete.
func (c *Client) ListMilestones(ctx context.Context, project int, opts ListMilestonesOptions) ([]Milestone, error) {
	q := url.Values{}
	if !opts.UpdatedAfter.IsZero() {
		q.Set("updated_after", opts.UpdatedAfter.UTC().Format(time.RFC3339))
	}
	if opts.State != "" {
		q.Set("state", opts.State)
	}
	return completeList[Milestone](ctx, c, projectPath(project, "milestones"), q, pagesOr(opts.MaxPages, defaultMilestonePages), "milestones")
}

// Tag is one repository tag with the commit it points at.
type Tag struct {
	Name   string `json:"name"`
	Target string `json:"target"`
	Commit Commit `json:"commit"`
}

// Commit is a commit as tags and compares report it.
type Commit struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	CreatedAt time.Time `json:"created_at"`
	WebURL    string    `json:"web_url"`
}

// ListTagsOptions bounds ListTags. OrderBy is GitLab's order_by (name,
// updated, version); empty keeps the server default.
type ListTagsOptions struct {
	MaxPages int
	OrderBy  string
}

// ListTags reads a project's tags, or ErrIncomplete.
func (c *Client) ListTags(ctx context.Context, project int, opts ListTagsOptions) ([]Tag, error) {
	q := url.Values{}
	if opts.OrderBy != "" {
		q.Set("order_by", opts.OrderBy)
	}
	return completeList[Tag](ctx, c, projectPath(project, "repository/tags"), q, pagesOr(opts.MaxPages, defaultTagPages), "tags")
}

// Comparison is the commit list of from..to.
type Comparison struct {
	Commits []Commit `json:"commits"`
	WebURL  string   `json:"web_url"`
}

// Compare lists the commits on to that are not on from (straight, from..to).
func (c *Client) Compare(ctx context.Context, project int, from, to string) (Comparison, error) {
	q := url.Values{}
	q.Set("from", from)
	q.Set("to", to)
	q.Set("straight", "true")
	var out Comparison
	err := c.getJSON(ctx, projectPath(project, "repository/compare"), q, "compare", &out)
	return out, err
}

// MergeRequest is one row of a project's MR list.
type MergeRequest struct {
	IID          int        `json:"iid"`
	Title        string     `json:"title"`
	State        string     `json:"state"`
	SourceBranch string     `json:"source_branch"`
	TargetBranch string     `json:"target_branch"`
	Author       User       `json:"author"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	MergedAt     *time.Time `json:"merged_at"`
	MilestoneID  *int       `json:"-"`
	WebURL       string     `json:"web_url"`
}

// UnmarshalJSON lifts milestone.id, which GitLab nests, onto MilestoneID.
func (m *MergeRequest) UnmarshalJSON(b []byte) error {
	type plain MergeRequest
	var raw struct {
		plain
		Milestone *struct {
			ID int `json:"id"`
		} `json:"milestone"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	*m = MergeRequest(raw.plain)
	if raw.Milestone != nil {
		id := raw.Milestone.ID
		m.MilestoneID = &id
	}
	return nil
}

// ListMRsOptions selects one page of a project's MRs ordered by updated_at
// ascending, for a keyset cursor over UpdatedAfter (inclusive) and Page.
type ListMRsOptions struct {
	UpdatedAfter time.Time
	Page         int
}

// MRPage is one page of MRs. Full means the page held per_page rows, so the
// next page may hold more.
type MRPage struct {
	Items []MergeRequest
	Full  bool
}

// ListMergeRequestsPage reads one page of every MR (any state, any scope)
// updated at or after opts.UpdatedAfter.
func (c *Client) ListMergeRequestsPage(ctx context.Context, project int, opts ListMRsOptions) (MRPage, error) {
	page := opts.Page
	if page < 1 {
		page = 1
	}
	q := url.Values{}
	q.Set("state", "all")
	q.Set("scope", "all")
	q.Set("order_by", "updated_at")
	q.Set("sort", "asc")
	if !opts.UpdatedAfter.IsZero() {
		q.Set("updated_after", opts.UpdatedAfter.UTC().Format(time.RFC3339))
	}
	q.Set("per_page", strconv.Itoa(perPage))
	q.Set("page", strconv.Itoa(page))
	var items []MergeRequest
	if err := c.getJSON(ctx, projectPath(project, "merge_requests"), q, "merge requests", &items); err != nil {
		return MRPage{}, err
	}
	return MRPage{Items: items, Full: len(items) >= perPage}, nil
}

// MRDetail is the part of a single MR that the list rows lack.
type MRDetail struct {
	HeadPipeline        *Pipeline `json:"head_pipeline"`
	DetailedMergeStatus string    `json:"detailed_merge_status"`
	HasConflicts        bool      `json:"has_conflicts"`
}

// Pipeline is an MR's head pipeline. Duration is nil while it runs.
type Pipeline struct {
	ID        int       `json:"id"`
	Status    string    `json:"status"`
	WebURL    string    `json:"web_url"`
	Duration  *int      `json:"duration"`
	CreatedAt time.Time `json:"created_at"`
}

// MergeRequestDetail reads one MR's head pipeline and merge status.
func (c *Client) MergeRequestDetail(ctx context.Context, project, iid int) (MRDetail, error) {
	var out MRDetail
	err := c.getJSON(ctx, projectPath(project, "merge_requests/"+strconv.Itoa(iid)), nil, "merge request", &out)
	return out, err
}

// Job is one pipeline job.
type Job struct {
	ID           int    `json:"id"`
	Name         string `json:"name"`
	Status       string `json:"status"`
	AllowFailure bool   `json:"allow_failure"`
	Stage        string `json:"stage"`
}

// ListPipelineJobs reads a pipeline's latest jobs (retries excluded), or
// ErrIncomplete.
func (c *Client) ListPipelineJobs(ctx context.Context, project, pipelineID, maxPages int) ([]Job, error) {
	q := url.Values{}
	q.Set("include_retried", "false")
	path := projectPath(project, "pipelines/"+strconv.Itoa(pipelineID)+"/jobs")
	return completeList[Job](ctx, c, path, q, pagesOr(maxPages, defaultJobPages), "pipeline jobs")
}

// User is a GitLab account.
type User struct {
	Username string `json:"username"`
	Name     string `json:"name"`
}

// CurrentUser reads the account that owns the token.
func (c *Client) CurrentUser(ctx context.Context) (User, error) {
	var out User
	err := c.getJSON(ctx, "user", nil, "user", &out)
	return out, err
}

func completeList[T any](ctx context.Context, c *Client, path string, q url.Values, maxPages int, what string) ([]T, error) {
	rows, capped, err := walkPages[T](ctx, c, path, q, maxPages, what)
	if err != nil {
		return nil, err
	}
	if capped {
		return nil, fmt.Errorf("%w: %s at %d pages", ErrIncomplete, what, maxPages)
	}
	return rows, nil
}

func (c *Client) getJSON(ctx context.Context, path string, q url.Values, what string, out any) error {
	resp, err := c.doREST(ctx, path, q)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(resp.Body, out); err != nil {
		return fmt.Errorf("gitlab scm: decode %s: %w", what, err)
	}
	return nil
}

func projectPath(project int, rest string) string {
	return "projects/" + url.PathEscape(strconv.Itoa(project)) + "/" + rest
}

func pagesOr(n, def int) int {
	if n > 0 {
		return n
	}
	return def
}
