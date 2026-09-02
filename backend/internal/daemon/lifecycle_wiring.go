package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/activitydispatch"
	agentregistry "github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/registry"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/reviewer"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/runtime/runtimeselect"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/scm/composite"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/workspace/gitworktree"
	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/lifecycle"
	"github.com/aoagents/agent-orchestrator/backend/internal/looptelemetry"
	"github.com/aoagents/agent-orchestrator/backend/internal/messagetemplates"
	"github.com/aoagents/agent-orchestrator/backend/internal/observe/reaper"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/promptoverrides"
	"github.com/aoagents/agent-orchestrator/backend/internal/reclaimsettings"
	"github.com/aoagents/agent-orchestrator/backend/internal/responselang"
	reviewcore "github.com/aoagents/agent-orchestrator/backend/internal/review"
	reviewsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/review"
	sessionsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/session"
	smokesvc "github.com/aoagents/agent-orchestrator/backend/internal/service/smoke"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
	"github.com/aoagents/agent-orchestrator/backend/internal/spawnconfirm"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

type notificationSink interface {
	Notify(context.Context, ports.NotificationIntent) error
}

// lifecycleStack owns the runtime reaper goroutine started with the lifecycle
// reducer. The reducer itself is only used for wiring observations into storage.
type lifecycleStack struct {
	// LCM is the Lifecycle Manager (the canonical write path). It is exposed so
	// startSession can share the same reducer the reaper drives, rather than
	// standing up a second store+LCM pair that would diverge under writes.
	LCM         *lifecycle.Manager
	reaperDone  <-chan struct{}
	scmDone     <-chan struct{}
	trackerDone <-chan struct{}
}

// startLifecycle constructs the Lifecycle Manager over the store and starts the
// reaper. The goroutine stops when ctx is cancelled; Stop waits for it to drain.
// The messenger is the per-daemon agent messenger the LCM uses to nudge agents
// in response to SCM observations (CI failure, review feedback, merge conflict).
// templates is the prompt-overrides store's Templates getter; it lets an
// operator's edited nudge text take effect on the next observation without a
// daemon restart.
func startLifecycle(ctx context.Context, store *sqlite.Store, runtime ports.Runtime, messenger ports.AgentMessenger, notifier notificationSink, telemetry ports.EventSink, templates func() map[string]string, autoNudgeDefault func() bool, reg *looptelemetry.Registry, logger *slog.Logger) *lifecycleStack {
	renderer := messagetemplates.NewRenderer(templates)
	lcm := lifecycle.New(store, messenger, lifecycle.WithNotificationSink(notifier), lifecycle.WithTelemetry(telemetry), lifecycle.WithMessageRenderer(renderer), lifecycle.WithAutoNudgeDefault(autoNudgeDefault), lifecycle.WithTranscriptLocator(locateTranscript))
	reaperRec := reg.Register(looptelemetry.Spec{
		Name:        "reaper",
		Display:     "Runtime liveness",
		Description: "Probes each active session's runtime so dead tmux panes flip to terminated.",
		Interval:    reaper.DefaultTickInterval,
	})
	rp := reaper.New(lcm, store, runtime, reaper.Config{Logger: logger, OnTick: reaperRec.Tick})
	return &lifecycleStack{LCM: lcm, reaperDone: rp.Start(ctx)}
}

// Stop waits for the reaper goroutine to exit. The caller must cancel the ctx
// passed to startLifecycle before calling Stop.
func (l *lifecycleStack) Stop() {
	<-l.reaperDone
	if l.scmDone != nil {
		<-l.scmDone
	}
	if l.trackerDone != nil {
		<-l.trackerDone
	}
}

