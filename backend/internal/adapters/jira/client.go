// Package jira reads a single Jira issue for display inside AO's Summary tab.
//
// Access is via the Jira Cloud REST v3 issue endpoint
// (`GET /rest/api/3/issue/{key}?fields=*all`) over the SAME auth seam as search
// and status transitions: base URL + login from env or jira-cli's config file,
// and the API token from AO_JIRA_TOKEN → JIRA_API_TOKEN. There is no jira-cli
// shell-out and no keychain read — one credential path (the REST API token)
// serves display, search, and the status move. Nothing is hardcoded: the browse
// host is derived from the response `self` URL, the sprint custom-field is
// auto-detected, and the issue key is per-request.
//
// This adapter is READ-ONLY. The single sanctioned write (a status transition)
// is a separate concern (transitions.go).
package jira

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/jira/adf"
)

// Sentinel errors. Callers match with errors.Is; the service maps these to HTTP
// envelopes.
var (
	// ErrNotFound is a missing issue or one the credential cannot see (Jira
	// conflates the two in its 404, so we cannot distinguish them).
	ErrNotFound = errors.New("jira: issue not found")
	// ErrAuthFailed is a rejected/absent credential.
	ErrAuthFailed = errors.New("jira: authentication failed")
	// ErrUnavailable is a transport/tooling failure: Jira is unreachable, times
	// out, or returns unparseable output.
	ErrUnavailable = errors.New("jira: unavailable")
	// ErrBadKey is a syntactically invalid issue key.
	ErrBadKey = errors.New("jira: malformed issue key")
	// errBadTransition backs ErrBadTransition (surfaced in transitions.go): an
	// unknown transition id or one Jira's workflow refuses (validator/condition).
	errBadTransition = errors.New("jira: transition rejected")
	// errBadQuery backs ErrBadQuery (surfaced in search.go): a 400 from a search,
	// usually invalid JQL (e.g. a project key that doesn't exist). Lets the service
	// fall back from a project-scoped guess to a plain text search.
	errBadQuery = errors.New("jira: invalid search query")
)

// keyPattern is the Jira issue-key shape (PROJECT-123). Validated before we build
// a request URL, so an untrusted string can never form a malformed path segment.
var keyPattern = regexp.MustCompile(`^[A-Z][A-Z0-9]+-\d+$`)

// Client reads Jira issues, lists/applies status transitions, and runs
// cross-project search — all via the Jira Cloud REST v3 API over a shared auth
// seam (see transitions.go). httpDo + config are seams so tests drive an httptest
// server with a static identity instead of a real network.
type Client struct {
	// sprintFieldID is an optional explicit sprint custom-field id. Empty means
	// auto-detect (the default), which is robust across Jira instances.
	sprintFieldID string
	// httpDo + config back every REST call (display, transitions, search); both are
	// seams so tests drive an httptest server with a static identity.
	httpDo HTTPDoer
	config ConfigSource
}

// Option configures a Client.
type Option func(*Client)

// WithSprintField pins the sprint custom-field id instead of auto-detecting it.
func WithSprintField(id string) Option {
	return func(c *Client) { c.sprintFieldID = strings.TrimSpace(id) }
}

// NewClient returns a Client backed by the Jira Cloud REST v3 API. Auth resolves
// from env then jira-cli's config file (see defaultConfigSource); a missing token
// surfaces as ErrAuthFailed at call time, never a panic.
func NewClient(opts ...Option) *Client {
	c := &Client{httpDo: defaultHTTPClient.Do, config: defaultConfigSource}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// issueFields asks for every field so the sprint custom-field (whose id varies
// per Jira instance) is present for auto-detection, alongside the ADF description
// and subtasks the Summary tab renders.
const issueFields = "*all"

// Get fetches and normalizes one issue for display via REST.
func (c *Client) Get(ctx context.Context, key string) (Issue, error) {
	key = strings.TrimSpace(key)
	if !keyPattern.MatchString(key) {
		return Issue{}, fmt.Errorf("%w: %q", ErrBadKey, key)
	}
	cfg, err := c.config()
	if err != nil {
		return Issue{}, err
	}
	q := url.Values{}
	q.Set("fields", issueFields)
	req, err := newJiraRequest(ctx, cfg, http.MethodGet, cfg.baseURL+"/rest/api/3/issue/"+key+"?"+q.Encode(), nil)
	if err != nil {
		return Issue{}, err
	}
	resp, err := c.httpDo(req)
	if err != nil {
		return Issue{}, fmt.Errorf("%w: get issue %s: %w", ErrUnavailable, key, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := issueStatusError(resp, key); err != nil {
		return Issue{}, err
	}
	var raw rawIssue
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return Issue{}, fmt.Errorf("%w: decode %s: %w", ErrUnavailable, key, err)
	}
	if strings.TrimSpace(raw.Key) == "" && len(raw.Fields) == 0 {
		return Issue{}, fmt.Errorf("%w: empty response for %s", ErrUnavailable, key)
	}
	return c.mapIssue(raw), nil
}

// issueStatusError maps a single-issue GET response status onto a sentinel. Jira
// returns 404 both for a missing issue and one the credential cannot see, so both
// surface as ErrNotFound; 401/403 → auth; any other non-2xx → unavailable. 2xx →
// nil.
func issueStatusError(resp *http.Response, key string) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	snippet := errorSnippet(resp.Body)
	switch resp.StatusCode {
	case http.StatusNotFound:
		return fmt.Errorf("%w: %s", ErrNotFound, key)
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("%w: %s%s", ErrAuthFailed, key, suffix(snippet))
	default:
		return fmt.Errorf("%w: %s: HTTP %d%s", ErrUnavailable, key, resp.StatusCode, suffix(snippet))
	}
}

// ---------------------------------------------------------------------------
// REST v3 → Issue mapping
// ---------------------------------------------------------------------------

type rawIssue struct {
	Key    string                     `json:"key"`
	Self   string                     `json:"self"`
	Fields map[string]json.RawMessage `json:"fields"`
}

type namedRef struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
	AccountId   string `json:"accountId"` // set for user refs (assignee/reporter); pushes assignee into server-side JQL
}

