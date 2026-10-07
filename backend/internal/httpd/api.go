package httpd

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/aoagents/agent-orchestrator/backend/internal/cdc"
	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	crewrunsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/crewrun"
	iosrunsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/iosrun"
	prsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/pr"
	projectsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/project"
	reviewsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/review"
	simsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/sim"
	"github.com/aoagents/agent-orchestrator/backend/internal/simbridge"
	"github.com/aoagents/agent-orchestrator/backend/internal/simctl"
	"github.com/aoagents/agent-orchestrator/backend/internal/simgesture"
	"github.com/aoagents/agent-orchestrator/backend/internal/simkeyboard"
	"github.com/aoagents/agent-orchestrator/backend/internal/simpaste"
	"github.com/aoagents/agent-orchestrator/backend/internal/simpower"
	"github.com/aoagents/agent-orchestrator/backend/internal/simstream"
	"github.com/aoagents/agent-orchestrator/backend/internal/simtrust"
)

// APIDeps bundles every service the API layer's controllers depend on.
type APIDeps struct {
	Agents   controllers.AgentCatalog
	Projects projectsvc.Manager
	Sessions controllers.SessionService
	Activity controllers.ActivityRecorder
	Jira     controllers.JiraService
	PRs      prsvc.ActionManager
	Reviews  reviewsvc.Manager
	// Testiny is a task's Testiny tab: the test runs linked to the task, read
	// live from Testiny. nil answers 501.
	Testiny controllers.TestinyService
	Sim     simsvc.Manager
	// IOSRun is the run bar above the terminal: what a session can build, and
	// the pane `ao sim run` runs in. nil answers 501, which is right on a
	// machine with no Xcode - the bar then renders nowhere.
	IOSRun iosrunsvc.Manager
	// CrewRuns is the bracket a crew member puts around a build or a test run -
	// the tree-write detector's two readings, and the "this member is running
	// something right now" signal that falls out of them. nil answers 501, which
	// is what a daemon with no detector should say rather than a quiet success.
	CrewRuns crewrunsvc.Manager
	// Children is the lifecycle of a worker's child worktrees, driven by the
	// Claude Code hooks a worker's subagents fire. nil answers 501.
	Children controllers.ChildrenService
	// Scripts is each mobileScripts workspace's own worktree of the scripts
	// store: its status and the publish into the store. nil answers 501.
	Scripts controllers.ScriptsService
	// SimScreen is the machine-local simulator surface behind the desktop app's
	// Simulator tab: device discovery, the live frame stream, and the driver a
	// click goes through. nil on a machine that cannot capture or touch a
	// simulator, and every route then answers 501.
	SimScreen SimScreen
	// SimVideo records a simulator's screen to a file, behind `ao sim record`.
	// It is its own dependency rather than part of Sim because it owns a
	// process that outlives every request: nil answers 501, which is what a
	// daemon that cannot spawn a recorder should say.
	SimVideo controllers.SimVideoService
	// SimDrags is the touches currently held down by the desktop pane. It is
	// per-daemon because one drag spans several requests, and the daemon owns
	// its lifetime so no finger is left down when the process goes away.
	SimDrags *simgesture.Drags
	// SimRunner is the warm XCTest runner behind `ao sim ax` and `ao sim type`: one
	// runner per simulator a session holds. nil on a machine without Xcode,
	// and the hierarchy route then answers "unavailable" so the CLI reads
	// through the accessibility bridge instead.
	SimRunner controllers.SimRunner
	// SimProfiles resolves a boot's slimming profile. Left nil, the router
	// builds one over Sessions and Projects; a test sets it to control the
	// answer without standing up either service.
	SimProfiles controllers.SimProfileResolver
	// SimTrust is the global list of root CAs AO makes a simulator trust on
	// boot and claim. nil trusts nothing and leaves the settings route
	// answering 501.
	SimTrust *simtrust.Store
	// SimTrustFiles resolves a session's root CAs. Left nil, the router builds
	// one over Sessions, Projects and SimTrust.
	SimTrustFiles controllers.SimTrustResolver
	// SimAssignments is which simulator each session was given at spawn, read
	// by `ao sim doctor`. nil reads every session as having none.
	SimAssignments     SimAssignments
	Notifications      controllers.NotificationService
	NotificationStream controllers.NotificationStream
	// ActivityFeed publishes curated per-session activity events; ActivityStream
	// is the SSE subscription side. Both are satisfied by *activity.Hub.
	ActivityFeed     controllers.ActivityFeed
	ActivityStream   controllers.ActivityStream
	Import           controllers.ImportService
	Settings         controllers.SettingsService
	SpawnConfirm     controllers.SpawnConfirmService
	AutoNudge        controllers.AutoNudgeService
	ResponseLanguage controllers.ResponseLanguageService
	// Wiki is the personal note vault destination: the global vault-path
	// setting, plus the one agent pane that runs inside it. It is deliberately
	// not a session, so it has no lifecycle wiring of its own.
	WikiSettings     controllers.WikiSettingsService
	RefLinks         controllers.RefLinksService
	Wiki             controllers.WikiService
	SystemPrompts    controllers.SystemPromptsService
	MessageTemplates controllers.MessageTemplatesService
	CDC              cdc.Source
	Events           cdcSubscriber
	Telemetry        ports.EventSink
	LoopTelemetry    controllers.LoopTelemetrySource
	// Learning is learning capture: the transcript bookkeeping agent hooks
	// report, and the read-only view of what capture stored.
	Learning controllers.LearningService
	// LearningRules is the standing-rules corpus learning checks lessons
	// against, and the rules the human pinned.
	LearningRules controllers.LearningRulesService
	// LearningDecide draws proposals from finished tasks and lists them.
	LearningDecide controllers.LearningDecideService
}

