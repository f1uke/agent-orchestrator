package integration

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	sessionsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/session"
)

// spawnUndelivered reproduces the incident's row against a REAL git worktree: a
// solo worker that ended its own turn holding work nobody has seen.
//
// Two facts have to be true at once, and they are DIFFERENT facts:
//   - the row is parked (`sleep_reason=undelivered`), because no PR was ever
//     opened from it - the #273 guard;
//   - the worktree is dirty, because the agent left files behind - which is what
//     the teardown refuses on.
//
// The incident had both. Nothing in AO ties them together, so the test states
// both explicitly rather than assuming one implies the other.
func spawnUndelivered(t *testing.T, s *crewStack) domain.SessionRecord {
	t.Helper()
	ctx := context.Background()
	rec, err := s.mgr.Spawn(ctx, ports.SpawnConfig{
		ProjectID: "mer", Kind: domain.KindWorker, Branch: "feature/task", Prompt: "build it",
		TaskSize: domain.TaskSizeMechanical,
	})
	if err != nil {
		t.Fatalf("spawn worker: %v", err)
	}
	tree := rec.Metadata.WorkspacePath
	if tree == "" {
		t.Fatal("worker has no worktree")
	}
	// Two modified files and one brand-new untracked file: the exact shape of the
	// tree the guard saved.
	if err := os.WriteFile(filepath.Join(tree, "README.md"), []byte("seed\nedited by the agent\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tree, "NewFile.swift"), []byte("struct New {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.lcm.ApplyActivitySignal(ctx, rec.ID, ports.ActivitySignal{
		Valid: true, State: domain.ActivityExited, End: &ports.SessionEnd{Reason: "other"},
	}); err != nil {
		t.Fatalf("agent exit signal: %v", err)
	}
	parked, ok, err := s.store.GetSession(ctx, rec.ID)
	if err != nil || !ok {
		t.Fatalf("re-read parked worker: %v", err)
	}
	if parked.SleepReason != domain.SleepReasonUndelivered || parked.IsTerminated {
		t.Fatalf("worker did not park undelivered: suspended=%v reason=%q terminated=%v",
			parked.IsSuspended, parked.SleepReason, parked.IsTerminated)
	}
	return parked
}

// The incident, from the human's side: `ao session kill` on a session parked
// with work nobody has seen. It used to answer success and do NOTHING - the row
// untouched, `updated_at` unmoved, nothing in the change log - which is how the
// board came to have no way of discarding this session at all.
//
// Now it refuses, names the files, and leaves the session exactly as it found
// it: still parked, still resumable, still holding every byte.
func TestUndelivered_KillIsRefusedAndChangesNothing(t *testing.T) {
	ctx := context.Background()
	s := newCrewStack(t)
	rec := spawnUndelivered(t, s)
	tree := rec.Metadata.WorkspacePath

	_, err := s.svc.Kill(ctx, rec.ID, sessionsvc.KillInput{})
	if err == nil {
		t.Fatal("kill SUCCEEDED over a session holding undelivered work: this is the incident")
	}
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) || apiErr.Code != sessionsvc.ErrUndeliveredWork {
		t.Fatalf("refusal = %v, want a %s conflict a caller can act on", err, sessionsvc.ErrUndeliveredWork)
	}
	// The file LIST, not a count: the decision turns on which files they are.
	files, _ := apiErr.Details["files"].([]map[string]any)
	paths := map[string]string{}
	for _, f := range files {
		path, _ := f["path"].(string)
		status, _ := f["status"].(string)
		paths[path] = status
	}
	if paths["README.md"] != "modified" || paths["NewFile.swift"] != "untracked" {
		t.Fatalf("refusal named %v, want the modified README.md and the untracked NewFile.swift", paths)
	}

	after, ok, err := s.store.GetSession(ctx, rec.ID)
	if err != nil || !ok {
		t.Fatalf("re-read: %v", err)
	}
	if after.IsTerminated {
		t.Fatal("a REFUSED kill terminated the session")
	}
	if !after.IsSuspended || after.SleepReason != domain.SleepReasonUndelivered {
		t.Fatalf("the park did not survive the refusal: suspended=%v reason=%q", after.IsSuspended, after.SleepReason)
	}
	if !dirExists(t, tree) {
		t.Fatal("a refused kill removed the worktree")
	}
	if got, err := os.ReadFile(filepath.Join(tree, "NewFile.swift")); err != nil || string(got) != "struct New {}\n" {
		t.Fatalf("the agent's new file = %q, %v; want it byte-for-byte intact", got, err)
	}
	// Nothing half-done either: the reviewer pane, the knowledge rescue and the
	// tmux destroy all sit behind the refusal, so the runtime is untouched.
	if s.rt.destroyed != 0 {
		t.Fatalf("a refused kill destroyed %d runtime(s); it must touch nothing", s.rt.destroyed)
	}
}

// The deliberate path. Told explicitly to discard, the same kill goes through:
// the session terminates, the worktree goes, the card can reach Done - and the
// work is captured at refs/ao/preserved/<id> first, because "discard" says where
// the work goes, not that it may be lost.
func TestUndelivered_DeliberateDiscardTerminatesAndFreesTheSession(t *testing.T) {
	ctx := context.Background()
	s := newCrewStack(t)
	rec := spawnUndelivered(t, s)
	tree := rec.Metadata.WorkspacePath

	out, err := s.svc.Kill(ctx, rec.ID, sessionsvc.KillInput{DiscardUncommitted: true})
	if err != nil {
		t.Fatalf("deliberate discard failed: %v", err)
	}
	if !out.Terminated {
		t.Fatal("the deliberate discard did not terminate the session; the card still cannot reach Done")
	}
	if !out.Freed {
		t.Fatal("the deliberate discard left the worktree on disk")
	}
	if len(out.Discarded) != 2 {
		t.Fatalf("discarded = %+v, want the two files it was shown", out.Discarded)
	}
	if out.PreservedRef == "" {
		t.Fatal("nothing was captured: a discard must stay recoverable")
	}
	if dirExists(t, tree) {
		t.Fatal("worktree still on disk after a discard")
	}

	after, ok, err := s.store.GetSession(ctx, rec.ID)
	if err != nil || !ok {
		t.Fatalf("re-read: %v", err)
	}
	if !after.IsTerminated {
		t.Fatal("row is not terminated: the board reads terminated as Done, so the card still cannot move")
	}
	if after.Termination.Reason != domain.TerminationCauseDiscardWork {
		t.Fatalf("termination cause = %q, want %q so the record says work was thrown away",
			after.Termination.Reason, domain.TerminationCauseDiscardWork)
	}

	// The capture is real: the ref names a commit holding the agent's file.
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not on PATH")
	}
	show := exec.Command(git, "-C", s.repo, "show", out.PreservedRef+":NewFile.swift")
	blob, err := show.CombinedOutput()
	if err != nil || string(blob) != "struct New {}\n" {
		t.Fatalf("preserved ref %s does not hold the discarded file: %v\n%s", out.PreservedRef, err, blob)
	}
}

// A session whose tree is CLEAN was never in the way: the plain kill still just
// works. The park (no PR) and the dirty tree are different facts, and only the
// second one can refuse anything.
func TestUndelivered_ParkedSessionWithACleanTreeStillKills(t *testing.T) {
	ctx := context.Background()
	s := newCrewStack(t)
	rec, err := s.mgr.Spawn(ctx, ports.SpawnConfig{
		ProjectID: "mer", Kind: domain.KindWorker, Branch: "feature/clean", Prompt: "look around",
		TaskSize: domain.TaskSizeMechanical,
	})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if err := s.lcm.ApplyActivitySignal(ctx, rec.ID, ports.ActivitySignal{
		Valid: true, State: domain.ActivityExited, End: &ports.SessionEnd{Reason: "other"},
	}); err != nil {
		t.Fatalf("agent exit: %v", err)
	}
	assertCleanWorktree(t, rec.Metadata.WorkspacePath)

	out, err := s.svc.Kill(ctx, rec.ID, sessionsvc.KillInput{})
	if err != nil {
		t.Fatalf("kill of a parked-but-clean session was refused: %v", err)
	}
	if !out.Terminated || !out.Freed {
		t.Fatalf("kill = %+v, want the session ended and the disk back", out)
	}
}

// A crew member's kill must not be refused because DEV is mid-task. The member
// removes no tree at all - the tree is dev's - so there is nothing for it to
// lose, and refusing would make one agent's work in progress a reason nobody
// could ever close the other's session.
func TestUndelivered_KillingACrewMemberIsNotRefusedByDevsWorkInProgress(t *testing.T) {
	ctx := context.Background()
	s := newCrewStack(t)
	dev, qa := s.spawnCrew(t)
	if err := os.WriteFile(filepath.Join(dev.Metadata.WorkspacePath, "wip.txt"), []byte("dev is mid-task\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	out, err := s.svc.Kill(ctx, qa.ID, sessionsvc.KillInput{})
	if err != nil {
		t.Fatalf("killing a crew member was refused over its DEV's work: %v", err)
	}
	if !out.Terminated {
		t.Fatal("the crew member did not end")
	}
	if !dirExists(t, dev.Metadata.WorkspacePath) {
		t.Fatal("killing the member took dev's tree")
	}
}
