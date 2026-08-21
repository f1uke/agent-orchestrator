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

// TestAttachCrewMember_TurnsASoloMechanicalTaskIntoACrew is the hole being
// closed. The design calls adding qa a "wake" - but a mechanical task's qa was
// never created, so it is a CREATE, and it must produce exactly what a
// spawn-time qa is: a row and an id, asleep, in dev's tree.
func TestAttachCrewMember_TurnsASoloMechanicalTaskIntoACrew(t *testing.T) {
	m, st, rt, ws := newManager()
	dev := spawnMechanical(t, m)
	rtCreatedBeforeAttach, wsCreatedBeforeAttach := rt.created, ws.createCalls

	qa, err := m.AttachCrewMember(ctx, dev.ID, domain.CrewRoleQA)
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

	// BORN SUSPENDED, exactly as at spawn: no runtime, no tmux, no provisioning.
	if !qa.IsSuspended || qa.Awake() {
		t.Fatalf("attached qa is not born suspended: suspended=%v awake=%v", qa.IsSuspended, qa.Awake())
	}
	if qa.Metadata.RuntimeHandleID != "" {
		t.Fatalf("attached qa took a runtime handle %q; it must have no tmux at all", qa.Metadata.RuntimeHandleID)
	}
	if rt.created != rtCreatedBeforeAttach || ws.createCalls != wsCreatedBeforeAttach {
		t.Fatalf("attaching touched the world: runtime %d->%d, workspace %d->%d",
			rtCreatedBeforeAttach, rt.created, wsCreatedBeforeAttach, ws.createCalls)
	}
	// dev's tree and branch, which IS the share.
	if qa.Metadata.WorkspacePath != devRow.Metadata.WorkspacePath || qa.Metadata.Branch != devRow.Metadata.Branch {
		t.Fatalf("attached qa is not in dev's tree: %q@%q vs %q@%q",
			qa.Metadata.WorkspacePath, qa.Metadata.Branch, devRow.Metadata.WorkspacePath, devRow.Metadata.Branch)
	}
	// A promptless worker cannot be relaunched at all, so an empty kickoff would
	// make the attached member permanently unwakeable.
	if !strings.Contains(qa.Metadata.Prompt, "rename the flag") {
		t.Fatalf("attached qa's kickoff does not carry dev's brief:\n%s", qa.Metadata.Prompt)
	}
}

// TestAttachCrewMember_LeavesDevRunningAndHoldingTheSlot is the
// no-two-members-awake guarantee, from the attach side. Attaching is additive:
// it never suspends dev, never reaps its terminal, and never relaunches it - so
// there is no instant at which the crew has two awake members, and none at which
// it has none.
func TestAttachCrewMember_LeavesDevRunningAndHoldingTheSlot(t *testing.T) {
	m, st, rt, _ := newManager()
	dev := spawnMechanical(t, m)
	handleBefore := st.sessions[dev.ID].Metadata.RuntimeHandleID
	destroyedBefore := rt.destroyed

	qa, err := m.AttachCrewMember(ctx, dev.ID, domain.CrewRoleQA)
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
	// Exactly one member holds the slot, and it is dev.
	holder, ok, err := m.CrewSlotHolder(ctx, dev.ID)
	if err != nil || !ok || holder.ID != dev.ID {
		t.Fatalf("crew slot holder = %v/%v/%v, want dev %s", holder.ID, ok, err, dev.ID)
	}
	awake := 0
	for _, rec := range st.sessions {
		if rec.CrewID == devRow.CrewID && rec.Awake() {
			awake++
		}
	}
	if awake != 1 {
		t.Fatalf("%d members of the crew are awake, want exactly 1", awake)
	}
	if qa.Awake() {
		t.Fatal("the attached member is awake")
	}
}

