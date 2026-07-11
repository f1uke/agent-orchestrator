package jira

// Status transitions — the ONE sanctioned Jira write. jira-cli 1.7.0 has no
// read-only "list transitions" command and `jira issue view --raw` omits them,
// so both listing and applying go through the Jira Cloud REST v3 transitions
// endpoint (`GET`/`POST /rest/api/3/issue/{key}/transitions`).
//
// Auth mirrors the app's SCM pattern (AO_<SCM>_TOKEN → generic env → error): the
// base URL + login come from env or jira-cli's own config file, and the API
// token from AO_JIRA_TOKEN → JIRA_API_TOKEN (the var jira-cli itself honors).
// The host is never hardcoded. A POST carries only `{"transition":{"id":...}}`
// — no comment, no field edit — so this stays a pure status move.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
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

// restConfig is the base URL + basic-auth identity for the transition endpoints.
type restConfig struct {
	baseURL string
	email   string
	token   string
}

// ConfigSource yields the Jira REST config. A seam so tests point the client at
// an httptest server with a static identity; the default resolves env then the
// jira-cli config file.
type ConfigSource func() (restConfig, error)

// HTTPDoer performs one HTTP request. Seam for tests (httptest).
type HTTPDoer func(*http.Request) (*http.Response, error)

// WithHTTPDoer injects the HTTP transport (tests pass an httptest client's Do).
func WithHTTPDoer(d HTTPDoer) Option { return func(c *Client) { c.httpDo = d } }

// WithConfigSource injects the REST config resolver (tests inject a static one).
func WithConfigSource(s ConfigSource) Option { return func(c *Client) { c.config = s } }

var transitionIDPattern = regexp.MustCompile(`^\d+$`)

// defaultHTTPClient bounds every transition call so a hung Jira never wedges the
// daemon request.
var defaultHTTPClient = &http.Client{Timeout: 15 * time.Second}

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
func transitionRequest(ctx context.Context, cfg restConfig, method, key string, body []byte) (*http.Request, error) {
	url := fmt.Sprintf("%s/rest/api/3/issue/%s/transitions", cfg.baseURL, key)
	return newJiraRequest(ctx, cfg, method, url, body)
}

// newJiraRequest builds an authenticated Jira Cloud REST request (Accept JSON +
// basic auth from the resolved config). Shared by the transition and search
// endpoints so the auth handling lives in one place. rawURL is fully built by
// the caller (query already encoded).
func newJiraRequest(ctx context.Context, cfg restConfig, method, rawURL string, body []byte) (*http.Request, error) {
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, rdr)
	if err != nil {
		return nil, fmt.Errorf("%w: build request: %w", ErrUnavailable, err)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(cfg.email+":"+cfg.token)))
	return req, nil
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

// firstEnv returns the first non-empty (trimmed) env var from names.
func firstEnv(names ...string) string {
	for _, n := range names {
		if v := strings.TrimSpace(os.Getenv(n)); v != "" {
			return v
		}
	}
	return ""
}

// defaultConfigSource resolves the REST config from env, then jira-cli's config
// file for the non-secret base URL + login. Mirrors the SCM adapters' precedence
// (an AO_-prefixed override, then the generic value jira-cli honors).
func defaultConfigSource() (restConfig, error) {
	cfg := restConfig{
		baseURL: firstEnv("AO_JIRA_URL", "JIRA_SERVER"),
		email:   firstEnv("AO_JIRA_EMAIL", "JIRA_LOGIN"),
		token:   firstEnv("AO_JIRA_TOKEN", "JIRA_API_TOKEN"),
	}
	if cfg.baseURL == "" || cfg.email == "" {
		server, login := readCLIConfig()
		if cfg.baseURL == "" {
			cfg.baseURL = server
		}
		if cfg.email == "" {
			cfg.email = login
		}
	}
	cfg.baseURL = strings.TrimRight(cfg.baseURL, "/")
	if cfg.baseURL == "" || cfg.email == "" {
		return restConfig{}, fmt.Errorf("%w: Jira server/login not configured — run `jira init` or set AO_JIRA_URL and AO_JIRA_EMAIL", ErrUnavailable)
	}
	if cfg.token == "" {
		return restConfig{}, fmt.Errorf("%w: no Jira API token — set JIRA_API_TOKEN (or AO_JIRA_TOKEN) so AO can read transitions and move status", ErrAuthFailed)
	}
	return cfg, nil
}

// readCLIConfig line-scans jira-cli's config for the top-level `server:`/`login:`
// values (no YAML dependency). Honors $JIRA_CONFIG_FILE, else the default path.
func readCLIConfig() (server, login string) {
	path := strings.TrimSpace(os.Getenv("JIRA_CONFIG_FILE"))
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", ""
		}
		path = filepath.Join(home, ".config", ".jira", ".config.yml")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		switch {
		case strings.HasPrefix(line, "server:"):
			server = unquote(strings.TrimSpace(strings.TrimPrefix(line, "server:")))
		case strings.HasPrefix(line, "login:"):
			login = unquote(strings.TrimSpace(strings.TrimPrefix(line, "login:")))
		}
	}
	return server, login
}

func unquote(s string) string {
	return strings.Trim(s, `"'`)
}
