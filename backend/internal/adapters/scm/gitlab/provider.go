package gitlab

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	scmobserve "github.com/aoagents/agent-orchestrator/backend/internal/observe/scm"
)

// Compile-time assertion that Provider implements the full 7-method
// observe/scm.Provider interface (ParseRepository, RepoPRListGuard,
// ListOpenPRsByRepo, CommitChecksGuard, FetchPullRequests,
// FetchFailedCheckLogTail, FetchReviewThreads).
var _ scmobserve.Provider = (*Provider)(nil)

// ProviderOptions configures a Provider. Production code typically sets
// Token, APIBase, and Host; tests inject a pre-built Client pointed at
// httptest.
type ProviderOptions struct {
	Client     *Client
	HTTPClient *http.Client
	Token      TokenSource
	// SkipTokenPreflight defers token validation until the first provider call.
	// Daemon wiring uses this so glab-token shell-out never blocks readiness.
	SkipTokenPreflight bool
	// APIBase is the GitLab REST v4 base URL, e.g.
	// "https://gitlab.example.com/api/v4".
	APIBase string
	// Host is the GitLab hostname this provider claims remotes for, e.g.
	// "gitlab.finnomena.com". ParseRepository rejects remotes whose host
	// does not match, so a composite dispatcher can try multiple SCM
	// providers without one claiming another's remote.
	Host      string
	UserAgent string
	Logger    *slog.Logger
}

// Provider observes one GitLab merge request and returns a normalized
// ports.PRObservation for the PR Manager to persist.
type Provider struct {
	client *Client
	host   string
	logger *slog.Logger
}

// NewProvider returns a Provider. If opts.Client is supplied it is used
// verbatim; otherwise a Client is built from the other options. When a
// Token source is supplied it is exercised once so missing credentials
// surface at daemon startup rather than at first observation, unless
// SkipTokenPreflight is set. Tests that want an unauthenticated fake pass
// opts.Client directly.
func NewProvider(opts ProviderOptions) (*Provider, error) {
	if opts.Client == nil && opts.Token != nil && !opts.SkipTokenPreflight {
		if _, err := opts.Token.Token(context.Background()); err != nil {
			return nil, err
		}
	}
	c := opts.Client
	if c == nil {
		c = NewClient(ClientOptions{
			HTTPClient: opts.HTTPClient,
			Token:      opts.Token,
			APIBase:    opts.APIBase,
			UserAgent:  opts.UserAgent,
		})
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Provider{client: c, host: opts.Host, logger: logger}, nil
}

// SCMCredentialsAvailable checks whether this provider can obtain a token. The
// SCM observer calls it lazily during the first poll that has SCM subjects, so
// daemon readiness is not blocked by shelling out to glab auth status and idle
// daemons do not warn about missing credentials.
func (p *Provider) SCMCredentialsAvailable(ctx context.Context) (bool, error) {
	if p.client == nil || p.client.tokens == nil {
		return true, nil
	}
	if _, err := p.client.tokens.Token(ctx); err != nil {
		if errors.Is(err, ErrNoToken) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
