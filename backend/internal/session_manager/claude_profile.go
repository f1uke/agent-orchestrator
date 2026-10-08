package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/claudeprofile"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// ErrClaudeProfileUnsupported is a Claude profile switch on a session whose
// agent is not Claude Code.
var ErrClaudeProfileUnsupported = errors.New("session: Claude profiles apply only to claude-code sessions")

// ClaudeProfileRegistry resolves a Claude profile by name. *claudeprofile.Store
// satisfies it.
type ClaudeProfileRegistry interface {
	Lookup(name string) (claudeprofile.Profile, error)
}

func (m *Manager) spawnClaudeProfile(harness domain.AgentHarness, override string, project domain.ProjectRecord) (string, error) {
	if harness != domain.HarnessClaudeCode {
		return "", nil
	}
	name := strings.TrimSpace(override)
	if name == "" {
		name = project.Config.ClaudeProfile
	}
	p, err := m.claudeProfiles.Lookup(name)
	if err != nil {
		return "", err
	}
	return p.Name, nil
}

func (m *Manager) claudeSettingsFile(harness domain.AgentHarness, profile string) (string, error) {
	if harness != domain.HarnessClaudeCode {
		return "", nil
	}
	p, err := m.claudeProfiles.Lookup(profile)
	if err != nil {
		return "", err
	}
	return claudeprofile.SettingsPath(p)
}

func (m *Manager) todoClaudeProfile(override string) (string, error) {
	if strings.TrimSpace(override) == "" {
		return "", nil
	}
	p, err := m.claudeProfiles.Lookup(override)
	if err != nil {
		return "", err
	}
	return p.Name, nil
}

func withLaunchEnv(env map[string]string, agent ports.Agent) map[string]string {
	for k, v := range ports.LaunchEnvOf(agent) {
		env[k] = v
	}
	return env
}

// SetClaudeProfile switches a claude-code session to another profile and,
// when asked, restarts it onto it: now when the agent is idle or parked, once
// it is idle when it is mid-turn, and never for a session with no runtime,
// whose next launch applies the profile anyway.
func (m *Manager) SetClaudeProfile(ctx context.Context, id domain.SessionID, name string, restart bool) (domain.SessionRecord, domain.ClaudeProfileRestart, error) {
	rec, err := m.getRecord(ctx, id)
	if err != nil {
		return domain.SessionRecord{}, "", err
	}
	harness := rec.Harness
	if rec.IsTodo {
		project, err := m.loadProject(ctx, rec.ProjectID)
		if err != nil {
			return domain.SessionRecord{}, "", err
		}
		harness = effectiveHarness(harness, rec.Kind, project.Config)
	}
	if harness != domain.HarnessClaudeCode {
		return domain.SessionRecord{}, "", fmt.Errorf("set claude profile %s: %w (agent %q)", id, ErrClaudeProfileUnsupported, harness)
	}
	p, err := m.claudeProfiles.Lookup(name)
	if err != nil {
		return domain.SessionRecord{}, "", fmt.Errorf("set claude profile %s: %w", id, err)
	}
	if _, err := claudeprofile.SettingsPath(p); err != nil {
		return domain.SessionRecord{}, "", fmt.Errorf("set claude profile %s: %w", id, err)
	}
	if err := m.setClaudeProfile(ctx, id, p.Name); err != nil {
		return domain.SessionRecord{}, "", fmt.Errorf("set claude profile %s: %w", id, err)
	}

	outcome := domain.ClaudeProfileNextLaunch
	switch {
	case !restart || rec.IsTerminated || rec.IsSuspended || rec.IsTodo:
	case restartableNow(rec.Activity.State):
		if _, err := m.Restart(ctx, id); err != nil {
			return domain.SessionRecord{}, "", err
		}
		outcome = domain.ClaudeProfileRestarted
	default:
		if _, err := m.store.SetSessionRestartPending(ctx, id, true, m.clock()); err != nil {
			return domain.SessionRecord{}, "", fmt.Errorf("set claude profile %s: %w", id, err)
		}
		outcome = domain.ClaudeProfileRestartPending
	}
	rec, err = m.getRecord(ctx, id)
	return rec, outcome, err
}

func restartableNow(state domain.ActivityState) bool {
	return state == domain.ActivityIdle || state == domain.ActivityParked
}

// RestartPendingSessions runs the restarts profile switches left waiting for
// the agent to go idle. A session with no runtime just drops the flag (its next
// launch applies the profile), and a restart that fails drops it too, so one
// broken session is never retried on every tick.
func (m *Manager) RestartPendingSessions(ctx context.Context) error {
	recs, err := m.store.ListAllSessions(ctx)
	if err != nil {
		return fmt.Errorf("restart pending sessions: %w", err)
	}
	for _, rec := range recs {
		if !rec.RestartPending {
			continue
		}
		if rec.IsTerminated || rec.IsSuspended {
			m.clearRestartPending(ctx, rec.ID)
			continue
		}
		if !restartableNow(rec.Activity.State) {
			continue
		}
		if _, err := m.Restart(ctx, rec.ID); err != nil {
			m.logger.Warn("pending restart onto a new Claude profile failed; dropping it", "sessionID", rec.ID, "error", err)
			m.clearRestartPending(ctx, rec.ID)
		}
	}
	return nil
}

func (m *Manager) setClaudeProfile(ctx context.Context, id domain.SessionID, profile string) error {
	ok, err := m.store.SetSessionClaudeProfile(ctx, id, profile, m.clock())
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotFound
	}
	return nil
}

func (m *Manager) clearRestartPending(ctx context.Context, id domain.SessionID) {
	if _, err := m.store.SetSessionRestartPending(ctx, id, false, m.clock()); err != nil {
		m.logger.Warn("clear pending restart", "sessionID", id, "error", err)
	}
}
