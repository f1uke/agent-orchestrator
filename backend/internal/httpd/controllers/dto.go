package controllers

import (
	"encoding/json"
	"errors"
	"time"

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
	AutoNameBranch bool   `json:"autoNameBranch,omitempty"`
	Prompt         string `json:"prompt,omitempty" maxLength:"4096"`
	// DisplayName is the sidebar label for the session, capped at 20 characters.
	// `ao spawn --name` always sets it; other clients (e.g. the desktop new-task
	// dialog) may omit it and fall back to the session id in the read model.
	DisplayName string `json:"displayName,omitempty" maxLength:"20"`
	// StartImmediately controls deferral. Absent/null (the default) or true
	// spawns the session now — the unchanged behavior. false stages it as a
	// prepared TODO on the board (no branch/worktree/tmux until Start).
	StartImmediately *bool `json:"startImmediately,omitempty"`
	// PRTarget is the intended PR merge target, stored on a deferred TODO so the
	// board detail modal can show/edit it. Ignored for an immediate spawn.
	PRTarget string `json:"prTarget,omitempty"`
	// CreatedBy is the orchestrator session id queuing a deferred TODO, kept for
	// the report-back. `ao spawn --todo` sets it from AO_SESSION_ID.
	CreatedBy domain.SessionID `json:"createdBy,omitempty"`
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
	return SessionPRReviewSummary{Decision: in.Decision, HasUnresolvedHumanComments: in.HasUnresolvedHumanComments, UnresolvedBy: reviewers}
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
type SetActivityRequest struct {
	State string `json:"state" enum:"active,idle,waiting_input,exited" description:"Agent activity state reported by an agent hook."`
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
