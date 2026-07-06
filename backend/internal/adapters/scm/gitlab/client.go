package gitlab

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	defaultUserAgent = "ao-agent-orchestrator/scm-gitlab"
)

// ClientOptions configures a Client. Production code sets Token and APIBase
// alone; tests inject HTTPClient and APIBase to point at an httptest fake.
type ClientOptions struct {
	HTTPClient *http.Client
	Token      TokenSource
	// APIBase is the GitLab REST v4 base URL, e.g. "https://gitlab.example.com/api/v4".
	// A trailing slash is trimmed; requests join APIBase + "/" + path.
	APIBase   string
	UserAgent string
}

// Client is a thin HTTP wrapper around GitLab's REST v4 API. It owns:
//   - PRIVATE-TOKEN header injection (with cache invalidation on auth
//     failures via the tokenInvalidator interface), and
//   - ETag/If-None-Match plumbing for conditional GETs.
//
// There is no GraphQL client for GitLab; REST v4 is the only surface.
type Client struct {
	http      *http.Client
	tokens    TokenSource
	apiBase   string
	userAgent string
}

// NewClient returns a Client. It is intentionally tolerant of nil
// dependencies: production passes a TokenSource; tests sometimes leave it
// nil.
func NewClient(opts ClientOptions) *Client {
	c := &Client{
		http:      opts.HTTPClient,
		tokens:    opts.Token,
		apiBase:   strings.TrimSuffix(opts.APIBase, "/"),
		userAgent: opts.UserAgent,
	}
	if c.http == nil {
		c.http = &http.Client{Timeout: 30 * time.Second}
	}
	if c.userAgent == "" {
		c.userAgent = defaultUserAgent
	}
	return c
}

// restResponse is what doREST / doRESTWithETag return to callers.
// NotModified=true means the caller's cached body is still fresh; Body is
// empty in that case and the caller is expected to replay its own cache.
type restResponse struct {
	Body        []byte
	ETag        string
	NotModified bool
	Status      int
}

// doRESTWithETag performs one REST GET with an explicit caller-owned ETag.
// It does not cache bodies itself; that responsibility belongs to the
// caller (mirrors github.Client.doRESTWithETag).
func (c *Client) doRESTWithETag(ctx context.Context, path string, q url.Values, etag string) (restResponse, error) {
	return c.doRESTWithETagAndMethod(ctx, http.MethodGet, path, q, etag, nil)
}

// doREST performs one REST request with no ETag pre-condition. For
// non-GET methods, a non-nil body is JSON-encoded and sent as the request
// body.
func (c *Client) doREST(ctx context.Context, method, path string, q url.Values, body any) (restResponse, error) {
	return c.doRESTWithETagAndMethod(ctx, method, path, q, "", body)
}

func (c *Client) doRESTWithETagAndMethod(ctx context.Context, method, path string, q url.Values, etag string, body any) (restResponse, error) {
	u, err := c.restURL(path, q)
	if err != nil {
		return restResponse{}, fmt.Errorf("gitlab scm: build %s URL: %w", path, err)
	}

	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return restResponse{}, fmt.Errorf("gitlab scm: encode %s %s body: %w", method, path, err)
		}
		rdr = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, u, rdr)
	if err != nil {
		return restResponse{}, fmt.Errorf("gitlab scm: build %s %s request: %w", method, path, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent)
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	if err := c.authorize(ctx, req); err != nil {
		return restResponse{}, err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return restResponse{}, fmt.Errorf("gitlab scm: %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotModified {
		return restResponse{
			NotModified: true,
			ETag:        firstNonEmptyHeader(resp.Header.Get("ETag"), etag),
			Status:      resp.StatusCode,
		}, nil
	}

	b, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return restResponse{}, fmt.Errorf("gitlab scm: read %s body: %w", path, readErr)
	}

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return restResponse{Body: b, ETag: resp.Header.Get("ETag"), Status: resp.StatusCode}, nil
	}

	err = classifyError(resp, b)
	if errors.Is(err, ErrAuthFailed) {
		c.invalidateToken()
	}
	return restResponse{Body: b, Status: resp.StatusCode}, err
}

func (c *Client) authorize(ctx context.Context, req *http.Request) error {
	if c.tokens == nil {
		return nil
	}
	token, err := c.tokens.Token(ctx)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrAuthFailed, err)
	}
	req.Header.Set("PRIVATE-TOKEN", token)
	return nil
}

func (c *Client) invalidateToken() {
	if inv, ok := c.tokens.(tokenInvalidator); ok {
		inv.InvalidateToken()
	}
}

func (c *Client) restURL(path string, q url.Values) (string, error) {
	base, err := url.Parse(c.apiBase)
	if err != nil {
		return "", err
	}
	base.Path = strings.TrimSuffix(base.Path, "/") + "/" + strings.TrimPrefix(path, "/")
	if q != nil {
		base.RawQuery = q.Encode()
	}
	return base.String(), nil
}

// ErrAuthFailed is returned when GitLab responds with an auth-class
// failure (401 or 403). Unlike GitHub, GitLab does not overload 403 for
// rate-limiting (GitLab uses 429 for that), so no rate-limit carve-out is
// needed here.
var ErrAuthFailed = errors.New("gitlab scm: authentication failed")

func classifyError(resp *http.Response, body []byte) error {
	msg := gitlabMessage(body)
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return fmt.Errorf("%w: %s", ErrAuthFailed, msg)
	}
	return fmt.Errorf("gitlab scm: %d %s", resp.StatusCode, msg)
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

func firstNonEmptyHeader(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
