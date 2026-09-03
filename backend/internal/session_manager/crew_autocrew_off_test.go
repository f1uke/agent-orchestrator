package sessionmanager

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// A PROJECT THAT FORMS NO CREW BY ITSELF.
//
// `ProjectConfig.DisableAutoCrew` turns off AUTOMATIC crew formation for one
// project and nothing else. It is emphatically NOT `--task-size mechanical`:
// mechanical also authorizes an agent to skip the requirements -> plan -> test-first
// ceremony, and a `standard` or `deep` task on a crew-off project must still get
// the full ceremony - just with nobody else in the crew.
//
// The tests below pin the three things that can go wrong.

// crewOffManager is newManager with automatic crew formation turned off for the
// project, and nothing else changed.
func crewOffManager(t *testing.T) (*Manager, *fakeStore, *fakeRuntime, *fakeWorkspace) {
	t.Helper()
	m, st, rt, ws := newManager()
	cfg := testRoleAgents()
	cfg.DisableAutoCrew = true
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: cfg}
	return m, st, rt, ws
}

// THE TEST THIS FEATURE EXISTS FOR.
//
// Spawn builds dev's system prompt BEFORE anything could create a qa, from the
// spawn's INTENT (promptCrewRole). If the flag were read only where qa is
// created, dev would already have been handed the CREW prompt - which since #240
// tells dev that the smoke checklist belongs to qa and that `ao smoke set` from
// it is refused. On a project that will never create a qa that means NOBODY
// writes the checklist, silently, with no error anywhere.
//
// So the flag has to be resolved at the eligibility seam both the prompt and the
// trigger read, and dev's prompt on a crew-off project must be the SOLO one: no
// crew block, no "the checklist is qa's", and the checklist protocol it has
// always had.
func TestSpawn_CrewOffProjectLaunchesDevWithTheSoloPrompt(t *testing.T) {
	for _, size := range []domain.TaskSize{domain.TaskSizeStandard, domain.TaskSizeDeep} {
		t.Run(string(size), func(t *testing.T) {
			st := newFakeStore()
			cfg := testRoleAgents()
			cfg.DisableAutoCrew = true
			st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: cfg}
			agent := &recordingAgent{}
			lookPath := func(string) (string, error) { return "/bin/true", nil }
			m := New(Deps{Runtime: &fakeRuntime{}, Agents: singleAgent{agent: agent}, Workspace: &fakeWorkspace{}, Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st}, LookPath: lookPath})

			if _, err := m.Spawn(ctx, ports.SpawnConfig{
				ProjectID: "mer", Kind: domain.KindWorker, Prompt: "build the thing", TaskSize: size,
			}); err != nil {
				t.Fatalf("Spawn: %v", err)
			}
			if len(st.sessions) != 1 {
				t.Fatalf("a crew-off spawn produced %d rows, want exactly 1", len(st.sessions))
			}

			launched := agent.lastLaunch.SystemPrompt
			// THE DUTY IT MUST KEEP. Nobody else is coming, so dev owns the list.
			if !strings.Contains(launched, "## Smoke-test checklist (AO)") {
				t.Fatalf("dev on a crew-off project cannot author the checklist nobody else will:\n%s", launched)
			}
			// THE INSTRUCTION IT MUST NOT GET. Every sentence here hands the list to
			// an agent this project never creates.
			for _, unwanted := range []string{
				"The checklist is yours only while you have no qa",
				"REFUSED by AO for as long as a qa is on this task",
				"## Your crewmate (AO)",
				"AO creates a qa the first time you touch the app's runtime",
			} {
				if strings.Contains(launched, unwanted) {
					t.Fatalf("dev on a crew-off project was told %q, about a qa AO will never create:\n%s", unwanted, launched)
				}
			}
		})
	}
}

