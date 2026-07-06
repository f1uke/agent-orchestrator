package gitlab

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	defaultAPIBase   = "https://gitlab.com/api/v4"
	defaultUserAgent = "ao-agent-orchestrator/tracker-gitlab"

	stateOpenedGL = "opened"
	stateClosedGL = "closed"

	// List pagination — GitLab's per_page maxes at 100. ListFilter.Limit is
	// an optional total-result cap; a single page is fetched in v1 (see doc.go).
	listPageSize = 100
)

// Sentinel errors. Adapter-level callers should match on these via
// errors.Is; the orchestrator's lifecycle code is intentionally insulated
// from raw HTTP status codes.
var (
	ErrNotFound      = errors.New("gitlab tracker: issue not found")
	ErrAuthFailed    = errors.New("gitlab tracker: authentication failed")
	ErrWrongProvider = errors.New("gitlab tracker: id is not a gitlab tracker id")
	ErrBadID         = errors.New("gitlab tracker: malformed native id")
)

// Options configures a Tracker. All fields except Token are optional —
// production code typically sets Token and APIBase alone; tests inject
// HTTPClient and APIBase to point at an httptest fake.
type Options struct {
	Token      TokenSource
	HTTPClient *http.Client
	// APIBase is the GitLab REST v4 base URL, e.g.
	// "https://gitlab.example.com/api/v4". A trailing slash is trimmed;
	// requests join APIBase + "/" + path.
	APIBase   string
	UserAgent string
}

// Tracker implements ports.Tracker against the GitLab REST v4 API.
type Tracker struct {
	http      *http.Client
	tokens    TokenSource
	apiBase   string
	userAgent string
}

// New returns a Tracker. It fails fast when no token can be obtained so
// daemons crash at startup rather than at first issue lookup.
func New(opts Options) (*Tracker, error) {
	src := opts.Token
	if src == nil {
		return nil, ErrNoToken
	}
	if _, err := src.Token(context.Background()); err != nil {
		return nil, err
	}
	t := &Tracker{
		http:      opts.HTTPClient,
		tokens:    src,
		apiBase:   strings.TrimSuffix(opts.APIBase, "/"),
		userAgent: opts.UserAgent,
	}
	if t.http == nil {
		t.http = &http.Client{Timeout: 30 * time.Second}
	}
	if t.apiBase == "" {
		t.apiBase = defaultAPIBase
	}
	if t.userAgent == "" {
		t.userAgent = defaultUserAgent
	}
	return t, nil
}

// Statically assert Tracker satisfies the port. If this stops compiling, the
// port shape changed and the adapter needs to follow.
var _ ports.Tracker = (*Tracker)(nil)

// ---------------------------------------------------------------------------
// Get
// ---------------------------------------------------------------------------

