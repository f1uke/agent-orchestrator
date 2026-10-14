package jira

// Status transitions — the ONE sanctioned Jira write. jira-cli 1.7.0 has no
// read-only "list transitions" command and `jira issue view --raw` omits them,
// so both listing and applying go through the Jira Cloud REST v3 transitions
// endpoint (`GET`/`POST /rest/api/3/issue/{key}/transitions`).
//
// Auth and the site come from adapters/atlassian. A POST carries only
// `{"transition":{"id":...}}` — no comment, no field edit — so this stays a pure
// status move.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/atlassian"
)

// Transition is one available status transition for an issue, read live from
// Jira (never hardcoded — the set differs per issue type and current status).
type Transition struct {
	ID         string // Jira transition id (numeric), used to apply the move
	Name       string // transition name shown to the user ("Start Testing")
	To         string // target status name the issue lands in ("In Progress")
	ToCategory string // target status category key (new|indeterminate|done)
	ToColor    string // target status-category colorName
}

// ErrBadTransition is a rejected transition: an unknown id, or one Jira refuses
// (a workflow validator/condition, e.g. a required field). Distinct from ErrBadKey.
// The underlying sentinel lives in client.go with the others.
var ErrBadTransition = errBadTransition

// HTTPDoer performs one HTTP request. Seam for tests (httptest).
type HTTPDoer func(*http.Request) (*http.Response, error)

// WithHTTPDoer injects the HTTP transport (tests pass an httptest client's Do).
// It sets the transfer doer too: a test that injects a server must see the file
// calls as well, and a caller substituting the transport means all of it.
func WithHTTPDoer(d HTTPDoer) Option { return func(c *Client) { c.httpDo, c.transferDo = d, d } }

// WithConfigSource injects the REST config resolver (tests inject a static one).
func WithConfigSource(s atlassian.ConfigSource) Option { return func(c *Client) { c.config = s } }

var transitionIDPattern = regexp.MustCompile(`^\d+$`)

// defaultHTTPClient bounds every transition call so a hung Jira never wedges the
// daemon request.
var defaultHTTPClient = &http.Client{Timeout: 15 * time.Second}

// defaultTransferHTTPClient backs the call that moves a whole file rather than a
// JSON document: streaming an attachment back for inline display. Attachments
// are routinely multiple megabytes (a screen recording is tens), so the 15s
// budget that suits a read is the wrong unit here: it aborts a download that was
// progressing fine.
var defaultTransferHTTPClient = &http.Client{Timeout: 5 * time.Minute}

// Transitions returns the issue's available status transitions, read live.
func (c *Client) Transitions(ctx context.Context, key string) ([]Transition, error) {
	key = strings.TrimSpace(key)
	if !keyPattern.MatchString(key) {
		return nil, fmt.Errorf("%w: %q", ErrBadKey, key)
	}
	cfg, err := c.config()
	if err != nil {
		return nil, err
	}
	req, err := transitionRequest(ctx, cfg, http.MethodGet, key, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpDo(req)
	if err != nil {
		return nil, fmt.Errorf("%w: list transitions %s: %w", ErrUnavailable, key, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := statusError(resp, key); err != nil {
		return nil, err
	}
	var payload struct {
		Transitions []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			To   struct {
				Name           string `json:"name"`
				StatusCategory struct {
					Key       string `json:"key"`
					ColorName string `json:"colorName"`
				} `json:"statusCategory"`
			} `json:"to"`
		} `json:"transitions"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("%w: decode transitions %s: %w", ErrUnavailable, key, err)
	}
	out := make([]Transition, 0, len(payload.Transitions))
	for _, t := range payload.Transitions {
		out = append(out, Transition{
			ID:         t.ID,
			Name:       t.Name,
			To:         t.To.Name,
			ToCategory: t.To.StatusCategory.Key,
			ToColor:    t.To.StatusCategory.ColorName,
		})
	}
	return out, nil
}

// Move applies a status transition by its id. Success is HTTP 204; a workflow
// rejection (validators/permissions) surfaces via ErrBadTransition/ErrAuthFailed.
func (c *Client) Move(ctx context.Context, key, transitionID string) error {
	key = strings.TrimSpace(key)
	if !keyPattern.MatchString(key) {
		return fmt.Errorf("%w: %q", ErrBadKey, key)
	}
	transitionID = strings.TrimSpace(transitionID)
	if !transitionIDPattern.MatchString(transitionID) {
		return fmt.Errorf("%w: transition id %q is not numeric", ErrBadTransition, transitionID)
	}
	cfg, err := c.config()
	if err != nil {
		return err
	}
	body, err := json.Marshal(map[string]any{"transition": map[string]string{"id": transitionID}})
	if err != nil {
		return fmt.Errorf("%w: encode move %s: %w", ErrUnavailable, key, err)
	}
	req, err := transitionRequest(ctx, cfg, http.MethodPost, key, body)
	if err != nil {
		return err
	}
	resp, err := c.httpDo(req)
	if err != nil {
		return fmt.Errorf("%w: move %s: %w", ErrUnavailable, key, err)
	}
	defer func() { _ = resp.Body.Close() }()
	return statusError(resp, key)
}

// transitionRequest builds the authenticated request for the issue's transitions
// endpoint. The key is already keyPattern-validated, so it is a safe path segment.
func transitionRequest(ctx context.Context, cfg atlassian.Config, method, key string, body []byte) (*http.Request, error) {
	url := fmt.Sprintf("%s/rest/api/3/issue/%s/transitions", cfg.BaseURL, key)
	return atlassian.NewRequest(ctx, cfg, method, url, body)
}

// statusError maps a REST response status onto a sentinel. 2xx → nil. It reads a
// bounded slice of the body so a Jira error message (permissions/validators)
// surfaces to the user instead of a bare status code.
func statusError(resp *http.Response, key string) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	snippet := errorSnippet(resp.Body)
	switch resp.StatusCode {
	case http.StatusBadRequest:
		return fmt.Errorf("%w: %s%s", ErrBadTransition, key, suffix(snippet))
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("%w: %s%s", ErrAuthFailed, key, suffix(snippet))
	case http.StatusNotFound:
		return fmt.Errorf("%w: %s", ErrNotFound, key)
	default:
		return fmt.Errorf("%w: %s: HTTP %d%s", ErrUnavailable, key, resp.StatusCode, suffix(snippet))
	}
}

// errorSnippet extracts a short human message from a Jira error body. Jira Cloud
// returns {"errorMessages":[...], "errors":{...}}; falls back to a raw slice.
func errorSnippet(body io.Reader) string {
	const maxBytes = 2 << 10
	data, _ := io.ReadAll(io.LimitReader(body, maxBytes))
	if len(data) == 0 {
		return ""
	}
	var jiraErr struct {
		ErrorMessages []string          `json:"errorMessages"`
		Errors        map[string]string `json:"errors"`
	}
	if json.Unmarshal(data, &jiraErr) == nil {
		if len(jiraErr.ErrorMessages) > 0 {
			return strings.Join(jiraErr.ErrorMessages, "; ")
		}
		if len(jiraErr.Errors) > 0 {
			parts := make([]string, 0, len(jiraErr.Errors))
			for k, v := range jiraErr.Errors {
				parts = append(parts, k+": "+v)
			}
			return strings.Join(parts, "; ")
		}
	}
	return strings.TrimSpace(string(data))
}

func suffix(snippet string) string {
	if snippet == "" {
		return ""
	}
	return ": " + snippet
}
