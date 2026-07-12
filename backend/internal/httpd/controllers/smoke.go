package controllers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	jiraadapter "github.com/aoagents/agent-orchestrator/backend/internal/adapters/jira"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	smokesvc "github.com/aoagents/agent-orchestrator/backend/internal/service/smoke"
)

// maxSmokeUploadBytes bounds a single evidence upload (video cap + multipart
// overhead slack) so a runaway request cannot spool an unbounded temp file
// before the service's per-kind size check runs.
const maxSmokeUploadBytes int64 = (200 << 20) + (1 << 20)

// SmokeAuthoredCaseInput is one worker-authored case in a PUT smoke-checks body.
// tag is derived (CHECK N) from position on read and accepted only for
// forward-compatibility; it is not persisted.
type SmokeAuthoredCaseInput struct {
	ID       string   `json:"id,omitempty" description:"Stable case id. Optional — derived from the name (slugified) when omitted. Supplying it keeps the user's verdict/note/evidence across a re-author."`
	Tag      string   `json:"tag,omitempty" description:"Derived display tag (CHECK N); accepted but not persisted."`
	Name     string   `json:"name" description:"One-line 'what to verify'."`
	Why      string   `json:"why,omitempty" description:"Why it matters / what it confirms."`
	Steps    []string `json:"steps,omitempty" description:"Ordered play steps."`
	Expected string   `json:"expected,omitempty" description:"Expected result."`
	PRNum    int      `json:"prNum,omitempty" description:"PR/MR number the change belongs to (0 if none)."`
	FileRef  string   `json:"fileRef,omitempty" description:"file:line the change touched."`
}

// AuthorSmokeChecksInput is the body of PUT .../smoke-checks: the whole
// checklist, replacing any prior one (results preserved by case id).
type AuthorSmokeChecksInput struct {
	Cases []SmokeAuthoredCaseInput `json:"cases" description:"The full 3–6 case checklist."`
}

// ListSmokeChecksResponse is the body of GET .../smoke-checks.
type ListSmokeChecksResponse struct {
	Worker     string              `json:"worker" description:"Worker label for the tab subtitle."`
	ReportedAt *time.Time          `json:"reportedAt,omitempty" description:"When this session's results were last reported back."`
	Checks     []domain.SmokeCheck `json:"checks"`
}

// SmokeCheckResponse is the { check } body returned by verdict/reset.
type SmokeCheckResponse struct {
	Check domain.SmokeCheck `json:"check"`
}

// SetSmokeVerdictInput is the body of POST .../{checkId}/verdict.
type SetSmokeVerdictInput struct {
	Verdict string `json:"verdict" description:"pass | fail | skip."`
	Note    string `json:"note,omitempty" description:"Optional note about what the user saw."`
}

// SmokeEvidenceResponse is the { evidence } body returned by an evidence upload.
type SmokeEvidenceResponse struct {
	Evidence domain.SmokeEvidence `json:"evidence"`
}

// ReportSmokeResponse is the body of POST .../smoke-checks/report.
type ReportSmokeResponse struct {
	Delivered bool   `json:"delivered" description:"Whether the summary was delivered to a live session."`
	Target    string `json:"target" description:"worker | orchestrator | persisted."`
	Summary   string `json:"summary" description:"The composed results summary."`
}

// PostSmokeToJiraResponse is the body of POST .../smoke-checks/jira.
type PostSmokeToJiraResponse struct {
	Key                 string `json:"key" description:"The Jira issue key the results were posted to."`
	CommentURL          string `json:"commentUrl" description:"Deep link to the created comment (empty if Jira returned no self link)."`
	AttachmentsUploaded int    `json:"attachmentsUploaded" description:"Number of evidence files uploaded as Jira attachments."`
	RowsPosted          int    `json:"rowsPosted" description:"Number of run rows (verdict set) posted in the table."`
	EmbeddedMedia       bool   `json:"embeddedMedia" description:"Whether image evidence embedded inline (false = attachment-link fallback)."`
}

// SmokeController owns the session-scoped /smoke-checks routes. A nil Svc
// returns 501, mirroring ReviewsController.
type SmokeController struct {
	Svc smokesvc.Manager
}

// Register mounts the smoke routes on the supplied router.
func (c *SmokeController) Register(r chi.Router) {
	r.Get("/sessions/{sessionId}/smoke-checks", c.list)
	r.Put("/sessions/{sessionId}/smoke-checks", c.author)
	r.Post("/sessions/{sessionId}/smoke-checks/report", c.report)
	r.Post("/sessions/{sessionId}/smoke-checks/jira", c.postJira)
	r.Post("/sessions/{sessionId}/smoke-checks/{checkId}/verdict", c.verdict)
	r.Post("/sessions/{sessionId}/smoke-checks/{checkId}/reset", c.reset)
	r.Post("/sessions/{sessionId}/smoke-checks/{checkId}/evidence", c.uploadEvidence)
	r.Get("/sessions/{sessionId}/smoke-checks/{checkId}/evidence/{evidenceId}", c.serveEvidence)
}

