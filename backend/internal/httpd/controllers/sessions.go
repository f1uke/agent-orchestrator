package controllers

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	previewutil "github.com/aoagents/agent-orchestrator/backend/internal/preview"
	sessionsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/session"
)

const (
	// maxPromptLen bounds the spawn prompt. It is a defensive guard against a
	// runaway body, not an agent/model context limit: the prompt is delivered to
	// the agent as a positional CLI argument (e.g. `claude … -- <prompt>`), so
	// the real ceiling is the OS ARG_MAX (~1 MiB on macOS/Linux for the whole
	// argv+env). 128 KiB leaves comfortable headroom for env and shell-quote
	// expansion while still rejecting pathological multi-megabyte bodies. Keep in
	// sync with the `maxLength` tag on SpawnSessionRequest.Prompt in dto.go.
	maxPromptLen      = 128 * 1024
	maxMessageLen     = 4096
	maxDisplayNameLen = 20
)

var errPreviewFileNotFound = errors.New("preview file not found")

// SessionService is the controller-facing session service contract.
type SessionService interface {
	List(ctx context.Context, filter sessionsvc.ListFilter) ([]domain.Session, error)
	Spawn(ctx context.Context, cfg ports.SpawnConfig) (domain.Session, error)
	// PrepareTodo stages a deferred/TODO session; StartTodo materializes it;
	// UpdateTodoSpec edits its spec while still queued.
	PrepareTodo(ctx context.Context, cfg ports.SpawnConfig) (domain.Session, error)
	StartTodo(ctx context.Context, id domain.SessionID) (domain.Session, error)
	UpdateTodoSpec(ctx context.Context, id domain.SessionID, patch ports.TodoSpecPatch) (domain.Session, error)
	SpawnOrchestrator(ctx context.Context, projectID domain.ProjectID, clean bool) (domain.Session, error)
	Get(ctx context.Context, id domain.SessionID) (domain.Session, error)
	Restore(ctx context.Context, id domain.SessionID) (domain.Session, error)
	Restart(ctx context.Context, id domain.SessionID) (domain.Session, error)
	// Wake resumes a suspended session in place or resets a live session's
	// idle-close countdown; the UI calls it when the user opens/selects a session.
	Wake(ctx context.Context, id domain.SessionID) (domain.Session, error)
	Kill(ctx context.Context, id domain.SessionID) (bool, error)
	Delete(ctx context.Context, id domain.SessionID, force bool) error
	RollbackSpawn(ctx context.Context, id domain.SessionID) (sessionsvc.RollbackOutcome, error)
	Cleanup(ctx context.Context, project domain.ProjectID) (sessionsvc.CleanupOutcome, error)
	Rename(ctx context.Context, id domain.SessionID, displayName string) error
	SetPreview(ctx context.Context, id domain.SessionID, previewURL string) (domain.Session, error)
	SetAutoNudge(ctx context.Context, id domain.SessionID, override *bool) (domain.Session, error)
	SetAutoResolve(ctx context.Context, id domain.SessionID, override *bool) (domain.Session, error)
	SetKeepWarmOnMerge(ctx context.Context, id domain.SessionID, enabled bool) (domain.Session, error)
	Send(ctx context.Context, id domain.SessionID, message string) error
	DispatchCommentToWorker(ctx context.Context, id domain.SessionID, prURL, threadID, extraPrompt string) error
	ReplyToThread(ctx context.Context, id domain.SessionID, prURL, threadID, body string) (sessionsvc.PRThreadComment, error)
	ResolveThread(ctx context.Context, id domain.SessionID, prURL, threadID string) error
	ListPRSummaries(ctx context.Context, id domain.SessionID) ([]sessionsvc.PRSummary, error)
	ListPRCommentThreads(ctx context.Context, id domain.SessionID) ([]sessionsvc.PRCommentGroup, error)
	ClaimPR(ctx context.Context, id domain.SessionID, ref string, opts sessionsvc.ClaimPROptions) (sessionsvc.ClaimPRResult, error)
	DiffContext(ctx context.Context, id domain.SessionID, q sessionsvc.DiffContextQuery) (sessionsvc.DiffContextResult, error)
	// ResolveWorkspaceRef maps a terminal file reference (absolute path,
	// workspace-relative path, or bare filename) to candidate workspace-relative
	// paths, confined to the session's workspace.
	ResolveWorkspaceRef(ctx context.Context, id domain.SessionID, ref string) ([]string, error)
	// ReadWorkspaceFile returns a workspace file's content plus its per-line
	// uncommitted-change map (working tree vs HEAD), confined to the workspace.
	ReadWorkspaceFile(ctx context.Context, id domain.SessionID, path string) (sessionsvc.WorkspaceFileResult, error)
}

// ActivityRecorder applies an agent activity-state signal to a session. It is
// satisfied directly by *lifecycle.Manager: an activity signal is a pure
// lifecycle reduction (no runtime/workspace teardown), so it bypasses
// SessionService rather than threading a no-op passthrough through the session
// manager.
type ActivityRecorder interface {
	ApplyActivitySignal(ctx context.Context, id domain.SessionID, s ports.ActivitySignal) error
}