// sessionLifecycle is the narrow surface of sessionmanager.Manager used for
// boot/shutdown wiring. A minimal interface keeps the daemon testable without
// depending on the concrete manager type.
//
// SaveAndTeardownAll is deliberately ABSENT from this interface so the daemon
// cannot tear down live sessions on shutdown. Sessions survive the daemon exit
// and Reconcile on the next boot adopts them, preserving session IDs. Re-adding
// the method here is a visible, reviewable interface change.
type sessionLifecycle interface {
	Reconcile(ctx context.Context) error
	RestoreAll(ctx context.Context) error
	CloseIdleSessions(ctx context.Context) error
	SyncOrchestratorWorkspaces(ctx context.Context) error
	// NoteRuntimeTouch is the crew's lazy-creation trigger: the simulator service
	// reports a granted lease and the manager decides whether that means this task
	// has just earned a qa (crew_join.go). It is on this narrow interface because
	// the daemon is where the two halves meet.
	NoteRuntimeTouch(ctx context.Context, id domain.SessionID, reason domain.CrewJoinReason)
	// SetSimDeviceAssigner wires the per-session simulator reservation whose udid
	// a spawned agent reads as AO_SIM_UDID / AO_SIM_DESTINATION. It is on this
	// interface for the same reason NoteRuntimeTouch is: the manager is built
	// before the simulator services, so the daemon is where the two halves meet.
	SetSimDeviceAssigner(fn func(context.Context, domain.SessionID) (string, error))
}

