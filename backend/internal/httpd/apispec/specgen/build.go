// Package specgen builds the code-first OpenAPI document from the Go contract
// types. It lives outside apispec because it imports the controllers (to
// reflect their request/response shapes), and controllers import apispec (for
// the 501 stub) — keeping Build here breaks that cycle. apispec only embeds and
// serves the committed openapi.yaml; specgen produces it.
package specgen

import (
	"fmt"
	"net/http"
	"reflect"
	"strings"

	jsonschema "github.com/swaggest/jsonschema-go"
	openapi "github.com/swaggest/openapi-go"
	"github.com/swaggest/openapi-go/openapi31"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	projectsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/project"
)

// Build reflects the Go contract types and the operation registry below into
// the OpenAPI document. It is the single source of truth for the /api/v1
// contract: `cmd/genspec` writes its output to apispec/openapi.yaml (the
// committed, embedded artifact) and TestBuild_MatchesEmbedded asserts the embed
// equals fresh Build() output so the two can never drift. Schema facets live as
// struct tags on the service.*/controllers.* types; operation metadata (path,
// status codes, summaries) lives here.
//
// Every wire shape is reflected straight from where it is used at runtime — the
// request bodies, path params, and response envelopes from controllers, the
// error envelope from httpd/envelope — so the served responses and the
// generated schema share one definition each.
func Build() ([]byte, error) {
	r := openapi31.NewReflector()
	// Derive `required` from the idiomatic Go convention: a JSON field without
	// `omitempty` is required. swaggest does not infer this on its own, so the
	// structs stay clean (only description/enum tags) and this hook adds the
	// required array. nonNullableSlices drops the spurious "null" type swaggest
	// stamps on every Go slice.
	r.DefaultOptions = append(r.DefaultOptions,
		jsonschema.InterceptProp(requiredFromJSONTag),
		jsonschema.InterceptNullability(nonNullableSlices),
		// Clean component schema names (which become the generated TS type names):
		// swaggest defaults to PackageType, e.g. "ProjectProject", "EnvelopeAPIError".
		jsonschema.InterceptDefName(schemaName),
	)

	r.Spec.SetTitle("Agent Orchestrator HTTP daemon")
	r.Spec.SetVersion("0.1.0-route-shell")
	r.Spec.SetDescription("Loopback-only HTTP surface served by the Go daemon. " +
		"Generated from Go (code-first) — do not edit by hand; run `go generate ./...`.")
	r.Spec.Servers = []openapi31.Server{
		*(&openapi31.Server{URL: "http://127.0.0.1:3001"}).WithDescription("Local daemon (loopback only)"),
	}
	r.Spec.Tags = []openapi31.Tag{
		*(&openapi31.Tag{Name: "agents"}).WithDescription(
			"Supported and locally runnable agent adapters"),
		*(&openapi31.Tag{Name: "projects"}).WithDescription(
			"Project registry, configuration, and lifecycle administration"),
		*(&openapi31.Tag{Name: "sessions"}).WithDescription(
			"Agent session lifecycle and messaging"),
		*(&openapi31.Tag{Name: "prs"}).WithDescription(
			"Pull-request actions (SCM lane)"),
		*(&openapi31.Tag{Name: "reviews"}).WithDescription(
			"Code-review runs and findings"),
		*(&openapi31.Tag{Name: "notifications"}).WithDescription(
			"Durable dashboard notifications"),
		*(&openapi31.Tag{Name: "events"}).WithDescription(
			"Server-sent CDC event stream with durable replay"),
		*(&openapi31.Tag{Name: "import"}).WithDescription(
			"Legacy AO project import (availability probe and run)"),
		*(&openapi31.Tag{Name: "settings"}).WithDescription(
			"User-editable daemon settings (auto-reclaim, etc.)"),
	}

	for _, op := range operations() {
		oc, err := r.NewOperationContext(op.method, op.path)
		if err != nil {
			return nil, fmt.Errorf("new operation %s %s: %w", op.method, op.path, err)
		}
		oc.SetID(op.id)
		oc.SetSummary(op.summary)
		oc.SetTags(op.tag)
		for _, param := range op.pathParams {
			oc.AddReqStructure(param)
		}
		if op.reqBody != nil {
			// AddReqStructure leaves requestBody.required absent, which
			// OpenAPI reads as optional. These bodies are mandatory, so force
			// it — otherwise validators/generators treat the body as skippable.
			oc.AddReqStructure(op.reqBody, openapi.WithCustomize(markRequestBodyRequired))
		}
		for _, resp := range op.resps {
			opts := []openapi.ContentOption{openapi.WithHTTPStatus(resp.status)}
			if op.contentTypes != nil && op.contentTypes[resp.status] != "" {
				opts = append(opts, openapi.WithContentType(op.contentTypes[resp.status]))
			}
			oc.AddRespStructure(resp.body, opts...)
		}
		if err := r.AddOperation(oc); err != nil {
			return nil, fmt.Errorf("add operation %s %s: %w", op.method, op.path, err)
		}
	}

	return r.Spec.MarshalYAML()
}

// schemaName maps swaggest's default PackageType component names (e.g.
// "ProjectProject", "EnvelopeAPIError") to the clean, stable schema names that
// become the generated TypeScript type names. Every reflected type is listed
// explicitly: an unrecognised default name is returned verbatim, so a new type
// surfaces as a visibly-wrong "PackageType" name in the diff (and the drift
// test) rather than silently colliding with an existing schema via a
// TrimPrefix catch-all.
func schemaName(_ reflect.Type, defaultName string) string {
	if clean, ok := schemaNames[defaultName]; ok {
		return clean
	}
	return defaultName
}

