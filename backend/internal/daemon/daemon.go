// Package daemon owns the Agent Orchestrator backend process: config loading,
// loopback HTTP serving, durable storage, CDC fan-out, lifecycle wiring, and
// graceful shutdown.
package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/activity"
	jiraadapter "github.com/aoagents/agent-orchestrator/backend/internal/adapters/jira"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/runtime/runtimeselect"
	"github.com/aoagents/agent-orchestrator/backend/internal/autonudge"
	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/daemon/supervisor"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/evidenceretention"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/inputgate"
	"github.com/aoagents/agent-orchestrator/backend/internal/looptelemetry"
	"github.com/aoagents/agent-orchestrator/backend/internal/msgqueue"
	"github.com/aoagents/agent-orchestrator/backend/internal/notify"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/preview"
	"github.com/aoagents/agent-orchestrator/backend/internal/promptoverrides"
	"github.com/aoagents/agent-orchestrator/backend/internal/reclaimlog"
	"github.com/aoagents/agent-orchestrator/backend/internal/reclaimsettings"
	"github.com/aoagents/agent-orchestrator/backend/internal/responselang"
	"github.com/aoagents/agent-orchestrator/backend/internal/runfile"
	agentsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/agent"
	crewrunsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/crewrun"
	importsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/importer"
	jirasvc "github.com/aoagents/agent-orchestrator/backend/internal/service/jira"
	notificationsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/notification"
	projectsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/project"
	simsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/sim"
	"github.com/aoagents/agent-orchestrator/backend/internal/simgesture"
	"github.com/aoagents/agent-orchestrator/backend/internal/simstream"
	"github.com/aoagents/agent-orchestrator/backend/internal/skillassets"
	"github.com/aoagents/agent-orchestrator/backend/internal/spawnconfirm"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/terminal"
	"github.com/aoagents/agent-orchestrator/backend/internal/treewatch"
)

