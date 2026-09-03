package sessionmanager

import (
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// A worker must be told the BRANCH its pull request merges into, and the flag
// that aims it there. Two workers in 24 hours ran `gh pr create` with no --base,
// which silently targets the repository's default branch: the PR merged toward
// the wrong place and CI ran the format gate over ~1300 commits of unrelated
// drift (#282, #287). The prompt is the seam - AO never retargets a PR on its
// own, so a deliberate, unusual base stays possible.
func TestWorkerPrompt_NamesThePRTargetAndTheBaseFlag(t *testing.T) {
	newMgr := func(defaultBranch string) (*Manager, *recordingAgent) {
		st := newFakeStore()
		cfg := testRoleAgents()
		cfg.DefaultBranch = defaultBranch
		st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: cfg}
		agent := &recordingAgent{}
		lookPath := func(string) (string, error) { return "/bin/true", nil }
		return New(Deps{Runtime: &fakeRuntime{}, Agents: singleAgent{agent: agent}, Workspace: &fakeWorkspace{}, Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st}, LookPath: lookPath}), agent
	}

	t.Run("explicit --target", func(t *testing.T) {
		m, agent := newMgr("main")
		if _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Prompt: "fix the rollup", BaseBranch: "main-fluke", PRTarget: "main-fluke"}); err != nil {
			t.Fatal(err)
		}
		sp := agent.lastLaunch.SystemPrompt
		for _, want := range []string{
			"## Opening this session's pull request",
			"This session's PR target is `main-fluke`",
			"gh pr create --base main-fluke",
			"glab mr create --target-branch main-fluke",
			"gh pr edit <number> --base main-fluke",
		} {
			if !strings.Contains(sp, want) {
				t.Fatalf("worker system prompt missing %q:\n%s", want, sp)
			}
		}
	})

	// The mistake AO must make impossible to fall into: a spawn that names no
	// --target still has one (it resolves to --from, and --from to the project
	// default), so the prompt must name THAT branch rather than going quiet and
	// leaving `gh pr create` to pick the repository default.
	t.Run("target resolved from the base branch", func(t *testing.T) {
		m, agent := newMgr("main")
		if _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Prompt: "fix the rollup", BaseBranch: "main-fluke"}); err != nil {
			t.Fatal(err)
		}
		if sp := agent.lastLaunch.SystemPrompt; !strings.Contains(sp, "gh pr create --base main-fluke") {
			t.Fatalf("worker prompt did not resolve the target from --from:\n%s", sp)
		}
	})

	t.Run("target resolved from the project default", func(t *testing.T) {
		m, agent := newMgr("main-fluke")
		if _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Prompt: "fix the rollup"}); err != nil {
			t.Fatal(err)
		}
		if sp := agent.lastLaunch.SystemPrompt; !strings.Contains(sp, "gh pr create --base main-fluke") {
			t.Fatalf("worker prompt did not resolve the target from the project default:\n%s", sp)
		}
	})

	// The orchestrator dispatches and does not open pull requests; qa is told
	// explicitly not to touch dev's. Neither carries the section.
	t.Run("not the orchestrator", func(t *testing.T) {
		m, agent := newMgr("main-fluke")
		if _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindOrchestrator}); err != nil {
			t.Fatal(err)
		}
		if sp := agent.lastLaunch.SystemPrompt; strings.Contains(sp, "## Opening this session's pull request") {
			t.Fatalf("orchestrator prompt should not carry the PR-target section:\n%s", sp)
		}
	})

	t.Run("not qa", func(t *testing.T) {
		st := newFakeStore()
		st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
		lookPath := func(string) (string, error) { return "/bin/true", nil }
		m := New(Deps{Runtime: &fakeRuntime{}, Agents: singleAgent{agent: &recordingAgent{}}, Workspace: &fakeWorkspace{}, Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st}, LookPath: lookPath})
		sp, err := m.buildSystemPrompt(ctx, systemPromptSpec{Kind: domain.KindWorker, ProjectID: "mer", TaskSize: domain.TaskSizeStandard, CrewRole: domain.CrewRoleQA, PRTarget: "main-fluke"})
		if err != nil {
			t.Fatalf("buildSystemPrompt: %v", err)
		}
		if strings.Contains(sp, "## Opening this session's pull request") {
			t.Fatalf("qa prompt should not carry the PR-target section:\n%s", sp)
		}
	})

	// Old rows carry no recorded target. Rendering the section with an empty
	// branch would print `gh pr create --base ` - worse than saying nothing, so
	// the floor's "recorded PR target" wording stands alone there.
	t.Run("unknown target renders nothing", func(t *testing.T) {
		if got := workerPRTargetPrompt("  "); got != "" {
			t.Fatalf("workerPRTargetPrompt(empty) = %q, want \"\"", got)
		}
	})
}

// A restored session rebuilds its standing instructions from the row, so the PR
// target has to survive the restore - otherwise the guidance is present for the
// first turn of a session's life and absent for every one after a daemon restart.
func TestRestoredWorkerPrompt_KeepsThePRTarget(t *testing.T) {
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	agent := &recordingAgent{}
	lookPath := func(string) (string, error) { return "/bin/true", nil }
	m := New(Deps{Runtime: &fakeRuntime{}, Agents: singleAgent{agent: agent}, Workspace: &fakeWorkspace{}, Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st}, LookPath: lookPath})

	st.sessions["mer-1"] = domain.SessionRecord{
		ID:           "mer-1",
		ProjectID:    "mer",
		Kind:         domain.KindWorker,
		BaseBranch:   "main-fluke",
		PRTarget:     "main-fluke",
		Metadata:     domain.SessionMetadata{WorkspacePath: "/ws/mer-1", Branch: "fix/rollup", AgentSessionID: "agent-x"},
		IsTerminated: true,
		Activity:     domain.Activity{State: domain.ActivityExited},
	}
	if _, err := m.Restore(ctx, "mer-1"); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if sp := agent.lastRestore.SystemPrompt; !strings.Contains(sp, "gh pr create --base main-fluke") {
		t.Fatalf("restored worker prompt lost the PR target:\n%s", sp)
	}
}