// startSession builds the controller-facing session service: a session manager
// over the selected runtime, a per-session gitworktree workspace, the shared
// store + LCM, the per-session agent resolver, and the agent messenger. The
// returned service is mounted at httpd APIDeps.Sessions. It also returns the
// manager so the caller can wire Reconcile into the boot sequence.
func startSession(cfg config.Config, runtime runtimeselect.Runtime, store *sqlite.Store, lcm *lifecycle.Manager, messenger ports.AgentMessenger, telemetry ports.EventSink, spawnConfirm *spawnconfirm.Store, promptOverrides *promptoverrides.Store, responseLang *responselang.Store, jiraPoster smokesvc.JiraPoster, reclaimSettings func() reclaimsettings.Settings, treeWatchers reviewcore.Watcher, log *slog.Logger) (*sessionsvc.Service, reviewsvc.Manager, smokesvc.Manager, sessionLifecycle, error) {
	defaultAgent := cfg.Agent
	if defaultAgent == "" {
		defaultAgent = config.DefaultAgent
	}
	agents, err := buildAgentResolver(defaultAgent, log)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	ws, err := gitworktree.New(gitworktree.Options{
		// Per-session worktrees live under the data dir, so a single AO_DATA_DIR
		// override moves all durable per-user state together.
		ManagedRoot: filepath.Join(cfg.DataDir, "worktrees"),
		// Resolve each project's source repo from the projects table, so a
		// session spawned for a registered project materialises its worktree off
		// that repo. Unregistered projects fail loudly.
		RepoResolver: projectRepoResolver{store: store},
		// Read through the settings store on every teardown so toggling
		// artefact clearing takes effect without a daemon restart.
		ArtifactPatterns: func() []string {
			if reclaimSettings == nil {
				return nil
			}
			return reclaimSettings().Patterns()
		},
	})
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("session workspace: %w", err)
	}
	mgr := sessionmanager.New(sessionmanager.Deps{
		Runtime:      runtime,
		Agents:       agents,
		Workspace:    ws,
		Store:        store,
		Messenger:    messenger,
		Lifecycle:    lcm,
		DataDir:      cfg.DataDir,
		RunFile:      cfg.RunFilePath,
		Logger:       log,
		IdleCloseTTL: cfg.SessionIdleClose,
		// The orchestrator prompt reads this at spawn/restore so a toggle of the
		// global gate takes effect on the next (re)launch of an orchestrator.
		SpawnConfirmEnabled: func() bool { return spawnConfirm.Get().Enabled },
		// The worker/orchestrator base is assembled from these global overrides at
		// (re)launch, so an edit through the settings API takes effect on the next
		// spawn/restore without a daemon restart.
		PromptOverrides: func() promptoverrides.Overrides { return promptOverrides.Get() },
		// The global default response language is read at (re)launch and resolved
		// against the per-project override, so a settings edit takes effect on the
		// next spawn/restore.
		ResponseLanguage: func() string { return responseLang.Language() },
	})
	// When a worker's PR merges, the lifecycle reducer suspends it in place (card
	// stays on the board) and calls back here to tear its tmux down, mirroring the
	// idle sweep. Wired after the manager exists; lifecycle has no runtime of its
	// own (feature/merge-suspend-in-place).
	lcm.SetRuntimeSuspender(mgr.SuspendRuntime)
	// A crew is one task on one worktree. When the reducer terminates a dev
	// because its PR merged (or its issue closed) it writes one row and nothing
	// else - so without this its qa would keep running, awake, on a worktree
	// whose owner is gone. This hands the reducer the same subordinates-first
	// fan-out Teardown already gives kill, purge, shutdown and auto-reclaim.
	// A solo session has no crew and takes a no-op path.
	lcm.SetCrewReaper(mgr.TeardownCrewSubordinates)
	// The PR-claim path shares the observer's provider set so a claimed GitLab
	// merge request resolves through the same GitLab client + auth as background
	// observation, not GitHub alone. GitHub is always present; GitLab is added
	// when AO_GITLAB_HOST is configured (see buildSCMEntries).
	scmEntries, err := buildSCMEntries(log)
	if err != nil {
		logSCMProviderDisabled(log, err)
	}
	scmProvider := composite.New(scmEntries...)
	// msgRenderer renders editable dispatch templates (send-to-worker); the
	// closure reads promptOverrides live so an operator edit takes effect on
	// the next render without a daemon restart.
	msgRenderer := messagetemplates.NewRenderer(func() map[string]string { return promptOverrides.Get().Templates })
	sessionSvc := sessionsvc.NewWithDeps(sessionsvc.Deps{
		Manager:   mgr,
		Store:     store,
		PRClaimer: store,
		SCM:       scmProvider,
		Telemetry: telemetry,
		// no_signal only makes sense for harnesses whose adapters install
		// activity hooks; the deriver registry is the source of truth for that.
		SignalCapable: activitydispatch.SupportsHarness,
		Renderer:      msgRenderer,
		// Same window the idle sweep uses, so the read model's IdleCloseAt
		// countdown lines up with when a session would actually be suspended.
		IdleCloseTTL: cfg.SessionIdleClose,
	})
	// Triggering a review spawns a reviewer over the worker's worktree, resolved
	// from the reviewer registry (distinct from the worker agent set). The
	// reviewer posts its review to the PR itself, so the service needs no SCM
	// writer.
	reviewers, err := reviewer.NewResolver()
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("reviewer resolver: %w", err)
	}
	reviewEngine := reviewcore.New(reviewcore.Deps{
		Store:    store,
		Sessions: store,
		PRs:      store,
		Projects: store,
		Launcher: reviewcore.NewLauncher(reviewers, runtime),
		// The ephemeral reviewer is a READER of the worker's checkout, and both crew
		// members now write that checkout while it reads. It cannot refuse to start
		// (it would refuse forever), so it brackets its run with the same write-
		// generation detector qa uses and DISCARDS a pass the tree moved under. A
		// solo worker's tree has one writer - the session under review - so no
		// bracket is taken and Trigger is unchanged for it.
		Watcher: treeWatchers,
		// The reviewer base is assembled from the same global overrides at launch,
		// so an edit through the settings API takes effect on the next review run.
		PromptOverrides: func() promptoverrides.Overrides { return promptOverrides.Get() },
		// The reviewer's review comments follow the same language directive; resolve
		// the global default here (the per-project override is applied in Trigger).
		ResponseLanguage: func() string { return responseLang.Language() },
	})
	reviewSvc := reviewsvc.New(reviewEngine, store, reviewsvc.WithLifecycleReducer(lcm))
	// Tie the reviewer pane's lifetime to its worker: when the session manager
	// tears a worker down (kill/reclaim/delete), it closes the worker's reviewer
	// pane too, instead of leaving a keep-alive shell behind. Wired here — after
	// the review service exists — because the manager is built first.
	mgr.SetReviewerReaper(reviewSvc.TeardownReviewer)
	// The smoke service backs the Tests tab: per-session checklists + evidence
	// blobs under <dataDir>/evidence, and report-back over the same Send path
	// `ao send` uses (sessionSvc). Built after sessionSvc so it can deliver
	// results; its evidence-purge hook is wired into the manager like the
	// reviewer reaper so a purged session leaves no blobs behind. The jiraPoster
	// is the write seam behind the Tests tab's "Post to Jira" button (comment +
	// attachment upload); nil leaves the button's endpoint reporting Jira as
	// unconfigured rather than panicking.
	smokeSvc := smokesvc.New(store, cfg.DataDir, sessionSvc, smokesvc.WithJiraPoster(jiraPoster))
	mgr.SetSmokeEvidencePurger(smokeSvc.PurgeSessionEvidence)
	return sessionSvc, reviewSvc, smokeSvc, mgr, nil
}