// CREW-OFF IS NOT MECHANICAL. The task-size directive is the one block that
// authorizes skipping the process skills, and it renders for `mechanical` only.
// A crew-off `standard` task must not gain it: the human asked for one agent, not
// for less rigor.
func TestBuildSystemPrompt_CrewOffDoesNotStripCeremony(t *testing.T) {
	m, _, _, _ := crewOffManager(t)

	got, err := m.buildSystemPrompt(ctx, systemPromptSpec{
		Kind:      domain.KindWorker,
		ProjectID: "mer",
		TaskSize:  domain.TaskSizeStandard,
		CrewRole: promptCrewRole(
			domain.ProjectRecord{ID: "mer", Config: domain.ProjectConfig{DisableAutoCrew: true}},
			ports.SpawnConfig{Kind: domain.KindWorker, TaskSize: domain.TaskSizeStandard},
		),
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "## Task size: mechanical (AO)") {
		t.Fatalf("a standard task on a crew-off project was demoted to mechanical:\n%s", got)
	}
}

// DEV MAY NOT ASK HERE. Asking for a qa is an AGENT deciding a task needs a
// second one, which is precisely what this project switched off - so the switch
// answers dev's own request as well as it ever answered the observation that
// preceded it.
func TestRequestCrewReview_CrewOffProjectCreatesNoQA(t *testing.T) {
	m, st, rt, _ := crewOffManager(t)
	dev := standardDev(t, m, st)
	if rt.aliveByHandle == nil {
		rt.aliveByHandle = map[string]bool{}
	}
	rt.aliveByHandle[dev.Metadata.RuntimeHandleID] = true

	_, err := m.RequestCrewReview(ctx, dev.ID, domain.CrewRoleQA)
	if !errors.Is(err, ErrCrewAutoFormationOff) {
		t.Fatalf("dev asked for a qa on a crew-off project: err = %v, want ErrCrewAutoFormationOff", err)
	}
	if len(st.sessions) != 1 {
		t.Fatalf("a refused request produced %d rows, want 1", len(st.sessions))
	}
	if st.sessions[dev.ID].InCrew() {
		t.Fatal("a refused request put dev in a crew on a crew-off project")
	}
}

// AND DEV IS NEVER TOLD THE VERB HERE, which is the half that keeps the refusal
// above from ever being reached. crewEligible gates the prompt and the request
// from one place, so the two cannot disagree.
func TestBuildSystemPrompt_CrewOffDevIsNotTaughtTheVerb(t *testing.T) {
	m, _, _, _ := crewOffManager(t)
	got, err := m.buildSystemPrompt(ctx, systemPromptSpec{
		Kind:      domain.KindWorker,
		ProjectID: "mer",
		TaskSize:  domain.TaskSizeStandard,
		CrewRole: promptCrewRole(
			domain.ProjectRecord{ID: "mer", Config: domain.ProjectConfig{DisableAutoCrew: true}},
			ports.SpawnConfig{Kind: domain.KindWorker, TaskSize: domain.TaskSizeStandard},
		),
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "ao crew review") {
		t.Fatalf("a crew-off project taught dev a verb it would be refused:\n%s", got)
	}
}

// THE ESCAPE HATCH SURVIVES. The whole point of turning off AUTOMATIC crew rather
// than crew is that a human can still opt ONE task into a qa by hand - `ao crew
// add`, or the topbar's `+ qa`. That path does not run the eligibility test the
// flag gates (resolveCrewDev), and this test is what keeps it that way.
func TestAddCrewMember_StillWorksOnACrewOffProject(t *testing.T) {
	m, st, _, _ := crewOffManager(t)
	dev := standardDev(t, m, st)

	qa, err := m.AttachCrewMember(ctx, dev.ID, domain.CrewRoleQA, "")
	if err != nil {
		t.Fatalf("a human could not add a qa by hand on a crew-off project: %v", err)
	}
	if qa.CrewRole != domain.CrewRoleQA || qa.CrewID != dev.ID {
		t.Fatalf("manual qa row role=%q crew=%q, want qa/%s", qa.CrewRole, qa.CrewID, dev.ID)
	}
	// `manual` is the reason the flag must NOT block; `review` - dev asking - is
	// the one it does.
	if qa.CrewJoinReason != domain.CrewJoinManual {
		t.Fatalf("manual qa join reason = %q, want %q", qa.CrewJoinReason, domain.CrewJoinManual)
	}
}

// THE FLAG IS PER PROJECT. A second project on the same daemon still lets its
// devs ask, so turning it off is never a global switch by accident.
func TestRequestCrewReview_OtherProjectsStillFormCrews(t *testing.T) {
	m, st, rt, _ := crewOffManager(t)
	st.projects["other"] = domain.ProjectRecord{ID: "other", Config: testRoleAgents()}

	dev, err := m.Spawn(ctx, ports.SpawnConfig{
		ProjectID: "other", Kind: domain.KindWorker, Prompt: "build the thing", TaskSize: domain.TaskSizeStandard,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if rt.aliveByHandle == nil {
		rt.aliveByHandle = map[string]bool{}
	}
	rt.aliveByHandle[dev.Metadata.RuntimeHandleID] = true

	if _, err := m.RequestCrewReview(ctx, dev.ID, domain.CrewRoleQA); err != nil {
		t.Fatalf("RequestCrewReview on another project: %v", err)
	}

	if !st.sessions[dev.ID].InCrew() {
		t.Fatal("turning automatic crew off for one project turned it off for another")
	}
}

// THE HATCH IS A PERSON'S, AND THE GATE IS WHAT MAKES THAT TRUE.
//
// The escape hatch above was designed for a human clicking `+ qa`. An AGENT can
// walk through it too, and did: with the flag on, six consecutive tasks still
// got a qa, because each worker's brief says the smoke checklist belongs to qa,
// so on finding none it ran `ao crew add` itself. `crew_join_reason` recorded
// `manual` on every one of them and nothing anywhere said the project's own
// setting was being overruled - for two days.
//
// So an attach that identifies itself as coming from an AO session is refused
// here. `requestedBy` is that identity: `ao crew add` sends $AO_SESSION_ID, and
// a human's shell and the desktop app both send nothing.
func TestAttachCrewMember_RefusesAnAOSessionOnACrewOffProject(t *testing.T) {
	m, st, _, _ := crewOffManager(t)
	dev := standardDev(t, m, st)
	before := len(st.sessions)

	_, err := m.AttachCrewMember(ctx, dev.ID, domain.CrewRoleQA, "mer-42")
	if !errors.Is(err, ErrCrewAutoFormationOff) {
		t.Fatalf("an agent attached a qa on a crew-off project: err = %v, want ErrCrewAutoFormationOff", err)
	}
	if len(st.sessions) != before {
		t.Fatalf("a refused attach still wrote a row: %d sessions, want %d", len(st.sessions), before)
	}
	if st.sessions[dev.ID].InCrew() {
		t.Fatal("a refused attach still put dev in a crew")
	}

	// THE REFUSAL HAS TO BE ACTIONABLE. A bare "refused" sends the next agent
	// hunting for a workaround, which is how the incident happened in the first
	// place: it must say this is the project's policy and that a PERSON can still
	// add a qa from the app.
	for _, want := range []string{"Never form a crew automatically", "person", "+ qa", "app"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the refusal does not mention %q, so the caller cannot act on it: %v", want, err)
		}
	}
}

// THE ORCHESTRATOR IS NOT EXEMPT. It is an AO session like any other, and it is
// the agent most able to override a project policy at scale - it is the one that
// dispatches every task. Its own `ao crew add` sends $AO_SESSION_ID exactly as a
// worker's does, so the same refusal answers it, and the human is the only
// caller left.
func TestAttachCrewMember_RefusesTheOrchestratorToo(t *testing.T) {
	m, st, _, _ := crewOffManager(t)
	dev := standardDev(t, m, st)

	orc, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindOrchestrator, Prompt: "dispatch"})
	if err != nil {
		t.Fatalf("Spawn orchestrator: %v", err)
	}
	if _, err := m.AttachCrewMember(ctx, dev.ID, domain.CrewRoleQA, orc.ID); !errors.Is(err, ErrCrewAutoFormationOff) {
		t.Fatalf("the orchestrator attached a qa on a crew-off project: err = %v, want ErrCrewAutoFormationOff", err)
	}
}

// THE GATE IS THE FLAG, NOT THE CALLER. An agent on an ordinary project still
// attaches a qa: `ao crew add` is a normal thing for a worker to run, and this
// change must not turn it into a refusal everywhere.
func TestAttachCrewMember_AnAOSessionStillAttachesOnAnOrdinaryProject(t *testing.T) {
	m, st, _, _ := newManager()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	dev := standardDev(t, m, st)

	qa, err := m.AttachCrewMember(ctx, dev.ID, domain.CrewRoleQA, "mer-42")
	if err != nil {
		t.Fatalf("an agent could not add a qa on a project that forms crews: %v", err)
	}
	if qa.CrewRole != domain.CrewRoleQA || qa.CrewID != dev.ID {
		t.Fatalf("qa row role=%q crew=%q, want qa/%s", qa.CrewRole, qa.CrewID, dev.ID)
	}
}

// A QA ON A CREW-OFF PROJECT IS NEVER SILENT. The human path stays open, so one
// can still appear there - and what made the incident last two days was that
// nothing said so. Every such attach leaves a WARN naming the project, the task
// and the member, whoever asked.
func TestAttachCrewMember_LogsWhenAQAAppearsOnACrewOffProject(t *testing.T) {
	m, st, _, _ := crewOffManager(t)
	var buf bytes.Buffer
	m.logger = slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	dev := standardDev(t, m, st)

	qa, err := m.AttachCrewMember(ctx, dev.ID, domain.CrewRoleQA, "")
	if err != nil {
		t.Fatalf("AttachCrewMember: %v", err)
	}
	logged := buf.String()
	if !strings.Contains(logged, "forms no crews automatically") {
		t.Fatalf("a qa appeared on a crew-off project and nothing was logged at WARN:\n%s", logged)
	}
	for _, want := range []string{string(dev.ProjectID), string(dev.ID), string(qa.ID)} {
		if !strings.Contains(logged, want) {
			t.Fatalf("the warning does not name %q, so it cannot be traced back:\n%s", want, logged)
		}
	}
}

// The ordinary project stays quiet: a warning on every attach everywhere is a
// warning nobody reads.
func TestAttachCrewMember_DoesNotWarnOnAnOrdinaryProject(t *testing.T) {
	m, st, _, _ := newManager()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	var buf bytes.Buffer
	m.logger = slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	dev := standardDev(t, m, st)

	if _, err := m.AttachCrewMember(ctx, dev.ID, domain.CrewRoleQA, ""); err != nil {
		t.Fatalf("AttachCrewMember: %v", err)
	}
	if strings.Contains(buf.String(), "forms no crews automatically") {
		t.Fatalf("a project that forms crews warned about forming one:\n%s", buf.String())
	}
}
