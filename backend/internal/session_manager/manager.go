// Package sessionmanager drives internal session command operations over runtime,
// agent, workspace, storage, messenger, and lifecycle dependencies.
package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/knowledgestore"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
	"github.com/aoagents/agent-orchestrator/backend/internal/promptoverrides"
	"github.com/aoagents/agent-orchestrator/backend/internal/prompts"
	"github.com/aoagents/agent-orchestrator/backend/internal/skillassets"
)

// Sentinel errors returned by the Session Manager; callers match them with
// errors.Is.
var (
	ErrNotFound         = errors.New("session: not found")
	ErrNotRestorable    = errors.New("session: not restorable (not terminal)")
	ErrTerminated       = errors.New("session: terminated")
	ErrIncompleteHandle = errors.New("session: incomplete teardown handle")
	// ErrProjectNotResolvable means the spawn's project has no usable repo
	// (unregistered, archived, or missing a path). The API maps it to a 400.
	ErrProjectNotResolvable = errors.New("session: project repo not resolvable")
	// ErrUnknownHarness means the requested agent harness has no registered
	// adapter. The API maps it to a 400 so a typo'd `--harness` is a validation
	// error, not an opaque 500.
	ErrUnknownHarness = errors.New("session: unknown agent harness")
	// ErrMissingHarness means neither the spawn request nor the project's role
	// config selected an agent. Worker/orchestrator spawns must be explicit.
	ErrMissingHarness = errors.New("session: agent harness required")
	// ErrNotTodo means a start/spec-edit targeted a session that is not a
	// prepared TODO (already started, or never was one). The API maps it to 409.
	ErrNotTodo = errors.New("session: not a prepared TODO")
	// ErrNotResumable means a terminated session cannot be relaunched: its adapter
	// cannot natively resume it AND it has no prompt to fresh-launch from, and it is
	// not an orchestrator (orchestrators are promptless by design and relaunch fresh
	// with the system prompt only). Workers without a task and without a native
	// session id have nothing meaningful to restore.
	ErrNotResumable = errors.New("session: nothing to resume from")
	// ErrNotTerminal means a delete was requested for a session that is not
	// finished (neither merged nor terminated). The API maps it to 409.
	ErrNotTerminal = errors.New("session: not terminal")
)

// Env vars a spawned process reads to learn who it is.
const (
	EnvSessionID = "AO_SESSION_ID"
	EnvProjectID = "AO_PROJECT_ID"
	EnvIssueID   = "AO_ISSUE_ID"
	// EnvDataDir tells a spawned agent's AO hook commands where the store lives.
	EnvDataDir = "AO_DATA_DIR"
	// EnvRunFile tells a spawned agent's AO hook commands which daemon to
	// report to: the CLI resolves the daemon's port from the run file, so a
	// daemon running under a non-default AO_RUN_FILE must export it or its
	// sessions' hook callbacks silently post to whatever daemon owns the
	// default run file.
	EnvRunFile = "AO_RUN_FILE"
)

// hookBinaryName is the executable name the workspace hook commands invoke:
// every agent adapter installs a bare `ao hooks <agent> <event>`. The session
// PATH pin (hookPATH) only works when the daemon's own executable carries this
// name, since prepending its directory must change what `ao` resolves to.
const hookBinaryName = "ao"

type lifecycleRecorder interface {
	MarkSpawned(ctx context.Context, id domain.SessionID, metadata domain.SessionMetadata) error
	MarkTerminated(ctx context.Context, id domain.SessionID) error
}

type runtimeController interface {
	Create(ctx context.Context, cfg ports.RuntimeConfig) (ports.RuntimeHandle, error)
	Destroy(ctx context.Context, handle ports.RuntimeHandle) error
	// IsAlive reports whether the handle's runtime session still exists. Used by
	// Reconcile on boot to adopt crash-surviving sessions and reap leaked ones.
	IsAlive(ctx context.Context, handle ports.RuntimeHandle) (bool, error)
}

// Store is the persistence surface needed by the internal session Manager.
type Store interface {
	// GetProject loads a project row so spawn can resolve its per-project agent
	// config into the launch command. ok=false means the project is unknown.
	GetProject(ctx context.Context, id string) (domain.ProjectRecord, bool, error)
	ListWorkspaceRepos(ctx context.Context, projectID string) ([]domain.WorkspaceRepoRecord, error)
	CreateSession(ctx context.Context, rec domain.SessionRecord) (domain.SessionRecord, error)
	// UpdateSession rewrites a session row's mutable state. Used to persist edits
	// to a prepared TODO's spec before it is started.
	UpdateSession(ctx context.Context, rec domain.SessionRecord) error
	GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error)
	ListSessions(ctx context.Context, project domain.ProjectID) ([]domain.SessionRecord, error)
	ListAllSessions(ctx context.Context) ([]domain.SessionRecord, error)
	// DeleteSession removes a session row only if it is still in seed state
	// (no workspace, runtime handle, agent session id, or prompt; not
	// terminated). Returns deleted=true when removal happened; deleted=false
	// when the row had already progressed past seed state — preserving the
	// no-resurrection guarantee for live sessions.
	DeleteSession(ctx context.Context, id domain.SessionID) (bool, error)
	// UpsertSessionWorktree records or updates the worktree row for a session.
	// SaveAndTeardownAll writes the preserved_ref here (even when empty) as the
	// "shutdown-saved" marker before ForceDestroying the worktree.
	UpsertSessionWorktree(ctx context.Context, row domain.SessionWorktreeRecord) error
	// ListSessionWorktrees returns every worktree row for a session. RestoreAll
	// uses this to identify sessions saved by the last SaveAndTeardownAll: the
	// presence of any row is the marker; preserved_ref may be empty for clean
	// worktrees.
	ListSessionWorktrees(ctx context.Context, id domain.SessionID) ([]domain.SessionWorktreeRecord, error)
	// DeleteSessionWorktrees consumes stale shutdown-restore markers. Explicit
	// Kill and successful RestoreAll must remove these rows to prevent
	// resurrecting sessions the user intentionally terminated.
	DeleteSessionWorktrees(ctx context.Context, id domain.SessionID) error
	// PurgeSession hard-deletes a session row and its cascading dependents,
	// regardless of state. Callers gate on terminal status; the branch is kept.
	PurgeSession(ctx context.Context, id domain.SessionID) error
}

// Manager coordinates internal session spawn, restore, kill, and cleanup over
// the outbound ports. User-facing read-model assembly lives in the service package.
type Manager struct {
	runtime   runtimeController
	agents    ports.AgentResolver
	workspace ports.Workspace
	store     Store
	messenger ports.AgentMessenger
	lcm       lifecycleRecorder
	dataDir   string
	runFile   string
	clock     func() time.Time
	// idleCloseTTL is the inactivity window after which CloseIdleSessions closes
	// a session. Zero disables the sweep.
	idleCloseTTL time.Duration
	// lookPath is exec.LookPath in production; tests substitute a stub so
	// they don't need real binaries on PATH. Returns ports.ErrAgentBinaryNotFound
	// when the binary is missing so the sentinel propagates through toAPIError.
	lookPath func(string) (string, error)
	// executable resolves the daemon's own binary (os.Executable in
	// production); its directory is prepended to spawned sessions' PATH so the
	// workspace hook commands resolve back to this daemon. Tests inject a stub.
	executable func() (string, error)
	logger     *slog.Logger
	// genBranchName overrides generateBranchName in tests so the auto-naming
	// path can be exercised without executing a real agent CLI. Nil in
	// production (falls back to the real method).
	genBranchName func(ctx context.Context, agent ports.Agent, cfg ports.SpawnConfig, project domain.ProjectRecord) (string, bool)
	// spawnConfirmEnabled reports whether the orchestrator must confirm before
	// spawning. Nil means "confirm" (the safe default).
	spawnConfirmEnabled func() bool
	// promptOverrides returns the current global per-kind base overrides. Nil
	// means "no overrides" (built-in defaults) — the safe default for a bare
	// Manager in tests or wiring that omits the store.
	promptOverrides func() promptoverrides.Overrides
	// reviewerReaper closes a worker's reviewer pane when the worker is torn
	// down, so the reviewer (a child of the worker, keyed on the worker id) does
	// not linger. Injected by the daemon after the review service is built to
	// avoid an import cycle; nil in tests/wiring that omit it, in which case
	// teardown simply skips reviewer reaping.
	reviewerReaper func(context.Context, domain.SessionID) error
	// smokeEvidencePurger hard-deletes a session's on-disk smoke-test evidence
	// tree when the session is purged (the DB rows cascade separately). Injected
	// by the daemon after the smoke service is built, same as reviewerReaper; nil
	// in tests/wiring that omit it, in which case purge simply skips it.
	smokeEvidencePurger func(context.Context, domain.SessionID) error
}

// Deps are the collaborators a Session Manager needs; New wires them together.
type Deps struct {
	Runtime   runtimeController
	Agents    ports.AgentResolver
	Workspace ports.Workspace
	Store     Store
	Messenger ports.AgentMessenger
	Lifecycle lifecycleRecorder
	// DataDir is exported to spawned agents as AO_DATA_DIR so their hook
	// commands can open the same store.
	DataDir string
	// RunFile is exported to spawned agents as AO_RUN_FILE so their hook
	// commands resolve this daemon rather than whichever daemon owns the
	// default run file. Empty omits the export (see EnvRunFile).
	RunFile string
	Clock   func() time.Time
	// IdleCloseTTL is the inactivity window for CloseIdleSessions. 0 disables it.
	IdleCloseTTL time.Duration
	// LookPath overrides exec.LookPath for the pre-launch agent-binary check.
	// Production wiring leaves this nil and the manager defaults to
	// exec.LookPath; tests inject a stub so they need not seed real binaries.
	LookPath func(string) (string, error)
	// Executable overrides os.Executable for the session PATH pin (see
	// hookPATH). Production wiring leaves this nil; tests inject a stub so they
	// control what the test binary appears to be.
	Executable func() (string, error)
	// Logger receives spawn-time diagnostics (e.g. when the session PATH
	// cannot be pinned to the daemon binary). Nil defaults to slog.Default().
	Logger *slog.Logger
	// SpawnConfirmEnabled reports whether the orchestrator must present a
	// confirmation summary and wait for approval before running `ao spawn`.
	// Nil defaults to enabled (confirm) — the safe default.
	SpawnConfirmEnabled func() bool
	// PromptOverrides returns the current global per-kind base overrides, read at
	// spawn/restore so an edit takes effect on the next (re)launch. Nil defaults
	// to built-in defaults.
	PromptOverrides func() promptoverrides.Overrides
}

// New builds a Session Manager from its dependencies, defaulting the clock to
// time.Now when Deps.Clock is nil.
func New(d Deps) *Manager {
	m := &Manager{
		runtime:             d.Runtime,
		agents:              d.Agents,
		workspace:           d.Workspace,
		store:               d.Store,
		messenger:           d.Messenger,
		lcm:                 d.Lifecycle,
		dataDir:             d.DataDir,
		runFile:             d.RunFile,
		clock:               d.Clock,
		idleCloseTTL:        d.IdleCloseTTL,
		lookPath:            d.LookPath,
		executable:          d.Executable,
		logger:              d.Logger,
		spawnConfirmEnabled: d.SpawnConfirmEnabled,
		promptOverrides:     d.PromptOverrides,
	}
	if m.clock == nil {
		// UTC so spawn-stamped CreatedAt/UpdatedAt match every other session
		// write (rename, activity) — all of which use time.Now().UTC(). A local
		// default produced mixed-timezone timestamps in `ao session get`.
		m.clock = func() time.Time { return time.Now().UTC() }
	}
	if m.lookPath == nil {
		m.lookPath = exec.LookPath
	}
	if m.executable == nil {
		m.executable = os.Executable
	}
	if m.logger == nil {
		m.logger = slog.Default()
	}
	return m
}

// SetReviewerReaper wires the hook that closes a worker's reviewer pane on
// teardown. The daemon calls this after the review service is constructed (the
// session manager is built first, so it cannot receive the hook via Deps without
// an import cycle). A manager with no reaper set simply skips reviewer reaping.
func (m *Manager) SetReviewerReaper(fn func(context.Context, domain.SessionID) error) {
	m.reviewerReaper = fn
}

// reapReviewer best-effort closes the worker's reviewer pane. Teardown of the
// worker must never fail because its reviewer could not be reaped, so any error
// is logged and swallowed. A nil reaper (unwired) is a no-op.
func (m *Manager) reapReviewer(ctx context.Context, id domain.SessionID) {
	if m.reviewerReaper == nil {
		return
	}
	if err := m.reviewerReaper(ctx, id); err != nil {
		m.logger.Warn("reviewer pane teardown failed", "sessionID", id, "error", err)
	}
}

// SetSmokeEvidencePurger wires the hook that hard-deletes a session's smoke-test
// evidence blobs on purge. Wired by the daemon after the smoke service exists,
// mirroring SetReviewerReaper. A manager with no purger set skips it.
func (m *Manager) SetSmokeEvidencePurger(fn func(context.Context, domain.SessionID) error) {
	m.smokeEvidencePurger = fn
}

// purgeSmokeEvidence best-effort removes the session's on-disk evidence tree.
// Purge of the session must never fail because its evidence blobs could not be
// removed, so any error is logged and swallowed. A nil purger is a no-op.
func (m *Manager) purgeSmokeEvidence(ctx context.Context, id domain.SessionID) {
	if m.smokeEvidencePurger == nil {
		return
	}
	if err := m.smokeEvidencePurger(ctx, id); err != nil {
		m.logger.Warn("smoke evidence purge failed", "sessionID", id, "error", err)
	}
}

// preserveWorkerKnowledge is the belt-and-suspenders safety net behind the
// worker prompt (which asks agents to write plans/proposals to the knowledge
// store directly): on worker teardown, BEFORE the worktree is removed, it copies
// any stray planning docs left in the worktree into the project's private
// knowledge store so they survive the teardown. It is best-effort and must never
// fail teardown — every error is logged and swallowed. Only workers are scanned
// (orchestrators keep no per-branch worktree artifacts); a session with no data
// dir or workspace path is a no-op.
func (m *Manager) preserveWorkerKnowledge(rec domain.SessionRecord) {
	if rec.Kind != domain.KindWorker || m.dataDir == "" || rec.Metadata.WorkspacePath == "" {
		return
	}
	dest := knowledgestore.PlansDir(m.dataDir, string(rec.ProjectID))
	written, err := knowledgestore.PreserveStrayDocs(rec.Metadata.WorkspacePath, rec.Metadata.Branch, dest)
	if err != nil {
		m.logger.Warn("preserve worker knowledge: partial failure", "sessionID", rec.ID, "error", err)
	}
	if len(written) > 0 {
		m.logger.Info("preserved worker planning docs to knowledge store", "sessionID", rec.ID, "count", len(written), "dest", dest)
	}
}

