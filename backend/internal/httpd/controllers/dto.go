package controllers

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/jira/adf"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/legacyimport"
	agentsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/agent"
	projectsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/project"
	sessionsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/session"
)

// HTTP response envelopes for the projects surface — the SINGLE definition of
// each wire shape. The handlers encode these (envelope.WriteJSON), and
// apispec.Build reflects these same types into openapi.yaml, so the served
// contract and the generated spec can't disagree. The request side needs no
// wrappers: handlers decode the body straight into the project commands
// (projectsvc.AddInput), which apispec also reflects.

// ProjectIDParam is the {id} path parameter shared by the /projects/{id}
// routes. Handlers read it via chi.URLParam (see projectID); it is declared here
// so every wire input/output shape has one home, and apispec.Build reflects it
// as the path parameter.
type ProjectIDParam struct {
	ID string `path:"id" description:"Project identifier (registry key)."`
}

// AgentIDParam is the {agent} path parameter for one-agent catalog probes.
type AgentIDParam struct {
	Agent string `path:"agent" description:"Agent adapter identifier."`
}

// ListProjectsResponse is the body of GET /api/v1/projects.
type ListProjectsResponse struct {
	Projects []projectsvc.Summary `json:"projects"`
}

// ProjectResponse is the { project } body shared by POST /projects (201).
type ProjectResponse struct {
	Project projectsvc.Project `json:"project"`
}

// GetProjectResponse is the { status, project } body of GET /projects/{id},
// where project is oneOf Project|Degraded discriminated by status.
type GetProjectResponse struct {
	Status  string            `json:"status" enum:"ok,degraded"`
	Project ProjectOrDegraded `json:"project"`
}

// ProjectOrDegraded is the discriminated `project` field: exactly one of
// Project/Degraded is set. It marshals as whichever is present (so the handler
// emits the right object) and exposes the oneOf variants to the spec reflector
// (so apispec.Build emits `oneOf: [Project, Degraded]`) — one type, both jobs.
type ProjectOrDegraded struct {
	Project  *projectsvc.Project
	Degraded *projectsvc.Degraded
}

// MarshalJSON encodes whichever variant is set (Project or Degraded).
func (p ProjectOrDegraded) MarshalJSON() ([]byte, error) {
	switch {
	case p.Degraded != nil:
		return json.Marshal(p.Degraded)
	case p.Project != nil:
		return json.Marshal(p.Project)
	default:
		// Unreachable in practice: the handler validates the GetResult via
		// newGetProjectResponse and writes a 500 before committing the 200
		// status, so this never encodes. Kept as a last-resort backstop —
		// erroring is still better than emitting a contract-breaking `null`,
		// though by here the status is already sent, so the real guard is
		// upstream.
		return nil, errEmptyProjectOrDegraded
	}
}

// errEmptyProjectOrDegraded marks a GetResult that set neither variant — a
// Manager-contract violation. newGetProjectResponse returns it so the handler
// can map it to a 500 before any response bytes are written.
var errEmptyProjectOrDegraded = errors.New("controllers: GetResult has neither Project nor Degraded set")

// JSONSchemaOneOf is read by swaggest's reflector (apispec.Build) to emit the
// oneOf for this field; it is not used at runtime.
func (ProjectOrDegraded) JSONSchemaOneOf() []interface{} {
	return []interface{}{projectsvc.Project{}, projectsvc.Degraded{}}
}

// newGetProjectResponse maps the internal GetResult onto the wire envelope —
// the explicit project→httpd boundary the result type exists for. It errors
// when the result sets neither variant, so the handler can return a clean 500
// BEFORE writing the 200 status rather than flushing a truncated body.
func newGetProjectResponse(res projectsvc.GetResult) (GetProjectResponse, error) {
	if res.Project == nil && res.Degraded == nil {
		return GetProjectResponse{}, errEmptyProjectOrDegraded
	}
	return GetProjectResponse{
		Status:  res.Status,
		Project: ProjectOrDegraded{Project: res.Project, Degraded: res.Degraded},
	}, nil
}

// ProjectBranchesResponse is the body of GET /api/v1/projects/{id}/branches.
type ProjectBranchesResponse struct {
	Branches []string `json:"branches"`
}

// SessionIDParam is the {sessionId} path parameter shared by session routes.
type SessionIDParam struct {
	SessionID string `path:"sessionId" description:"Session identifier, e.g. project-1."`
}

// SmokeCheckParam is the {sessionId}/{checkId} path parameters for the
// per-case smoke routes.
type SmokeCheckParam struct {
	SessionID string `path:"sessionId" description:"Session identifier, e.g. project-1."`
	CheckID   string `path:"checkId" description:"Smoke-check case identifier."`
}

// SmokeEvidenceParam is the {sessionId}/{checkId}/{evidenceId} path parameters
// for serving a stored evidence blob.
type SmokeEvidenceParam struct {
	SessionID  string `path:"sessionId" description:"Session identifier, e.g. project-1."`
	CheckID    string `path:"checkId" description:"Smoke-check case identifier."`
	EvidenceID string `path:"evidenceId" description:"Evidence blob identifier."`
}

// ListSessionsQuery is the query string accepted by GET /api/v1/sessions.
type ListSessionsQuery struct {
	Project          string `query:"project,omitempty" description:"Project id filter."`
	Active           *bool  `query:"active,omitempty" description:"When true, return non-terminated sessions; when false, return terminated sessions."`
	OrchestratorOnly *bool  `query:"orchestratorOnly,omitempty" description:"When true, return only orchestrator sessions."`
	Fresh            *bool  `query:"fresh,omitempty" description:"When true, return only fresh non-terminated sessions."`
}

// CleanupSessionsQuery is the query string accepted by POST /api/v1/sessions/cleanup.
type CleanupSessionsQuery struct {
	Project string `query:"project,omitempty" description:"Project id filter. When omitted, clean terminated sessions across all projects."`
}

// DeleteSessionQuery carries the force flag for DELETE /api/v1/sessions/{sessionId}.
type DeleteSessionQuery struct {
	// Force discards uncommitted worktree changes instead of refusing.
	Force bool `query:"force,omitempty" description:"When true, discard uncommitted worktree changes instead of refusing."`
}

// DiffContextParams is the query string accepted by
// GET /api/v1/sessions/{sessionId}/diff-context.
type DiffContextParams struct {
	PrURL string `query:"prUrl" description:"PR URL the comment belongs to."`
	Path  string `query:"path" description:"Repo-relative file path the comment anchors to."`
	Line  int    `query:"line" description:"1-based new-side line number of the anchor."`
	Mode  string `query:"mode" description:"hunk (default) or file." enum:"hunk,file"`
}

// SessionView is the session wire shape: the domain read model plus the
// display-safe branch name and the session's attributed pull requests in the
// curated SessionPRFacts shape. One session can own many PRs (e.g. a stack), so
// prs is a list. The embedded domain.Session.Metadata and domain.Session.PRs
// fields are json:"-"; these curated fields are what serialize.
type SessionView struct {
	domain.Session
	Branch string `json:"branch,omitempty"`
	// WorkspacePath is the session's working directory on disk — the git worktree
	// for a worker, the project root for an orchestrator. Surfaced (curated from
	// the json:"-" domain Metadata) so the desktop app can offer "Open in…"
	// actions (Finder/Terminal/editor) on it. Empty (omitted) when unknown.
	WorkspacePath string `json:"workspacePath,omitempty"`
	// PreviewURL is the browser preview target the desktop app opens for this
	// session, set via POST /sessions/{sessionId}/preview. Empty (omitted) when
	// no preview has been requested. Pulled from the json:"-" domain Metadata.
	PreviewURL string `json:"previewUrl,omitempty"`
	// PreviewRevision bumps on every `ao preview` call (even when previewUrl is
	// unchanged) so the desktop browser panel can re-navigate / refresh on a
	// repeated preview of the same target. Pulled from the json:"-" domain
	// Metadata.
	PreviewRevision int64 `json:"previewRevision,omitempty"`
	// Prompt is the deferred task's prompt. Surfaced (curated from the json:"-"
	// domain Metadata) only for a prepared TODO so the board detail modal can
	// show/edit it; omitted for live sessions to keep the sessions list lean.
	Prompt string           `json:"prompt,omitempty"`
	PRs    []SessionPRFacts `json:"prs"`
	// TokenUsage is the per-session token telemetry summed from the harness
	// transcript. Present only for a claude-code session AO has parsed at least
	// once; omitted entirely for agents without a readable transcript or a session
	// not yet parsed, which the UI renders as "no chip / n/a".
	TokenUsage *SessionTokenUsage `json:"tokenUsage,omitempty"`
}