// schemaNames is the exhaustive default→clean mapping for every type reflected
// by projectOperations(). Add an entry when a new contract type is introduced;
// the drift test fails until the spec is regenerated, which flags the gap.
var schemaNames = map[string]string{
	// httpd/envelope
	"EnvelopeAPIError": "APIError",
	// domain
	"DomainProjectID":           "ProjectID",
	"DomainSessionID":           "SessionID",
	"DomainIssueID":             "IssueID",
	"DomainSession":             "Session",
	"DomainProjectConfig":       "ProjectConfig",
	"DomainTrackerIntakeConfig": "TrackerIntakeConfig",
	"DomainGitConventionConfig": "GitConventionConfig",
	"DomainApprovalRule":        "ApprovalRule",
	"DomainAgentConfig":         "AgentConfig",
	"DomainRoleOverride":        "RoleOverride",
	// httpd/controllers (wire envelopes)
	"ControllersListProjectsResponse":             "ListProjectsResponse",
	"ControllersProjectResponse":                  "ProjectResponse",
	"ControllersProjectBranchesResponse":          "ProjectBranchesResponse",
	"ControllersAgentIDParam":                     "AgentIDParam",
	"ControllersGetProjectResponse":               "ProjectGetResponse",
	"ControllersProjectOrDegraded":                "ProjectOrDegraded",
	"ControllersListSessionsQuery":                "ListSessionsQuery",
	"ControllersCleanupSessionsQuery":             "CleanupSessionsQuery",
	"ControllersListSessionsResponse":             "ListSessionsResponse",
	"ControllersSpawnSessionRequest":              "SpawnSessionRequest",
	"ControllersSessionResponse":                  "SessionResponse",
	"ControllersSessionPreviewResponse":           "SessionPreviewResponse",
	"ControllersSetSessionPreviewRequest":         "SetSessionPreviewRequest",
	"ControllersSetAutoNudgeRequest":              "SetAutoNudgeRequest",
	"ControllersSetSessionKeepWarmRequest":        "SetSessionKeepWarmRequest",
	"ControllersRenameSessionRequest":             "RenameSessionRequest",
	"ControllersUpdateTodoSpecRequest":            "UpdateTodoSpecRequest",
	"ControllersRenameSessionResponse":            "RenameSessionResponse",
	"ControllersRestoreSessionResponse":           "RestoreSessionResponse",
	"ControllersRestartSessionResponse":           "RestartSessionResponse",
	"ControllersWakeSessionResponse":              "WakeSessionResponse",
	"ControllersDeleteSessionQuery":               "DeleteSessionQuery",
	"ControllersDeleteSessionResponse":            "DeleteSessionResponse",
	"ControllersCleanupSessionsResponse":          "CleanupSessionsResponse",
	"ControllersCleanupSkippedSession":            "CleanupSkippedSession",
	"ControllersKillSessionResponse":              "KillSessionResponse",
	"ControllersRollbackSessionResponse":          "RollbackSessionResponse",
	"ControllersSendSessionMessageRequest":        "SendSessionMessageRequest",
	"ControllersSendSessionMessageResponse":       "SendSessionMessageResponse",
	"ControllersDispatchCommentRequest":           "DispatchCommentRequest",
	"ControllersDispatchCommentResponse":          "DispatchCommentResponse",
	"ControllersReplyCommentRequest":              "ReplyCommentRequest",
	"ControllersReplyCommentResponse":             "ReplyCommentResponse",
	"ControllersResolveThreadRequest":             "ResolveThreadRequest",
	"ControllersResolveThreadResponse":            "ResolveThreadResponse",
	"ControllersClaimPRResponse":                  "ClaimPRResponse",
	"ControllersClaimPRRequest":                   "ClaimPRRequest",
	"ControllersSessionPRFacts":                   "SessionPRFacts",
	"ControllersSessionPRSummary":                 "SessionPRSummary",
	"ControllersSessionPRCISummary":               "SessionPRCISummary",
	"ControllersSessionPRFailingCheck":            "SessionPRFailingCheck",
	"ControllersSessionPRReviewSummary":           "SessionPRReviewSummary",
	"ControllersSessionPRUnresolvedReviewer":      "SessionPRUnresolvedReviewer",
	"ControllersSessionPRReviewCommentLink":       "SessionPRReviewCommentLink",
	"ControllersSessionPRMergeabilitySummary":     "SessionPRMergeabilitySummary",
	"ControllersSessionPRConflictFile":            "SessionPRConflictFile",
	"ControllersListSessionPRsResponse":           "ListSessionPRsResponse",
	"ControllersJiraContextResponse":              "JiraContextResponse",
	"ControllersJiraIssue":                        "JiraIssue",
	"ControllersJiraSprint":                       "JiraSprint",
	"ControllersJiraParentRef":                    "JiraParentRef",
	"ControllersJiraSubtask":                      "JiraSubtask",
	"ControllersJiraTransition":                   "JiraTransition",
	"ControllersJiraTransitionsResponse":          "JiraTransitionsResponse",
	"ControllersJiraMoveRequest":                  "JiraMoveRequest",
	"ControllersJiraMoveResponse":                 "JiraMoveResponse",
	"ControllersJiraIssueSummary":                 "JiraIssueSummary",
	"ControllersJiraIssueResponse":                "JiraIssueResponse",
	"ControllersJiraIssueQuery":                   "JiraIssueQuery",
	"ControllersJiraIssueMoveRequest":             "JiraIssueMoveRequest",
	"ControllersJiraSearchResponse":               "JiraSearchResponse",
	"ControllersJiraProject":                      "JiraProject",
	"ControllersJiraProjectsResponse":             "JiraProjectsResponse",
	"ControllersJiraSearchQuery":                  "JiraSearchQuery",
	"ControllersJiraProjectsQuery":                "JiraProjectsQuery",
	"ControllersJiraTransitionsQuery":             "JiraTransitionsQuery",
	"ControllersJiraLinkRequest":                  "JiraLinkRequest",
	"ControllersJiraLinkResponse":                 "JiraLinkResponse",
	"AdfNode":                                     "AdfNode",
	"AdfMark":                                     "AdfMark",
	"AdfAttrs":                                    "AdfAttrs",
	"ControllersListSessionPRCommentsResponse":    "ListSessionPRCommentsResponse",
	"ControllersSessionPRCommentGroup":            "SessionPRCommentGroup",
	"ControllersSessionPRCommentThread":           "SessionPRCommentThread",
	"ControllersSessionPRThreadComment":           "SessionPRThreadComment",
	"ControllersDiffContextParams":                "DiffContextParams",
	"ControllersDiffContextResponse":              "DiffContextResponse",
	"ControllersDiffContextLineDTO":               "DiffContextLineDTO",
	"ControllersSetActivityRequest":               "SetActivityRequest",
	"ControllersSetActivityResponse":              "SetActivityResponse",
	"ControllersSpawnOrchestratorRequest":         "SpawnOrchestratorRequest",
	"ControllersSpawnOrchestratorResponse":        "SpawnOrchestratorResponse",
	"ControllersOrchestratorResponse":             "OrchestratorResponse",
	"AgentInventory":                              "ListAgentsResponse",
	"AgentInfo":                                   "AgentInfo",
	"AgentProbeResult":                            "ProbeAgentResponse",
	"ControllersListNotificationsQuery":           "ListNotificationsQuery",
	"ControllersNotificationStreamQuery":          "NotificationStreamQuery",
	"ControllersNotificationIDParam":              "NotificationIDParam",
	"ControllersNotificationTarget":               "NotificationTarget",
	"ControllersNotificationResponse":             "NotificationResponse",
	"ControllersListNotificationsResponse":        "ListNotificationsResponse",
	"ControllersMarkNotificationReadRequest":      "MarkNotificationReadRequest",
	"ControllersNotificationEnvelope":             "NotificationEnvelope",
	"ControllersMarkAllNotificationsReadResponse": "MarkAllNotificationsReadResponse",
	// httpd/controllers — PR wire envelopes
	"ControllersMergePRResponse":         "MergePRResponse",
	"ControllersResolveCommentsRequest":  "ResolveCommentsRequest",
	"ControllersResolveCommentsResponse": "ResolveCommentsResponse",
	// httpd/controllers — review wire envelopes
	"ControllersListReviewsResponse":   "ListReviewsResponse",
	"ControllersReviewRunResponse":     "ReviewRunResponse",
	"ControllersTriggerReviewResponse": "TriggerReviewResponse",
	"ControllersSubmitReviewItem":      "SubmitReviewItem",
	"ControllersSubmitReviewInput":     "SubmitReviewInput",
	// domain review entities
	"DomainReviewRun":     "ReviewRun",
	"ReviewPRReviewState": "PRReviewState",
	// httpd/controllers — smoke-test wire envelopes
	"ControllersSmokeCheckParam":         "SmokeCheckParam",
	"ControllersSmokeEvidenceParam":      "SmokeEvidenceParam",
	"ControllersSmokeAuthoredCaseInput":  "SmokeAuthoredCaseInput",
	"ControllersAuthorSmokeChecksInput":  "AuthorSmokeChecksInput",
	"ControllersListSmokeChecksResponse": "ListSmokeChecksResponse",
	"ControllersSmokeCheckResponse":      "SmokeCheckResponse",
	"ControllersSetSmokeVerdictInput":    "SetSmokeVerdictInput",
	"ControllersSmokeEvidenceResponse":   "SmokeEvidenceResponse",
	"ControllersReportSmokeResponse":     "ReportSmokeResponse",
	// domain smoke entities
	"DomainSmokeCheck":    "SmokeCheck",
	"DomainSmokeEvidence": "SmokeEvidence",
	// httpd/controllers: import wire envelopes
	"ControllersImportStatusResponse": "ImportStatusResponse",
	"ControllersImportRunResponse":    "ImportRunResponse",
	// httpd/controllers: settings wire envelopes
	"ControllersReclaimSettingsResponse":        "ReclaimSettingsResponse",
	"ControllersSetReclaimSettingsRequest":      "SetReclaimSettingsRequest",
	"ControllersSpawnConfirmSettingsResponse":   "SpawnConfirmSettingsResponse",
	"ControllersSetSpawnConfirmSettingsRequest": "SetSpawnConfirmSettingsRequest",
	"ControllersAutoNudgeSettingsResponse":      "AutoNudgeSettingsResponse",
	"ControllersSetAutoNudgeSettingsRequest":    "SetAutoNudgeSettingsRequest",
	"ControllersSystemPromptItem":               "SystemPromptItem",
	"ControllersSystemPromptsResponse":          "SystemPromptsResponse",
	"ControllersSetSystemPromptRequest":         "SetSystemPromptRequest",
	"ControllersMessageTemplateItem":            "MessageTemplateItem",
	"ControllersMessageTemplatesResponse":       "MessageTemplatesResponse",
	"ControllersSetMessageTemplateRequest":      "SetMessageTemplateRequest",
	// legacyimport report
	"LegacyimportReport": "ImportReport",
	// service/project entities + DTOs
	"ProjectProject":        "Project",
	"ProjectSummary":        "ProjectSummary",
	"ProjectDegraded":       "DegradedProject",
	"ProjectAddInput":       "AddProjectInput",
	"ProjectRemoveResult":   "RemoveProjectResult",
	"ProjectSetConfigInput": "SetProjectConfigInput",
	"ProjectWorkspaceRepo":  "WorkspaceRepo",
}

