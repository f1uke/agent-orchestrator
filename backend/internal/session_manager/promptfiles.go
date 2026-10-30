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

// devPreloadSkill is the skill a dev on a standard or deep task loads before
// anything else. The SessionStart hook only advises it, and models judged the
// advice away, so AO opens the first turn with the skill's command instead.
const devPreloadSkill = "pstack:poteto-mode"

// withPreloadedSkill returns the initial prompt a session is launched with: the
// brief itself, opened by devPreloadSkill's command when preloadedSkill finds
// one. The prefix is never persisted: a restore that resumes the conversation
// sends no prompt, so the skill already in its context is not loaded twice, and
// a fresh relaunch adds it again here.
func withPreloadedSkill(ctx context.Context, agent ports.Agent, kind domain.SessionKind, size domain.TaskSize, role domain.CrewRole, prompt string) string {
	if prompt == "" {
		return prompt
	}
	command, ok := preloadedSkill(ctx, agent, kind, size, role)
	if !ok {
		return prompt
	}
	return command + "\n\n" + prompt
}

// preloadedSkill is the command that loads devPreloadSkill, for a dev on a
// standard or deep task whose agent can load the installed skill from a leading
// command (ports.SkillInvoker). A qa, a mechanical task, an orchestrator and
// every other agent get none.
func preloadedSkill(ctx context.Context, agent ports.Agent, kind domain.SessionKind, size domain.TaskSize, role domain.CrewRole) (string, bool) {
	if kind != domain.KindWorker || role == domain.CrewRoleQA || size.WithDefault() == domain.TaskSizeMechanical {
		return "", false
	}
	invoker, ok := agent.(ports.SkillInvoker)
	if !ok {
		return "", false
	}
	return invoker.SkillCommand(ctx, devPreloadSkill)
}

// withCheckInSetup sets EnvCheckInSetup for a session whose check-in must show
// the preloaded skill's set-up: one that preloads it, on a project with the
// check-in gate on. The gate's prompt asks for the set-up and the Stop hook
// refuses a check-in without it, because prompt text alone never produced the
// todolist (scripts/playbook-adherence). Restore sets it the same way; the
// hook checks only a session's first turn, so a resumed session past its
// check-in is never refused.
func withCheckInSetup(ctx context.Context, env map[string]string, agent ports.Agent, kind domain.SessionKind, size domain.TaskSize, role domain.CrewRole, cfg domain.ProjectConfig) map[string]string {
	delete(env, EnvCheckInSetup)
	if !cfg.PauseBeforeImplementing {
		return env
	}
	if _, ok := preloadedSkill(ctx, agent, kind, size, role); ok {
		env[EnvCheckInSetup] = "1"
	}
	return env
}

// withPROpenGate sets EnvPROpenGate for every session that preloads
// devPreloadSkill, independent of the check-in gate.
func withPROpenGate(ctx context.Context, env map[string]string, agent ports.Agent, kind domain.SessionKind, size domain.TaskSize, role domain.CrewRole) map[string]string {
	delete(env, EnvPROpenGate)
	if _, ok := preloadedSkill(ctx, agent, kind, size, role); ok {
		env[EnvPROpenGate] = "1"
	}
	return env
}

// deliverInitialPrompt decides how the initial prompt reaches the agent. When
// the agent reads its first prompt from stdin (ports.PromptDeliveryStdin) and
// the runtime can connect a file there, the prompt goes into the session's
// private prompt file and comes back as stdinFile, leaving nothing for the
// command line: argv is what `pkill -f <word>` matches, and a task brief that
// names a command made the agent killable by anyone who pattern-kills it.
// Otherwise the prompt stays in the command (inCommand), as it always has.
func (m *Manager) deliverInitialPrompt(ctx context.Context, agent ports.Agent, t domain.Transport, id domain.SessionID, prompt string) (inCommand, stdinFile string, err error) {
	if prompt == "" || m.dataDir == "" {
		return prompt, "", nil
	}
	if t != domain.TransportChat {
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