// SessionTokenUsage is the per-session token telemetry surfaced on the board. The
// four raw buckets (input/cacheCreation/cacheRead/output) and the turn count are
// measured facts summed from the harness transcript's per-assistant-message usage.
// rawTotal, costWeighted, and runaway are DERIVED server-side — the cost multipliers
// (cache-write ×1.25, cache-read ×0.1, output ×5) and the runaway threshold live in
// the daemon so the UI never re-implements them. runaway flags a session whose raw
// total is far into the measured outlier tail (a stuck/looping session).
type SessionTokenUsage struct {
	Input         int64     `json:"input"`
	CacheCreation int64     `json:"cacheCreation"`
	CacheRead     int64     `json:"cacheRead"`
	Output        int64     `json:"output"`
	Turns         int64     `json:"turns"`
	RawTotal      int64     `json:"rawTotal"`
	CostWeighted  int64     `json:"costWeighted"`
	Runaway       bool      `json:"runaway"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

// ListSessionsResponse is the body of GET /api/v1/sessions.
type ListSessionsResponse struct {
	Sessions []SessionView `json:"sessions"`
}

// SpawnSessionRequest is the body of POST /api/v1/sessions.
type SpawnSessionRequest struct {
	ProjectID domain.ProjectID    `json:"projectId"`
	IssueID   domain.IssueID      `json:"issueId,omitempty"`
	Kind      domain.SessionKind  `json:"kind,omitempty" enum:"worker,orchestrator"`
	Harness   domain.AgentHarness `json:"harness,omitempty" enum:"claude-code,codex,aider,opencode,grok,droid,amp,agy,crush,cursor,qwen,copilot,goose,auggie,continue,devin,cline,kimi,kiro,kilocode,vibe,pi,autohand"`
	Branch    string              `json:"branch,omitempty"`
	// BaseBranch is the branch the new worktree is created from. Empty falls
	// back to the project's configured default branch.
	BaseBranch string `json:"baseBranch,omitempty"`
	// AutoNameBranch asks the manager to generate a gitflow branch name via
	// the session's agent (one-shot) when Branch is empty.
	AutoNameBranch bool `json:"autoNameBranch,omitempty"`
	// Prompt is the initial task for the agent. The 128 KiB cap mirrors
	// maxPromptLen in sessions.go — a defensive bound well under ARG_MAX, not an
	// agent/model context limit. Keep the two in sync.
	Prompt string `json:"prompt,omitempty" maxLength:"131072"`
	// DisplayName is the sidebar label for the session, capped at 20 characters.
	// `ao spawn --name` always sets it; other clients (e.g. the desktop new-task
	// dialog) may omit it and fall back to the session id in the read model.
	DisplayName string `json:"displayName,omitempty" maxLength:"20"`
	// StartImmediately controls deferral. Absent/null (the default) or true
	// spawns the session now — the unchanged behavior. false stages it as a
	// prepared TODO on the board (no branch/worktree/tmux until Start).
	StartImmediately *bool `json:"startImmediately,omitempty"`
	// PRTarget is the branch this session's PR merges INTO — distinct from
	// BaseBranch, which only says where the worktree was cut from. Recorded on
	// every session, immediate or deferred, and surfaced in the Summary tab.
	// Absent/empty resolves to the base branch (itself the project default when
	// unstated) and the RESOLVED value is what gets stored, so a session's target
	// is always an answer on the row rather than something callers re-derive.
	PRTarget string `json:"prTarget,omitempty"`
	// CreatedBy is the orchestrator session id queuing a deferred TODO, kept for
	// the report-back. `ao spawn --todo` sets it from AO_SESSION_ID.
	CreatedBy domain.SessionID `json:"createdBy,omitempty"`
	// KeepWarmOnMerge marks a worker expected to open more PRs: when its PR merges
	// it SUSPENDS in place (card stays on the board, resumable) instead of
	// terminating to Done (feature/merge-suspend-in-place). Default false — an
	// ordinary single-PR worker still auto-archives on merge.
	KeepWarmOnMerge bool `json:"keepWarmOnMerge,omitempty"`
	// TaskSize is the ceremony level for the worker (`ao spawn --task-size`):
	// mechanical / standard / deep. Absent/empty means standard (full ceremony). A
	// mechanical task is authorized in the worker prompt to skip the process
	// skills. Persisted on the session; not surfaced on the read model.
	TaskSize domain.TaskSize `json:"taskSize,omitempty" enum:"mechanical,standard,deep"`
}

// UpdateTodoSpecRequest is the body of PATCH /api/v1/sessions/{sessionId}/spec:
// edits to a prepared TODO's spec before it is started. Every field is optional
// — an absent field is left unchanged; a present field is set (including to
// empty). Rejected once the task has started.
type UpdateTodoSpecRequest struct {
	DisplayName    *string              `json:"displayName,omitempty" maxLength:"20"`
	Harness        *domain.AgentHarness `json:"harness,omitempty" enum:"claude-code,codex,aider,opencode,grok,droid,amp,agy,crush,cursor,qwen,copilot,goose,auggie,continue,devin,cline,kimi,kiro,kilocode,vibe,pi,autohand"`
	Branch         *string              `json:"branch,omitempty"`
	BaseBranch     *string              `json:"baseBranch,omitempty"`
	PRTarget       *string              `json:"prTarget,omitempty"`
	Prompt         *string              `json:"prompt,omitempty" maxLength:"4096"`
	AutoNameBranch *bool                `json:"autoNameBranch,omitempty"`
}

// SessionResponse is the { session } body shared by session create/get.
type SessionResponse struct {
	Session SessionView `json:"session"`
}

// SessionPreviewResponse is the body of GET /api/v1/sessions/{sessionId}/preview.
type SessionPreviewResponse struct {
	SessionID  domain.SessionID `json:"sessionId"`
	PreviewURL string           `json:"previewUrl,omitempty"`
	Entry      string           `json:"entry,omitempty"`
}

// RenameSessionRequest is the body of PATCH /api/v1/sessions/{sessionId}.
type RenameSessionRequest struct {
	DisplayName string `json:"displayName" minLength:"1"`
}

// SetSessionPreviewRequest is the body of POST /api/v1/sessions/{sessionId}/preview.
// An empty url asks the daemon to autodetect a static entry point in the
// session workspace; a non-empty url is used verbatim as the preview target.
type SetSessionPreviewRequest struct {
	URL string `json:"url,omitempty" description:"Preview target URL. When empty, the daemon autodetects a static entry point in the session workspace."`
}

// SetAutoNudgeRequest is the body of PUT /api/v1/sessions/{sessionId}/auto-nudge.
// Override is a tri-state: true/false set an explicit per-session override; null
// clears it so the session inherits the global auto-nudge default.
type SetAutoNudgeRequest struct {
	Override *bool `json:"override"`
}

// SetAutoResolveRequest is the body of PUT
// /api/v1/sessions/{sessionId}/auto-resolve. Override is a tri-state: true/false
// set the per-session gate explicitly; null clears it (OFF — there is no global
// auto-resolve default to inherit).
type SetAutoResolveRequest struct {
	Override *bool `json:"override"`
}

// SetSessionKeepWarmRequest is the body of PUT
// /api/v1/sessions/{sessionId}/keep-warm: enable/disable
// suspend-in-place-on-merge for a worker.
type SetSessionKeepWarmRequest struct {
	Enabled bool `json:"enabled"`
}

// SetSessionTargetRequest is the body of PUT
// /api/v1/sessions/{sessionId}/target: change the branch this session's work
// merges into. When the session owns an OPEN pull/merge request, the daemon
// retargets that request on the forge FIRST and persists only if the forge
// accepts — so a rejected retarget leaves AO's stored value untouched rather
// than letting the two disagree.
type SetSessionTargetRequest struct {
	TargetBranch string `json:"targetBranch" minLength:"1"`
}

// RenameSessionResponse is the body of PATCH /api/v1/sessions/{sessionId}.
type RenameSessionResponse struct {
	OK          bool             `json:"ok"`
	SessionID   domain.SessionID `json:"sessionId"`
	DisplayName string           `json:"displayName"`
}

// RestoreSessionResponse is the body of POST /api/v1/sessions/{sessionId}/restore.
type RestoreSessionResponse struct {
	OK        bool             `json:"ok"`
	SessionID domain.SessionID `json:"sessionId"`
	Session   SessionView      `json:"session"`
}

// KillSessionResponse is the body of POST /api/v1/sessions/{sessionId}/kill.
type KillSessionResponse struct {
	OK        bool             `json:"ok"`
	SessionID domain.SessionID `json:"sessionId"`
	Freed     bool             `json:"freed,omitempty"`
}

// RestartSessionResponse is the body of POST /api/v1/sessions/{sessionId}/restart.
type RestartSessionResponse struct {
	OK        bool             `json:"ok"`
	SessionID domain.SessionID `json:"sessionId"`
	Session   SessionView      `json:"session"`
}

// WakeSessionResponse is the body of POST /api/v1/sessions/{sessionId}/wake:
// the fresh read model after resuming a suspended session or resetting a live
// session's idle-close countdown.
type WakeSessionResponse struct {
	OK        bool             `json:"ok"`
	SessionID domain.SessionID `json:"sessionId"`
	Session   SessionView      `json:"session"`
}

// DeleteSessionResponse is the body of DELETE /api/v1/sessions/{sessionId}.
type DeleteSessionResponse struct {
	OK        bool             `json:"ok"`
	SessionID domain.SessionID `json:"sessionId"`
}

// RollbackSessionResponse is the body of POST /api/v1/sessions/{sessionId}/rollback.
// Exactly one of Deleted/Killed is true on a successful rollback; both are
// false when the session was already absent or already terminated (benign).
type RollbackSessionResponse struct {
	OK        bool             `json:"ok"`
	SessionID domain.SessionID `json:"sessionId"`
	Deleted   bool             `json:"deleted,omitempty"`
	Killed    bool             `json:"killed,omitempty"`
}

// CleanupSkippedSession is one terminal session whose workspace cleanup
// preserved rather than reclaimed (a dirty worktree is never force-deleted),
// with the user-facing reason.
type CleanupSkippedSession struct {
	SessionID domain.SessionID `json:"sessionId"`
	Reason    string           `json:"reason"`
}

// CleanupSessionsResponse is the body of POST /api/v1/sessions/cleanup.
type CleanupSessionsResponse struct {
	OK      bool                    `json:"ok"`
	Cleaned []domain.SessionID      `json:"cleaned"`
	Skipped []CleanupSkippedSession `json:"skipped"`
}

// SendSessionMessageRequest is the body of POST /api/v1/sessions/{sessionId}/send.
type SendSessionMessageRequest struct {
	Message string `json:"message" minLength:"1" maxLength:"4096"`
}

// SendSessionMessageResponse is the body of POST /api/v1/sessions/{sessionId}/send.
type SendSessionMessageResponse struct {
	OK        bool             `json:"ok"`
	SessionID domain.SessionID `json:"sessionId"`
	Message   string           `json:"message"`
}

// DispatchCommentRequest is the body of POST /api/v1/sessions/{sessionId}/comment-dispatch.
type DispatchCommentRequest struct {
	PrURL       string `json:"prUrl"`
	ThreadID    string `json:"threadId"`
	ExtraPrompt string `json:"extraPrompt,omitempty"`
}

// DispatchCommentResponse acknowledges a manual comment dispatch to the worker.
type DispatchCommentResponse struct {
	OK        bool             `json:"ok"`
	SessionID domain.SessionID `json:"sessionId"`
}

// ReplyCommentRequest is the body of POST /api/v1/sessions/{sessionId}/comment-reply.
type ReplyCommentRequest struct {
	PrURL    string `json:"prUrl"`
	ThreadID string `json:"threadId"`
	Body     string `json:"body"`
}

// ReplyCommentResponse returns the newly created reply comment.
type ReplyCommentResponse struct {
	OK      bool                   `json:"ok"`
	Comment SessionPRThreadComment `json:"comment"`
}

// ResolveThreadRequest is the body of POST /api/v1/sessions/{sessionId}/comment-resolve.
type ResolveThreadRequest struct {
	PrURL    string `json:"prUrl"`
	ThreadID string `json:"threadId"`
}

// ResolveThreadResponse acknowledges a PR review thread resolution.
type ResolveThreadResponse struct {
	OK        bool             `json:"ok"`
	SessionID domain.SessionID `json:"sessionId"`
	Resolved  bool             `json:"resolved"`
}

// SessionPRFacts is the pull-request read shape returned under session PR routes.
type SessionPRFacts struct {
	URL            string                `json:"url"`
	Number         int                   `json:"number"`
	State          string                `json:"state" enum:"draft,open,merged,closed"`
	CI             domain.CIState        `json:"ci" enum:"unknown,pending,passing,failing"`
	Review         domain.ReviewDecision `json:"review" enum:"none,approved,changes_requested,review_required"`
	Mergeability   domain.Mergeability   `json:"mergeability" enum:"unknown,mergeable,conflicting,blocked,unstable"`
	ReviewComments bool                  `json:"reviewComments"`
	UpdatedAt      time.Time             `json:"updatedAt"`
}

// SessionPRSummary is the concise desktop SCM read model returned by GET
// /sessions/{sessionId}/pr. It intentionally omits CI log tails and review
// comment bodies.
type SessionPRSummary struct {
	URL              string                       `json:"url"`
	HTMLURL          string                       `json:"htmlUrl,omitempty"`
	Number           int                          `json:"number"`
	Title            string                       `json:"title"`
	State            domain.PRState               `json:"state" enum:"draft,open,merged,closed"`
	Provider         string                       `json:"provider" enum:"github,gitlab"`
	Repo             string                       `json:"repo"`
	Author           string                       `json:"author"`
	SourceBranch     string                       `json:"sourceBranch"`
	TargetBranch     string                       `json:"targetBranch"`
	HeadSHA          string                       `json:"headSha"`
	Additions        int                          `json:"additions"`
	Deletions        int                          `json:"deletions"`
	ChangedFiles     int                          `json:"changedFiles"`
	CI               SessionPRCISummary           `json:"ci"`
	Review           SessionPRReviewSummary       `json:"review"`
	Mergeability     SessionPRMergeabilitySummary `json:"mergeability"`
	UpdatedAt        time.Time                    `json:"updatedAt"`
	ObservedAt       time.Time                    `json:"observedAt,omitempty"`
	CIObservedAt     time.Time                    `json:"ciObservedAt,omitempty"`
	ReviewObservedAt time.Time                    `json:"reviewObservedAt,omitempty"`
}

// SessionPRCISummary is the CI status block for a session PR summary.
type SessionPRCISummary struct {
	State         domain.CIState          `json:"state" enum:"unknown,pending,passing,failing"`
	FailingChecks []SessionPRFailingCheck `json:"failingChecks"`
}

// SessionPRFailingCheck is one failed or cancelled CI check for a PR.
type SessionPRFailingCheck struct {
	Name       string               `json:"name"`
	Status     domain.PRCheckStatus `json:"status" enum:"failed,cancelled"`
	Conclusion string               `json:"conclusion"`
	URL        string               `json:"url,omitempty"`
}

// SessionPRReviewSummary is the review state block for a session PR summary.
type SessionPRReviewSummary struct {
	Decision                   domain.ReviewDecision         `json:"decision" enum:"none,approved,changes_requested,review_required"`
	HasUnresolvedHumanComments bool                          `json:"hasUnresolvedHumanComments"`
	UnresolvedBy               []SessionPRUnresolvedReviewer `json:"unresolvedBy"`
	// ApprovalsCount is the number of distinct human approvers observed. Omitted
	// when zero; the renderer treats an absent count as 0.
	ApprovalsCount int `json:"approvalsCount,omitempty"`
	// RequiredApprovals is the effective approval threshold, omitted when no rule
	// applies or the SCM exposes no numeric threshold. Absent ⇒ the surfaces keep
	// their pre-approval-progress behavior.
	RequiredApprovals *int `json:"requiredApprovals,omitempty"`
	// ApprovalRuleSource is which rule set the threshold: "scm", "ao", or "none".
	ApprovalRuleSource string `json:"approvalRuleSource,omitempty" enum:"none,ao,scm"`
}

// SessionPRUnresolvedReviewer groups unresolved human comments by reviewer.
type SessionPRUnresolvedReviewer struct {
	ReviewerID string                       `json:"reviewerId"`
	Count      int                          `json:"count"`
	Links      []SessionPRReviewCommentLink `json:"links"`
	ReviewURL  string                       `json:"reviewUrl,omitempty"`
	IsBot      bool                         `json:"isBot,omitempty"`
}

// SessionPRReviewCommentLink points to one unresolved review comment.
type SessionPRReviewCommentLink struct {
	URL  string `json:"url,omitempty"`
	File string `json:"file,omitempty"`
	Line int    `json:"line,omitempty"`
}

// SessionPRMergeabilitySummary is the mergeability block for a session PR summary.
type SessionPRMergeabilitySummary struct {
	State         domain.Mergeability     `json:"state" enum:"unknown,mergeable,conflicting,blocked,unstable"`
	Reasons       []string                `json:"reasons"`
	PRURL         string                  `json:"prUrl"`
	ConflictFiles []SessionPRConflictFile `json:"conflictFiles,omitempty"`
}

// SessionPRConflictFile is one file involved in a PR merge conflict.
type SessionPRConflictFile struct {
	Path string `json:"path"`
	URL  string `json:"url,omitempty"`
}

// ListSessionPRsResponse is the body of GET /sessions/{sessionId}/pr.
type ListSessionPRsResponse struct {
	SessionID domain.SessionID   `json:"sessionId"`
	PRs       []SessionPRSummary `json:"prs"`
}

// JiraContextResponse is the body of GET /sessions/{sessionId}/jira — the
// display-only Jira context for a session bound to a "jira:<KEY>" issue id.
// Linked is false when the session has no Jira binding (the UI renders nothing).
// Issue is present when the bound key resolved; FetchError carries a user-facing
// message when the session IS Jira-linked but the live fetch failed (missing
// issue, auth, or Jira unavailable) — returned as 200 so a Jira hiccup never
// breaks the Summary tab.
type JiraContextResponse struct {
	SessionID  domain.SessionID `json:"sessionId"`
	Linked     bool             `json:"linked"`
	Issue      *JiraIssue       `json:"issue,omitempty"`
	FetchError string           `json:"fetchError,omitempty"`
}

// JiraIssue is the display projection of one Jira issue. Structured fields drive
// the surrounding UI; Description is the faithful ADF render tree (never parsed
// into cards).
type JiraIssue struct {
	Key   string `json:"key"`
	URL   string `json:"url,omitempty"`
	Type  string `json:"type,omitempty"`
	Title string `json:"title,omitempty"`
	// Status is the human status name; StatusCategory is Jira's category key
	// (new|indeterminate|done); StatusColor is the category colorName. The UI
	// tints the status pill from the category, not the free-form name.
	Status         string `json:"status,omitempty"`
	StatusCategory string `json:"statusCategory,omitempty"`
	StatusColor    string `json:"statusColor,omitempty"`
	Priority       string `json:"priority,omitempty"`
	Assignee       string `json:"assignee,omitempty"`
	Reporter       string `json:"reporter,omitempty"`
	// Parent is the issue's parent (set for subtasks / epic children), so the
	// Browse Jira detail view can show a clickable parent breadcrumb.
	Parent      *JiraParentRef `json:"parent,omitempty"`
	Sprint      *JiraSprint    `json:"sprint,omitempty"`
	Description []adf.Node     `json:"description,omitempty"`
	Subtasks    []JiraSubtask  `json:"subtasks,omitempty"`
	// Attachments the description's media nodes resolve against (matched by
	// filename) to render inline previews; bytes stream via the session
	// attachment download proxy.
	Attachments []JiraAttachment `json:"attachments,omitempty"`
}

// JiraAttachment is one uploaded file on an issue: enough for the Summary tab to
// match a description media node by filename and stream its bytes for preview.
type JiraAttachment struct {
	ID       string `json:"id"`
	Filename string `json:"filename,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
}