func (c *SmokeController) list(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/sessions/{sessionId}/smoke-checks")
		return
	}
	res, err := c.Svc.List(r.Context(), sessionID(r))
	if err != nil {
		writeSmokeError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, smokeListResponse(res))
}

func (c *SmokeController) author(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "PUT", "/api/v1/sessions/{sessionId}/smoke-checks")
		return
	}
	var in AuthorSmokeChecksInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_BODY", "Invalid request body", nil)
		return
	}
	cases := make([]domain.SmokeAuthoredCase, 0, len(in.Cases))
	for _, item := range in.Cases {
		cases = append(cases, domain.SmokeAuthoredCase{
			ID:       item.ID,
			Name:     item.Name,
			Why:      item.Why,
			Steps:    item.Steps,
			Expected: item.Expected,
			PRNum:    item.PRNum,
			FileRef:  item.FileRef,
		})
	}
	res, err := c.Svc.Author(r.Context(), sessionID(r), cases)
	if err != nil {
		writeSmokeError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, smokeListResponse(res))
}

func (c *SmokeController) verdict(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/smoke-checks/{checkId}/verdict")
		return
	}
	var in SetSmokeVerdictInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_BODY", "Invalid request body", nil)
		return
	}
	check, err := c.Svc.SetVerdict(r.Context(), sessionID(r), chi.URLParam(r, "checkId"), domain.SmokeVerdict(strings.TrimSpace(in.Verdict)), in.Note)
	if err != nil {
		writeSmokeError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, SmokeCheckResponse{Check: check})
}

func (c *SmokeController) reset(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/smoke-checks/{checkId}/reset")
		return
	}
	check, err := c.Svc.Reset(r.Context(), sessionID(r), chi.URLParam(r, "checkId"))
	if err != nil {
		writeSmokeError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, SmokeCheckResponse{Check: check})
}

func (c *SmokeController) uploadEvidence(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/smoke-checks/{checkId}/evidence")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxSmokeUploadBytes)
	upload, cleanup, err := readEvidenceUpload(r)
	if err != nil {
		envelope.WriteAPIError(w, r, http.StatusUnprocessableEntity, "unprocessable", "SMOKE_EVIDENCE_INVALID", err.Error(), nil)
		return
	}
	defer cleanup()
	ev, err := c.Svc.AttachEvidence(r.Context(), sessionID(r), chi.URLParam(r, "checkId"), upload)
	if err != nil {
		writeSmokeError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, SmokeEvidenceResponse{Evidence: ev})
}

func (c *SmokeController) serveEvidence(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/sessions/{sessionId}/smoke-checks/{checkId}/evidence/{evidenceId}")
		return
	}
	blob, err := c.Svc.OpenEvidence(r.Context(), sessionID(r), chi.URLParam(r, "checkId"), chi.URLParam(r, "evidenceId"))
	if err != nil {
		writeSmokeError(w, r, err)
		return
	}
	if blob.Mime != "" {
		w.Header().Set("Content-Type", blob.Mime)
	}
	if blob.Filename != "" {
		w.Header().Set("Content-Disposition", "inline; filename=\""+sanitizeHeaderFilename(blob.Filename)+"\"")
	}
	http.ServeFile(w, r, blob.Path)
}

func (c *SmokeController) report(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/smoke-checks/report")
		return
	}
	outcome, err := c.Svc.Report(r.Context(), sessionID(r))
	if err != nil {
		writeSmokeError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ReportSmokeResponse{Delivered: outcome.Delivered, Target: outcome.Target, Summary: outcome.Summary})
}

func (c *SmokeController) postJira(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/smoke-checks/jira")
		return
	}
	out, err := c.Svc.PostToJira(r.Context(), sessionID(r))
	if err != nil {
		writeSmokeJiraError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, PostSmokeToJiraResponse{
		Key:                 out.Key,
		CommentURL:          out.CommentURL,
		AttachmentsUploaded: out.AttachmentsUploaded,
		RowsPosted:          out.RowsPosted,
		EmbeddedMedia:       out.EmbeddedMedia,
	})
}