// SessionsController owns the session routes. Nil keeps routes registered but
// returns OpenAPI-backed 501s.
type SessionsController struct {
	Svc      SessionService
	Activity ActivityRecorder
}

// Register mounts the session routes on the supplied router.
func (c *SessionsController) Register(r chi.Router) {
	r.Get("/sessions", c.list)
	r.Post("/sessions", c.spawn)
	r.Post("/sessions/cleanup", c.cleanup)
	r.Get("/sessions/{sessionId}", c.get)
	r.Delete("/sessions/{sessionId}", c.delete)
	r.Get("/sessions/{sessionId}/preview", c.preview)
	r.Post("/sessions/{sessionId}/preview", c.setPreview)
	r.Delete("/sessions/{sessionId}/preview", c.clearPreview)
	r.Put("/sessions/{sessionId}/auto-nudge", c.setAutoNudge)
	r.Put("/sessions/{sessionId}/auto-resolve", c.setAutoResolve)
	r.Put("/sessions/{sessionId}/keep-warm", c.setKeepWarm)
	r.Get("/sessions/{sessionId}/preview/files/*", c.previewFile)
	r.Get("/sessions/{sessionId}/pr", c.listPRs)
	r.Get("/sessions/{sessionId}/pr-comments", c.listPRComments)
	r.Get("/sessions/{sessionId}/diff-context", c.diffContext)
	r.Get("/sessions/{sessionId}/workspace/resolve", c.resolveWorkspaceRef)
	r.Get("/sessions/{sessionId}/workspace/file", c.readWorkspaceFile)
	r.Post("/sessions/{sessionId}/pr/claim", c.claimPR)
	r.Patch("/sessions/{sessionId}", c.rename)
	r.Patch("/sessions/{sessionId}/spec", c.updateSpec)
	r.Post("/sessions/{sessionId}/start", c.start)
	r.Post("/sessions/{sessionId}/restore", c.restore)
	r.Post("/sessions/{sessionId}/restart", c.restart)
	r.Post("/sessions/{sessionId}/wake", c.wake)
	r.Post("/sessions/{sessionId}/kill", c.kill)
	r.Post("/sessions/{sessionId}/rollback", c.rollback)
	r.Post("/sessions/{sessionId}/send", c.send)
	r.Post("/sessions/{sessionId}/comment-dispatch", c.commentDispatch)
	r.Post("/sessions/{sessionId}/comment-reply", c.commentReply)
	r.Post("/sessions/{sessionId}/comment-resolve", c.commentResolve)
	r.Post("/sessions/{sessionId}/activity", c.activity)
	r.Get("/orchestrators", c.listOrchestrators)
	r.Post("/orchestrators", c.spawnOrchestrator)
	r.Get("/orchestrators/{id}", c.getOrchestrator)
}

func (c *SessionsController) list(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/sessions")
		return
	}
	filter, err := parseSessionListFilter(r)
	if err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_QUERY", err.Error(), nil)
		return
	}
	sessions, err := c.Svc.List(r.Context(), filter)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ListSessionsResponse{Sessions: sessionViews(sessions)})
}

func (c *SessionsController) spawn(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions")
		return
	}
	var in SpawnSessionRequest
	if err := decodeJSON(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	if in.ProjectID == "" {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "PROJECT_ID_REQUIRED", "projectId is required", nil)
		return
	}
	if len(in.Prompt) > maxPromptLen {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "PROMPT_TOO_LONG", "prompt is too long", nil)
		return
	}
	// displayName is optional at the API (the desktop new-task dialog omits it
	// and the read model falls back to the session id). `ao spawn` makes it
	// required CLI-side. When present, it is held to the same length cap here so
	// a direct API call cannot exceed it.
	displayName := strings.TrimSpace(in.DisplayName)
	if utf8.RuneCountInString(displayName) > maxDisplayNameLen {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "DISPLAY_NAME_TOO_LONG", "displayName must be 20 characters or fewer", nil)
		return
	}
	if in.Kind == "" {
		in.Kind = domain.KindWorker
	}
	// task_size is optional; empty resolves to standard. Reject a present-but-
	// unknown value so a typo (`--task-size mechnical`) fails loudly here rather
	// than silently falling back to full ceremony.
	if in.TaskSize != "" && !in.TaskSize.Valid() {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "TASK_SIZE_INVALID", "taskSize must be one of mechanical, standard, deep", nil)
		return
	}
	cfg := ports.SpawnConfig{ProjectID: in.ProjectID, IssueID: in.IssueID, Kind: in.Kind, Harness: in.Harness, Branch: in.Branch, BaseBranch: in.BaseBranch, AutoNameBranch: in.AutoNameBranch, Prompt: in.Prompt, DisplayName: displayName, PRTarget: in.PRTarget, CreatedBy: in.CreatedBy, KeepWarmOnMerge: in.KeepWarmOnMerge, TaskSize: in.TaskSize.WithDefault()}
	// startImmediately absent/null/true keeps the current spawn-now behavior;
	// false stages the worker as a prepared TODO on the board.
	deferred := in.StartImmediately != nil && !*in.StartImmediately
	spawn := c.Svc.Spawn
	if deferred {
		spawn = c.Svc.PrepareTodo
	}
	sess, err := spawn(r.Context(), cfg)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusCreated, SessionResponse{Session: sessionView(sess)})
}