// runtimeMessageSender is the narrow part of the concrete runtime needed by
// ao send. Both tmux.Runtime and conpty.Runtime implement this via SendMessage.
type runtimeMessageSender interface {
	SendMessage(ctx context.Context, handle ports.RuntimeHandle, message string) error
}

// messageHolder holds a message for a session that cannot receive it now.
// *msgqueue.Queue satisfies it; nil leaves the messenger with its old
// deliver-or-fail behavior.
type messageHolder interface {
	Enqueue(ctx context.Context, id domain.SessionID, body string) (domain.QueuedMessage, int, error)
}

// runtimeMessenger sends the user's message directly to the session's live
// runtime pane, or hands it to the queue when the session is asleep. The HTTP
// controller has already validated and sanitized the message body; this adapter
// only resolves the stored runtime handle.
type runtimeMessenger struct {
	store   *sqlite.Store
	runtime runtimeMessageSender
	queue   messageHolder
}

func (m runtimeMessenger) Send(ctx context.Context, id domain.SessionID, message string) (ports.SendOutcome, error) {
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil {
		return ports.SendOutcome{}, err
	}
	if !ok {
		return ports.SendOutcome{}, fmt.Errorf("session %s: %w", id, sessionmanager.ErrNotFound)
	}
	if rec.IsTerminated {
		// Deliberately NOT queued. A terminated session is one the user finished;
		// it comes back only if someone restores it, which may be never and is
		// nobody's plan. Holding a message for it would turn a clear, immediate
		// "this session is over" into a silent wait for a delivery that never
		// happens - the exact confusion the queue exists to remove.
		return ports.SendOutcome{}, fmt.Errorf("session %s: %w", id, sessionmanager.ErrTerminated)
	}
	if !rec.CanReceiveMessage() && m.queue != nil {
		// The session cannot take the message right now, for one of two reasons:
		//
		//   - SUSPENDED: the record and the worktree are intact but the tmux was
		//     reaped, so the stored handle points at a pane that no longer exists.
		//     Typing at it would fail and lose the message.
		//   - AT A PROMPT (waiting_input): the pane exists, but a permission dialog
		//     owns the keyboard. Text typed now is consumed by the dialog and the
		//     trailing Enter can ANSWER it — a nudge that silently approves a tool
		//     call is far worse than a late one.
		//
		// Either way the message is HELD, not dropped, and the queue delivers it
		// once the agent is genuinely listening again.
		stored, pending, qErr := m.queue.Enqueue(ctx, id, message)
		if qErr != nil {
			return ports.SendOutcome{}, qErr
		}
		return ports.SendOutcome{Queued: true, QueuedAt: stored.QueuedAt, Pending: pending}, nil
	}
	handleID := rec.Metadata.RuntimeHandleID
	if handleID == "" {
		return ports.SendOutcome{}, fmt.Errorf("session %s: %w", id, sessionmanager.ErrIncompleteHandle)
	}
	// A LIVE session whose send fails still fails loudly rather than queueing: the
	// session is not asleep, so a failure here means something is genuinely wrong
	// and the caller must hear about it now, not in an inbox.
	if err := m.runtime.SendMessage(ctx, ports.RuntimeHandle{ID: handleID}, message); err != nil {
		return ports.SendOutcome{}, err
	}
	return ports.SendOutcome{}, nil
}

