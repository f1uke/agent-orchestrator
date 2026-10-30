package sessionmanager

import (
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func validateTransport(harness domain.AgentHarness, t domain.Transport) error {
	if t == domain.TransportChat && harness != domain.HarnessClaudeCode {
		return fmt.Errorf("%w: transport %s needs harness %s, not %s", ErrTransportUnsupported, t, domain.HarnessClaudeCode, harness)
	}
	return nil
}
