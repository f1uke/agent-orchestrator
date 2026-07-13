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
var _ scmobserve.ReviewThreadWriter = (*Provider)(nil)

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

// BaseBranchGuard routes to the child provider named by repo.Provider.
func (p *Provider) BaseBranchGuard(ctx context.Context, repo ports.SCMRepo, branch, etag string) (ports.SCMGuardResult, error) {
	child, err := p.lookup(repo.Provider)
	if err != nil {
		return ports.SCMGuardResult{}, err
	}
	return child.BaseBranchGuard(ctx, repo, branch, etag)
}

// FetchPullRequests splits the batch by ref.Repo.Provider and fetches each
// provider's group separately. The observer's batches are built by ranging a
// map of all tracked PRs across every project (github and gitlab interleaved,
// in random map order) and slicing the result into fixed-size chunks with no
// per-provider grouping (see selectRefreshCandidates/chunks in
// internal/observe/scm/observer.go); a single chunk can therefore contain refs
// for more than one provider. Routing the whole batch by refs[0].Repo.Provider
// would silently send another provider's refs to the wrong child, which
// 404s and looks like a refresh failure.
//
// A ref whose Repo.Provider matches no configured child is a caller/config
// bug (e.g. a stored PR row from a provider no longer configured). Rather
// than failing the whole batch — which would starve every other, valid ref
// in the same chunk — that ref gets a single Fetched:false observation
// (never inferred as closed) and processing continues for the remaining
// groups. If a known child's FetchPullRequests call itself errors, that
// error is returned immediately: the observer's caller (Poll) already
// tolerates a batch-level error by marking every ref in the chunk as a
// refresh failure and retrying next tick, so there is no need to degrade a
// child transport error into per-ref Fetched:false here.
func (p *Provider) FetchPullRequests(ctx context.Context, refs []ports.SCMPRRef) ([]ports.SCMObservation, error) {
	if len(refs) == 0 {
		return nil, nil
	}

	// Group refs by provider name while preserving first-seen provider order,
	// so results are assembled deterministically regardless of map iteration.
	var order []string
	byProvider := make(map[string][]ports.SCMPRRef, len(refs))
	for _, ref := range refs {
		name := ref.Repo.Provider
		if _, ok := byProvider[name]; !ok {
			order = append(order, name)
		}
		byProvider[name] = append(byProvider[name], ref)
	}

	out := make([]ports.SCMObservation, 0, len(refs))
	for _, name := range order {
		group := byProvider[name]
		child, err := p.lookup(name)
		if err != nil {
			for range group {
				out = append(out, ports.SCMObservation{Fetched: false})
			}
			continue
		}
		obs, err := child.FetchPullRequests(ctx, group)
		if err != nil {
			return nil, err
		}
		out = append(out, obs...)
	}
	return out, nil
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

// ReplyToThread routes to the child provider named by ref.Repo.Provider. If
// that child does not implement scmobserve.ReviewThreadWriter, it returns a
// clear "does not support" error rather than panicking on the type assertion.
func (p *Provider) ReplyToThread(ctx context.Context, ref ports.SCMPRRef, threadID, body string) (ports.SCMReviewCommentObservation, error) {
	child, err := p.lookup(ref.Repo.Provider)
	if err != nil {
		return ports.SCMReviewCommentObservation{}, err
	}
	w, ok := child.(scmobserve.ReviewThreadWriter)
	if !ok {
		return ports.SCMReviewCommentObservation{}, fmt.Errorf("composite scm: provider %q does not support thread writes", ref.Repo.Provider)
	}
	return w.ReplyToThread(ctx, ref, threadID, body)
}

// ResolveThread routes to the child provider named by ref.Repo.Provider. If
// that child does not implement scmobserve.ReviewThreadWriter, it returns a
// clear "does not support" error rather than panicking on the type assertion.
func (p *Provider) ResolveThread(ctx context.Context, ref ports.SCMPRRef, threadID string) error {
	child, err := p.lookup(ref.Repo.Provider)
	if err != nil {
		return err
	}
	w, ok := child.(scmobserve.ReviewThreadWriter)
	if !ok {
		return fmt.Errorf("composite scm: provider %q does not support thread writes", ref.Repo.Provider)
	}
	return w.ResolveThread(ctx, ref, threadID)
}