// start materializes a prepared TODO in place and hands it to the normal
// session lifecycle, returning the now-live session.
func (c *SessionsController) start(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/start")
		return
	}
	sess, err := c.Svc.StartTodo(r.Context(), sessionID(r))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, SessionResponse{Session: sessionView(sess)})
}

// updateSpec persists edits to a prepared TODO's spec before it is started.
func (c *SessionsController) updateSpec(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "PATCH", "/api/v1/sessions/{sessionId}/spec")
		return
	}
	var in UpdateTodoSpecRequest
	if err := decodeJSON(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	if in.DisplayName != nil && utf8.RuneCountInString(strings.TrimSpace(*in.DisplayName)) > maxDisplayNameLen {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "DISPLAY_NAME_TOO_LONG", "displayName must be 20 characters or fewer", nil)
		return
	}
	if in.Prompt != nil && len(*in.Prompt) > maxPromptLen {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "PROMPT_TOO_LONG", "prompt is too long", nil)
		return
	}
	patch := ports.TodoSpecPatch{DisplayName: in.DisplayName, Harness: in.Harness, Branch: in.Branch, BaseBranch: in.BaseBranch, PRTarget: in.PRTarget, Prompt: in.Prompt, AutoNameBranch: in.AutoNameBranch}
	sess, err := c.Svc.UpdateTodoSpec(r.Context(), sessionID(r), patch)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, SessionResponse{Session: sessionView(sess)})
}

func (c *SessionsController) get(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/sessions/{sessionId}")
		return
	}
	sess, err := c.Svc.Get(r.Context(), sessionID(r))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, SessionResponse{Session: sessionView(sess)})
}

// delete permanently removes a finished (merged or terminated) session,
// keeping its git branch. force=true discards an uncommitted worktree instead
// of refusing (SessionService.Delete maps that refusal to a 409).
func (c *SessionsController) delete(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "DELETE", "/api/v1/sessions/{sessionId}")
		return
	}
	force := r.URL.Query().Get("force") == "true"
	if err := c.Svc.Delete(r.Context(), sessionID(r), force); err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, DeleteSessionResponse{OK: true, SessionID: sessionID(r)})
}

func (c *SessionsController) preview(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/sessions/{sessionId}/preview")
		return
	}
	sess, err := c.Svc.Get(r.Context(), sessionID(r))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	entry, ok := discoverPreviewEntry(sess.Metadata.WorkspacePath)
	res := SessionPreviewResponse{SessionID: sessionID(r)}
	if ok {
		res.Entry = entry
		res.PreviewURL = previewFileURL(r, sessionID(r), entry)
	}
	envelope.WriteJSON(w, http.StatusOK, res)
}