type statusRef struct {
	Name           string `json:"name"`
	StatusCategory struct {
		Key       string `json:"key"`
		ColorName string `json:"colorName"`
	} `json:"statusCategory"`
}

type sprintRef struct {
	Name      string `json:"name"`
	State     string `json:"state"`
	BoardID   int    `json:"boardId"`
	StartDate string `json:"startDate"`
	EndDate   string `json:"endDate"`
}

type subtaskRef struct {
	Key    string `json:"key"`
	Fields struct {
		Summary   string    `json:"summary"`
		IssueType namedRef  `json:"issuetype"`
		Status    statusRef `json:"status"`
	} `json:"fields"`
}

func (c *Client) mapIssue(raw rawIssue) Issue {
	f := raw.Fields
	iss := Issue{
		Key:         raw.Key,
		URL:         browseURL(raw.Self, raw.Key),
		Title:       decodeString(f["summary"]),
		Type:        decodeNamed(f["issuetype"]).Name,
		Priority:    decodeNamed(f["priority"]).Name,
		Assignee:    decodeNamed(f["assignee"]).DisplayName,
		Reporter:    decodeNamed(f["reporter"]).DisplayName,
		Description: adf.Parse(f["description"]),
	}
	if st := decodeStatus(f["status"]); st != nil {
		iss.Status = st.Name
		iss.StatusCategory = st.StatusCategory.Key
		iss.StatusColor = st.StatusCategory.ColorName
	}
	iss.Parent = decodeParent(f["parent"])
	iss.Sprint = c.detectSprint(f)
	iss.Subtasks = decodeSubtasks(f["subtasks"])
	return iss
}

func decodeString(raw json.RawMessage) string {
	var s string
	_ = json.Unmarshal(raw, &s)
	return s
}

func decodeNamed(raw json.RawMessage) namedRef {
	var n namedRef
	if len(raw) == 0 {
		return n
	}
	_ = json.Unmarshal(raw, &n)
	return n
}

func decodeStatus(raw json.RawMessage) *statusRef {
	if len(raw) == 0 {
		return nil
	}
	var s statusRef
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil
	}
	return &s
}

// decodeParent extracts a row's/issue's parent (key + summary) from the raw
// `parent` field, present on subtasks and epic children. nil when absent/malformed.
func decodeParent(raw json.RawMessage) *ParentRef {
	if len(raw) == 0 {
		return nil
	}
	var p struct {
		Key    string `json:"key"`
		Fields struct {
			Summary string `json:"summary"`
		} `json:"fields"`
	}
	if err := json.Unmarshal(raw, &p); err != nil || p.Key == "" {
		return nil
	}
	return &ParentRef{Key: p.Key, Title: p.Fields.Summary}
}

func decodeSubtasks(raw json.RawMessage) []Subtask {
	if len(raw) == 0 {
		return nil
	}
	var refs []subtaskRef
	if err := json.Unmarshal(raw, &refs); err != nil {
		return nil
	}
	out := make([]Subtask, 0, len(refs))
	for _, r := range refs {
		out = append(out, Subtask{
			Key:            r.Key,
			Title:          r.Fields.Summary,
			Type:           r.Fields.IssueType.Name,
			Status:         r.Fields.Status.Name,
			StatusCategory: r.Fields.Status.StatusCategory.Key,
			StatusColor:    r.Fields.Status.StatusCategory.ColorName,
		})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// detectSprint finds the issue's sprint. When a field id is pinned it reads that
// one; otherwise it scans customfield_* for the first array of sprint-shaped
// objects (Agile's sprint field id varies per Jira instance, so we do not
// hardcode it). The active sprint is preferred, else the last listed.
func (c *Client) detectSprint(f map[string]json.RawMessage) *Sprint {
	pick := func(raw json.RawMessage) *Sprint {
		var refs []sprintRef
		if json.Unmarshal(raw, &refs) != nil || len(refs) == 0 {
			return nil
		}
		chosen := refs[len(refs)-1]
		for _, s := range refs {
			if strings.EqualFold(s.State, "active") {
				chosen = s
				break
			}
		}
		if chosen.Name == "" {
			return nil
		}
		return &Sprint{Name: chosen.Name, State: chosen.State, StartDate: chosen.StartDate, EndDate: chosen.EndDate}
	}

	if c.sprintFieldID != "" {
		return pick(f[c.sprintFieldID])
	}
	for name, raw := range f {
		if !strings.HasPrefix(name, "customfield_") {
			continue
		}
		var refs []sprintRef
		if json.Unmarshal(raw, &refs) != nil || len(refs) == 0 {
			continue
		}
		// Sprint objects carry a name plus a state or boardId; that combination
		// disambiguates them from other array custom-fields.
		if refs[0].Name != "" && (refs[0].State != "" || refs[0].BoardID != 0) {
			return pick(raw)
		}
	}
	return nil
}

// browseURL derives the human issue URL from the REST `self` link and the key,
// e.g. self "https://x.atlassian.net/rest/api/3/issue/1" + key "DEMO-2" ->
// "https://x.atlassian.net/browse/DEMO-2". Falls back to "" when self has no
// recognizable host.
func browseURL(self, key string) string {
	i := strings.Index(self, "/rest/")
	if i <= 0 {
		return ""
	}
	return self[:i] + "/browse/" + key
}
