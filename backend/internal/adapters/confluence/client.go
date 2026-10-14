// Package confluence reads Confluence Cloud pages by CQL for the release-train
// monitor. It is read-only and complete-or-error: a capped or broken walk is
// ErrIncomplete, never a short list.
package confluence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/atlassian"
)

type classed struct {
	msg   string
	class error
}

func (e *classed) Error() string { return e.msg }

func (e *classed) Is(target error) bool { return target == e.class }

// Sentinel errors. ErrAuthFailed and ErrUnavailable match the shared atlassian
// classes under errors.Is, with Confluence's own text.
var (
	ErrAuthFailed  error = &classed{"confluence: authentication failed", atlassian.ErrAuthFailed}
	ErrUnavailable error = &classed{"confluence: unavailable", atlassian.ErrUnavailable}
	ErrNotFound          = errors.New("confluence: not found")
	ErrBadQuery          = errors.New("confluence: invalid CQL")
	ErrIncomplete        = errors.New("confluence: incomplete read")
)

const (
	pageSize        = 25
	defaultMaxPages = 8
	maxErrorMessage = 300
	searchPath      = "/rest/api/content/search"
)

// Page is one page with its storage-format body.
type Page struct {
	ID      string
	Title   string
	URL     string
	Created time.Time
	Body    string
}

// HTTPDoer sends one request.
type HTTPDoer func(*http.Request) (*http.Response, error)

// Client reads Confluence over the shared Atlassian site config, at BaseURL + "/wiki".
type Client struct {
	httpDo HTTPDoer
	config atlassian.ConfigSource
}

// Option configures a Client.
type Option func(*Client)

// WithHTTPDoer injects the transport.
func WithHTTPDoer(d HTTPDoer) Option { return func(c *Client) { c.httpDo = d } }

// WithConfigSource injects the site config resolver.
func WithConfigSource(s atlassian.ConfigSource) Option { return func(c *Client) { c.config = s } }

// NewClient returns a Client on atlassian.DefaultConfigSource.
func NewClient(opts ...Option) *Client {
	c := &Client{httpDo: (&http.Client{Timeout: 30 * time.Second}).Do, config: atlassian.DefaultConfigSource}
	for _, o := range opts {
		o(c)
	}
	return c
}

type rawPage struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Body  struct {
		Storage struct {
			Value string `json:"value"`
		} `json:"storage"`
	} `json:"body"`
	History struct {
		CreatedDate time.Time `json:"createdDate"`
	} `json:"history"`
	Links struct {
		WebUI string `json:"webui"`
	} `json:"_links"`
}

// SearchPages returns every page the CQL matches, with body and history, or
// ErrIncomplete when a later page fails or more than maxPages pages exist.
// It follows the search's next link: CQL search ignores start.
func (c *Client) SearchPages(ctx context.Context, cql string, maxPages int) ([]Page, error) {
	cql = strings.TrimSpace(cql)
	if cql == "" {
		return nil, fmt.Errorf("%w: empty query", ErrBadQuery)
	}
	if maxPages <= 0 {
		maxPages = defaultMaxPages
	}
	cfg, err := c.config()
	if err != nil {
		return nil, err
	}
	base := cfg.BaseURL + "/wiki"
	q := url.Values{}
	q.Set("cql", cql)
	q.Set("limit", fmt.Sprint(pageSize))
	q.Set("expand", "body.storage,history")
	next := searchPath + "?" + q.Encode()
	var out []Page
	for page := 0; page < maxPages; page++ {
		var body struct {
			Results []rawPage `json:"results"`
			Links   struct {
				Next string `json:"next"`
			} `json:"_links"`
		}
		if err := c.get(ctx, cfg, base+next, &body); err != nil {
			if page > 0 {
				return nil, fmt.Errorf("%w: page %d: %w", ErrIncomplete, page+1, err)
			}
			return nil, err
		}
		for _, r := range body.Results {
			out = append(out, Page{ID: r.ID, Title: r.Title, URL: base + r.Links.WebUI, Created: r.History.CreatedDate, Body: r.Body.Storage.Value})
		}
		if body.Links.Next == "" {
			return out, nil
		}
		if !strings.HasPrefix(body.Links.Next, searchPath+"?") {
			return nil, fmt.Errorf("%w: next link leaves the search endpoint", ErrUnavailable)
		}
		next = body.Links.Next
	}
	return nil, fmt.Errorf("%w: more than %d pages", ErrIncomplete, maxPages)
}

func (c *Client) get(ctx context.Context, cfg atlassian.Config, rawURL string, out any) error {
	req, err := atlassian.NewRequest(ctx, cfg, http.MethodGet, rawURL, nil)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	resp, err := c.httpDo(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return fmt.Errorf("%w: search: %w", ErrUnavailable, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := statusError(resp); err != nil {
		return err
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("%w: decode search: %w", ErrUnavailable, err)
	}
	return nil
}

func statusError(resp *http.Response) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	msg := message(resp.Body)
	switch resp.StatusCode {
	case http.StatusTooManyRequests:
		return &atlassian.RateLimitError{RetryAfter: atlassian.ParseRetryAfter(resp.Header.Get("Retry-After"), time.Now()), Message: msg}
	case http.StatusBadRequest:
		return fmt.Errorf("%w%s", ErrBadQuery, suffix(msg))
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("%w%s", ErrAuthFailed, suffix(msg))
	case http.StatusNotFound:
		return ErrNotFound
	}
	return fmt.Errorf("%w: HTTP %d%s", ErrUnavailable, resp.StatusCode, suffix(msg))
}

func message(body io.Reader) string {
	data, _ := io.ReadAll(io.LimitReader(body, 4<<10))
	var e struct {
		Message string `json:"message"`
	}
	msg := strings.TrimSpace(string(data))
	if json.Unmarshal(data, &e) == nil && e.Message != "" {
		msg = e.Message
	}
	if len(msg) > maxErrorMessage {
		msg = msg[:maxErrorMessage]
	}
	return msg
}

func suffix(msg string) string {
	if msg == "" {
		return ""
	}
	return ": " + msg
}
