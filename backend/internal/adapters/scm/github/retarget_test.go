package github

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func ghRetargetRef() ports.SCMPRRef {
	return ports.SCMPRRef{Repo: ports.SCMRepo{Provider: "github", Owner: "o", Name: "r"}, Number: 7}
}

// Asserts the METHOD, PATH and BODY GitHub receives. A test that only checked
// for a nil error would pass against a server that ignored the request, which
// is exactly the silent no-op this write must never have.
func TestRetargetPR_PatchesBase(t *testing.T) {
	f := newFakeGH(t)
	var gotMethod, gotBase string
	f.on(http.MethodPatch, "/repos/o/r/pulls/7", func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		var decoded map[string]string
		if err := json.NewDecoder(r.Body).Decode(&decoded); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		gotBase = decoded["base"]
		_, _ = w.Write([]byte(`{"number":7,"base":{"ref":"develop"}}`))
	})

	if err := newProviderForTest(t, f).RetargetPR(context.Background(), ghRetargetRef(), "develop"); err != nil {
		t.Fatalf("RetargetPR: %v", err)
	}
	if gotMethod != http.MethodPatch {
		t.Errorf("method = %s, want PATCH", gotMethod)
	}
	if gotBase != "develop" {
		t.Errorf("base = %q, want develop", gotBase)
	}
}

// GitHub answers 422 when it refuses the change on its merits. That must reach
// the caller as ErrSCMInvalid, not as a generic failure rendered "unavailable".
func TestRetargetPR_UnprocessableMapsToSCMInvalid(t *testing.T) {
	f := newFakeGH(t)
	f.on(http.MethodPatch, "/repos/o/r/pulls/7", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"message":"Validation Failed"}`))
	})

	err := newProviderForTest(t, f).RetargetPR(context.Background(), ghRetargetRef(), "nope")
	if !errors.Is(err, ports.ErrSCMInvalid) {
		t.Fatalf("err = %v, want ErrSCMInvalid", err)
	}
}

func TestRetargetPR_AuthFailedMapsToForbidden(t *testing.T) {
	f := newFakeGH(t)
	f.on(http.MethodPatch, "/repos/o/r/pulls/7", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})

	err := newProviderForTest(t, f).RetargetPR(context.Background(), ghRetargetRef(), "develop")
	if !errors.Is(err, ports.ErrSCMForbidden) {
		t.Fatalf("err = %v, want ErrSCMForbidden", err)
	}
}

func TestRetargetPR_NotFoundMapsToSCMNotFound(t *testing.T) {
	f := newFakeGH(t)
	f.on(http.MethodPatch, "/repos/o/r/pulls/7", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	err := newProviderForTest(t, f).RetargetPR(context.Background(), ghRetargetRef(), "develop")
	if !errors.Is(err, ports.ErrSCMNotFound) {
		t.Fatalf("err = %v, want ErrSCMNotFound", err)
	}
}

func TestBranchExists(t *testing.T) {
	repo := ports.SCMRepo{Provider: "github", Owner: "o", Name: "r"}
	f := newFakeGH(t)
	f.on(http.MethodGet, "/repos/o/r/branches/develop", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"name":"develop"}`))
	})
	f.on(http.MethodGet, "/repos/o/r/branches/ghost", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	p := newProviderForTest(t, f)

	ok, err := p.BranchExists(context.Background(), repo, "develop")
	if err != nil || !ok {
		t.Fatalf("BranchExists(develop) = %v, %v; want true, nil", ok, err)
	}
	// A missing branch is a normal answer the caller acts on, not a failure.
	ok, err = p.BranchExists(context.Background(), repo, "ghost")
	if err != nil {
		t.Fatalf("BranchExists(ghost) errored: %v", err)
	}
	if ok {
		t.Fatal("BranchExists(ghost) = true, want false")
	}
}

// An empty branch must be refused before any request goes out — asking GitHub
// about "" would address the branches collection and wrongly answer "exists".
func TestBranchExists_EmptyBranchMakesNoRequest(t *testing.T) {
	f := newFakeGH(t)
	ok, err := newProviderForTest(t, f).BranchExists(context.Background(),
		ports.SCMRepo{Provider: "github", Owner: "o", Name: "r"}, "  ")
	if err != nil {
		t.Fatalf("BranchExists(empty) errored: %v", err)
	}
	if ok {
		t.Fatal("BranchExists(empty) = true, want false")
	}
	if n := len(f.calls()); n != 0 {
		t.Fatalf("made %d request(s) for an empty branch, want 0", n)
	}
}