func (c *SessionsController) previewFile(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/sessions/{sessionId}/preview/files/*")
		return
	}
	sess, err := c.Svc.Get(r.Context(), sessionID(r))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	file, ok := confinedPreviewPath(sess.Metadata.WorkspacePath, chi.URLParam(r, "*"))
	if !ok {
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "PREVIEW_FILE_NOT_FOUND", "Preview file not found", nil)
		return
	}
	if previewutil.IsMarkdownPath(file) {
		c.servePreviewMarkdown(w, r, file)
		return
	}
	http.ServeFile(w, r, file)
}

// servePreviewMarkdown renders a workspace Markdown file to a self-contained
// HTML document so the browser panel displays formatted content instead of raw
// source.
func (c *SessionsController) servePreviewMarkdown(w http.ResponseWriter, r *http.Request, file string) {
	source, err := os.ReadFile(file)
	if err != nil {
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "PREVIEW_FILE_NOT_FOUND", "Preview file not found", nil)
		return
	}
	rendered, err := previewutil.RenderMarkdown(source, filepath.Base(file))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	_, _ = w.Write(rendered) //nolint:gosec // G705: preview content is workspace-local and agent-trusted
}

// setPreview persists the browser preview URL the desktop app opens for a
// session and fans out a session_updated CDC event so the dashboard's browser
// panel reacts live. The target is resolved as follows:
//
//   - An empty url opens the workspace's static entry point (index.html and
//     friends), falling back to the session's existing preview target only
//     when no entry point exists.
//   - An explicit workspace-local path (e.g. `index.html`, `./dist/index.html`)
//     is served through the preview/files route so local files load.
//   - Anything else (http(s)/file URLs, host:port dev servers) is kept verbatim.
//
// Every call bumps the session's preview revision, so re-running `ao preview`
// with the same target still refreshes the panel.
func (c *SessionsController) setPreview(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/preview")
		return
	}
	var in SetSessionPreviewRequest
	if err := decodeJSON(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	// Get first so a missing session is rejected with the normal 404 before any
	// write, and so autodetect/local resolution has the workspace path to probe.
	sess, err := c.Svc.Get(r.Context(), sessionID(r))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	// ponytail: no URL sanitization on preview target; agent-trusted for now
	previewURL := strings.TrimSpace(in.URL)
	if previewURL == "" {
		if entry, ok := discoverPreviewEntry(sess.Metadata.WorkspacePath); ok {
			previewURL = previewFileURL(r, sessionID(r), entry)
		} else if existing := strings.TrimSpace(sess.Metadata.PreviewURL); existing != "" {
			var resolveErr error
			previewURL, resolveErr = resolvePreviewTarget(r, sessionID(r), sess.Metadata.WorkspacePath, existing)
			if resolveErr != nil {
				writePreviewResolveError(w, r, resolveErr)
				return
			}
		} else {
			envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "NO_PREVIEW_ENTRY", "No preview entry point found in session workspace", nil)
			return
		}
	} else {
		var resolveErr error
		previewURL, resolveErr = resolvePreviewTarget(r, sessionID(r), sess.Metadata.WorkspacePath, previewURL)
		if resolveErr != nil {
			writePreviewResolveError(w, r, resolveErr)
			return
		}
	}
	updated, err := c.Svc.SetPreview(r.Context(), sessionID(r), previewURL)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, SessionResponse{Session: sessionView(updated)})
}

// setAutoNudge sets (or clears) the per-session override for auto-nudging the
// worker on unresolved PR review comments. A null `override` clears it so the
// session inherits the global default.
func (c *SessionsController) setAutoNudge(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "PUT", "/api/v1/sessions/{sessionId}/auto-nudge")
		return
	}
	var in SetAutoNudgeRequest
	if err := decodeJSON(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	updated, err := c.Svc.SetAutoNudge(r.Context(), sessionID(r), in.Override)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, SessionResponse{Session: sessionView(updated)})
}

// setAutoResolve sets (or clears) the per-session gate for auto-resolving a review
// thread once our side replies on it. A null `override` clears it (OFF).
func (c *SessionsController) setAutoResolve(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "PUT", "/api/v1/sessions/{sessionId}/auto-resolve")
		return
	}
	var in SetAutoResolveRequest
	if err := decodeJSON(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	updated, err := c.Svc.SetAutoResolve(r.Context(), sessionID(r), in.Override)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, SessionResponse{Session: sessionView(updated)})
}

// setKeepWarm toggles whether a worker suspends-in-place on merge (card stays on
// the board, resumable) rather than terminating to Done
// (feature/merge-suspend-in-place).
func (c *SessionsController) setKeepWarm(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "PUT", "/api/v1/sessions/{sessionId}/keep-warm")
		return
	}
	var in SetSessionKeepWarmRequest
	if err := decodeJSON(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	updated, err := c.Svc.SetKeepWarmOnMerge(r.Context(), sessionID(r), in.Enabled)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, SessionResponse{Session: sessionView(updated)})
}

// clearPreview resets a session's browser preview to empty (`ao preview
// clear`). Unlike setPreview with an empty url it never autodetects: it persists
// an empty target so the desktop browser panel returns to its blank state. The
// write still bumps the preview revision, so the panel hears the change over
// CDC even though the url field is now empty.
func (c *SessionsController) clearPreview(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "DELETE", "/api/v1/sessions/{sessionId}/preview")
		return
	}
	updated, err := c.Svc.SetPreview(r.Context(), sessionID(r), "")
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, SessionResponse{Session: sessionView(updated)})
}

func (c *SessionsController) listPRs(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/sessions/{sessionId}/pr")
		return
	}
	prs, err := c.Svc.ListPRSummaries(r.Context(), sessionID(r))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ListSessionPRsResponse{SessionID: sessionID(r), PRs: sessionPRSummaries(prs)})
}

func (c *SessionsController) listPRComments(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/sessions/{sessionId}/pr-comments")
		return
	}
	groups, err := c.Svc.ListPRCommentThreads(r.Context(), sessionID(r))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ListSessionPRCommentsResponse{SessionID: sessionID(r), PRs: sessionPRCommentGroups(groups)})
}

func (c *SessionsController) diffContext(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/sessions/{sessionId}/diff-context")
		return
	}
	mode := r.URL.Query().Get("mode")
	if mode == "" {
		mode = "hunk"
	}
	line, _ := strconv.Atoi(r.URL.Query().Get("line"))
	q := sessionsvc.DiffContextQuery{
		PRURL: r.URL.Query().Get("prUrl"),
		Path:  r.URL.Query().Get("path"),
		Line:  line,
		Mode:  mode,
	}
	res, err := c.Svc.DiffContext(r.Context(), sessionID(r), q)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, diffContextResponse(res))
}

// resolveWorkspaceRef maps a file reference printed in the terminal to candidate
// workspace-relative paths (0 = not found, 1 = open directly, many = the UI
// shows a disambiguation picker). Resolution is confined to the session's
// workspace.
func (c *SessionsController) resolveWorkspaceRef(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/sessions/{sessionId}/workspace/resolve")
		return
	}
	ref := r.URL.Query().Get("ref")
	candidates, err := c.Svc.ResolveWorkspaceRef(r.Context(), sessionID(r), ref)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	if candidates == nil {
		candidates = []string{}
	}
	envelope.WriteJSON(w, http.StatusOK, WorkspaceResolveResponse{Ref: ref, Candidates: candidates})
}

// readWorkspaceFile returns a workspace file's content plus its per-line
// uncommitted-change map, confined to the session's workspace.
func (c *SessionsController) readWorkspaceFile(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/sessions/{sessionId}/workspace/file")
		return
	}
	res, err := c.Svc.ReadWorkspaceFile(r.Context(), sessionID(r), r.URL.Query().Get("path"))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, workspaceFileResponse(res))
}

func (c *SessionsController) claimPR(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/pr/claim")
		return
	}
	var in ClaimPRRequest
	if err := decodeJSON(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	if strings.TrimSpace(in.PR) == "" {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "PR_REQUIRED", "pr is required", nil)
		return
	}
	allowTakeover := true
	if in.AllowTakeover != nil {
		allowTakeover = *in.AllowTakeover
	}
	res, err := c.Svc.ClaimPR(r.Context(), sessionID(r), in.PR, sessionsvc.ClaimPROptions{AllowTakeover: allowTakeover})
	if err != nil {
		writeSessionPRError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ClaimPRResponse{OK: true, SessionID: sessionID(r), PRs: sessionPRFacts(res.PRs), BranchChanged: res.BranchChanged, TakenOverFrom: nonNilSessionIDs(res.TakenOverFrom)})
}

func (c *SessionsController) rename(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "PATCH", "/api/v1/sessions/{sessionId}")
		return
	}
	var in RenameSessionRequest
	if err := decodeJSON(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	displayName := strings.TrimSpace(in.DisplayName)
	if displayName == "" {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "DISPLAY_NAME_REQUIRED", "displayName is required", nil)
		return
	}
	if err := c.Svc.Rename(r.Context(), sessionID(r), displayName); err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, RenameSessionResponse{OK: true, SessionID: sessionID(r), DisplayName: displayName})
}

func (c *SessionsController) restore(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/restore")
		return
	}
	sess, err := c.Svc.Restore(r.Context(), sessionID(r))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, RestoreSessionResponse{OK: true, SessionID: sessionID(r), Session: sessionView(sess)})
}

func (c *SessionsController) kill(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/kill")
		return
	}
	freed, err := c.Svc.Kill(r.Context(), sessionID(r))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, KillSessionResponse{OK: true, SessionID: sessionID(r), Freed: freed})
}

// restart tears the session down and relaunches it in place (kill-then-restore),
// keeping the same session id and native transcript. Used by the terminal
// toolbar's "Restart session" control so a live agent picks up a freshly
// recomputed system prompt without losing its conversation.
func (c *SessionsController) restart(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/restart")
		return
	}
	sess, err := c.Svc.Restart(r.Context(), sessionID(r))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, RestartSessionResponse{OK: true, SessionID: sessionID(r), Session: sessionView(sess)})
}

// wake is the user-open hook. The desktop app POSTs here when the user
// opens/selects a session: a suspended session resumes in place, a live session
// has its idle-close countdown reset, and a terminated one is returned
// unchanged. The response carries the fresh read model (isSuspended cleared,
// idleCloseAt pushed forward) so the UI reflects the reset without waiting for a
// CDC round-trip.
func (c *SessionsController) wake(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/wake")
		return
	}
	sess, err := c.Svc.Wake(r.Context(), sessionID(r))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, WakeSessionResponse{OK: true, SessionID: sessionID(r), Session: sessionView(sess)})
}

// rollback undoes a partially-completed spawn: if the session row is still in
// seed state (no workspace, no runtime handle yet), the row is deleted
// outright. If anything observable has landed it falls back to Kill so the
// runtime/workspace are torn down. Used by `ao spawn --claim-pr` to undo a
// session whose claim step failed, avoiding the orphan terminated row a
// plain Kill would leave behind.
func (c *SessionsController) rollback(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/rollback")
		return
	}
	out, err := c.Svc.RollbackSpawn(r.Context(), sessionID(r))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, RollbackSessionResponse{OK: true, SessionID: sessionID(r), Deleted: out.Deleted, Killed: out.Killed})
}

func (c *SessionsController) cleanup(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/cleanup")
		return
	}
	out, err := c.Svc.Cleanup(r.Context(), domain.ProjectID(r.URL.Query().Get("project")))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	skipped := make([]CleanupSkippedSession, 0, len(out.Skipped))
	for _, skip := range out.Skipped {
		skipped = append(skipped, CleanupSkippedSession{SessionID: skip.SessionID, Reason: skip.Reason})
	}
	envelope.WriteJSON(w, http.StatusOK, CleanupSessionsResponse{OK: true, Cleaned: out.Cleaned, Skipped: skipped})
}

func (c *SessionsController) send(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/send")
		return
	}
	var in SendSessionMessageRequest
	if err := decodeJSON(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	if in.Message == "" {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "MESSAGE_REQUIRED", "Message is required", nil)
		return
	}
	if len(in.Message) > maxMessageLen {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "MESSAGE_TOO_LONG", "Message is too long", nil)
		return
	}
	message := domain.SanitizeControlChars(in.Message)
	if err := c.Svc.Send(r.Context(), sessionID(r), message); err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, SendSessionMessageResponse{OK: true, SessionID: sessionID(r), Message: message})
}

// commentDispatch forwards a review-thread comment (plus an optional extra
// prompt) to the session's worker, letting a human manually nudge an agent
// that isn't auto-polling the thread.
func (c *SessionsController) commentDispatch(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/comment-dispatch")
		return
	}
	var in DispatchCommentRequest
	if err := decodeJSON(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	if in.PrURL == "" || in.ThreadID == "" {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "THREAD_REQUIRED", "prUrl and threadId are required", nil)
		return
	}
	if len(in.ExtraPrompt) > maxMessageLen {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "MESSAGE_TOO_LONG", "Extra prompt is too long", nil)
		return
	}
	if err := c.Svc.DispatchCommentToWorker(r.Context(), sessionID(r), in.PrURL, in.ThreadID, in.ExtraPrompt); err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, DispatchCommentResponse{OK: true, SessionID: sessionID(r)})
}

// commentReply posts a reply comment on a PR review thread on behalf of the
// session's SCM identity.
func (c *SessionsController) commentReply(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/comment-reply")
		return
	}
	var in ReplyCommentRequest
	if err := decodeJSON(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	if in.PrURL == "" || in.ThreadID == "" {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "THREAD_REQUIRED", "prUrl and threadId are required", nil)
		return
	}
	body := domain.SanitizeControlChars(in.Body)
	if strings.TrimSpace(body) == "" {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "BODY_REQUIRED", "Reply body is required", nil)
		return
	}
	if len(body) > maxMessageLen {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "MESSAGE_TOO_LONG", "Reply body is too long", nil)
		return
	}
	comment, err := c.Svc.ReplyToThread(r.Context(), sessionID(r), in.PrURL, in.ThreadID, body)
	if err != nil {
		writeThreadWriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ReplyCommentResponse{OK: true, Comment: newSessionPRThreadComment(comment)})
}

// commentResolve marks a PR review thread resolved on behalf of the session's
// SCM identity.
func (c *SessionsController) commentResolve(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/comment-resolve")
		return
	}
	var in ResolveThreadRequest
	if err := decodeJSON(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	if in.PrURL == "" || in.ThreadID == "" {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "THREAD_REQUIRED", "prUrl and threadId are required", nil)
		return
	}
	if err := c.Svc.ResolveThread(r.Context(), sessionID(r), in.PrURL, in.ThreadID); err != nil {
		writeThreadWriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ResolveThreadResponse{OK: true, SessionID: sessionID(r), Resolved: true})
}

// writeThreadWriteError maps thread-write sentinels to explicit statuses,
// delegating apierr-coded errors (SESSION_NOT_FOUND / PR_NOT_FOUND /
// THREAD_NOT_FOUND) to envelope.WriteError.
func writeThreadWriteError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, sessionsvc.ErrSCMWriteForbidden):
		envelope.WriteAPIError(w, r, http.StatusForbidden, "forbidden", "SCM_WRITE_FORBIDDEN", "The configured token cannot write to this thread (missing write scope)", nil)
	case errors.Is(err, sessionsvc.ErrSCMUnavailable):
		envelope.WriteAPIError(w, r, http.StatusServiceUnavailable, "unavailable", "SCM_UNAVAILABLE", "SCM unavailable", nil)
	default:
		envelope.WriteError(w, r, err)
	}
}

// activity records an agent activity-state signal reported by an agent hook
// (via `ao hooks <agent> <event>`). It funnels through the single
// lifecycle.Manager so the reaper and hooks never race on the session's
// activity/termination columns.
func (c *SessionsController) activity(w http.ResponseWriter, r *http.Request) {
	if c.Activity == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/activity")
		return
	}
	var in SetActivityRequest
	if err := decodeJSON(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	state := domain.ActivityState(in.State)
	switch state {
	case domain.ActivityActive, domain.ActivityIdle, domain.ActivityWaitingInput, domain.ActivityExited:
	default:
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_ACTIVITY_STATE", "Unknown activity state", nil)
		return
	}
	if err := c.Activity.ApplyActivitySignal(r.Context(), sessionID(r), ports.ActivitySignal{Valid: true, State: state}); err != nil {
		if errors.Is(err, ports.ErrSessionNotFound) {
			envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "SESSION_NOT_FOUND", "Unknown session", nil)
			return
		}
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, SetActivityResponse{OK: true, SessionID: sessionID(r), State: in.State})
}

func (c *SessionsController) spawnOrchestrator(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/orchestrators")
		return
	}
	var in SpawnOrchestratorRequest
	if err := decodeJSON(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	if in.ProjectID == "" {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "PROJECT_ID_REQUIRED", "projectId is required", nil)
		return
	}
	sess, err := c.Svc.SpawnOrchestrator(r.Context(), in.ProjectID, in.Clean)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusCreated, SpawnOrchestratorResponse{
		Orchestrator: OrchestratorResponse{ID: sess.ID, ProjectID: sess.ProjectID},
	})
}

func (c *SessionsController) listOrchestrators(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/orchestrators")
		return
	}
	sessions, err := c.Svc.List(r.Context(), sessionsvc.ListFilter{OrchestratorOnly: true})
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ListSessionsResponse{Sessions: sessionViews(sessions)})
}

func (c *SessionsController) getOrchestrator(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/orchestrators/{id}")
		return
	}
	sess, err := c.Svc.Get(r.Context(), orchestratorID(r))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	if sess.Kind != domain.KindOrchestrator {
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "SESSION_NOT_FOUND", "Unknown session", nil)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, SessionResponse{Session: sessionView(sess)})
}

func sessionID(r *http.Request) domain.SessionID {
	return domain.SessionID(chi.URLParam(r, "sessionId"))
}

func orchestratorID(r *http.Request) domain.SessionID {
	return domain.SessionID(chi.URLParam(r, "id"))
}

func parseSessionListFilter(r *http.Request) (sessionsvc.ListFilter, error) {
	q := r.URL.Query()
	filter := sessionsvc.ListFilter{ProjectID: domain.ProjectID(q.Get("project"))}
	if raw := q.Get("active"); raw != "" {
		active, err := strconv.ParseBool(raw)
		if err != nil {
			return sessionsvc.ListFilter{}, errors.New("active must be a boolean")
		}
		filter.Active = &active
	}
	if raw := q.Get("orchestratorOnly"); raw != "" {
		orchestratorOnly, err := strconv.ParseBool(raw)
		if err != nil {
			return sessionsvc.ListFilter{}, errors.New("orchestratorOnly must be a boolean")
		}
		filter.OrchestratorOnly = orchestratorOnly
	}
	if raw := q.Get("fresh"); raw != "" {
		fresh, err := strconv.ParseBool(raw)
		if err != nil {
			return sessionsvc.ListFilter{}, errors.New("fresh must be a boolean")
		}
		filter.Fresh = fresh
	}
	return filter, nil
}

func writeSessionPRError(w http.ResponseWriter, r *http.Request, err error) {
	var claimed ports.PRClaimedByActiveSessionError
	switch {
	case errors.Is(err, sessionsvc.ErrInvalidPRRef):
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_PR_REF", "PR reference must be a github.com PR URL, a GitLab merge-request URL, or a number", nil)
	case errors.Is(err, sessionsvc.ErrPRNotFound):
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "PR_NOT_FOUND", "Unknown PR", nil)
	case errors.Is(err, sessionsvc.ErrPRNotOpen):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "PR_NOT_OPEN", "PR is not open", nil)
	case errors.As(err, &claimed):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "PR_CLAIMED_BY_ACTIVE_SESSION", "PR is already claimed by active session "+string(claimed.Owner)+" (omit --no-takeover to steal)", map[string]any{"ownerSessionId": string(claimed.Owner)})
	case errors.Is(err, sessionsvc.ErrSessionNotClaimable):
		envelope.WriteAPIError(w, r, http.StatusUnprocessableEntity, "unprocessable", "SESSION_NOT_CLAIMABLE", "Session cannot claim PRs", nil)
	case errors.Is(err, sessionsvc.ErrSessionNoWorkspace):
		envelope.WriteAPIError(w, r, http.StatusUnprocessableEntity, "unprocessable", "SESSION_NO_WORKSPACE", "Session has no workspace", nil)
	case errors.Is(err, sessionsvc.ErrProjectMismatch):
		envelope.WriteAPIError(w, r, http.StatusUnprocessableEntity, "unprocessable", "PR_PROJECT_MISMATCH", "PR does not belong to the session project", nil)
	case errors.Is(err, sessionsvc.ErrSCMUnavailable):
		envelope.WriteAPIError(w, r, http.StatusServiceUnavailable, "unavailable", "SCM_UNAVAILABLE", "SCM unavailable", nil)
	default:
		envelope.WriteError(w, r, err)
	}
}

func discoverPreviewEntry(workspacePath string) (string, bool) {
	entry, ok := previewutil.DiscoverEntry(workspacePath)
	return entry.Path, ok
}

// resolveLocalPreview maps a workspace-local path (e.g. "index.html" or
// "./dist/index.html") to its preview/files proxy URL when the path resolves to
// a regular file inside the session workspace. It returns ok=false for anything
// that already looks like a URL (an http(s)/file scheme, or a host:port dev
// server) and for paths that escape the workspace or do not point at a file, so
// the caller keeps those targets verbatim.
func resolveLocalPreview(r *http.Request, id domain.SessionID, workspacePath, raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || hasURLScheme(raw) {
		return "", false
	}
	file, ok := confinedPreviewPath(workspacePath, raw)
	if !ok {
		return "", false
	}
	info, err := os.Stat(file)
	if err != nil || info.IsDir() {
		return "", false
	}
	entry := strings.TrimPrefix(path.Clean("/"+raw), "/")
	return previewFileURL(r, id, entry), true
}

func resolvePreviewTarget(r *http.Request, id domain.SessionID, workspacePath, raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if isAbsolutePreviewPath(raw) {
		return absolutePreviewFileURL(raw)
	}
	if resolved, ok := resolveLocalPreview(r, id, workspacePath, raw); ok {
		return resolved, nil
	}
	return raw, nil
}

func isAbsolutePreviewPath(raw string) bool {
	return filepath.IsAbs(raw) || isWindowsAbsolutePath(raw)
}

func isWindowsAbsolutePath(raw string) bool {
	return len(raw) >= 3 && ((raw[0] >= 'a' && raw[0] <= 'z') || (raw[0] >= 'A' && raw[0] <= 'Z')) && raw[1] == ':' && (raw[2] == '\\' || raw[2] == '/')
}

func absolutePreviewFileURL(raw string) (string, error) {
	file, err := filepath.Abs(raw)
	if err != nil {
		return "", errPreviewFileNotFound
	}
	info, err := os.Stat(file)
	if err != nil || info.IsDir() {
		return "", errPreviewFileNotFound
	}
	filePath := filepath.ToSlash(file)
	if filepath.VolumeName(file) != "" || isWindowsAbsolutePath(filePath) {
		filePath = "/" + filePath
	}
	return (&url.URL{Scheme: "file", Path: filePath}).String(), nil
}

func writePreviewResolveError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, errPreviewFileNotFound) {
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "PREVIEW_FILE_NOT_FOUND", "Preview file not found", nil)
		return
	}
	envelope.WriteError(w, r, err)
}

// hasURLScheme reports whether raw begins with an RFC-3986 "scheme:" prefix
// (http:, https:, file:, or a host:port like localhost:5173). It mirrors the
// renderer's withDefaultScheme heuristic so the daemon and browser panel agree
// on what counts as a URL versus a workspace-relative path.
func hasURLScheme(raw string) bool {
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if c == ':' {
			return i > 0
		}
		isSchemeChar := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '+' || c == '.' || c == '-'
		if !isSchemeChar {
			return false
		}
	}
	return false
}

func confinedPreviewPath(workspacePath, assetPath string) (string, bool) {
	return previewutil.ConfinedPath(workspacePath, assetPath)
}

func previewFileURL(r *http.Request, id domain.SessionID, entry string) string {
	return previewutil.FileURL("http://"+r.Host, id, entry)
}

func sessionView(s domain.Session) SessionView {
	// The prompt is surfaced only for a prepared TODO (the board detail modal
	// edits it); a live session keeps its prompt off the read model.
	prompt := ""
	if s.IsTodo {
		prompt = s.Metadata.Prompt
	}
	return SessionView{Session: s, Branch: s.Metadata.Branch, WorkspacePath: s.Metadata.WorkspacePath, PreviewURL: s.Metadata.PreviewURL, PreviewRevision: s.Metadata.PreviewRevision, Prompt: prompt, PRs: sessionPRFacts(s.PRs), TokenUsage: sessionTokenUsage(s)}
}

// sessionTokenUsage builds the curated token-telemetry wire object, deriving the raw
// + cost-weighted totals and the runaway flag from the stored buckets. It returns nil
// (so the field is omitted) when the session has never been parsed — a fresh session
// or a non-claude-code agent whose transcript AO cannot read — which the UI reads as
// "no chip / n/a".
func sessionTokenUsage(s domain.Session) *SessionTokenUsage {
	if s.TokensUpdatedAt.IsZero() {
		return nil
	}
	u := s.TokenUsage
	return &SessionTokenUsage{
		Input:         u.Input,
		CacheCreation: u.CacheCreation,
		CacheRead:     u.CacheRead,
		Output:        u.Output,
		Turns:         u.Turns,
		RawTotal:      u.RawTotal(),
		CostWeighted:  u.CostWeighted(),
		Runaway:       u.IsRunaway(),
		UpdatedAt:     s.TokensUpdatedAt,
	}
}

func sessionViews(sessions []domain.Session) []SessionView {
	out := make([]SessionView, 0, len(sessions))
	for _, s := range sessions {
		out = append(out, sessionView(s))
	}
	return out
}

func sessionPRFacts(prs []domain.PRFacts) []SessionPRFacts {
	out := make([]SessionPRFacts, 0, len(prs))
	for _, pr := range prs {
		out = append(out, SessionPRFacts{URL: pr.URL, Number: pr.Number, State: prState(pr), CI: pr.CI, Review: pr.Review, Mergeability: pr.Mergeability, ReviewComments: pr.ReviewComments, UpdatedAt: pr.UpdatedAt})
	}
	return out
}

func sessionPRSummaries(prs []sessionsvc.PRSummary) []SessionPRSummary {
	out := make([]SessionPRSummary, 0, len(prs))
	for _, pr := range prs {
		out = append(out, NewSessionPRSummary(pr))
	}
	return out
}

func prState(pr domain.PRFacts) string {
	switch {
	case pr.Merged:
		return string(domain.PRStateMerged)
	case pr.Closed:
		return string(domain.PRStateClosed)
	case pr.Draft:
		return string(domain.PRStateDraft)
	default:
		return string(domain.PRStateOpen)
	}
}

func nonNilSessionIDs(ids []domain.SessionID) []domain.SessionID {
	if ids == nil {
		return []domain.SessionID{}
	}
	return ids
}