// Spawn creates the session row (which assigns the "{project}-{n}" id), then the
// workspace and runtime, then reports completion to the LCM. If workspace
// materialization fails the still-seed row is deleted outright; a later failure
// parks the row as terminated and rolls back what was built.
func (m *Manager) Spawn(ctx context.Context, cfg ports.SpawnConfig) (domain.SessionRecord, error) {
	project, err := m.loadProject(ctx, cfg.ProjectID)
	if err != nil {
		return domain.SessionRecord{}, fmt.Errorf("spawn: %w", err)
	}
	// A per-project role override picks the harness when the spawn names none,
	// so a project can default workers to one agent and orchestrators to another.
	cfg.Harness = effectiveHarness(cfg.Harness, cfg.Kind, project.Config)
	if cfg.Harness == "" {
		return domain.SessionRecord{}, fmt.Errorf("spawn: %w: configure project %s.agent or pass --harness", ErrMissingHarness, roleConfigName(cfg.Kind))
	}

	// Reject an unknown harness before any durable state is created. Doing this
	// after CreateSession would leave a terminated orphan row and waste a
	// worktree on a spawn that can never launch.
	if _, ok := m.agents.Agent(cfg.Harness); !ok {
		return domain.SessionRecord{}, fmt.Errorf("spawn: %w: %q", ErrUnknownHarness, cfg.Harness)
	}

	if err := m.validateRuntimePrerequisites(); err != nil {
		return domain.SessionRecord{}, fmt.Errorf("spawn: %w", err)
	}

	prompt, systemPrompt, err := m.buildSpawnTexts(ctx, cfg)
	if err != nil {
		return domain.SessionRecord{}, fmt.Errorf("spawn: prompt: %w", err)
	}

	rec, err := m.store.CreateSession(ctx, seedRecord(cfg, m.clock()))
	if err != nil {
		return domain.SessionRecord{}, fmt.Errorf("spawn: create: %w", err)
	}
	// Materialize the freshly-seeded row: a hard failure deletes the seed
	// outright (keepTodo=false) so it never lingers as a terminated orphan.
	return m.materialize(ctx, project, cfg, rec.ID, prompt, systemPrompt, false)
}

// materialize runs the branch → worktree → provision → agent-prep → runtime →
// MarkSpawned half of a spawn against an already-persisted seed row `id`. On any
// failure it tears down whatever partial workspace/runtime it created; the seed
// row is then either deleted (keepTodo=false, a fresh spawn) or left intact as a
// TODO for the user to retry (keepTodo=true, a TODO Start).
func (m *Manager) materialize(ctx context.Context, project domain.ProjectRecord, cfg ports.SpawnConfig, id domain.SessionID, prompt, systemPrompt string, keepTodo bool) (domain.SessionRecord, error) {
	disposeSeed := func() {
		if keepTodo {
			m.logger.Warn("start todo: materialize failed, keeping task in TODO for retry", "sessionID", id)
			return
		}
		m.rollbackSpawnSeedRow(ctx, id)
	}

	branch := cfg.Branch
	autoNamed := false
	if branch == "" {
		if cfg.AutoNameBranch && cfg.Kind != domain.KindOrchestrator {
			if agent, ok := m.agents.Agent(cfg.Harness); ok {
				gen := m.genBranchName
				if gen == nil {
					gen = m.generateBranchName
				}
				if name, ok := gen(ctx, agent, cfg, project); ok {
					// Apply the project's branch convention (custom prefix) to the
					// AI-named branch before de-duping, so an omitted --branch still
					// lands on-convention.
					name = applyConventionPrefix(name, project.Config.GitConvention)
					branch = ensureUniqueBranch(m.existingBranchNames(ctx, project), name)
					autoNamed = branch != ""
				}
			}
		}
		if branch == "" {
			// defaultSpawnBranch adds the workspace-project default (ao/<id>) while
			// still delegating to main-fluke's defaultSessionBranch (ao/<id>/root)
			// for regular projects.
			branch = defaultSpawnBranch(id, cfg.Kind, sessionPrefix(project), project.Kind.WithDefault())
		}
	}
	ws, workspaceProject, err := m.createSessionWorkspace(ctx, project, cfg, id, branch)
	if err != nil && autoNamed {
		// An AI-generated branch name can collide with an existing branch the
		// de-dup listing missed (e.g. a transient git error emptied the set).
		// Auto-naming must never fail a spawn the default name would succeed at,
		// so retry once with the session-unique default before giving up.
		m.logger.Warn("spawn: auto-named branch rejected, retrying with default name",
			"sessionID", id, "branch", branch, "error", err)
		branch = defaultSpawnBranch(id, cfg.Kind, sessionPrefix(project), project.Kind.WithDefault())
		ws, workspaceProject, err = m.createSessionWorkspace(ctx, project, cfg, id, branch)
	}
	if err != nil {
		// Nothing observable exists yet — no worktree, no runtime — so the seed
		// row is deleted outright instead of accumulating as a terminated orphan
		// in session lists (e.g. when gitworktree refuses the branch). A TODO
		// Start keeps the row so the task can be retried.
		disposeSeed()
		return domain.SessionRecord{}, fmt.Errorf("spawn %s: workspace: %w", id, err)
	}

	// Per-project workspace provisioning: symlink shared files, then run any
	// post-create commands (e.g. `pnpm install`) before the agent launches.
	if err := m.provisionWorkspace(ctx, project, ws.Path); err != nil {
		m.destroySpawnWorkspace(ctx, ws, workspaceProject)
		disposeSeed()
		return domain.SessionRecord{}, fmt.Errorf("spawn %s: provision: %w", id, err)
	}

	agent, ok := m.agents.Agent(cfg.Harness)
	if !ok {
		m.destroySpawnWorkspace(ctx, ws, workspaceProject)
		disposeSeed()
		return domain.SessionRecord{}, fmt.Errorf("spawn %s: no agent adapter for harness %q", id, cfg.Harness)
	}
	agentConfig := effectiveAgentConfig(cfg.Kind, project.Config)
	if err := m.prepareWorkspace(ctx, agent, id, ws.Path, systemPrompt, agentConfig); err != nil {
		m.destroySpawnWorkspace(ctx, ws, workspaceProject)
		disposeSeed()
		return domain.SessionRecord{}, fmt.Errorf("spawn %s: %w", id, err)
	}
	argv, err := agent.GetLaunchCommand(ctx, ports.LaunchConfig{
		SessionID:     string(id),
		WorkspacePath: ws.Path,
		Kind:          cfg.Kind,
		Prompt:        prompt,
		SystemPrompt:  systemPrompt,
		IssueID:       string(cfg.IssueID),
		Config:        agentConfig,
		Permissions:   agentConfig.Permissions,
	})
	if err != nil {
		m.destroySpawnWorkspace(ctx, ws, workspaceProject)
		disposeSeed()
		return domain.SessionRecord{}, fmt.Errorf("spawn %s: launch command: %w", id, err)
	}
	// Pre-flight: confirm argv[0] actually exists on PATH (or as an absolute
	// path the adapter returned) BEFORE handing the launch to the runtime.
	// tmux happily creates a session+pane around a missing command, so an
	// unresolved binary would leak through as a "live" session that never ran.
	if err := m.validateAgentBinary(argv); err != nil {
		m.destroySpawnWorkspace(ctx, ws, workspaceProject)
		disposeSeed()
		return domain.SessionRecord{}, fmt.Errorf("spawn %s: %w", id, err)
	}
	handle, err := m.runtime.Create(ctx, ports.RuntimeConfig{
		SessionID:     id,
		ProjectID:     cfg.ProjectID,
		Branch:        ws.Branch,
		WorkspacePath: ws.Path,
		Argv:          argv,
		Env:           m.runtimeEnv(id, cfg.ProjectID, cfg.IssueID, project.Config.Env),
	})
	if err != nil {
		m.destroySpawnWorkspace(ctx, ws, workspaceProject)
		disposeSeed()
		return domain.SessionRecord{}, fmt.Errorf("spawn %s: runtime: %w", id, err)
	}

	metadata := domain.SessionMetadata{Branch: ws.Branch, WorkspacePath: ws.Path, RuntimeHandleID: handle.ID, Prompt: prompt}
	if err := m.lcm.MarkSpawned(ctx, id, metadata); err != nil {
		_ = m.runtime.Destroy(ctx, handle)
		m.destroySpawnWorkspace(ctx, ws, workspaceProject)
		// Runtime came up but the completing DB write failed. A fresh spawn
		// terminates the row; a TODO Start leaves it queued (still is_todo) for
		// a retry rather than stranding it as terminated.
		if keepTodo {
			m.logger.Warn("start todo: mark spawned failed, keeping task in TODO for retry", "sessionID", id)
		} else {
			m.markSpawnFailedTerminated(ctx, id)
		}
		return domain.SessionRecord{}, fmt.Errorf("spawn %s: completed: %w", id, err)
	}
	return m.getRecord(ctx, id)
}

// PrepareTodo persists a session in the TODO/prepared state: the spec is saved
// (project, harness, base/new branch, prTarget, prompt, createdBy) but NO
// branch, worktree, runtime or agent process is created. StartTodo materializes
// it later. Unlike Spawn, an empty harness is allowed (it resolves to the
// project default at Start); a non-empty but unknown harness is still rejected
// so bad data never persists.
func (m *Manager) PrepareTodo(ctx context.Context, cfg ports.SpawnConfig) (domain.SessionRecord, error) {
	if _, err := m.loadProject(ctx, cfg.ProjectID); err != nil {
		return domain.SessionRecord{}, fmt.Errorf("prepare todo: %w", err)
	}
	if cfg.Harness != "" {
		if _, ok := m.agents.Agent(cfg.Harness); !ok {
			return domain.SessionRecord{}, fmt.Errorf("prepare todo: %w: %q", ErrUnknownHarness, cfg.Harness)
		}
	}
	rec, err := m.store.CreateSession(ctx, todoSeedRecord(cfg, m.clock()))
	if err != nil {
		return domain.SessionRecord{}, fmt.Errorf("prepare todo: create: %w", err)
	}
	return m.getRecord(ctx, rec.ID)
}

// StartTodo materializes a prepared TODO in place: it replays the stored spec
// through the normal spawn materialization on the SAME row, so the id carries
// through into the live session. A materialize failure keeps the row queued in
// TODO for a retry rather than deleting or terminating it.
func (m *Manager) StartTodo(ctx context.Context, id domain.SessionID) (domain.SessionRecord, error) {
	row, ok, err := m.store.GetSession(ctx, id)
	if err != nil {
		return domain.SessionRecord{}, fmt.Errorf("start todo %s: %w", id, err)
	}
	if !ok {
		return domain.SessionRecord{}, ErrNotFound
	}
	if !row.IsTodo {
		return domain.SessionRecord{}, ErrNotTodo
	}
	project, err := m.loadProject(ctx, row.ProjectID)
	if err != nil {
		return domain.SessionRecord{}, fmt.Errorf("start todo %s: %w", id, err)
	}
	cfg := ports.SpawnConfig{
		ProjectID:      row.ProjectID,
		IssueID:        row.IssueID,
		Kind:           row.Kind,
		Harness:        row.Harness,
		Branch:         row.Metadata.Branch,
		AutoNameBranch: row.AutoNameBranch,
		BaseBranch:     row.BaseBranch,
		Prompt:         row.Metadata.Prompt,
		DisplayName:    row.DisplayName,
		PRTarget:       row.PRTarget,
		CreatedBy:      row.CreatedBy,
	}
	cfg.Harness = effectiveHarness(cfg.Harness, cfg.Kind, project.Config)
	if cfg.Harness == "" {
		return domain.SessionRecord{}, fmt.Errorf("start todo %s: %w: configure project %s.agent or set the task's agent", id, ErrMissingHarness, roleConfigName(cfg.Kind))
	}
	if _, ok := m.agents.Agent(cfg.Harness); !ok {
		return domain.SessionRecord{}, fmt.Errorf("start todo %s: %w: %q", id, ErrUnknownHarness, cfg.Harness)
	}
	if err := m.validateRuntimePrerequisites(); err != nil {
		return domain.SessionRecord{}, fmt.Errorf("start todo %s: %w", id, err)
	}
	prompt, systemPrompt, err := m.buildSpawnTexts(ctx, cfg)
	if err != nil {
		return domain.SessionRecord{}, fmt.Errorf("start todo %s: prompt: %w", id, err)
	}
	return m.materialize(ctx, project, cfg, id, prompt, systemPrompt, true)
}

// UpdateTodoSpec persists edits to a prepared TODO's spec (name, agent, base/new
// branch, PR target, prompt). It is rejected once the task has started
// (ErrNotTodo). An unknown non-empty harness is rejected; harness resolution to
// the project default is deferred to Start.
func (m *Manager) UpdateTodoSpec(ctx context.Context, id domain.SessionID, patch ports.TodoSpecPatch) (domain.SessionRecord, error) {
	row, ok, err := m.store.GetSession(ctx, id)
	if err != nil {
		return domain.SessionRecord{}, fmt.Errorf("update todo %s: %w", id, err)
	}
	if !ok {
		return domain.SessionRecord{}, ErrNotFound
	}
	if !row.IsTodo {
		return domain.SessionRecord{}, ErrNotTodo
	}
	if patch.DisplayName != nil {
		row.DisplayName = *patch.DisplayName
	}
	if patch.Harness != nil {
		if *patch.Harness != "" {
			if _, ok := m.agents.Agent(*patch.Harness); !ok {
				return domain.SessionRecord{}, fmt.Errorf("update todo %s: %w: %q", id, ErrUnknownHarness, *patch.Harness)
			}
		}
		row.Harness = *patch.Harness
	}
	if patch.Branch != nil {
		row.Metadata.Branch = *patch.Branch
	}
	if patch.BaseBranch != nil {
		row.BaseBranch = *patch.BaseBranch
	}
	if patch.PRTarget != nil {
		row.PRTarget = *patch.PRTarget
	}
	if patch.Prompt != nil {
		row.Metadata.Prompt = *patch.Prompt
	}
	if patch.AutoNameBranch != nil {
		row.AutoNameBranch = *patch.AutoNameBranch
	}
	row.UpdatedAt = m.clock()
	if err := m.store.UpdateSession(ctx, row); err != nil {
		return domain.SessionRecord{}, fmt.Errorf("update todo %s: %w", id, err)
	}
	return m.getRecord(ctx, id)
}

