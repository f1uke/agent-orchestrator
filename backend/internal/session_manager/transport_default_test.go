package sessionmanager

import (
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestResolveTransport(t *testing.T) {
	const (
		terminal = domain.TransportTerminal
		chat     = domain.TransportChat
		claude   = domain.HarnessClaudeCode
	)
	for _, tc := range []struct {
		name string
		in   TransportChoice
		want domain.Transport
	}{
		{"nothing set is the built-in chat", TransportChoice{Harness: claude}, chat},
		{"the global setting beats the built-in", TransportChoice{Global: terminal, Harness: claude}, terminal},
		{"the project beats the global setting", TransportChoice{Project: chat, Global: terminal, Harness: claude}, chat},
		{"the project beats the global setting, the other way", TransportChoice{Project: terminal, Global: chat, Harness: claude}, terminal},
		{"an explicit terminal beats a project chat", TransportChoice{Explicit: terminal, Project: chat, Global: chat, Harness: claude}, terminal},
		{"an explicit chat beats a project terminal", TransportChoice{Explicit: chat, Project: terminal, Global: terminal, Harness: claude}, chat},
		{"another agent is terminal whatever the settings say", TransportChoice{Project: chat, Global: chat, Harness: domain.HarnessCodex}, terminal},
		{"an explicit chat reaches an agent that cannot host it, for validateTransport to refuse", TransportChoice{Explicit: chat, Harness: domain.HarnessCodex}, chat},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ResolveTransport(tc.in); got != tc.want {
				t.Fatalf("ResolveTransport(%+v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestSpawnResolvesTheTransportFromTheSettings(t *testing.T) {
	const (
		terminal = domain.TransportTerminal
		chat     = domain.TransportChat
	)
	type settings struct{ projectWorker, projectOrch, globalWorker, globalOrch domain.Transport }
	for _, tc := range []struct {
		name         string
		settings     settings
		spawn        ports.SpawnConfig
		wantTerminal bool
	}{
		{name: "nothing set gives a chat worker", spawn: ports.SpawnConfig{Kind: domain.KindWorker}},
		{name: "a project terminal gives a terminal worker", settings: settings{projectWorker: terminal}, spawn: ports.SpawnConfig{Kind: domain.KindWorker}, wantTerminal: true},
		{name: "a global terminal gives a terminal worker", settings: settings{globalWorker: terminal}, spawn: ports.SpawnConfig{Kind: domain.KindWorker}, wantTerminal: true},
		{name: "a project chat beats a global terminal", settings: settings{projectWorker: chat, globalWorker: terminal}, spawn: ports.SpawnConfig{Kind: domain.KindWorker}},
		{name: "an explicit terminal beats a project chat", settings: settings{projectWorker: chat}, spawn: ports.SpawnConfig{Kind: domain.KindWorker, Transport: terminal}, wantTerminal: true},
		{name: "another agent stays terminal and the setting is no error", settings: settings{projectWorker: chat, globalWorker: chat}, spawn: ports.SpawnConfig{Kind: domain.KindWorker, Harness: domain.HarnessCodex}, wantTerminal: true},
		{name: "the orchestrator settings do not move a worker", settings: settings{projectOrch: terminal, globalOrch: terminal}, spawn: ports.SpawnConfig{Kind: domain.KindWorker}},

		{name: "nothing set gives a chat orchestrator", spawn: ports.SpawnConfig{Kind: domain.KindOrchestrator}},
		{name: "a project terminal gives a terminal orchestrator", settings: settings{projectOrch: terminal}, spawn: ports.SpawnConfig{Kind: domain.KindOrchestrator}, wantTerminal: true},
		{name: "a global terminal gives a terminal orchestrator", settings: settings{globalOrch: terminal}, spawn: ports.SpawnConfig{Kind: domain.KindOrchestrator}, wantTerminal: true},
		{name: "a project chat beats a global terminal for an orchestrator", settings: settings{projectOrch: chat, globalOrch: terminal}, spawn: ports.SpawnConfig{Kind: domain.KindOrchestrator}},
		{name: "the worker settings do not move an orchestrator", settings: settings{projectWorker: terminal, globalWorker: terminal}, spawn: ports.SpawnConfig{Kind: domain.KindOrchestrator}},
		{name: "an explicit terminal beats an orchestrator project chat", settings: settings{projectOrch: chat}, spawn: ports.SpawnConfig{Kind: domain.KindOrchestrator, Transport: terminal}, wantTerminal: true},
		{name: "another agent's orchestrator stays terminal", settings: settings{projectOrch: chat, globalOrch: chat}, spawn: ports.SpawnConfig{Kind: domain.KindOrchestrator, Harness: domain.HarnessCodex}, wantTerminal: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newChatFixture(t)
			cfg := testRoleAgents()
			cfg.WorkerTransport, cfg.OrchestratorTransport = tc.settings.projectWorker, tc.settings.projectOrch
			f.st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: cfg}
			f.m.workerTransport = func() domain.Transport { return tc.settings.globalWorker }
			f.m.orchestratorTransport = func() domain.Transport { return tc.settings.globalOrch }

			tc.spawn.ProjectID, tc.spawn.Prompt = "mer", "do it"
			rec, err := f.m.Spawn(ctx, tc.spawn)
			if err != nil {
				t.Fatal(err)
			}
			if tc.wantTerminal {
				if rec.Transport != terminal || f.rt.lastCfg.Argv[0] == daemonExe {
					t.Fatalf("transport = %q, argv = %q; want a terminal launch", rec.Transport, f.rt.lastCfg.Argv)
				}
				return
			}
			if rec.Transport != chat {
				t.Fatalf("stored transport = %q, want chat", rec.Transport)
			}
			f.assertHostLaunch(t, rec.ID, "spawn")
		})
	}
}

func TestChangingASettingNeverMovesAnExistingSession(t *testing.T) {
	f := newChatFixture(t)
	chatRec, err := f.m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Prompt: "do it"})
	if err != nil {
		t.Fatal(err)
	}
	terminalRec, err := f.m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Prompt: "do it", Transport: domain.TransportTerminal})
	if err != nil {
		t.Fatal(err)
	}

	cfg := testRoleAgents()
	cfg.WorkerTransport = domain.TransportTerminal
	f.st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: cfg}
	f.m.workerTransport = func() domain.Transport { return domain.TransportTerminal }
	for _, rec := range []domain.SessionRecord{chatRec, terminalRec} {
		row := f.st.sessions[rec.ID]
		row.IsTerminated = true
		f.st.sessions[rec.ID] = row
	}

	if _, err := f.m.Restore(ctx, chatRec.ID); err != nil {
		t.Fatal(err)
	}
	f.assertHostLaunch(t, chatRec.ID, "restore of a chat session after the settings went to terminal")

	cfg.WorkerTransport = domain.TransportChat
	f.st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: cfg}
	if _, err := f.m.Restore(ctx, terminalRec.ID); err != nil {
		t.Fatal(err)
	}
	if got := f.st.sessions[terminalRec.ID].Transport; got != domain.TransportTerminal || f.rt.lastCfg.Argv[0] == daemonExe {
		t.Fatalf("restore of a terminal session after the project went to chat: transport = %q, argv = %q; want terminal", got, f.rt.lastCfg.Argv)
	}
}

