package gitlab

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func retargetRef() ports.SCMPRRef {
	return ports.SCMPRRef{Repo: ports.SCMRepo{Provider: "gitlab", Repo: "grp/proj"}, Number: 7}
}

// The happy path asserts the METHOD, PATH and BODY GitLab actually receives.
// Asserting only "no error" would pass against a server that ignored the
// request entirely, which is the failure this write must never have.
func TestRetargetPR_PutsTargetBranch(t *testing.T) {
	var gotMethod, gotBody string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/projects/grp%2Fproj/merge_requests/7", func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		var decoded map[string]string
		if err := json.NewDecoder(r.Body).Decode(&decoded); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		gotBody = decoded["target_branch"]
		_, _ = w.Write([]byte(`{"iid":7,"target_branch":"develop"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	if err := newTestProvider(t, srv.URL).RetargetPR(context.Background(), retargetRef(), "develop"); err != nil {
		t.Fatalf("RetargetPR: %v", err)
	}
	if gotMethod != http.MethodPut {
		t.Errorf("method = %s, want PUT", gotMethod)
	}
	if gotBody != "develop" {
		t.Errorf("target_branch = %q, want develop", gotBody)
	}
}

// GitLab answers 400 when the target branch is unusable. That MUST become
// ErrSCMInvalid: without it the error falls through to a generic failure and
// gets rendered as "SCM unavailable", blaming the service for bad input.
func TestRetargetPR_BadRequestMapsToSCMInvalid(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/projects/grp%2Fproj/merge_requests/7", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"message":"Target branch is invalid"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	err := newTestProvider(t, srv.URL).RetargetPR(context.Background(), retargetRef(), "nope")
	if !errors.Is(err, ports.ErrSCMInvalid) {
		t.Fatalf("err = %v, want ErrSCMInvalid", err)
	}
}

func TestRetargetPR_AuthFailedMapsToForbidden(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/projects/grp%2Fproj/merge_requests/7", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	err := newTestProvider(t, srv.URL).RetargetPR(context.Background(), retargetRef(), "develop")
	if !errors.Is(err, ports.ErrSCMForbidden) {
		t.Fatalf("err = %v, want ErrSCMForbidden", err)
	}
}

func TestRetargetPR_NotFoundMapsToSCMNotFound(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/projects/grp%2Fproj/merge_requests/7", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	err := newTestProvider(t, srv.URL).RetargetPR(context.Background(), retargetRef(), "develop")
	if !errors.Is(err, ports.ErrSCMNotFound) {
		t.Fatalf("err = %v, want ErrSCMNotFound", err)
	}
}

func TestBranchExists(t *testing.T) {
	repo := ports.SCMRepo{Provider: "gitlab", Repo: "grp/proj"}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/projects/grp%2Fproj/repository/branches/develop", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"name":"develop"}`))
	})
	mux.HandleFunc("/api/v4/projects/grp%2Fproj/repository/branches/ghost", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	p := newTestProvider(t, srv.URL)

	ok, err := p.BranchExists(context.Background(), repo, "develop")
	if err != nil || !ok {
		t.Fatalf("BranchExists(develop) = %v, %v; want true, nil", ok, err)
	}
	// A missing branch is a normal answer, not an error: the caller refuses the
	// retarget rather than reporting the SCM as broken.
	ok, err = p.BranchExists(context.Background(), repo, "ghost")
	if err != nil {
		t.Fatalf("BranchExists(ghost) errored: %v", err)
	}
	if ok {
		t.Fatal("BranchExists(ghost) = true, want false")
	}
}

// A branch name with a slash must be path-escaped, or `release/2.1` would
// address the wrong endpoint and report a real branch as missing.
func TestBranchExists_EscapesSlashes(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		_, _ = w.Write([]byte(`{"name":"release/2.1"}`))
	}))
	defer srv.Close()

	ok, err := newTestProvider(t, srv.URL).BranchExists(context.Background(),
		ports.SCMRepo{Provider: "gitlab", Repo: "grp/proj"}, "release/2.1")
	if err != nil || !ok {
		t.Fatalf("BranchExists = %v, %v", ok, err)
	}
	if want := "/api/v4/projects/grp%2Fproj/repository/branches/release%2F2.1"; gotPath != want {
		t.Fatalf("path = %q, want %q", gotPath, want)
	}
}
