package sessionmanager

import (
	"errors"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/sessionhost"
)

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
