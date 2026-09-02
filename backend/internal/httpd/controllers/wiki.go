package controllers

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	wikisvc "github.com/aoagents/agent-orchestrator/backend/internal/service/wiki"
)

// WikiService is the controller-facing contract for the Wiki destination: the
// note vault on disk and the one agent running inside it. *wiki.Service
// satisfies it.
type WikiService interface {
	Status(ctx context.Context) (wikisvc.Status, error)
	Start(ctx context.Context, harness domain.AgentHarness) (wikisvc.Status, error)
	Restart(ctx context.Context) (wikisvc.Status, error)
	Stop(ctx context.Context) (wikisvc.Status, error)
	ListFiles(ctx context.Context) (wikisvc.Files, error)
	ReadNote(ctx context.Context, path string) (wikisvc.NoteContent, error)
}

// WikiController owns the /wiki routes. A nil service keeps them mounted and
// answers OpenAPI-backed 501s, matching every other controller here.
type WikiController struct {
	Svc WikiService
}

// Register mounts the Wiki routes.
func (c *WikiController) Register(r chi.Router) {
	r.Get("/wiki", c.status)
	r.Post("/wiki/agent", c.start)
	r.Post("/wiki/agent/restart", c.restart)
	r.Delete("/wiki/agent", c.stop)
	r.Get("/wiki/files", c.files)
	r.Get("/wiki/file", c.note)
}

func (c *WikiController) status(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/wiki")
		return
	}
	st, err := c.Svc.Status(r.Context())
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, wikiStatusResponse(st))
}

func (c *WikiController) start(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/wiki/agent")
		return
	}
	var in StartWikiAgentRequest
	if err := decodeJSON(r, &in); err != nil && r.ContentLength > 0 {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	st, err := c.Svc.Start(r.Context(), domain.AgentHarness(strings.TrimSpace(in.Harness)))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, wikiStatusResponse(st))
}

func (c *WikiController) restart(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/wiki/agent/restart")
		return
	}
	st, err := c.Svc.Restart(r.Context())
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, wikiStatusResponse(st))
}

func (c *WikiController) stop(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "DELETE", "/api/v1/wiki/agent")
		return
	}
	st, err := c.Svc.Stop(r.Context())
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, wikiStatusResponse(st))
}

func (c *WikiController) files(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/wiki/files")
		return
	}
	res, err := c.Svc.ListFiles(r.Context())
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	notes := make([]WikiNoteSummary, 0, len(res.Notes))
	for _, n := range res.Notes {
		notes = append(notes, WikiNoteSummary{Path: n.Path, Size: n.Size, ModifiedAt: wikiStamp(n.ModifiedAt)})
	}
	envelope.WriteJSON(w, http.StatusOK, WikiFilesResponse{Notes: notes, Truncated: res.Truncated})
}

func (c *WikiController) note(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/wiki/file")
		return
	}
	path := strings.TrimSpace(r.URL.Query().Get("path"))
	if path == "" {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "PATH_REQUIRED", "path is required", nil)
		return
	}
	note, err := c.Svc.ReadNote(r.Context(), path)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	backlinks := note.Backlinks
	if backlinks == nil {
		backlinks = []string{}
	}
	envelope.WriteJSON(w, http.StatusOK, WikiNoteResponse{
		Path:       note.Path,
		Content:    note.Content,
		Size:       note.Size,
		Backlinks:  backlinks,
		ModifiedAt: wikiStamp(note.ModifiedAt),
	})
}

func wikiStatusResponse(st wikisvc.Status) WikiStatusResponse {
	return WikiStatusResponse{
		Configured:  st.Configured,
		VaultPath:   st.VaultPath,
		DisplayPath: st.DisplayPath,
		Harness:     st.Harness,
		Running:     st.Running,
		HandleID:    st.HandleID,
		StartedAt:   wikiStamp(st.StartedAt),
	}
}

func wikiStamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