// glIssue is the subset of fields we read off the GitLab REST issue payload.
type glIssue struct {
	IID         int      `json:"iid"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	State       string   `json:"state"`
	WebURL      string   `json:"web_url"`
	Labels      []string `json:"labels"`
	Assignees   []glUser `json:"assignees"`
}

type glUser struct {
	Username string `json:"username"`
}

// Get fetches a single issue by id and maps it onto the normalized domain.Issue.
func (t *Tracker) Get(ctx context.Context, id domain.TrackerID) (domain.Issue, error) {
	projectPath, iid, err := t.parseID(id)
	if err != nil {
		return domain.Issue{}, err
	}
	path := "/projects/" + url.PathEscape(projectPath) + "/issues/" + strconv.Itoa(iid)

	resp, err := t.do(ctx, http.MethodGet, path)
	if err != nil {
		return domain.Issue{}, err
	}
	var raw glIssue
	if err := json.Unmarshal(resp, &raw); err != nil {
		return domain.Issue{}, fmt.Errorf("gitlab tracker: decode issue: %w", err)
	}
	return issueFromGL(projectPath, raw), nil
}

// issueFromGL projects a raw GitLab issue payload into the normalized
// domain.Issue. projectPath is passed in because the TrackerID.Native shape
// is "group/sub/proj#iid" and we want the returned ID to round-trip
// through the same adapter even if the original caller used a zero
// Provider.
func issueFromGL(projectPath string, raw glIssue) domain.Issue {
	assignees := make([]string, 0, len(raw.Assignees))
	for _, a := range raw.Assignees {
		assignees = append(assignees, a.Username)
	}
	labels := raw.Labels
	if labels == nil {
		labels = []string{}
	}
	out := domain.Issue{
		ID: domain.TrackerID{
			Provider: domain.TrackerProviderGitLab,
			Native:   fmt.Sprintf("%s#%d", projectPath, raw.IID),
		},
		Title:     raw.Title,
		Body:      raw.Description,
		State:     mapStateFromGitLab(raw.State),
		URL:       raw.WebURL,
		Labels:    labels,
		Assignees: assignees,
	}
	if len(out.Labels) == 0 {
		out.Labels = nil
	}
	if len(out.Assignees) == 0 {
		out.Assignees = nil
	}
	return out
}

// mapStateFromGitLab projects GitLab's opened/closed surface onto the
// normalized state. GitLab has no native in-progress/review state without
// board columns (a premium feature), so both round-trip to open/done only —
// see doc.go for the full rationale.
func mapStateFromGitLab(state string) domain.NormalizedIssueState {
	if strings.EqualFold(state, stateClosedGL) {
		return domain.IssueDone
	}
	return domain.IssueOpen
}

// ---------------------------------------------------------------------------
// List
// ---------------------------------------------------------------------------

// List returns issues for a project, filtered by state/labels/assignee.
func (t *Tracker) List(ctx context.Context, repo domain.TrackerRepo, filter domain.ListFilter) ([]domain.Issue, error) {
	if repo.Provider != domain.TrackerProviderGitLab {
		return nil, fmt.Errorf("%w: provider=%q", ErrWrongProvider, repo.Provider)
	}
	projectPath, err := parseGitLabProjectPath(repo.Native)
	if err != nil {
		return nil, err
	}

	q := url.Values{}
	switch filter.State {
	case domain.ListOpen:
		q.Set("state", "opened")
	case domain.ListClosed:
		q.Set("state", "closed")
	}
	if len(filter.Labels) > 0 {
		q.Set("labels", strings.Join(filter.Labels, ","))
	}
	if filter.Assignee != "" {
		q.Set("assignee_username", filter.Assignee)
	}
	perPage := listPageSize
	if filter.Limit > 0 && filter.Limit < listPageSize {
		perPage = filter.Limit
	}
	q.Set("per_page", strconv.Itoa(perPage))

	path := "/projects/" + url.PathEscape(projectPath) + "/issues?" + q.Encode()
	resp, err := t.do(ctx, http.MethodGet, path)
	if err != nil {
		return nil, err
	}
	var raw []glIssue
	if err := json.Unmarshal(resp, &raw); err != nil {
		return nil, fmt.Errorf("gitlab tracker: decode list: %w", err)
	}
	out := make([]domain.Issue, 0, len(raw))
	for _, r := range raw {
		out = append(out, issueFromGL(projectPath, r))
	}
	if filter.Limit > 0 && len(out) > filter.Limit {
		out = out[:filter.Limit]
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Preflight
// ---------------------------------------------------------------------------

// Preflight verifies the configured token is currently accepted by GitLab
// (one GET /user). It does NOT prove the token has access to any specific
// project — those may still fail with ErrAuthFailed even after a
// successful Preflight.
func (t *Tracker) Preflight(ctx context.Context) error {
	_, err := t.do(ctx, http.MethodGet, "/user")
	return err
}

// ---------------------------------------------------------------------------
// HTTP plumbing
// ---------------------------------------------------------------------------

// do issues one request against the GitLab REST API. The request URL is
// built as a raw string (apiBase + path) rather than round-tripped through
// url.URL.Path, because path may already contain a percent-escaped project
// segment (url.PathEscape turns "group/sub/proj" into
// "group%2Fsub%2Fproj"); assigning that pre-escaped segment to url.URL.Path
// and letting url.URL.String() re-escape it would corrupt "%2F" into
// "%252F" on the wire. See doc.go and TestTrackerGet_NestedGroupPathIsSingleEncoded.
func (t *Tracker) do(ctx context.Context, method, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, t.apiBase+path, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("gitlab tracker: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", t.userAgent)
	tok, err := t.tokens.Token(ctx)
	if err != nil {
		return nil, err
	}
	req.Header.Set("PRIVATE-TOKEN", tok)

	resp, err := t.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gitlab tracker: %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return nil, fmt.Errorf("gitlab tracker: read response body: %w", readErr)
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return respBody, nil
	}
	return respBody, classifyError(resp, respBody)
}

func classifyError(resp *http.Response, body []byte) error {
	msg := gitlabMessage(body)
	switch resp.StatusCode {
	case http.StatusNotFound:
		return fmt.Errorf("%w: %s", ErrNotFound, msg)
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("%w: %s", ErrAuthFailed, msg)
	}
	return fmt.Errorf("gitlab tracker: %d %s", resp.StatusCode, msg)
}

func gitlabMessage(body []byte) string {
	var p struct {
		Message json.RawMessage `json:"message"`
		Error   string          `json:"error"`
	}
	if json.Unmarshal(body, &p) == nil {
		if len(p.Message) > 0 {
			return strings.Trim(string(p.Message), `"`)
		}
		if p.Error != "" {
			return p.Error
		}
	}
	return strings.TrimSpace(string(body))
}