// JiraAttachmentParam addresses one attachment's bytes for the download proxy.
type JiraAttachmentParam struct {
	SessionID    string `path:"sessionId" description:"Session identifier, e.g. project-1."`
	AttachmentID string `path:"attachmentId" description:"Jira attachment id (numeric)."`
}

// JiraSprint is the issue's current/most-relevant sprint.
type JiraSprint struct {
	Name      string `json:"name"`
	State     string `json:"state,omitempty"`
	StartDate string `json:"startDate,omitempty"`
	EndDate   string `json:"endDate,omitempty"`
}

// JiraSubtask is a display-only child issue row (status movable via the Move
// action).
type JiraSubtask struct {
	Key            string `json:"key"`
	Title          string `json:"title,omitempty"`
	Type           string `json:"type,omitempty"`
	Status         string `json:"status,omitempty"`
	StatusCategory string `json:"statusCategory,omitempty"`
	StatusColor    string `json:"statusColor,omitempty"`
}

// JiraTransition is one available status transition, read LIVE from Jira (never
// hardcoded — the set differs per issue type and current status).
type JiraTransition struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	To         string `json:"to,omitempty"`         // target status name
	ToCategory string `json:"toCategory,omitempty"` // target status category (new|indeterminate|done)
	ToColor    string `json:"toColor,omitempty"`
}