// loadProject loads the project record so spawn can resolve its per-project
// config (harness/agent overrides, env, branch, rules, provisioning). A missing
// project yields a zero record rather than an error: the project may be
// unregistered yet still have live sessions, and an empty config simply means
// every field falls back to its default.
func (m *Manager) loadProject(ctx context.Context, projectID domain.ProjectID) (domain.ProjectRecord, error) {
	row, ok, err := m.store.GetProject(ctx, string(projectID))
	if err != nil {
		return domain.ProjectRecord{}, fmt.Errorf("load project: %w", err)
	}
	if !ok {
		return domain.ProjectRecord{}, nil
	}
	return row, nil
}

func (m *Manager) createSessionWorkspace(ctx context.Context, project domain.ProjectRecord, cfg ports.SpawnConfig, id domain.SessionID, branch string) (ports.WorkspaceInfo, *ports.WorkspaceProjectInfo, error) {
	// Honor an explicit --base override (main-fluke behavior); empty falls back
	// to the project's configured default branch.
	baseBranch := cfg.BaseBranch
	if baseBranch == "" {
		baseBranch = project.Config.WithDefaults().DefaultBranch
	}
	if project.Kind.WithDefault() != domain.ProjectKindWorkspace {
		ws, err := m.workspace.Create(ctx, ports.WorkspaceConfig{
			ProjectID:     cfg.ProjectID,
			SessionID:     id,
			Kind:          cfg.Kind,
			SessionPrefix: sessionPrefix(project),
			Branch:        branch,
			BaseBranch:    baseBranch,
		})
		return ws, nil, err
	}
	workspaceProject, ok := m.workspace.(ports.WorkspaceProject)
	if !ok {
		return ports.WorkspaceInfo{}, nil, errors.New("workspace project materialization is not supported by workspace adapter")
	}
	repos, err := m.store.ListWorkspaceRepos(ctx, project.ID)
	if err != nil {
		return ports.WorkspaceInfo{}, nil, err
	}
	childRepos := make([]ports.WorkspaceProjectRepoConfig, 0, len(repos))
	for _, repo := range repos {
		childRepos = append(childRepos, ports.WorkspaceProjectRepoConfig{
			Name:         repo.Name,
			RelativePath: repo.RelativePath,
			RepoPath:     filepath.Join(project.Path, filepath.FromSlash(repo.RelativePath)),
		})
	}
	info, err := workspaceProject.CreateWorkspaceProject(ctx, ports.WorkspaceProjectConfig{
		ProjectID:     cfg.ProjectID,
		SessionID:     id,
		Kind:          cfg.Kind,
		SessionPrefix: sessionPrefix(project),
		Branch:        branch,
		RootRepoPath:  project.Path,
		BaseBranch:    baseBranch,
		Repos:         childRepos,
	})
	if err != nil {
		return ports.WorkspaceInfo{}, nil, err
	}
	for _, wt := range info.Worktrees {
		if err := m.store.UpsertSessionWorktree(ctx, domain.SessionWorktreeRecord{
			SessionID:    id,
			RepoName:     wt.RepoName,
			Branch:       wt.Branch,
			BaseSHA:      wt.BaseSHA,
			WorktreePath: wt.Path,
			State:        "active",
		}); err != nil {
			_ = workspaceProject.DestroyWorkspaceProject(ctx, info)
			return ports.WorkspaceInfo{}, nil, fmt.Errorf("record workspace worktree %q: %w", wt.RepoName, err)
		}
	}
	return info.Root, &info, nil
}

func (m *Manager) destroySpawnWorkspace(ctx context.Context, ws ports.WorkspaceInfo, workspaceProject *ports.WorkspaceProjectInfo) {
	if workspaceProject != nil {
		if adapter, ok := m.workspace.(ports.WorkspaceProject); ok {
			_ = adapter.DestroyWorkspaceProject(ctx, *workspaceProject)
			return
		}
	}
	_ = m.workspace.Destroy(ctx, ws)
}

// effectiveHarness resolves the harness for a spawn: an explicit harness wins;
// otherwise the project's role override for the session kind applies. Empty is
// invalid for new worker/orchestrator launches and is rejected by Spawn.
func effectiveHarness(explicit domain.AgentHarness, kind domain.SessionKind, cfg domain.ProjectConfig) domain.AgentHarness {
	if explicit != "" {
		return explicit
	}
	if role := roleOverride(kind, cfg).Harness; role != "" {
		return role
	}
	return ""
}

func roleConfigName(kind domain.SessionKind) string {
	if kind == domain.KindOrchestrator {
		return "orchestrator"
	}
	return "worker"
}

// effectiveAgentConfig merges the role override's agent config over the
// project's base agent config; set override fields win.
func effectiveAgentConfig(kind domain.SessionKind, cfg domain.ProjectConfig) ports.AgentConfig {
	merged := cfg.AgentConfig
	override := roleOverride(kind, cfg).AgentConfig
	if override.Model != "" {
		merged.Model = override.Model
	}
	if override.Permissions != "" {
		merged.Permissions = override.Permissions
	}
	return merged
}

func roleOverride(kind domain.SessionKind, cfg domain.ProjectConfig) domain.RoleOverride {
	if kind == domain.KindOrchestrator {
		return cfg.Orchestrator
	}
	return cfg.Worker
}

// sessionPrefix returns the display prefix for a project: the explicit
// SessionPrefix when set, otherwise the first 12 characters of the project ID.
func sessionPrefix(project domain.ProjectRecord) string {
	if p := strings.TrimSpace(project.Config.SessionPrefix); p != "" {
		return p
	}
	if len(project.ID) <= 12 {
		return project.ID
	}
	return project.ID[:12]
}

// markSpawnFailedTerminated best-effort parks an orphaned spawn as terminated.
// A phantom half-spawned row is worse than a terminal one; we only delete the
// row when nothing observable has landed yet (seed state) via rollbackSpawn or
// rollbackSpawnSeedRow.
func (m *Manager) markSpawnFailedTerminated(ctx context.Context, id domain.SessionID) {
	_ = m.lcm.MarkTerminated(ctx, id)
}

// rollbackSpawnSeedRow best-effort removes the row of a spawn that failed
// before anything observable (worktree, runtime) was built, so failed spawns
// don't accumulate terminated rows in session lists. DeleteSession only removes
// rows still in seed state; if the row has progressed or the delete itself
// fails, fall back to parking it terminated so a phantom row never looks live.
func (m *Manager) rollbackSpawnSeedRow(ctx context.Context, id domain.SessionID) {
	if deleted, err := m.store.DeleteSession(ctx, id); err == nil && deleted {
		return
	}
	m.markSpawnFailedTerminated(ctx, id)
}

// rollbackSpawn deletes a session row when it is still in seed state — used
// when an out-of-band step that happens AFTER `Spawn` returns (e.g. PR claim
// over HTTP) has failed and the caller wants the partially-spawned session
// gone without leaving a terminated orphan visible under `--include-terminated`.
//
// If the row has progressed past seed state (workspace exists, runtime created,
// etc.), DeleteSession is a no-op and rollbackSpawn falls back to a Kill so the
// runtime/workspace are torn down. Returns (deleted, killed):
//   - deleted=true: the row was a seed row and has been removed
//   - killed=true:  the row had spawn output and was torn down + terminated
//   - both false:   the row was already terminated or absent — benign no-op
func (m *Manager) rollbackSpawn(ctx context.Context, id domain.SessionID) (deleted, killed bool, err error) {
	deleted, err = m.store.DeleteSession(ctx, id)
	if err != nil {
		return false, false, fmt.Errorf("rollback %s: %w", id, err)
	}
	if deleted {
		return true, false, nil
	}
	killed, err = m.Kill(ctx, id)
	if err != nil {
		return false, false, err
	}
	return false, killed, nil
}

// RollbackSpawn is the public surface of rollbackSpawn for service-layer callers.
func (m *Manager) RollbackSpawn(ctx context.Context, id domain.SessionID) (deleted, killed bool, err error) {
	return m.rollbackSpawn(ctx, id)
}

// Kill tears down the runtime and workspace, then records terminal intent with
// the LCM. A workspace teardown refused by the worktree-remove safety
// (uncommitted work) is never forced: Kill succeeds with freed=false,
// signalling the workspace was preserved and the session is left retryable.
//
// A session whose runtime handle or workspace path is missing (e.g. spawn
// failed partway, handle lost after a crash) is still terminated after the
// available destroy steps are skipped so it can be cleaned up from the
// dashboard.
func (m *Manager) Kill(ctx context.Context, id domain.SessionID) (bool, error) {
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil {
		return false, fmt.Errorf("kill %s: %w", id, err)
	}
	if !ok {
		return false, nil // already gone: benign race
	}
	handle := runtimeHandle(rec.Metadata)
	ws := workspaceInfo(rec)

	var workspaceProjectRows []ports.WorkspaceRepoInfo
	workspaceProject := false
	if rows, ok, rowErr := m.workspaceProjectRows(ctx, rec); rowErr != nil {
		return false, fmt.Errorf("kill %s: workspace rows: %w", id, rowErr)
	} else if ok {
		workspaceProjectRows = rows
		workspaceProject = true
	}

	if handle.ID != "" {
		if err := m.runtime.Destroy(ctx, handle); err != nil {
			return false, fmt.Errorf("kill %s: runtime: %w", id, err)
		}
	}
	// Rescue any stray planning docs left in the worktree into the private
	// knowledge store before the (possibly refused) workspace teardown, so plans
	// and proposals survive even if the agent did not save them there directly.
	m.preserveWorkerKnowledge(rec)
	// The worker is terminating: close its reviewer pane too. Done before the
	// (possibly refused) workspace teardown so a preserved dirty worktree still
	// reaps the reviewer.
	m.reapReviewer(ctx, id)
	freed := false
	if workspaceProject {
		cleaned, err := m.destroyWorkspaceProjectRows(ctx, workspaceProjectRows)
		if err != nil {
			if errors.Is(err, ports.ErrWorkspaceDirty) {
				return false, nil
			}
			return false, fmt.Errorf("kill %s: workspace: %w", id, err)
		}
		freed = cleaned
	} else if ws.Path != "" {
		if err := m.workspace.Destroy(ctx, ws); err != nil {
			if errors.Is(err, ports.ErrWorkspaceDirty) {
				return false, nil
			}
			return false, fmt.Errorf("kill %s: workspace: %w", id, err)
		}
		freed = true
	}
	// Clear the restore marker so the next boot's RestoreAll cannot resurrect a
	// killed session (#2319). For workspace projects this must happen after
	// teardown reads the rows; dirty-preserved rows return above and are left as
	// non-restorable inventory.
	if err := m.store.DeleteSessionWorktrees(ctx, id); err != nil {
		m.logger.Warn("kill: delete restore marker failed", "sessionID", id, "error", err)
	}
	if err := m.lcm.MarkTerminated(ctx, id); err != nil {
		return false, fmt.Errorf("kill %s: %w", id, err)
	}
	return freed, nil
}

// PurgeSession tears the session down like Kill, then hard-deletes its row and
// cascading dependents. A dirty worktree is refused (ErrWorkspaceDirty) unless
// force is set, in which case it is force-destroyed. The git branch is never
// removed — only the runtime, the worktree directory, and DB rows are. Callers
// (the session service) must gate this on terminal status.
func (m *Manager) PurgeSession(ctx context.Context, id domain.SessionID, force bool) error {
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil {
		return fmt.Errorf("purge %s: %w", id, err)
	}
	if !ok {
		return nil // already gone: benign race
	}
	handle := runtimeHandle(rec.Metadata)
	ws := workspaceInfo(rec)

	if !rec.IsTerminated {
		if err := m.lcm.MarkTerminated(ctx, id); err != nil {
			return fmt.Errorf("purge %s: %w", id, err)
		}
	}
	if handle.ID != "" {
		if err := m.runtime.Destroy(ctx, handle); err != nil {
			return fmt.Errorf("purge %s: runtime: %w", id, err)
		}
	}
	if ws.Path != "" {
		if err := m.workspace.Destroy(ctx, ws); err != nil {
			if errors.Is(err, ports.ErrWorkspaceDirty) {
				if !force {
					return fmt.Errorf("purge %s: %w", id, ports.ErrWorkspaceDirty)
				}
				if ferr := m.workspace.ForceDestroy(ctx, ws); ferr != nil {
					return fmt.Errorf("purge %s: force destroy: %w", id, ferr)
				}
			} else {
				return fmt.Errorf("purge %s: workspace: %w", id, err)
			}
		}
	}
	// Close the worker's reviewer pane before the row (and its cascading review
	// rows) are hard-deleted, so a delete never orphans the reviewer's tmux.
	m.reapReviewer(ctx, id)
	// Hard-delete the session's smoke-test evidence blobs; the DB rows cascade
	// with the session row below.
	m.purgeSmokeEvidence(ctx, id)
	return m.store.PurgeSession(ctx, id)
}

// RetireForReplacement terminates a live orchestrator and releases its branch
// for a replacement session. Unlike Kill, this captures uncommitted work before
// force-removing the worktree, so a dirty canonical orchestrator worktree does
// not block the replacement from claiming the canonical branch.
//
// This deliberately does not write a session_worktrees row: those rows are
// boot-restore markers, and a replaced orchestrator must stay terminated.
func (m *Manager) RetireForReplacement(ctx context.Context, id domain.SessionID) error {
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil {
		return fmt.Errorf("retire replacement %s: %w", id, err)
	}
	if !ok || rec.IsTerminated {
		return nil
	}
	if rec.Metadata.WorkspacePath == "" || rec.Metadata.Branch == "" {
		if err := m.store.DeleteSessionWorktrees(ctx, rec.ID); err != nil {
			return fmt.Errorf("retire replacement %s: clear restore markers: %w", id, err)
		}
		handle := runtimeHandle(rec.Metadata)
		if handle.ID != "" {
			if err := m.runtime.Destroy(ctx, handle); err != nil {
				return fmt.Errorf("retire replacement %s: runtime: %w", id, err)
			}
		}
		if err := m.lcm.MarkTerminated(ctx, id); err != nil {
			return fmt.Errorf("retire replacement %s: mark terminated: %w", id, err)
		}
		return nil
	}
	if rows, ok, rowErr := m.workspaceProjectRows(ctx, rec); rowErr != nil {
		return fmt.Errorf("retire replacement %s: workspace rows: %w", id, rowErr)
	} else if ok {
		return m.retireWorkspaceProjectForReplacement(ctx, rec, rows)
	}

	ws := workspaceInfo(rec)
	if _, err := m.workspace.StashUncommitted(ctx, ws); err != nil {
		return fmt.Errorf("retire replacement %s: stash: %w", id, err)
	}
	if err := m.store.DeleteSessionWorktrees(ctx, rec.ID); err != nil {
		return fmt.Errorf("retire replacement %s: clear restore markers: %w", id, err)
	}
	handle := runtimeHandle(rec.Metadata)
	if handle.ID != "" {
		if err := m.runtime.Destroy(ctx, handle); err != nil {
			return fmt.Errorf("retire replacement %s: runtime: %w", id, err)
		}
	}
	if err := m.workspace.ForceDestroy(ctx, ws); err != nil {
		return fmt.Errorf("retire replacement %s: force destroy: %w", id, err)
	}
	if err := m.lcm.MarkTerminated(ctx, rec.ID); err != nil {
		return fmt.Errorf("retire replacement %s: mark terminated: %w", id, err)
	}
	return nil
}

