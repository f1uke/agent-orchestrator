package session

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// gitlabFakeSCM is a GitLab-aware SCM provider fake mirroring the composite's
// GitLab routing: it parses a GitLab origin into a gitlab SCMRepo and records
// the ref it was asked to fetch so tests can assert IID + project path routing.
type gitlabFakeSCM struct {
	obs    ports.SCMObservation
	review ports.SCMReviewObservation
	gotRef ports.SCMPRRef
}

func (f *gitlabFakeSCM) ParseRepository(remote string) (ports.SCMRepo, bool) {
	host, projectPath, err := gitlabRepoFromURL(remote)
	if err != nil {
		return ports.SCMRepo{}, false
	}
	return gitlabSCMRepoFromPath(host, projectPath), true
}

func (f *gitlabFakeSCM) FetchPullRequests(_ context.Context, refs []ports.SCMPRRef) ([]ports.SCMObservation, error) {
	if len(refs) > 0 {
		f.gotRef = refs[0]
	}
	return []ports.SCMObservation{f.obs}, nil
}

func (f *gitlabFakeSCM) FetchReviewThreads(context.Context, ports.SCMPRRef) (ports.SCMReviewObservation, error) {
	return f.review, nil
}

// ReplyToThread and ResolveThread satisfy scmProvider; the GitLab claim tests
// in this file only exercise the read (claim) path, so these are unused stubs.
func (f *gitlabFakeSCM) ReplyToThread(context.Context, ports.SCMPRRef, string, string) (ports.SCMReviewCommentObservation, error) {
	return ports.SCMReviewCommentObservation{}, nil
}

func (f *gitlabFakeSCM) ResolveThread(context.Context, ports.SCMPRRef, string) error {
	return nil
}

func TestNormalizePRRefGitLabMergeRequest(t *testing.T) {
	cases := []struct {
		name       string
		ref        string
		origin     string
		wantURL    string
		wantNumber int
		wantErr    bool
	}{
		{
			name:       "nested group MR url",
			ref:        "https://gitlab.finnomena.com/group/sub/proj/-/merge_requests/123",
			origin:     "git@gitlab.finnomena.com:group/sub/proj.git",
			wantURL:    "https://gitlab.finnomena.com/group/sub/proj/-/merge_requests/123",
			wantNumber: 123,
		},
		{
			name:       "trailing slash, query and sub-tab are dropped",
			ref:        "https://gitlab.finnomena.com/group/proj/-/merge_requests/7/diffs?tab=x",
			origin:     "https://gitlab.finnomena.com/group/proj",
			wantURL:    "https://gitlab.finnomena.com/group/proj/-/merge_requests/7",
			wantNumber: 7,
		},
		{
			name:    "missing iid is invalid",
			ref:     "https://gitlab.finnomena.com/group/proj/-/merge_requests/",
			wantErr: true,
		},
		{
			name:    "single-segment project path is invalid",
			ref:     "https://gitlab.finnomena.com/proj/-/merge_requests/5",
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotURL, gotNumber, err := normalizePRRef(tc.ref, tc.origin)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got url=%q number=%d", gotURL, gotNumber)
				}
				return
			}
			if err != nil {
				t.Fatalf("normalizePRRef(%q): %v", tc.ref, err)
			}
			if gotURL != tc.wantURL || gotNumber != tc.wantNumber {
				t.Fatalf("normalizePRRef(%q) = (%q, %d), want (%q, %d)", tc.ref, gotURL, gotNumber, tc.wantURL, tc.wantNumber)
			}
		})
	}
}

func TestParseGitLabMRURLParts(t *testing.T) {
	host, projectPath, iid, err := parseGitLabMRURL("https://gitlab.finnomena.com/group/sub/proj/-/merge_requests/88")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if host != "gitlab.finnomena.com" || projectPath != "group/sub/proj" || iid != 88 {
		t.Fatalf("parts = (%q, %q, %d)", host, projectPath, iid)
	}
	// A GitHub pull URL is not a GitLab MR URL.
	if isGitLabMRURL("https://github.com/acme/repo/pull/7") {
		t.Fatal("github pull URL misdetected as gitlab MR")
	}
}

