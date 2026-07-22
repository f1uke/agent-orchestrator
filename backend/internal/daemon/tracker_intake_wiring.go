package daemon

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	trackergithub "github.com/aoagents/agent-orchestrator/backend/internal/adapters/tracker/github"
	trackergitlab "github.com/aoagents/agent-orchestrator/backend/internal/adapters/tracker/gitlab"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/looptelemetry"
	trackerintake "github.com/aoagents/agent-orchestrator/backend/internal/observe/trackerintake"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
	sessionsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/session"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// startTrackerIntake wires the opt-in issue-intake loop. The observer always
// runs — Poll re-reads each project's config on every tick and skips
// projects with intake disabled, so a project enabling intake after daemon
// boot is picked up on the next tick without a restart. Each adapter stays
// lazy so daemon readiness is not blocked by credential probing or a gh/glab
// CLI call; no token is resolved until some enabled project is actually
// polled. GitHub is always wired; GitLab is added only when AO_GITLAB_HOST
// names at least one host.
func startTrackerIntake(ctx context.Context, store *sqlite.Store, sessions *sessionsvc.Service, reg *looptelemetry.Registry, logger *slog.Logger) <-chan struct{} {
	rec := reg.Register(looptelemetry.Spec{
		Name:        "tracker-intake",
		Display:     "Issue polling",
		Description: "Scans intake-enabled projects for available issues and spawns sessions for them.",
		Interval:    trackerintake.DefaultTickInterval,
	})
	adapters := map[domain.TrackerProvider]ports.Tracker{
		domain.TrackerProviderGitHub: newLazyGitHubTracker(logger),
	}
	if hosts := gitlabHosts(); len(hosts) > 0 {
		adapters[domain.TrackerProviderGitLab] = newLazyGitLabTracker(hosts[0], logger)
	}
	resolver := trackerintake.MultiTrackerResolver{Adapters: adapters}
	observer := trackerintake.New(resolver, store, sessions, trackerintake.Config{Logger: logger, OnTick: rec.Tick})
	return observer.Start(ctx)
}

// ---------------------------------------------------------------------------
// GitHub lazy adapter (token sourced from env or gh CLI fallback)
// ---------------------------------------------------------------------------

type lazyGitHubTracker struct {
	logger  *slog.Logger
	tokens  *trackerTokenSource
	mu      sync.Mutex
	tracker ports.Tracker
}

func newLazyGitHubTracker(logger *slog.Logger) *lazyGitHubTracker {
	return &lazyGitHubTracker{logger: logger, tokens: &trackerTokenSource{}}
}

func (t *lazyGitHubTracker) Get(ctx context.Context, id domain.TrackerID) (domain.Issue, error) {
	tracker, err := t.resolve()
	if err != nil {
		return domain.Issue{}, err
	}
	return tracker.Get(ctx, id)
}

func (t *lazyGitHubTracker) List(ctx context.Context, repo domain.TrackerRepo, filter domain.ListFilter) ([]domain.Issue, error) {
	tracker, err := t.resolve()
	if err != nil {
		return nil, err
	}
	return tracker.List(ctx, repo, filter)
}

func (t *lazyGitHubTracker) Preflight(ctx context.Context) error {
	tracker, err := t.resolve()
	if err != nil {
		return err
	}
	return tracker.Preflight(ctx)
}

func (t *lazyGitHubTracker) resolve() (ports.Tracker, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.tracker != nil {
		return t.tracker, nil
	}
	tracker, err := trackergithub.New(trackergithub.Options{Token: t.tokens})
	if err != nil {
		if errors.Is(err, trackergithub.ErrNoToken) && t.logger != nil {
			t.logger.Warn("tracker intake disabled: no usable GitHub token", "err", err)
		}
		return nil, err
	}
	t.tracker = tracker
	return tracker, nil
}

const (
	trackerTokenCacheTTL       = 5 * time.Minute
	trackerTokenCommandTimeout = 5 * time.Second
)

// trackerTokenSource mirrors the SCM credential precedence while returning the
// tracker adapter's own ErrNoToken sentinel.
type trackerTokenSource struct {
	mu        sync.Mutex
	token     string
	expiresAt time.Time
}

