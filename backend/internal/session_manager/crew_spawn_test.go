package sessionmanager

import (
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/runtime/tmux"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// seedCrewDev puts a live, materialized worker on the board for a crew member to
// join, and points the fake workspace at the same path a real gitworktree would
// hand back for that branch (the directory is named for the BRANCH, so a second
// session on it resolves to the same tree).
func seedCrewDev(m *Manager, st *fakeStore, ws *fakeWorkspace) domain.SessionRecord {
	dev := domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
		BaseBranch: "main", PRTarget: "main",
		Metadata: domain.SessionMetadata{Branch: "feature/task", WorkspacePath: "/ws/feature/task", RuntimeHandleID: "h1", Prompt: "do the task"},
		Activity: domain.Activity{State: domain.ActivityActive},
	}
	st.sessions[dev.ID] = dev
	st.num = 1
	ws.path = "/ws/feature/task"
	return dev
}

// TestSpawnCrewMember_JoinsDevsWorktreeAndBranch is the capability itself: a
// second long-lived session that works in the SAME tree, on the SAME branch, as
// the task it belongs to — and a crew recorded on both rows, with dev named as
// the owner.
func TestSpawnCrewMember_JoinsDevsWorktreeAndBranch(t *testing.T) {
	m, st, rt, ws := newManager()
	dev := seedCrewDev(m, st, ws)

	qa, err := m.Spawn(ctx, ports.SpawnConfig{
		ProjectID: "mer", Kind: domain.KindWorker, Prompt: "test the task",
		CrewOf: dev.ID, CrewRole: domain.CrewRoleQA,
	})
	if err != nil {
		t.Fatalf("crew spawn: %v", err)
	}

	if qa.Metadata.WorkspacePath != dev.Metadata.WorkspacePath {
		t.Fatalf("crew member workspace = %q, want dev's %q — the whole point is one worktree", qa.Metadata.WorkspacePath, dev.Metadata.WorkspacePath)
	}
	if qa.Metadata.Branch != dev.Metadata.Branch {
		t.Fatalf("crew member branch = %q, want dev's %q", qa.Metadata.Branch, dev.Metadata.Branch)
	}
	if got := ws.createdBranches; len(got) != 1 || got[0] != dev.Metadata.Branch {
		t.Fatalf("workspace create branches = %v, want exactly [%s]: a crew member must never cut a second branch", got, dev.Metadata.Branch)
	}
	if qa.CrewID != dev.ID || qa.CrewRole != domain.CrewRoleQA {
		t.Fatalf("member crew = %q/%q, want %q/qa", qa.CrewID, qa.CrewRole, dev.ID)
	}
	gotDev := st.sessions[dev.ID]
	if gotDev.CrewID != dev.ID || !gotDev.CrewRole.IsDev() {
		t.Fatalf("dev crew = %q/%q, want %q/dev - dev must be recorded as the owner", gotDev.CrewID, gotDev.CrewRole, dev.ID)
	}
	if rt.created != 1 {
		t.Fatalf("runtime created %d times, want 1", rt.created)
	}
}

// TestSpawnCrewMember_GetsItsOwnRuntimeName pins the one thing sharing a branch
// genuinely breaks. The tmux adapter names a session after project+branch, so two
// members on one branch would land in ONE tmux: destroying either would kill both
// agents, and the idle sweep could not tell whose runtime it was reaping. A
// non-dev member is named after its session id instead.
func TestSpawnCrewMember_GetsItsOwnRuntimeName(t *testing.T) {
	m, st, rt, ws := newManager()
	dev := seedCrewDev(m, st, ws)

	if _, err := m.Spawn(ctx, ports.SpawnConfig{
		ProjectID: "mer", Kind: domain.KindWorker, Prompt: "test the task",
		CrewOf: dev.ID, CrewRole: domain.CrewRoleQA,
	}); err != nil {
		t.Fatalf("crew spawn: %v", err)
	}
	if rt.lastCfg.Branch != "" {
		t.Fatalf("crew member runtime branch = %q, want empty so the handle is named after the session id, not the shared branch", rt.lastCfg.Branch)
	}
	if rt.lastCfg.WorkspacePath != dev.Metadata.WorkspacePath {
		t.Fatalf("crew member runtime cwd = %q, want dev's worktree %q", rt.lastCfg.WorkspacePath, dev.Metadata.WorkspacePath)
	}

	// And the adapter really does produce two different names from those inputs.
	devName, err := tmux.SessionNameFor("mer", dev.Metadata.Branch, string(dev.ID))
	if err != nil {
		t.Fatal(err)
	}
	qaName, err := tmux.SessionNameFor("mer", "", "mer-2")
	if err != nil {
		t.Fatal(err)
	}
	if devName == qaName {
		t.Fatalf("both crew members resolve to tmux session %q; they must not share a pane", devName)
	}
}

