package integration

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/observe/reclaimer"
	scmobserve "github.com/aoagents/agent-orchestrator/backend/internal/observe/scm"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/reclaimsettings"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
)

// A merged PR must end the whole TASK, not just the session that owns the PR.
//
// These tests drive the REAL path the bug lives in: a canned SCM provider ->
// the real observe/scm.Observer -> the real lifecycle reducer -> the real
// session manager, over a real git worktree. Nothing calls Teardown or Kill
// directly, because the whole point of the defect is that the merge path is the
// one route to termination that skipped the crew fan-out.

// mergeObserverFor wires the real SCM observer over an existing crew stack, and
// gives the project an origin URL so the observer can resolve a repo for it.
// The returned provider is the only faked layer.
func mergeObserverFor(t *testing.T, s *crewStack) (*cannedSCMProvider, *scmobserve.Observer) {
	t.Helper()
	ctx := context.Background()
	proj, ok, err := s.store.GetProject(ctx, "mer")
	if err != nil || !ok {
		t.Fatalf("GetProject: ok=%v err=%v", ok, err)
	}
	proj.RepoOriginURL = scmTestOriginURL
	if err := s.store.UpsertProject(ctx, proj); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	provider := newCannedSCMProvider()
	obs := scmobserve.New(provider, s.store, s.lcm, scmobserve.Config{
		Tick:   time.Hour,
		Clock:  func() time.Time { return s.now },
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	return provider, obs
}

// mergePR makes the provider report branch's PR as already merged, which is what
// the observer sees on the poll after a human clicks Merge.
func mergePR(p *cannedSCMProvider, branch string, num int) string {
	const headSHA = "cafef00d"
	prURL := "https://github.com/octocat/hello/pull/" + strconv.Itoa(num)
	p.detected[branch] = ports.SCMPRObservation{
		URL: prURL, Number: num, SourceBranch: branch, HeadRepo: scmTestRepo.Repo,
		TargetBranch: "main", HeadSHA: headSHA, Merged: true,
	}
	obs := mergedSCMObservation(prURL, num, headSHA)
	obs.PR.SourceBranch = branch
	p.observations[num] = obs
	return prURL
}

// TestCrew_DevPRMergedEndsTheWholeCrew is the regression this branch exists for.
// It fails on main-fluke with qa still running.
func TestCrew_DevPRMergedEndsTheWholeCrew(t *testing.T) {
	ctx := context.Background()
	s := newCrewStack(t)
	dev, qa := s.spawnCrew(t)
	tree := dev.Metadata.WorkspacePath

	// qa was mid-turn when the merge landed: an edit it had not committed.
	const inFlight = "qa was still writing this\n"
	if err := os.WriteFile(filepath.Join(tree, "qa-notes.txt"), []byte(inFlight), 0o600); err != nil {
		t.Fatal(err)
	}

	// dev's own runtime was already reaped when it stood down to let qa be born
	// (one awake at a time), so only what the MERGE destroys counts.
	destroyedBefore := len(s.rt.destroyedHandles)

	provider, observer := mergeObserverFor(t, s)
	mergePR(provider, dev.Metadata.Branch, 77)
	if err := observer.Poll(ctx); err != nil {
		t.Fatalf("Poll: %v", err)
	}
	destroyedByMerge := s.rt.destroyedHandles[destroyedBefore:]

	devRec, ok, err := s.store.GetSession(ctx, dev.ID)
	if err != nil || !ok {
		t.Fatalf("read dev: %v", err)
	}
	if !devRec.IsTerminated {
		t.Fatal("the merged PR did not terminate dev; the fixture is not exercising the merge path")
	}
	if devRec.Termination.Reason != domain.TerminationCauseWorkComplete {
		t.Fatalf("dev termination reason = %q, want %q", devRec.Termination.Reason, domain.TerminationCauseWorkComplete)
	}

	qaRec, ok, err := s.store.GetSession(ctx, qa.ID)
	if err != nil || !ok {
		t.Fatalf("read qa: %v", err)
	}
	if !qaRec.IsTerminated {
		t.Fatal("dev's PR merged and its qa is STILL RUNNING: the merge path skipped the crew teardown")
	}
	if qaRec.Termination.Reason != domain.TerminationCauseWorkComplete {
		t.Fatalf("qa termination reason = %q, want %q (it ended with its dev)", qaRec.Termination.Reason, domain.TerminationCauseWorkComplete)
	}

	// qa's RUNTIME is gone - that is what stops it burning turns. dev's runtime
	// is left to the reclaim pass, exactly as a solo merged worker's is.
	if !containsString(destroyedByMerge, "h-"+string(qa.ID)) {
		t.Fatalf("qa's runtime was never destroyed: the merge destroyed %v", destroyedByMerge)
	}
	if containsString(destroyedByMerge, "h-"+string(dev.ID)) {
		t.Fatalf("the merge path destroyed dev's runtime; on main-fluke it does not: %v", destroyedByMerge)
	}

	// The disk is still the reclaimer's, unchanged: the shared worktree stands,
	// with qa's uncommitted work byte-for-byte intact.
	if !dirExists(t, tree) {
		t.Fatal("the merge path removed the shared worktree; reclaim owns that, after its grace")
	}
	got, err := os.ReadFile(filepath.Join(tree, "qa-notes.txt"))
	if err != nil || string(got) != inFlight {
		t.Fatalf("qa's uncommitted work = %q, %v; want it byte-for-byte intact", got, err)
	}
}

// TestSolo_PRMergedIsUnCHANGED pins the path almost every task on this machine
// takes: a solo worker still just terminates, keeping its worktree and its
// runtime for the reclaim pass.
func TestSolo_PRMergedIsUnchanged(t *testing.T) {
	ctx := context.Background()
	s := newCrewStack(t)
	solo, err := s.mgr.Spawn(ctx, ports.SpawnConfig{
		ProjectID: "mer", Kind: domain.KindWorker, Branch: "feature/solo", Prompt: "ship it",
		TaskSize: domain.TaskSizeMechanical,
	})
	if err != nil {
		t.Fatalf("spawn solo: %v", err)
	}
	tree := solo.Metadata.WorkspacePath
	if tree == "" {
		t.Fatal("solo worker has no worktree")
	}
	destroyedBefore := len(s.rt.destroyedHandles)

	provider, observer := mergeObserverFor(t, s)
	mergePR(provider, solo.Metadata.Branch, 91)
	if err := observer.Poll(ctx); err != nil {
		t.Fatalf("Poll: %v", err)
	}

	rec, ok, err := s.store.GetSession(ctx, solo.ID)
	if err != nil || !ok {
		t.Fatalf("read solo: %v", err)
	}
	if !rec.IsTerminated {
		t.Fatal("a solo worker no longer terminates when its PR merges")
	}
	if rec.Termination.Reason != domain.TerminationCauseWorkComplete {
		t.Fatalf("solo termination reason = %q, want %q", rec.Termination.Reason, domain.TerminationCauseWorkComplete)
	}
	if rec.InCrew() {
		t.Fatalf("a mechanical spawn formed a crew: %q/%q", rec.CrewID, rec.CrewRole)
	}
	if len(s.rt.destroyedHandles) != destroyedBefore {
		t.Fatalf("the merge path destroyed a runtime for a solo worker: %v", s.rt.destroyedHandles[destroyedBefore:])
	}
	if !dirExists(t, tree) {
		t.Fatal("the merge path removed a solo worker's worktree")
	}
}

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// TestCrew_MergeThenReclaimFreesTheTree is the other half of the contract: a
// member ended by the MERGE route must leave the worktree in exactly the state
// #224's reclaim expects, so the reclaimer frees the disk on its ordinary
// schedule (arm on one pass, act on the next, like any finished solo session)
// and never has to refuse a tree because it is still ending the crew itself.
//
// The tree is asserted CLEAN on purpose: gitworktree refuses to remove a dirty
// worktree, so a dirty tree would make this pass on the dirty refusal rather
// than on the refcount.
func TestCrew_MergeThenReclaimFreesTheTree(t *testing.T) {
	ctx := context.Background()
	s := newCrewStack(t)
	dev, qa := s.spawnCrew(t)
	tree := dev.Metadata.WorkspacePath
	assertCleanWorktree(t, tree)

	provider, observer := mergeObserverFor(t, s)
	mergePR(provider, dev.Metadata.Branch, 78)
	if err := observer.Poll(ctx); err != nil {
		t.Fatalf("Poll: %v", err)
	}
	for _, id := range []domain.SessionID{dev.ID, qa.ID} {
		rec, ok, err := s.store.GetSession(ctx, id)
		if err != nil || !ok {
			t.Fatalf("read %s: ok=%v err=%v", id, ok, err)
		}
		if !rec.IsTerminated {
			t.Fatalf("%s survived the merge", id)
		}
	}
	if !dirExists(t, tree) {
		t.Fatal("the merge itself removed the worktree; the disk is auto-reclaim's, after its grace")
	}

	log := &memAudit{}
	const graceMinutes = 15
	r := reclaimer.New(s.svc, fixedSettings{reclaimsettings.Settings{Enabled: true, GraceMinutes: graceMinutes}},
		reclaimer.Config{
			Clock:    func() time.Time { return s.now },
			SelfPath: filepath.Join(t.TempDir(), "elsewhere"),
			Audit:    log,
		})
	// The first pass arms the grace, the second acts on it - the reclaimer's
	// ordinary two-pass schedule for any finished session.
	s.tick(t, r, graceMinutes*time.Minute)
	s.tick(t, r, graceMinutes*time.Minute)

	if dirExists(t, tree) {
		t.Fatalf("the worktree survived reclaim after the whole crew finished: %s (log %v)", tree, log.reasons())
	}
	for _, reason := range log.reasons() {
		if reason == sessionmanager.ReasonWorkspaceShared {
			t.Fatalf("reclaim still had to refuse a shared worktree: %v. The merge path should have ended the crew already", log.reasons())
		}
	}
}