func (m *Manager) retireWorkspaceProjectForReplacement(ctx context.Context, rec domain.SessionRecord, rows []ports.WorkspaceRepoInfo) error {
	for _, row := range rows {
		if _, err := m.workspace.StashUncommitted(ctx, workspaceInfoFromRepoInfo(row)); err != nil {
			return fmt.Errorf("retire replacement %s repo %s: stash: %w", rec.ID, row.RepoName, err)
		}
	}
	handle := runtimeHandle(rec.Metadata)
	if handle.ID != "" {
		if err := m.runtime.Destroy(ctx, handle); err != nil {
			return fmt.Errorf("retire replacement %s: runtime: %w", rec.ID, err)
		}
	}
	for i := len(rows) - 1; i >= 0; i-- {
		if err := m.workspace.ForceDestroy(ctx, workspaceInfoFromRepoInfo(rows[i])); err != nil {
			return fmt.Errorf("retire replacement %s repo %s: force destroy: %w", rec.ID, rows[i].RepoName, err)
		}
	}
	if err := m.store.DeleteSessionWorktrees(ctx, rec.ID); err != nil {
		return fmt.Errorf("retire replacement %s: clear restore markers: %w", rec.ID, err)
	}
	if err := m.lcm.MarkTerminated(ctx, rec.ID); err != nil {
		return fmt.Errorf("retire replacement %s: mark terminated: %w", rec.ID, err)
	}
	return nil
}

// Restore relaunches a torn-down session in its workspace. The fallible I/O runs
// before any durable session write, so a failure never resurrects the row or destroys
// the worktree (it may hold the agent's prior work).
func (m *Manager) Restore(ctx context.Context, id domain.SessionID) (domain.SessionRecord, error) {
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil {
		return domain.SessionRecord{}, fmt.Errorf("restore %s: %w", id, err)
	}
	if !ok {
		return domain.SessionRecord{}, fmt.Errorf("restore %s: %w", id, ErrNotFound)
	}
	if !rec.IsTerminated {
		return domain.SessionRecord{}, fmt.Errorf("restore %s: %w", id, ErrNotRestorable)
	}
	meta := rec.Metadata
	// Mirror Kill's incomplete-handle guard: a session whose spawn failed before
	// the workspace landed has neither WorkspacePath nor Branch, and there is
	// nothing meaningful to restore from. Surface this as a typed 409 instead of
	// letting workspace.Restore fail with an opaque wrapped error.
	if meta.WorkspacePath == "" || meta.Branch == "" {
		return domain.SessionRecord{}, fmt.Errorf("restore %s: %w", id, ErrIncompleteHandle)
	}
	// A session can be terminated in the store while its runtime is still alive:
	// e.g. its PR merged and the reaper/reclaim marked it done, but the agent
	// process is still attached. Relaunching would then collide with the existing
	// runtime (`tmux new-session` fails "duplicate session", surfacing as an opaque
	// 500). If the runtime is definitively alive, adopt it instead — clear the
	// terminal state with no relaunch and no teardown of the running agent. Only a
	// genuinely dead runtime falls through to the relaunch path below. A probe error
	// is not proof of death, so it also falls through rather than adopting.
	if handleID := strings.TrimSpace(meta.RuntimeHandleID); handleID != "" {
		if alive, err := m.runtime.IsAlive(ctx, ports.RuntimeHandle{ID: handleID}); err == nil && alive {
			if err := m.lcm.MarkSpawned(ctx, id, meta); err != nil {
				return domain.SessionRecord{}, fmt.Errorf("restore %s: adopt live runtime: %w", id, err)
			}
			return m.getRecord(ctx, id)
		}
	}
	// Resumability is decided inside restoreArgv, not here. A promptless session
	// can still be fully resumable when the harness pins a deterministic session id
	// (Claude Code) AND the agent still holds that conversation for this worktree;
	// the adapter reports not-resumable when the transcript is gone (e.g. a recycled
	// session number whose id collides with a purged session's transcript), so the
	// session relaunches fresh instead of resuming into a dead shell. restoreArgv
	// returns ErrNotResumable only for a promptless, unresumable non-orchestrator (a
	// worker with no task and no conversation to resume). Orchestrators are promptless
	// by design and relaunch with the system prompt only when they cannot resume.

	project, err := m.loadProject(ctx, rec.ProjectID)
	if err != nil {
		return domain.SessionRecord{}, fmt.Errorf("restore %s: %w", id, err)
	}
	ws, err := m.restoreSessionWorkspace(ctx, project, rec)
	if err != nil {
		return domain.SessionRecord{}, fmt.Errorf("restore %s: workspace: %w", id, err)
	}
	return m.relaunchRestoredSession(ctx, rec, project, ws)
}

func (m *Manager) relaunchRestoredSession(ctx context.Context, rec domain.SessionRecord, project domain.ProjectRecord, ws ports.WorkspaceInfo) (domain.SessionRecord, error) {
	agent, ok := m.agents.Agent(rec.Harness)
	if !ok {
		return domain.SessionRecord{}, fmt.Errorf("restore %s: no agent adapter for harness %q", rec.ID, rec.Harness)
	}
	// The system prompt is derived, not persisted: recompute it so a restored
	// session keeps its standing instructions across the relaunch.
	systemPrompt, err := m.buildSystemPrompt(ctx, rec.Kind, rec.ProjectID)
	if err != nil {
		return domain.SessionRecord{}, fmt.Errorf("restore %s: system prompt: %w", rec.ID, err)
	}
	// Restore re-applies the project's resolved agent config so a configured
	// model/permissions carry across a restore, matching fresh spawn.
	agentConfig := effectiveAgentConfig(rec.Kind, project.Config)
	if err := m.prepareWorkspace(ctx, agent, rec.ID, ws.Path, systemPrompt, agentConfig); err != nil {
		return domain.SessionRecord{}, fmt.Errorf("restore %s: %w", rec.ID, err)
	}
	argv, err := restoreArgv(ctx, agent, rec.ID, ws.Path, rec.Metadata, systemPrompt, agentConfig, rec.Kind)
	if err != nil {
		return domain.SessionRecord{}, fmt.Errorf("restore %s: %w", rec.ID, err)
	}
	handle, err := m.runtime.Create(ctx, ports.RuntimeConfig{
		SessionID:     rec.ID,
		ProjectID:     rec.ProjectID,
		Branch:        ws.Branch,
		WorkspacePath: ws.Path,
		Argv:          argv,
		Env:           m.runtimeEnv(rec.ID, rec.ProjectID, rec.IssueID, project.Config.Env),
	})
	if err != nil {
		return domain.SessionRecord{}, fmt.Errorf("restore %s: runtime: %w", rec.ID, err)
	}
	metadata := domain.SessionMetadata{Branch: ws.Branch, WorkspacePath: ws.Path, RuntimeHandleID: handle.ID, AgentSessionID: rec.Metadata.AgentSessionID, Prompt: rec.Metadata.Prompt}
	if err := m.lcm.MarkSpawned(ctx, rec.ID, metadata); err != nil {
		_ = m.runtime.Destroy(ctx, handle)
		return domain.SessionRecord{}, fmt.Errorf("restore %s: completed: %w", rec.ID, err)
	}
	return m.getRecord(ctx, rec.ID)
}

// Restart tears a session down and relaunches it in place, keeping the same
// session id and native agent transcript. It exists so a running agent can pick
// up a freshly recomputed system prompt (e.g. after the orchestrator/worker
// prompt changed) without losing its conversation: the Restore leg recomputes
// the system prompt and resumes via the harness's native --resume.
//
// A live session is killed first (Kill preserves a dirty worktree and recreates
// a clean one from the branch on restore, so no committed or uncommitted work is
// lost); an already-terminated session skips the kill and restores directly.
func (m *Manager) Restart(ctx context.Context, id domain.SessionID) (domain.SessionRecord, error) {
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil {
		return domain.SessionRecord{}, fmt.Errorf("restart %s: %w", id, err)
	}
	if !ok {
		return domain.SessionRecord{}, fmt.Errorf("restart %s: %w", id, ErrNotFound)
	}
	if !rec.IsTerminated {
		if _, err := m.Kill(ctx, id); err != nil {
			return domain.SessionRecord{}, err
		}
	}
	return m.Restore(ctx, id)
}

func (m *Manager) getRecord(ctx context.Context, id domain.SessionID) (domain.SessionRecord, error) {
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil {
		return domain.SessionRecord{}, fmt.Errorf("get %s: %w", id, err)
	}
	if !ok {
		return domain.SessionRecord{}, fmt.Errorf("get %s: %w", id, ErrNotFound)
	}
	return rec, nil
}

// SaveAndTeardownAll captures uncommitted work and tears down every live
// session that has a workspace path. It is the shutdown path for the daemon:
// each session's uncommitted work is stashed into a preserve ref, the ref is
// written to session_worktrees (the "shutdown-saved" marker) BEFORE the
// worktree is force-removed. The DB write is committed before the worktree is
// destroyed so a crash between the two leaves the ref in place and the row
// present; RestoreAll will replay both.
//
// Failures on individual sessions are logged and do not abort the loop.
// ForceDestroy is never called if capture or the DB write did not succeed.
func (m *Manager) SaveAndTeardownAll(ctx context.Context) error {
	recs, err := m.store.ListAllSessions(ctx)
	if err != nil {
		return fmt.Errorf("save-teardown-all: list sessions: %w", err)
	}
	for _, rec := range recs {
		if rec.IsTerminated {
			continue
		}
		if rec.Metadata.WorkspacePath == "" || rec.Metadata.Branch == "" {
			continue
		}
		if err := m.saveAndTeardownOne(ctx, rec, true); err != nil {
			m.logger.Error("save-teardown-all: session failed, skipping", "sessionID", rec.ID, "error", err)
		}
	}
	return nil
}

// saveAndTeardownOne runs the capture-then-destroy sequence for a single
// session. The DB write (UpsertSessionWorktree) is committed before
// ForceDestroy; if either capture or the DB write fails, ForceDestroy is
// not called.
func (m *Manager) saveAndTeardownOne(ctx context.Context, rec domain.SessionRecord, destroyRuntime bool) error {
	// Rescue stray planning docs into the private knowledge store before anything
	// stashes or removes the worktree, so a worker's plans/proposals survive the
	// shutdown/crash teardown path too (best-effort; never fails teardown).
	m.preserveWorkerKnowledge(rec)
	if rows, ok, err := m.workspaceProjectRows(ctx, rec); err != nil {
		return fmt.Errorf("save %s: workspace rows: %w", rec.ID, err)
	} else if ok {
		return m.saveAndTeardownWorkspaceProject(ctx, rec, rows, destroyRuntime)
	}

	// 1. Capture uncommitted work (ref may be "" for clean worktrees).
	ws := workspaceInfo(rec)
	ref, err := m.workspace.StashUncommitted(ctx, ws)
	if err != nil {
		return fmt.Errorf("save %s: stash: %w", rec.ID, err)
	}

	// 2. Write the shutdown-saved marker to the DB. The row's presence (even
	// with an empty preserved_ref) is what RestoreAll uses to identify sessions
	// saved by this run. This MUST be committed before ForceDestroy.
	row := domain.SessionWorktreeRecord{
		SessionID:    rec.ID,
		RepoName:     domain.RootWorkspaceRepoName,
		Branch:       rec.Metadata.Branch,
		WorktreePath: rec.Metadata.WorkspacePath,
		PreservedRef: ref,
		State:        "removed",
	}
	if err := m.store.UpsertSessionWorktree(ctx, row); err != nil {
		return fmt.Errorf("save %s: upsert worktree row: %w", rec.ID, err)
	}

	// 3. Mark terminal via the LCM (same path Kill uses).
	if err := m.lcm.MarkTerminated(ctx, rec.ID); err != nil {
		return fmt.Errorf("save %s: mark terminated: %w", rec.ID, err)
	}

	// 4. Runtime teardown (best-effort; same pattern as Kill).
	handle := runtimeHandle(rec.Metadata)
	if destroyRuntime && handle.ID != "" {
		if err := m.runtime.Destroy(ctx, handle); err != nil {
			m.logger.Warn("save-teardown-all: runtime destroy failed", "sessionID", rec.ID, "error", err)
		}
	}

	// 5. Force-remove the worktree (safe: work is captured in step 1 and the
	// DB write in step 2 is already committed).
	if err := m.workspace.ForceDestroy(ctx, ws); err != nil {
		m.logger.Warn("save-teardown-all: force destroy failed", "sessionID", rec.ID, "error", err)
	}
	return nil
}

