package controllers

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/chatblob"
	"github.com/aoagents/agent-orchestrator/backend/internal/chatfiles"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/sessionhost"
)

const (
	maxChatFileRefs     = 64
	maxChatFileRefBytes = 1024
	maxChatFilesBody    = 512 << 10

	// chatFileCSP keeps an HTML or SVG file opened as a document on the daemon
	// origin from running script or reaching the API.
	chatFileCSP = "sandbox; default-src 'none'; img-src data:; style-src 'unsafe-inline'"
)

// ChatFilesSessions is the part of the session service the chat file routes need.
type ChatFilesSessions interface {
	Get(ctx context.Context, id domain.SessionID) (domain.Session, error)
}

// ChatFilesController serves the files a chat session's agent names, from the
// session's worktree and its project's knowledge directory only.
type ChatFilesController struct {
	Sessions ChatFilesSessions
	// KnowledgeDir is the knowledge store root; a session reads KnowledgeDir/<project>.
	KnowledgeDir string
	DataDir      string
}

// Register mounts the chat file routes on the supplied router.
func (c *ChatFilesController) Register(r chi.Router) {
	r.Post("/sessions/{sessionId}/chat/files/resolve", c.resolve)
	r.Get("/sessions/{sessionId}/chat/files/{root}/*", c.serve)
	r.Head("/sessions/{sessionId}/chat/files/{root}/*", c.serve)
	r.Get("/sessions/{sessionId}/chat/blobs/{blob}", c.blob)
	r.Get("/sessions/{sessionId}/chat/tasks/{taskId}/output", c.taskOutput)
}

func (c *ChatFilesController) roots(r *http.Request) (chatfiles.Roots, error) {
	sess, err := c.Sessions.Get(r.Context(), sessionID(r))
	if err != nil {
		return nil, err
	}
	knowledge := ""
	if id := string(sess.ProjectID); c.KnowledgeDir != "" && isSingleDirName(id) {
		knowledge = filepath.Join(c.KnowledgeDir, id)
	}
	return chatfiles.NewRoots(sess.Metadata.WorkspacePath, knowledge), nil
}

func isSingleDirName(name string) bool {
	return name != "" && name != "." && name != ".." && filepath.Base(name) == name
}

func (c *ChatFilesController) resolve(w http.ResponseWriter, r *http.Request) {
	if c.Sessions == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/chat/files/resolve")
		return
	}
	var body ResolveChatFilesRequest
	r.Body = http.MaxBytesReader(w, r.Body, maxChatFilesBody)
	if err := decodeJSON(r, &body); err != nil {
		chatBadRequest(w, r, "INVALID_JSON", "Invalid JSON body")
		return
	}
	if len(body.Refs) == 0 || len(body.Refs) > maxChatFileRefs {
		chatBadRequest(w, r, "INVALID_REFS", "refs must hold 1 to 64 entries")
		return
	}
	for _, ref := range body.Refs {
		if len(ref) > maxChatFileRefBytes {
			chatBadRequest(w, r, "INVALID_REFS", "each ref must be at most 1024 bytes")
			return
		}
	}
	roots, err := c.roots(r)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	out := ResolveChatFilesResponse{Files: make([]ChatFileRef, 0, len(body.Refs))}
	for _, ref := range body.Refs {
		entry := ChatFileRef{Ref: ref, Matches: []ChatFileMatch{}}
		for _, f := range roots.Resolve(ref) {
			entry.Matches = append(entry.Matches, ChatFileMatch{
				Root:       string(f.Root),
				Path:       f.Path,
				Kind:       string(f.Kind),
				MIME:       f.MIME,
				Size:       f.Size,
				ModifiedAt: f.ModTime.UTC().Format(time.RFC3339),
			})
		}
		out.Files = append(out.Files, entry)
	}
	envelope.WriteJSON(w, http.StatusOK, out)
}

func (c *ChatFilesController) serve(w http.ResponseWriter, r *http.Request) {
	if c.Sessions == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/sessions/{sessionId}/chat/files/{root}/*")
		return
	}
	roots, err := c.roots(r)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	rel := chi.URLParam(r, "*")
	// chi routes on the raw path when it differs from the decoded one, so an
	// encoded "%2e%2e" reaches here still encoded. Decode it so the ".." check
	// sees what the filesystem would.
	if r.URL.RawPath != "" {
		decoded, err := url.PathUnescape(rel)
		if err != nil {
			chatFileNotFound(w, r)
			return
		}
		rel = decoded
	}
	file, abs, err := roots.Open(chatfiles.Root(chi.URLParam(r, "root")), rel)
	if errors.Is(err, chatfiles.ErrTooLarge) {
		envelope.WriteAPIError(w, r, http.StatusRequestEntityTooLarge, "payload_too_large", "FILE_TOO_LARGE", "File is too large to show", nil)
		return
	}
	if err != nil {
		chatFileNotFound(w, r)
		return
	}
	f, err := os.Open(abs)
	if err != nil {
		chatFileNotFound(w, r)
		return
	}
	defer func() { _ = f.Close() }()
	h := w.Header()
	h.Set("Content-Type", file.MIME)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", chatFileCSP)
	h.Set("Cache-Control", "private, no-cache")
	http.ServeContent(w, r, "", file.ModTime, f)
}

func (c *ChatFilesController) blob(w http.ResponseWriter, r *http.Request) {
	if c.Sessions == nil || c.DataDir == "" {
		apispec.NotImplemented(w, r, "GET", "/api/v1/sessions/{sessionId}/chat/blobs/{blob}")
		return
	}
	id := sessionID(r)
	if _, err := c.Sessions.Get(r.Context(), id); err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	logPath, _ := sessionhost.Paths(c.DataDir, string(id))
	name := chi.URLParam(r, "blob")
	path, ok := chatblob.ForLog(logPath).Path(name)
	if !ok {
		chatFileNotFound(w, r)
		return
	}
	f, err := os.Open(path) //nolint:gosec // G304: Path accepts only <sha256>.<ext> names inside the session's blob dir
	if err != nil {
		chatFileNotFound(w, r)
		return
	}
	defer func() { _ = f.Close() }()
	h := w.Header()
	h.Set("Content-Type", chatblob.MIME(name))
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", chatFileCSP)
	h.Set("Cache-Control", "private, max-age=31536000, immutable")
	http.ServeContent(w, r, "", time.Time{}, f)
}

func chatFileNotFound(w http.ResponseWriter, r *http.Request) {
	envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "CHAT_FILE_NOT_FOUND", "File not found", nil)
}