// JiraTransitionsResponse is the body of GET /sessions/{sessionId}/jira/transitions.
type JiraTransitionsResponse struct {
	SessionID   domain.SessionID `json:"sessionId"`
	Transitions []JiraTransition `json:"transitions"`
}

// JiraMoveRequest is the body of POST /sessions/{sessionId}/jira/move — apply a
// status transition by its id. This is the ONLY write AO makes to Jira; it
// carries nothing but the transition id (no comment, no field edit) and an
// optional target key.
type JiraMoveRequest struct {
	TransitionID string `json:"transitionId"`
	// IssueKey optionally targets a subtask of the session's bound issue instead
	// of the bound issue itself. Empty = the bound issue (the original behavior).
	IssueKey string `json:"issueKey,omitempty"`
}

// JiraMoveResponse reports the issue's status after a successful move so the UI
// can update the pill without a round trip.
type JiraMoveResponse struct {
	SessionID      domain.SessionID `json:"sessionId"`
	Key            string           `json:"key"`
	Status         string           `json:"status,omitempty"`
	StatusCategory string           `json:"statusCategory,omitempty"`
	StatusColor    string           `json:"statusColor,omitempty"`
}

// JiraIssueResponse is the body of GET /jira/issue — one issue's full display
// projection, read live by key for the pre-session Browse Jira detail view.
type JiraIssueResponse struct {
	Issue *JiraIssue `json:"issue,omitempty"`
}

