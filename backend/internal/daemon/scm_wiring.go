package daemon

// This file wires the provider-neutral SCM observer into daemon startup using
// the GitHub provider for v1. It keeps provider setup non-blocking for readiness
// by resolving tokens lazily inside the background observer path.

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/scm/composite"
	scmgithub "github.com/aoagents/agent-orchestrator/backend/internal/adapters/scm/github"
	scmgitlab "github.com/aoagents/agent-orchestrator/backend/internal/adapters/scm/gitlab"
	"github.com/aoagents/agent-orchestrator/backend/internal/lifecycle"
	scmobserve "github.com/aoagents/agent-orchestrator/backend/internal/observe/scm"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// startSCMObserver wires the provider-neutral SCM observer. GitHub is always
// wired for v1; when AO_GITLAB_HOST names one or more hosts, the first host
// is also wired (GitLab entry first so it claims its host before GitHub's
// ParseRepository gets a chance). Missing credentials do not fail daemon
// startup; each provider performs a lazy credential check in the background
// observer goroutine, logs one warning, and disables itself before any
// provider API calls.
func startSCMObserver(ctx context.Context, store *sqlite.Store, lcm *lifecycle.Manager, logger *slog.Logger) <-chan struct{} {
	entries, err := buildSCMEntries(logger)
	if err != nil {
		logSCMProviderDisabled(logger, err)
		return closedDone()
	}
	provider := composite.New(entries...)
	observer := scmobserve.New(provider, store, lcm, scmobserve.Config{Logger: logger})
	return observer.Start(ctx)
}

// buildSCMEntries assembles the composite SCM entries: GitHub is always
// included; GitLab is prepended (so it claims its host before GitHub's
// ParseRepository gets a chance) only when AO_GITLAB_HOST names at least one
// host. A GitHub provider construction failure is fatal (returned as err, the
// same as today's GitHub-only behavior); a GitLab provider construction
// failure is logged and skipped so a GitLab misconfiguration never disables
// the GitHub-only path that already worked before this feature existed.
func buildSCMEntries(logger *slog.Logger) ([]composite.Entry, error) {
	github, err := newGitHubSCMProvider(logger)
	if err != nil {
		return nil, err
	}
	entries := []composite.Entry{{Name: "github", Provider: github}}

	if hosts := gitlabHosts(); len(hosts) > 0 {
		host := hosts[0]
		gitlab, err := newGitLabSCMProvider(host, logger)
		if err != nil {
			logger.Warn("scm observer: GitLab provider setup failed, continuing with GitHub only", "host", host, "err", err)
		} else {
			entries = append([]composite.Entry{{Name: "gitlab", Provider: gitlab}}, entries...)
		}
	}
	return entries, nil
}

// gitlabHosts reads AO_GITLAB_HOST, splits it on commas, trims whitespace
// around each entry, and drops empties. An unset or empty env var yields an
// empty slice, which keeps GitLab wiring disabled and behavior identical to
// the GitHub-only path.
func gitlabHosts() []string {
	raw := os.Getenv("AO_GITLAB_HOST")
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var hosts []string
	for _, part := range strings.Split(raw, ",") {
		host := strings.TrimSpace(part)
		if host == "" {
			continue
		}
		hosts = append(hosts, host)
	}
	return hosts
}

// gitlabTokenSource builds the GitLab token precedence used by daemon
// wiring: an env override first, falling back to `glab auth status` for the
// given host.
func gitlabTokenSource(host string) scmgitlab.TokenSource {
	return scmgitlab.FallbackTokenSource{
		scmgitlab.EnvTokenSource{EnvVars: []string{"AO_GITLAB_TOKEN"}},
		&scmgitlab.GlabTokenSource{Host: host},
	}
}

func newGitLabSCMProvider(host string, logger *slog.Logger) (*scmgitlab.Provider, error) {
	// Avoid token preflight on daemon startup; SkipTokenPreflight defers the
	// glab shell-out until the observer's first real poll.
	return scmgitlab.NewProvider(scmgitlab.ProviderOptions{
		Host:               host,
		APIBase:            "https://" + host + "/api/v4",
		Token:              gitlabTokenSource(host),
		SkipTokenPreflight: true,
		Logger:             logger,
	})
}

func newGitHubSCMProvider(logger *slog.Logger) (*scmgithub.Provider, error) {
	tokens := scmgithub.FallbackTokenSource{
		scmgithub.EnvTokenSource{EnvVars: []string{"AO_GITHUB_TOKEN"}},
		&scmgithub.GHTokenSource{},
	}
	// Avoid token preflight on daemon startup and session service construction.
	// GHTokenSource may shell out to `gh`, which is too slow/flaky for the startup
	// readiness path. Provider calls resolve credentials lazily when claim-pr or
	// the background observer actually needs GitHub.
	return scmgithub.NewProvider(scmgithub.ProviderOptions{Token: tokens, SkipTokenPreflight: true, Logger: logger})
}

func logSCMProviderDisabled(logger *slog.Logger, err error) {
	if errors.Is(err, scmgithub.ErrNoToken) || errors.Is(err, scmgithub.ErrAuthFailed) {
		logger.Warn("scm observer disabled: no usable GitHub token", "err", err)
	} else {
		logger.Warn("scm observer disabled: GitHub provider setup failed", "err", err)
	}
}

func closedDone() <-chan struct{} {
	done := make(chan struct{})
	close(done)
	return done
}
