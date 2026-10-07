package sessionmanager

import (
	"context"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type fakeChildren struct {
	undelivered []domain.SessionChild
	settled     []domain.SessionID
	orphaned    []domain.SessionID
}

func (f *fakeChildren) Undelivered(context.Context, domain.SessionID) ([]domain.SessionChild, error) {
	return f.undelivered, nil
}

func (f *fakeChildren) SettleForTeardown(_ context.Context, id domain.SessionID) error {
	f.settled = append(f.settled, id)
	return nil
}

func (f *fakeChildren) SettleOrphans(_ context.Context, id domain.SessionID) error {
	f.orphaned = append(f.orphaned, id)
	return nil
}

// childCapableAgent is an agent whose installed binary hands isolated
// subagents' worktrees to AO.
type childCapableAgent struct {
	recordingAgent
	capable bool
	hooks   ports.WorkspaceHookConfig
}

func (a *childCapableAgent) SupportsChildWorktrees(context.Context) bool { return a.capable }

func (a *childCapableAgent) GetAgentHooks(_ context.Context, cfg ports.WorkspaceHookConfig) error {
	a.hooks = cfg
	return nil
}

func newChildManager(capable bool, children *fakeChildren) (*Manager, *fakeStore, *fakeRuntime, *childCapableAgent) {
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	rt := &fakeRuntime{}
	agent := &childCapableAgent{capable: capable}
	lookPath := func(string) (string, error) { return "/bin/true", nil }
	m := New(Deps{Runtime: rt, Agents: singleAgent{agent: agent}, Workspace: &fakeWorkspace{}, Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st}, LookPath: lookPath})
	if children != nil {
		m.SetChildren(children)
	}
	return m, st, rt, agent
}

// The hooks, the env var the hook CLI reads and the worker floor are one
// decision: a worker told it may isolate children must also have the hook that
// makes isolation safe, and the hook must never exist without the permission.
func TestSpawn_ChildWorktreesSwitchHooksEnvAndFloorTogether(t *testing.T) {
	cases := []struct {
		name     string
		capable  bool
		children bool
		kind     domain.SessionKind
		want     bool
	}{
		{"capable worker", true, true, domain.KindWorker, true},
		{"older Claude Code", false, true, domain.KindWorker, false},
		{"no child service", true, false, domain.KindWorker, false},
		{"orchestrator", true, true, domain.KindOrchestrator, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var children *fakeChildren
			if tc.children {
				children = &fakeChildren{}
			}
			m, _, rt, agent := newChildManager(tc.capable, children)
			if _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: tc.kind}); err != nil {
				t.Fatal(err)
			}
			if agent.hooks.ChildWorktrees != tc.want {
				t.Errorf("hooks ChildWorktrees = %v, want %v", agent.hooks.ChildWorktrees, tc.want)
			}
			if got := rt.lastCfg.Env[EnvChildWorktrees] == "1"; got != tc.want {
				t.Errorf("%s set = %v, want %v", EnvChildWorktrees, got, tc.want)
			}
			if tc.kind != domain.KindWorker {
				return
			}
			own := strings.Contains(agent.lastLaunch.SystemPrompt, "Child agents and their worktrees")
			shared := strings.Contains(agent.lastLaunch.SystemPrompt, "Child agents share this AO worktree")
			if own != tc.want || shared == tc.want {
				t.Errorf("floor: own-worktrees section %v, shared section %v; want own=%v", own, shared, tc.want)
			}
		})
	}
}

func TestRuntimeEnv_ProjectCannotTurnChildWorktreesOn(t *testing.T) {
	m, _, _, _ := newChildManager(false, &fakeChildren{})
	env := m.runtimeEnv(ctx, "mer-1", "mer", "", domain.KindWorker, "", "", "/work", map[string]string{EnvChildWorktrees: "1"}, false, "")
	if _, ok := env[EnvChildWorktrees]; ok {
		t.Fatalf("a project env turned %s on for a worker AO did not enable", EnvChildWorktrees)
	}
}

