package integration

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/msgqueue"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
)

// ONE AWAKE AT A TIME, against a real git worktree, a real SQLite store and the
// real lifecycle reducer.
//
// The failure these tests exist to rule out cannot be expressed by a fake
// workspace: two agents editing and testing ONE checkout at the same time. So the
// tree is real and the assertions are about what is true of the ROWS the daemon
// will act on next - which member has a runtime, which one comes back after a
// restart, and whether anybody is left holding a lock nobody can take.

// awakeMembers returns the ids of every member of a crew that currently has a
// runtime. The invariant this whole file defends is len(awakeMembers) <= 1.
func (s *crewStack) awakeMembers(t *testing.T, ids ...domain.SessionID) []domain.SessionID {
	t.Helper()
	ctx := context.Background()
	out := make([]domain.SessionID, 0, len(ids))
	for _, id := range ids {
		rec, ok, err := s.store.GetSession(ctx, id)
		if err != nil || !ok {
			t.Fatalf("read %s: %v", id, err)
		}
		if rec.Awake() {
			out = append(out, id)
		}
	}
	return out
}

// assertOneAwake is the invariant, asserted as a sentence.
func (s *crewStack) assertOneAwake(t *testing.T, want domain.SessionID, ids ...domain.SessionID) {
	t.Helper()
	got := s.awakeMembers(t, ids...)
	if len(got) != 1 || got[0] != want {
		t.Fatalf("awake members = %v, want exactly [%s]", got, want)
	}
}

func (s *crewStack) record(t *testing.T, id domain.SessionID) domain.SessionRecord {
	t.Helper()
	rec, ok, err := s.store.GetSession(context.Background(), id)
	if err != nil || !ok {
		t.Fatalf("read %s: %v", id, err)
	}
	return rec
}

// TestCrewOneAwake_SpawnCannotBringUpASecondMember closes the SPAWN route. A crew
// is formed by putting a second session into a running task's tree; if that were
// allowed while the task is running, the very first thing a crew ever did would be
// the thing this rule forbids.
func TestCrewOneAwake_SpawnCannotBringUpASecondMember(t *testing.T) {
	ctx := context.Background()
	s := newCrewStack(t)
	dev, err := s.mgr.Spawn(ctx, ports.SpawnConfig{
		ProjectID: "mer", Kind: domain.KindWorker, Branch: "feature/task", Prompt: "build it",
		// mechanical is what makes this a SOLO spawn: a standard task now forms
		// its own crew, which is a different scenario from the one under test.
		TaskSize: domain.TaskSizeMechanical,
	})
	if err != nil {
		t.Fatal(err)
	}

	before := s.rt.created
	_, err = s.mgr.Spawn(ctx, ports.SpawnConfig{
		ProjectID: "mer", Kind: domain.KindWorker, Prompt: "test it",
		CrewOf: dev.ID, CrewRole: domain.CrewRoleQA,
	})
	if !errors.Is(err, sessionmanager.ErrCrewBusy) {
		t.Fatalf("spawning a crew member into a RUNNING dev's tree returned %v, want ErrCrewBusy", err)
	}
	if s.rt.created != before {
		t.Fatal("the refused spawn still launched an agent into the shared tree")
	}
	// And it left nothing behind: dev is still a solo session, untouched.
	if got := s.record(t, dev.ID); got.InCrew() || got.IsSuspended || got.IsTerminated {
		t.Fatalf("the refused crew spawn changed dev: crew=%q suspended=%v terminated=%v", got.CrewID, got.IsSuspended, got.IsTerminated)
	}
	all, err := s.store.ListAllSessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Fatalf("sessions after a refused crew spawn = %d, want 1: the seed row must not survive", len(all))
	}
}