// reconcileLive handles a single non-terminated session on boot. If its runtime
// session is still alive (tmux is the persistence layer, so it survives a daemon
// crash) we adopt it: a no-op, the agent keeps running. If the runtime is gone,
// the agent died with the daemon, so we save-and-tear-down to the SAME end state
// a graceful shutdown produces: capture uncommitted work into a preserve ref,
// record the session_worktrees restore marker, mark terminated, and remove the
// worktree. RestoreAll (which Reconcile runs immediately after) then relaunches
// it on this same boot, resuming history. Crash recovery thus matches graceful
// restart instead of silently abandoning the session.
//
// Exception: a gone-runtime session that is ALSO idle past the auto-close TTL
// is closed the way CloseIdleSessions would (marked terminated, worktree left
// in place, no restore marker) instead of captured and relaunched — a session
// the user let go idle must stay closed across a reboot, not come back.
//
// If the work capture fails we mark terminated WITHOUT a marker and leave the
// worktree intact: better to skip the relaunch than to tear down un-preserved
// work or relaunch onto an inconsistent worktree.
func (m *Manager) reconcileLive(ctx context.Context, rec domain.SessionRecord) error {
	if rec.Metadata.WorkspacePath == "" || rec.Metadata.Branch == "" {
		return nil
	}
	handle := runtimeHandle(rec.Metadata)
	if handle.ID != "" {
		alive, err := m.runtime.IsAlive(ctx, handle)
		if err != nil {
			// A failed probe is not proof of death: leave the session as-is.
			return fmt.Errorf("reconcile %s: probe: %w", rec.ID, err)
		}
		if alive {
			return nil // adopt: the session survived the crash.
		}
	}
	// Runtime is gone. If the session is idle past the auto-close TTL, close it
	// the way CloseIdleSessions would instead of capturing + relaunching: mark it
	// terminated and leave the worktree in place (its uncommitted work sits on
	// disk for on-demand Restore) with no restore marker, so this boot does not
	// relaunch a session the user let go idle past the window.
	if m.idleCloseTTL > 0 && m.clock().Sub(idleReference(rec)) > m.idleCloseTTL {
		if err := m.lcm.MarkTerminated(ctx, rec.ID); err != nil {
			return fmt.Errorf("reconcile %s: mark terminated (idle): %w", rec.ID, err)
		}
		return nil
	}
	// Not idle: capture uncommitted work, write the restore marker, and tear the
	// worktree down so RestoreAll can relaunch it. saveAndTeardownOne is the
	// workspace-project-aware capture path (equivalent to the previous inline
	// stash+marker+destroy for regular projects, and multi-repo for workspaces).
	if err := m.saveAndTeardownOne(ctx, rec, false); err != nil {
		m.logger.Warn("reconcile: save-and-teardown failed; terminating without restore marker", "sessionID", rec.ID, "error", err)
		if mErr := m.lcm.MarkTerminated(ctx, rec.ID); mErr != nil {
			return fmt.Errorf("reconcile %s: mark terminated: %w", rec.ID, mErr)
		}
	}
	return nil
}

// Reconcile is the boot-time consistency pass. It replaces the bare RestoreAll
// call so that however the previous daemon died (clean shutdown, SIGKILL, or
// crash), live reality matches the DB:
//
//  1. Live pass: for each non-terminated session, adopt it if its runtime
//     survived, else capture work and mark terminated (reconcileLive).
//  2. Idle sweep: close sessions idle past the configured TTL — destroy their
//     tmux and mark them terminated while KEEPING the worktree, so a normally
//     terminated session's tmux survives app reopen and only ages out on
//     inactivity (CloseIdleSessions). Replaces the old immediate reap.
//  3. Restore pass: relaunch shutdown-saved sessions (existing RestoreAll).
//
// Best-effort throughout: a per-session failure is logged and never aborts the
// pass or blocks boot.
func (m *Manager) Reconcile(ctx context.Context) error {
	recs, err := m.store.ListAllSessions(ctx)
	if err != nil {
		return fmt.Errorf("reconcile: list sessions: %w", err)
	}
	for _, rec := range recs {
		if rec.IsTerminated {
			continue
		}
		if err := m.reconcileLive(ctx, rec); err != nil {
			m.logger.Error("reconcile: live pass failed, skipping", "sessionID", rec.ID, "error", err)
		}
	}
	if err := m.CloseIdleSessions(ctx); err != nil {
		m.logger.Error("reconcile: idle sweep failed", "error", err)
	}
	return m.RestoreAll(ctx)
}

// CloseIdleSessions auto-closes every session idle longer than the configured
// TTL: it destroys the session's runtime (tmux) and marks it terminated while
// KEEPING its worktree on disk, so the session stays restorable via the existing
// Restore path. A non-positive TTL disables the sweep. Best-effort: a per-session
// failure is logged and never aborts the pass.
func (m *Manager) CloseIdleSessions(ctx context.Context) error {
	if m.idleCloseTTL <= 0 {
		return nil
	}
	recs, err := m.store.ListAllSessions(ctx)
	if err != nil {
		return fmt.Errorf("close idle: list sessions: %w", err)
	}
	// A project's orchestrators SHARE one runtime handle (the branch-mirrored
	// tmux name), so several terminated records and the one live session collide
	// on the same handle. Collect the handles a live (non-terminated) session
	// still owns so closeIdle never reaps a tmux that a live session is using —
	// the app-reopen bug where an ancient idle terminated sibling destroyed the
	// live orchestrator's tmux.
	liveHandles := make(map[string]bool)
	for _, rec := range recs {
		if rec.IsTerminated {
			continue
		}
		if h := runtimeHandle(rec.Metadata); h.ID != "" {
			liveHandles[h.ID] = true
		}
	}
	now := m.clock()
	for _, rec := range recs {
		if now.Sub(idleReference(rec)) <= m.idleCloseTTL {
			continue
		}
		if err := m.closeIdle(ctx, rec, liveHandles); err != nil {
			m.logger.Error("close idle: failed, skipping", "sessionID", rec.ID, "error", err)
		}
	}
	return nil
}

// closeIdle destroys a session's runtime (if any survives) and marks it
// terminated, deliberately keeping the worktree so the session stays restorable.
// liveHandles is the set of runtime handles a live (non-terminated) session still
// owns; because handles are shared across a project's orchestrators, a terminated
// record must not reap a tmux that a live session is using.
func (m *Manager) closeIdle(ctx context.Context, rec domain.SessionRecord, liveHandles map[string]bool) error {
	handle := runtimeHandle(rec.Metadata)
	if rec.IsTerminated {
		// The record's runtime was torn down when it ended; a tmux still alive
		// under its shared handle belongs to a live session unless none owns it.
		// Reap a genuinely-leaked tmux (no live owner); otherwise leave it — it is
		// the live session's tmux and destroying it would kill it on reopen.
		if !liveHandles[handle.ID] {
			return m.reapRuntimeIfAlive(ctx, rec.ID, handle)
		}
		return nil
	}
	// Live session idle past the TTL: destroy its own runtime, keep the worktree.
	if err := m.reapRuntimeIfAlive(ctx, rec.ID, handle); err != nil {
		return err
	}
	// Clear any shutdown-restore marker so boot never auto-relaunches it: the
	// user restores on demand. The worktree is deliberately kept on disk.
	if err := m.store.DeleteSessionWorktrees(ctx, rec.ID); err != nil {
		return fmt.Errorf("close idle %s: clear restore marker: %w", rec.ID, err)
	}
	if err := m.lcm.MarkTerminated(ctx, rec.ID); err != nil {
		return fmt.Errorf("close idle %s: mark terminated: %w", rec.ID, err)
	}
	return nil
}

// agentAlive reports whether a live AGENT process is attached to handle, seeing
// past a keep-alive shell that IsAlive (session existence) cannot. It is the
// reap-safety gate: an inferred/stale-terminated session whose pane still runs a
// live agent (a late SessionEnd, or a user who resumed into it) must not be
// reaped. Returns (false, nil) for a runtime without the AgentAlive capability so
// callers keep their prior behavior; a probe error is surfaced (never treated as
// death).
func (m *Manager) agentAlive(ctx context.Context, handle ports.RuntimeHandle) (bool, error) {
	if handle.ID == "" {
		return false, nil
	}
	prober, ok := m.runtime.(ports.AgentLivenessProber)
	if !ok {
		return false, nil
	}
	return prober.AgentAlive(ctx, handle)
}

// reapRuntimeIfAlive destroys the runtime under handle if it is still alive. A
// blank handle or a dead runtime is a no-op. Used by the idle sweep to tear down
// tmux without disturbing the worktree.
func (m *Manager) reapRuntimeIfAlive(ctx context.Context, sessionID domain.SessionID, handle ports.RuntimeHandle) error {
	if handle.ID == "" {
		return nil
	}
	alive, err := m.runtime.IsAlive(ctx, handle)
	if err != nil {
		return fmt.Errorf("close idle %s: probe: %w", sessionID, err)
	}
	if alive {
		if err := m.runtime.Destroy(ctx, handle); err != nil {
			return fmt.Errorf("close idle %s: destroy: %w", sessionID, err)
		}
	}
	return nil
}

// idleReference is the timestamp idle time is measured from: the last activity
// signal, or the session's creation time when no signal has arrived yet (so a
// freshly-spawned, not-yet-reporting session is not closed immediately).
func idleReference(rec domain.SessionRecord) time.Time {
	if !rec.Activity.LastActivityAt.IsZero() {
		return rec.Activity.LastActivityAt
	}
	return rec.CreatedAt
}

// RestoreAll relaunches every terminated session that was saved by the last
// SaveAndTeardownAll. The "shutdown-saved" marker is the presence of a
// session_worktrees row for the session; sessions the user killed before
// shutdown have no such row and are left terminated.
//
// For each saved session:
//  1. Ensure the worktree exists via workspace.Restore.
//  2. If a preserve ref is recorded, replay it via ApplyPreserved; on conflict
//     log and continue (still relaunch the agent, never delete the ref).
//  3. Relaunch via the existing Restore method.
//
// Failures on individual sessions are logged and do not abort the loop.
func (m *Manager) RestoreAll(ctx context.Context) error {
	recs, err := m.store.ListAllSessions(ctx)
	if err != nil {
		return fmt.Errorf("restore-all: list sessions: %w", err)
	}
	for _, rec := range recs {
		if !rec.IsTerminated {
			continue
		}
		// Check the shutdown-saved marker: is there a session_worktrees row?
		rows, err := m.store.ListSessionWorktrees(ctx, rec.ID)
		if err != nil {
			m.logger.Error("restore-all: list worktrees failed", "sessionID", rec.ID, "error", err)
			continue
		}
		if len(rows) == 0 {
			// No marker: this session was killed by the user before shutdown.
			continue
		}
		rows = restorableWorktreeRows(rows)
		if len(rows) == 0 {
			continue
		}

		// Step 1: ensure the worktree exists. workspace.Restore re-creates it
		// if it was removed by SaveAndTeardownAll.
		project, err := m.loadProject(ctx, rec.ProjectID)
		if err != nil {
			m.logger.Error("restore-all: load project failed", "sessionID", rec.ID, "error", err)
			continue
		}
		var ws ports.WorkspaceInfo
		restoredWorkspaceProject := project.Kind.WithDefault() == domain.ProjectKindWorkspace
		var projectRows []ports.WorkspaceRepoInfo
		if restoredWorkspaceProject {
			var rowErr error
			projectRows, rowErr = m.workspaceProjectRestoreRowsFromMarkers(ctx, project, rec, rows)
			if rowErr != nil {
				m.logger.Error("restore-all: workspace rows failed", "sessionID", rec.ID, "error", rowErr)
				continue
			}
			root, restoreErr := m.restoreWorkspaceProjectRows(ctx, projectRows)
			if restoreErr != nil {
				m.logger.Error("restore-all: workspace project restore failed", "sessionID", rec.ID, "error", restoreErr)
				continue
			}
			ws = workspaceInfoFromRepoInfo(root)
		} else {
			var restoreErr error
			ws, restoreErr = m.workspace.Restore(ctx, ports.WorkspaceConfig{
				ProjectID:     rec.ProjectID,
				SessionID:     rec.ID,
				Kind:          rec.Kind,
				SessionPrefix: sessionPrefix(project),
				Branch:        rec.Metadata.Branch,
			})
			if restoreErr != nil {
				m.logger.Error("restore-all: workspace restore failed", "sessionID", rec.ID, "error", restoreErr)
				continue
			}
		}
		if ws.Path == "" {
			m.logger.Error("restore-all: workspace restore failed", "sessionID", rec.ID, "error", "empty restored root path")
			continue
		}

		// Step 2: replay preserve ref when one was recorded.
		if restoredWorkspaceProject {
			m.applyWorkspaceProjectPreserved(ctx, projectRows)
		} else {
			var preserveRef string
			for _, r := range rows {
				if r.PreservedRef != "" {
					preserveRef = r.PreservedRef
					break
				}
			}
			if preserveRef != "" {
				if applyErr := m.workspace.ApplyPreserved(ctx, ws, preserveRef); applyErr != nil {
					if errors.Is(applyErr, ports.ErrPreservedConflict) {
						m.logger.Warn("restore-all: apply preserved produced conflicts; agent relaunched with conflict markers in place",
							"sessionID", rec.ID, "ref", preserveRef, "error", applyErr)
					} else {
						m.logger.Error("restore-all: apply preserved failed", "sessionID", rec.ID, "error", applyErr)
					}
					// Continue: always relaunch even on conflict (never delete the ref here).
				}
			}
		}

		// Step 3: relaunch the agent in the restored workspace.
		if _, err := m.relaunchRestoredSession(ctx, rec, project, ws); err != nil {
			// A promptless, unresumable worker is intentionally left terminated
			// (ErrNotResumable): expected, not an operational failure, so log it
			// quietly rather than as an error.
			if errors.Is(err, ErrNotResumable) {
				m.logger.Warn("restore-all: session left terminated (nothing to resume)", "sessionID", rec.ID)
			} else {
				m.logger.Error("restore-all: relaunch failed", "sessionID", rec.ID, "error", err)
			}
			continue
		}

		// One-shot: drop the consumed marker so it never outlives one restart
		// (#2319). A still-live session re-acquires it at the next quit.
		if restoredWorkspaceProject {
			for _, row := range projectRows {
				if err := m.upsertWorkspaceProjectRowState(ctx, row, "active"); err != nil {
					m.logger.Warn("restore-all: marking workspace repo active failed", "sessionID", rec.ID, "repo", row.RepoName, "error", err)
				}
			}
		} else {
			if err := m.markSessionWorktreesActive(ctx, rows); err != nil {
				m.logger.Warn("restore-all: marking worktrees active failed", "sessionID", rec.ID, "error", err)
			}
			if err := m.store.DeleteSessionWorktrees(ctx, rec.ID); err != nil {
				m.logger.Warn("restore-all: delete restore marker failed", "sessionID", rec.ID, "error", err)
			}
		}
	}
	return nil
}

func restorableWorktreeRows(rows []domain.SessionWorktreeRecord) []domain.SessionWorktreeRecord {
	out := make([]domain.SessionWorktreeRecord, 0, len(rows))
	for _, row := range rows {
		if row.State == "removed" || legacyRestorableWorktreeRow(row) {
			out = append(out, row)
		}
	}
	return out
}

