package composite

import (
	"context"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type fakeProvider struct {
	name    string
	host    string
	listErr error
	listN   int

	// fetchRefs records every ref FetchPullRequests has ever been called with,
	// across all calls, so tests can assert exactly which refs this provider saw.
	fetchRefs []ports.SCMPRRef
	fetchErr  error
}

func (f *fakeProvider) ParseRepository(remote string) (ports.SCMRepo, bool) {
	if f.host == "" || !strings.Contains(remote, f.host) {
		return ports.SCMRepo{}, false
	}
	return ports.SCMRepo{Provider: f.name, Host: f.host, Repo: "o/n"}, true
}
func (f *fakeProvider) RepoPRListGuard(context.Context, ports.SCMRepo, string) (ports.SCMGuardResult, error) {
	return ports.SCMGuardResult{}, nil
}
func (f *fakeProvider) ListOpenPRsByRepo(context.Context, ports.SCMRepo) ([]ports.SCMPRObservation, error) {
	return make([]ports.SCMPRObservation, f.listN), f.listErr
}
func (f *fakeProvider) CommitChecksGuard(context.Context, ports.SCMRepo, string, string) (ports.SCMGuardResult, error) {
	return ports.SCMGuardResult{}, nil
}
// FetchPullRequests records every ref it receives (so tests can assert which
// refs reached this fake) and returns one observation per ref, tagged with
// this fake's own provider name and the ref's repo/number, so tests can also
// verify the returned observations came from the expected provider.
func (f *fakeProvider) FetchPullRequests(_ context.Context, refs []ports.SCMPRRef) ([]ports.SCMObservation, error) {
	f.fetchRefs = append(f.fetchRefs, refs...)
	if f.fetchErr != nil {
		return nil, f.fetchErr
	}
	obs := make([]ports.SCMObservation, 0, len(refs))
	for _, ref := range refs {
		obs = append(obs, ports.SCMObservation{
			Fetched:  true,
			Provider: f.name,
			Host:     ref.Repo.Host,
			Repo:     ref.Repo.Repo,
			PR:       ports.SCMPRObservation{Number: ref.Number},
		})
	}
	return obs, nil
}
func (f *fakeProvider) FetchFailedCheckLogTail(context.Context, ports.SCMRepo, ports.SCMCheckObservation) (string, error) {
	return "", nil
}
func (f *fakeProvider) FetchReviewThreads(context.Context, ports.SCMPRRef) (ports.SCMReviewObservation, error) {
	return ports.SCMReviewObservation{}, nil
}

func TestParseRoutesByHost(t *testing.T) {
	gl := &fakeProvider{name: "gitlab", host: "gitlab.finnomena.com", listN: 3}
	gh := &fakeProvider{name: "github", host: "github.com", listN: 1}
	c := New(Entry{"gitlab", gl}, Entry{"github", gh})

	repo, ok := c.ParseRepository("https://gitlab.finnomena.com/o/n.git")
	if !ok || repo.Provider != "gitlab" {
		t.Fatalf("parse => %+v ok=%v", repo, ok)
	}
	prs, _ := c.ListOpenPRsByRepo(context.Background(), repo)
	if len(prs) != 3 {
		t.Fatalf("routed to wrong provider, len=%d", len(prs))
	}
	ghRepo, _ := c.ParseRepository("https://github.com/o/n")
	prs2, _ := c.ListOpenPRsByRepo(context.Background(), ghRepo)
	if len(prs2) != 1 {
		t.Fatalf("github route len=%d", len(prs2))
	}
}

func TestUnknownProviderRoutingErrors(t *testing.T) {
	gl := &fakeProvider{name: "gitlab", host: "gitlab.finnomena.com", listN: 3}
	gh := &fakeProvider{name: "github", host: "github.com", listN: 1}
	c := New(Entry{"gitlab", gl}, Entry{"github", gh})

	unknown := ports.SCMRepo{Provider: "nonexistent", Repo: "o/n"}

	prs, err := c.ListOpenPRsByRepo(context.Background(), unknown)
	if err == nil {
		t.Fatalf("ListOpenPRsByRepo: expected error for unknown provider, got nil")
	}
	if !strings.Contains(err.Error(), "nonexistent") {
		t.Fatalf("ListOpenPRsByRepo error %q does not mention unknown provider name", err.Error())
	}
	if prs != nil {
		t.Fatalf("ListOpenPRsByRepo: expected nil result on error, got %+v", prs)
	}

	review, err := c.FetchReviewThreads(context.Background(), ports.SCMPRRef{Repo: unknown, Number: 1})
	if err == nil {
		t.Fatalf("FetchReviewThreads: expected error for unmatched ref.Repo.Provider, got nil")
	}
	if !strings.Contains(err.Error(), "nonexistent") {
		t.Fatalf("FetchReviewThreads error %q does not mention unknown provider name", err.Error())
	}
	if review.Decision != "" || review.Reviews != nil || review.Threads != nil || review.Partial {
		t.Fatalf("FetchReviewThreads: expected zero-value result on error, got %+v", review)
	}
}

// TestFetchPullRequestsSplitsByProvider proves a batch containing refs from
// more than one provider is split so each ref reaches only its own child
// provider, rather than the whole batch being routed by refs[0].Repo.Provider.
func TestFetchPullRequestsSplitsByProvider(t *testing.T) {
	gl := &fakeProvider{name: "gitlab", host: "gitlab.finnomena.com"}
	gh := &fakeProvider{name: "github", host: "github.com"}
	c := New(Entry{"gitlab", gl}, Entry{"github", gh})

	glRef := ports.SCMPRRef{Repo: ports.SCMRepo{Provider: "gitlab", Host: "gitlab.finnomena.com", Repo: "o/n"}, Number: 1}
	ghRef := ports.SCMPRRef{Repo: ports.SCMRepo{Provider: "github", Host: "github.com", Repo: "o/n"}, Number: 2}

	// Interleave gitlab then github so refs[0] is gitlab; under the old
	// refs[0]-routing bug this would send ghRef to the gitlab fake too.
	obs, err := c.FetchPullRequests(context.Background(), []ports.SCMPRRef{glRef, ghRef})
	if err != nil {
		t.Fatalf("FetchPullRequests: unexpected error %v", err)
	}

	if len(gl.fetchRefs) != 1 || gl.fetchRefs[0].Number != 1 {
		t.Fatalf("gitlab fake should have received only its own ref, got %+v", gl.fetchRefs)
	}
	if len(gh.fetchRefs) != 1 || gh.fetchRefs[0].Number != 2 {
		t.Fatalf("github fake should have received only its own ref, got %+v", gh.fetchRefs)
	}

	if len(obs) != 2 {
		t.Fatalf("expected 2 observations (one per ref), got %d: %+v", len(obs), obs)
	}
	byNumber := map[int]ports.SCMObservation{}
	for _, o := range obs {
		byNumber[o.PR.Number] = o
	}
	if byNumber[1].Provider != "gitlab" {
		t.Fatalf("observation for gitlab ref #1 not tagged gitlab: %+v", byNumber[1])
	}
	if byNumber[2].Provider != "github" {
		t.Fatalf("observation for github ref #2 not tagged github: %+v", byNumber[2])
	}
}

// TestFetchPullRequestsUnknownProviderInMixedBatch proves a ref whose
// Repo.Provider matches no configured child does not starve the rest of the
// batch: the valid ref is still fetched, and the unknown ref yields a single
// Fetched:false observation instead of an error for the whole call.
func TestFetchPullRequestsUnknownProviderInMixedBatch(t *testing.T) {
	gl := &fakeProvider{name: "gitlab", host: "gitlab.finnomena.com"}
	gh := &fakeProvider{name: "github", host: "github.com"}
	c := New(Entry{"gitlab", gl}, Entry{"github", gh})

	validRef := ports.SCMPRRef{Repo: ports.SCMRepo{Provider: "github", Host: "github.com", Repo: "o/n"}, Number: 5}
	unknownRef := ports.SCMPRRef{Repo: ports.SCMRepo{Provider: "nonexistent", Repo: "o/n"}, Number: 9}

	obs, err := c.FetchPullRequests(context.Background(), []ports.SCMPRRef{unknownRef, validRef})
	if err != nil {
		t.Fatalf("FetchPullRequests: expected no error for a batch with a valid ref alongside an unknown one, got %v", err)
	}

	if len(gh.fetchRefs) != 1 || gh.fetchRefs[0].Number != 5 {
		t.Fatalf("github fake should still have received the valid ref, got %+v", gh.fetchRefs)
	}
	if len(gl.fetchRefs) != 0 {
		t.Fatalf("gitlab fake should not have received any refs, got %+v", gl.fetchRefs)
	}

	if len(obs) != 2 {
		t.Fatalf("expected 2 observations (1 valid fetch + 1 Fetched:false for the unknown provider), got %d: %+v", len(obs), obs)
	}
	var sawValid, sawUnknownFailed bool
	for _, o := range obs {
		if o.Provider == "github" && o.PR.Number == 5 && o.Fetched {
			sawValid = true
		}
		if !o.Fetched && o.Provider == "" {
			sawUnknownFailed = true
		}
	}
	if !sawValid {
		t.Fatalf("missing fetched observation for the valid github ref: %+v", obs)
	}
	if !sawUnknownFailed {
		t.Fatalf("missing Fetched:false observation for the unknown-provider ref: %+v", obs)
	}
}

func TestParseRepositoryNoMatch(t *testing.T) {
	gl := &fakeProvider{name: "gitlab", host: "gitlab.finnomena.com", listN: 3}
	gh := &fakeProvider{name: "github", host: "github.com", listN: 1}
	c := New(Entry{"gitlab", gl}, Entry{"github", gh})

	repo, ok := c.ParseRepository("https://bitbucket.org/o/n.git")
	if ok {
		t.Fatalf("ParseRepository: expected ok=false for unmatched remote, got ok=true repo=%+v", repo)
	}
	if repo != (ports.SCMRepo{}) {
		t.Fatalf("ParseRepository: expected zero-value SCMRepo, got %+v", repo)
	}
}
