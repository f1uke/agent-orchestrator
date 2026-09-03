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

// TestNoteRuntimeTouch_CreatesNobody is the removal, asserted directly.
//
// Driving the app used to CREATE the task's qa. It fired when dev was starting to
// look at the change rather than when dev was finished with it, so the qa that
// appeared went for the same device dev was still using. Both surfaces are still
// observed; neither produces an agent.
func TestNoteRuntimeTouch_CreatesNobody(t *testing.T) {
	for _, touch := range []domain.RuntimeTouch{domain.RuntimeTouchSim, domain.RuntimeTouchPreview} {
		t.Run(string(touch), func(t *testing.T) {
			m, st, rt, _ := newManager()
			dev := standardDev(t, m, st)

			m.NoteRuntimeTouch(ctx, dev.ID, touch)

			if len(st.sessions) != 1 {
				t.Fatalf("driving the app produced %d rows, want 1 - nothing may create a qa but a request", len(st.sessions))
			}
			if st.sessions[dev.ID].InCrew() {
				t.Fatalf("driving the app put dev in a crew: crew=%q role=%q",
					st.sessions[dev.ID].CrewID, st.sessions[dev.ID].CrewRole)
			}
			if rt.created != 1 {
				t.Fatalf("runtime created %d times, want 1 - no second agent was launched", rt.created)
			}
			// The observation SURVIVES as a fact. It is the input to the warning
			// that replaced the trigger, and without it a task that drove the app
			// and never asked for a qa would close out in silence.
			if got := st.sessions[dev.ID].RuntimeTouch; got != touch {
				t.Fatalf("runtime touch recorded as %q, want %q", got, touch)
			}
		})
	}
}

// TestNoteRuntimeTouch_RecordsTheFirstSurfaceOnly. dev drives the app all day and
// the row keeps saying what it FIRST did. Write-once lives in the store's own
// UPDATE, so it is a property of the data rather than of this caller.
func TestNoteRuntimeTouch_RecordsTheFirstSurfaceOnly(t *testing.T) {
	m, st, _, _ := newManager()
	dev := standardDev(t, m, st)

	for i := 0; i < 5; i++ {
		m.NoteRuntimeTouch(ctx, dev.ID, domain.RuntimeTouchSim)
		m.NoteRuntimeTouch(ctx, dev.ID, domain.RuntimeTouchPreview)
	}

	if got := st.sessions[dev.ID].RuntimeTouch; got != domain.RuntimeTouchSim {
		t.Fatalf("runtime touch = %q, want the FIRST surface (%q)", got, domain.RuntimeTouchSim)
	}
	if len(st.sessions) != 1 {
		t.Fatalf("ten runtime touches produced %d rows, want 1", len(st.sessions))
	}
}

// TestNoteRuntimeTouch_BackendOnlyTaskRecordsNothing is what keeps a backend
// change free. It drives no runtime surface, so it carries no touch, so it is
// never warned about a tester it never needed and never gains one.
func TestNoteRuntimeTouch_BackendOnlyTaskRecordsNothing(t *testing.T) {
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
	if st.sessions[dev.ID].RuntimeTouch != "" {
		t.Fatalf("a task that drove nothing recorded a runtime touch: %q", st.sessions[dev.ID].RuntimeTouch)
	}
	if st.sessions[dev.ID].InCrew() {
		t.Fatalf("a backend-only task is in a crew")
	}
}

// TestNoteRuntimeTouch_IsBestEffortAndSilent. The caller is a sim claim or an
// `ao preview` - commands about something else - so an unknown session, an
// orchestrator or an unrecognised surface is an ordinary outcome, never an error.
func TestNoteRuntimeTouch_IsBestEffortAndSilent(t *testing.T) {
	t.Run("a session that does not exist", func(t *testing.T) {
		m, st, _, _ := newManager()
		m.NoteRuntimeTouch(ctx, "mer-404", domain.RuntimeTouchSim)
		if len(st.sessions) != 0 {
			t.Fatalf("an unknown session created %d rows", len(st.sessions))
		}
	})
	t.Run("a surface that is not one", func(t *testing.T) {
		m, st, _, _ := newManager()
		dev := standardDev(t, m, st)
		m.NoteRuntimeTouch(ctx, dev.ID, domain.RuntimeTouch("manual"))
		if got := st.sessions[dev.ID].RuntimeTouch; got != "" {
			t.Fatalf("an invalid surface was recorded as %q", got)
		}
	})
	t.Run("an orchestrator", func(t *testing.T) {
		// Recorded rather than refused: nothing acts on an orchestrator's touch,
		// and a predicate here would be a second place for the rule to drift from.
		m, st, _, _ := newManager()
		orch, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindOrchestrator})
		if err != nil {
			t.Fatalf("Spawn: %v", err)
		}
		m.NoteRuntimeTouch(ctx, orch.ID, domain.RuntimeTouchSim)
		if len(st.sessions) != 1 {
			t.Fatalf("an orchestrator gained a crew member: %d rows", len(st.sessions))
		}
	})
}