// newSessionMessenger assembles the per-daemon agent messenger: submit the
// message to the live runtime pane, or hand it to queue when the session is
// suspended so it is delivered once the session's agent is listening again.
func newSessionMessenger(store *sqlite.Store, runtime runtimeMessageSender, queue messageHolder, _ *slog.Logger) ports.AgentMessenger {
	return runtimeMessenger{store: store, runtime: runtime, queue: queue}
}

// buildAgentRegistry returns a registry populated with the agent adapters the
// daemon ships, keyed by manifest id. Registration only fails on an
// empty/duplicate id — a programmer error, not a runtime condition.
// The shipped adapter list lives in the adapters/agent/registry package
// (registry.Constructors). Adding a new harness is a one-line edit there.
func buildAgentRegistry() (*adapters.Registry, error) {
	return agentregistry.Build()
}

// agentRegistry adapts the generic adapter Registry to ports.AgentResolver: it
// maps a session's harness onto the registered adapter of the same id and
// asserts that adapter drives an agent. Empty harnesses are invalid at the
// session manager boundary and deliberately do not resolve here.
type agentRegistry struct {
	reg *adapters.Registry
}

var _ ports.AgentResolver = agentRegistry{}

func (a agentRegistry) Agent(harness domain.AgentHarness) (ports.Agent, bool) {
	adapter, ok := a.reg.Get(string(harness))
	if !ok {
		return nil, false
	}
	agent, ok := adapter.(ports.Agent)
	return agent, ok
}

// buildAgentRegistryResolver builds a bare agent resolver over the shipped
// adapters, with none of buildAgentResolver's session-oriented validation
// (there is no configured default to check) and none of its logging. It is what
// a non-session surface — the Wiki's vault pane — resolves its harness through.
func buildAgentRegistryResolver() (ports.AgentResolver, error) {
	reg, err := buildAgentRegistry()
	if err != nil {
		return nil, err
	}
	return agentRegistry{reg: reg}, nil
}

// buildAgentResolver constructs the per-session agent resolver the Session
// Manager consumes (sessionmanager.Deps.Agents): a registry of the shipped
// adapters. It still validates AO_AGENT at startup for compatibility with the
// config surface, but worker/orchestrator spawns must provide a resolved
// harness before calling Agent.
func buildAgentResolver(defaultAgent string, log *slog.Logger) (ports.AgentResolver, error) {
	if defaultAgent == "" {
		defaultAgent = config.DefaultAgent
	}
	reg, err := buildAgentRegistry()
	if err != nil {
		return nil, err
	}
	resolver := agentRegistry{reg: reg}
	if _, ok := resolver.Agent(domain.AgentHarness(defaultAgent)); !ok {
		return nil, fmt.Errorf("configured default agent %q is not a registered adapter", defaultAgent)
	}
	ids := make([]string, 0)
	for _, mf := range reg.Manifests() {
		ids = append(ids, mf.ID)
	}
	log.Info("built per-session agent resolver", "default", defaultAgent, "registered", ids)
	return resolver, nil
}

// projectRepoResolver resolves a project's on-disk repo path from the projects
// table so gitworktree can materialise per-session worktrees off it. It replaces
// the empty StaticRepoResolver the daemon used before (which failed every
// lookup), turning a registered project into a spawnable one.
type projectRepoResolver struct{ store *sqlite.Store }

var _ gitworktree.RepoResolver = projectRepoResolver{}

func (r projectRepoResolver) RepoPath(projectID domain.ProjectID) (string, error) {
	rec, ok, err := r.store.GetProject(context.Background(), string(projectID))
	if err != nil {
		return "", fmt.Errorf("look up project %q: %w", projectID, err)
	}
	if !ok {
		return "", fmt.Errorf("no project registered with id %q — add one with `ao project add`: %w", projectID, sessionmanager.ErrProjectNotResolvable)
	}
	if !rec.ArchivedAt.IsZero() {
		return "", fmt.Errorf("project %q is archived: %w", projectID, sessionmanager.ErrProjectNotResolvable)
	}
	if rec.Path == "" {
		return "", fmt.Errorf("project %q has no repo path on record: %w", projectID, sessionmanager.ErrProjectNotResolvable)
	}
	return rec.Path, nil
}
