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
	WriteNote(ctx context.Context, in wikisvc.WriteNoteInput) (wikisvc.WriteNoteResult, error)
	ListTasks(ctx context.Context) (wikisvc.Tasks, error)
	CompleteTask(ctx context.Context, in wikisvc.CompleteTaskInput) (wikisvc.CompleteTaskResult, error)
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
	r.Put("/wiki/file", c.writeNote)
	r.Get("/wiki/tasks", c.tasks)
	r.Post("/wiki/tasks/complete", c.completeTask)
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
		Path:        note.Path,
		Content:     note.Content,
		Size:        note.Size,
		ContentHash: note.ContentHash,
		Backlinks:   backlinks,
		ModifiedAt:  wikiStamp(note.ModifiedAt),
	})
}

// writeNote saves one note. The body carries the note's whole new bytes plus
// the hash it was read with; the service refuses the write outright if the file
// moved underneath the editor.
func (c *WikiController) writeNote(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "PUT", "/api/v1/wiki/file")
		return
	}
	var in WriteWikiNoteRequest
	if err := decodeJSON(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	if strings.TrimSpace(in.Path) == "" {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "PATH_REQUIRED", "path is required", nil)
		return
	}
	if in.Content == nil {
		envelope.WriteAPIError(
			w, r, http.StatusBadRequest, "bad_request", "WIKI_NOTE_CONTENT_REQUIRED",
			"content is required; emptying a note must be spelled as an explicit empty string", nil,
		)
		return
	}
	res, err := c.Svc.WriteNote(r.Context(), wikisvc.WriteNoteInput{
		Path:     in.Path,
		Content:  *in.Content,
		BaseHash: strings.TrimSpace(in.BaseHash),
	})
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, WriteWikiNoteResponse{
		Path:        res.Path,
		ContentHash: res.ContentHash,
		Size:        res.Size,
		ModifiedAt:  wikiStamp(res.ModifiedAt),
	})
}

// tasks lists every unchecked row in the configured subtrees. The cutoff and
// the owner filter travel WITH the rows rather than being applied here — see
// WikiTasksResponse for why.
func (c *WikiController) tasks(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/wiki/tasks")
		return
	}
	res, err := c.Svc.ListTasks(r.Context())
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	rows := make([]WikiTaskRow, 0, len(res.Rows))
	for _, t := range res.Rows {
		rows = append(rows, WikiTaskRow{
			ID:         t.ID,
			Path:       t.Path,
			Line:       t.Line,
			Raw:        t.Raw,
			Text:       t.Text,
			Section:    t.Section,
			Subsection: t.Subsection,
			Owner:      t.Owner,
			Due:        t.Due,
			Created:    t.Created,
			FromDate:   t.FromDate,
		})
	}
	envelope.WriteJSON(w, http.StatusOK, WikiTasksResponse{
		Configured:   res.Configured,
		Folders:      nonNil(res.Folders),
		Sections:     nonNil(res.Sections),
		Cutoff:       res.Cutoff,
		OwnerAliases: nonNil(res.OwnerAliases),
		Tasks:        rows,
		Owners:       nonNil(res.Owners),
		ScannedNotes: res.ScannedNotes,
		Truncated:    res.Truncated,
	})
}

// completeTask ticks one row off in the note it lives in. The body carries the
// row's exact text, and a line whose text no longer matches is refused rather
// than written to.
func (c *WikiController) completeTask(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/wiki/tasks/complete")
		return
	}
	var in CompleteWikiTaskRequest
	if err := decodeJSON(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	if strings.TrimSpace(in.Path) == "" {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "PATH_REQUIRED", "path is required", nil)
		return
	}
	res, err := c.Svc.CompleteTask(r.Context(), wikisvc.CompleteTaskInput{
		Path: in.Path,
		Line: in.Line,
		// NOT trimmed: the row's identity is its exact bytes, and trimming here
		// would make a row with trailing whitespace unmatchable.
		Raw: in.Raw,
	})
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, CompleteWikiTaskResponse{
		Path:           res.Path,
		Line:           res.Line,
		Raw:            res.Raw,
		Moved:          res.Moved,
		NoteModifiedAt: res.NoteModifiedAt,
	})
}

// nonNil keeps a list field rendering as [] rather than null, so the renderer
// never has to guard a map over it.
func nonNil(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
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
