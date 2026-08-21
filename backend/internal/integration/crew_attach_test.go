package integration

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/msgqueue"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
)

// ATTACHING A QA to a task that is already running, against a real git worktree,
// a real SQLite store and the real lifecycle reducer.
//
// The whole feature is one claim - "you can add a second agent to a task in
// flight without disturbing the one that is working" - and it is a claim about
// what happens to a REAL checkout that a REAL agent is holding. A fake workspace
// cannot fail the way this must not fail, so the tree here is real.

// TestCrewAttach_AMechanicalTaskGainsASleepingQA is the case the design's own
// escape hatch could not serve: qa was never created, so adding one is a create.
func TestCrewAttach_AMechanicalTaskGainsASleepingQA(t *testing.T) {
	ctx := context.Background()
	s := newCrewStack(t)
	s.rt.perSessionHandles = true

	dev, err := s.mgr.Spawn(ctx, ports.SpawnConfig{
		ProjectID: "mer", Kind: domain.KindWorker, Branch: "feature/task", Prompt: "rename the flag",
		TaskSize: domain.TaskSizeMechanical,
	})
	if err != nil {
		t.Fatalf("spawn dev: %v", err)
	}
	createdBefore, destroyedBefore := s.rt.created, s.rt.destroyed

	qa, err := s.mgr.AttachCrewMember(ctx, dev.ID, domain.CrewRoleQA)
	if err != nil {
		t.Fatalf("AttachCrewMember: %v", err)
	}

	// The member exists and is asleep: a row and an id, and no process anywhere.
	if !qa.IsSuspended || qa.Awake() {
		t.Fatalf("the attached member is not asleep: suspended=%v awake=%v", qa.IsSuspended, qa.Awake())
	}
	if qa.Metadata.RuntimeHandleID != "" {
		t.Fatalf("the attached member took a runtime handle %q", qa.Metadata.RuntimeHandleID)
	}
	if s.rt.created != createdBefore || s.rt.destroyed != destroyedBefore {
		t.Fatalf("attaching touched the runtime: created %d->%d, destroyed %d->%d",
			createdBefore, s.rt.created, destroyedBefore, s.rt.destroyed)
	}

	// dev is untouched and still holds the slot. This is the no-two-awake
	// guarantee from the attach side: nothing was released, so nothing could race.
	devRow := s.record(t, dev.ID)
	if devRow.IsSuspended || !devRow.Awake() {
		t.Fatalf("attaching stood dev down: suspended=%v awake=%v", devRow.IsSuspended, devRow.Awake())
	}
	s.assertOneAwake(t, dev.ID, dev.ID, qa.ID)

	// One REAL worktree, still on disk, with dev's work in it.
	if qa.Metadata.WorkspacePath != devRow.Metadata.WorkspacePath || qa.Metadata.Branch != devRow.Metadata.Branch {
		t.Fatalf("the attached member is not in dev's tree: %q@%q vs %q@%q",
			qa.Metadata.WorkspacePath, qa.Metadata.Branch, devRow.Metadata.WorkspacePath, devRow.Metadata.Branch)
	}
	if !dirExists(t, devRow.Metadata.WorkspacePath) {
		t.Fatal("attaching disturbed the shared worktree")
	}
	// The crew is recorded on BOTH rows, keyed on dev's id.
	if devRow.CrewID != devRow.ID || !devRow.CrewRole.IsDev() {
		t.Fatalf("dev row = crew %q role %q, want %s/dev", devRow.CrewID, devRow.CrewRole, devRow.ID)
	}
	if qa.CrewID != devRow.ID || qa.CrewRole != domain.CrewRoleQA {
		t.Fatalf("qa row = crew %q role %q, want %s/qa", qa.CrewID, qa.CrewRole, devRow.ID)
	}
}