// Run starts the daemon and blocks until it exits. SIGINT/SIGTERM drive
// graceful shutdown through the HTTP server and background workers.
func Run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	log := newLogger()

	// Fail fast only if a daemon is genuinely still serving the recorded port.
	// CheckStale confirms the run-file's PID is alive, but that alone is not
	// proof a predecessor owns the port: the file leaks when the daemon is hard
	// killed without a graceful shutdown (the norm on Windows, where the desktop
	// supervisor can only TerminateProcess it), and Windows reuses the recorded
	// PID for unrelated processes. So a "live" PID is verified against an actual
	// /healthz probe; a run-file left by a crashed/hard-killed/reused-PID
	// predecessor is treated as stale and overwritten when the new server starts.
	if live, err := runfile.CheckStale(cfg.RunFilePath); err != nil {
		return fmt.Errorf("inspect run-file: %w", err)
	} else if live != nil && runFileOwnerServing(&http.Client{Timeout: staleProbeTimeout}, config.LoopbackHost, live) {
		return fmt.Errorf("daemon already running (pid %d, port %d); refusing to start", live.PID, live.Port)
	}

	// Open the durable store and bring up the CDC substrate: DB triggers capture
	// changes into change_log, the poller tails it, and the broadcaster fans
	// events out to live transports.
	store, err := sqlite.Open(cfg.DataDir)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer func() { _ = store.Close() }()

	// Refresh the embedded using-ao skill into the data dir so worker sessions
	// in any project can read the ao CLI catalog from a stable absolute path.
	// Non-fatal: the skill is an enhancement over `ao --help`, not required.
	if err := skillassets.Install(cfg.DataDir); err != nil {
		log.Warn("install using-ao skill", "err", err)
	}

	telemetrySink := newTelemetrySink(cfg, store, log)
	defer func() { _ = telemetrySink.Close(context.Background()) }()
	telemetrySink.Emit(context.Background(), ports.TelemetryEvent{
		Name:       "ao.daemon.started",
		Source:     "daemon",
		OccurredAt: time.Now().UTC(),
		Level:      ports.TelemetryLevelInfo,
		Payload: map[string]any{
			"port":  cfg.Port,
			"agent": cfg.Agent,
		},
	})

	// signal.NotifyContext cancels ctx on SIGINT/SIGTERM, which drives the
	// graceful shutdown inside Server.Run and stops the background goroutines.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cdcPipe, err := startCDC(ctx, store, log)
	if err != nil {
		return err
	}

	// Terminal streaming: the selected runtime (tmux on macOS/Linux, conpty on Windows) supplies the
	// attach Stream and liveness; the CDC broadcaster feeds the session-state channel. The manager
	// is handed to httpd, which mounts it at /mux. Raw PTY bytes never flow
	// through the CDC change_log -- only session-state events do.
	runtimeAdapter := runtimeselect.New(log)
	// The input gate couples message injection with live user typing. The terminal
	// mux records every client keystroke into it (WithInputRecorder); the gated
	// runtime consults it before SendMessage so an inbound message never merges
	// into — or submits — the line the user is mid-typing in the shared pane.
	inputGate := inputgate.New()
	gatedRuntime := newGatedRuntime(runtimeAdapter, inputGate)
	termMgr := terminal.NewManager(runtimeAdapter, cdcPipe.Broadcaster, log, terminal.WithInputRecorder(inputGate))
	defer termMgr.Close()

	// The simulator screen surface behind the desktop app's Simulator tab.
	// Nothing here starts a process: device discovery shells out on demand, and
	// a capture exists only while somebody is watching one. Shutting it down is
	// what guarantees no capture process outlives the daemon.
	simScreen := simstream.NewScreen(cfg.DataDir)
	defer simScreen.Shutdown()
	// The touches the desktop pane currently holds down. A drag spans several
	// requests, so it outlives any one of them - and a finger left down wedges a
	// device's input until it is rebooted, so the daemon lifts them on the way
	// out rather than leaving it to the watchdog it will not be alive to run.
	simDrags := simgesture.NewDrags()
	defer simDrags.Shutdown()

	// The durable inbox for sessions that cannot receive a message right now: a
	// suspended session's tmux is gone, so typing at its stored handle fails and
	// the message is lost, and a session sitting at a permission prompt has a
	// dialog on the keyboard that would eat the text and could be ANSWERED by its
	// trailing Enter. It delivers through the SAME gated runtime, so a held message
	// lands on an empty input line like any other. Built before the messenger,
	// which hands it anything addressed to a session that is not listening.
	// The readiness signal is the runtime's own agent-liveness probe, taken from
	// the UNWRAPPED adapter: gatedRuntime's method set is the union interface it
	// embeds, which does not carry AgentAlive (conpty cannot implement it), so
	// asking the wrapper for the capability would silently find nothing.
	messageQueue := msgqueue.New(store, gatedRuntime, agentLivenessProber(runtimeAdapter), log)
	// The agent messenger sends validated user input to the session's live
	// runtime pane, or queues it when the session cannot receive it. Built before the
	// Lifecycle Manager so the LCM can use it for SCM-driven agent nudges (CI
	// failure, review feedback, merge conflict). It injects through the gated
	// runtime so delivery waits for a typing gap.
	messenger := newSessionMessenger(store, gatedRuntime, messageQueue, log)
	notificationHub := notify.NewHub()
	// The activity feed is an in-memory, lossy push path for ephemeral per-tool
	// agent activity. It sits OUTSIDE the CDC pipeline on purpose: CDC carries
	// durable changes and fires only when activity_state actually changes, so it
	// cannot carry per-tool ticks. See internal/activity for the reasoning.
	activityHub := activity.NewHub()
	notifier := notificationsvc.New(notificationsvc.Deps{Store: store})
	notificationWriter := notify.New(notify.Deps{Store: store, Publisher: notificationHub})

	// The global system-prompt/message-template overrides are read by the
	// session manager (worker/orchestrator base), the review engine (reviewer
	// base), and the Lifecycle Manager (CI/review/merge-conflict/tracker
	// nudges) at (re)launch/observation time, and edited through the settings
	// API. Built before the Lifecycle Manager so its Templates getter can be
	// threaded into startLifecycle. A missing/corrupt file degrades to
	// built-in defaults (no overrides).
	promptOverrides, err := promptoverrides.NewStore(cfg.DataDir)
	if err != nil {
		stop()
		if cdcErr := cdcPipe.Stop(); cdcErr != nil {
			log.Error("cdc pipeline shutdown", "err", cdcErr)
		}
		return fmt.Errorf("prompt overrides: %w", err)
	}

	// The auto-nudge gate is a global setting the Lifecycle Manager will read to
	// decide whether to nudge the worker on unresolved review comments. Built
	// before startLifecycle so a later wiring can thread its getter in. A
	// missing/corrupt file degrades to OFF (no auto-nudge).
	autoNudge, err := autonudge.NewStore(cfg.DataDir)
	if err != nil {
		stop()
		if cdcErr := cdcPipe.Stop(); cdcErr != nil {
			log.Error("cdc pipeline shutdown", "err", cdcErr)
		}
		return fmt.Errorf("auto-nudge settings: %w", err)
	}

	// The global default human-facing response language is read at spawn/restore
	// and review-trigger time to inject the always-on language directive into every
	// agent kind's prompt (resolving a per-project override over this default). A
	// missing/corrupt file degrades to English (no directive).
	responseLangSettings, err := responselang.NewStore(cfg.DataDir)
	if err != nil {
		stop()
		if cdcErr := cdcPipe.Stop(); cdcErr != nil {
			log.Error("cdc pipeline shutdown", "err", cdcErr)
		}
		return fmt.Errorf("response-language settings: %w", err)
	}

	// loopReg tracks each fixed-interval background loop's last-run time so the
	// API can surface a live countdown to each loop's next run. In-memory only:
	// rebuilt on boot, forgotten on shutdown. Created before any loop starts so
	// every loop registers into the same registry.
	loopReg := looptelemetry.New(func() time.Time { return time.Now().UTC() })

	// Bring up the Lifecycle Manager and the reaper first: it makes the session
	// lifecycle write path live (reducer write -> store -> DB trigger ->
	// change_log -> poller -> broadcaster) and gives startSession the shared LCM.
	lcStack := startLifecycle(ctx, store, gatedRuntime, messenger, notificationWriter, telemetrySink, func() map[string]string { return promptOverrides.Get().Templates }, func() bool { return autoNudge.Get().Enabled }, loopReg, log)
	lcStack.scmDone = startSCMObserver(ctx, store, lcStack.LCM, loopReg, log)

	// The spawn-confirm gate is a global setting the orchestrator prompt reads at
	// spawn/restore time, so its store is built before the session manager and its
	// getter handed in. A missing/corrupt file degrades to ON (confirm).
	spawnConfirmSettings, err := spawnconfirm.NewStore(cfg.DataDir)
	if err != nil {
		stop()
		lcStack.Stop()
		if cdcErr := cdcPipe.Stop(); cdcErr != nil {
			log.Error("cdc pipeline shutdown", "err", cdcErr)
		}
		return fmt.Errorf("spawn-confirm settings: %w", err)
	}

	// One jira client backs the display read, the status transitions, cross-project
	// search, AND the smoke-results comment/attachment write — all over Jira Cloud
	// REST v3 (a single API-token auth path). It satisfies IssueReader,
	// TransitionMover, IssueSearcher, and smoke's JiraPoster. Built before the
	// session service so the smoke service can take it as its Jira write seam.
	jiraClient := jiraadapter.NewClient()

	// Wire the controller-facing session service over the same store + LCM, the
	// selected runtime, a gitworktree workspace, the per-session agent resolver
	// (AO_AGENT validated here for compatibility), and the agent messenger, then mount it
	// on the API.
	// Auto-reclaim settings are read BEFORE the session service because the
	// workspace adapter consults them during teardown, to decide whether
	// untracked build output may be cleared out of a finished worktree's way.
	reclaimSettings, err := reclaimsettings.NewStore(cfg.DataDir)
	if err != nil {
		stop()
		lcStack.Stop()
		if cdcErr := cdcPipe.Stop(); cdcErr != nil {
			log.Error("cdc pipeline shutdown", "err", cdcErr)
		}
		return fmt.Errorf("reclaim settings: %w", err)
	}

	// The tree-write detector. The registry holds a filesystem watch per worktree
	// only while a run is bracketed, so a solo session - and a crew that never
	// brackets - costs nothing here. Two consumers share it and therefore share one
	// counter per worktree: qa's `ao crew run` bracket, and a review pass over a
	// crew's shared checkout.
	treeWatchers := treewatch.NewRegistry(treewatch.Options{Logger: log})
	sessionSvc, reviewSvc, smokeSvc, sessMgr, err := startSession(cfg, gatedRuntime, store, lcStack.LCM, messenger, telemetrySink, spawnConfirmSettings, promptOverrides, responseLangSettings, jiraClient, reclaimSettings.Get, treeWatchers, log)
	if err != nil {
		stop()
		lcStack.Stop()
		if cdcErr := cdcPipe.Stop(); cdcErr != nil {
			log.Error("cdc pipeline shutdown", "err", cdcErr)
		}
		return fmt.Errorf("wire session service: %w", err)
	}
	lcStack.trackerDone = startTrackerIntake(ctx, store, sessionSvc, loopReg, log)

	// Auto-reclaim: a settings-backed poll loop that tears down finished worker
	// sessions (tmux + worktree, branch kept) once they have sat past the
	// configured grace period. Every decision it makes — reclaim or refusal — is
	// appended to the durable log under the data dir.
	reclaimAudit, err := reclaimlog.New(cfg.DataDir)
	if err != nil {
		stop()
		lcStack.Stop()
		if cdcErr := cdcPipe.Stop(); cdcErr != nil {
			log.Error("cdc pipeline shutdown", "err", cdcErr)
		}
		return fmt.Errorf("reclaim log: %w", err)
	}
	reclaimerDone := startReclaimer(ctx, sessionSvc, reclaimSettings, reclaimAudit, filepath.Join(cfg.DataDir, "worktrees"), loopReg, log)

	// Evidence retention: a settings-backed store + sweeper that purges smoke-test
	// evidence blobs older than the configured TTL (default 30 days, from each
	// row's created_at). Constructed here so a settings-store failure is cleaned up
	// the same way as above. The sweeper is shared by the manual-trigger endpoint
	// and the periodic background sweep started below.
	evidenceRetentionSettings, err := evidenceretention.NewStore(cfg.DataDir)
	if err != nil {
		stop()
		lcStack.Stop()
		if cdcErr := cdcPipe.Stop(); cdcErr != nil {
			log.Error("cdc pipeline shutdown", "err", cdcErr)
		}
		return fmt.Errorf("evidence retention settings: %w", err)
	}
	evidenceSweep := &evidenceSweeper{
		settings: evidenceRetentionSettings,
		purge:    smokeSvc.PurgeEvidenceOlderThan,
		clock:    func() time.Time { return time.Now().UTC() },
		log:      log,
	}

	// The run bracket that reads the tree-write detector. Runs left open by a
	// previous process are closed as UNCERTIFIED at boot: their watchers died with
	// that process, and a run nothing watched can never be certified after the fact.
	crewRunSvc := crewrunsvc.New(crewrunsvc.Options{Store: store, Watcher: treeWatchers, Logger: log})
	if err := crewRunSvc.ReconcileOpenRuns(ctx); err != nil {
		log.Warn("crew runs: could not close runs left open by a previous daemon", "err", err)
	}
	defer treeWatchers.Close()

	previewDone := preview.NewPoller(store, sessionSvc, "http://"+cfg.Addr(), preview.PollerConfig{Logger: log}).Start(ctx)
	// Per-session token telemetry: a background poll that reads claude-code session
	// transcripts and persists token/cost totals on the session row (additive; never
	// blocks lifecycle). Non-claude sessions are skipped (no chip).
	tokenUsageDone := startTokenUsageObserver(ctx, store, loopReg, log)
	agentSvc := agentsvc.New()
	go func() {
		if _, err := agentSvc.Refresh(ctx); err != nil {
			log.Warn("initial agent catalog refresh failed", "err", err)
		}
	}()

	// One simulator per session, exported into the agent's environment at spawn.
	// Wired here rather than in startSession because the assigner needs the
	// daemon's resident device listing (simScreen), which is built for the Device
	// tab and whose cache means a spawn pays a map lookup rather than a `simctl
	// list` subprocess.
	simAssigner := simsvc.NewAssigner(store, simScreen, func() time.Time { return time.Now().UTC() })
	sessMgr.SetSimDeviceAssigner(func(ctx context.Context, id domain.SessionID) (string, error) {
		assignment, err := simAssigner.AssignDevice(ctx, id)
		return assignment.UDID, err
	})

	// sessionSvc is the Jira SessionGateway (read + set the after-the-fact binding).
	srv, err := httpd.NewWithDeps(cfg, log, termMgr, httpd.APIDeps{
		Projects:           projectsvc.NewWithDeps(projectsvc.Deps{Store: store, Sessions: sessionSvc, DefaultHarness: domain.AgentHarness(cfg.Agent), Telemetry: telemetrySink}),
		Agents:             agentSvc,
		Sessions:           sessionSvc,
		Jira:               jirasvc.New(sessionSvc, jiraClient, jiraClient, jiraClient),
		Reviews:            reviewSvc,
		Smoke:              smokeSvc,
		CrewRuns:           crewRunSvc,
		Sim:                newSimService(store, simScreen, sessMgr),
		SimScreen:          simScreen,
		SimDrags:           simDrags,
		Notifications:      notifier,
		NotificationStream: notificationHub,
		ActivityFeed:       activityHub,
		ActivityStream:     activityHub,
		Import:             importsvc.New(importsvc.Deps{Store: store}),
		CDC:                store,
		Events:             cdcPipe.Broadcaster,
		Activity:           lcStack.LCM,
		Telemetry:          telemetrySink,
		Settings:           reclaimSettings,
		SpawnConfirm:       spawnConfirmSettings,
		AutoNudge:          autoNudge,
		ResponseLanguage:   responseLangSettings,
		EvidenceRetention:  evidenceRetentionSettings,
		EvidenceSweeper:    evidenceSweep,
		SystemPrompts:      promptOverrides,
		MessageTemplates:   promptOverrides,
		LoopTelemetry:      loopReg,
	})
	if err != nil {
		stop()
		<-previewDone
		<-reclaimerDone
		<-tokenUsageDone
		lcStack.Stop()
		if cdcErr := cdcPipe.Stop(); cdcErr != nil {
			log.Error("cdc pipeline shutdown", "err", cdcErr)
		}
		return err
	}

	// Reconcile sessions on boot: adopt crash-surviving runtimes, capture and
	// terminate dead ones, reap leaked tmux, then restore shutdown-saved
	// sessions. Best-effort: a failure is logged but never blocks boot. Placed
	// before srv.Run so sessions are consistent before the server serves.
	if reconcileErr := sessMgr.Reconcile(ctx); reconcileErr != nil {
		log.Error("reconcile sessions on boot failed", "err", reconcileErr)
	}

	// A queued message still marked "delivering" was in flight when the previous
	// daemon died: the runtime may already have typed it into the pane, so it is
	// failed (kept, visible) rather than re-sent. Losing one message loudly beats
	// giving an agent the same instruction twice.
	if _, recoverErr := messageQueue.RecoverInFlight(ctx); recoverErr != nil {
		log.Error("queued message recovery on boot failed", "err", recoverErr)
	}

	if reviewSvc != nil {
		// Close reviewer panes whose worker has ended or was killed while the daemon
		// was down: reviewers have no session row, so no session-reconcile pass ever
		// reaps them and they otherwise linger forever as keep-alive shells. Run
		// before ReconcileOrphanedRuns so a terminal worker's pane is destroyed
		// first and its now-orphaned run is failed in the same boot. Best-effort.
		if reaped, reapErr := reviewSvc.ReapOrphanedReviewers(ctx); reapErr != nil {
			log.Error("reap orphaned reviewer panes on boot failed", "err", reapErr)
		} else if reaped > 0 {
			log.Info("reaped orphaned reviewer panes on boot", "reaped", reaped)
		}

		// Fail review runs left "running" by a reviewer that died out of band (or did
		// not survive this restart), so a board stuck on "Reviewing…" unsticks without
		// a manual trigger. Best-effort; never blocks boot.
		if failed, reconcileErr := reviewSvc.ReconcileOrphanedRuns(ctx); reconcileErr != nil {
			log.Error("reconcile orphaned review runs on boot failed", "err", reconcileErr)
		} else if failed > 0 {
			log.Info("reconciled orphaned review runs on boot", "failed", failed)
		}
	}

	// ponytail: 5s tolerates a brief frontend restart; tune if dev hot-reload trips it.
	const supervisorGrace = 5 * time.Second

	if ln, addr, err := supervisor.Listen(cfg.RunFilePath); err != nil {
		// Non-fatal: without the link the daemon still works (e.g. headless "ao start"),
		// it just will not auto-stop when a frontend dies. Do not block startup on it.
		log.Warn("supervisor: listener unavailable; frontend-death auto-stop disabled", "err", err)
	} else {
		log.Info("supervisor: listening", "addr", addr)
		sup := supervisor.New(supervisorGrace, srv.RequestShutdown, log)
		go func() {
			if err := sup.Serve(ctx, ln); err != nil {
				log.Warn("supervisor: serve stopped with error", "err", err)
			}
		}()
	}

	// Auto-close idle sessions while the daemon runs. Disabled (interval 0) when
	// AO_SESSION_IDLE_CLOSE <= 0; boot-time closing already ran inside Reconcile.
	sweepInterval := time.Duration(0)
	if cfg.SessionIdleClose > 0 {
		sweepInterval = idleSweepIntervalDefault
	}
	idleRec := loopReg.Register(looptelemetry.Spec{
		Name:        "idle-sweep",
		Display:     "Auto-close idle",
		Description: "Scans for idle sessions and closes them once past the idle TTL.",
		Interval:    sweepInterval,
	})
	idleSweepDone := startTickerSweep(ctx, "idle session sweep", sweepInterval, func(ctx context.Context) error {
		idleRec.Tick()
		return sessMgr.CloseIdleSessions(ctx)
	}, log)

	// Deliver messages held for sessions that could not receive them. The sweep
	// is the whole delivery path: it is what survives a restart, and re-checking
	// on a tick is also what makes "the session is still coming up" a non-event
	// rather than a race - the message simply lands on a later tick.
	queueRec := loopReg.Register(looptelemetry.Spec{
		Name:        "message-queue",
		Display:     "Deliver held messages",
		Description: "Delivers messages queued for sessions that were asleep, once their agent is listening again.",
		Interval:    msgQueueSweepInterval,
	})
	queueSweepDone := startTickerSweep(ctx, "queued message delivery", msgQueueSweepInterval, func(ctx context.Context) error {
		queueRec.Tick()
		return messageQueue.Drain(ctx)
	}, log)

	// Keep every live orchestrator's worktree on its project's default branch.
	// Spawn and restore already sync at startup; this covers the drift in
	// between, because an orchestrator session runs for days while the default
	// branch moves under it, and an orchestrator reading stale code answers
	// questions about the codebase wrongly. Worker worktrees are never touched.
	orchSyncRec := loopReg.Register(looptelemetry.Spec{
		Name:        "orchestrator-worktree-sync",
		Display:     "Orchestrator code refresh",
		Description: "Fast-forwards each live orchestrator's worktree to its project's default branch.",
		Interval:    orchestratorSyncIntervalDefault,
	})
	orchSyncDone := startTickerSweep(ctx, "orchestrator worktree sync", orchestratorSyncIntervalDefault, func(ctx context.Context) error {
		orchSyncRec.Tick()
		return sessMgr.SyncOrchestratorWorkspaces(ctx)
	}, log)

	// Age-based evidence retention: periodic sweep (plus an immediate first run)
	// that purges evidence past the configured TTL. Self-disables via settings, so
	// it always starts; a disabled policy just makes each tick a no-op.
	evidenceRec := loopReg.Register(looptelemetry.Spec{
		Name:        "evidence-sweep",
		Display:     "Evidence TTL purge",
		Description: "Purges smoke-test evidence blobs older than the retention TTL.",
		Interval:    evidenceSweepIntervalDefault,
	})
	evidenceSweepDone := startEvidenceRetentionSweep(ctx, evidenceSweepIntervalDefault, func(ctx context.Context) error {
		evidenceRec.Tick()
		_, _, err := evidenceSweep.SweepEvidenceNow(ctx)
		return err
	}, log)

	runErr := srv.Run(ctx)

	// Both graceful shutdown paths (SIGTERM and POST /shutdown) funnel through
	// srv.Run returning. We deliberately do NOT tear down sessions here: they
	// survive the daemon exit and the next boot's Reconcile adopts them,
	// preserving session IDs. The narrowed sessionLifecycle interface makes
	// teardown-on-shutdown a compile error.

	// Shut the background goroutines down in order: cancel the context FIRST so
	// their loops exit, then wait for them to drain. Doing this explicitly (not
	// via defer) avoids the LIFO trap where a Stop() that blocks on ctx-cancel
	// runs before the cancel: a non-signal exit path would hang otherwise.
	stop()
	<-previewDone
	<-idleSweepDone
	<-queueSweepDone
	<-orchSyncDone
	<-evidenceSweepDone
	<-reclaimerDone
	<-tokenUsageDone
	lcStack.Stop()
	if err := cdcPipe.Stop(); err != nil {
		log.Error("cdc pipeline shutdown", "err", err)
	}
	return runErr
}

// newLogger returns the daemon's slog logger. It writes to stderr so supervisors
// can capture it separately from any structured stdout protocol added later.
func newLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
}