// JiraIssueQuery is the query string of GET /jira/issue and GET
// /jira/issue/transitions (the pre-session detail view reads by key).
type JiraIssueQuery struct {
	Key string `query:"key" description:"The issue key to read (e.g. PROJ-123)."`
}

// JiraIssueMoveRequest is the body of POST /jira/issue/move — a by-key status move
// (the pre-session detail view's Move-status). Carries only a key + transition id;
// still the ONE sanctioned Jira write.
type JiraIssueMoveRequest struct {
	Key          string `json:"key" description:"The issue key to move (e.g. PROJ-123)."`
	TransitionID string `json:"transitionId" description:"The chosen transition id (read live from the issue)."`
}

// JiraIssueSummary is one search-result / picker row — the structured fields
// needed to render and pick an issue (the full description/subtasks live in the
// display projection, JiraIssue).
type JiraIssueSummary struct {
	Key            string `json:"key"`
	Type           string `json:"type,omitempty"`
	Title          string `json:"title,omitempty"`
	Status         string `json:"status,omitempty"`
	StatusCategory string `json:"statusCategory,omitempty"`
	StatusColor    string `json:"statusColor,omitempty"`
	Assignee       string `json:"assignee,omitempty"`
	// AssigneeAccountId is the assignee's opaque Jira accountId, so the Browse Jira
	// assignee dropdown can filter by assignee server-side (JQL) rather than paring
	// down a capped page. Empty when the issue is unassigned.
	AssigneeAccountId string `json:"assigneeAccountId,omitempty"`
	// Parent is the row's parent issue (set for subtasks / epic children), so Browse
	// Jira can nest a subtask beneath its parent like the backlog. nil for top-level.
	Parent *JiraParentRef `json:"parent,omitempty"`
	// Sprint is the row's current/most-relevant sprint, so Browse Jira can group
	// results by sprint like the Jira board. nil when the issue is in no sprint.
	Sprint *JiraSprint `json:"sprint,omitempty"`
	URL    string      `json:"url,omitempty"`
}

// JiraParentRef is a summary row's parent issue (key + title).
type JiraParentRef struct {
	Key   string `json:"key"`
	Title string `json:"title,omitempty"`
}

// JiraSearchResponse is the body of GET /jira/search — matching issues for a
// free-text query (or exact key), read live via REST.
type JiraSearchResponse struct {
	Issues []JiraIssueSummary `json:"issues"`
}

// JiraProject is one Jira project for the project picker.
type JiraProject struct {
	Key  string `json:"key"`
	Name string `json:"name,omitempty"`
}

// JiraProjectsResponse is the body of GET /jira/projects.
type JiraProjectsResponse struct {
	Projects []JiraProject `json:"projects"`
}

// JiraMyselfResponse is the body of GET /jira/myself — the authenticated Jira
// account, so Browse Jira can highlight the viewer's own rows. AccountID is empty
// when Jira access is unconfigured (the endpoint still 200s so the UI degrades).
type JiraMyselfResponse struct {
	AccountID   string `json:"accountId"`
	DisplayName string `json:"displayName,omitempty"`
}

// JiraSearchQuery is the query string of GET /jira/search.
type JiraSearchQuery struct {
	Q            string `query:"q" description:"Free-text query, or an exact issue key (e.g. PROJ-123)."`
	Project      string `query:"project,omitempty" description:"Optional project key to scope the search to."`
	Assignee     string `query:"assignee,omitempty" description:"Optional assignee accountId to filter by, or 'unassigned' for issues with no assignee."`
	Type         string `query:"type,omitempty" description:"Optional comma-separated issue-type names to filter by (e.g. Story,Bug). Empty matches all types."`
	HideDone     bool   `query:"hideDone,omitempty" description:"When true, exclude done issues (statusCategory != Done)."`
	ActiveSprint bool   `query:"activeSprint,omitempty" description:"When true, only issues in an open sprint (sprint in openSprints())."`
	JQL          string `query:"jql,omitempty" description:"Raw advanced JQL. When set, it drives the search verbatim and the structured filters above are ignored."`
}

// JiraProjectsQuery is the query string of GET /jira/projects.
type JiraProjectsQuery struct {
	Q string `query:"q,omitempty" description:"Optional filter matched against project key/name."`
}

// JiraTransitionsQuery is the query string of GET /sessions/{sessionId}/jira/transitions.
type JiraTransitionsQuery struct {
	Key string `query:"key,omitempty" description:"Optional issue key (a subtask of the bound issue) to list transitions for instead of the bound issue."`
}

// JiraLinkRequest is the body of PUT /sessions/{sessionId}/jira — bind an
// existing session to a Jira issue after the fact.
type JiraLinkRequest struct {
	IssueKey string `json:"issueKey" description:"Jira issue key to bind (e.g. PROJ-123)."`
}

// JiraLinkResponse reports a session's Jira binding after a link or unlink.
// Linked is false after an unlink; Issue is the resolved issue on a link.
type JiraLinkResponse struct {
	SessionID domain.SessionID  `json:"sessionId"`
	Linked    bool              `json:"linked"`
	Issue     *JiraIssueSummary `json:"issue,omitempty"`
}

// NewSessionPRSummary maps the service PR summary model to its HTTP DTO.
func NewSessionPRSummary(in sessionsvc.PRSummary) SessionPRSummary {
	return SessionPRSummary{
		URL:              in.URL,
		HTMLURL:          in.HTMLURL,
		Number:           in.Number,
		Title:            in.Title,
		State:            in.State,
		Provider:         in.Provider,
		Repo:             in.Repo,
		Author:           in.Author,
		SourceBranch:     in.SourceBranch,
		TargetBranch:     in.TargetBranch,
		HeadSHA:          in.HeadSHA,
		Additions:        in.Additions,
		Deletions:        in.Deletions,
		ChangedFiles:     in.ChangedFiles,
		CI:               newSessionPRCISummary(in.CI),
		Review:           newSessionPRReviewSummary(in.Review),
		Mergeability:     newSessionPRMergeabilitySummary(in.Mergeability),
		UpdatedAt:        in.UpdatedAt,
		ObservedAt:       in.ObservedAt,
		CIObservedAt:     in.CIObservedAt,
		ReviewObservedAt: in.ReviewObservedAt,
	}
}

func newSessionPRCISummary(in sessionsvc.PRCISummary) SessionPRCISummary {
	checks := make([]SessionPRFailingCheck, 0, len(in.FailingChecks))
	for _, ch := range in.FailingChecks {
		checks = append(checks, SessionPRFailingCheck{Name: ch.Name, Status: ch.Status, Conclusion: ch.Conclusion, URL: ch.URL})
	}
	return SessionPRCISummary{State: in.State, FailingChecks: checks}
}

func newSessionPRReviewSummary(in sessionsvc.PRReviewSummary) SessionPRReviewSummary {
	reviewers := make([]SessionPRUnresolvedReviewer, 0, len(in.UnresolvedBy))
	for _, reviewer := range in.UnresolvedBy {
		links := make([]SessionPRReviewCommentLink, 0, len(reviewer.Links))
		for _, link := range reviewer.Links {
			links = append(links, SessionPRReviewCommentLink{URL: link.URL, File: link.File, Line: link.Line})
		}
		reviewers = append(reviewers, SessionPRUnresolvedReviewer{ReviewerID: reviewer.ReviewerID, Count: reviewer.Count, Links: links, ReviewURL: reviewer.ReviewURL, IsBot: reviewer.IsBot})
	}
	return SessionPRReviewSummary{
		Decision:                   in.Decision,
		HasUnresolvedHumanComments: in.HasUnresolvedHumanComments,
		UnresolvedBy:               reviewers,
		ApprovalsCount:             in.ApprovalsCount,
		RequiredApprovals:          in.RequiredApprovals,
		ApprovalRuleSource:         in.ApprovalRuleSource,
	}
}

