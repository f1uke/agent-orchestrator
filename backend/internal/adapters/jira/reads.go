package jira

// Read-only Jira reads for the release-train monitor. Unlike SearchIssues
// they are complete-or-error: a capped or broken walk returns ErrIncomplete,
// never a silent partial, and a 429 is a *RateLimitError.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/atlassian"
)

// ErrIncomplete is a paged read that hit its page cap before the last page,
// or failed on a later page. It wraps the later page's error.
var ErrIncomplete = errors.New("jira: incomplete read")

// ErrRateLimited is the class of a 429 from the new reads.
var ErrRateLimited = atlassian.ErrRateLimited

// RateLimitError is a 429 from the new reads, with the server's wait.
type RateLimitError = atlassian.RateLimitError

// Page caps used when a read's maxPages is zero.
const (
	defaultVersionPages = 10
	defaultSearchPages  = 10
	defaultSprintPages  = 5
	offsetPageSize      = 50
	maxErrorMessage     = 300
)

var (
	projectKeyPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)
	numericIDPattern  = regexp.MustCompile(`^\d+$`)
)

// Version is a Jira project version with its approvers and issue counts.
// ReleaseDate is Jira's YYYY-MM-DD, empty when unset.
type Version struct {
	ID          string
	Name        string
	Description string
	Released    bool
	ReleaseDate string
	Approvers   []VersionApprover
	IssueCounts IssueStatusCounts
	URL         string
}

// VersionApprover is one approver on a version.
type VersionApprover struct {
	AccountID string `json:"accountId"`
	Status    string `json:"status"`
}

// IssueStatusCounts is the per-category issue count Jira reports for a version.
type IssueStatusCounts struct {
	Unmapped   int `json:"unmapped"`
	ToDo       int `json:"toDo"`
	InProgress int `json:"inProgress"`
	Done       int `json:"done"`
}

// ProjectVersions reads every version of a project with approvers and issue
// counts, or ErrIncomplete.
func (c *Client) ProjectVersions(ctx context.Context, projectKey string, maxPages int) ([]Version, error) {
	if !projectKeyPattern.MatchString(projectKey) {
		return nil, fmt.Errorf("%w: project key %q", ErrBadRequest, projectKey)
	}
	cfg, err := c.config()
	if err != nil {
		return nil, err
	}
	q := url.Values{}
	q.Set("expand", "approvers,issuesstatus")
	type rawVersion struct {
		ID          string            `json:"id"`
		Name        string            `json:"name"`
		Description string            `json:"description"`
		Released    bool              `json:"released"`
		ReleaseDate string            `json:"releaseDate"`
		Approvers   []VersionApprover `json:"approvers"`
		IssueCounts IssueStatusCounts `json:"issuesStatusForFixVersion"`
	}
	path := "/rest/api/3/project/" + url.PathEscape(projectKey) + "/version"
	raw, err := offsetPages[rawVersion](ctx, c, cfg, path, q, pagesOr(maxPages, defaultVersionPages), "versions")
	if err != nil {
		return nil, err
	}
	out := make([]Version, 0, len(raw))
	for _, v := range raw {
		out = append(out, Version{
			ID: v.ID, Name: v.Name, Description: v.Description, Released: v.Released, ReleaseDate: v.ReleaseDate,
			Approvers: v.Approvers, IssueCounts: v.IssueCounts,
			URL: cfg.BaseURL + "/projects/" + url.PathEscape(projectKey) + "/versions/" + url.PathEscape(v.ID),
		})
	}
	return out, nil
}

// RelatedWorkLink is one related-work link on a version.
type RelatedWorkLink struct {
	Category string `json:"category"`
	Title    string `json:"title"`
	URL      string `json:"url"`
}

// VersionRelatedWork reads a version's related-work links.
func (c *Client) VersionRelatedWork(ctx context.Context, versionID string) ([]RelatedWorkLink, error) {
	if !numericIDPattern.MatchString(versionID) {
		return nil, fmt.Errorf("%w: version id %q", ErrBadRequest, versionID)
	}
	var out []RelatedWorkLink
	err := c.getJSON(ctx, "/rest/api/3/version/"+versionID+"/relatedwork", nil, "related work", &out)
	return out, err
}

// IssueRowFields is what SearchAllFields asks for when the caller names none.
var IssueRowFields = []string{"summary", "status", "issuetype", "assignee", "fixVersions", "parent", "subtasks", "labels", "duedate", "created"}

// IssueRow is one search row with the fields the release train reads.
// DueDate is Jira's YYYY-MM-DD, empty when unset; Created is zero when absent.
type IssueRow struct {
	Key            string
	Summary        string
	Status         string
	StatusCategory string
	IssueType      string
	Subtask        bool
	Assignee       *Assignee
	FixVersionIDs  []string
	ParentKey      string
	ParentType     string
	SubtaskKeys    []string
	Labels         []string
	DueDate        string
	Created        time.Time
	URL            string
}