// TestCrewOneAwake_EveryWakeRouteIsClosed walks each route by which a second
// member could come up while the first holds the slot. They are separate code
// paths - Restore and Resume each short-circuit through their own adopt-if-alive
// branch before the shared relaunch - so each one is asserted, not assumed.
func TestCrewOneAwake_EveryWakeRouteIsClosed(t *testing.T) {
	routes := []struct {
		name string
		// prepare puts dev into the state this route starts from.
		prepare func(t *testing.T, s *crewStack, dev domain.SessionRecord)
		wake    func(s *crewStack, dev domain.SessionID) error
	}{
		{
			name:    "resume a suspended member",
			prepare: func(*testing.T, *crewStack, domain.SessionRecord) {},
			wake: func(s *crewStack, dev domain.SessionID) error {
				_, err := s.mgr.Resume(context.Background(), dev)
				return err
			},
		},
		{
			name:    "wake a suspended member (opening its card)",
			prepare: func(*testing.T, *crewStack, domain.SessionRecord) {},
			wake: func(s *crewStack, dev domain.SessionID) error {
				_, err := s.mgr.Wake(context.Background(), dev)
				return err
			},
		},
		{
			name:    "restart a suspended member",
			prepare: func(*testing.T, *crewStack, domain.SessionRecord) {},
			wake: func(s *crewStack, dev domain.SessionID) error {
				_, err := s.mgr.Restart(context.Background(), dev)
				return err
			},
		},
		{
			name: "restore a terminated member",
			prepare: func(t *testing.T, s *crewStack, dev domain.SessionRecord) {
				t.Helper()
				if err := s.lcm.MarkTerminated(context.Background(), dev.ID, domain.TerminationCauseKill); err != nil {
					t.Fatal(err)
				}
			},
			wake: func(s *crewStack, dev domain.SessionID) error {
				_, err := s.mgr.Restore(context.Background(), dev)
				return err
			},
		},
	}

	for _, route := range routes {
		t.Run(route.name, func(t *testing.T) {
			s := newCrewStack(t)
			dev, qa := s.spawnCrew(t) // dev stood down; qa holds the slot
			s.assertOneAwake(t, qa.ID, dev.ID, qa.ID)
			route.prepare(t, s, dev)

			before := s.rt.created
			err := route.wake(s, dev.ID)
			if !errors.Is(err, sessionmanager.ErrCrewBusy) {
				t.Fatalf("%s while %s holds the slot returned %v, want ErrCrewBusy", route.name, qa.ID, err)
			}
			if s.rt.created != before {
				t.Fatal("the refused wake still launched an agent into the shared tree")
			}
			s.assertOneAwake(t, qa.ID, dev.ID, qa.ID)
			// The refusal must be free: the holder is untouched and the refused
			// member is still exactly as restorable as it was.
			if qaRec := s.record(t, qa.ID); qaRec.IsSuspended || qaRec.IsTerminated {
				t.Fatalf("the holder was disturbed by a refused wake: suspended=%v terminated=%v", qaRec.IsSuspended, qaRec.IsTerminated)
			}
			if _, err := os.Stat(dev.Metadata.WorkspacePath); err != nil {
				t.Fatalf("the shared worktree did not survive a refused wake: %v", err)
			}
		})
	}
}

// TestCrewOneAwake_ARefusedRestartDoesNotKillTheSession is the trap inside the
// restart route. restartInPlace destroys the runtime and marks the session
// terminated BEFORE it relaunches, so a refusal discovered at the relaunch would
// cost the user their agent and give nothing back. The refusal has to come first.
func TestCrewOneAwake_ARefusedRestartDoesNotKillTheSession(t *testing.T) {
	ctx := context.Background()
	s := newCrewStack(t)
	dev, qa := s.spawnCrew(t)

	// Hand the slot to dev so the member being restarted is the LIVE one.
	if _, err := s.mgr.HandOverCrewSlot(ctx, qa.ID, dev.ID); err != nil {
		t.Fatalf("hand over: %v", err)
	}
	// Now contrive the forbidden state the guard exists for: bring qa back behind
	// the manager's back, exactly as a bug or a stale row would.
	qaRec := s.record(t, qa.ID)
	if err := s.lcm.MarkSpawned(ctx, qa.ID, qaRec.Metadata); err != nil {
		t.Fatal(err)
	}
	s.rt.setLive(qaRec.Metadata.RuntimeHandleID, true) // and its pane really is back

	destroyedBefore := s.rt.destroyed
	if _, err := s.mgr.Restart(ctx, dev.ID); !errors.Is(err, sessionmanager.ErrCrewBusy) {
		t.Fatalf("restart returned %v, want ErrCrewBusy", err)
	}
	if s.rt.destroyed != destroyedBefore {
		t.Fatal("the refused restart destroyed the running agent it was refusing to restart")
	}
	if got := s.record(t, dev.ID); got.IsTerminated {
		t.Fatal("the refused restart left dev terminated: a refusal must cost nothing")
	}
}

