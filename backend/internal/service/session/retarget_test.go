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

// retargetSCM records what the forge was asked to do, so tests can assert the
// forge-side effect rather than just a nil error. Asserting "no error" would
// pass whether or not the retarget ever happened.
type retargetSCM struct {
	fakeSCM

	exists      bool
	existsErr   error
	retargetErr error

	retargetCalls int
	gotRef        ports.SCMPRRef
	gotTarget     string
	branchAsked   string
}

func (f *retargetSCM) BranchExists(_ context.Context, _ ports.SCMRepo, branch string) (bool, error) {
	f.branchAsked = branch
	return f.exists, f.existsErr
}

func (f *retargetSCM) RetargetPR(_ context.Context, ref ports.SCMPRRef, target string) error {
	f.retargetCalls++
	f.gotRef, f.gotTarget = ref, target
	return f.retargetErr
}

const retargetPRURL = "https://github.com/o/r/pull/7"

// newRetargetFixture builds a session owning one PR in the given state.
func newRetargetFixture(t *testing.T, prTarget string, merged, closed bool) (*Service, *fakeStore, *retargetSCM) {
	t.Helper()
	st := newFakeStore()
	st.sessions["s-1"] = domain.SessionRecord{
		ID: "s-1", ProjectID: "p-1", Kind: domain.KindWorker, PRTarget: "main",
	}
	st.projects["p-1"] = domain.ProjectRecord{
		ID: "p-1", RepoOriginURL: "https://github.com/o/r.git",
		Config: domain.ProjectConfig{DefaultBranch: "main"},
	}
	st.pr["s-1"] = domain.PRFacts{
		URL: retargetPRURL, Number: 7,
		TargetBranch: prTarget, Merged: merged, Closed: closed,
	}
	scm := &retargetSCM{exists: true}
	return &Service{store: st, scm: scm, clock: func() time.Time { return time.Unix(0, 0).UTC() }}, st, scm
}

// The headline contract: the forge is written FIRST and the local value is
// persisted only after it accepts.
func TestSetTargetBranch_RetargetsForgeThenPersists(t *testing.T) {
	svc, st, scm := newRetargetFixture(t, "main", false, false)

	if _, err := svc.SetTargetBranch(context.Background(), "s-1", "develop"); err != nil {
		t.Fatalf("SetTargetBranch: %v", err)
	}
	if scm.retargetCalls != 1 {
		t.Fatalf("retarget calls = %d, want 1", scm.retargetCalls)
	}
	if scm.gotTarget != "develop" || scm.gotRef.Number != 7 {
		t.Fatalf("forge asked for target=%q pr=%d, want develop/7", scm.gotTarget, scm.gotRef.Number)
	}
	if got := st.sessions["s-1"].PRTarget; got != "develop" {
		t.Fatalf("stored PRTarget = %q, want develop", got)
	}
	// The stored PR row must move too, or the read model would keep resolving
	// the OLD target from the PR (which outranks the stored value) until the
	// observer next polls -- the UI would show the edit as having failed.
	if got := st.pr["s-1"].TargetBranch; got != "develop" {
		t.Fatalf("stored PR TargetBranch = %q, want develop", got)
	}
}

// THE load-bearing test. If the forge refuses, AO must keep nothing: divergence
// has to be structurally impossible, not merely reported.
func TestSetTargetBranch_ForgeFailurePersistsNothing(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"invalid", ports.ErrSCMInvalid},
		{"forbidden", ports.ErrSCMForbidden},
		{"not found", ports.ErrSCMNotFound},
		{"transport", errors.New("connection reset")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, st, scm := newRetargetFixture(t, "main", false, false)
			scm.retargetErr = tc.err

			if _, err := svc.SetTargetBranch(context.Background(), "s-1", "develop"); err == nil {
				t.Fatal("expected an error when the forge refuses")
			}
			if got := st.sessions["s-1"].PRTarget; got != "main" {
				t.Fatalf("stored PRTarget = %q, want main (unchanged) -- AO and the forge diverged", got)
			}
			if got := st.pr["s-1"].TargetBranch; got != "main" {
				t.Fatalf("stored PR TargetBranch = %q, want main (unchanged)", got)
			}
		})
	}
}

