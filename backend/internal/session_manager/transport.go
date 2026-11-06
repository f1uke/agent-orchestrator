package sessionmanager

import (
	"context"
	"errors"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/sessionhost"
)

// TransportChoice is every input to ResolveTransport; an empty level named nothing.
type TransportChoice struct {
	Explicit domain.Transport
	Project  domain.Transport
	Global   domain.Transport
	Harness  domain.AgentHarness
}

// ResolveTransport decides a new session's transport: explicit, project, global, then the built-in default.
func ResolveTransport(c TransportChoice) domain.Transport {
	if c.Explicit != domain.TransportUnresolved {
		return c.Explicit
	}
	if c.Harness != domain.HarnessClaudeCode {
		return domain.TransportTerminal
	}
	for _, t := range []domain.Transport{c.Project, c.Global} {
		if t != domain.TransportUnresolved {
			return t
		}
	}
	return domain.DefaultClaudeCodeTransport
}

func (m *Manager) resolveTransport(explicit domain.Transport, harness domain.AgentHarness, kind domain.SessionKind, project domain.ProjectConfig) domain.Transport {
	choice := TransportChoice{Explicit: explicit, Harness: harness}
	global := m.workerTransport
	choice.Project = project.WorkerTransport
	if kind == domain.KindOrchestrator {
		global = m.orchestratorTransport
		choice.Project = project.OrchestratorTransport
	}
	if global != nil {
		choice.Global = global()
	}
	return ResolveTransport(choice)
}

func (m *Manager) setTransport(ctx context.Context, id domain.SessionID, t domain.Transport) error {
	ok, err := m.store.SetSessionTransport(ctx, id, t, m.clock())
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotFound
	}
	return nil
}

func validateTransport(harness domain.AgentHarness, t domain.Transport) error {
	if t == domain.TransportChat && harness != domain.HarnessClaudeCode {
		return fmt.Errorf("%w: transport %s needs harness %s, not %s", ErrTransportUnsupported, t, domain.HarnessClaudeCode, harness)
	}
	return nil
}

func (m *Manager) wrapForTransport(t domain.Transport, id domain.SessionID, agentArgv []string, stdinFile string, project domain.ProjectConfig) ([]string, string, error) {
	if t != domain.TransportChat {
		return agentArgv, stdinFile, nil
	}
	if m.dataDir == "" {
		return nil, "", errors.New("session host: the daemon has no data dir")
	}
	exe, err := m.executable()
	if err != nil {
		return nil, "", fmt.Errorf("session host: daemon executable: %w", err)
	}
	logPath, socketPath, err := sessionhost.PreparePaths(m.dataDir, string(id))
	if err != nil {
		return nil, "", err
	}
	argv := []string{exe, "session-host", "--log", logPath, "--socket", socketPath}
	if stdinFile != "" {
		argv = append(argv, "--prompt-file", stdinFile)
	}
	if project.NoIdleCompaction {
		argv = append(argv, "--no-idle-compact")
	}
	if project.NoPromptSuggestions {
		argv = append(argv, "--no-prompt-suggestions")
	}
	argv = append(argv, "--")
	return append(argv, agentArgv...), "", nil
}