// TestCrewOneAwake_ADeadHolderDoesNotDeadlockTheCrew. The holder's row says it is
// awake; its agent died mid-turn and nothing has noticed. A lock that can be
// dropped and never recovered is worse than no lock, so the guard probes rather
// than trusting the row, and takes the slot off a corpse.
func TestCrewOneAwake_ADeadHolderDoesNotDeadlockTheCrew(t *testing.T) {
	ctx := context.Background()
	s := newCrewStack(t)
	dev, qa := s.spawnCrew(t)
	qaRec := s.record(t, qa.ID)

	// qa's agent dies. Nothing marks the row: that is the whole problem.
	s.rt.kill(qaRec.Metadata.RuntimeHandleID)
	if !s.record(t, qa.ID).Awake() {
		t.Fatal("precondition: qa's row must still claim the slot")
	}

	if _, err := s.mgr.Resume(ctx, dev.ID); err != nil {
		t.Fatalf("the crew is deadlocked behind a dead holder: %v", err)
	}
	s.assertOneAwake(t, dev.ID, dev.ID, qa.ID)
	// The corpse is SUSPENDED, not terminated: its card stays in its lane, its
	// worktree and transcript are intact, and it is one Resume from coming back.
	got := s.record(t, qa.ID)
	if !got.IsSuspended || got.IsTerminated {
		t.Fatalf("the released dead holder = suspended %v, terminated %v; want suspended and recoverable", got.IsSuspended, got.IsTerminated)
	}
	if _, err := os.Stat(dev.Metadata.WorkspacePath); err != nil {
		t.Fatalf("the shared worktree was lost: %v", err)
	}
}

// TestCrewOneAwake_AnUnprobeableHolderKeepsTheSlot. A failed probe is not proof of
// death - a rule this daemon leans on everywhere - so it must NOT free the slot.
// Refusing costs one retry; guessing costs two agents in one checkout.
func TestCrewOneAwake_AnUnprobeableHolderKeepsTheSlot(t *testing.T) {
	ctx := context.Background()
	s := newCrewStack(t)
	dev, qa := s.spawnCrew(t)

	s.rt.aliveErr = errors.New("tmux server not responding")
	if _, err := s.mgr.Resume(ctx, dev.ID); !errors.Is(err, sessionmanager.ErrCrewBusy) {
		t.Fatalf("resume with an unprobeable holder returned %v, want ErrCrewBusy", err)
	}
	if got := s.record(t, qa.ID); got.IsSuspended {
		t.Fatal("a failed probe was treated as proof the holder was dead")
	}

	// And it is not a deadlock: the next attempt probes again.
	s.rt.aliveErr = nil
	s.rt.kill(s.record(t, qa.ID).Metadata.RuntimeHandleID)
	if _, err := s.mgr.Resume(ctx, dev.ID); err != nil {
		t.Fatalf("the retry after a recovered probe failed: %v", err)
	}
	s.assertOneAwake(t, dev.ID, dev.ID, qa.ID)
}

