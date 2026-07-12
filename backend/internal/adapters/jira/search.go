package jira

// Cross-project issue search + the project list, via Jira Cloud REST v3. jira-cli
// `issue list` is unusable here (it scopes to the configured scrum board's active
// sprint and returns empty even with a project JQL), so search bypasses it and
// goes straight to REST — reusing the same auth seam as the status transitions
// (transitions.go): base URL + login from env/jira-cli config, API token from
// AO_JIRA_TOKEN → JIRA_API_TOKEN. Read-only (GET only).
//
// Endpoint choice: Jira Cloud's classic `/rest/api/3/search` is deprecated and
// removed on current instances in favor of the enhanced `/rest/api/3/search/jql`.
// We call the enhanced endpoint first and fall back to the classic one only when
// it is absent (404/410), so both old and new instances work.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// ErrBadQuery is a rejected search — usually invalid JQL (e.g. a project key that
// does not exist). The service uses it to fall back from a project-scoped guess
// to a plain text search. The underlying sentinel lives in client.go.
var ErrBadQuery = errBadQuery

// IssueSummary is a lightweight issue row for search results and pickers — the
// structured fields needed to render and pick an issue, without the full
// description/subtasks the display projection (Issue) carries.
type IssueSummary struct {
	Key               string
	Type              string
	Title             string
	Status            string
	StatusCategory    string // Jira category key: new|indeterminate|done
	StatusColor       string
	Assignee          string
	AssigneeAccountId string     // opaque Jira accountId, so the UI can filter by assignee server-side (JQL)
	Parent            *ParentRef // set for subtasks / epic children, so Browse Jira can nest under the parent
	Sprint            *Sprint    // current/most-relevant sprint, for Browse Jira grouping (nil = none)
	URL               string     // human browse URL, derived from the site base
}

// ParentRef is a row's parent issue (set for subtasks and epic children) so Browse
// Jira can nest a subtask beneath its parent like the Jira backlog.
type ParentRef struct {
	Key   string
	Title string
}

// ProjectRef is one Jira project for the project picker (Browse Jira, Slice 5;
// used server-side here to resolve a bare project-key query).
type ProjectRef struct {
	Key  string
	Name string
}

// Ask for every field (like the single-issue display path) so the Agile sprint
// custom-field — whose id varies per instance, and which some instances do NOT
// expose under *navigable — is reliably present for detectSprint to find, letting
// Browse Jira group rows by sprint. The row set is bounded (SearchMaxResults) so
// the heavier *all payload stays acceptable for a manual, user-initiated browse.
const searchFields = "*all"

const (
	// searchPageSize is the per-request page for the paginated search. Both the
	// enhanced and classic endpoints accept maxResults up to 100; 100 keeps the
	// paging round-trips low while the *all payload per page stays bounded.
	searchPageSize = 100
	// SearchMaxResults caps how many rows a single browse fetch collects across
	// pages. It bounds the *all payload for a huge, unfiltered project while
	// sitting far above any one person's issue count — the assignee/type filters
	// are pushed into the JQL, so a filtered set is narrow and fits in one page.
	SearchMaxResults = 200
)

// SearchIssues runs a JQL query and returns matching issues, paging until it has
// `limit` rows or the result set is exhausted. The JQL is built by the service —
// this adapter is a dumb executor so the query semantics stay testable in one
// place. limit is clamped to a sane window (SearchMaxResults).
func (c *Client) SearchIssues(ctx context.Context, jql string, limit int) ([]IssueSummary, error) {
	jql = strings.TrimSpace(jql)
	if jql == "" {
		return nil, fmt.Errorf("%w: empty search query", errBadQuery)
	}
	if limit <= 0 || limit > SearchMaxResults {
		limit = SearchMaxResults
	}
	cfg, err := c.config()
	if err != nil {
		return nil, err
	}
	// Enhanced endpoint (nextPageToken paging) first; fall back to the classic one
	// (startAt paging) only when it is absent (404/410).
	issues, status, err := c.searchAll(ctx, cfg, "/rest/api/3/search/jql", jql, limit, true)
	if err == nil {
		return issues, nil
	}
	if status == http.StatusNotFound || status == http.StatusGone {
		issues, _, err = c.searchAll(ctx, cfg, "/rest/api/3/search", jql, limit, false)
		return issues, err
	}
	return nil, err
}

// searchAll pages one endpoint until it has `limit` rows or the endpoint is
// exhausted. enhanced selects token paging (nextPageToken) over classic offset
// paging (startAt). It returns the FIRST page's HTTP status so SearchIssues can
// decide whether to fall back to the classic endpoint. A failure mid-pagination
// (after at least one page succeeded) returns the rows already gathered rather
// than erroring, so a transient later-page hiccup degrades to a partial list.
func (c *Client) searchAll(ctx context.Context, cfg restConfig, path, jql string, limit int, enhanced bool) ([]IssueSummary, int, error) {
	var out []IssueSummary
	var pageToken string
	startAt := 0
	firstStatus := 0
	for len(out) < limit {
		pageLimit := limit - len(out)
		if pageLimit > searchPageSize {
			pageLimit = searchPageSize
		}
		page, next, status, err := c.searchPage(ctx, cfg, path, jql, pageLimit, enhanced, pageToken, startAt)
		if firstStatus == 0 {
			firstStatus = status
		}
		if err != nil {
			if len(out) > 0 {
				return out, firstStatus, nil // partial: keep the pages we did get
			}
			return nil, firstStatus, err
		}
		out = append(out, page...)
		if len(page) == 0 {
			break // defensive: no progress, avoid an infinite loop
		}
		if enhanced {
			if next == "" {
				break // no nextPageToken → last page
			}
			pageToken = next
		} else {
			startAt += len(page)
			if len(page) < pageLimit {
				break // short page → last page
			}
		}
	}
	return out, firstStatus, nil
}

