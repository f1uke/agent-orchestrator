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
func (f *fakeProvider) FetchPullRequests(context.Context, []ports.SCMPRRef) ([]ports.SCMObservation, error) {
	return nil, nil
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