// readEvidenceUpload accepts either multipart/form-data (a "file" field, the
// frontend path) or a raw body with X-Filename/Content-Type headers. cleanup
// releases any spooled multipart temp files after the handler returns.
func readEvidenceUpload(r *http.Request) (smokesvc.EvidenceUpload, func(), error) {
	noop := func() {}
	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "multipart/form-data") {
		// r.Body is already bounded by http.MaxBytesReader(maxSmokeUploadBytes) in
		// the handler, so this parse cannot spool an unbounded temp file.
		if err := r.ParseMultipartForm(16 << 20); err != nil { //nolint:gosec // G120: body capped by MaxBytesReader above
			return smokesvc.EvidenceUpload{}, noop, errors.New("could not read the uploaded file")
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			return smokesvc.EvidenceUpload{}, noop, errors.New("expected a 'file' form field")
		}
		mimeType := ""
		if header != nil {
			mimeType = header.Header.Get("Content-Type")
		}
		cleanup := func() {
			_ = file.Close()
			if r.MultipartForm != nil {
				_ = r.MultipartForm.RemoveAll()
			}
		}
		name := ""
		if header != nil {
			name = header.Filename
		}
		return smokesvc.EvidenceUpload{Filename: name, Mime: mimeType, Reader: file}, cleanup, nil
	}
	return smokesvc.EvidenceUpload{
		Filename: r.Header.Get("X-Filename"),
		Mime:     ct,
		Reader:   r.Body,
	}, noop, nil
}

func smokeListResponse(res smokesvc.SessionSmoke) ListSmokeChecksResponse {
	checks := res.Checks
	if checks == nil {
		checks = []domain.SmokeCheck{}
	}
	return ListSmokeChecksResponse{Worker: res.Worker, ReportedAt: res.ReportedAt, Checks: checks}
}

// sanitizeHeaderFilename drops quotes/newlines so a stored display filename
// cannot break the Content-Disposition header.
func sanitizeHeaderFilename(name string) string {
	return strings.Map(func(r rune) rune {
		if r == '"' || r == '\\' || r == '\n' || r == '\r' {
			return '_'
		}
		return r
	}, name)
}

func writeSmokeError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, smokesvc.ErrInvalid):
		envelope.WriteAPIError(w, r, http.StatusUnprocessableEntity, "unprocessable", "SMOKE_INVALID", err.Error(), nil)
	case errors.Is(err, smokesvc.ErrNotFound):
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "SMOKE_NOT_FOUND", err.Error(), nil)
	default:
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "SMOKE_OPERATION_FAILED", "Smoke operation failed", nil)
	}
}

// writeSmokeJiraError maps the Post-to-Jira failures. ErrNotLinked gets a distinct
// code the Tests tab uses to steer the user to link an issue first; the Jira
// adapter sentinels surface their (actionable) message so a missing/write-scoped
// token, bad key, or Jira hiccup shows inline rather than crashing the view. It
// falls back to the shared smoke mapper for ErrInvalid/ErrNotFound.
func writeSmokeJiraError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, smokesvc.ErrNotLinked):
		envelope.WriteAPIError(w, r, http.StatusUnprocessableEntity, "unprocessable", "SMOKE_JIRA_NOT_LINKED",
			"This session isn't linked to a Jira issue. Link one on the Summary tab first, then post results.", nil)
	case errors.Is(err, jiraadapter.ErrNotFound):
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "SMOKE_JIRA_ISSUE_NOT_FOUND",
			"The linked Jira issue wasn't found or isn't visible to your account.", nil)
	case errors.Is(err, jiraadapter.ErrBadKey):
		envelope.WriteAPIError(w, r, http.StatusUnprocessableEntity, "unprocessable", "SMOKE_JIRA_BAD_KEY",
			"The linked Jira key is invalid.", nil)
	case errors.Is(err, jiraadapter.ErrBadRequest):
		envelope.WriteAPIError(w, r, http.StatusUnprocessableEntity, "unprocessable", "SMOKE_JIRA_BAD_REQUEST",
			smokeJiraMessage(err, "Jira rejected the comment."), nil)
	case errors.Is(err, jiraadapter.ErrAuthFailed):
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "SMOKE_JIRA_AUTH_FAILED",
			smokeJiraMessage(err, "Jira authentication failed — set a write-scoped JIRA_API_TOKEN (or AO_JIRA_TOKEN)."), nil)
	case errors.Is(err, jiraadapter.ErrUnavailable):
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "SMOKE_JIRA_UNAVAILABLE",
			smokeJiraMessage(err, "Couldn't reach Jira."), nil)
	default:
		writeSmokeError(w, r, err)
	}
}

// smokeJiraMessage surfaces the sentinel-wrapped detail (e.g. Jira's error text)
// when present, falling back to a generic message.
func smokeJiraMessage(err error, fallback string) string {
	if msg := strings.TrimSpace(err.Error()); msg != "" {
		return msg
	}
	return fallback
}
