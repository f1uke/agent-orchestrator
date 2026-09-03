package sessionmanager

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// spawnMechanical is the task this whole feature exists for: one agent, no crew,
// and no way to gain one until now.
func spawnMechanical(t *testing.T, m *Manager) domain.SessionRecord {
	t.Helper()
	dev, err := m.Spawn(ctx, ports.SpawnConfig{
		ProjectID: "mer", Kind: domain.KindWorker, Prompt: "rename the flag",
		TaskSize: domain.TaskSizeMechanical,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	return dev
}

// TestAttachCrewMember_TurnsASoloMechanicalTaskIntoACrew is the manual half of
// lazy creation. A `mechanical` task is never given a qa by the trigger, so a
// human asking for one is a CREATE - and what it creates is what the trigger
// creates: a member in dev's tree, working.
func TestAttachCrewMember_TurnsASoloMechanicalTaskIntoACrew(t *testing.T) {
	m, st, rt, ws := newManager()
	dev := spawnMechanical(t, m)
	if rt.aliveByHandle == nil {
		rt.aliveByHandle = map[string]bool{}
	}
	rt.aliveByHandle[dev.Metadata.RuntimeHandleID] = true
	rtCreatedBeforeAttach, wsCreatedBeforeAttach := rt.created, ws.createCalls
	_ = wsCreatedBeforeAttach

	qa, err := m.AttachCrewMember(ctx, dev.ID, domain.CrewRoleQA, "")
	if err != nil {
		t.Fatalf("AttachCrewMember: %v", err)
	}
	devRow, found := crewOf(t, st, dev.ID)
	if found.ID != qa.ID {
		t.Fatalf("the attached member is %s, but the crew holds %s", qa.ID, found.ID)
	}
	if !devRow.CrewRole.IsDev() || devRow.CrewID != devRow.ID {
		t.Fatalf("dev row role=%q crew=%q, want dev/%s", devRow.CrewRole, devRow.CrewID, devRow.ID)
	}
	if qa.CrewRole != domain.CrewRoleQA || qa.CrewID != devRow.ID {
		t.Fatalf("qa row role=%q crew=%q, want qa/%s", qa.CrewRole, qa.CrewID, devRow.ID)
	}

	// AWAKE: a human who asks for a qa gets one that is working. Nothing waits for
	// a turn, and the control that used to start a sleeping member is gone.
	if qa.IsSuspended || !qa.Awake() {
		t.Fatalf("attached qa did not start: suspended=%v awake=%v", qa.IsSuspended, qa.Awake())
	}
	if qa.Metadata.RuntimeHandleID == "" {
		t.Fatalf("attached qa is awake with no runtime handle; nothing was launched")
	}
	if rt.created != rtCreatedBeforeAttach+1 {
		t.Fatalf("runtime created %d times, want one more than before the attach (%d)", rt.created, rtCreatedBeforeAttach)
	}
	// dev's branch, which IS the share: two sessions on one branch resolve to one
	// worktree directory.
	if qa.Metadata.Branch != devRow.Metadata.Branch {
		t.Fatalf("attached qa is not on dev's branch: %q vs %q", qa.Metadata.Branch, devRow.Metadata.Branch)
	}
	// A promptless worker cannot be relaunched at all, so an empty kickoff would
	// make the attached member permanently unwakeable.
	if !strings.Contains(qa.Metadata.Prompt, "rename the flag") {
		t.Fatalf("attached qa's kickoff does not carry dev's brief:\n%s", qa.Metadata.Prompt)
	}
}

// TestAttachCrewMember_LeavesDevRunning: attaching is ADDITIVE. It never
// suspends dev, never reaps its terminal and never relaunches it - dev keeps
// working straight through, which is the whole point of being able to do this to
// a task in flight.
func TestAttachCrewMember_LeavesDevRunning(t *testing.T) {
	m, st, rt, _ := newManager()
	dev := spawnMechanical(t, m)
	if rt.aliveByHandle == nil {
		rt.aliveByHandle = map[string]bool{}
	}
	rt.aliveByHandle[dev.Metadata.RuntimeHandleID] = true
	handleBefore := st.sessions[dev.ID].Metadata.RuntimeHandleID
	destroyedBefore := rt.destroyed

	qa, err := m.AttachCrewMember(ctx, dev.ID, domain.CrewRoleQA, "")
	if err != nil {
		t.Fatalf("AttachCrewMember: %v", err)
	}
	devRow := st.sessions[dev.ID]
	if devRow.IsSuspended || !devRow.Awake() {
		t.Fatalf("attaching stood dev down: suspended=%v awake=%v", devRow.IsSuspended, devRow.Awake())
	}
	if devRow.Metadata.RuntimeHandleID != handleBefore || rt.destroyed != destroyedBefore {
		t.Fatalf("attaching disturbed dev's terminal: handle %q->%q, destroys %d->%d",
			handleBefore, devRow.Metadata.RuntimeHandleID, destroyedBefore, rt.destroyed)
	}
	// ...and the new member is running beside it. Both awake at once in one
	// worktree is the shape now, not a violation of anything.
	if !qa.Awake() {
		t.Fatal("the attached member did not start")
	}
}

// TestAttachCrewMember_TellsTheArrivalWhatDevDidNotKnow. Every qa now arrives
// after dev started work, and dev did not know it would get one: dev has been
// authoring the checklist alone and goes on co-authoring it, so a checklist may
// already be there, possibly already carrying the human's verdicts - and
// replacing the whole list (or re-sending a case under a new name, #226's id
// trap) is what destroys them.
func TestAttachCrewMember_TellsTheArrivalWhatDevDidNotKnow(t *testing.T) {
	m, _, _, _ := newManager()
	dev := spawnMechanical(t, m)
	qa, err := m.AttachCrewMember(ctx, dev.ID, domain.CrewRoleQA, "")
	if err != nil {
		t.Fatalf("AttachCrewMember: %v", err)
	}
	for _, want := range []string{"A HUMAN added you", "dev has been working alone until now", "ao smoke list", "ao smoke add", "never `ao smoke set`"} {
		if !strings.Contains(qa.Metadata.Prompt, want) {
			t.Fatalf("an attached member's kickoff is missing %q:\n%s", want, qa.Metadata.Prompt)
		}
	}

	// The same warning reaches a member DEV asked for, because the same thing is
	// true of it - but it is told that dev asked, not that a human did.
	m2, st2, _, _ := newManager()
	auto, err := m2.Spawn(ctx, ports.SpawnConfig{
		ProjectID: "mer", Kind: domain.KindWorker, Prompt: "build it", TaskSize: domain.TaskSizeStandard,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if _, err := m2.RequestCrewReview(ctx, auto.ID, domain.CrewRoleQA); err != nil {
		t.Fatalf("RequestCrewReview: %v", err)
	}
	_, autoQA := crewOf(t, st2, auto.ID)
	if strings.Contains(autoQA.Metadata.Prompt, "A HUMAN added you") {
		t.Fatalf("a member dev asked for was told a human asked for it:\n%s", autoQA.Metadata.Prompt)
	}
	if !strings.Contains(autoQA.Metadata.Prompt, "dev has been working alone until now") {
		t.Fatalf("a member dev asked for was not warned about work already in progress:\n%s", autoQA.Metadata.Prompt)
	}
}

// TestAttachCrewMember_RefusesASecondMemberInTheSameRole. One qa per task is the
// invariant; a stood-down qa still holds its seat, because standing it down is
// how an attach is undone and its id is what smoke_check / review_run rows name.
func TestAttachCrewMember_RefusesASecondMemberInTheSameRole(t *testing.T) {
	t.Run("attached twice", func(t *testing.T) {
		m, st, _, _ := newManager()
		dev := spawnMechanical(t, m)
		if _, err := m.AttachCrewMember(ctx, dev.ID, domain.CrewRoleQA, ""); err != nil {
			t.Fatalf("first attach: %v", err)
		}
		_, err := m.AttachCrewMember(ctx, dev.ID, domain.CrewRoleQA, "")
		if !errors.Is(err, ErrCrewRoleTaken) {
			t.Fatalf("second attach err = %v, want ErrCrewRoleTaken", err)
		}
		if len(st.sessions) != 2 {
			t.Fatalf("a refused attach left %d rows, want 2", len(st.sessions))
		}
	})

	t.Run("dev already asked for one", func(t *testing.T) {
		m, st, _, _ := newManager()
		dev, err := m.Spawn(ctx, ports.SpawnConfig{
			ProjectID: "mer", Kind: domain.KindWorker, Prompt: "build it", TaskSize: domain.TaskSizeStandard,
		})
		if err != nil {
			t.Fatalf("Spawn: %v", err)
		}
		if _, err := m.RequestCrewReview(ctx, dev.ID, domain.CrewRoleQA); err != nil {
			t.Fatalf("RequestCrewReview: %v", err)
		}
		if _, err := m.AttachCrewMember(ctx, dev.ID, domain.CrewRoleQA, ""); !errors.Is(err, ErrCrewRoleTaken) {
			t.Fatalf("attach to a full crew err = %v, want ErrCrewRoleTaken", err)
		}
		if len(st.sessions) != 2 {
			t.Fatalf("a refused attach left %d rows, want 2", len(st.sessions))
		}
	})

	t.Run("the qa was stood down", func(t *testing.T) {
		m, st, _, _ := newManager()
		dev := spawnMechanical(t, m)
		qa, err := m.AttachCrewMember(ctx, dev.ID, domain.CrewRoleQA, "")
		if err != nil {
			t.Fatalf("attach: %v", err)
		}
		// `ao kill <qa>`: local teardown, dev's tree untouched (#224).
		if _, err := m.Teardown(ctx, qa.ID, "test: stand qa down"); err != nil {
			t.Fatalf("Teardown(qa): %v", err)
		}
		if _, err := m.AttachCrewMember(ctx, dev.ID, domain.CrewRoleQA, ""); !errors.Is(err, ErrCrewRoleTaken) {
			t.Fatalf("a replacement qa was accepted: %v - restore the original id instead", err)
		}
		if len(st.sessions) != 2 {
			t.Fatalf("a refused attach left %d rows, want 2", len(st.sessions))
		}
	})
}

// TestAttachCrewMember_RefusesWhatCanNeverHostACrew: the structural refusals,
// shared with the spawn seam through resolveCrewDev so the two cannot drift.
func TestAttachCrewMember_RefusesWhatCanNeverHostACrew(t *testing.T) {
	t.Run("an orchestrator", func(t *testing.T) {
		m, _, _, _ := newManager()
		orc, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindOrchestrator})
		if err != nil {
			t.Fatalf("Spawn: %v", err)
		}
		if _, err := m.AttachCrewMember(ctx, orc.ID, domain.CrewRoleQA, ""); !errors.Is(err, ErrInvalidCrew) {
			t.Fatalf("err = %v, want ErrInvalidCrew", err)
		}
	})

	t.Run("a terminated dev", func(t *testing.T) {
		m, _, _, _ := newManager()
		dev := spawnMechanical(t, m)
		if _, err := m.Teardown(ctx, dev.ID, "test: task over"); err != nil {
			t.Fatalf("Teardown: %v", err)
		}
		if _, err := m.AttachCrewMember(ctx, dev.ID, domain.CrewRoleQA, ""); !errors.Is(err, ErrInvalidCrew) {
			t.Fatalf("err = %v, want ErrInvalidCrew", err)
		}
	})

	t.Run("a prepared TODO", func(t *testing.T) {
		m, st, _, _ := newManager()
		todo, err := m.PrepareTodo(ctx, ports.SpawnConfig{
			ProjectID: "mer", Kind: domain.KindWorker, Prompt: "staged",
			Harness: domain.HarnessClaudeCode, TaskSize: domain.TaskSizeMechanical,
		})
		if err != nil {
			t.Fatalf("PrepareTodo: %v", err)
		}
		err = func() error { _, e := m.AttachCrewMember(ctx, todo.ID, domain.CrewRoleQA, ""); return e }()
		if !errors.Is(err, ErrInvalidCrew) {
			t.Fatalf("err = %v, want ErrInvalidCrew", err)
		}
		if !strings.Contains(err.Error(), "start it first") {
			t.Fatalf("a TODO deserves the one-sentence answer, got %v", err)
		}
		if len(st.sessions) != 1 {
			t.Fatalf("a refused attach left %d rows, want 1", len(st.sessions))
		}
	})

	t.Run("a workspace project", func(t *testing.T) {
		m, st, _, _ := newManager()
		dev := spawnMechanical(t, m)
		p := st.projects["mer"]
		p.Kind = domain.ProjectKindWorkspace
		st.projects["mer"] = p
		if _, err := m.AttachCrewMember(ctx, dev.ID, domain.CrewRoleQA, ""); !errors.Is(err, ErrInvalidCrew) {
			t.Fatalf("err = %v, want ErrInvalidCrew", err)
		}
	})

	t.Run("dev is not a joinable role", func(t *testing.T) {
		m, _, _, _ := newManager()
		dev := spawnMechanical(t, m)
		if _, err := m.AttachCrewMember(ctx, dev.ID, domain.CrewRoleDev, ""); !errors.Is(err, ErrInvalidCrew) {
			t.Fatalf("err = %v, want ErrInvalidCrew - dev is the crew's root, not a seat", err)
		}
	})
}

// TestAttachCrewMember_ConcurrentAttachesProduceExactlyOneQA. The attach is
// check-then-create, so without the crew lock two callers both see a free seat.
// One wins; the loser is refused and leaves nothing behind.
func TestAttachCrewMember_ConcurrentAttachesProduceExactlyOneQA(t *testing.T) {
	m, st, _, _ := newManager()
	dev := spawnMechanical(t, m)

	const callers = 4
	var wg sync.WaitGroup
	errs := make([]error, callers)
	wg.Add(callers)
	for i := range callers {
		go func() {
			defer wg.Done()
			_, errs[i] = m.AttachCrewMember(ctx, dev.ID, domain.CrewRoleQA, "")
		}()
	}
	wg.Wait()

	won := 0
	for _, err := range errs {
		switch {
		case err == nil:
			won++
		case errors.Is(err, ErrCrewRoleTaken):
		default:
			t.Fatalf("a losing attach failed with the wrong error: %v", err)
		}
	}
	if won != 1 {
		t.Fatalf("%d of %d concurrent attaches succeeded, want exactly 1", won, callers)
	}
	if len(st.sessions) != 2 {
		t.Fatalf("concurrent attaches left %d rows, want 2", len(st.sessions))
	}
	crewOf(t, st, dev.ID)
}

// TestCrewDevOf_ResolvesEitherIdToTheTask. A human holding one id should not have
// to know which one it is - and a SOLO session is its own task, the same equality
// AO_CREW_ID relies on.
func TestCrewDevOf_ResolvesEitherIdToTheTask(t *testing.T) {
	m, _, _, _ := newManager()
	dev := spawnMechanical(t, m)

	solo, err := m.CrewDevOf(ctx, dev.ID)
	if err != nil || solo.ID != dev.ID {
		t.Fatalf("CrewDevOf(solo) = %v/%v, want itself (%s)", solo.ID, err, dev.ID)
	}

	qa, err := m.AttachCrewMember(ctx, dev.ID, domain.CrewRoleQA, "")
	if err != nil {
		t.Fatalf("AttachCrewMember: %v", err)
	}
	for _, id := range []domain.SessionID{dev.ID, qa.ID} {
		got, err := m.CrewDevOf(ctx, id)
		if err != nil || got.ID != dev.ID {
			t.Fatalf("CrewDevOf(%s) = %v/%v, want dev %s", id, got.ID, err, dev.ID)
		}
	}
}

// TestAttachCrewMember_StartingTheAttachedMemberLeavesDevRunning: starting one
// member is not a handover any more. dev keeps its agent, its terminal and its
// turn, because there is no turn - both members work at the same time.
func TestAttachCrewMember_StartingTheAttachedMemberLeavesDevRunning(t *testing.T) {
	m, st, rt, _ := newManager()
	dev := spawnMechanical(t, m)
	// dev's agent is genuinely running: the wake routes probe a crewmate that
	// claims to be awake and put a CORPSE to sleep, which is the half of the old
	// guard that survives.
	if rt.aliveByHandle == nil {
		rt.aliveByHandle = map[string]bool{}
	}
	rt.aliveByHandle["h1"] = true
	qa, err := m.AttachCrewMember(ctx, dev.ID, domain.CrewRoleQA, "")
	if err != nil {
		t.Fatalf("AttachCrewMember: %v", err)
	}

	// Starting a member that is already up is a no-op, not an error.
	if _, err := m.WakeCrewMember(ctx, qa.ID); err != nil {
		t.Fatalf("WakeCrewMember: %v", err)
	}

	devRow, qaRow := st.sessions[dev.ID], st.sessions[qa.ID]
	if devRow.IsSuspended || !devRow.Awake() {
		t.Fatalf("starting qa stood dev down: suspended=%v awake=%v", devRow.IsSuspended, devRow.Awake())
	}
	if qaRow.IsSuspended || !qaRow.Awake() {
		t.Fatalf("qa did not come up: suspended=%v awake=%v", qaRow.IsSuspended, qaRow.Awake())
	}
	// qa is launched under a SESSION-ID handle, not dev's branch handle: two crew
	// members sharing one tmux name is what #224 had to break apart.
	if rt.lastCfg.Branch != "" {
		t.Fatalf("attached qa launched with branch %q; a non-dev member must use the session-id handle", rt.lastCfg.Branch)
	}
}