func TestChangingASettingNeverMovesAnExistingOrchestrator(t *testing.T) {
	f := newChatFixture(t)
	cfg := testRoleAgents()
	cfg.OrchestratorTransport = domain.TransportTerminal
	f.st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: cfg}
	terminalRec, err := f.m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindOrchestrator, Prompt: "do it"})
	if err != nil {
		t.Fatal(err)
	}
	if terminalRec.Transport != domain.TransportTerminal {
		t.Fatalf("spawned under a terminal project: transport = %q, want terminal", terminalRec.Transport)
	}
	row := f.st.sessions[terminalRec.ID]
	row.IsTerminated = true
	f.st.sessions[terminalRec.ID] = row

	cfg.OrchestratorTransport = domain.TransportChat
	f.st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: cfg}
	f.m.orchestratorTransport = func() domain.Transport { return domain.TransportChat }
	if _, err := f.m.Restore(ctx, terminalRec.ID); err != nil {
		t.Fatal(err)
	}
	if got := f.st.sessions[terminalRec.ID].Transport; got != domain.TransportTerminal || f.rt.lastCfg.Argv[0] == daemonExe {
		t.Fatalf("restore of a terminal orchestrator after the default went to chat: transport = %q, argv = %q; want terminal", got, f.rt.lastCfg.Argv)
	}
}

