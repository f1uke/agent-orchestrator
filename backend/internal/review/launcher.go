package review

import (
	"context"
	"fmt"
	"os"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
)

// Launcher spawns, re-notifies, and probes a reviewer over a worker's worktree.
// It is the side of the engine that talks to the reviewer registry and runtime;
// the engine owns the orchestration and persistence.
type Launcher interface {
	// Spawn launches a fresh reviewer and returns the runtime handle id of the
	// live pane (stable per worker, reused across passes).
	Spawn(ctx context.Context, spec LaunchSpec) (handleID string, err error)
	// Notify asks an already-running reviewer pane to review a new commit.
	Notify(ctx context.Context, handleID string, spec LaunchSpec) error
	// Alive reports whether a reviewer pane is still running.
	Alive(ctx context.Context, handleID string) (bool, error)
	// Teardown closes a worker's reviewer pane (by its stable deterministic
	// handle), so a completed or orphaned reviewer's tmux session does not linger
	// as a keep-alive shell. Idempotent: a missing pane is a no-op.
	Teardown(ctx context.Context, workerID domain.SessionID) error
}

// LaunchSpec is the engine's request to (re)launch a reviewer for one pass.
type LaunchSpec struct {
	RunID         string
	WorkerID      domain.SessionID
	Harness       domain.ReviewerHarness
	WorkspacePath string
	PRURL         string
	TargetSHA     string
	ReviewQueue   []ports.ReviewTask
	ReviewIndex   int
	// ReviewerBase is the effective global reviewer base (override else default),
	// resolved by the Engine. Empty falls back to prompts.DefaultBase(reviewer).
	ReviewerBase string
	// ReviewerAddition is the project's per-project reviewer addition (may be "").
	ReviewerAddition string
	// AgentSessionID is the unique-per-launch native agent session id (see
	// ports.ReviewInvocation.AgentSessionID). Empty falls back to the handle id.
	AgentSessionID string
}

// reviewerRuntime is the runtime surface the launcher needs: create a pane,
// destroy a stale one, inject a message into a running pane, and probe liveness.
// The tmux runtime satisfies it. AgentAlive (agent-process liveness, distinct
// from IsAlive session-existence) is an optional capability probed via
// ports.AgentLivenessProber, so it is not part of this interface.
type reviewerRuntime interface {
	Create(ctx context.Context, cfg ports.RuntimeConfig) (ports.RuntimeHandle, error)
	Destroy(ctx context.Context, handle ports.RuntimeHandle) error
	IsAlive(ctx context.Context, handle ports.RuntimeHandle) (bool, error)
	SendMessage(ctx context.Context, handle ports.RuntimeHandle, message string) error
}

// agentLauncher resolves a reviewer adapter from the registry and drives the
// runtime. The reviewer reuses the worker's worktree (a fresh session worktree
// would branch off the default branch and so would not contain the PR changes).
type agentLauncher struct {
	reviewers ports.ReviewerResolver
	runtime   reviewerRuntime
}

type preLaunchReviewer interface {
	PreLaunch(ctx context.Context, inv ports.ReviewInvocation) error
}

// NewLauncher builds the production reviewer launcher.
func NewLauncher(reviewers ports.ReviewerResolver, runtime reviewerRuntime) Launcher {
	return &agentLauncher{reviewers: reviewers, runtime: runtime}
}

// reviewerHandleID is the stable runtime handle for a worker's reviewer pane, so
// one live reviewer is reused across passes.
func reviewerHandleID(workerID domain.SessionID) string {
	return "review-" + string(workerID)
}

// reviewerAgentSessionID is the UNIQUE-per-launch native agent session id for a
// reviewer, keyed on the batch's first run so each relaunch gets a distinct
// `claude --session-id` (never colliding with a prior pass's transcript). It is
// distinct from reviewerHandleID, which stays stable for live-pane reuse.
func reviewerAgentSessionID(workerID domain.SessionID, runID string) string {
	return "review-" + string(workerID) + "-" + runID
}

func (l *agentLauncher) invocation(spec LaunchSpec) ports.ReviewInvocation {
	prompt, systemPrompt := reviewTexts(spec)
	agentSessionID := spec.AgentSessionID
	if agentSessionID == "" {
		agentSessionID = reviewerHandleID(spec.WorkerID)
	}
	return ports.ReviewInvocation{
		ReviewerID:      reviewerHandleID(spec.WorkerID),
		AgentSessionID:  agentSessionID,
		RunID:           spec.RunID,
		WorkerSessionID: spec.WorkerID,
		PRURL:           spec.PRURL,
		TargetSHA:       spec.TargetSHA,
		ReviewQueue:     spec.ReviewQueue,
		ReviewIndex:     spec.ReviewIndex,
		WorkspacePath:   spec.WorkspacePath,
		Prompt:          prompt,
		SystemPrompt:    systemPrompt,
	}
}