func TestRequireSameRepoGitLab(t *testing.T) {
	const mrURL = "https://gitlab.finnomena.com/group/proj/-/merge_requests/9"
	cases := []struct {
		name   string
		prURL  string
		origin string
		want   error
	}{
		{"ssh origin matches", mrURL, "git@gitlab.finnomena.com:group/proj.git", nil},
		{"https origin matches", mrURL, "https://gitlab.finnomena.com/group/proj", nil},
		{"blank origin is allowed", mrURL, "", nil},
		{"host mismatch", mrURL, "git@gitlab.other.com:group/proj.git", ErrProjectMismatch},
		{"path mismatch", mrURL, "git@gitlab.finnomena.com:group/elsewhere.git", ErrProjectMismatch},
		{"gitlab MR against github origin", mrURL, "https://github.com/acme/repo", ErrProjectMismatch},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := requireSameRepo(tc.prURL, tc.origin)
			if !errors.Is(err, tc.want) {
				t.Fatalf("requireSameRepo = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestScmRepoForClaimGitLabFallback(t *testing.T) {
	// fakeSCM.ParseRepository only recognizes github origins, so a GitLab origin
	// forces scmRepoForClaim onto the MR-URL fallback path.
	repo, err := scmRepoForClaim(fakeSCM{}, "", "https://gitlab.finnomena.com/group/sub/proj/-/merge_requests/12")
	if err != nil {
		t.Fatalf("scmRepoForClaim: %v", err)
	}
	if repo.Provider != "gitlab" || repo.Host != "gitlab.finnomena.com" || repo.Owner != "group/sub" || repo.Name != "proj" || repo.Repo != "group/sub/proj" {
		t.Fatalf("repo = %+v", repo)
	}
}

func TestClaimPRGitLabMergeRequest(t *testing.T) {
	st := newFakeStore()
	now := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker, Metadata: domain.SessionMetadata{WorkspacePath: "/ws"}}
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", RepoOriginURL: "git@gitlab.finnomena.com:group/sub/proj.git"}
	const mrURL = "https://gitlab.finnomena.com/group/sub/proj/-/merge_requests/42"
	st.pr["mer-1"] = domain.PRFacts{URL: mrURL, Number: 42, CI: domain.CIPassing, UpdatedAt: now}

	scm := &gitlabFakeSCM{obs: ports.SCMObservation{
		Fetched:  true,
		Provider: "gitlab",
		Host:     "gitlab.finnomena.com",
		Repo:     "group/sub/proj",
		PR:       ports.SCMPRObservation{URL: mrURL, Number: 42},
	}}
	svc := NewWithDeps(Deps{Store: st, PRClaimer: fakePRClaimer{}, SCM: scm, Clock: func() time.Time { return now }})

	// Passing the MR URL with a trailing sub-tab must still route to IID 42.
	res, err := svc.ClaimPR(context.Background(), "mer-1", mrURL+"/diffs", ClaimPROptions{AllowTakeover: true})
	if err != nil {
		t.Fatalf("claim gitlab MR: %v", err)
	}
	if scm.gotRef.Number != 42 || scm.gotRef.Repo.Provider != "gitlab" || scm.gotRef.Repo.Repo != "group/sub/proj" {
		t.Fatalf("fetched ref = %+v", scm.gotRef)
	}
	if scm.gotRef.URL != mrURL {
		t.Fatalf("fetched ref URL = %q, want canonical %q", scm.gotRef.URL, mrURL)
	}
	if len(res.PRs) != 1 || res.PRs[0].URL != mrURL || res.PRs[0].Number != 42 {
		t.Fatalf("claim result = %+v", res.PRs)
	}
}

func TestClaimPRGitLabProjectMismatch(t *testing.T) {
	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker, Metadata: domain.SessionMetadata{WorkspacePath: "/ws"}}
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", RepoOriginURL: "git@gitlab.finnomena.com:group/proj.git"}
	scm := &gitlabFakeSCM{obs: ports.SCMObservation{Fetched: true, Provider: "gitlab", Host: "gitlab.finnomena.com", Repo: "group/other"}}
	svc := NewWithDeps(Deps{Store: st, PRClaimer: fakePRClaimer{}, SCM: scm})

	_, err := svc.ClaimPR(context.Background(), "mer-1", "https://gitlab.finnomena.com/group/other/-/merge_requests/1", ClaimPROptions{AllowTakeover: true})
	if !errors.Is(err, ErrProjectMismatch) {
		t.Fatalf("err = %v, want ErrProjectMismatch", err)
	}
}

func TestNormalizePRRefGitHubUnchanged(t *testing.T) {
	// GitHub inputs must keep resolving exactly as before the GitLab support.
	url, n, err := normalizePRRef("https://github.com/acme/repo/pull/7", "https://github.com/acme/repo")
	if err != nil || url != "https://github.com/acme/repo/pull/7" || n != 7 {
		t.Fatalf("github url = (%q,%d,%v)", url, n, err)
	}
	url, n, err = normalizePRRef("#42", "git@github.com:acme/repo.git")
	if err != nil || url != "https://github.com/acme/repo/pull/42" || n != 42 {
		t.Fatalf("github number = (%q,%d,%v)", url, n, err)
	}
	if !strings.Contains("https://github.com/acme/repo/pull/42", "github.com") {
		t.Fatal("sanity")
	}
}
