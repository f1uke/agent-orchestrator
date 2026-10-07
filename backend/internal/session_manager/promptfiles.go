package sessionmanager

import (
	"context"
	"errors"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/promptfile"
)

// writeSystemPromptFile stores the session's standing instructions in its
// private prompt file (promptfile) and returns the path to hand the agent in
// place of the text, so the instructions never ride on the agent's command
// line. Every launch rewrites it from the freshly derived prompt, which is what
// keeps a restore pointing at the orchestrator that is active now. A manager
// with no data dir (tests that do not wire one) returns no path and the agent
// is handed the text as before.
func (m *Manager) writeSystemPromptFile(id domain.SessionID, systemPrompt string) (string, error) {
	if m.dataDir == "" {
		return "", nil
	}
	path, err := promptfile.Write(m.dataDir, id, promptfile.SystemPrompt, systemPrompt)
	if err != nil {
		return "", fmt.Errorf("system prompt file: %w", err)
	}
	return path, nil
}

// deliverInitialPrompt decides how the initial prompt reaches the agent. When
// the agent reads its first prompt from stdin (ports.PromptDeliveryStdin) and
// the runtime can connect a file there, the prompt goes into the session's
// private prompt file and comes back as stdinFile, leaving nothing for the
// command line: argv is what `pkill -f <word>` matches, and a task brief that
// names a command made the agent killable by anyone who pattern-kills it.
// Otherwise the prompt stays in the command (inCommand), as it always has.
func (m *Manager) deliverInitialPrompt(ctx context.Context, agent ports.Agent, id domain.SessionID, prompt string) (inCommand, stdinFile string, err error) {
	if prompt == "" || m.dataDir == "" {
		return prompt, "", nil
	}
	if c, ok := m.runtime.(ports.StdinFileConnector); !ok || !c.ConnectsStdinFile() {
		return prompt, "", nil
	}
	strategy, err := agent.GetPromptDeliveryStrategy(ctx, ports.LaunchConfig{SessionID: string(id), Prompt: prompt})
	if err != nil {
		return "", "", fmt.Errorf("prompt delivery: %w", err)
	}
	if strategy != ports.PromptDeliveryStdin {
		return prompt, "", nil
	}
	path, err := promptfile.Write(m.dataDir, id, promptfile.InitialPrompt, prompt)
	if err != nil {
		return "", "", fmt.Errorf("initial prompt file: %w", err)
	}
	return "", path, nil
}

// ReapOrphanedPromptFiles removes the prompt files of every session that has
// ended - or no longer exists - and reports how many it removed. It is the boot
// half of the reap in ReapSessionPanes: a session that ended while the daemon
// was down, or in a crash between its terminal write and the reap, would
// otherwise keep its instructions on disk forever. A live session's files are
// always left alone.
func (m *Manager) ReapOrphanedPromptFiles(ctx context.Context) (int, error) {
	owners, err := promptfile.Owners(m.dataDir)
	if err != nil {
		return 0, err
	}
	reaped := 0
	var errs []error
	for _, id := range owners {
		if err := ctx.Err(); err != nil {
			return reaped, err
		}
		rec, ok, err := m.store.GetSession(ctx, id)
		if err != nil {
			errs = append(errs, fmt.Errorf("read %s: %w", id, err))
			continue
		}
		if ok && !rec.IsTerminated {
			continue
		}
		if err := promptfile.Remove(m.dataDir, id); err != nil {
			errs = append(errs, err)
			continue
		}
		reaped++
	}
	return reaped, errors.Join(errs...)
}