func newSessionPRMergeabilitySummary(in sessionsvc.PRMergeabilitySummary) SessionPRMergeabilitySummary {
	files := make([]SessionPRConflictFile, 0, len(in.ConflictFiles))
	for _, file := range in.ConflictFiles {
		files = append(files, SessionPRConflictFile{Path: file.Path, URL: file.URL})
	}
	return SessionPRMergeabilitySummary{State: in.State, Reasons: in.Reasons, PRURL: in.PRURL, ConflictFiles: files}
}

// ListSessionPRCommentsResponse is the body of GET /sessions/{sessionId}/pr-comments.
type ListSessionPRCommentsResponse struct {
	SessionID domain.SessionID        `json:"sessionId"`
	PRs       []SessionPRCommentGroup `json:"prs"`
}

// SessionPRCommentGroup is one PR's review threads.
type SessionPRCommentGroup struct {
	PrURL    string                   `json:"prUrl"`
	HtmlURL  string                   `json:"htmlUrl"`
	Provider string                   `json:"provider"`
	Number   int                      `json:"number"`
	HeadSHA  string                   `json:"headSha"`
	Threads  []SessionPRCommentThread `json:"threads"`
}

// SessionPRCommentThread is a review thread anchored to a file/line.
type SessionPRCommentThread struct {
	ThreadID string                   `json:"threadId"`
	Path     string                   `json:"path"`
	Line     int                      `json:"line"`
	Resolved bool                     `json:"resolved"`
	IsBot    bool                     `json:"isBot"`
	Comments []SessionPRThreadComment `json:"comments"`
}

// SessionPRThreadComment is one review comment.
type SessionPRThreadComment struct {
	ID       string `json:"id"`
	Author   string `json:"author"`
	Body     string `json:"body"`
	URL      string `json:"url"`
	Resolved bool   `json:"resolved"`
	IsBot    bool   `json:"isBot"`
	// System is true for provider-generated system notes (e.g. GitLab's
	// "changed this line in version N of the diff"); the UI renders these as a
	// de-emphasized activity line rather than a user comment.
	System    bool   `json:"system"`
	CreatedAt string `json:"createdAt"`
}

// newSessionPRThreadComment maps a service review comment to its wire DTO,
// formatting CreatedAt as RFC3339 (empty string when zero).
func newSessionPRThreadComment(c sessionsvc.PRThreadComment) SessionPRThreadComment {
	createdAt := ""
	if !c.CreatedAt.IsZero() {
		createdAt = c.CreatedAt.UTC().Format(time.RFC3339)
	}
	return SessionPRThreadComment{
		ID: c.ID, Author: c.Author, Body: c.Body, URL: c.URL,
		Resolved: c.Resolved, IsBot: c.IsBot, System: c.System, CreatedAt: createdAt,
	}
}

// sessionPRCommentGroups maps service models to wire DTOs.
func sessionPRCommentGroups(groups []sessionsvc.PRCommentGroup) []SessionPRCommentGroup {
	out := make([]SessionPRCommentGroup, 0, len(groups))
	for _, g := range groups {
		threads := make([]SessionPRCommentThread, 0, len(g.Threads))
		for _, t := range g.Threads {
			comments := make([]SessionPRThreadComment, 0, len(t.Comments))
			for _, c := range t.Comments {
				comments = append(comments, newSessionPRThreadComment(c))
			}
			threads = append(threads, SessionPRCommentThread{
				ThreadID: t.ThreadID, Path: t.Path, Line: t.Line,
				Resolved: t.Resolved, IsBot: t.IsBot, Comments: comments,
			})
		}
		out = append(out, SessionPRCommentGroup{
			PrURL: g.PRURL, HtmlURL: g.HTMLURL, Provider: g.Provider,
			Number: g.Number, HeadSHA: g.HeadSHA, Threads: threads,
		})
	}
	return out
}

// DiffContextResponse is the body of GET /sessions/{sessionId}/diff-context.
type DiffContextResponse struct {
	Available bool                 `json:"available"`
	Mode      string               `json:"mode"`
	Path      string               `json:"path"`
	Lines     []DiffContextLineDTO `json:"lines"`
	Truncated bool                 `json:"truncated"`
}

// DiffContextLineDTO is one classified code-context line.
type DiffContextLineDTO struct {
	Kind    string `json:"kind"`
	OldLine int    `json:"oldLine"`
	NewLine int    `json:"newLine"`
	Text    string `json:"text"`
}

// diffContextResponse maps the service result to the wire DTO.
func diffContextResponse(res sessionsvc.DiffContextResult) DiffContextResponse {
	lines := make([]DiffContextLineDTO, 0, len(res.Lines))
	for _, l := range res.Lines {
		lines = append(lines, DiffContextLineDTO{Kind: l.Kind, OldLine: l.OldLine, NewLine: l.NewLine, Text: l.Text})
	}
	return DiffContextResponse{Available: res.Available, Mode: res.Mode, Path: res.Path, Lines: lines, Truncated: res.Truncated}
}

// WorkspaceResolveParams is the query string accepted by
// GET /api/v1/sessions/{sessionId}/workspace/resolve.
type WorkspaceResolveParams struct {
	Ref string `query:"ref" description:"File reference printed in the terminal: an absolute path, a ~/ path, a workspace-relative path, or a bare filename."`
}

// WorkspaceResolveCandidateDTO is one path a terminal file reference maps to.
type WorkspaceResolveCandidateDTO struct {
	// Path is workspace-relative for a file inside the session's workspace,
	// absolute for one outside it.
	Path string `json:"path"`
	// InWorkspace reports whether the file lives inside the session's workspace.
	// The Files tab reveals a clicked reference in its tree only when this is
	// true; a reference outside the workspace keeps the standalone viewer.
	//
	// Read this flag rather than testing Path's shape: an absolute or `~/` ref is
	// resolved anywhere on disk by design, and only the server can decide
	// containment correctly (it must compare symlink-resolved paths — a macOS
	// worktree under /var is really /private/var).
	InWorkspace bool `json:"inWorkspace"`
}

// WorkspaceResolveResponse is the body of the workspace/resolve route: the
// candidates a terminal file reference maps to. Empty when nothing matches.
type WorkspaceResolveResponse struct {
	Ref        string                         `json:"ref"`
	Candidates []WorkspaceResolveCandidateDTO `json:"candidates"`
}

// WorkspaceFileParams is the query string accepted by
// GET /api/v1/sessions/{sessionId}/workspace/file.
type WorkspaceFileParams struct {
	Path string `query:"path" description:"Path of the file to read (from workspace/resolve): workspace-relative, or absolute for a file outside the workspace."`
}

// WorkspaceFileResponse is the body of the workspace/file route: a file's
// content as context lines plus the per-line map of its uncommitted changes
// (working tree vs HEAD, empty when the file is not inside a git repository).
type WorkspaceFileResponse struct {
	Available bool                 `json:"available"`
	Path      string               `json:"path"`
	Lines     []DiffContextLineDTO `json:"lines"`
	// Reason explains an available=false response: "too_large" or "binary".
	// Empty when the file is displayable.
	Reason       string          `json:"reason,omitempty"`
	ChangedLines []LineChangeDTO `json:"changedLines"`
	Truncated    bool            `json:"truncated"`
}

// LineChangeDTO is one uncommitted-change gutter marker in new-side line
// coordinates (1-based, inclusive). Kind is "added", "modified", or "removed";
// a "removed" marker is zero-height (Start == End).
type LineChangeDTO struct {
	Start int    `json:"start"`
	End   int    `json:"end"`
	Kind  string `json:"kind"`
}