// A nonexistent branch is refused BEFORE the write.
//
// This is not defensive politeness — on GitLab it is the ONLY guard. Verified
// against a real instance (example-org, MR !3041): a PUT naming a branch that does
// not exist returns 200 and GitLab silently points the merge request at the
// missing branch. GitHub refuses the same request with 422. So an implementation
// that skipped this check and relied on the provider to object would leave every
// GitLab merge request one typo away from aiming at nothing.
func TestSetTargetBranch_RefusesMissingBranchWithoutWriting(t *testing.T) {
	svc, st, scm := newRetargetFixture(t, "main", false, false)
	scm.exists = false

	_, err := svc.SetTargetBranch(context.Background(), "s-1", "ghost")
	if !errors.Is(err, ErrTargetBranchNotFound) {
		t.Fatalf("err = %v, want ErrTargetBranchNotFound", err)
	}
	if scm.retargetCalls != 0 {
		t.Fatalf("retarget calls = %d, want 0 -- must validate before writing", scm.retargetCalls)
	}
	if got := st.sessions["s-1"].PRTarget; got != "main" {
		t.Fatalf("stored PRTarget = %q, want main (unchanged)", got)
	}
}

// Idempotence: retargeting to the branch the PR is already on is a no-op, not
// an error, and must not spend an outbound write.
func TestSetTargetBranch_AlreadyOnTargetIsNoOp(t *testing.T) {
	svc, st, scm := newRetargetFixture(t, "develop", false, false)

	if _, err := svc.SetTargetBranch(context.Background(), "s-1", "develop"); err != nil {
		t.Fatalf("retargeting to the current target must be a no-op, got: %v", err)
	}
	if scm.retargetCalls != 0 {
		t.Fatalf("retarget calls = %d, want 0 for a no-op", scm.retargetCalls)
	}
	// Still reconciles the stored value, which may have drifted from the PR.
	if got := st.sessions["s-1"].PRTarget; got != "develop" {
		t.Fatalf("stored PRTarget = %q, want develop", got)
	}
}

// With no open PR there is nothing to retarget: record the intent, write
// nothing outbound.
func TestSetTargetBranch_NoOpenPRPersistsWithoutWriting(t *testing.T) {
	svc, st, scm := newRetargetFixture(t, "main", true, false) // merged PR

	if _, err := svc.SetTargetBranch(context.Background(), "s-1", "develop"); err != nil {
		t.Fatalf("SetTargetBranch: %v", err)
	}
	if scm.retargetCalls != 0 {
		t.Fatalf("retarget calls = %d, want 0 when no PR is open", scm.retargetCalls)
	}
	if got := st.sessions["s-1"].PRTarget; got != "develop" {
		t.Fatalf("stored PRTarget = %q, want develop", got)
	}
}

func TestSetTargetBranch_RejectsEmptyTarget(t *testing.T) {
	svc, st, scm := newRetargetFixture(t, "main", false, false)

	if _, err := svc.SetTargetBranch(context.Background(), "s-1", "   "); err == nil {
		t.Fatal("expected an error for an empty target")
	}
	if scm.retargetCalls != 0 {
		t.Fatalf("retarget calls = %d, want 0", scm.retargetCalls)
	}
	if got := st.sessions["s-1"].PRTarget; got != "main" {
		t.Fatalf("stored PRTarget = %q, want main (unchanged)", got)
	}
}

// Every failure must say WHY in terms the human can act on. A retarget refused
// for bad input must never surface as "the SCM is unavailable".
func TestSetTargetBranch_FailuresExplainThemselves(t *testing.T) {
	cases := []struct {
		name    string
		scmErr  error
		wantSub string
		notSub  string
	}{
		{"invalid", ports.ErrSCMInvalid, "refused", "unavailable"},
		{"forbidden", ports.ErrSCMForbidden, "permission", "unavailable"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, _, scm := newRetargetFixture(t, "main", false, false)
			scm.retargetErr = tc.scmErr

			_, err := svc.SetTargetBranch(context.Background(), "s-1", "develop")
			if err == nil {
				t.Fatal("expected an error")
			}
			msg := strings.ToLower(err.Error())
			if !strings.Contains(msg, tc.wantSub) {
				t.Fatalf("error %q does not explain the cause (want %q)", msg, tc.wantSub)
			}
			if strings.Contains(msg, tc.notSub) {
				t.Fatalf("error %q blames the service for a %s failure", msg, tc.name)
			}
		})
	}
}