// markRequestBodyRequired sets requestBody.required: true on the operation's
// JSON body. swaggest leaves it absent (== optional) for AddReqStructure bodies.
func markRequestBodyRequired(cor openapi.ContentOrReference) {
	if rb, ok := cor.(*openapi31.RequestBodyOrReference); ok && rb.RequestBody != nil {
		rb.RequestBody.WithRequired(true)
	}
}

// nonNullableSlices drops the "null" that swaggest unions into every Go slice
// type (a nil slice marshals as JSON null). A required array field should be
// `T[]`, not `T[] | null`; the handlers normalise nil to an empty slice, so
// null never reaches the wire. Byte slices (base64 strings) are left alone.
func nonNullableSlices(p jsonschema.InterceptNullabilityParams) {
	if !p.NullAdded || p.Type == nil || p.Type.Kind() != reflect.Slice {
		return
	}
	if p.Type.Elem().Kind() == reflect.Uint8 {
		return
	}
	p.Schema.TypeEns().WithSimpleTypes(jsonschema.Array)
	p.Schema.Type.SliceOfSimpleTypeValues = nil
}

// requiredFromJSONTag marks a property required when its json tag lacks
// `omitempty` (the Go convention for "always present"). Runs after default
// processing so ParentSchema exists; skips fields without a json tag (e.g. path
// params, which swaggest marks required on their own).
func requiredFromJSONTag(p jsonschema.InterceptPropParams) error {
	if !p.Processed || p.ParentSchema == nil {
		return nil
	}
	jsonTag := p.Field.Tag.Get("json")
	if jsonTag == "" || jsonTag == "-" {
		return nil
	}
	parts := strings.Split(jsonTag, ",")
	name := parts[0]
	if name == "" {
		name = p.Name
	}
	for _, opt := range parts[1:] {
		if opt == "omitempty" {
			return nil
		}
	}
	for _, existing := range p.ParentSchema.Required {
		if existing == name {
			return nil
		}
	}
	p.ParentSchema.Required = append(p.ParentSchema.Required, name)
	return nil
}