// TestCrewOneAwake_HandoverPassesTheSlotAndOnlyTheSlot is the mechanism. No
// scheduler, no policy about who goes next: release, then wake.
func TestCrewOneAwake_HandoverPassesTheSlotAndOnlyTheSlot(t *testing.T) {
	ctx := context.Background()
	s := newCrewStack(t)
	dev, qa := s.spawnCrew(t)
	tree := dev.Metadata.WorkspacePath

	// Work in progress belongs to the TREE, not to whoever is awake in it.
	const wip = "half-written analysis\n"
	if err := os.WriteFile(filepath.Join(tree, "wip.txt"), []byte(wip), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := s.mgr.HandOverCrewSlot(ctx, qa.ID, dev.ID); err != nil {
		t.Fatalf("hand the slot to dev: %v", err)
	}
	s.assertOneAwake(t, dev.ID, dev.ID, qa.ID)

	if _, err := s.mgr.HandOverCrewSlot(ctx, dev.ID, qa.ID); err != nil {
		t.Fatalf("hand the slot back to qa: %v", err)
	}
	s.assertOneAwake(t, qa.ID, dev.ID, qa.ID)

	got, err := os.ReadFile(filepath.Join(tree, "wip.txt"))
	if err != nil || string(got) != wip {
		t.Fatalf("uncommitted work after two handovers = %q, %v; want it untouched", got, err)
	}
	// Observable without a stored field: the derivation answers at any moment.
	holder, ok, err := s.mgr.CrewSlotHolder(ctx, dev.ID)
	if err != nil || !ok || holder.ID != qa.ID {
		t.Fatalf("CrewSlotHolder = %s (%v), %v; want %s", holder.ID, ok, err, qa.ID)
	}
}

// TestCrewOneAwake_AFailedHandoverLeavesTheSlotFreeNotHeld is why the release
// comes first. If the taker cannot come up, the slot must be FREE - so either
// member can still be brought up - rather than held by a member nobody is using.
func TestCrewOneAwake_AFailedHandoverLeavesTheSlotFreeNotHeld(t *testing.T) {
	ctx := context.Background()
	s := newCrewStack(t)
	dev, qa := s.spawnCrew(t)

	// dev's pane really is gone (it stood down), so the wake must go through the
	// full relaunch rather than adopting a live runtime - and that relaunch fails.
	s.rt.createErr = errors.New("tmux: no space left on device")
	if _, err := s.mgr.HandOverCrewSlot(ctx, qa.ID, dev.ID); err == nil {
		t.Fatal("the handover reported success although the taker never came up")
	}
	if got := s.awakeMembers(t, dev.ID, qa.ID); len(got) != 0 {
		t.Fatalf("awake members after a failed handover = %v, want none: the slot must be free", got)
	}

	// Free means takeable - by either member, which is what "cannot deadlock"
	// means in practice.
	if _, err := s.mgr.Resume(ctx, dev.ID); err != nil {
		t.Fatalf("the slot could not be taken after a failed handover: %v", err)
	}
	s.assertOneAwake(t, dev.ID, dev.ID, qa.ID)
}

// TestCrewOneAwake_HandoverRefusesWhatWouldNotBeAHandover: the mechanism has to be
// impossible to misuse into two awake members or into a lost slot.
func TestCrewOneAwake_HandoverRefusesWhatWouldNotBeAHandover(t *testing.T) {
	ctx := context.Background()
	s := newCrewStack(t)
	dev, qa := s.spawnCrew(t)

	if _, err := s.mgr.HandOverCrewSlot(ctx, qa.ID, qa.ID); !errors.Is(err, sessionmanager.ErrInvalidCrew) {
		t.Fatalf("handing the slot to its own holder returned %v, want ErrInvalidCrew", err)
	}
	solo, err := s.mgr.Spawn(ctx, ports.SpawnConfig{
		ProjectID: "mer", Kind: domain.KindWorker, Branch: "feature/other", Prompt: "unrelated",
		// mechanical is what makes this a SOLO spawn: a standard task now forms
		// its own crew, which is a different scenario from the one under test.
		TaskSize: domain.TaskSizeMechanical,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.mgr.HandOverCrewSlot(ctx, qa.ID, solo.ID); !errors.Is(err, sessionmanager.ErrInvalidCrew) {
		t.Fatalf("handing the slot to a session in another task returned %v, want ErrInvalidCrew", err)
	}
	// Nothing was released on the way to either refusal.
	s.assertOneAwake(t, qa.ID, dev.ID, qa.ID)
}

// TestCrewOneAwake_SurvivesACleanDaemonRestart. The holder is terminated and
// markered by the shutdown sweep; the released member is skipped because it is
// suspended. Boot brings back exactly the one that was running.
func TestCrewOneAwake_SurvivesACleanDaemonRestart(t *testing.T) {
	ctx := context.Background()
	s := newCrewStack(t)
	dev, qa := s.spawnCrew(t)
	tree := dev.Metadata.WorkspacePath
	const wip = "work that must survive a restart\n"
	if err := os.WriteFile(filepath.Join(tree, "wip.txt"), []byte(wip), 0o600); err != nil {
		t.Fatal(err)
	}

	// Shutdown, then boot.
	if err := s.mgr.SaveAndTeardownAll(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	s.rt.killAll() // every pane died with the daemon
	if err := s.mgr.Reconcile(ctx); err != nil {
		t.Fatalf("boot: %v", err)
	}

	s.assertOneAwake(t, qa.ID, dev.ID, qa.ID)
	if got := s.record(t, dev.ID); !got.IsSuspended {
		t.Fatalf("the released member came back or was lost across the restart: suspended=%v terminated=%v", got.IsSuspended, got.IsTerminated)
	}
	if _, err := os.Stat(tree); err != nil {
		t.Fatalf("the shared worktree did not survive the restart: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(tree, "wip.txt"))
	if err != nil || string(got) != wip {
		t.Fatalf("uncommitted work after the restart = %q, %v; want it intact", got, err)
	}
}

// TestCrewOneAwake_SurvivesACrash is the same restart WITHOUT a clean shutdown -
// the path #224 found the trap on, because reconcileLive reaches
// saveAndTeardownOne at BOOT and that path force-destroys.
func TestCrewOneAwake_SurvivesACrash(t *testing.T) {
	ctx := context.Background()
	s := newCrewStack(t)
	dev, qa := s.spawnCrew(t)
	tree := dev.Metadata.WorkspacePath

	// The daemon was killed. No shutdown sweep ran; the panes died with it.
	s.rt.killAll()
	if err := s.mgr.Reconcile(ctx); err != nil {
		t.Fatalf("boot after a crash: %v", err)
	}

	s.assertOneAwake(t, qa.ID, dev.ID, qa.ID)
	if got := s.record(t, dev.ID); !got.IsSuspended {
		t.Fatalf("the released member did not stay released across a crash: suspended=%v terminated=%v", got.IsSuspended, got.IsTerminated)
	}
	if _, err := os.Stat(tree); err != nil {
		t.Fatalf("the shared worktree was destroyed by boot reconciliation: %v", err)
	}
}

// TestCrewOneAwake_BootBringsUpAtMostOneEvenWithTwoMarkers. Only one member can
// carry a restore marker under this rule - but a database written before it, or
// one a bug got at, can hold two, and boot must not answer that by running both
// agents in one checkout. dev wins; the other is left restorable.
func TestCrewOneAwake_BootBringsUpAtMostOneEvenWithTwoMarkers(t *testing.T) {
	ctx := context.Background()
	s := newCrewStack(t)
	dev, qa := s.spawnCrew(t)

	// Contrive the corrupt state: both members awake, then a clean shutdown, which
	// markers both of them.
	if err := s.lcm.MarkSpawned(ctx, dev.ID, s.record(t, dev.ID).Metadata); err != nil {
		t.Fatal(err)
	}
	if len(s.awakeMembers(t, dev.ID, qa.ID)) != 2 {
		t.Fatal("precondition: both members must be awake for this to test anything")
	}
	if err := s.mgr.SaveAndTeardownAll(ctx); err != nil {
		t.Fatal(err)
	}
	s.rt.killAll()

	if err := s.mgr.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	s.assertOneAwake(t, dev.ID, dev.ID, qa.ID)
	// The one that lost is not lost: its marker is deliberately kept, so it comes
	// back the moment the slot frees.
	rows, err := s.store.ListSessionWorktrees(ctx, qa.ID)
	if err != nil || len(rows) == 0 {
		t.Fatalf("the refused member's restore marker was consumed: %d rows, %v", len(rows), err)
	}
}

// TestCrewOneAwake_TheReviewerReadsOnlyInTheGap. The ephemeral reviewer is a
// READER, so it is not in the exclusion set: it never takes the slot and never
// stops a member from taking one. But a reader can still see a half-written file,
// so it runs in the gap between one member standing down and the next waking.
func TestCrewOneAwake_TheReviewerReadsOnlyInTheGap(t *testing.T) {
	ctx := context.Background()
	s := newCrewStack(t)
	dev, qa := s.spawnCrew(t)

	writer, busy, err := s.mgr.CrewTreeWriter(ctx, s.record(t, dev.ID))
	if err != nil {
		t.Fatal(err)
	}
	if !busy || writer != qa.ID {
		t.Fatalf("tree writer while qa is awake = %s (busy %v), want %s", writer, busy, qa.ID)
	}

	// The gap: nobody awake. The reviewer may read.
	if err := s.mgr.ReleaseCrewSlot(ctx, qa.ID); err != nil {
		t.Fatal(err)
	}
	if _, busy, err = s.mgr.CrewTreeWriter(ctx, s.record(t, dev.ID)); err != nil || busy {
		t.Fatalf("tree writer in the handoff gap = busy %v, %v; want nobody", busy, err)
	}
	// Reading never takes the slot: after the reviewer's turn either member can
	// still be woken, which is what "not a third participant" means.
	if _, err := s.mgr.Resume(ctx, dev.ID); err != nil {
		t.Fatalf("the slot was not takeable after the reviewer's gap: %v", err)
	}
	s.assertOneAwake(t, dev.ID, dev.ID, qa.ID)
}

// TestSolo_TheTreeIsNeverReportedBusy is the preservation guard for the reviewer
// rule. A solo worker's checkout has exactly one writer and that writer IS the
// session under review; reviewing while it works is what AO has always done.
func TestSolo_TheTreeIsNeverReportedBusy(t *testing.T) {
	ctx := context.Background()
	s := newCrewStack(t)
	rec, err := s.mgr.Spawn(ctx, ports.SpawnConfig{
		ProjectID: "mer", Kind: domain.KindWorker, Branch: "feature/solo", Prompt: "work",
		// mechanical is what makes this a SOLO spawn: a standard task now forms
		// its own crew, which is a different scenario from the one under test.
		TaskSize: domain.TaskSizeMechanical,
	})
	if err != nil {
		t.Fatal(err)
	}
	writer, busy, err := s.mgr.CrewTreeWriter(ctx, rec)
	if err != nil || busy {
		t.Fatalf("a solo worker reported writer %q busy=%v, %v; want nobody, always", writer, busy, err)
	}
}

// TestSolo_LifecycleIsUnchanged is the preservation suite for the routes this
// slice put a guard on. A solo session is in no crew, so every guard must be a
// zero-value no-op: spawn, suspend, wake, restart, restore across a restart, and
// teardown all behave exactly as they do on main.
func TestSolo_LifecycleIsUnchanged(t *testing.T) {
	ctx := context.Background()
	s := newCrewStack(t)
	rec, err := s.mgr.Spawn(ctx, ports.SpawnConfig{
		ProjectID: "mer", Kind: domain.KindWorker, Branch: "feature/solo", Prompt: "work",
		// mechanical is what makes this a SOLO spawn: a standard task now forms
		// its own crew, which is a different scenario from the one under test.
		TaskSize: domain.TaskSizeMechanical,
	})
	if err != nil {
		t.Fatalf("solo spawn: %v", err)
	}
	tree := rec.Metadata.WorkspacePath
	if rec.InCrew() || rec.CrewID != "" || rec.CrewRole != "" {
		t.Fatalf("an ordinary spawn produced crew fields: %q/%q", rec.CrewID, rec.CrewRole)
	}
	if holder, ok, err := s.mgr.CrewSlotHolder(ctx, rec.ID); err != nil || ok {
		t.Fatalf("a solo session reported a crew slot holder %s (%v), %v", holder.ID, ok, err)
	}

	// Suspend (what the idle sweep does) then wake: resumes in place.
	if err := s.lcm.MarkSuspended(ctx, rec.ID); err != nil {
		t.Fatalf("solo suspend: %v", err)
	}
	if err := s.mgr.SuspendRuntime(ctx, rec.ID); err != nil {
		t.Fatalf("solo suspend runtime: %v", err)
	}
	woken, err := s.mgr.Wake(ctx, rec.ID)
	if err != nil {
		t.Fatalf("solo wake: %v", err)
	}
	if woken.IsSuspended || woken.Metadata.WorkspacePath != tree {
		t.Fatalf("woken solo session = suspended %v, workspace %q, want live in %q", woken.IsSuspended, woken.Metadata.WorkspacePath, tree)
	}

	// Restart in place: same id, same tree.
	restarted, err := s.mgr.Restart(ctx, rec.ID)
	if err != nil {
		t.Fatalf("solo restart: %v", err)
	}
	if restarted.IsTerminated || restarted.Metadata.WorkspacePath != tree {
		t.Fatalf("restarted solo session = terminated %v, workspace %q", restarted.IsTerminated, restarted.Metadata.WorkspacePath)
	}

	// A daemon restart: saved, torn down, and relaunched on boot.
	if err := s.mgr.SaveAndTeardownAll(ctx); err != nil {
		t.Fatalf("solo shutdown: %v", err)
	}
	s.rt.killAll()
	if err := s.mgr.Reconcile(ctx); err != nil {
		t.Fatalf("solo boot: %v", err)
	}
	after := s.record(t, rec.ID)
	if after.IsTerminated || after.IsSuspended {
		t.Fatalf("a solo session did not come back from a restart: terminated %v, suspended %v", after.IsTerminated, after.IsSuspended)
	}
	if _, err := os.Stat(tree); err != nil {
		t.Fatalf("a solo worktree did not survive the restart: %v", err)
	}

	// Teardown frees the tree, with nothing shared to refuse over.
	res, err := s.mgr.Teardown(ctx, rec.ID, "test")
	if err != nil {
		t.Fatalf("solo teardown: %v", err)
	}
	if !res.Freed {
		t.Fatalf("solo teardown did not free the worktree: reason %q", res.Reason)
	}
	if dirExists(t, tree) {
		t.Fatalf("solo teardown left the worktree on disk: %s", tree)
	}
}

// TestSolo_IdleSweepIsUnchanged: the sweep still suspends an idle solo session and
// keeps its worktree, and this slice added no baton special case to it. Suspending
// a crew member is not a stall - it is exactly how the slot is released - so there
// was nothing for the sweep to learn.
func TestSolo_IdleSweepIsUnchanged(t *testing.T) {
	ctx := context.Background()
	s := newCrewStackWithIdle(t, time.Hour)
	rec, err := s.mgr.Spawn(ctx, ports.SpawnConfig{
		ProjectID: "mer", Kind: domain.KindWorker, Branch: "feature/solo", Prompt: "work",
		// mechanical is what makes this a SOLO spawn: a standard task now forms
		// its own crew, which is a different scenario from the one under test.
		TaskSize: domain.TaskSizeMechanical,
	})
	if err != nil {
		t.Fatal(err)
	}
	s.now = s.now.Add(2 * time.Hour)
	if err := s.mgr.CloseIdleSessions(ctx); err != nil {
		t.Fatal(err)
	}
	got := s.record(t, rec.ID)
	if !got.IsSuspended || got.IsTerminated {
		t.Fatalf("idle solo session = suspended %v, terminated %v; want suspended", got.IsSuspended, got.IsTerminated)
	}
	if !dirExists(t, rec.Metadata.WorkspacePath) {
		t.Fatal("the idle sweep removed a solo worktree")
	}
}

// TestCrewOneAwake_TheMessageQueueCannotBringAMemberUp closes the route that is
// easiest to miss, because nothing in it looks like a wake: a nudge (CI went red,
// a review verdict, a report-back) addressed to the member that stood down.
//
// It is asserted against the REAL queue, not reasoned about. The message is HELD
// - not dropped, which would lose the verdict - and holding it changes nothing
// about who is awake. It is delivered when that member next takes the slot.
func TestCrewOneAwake_TheMessageQueueCannotBringAMemberUp(t *testing.T) {
	ctx := context.Background()
	s := newCrewStack(t)
	dev, qa := s.spawnCrew(t) // dev stood down; qa holds the slot

	pane := &paneSpy{}
	now := s.now
	queue := msgqueue.New(s.store, pane, pane, slog.New(slog.NewTextHandler(io.Discard, nil)),
		msgqueue.WithClock(func() time.Time { return now }))
	messenger := queueingMessenger{store: s.store, pane: pane, queue: queue}

	out, err := messenger.Send(ctx, dev.ID, "CI went red on your PR")
	if err != nil {
		t.Fatalf("send to the stood-down member: %v", err)
	}
	if !out.Queued {
		t.Fatal("a message for a stood-down crew member was typed at a pane that is gone instead of held")
	}
	for range 3 {
		if err := queue.Drain(ctx); err != nil {
			t.Fatalf("drain: %v", err)
		}
		now = now.Add(2 * time.Second)
	}
	if typed := pane.typed(); len(typed) != 0 {
		t.Fatalf("the queue delivered %v to a member that is not awake", typed)
	}
	s.assertOneAwake(t, qa.ID, dev.ID, qa.ID)
	if got := s.record(t, dev.ID); !got.IsSuspended {
		t.Fatal("draining the queue woke the stood-down member")
	}

	// And the message is not lost: it lands when that member takes the slot.
	if _, err := s.mgr.HandOverCrewSlot(ctx, qa.ID, dev.ID); err != nil {
		t.Fatalf("hand over: %v", err)
	}
	for range 3 {
		if err := queue.Drain(ctx); err != nil {
			t.Fatalf("drain: %v", err)
		}
		now = now.Add(2 * time.Second)
	}
	if typed := pane.typed(); len(typed) != 1 {
		t.Fatalf("messages delivered after the handover = %v, want exactly the one that was held", typed)
	}
}

// TestCrewOneAwake_ConcurrentWakesStillBringUpOnlyOne. The guard is a
// check-then-act, so it is only as good as what serialises it. Two wakes that
// arrive together - two clicks on the board, a wake racing the boot restore pass
// - could otherwise both read a free slot and both take it, which is the exact
// state everything here exists to make impossible.
func TestCrewOneAwake_ConcurrentWakesStillBringUpOnlyOne(t *testing.T) {
	ctx := context.Background()
	s := newCrewStack(t)
	dev, qa := s.spawnCrew(t)

	// Start from an EMPTY slot, so both racers have a real chance at it.
	if err := s.mgr.ReleaseCrewSlot(ctx, qa.ID); err != nil {
		t.Fatal(err)
	}
	if got := s.awakeMembers(t, dev.ID, qa.ID); len(got) != 0 {
		t.Fatalf("precondition: the slot must start free, got %v", got)
	}

	var wg sync.WaitGroup
	errs := make([]error, 2)
	start := make(chan struct{})
	for i, id := range []domain.SessionID{dev.ID, qa.ID} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, errs[i] = s.mgr.Wake(ctx, id)
		}()
	}
	close(start)
	wg.Wait()

	awake := s.awakeMembers(t, dev.ID, qa.ID)
	if len(awake) != 1 {
		t.Fatalf("awake members after two simultaneous wakes = %v, want exactly one", awake)
	}
	// One won and one was told why, rather than one failing for some other reason.
	won, lost := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			won++
		case errors.Is(err, sessionmanager.ErrCrewBusy):
			lost++
		default:
			t.Fatalf("a racing wake failed for an unrelated reason: %v", err)
		}
	}
	if won != 1 || lost != 1 {
		t.Fatalf("racing wakes = %d won, %d refused; want exactly one of each (%v)", won, lost, errs)
	}
}