// TestCrewAttach_TheNewMemberIsReachableBeforeItWakes. The id existing from the
// moment the attach returns is half of what "born suspended" buys: `ao send` to
// it must be HELD by #217's queue, not dropped and not a wake.
func TestCrewAttach_TheNewMemberIsReachableBeforeItWakes(t *testing.T) {
	ctx := context.Background()
	s := newCrewStack(t)
	s.rt.perSessionHandles = true
	dev, qa := s.attachedCrew(t)

	pane := &paneSpy{}
	now := s.now
	queue := msgqueue.New(s.store, pane, pane, slog.New(slog.NewTextHandler(io.Discard, nil)),
		msgqueue.WithClock(func() time.Time { return now }))
	messenger := queueingMessenger{store: s.store, pane: pane, queue: queue}

	out, err := messenger.Send(ctx, qa.ID, "have a look at the flag rename before we merge")
	if err != nil {
		t.Fatalf("send to the newly attached member: %v", err)
	}
	if !out.Queued {
		t.Fatal("a message for a member that has never woken was typed at a pane that does not exist")
	}
	for range 3 {
		if err := queue.Drain(ctx); err != nil {
			t.Fatalf("drain: %v", err)
		}
		now = now.Add(2 * time.Second)
	}
	if typed := pane.typed(); len(typed) != 0 {
		t.Fatalf("draining delivered %v to a member with no terminal", typed)
	}
	s.assertOneAwake(t, dev.ID, dev.ID, qa.ID)

	// And it is not lost: it lands when the member takes the slot.
	if _, err := s.mgr.WakeCrewMember(ctx, qa.ID); err != nil {
		t.Fatalf("wake the attached member: %v", err)
	}
	for range 3 {
		if err := queue.Drain(ctx); err != nil {
			t.Fatalf("drain: %v", err)
		}
		now = now.Add(2 * time.Second)
	}
	if len(pane.typed()) == 0 {
		t.Fatal("the held message never reached the member after it woke")
	}
}

// TestCrewAttach_WakingItIsAHandoverThroughTheExclusion. An attached member is
// woken by exactly the route a spawn-time one is: the holder is stood down FIRST
// and its terminal reaped, then the taker comes up. At no observable moment do
// two agents have the shared checkout.
func TestCrewAttach_WakingItIsAHandoverThroughTheExclusion(t *testing.T) {
	ctx := context.Background()
	s := newCrewStack(t)
	s.rt.perSessionHandles = true
	s.rt.trackLiveness = true
	dev, qa := s.attachedCrew(t)
	devHandle := s.record(t, dev.ID).Metadata.RuntimeHandleID

	if _, err := s.mgr.WakeCrewMember(ctx, qa.ID); err != nil {
		t.Fatalf("WakeCrewMember: %v", err)
	}
	s.assertOneAwake(t, qa.ID, dev.ID, qa.ID)
	if !s.rt.wasDestroyed(devHandle) {
		t.Fatalf("dev's terminal %q was left running while qa came up", devHandle)
	}
	devRow := s.record(t, dev.ID)
	if !devRow.IsSuspended || devRow.IsTerminated {
		t.Fatalf("dev = suspended %v terminated %v; standing down must keep the card and the tree",
			devRow.IsSuspended, devRow.IsTerminated)
	}
	if !dirExists(t, devRow.Metadata.WorkspacePath) {
		t.Fatal("the handover removed the shared worktree")
	}

	// AO_CREW_ID is dev's id for the member that just came up, so the checklist it
	// writes lands on the card the human opens.
	if got := s.rt.lastCfg.Env[sessionmanager.EnvCrewID]; got != string(dev.ID) {
		t.Fatalf("the attached member launched with AO_CREW_ID=%q, want dev's id %q", got, dev.ID)
	}
	// And dev's own AO_CREW_ID was already right without a restart: a solo spawn
	// exports its OWN id, and after the attach the crew id IS its own id.
	if devRow.CrewID != devRow.ID {
		t.Fatalf("dev's crew id %q is not its own id %q, so its AO_CREW_ID is now stale", devRow.CrewID, devRow.ID)
	}

	// Handing back is symmetric, and the slot never holds two.
	if _, err := s.mgr.WakeCrewMember(ctx, dev.ID); err != nil {
		t.Fatalf("hand the turn back: %v", err)
	}
	s.assertOneAwake(t, dev.ID, dev.ID, qa.ID)
}

// TestCrewAttach_RefusesAFinishedTask goes through the SERVICE, because that is
// where a task's status is derived - a merged PR is a fact about PR rows, not
// about the session row, and the manager cannot see it.
func TestCrewAttach_RefusesAFinishedTask(t *testing.T) {
	ctx := context.Background()
	s := newCrewStack(t)
	dev, err := s.mgr.Spawn(ctx, ports.SpawnConfig{
		ProjectID: "mer", Kind: domain.KindWorker, Branch: "feature/task", Prompt: "rename the flag",
		TaskSize: domain.TaskSizeMechanical,
	})
	if err != nil {
		t.Fatalf("spawn dev: %v", err)
	}
	pr := domain.PullRequest{URL: "https://example.test/pr/1", SessionID: dev.ID, Number: 1, Merged: true, UpdatedAt: s.now}
	if err := s.store.WritePR(ctx, pr, nil, nil); err != nil {
		t.Fatalf("record the merge: %v", err)
	}

	if _, err := s.svc.AttachCrewMember(ctx, dev.ID, domain.CrewRoleQA); err == nil {
		t.Fatal("a task whose PR has merged accepted a new crew member")
	}
	all, err := s.store.ListAllSessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Fatalf("a refused attach left %d rows, want 1", len(all))
	}
}