// API owns one controller per resource and is the single Register call the
// router invokes to mount the /api/v1 surface.
type API struct {
	cfg            config.Config
	agents         *controllers.AgentsController
	projects       *controllers.ProjectsController
	sessions       *controllers.SessionsController
	jira           *controllers.JiraController
	prs            *controllers.PRsController
	reviews        *controllers.ReviewsController
	testiny        *controllers.TestinyController
	iosRun         *controllers.IOSRunController
	crewRuns       *controllers.CrewRunsController
	children       *controllers.ChildrenController
	scripts        *controllers.ScriptsController
	sim            *controllers.SimController
	simFlows       *controllers.SimFlowsController
	simVideo       *controllers.SimVideoController
	simScreen      *controllers.SimScreenController
	simHierarchy   *controllers.SimHierarchyController
	simType        *controllers.SimTypeController
	simDoctor      *controllers.SimDoctorController
	notifications  *controllers.NotificationsController
	activity       *controllers.ActivityController
	imports        *controllers.ImportController
	settings       *controllers.SettingsController
	wiki           *controllers.WikiController
	daemon         *controllers.DaemonController
	learning       *controllers.LearningController
	learningRules  *controllers.LearningRulesController
	learningDecide *controllers.LearningDecideController
	events         *EventsController
}