// ---------------------------------------------------------------------------
// ID parsing
// ---------------------------------------------------------------------------

// parseID accepts a TrackerID whose Native form is "group/sub/proj#iid" and
// returns the project path and iid separately.
func (t *Tracker) parseID(id domain.TrackerID) (projectPath string, iid int, err error) {
	// Strict: the Session Manager picks an adapter by Provider, so reaching
	// this adapter with a non-gitlab Provider is a routing bug, not user
	// input. Empty Provider is treated the same way — it would round-trip
	// to an Issue whose ID can't be re-routed.
	if id.Provider != domain.TrackerProviderGitLab {
		return "", 0, fmt.Errorf("%w: provider=%q", ErrWrongProvider, id.Provider)
	}
	return parseGitLabID(id.Native)
}

// parseGitLabID accepts "group/sub/proj#iid" and splits on the LAST "#" —
// project paths never contain "#", but splitting on the last occurrence
// keeps the parse unambiguous even if that ever changes.
func parseGitLabID(native string) (projectPath string, iid int, err error) {
	hash := strings.LastIndexByte(native, '#')
	if hash < 0 {
		return "", 0, fmt.Errorf("%w: missing #issue", ErrBadID)
	}
	projectPath, err = parseGitLabProjectPath(native[:hash])
	if err != nil {
		return "", 0, err
	}
	numPart := native[hash+1:]
	n, parseErr := strconv.Atoi(numPart)
	if parseErr != nil || n <= 0 {
		return "", 0, fmt.Errorf("%w: bad issue iid %q", ErrBadID, numPart)
	}
	return projectPath, n, nil
}

// parseGitLabProjectPath accepts a GitLab project's full path ("group/proj"
// or "group/sub/proj" for nested groups) and rejects empty segments,
// embedded "#", and whitespace.
func parseGitLabProjectPath(native string) (string, error) {
	if native == "" {
		return "", fmt.Errorf("%w: empty project path", ErrBadID)
	}
	if strings.ContainsAny(native, "# \t\n\r") {
		return "", fmt.Errorf("%w: invalid project path %q", ErrBadID, native)
	}
	for _, seg := range strings.Split(native, "/") {
		if seg == "" {
			return "", fmt.Errorf("%w: empty path segment in %q", ErrBadID, native)
		}
	}
	return native, nil
}