// TestRequestCrewReview_CreatesQAAwake is the feature in one test: a task that
// was one agent becomes two when DEV says the work is ready, and the member that
// arrives is WORKING - not a row waiting for a button that no longer exists.
func TestRequestCrewReview_CreatesQAAwake(t *testing.T) {
	m, st, rt, ws, msgr := newManagerWithMessenger()
	dev := standardDev(t, m, st)
	// dev's agent is genuinely running: bringing a member up probes its crewmate,
	// and a corpse would be put to sleep.
	if rt.aliveByHandle == nil {
		rt.aliveByHandle = map[string]bool{}
	}
	rt.aliveByHandle[dev.Metadata.RuntimeHandleID] = true

	if _, err := m.RequestCrewReview(ctx, dev.ID, domain.CrewRoleQA); err != nil {
		t.Fatalf("RequestCrewReview: %v", err)
	}

	devRow, qa := crewOf(t, st, dev.ID)
	if !devRow.CrewRole.IsDev() || devRow.CrewID != devRow.ID {
		t.Fatalf("dev row role=%q crew=%q, want dev/%s", devRow.CrewRole, devRow.CrewID, devRow.ID)
	}
	if qa.CrewRole != domain.CrewRoleQA || qa.CrewID != devRow.ID {
		t.Fatalf("qa row role=%q crew=%q, want qa/%s", qa.CrewRole, qa.CrewID, devRow.ID)
	}
	// AWAKE. Nothing waits for a turn, and the control that used to start a
	// sleeping member went with the baton bar.
	if qa.IsSuspended || !qa.Awake() {
		t.Fatalf("qa did not start: suspended=%v awake=%v", qa.IsSuspended, qa.Awake())
	}
	if qa.Metadata.RuntimeHandleID == "" {
		t.Fatalf("qa is awake with no runtime handle; nothing was actually launched")
	}
	if rt.created != 2 {
		t.Fatalf("runtime created %d times, want 2 (dev, then qa)", rt.created)
	}
	// ONE worktree: the tree is dev's and qa is put INTO it. The fake workspace
	// derives a path from the session id; the real one derives it from the BRANCH,
	// which is why the branch is the assertion that matters.
	_ = ws
	if qa.Metadata.Branch != devRow.Metadata.Branch {
		t.Fatalf("qa is not on dev's branch: %q vs %q", qa.Metadata.Branch, devRow.Metadata.Branch)
	}
	// dev keeps working straight through: gaining a crewmate is not a handover.
	if devRow.IsSuspended || !devRow.Awake() {
		t.Fatalf("creating qa stood dev down: suspended=%v awake=%v", devRow.IsSuspended, devRow.Awake())
	}
	// WHY it joined, recorded once, so the board can say so in a sentence - and so
	// qa's own first turn opens on the right question.
	if qa.CrewJoinReason != domain.CrewJoinReview {
		t.Fatalf("qa join reason = %q, want %q", qa.CrewJoinReason, domain.CrewJoinReview)
	}
	for _, want := range []string{
		"build the thing",
		"dev asked for you",
		"dev has been working alone until now",
		"ao send --crew dev --about",
		"even if the answer is that there was nothing to exercise",
	} {
		if !strings.Contains(qa.Metadata.Prompt, want) {
			t.Fatalf("qa's kickoff is missing %q:\n%s", want, qa.Metadata.Prompt)
		}
	}
	// dev asked for this one, so AO does not also tell dev it happened: dev reads
	// the answer on its own stdout.
	if got := msgr.sentTo(dev.ID); len(got) != 0 {
		t.Fatalf("dev was messaged about a qa it asked for itself: %q", got)
	}
}

