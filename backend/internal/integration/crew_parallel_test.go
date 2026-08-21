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

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/msgqueue"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	crewrunsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/crewrun"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
	"github.com/aoagents/agent-orchestrator/backend/internal/treewatch"
)

// BOTH MEMBERS AWAKE IN ONE WORKTREE, against a real git worktree, a real SQLite
// store and the real lifecycle reducer.
//
// This file used to assert the opposite. #225 refused to bring a second member
// up while the first was awake, because qa reading a tree dev is halfway through
// writing produces a result that is meaningless AND LOOKS FINE. What replaced the
// refusal is not a weaker promise: the race is DETECTED and the result thrown
// away (the run bracket, #238), which is exact where the exclusion was coarse.
//
// So the assertions here are the inverse of the ones they replace - every route
// that used to refuse must now succeed, and the two agents must be able to run,
// write and talk at the same time - plus the one thing that survived: a corpse
// must not go on reading as a working agent, and a GLANCE must not start an agent
// nobody asked for.

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

// assertBothAwake is the invariant this file defends, and it is the one the file
// it replaces existed to make impossible.
func (s *crewStack) assertBothAwake(t *testing.T, ids ...domain.SessionID) {
	t.Helper()
	got := s.awakeMembers(t, ids...)
	if len(got) != len(ids) {
		t.Fatalf("awake members = %v, want all of %v", got, ids)
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

// TestCrewParallel_SpawnBringsUpASecondMemberInARunningTree is the spawn route,
// inverted. A crew is formed by putting a second session into a running task's
// tree; under the exclusion that was refused, which meant the very first thing a
// crew ever did was the thing the rule forbade.
func TestCrewParallel_SpawnBringsUpASecondMemberInARunningTree(t *testing.T) {
	ctx := context.Background()
	s := newCrewStack(t)
	dev, err := s.mgr.Spawn(ctx, ports.SpawnConfig{
		ProjectID: "mer", Kind: domain.KindWorker, Branch: "feature/task", Prompt: "build it",
		TaskSize: domain.TaskSizeMechanical,
	})
	if err != nil {
		t.Fatal(err)
	}

	qa, err := s.mgr.Spawn(ctx, ports.SpawnConfig{
		ProjectID: "mer", Kind: domain.KindWorker, Prompt: "test it",
		CrewOf: dev.ID, CrewRole: domain.CrewRoleQA,
	})
	if err != nil {
		t.Fatalf("spawning a crew member into a RUNNING dev's tree: %v", err)
	}
	s.assertBothAwake(t, dev.ID, qa.ID)
	if qa.Metadata.WorkspacePath != dev.Metadata.WorkspacePath {
		t.Fatalf("the crew is not on one worktree: %q vs %q", qa.Metadata.WorkspacePath, dev.Metadata.WorkspacePath)
	}
	// dev was not disturbed on the way: same runtime, same row.
	if got := s.record(t, dev.ID); got.IsSuspended || got.IsTerminated {
		t.Fatalf("forming the crew disturbed dev: suspended=%v terminated=%v", got.IsSuspended, got.IsTerminated)
	}
}

// TestCrewParallel_TheRuntimeTouchBringsUpASecondAgent is lazy creation end to
// end, against a real worktree: a `standard` task starts as ONE agent, and dev
// driving the app turns it into two - both awake, in one tree, with dev never
// interrupted. This is the whole feature; everything else here is a property of
// it.
func TestCrewParallel_TheRuntimeTouchBringsUpASecondAgent(t *testing.T) {
	ctx := context.Background()
	s := newCrewStack(t)
	dev, qa := setupCrew(t, s)

	s.assertBothAwake(t, dev.ID, qa.ID)
	if qa.CrewJoinReason != domain.CrewJoinSim {
		t.Fatalf("qa join reason = %q, want %q", qa.CrewJoinReason, domain.CrewJoinSim)
	}
	// ONE worktree, and it is dev's - still on disk, with dev's work in it.
	if qa.Metadata.WorkspacePath != dev.Metadata.WorkspacePath || qa.Metadata.Branch != dev.Metadata.Branch {
		t.Fatalf("the crew is not on one worktree: %q@%q vs %q@%q",
			qa.Metadata.WorkspacePath, qa.Metadata.Branch, dev.Metadata.WorkspacePath, dev.Metadata.Branch)
	}
	if !dirExists(t, dev.Metadata.WorkspacePath) {
		t.Fatal("gaining a qa disturbed the shared worktree")
	}
	// dev was not disturbed on the way: same row, same runtime.
	if got := s.record(t, dev.ID); got.IsSuspended || got.IsTerminated {
		t.Fatalf("gaining a qa disturbed dev: suspended=%v terminated=%v", got.IsSuspended, got.IsTerminated)
	}
	// The checklist the new member writes must land on dev's card, which is what
	// AO_CREW_ID being dev's id buys - and it is set at LAUNCH, so a member that
	// is created and started in one breath has to get it right first time.
	if got := s.rt.lastCfg.Env[sessionmanager.EnvCrewID]; got != string(dev.ID) {
		t.Fatalf("the new member launched with AO_CREW_ID=%q, want dev's id %q", got, dev.ID)
	}

	// And it happens ONCE: dev drives the device all day and the task stays two.
	for range 3 {
		s.mgr.NoteRuntimeTouch(ctx, dev.ID, domain.CrewJoinSim)
		s.mgr.NoteRuntimeTouch(ctx, dev.ID, domain.CrewJoinPreview)
	}
	all, err := s.store.ListSessions(ctx, "mer")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("six more runtime touches left %d sessions, want 2", len(all))
	}
}

// TestCrewParallel_EveryWakeRouteBringsAMemberUp walks each route by which a
// member can come up while its crewmate is running. They are separate code paths
// - Restore and Resume each short-circuit through their own adopt-if-alive branch
// before the shared relaunch - so each one is asserted, not assumed. This is the
// same table the exclusion used, with the expectation turned over.
func TestCrewParallel_EveryWakeRouteBringsAMemberUp(t *testing.T) {
	routes := []struct {
		name    string
		prepare func(t *testing.T, s *crewStack, member domain.SessionRecord)
		wake    func(s *crewStack, member domain.SessionID) error
	}{
		{
			name:    "resume a suspended member",
			prepare: func(*testing.T, *crewStack, domain.SessionRecord) {},
			wake: func(s *crewStack, id domain.SessionID) error {
				_, err := s.mgr.Resume(context.Background(), id, domain.WokenByWake)
				return err
			},
		},
		{
			name:    "wake a suspended member (opening its card)",
			prepare: func(*testing.T, *crewStack, domain.SessionRecord) {},
			wake: func(s *crewStack, id domain.SessionID) error {
				_, err := s.mgr.Wake(context.Background(), id)
				return err
			},
		},
		{
			name:    "restart a member",
			prepare: func(*testing.T, *crewStack, domain.SessionRecord) {},
			wake: func(s *crewStack, id domain.SessionID) error {
				_, err := s.mgr.Restart(context.Background(), id)
				return err
			},
		},
		{
			name: "restore a terminated member",
			prepare: func(t *testing.T, s *crewStack, member domain.SessionRecord) {
				t.Helper()
				if err := s.lcm.MarkTerminated(context.Background(), member.ID, domain.TerminationCauseKill); err != nil {
					t.Fatal(err)
				}
			},
			wake: func(s *crewStack, id domain.SessionID) error {
				_, err := s.mgr.Restore(context.Background(), id)
				return err
			},
		},
	}

	for _, route := range routes {
		t.Run(route.name, func(t *testing.T) {
			ctx := context.Background()
			s := newCrewStack(t)
			dev, qa := s.spawnCrew(t)
			// Put qa down by the route's precondition, with dev still working.
			if err := s.lcm.MarkSuspended(ctx, qa.ID, domain.SleepReasonIdle); err != nil {
				t.Fatal(err)
			}
			if err := s.mgr.SuspendRuntime(ctx, qa.ID); err != nil {
				t.Fatal(err)
			}
			route.prepare(t, s, qa)

			if err := route.wake(s, qa.ID); err != nil {
				t.Fatalf("%s while dev is running: %v", route.name, err)
			}
			s.assertBothAwake(t, dev.ID, qa.ID)
			// The crewmate was not touched on the way.
			if got := s.record(t, dev.ID); got.IsSuspended || got.IsTerminated {
				t.Fatalf("bringing qa up disturbed dev: suspended=%v terminated=%v", got.IsSuspended, got.IsTerminated)
			}
			if _, err := os.Stat(dev.Metadata.WorkspacePath); err != nil {
				t.Fatalf("the shared worktree did not survive: %v", err)
			}
		})
	}
}

// TestCrewParallel_AGlanceDoesNotStartAMemberThatNeverRan is the ONE half of the
// old rule that survives, and it was never about turns.
//
// The incident: dev's PR merged, so lifecycle terminated it; twelve seconds later
// qa - which had never run - was running, and nobody had pressed anything. The
// desktop app POSTs /wake whenever a session view opens. Starting an agent for
// the first time spends money and is a decision; resuming one that was paused is
// what a glance has always done and still does.
func TestCrewParallel_AGlanceDoesNotStartAMemberThatNeverRan(t *testing.T) {
	ctx := context.Background()
	s := newCrewStack(t)
	// A member that has never run: created by the trigger, with its start failing.
	// Starting is best effort, so this is the state a real one can land in.
	dev, qa := setupCrewNeverStarted(t, s)
	created := s.rt.created

	if _, err := s.svc.Wake(ctx, qa.ID); err != nil {
		t.Fatalf("opening a never-started member's view must not error: %v", err)
	}
	if got := s.record(t, qa.ID); got.Awake() {
		t.Fatal("VIEWING qa started it: nobody asked for a second agent")
	}
	if s.rt.created != created {
		t.Fatalf("runtimes created = %d, want %d: viewing qa launched an agent", s.rt.created, created)
	}

	// An ACTION still starts it - and dev keeps running, which is the change.
	if _, err := s.svc.WakeCrewMember(ctx, qa.ID); err != nil {
		t.Fatalf("explicit start: %v", err)
	}
	s.assertBothAwake(t, dev.ID, qa.ID)

	// And once it HAS run, a glance resumes it like any other paused session:
	// nothing new is being started, so there is nothing to decide.
	if err := s.lcm.MarkSuspended(ctx, qa.ID, domain.SleepReasonIdle); err != nil {
		t.Fatal(err)
	}
	if err := s.mgr.SuspendRuntime(ctx, qa.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.svc.Wake(ctx, qa.ID); err != nil {
		t.Fatalf("wake a paused member that has run before: %v", err)
	}
	if got := s.record(t, qa.ID); !got.Awake() {
		t.Fatal("a paused member that had already run did not come back when it was opened")
	}
}

// TestCrewParallel_ADeadCrewmateIsMarkedAsleep. The refusal is gone; the PROBE
// is not, and this is why. A member whose agent died mid-turn still reads as
// AWAKE off its row, and "nobody is working on this" - the rollup rule a real
// stalled run produced - is derived from exactly that column. A crew showing a
// corpse as a working agent is that same lie one layer down.
func TestCrewParallel_ADeadCrewmateIsMarkedAsleep(t *testing.T) {
	ctx := context.Background()
	s := newCrewStack(t)
	dev, qa := s.spawnCrew(t)
	qaRec := s.record(t, qa.ID)

	// qa's agent dies. Nothing marks the row: that is the whole problem.
	s.rt.kill(qaRec.Metadata.RuntimeHandleID)
	if !s.record(t, qa.ID).Awake() {
		t.Fatal("precondition: qa's row must still claim to be awake")
	}

	// Any wake route settles it on the way through.
	if err := s.lcm.MarkSuspended(ctx, dev.ID, domain.SleepReasonIdle); err != nil {
		t.Fatal(err)
	}
	if err := s.mgr.SuspendRuntime(ctx, dev.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.mgr.Resume(ctx, dev.ID, domain.WokenByWake); err != nil {
		t.Fatalf("resume dev: %v", err)
	}

	got := s.record(t, qa.ID)
	if got.Awake() {
		t.Fatal("a member whose runtime is gone still reads as a working agent")
	}
	// SUSPENDED, not terminated: its card stays in its lane, its worktree and
	// transcript are intact, and it is one Resume from coming back.
	if !got.IsSuspended || got.IsTerminated {
		t.Fatalf("the dead member = suspended %v, terminated %v; want suspended and recoverable", got.IsSuspended, got.IsTerminated)
	}
	if _, err := os.Stat(dev.Metadata.WorkspacePath); err != nil {
		t.Fatalf("the shared worktree was lost: %v", err)
	}
}

// TestCrewParallel_AnUnprobeableCrewmateIsLeftAlone. A failed probe is not proof
// of death - a rule this daemon leans on everywhere - so it must not put a
// crewmate to sleep. It costs nothing to be wrong in this direction now: the
// worst case is a stale row the next route settles.
func TestCrewParallel_AnUnprobeableCrewmateIsLeftAlone(t *testing.T) {
	ctx := context.Background()
	s := newCrewStack(t)
	dev, qa := s.spawnCrew(t)

	if err := s.lcm.MarkSuspended(ctx, dev.ID, domain.SleepReasonIdle); err != nil {
		t.Fatal(err)
	}
	if err := s.mgr.SuspendRuntime(ctx, dev.ID); err != nil {
		t.Fatal(err)
	}
	// The probe breaks only once dev is down, so what it answers about qa is the
	// only thing under test.
	s.rt.aliveErr = errors.New("tmux server not responding")
	if _, err := s.mgr.Resume(ctx, dev.ID, domain.WokenByWake); err != nil {
		t.Fatalf("resume with an unprobeable crewmate: %v", err)
	}
	if got := s.record(t, qa.ID); got.IsSuspended {
		t.Fatal("a failed probe was treated as proof the crewmate was dead")
	}
	s.assertBothAwake(t, dev.ID, qa.ID)
}

// TestCrewParallel_BothComeBackFromADaemonRestart. Under the exclusion, boot had
// to bring back at most ONE member and deliberately left the other asleep. Both
// were running when the daemon died now, so both have to come back - a task that
// silently loses half its crew across a restart is the same stall in a new coat.
func TestCrewParallel_BothComeBackFromADaemonRestart(t *testing.T) {
	ctx := context.Background()
	s := newCrewStack(t)
	dev, qa := s.spawnCrew(t)
	tree := dev.Metadata.WorkspacePath
	const wip = "work that must survive a restart\n"
	if err := os.WriteFile(filepath.Join(tree, "wip.txt"), []byte(wip), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := s.mgr.SaveAndTeardownAll(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	s.rt.killAll() // every pane died with the daemon
	if err := s.mgr.Reconcile(ctx); err != nil {
		t.Fatalf("boot: %v", err)
	}

	s.assertBothAwake(t, dev.ID, qa.ID)
	if _, err := os.Stat(tree); err != nil {
		t.Fatalf("the shared worktree did not survive the restart: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(tree, "wip.txt"))
	if err != nil || string(got) != wip {
		t.Fatalf("uncommitted work after the restart = %q, %v; want it intact", got, err)
	}
}

// TestCrewParallel_BothSurviveACrash is the same restart WITHOUT a clean
// shutdown - the path #224 found the trap on, because reconcileLive reaches
// saveAndTeardownOne at BOOT and that path force-destroys. A crew must not lose
// a member, or its worktree, to a daemon that was killed.
func TestCrewParallel_BothSurviveACrash(t *testing.T) {
	ctx := context.Background()
	s := newCrewStack(t)
	dev, qa := s.spawnCrew(t)
	tree := dev.Metadata.WorkspacePath

	// The daemon was killed. No shutdown sweep ran; the panes died with it.
	s.rt.killAll()
	if err := s.mgr.Reconcile(ctx); err != nil {
		t.Fatalf("boot after a crash: %v", err)
	}

	s.assertBothAwake(t, dev.ID, qa.ID)
	if _, err := os.Stat(tree); err != nil {
		t.Fatalf("the shared worktree was destroyed by boot reconciliation: %v", err)
	}
}

// TestCrewParallel_ConcurrentWakesBringUpBoth. Two wakes arriving together used
// to be the dangerous case: a check-then-act guard could let both through and
// produce the one state everything existed to prevent. That state is now the
// intended one, so the assertion is simply that neither racer is lost.
func TestCrewParallel_ConcurrentWakesBringUpBoth(t *testing.T) {
	ctx := context.Background()
	s := newCrewStack(t)
	dev, qa := s.spawnCrew(t)
	for _, id := range []domain.SessionID{dev.ID, qa.ID} {
		if err := s.lcm.MarkSuspended(ctx, id, domain.SleepReasonIdle); err != nil {
			t.Fatal(err)
		}
		if err := s.mgr.SuspendRuntime(ctx, id); err != nil {
			t.Fatal(err)
		}
	}

	var wg sync.WaitGroup
	errs := make([]error, 2)
	start := make(chan struct{})
	for i, id := range []domain.SessionID{dev.ID, qa.ID} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, errs[i] = s.mgr.WakeCrewMember(ctx, id)
		}()
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("simultaneous wake %d failed: %v", i, err)
		}
	}
	s.assertBothAwake(t, dev.ID, qa.ID)
}

// TestCrewParallel_ARunTheOtherMemberWroteUnderIsDiscarded is the whole point of
// removing the exclusion, end to end and with real work.
//
// Both members are awake in one real worktree. qa brackets a run; dev writes a
// file into that tree while it is open. The result qa reports is PASS - and the
// bracket throws it away, because it was read off a tree that moved. That is the
// trade this chunk makes: the race is not prevented, it is caught.
func TestCrewParallel_ARunTheOtherMemberWroteUnderIsDiscarded(t *testing.T) {
	ctx := context.Background()
	s := newCrewStack(t)
	dev, qa := s.spawnCrew(t)
	s.assertBothAwake(t, dev.ID, qa.ID)
	tree := s.record(t, qa.ID).Metadata.WorkspacePath

	reg := treewatch.NewRegistry(treewatch.Options{})
	t.Cleanup(reg.Close)
	runs := crewrunsvc.New(crewrunsvc.Options{Store: s.store, Watcher: reg})

	started, err := runs.Start(ctx, qa.ID, crewrunsvc.StartInput{Kind: domain.CrewRunTest, Label: "go test ./..."})
	if err != nil {
		t.Fatalf("bracket a run: %v", err)
	}
	if !started.Certified {
		t.Fatalf("nothing is watching the shared worktree: %s", started.Run.DetectorReason)
	}

	// dev types, mid-run - which is exactly what dev is FOR, and what the
	// exclusion used to forbid.
	if err := os.WriteFile(filepath.Join(tree, "app.go"), []byte("package app // dev is still writing\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	waitForTreeMove(t, reg, tree, started.Run.GenAtStart)

	ended, err := runs.End(ctx, qa.ID, crewrunsvc.EndInput{Result: domain.CrewRunResultPass})
	if err != nil {
		t.Fatalf("end the run: %v", err)
	}
	if ended.Run.Outcome != domain.CrewRunDiscarded {
		t.Fatalf("outcome = %q, want discarded: a run the tree moved under must never be trusted", ended.Run.Outcome)
	}
	if got := ended.Run.State(); got != domain.CrewRunStateDiscarded {
		t.Fatalf("a discarded run reads as %q; the PASS it reported must not survive as a verdict", got)
	}
	// And a QUIET run in the same tree is still trusted, so the detector is
	// discriminating rather than simply refusing everything a crew does.
	quiet, err := runs.Start(ctx, qa.ID, crewrunsvc.StartInput{Kind: domain.CrewRunTest})
	if err != nil {
		t.Fatalf("second bracket: %v", err)
	}
	_ = quiet
	endedQuiet, err := runs.End(ctx, qa.ID, crewrunsvc.EndInput{Result: domain.CrewRunResultPass})
	if err != nil {
		t.Fatalf("end the quiet run: %v", err)
	}
	if endedQuiet.Run.Outcome != domain.CrewRunTrusted {
		t.Fatalf("a run nobody wrote under = %q, want trusted", endedQuiet.Run.Outcome)
	}
	if dev.ID == "" {
		t.Fatal("unreachable")
	}
}

// waitForTreeMove gives the filesystem event a moment to land: writing a file and
// immediately ending the bracket would test the scheduler, not the detector.
func waitForTreeMove(t *testing.T, reg *treewatch.Registry, tree string, above uint64) {
	t.Helper()
	lease, err := reg.Attach(context.Background(), tree)
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	defer lease.Release()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if got, err := lease.Generation(); err == nil && got > above {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("the watcher never saw a write past generation %d", above)
}

// TestCrewParallel_AMessageReachesARunningCrewmate. Under the exclusion the
// interesting case was a message HELD for a member that had stood down. With both
// awake the interesting case is the ordinary one: it lands.
func TestCrewParallel_AMessageReachesARunningCrewmate(t *testing.T) {
	ctx := context.Background()
	s := newCrewStack(t)
	dev, qa := s.spawnCrew(t)

	pane := &paneSpy{}
	now := s.now
	queue := msgqueue.New(s.store, pane, pane, slog.New(slog.NewTextHandler(io.Discard, nil)),
		msgqueue.WithClock(func() time.Time { return now }))
	messenger := queueingMessenger{store: s.store, pane: pane, queue: queue}

	out, err := messenger.Send(ctx, qa.ID, "pushed the fix at 4a1b2c3")
	if err != nil {
		t.Fatalf("dev's message to qa: %v", err)
	}
	if out.Queued {
		t.Fatal("a message to a running crewmate was held instead of delivered")
	}
	if typed := pane.typed(); len(typed) != 1 {
		t.Fatalf("qa received %v, want exactly the one message", typed)
	}
	s.assertBothAwake(t, dev.ID, qa.ID)
}

// TestSolo_LifecycleIsUnchanged is the preservation suite, kept word for word
// from the slice that added the crew exclusion and now proving the same thing
// about its removal. A solo session is in no crew, so every crew branch is a
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

	// Suspend (what the idle sweep does) then wake: resumes in place.
	if err := s.lcm.MarkSuspended(ctx, rec.ID, domain.SleepReasonIdle); err != nil {
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

// TestSolo_IdleSweepIsUnchanged: the sweep still suspends an idle solo session
// and keeps its worktree. It never learned anything about crews and it still has
// not: a member the sweep pauses is paused for resources, like anything else.
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
