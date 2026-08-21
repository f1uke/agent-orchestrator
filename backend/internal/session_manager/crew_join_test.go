package sessionmanager

import (
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// standardDev spawns the ordinary subject of these tests: a `standard` task,
// which is ALLOWED a qa and does not have one.
func standardDev(t *testing.T, m *Manager, st *fakeStore) domain.SessionRecord {
	t.Helper()
	dev, err := m.Spawn(ctx, ports.SpawnConfig{
		ProjectID: "mer", Kind: domain.KindWorker, Prompt: "build the thing", TaskSize: domain.TaskSizeStandard,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if len(st.sessions) != 1 {
		t.Fatalf("a standard spawn produced %d rows, want 1", len(st.sessions))
	}
	return dev
}

// TestNoteRuntimeTouch_SimClaimCreatesQAAwake is the feature in one test: a task
// that was one agent becomes two the moment dev drives the app, and the member
// that arrives is WORKING - not a row waiting for a button that no longer exists.
func TestNoteRuntimeTouch_SimClaimCreatesQAAwake(t *testing.T) {
	m, st, rt, ws := newManager()
	dev := standardDev(t, m, st)
	// dev's agent is genuinely running: bringing a member up probes its crewmate,
	// and a corpse would be put to sleep.
	if rt.aliveByHandle == nil {
		rt.aliveByHandle = map[string]bool{}
	}
	rt.aliveByHandle[dev.Metadata.RuntimeHandleID] = true

	m.NoteRuntimeTouch(ctx, dev.ID, domain.CrewJoinSim)

	devRow, qa := crewOf(t, st, dev.ID)
	if !devRow.CrewRole.IsDev() || devRow.CrewID != devRow.ID {
		t.Fatalf("dev row role=%q crew=%q, want dev/%s", devRow.CrewRole, devRow.CrewID, devRow.ID)
	}
	if qa.CrewRole != domain.CrewRoleQA || qa.CrewID != devRow.ID {
		t.Fatalf("qa row role=%q crew=%q, want qa/%s", qa.CrewRole, qa.CrewID, devRow.ID)
	}
	// AWAKE. The whole point: nothing waits for a turn any more, and the control
	// that used to start a sleeping member went with the baton bar.
	if qa.IsSuspended || !qa.Awake() {
		t.Fatalf("qa did not start: suspended=%v awake=%v", qa.IsSuspended, qa.Awake())
	}
	if qa.Metadata.RuntimeHandleID == "" {
		t.Fatalf("qa is awake with no runtime handle; nothing was actually launched")
	}
	if rt.created != 2 {
		t.Fatalf("runtime created %d times, want 2 (dev, then qa)", rt.created)
	}
	// ONE worktree: the tree is dev's and qa is put INTO it. Starting a member
	// goes through Resume, which restores the workspace it is given - the same
	// path a woken crew member has always taken - so what is asserted is the
	// SHARE, not the call count.
	// (The fake workspace derives a path from the session id; the real one derives
	// it from the BRANCH, which is why the branch is the assertion that matters -
	// two sessions on one branch resolve to one directory.)
	_ = ws
	if qa.Metadata.Branch != devRow.Metadata.Branch {
		t.Fatalf("qa is not on dev's branch: %q vs %q", qa.Metadata.Branch, devRow.Metadata.Branch)
	}
	// dev keeps working straight through: gaining a crewmate is not a handover.
	if devRow.IsSuspended || !devRow.Awake() {
		t.Fatalf("creating qa stood dev down: suspended=%v awake=%v", devRow.IsSuspended, devRow.Awake())
	}
	// WHY it joined, recorded once, so the board can say so in a sentence.
	if qa.CrewJoinReason != domain.CrewJoinSim {
		t.Fatalf("qa join reason = %q, want %q", qa.CrewJoinReason, domain.CrewJoinSim)
	}
	// The turn qa reads: dev's brief, what dev was doing, and a handback that ENDS
	// the run somewhere - the first real crew run stalled precisely there.
	for _, want := range []string{
		"build the thing",
		"CLAIMED THE SIMULATOR",
		"dev has been working alone until now",
		"ao send --crew dev --about",
		"even if the answer is that there was nothing to exercise",
	} {
		if !strings.Contains(qa.Metadata.Prompt, want) {
			t.Fatalf("qa's kickoff is missing %q:\n%s", want, qa.Metadata.Prompt)
		}
	}
}

// TestNoteRuntimeTouch_PreviewCreatesQAToo: the other half of the trigger. An
// `ao preview` that moves the session's preview is the same observation as a
// claim - this task has a running surface - and it is recorded as its own reason
// so the board's line names what actually happened.
func TestNoteRuntimeTouch_PreviewCreatesQAToo(t *testing.T) {
	m, st, _, _ := newManager()
	dev := standardDev(t, m, st)

	m.NoteRuntimeTouch(ctx, dev.ID, domain.CrewJoinPreview)

	_, qa := crewOf(t, st, dev.ID)
	if qa.CrewJoinReason != domain.CrewJoinPreview {
		t.Fatalf("qa join reason = %q, want %q", qa.CrewJoinReason, domain.CrewJoinPreview)
	}
	if qa.IsSuspended {
		t.Fatalf("qa created by a preview did not start")
	}
	if !strings.Contains(qa.Metadata.Prompt, "ao preview") {
		t.Fatalf("qa's kickoff does not say what dev was doing:\n%s", qa.Metadata.Prompt)
	}
}

// TestNoteRuntimeTouch_FiresOnceNotPerCommand. dev drives the simulator all day;
// it gets ONE qa. The guard is the data, not a flag: creating qa writes dev's
// crew columns, and a session already in a crew is never eligible again.
func TestNoteRuntimeTouch_FiresOnceNotPerCommand(t *testing.T) {
	m, st, rt, _ := newManager()
	dev := standardDev(t, m, st)

	for i := 0; i < 5; i++ {
		m.NoteRuntimeTouch(ctx, dev.ID, domain.CrewJoinSim)
		m.NoteRuntimeTouch(ctx, dev.ID, domain.CrewJoinPreview)
	}

	if len(st.sessions) != 2 {
		t.Fatalf("ten runtime touches produced %d rows, want 2", len(st.sessions))
	}
	_, qa := crewOf(t, st, dev.ID)
	if qa.CrewJoinReason != domain.CrewJoinSim {
		t.Fatalf("the FIRST touch is what created the member; reason = %q, want %q", qa.CrewJoinReason, domain.CrewJoinSim)
	}
	if rt.created != 2 {
		t.Fatalf("runtime created %d times, want 2 - a repeat touch must not relaunch anything", rt.created)
	}
}

// TestNoteRuntimeTouch_BackendOnlyTaskNeverGetsAQA is what lazy creation is FOR.
// A change with nothing to drive touches neither surface, so it stays one agent
// and pays for one - the stand-down floor the old design charged every backend
// task is simply gone.
func TestNoteRuntimeTouch_BackendOnlyTaskNeverGetsAQA(t *testing.T) {
	m, st, rt, _ := newManager()
	dev := standardDev(t, m, st)

	// Everything a backend task does do: it works, it is read, it is messaged.
	if _, err := m.Send(ctx, dev.ID, "keep going"); err != nil {
		t.Fatalf("Send: %v", err)
	}

	if len(st.sessions) != 1 {
		t.Fatalf("a backend-only task grew to %d rows, want 1", len(st.sessions))
	}
	if rt.created != 1 {
		t.Fatalf("runtime created %d times, want 1", rt.created)
	}
	if st.sessions[dev.ID].InCrew() {
		t.Fatalf("a backend-only task is in a crew")
	}
}

// TestNoteRuntimeTouch_MechanicalNeverGetsOneAutomatically: `mechanical` is the
// tag that OPTS OUT, and driving a device does not opt back in. A human still
// can, by hand (AttachCrewMember).
func TestNoteRuntimeTouch_MechanicalNeverGetsOneAutomatically(t *testing.T) {
	m, st, _, _ := newManager()
	dev, err := m.Spawn(ctx, ports.SpawnConfig{
		ProjectID: "mer", Kind: domain.KindWorker, Prompt: "rename the flag", TaskSize: domain.TaskSizeMechanical,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	m.NoteRuntimeTouch(ctx, dev.ID, domain.CrewJoinSim)

	if len(st.sessions) != 1 {
		t.Fatalf("a mechanical task gained a member from a sim claim: %d rows", len(st.sessions))
	}
}

// TestNoteRuntimeTouch_IgnoresWhatCanNeverHostACrew. Every one of these is an
// ordinary outcome rather than an error: the caller is a sim claim or a preview,
// and neither may fail because a crew was not formed.
func TestNoteRuntimeTouch_IgnoresWhatCanNeverHostACrew(t *testing.T) {
	t.Run("an orchestrator", func(t *testing.T) {
		m, st, _, _ := newManager()
		orch, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindOrchestrator})
		if err != nil {
			t.Fatalf("Spawn: %v", err)
		}
		m.NoteRuntimeTouch(ctx, orch.ID, domain.CrewJoinSim)
		if len(st.sessions) != 1 {
			t.Fatalf("an orchestrator gained a crew member: %d rows", len(st.sessions))
		}
	})
	t.Run("a session that does not exist", func(t *testing.T) {
		m, st, _, _ := newManager()
		m.NoteRuntimeTouch(ctx, "mer-404", domain.CrewJoinSim)
		if len(st.sessions) != 0 {
			t.Fatalf("an unknown session created %d rows", len(st.sessions))
		}
	})
	t.Run("qa's own touch", func(t *testing.T) {
		m, st, _, _ := newManager()
		dev := standardDev(t, m, st)
		m.NoteRuntimeTouch(ctx, dev.ID, domain.CrewJoinSim)
		_, qa := crewOf(t, st, dev.ID)

		// qa driving the device is the ordinary case, and it must not nest a crew
		// inside a crew.
		m.NoteRuntimeTouch(ctx, qa.ID, domain.CrewJoinSim)

		if len(st.sessions) != 2 {
			t.Fatalf("qa's own claim grew the task to %d rows, want 2", len(st.sessions))
		}
	})
	t.Run("a manual reason", func(t *testing.T) {
		// `manual` is what AttachCrewMember records; it is not a thing the daemon
		// ever OBSERVES, so the trigger refuses to act on it.
		m, st, _, _ := newManager()
		dev := standardDev(t, m, st)
		m.NoteRuntimeTouch(ctx, dev.ID, domain.CrewJoinManual)
		if len(st.sessions) != 1 {
			t.Fatalf("a manual reason went through the observation path: %d rows", len(st.sessions))
		}
	})
}

// TestNoteRuntimeTouch_IsBestEffort: dev is running with a worktree by the time
// the trigger fires, and the command that fired it is about something else
// entirely. A crew that cannot be created leaves a working solo task behind.
func TestNoteRuntimeTouch_IsBestEffort(t *testing.T) {
	m, st, _, _ := newManager()
	dev := standardDev(t, m, st)
	st.failCreateAfter = 1 // dev's row landed; qa's create explodes

	m.NoteRuntimeTouch(ctx, dev.ID, domain.CrewJoinSim)

	if len(st.sessions) != 1 {
		t.Fatalf("a failed crew left %d rows behind, want 1", len(st.sessions))
	}
	if st.sessions[dev.ID].InCrew() {
		t.Fatalf("dev claims a crew that was never created: crew=%q role=%q",
			st.sessions[dev.ID].CrewID, st.sessions[dev.ID].CrewRole)
	}
}