// TestRequestCrewReview_OnceOnly. A task has one qa and keeps its id; a second
// ask is refused rather than producing a stranger that inherits the first's
// artefacts. It stays refused after that qa is stood down - `ao session restore`
// is how it comes back.
func TestRequestCrewReview_OnceOnly(t *testing.T) {
	m, st, _, _ := newManager()
	dev := standardDev(t, m, st)

	if _, err := m.RequestCrewReview(ctx, dev.ID, domain.CrewRoleQA); err != nil {
		t.Fatalf("first request: %v", err)
	}
	_, err := m.RequestCrewReview(ctx, dev.ID, domain.CrewRoleQA)
	if err == nil {
		t.Fatal("a second request created a second qa")
	}
	if !strings.Contains(err.Error(), "already has a") {
		t.Fatalf("second request refused with %v, want the role-taken refusal", err)
	}
	if len(st.sessions) != 2 {
		t.Fatalf("two requests produced %d rows, want 2", len(st.sessions))
	}
}

// TestRequestCrewReview_MechanicalIsRefused. `mechanical` is one agent by an
// explicit decision somebody made when they sized the task, and undoing it is a
// person's call. dev is not even told the verb there, so this is the belt to that
// prompt's braces - and the refusal names the human route rather than just saying
// no, because a bare "refused" is what sends the next agent hunting for a
// workaround.
func TestRequestCrewReview_MechanicalIsRefused(t *testing.T) {
	m, st, _, _ := newManager()
	dev, err := m.Spawn(ctx, ports.SpawnConfig{
		ProjectID: "mer", Kind: domain.KindWorker, Prompt: "rename the flag", TaskSize: domain.TaskSizeMechanical,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	_, err = m.RequestCrewReview(ctx, dev.ID, domain.CrewRoleQA)
	if err == nil {
		t.Fatal("a mechanical task asked for a qa and got one")
	}
	for _, want := range []string{"mechanical", "ao crew add", "+ qa"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the refusal does not mention %q: %v", want, err)
		}
	}
	if len(st.sessions) != 1 {
		t.Fatalf("a mechanical task grew to %d rows", len(st.sessions))
	}
}

// TestRequestCrewReview_QAMayNotAsk. A qa running this would be asking for
// itself. It is refused by name rather than falling through to "crews do not
// nest", because the useful answer is that the review is qa's job to DO.
func TestRequestCrewReview_QAMayNotAsk(t *testing.T) {
	m, st, _, _ := newManager()
	dev := standardDev(t, m, st)
	if _, err := m.RequestCrewReview(ctx, dev.ID, domain.CrewRoleQA); err != nil {
		t.Fatalf("RequestCrewReview: %v", err)
	}
	_, qa := crewOf(t, st, dev.ID)

	_, err := m.RequestCrewReview(ctx, qa.ID, domain.CrewRoleQA)
	if err == nil {
		t.Fatal("qa asked for a qa and got one")
	}
	if !strings.Contains(err.Error(), "not its dev") {
		t.Fatalf("qa's refusal reads %v, want the wrong-member answer", err)
	}
	if len(st.sessions) != 2 {
		t.Fatalf("qa's request grew the task to %d rows, want 2", len(st.sessions))
	}
}

// TestRequestCrewReview_IsRefusedOnACrewOffProject. The project switch says AGENTS
// may not decide a task needs a qa, and dev asking is exactly that decision. A
// person's `ao crew add` still works, which is what the switch is for.
func TestRequestCrewReview_IsRefusedOnACrewOffProject(t *testing.T) {
	m, st, _, _ := newManager()
	cfg := testRoleAgents()
	cfg.DisableAutoCrew = true
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Path: "/tmp/mer", Config: cfg}
	dev := standardDev(t, m, st)

	_, err := m.RequestCrewReview(ctx, dev.ID, domain.CrewRoleQA)
	if err == nil {
		t.Fatal("a crew-off project let dev ask for a qa")
	}
	if len(st.sessions) != 1 {
		t.Fatalf("a crew-off project grew to %d rows", len(st.sessions))
	}
	// The human's door is still open on the same task.
	if _, err := m.AttachCrewMember(ctx, dev.ID, domain.CrewRoleQA, ""); err != nil {
		t.Fatalf("a person could not add a qa on a crew-off project: %v", err)
	}
}