// Assignee is an issue's assignee.
type Assignee struct {
	AccountID   string `json:"accountId"`
	DisplayName string `json:"displayName"`
}

// SearchAllFields runs JQL over the enhanced search endpoint and returns every
// row, or ErrIncomplete when a later page fails or maxPages is reached first.
// fields defaults to IssueRowFields.
func (c *Client) SearchAllFields(ctx context.Context, jql string, fields []string, maxPages int) ([]IssueRow, error) {
	jql = strings.TrimSpace(jql)
	if jql == "" {
		return nil, fmt.Errorf("%w: empty search query", errBadQuery)
	}
	if len(fields) == 0 {
		fields = IssueRowFields
	}
	cfg, err := c.config()
	if err != nil {
		return nil, err
	}
	pages := pagesOr(maxPages, defaultSearchPages)
	spec := searchSpec{
		path: "/rest/api/3/search/jql", jql: jql, fields: strings.Join(fields, ","),
		limit: pages * searchPageSize, maxPages: pages, enhanced: true,
		classify:  func(resp *http.Response) error { return readStatusError(resp, "search", errBadQuery) },
		transport: func(err error) error { return transportError("search", err) },
	}
	raw, res := c.searchAll(ctx, cfg, spec)
	if err := completeness(res.err, len(raw) > 0, res.complete, "search", pages); err != nil {
		return nil, err
	}
	out := make([]IssueRow, 0, len(raw))
	for _, it := range raw {
		row, err := issueRow(cfg, it)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, nil
}

// jiraTimeLayout is how Jira's REST v3 writes issue timestamps (no colon in
// the zone offset), which time.Time's RFC 3339 decoder rejects.
const jiraTimeLayout = "2006-01-02T15:04:05.000-0700"

func issueRow(cfg atlassian.Config, it rawSearchIssue) (IssueRow, error) {
	var f struct {
		Summary string `json:"summary"`
		Status  struct {
			Name           string `json:"name"`
			StatusCategory struct {
				Key string `json:"key"`
			} `json:"statusCategory"`
		} `json:"status"`
		IssueType struct {
			Name    string `json:"name"`
			Subtask bool   `json:"subtask"`
		} `json:"issuetype"`
		Assignee    *Assignee `json:"assignee"`
		FixVersions []struct {
			ID string `json:"id"`
		} `json:"fixVersions"`
		Parent *struct {
			Key    string `json:"key"`
			Fields struct {
				IssueType struct {
					Name string `json:"name"`
				} `json:"issuetype"`
			} `json:"fields"`
		} `json:"parent"`
		Subtasks []struct {
			Key string `json:"key"`
		} `json:"subtasks"`
		Labels  []string `json:"labels"`
		DueDate string   `json:"duedate"`
		Created string   `json:"created"`
	}
	fieldsJSON, err := json.Marshal(it.Fields)
	if err == nil {
		err = json.Unmarshal(fieldsJSON, &f)
	}
	if err != nil {
		return IssueRow{}, fmt.Errorf("%w: decode %s: %w", ErrUnavailable, it.Key, err)
	}
	row := IssueRow{
		Key: it.Key, Summary: f.Summary, Status: f.Status.Name, StatusCategory: f.Status.StatusCategory.Key,
		IssueType: f.IssueType.Name, Subtask: f.IssueType.Subtask, Assignee: f.Assignee, Labels: f.Labels,
		DueDate: f.DueDate, URL: cfg.BaseURL + "/browse/" + it.Key,
	}
	for _, v := range f.FixVersions {
		row.FixVersionIDs = append(row.FixVersionIDs, v.ID)
	}
	for _, s := range f.Subtasks {
		row.SubtaskKeys = append(row.SubtaskKeys, s.Key)
	}
	if f.Parent != nil {
		row.ParentKey, row.ParentType = f.Parent.Key, f.Parent.Fields.IssueType.Name
	}
	if f.Created != "" {
		created, err := time.Parse(jiraTimeLayout, f.Created)
		if err != nil {
			return IssueRow{}, fmt.Errorf("%w: %s created %q: %w", ErrUnavailable, it.Key, f.Created, err)
		}
		row.Created = created
	}
	return row, nil
}

// BoardSprint is one sprint of an agile board. Dates are nil for a sprint
// that has not been planned.
type BoardSprint struct {
	ID        int        `json:"id"`
	Name      string     `json:"name"`
	State     string     `json:"state"`
	StartDate *time.Time `json:"startDate"`
	EndDate   *time.Time `json:"endDate"`
}

// BoardSprints reads a board's sprints in the given states (all when empty),
// or ErrIncomplete.
func (c *Client) BoardSprints(ctx context.Context, boardID int, states []string, maxPages int) ([]BoardSprint, error) {
	cfg, err := c.config()
	if err != nil {
		return nil, err
	}
	q := url.Values{}
	if len(states) > 0 {
		q.Set("state", strings.Join(states, ","))
	}
	path := "/rest/agile/1.0/board/" + strconv.Itoa(boardID) + "/sprint"
	return offsetPages[BoardSprint](ctx, c, cfg, path, q, pagesOr(maxPages, defaultSprintPages), "sprints")
}

// AgileBoard is an agile board's identity.
type AgileBoard struct {
	Name string `json:"name"`
}

// Board reads an agile board.
func (c *Client) Board(ctx context.Context, boardID int) (AgileBoard, error) {
	var out AgileBoard
	err := c.getJSON(ctx, "/rest/agile/1.0/board/"+strconv.Itoa(boardID), nil, "board", &out)
	return out, err
}

// UserProfile is a Jira user's name and 48x48 avatar URL.
type UserProfile struct {
	AccountID   string
	DisplayName string
	AvatarURL   string
}

// User reads one Jira user by account id.
func (c *Client) User(ctx context.Context, accountID string) (UserProfile, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return UserProfile{}, fmt.Errorf("%w: empty account id", ErrBadRequest)
	}
	q := url.Values{}
	q.Set("accountId", accountID)
	var raw struct {
		AccountID   string            `json:"accountId"`
		DisplayName string            `json:"displayName"`
		AvatarURLs  map[string]string `json:"avatarUrls"`
	}
	if err := c.getJSON(ctx, "/rest/api/3/user", q, "user", &raw); err != nil {
		return UserProfile{}, err
	}
	return UserProfile{AccountID: raw.AccountID, DisplayName: raw.DisplayName, AvatarURL: raw.AvatarURLs["48x48"]}, nil
}