// TestAttachCrewMember_TellsALateArrivalWhatDevDidNotKnow. Everything else a late
// qa inherits it inherits the same way a spawn-time one does. The one asymmetry
// is that dev has been running the SOLO prompt, which owns the smoke checklist -
// so a checklist may already exist, and re-sending a case under a new name is
// what destroys the human's verdict (#226's id trap).
func TestAttachCrewMember_TellsALateArrivalWhatDevDidNotKnow(t *testing.T) {
	m, _, _, _ := newManager()
	dev := spawnMechanical(t, m)
	qa, err := m.AttachCrewMember(ctx, dev.ID, domain.CrewRoleQA)
	if err != nil {
		t.Fatalf("AttachCrewMember: %v", err)
	}
	for _, want := range []string{"AFTER it started", "ao smoke list", "id it already has"} {
		if !strings.Contains(qa.Metadata.Prompt, want) {
			t.Fatalf("a late arrival's kickoff is missing %q:\n%s", want, qa.Metadata.Prompt)
		}
	}

	// A spawn-time qa must NOT carry it: it arrives before there is a PR or a
	// checklist, and telling it to be careful of work that does not exist is noise.
	m2, st2, _, _ := newManager()
	atSpawn, err := m2.Spawn(ctx, ports.SpawnConfig{
		ProjectID: "mer", Kind: domain.KindWorker, Prompt: "build it", TaskSize: domain.TaskSizeStandard,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	_, spawnQA := crewOf(t, st2, atSpawn.ID)
	if strings.Contains(spawnQA.Metadata.Prompt, "AFTER it started") {
		t.Fatalf("a spawn-time qa was told it arrived late:\n%s", spawnQA.Metadata.Prompt)
	}
}

// TestAttachCrewMember_RefusesASecondMemberInTheSameRole. One qa per task is the
// invariant; a stood-down qa still holds its seat, because standing it down is
// how an attach is undone and its id is what smoke_check / review_run rows name.
func TestAttachCrewMember_RefusesASecondMemberInTheSameRole(t *testing.T) {
	t.Run("attached twice", func(t *testing.T) {
		m, st, _, _ := newManager()
		dev := spawnMechanical(t, m)
		if _, err := m.AttachCrewMember(ctx, dev.ID, domain.CrewRoleQA); err != nil {
			t.Fatalf("first attach: %v", err)
		}
		_, err := m.AttachCrewMember(ctx, dev.ID, domain.CrewRoleQA)
		if !errors.Is(err, ErrCrewRoleTaken) {
			t.Fatalf("second attach err = %v, want ErrCrewRoleTaken", err)
		}
		if len(st.sessions) != 2 {
			t.Fatalf("a refused attach left %d rows, want 2", len(st.sessions))
		}
	})

	t.Run("the crew was formed at spawn", func(t *testing.T) {
		m, st, _, _ := newManager()
		dev, err := m.Spawn(ctx, ports.SpawnConfig{
			ProjectID: "mer", Kind: domain.KindWorker, Prompt: "build it", TaskSize: domain.TaskSizeStandard,
		})
		if err != nil {
			t.Fatalf("Spawn: %v", err)
		}
		if _, err := m.AttachCrewMember(ctx, dev.ID, domain.CrewRoleQA); !errors.Is(err, ErrCrewRoleTaken) {
			t.Fatalf("attach to a full crew err = %v, want ErrCrewRoleTaken", err)
		}
		if len(st.sessions) != 2 {
			t.Fatalf("a refused attach left %d rows, want 2", len(st.sessions))
		}
	})

	t.Run("the qa was stood down", func(t *testing.T) {
		m, st, _, _ := newManager()
		dev := spawnMechanical(t, m)
		qa, err := m.AttachCrewMember(ctx, dev.ID, domain.CrewRoleQA)
		if err != nil {
			t.Fatalf("attach: %v", err)
		}
		// `ao kill <qa>`: local teardown, dev's tree untouched (#224).
		if _, err := m.Teardown(ctx, qa.ID, "test: stand qa down"); err != nil {
			t.Fatalf("Teardown(qa): %v", err)
		}
		if _, err := m.AttachCrewMember(ctx, dev.ID, domain.CrewRoleQA); !errors.Is(err, ErrCrewRoleTaken) {
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
		if _, err := m.AttachCrewMember(ctx, orc.ID, domain.CrewRoleQA); !errors.Is(err, ErrInvalidCrew) {
			t.Fatalf("err = %v, want ErrInvalidCrew", err)
		}
	})

	t.Run("a terminated dev", func(t *testing.T) {
		m, _, _, _ := newManager()
		dev := spawnMechanical(t, m)
		if _, err := m.Teardown(ctx, dev.ID, "test: task over"); err != nil {
			t.Fatalf("Teardown: %v", err)
		}
		if _, err := m.AttachCrewMember(ctx, dev.ID, domain.CrewRoleQA); !errors.Is(err, ErrInvalidCrew) {
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
		err = func() error { _, e := m.AttachCrewMember(ctx, todo.ID, domain.CrewRoleQA); return e }()
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
		if _, err := m.AttachCrewMember(ctx, dev.ID, domain.CrewRoleQA); !errors.Is(err, ErrInvalidCrew) {
			t.Fatalf("err = %v, want ErrInvalidCrew", err)
		}
	})

	t.Run("dev is not a joinable role", func(t *testing.T) {
		m, _, _, _ := newManager()
		dev := spawnMechanical(t, m)
		if _, err := m.AttachCrewMember(ctx, dev.ID, domain.CrewRoleDev); !errors.Is(err, ErrInvalidCrew) {
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
			_, errs[i] = m.AttachCrewMember(ctx, dev.ID, domain.CrewRoleQA)
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

	qa, err := m.AttachCrewMember(ctx, dev.ID, domain.CrewRoleQA)
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

// TestAttachCrewMember_WakingTheAttachedMemberGoesThroughTheExclusion: an
// attached qa is woken by exactly the route a spawn-time one is, so the handover
// is #225's, not a second mechanism.
func TestAttachCrewMember_WakingTheAttachedMemberGoesThroughTheExclusion(t *testing.T) {
	m, st, rt, _ := newManager()
	dev := spawnMechanical(t, m)
	qa, err := m.AttachCrewMember(ctx, dev.ID, domain.CrewRoleQA)
	if err != nil {
		t.Fatalf("AttachCrewMember: %v", err)
	}
	if _, err := m.WakeCrewMember(ctx, qa.ID); err != nil {
		t.Fatalf("WakeCrewMember: %v", err)
	}

	devRow, qaRow := st.sessions[dev.ID], st.sessions[qa.ID]
	if !devRow.IsSuspended || devRow.Awake() {
		t.Fatalf("dev still holds the slot after the handover: suspended=%v awake=%v", devRow.IsSuspended, devRow.Awake())
	}
	if qaRow.IsSuspended || !qaRow.Awake() {
		t.Fatalf("qa did not come up: suspended=%v awake=%v", qaRow.IsSuspended, qaRow.Awake())
	}
	holder, ok, err := m.CrewSlotHolder(ctx, dev.ID)
	if err != nil || !ok || holder.ID != qa.ID {
		t.Fatalf("crew slot holder = %v/%v/%v, want qa %s", holder.ID, ok, err, qa.ID)
	}
	// qa is launched under a SESSION-ID handle, not dev's branch handle: two crew
	// members sharing one tmux name is what #224 had to break apart.
	if rt.lastCfg.Branch != "" {
		t.Fatalf("attached qa launched with branch %q; a non-dev member must use the session-id handle", rt.lastCfg.Branch)
	}
}
