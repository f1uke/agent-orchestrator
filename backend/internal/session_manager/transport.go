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
	Kind     domain.SessionKind
}

// ResolveTransport decides a new session's transport: explicit, project, global, then the built-in default.
func ResolveTransport(c TransportChoice) domain.Transport {
	if c.Explicit != domain.TransportUnresolved {
		return c.Explicit
	}
	if c.Kind == domain.KindOrchestrator || c.Harness != domain.HarnessClaudeCode {
		return domain.TransportTerminal
	}
	for _, t := range []domain.Transport{c.Project, c.Global} {
		if t != domain.TransportUnresolved {
			return t
		}
	}
	return domain.DefaultWorkerTransport
}

func (m *Manager) resolveTransport(explicit domain.Transport, harness domain.AgentHarness, kind domain.SessionKind, project domain.ProjectConfig) domain.Transport {
	var global domain.Transport
	if m.workerTransport != nil {
		global = m.workerTransport()
	}
	return ResolveTransport(TransportChoice{Explicit: explicit, Project: project.WorkerTransport, Global: global, Harness: harness, Kind: kind})
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

func (m *Manager) wrapForTransport(t domain.Transport, id domain.SessionID, agentArgv []string, stdinFile string) ([]string, string, error) {
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
	argv = append(argv, "--")
	return append(argv, agentArgv...), "", nil
}
