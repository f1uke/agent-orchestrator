package composite

import (
	"context"
	"fmt"

	scmobserve "github.com/aoagents/agent-orchestrator/backend/internal/observe/scm"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Entry names one child provider so the composite can route by the name each
// child stamps onto ports.SCMRepo.Provider during ParseRepository.
type Entry struct {
	// Name identifies the provider, e.g. "github" or "gitlab". It must match
	// the Provider field the child's ParseRepository stamps onto parsed repos.
	Name string
	// Provider is the child adapter implementing the SCM provider contract.
	Provider scmobserve.Provider
}

// Provider dispatches scmobserve.Provider calls to the matching child by
// repo/ref provider name. See doc.go for the routing contract.
type Provider struct {
	ordered []Entry
	byName  map[string]scmobserve.Provider
}

var _ scmobserve.Provider = (*Provider)(nil)

// New builds a composite Provider from an ordered list of named children.
// ParseRepository tries entries in the given order; every other method
// routes by provider name regardless of order.
func New(entries ...Entry) *Provider {
	byName := make(map[string]scmobserve.Provider, len(entries))
	for _, e := range entries {
		byName[e.Name] = e.Provider
	}
	return &Provider{ordered: entries, byName: byName}
}

// lookup resolves the child provider for name, or a clear error if none matches.
func (p *Provider) lookup(name string) (scmobserve.Provider, error) {
	child, ok := p.byName[name]
	if !ok {
		return nil, fmt.Errorf("composite scm: no provider %q", name)
	}
	return child, nil
}

// ParseRepository tries each child in order and returns the first ok result.
// The winning child's ParseRepository is what stamps ports.SCMRepo.Provider,
// which every other method then routes on.
func (p *Provider) ParseRepository(remote string) (ports.SCMRepo, bool) {
	for _, e := range p.ordered {
		if repo, ok := e.Provider.ParseRepository(remote); ok {
			return repo, true
		}
	}
	return ports.SCMRepo{}, false
}

// RepoPRListGuard routes to the child provider named by repo.Provider.
func (p *Provider) RepoPRListGuard(ctx context.Context, repo ports.SCMRepo, etag string) (ports.SCMGuardResult, error) {
	child, err := p.lookup(repo.Provider)
	if err != nil {
		return ports.SCMGuardResult{}, err
	}
	return child.RepoPRListGuard(ctx, repo, etag)
}

// ListOpenPRsByRepo routes to the child provider named by repo.Provider.
func (p *Provider) ListOpenPRsByRepo(ctx context.Context, repo ports.SCMRepo) ([]ports.SCMPRObservation, error) {
	child, err := p.lookup(repo.Provider)
	if err != nil {
		return nil, err
	}
	return child.ListOpenPRsByRepo(ctx, repo)
}

// CommitChecksGuard routes to the child provider named by repo.Provider.
func (p *Provider) CommitChecksGuard(ctx context.Context, repo ports.SCMRepo, headSHA, etag string) (ports.SCMGuardResult, error) {
	child, err := p.lookup(repo.Provider)
	if err != nil {
		return ports.SCMGuardResult{}, err
	}
	return child.CommitChecksGuard(ctx, repo, headSHA, etag)
}

// FetchPullRequests routes the whole batch by the first ref's Repo.Provider.
// Refs could in principle span providers, but the observer already groups
// refs per repo (and therefore per provider) before batching, so routing on
// the first ref is safe for round 1. Revisit if a future caller ever mixes
// providers within one batch.
func (p *Provider) FetchPullRequests(ctx context.Context, refs []ports.SCMPRRef) ([]ports.SCMObservation, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	child, err := p.lookup(refs[0].Repo.Provider)
	if err != nil {
		return nil, err
	}
	return child.FetchPullRequests(ctx, refs)
}

// FetchFailedCheckLogTail routes to the child provider named by repo.Provider.
func (p *Provider) FetchFailedCheckLogTail(ctx context.Context, repo ports.SCMRepo, check ports.SCMCheckObservation) (string, error) {
	child, err := p.lookup(repo.Provider)
	if err != nil {
		return "", err
	}
	return child.FetchFailedCheckLogTail(ctx, repo, check)
}

// FetchReviewThreads routes to the child provider named by ref.Repo.Provider.
func (p *Provider) FetchReviewThreads(ctx context.Context, ref ports.SCMPRRef) (ports.SCMReviewObservation, error) {
	child, err := p.lookup(ref.Repo.Provider)
	if err != nil {
		return ports.SCMReviewObservation{}, err
	}
	return child.FetchReviewThreads(ctx, ref)
}