// --- operation registry -----------------------------------------------------

type respUnit struct {
	status int
	body   any
}

type operation struct {
	method, path, id, summary string
	tag                       string
	pathParams                []any // path/query param containers (e.g. ProjectIDParam)
	reqBody                   any   // JSON request body struct, nil when the op takes none
	resps                     []respUnit
	contentTypes              map[int]string // optional non-JSON response content types by status
}

func operations() []operation {
	ops := append([]operation{}, eventOperations()...)
	ops = append(ops, agentOperations()...)
	ops = append(ops, projectOperations()...)
	ops = append(ops, sessionOperations()...)
	ops = append(ops, jiraOperations()...)
	ops = append(ops, settingsOperations()...)
	ops = append(ops, prOperations()...)
	ops = append(ops, reviewOperations()...)
	ops = append(ops, smokeOperations()...)
	ops = append(ops, notificationOperations()...)
	ops = append(ops, importOperations()...)
	return ops
}

func agentOperations() []operation {
	return []operation{
		{
			method: http.MethodGet, path: "/api/v1/agents", id: "listAgents", tag: "agents",
			summary: "Return cached supported and locally installed agent adapters",
			resps: []respUnit{
				{http.StatusOK, controllers.ListAgentsResponse{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/agents/refresh", id: "refreshAgents", tag: "agents",
			summary: "Refresh the cached local agent adapter catalog",
			resps: []respUnit{
				{http.StatusOK, controllers.RefreshAgentsResponse{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/agents/{agent}/probe", id: "probeAgent", tag: "agents",
			summary:    "Run a fresh local readiness probe for one agent adapter",
			pathParams: []any{controllers.AgentIDParam{}},
			resps: []respUnit{
				{http.StatusOK, controllers.ProbeAgentResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
	}
}

// importOperations declares the 2 /import operations. Must stay 1:1 with
// the routes ImportController.Register mounts (enforced by the parity test).
func importOperations() []operation {
	return []operation{
		{
			method: http.MethodGet, path: "/api/v1/import", id: "getImportStatus", tag: "import",
			summary: "Check whether a legacy AO install is available to import",
			resps: []respUnit{
				{http.StatusOK, controllers.ImportStatusResponse{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/import", id: "runImport", tag: "import",
			summary: "Run the legacy AO project import through the daemon store",
			resps: []respUnit{
				{http.StatusOK, controllers.ImportRunResponse{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
	}
}

func notificationOperations() []operation {
	return []operation{
		{
			method: http.MethodGet, path: "/api/v1/notifications", id: "listNotifications", tag: "notifications",
			summary:    "List unread notifications",
			pathParams: []any{controllers.ListNotificationsQuery{}},
			resps: []respUnit{
				{http.StatusOK, controllers.ListNotificationsResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPatch, path: "/api/v1/notifications/{id}", id: "markNotificationRead", tag: "notifications",
			summary:    "Mark a notification read",
			pathParams: []any{controllers.NotificationIDParam{}},
			reqBody:    controllers.MarkNotificationReadRequest{},
			resps: []respUnit{
				{http.StatusOK, controllers.NotificationEnvelope{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/notifications/read-all", id: "markAllNotificationsRead", tag: "notifications",
			summary: "Mark all unread notifications read",
			resps: []respUnit{
				{http.StatusOK, controllers.MarkAllNotificationsReadResponse{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodGet, path: "/api/v1/notifications/stream", id: "streamNotifications", tag: "notifications",
			summary:    "Stream created notifications",
			pathParams: []any{controllers.NotificationStreamQuery{}},
			resps: []respUnit{
				{http.StatusOK, ""},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
			contentTypes: map[int]string{http.StatusOK: "text/event-stream"},
		},
	}
}

// jiraOperations declares the session-scoped /jira operation. Must stay 1:1
// with the routes JiraController.Register mounts (enforced by the parity test).
func jiraOperations() []operation {
	return []operation{
		{
			method: http.MethodGet, path: "/api/v1/jira/search", id: "searchJira", tag: "jira",
			summary:    "Search Jira issues cross-project (free-text or exact key), read live via REST",
			pathParams: []any{controllers.JiraSearchQuery{}},
			resps: []respUnit{
				{http.StatusOK, controllers.JiraSearchResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodGet, path: "/api/v1/jira/projects", id: "listJiraProjects", tag: "jira",
			summary:    "List the user's Jira projects for the project picker",
			pathParams: []any{controllers.JiraProjectsQuery{}},
			resps: []respUnit{
				{http.StatusOK, controllers.JiraProjectsResponse{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodGet, path: "/api/v1/jira/issue", id: "getJiraIssue", tag: "jira",
			summary:    "Read one Jira issue's full display projection by key (pre-session detail view)",
			pathParams: []any{controllers.JiraIssueQuery{}},
			resps: []respUnit{
				{http.StatusOK, controllers.JiraIssueResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodGet, path: "/api/v1/jira/issue/transitions", id: "listJiraIssueTransitions", tag: "jira",
			summary:    "List any Jira issue's available status transitions by key (read live)",
			pathParams: []any{controllers.JiraIssueQuery{}},
			resps: []respUnit{
				{http.StatusOK, controllers.JiraTransitionsResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/jira/issue/move", id: "moveJiraIssue", tag: "jira",
			summary: "Apply a status transition to any Jira issue by key — the one sanctioned write",
			reqBody: controllers.JiraIssueMoveRequest{},
			resps: []respUnit{
				{http.StatusOK, controllers.JiraMoveResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodGet, path: "/api/v1/sessions/{sessionId}/jira", id: "getSessionJira", tag: "jira",
			summary:    "Return the display-only Jira issue context for a session bound to a Jira key",
			pathParams: []any{controllers.SessionIDParam{}},
			resps: []respUnit{
				{http.StatusOK, controllers.JiraContextResponse{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPut, path: "/api/v1/sessions/{sessionId}/jira", id: "linkSessionJira", tag: "jira",
			summary:    "Bind an existing session to a Jira issue (validated) after the fact",
			pathParams: []any{controllers.SessionIDParam{}},
			reqBody:    controllers.JiraLinkRequest{},
			resps: []respUnit{
				{http.StatusOK, controllers.JiraLinkResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodDelete, path: "/api/v1/sessions/{sessionId}/jira", id: "unlinkSessionJira", tag: "jira",
			summary:    "Remove a session's Jira binding",
			pathParams: []any{controllers.SessionIDParam{}},
			resps: []respUnit{
				{http.StatusOK, controllers.JiraLinkResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodGet, path: "/api/v1/sessions/{sessionId}/jira/transitions", id: "listSessionJiraTransitions", tag: "jira",
			summary:    "List the linked Jira issue's (or a subtask's) available status transitions (read live)",
			pathParams: []any{controllers.SessionIDParam{}, controllers.JiraTransitionsQuery{}},
			resps: []respUnit{
				{http.StatusOK, controllers.JiraTransitionsResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/sessions/{sessionId}/jira/move", id: "moveSessionJiraStatus", tag: "jira",
			summary:    "Move the linked Jira issue's status (the one sanctioned Jira write)",
			pathParams: []any{controllers.SessionIDParam{}},
			reqBody:    controllers.JiraMoveRequest{},
			resps: []respUnit{
				{http.StatusOK, controllers.JiraMoveResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
	}
}

// reviewOperations declares the session-scoped /reviews operations. Must stay
// 1:1 with the routes ReviewsController.Register mounts (enforced by the parity
// test).
func reviewOperations() []operation {
	return []operation{
		{
			method: http.MethodGet, path: "/api/v1/sessions/{sessionId}/reviews", id: "listReviews", tag: "reviews",
			summary:    "List a worker's code-review runs",
			pathParams: []any{controllers.SessionIDParam{}},
			resps: []respUnit{
				{http.StatusOK, controllers.ListReviewsResponse{}},
				{http.StatusUnprocessableEntity, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/sessions/{sessionId}/reviews/trigger", id: "triggerReview", tag: "reviews",
			summary:    "Trigger a code review of a worker's PR",
			pathParams: []any{controllers.SessionIDParam{}},
			resps: []respUnit{
				{http.StatusOK, controllers.TriggerReviewResponse{}},
				{http.StatusCreated, controllers.TriggerReviewResponse{}},
				{http.StatusUnprocessableEntity, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/sessions/{sessionId}/reviews/submit", id: "submitReview", tag: "reviews",
			summary:    "Record a reviewer's result for a worker's PR",
			pathParams: []any{controllers.SessionIDParam{}},
			reqBody:    controllers.SubmitReviewInput{},
			resps: []respUnit{
				{http.StatusOK, controllers.ReviewRunResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusUnprocessableEntity, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/sessions/{sessionId}/reviews/reset", id: "resetReview", tag: "reviews",
			summary:    "Clear a worker's stuck review by failing its orphaned running runs",
			pathParams: []any{controllers.SessionIDParam{}},
			resps: []respUnit{
				{http.StatusOK, controllers.ResetReviewResponse{}},
				{http.StatusUnprocessableEntity, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
	}
}

// smokeOperations declares the session-scoped /smoke-checks operations. Must
// stay 1:1 with the routes SmokeController.Register mounts (enforced by the
// parity test).
func smokeOperations() []operation {
	return []operation{
		{
			method: http.MethodGet, path: "/api/v1/sessions/{sessionId}/smoke-checks", id: "listSmokeChecks", tag: "smoke",
			summary:    "List a session's smoke-test checklist",
			pathParams: []any{controllers.SessionIDParam{}},
			resps: []respUnit{
				{http.StatusOK, controllers.ListSmokeChecksResponse{}},
				{http.StatusUnprocessableEntity, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPut, path: "/api/v1/sessions/{sessionId}/smoke-checks", id: "authorSmokeChecks", tag: "smoke",
			summary:    "Author/replace a session's smoke-test checklist (results preserved by case id)",
			pathParams: []any{controllers.SessionIDParam{}},
			reqBody:    controllers.AuthorSmokeChecksInput{},
			resps: []respUnit{
				{http.StatusOK, controllers.ListSmokeChecksResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusUnprocessableEntity, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/sessions/{sessionId}/smoke-checks/report", id: "reportSmokeChecks", tag: "smoke",
			summary:    "Report a session's smoke-test results back to the worker",
			pathParams: []any{controllers.SessionIDParam{}},
			resps: []respUnit{
				{http.StatusOK, controllers.ReportSmokeResponse{}},
				{http.StatusUnprocessableEntity, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/sessions/{sessionId}/smoke-checks/{checkId}/verdict", id: "setSmokeVerdict", tag: "smoke",
			summary:    "Record the user's verdict + note for a smoke-test case",
			pathParams: []any{controllers.SmokeCheckParam{}},
			reqBody:    controllers.SetSmokeVerdictInput{},
			resps: []respUnit{
				{http.StatusOK, controllers.SmokeCheckResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusUnprocessableEntity, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/sessions/{sessionId}/smoke-checks/{checkId}/reset", id: "resetSmokeCheck", tag: "smoke",
			summary:    "Clear a smoke-test case's verdict/note/evidence",
			pathParams: []any{controllers.SmokeCheckParam{}},
			resps: []respUnit{
				{http.StatusOK, controllers.SmokeCheckResponse{}},
				{http.StatusUnprocessableEntity, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/sessions/{sessionId}/smoke-checks/{checkId}/evidence", id: "uploadSmokeEvidence", tag: "smoke",
			summary:    "Attach a screenshot/short clip to a smoke-test case (multipart/form-data 'file' or raw body)",
			pathParams: []any{controllers.SmokeCheckParam{}},
			resps: []respUnit{
				{http.StatusOK, controllers.SmokeEvidenceResponse{}},
				{http.StatusUnprocessableEntity, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodGet, path: "/api/v1/sessions/{sessionId}/smoke-checks/{checkId}/evidence/{evidenceId}", id: "serveSmokeEvidence", tag: "smoke",
			summary:      "Serve a stored smoke-test evidence blob",
			pathParams:   []any{controllers.SmokeEvidenceParam{}},
			resps:        []respUnit{{http.StatusOK, ""}, {http.StatusNotFound, envelope.APIError{}}, {http.StatusNotImplemented, envelope.APIError{}}},
			contentTypes: map[int]string{http.StatusOK: "application/octet-stream"},
		},
	}
}

type eventsQuery struct {
	After *int64 `query:"after,omitempty" minimum:"0" description:"Replay events with seq greater than this cursor. When omitted, clients may send Last-Event-ID instead."`
}

func eventOperations() []operation {
	return []operation{
		{
			method: http.MethodGet, path: "/api/v1/events", id: "streamEvents", tag: "events",
			summary:    "Stream CDC events with durable replay",
			pathParams: []any{eventsQuery{}},
			resps: []respUnit{
				{http.StatusOK, ""},
				{status: http.StatusBadRequest, body: envelope.APIError{}},
				{status: http.StatusInternalServerError, body: envelope.APIError{}},
				{status: http.StatusNotImplemented, body: envelope.APIError{}},
			},
			contentTypes: map[int]string{http.StatusOK: "text/event-stream"},
		},
	}
}

// projectOperations declares the 5 canonical /projects operations. The set must
// stay 1:1 with the routes ProjectsController.Register mounts —
// TestRouteSpecParity fails the build otherwise.
func projectOperations() []operation {
	return []operation{
		{
			method: http.MethodGet, path: "/api/v1/projects", id: "listProjects", tag: "projects",
			summary: "List all registered projects (active + degraded)",
			resps: []respUnit{
				{http.StatusOK, controllers.ListProjectsResponse{}},
				{http.StatusInternalServerError, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/projects", id: "addProject", tag: "projects",
			summary: "Register a new project from a git repository path",
			reqBody: projectsvc.AddInput{},
			resps: []respUnit{
				{http.StatusCreated, controllers.ProjectResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusConflict, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
			},
		},
		{
			method: http.MethodGet, path: "/api/v1/projects/{id}", id: "getProject", tag: "projects",
			summary:    "Fetch one project; discriminates ok vs degraded",
			pathParams: []any{controllers.ProjectIDParam{}},
			resps: []respUnit{
				{http.StatusOK, controllers.GetProjectResponse{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
			},
		},
		{
			method: http.MethodGet, path: "/api/v1/projects/{id}/branches", id: "listProjectBranches", tag: "projects",
			summary:    "List branch names for a project's repository",
			pathParams: []any{controllers.ProjectIDParam{}},
			resps: []respUnit{
				{http.StatusOK, controllers.ProjectBranchesResponse{}},
				{http.StatusInternalServerError, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPut, path: "/api/v1/projects/{id}/config", id: "setProjectConfig", tag: "projects",
			summary:    "Replace a project's per-project config",
			pathParams: []any{controllers.ProjectIDParam{}},
			reqBody:    projectsvc.SetConfigInput{},
			resps: []respUnit{
				{http.StatusOK, controllers.ProjectResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
			},
		},
		{
			method: http.MethodDelete, path: "/api/v1/projects/{id}", id: "removeProject", tag: "projects",
			summary:    "Remove a project; stops sessions, cleans workspaces, unregisters",
			pathParams: []any{controllers.ProjectIDParam{}},
			resps: []respUnit{
				{http.StatusOK, projectsvc.RemoveResult{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
			},
		},
	}
}

func sessionOperations() []operation {
	return []operation{
		{
			method: http.MethodGet, path: "/api/v1/sessions", id: "listSessions", tag: "sessions",
			summary:    "List sessions",
			pathParams: []any{controllers.ListSessionsQuery{}},
			resps: []respUnit{
				{http.StatusOK, controllers.ListSessionsResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/sessions", id: "spawnSession", tag: "sessions",
			summary: "Spawn a new agent session",
			reqBody: controllers.SpawnSessionRequest{},
			resps: []respUnit{
				{http.StatusCreated, controllers.SessionResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
			},
		},
		{
			method: http.MethodGet, path: "/api/v1/sessions/{sessionId}", id: "getSession", tag: "sessions",
			summary:    "Fetch one session",
			pathParams: []any{controllers.SessionIDParam{}},
			resps: []respUnit{
				{http.StatusOK, controllers.SessionResponse{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
			},
		},
		{
			method: http.MethodDelete, path: "/api/v1/sessions/{sessionId}", id: "deleteSession", tag: "sessions",
			summary:    "Permanently delete a finished session (keeps the git branch)",
			pathParams: []any{controllers.SessionIDParam{}, controllers.DeleteSessionQuery{}},
			resps: []respUnit{
				{http.StatusOK, controllers.DeleteSessionResponse{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusConflict, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
			},
		},
		{
			method: http.MethodGet, path: "/api/v1/sessions/{sessionId}/preview", id: "getSessionPreview", tag: "sessions",
			summary:    "Discover a browser preview URL for a session workspace",
			pathParams: []any{controllers.SessionIDParam{}},
			resps: []respUnit{
				{http.StatusOK, controllers.SessionPreviewResponse{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/sessions/{sessionId}/preview", id: "setSessionPreview", tag: "sessions",
			summary:    "Set (or autodetect) the browser preview URL for a session",
			pathParams: []any{controllers.SessionIDParam{}},
			reqBody:    controllers.SetSessionPreviewRequest{},
			resps: []respUnit{
				{http.StatusOK, controllers.SessionResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPut, path: "/api/v1/sessions/{sessionId}/auto-nudge", id: "setSessionAutoNudge", tag: "sessions",
			summary:    "Set (or clear) the per-session auto-nudge-on-comments override",
			pathParams: []any{controllers.SessionIDParam{}},
			reqBody:    controllers.SetAutoNudgeRequest{},
			resps: []respUnit{
				{http.StatusOK, controllers.SessionResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPut, path: "/api/v1/sessions/{sessionId}/keep-warm", id: "setSessionKeepWarm", tag: "sessions",
			summary:    "Toggle suspend-in-place-on-merge (keep-warm) for a worker session",
			pathParams: []any{controllers.SessionIDParam{}},
			reqBody:    controllers.SetSessionKeepWarmRequest{},
			resps: []respUnit{
				{http.StatusOK, controllers.SessionResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodDelete, path: "/api/v1/sessions/{sessionId}/preview", id: "clearSessionPreview", tag: "sessions",
			summary:    "Clear the browser preview URL for a session",
			pathParams: []any{controllers.SessionIDParam{}},
			resps: []respUnit{
				{http.StatusOK, controllers.SessionResponse{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodGet, path: "/api/v1/sessions/{sessionId}/preview/files/*", id: "getSessionPreviewFile", tag: "sessions",
			summary:    "Serve a static browser preview file from a session workspace",
			pathParams: []any{controllers.SessionIDParam{}},
			resps: []respUnit{
				{http.StatusOK, ""},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
			contentTypes: map[int]string{http.StatusOK: "text/html"},
		},
		{
			method: http.MethodGet, path: "/api/v1/sessions/{sessionId}/pr", id: "listSessionPRs", tag: "sessions",
			summary:    "List pull requests owned by a session",
			pathParams: []any{controllers.SessionIDParam{}},
			resps: []respUnit{
				{http.StatusOK, controllers.ListSessionPRsResponse{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodGet, path: "/api/v1/sessions/{sessionId}/pr-comments", id: "listSessionPRComments", tag: "sessions",
			summary:    "List review comment threads across a session's pull requests",
			pathParams: []any{controllers.SessionIDParam{}},
			resps: []respUnit{
				{http.StatusOK, controllers.ListSessionPRCommentsResponse{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodGet, path: "/api/v1/sessions/{sessionId}/diff-context", id: "sessionDiffContext", tag: "sessions",
			summary:    "Return the diff hunk or full file a review comment anchors to",
			pathParams: []any{controllers.SessionIDParam{}, controllers.DiffContextParams{}},
			resps: []respUnit{
				{http.StatusOK, controllers.DiffContextResponse{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/sessions/{sessionId}/pr/claim", id: "claimSessionPR", tag: "sessions",
			summary:    "Claim an existing pull request for a session",
			pathParams: []any{controllers.SessionIDParam{}},
			reqBody:    controllers.ClaimPRRequest{},
			resps: []respUnit{
				{http.StatusOK, controllers.ClaimPRResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusConflict, envelope.APIError{}},
				{http.StatusUnprocessableEntity, envelope.APIError{}},
				{http.StatusServiceUnavailable, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPatch, path: "/api/v1/sessions/{sessionId}", id: "renameSession", tag: "sessions",
			summary:    "Rename a session display name",
			pathParams: []any{controllers.SessionIDParam{}},
			reqBody:    controllers.RenameSessionRequest{},
			resps: []respUnit{
				{http.StatusOK, controllers.RenameSessionResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPatch, path: "/api/v1/sessions/{sessionId}/spec", id: "updateTodoSpec", tag: "sessions",
			summary:    "Edit a prepared TODO's spec before it is started",
			pathParams: []any{controllers.SessionIDParam{}},
			reqBody:    controllers.UpdateTodoSpecRequest{},
			resps: []respUnit{
				{http.StatusOK, controllers.SessionResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusConflict, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/sessions/{sessionId}/start", id: "startTodoSession", tag: "sessions",
			summary:    "Start (materialize) a prepared TODO session",
			pathParams: []any{controllers.SessionIDParam{}},
			resps: []respUnit{
				{http.StatusOK, controllers.SessionResponse{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusConflict, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/sessions/cleanup", id: "cleanupSessions", tag: "sessions",
			summary:    "Clean up terminated session workspaces",
			pathParams: []any{controllers.CleanupSessionsQuery{}},
			resps: []respUnit{
				{http.StatusOK, controllers.CleanupSessionsResponse{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/sessions/{sessionId}/restore", id: "restoreSession", tag: "sessions",
			summary:    "Restore a terminated session",
			pathParams: []any{controllers.SessionIDParam{}},
			resps: []respUnit{
				{http.StatusOK, controllers.RestoreSessionResponse{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusConflict, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/sessions/{sessionId}/restart", id: "restartSession", tag: "sessions",
			summary:    "Restart a session (kill then restore), keeping the conversation and recomputing the system prompt",
			pathParams: []any{controllers.SessionIDParam{}},
			resps: []respUnit{
				{http.StatusOK, controllers.RestartSessionResponse{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusConflict, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/sessions/{sessionId}/wake", id: "wakeSession", tag: "sessions",
			summary:    "Wake a session on user-open: resume it if the idle sweep suspended it, else reset its idle-close countdown",
			pathParams: []any{controllers.SessionIDParam{}},
			resps: []respUnit{
				{http.StatusOK, controllers.WakeSessionResponse{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusConflict, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/sessions/{sessionId}/kill", id: "killSession", tag: "sessions",
			summary:    "Mark a session terminated and tear down runtime/workspace resources",
			pathParams: []any{controllers.SessionIDParam{}},
			resps: []respUnit{
				{http.StatusOK, controllers.KillSessionResponse{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusConflict, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/sessions/{sessionId}/rollback", id: "rollbackSession", tag: "sessions",
			summary:    "Undo a partially-completed spawn (delete seed row, or kill if spawn output exists)",
			pathParams: []any{controllers.SessionIDParam{}},
			resps: []respUnit{
				{http.StatusOK, controllers.RollbackSessionResponse{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusConflict, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/sessions/{sessionId}/send", id: "sendSessionMessage", tag: "sessions",
			summary:    "Send a message to a running session's agent",
			pathParams: []any{controllers.SessionIDParam{}},
			reqBody:    controllers.SendSessionMessageRequest{},
			resps: []respUnit{
				{http.StatusOK, controllers.SendSessionMessageResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/sessions/{sessionId}/comment-dispatch", id: "sessionDispatchComment", tag: "sessions",
			summary:    "Dispatch a review-thread comment (plus an optional extra prompt) to the session's worker",
			pathParams: []any{controllers.SessionIDParam{}},
			reqBody:    controllers.DispatchCommentRequest{},
			resps: []respUnit{
				{http.StatusOK, controllers.DispatchCommentResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/sessions/{sessionId}/comment-reply", id: "sessionReplyComment", tag: "sessions",
			summary:    "Post a reply comment on a PR review thread on behalf of the session's SCM identity",
			pathParams: []any{controllers.SessionIDParam{}},
			reqBody:    controllers.ReplyCommentRequest{},
			resps: []respUnit{
				{http.StatusOK, controllers.ReplyCommentResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusForbidden, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusServiceUnavailable, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/sessions/{sessionId}/comment-resolve", id: "sessionResolveThread", tag: "sessions",
			summary:    "Mark a PR review thread resolved on behalf of the session's SCM identity",
			pathParams: []any{controllers.SessionIDParam{}},
			reqBody:    controllers.ResolveThreadRequest{},
			resps: []respUnit{
				{http.StatusOK, controllers.ResolveThreadResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusForbidden, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusServiceUnavailable, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/sessions/{sessionId}/activity", id: "setSessionActivity", tag: "sessions",
			summary:    "Report an agent activity-state signal for a session",
			pathParams: []any{controllers.SessionIDParam{}},
			reqBody:    controllers.SetActivityRequest{},
			resps: []respUnit{
				{http.StatusOK, controllers.SetActivityResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodGet, path: "/api/v1/orchestrators", id: "listOrchestrators", tag: "sessions",
			summary: "List orchestrator sessions across projects",
			resps: []respUnit{
				{http.StatusOK, controllers.ListSessionsResponse{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/orchestrators", id: "spawnOrchestrator", tag: "sessions",
			summary: "Spawn an orchestrator session",
			reqBody: controllers.SpawnOrchestratorRequest{},
			resps: []respUnit{
				{http.StatusCreated, controllers.SpawnOrchestratorResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodGet, path: "/api/v1/orchestrators/{id}", id: "getOrchestrator", tag: "sessions",
			summary:    "Fetch one orchestrator session",
			pathParams: []any{controllers.OrchestratorIDParam{}},
			resps: []respUnit{
				{http.StatusOK, controllers.SessionResponse{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
	}
}

// settingsOperations declares the user-editable daemon settings surface (just
// auto-reclaim for now). Both routes are backed by controllers.SettingsService
// (satisfied by *reclaimsettings.Store).
func settingsOperations() []operation {
	return []operation{
		{
			method: http.MethodGet, path: "/api/v1/settings/reclaim", id: "getReclaimSettings", tag: "settings",
			summary: "Fetch the auto-reclaim settings",
			resps: []respUnit{
				{http.StatusOK, controllers.ReclaimSettingsResponse{}},
				{http.StatusInternalServerError, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPut, path: "/api/v1/settings/reclaim", id: "setReclaimSettings", tag: "settings",
			summary: "Replace the auto-reclaim settings",
			reqBody: controllers.SetReclaimSettingsRequest{},
			resps: []respUnit{
				{http.StatusOK, controllers.ReclaimSettingsResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
			},
		},
		{
			method: http.MethodGet, path: "/api/v1/settings/spawn-confirm", id: "getSpawnConfirmSettings", tag: "settings",
			summary: "Fetch the spawn-confirmation gate setting",
			resps: []respUnit{
				{http.StatusOK, controllers.SpawnConfirmSettingsResponse{}},
				{http.StatusInternalServerError, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPut, path: "/api/v1/settings/spawn-confirm", id: "setSpawnConfirmSettings", tag: "settings",
			summary: "Replace the spawn-confirmation gate setting",
			reqBody: controllers.SetSpawnConfirmSettingsRequest{},
			resps: []respUnit{
				{http.StatusOK, controllers.SpawnConfirmSettingsResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
			},
		},
		{
			method: http.MethodGet, path: "/api/v1/settings/auto-nudge", id: "getAutoNudgeSettings", tag: "settings",
			summary: "Fetch the auto-nudge-on-comments gate setting",
			resps: []respUnit{
				{http.StatusOK, controllers.AutoNudgeSettingsResponse{}},
				{http.StatusInternalServerError, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPut, path: "/api/v1/settings/auto-nudge", id: "setAutoNudgeSettings", tag: "settings",
			summary: "Replace the auto-nudge-on-comments gate setting",
			reqBody: controllers.SetAutoNudgeSettingsRequest{},
			resps: []respUnit{
				{http.StatusOK, controllers.AutoNudgeSettingsResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
			},
		},
		{
			method: http.MethodGet, path: "/api/v1/settings/prompts", id: "getSystemPrompts", tag: "settings",
			summary: "Fetch the editable system prompts (default + override per kind)",
			resps: []respUnit{
				{http.StatusOK, controllers.SystemPromptsResponse{}},
				{http.StatusInternalServerError, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPut, path: "/api/v1/settings/prompts/{kind}", id: "setSystemPrompt", tag: "settings",
			summary:    "Set the global base override for a prompt kind",
			pathParams: []any{controllers.PromptKindParam{}},
			reqBody:    controllers.SetSystemPromptRequest{},
			resps: []respUnit{
				{http.StatusOK, controllers.SystemPromptsResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
			},
		},
		{
			method: http.MethodDelete, path: "/api/v1/settings/prompts/{kind}", id: "clearSystemPrompt", tag: "settings",
			summary:    "Reset a prompt kind to its built-in default",
			pathParams: []any{controllers.PromptKindParam{}},
			resps: []respUnit{
				{http.StatusOK, controllers.SystemPromptsResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
			},
		},
		{
			method: http.MethodGet, path: "/api/v1/settings/message-templates", id: "getMessageTemplates", tag: "settings",
			summary: "Fetch the editable nudge message templates (default + override per name)",
			resps: []respUnit{
				{http.StatusOK, controllers.MessageTemplatesResponse{}},
				{http.StatusInternalServerError, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPut, path: "/api/v1/settings/message-templates/{name}", id: "setMessageTemplate", tag: "settings",
			summary:    "Set the override text for a nudge message template",
			pathParams: []any{controllers.MessageTemplateNameParam{}},
			reqBody:    controllers.SetMessageTemplateRequest{},
			resps: []respUnit{
				{http.StatusOK, controllers.MessageTemplatesResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
			},
		},
		{
			method: http.MethodDelete, path: "/api/v1/settings/message-templates/{name}", id: "clearMessageTemplate", tag: "settings",
			summary:    "Reset a nudge message template to its built-in default",
			pathParams: []any{controllers.MessageTemplateNameParam{}},
			resps: []respUnit{
				{http.StatusOK, controllers.MessageTemplatesResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
			},
		},
	}
}

// prOperations declares the PR action operations. These live in the SCM lane:
// the handler delegates to a PRService backed by the SCM provider. A nil
// PRService (SCM not configured) returns 501 for both routes.
func prOperations() []operation {
	return []operation{
		{
			method: http.MethodPost, path: "/api/v1/prs/{id}/merge", id: "mergePR", tag: "prs",
			summary:    "Squash-merge a pull request",
			pathParams: []any{controllers.PRIDParam{}},
			resps: []respUnit{
				{http.StatusOK, controllers.MergePRResponse{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusConflict, envelope.APIError{}},
				{http.StatusUnprocessableEntity, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/prs/{id}/resolve-comments", id: "resolveComments", tag: "prs",
			summary:    "Resolve review threads on a pull request",
			pathParams: []any{controllers.PRIDParam{}},
			reqBody:    nil, // body is optional: omitting it resolves all unresolved threads
			resps: []respUnit{
				{http.StatusOK, controllers.ResolveCommentsResponse{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusUnprocessableEntity, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
	}
}