// WorkspaceChangesResponse is the body of the workspace/changes route: the
// files differing between the session's branch (working tree included) and its
// resolved target branch.
type WorkspaceChangesResponse struct {
	Available bool `json:"available"`
	// Reason explains an available=false response: "no_workspace" (the worktree
	// is gone from disk), "not_a_repo", or "no_target_branch". Empty when the
	// list is usable.
	Reason string `json:"reason,omitempty"`
	// TargetBranch is the resolved comparison branch and TargetSource says how
	// it was resolved ("pr", "session_pr_target", "session_base", "project",
	// "git_origin_head"), so the UI can distinguish a certain target from an
	// inferred one. Both may be set on an available=false response.
	TargetBranch string           `json:"targetBranch,omitempty"`
	TargetSource string           `json:"targetSource,omitempty"`
	MergeBase    string           `json:"mergeBase,omitempty"`
	Files        []ChangedFileDTO `json:"files"`
	Truncated    bool             `json:"truncated"`
}

// ChangedFileDTO is one changed file in the Changes list.
type ChangedFileDTO struct {
	// Path is repo-relative and slash-separated; for a rename it is the NEW path
	// and OldPath carries the previous one.
	Path    string `json:"path"`
	OldPath string `json:"oldPath,omitempty"`
	// Status is "added", "modified", "deleted", or "renamed".
	Status    string `json:"status"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	// Binary reports that git emitted "-" counts; additions/deletions are then
	// meaningless and must not be rendered arithmetically.
	Binary bool `json:"binary"`
	// Committed is false when the file also carries working-tree changes that
	// are not yet committed.
	Committed bool `json:"committed"`
}

// workspaceChangesResponse maps the service result to the wire DTO.
func workspaceChangesResponse(res sessionsvc.WorkspaceChangesResult) WorkspaceChangesResponse {
	files := make([]ChangedFileDTO, 0, len(res.Files))
	for _, f := range res.Files {
		files = append(files, ChangedFileDTO{
			Path:      f.Path,
			OldPath:   f.OldPath,
			Status:    f.Status,
			Additions: f.Additions,
			Deletions: f.Deletions,
			Binary:    f.Binary,
			Committed: f.Committed,
		})
	}
	return WorkspaceChangesResponse{
		Available:    res.Available,
		Reason:       res.Reason,
		TargetBranch: res.TargetBranch,
		TargetSource: res.TargetSource,
		MergeBase:    res.MergeBase,
		Files:        files,
		Truncated:    res.Truncated,
	}
}

// WorkspaceFileDiffParams is the query string accepted by
// GET /api/v1/sessions/{sessionId}/workspace/file-diff.
type WorkspaceFileDiffParams struct {
	Path string `query:"path" description:"Repo-relative path of the file to diff against the session's target branch. Absolute and ~/ paths are rejected."`
}

// workspaceFileResponse maps the service result to the wire DTO.
func workspaceFileResponse(res sessionsvc.WorkspaceFileResult) WorkspaceFileResponse {
	lines := make([]DiffContextLineDTO, 0, len(res.Lines))
	for _, l := range res.Lines {
		lines = append(lines, DiffContextLineDTO{Kind: l.Kind, OldLine: l.OldLine, NewLine: l.NewLine, Text: l.Text})
	}
	changed := make([]LineChangeDTO, 0, len(res.ChangedLines))
	for _, c := range res.ChangedLines {
		changed = append(changed, LineChangeDTO{Start: c.Start, End: c.End, Kind: string(c.Kind)})
	}
	return WorkspaceFileResponse{
		Available:    res.Available,
		Path:         res.Path,
		Lines:        lines,
		Reason:       res.Reason,
		ChangedLines: changed,
		Truncated:    res.Truncated,
	}
}

// ClaimPRRequest is the body of POST /sessions/{sessionId}/pr/claim.
type ClaimPRRequest struct {
	PR            string `json:"pr" minLength:"1"`
	AllowTakeover *bool  `json:"allowTakeover,omitempty"`
}

// ClaimPRResponse is the body of POST /sessions/{sessionId}/pr/claim.
type ClaimPRResponse struct {
	OK            bool               `json:"ok"`
	SessionID     domain.SessionID   `json:"sessionId"`
	PRs           []SessionPRFacts   `json:"prs"`
	BranchChanged bool               `json:"branchChanged"`
	TakenOverFrom []domain.SessionID `json:"takenOverFrom"`
}

// SetActivityRequest is the body of POST /api/v1/sessions/{sessionId}/activity.
//
// Detail is optional and additive: it carries the CURATED description of the
// action behind the signal, for the ephemeral activity feed
// (GET /api/v1/activity/stream). It is produced by a per-tool whitelist inside
// `ao hooks` before this request is sent, so a raw agent payload — a file body,
// a command with an inline token, a tool response — never reaches the daemon.
// Harnesses with no per-tool hook simply omit it.
type SetActivityRequest struct {
	State  string                 `json:"state" enum:"active,idle,waiting_input,exited" description:"Agent activity state reported by an agent hook."`
	Detail *domain.ActivityDetail `json:"detail,omitempty" description:"Optional curated detail of the action behind this signal."`
}

// SetActivityResponse is the body of POST /api/v1/sessions/{sessionId}/activity.
type SetActivityResponse struct {
	OK        bool             `json:"ok"`
	SessionID domain.SessionID `json:"sessionId"`
	State     string           `json:"state"`
}

// OrchestratorIDParam is the {id} path parameter for orchestrator routes.
type OrchestratorIDParam struct {
	ID string `path:"id" description:"Orchestrator session identifier, e.g. project-orchestrator."`
}

// SpawnOrchestratorRequest is the body of POST /api/v1/orchestrators.
type SpawnOrchestratorRequest struct {
	ProjectID domain.ProjectID `json:"projectId"`
	Clean     bool             `json:"clean,omitempty"`
}

// SpawnOrchestratorResponse is the body of POST /api/v1/orchestrators.
type SpawnOrchestratorResponse struct {
	Orchestrator OrchestratorResponse `json:"orchestrator"`
}

// OrchestratorResponse is the minimal orchestrator read model returned after spawn.
type OrchestratorResponse struct {
	ID          domain.SessionID `json:"id"`
	ProjectID   domain.ProjectID `json:"projectId"`
	ProjectName string           `json:"projectName,omitempty"`
}

// ListAgentsResponse is the body of GET /api/v1/agents.
type ListAgentsResponse = agentsvc.Inventory

// RefreshAgentsResponse is the body of POST /api/v1/agents/refresh.
type RefreshAgentsResponse = agentsvc.Inventory

// ProbeAgentResponse is the body of POST /api/v1/agents/{agent}/probe.
type ProbeAgentResponse = agentsvc.ProbeResult

// AgentInfo is one supported or installed agent entry.
type AgentInfo = agentsvc.Info

// ListNotificationsQuery is the query string accepted by GET /api/v1/notifications.
type ListNotificationsQuery struct {
	Status string `query:"status,omitempty" enum:"unread" description:"Notification status filter. V1 supports only unread."`
	Limit  int    `query:"limit,omitempty" minimum:"1" maximum:"100" description:"Maximum notifications to return. Defaults to 50; capped at 100."`
}

// ActivityStreamQuery is the query string accepted by GET /api/v1/activity/stream.
type ActivityStreamQuery struct {
	SessionID string `query:"sessionId,omitempty" description:"Optional session id filter. Omit to receive every session, which is what a desktop overlay wants."`
}

// NotificationStreamQuery is the query string accepted by GET /api/v1/notifications/stream.
type NotificationStreamQuery struct {
	ProjectID string `query:"projectId,omitempty" description:"Optional project id filter for live notifications."`
}

// NotificationIDParam is the {id} path parameter shared by notification routes.
type NotificationIDParam struct {
	ID string `path:"id" description:"Notification identifier."`
}

// NotificationTarget is the dashboard navigation target for a notification.
type NotificationTarget struct {
	Kind      string `json:"kind" enum:"session,pr"`
	SessionID string `json:"sessionId"`
	PRURL     string `json:"prUrl,omitempty"`
}

// NotificationResponse is one stored notification returned by the API.
type NotificationResponse struct {
	ID        string             `json:"id"`
	SessionID string             `json:"sessionId"`
	ProjectID string             `json:"projectId"`
	PRURL     string             `json:"prUrl"`
	Type      string             `json:"type" enum:"needs_input,ready_to_merge,pr_merged,pr_closed_unmerged"`
	Title     string             `json:"title"`
	Body      string             `json:"body"`
	Status    string             `json:"status" enum:"unread,read"`
	CreatedAt time.Time          `json:"createdAt"`
	Target    NotificationTarget `json:"target"`
}

// ListNotificationsResponse is the body of GET /api/v1/notifications.
type ListNotificationsResponse struct {
	Notifications []NotificationResponse `json:"notifications"`
}

// MarkNotificationReadRequest is the body of PATCH /api/v1/notifications/{id}.
type MarkNotificationReadRequest struct {
	Status string `json:"status" enum:"read" description:"V1 supports only marking an unread notification read."`
}

// NotificationEnvelope is the { notification } response body for notification mutations.
type NotificationEnvelope struct {
	Notification NotificationResponse `json:"notification"`
}

// MarkAllNotificationsReadResponse is the body of POST /api/v1/notifications/read-all.
type MarkAllNotificationsReadResponse struct {
	Notifications []NotificationResponse `json:"notifications"`
}

// ImportStatusResponse is the body of GET /api/v1/import: whether a legacy AO
// install is available to import, and the root the daemon would read from.
type ImportStatusResponse struct {
	Available  bool   `json:"available"`
	LegacyRoot string `json:"legacyRoot"`
}

// ImportRunResponse is the body of POST /api/v1/import: the structured outcome
// of the import run (counts + notes), reused verbatim from the import engine.
type ImportRunResponse struct {
	Report legacyimport.Report `json:"report"`
}

// PRIDParam is the {id} path parameter shared by the /prs/{id} routes.
type PRIDParam struct {
	ID string `path:"id" description:"PR number."`
}

// MergePRResponse is the body of POST /api/v1/prs/{id}/merge (200).
type MergePRResponse struct {
	OK       bool   `json:"ok"`
	PRNumber int    `json:"prNumber"`
	Method   string `json:"method"`
}

// ResolveCommentsRequest is the optional body of POST /api/v1/prs/{id}/resolve-comments.
type ResolveCommentsRequest struct {
	CommentIDs []string `json:"commentIds,omitempty"`
}

// ResolveCommentsResponse is the body of POST /api/v1/prs/{id}/resolve-comments (200).
type ResolveCommentsResponse struct {
	OK       bool `json:"ok"`
	Resolved int  `json:"resolved"`
}

// ReclaimSettingsResponse mirrors reclaimsettings.Settings on the wire. It is
// the body of GET/PUT /api/v1/settings/reclaim.
type ReclaimSettingsResponse struct {
	Enabled      bool `json:"enabled"`
	GraceMinutes int  `json:"graceMinutes"`
}

// SetReclaimSettingsRequest is the body of PUT /api/v1/settings/reclaim.
type SetReclaimSettingsRequest struct {
	Enabled      bool `json:"enabled"`
	GraceMinutes int  `json:"graceMinutes"`
}

// EvidenceRetentionSettingsResponse mirrors evidenceretention.Settings on the
// wire. It is the body of GET/PUT /api/v1/settings/evidence-retention.
type EvidenceRetentionSettingsResponse struct {
	Enabled    bool `json:"enabled" description:"Whether the age-based evidence retention sweep runs at all."`
	MaxAgeDays int  `json:"maxAgeDays" description:"Purge evidence older than this many days (from its created_at). 0/disabled = keep forever."`
}

// SetEvidenceRetentionSettingsRequest is the body of PUT
// /api/v1/settings/evidence-retention.
type SetEvidenceRetentionSettingsRequest struct {
	Enabled    bool `json:"enabled"`
	MaxAgeDays int  `json:"maxAgeDays"`
}

// EvidenceRetentionSweepResponse is the body of POST
// /api/v1/settings/evidence-retention/sweep (the manual trigger).
type EvidenceRetentionSweepResponse struct {
	Purged     int   `json:"purged" description:"Number of evidence items removed."`
	FreedBytes int64 `json:"freedBytes" description:"On-disk bytes freed by the sweep."`
}

// SpawnConfirmSettingsResponse mirrors spawnconfirm.Settings on the wire. It is
// the body of GET/PUT /api/v1/settings/spawn-confirm.
type SpawnConfirmSettingsResponse struct {
	Enabled bool `json:"enabled"`
}

// SetSpawnConfirmSettingsRequest is the body of PUT /api/v1/settings/spawn-confirm.
type SetSpawnConfirmSettingsRequest struct {
	Enabled bool `json:"enabled"`
}

// AutoNudgeSettingsResponse mirrors autonudge.Settings on the wire. It is
// the body of GET/PUT /api/v1/settings/auto-nudge.
type AutoNudgeSettingsResponse struct {
	Enabled bool `json:"enabled"`
}

// SetAutoNudgeSettingsRequest is the body of PUT /api/v1/settings/auto-nudge.
type SetAutoNudgeSettingsRequest struct {
	Enabled bool `json:"enabled"`
}

// ResponseLanguageSettingsResponse mirrors responselang.Settings on the wire. It
// is the body of GET/PUT /api/v1/settings/response-language. Language is the
// global default human-facing response language (e.g. "English", "Thai").
type ResponseLanguageSettingsResponse struct {
	Language string `json:"language"`
}

// SetResponseLanguageSettingsRequest is the body of PUT
// /api/v1/settings/response-language.
type SetResponseLanguageSettingsRequest struct {
	Language string `json:"language"`
}

// SystemPromptItem is one editable prompt kind on the wire: its built-in default
// (for the editor + Reset) and the current override (null when using the default).
type SystemPromptItem struct {
	Kind     string  `json:"kind"`
	Default  string  `json:"default"`
	Override *string `json:"override"`
}

// SystemPromptsResponse is the body of GET /api/v1/settings/prompts.
type SystemPromptsResponse struct {
	Prompts []SystemPromptItem `json:"prompts"`
}

// SetSystemPromptRequest is the body of PUT /api/v1/settings/prompts/{kind}.
type SetSystemPromptRequest struct {
	Base string `json:"base"`
}

// PromptKindParam is the {kind} path parameter shared by the
// /settings/prompts/{kind} routes. Handlers read it via chi.URLParam; it is
// declared here so apispec.Build reflects it as the path parameter.
type PromptKindParam struct {
	Kind string `path:"kind" description:"Editable prompt kind: orchestrator, worker, or reviewer." enum:"orchestrator,worker,reviewer"`
}

// MessageTemplateItem is one editable nudge template on the wire: its built-in
// default, documented placeholders, and current override (null ⇒ default).
type MessageTemplateItem struct {
	Name         string   `json:"name"`
	Default      string   `json:"default"`
	Placeholders []string `json:"placeholders"`
	Override     *string  `json:"override"`
}

// MessageTemplatesResponse is the body of GET /api/v1/settings/message-templates.
type MessageTemplatesResponse struct {
	Templates []MessageTemplateItem `json:"templates"`
}

// SetMessageTemplateRequest is the body of PUT /api/v1/settings/message-templates/{name}.
type SetMessageTemplateRequest struct {
	Template string `json:"template"`
}

// MessageTemplateNameParam is the {name} path parameter for the
// /settings/message-templates/{name} routes.
type MessageTemplateNameParam struct {
	Name string `path:"name" description:"Editable nudge template name." enum:"review-comment-dispatch,ci-failing,merge-conflict,tracker-bot-comment,ao-reviewer-batch,ao-reviewer-single"`
}