// searchPage hits one search endpoint for a single page. It returns the page's
// issues, the nextPageToken (enhanced endpoint only; "" when there is no further
// page), and the HTTP status so searchAll can page and fall back.
func (c *Client) searchPage(ctx context.Context, cfg restConfig, path, jql string, limit int, enhanced bool, pageToken string, startAt int) ([]IssueSummary, string, int, error) {
	q := url.Values{}
	q.Set("jql", jql)
	q.Set("fields", searchFields)
	q.Set("maxResults", strconv.Itoa(limit))
	if enhanced {
		if pageToken != "" {
			q.Set("nextPageToken", pageToken)
		}
	} else {
		q.Set("startAt", strconv.Itoa(startAt))
	}
	req, err := newJiraRequest(ctx, cfg, http.MethodGet, cfg.baseURL+path+"?"+q.Encode(), nil)
	if err != nil {
		return nil, "", 0, err
	}
	resp, err := c.httpDo(req)
	if err != nil {
		return nil, "", 0, fmt.Errorf("%w: search: %w", ErrUnavailable, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := searchStatusError(resp); err != nil {
		return nil, "", resp.StatusCode, err
	}
	// Decode fields as a raw map (like the display path) so the sprint custom-field
	// — whose id varies per instance — can be located by detectSprint alongside the
	// known summary/type/status/assignee fields.
	var payload struct {
		Issues []struct {
			Key    string                     `json:"key"`
			Fields map[string]json.RawMessage `json:"fields"`
		} `json:"issues"`
		NextPageToken string `json:"nextPageToken"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, "", resp.StatusCode, fmt.Errorf("%w: decode search: %w", ErrUnavailable, err)
	}
	out := make([]IssueSummary, 0, len(payload.Issues))
	for _, it := range payload.Issues {
		f := it.Fields
		assignee := decodeNamed(f["assignee"])
		sum := IssueSummary{
			Key:               it.Key,
			Type:              decodeNamed(f["issuetype"]).Name,
			Title:             decodeString(f["summary"]),
			Assignee:          assignee.DisplayName,
			AssigneeAccountId: assignee.AccountId,
			Parent:            decodeParent(f["parent"]),
			Sprint:            c.detectSprint(f),
			URL:               cfg.baseURL + "/browse/" + it.Key,
		}
		if st := decodeStatus(f["status"]); st != nil {
			sum.Status = st.Name
			sum.StatusCategory = st.StatusCategory.Key
			sum.StatusColor = st.StatusCategory.ColorName
		}
		out = append(out, sum)
	}
	return out, payload.NextPageToken, resp.StatusCode, nil
}

// ListProjects returns the user's Jira projects, optionally filtered by a query
// (matched against key/name by Jira). Backs the project picker; also used
// server-side to confirm a bare-word query really is a project key.
func (c *Client) ListProjects(ctx context.Context, query string) ([]ProjectRef, error) {
	cfg, err := c.config()
	if err != nil {
		return nil, err
	}
	q := url.Values{}
	if s := strings.TrimSpace(query); s != "" {
		q.Set("query", s)
	}
	q.Set("maxResults", "100")
	q.Set("orderBy", "key")
	req, err := newJiraRequest(ctx, cfg, http.MethodGet, cfg.baseURL+"/rest/api/3/project/search?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpDo(req)
	if err != nil {
		return nil, fmt.Errorf("%w: list projects: %w", ErrUnavailable, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := searchStatusError(resp); err != nil {
		return nil, err
	}
	var payload struct {
		Values []struct {
			Key  string `json:"key"`
			Name string `json:"name"`
		} `json:"values"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("%w: decode projects: %w", ErrUnavailable, err)
	}
	out := make([]ProjectRef, 0, len(payload.Values))
	for _, p := range payload.Values {
		out = append(out, ProjectRef{Key: p.Key, Name: p.Name})
	}
	return out, nil
}

// searchStatusError maps a search/project-search response status onto a sentinel.
// Unlike statusError (transitions), a 400 here is a bad query (invalid JQL), and
// a 404/410 signals an absent endpoint the caller may retry against the classic
// path. 2xx → nil.
func searchStatusError(resp *http.Response) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	snippet := errorSnippet(resp.Body)
	switch resp.StatusCode {
	case http.StatusBadRequest:
		return fmt.Errorf("%w%s", errBadQuery, suffix(snippet))
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("%w%s", ErrAuthFailed, suffix(snippet))
	case http.StatusNotFound, http.StatusGone:
		return fmt.Errorf("%w: search endpoint unavailable%s", ErrNotFound, suffix(snippet))
	default:
		return fmt.Errorf("%w: search: HTTP %d%s", ErrUnavailable, resp.StatusCode, suffix(snippet))
	}
}