func (s *trackerTokenSource) Token(ctx context.Context) (string, error) {
	env := trackergithub.EnvTokenSource{EnvVars: []string{"AO_GITHUB_TOKEN"}}
	if tok, err := env.Token(ctx); err == nil {
		return tok, nil
	} else if !errors.Is(err, trackergithub.ErrNoToken) {
		return "", err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	if s.token != "" && now.Before(s.expiresAt) {
		return s.token, nil
	}
	cmdCtx, cancel := context.WithTimeout(ctx, trackerTokenCommandTimeout)
	defer cancel()
	out, err := aoprocess.CommandContext(cmdCtx, "gh", "auth", "token").Output()
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(string(out))
	if token == "" {
		return "", trackergithub.ErrNoToken
	}
	s.token = token
	s.expiresAt = now.Add(trackerTokenCacheTTL)
	return token, nil
}

// ---------------------------------------------------------------------------
// GitLab lazy adapter (token sourced from env or glab CLI fallback)
// ---------------------------------------------------------------------------

type lazyGitLabTracker struct {
	logger  *slog.Logger
	host    string
	tokens  *gitlabTrackerTokenSource
	mu      sync.Mutex
	tracker ports.Tracker
}

func newLazyGitLabTracker(host string, logger *slog.Logger) *lazyGitLabTracker {
	return &lazyGitLabTracker{logger: logger, host: host, tokens: &gitlabTrackerTokenSource{host: host}}
}

func (t *lazyGitLabTracker) Get(ctx context.Context, id domain.TrackerID) (domain.Issue, error) {
	tracker, err := t.resolve()
	if err != nil {
		return domain.Issue{}, err
	}
	return tracker.Get(ctx, id)
}

func (t *lazyGitLabTracker) List(ctx context.Context, repo domain.TrackerRepo, filter domain.ListFilter) ([]domain.Issue, error) {
	tracker, err := t.resolve()
	if err != nil {
		return nil, err
	}
	return tracker.List(ctx, repo, filter)
}

func (t *lazyGitLabTracker) Preflight(ctx context.Context) error {
	tracker, err := t.resolve()
	if err != nil {
		return err
	}
	return tracker.Preflight(ctx)
}

func (t *lazyGitLabTracker) resolve() (ports.Tracker, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.tracker != nil {
		return t.tracker, nil
	}
	tracker, err := trackergitlab.New(trackergitlab.Options{
		APIBase: "https://" + t.host + "/api/v4",
		Token:   t.tokens,
	})
	if err != nil {
		if errors.Is(err, trackergitlab.ErrNoToken) && t.logger != nil {
			t.logger.Warn("tracker intake disabled: no usable GitLab token", "host", t.host, "err", err)
		}
		return nil, err
	}
	t.tracker = tracker
	return tracker, nil
}

// gitlabTrackerTokenSource mirrors the SCM credential precedence (env var,
// then glab CLI) while returning the tracker adapter's own ErrNoToken
// sentinel.
type gitlabTrackerTokenSource struct {
	host string

	mu        sync.Mutex
	token     string
	expiresAt time.Time
}

func (s *gitlabTrackerTokenSource) Token(ctx context.Context) (string, error) {
	env := trackergitlab.EnvTokenSource{EnvVars: []string{"AO_GITLAB_TOKEN"}}
	if tok, err := env.Token(ctx); err == nil {
		return tok, nil
	} else if !errors.Is(err, trackergitlab.ErrNoToken) {
		return "", err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	if s.token != "" && now.Before(s.expiresAt) {
		return s.token, nil
	}
	cmdCtx, cancel := context.WithTimeout(ctx, trackerTokenCommandTimeout)
	defer cancel()
	out, err := aoprocess.CommandContext(cmdCtx, "glab", "auth", "status", "--show-token", "--hostname", s.host).Output()
	if err != nil {
		return "", err
	}
	token, err := parseGlabAuthToken(string(out))
	if err != nil {
		return "", err
	}
	s.token = token
	s.expiresAt = now.Add(trackerTokenCacheTTL)
	return token, nil
}

// parseGlabAuthToken extracts the token from `glab auth status --show-token`
// output. It mirrors internal/adapters/scm/gitlab's parseGlabToken: scan for
// a line containing "Token" and return the trimmed text after the last
// colon, skipping masked tokens (which contain "*").
func parseGlabAuthToken(out string) (string, error) {
	for _, line := range strings.Split(out, "\n") {
		if !strings.Contains(line, "Token") {
			continue
		}
		idx := strings.LastIndex(line, ":")
		if idx < 0 {
			continue
		}
		tok := strings.TrimSpace(line[idx+1:])
		if tok != "" && !strings.Contains(tok, "*") { // masked tokens contain asterisks
			return tok, nil
		}
	}
	return "", trackergitlab.ErrNoToken
}