// TestSpawnCrewMember_LeavesTheSharedTreeAlone: dev is a LIVE agent working in
// that worktree. Re-running the project's post-create provisioning (a `pnpm
// install`, a symlink pass) into a tree somebody is editing is not a fresh
// checkout, it is an interruption.
func TestSpawnCrewMember_LeavesTheSharedTreeAlone(t *testing.T) {
	m, st, _, ws := newManager()
	dev := seedCrewDev(m, st, ws)
	st.projects["mer"] = domain.ProjectRecord{
		ID: "mer", Config: domain.ProjectConfig{
			Worker:       domain.RoleOverride{Harness: domain.HarnessClaudeCode},
			Orchestrator: domain.RoleOverride{Harness: domain.HarnessClaudeCode},
			PostCreate:   []string{"exit 7"}, // would fail the spawn if it ran
		},
	}

	if _, err := m.Spawn(ctx, ports.SpawnConfig{
		ProjectID: "mer", Kind: domain.KindWorker, Prompt: "test the task",
		CrewOf: dev.ID, CrewRole: domain.CrewRoleQA,
	}); err != nil {
		t.Fatalf("crew spawn ran the project's post-create commands into a live worktree: %v", err)
	}
}

// TestSpawnCrewMember_RefusesAnImpossibleCrew. Crews are formed only through the
// Go seam, so these are programming errors — but each one would leave a session
// attached to work it can never land, so every one is refused BEFORE any durable
// row exists.
func TestSpawnCrewMember_RefusesAnImpossibleCrew(t *testing.T) {
	cases := []struct {
		name  string
		seed  func(st *fakeStore)
		devID domain.SessionID
		role  domain.CrewRole
	}{
		{name: "unknown dev", devID: "mer-99", role: domain.CrewRoleQA},
		{name: "role dev", devID: "mer-1", role: domain.CrewRoleDev},
		{name: "no role", devID: "mer-1", role: ""},
		{
			name:  "terminated dev",
			seed:  func(st *fakeStore) { r := st.sessions["mer-1"]; r.IsTerminated = true; st.sessions["mer-1"] = r },
			devID: "mer-1", role: domain.CrewRoleQA,
		},
		{
			name:  "orchestrator dev",
			seed:  func(st *fakeStore) { r := st.sessions["mer-1"]; r.Kind = domain.KindOrchestrator; st.sessions["mer-1"] = r },
			devID: "mer-1", role: domain.CrewRoleQA,
		},
		{
			name: "dev is itself a subordinate",
			seed: func(st *fakeStore) {
				r := st.sessions["mer-1"]
				r.CrewID, r.CrewRole = "mer-0", domain.CrewRoleQA
				st.sessions["mer-1"] = r
			},
			devID: "mer-1", role: domain.CrewRoleQA,
		},
		{
			name:  "dev has no worktree yet",
			seed:  func(st *fakeStore) { r := st.sessions["mer-1"]; r.Metadata.WorkspacePath = ""; st.sessions["mer-1"] = r },
			devID: "mer-1", role: domain.CrewRoleQA,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, st, rt, ws := newManager()
			seedCrewDev(m, st, ws)
			if tc.seed != nil {
				tc.seed(st)
			}
			before := len(st.sessions)

			_, err := m.Spawn(ctx, ports.SpawnConfig{
				ProjectID: "mer", Kind: domain.KindWorker, Prompt: "test",
				CrewOf: tc.devID, CrewRole: tc.role,
			})
			if !errors.Is(err, ErrInvalidCrew) {
				t.Fatalf("Spawn = %v, want ErrInvalidCrew", err)
			}
			if len(st.sessions) != before {
				t.Fatalf("a refused crew spawn left %d session row(s) behind", len(st.sessions)-before)
			}
			if rt.created != 0 || ws.createCalls != 0 {
				t.Fatalf("a refused crew spawn touched the world: runtime=%d workspace=%d", rt.created, ws.createCalls)
			}
		})
	}
}

// TestSpawn_SoloIsUntouched is the preservation guard for the spawn path: an
// ordinary spawn names no crew, so it must still cut its own branch, provision
// its own tree, take a branch-named runtime handle, and come out SOLO.
func TestSpawn_SoloIsUntouched(t *testing.T) {
	m, st, rt, ws := newManager()

	rec, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Prompt: "work"})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if rec.InCrew() || rec.CrewID != "" || rec.CrewRole != "" {
		t.Fatalf("an ordinary spawn produced a crew member: crewID=%q role=%q", rec.CrewID, rec.CrewRole)
	}
	if rt.lastCfg.Branch != rec.Metadata.Branch || rec.Metadata.Branch == "" {
		t.Fatalf("solo runtime branch = %q, want the session branch %q (branch-named tmux is unchanged)", rt.lastCfg.Branch, rec.Metadata.Branch)
	}
	if ws.createCalls != 1 {
		t.Fatalf("solo spawn created %d workspaces, want 1", ws.createCalls)
	}
	if len(st.sessions) != 1 {
		t.Fatalf("solo spawn created %d sessions, want exactly 1", len(st.sessions))
	}
}