func TestQueuedTodoResolvesItsTransportWhenItStarts(t *testing.T) {
	f := newChatFixture(t)
	todo, err := f.m.PrepareTodo(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Prompt: "do it"})
	if err != nil {
		t.Fatal(err)
	}
	if got := f.st.sessions[todo.ID].Transport; got != "" {
		t.Fatalf("queued TODO stored transport %q, want it unresolved", got)
	}

	cfg := testRoleAgents()
	cfg.WorkerTransport = domain.TransportTerminal
	f.st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: cfg}
	started, err := f.m.StartTodo(ctx, todo.ID)
	if err != nil {
		t.Fatal(err)
	}
	if started.Transport != domain.TransportTerminal || f.rt.lastCfg.Argv[0] == daemonExe {
		t.Fatalf("TODO started under a terminal project: transport = %q, argv = %q", started.Transport, f.rt.lastCfg.Argv)
	}
}

func TestQueuedTodoWithNothingSetStartsAsChat(t *testing.T) {
	f := newChatFixture(t)
	todo, err := f.m.PrepareTodo(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Prompt: "do it"})
	if err != nil {
		t.Fatal(err)
	}
	started, err := f.m.StartTodo(ctx, todo.ID)
	if err != nil {
		t.Fatal(err)
	}
	if started.Transport != domain.TransportChat {
		t.Fatalf("stored transport = %q, want chat", started.Transport)
	}
	f.assertHostLaunch(t, todo.ID, "todo start")
}

func TestQueuedTodoNamedTerminalStaysTerminalWhateverTheSettingsSay(t *testing.T) {
	f := newChatFixture(t)
	todo, err := f.m.PrepareTodo(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Prompt: "do it", Transport: domain.TransportTerminal})
	if err != nil {
		t.Fatal(err)
	}
	f.m.workerTransport = func() domain.Transport { return domain.TransportChat }
	started, err := f.m.StartTodo(ctx, todo.ID)
	if err != nil {
		t.Fatal(err)
	}
	if started.Transport != domain.TransportTerminal {
		t.Fatalf("stored transport = %q, want terminal", started.Transport)
	}
}

func TestQueuedTodoAgentEditedToCodexStartsTerminalNotRefused(t *testing.T) {
	f := newChatFixture(t)
	todo, err := f.m.PrepareTodo(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Prompt: "do it"})
	if err != nil {
		t.Fatal(err)
	}
	codex := domain.HarnessCodex
	if _, err := f.m.UpdateTodoSpec(ctx, todo.ID, ports.TodoSpecPatch{Harness: &codex}); err != nil {
		t.Fatal(err)
	}
	started, err := f.m.StartTodo(ctx, todo.ID)
	if err != nil {
		t.Fatalf("StartTodo after switching the agent to codex: %v", err)
	}
	if started.Transport != domain.TransportTerminal {
		t.Fatalf("stored transport = %q, want terminal", started.Transport)
	}
}

func TestFailedTodoStartLeavesItsTransportUnresolvedForTheRetry(t *testing.T) {
	f := newChatFixture(t)
	todo, err := f.m.PrepareTodo(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Prompt: "do it"})
	if err != nil {
		t.Fatal(err)
	}
	f.rt.createErr = errors.New("tmux is down")
	if _, err := f.m.StartTodo(ctx, todo.ID); err == nil {
		t.Fatal("StartTodo succeeded against a runtime that cannot create")
	}
	if got := f.st.sessions[todo.ID].Transport; got != "" {
		t.Fatalf("failed start left transport %q, want it unresolved", got)
	}

	f.rt.createErr = nil
	cfg := testRoleAgents()
	cfg.WorkerTransport = domain.TransportTerminal
	f.st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: cfg}
	started, err := f.m.StartTodo(ctx, todo.ID)
	if err != nil {
		t.Fatal(err)
	}
	if started.Transport != domain.TransportTerminal {
		t.Fatalf("retry after the project went to terminal: transport = %q, want terminal", started.Transport)
	}
}