// NewAPI constructs the API surface from its dependencies. cfg carries the
// per-request timeout so the REST group can apply it without re-reading the
// environment.
func NewAPI(cfg config.Config, deps APIDeps) *API {
	simProfileResolver := deps.SimProfiles
	if simProfileResolver == nil && deps.Sessions != nil && deps.Projects != nil {
		simProfileResolver = simProfiles{sessions: deps.Sessions, projects: deps.Projects}
	}
	simTrustResolver := deps.SimTrustFiles
	if simTrustResolver == nil && deps.SimTrust != nil && deps.Sessions != nil && deps.Projects != nil {
		simTrustResolver = simTrustFiles{sessions: deps.Sessions, projects: deps.Projects, global: deps.SimTrust}
	}
	return &API{
		cfg: cfg,
		agents: &controllers.AgentsController{
			Catalog: deps.Agents,
		},
		projects: &controllers.ProjectsController{
			Mgr: deps.Projects,
		},
		sessions: &controllers.SessionsController{
			Svc:      deps.Sessions,
			Activity: deps.Activity,
			Feed:     deps.ActivityFeed,
		},
		jira:           &controllers.JiraController{Svc: deps.Jira},
		prs:            &controllers.PRsController{Svc: deps.PRs},
		reviews:        &controllers.ReviewsController{Svc: deps.Reviews},
		testiny:        &controllers.TestinyController{Svc: deps.Testiny},
		iosRun:         &controllers.IOSRunController{Svc: deps.IOSRun},
		crewRuns:       &controllers.CrewRunsController{Svc: deps.CrewRuns, Tasks: deps.Sessions},
		children:       &controllers.ChildrenController{Svc: deps.Children},
		scripts:        &controllers.ScriptsController{Svc: deps.Scripts},
		sim:            &controllers.SimController{Svc: deps.Sim, DataDir: cfg.DataDir, Screen: screenProvider(deps.SimScreen), Trust: simTrustResolver},
		simFlows:       &controllers.SimFlowsController{DataDir: cfg.DataDir},
		simVideo:       &controllers.SimVideoController{Svc: deps.SimVideo},
		simScreen:      &controllers.SimScreenController{Screen: screenProvider(deps.SimScreen), Leases: deps.Sim, Drags: deps.SimDrags, Profiles: simProfileResolver, Trust: simTrustResolver},
		simHierarchy:   &controllers.SimHierarchyController{Runner: deps.SimRunner},
		simType:        &controllers.SimTypeController{Runner: deps.SimRunner, Leases: deps.Sim, Screen: screenProvider(deps.SimScreen)},
		simDoctor:      &controllers.SimDoctorController{Sessions: deps.Sessions, Readers: simDoctorReaders(deps, simTrustResolver)},
		notifications:  &controllers.NotificationsController{Svc: deps.Notifications, Stream: deps.NotificationStream},
		activity:       &controllers.ActivityController{Stream: deps.ActivityStream},
		imports:        &controllers.ImportController{Svc: deps.Import},
		settings:       &controllers.SettingsController{Svc: deps.Settings, SpawnConfirm: deps.SpawnConfirm, AutoNudge: deps.AutoNudge, ResponseLanguage: deps.ResponseLanguage, Wiki: deps.WikiSettings, RefLinks: deps.RefLinks, SimTrust: simTrustSettings(deps.SimTrust), SystemPrompts: deps.SystemPrompts, MessageTemplates: deps.MessageTemplates},
		wiki:           &controllers.WikiController{Svc: deps.Wiki},
		daemon:         &controllers.DaemonController{Loops: deps.LoopTelemetry},
		learning:       &controllers.LearningController{Svc: deps.Learning},
		learningRules:  &controllers.LearningRulesController{Svc: deps.LearningRules},
		learningDecide: &controllers.LearningDecideController{Svc: deps.LearningDecide},
		events:         &EventsController{Source: deps.CDC, Live: deps.Events},
	}
}