func legacyRestorableWorktreeRow(row domain.SessionWorktreeRecord) bool {
	return row.State == "" && (row.PreservedRef != "" || row.RepoName == domain.RootWorkspaceRepoName)
}

func (m *Manager) markSessionWorktreesActive(ctx context.Context, rows []domain.SessionWorktreeRecord) error {
	for _, row := range rows {
		row.State = "active"
		row.PreservedRef = ""
		if err := m.store.UpsertSessionWorktree(ctx, row); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) restoreSessionWorkspace(ctx context.Context, project domain.ProjectRecord, rec domain.SessionRecord) (ports.WorkspaceInfo, error) {
	if project.Kind.WithDefault() != domain.ProjectKindWorkspace {
		return m.workspace.Restore(ctx, ports.WorkspaceConfig{
			ProjectID:     rec.ProjectID,
			SessionID:     rec.ID,
			Kind:          rec.Kind,
			SessionPrefix: sessionPrefix(project),
			Branch:        rec.Metadata.Branch,
		})
	}
	rows, err := m.workspaceProjectRestoreRows(ctx, project, rec)
	if err != nil {
		return ports.WorkspaceInfo{}, err
	}
	root, err := m.restoreWorkspaceProjectRows(ctx, rows)
	if err != nil {
		return ports.WorkspaceInfo{}, err
	}
	for _, row := range rows {
		if err := m.upsertWorkspaceProjectRowState(ctx, row, "active"); err != nil {
			return ports.WorkspaceInfo{}, fmt.Errorf("mark repo %s active: %w", row.RepoName, err)
		}
	}
	return workspaceInfoFromRepoInfo(root), nil
}

func (m *Manager) workspaceProjectRestoreRows(ctx context.Context, project domain.ProjectRecord, rec domain.SessionRecord) ([]ports.WorkspaceRepoInfo, error) {
	rows, err := m.store.ListSessionWorktrees(ctx, rec.ID)
	if err != nil {
		return nil, err
	}
	return m.workspaceProjectRestoreRowsFromMarkers(ctx, project, rec, rows)
}

func (m *Manager) workspaceProjectRestoreRowsFromMarkers(ctx context.Context, project domain.ProjectRecord, rec domain.SessionRecord, rows []domain.SessionWorktreeRecord) ([]ports.WorkspaceRepoInfo, error) {
	if len(rows) > 1 {
		return m.sessionWorktreeRowsToRepoInfos(ctx, project, rec, rows)
	}
	childRepos, err := m.store.ListWorkspaceRepos(ctx, project.ID)
	if err != nil {
		return nil, err
	}
	rootPath := rec.Metadata.WorkspacePath
	rootBranch := rec.Metadata.Branch
	var rootBaseSHA string
	if len(rows) == 1 && (rows[0].RepoName == "" || rows[0].RepoName == domain.RootWorkspaceRepoName) {
		rootPath = firstNonEmptyString(rows[0].WorktreePath, rootPath)
		rootBranch = firstNonEmptyString(rows[0].Branch, rootBranch)
		rootBaseSHA = rows[0].BaseSHA
	}
	out := []ports.WorkspaceRepoInfo{{
		RepoName:  domain.RootWorkspaceRepoName,
		RepoPath:  project.Path,
		Path:      rootPath,
		Branch:    rootBranch,
		BaseSHA:   rootBaseSHA,
		SessionID: rec.ID,
		ProjectID: rec.ProjectID,
	}}
	for _, repo := range childRepos {
		out = append(out, ports.WorkspaceRepoInfo{
			RepoName:     repo.Name,
			RepoPath:     filepath.Join(project.Path, filepath.FromSlash(repo.RelativePath)),
			Path:         filepath.Join(rootPath, filepath.FromSlash(repo.RelativePath)),
			Branch:       rootBranch,
			SessionID:    rec.ID,
			ProjectID:    rec.ProjectID,
			RelativePath: repo.RelativePath,
		})
	}
	return out, nil
}

func (m *Manager) workspaceProjectRows(ctx context.Context, rec domain.SessionRecord) ([]ports.WorkspaceRepoInfo, bool, error) {
	rows, err := m.store.ListSessionWorktrees(ctx, rec.ID)
	if err != nil {
		return nil, false, err
	}
	if len(rows) <= 1 {
		return nil, false, nil
	}
	project, err := m.loadProject(ctx, rec.ProjectID)
	if err != nil {
		return nil, false, err
	}
	if project.Kind.WithDefault() != domain.ProjectKindWorkspace {
		return nil, false, nil
	}
	infos, err := m.sessionWorktreeRowsToRepoInfos(ctx, project, rec, rows)
	if err != nil {
		return nil, false, err
	}
	return infos, true, nil
}

func (m *Manager) sessionWorktreeRowsToRepoInfos(ctx context.Context, project domain.ProjectRecord, rec domain.SessionRecord, rows []domain.SessionWorktreeRecord) ([]ports.WorkspaceRepoInfo, error) {
	childRepos, err := m.store.ListWorkspaceRepos(ctx, project.ID)
	if err != nil {
		return nil, err
	}
	repoPaths := map[string]string{domain.RootWorkspaceRepoName: project.Path}
	relPaths := map[string]string{}
	for _, repo := range childRepos {
		repoPaths[repo.Name] = filepath.Join(project.Path, filepath.FromSlash(repo.RelativePath))
		relPaths[repo.Name] = repo.RelativePath
	}
	out := make([]ports.WorkspaceRepoInfo, 0, len(rows))
	for _, row := range rows {
		repoPath := repoPaths[row.RepoName]
		if repoPath == "" {
			return nil, fmt.Errorf("session worktree row %q no longer matches workspace registry", row.RepoName)
		}
		out = append(out, ports.WorkspaceRepoInfo{
			RepoName:     row.RepoName,
			RepoPath:     repoPath,
			Path:         row.WorktreePath,
			Branch:       firstNonEmptyString(row.Branch, rec.Metadata.Branch),
			BaseSHA:      row.BaseSHA,
			SessionID:    rec.ID,
			ProjectID:    rec.ProjectID,
			RelativePath: relPaths[row.RepoName],
		})
	}
	return out, nil
}

func (m *Manager) saveAndTeardownWorkspaceProject(ctx context.Context, rec domain.SessionRecord, rows []ports.WorkspaceRepoInfo, destroyRuntime bool) error {
	for _, row := range rows {
		ref, err := m.workspace.StashUncommitted(ctx, workspaceInfoFromRepoInfo(row))
		if err != nil {
			return fmt.Errorf("save %s repo %s: stash: %w", rec.ID, row.RepoName, err)
		}
		if err := m.store.UpsertSessionWorktree(ctx, domain.SessionWorktreeRecord{
			SessionID:    rec.ID,
			RepoName:     row.RepoName,
			Branch:       row.Branch,
			BaseSHA:      row.BaseSHA,
			WorktreePath: row.Path,
			PreservedRef: ref,
			State:        "removed",
		}); err != nil {
			return fmt.Errorf("save %s repo %s: upsert worktree row: %w", rec.ID, row.RepoName, err)
		}
	}
	if err := m.lcm.MarkTerminated(ctx, rec.ID); err != nil {
		return fmt.Errorf("save %s: mark terminated: %w", rec.ID, err)
	}
	handle := runtimeHandle(rec.Metadata)
	if destroyRuntime && handle.ID != "" {
		if err := m.runtime.Destroy(ctx, handle); err != nil {
			m.logger.Warn("save-teardown-all: runtime destroy failed", "sessionID", rec.ID, "error", err)
		}
	}
	for i := len(rows) - 1; i >= 0; i-- {
		if err := m.workspace.ForceDestroy(ctx, workspaceInfoFromRepoInfo(rows[i])); err != nil {
			m.logger.Warn("save-teardown-all: force destroy failed", "sessionID", rec.ID, "repo", rows[i].RepoName, "error", err)
		}
	}
	return nil
}

func (m *Manager) destroyWorkspaceProjectRows(ctx context.Context, rows []ports.WorkspaceRepoInfo) (bool, error) {
	cleaned := false
	var firstErr error
	for i := len(rows) - 1; i >= 0; i-- {
		if rows[i].Path == "" {
			continue
		}
		info := workspaceInfoFromRepoInfo(rows[i])
		if err := m.workspace.Destroy(ctx, info); err != nil {
			if errors.Is(err, ports.ErrWorkspaceDirty) {
				return cleaned, err
			}
			if stateErr := m.upsertWorkspaceProjectRowState(ctx, rows[i], "retry_remove"); stateErr != nil && firstErr == nil {
				firstErr = stateErr
			}
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if err := m.upsertWorkspaceProjectRowState(ctx, rows[i], "unavailable"); err != nil && firstErr == nil {
			firstErr = err
		}
		cleaned = true
	}
	return cleaned, firstErr
}

func (m *Manager) upsertWorkspaceProjectRowState(ctx context.Context, row ports.WorkspaceRepoInfo, state string) error {
	return m.store.UpsertSessionWorktree(ctx, domain.SessionWorktreeRecord{
		SessionID:    row.SessionID,
		RepoName:     row.RepoName,
		Branch:       row.Branch,
		BaseSHA:      row.BaseSHA,
		WorktreePath: row.Path,
		State:        state,
	})
}

func (m *Manager) restoreWorkspaceProjectRows(ctx context.Context, rows []ports.WorkspaceRepoInfo) (ports.WorkspaceRepoInfo, error) {
	var root ports.WorkspaceRepoInfo
	for _, row := range rows {
		restored, err := m.workspace.Restore(ctx, ports.WorkspaceConfig{
			ProjectID: row.ProjectID,
			SessionID: row.SessionID,
			Branch:    row.Branch,
			RepoPath:  row.RepoPath,
			Path:      row.Path,
		})
		if err != nil {
			return ports.WorkspaceRepoInfo{}, fmt.Errorf("repo %s: %w", row.RepoName, err)
		}
		row.Path = restored.Path
		row.Branch = restored.Branch
		if row.RepoName == domain.RootWorkspaceRepoName {
			root = row
		}
	}
	if root.Path == "" {
		return ports.WorkspaceRepoInfo{}, errors.New("workspace project root worktree row missing")
	}
	return root, nil
}

func (m *Manager) applyWorkspaceProjectPreserved(ctx context.Context, rows []ports.WorkspaceRepoInfo) {
	for _, row := range rows {
		var preserveRef string
		sessionRows, err := m.store.ListSessionWorktrees(ctx, row.SessionID)
		if err != nil {
			m.logger.Error("restore-all: list worktrees failed", "sessionID", row.SessionID, "error", err)
			continue
		}
		for _, sessionRow := range sessionRows {
			if sessionRow.RepoName == row.RepoName {
				preserveRef = sessionRow.PreservedRef
				break
			}
		}
		if preserveRef == "" {
			continue
		}
		if applyErr := m.workspace.ApplyPreserved(ctx, workspaceInfoFromRepoInfo(row), preserveRef); applyErr != nil {
			if errors.Is(applyErr, ports.ErrPreservedConflict) {
				m.logger.Warn("restore-all: apply preserved produced conflicts; agent relaunched with conflict markers in place",
					"sessionID", row.SessionID, "repo", row.RepoName, "ref", preserveRef, "error", applyErr)
			} else {
				m.logger.Error("restore-all: apply preserved failed", "sessionID", row.SessionID, "repo", row.RepoName, "error", applyErr)
			}
		}
	}
}

// Send delivers a message to a running session's agent via the messenger.
func (m *Manager) Send(ctx context.Context, id domain.SessionID, message string) error {
	if err := m.messenger.Send(ctx, id, message); err != nil {
		return fmt.Errorf("send %s: %w", id, err)
	}
	return nil
}

// CleanupSkip reports one terminal session whose workspace was preserved
// rather than reclaimed, and why.
type CleanupSkip struct {
	SessionID domain.SessionID
	Reason    string
}

// CleanupResult reports what Cleanup reclaimed and what it preserved.
type CleanupResult struct {
	Cleaned []domain.SessionID
	Skipped []CleanupSkip
}

// Cleanup reclaims the workspaces of terminal sessions in a project. A workspace
// whose teardown is refused (uncommitted work) is never forced; it is reported
// in Skipped with the reason so the refusal is visible instead of silent.
func (m *Manager) Cleanup(ctx context.Context, project domain.ProjectID) (CleanupResult, error) {
	recs, err := m.cleanupRecords(ctx, project)
	if err != nil {
		return CleanupResult{}, fmt.Errorf("cleanup %s: %w", project, err)
	}
	result := CleanupResult{Cleaned: make([]domain.SessionID, 0, len(recs)), Skipped: []CleanupSkip{}}
	for _, rec := range recs {
		if !rec.IsTerminated {
			continue
		}
		ws := workspaceInfo(rec)
		if ws.Path == "" {
			continue
		}
		if h := runtimeHandle(rec.Metadata); h.ID != "" {
			// Reap-safety: a terminated row whose pane still has a LIVE agent means
			// the termination was inferred/stale (a late SessionEnd, or the user
			// resumed into the pane). Reaping it would kill a session the user is
			// still using — the orchestrator "closes repeatedly" bug. Skip it (and
			// its worktree); it can be reaped once the agent actually exits. On an
			// ambiguous probe error, skip too rather than risk killing a live pane.
			alive, err := m.agentAlive(ctx, h)
			if err != nil || alive {
				reason := "agent still running"
				if err != nil {
					reason = "agent liveness probe failed"
				}
				result.Skipped = append(result.Skipped, CleanupSkip{SessionID: rec.ID, Reason: reason})
				continue
			}
			_ = m.runtime.Destroy(ctx, h) // best effort; usually already gone
		}
		if rows, ok, rowErr := m.workspaceProjectRows(ctx, rec); rowErr != nil {
			m.logger.Warn("cleanup: workspace rows failed", "sessionID", rec.ID, "error", rowErr)
			result.Skipped = append(result.Skipped, CleanupSkip{SessionID: rec.ID, Reason: "workspace teardown failed"})
			continue
		} else if ok {
			if _, err := m.destroyWorkspaceProjectRows(ctx, rows); err != nil {
				if !errors.Is(err, ports.ErrWorkspaceDirty) {
					m.logger.Warn("cleanup: workspace teardown failed", "sessionID", rec.ID, "path", ws.Path, "error", err)
				}
				result.Skipped = append(result.Skipped, CleanupSkip{SessionID: rec.ID, Reason: cleanupSkipReason(err)})
				continue
			}
		} else if err := m.workspace.Destroy(ctx, ws); err != nil {
			if !errors.Is(err, ports.ErrWorkspaceDirty) {
				// The public reason stays a fixed string (the raw error carries
				// internal filesystem paths); the full cause lands here.
				m.logger.Warn("cleanup: workspace teardown failed", "sessionID", rec.ID, "path", ws.Path, "error", err)
			}
			result.Skipped = append(result.Skipped, CleanupSkip{SessionID: rec.ID, Reason: cleanupSkipReason(err)})
			continue
		}
		// The worktree is gone; reap any stray process the agent left running in it
		// (e.g. a detached dev server holding a port) that tmux teardown missed.
		// Best-effort and tightly guarded to AO worktree paths.
		newStrayReaper(m.logger).reap(ctx, ws.Path)
		// A terminal worker reclaimed here may never have gone through Kill (e.g.
		// merged, or crash-terminated by reconcile), so this is the choke point that
		// closes its reviewer pane.
		m.reapReviewer(ctx, rec.ID)
		result.Cleaned = append(result.Cleaned, rec.ID)
	}
	return result, nil
}

// cleanupSkipReason renders a workspace teardown refusal as a short
// user-facing reason for the cleanup report. Deliberately not the raw error:
// it flows to the API response and CLI output, and teardown errors embed
// internal filesystem paths.
func cleanupSkipReason(err error) string {
	if errors.Is(err, ports.ErrWorkspaceDirty) {
		return "workspace has uncommitted changes"
	}
	return "workspace teardown failed"
}

func (m *Manager) cleanupRecords(ctx context.Context, project domain.ProjectID) ([]domain.SessionRecord, error) {
	if project == "" {
		return m.store.ListAllSessions(ctx)
	}
	return m.store.ListSessions(ctx, project)
}

// ---- helpers ----

func seedRecord(cfg ports.SpawnConfig, now time.Time) domain.SessionRecord {
	return domain.SessionRecord{
		ProjectID:   cfg.ProjectID,
		IssueID:     cfg.IssueID,
		Kind:        cfg.Kind,
		CreatedAt:   now,
		UpdatedAt:   now,
		Harness:     cfg.Harness,
		DisplayName: cfg.DisplayName,
		Activity:    domain.Activity{State: domain.ActivityIdle, LastActivityAt: now},
	}
}

// todoSeedRecord builds the durable row for a prepared TODO. Unlike seedRecord
// (a transient row whose spec is only written at MarkSpawned), this persists the
// full spec — prompt, desired new branch, base branch, PR target, createdBy —
// so StartTodo can replay it verbatim, and marks the row is_todo so status
// derivation reads it as StatusTodo and it never counts as a live session.
func todoSeedRecord(cfg ports.SpawnConfig, now time.Time) domain.SessionRecord {
	return domain.SessionRecord{
		ProjectID:      cfg.ProjectID,
		IssueID:        cfg.IssueID,
		Kind:           cfg.Kind,
		CreatedAt:      now,
		UpdatedAt:      now,
		Harness:        cfg.Harness,
		DisplayName:    cfg.DisplayName,
		Activity:       domain.Activity{State: domain.ActivityIdle, LastActivityAt: now},
		IsTodo:         true,
		BaseBranch:     cfg.BaseBranch,
		AutoNameBranch: cfg.AutoNameBranch,
		PRTarget:       cfg.PRTarget,
		CreatedBy:      cfg.CreatedBy,
		Metadata:       domain.SessionMetadata{Branch: cfg.Branch, Prompt: cfg.Prompt},
	}
}

func defaultSessionBranch(id domain.SessionID, kind domain.SessionKind, prefix string) string {
	if kind == domain.KindOrchestrator {
		return "ao/" + prefix + "-orchestrator"
	}
	// A fresh, unique branch per worker session: gitworktree can't add a worktree
	// on a branch already checked out elsewhere (e.g. main). Put the root work
	// branch under a session namespace so sibling PR branches such as
	// ao/<session>/<topic> remain valid Git refs.
	return "ao/" + string(id) + "/root"
}

func defaultSpawnBranch(id domain.SessionID, kind domain.SessionKind, prefix string, projectKind domain.ProjectKind) string {
	if projectKind == domain.ProjectKindWorkspace {
		return "ao/" + string(id)
	}
	return defaultSessionBranch(id, kind, prefix)
}

func buildPrompt(cfg ports.SpawnConfig) string {
	return cfg.Prompt
}

// buildSpawnTexts returns the user-facing prompt and the system prompt to
// deliver separately to the agent. Orchestrator role instructions and worker
// coordination hints are placed in the system prompt so they are treated as
// standing instructions rather than part of the human's task request. A
// promptless spawn delivers no user prompt at all: the agent simply lands at an
// empty input box rather than receiving an auto-generated kickoff turn.
func (m *Manager) buildSpawnTexts(ctx context.Context, cfg ports.SpawnConfig) (prompt, systemPrompt string, err error) {
	prompt = buildPrompt(cfg)
	systemPrompt, err = m.buildSystemPrompt(ctx, cfg.Kind, cfg.ProjectID)
	if err != nil {
		return "", "", err
	}
	return prompt, systemPrompt, nil
}

// buildSystemPrompt derives the standing instructions for a session of the
// given kind from current store state. Restore recomputes them through here
// rather than persisting them, so a restored worker points at the orchestrator
// that is active now, not the one from its original spawn.
func (m *Manager) buildSystemPrompt(ctx context.Context, kind domain.SessionKind, projectID domain.ProjectID) (string, error) {
	// Resolve the project's convention so the orchestrator/worker prompts carry the
	// branch prefix and base branch. A missing project yields a zero config, which
	// resolves to no convention (prompts unchanged from the pre-convention default).
	project, err := m.loadProject(ctx, projectID)
	if err != nil {
		return "", err
	}
	cfg := project.Config.WithDefaults()
	conv := cfg.GitConvention
	adds := cfg.SystemPromptAdditions

	// Each kind assembles in layers: the effective global base (override else
	// built-in default, project-id substituted) + the per-project addition + AO's
	// protected coordination floor + the existing dynamic injections. The
	// always-last confidentiality guard is appended below.
	var base string
	switch kind {
	case domain.KindOrchestrator:
		base = m.effectiveBase(prompts.KindOrchestrator, projectID) +
			prompts.Section(adds.Orchestrator) +
			prompts.CoordinationFloor(prompts.KindOrchestrator) +
			orchestratorGitConventionPrompt(conv, cfg.DefaultBranch) +
			orchestratorSpawnConfirmPrompt(m.confirmBeforeSpawn(), conv, cfg.DefaultBranch)
	case domain.KindWorker:
		orchestratorID, ok, err := m.activeOrchestratorSessionID(ctx, projectID)
		if err != nil {
			return "", err
		}
		body := m.effectiveBase(prompts.KindWorker, projectID) +
			prompts.Section(adds.Worker) +
			prompts.CoordinationFloor(prompts.KindWorker) +
			workerGitConventionPrompt(conv, cfg.DefaultBranch)
		if ok {
			base = workerOrchestratorPrompt(orchestratorID) + "\n\n" + body
		} else {
			base = body
		}
	}
	if base == "" {
		return "", nil
	}
	// The @session / #PR / !MR reference convention applies to both orchestrator
	// and worker prompts, so it is injected here rather than baked into either
	// editable base — it stays present even when a base is overridden or cleared.
	base += prompts.ReferenceConvention()
	// Workers additionally get the always-injected smoke-test checklist protocol,
	// placed here (not in the editable base) so it survives a cleared/overridden
	// base, same as the reference convention.
	if kind == domain.KindWorker {
		base += prompts.SmokeChecklistProtocol()
	}
	workspacePrompt, err := m.workspaceProjectPrompt(ctx, kind, projectID)
	if err != nil {
		return "", err
	}
	if workspacePrompt != "" {
		base += "\n\n" + workspacePrompt
	}
	return base + m.aoSkillPointer() + prompts.ConfidentialityGuard, nil
}

// effectiveBase returns the assembled, project-rendered global base for a kind:
// the stored override when set, otherwise the built-in default, with the
// project-id placeholder substituted.
func (m *Manager) effectiveBase(k prompts.Kind, projectID domain.ProjectID) string {
	base := prompts.DefaultBase(k)
	if m.promptOverrides != nil {
		if ov, ok := m.promptOverrides().Base[k]; ok {
			base = ov
		}
	}
	return prompts.RenderBase(base, string(projectID))
}

// aoSkillPointer is appended to every agent system prompt. It points the agent
// at the using-ao skill the daemon installs under the data dir, rather than
// inlining the whole CLI catalog. The path is absolute so it resolves from any
// project's worktree, not just the AO repo (the only place a repo-relative
// skills/ path would exist). The skill file carries exact flags and examples,
// so the standing prompt stays a short pointer rather than a command dump.
func (m *Manager) aoSkillPointer() string {
	dir := skillassets.Dir(m.dataDir)
	skillFile := filepath.Join(dir, "SKILL.md")
	commandsGlob := filepath.Join(dir, "commands", "*.md")
	return "\n\n" + "## Using the ao CLI\n\n" +
		"When you need to use the `ao` CLI, read `" + skillFile + "` first (and the relevant `" + commandsGlob + "`) for the full command catalog, flags, and examples."
}

func (m *Manager) workspaceProjectPrompt(ctx context.Context, kind domain.SessionKind, projectID domain.ProjectID) (string, error) {
	project, err := m.loadProject(ctx, projectID)
	if err != nil {
		return "", err
	}
	if project.Kind.WithDefault() != domain.ProjectKindWorkspace {
		return "", nil
	}
	repos, err := m.store.ListWorkspaceRepos(ctx, string(projectID))
	if err != nil {
		return "", fmt.Errorf("list workspace repos for prompt: %w", err)
	}
	switch kind {
	case domain.KindOrchestrator:
		return workspaceOrchestratorPrompt(repos), nil
	case domain.KindWorker:
		return workspaceWorkerPrompt(repos), nil
	default:
		return "", nil
	}
}

func (m *Manager) activeOrchestratorSessionID(ctx context.Context, project domain.ProjectID) (domain.SessionID, bool, error) {
	recs, err := m.store.ListSessions(ctx, project)
	if err != nil {
		return "", false, fmt.Errorf("list sessions for %s: %w", project, err)
	}
	for _, rec := range recs {
		if rec.Kind == domain.KindOrchestrator && !rec.IsTerminated {
			return rec.ID, true, nil
		}
	}
	return "", false, nil
}

func workspaceOrchestratorPrompt(repos []domain.WorkspaceRepoRecord) string {
	return fmt.Sprintf(`## Workspace project

This project is a multi-repository workspace. Sessions start at the workspace root. The root repository is %s at path `+"`.`"+`; child repositories are nested below it.

Repositories:
%s

When spawning workers, name the repository path or paths they should work in. Work can span multiple repositories, so track deliverables, pull requests, and checks by repository.`, domain.RootWorkspaceRepoName, workspaceRepoList(repos))
}

func workspaceWorkerPrompt(repos []domain.WorkspaceRepoRecord) string {
	return fmt.Sprintf(`## Workspace project

This session is a multi-repository workspace. You start at the workspace root. The root repository is %s at path `+"`.`"+`; child repositories are nested below it.

Repositories:
%s

Before editing, identify which repository owns the task and keep changes scoped to the requested repository or repositories. If you touch root files, call that out explicitly because root changes are separate from child-repository changes.`, domain.RootWorkspaceRepoName, workspaceRepoList(repos))
}

func workspaceRepoList(repos []domain.WorkspaceRepoRecord) string {
	lines := make([]string, 0, 1+len(repos))
	lines = append(lines, fmt.Sprintf("- %s: .", domain.RootWorkspaceRepoName))
	for _, repo := range repos {
		lines = append(lines, fmt.Sprintf("- %s: %s", repo.Name, repo.RelativePath))
	}
	return strings.Join(lines, "\n")
}

func workerOrchestratorPrompt(orchestratorID domain.SessionID) string {
	return fmt.Sprintf(`## Orchestrator coordination

An active orchestrator session exists for this project. If you hit a true blocker or need cross-session coordination, message it with:
`+"`ao send --session %s --message \"<your message>\"`"+`

Only ping the orchestrator for true blockers, cross-session coordination, or decisions that cannot be resolved within your own task.`, orchestratorID)
}

// orchestratorGitConventionPrompt returns the branch-convention section injected
// into the orchestrator prompt, or "" when the project sets no convention. This is
// the primary mechanism: the orchestrator builds every `ao spawn`, so it must know
// the project's prefix, base branch, and PR target to pass the right --branch/--from
// and brief the worker. baseBranch is the project's DefaultBranch (base + PR target).
func orchestratorGitConventionPrompt(conv domain.GitConventionConfig, baseBranch string) string {
	if !conv.Active() {
		return ""
	}
	if conv.Workflow == domain.GitWorkflowGitflow {
		return fmt.Sprintf("\n\n"+`## Git branch convention (gitflow)

This project follows gitflow. When you spawn a worker, start it from `+"`%[1]s`"+` and set its branch explicitly so it lands on-convention:
`+"`ao spawn --from %[1]s --branch <type>/<topic> ...`"+`
- `+"`feature/<topic>`"+` — new features and enhancements
- `+"`bugfix/<topic>`"+` — bug fixes
- `+"`hotfix/<topic>`"+` — urgent production fixes
When the task has a Jira card key, put it uppercase right after the type, e.g. `+"`feature/STAR-2270-ecoupon-list`"+`. Tell the worker to open its pull request against `+"`%[1]s`"+`. If you leave --branch off, AO auto-names a gitflow branch from the task.`, baseBranch)
	}
	prefix := conv.NormalizedBranchPrefix()
	return fmt.Sprintf("\n\n"+`## Git branch convention

This project prefixes every branch with `+"`%[2]s`"+`. When you spawn a worker, start it from `+"`%[1]s`"+` and set its branch explicitly so it lands on-convention:
`+"`ao spawn --from %[1]s --branch %[2]s<topic> ...`"+`
For example `+"`%[2]sadd-login`"+`, or `+"`%[2]sSTAR-2270-ecoupon-list`"+` when the task has a Jira card key. Tell the worker to open its pull request against `+"`%[1]s`"+`. If you leave --branch off, AO applies the `+"`%[2]s`"+` prefix automatically.`, baseBranch, prefix)
}

// confirmBeforeSpawn reports whether the orchestrator prompt should carry the
// spawn-confirmation gate. A nil getter (e.g. a bare Manager in tests, or wiring
// that omits the store) defaults to true so the safe "confirm" behavior holds.
func (m *Manager) confirmBeforeSpawn() bool {
	if m.spawnConfirmEnabled == nil {
		return true
	}
	return m.spawnConfirmEnabled()
}

// orchestratorSpawnConfirmPrompt returns the confirmation-gate section injected
// into the orchestrator prompt, or "" when the gate is disabled. When enabled it
// tells the orchestrator to present a summary (task, source branch, new branch,
// PR target) and wait for explicit approval before running `ao spawn`. The
// new-branch line references the git-convention section injected just above when
// a convention is active, reusing that feature's prefix rather than repeating
// them. baseBranch is the project's DefaultBranch (base + PR target).
func orchestratorSpawnConfirmPrompt(enabled bool, conv domain.GitConventionConfig, baseBranch string) string {
	if !enabled {
		return ""
	}
	newBranch := "the branch that will be created"
	if conv.Active() {
		newBranch = "the branch that will be created, following the git branch convention above (e.g. `feature/<topic>`)"
	}
	return fmt.Sprintf("\n\n"+`## Confirm before spawning

Before you run `+"`ao spawn`"+`, present a short confirmation summary to the human and wait for their explicit approval. Do NOT spawn until they confirm. The summary must list:
- **Task** — one line on what the worker will do
- **Source branch** — the `+"`--from`"+` base branch (default `+"`%[1]s`"+`)
- **New branch** — %[2]s
- **PR target** — where the worker's pull request will merge (`+"`%[1]s`"+`)

If the human asks for changes, revise and re-confirm. Run `+"`ao spawn`"+` only after they approve. This confirmation is conversational — ask in chat and wait; there is no separate UI dialog.`, baseBranch, newBranch)
}

// workerGitConventionPrompt returns the branch-convention section injected into the
// worker prompt, or "" when the project sets no convention. It is a short standing
// note so a worker independently keeps any branches it creates on-convention and
// targets the right base; the namespace rules in the worker base (prompts.KindWorker)
// still govern how sibling/stacked branches are named.
func workerGitConventionPrompt(conv domain.GitConventionConfig, baseBranch string) string {
	if !conv.Active() {
		return ""
	}
	if conv.Workflow == domain.GitWorkflowGitflow {
		return fmt.Sprintf("\n\n"+`## Git branch convention

This project follows gitflow: name branches by type (`+"`feature/…`"+`, `+"`bugfix/…`"+`, `+"`hotfix/…`"+`) and open your pull requests against `+"`%s`"+`.`, baseBranch)
	}
	prefix := conv.NormalizedBranchPrefix()
	return fmt.Sprintf("\n\n"+`## Git branch convention

This project prefixes branches with `+"`%[2]s`"+`: keep any branches you create under that prefix and open your pull requests against `+"`%[1]s`"+`.`, baseBranch, prefix)
}

// spawnEnv builds the runtime environment: the per-project env vars first, then
// the AO-internal vars last so they always win (a project cannot override
// AO_SESSION_ID and friends). An empty runFile is omitted so the hook CLI's own
// default run-file resolution applies.
func spawnEnv(id domain.SessionID, project domain.ProjectID, issue domain.IssueID, dataDir, runFile string, projectEnv map[string]string) map[string]string {
	env := make(map[string]string, len(projectEnv)+5)
	for k, v := range projectEnv {
		env[k] = v
	}
	env[EnvSessionID] = string(id)
	env[EnvProjectID] = string(project)
	env[EnvIssueID] = string(issue)
	env[EnvDataDir] = dataDir
	if runFile != "" {
		env[EnvRunFile] = runFile
	} else {
		delete(env, EnvRunFile)
	}
	return env
}

// runtimeEnv is spawnEnv plus the hook PATH pin: the session's PATH puts the
// running daemon's own directory first, so the bare `ao` in workspace hook
// commands resolves to the daemon that installed them rather than whatever
// `ao` is first on the inherited PATH (e.g. a legacy CLI without the hooks
// command, which fails every callback and silently kills activity tracking).
// When the pin cannot be applied the inherited PATH is kept and a warning is
// logged so the degradation isn't silent.
func (m *Manager) runtimeEnv(id domain.SessionID, project domain.ProjectID, issue domain.IssueID, projectEnv map[string]string) map[string]string {
	env := spawnEnv(id, project, issue, m.dataDir, m.runFile, projectEnv)
	path, err := HookPATH(m.executable, os.Getenv, projectEnv)
	if err != nil {
		m.logger.Warn("session PATH not pinned to the daemon binary; `ao hooks` callbacks may resolve to a different ao and activity tracking will stall",
			"session", id, "error", err)
		return env
	}
	env["PATH"] = path
	return env
}

// HookPATH builds the PATH value pinned into a spawned session: the daemon
// executable's directory prepended to the base PATH (the project's PATH
// override when set, else the daemon's inherited PATH — matching what the
// runtime would have exported anyway). An error means the pin cannot be
// applied: the executable is unresolvable, or is not named "ao", in which case
// prepending its directory would not change what `ao` resolves to. Exported so
// the reviewer launcher can pin its pane's PATH the same way.
func HookPATH(executable func() (string, error), getenv func(string) string, projectEnv map[string]string) (string, error) {
	exe, err := executable()
	if err != nil {
		return "", fmt.Errorf("resolve daemon executable: %w", err)
	}
	name := filepath.Base(exe)
	if runtime.GOOS == "windows" {
		name = strings.TrimSuffix(strings.ToLower(name), ".exe")
	}
	if name != hookBinaryName {
		return "", fmt.Errorf("daemon executable %s is not named %q", exe, hookBinaryName)
	}
	base := projectEnv["PATH"]
	if base == "" {
		base = getenv("PATH")
	}
	dir := filepath.Dir(exe)
	if base == "" {
		return dir, nil
	}
	return dir + string(os.PathListSeparator) + base, nil
}

// provisionWorkspace applies the project's per-workspace setup after the
// worktree exists: symlink shared files from the project repo, then run any
// post-create commands. Either failing aborts the spawn so a half-provisioned
// workspace never launches an agent.
func (m *Manager) provisionWorkspace(ctx context.Context, project domain.ProjectRecord, workspacePath string) error {
	if err := applySymlinks(project.Path, workspacePath, project.Config.Symlinks); err != nil {
		return err
	}
	return runPostCreate(ctx, workspacePath, project.Config.PostCreate)
}

// applySymlinks links each repo-relative path into the workspace. A source that
// does not exist is skipped (symlinks are a convenience for optional files like
// .env); a real link failure aborts. Paths must be repo-relative with no
// parent traversal (no leading "/", no ".." segment) — a bad path is refused
// up front so a project config cannot escape the project or workspace tree.
func applySymlinks(projectPath, workspacePath string, symlinks []string) error {
	for _, rel := range symlinks {
		rel = strings.TrimSpace(rel)
		if rel == "" {
			continue
		}
		clean, err := safeRelPath(rel)
		if err != nil {
			return fmt.Errorf("symlink %q: %w", rel, err)
		}
		source := filepath.Join(projectPath, clean)
		if _, err := os.Stat(source); err != nil {
			continue
		}
		target := filepath.Join(workspacePath, clean)
		if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
			return fmt.Errorf("symlink %q: %w", rel, err)
		}
		if _, err := os.Lstat(target); err == nil {
			continue
		}
		if err := os.Symlink(source, target); err != nil {
			return fmt.Errorf("symlink %q: %w", rel, err)
		}
	}
	return nil
}

// safeRelPath confines rel to a repo-relative path: no absolute paths and no
// ".." segments (before or after Clean). The cleaned form is returned so
// callers join it against project/workspace roots safely.
func safeRelPath(rel string) (string, error) {
	if filepath.IsAbs(rel) || strings.HasPrefix(rel, "/") || strings.HasPrefix(rel, `\`) {
		return "", fmt.Errorf("path must be repo-relative")
	}
	clean := filepath.Clean(rel)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || clean == "." || clean == "" {
		return "", fmt.Errorf("path must be repo-relative")
	}
	for _, seg := range strings.Split(filepath.ToSlash(clean), "/") {
		if seg == ".." {
			return "", fmt.Errorf("path must be repo-relative")
		}
	}
	return clean, nil
}

// runPostCreate runs each post-create command in the workspace via the platform
// shell, so OS-agnostic commands like "pnpm install" work. A non-zero exit
// aborts the spawn with the command output.
func runPostCreate(ctx context.Context, workspacePath string, commands []string) error {
	for _, command := range commands {
		command = strings.TrimSpace(command)
		if command == "" {
			continue
		}
		var cmd *exec.Cmd
		if runtime.GOOS == "windows" {
			cmd = aoprocess.CommandContext(ctx, "cmd", "/c", command)
		} else {
			cmd = aoprocess.CommandContext(ctx, "sh", "-c", command)
		}
		cmd.Dir = workspacePath
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("postCreate %q: %w: %s", command, err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}

// preLauncher is an optional Agent capability: a step the manager runs before
// launch. Claude Code implements it to record workspace trust in ~/.claude.json
// so its interactive "do you trust this folder?" dialog can't block the headless
// pane. Adapters that don't need it simply omit the method.
type preLauncher interface {
	PreLaunch(ctx context.Context, cfg ports.LaunchConfig) error
}

// prepareWorkspace runs the per-session pre-launch steps before the runtime
// starts the agent: installing the workspace-local activity hooks (so early
// startup hooks can update the already-created session row), then any optional
// PreLaunch step. Shared by Spawn and Restore.
func (m *Manager) prepareWorkspace(ctx context.Context, agent ports.Agent, id domain.SessionID, workspacePath, systemPrompt string, agentConfig ports.AgentConfig) error {
	if err := agent.GetAgentHooks(ctx, ports.WorkspaceHookConfig{
		SessionID:     string(id),
		WorkspacePath: workspacePath,
		DataDir:       m.dataDir,
		SystemPrompt:  systemPrompt,
		Config:        agentConfig,
	}); err != nil {
		return fmt.Errorf("install hooks: %w", err)
	}
	if pl, ok := agent.(preLauncher); ok {
		if err := pl.PreLaunch(ctx, ports.LaunchConfig{SessionID: string(id), WorkspacePath: workspacePath}); err != nil {
			return fmt.Errorf("pre-launch: %w", err)
		}
	}
	return nil
}

// restoreArgv builds the argv to relaunch a torn-down session: the agent's
// native resume command when it can continue the session, else a fresh launch.
// The agent signals via ok=false (e.g. no native session id captured yet, or the
// conversation transcript for this worktree is gone so a resume would fail).
// Returns ErrNotResumable only for a promptless, unresumable non-orchestrator:
// a worker with no prompt and no native session id has nothing to restore from.
// Orchestrators are promptless by design, so when they cannot resume they
// relaunch fresh with the system prompt only rather than erroring.
func restoreArgv(ctx context.Context, agent ports.Agent, id domain.SessionID, workspacePath string, meta domain.SessionMetadata, systemPrompt string, agentConfig ports.AgentConfig, kind domain.SessionKind) ([]string, error) {
	ref := ports.SessionRef{
		ID:            string(id),
		WorkspacePath: workspacePath,
		Metadata:      map[string]string{ports.MetadataKeyAgentSessionID: meta.AgentSessionID},
	}
	cmd, ok, err := agent.GetRestoreCommand(ctx, ports.RestoreConfig{Session: ref, Kind: kind, SystemPrompt: systemPrompt, Config: agentConfig, Permissions: agentConfig.Permissions})
	if err != nil {
		return nil, fmt.Errorf("restore command: %w", err)
	}
	if ok {
		return cmd, nil
	}
	// Adapter cannot resume. A saved prompt is replayed fresh. An orchestrator is
	// promptless by design and relaunches with the system prompt only. A promptless
	// WORKER has no task and no session id to restore from: do not blank-relaunch it.
	if meta.Prompt == "" && kind != domain.KindOrchestrator {
		return nil, ErrNotResumable
	}
	// Fall through to GetLaunchCommand (replays meta.Prompt; empty for an orchestrator).
	argv, err := agent.GetLaunchCommand(ctx, ports.LaunchConfig{
		SessionID:     string(id),
		WorkspacePath: workspacePath,
		Kind:          kind,
		Prompt:        meta.Prompt,
		SystemPrompt:  systemPrompt,
		Config:        agentConfig,
		Permissions:   agentConfig.Permissions,
	})
	if err != nil {
		return nil, fmt.Errorf("launch command: %w", err)
	}
	return argv, nil
}

// validateAgentBinary checks that argv[0] resolves via the manager's
// lookPath (exec.LookPath in prod) before any runtime work happens. Adapters
// that can't resolve their binary now return ports.ErrAgentBinaryNotFound from
// GetLaunchCommand directly; this guard is a defense-in-depth for adapters
// that return an argv[0] like "claude" without verifying.
func (m *Manager) validateAgentBinary(argv []string) error {
	if len(argv) == 0 {
		return fmt.Errorf("agent: empty launch argv: %w", ports.ErrAgentBinaryNotFound)
	}
	bin := argv[0]
	if _, err := m.lookPath(bin); err != nil {
		return fmt.Errorf("agent binary %q: %w", bin, ports.ErrAgentBinaryNotFound)
	}
	return nil
}

func (m *Manager) validateRuntimePrerequisites() error {
	if runtime.GOOS == "windows" {
		return nil
	}
	if path, err := m.lookPath("tmux"); err != nil || path == "" {
		return fmt.Errorf("%w: tmux required on macOS/Linux but not in PATH", ports.ErrRuntimePrerequisite)
	}
	return nil
}

func runtimeHandle(meta domain.SessionMetadata) ports.RuntimeHandle {
	return ports.RuntimeHandle{ID: meta.RuntimeHandleID}
}

func workspaceInfo(rec domain.SessionRecord) ports.WorkspaceInfo {
	return ports.WorkspaceInfo{
		Path:      rec.Metadata.WorkspacePath,
		Branch:    rec.Metadata.Branch,
		SessionID: rec.ID,
		ProjectID: rec.ProjectID,
	}
}

func workspaceInfoFromRepoInfo(info ports.WorkspaceRepoInfo) ports.WorkspaceInfo {
	return ports.WorkspaceInfo{
		Path:      info.Path,
		Branch:    info.Branch,
		SessionID: info.SessionID,
		ProjectID: info.ProjectID,
		RepoPath:  info.RepoPath,
	}
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