func TestKill_RefusedWhileAChildHoldsUndeliveredWorkAndTouchesNothing(t *testing.T) {
	children := &fakeChildren{undelivered: []domain.SessionChild{{AgentID: "a1", State: domain.ChildRunning}}}
	m, st, rt, _ := newChildManager(true, children)
	st.sessions["mer-1"] = mkLive("mer-1")

	res, err := m.Kill(ctx, "mer-1", KillOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Reason != ReasonChildrenUndelivered || res.Terminated || len(res.UndeliveredChildren) != 1 {
		t.Fatalf("res = %+v, want a refusal naming the child", res)
	}
	if rt.destroyed != 0 || len(children.settled) != 0 || st.sessions["mer-1"].IsTerminated {
		t.Fatal("a refused kill touched the session or its children")
	}
}

func TestKill_DiscardSetsChildWorkAsideInsteadOfRefusing(t *testing.T) {
	children := &fakeChildren{undelivered: []domain.SessionChild{{AgentID: "a1", State: domain.ChildRunning}}}
	m, st, rt, _ := newChildManager(true, children)
	st.sessions["mer-1"] = mkLive("mer-1")

	res, err := m.Kill(ctx, "mer-1", KillOptions{DiscardUncommitted: true})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Terminated || rt.destroyed != 1 {
		t.Fatalf("res = %+v, want the session ended", res)
	}
	if len(children.settled) != 1 || children.settled[0] != "mer-1" {
		t.Fatalf("settled = %v, want the worker's children set aside", children.settled)
	}
}

func TestTeardown_BackgroundSettlesChildren(t *testing.T) {
	children := &fakeChildren{undelivered: []domain.SessionChild{{AgentID: "a1", State: domain.ChildHeld}}}
	m, st, _, _ := newChildManager(true, children)
	st.sessions["mer-1"] = mkLive("mer-1")
	if _, err := m.Teardown(ctx, "mer-1", domain.TerminationCauseAutoReclaim); err != nil {
		t.Fatal(err)
	}
	if len(children.settled) != 1 {
		t.Fatalf("settled = %v, want the reclaim to set the children aside", children.settled)
	}
}

func TestRestore_SettlesOrphanedChildrenBeforeRelaunch(t *testing.T) {
	children := &fakeChildren{}
	m, st, rt, _ := newChildManager(true, children)
	seedTerminal(st, "mer-1", domain.SessionMetadata{WorkspacePath: "/ws/mer-1", Branch: "b", AgentSessionID: "agent-x"})
	rec := st.sessions["mer-1"]
	rec.Kind = domain.KindWorker
	st.sessions["mer-1"] = rec
	if _, err := m.Restore(ctx, "mer-1"); err != nil {
		t.Fatal(err)
	}
	if rt.created != 1 || len(children.orphaned) != 1 || children.orphaned[0] != "mer-1" {
		t.Fatalf("relaunched %d, orphans settled for %v; want the children settled before the relaunch", rt.created, children.orphaned)
	}
}

// The refusal is the preview a discard is confirmed against, so with both kinds
// of undelivered work it must name both: naming only the children would let a
// discard throw away files nobody was shown.
func TestKill_RefusalNamesFilesAndChildrenTogether(t *testing.T) {
	children := &fakeChildren{undelivered: []domain.SessionChild{{AgentID: "a1", State: domain.ChildRunning}}}
	m, st, _, _ := newChildManager(true, children)
	ws := &fakeWorkspace{}
	m.workspace = ws
	st.sessions["mer-1"] = mkLive("mer-1")
	dirtyWorkspace(ws, ports.UncommittedFile{Path: "src/main.go", Status: ports.UncommittedModified})

	res, err := m.Kill(ctx, "mer-1", KillOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Reason != ReasonWorkspaceDirty || len(res.Undelivered) != 1 || len(res.UndeliveredChildren) != 1 {
		t.Fatalf("res = %+v, want the files refusal carrying the children too", res)
	}
}