func (l *agentLauncher) Spawn(ctx context.Context, spec LaunchSpec) (string, error) {
	reviewer, ok := l.reviewers.Reviewer(spec.Harness)
	if !ok {
		return "", fmt.Errorf("no reviewer adapter for harness %q", spec.Harness)
	}
	handleID := reviewerHandleID(spec.WorkerID)
	inv := l.invocation(spec)
	if pl, ok := reviewer.(preLaunchReviewer); ok {
		if err := pl.PreLaunch(ctx, inv); err != nil {
			return "", fmt.Errorf("reviewer pre-launch: %w", err)
		}
	}
	cmd, err := reviewer.ReviewCommand(ctx, inv)
	if err != nil {
		return "", fmt.Errorf("reviewer command: %w", err)
	}
	// Destroy any stale pane under this deterministic handle before creating a
	// fresh one. Spawn is only reached when the prior pane is not agent-alive, but
	// its tmux session may still linger (a keep-alive shell), which would fail
	// new-session with "duplicate session". Destroy is idempotent (a missing
	// session is a no-op), so this is safe on a first-ever spawn too.
	if err := l.runtime.Destroy(ctx, ports.RuntimeHandle{ID: handleID}); err != nil {
		return "", fmt.Errorf("reviewer destroy stale pane: %w", err)
	}
	handle, err := l.runtime.Create(ctx, ports.RuntimeConfig{
		SessionID:     domain.SessionID(handleID),
		WorkspacePath: spec.WorkspacePath,
		Argv:          cmd.Argv,
		Env:           pinnedEnv(cmd.Env),
	})
	if err != nil {
		return "", fmt.Errorf("reviewer runtime: %w", err)
	}
	return handle.ID, nil
}

// pinnedEnv returns the reviewer command's env with PATH pinned to the daemon's
// own directory, so the bare `ao` the reviewer runs (e.g. `ao review submit`)
// resolves to this daemon's CLI rather than a foreign `ao` first on the
// inherited PATH. Mirrors the worker-session pin in the session manager.
// Best-effort: an unpinnable daemon (not named "ao") keeps the inherited PATH.
func pinnedEnv(base map[string]string) map[string]string {
	path, err := sessionmanager.HookPATH(os.Executable, os.Getenv, base)
	if err != nil {
		return base
	}
	env := make(map[string]string, len(base)+1)
	for k, v := range base {
		env[k] = v
	}
	env["PATH"] = path
	return env
}

func (l *agentLauncher) Notify(ctx context.Context, handleID string, spec LaunchSpec) error {
	reviewer, ok := l.reviewers.Reviewer(spec.Harness)
	if !ok {
		return fmt.Errorf("no reviewer adapter for harness %q", spec.Harness)
	}
	msg, err := reviewer.ReviewMessage(ctx, l.invocation(spec))
	if err != nil {
		return fmt.Errorf("reviewer message: %w", err)
	}
	if err := l.runtime.SendMessage(ctx, ports.RuntimeHandle{ID: handleID}, msg); err != nil {
		return fmt.Errorf("notify reviewer: %w", err)
	}
	return nil
}

// Teardown destroys the worker's reviewer pane. Uses the same deterministic
// handle Spawn/Notify key on, and relies on Destroy being idempotent so tearing
// down a worker that never had a reviewer (or whose pane is already gone) is a
// harmless no-op.
func (l *agentLauncher) Teardown(ctx context.Context, workerID domain.SessionID) error {
	return l.runtime.Destroy(ctx, ports.RuntimeHandle{ID: reviewerHandleID(workerID)})
}

func (l *agentLauncher) Alive(ctx context.Context, handleID string) (bool, error) {
	if handleID == "" {
		return false, nil
	}
	// Prefer agent-process liveness: a reviewer whose claude-code exited leaves a
	// keep-alive shell that IsAlive (session-existence) still reports as alive,
	// which would make the engine type the review prompt into that shell. Fall
	// back to IsAlive only for a runtime without the AgentAlive capability.
	if prober, ok := l.runtime.(ports.AgentLivenessProber); ok {
		return prober.AgentAlive(ctx, ports.RuntimeHandle{ID: handleID})
	}
	return l.runtime.IsAlive(ctx, ports.RuntimeHandle{ID: handleID})
}