// Register mounts the bounded /api/v1 REST surface. Long-lived surfaces such
// as muxed terminal streams stay outside this timeout group.
func (a *API) Register(root chi.Router) {
	timeout := a.cfg.RequestTimeout
	if timeout <= 0 {
		timeout = config.DefaultRequestTimeout
	}

	root.Route("/api/v1", func(r chi.Router) {
		// Serve the OpenAPI document from the same origin as the routes it describes.
		r.Get("/openapi.yaml", apispec.ServeYAML)

		r.Group(func(r chi.Router) {
			r.Use(middleware.Timeout(timeout))
			// Normalise the session id BEFORE anything reads it, so a Claude
			// Code session name works anywhere an AO id does. It has to sit
			// above TaskScoped (which reads the same parameter) and above every
			// controller; on a route with no {sessionId} it costs nothing.
			// Optional capability, reached by assertion like every other one:
			// a controller test that wires a minimal session service simply
			// does not get alias resolution, rather than being forced to grow
			// a method it has no use for.
			if resolver, ok := a.sessions.Svc.(controllers.SessionAliasResolver); ok {
				r.Use(controllers.SessionAlias(resolver))
			}
			a.agents.Register(r)
			a.projects.Register(r)
			a.sessions.Register(r)
			a.jira.Register(r)
			a.prs.Register(r)
			a.crewRuns.Register(r)
			a.children.Register(r)
			a.scripts.Register(r)
			a.sim.Register(r)
			a.simFlows.Register(r)
			a.simVideo.Register(r)
			a.simScreen.Register(r)
			a.simHierarchy.Register(r)
			a.simType.Register(r)
			a.simDoctor.Register(r)
			a.notifications.Register(r)
			a.imports.Register(r)
			a.settings.Register(r)
			a.wiki.Register(r)
			// Agent-scoped deliberately, not task-scoped. A run takes the
			// CALLING session's simulator lease and installs on the device that
			// session was assigned, so a crew's dev and qa each run their own
			// build on their own device. Task-scoping it would claim qa's
			// device under dev's id, which is the one thing the lease exists to
			// make impossible. The two members do share a worktree and so one
			// DerivedData - that hazard is `ao crew run`'s to warn about, and
			// turning it into a silent one-pane-per-task rule here would hide it.
			a.iosRun.Register(r)
			a.daemon.Register(r)
			// Agent-scoped: a transcript belongs to the session whose hook
			// reported it, never to its crewmate.
			a.learning.Register(r)
			a.learningRules.Register(r)
			a.learningDecide.Register(r)
			// Sibling REST controllers plug in here.

			// THE TASK-SCOPED SURFACES, and the only place that list lives.
			//
			// What these controllers own belongs to the TASK, not to the agent whose
			// id the path names: the branch's pull request and its comment threads,
			// AO's review verdicts on it, and the Testiny runs its cases were played
			// in. A crew's two members share one of each, so both must be answered
			// the same - reading them per-session is what left qa with a readiness
			// strip that saw no pull request at all.
			//
			// Everything above stays agent-scoped, which is the safe default: a
			// task-level surface left out of this group merely keeps today's
			// behaviour, while an agent-level one swept in would deliver qa's
			// message, or qa's kill, to dev. Mount by CONTROLLER wherever every
			// route it owns is task-scoped, so a route added later inherits the
			// scope instead of having to remember it.
			r.Group(func(r chi.Router) {
				r.Use(controllers.TaskScoped(a.sessions.Svc))
				a.reviews.Register(r)
				a.testiny.Register(r)
				a.sessions.RegisterTaskScoped(r)
			})
		})
		// Long-lived streams intentionally bypass the REST timeout middleware.
		a.notifications.RegisterStream(r)
		a.activity.RegisterStream(r)
		a.events.Register(r)
	})
}

// notFoundJSON returns the locked envelope for unmatched routes. Chi's default
// 404 is a text/plain body; the API surface must answer JSON so consumers can
// parse it uniformly.
func notFoundJSON(w http.ResponseWriter, r *http.Request) {
	envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "ROUTE_NOT_FOUND",
		r.Method+" "+r.URL.Path+" has no handler", nil)
}

// methodNotAllowedJSON returns the locked envelope when a method probes a
// known path without a matching verb (e.g. PUT /projects/{id} after we drop
// the legacy PUT alias).
func methodNotAllowedJSON(w http.ResponseWriter, r *http.Request) {
	envelope.WriteAPIError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "METHOD_NOT_ALLOWED",
		r.Method+" not allowed on "+r.URL.Path, nil)
}

// SimScreen is the daemon-side simulator screen surface. It is declared here
// rather than taken from the controller package so wiring code and tests name
// one type; *simstream.Screen satisfies it.
type SimScreen interface {
	Devices(ctx context.Context) (simctl.Listing, error)
	Subscribe(ctx context.Context, udid string) (<-chan simstream.Event, error)
	Driver(ctx context.Context) (simbridge.Driver, error)
	Keyboard(ctx context.Context, udid string) (simkeyboard.Mode, error)
	Pasteboard() simpaste.Pasteboard
	StartPower(ctx context.Context, udid string, op simpower.Op, setup *simpower.Setup, done func()) error
	PowerStatus() map[string]simpower.Status
	ClearPower(udid string)
	Truster() *simtrust.Truster
}

// screenProvider converts a nil interface value to a nil controller dependency.
// A typed nil hiding inside a non-nil interface would make the 501 checks pass
// and then panic, which is the opposite of degrading honestly.
func screenProvider(s SimScreen) controllers.SimScreenProvider {
	if s == nil {
		return nil
	}
	return s
}

// simTrustSettings converts a nil store to a nil controller dependency, for the
// reason screenProvider does: a typed nil inside an interface passes the 501
// check and then panics.
func simTrustSettings(s *simtrust.Store) controllers.SimTrustSettingsService {
	if s == nil {
		return nil
	}
	return s
}