// TestCrewAttach_RefusesASecondQAEvenAgainstTheRealDatabase. The Go check runs
// under the crew lock; the partial unique index (0047) is what makes the rule a
// property of the DATA rather than of one process. Both are asserted here,
// because a test against a map store can only see the first.
func TestCrewAttach_RefusesASecondQAEvenAgainstTheRealDatabase(t *testing.T) {
	ctx := context.Background()
	s := newCrewStack(t)
	s.rt.perSessionHandles = true
	dev, qa := s.attachedCrew(t)

	if _, err := s.mgr.AttachCrewMember(ctx, dev.ID, domain.CrewRoleQA); !errors.Is(err, sessionmanager.ErrCrewRoleTaken) {
		t.Fatalf("a second attach returned %v, want ErrCrewRoleTaken", err)
	}
	// The seat stays qa's even after it is stood down: `ao session restore` is how
	// it comes back, so the id that smoke_check and review_run rows name survives.
	if _, err := s.mgr.Teardown(ctx, qa.ID, domain.TerminationCauseKill); err != nil {
		t.Fatalf("stand qa down: %v", err)
	}
	if _, err := s.mgr.AttachCrewMember(ctx, dev.ID, domain.CrewRoleQA); !errors.Is(err, sessionmanager.ErrCrewRoleTaken) {
		t.Fatalf("a replacement qa was accepted: %v", err)
	}
	// Writing the membership directly is refused by SQLite itself, which is the
	// invariant a racing caller cannot step around.
	stray, err := s.store.CreateSession(ctx, domain.SessionRecord{ProjectID: "mer", Kind: domain.KindWorker})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.SetSessionCrew(ctx, stray.ID, dev.ID, domain.CrewRoleQA, s.now); err == nil {
		t.Fatal("the database accepted a second qa on one crew; the partial unique index must refuse it")
	}
	// dev's tree is still standing through all of that.
	if !dirExists(t, s.record(t, dev.ID).Metadata.WorkspacePath) {
		t.Fatal("the refused attaches removed the shared worktree")
	}
}

// TestSolo_AttachingToNobodyChangesNothing is the hard requirement: a task nobody
// touches must behave exactly as it does today. Nothing here calls the attach at
// all - it asserts that the ROUTE existing costs a solo task nothing.
func TestSolo_AttachingToNobodyChangesNothing(t *testing.T) {
	ctx := context.Background()
	s := newCrewStack(t)
	rec, err := s.mgr.Spawn(ctx, ports.SpawnConfig{
		ProjectID: "mer", Kind: domain.KindWorker, Branch: "feature/untouched", Prompt: "small tweak",
		TaskSize: domain.TaskSizeMechanical,
	})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	all, err := s.store.ListAllSessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Fatalf("a mechanical spawn produced %d rows, want exactly 1", len(all))
	}
	if rec.InCrew() {
		t.Fatalf("a mechanical spawn produced a crew: crew=%q role=%q", rec.CrewID, rec.CrewRole)
	}
	// The whole lifetime, unchanged: the tree is its own, and its teardown frees it.
	res, err := s.mgr.Teardown(ctx, rec.ID, domain.TerminationCauseKill)
	if err != nil {
		t.Fatalf("teardown: %v", err)
	}
	if !res.Freed {
		t.Fatalf("a solo teardown did not free its worktree: %+v", res)
	}
	if dirExists(t, rec.Metadata.WorkspacePath) {
		t.Fatal("a solo worktree survived its own teardown")
	}
}

// attachedCrew is the state this feature creates: a mechanical task that was
// spawned solo, is still running, and has since been given a qa.
func (s *crewStack) attachedCrew(t *testing.T) (dev, qa domain.SessionRecord) {
	t.Helper()
	ctx := context.Background()
	devRec, err := s.mgr.Spawn(ctx, ports.SpawnConfig{
		ProjectID: "mer", Kind: domain.KindWorker, Branch: "feature/task", Prompt: "rename the flag",
		TaskSize: domain.TaskSizeMechanical,
	})
	if err != nil {
		t.Fatalf("spawn dev: %v", err)
	}
	qaRec, err := s.mgr.AttachCrewMember(ctx, devRec.ID, domain.CrewRoleQA)
	if err != nil {
		t.Fatalf("attach qa: %v", err)
	}
	return s.record(t, devRec.ID), qaRec
}