// offsetPages walks a startAt/isLast list of 50 per page.
func offsetPages[T any](ctx context.Context, c *Client, cfg atlassian.Config, path string, q url.Values, maxPages int, what string) ([]T, error) {
	var out []T
	for page := 0; page < maxPages; page++ {
		pq := url.Values{}
		for k, v := range q {
			pq[k] = v
		}
		pq.Set("startAt", strconv.Itoa(len(out)))
		pq.Set("maxResults", strconv.Itoa(offsetPageSize))
		var body struct {
			Values []T  `json:"values"`
			IsLast bool `json:"isLast"`
		}
		if err := c.getJSONWith(ctx, cfg, path, pq, what, &body); err != nil {
			return nil, completeness(err, page > 0, false, what, maxPages)
		}
		out = append(out, body.Values...)
		if body.IsLast || len(body.Values) == 0 {
			return out, nil
		}
	}
	return nil, completeness(nil, true, false, what, maxPages)
}

// completeness turns how a walk ended into the complete-or-error contract. A
// first-page failure is returned as is; a later one is ErrIncomplete.
func completeness(err error, readSome, complete bool, what string, maxPages int) error {
	switch {
	case err != nil && readSome:
		return fmt.Errorf("%w: %s: %w", ErrIncomplete, what, err)
	case err != nil:
		return err
	case !complete:
		return fmt.Errorf("%w: %s at %d pages", ErrIncomplete, what, maxPages)
	}
	return nil
}

func (c *Client) getJSON(ctx context.Context, path string, q url.Values, what string, out any) error {
	cfg, err := c.config()
	if err != nil {
		return err
	}
	return c.getJSONWith(ctx, cfg, path, q, what, out)
}

func (c *Client) getJSONWith(ctx context.Context, cfg atlassian.Config, path string, q url.Values, what string, out any) error {
	u := cfg.BaseURL + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := atlassian.NewRequest(ctx, cfg, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	resp, err := c.httpDo(req)
	if err != nil {
		return transportError(what, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := readStatusError(resp, what, errBadRequest); err != nil {
		return err
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("%w: decode %s: %w", ErrUnavailable, what, err)
	}
	return nil
}

// transportError drops the request URL that *url.Error carries, so no query
// string reaches an error message.
func transportError(what string, err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	return fmt.Errorf("%w: %s: %w", ErrUnavailable, what, err)
}

// readStatusError classifies a new read's response; badRequest is the
// sentinel a 400 maps to. The server message is capped.
func readStatusError(resp *http.Response, what string, badRequest error) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	snippet := capMessage(errorSnippet(resp.Body))
	switch resp.StatusCode {
	case http.StatusTooManyRequests:
		return &RateLimitError{RetryAfter: atlassian.ParseRetryAfter(resp.Header.Get("Retry-After"), time.Now()), Message: snippet}
	case http.StatusBadRequest:
		return fmt.Errorf("%w: %s%s", badRequest, what, suffix(snippet))
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("%w: %s%s", ErrAuthFailed, what, suffix(snippet))
	case http.StatusNotFound:
		return fmt.Errorf("%w: %s", ErrNotFound, what)
	default:
		return fmt.Errorf("%w: %s: HTTP %d%s", ErrUnavailable, what, resp.StatusCode, suffix(snippet))
	}
}

func capMessage(s string) string {
	if len(s) <= maxErrorMessage {
		return s
	}
	return s[:maxErrorMessage]
}

func pagesOr(n, def int) int {
	if n > 0 {
		return n
	}
	return def
}
