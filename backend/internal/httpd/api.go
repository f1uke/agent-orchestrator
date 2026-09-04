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
	prsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/pr"
	projectsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/project"
	reviewsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/review"
	simsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/sim"
	smokesvc "github.com/aoagents/agent-orchestrator/backend/internal/service/smoke"
	"github.com/aoagents/agent-orchestrator/backend/internal/simbridge"
	"github.com/aoagents/agent-orchestrator/backend/internal/simctl"
	"github.com/aoagents/agent-orchestrator/backend/internal/simgesture"
	"github.com/aoagents/agent-orchestrator/backend/internal/simkeyboard"
	"github.com/aoagents/agent-orchestrator/backend/internal/simpaste"
	"github.com/aoagents/agent-orchestrator/backend/internal/simpower"
	"github.com/aoagents/agent-orchestrator/backend/internal/simslim"
	"github.com/aoagents/agent-orchestrator/backend/internal/simstream"
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
	Smoke    smokesvc.Manager
	Sim      simsvc.Manager
	// CrewRuns is the bracket a crew member puts around a build or a test run -
	// the tree-write detector's two readings, and the "this member is running
	// something right now" signal that falls out of them. nil answers 501, which
	// is what a daemon with no detector should say rather than a quiet success.
	CrewRuns crewrunsvc.Manager
	// SimScreen is the machine-local simulator surface behind the desktop app's
	// Simulator tab: device discovery, the live frame stream, and the driver a
	// click goes through. nil on a machine that cannot capture or touch a
	// simulator, and every route then answers 501.
	SimScreen SimScreen
	// SimDrags is the touches currently held down by the desktop pane. It is
	// per-daemon because one drag spans several requests, and the daemon owns
	// its lifetime so no finger is left down when the process goes away.
	SimDrags *simgesture.Drags
	// SimProfiles resolves a boot's slimming profile. Left nil, the router
	// builds one over Sessions and Projects; a test sets it to control the
	// answer without standing up either service.
	SimProfiles        controllers.SimProfileResolver
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
	WikiSettings      controllers.WikiSettingsService
	Wiki              controllers.WikiService
	EvidenceRetention controllers.EvidenceRetentionService
	EvidenceSweeper   controllers.EvidenceSweeper
	SystemPrompts     controllers.SystemPromptsService
	MessageTemplates  controllers.MessageTemplatesService
	CDC               cdc.Source
	Events            cdcSubscriber
	Telemetry         ports.EventSink
	LoopTelemetry     controllers.LoopTelemetrySource
}

// API owns one controller per resource and is the single Register call the
// router invokes to mount the /api/v1 surface.
type API struct {
	cfg           config.Config
	agents        *controllers.AgentsController
	projects      *controllers.ProjectsController
	sessions      *controllers.SessionsController
	jira          *controllers.JiraController
	prs           *controllers.PRsController
	reviews       *controllers.ReviewsController
	smoke         *controllers.SmokeController
	crewRuns      *controllers.CrewRunsController
	sim           *controllers.SimController
	simFlows      *controllers.SimFlowsController
	simScreen     *controllers.SimScreenController
	notifications *controllers.NotificationsController
	activity      *controllers.ActivityController
	imports       *controllers.ImportController
	settings      *controllers.SettingsController
	wiki          *controllers.WikiController
	daemon        *controllers.DaemonController
	events        *EventsController
}

// NewAPI constructs the API surface from its dependencies. cfg carries the
// per-request timeout so the REST group can apply it without re-reading the
// environment.
func NewAPI(cfg config.Config, deps APIDeps) *API {
	simProfileResolver := deps.SimProfiles
	if simProfileResolver == nil && deps.Sessions != nil && deps.Projects != nil {
		simProfileResolver = simProfiles{sessions: deps.Sessions, projects: deps.Projects}
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
		jira:          &controllers.JiraController{Svc: deps.Jira},
		prs:           &controllers.PRsController{Svc: deps.PRs},
		reviews:       &controllers.ReviewsController{Svc: deps.Reviews},
		smoke:         &controllers.SmokeController{Svc: deps.Smoke},
		crewRuns:      &controllers.CrewRunsController{Svc: deps.CrewRuns},
		sim:           &controllers.SimController{Svc: deps.Sim, DataDir: cfg.DataDir, Screen: screenProvider(deps.SimScreen)},
		simFlows:      &controllers.SimFlowsController{DataDir: cfg.DataDir},
		simScreen:     &controllers.SimScreenController{Screen: screenProvider(deps.SimScreen), Leases: deps.Sim, Drags: deps.SimDrags, Profiles: simProfileResolver},
		notifications: &controllers.NotificationsController{Svc: deps.Notifications, Stream: deps.NotificationStream},
		activity:      &controllers.ActivityController{Stream: deps.ActivityStream},
		imports:       &controllers.ImportController{Svc: deps.Import},
		settings:      &controllers.SettingsController{Svc: deps.Settings, SpawnConfirm: deps.SpawnConfirm, AutoNudge: deps.AutoNudge, ResponseLanguage: deps.ResponseLanguage, Wiki: deps.WikiSettings, EvidenceRetention: deps.EvidenceRetention, EvidenceSweeper: deps.EvidenceSweeper, SystemPrompts: deps.SystemPrompts, MessageTemplates: deps.MessageTemplates},
		wiki:          &controllers.WikiController{Svc: deps.Wiki},
		daemon:        &controllers.DaemonController{Loops: deps.LoopTelemetry},
		events:        &EventsController{Source: deps.CDC, Live: deps.Events},
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
			a.sim.Register(r)
			a.simFlows.Register(r)
			a.simScreen.Register(r)
			a.notifications.Register(r)
			a.imports.Register(r)
			a.settings.Register(r)
			a.wiki.Register(r)
			a.daemon.Register(r)
			// Sibling REST controllers plug in here.

			// THE TASK-SCOPED SURFACES, and the only place that list lives.
			//
			// What these controllers own belongs to the TASK, not to the agent whose
			// id the path names: the branch's pull request and its comment threads,
			// AO's review verdicts on it, and the smoke checklist. A crew's two
			// members share one of each, so both must be answered the same - reading
			// them per-session is what left qa with an empty Tests tab and a
			// readiness strip that saw no pull request at all.
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
				a.smoke.Register(r)
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
	StartPower(ctx context.Context, udid string, op simpower.Op, req *simslim.Request, done func()) error
	PowerStatus() map[string]simpower.Status
	ClearPower(udid string)
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
