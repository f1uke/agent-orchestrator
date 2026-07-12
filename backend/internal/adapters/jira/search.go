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
	Key            string
	Type           string
	Title          string
	Status         string
	StatusCategory string // Jira category key: new|indeterminate|done
	StatusColor    string
	Assignee       string
	Sprint         *Sprint // current/most-relevant sprint, for Browse Jira grouping (nil = none)
	URL            string  // human browse URL, derived from the site base
}

// ProjectRef is one Jira project for the project picker (Browse Jira, Slice 5;
// used server-side here to resolve a bare project-key query).
type ProjectRef struct {
	Key  string
	Name string
}

// Ask for the navigable field set (what Jira's issue navigator / board columns
// use) rather than a fixed list, so the Agile sprint custom-field — whose id
// varies per instance — is present for detectSprint to find and Browse Jira can
// group rows by sprint. Lighter than *all (no ADF description/comments/worklog),
// which the list view does not need.
const searchFields = "*navigable"

// SearchIssues runs a JQL query and returns matching issues (capped). The JQL is
// built by the service — this adapter is a dumb executor so the query semantics
// stay testable in one place. limit is clamped to a sane window.
func (c *Client) SearchIssues(ctx context.Context, jql string, limit int) ([]IssueSummary, error) {
	jql = strings.TrimSpace(jql)
	if jql == "" {
		return nil, fmt.Errorf("%w: empty search query", errBadQuery)
	}
	if limit <= 0 || limit > 50 {
		limit = 25
	}
	cfg, err := c.config()
	if err != nil {
		return nil, err
	}
	// Enhanced endpoint first; fall back to the classic one only when it is absent.
	issues, status, err := c.searchOnce(ctx, cfg, "/rest/api/3/search/jql", jql, limit)
	if err == nil {
		return issues, nil
	}
	if status == http.StatusNotFound || status == http.StatusGone {
		issues, _, err = c.searchOnce(ctx, cfg, "/rest/api/3/search", jql, limit)
		return issues, err
	}
	return nil, err
}

// searchOnce hits one search endpoint. It returns the HTTP status alongside the
// result so SearchIssues can decide whether to fall back to the classic endpoint.
func (c *Client) searchOnce(ctx context.Context, cfg restConfig, path, jql string, limit int) ([]IssueSummary, int, error) {
	q := url.Values{}
	q.Set("jql", jql)
	q.Set("fields", searchFields)
	q.Set("maxResults", strconv.Itoa(limit))
	req, err := newJiraRequest(ctx, cfg, http.MethodGet, cfg.baseURL+path+"?"+q.Encode(), nil)
	if err != nil {
		return nil, 0, err
	}
	resp, err := c.httpDo(req)
	if err != nil {
		return nil, 0, fmt.Errorf("%w: search: %w", ErrUnavailable, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := searchStatusError(resp); err != nil {
		return nil, resp.StatusCode, err
	}
	// Decode fields as a raw map (like the display path) so the sprint custom-field
	// — whose id varies per instance — can be located by detectSprint alongside the
	// known summary/type/status/assignee fields.
	var payload struct {
		Issues []struct {
			Key    string                     `json:"key"`
			Fields map[string]json.RawMessage `json:"fields"`
		} `json:"issues"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, resp.StatusCode, fmt.Errorf("%w: decode search: %w", ErrUnavailable, err)
	}
	out := make([]IssueSummary, 0, len(payload.Issues))
	for _, it := range payload.Issues {
		f := it.Fields
		sum := IssueSummary{
			Key:      it.Key,
			Type:     decodeNamed(f["issuetype"]).Name,
			Title:    decodeString(f["summary"]),
			Assignee: decodeNamed(f["assignee"]).DisplayName,
			Sprint:   c.detectSprint(f),
			URL:      cfg.baseURL + "/browse/" + it.Key,
		}
		if st := decodeStatus(f["status"]); st != nil {
			sum.Status = st.Name
			sum.StatusCategory = st.StatusCategory.Key
			sum.StatusColor = st.StatusCategory.ColorName
		}
		out = append(out, sum)
	}
	return out, resp.StatusCode, nil
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
