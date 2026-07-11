// Package jira reads a single Jira issue for display inside AO's Summary tab.
//
// Access is by shelling out to the user's jira-cli (`jira issue view <KEY>
// --raw`), which returns the full Jira Cloud REST v3 issue JSON. This reuses the
// jira-cli auth the user already has (config + macOS-keychain token) instead of
// AO managing a Jira credential itself — matching the project decision to
// "prefer jira-cli". Nothing is hardcoded: the browse host is derived from the
// response `self` URL, the sprint custom-field is auto-detected, and the issue
// key is per-request.
//
// This adapter is READ-ONLY. The single sanctioned write (a status transition)
// is a separate, later concern.
package jira

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/jira/adf"
)

// Sentinel errors. Callers match with errors.Is; the service maps these to HTTP
// envelopes.
var (
	// ErrNotFound is a missing issue or one the credential cannot see (Jira
	// conflates the two in its error text, so we cannot distinguish them).
	ErrNotFound = errors.New("jira: issue not found")
	// ErrAuthFailed is a rejected/absent credential.
	ErrAuthFailed = errors.New("jira: authentication failed")
	// ErrUnavailable is a transport/tooling failure: the jira binary is missing,
	// times out, or returns unparseable output.
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

// keyPattern is the Jira issue-key shape (PROJECT-123). Validated before we ever
// shell out, so an untrusted string can never become CLI arguments.
var keyPattern = regexp.MustCompile(`^[A-Z][A-Z0-9]+-\d+$`)

// Runner executes the jira CLI for one issue key and returns its stdout. On a
// non-zero exit it returns a non-nil error whose message includes the CLI's
// stderr, so Get can classify not-found / auth failures. It is a seam so tests
// can inject canned fixtures without a real jira binary.
type Runner func(ctx context.Context, key string) ([]byte, error)

// Client reads Jira issues via a Runner, and lists/applies status transitions
// via the REST endpoints (see transitions.go).
type Client struct {
	run Runner
	// sprintFieldID is an optional explicit sprint custom-field id. Empty means
	// auto-detect (the default), which is robust across Jira instances.
	sprintFieldID string
	// httpDo + config back the REST transition endpoints; both are seams so tests
	// drive an httptest server with a static identity (no jira binary, no network).
	httpDo HTTPDoer
	config ConfigSource
}

// Option configures a Client.
type Option func(*Client)

// WithRunner injects a Runner (tests inject a fixture runner).
func WithRunner(r Runner) Option { return func(c *Client) { c.run = r } }

// WithSprintField pins the sprint custom-field id instead of auto-detecting it.
func WithSprintField(id string) Option {
	return func(c *Client) { c.sprintFieldID = strings.TrimSpace(id) }
}

// NewClient returns a Client. With no WithRunner option it shells out to the
// `jira` binary on PATH (override the binary via AO_JIRA_BIN).
func NewClient(opts ...Option) *Client {
	c := &Client{run: cliRunner, httpDo: defaultHTTPClient.Do, config: defaultConfigSource}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// cliRunner is the production Runner: `jira issue view <key> --raw`.
func cliRunner(ctx context.Context, key string) ([]byte, error) {
	bin := strings.TrimSpace(os.Getenv("AO_JIRA_BIN"))
	if bin == "" {
		bin = "jira"
	}
	path, err := exec.LookPath(bin)
	if err != nil {
		return nil, fmt.Errorf("%w: %q not found on PATH — install jira-cli (https://github.com/ankitpokhrel/jira-cli) or set AO_JIRA_BIN", ErrUnavailable, bin)
	}
	cmd := exec.CommandContext(ctx, path, "issue", "view", key, "--raw")
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("jira issue view %s: %w: %s", key, err, strings.TrimSpace(stderr.String()))
	}
	return []byte(stdout.String()), nil
}

// Get fetches and normalizes one issue for display.
func (c *Client) Get(ctx context.Context, key string) (Issue, error) {
	key = strings.TrimSpace(key)
	if !keyPattern.MatchString(key) {
		return Issue{}, fmt.Errorf("%w: %q", ErrBadKey, key)
	}
	out, err := c.run(ctx, key)
	if err != nil {
		return Issue{}, classify(err)
	}
	if strings.TrimSpace(string(out)) == "" {
		return Issue{}, fmt.Errorf("%w: empty response for %s", ErrUnavailable, key)
	}
	var raw rawIssue
	if err := json.Unmarshal(out, &raw); err != nil {
		return Issue{}, fmt.Errorf("%w: decode %s: %w", ErrUnavailable, key, err)
	}
	return c.mapIssue(raw), nil
}

// classify maps a Runner error onto a sentinel using the CLI's stderr text.
// jira-cli reports both "no such issue" and "no permission" with the same 404
// message, so both surface as ErrNotFound.
func classify(err error) error {
	if errors.Is(err, ErrUnavailable) || errors.Is(err, ErrNotFound) || errors.Is(err, ErrAuthFailed) {
		return err
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "404") || strings.Contains(msg, "does not exist") || strings.Contains(msg, "not found"):
		return fmt.Errorf("%w: %w", ErrNotFound, err)
	case strings.Contains(msg, "401") || strings.Contains(msg, "403") ||
		strings.Contains(msg, "unauthor") || strings.Contains(msg, "authenticat") ||
		strings.Contains(msg, "invalid token") || strings.Contains(msg, "login"):
		return fmt.Errorf("%w: %w", ErrAuthFailed, err)
	default:
		return fmt.Errorf("%w: %w", ErrUnavailable, err)
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
