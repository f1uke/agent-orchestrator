package smoke

// Post a session's smoke-test results to its linked Jira issue as an ADF
// comment — one readable section per run row (title, status, the authored
// context, the note, and evidence) — with each evidence screenshot/clip uploaded
// as a Jira attachment and referenced inline. This is an explicit, user-triggered
// action (the "Post to Jira" button on the Tests tab) — the SECOND sanctioned
// Jira write after the status move, never automatic.
//
// Design: image evidence is embedded inline via ADF media nodes so it previews
// directly on the issue. Because a media node the instance can't resolve can 400
// the whole comment, a 400 falls back to re-posting the same comment without the
// media nodes — each evidence file then renders as a link to its attachment
// `content` URL instead. The outcome reports whether the inline media survived.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	jiraadapter "github.com/aoagents/agent-orchestrator/backend/internal/adapters/jira"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// ErrNotLinked reports that Post-to-Jira targeted a session with no Jira binding.
// The controller maps it to a 4xx with a code the Tests tab uses to guide the
// user to link an issue first.
var ErrNotLinked = errors.New("smoke: session is not linked to a Jira issue")

// jiraIssueIDPrefix is the canonical prefix a Jira-bound session carries in
// sessions.issue_id ("jira:<KEY>").
const jiraIssueIDPrefix = string(domain.TrackerProviderJira) + ":"

// JiraPoster is the write surface PostToJira needs: upload an evidence file and
// post an ADF comment. Satisfied by *adapters/jira.Client. Kept narrow so the
// smoke service depends on the Jira adapter's types only, not its service.
type JiraPoster interface {
	AddAttachment(ctx context.Context, key, filename, mimeType string, r io.Reader) (jiraadapter.Attachment, error)
	AddComment(ctx context.Context, key string, body any) (jiraadapter.Comment, error)
}

// JiraPostOutcome describes a completed Post-to-Jira: the issue it landed on, a
// deep link to the created comment, how many evidence files were attached, how
// many result rows the table carried, and whether the inline image media
// survived (false = the media-free fallback was used).
type JiraPostOutcome struct {
	Key                 string `json:"key"`
	CommentURL          string `json:"commentUrl"`
	AttachmentsUploaded int    `json:"attachmentsUploaded"`
	RowsPosted          int    `json:"rowsPosted"`
	EmbeddedMedia       bool   `json:"embeddedMedia"`
}

// uploadedEvidence pairs an uploaded attachment with whether it is an image (only
// images are embedded inline as media; videos stay link-only). failed marks a
// file whose upload was skipped after a non-fatal error — the comment renders a
// short note for it (name preserved) instead of a link or media.
type uploadedEvidence struct {
	att     jiraadapter.Attachment
	isImage bool
	failed  bool
	name    string
}

// PostToJira resolves the session's linked Jira key, uploads the evidence on the
// run rows (verdict set), builds the results table, and posts it as a comment.
func (s *Service) PostToJira(ctx context.Context, sessionID domain.SessionID) (JiraPostOutcome, error) {
	if sessionID == "" {
		return JiraPostOutcome{}, fmt.Errorf("%w: session id is required", ErrInvalid)
	}
	rec, ok, err := s.store.GetSession(ctx, sessionID)
	if err != nil {
		return JiraPostOutcome{}, err
	}
	if !ok {
		return JiraPostOutcome{}, fmt.Errorf("%w: session %q", ErrNotFound, sessionID)
	}
	key, ok := jiraKeyFromIssueID(string(rec.IssueID))
	if !ok {
		return JiraPostOutcome{}, ErrNotLinked
	}
	if s.jira == nil {
		return JiraPostOutcome{}, fmt.Errorf("%w: Jira posting is not configured", jiraadapter.ErrUnavailable)
	}
	checks, err := s.store.ListSmokeChecksBySession(ctx, sessionID)
	if err != nil {
		return JiraPostOutcome{}, err
	}
	run := runChecks(checks)
	if len(run) == 0 {
		return JiraPostOutcome{}, fmt.Errorf("%w: no checked cases to post — mark at least one case Pass/Fail/Skip first", ErrInvalid)
	}

	// Upload each run row's evidence, keyed by check id, before building the
	// comment so each case section can embed/link its attachments. A systemic
	// failure (bad or unscoped token, Jira unavailable) hits every upload, so it
	// aborts with a clear error; a one-off file problem must not sink the whole
	// gated post — it is recorded as a failed marker the comment notes and the
	// upload loop keeps going.
	uploads := make(map[string][]uploadedEvidence, len(run))
	total := 0
	for _, c := range run {
		for _, ev := range c.Evidence {
			att, err := s.uploadEvidence(ctx, key, sessionID, c.ID, ev)
			if err != nil {
				if errors.Is(err, jiraadapter.ErrAuthFailed) || errors.Is(err, jiraadapter.ErrUnavailable) {
					return JiraPostOutcome{}, err
				}
				uploads[c.ID] = append(uploads[c.ID], uploadedEvidence{failed: true, isImage: ev.Kind == "image", name: evidenceDisplayName(ev)})
				continue
			}
			uploads[c.ID] = append(uploads[c.ID], uploadedEvidence{att: att, isImage: ev.Kind == "image"})
			total++
		}
	}

	// Attempt the rich comment (image media embedded); on a 400 (likely an
	// unresolved media node) retry once with a media-free doc.
	comment, embedded, err := s.postResultsComment(ctx, key, run, uploads)
	if err != nil {
		return JiraPostOutcome{}, err
	}
	return JiraPostOutcome{
		Key:                 key,
		CommentURL:          comment.URL,
		AttachmentsUploaded: total,
		RowsPosted:          len(run),
		EmbeddedMedia:       embedded,
	}, nil
}

// postResultsComment posts the results table, embedding image media first and
// falling back to a links-only comment if Jira rejects the media (400).
func (s *Service) postResultsComment(ctx context.Context, key string, run []domain.SmokeCheck, uploads map[string][]uploadedEvidence) (jiraadapter.Comment, bool, error) {
	hasImageMedia := false
	for _, evs := range uploads {
		for _, e := range evs {
			if e.isImage && !e.failed && strings.TrimSpace(e.att.ID) != "" {
				hasImageMedia = true
			}
		}
	}
	if hasImageMedia {
		comment, err := s.jira.AddComment(ctx, key, buildResultsADF(run, uploads, true))
		if err == nil {
			return comment, true, nil
		}
		if !errors.Is(err, jiraadapter.ErrBadRequest) {
			return jiraadapter.Comment{}, false, err
		}
		// Fall through: retry without the inline media nodes.
	}
	comment, err := s.jira.AddComment(ctx, key, buildResultsADF(run, uploads, false))
	if err != nil {
		return jiraadapter.Comment{}, false, err
	}
	return comment, false, nil
}

// uploadEvidence streams one stored evidence blob to Jira as an attachment,
// reusing OpenEvidence for the confined path + recorded mime/filename.
func (s *Service) uploadEvidence(ctx context.Context, key string, sessionID domain.SessionID, checkID string, ev domain.SmokeEvidence) (jiraadapter.Attachment, error) {
	blob, err := s.OpenEvidence(ctx, sessionID, checkID, ev.ID)
	if err != nil {
		return jiraadapter.Attachment{}, err
	}
	f, err := os.Open(blob.Path)
	if err != nil {
		return jiraadapter.Attachment{}, fmt.Errorf("open evidence %s: %w", ev.ID, err)
	}
	defer func() { _ = f.Close() }()
	filename := blob.Filename
	if strings.TrimSpace(filename) == "" {
		filename = evidenceFallbackName(ev)
	}
	return s.jira.AddAttachment(ctx, key, filename, blob.Mime, f)
}

// runChecks returns, in seq order, the checks that have a verdict set (the "run"
// rows) — the only cases posted. Untouched "to check" rows are omitted.
func runChecks(checks []domain.SmokeCheck) []domain.SmokeCheck {
	out := make([]domain.SmokeCheck, 0, len(checks))
	for _, c := range checks {
		if c.Verdict == domain.SmokePass || c.Verdict == domain.SmokeFail || c.Verdict == domain.SmokeSkip {
			out = append(out, c)
		}
	}
	sortBySeq(out)
	return out
}

// jiraKeyFromIssueID extracts the bare Jira key from a canonical issue id, or
// reports false when the session is not Jira-bound.
func jiraKeyFromIssueID(issueID string) (string, bool) {
	if !strings.HasPrefix(issueID, jiraIssueIDPrefix) {
		return "", false
	}
	key := strings.TrimSpace(strings.TrimPrefix(issueID, jiraIssueIDPrefix))
	if key == "" {
		return "", false
	}
	return key, true
}

// evidenceDisplayName is the human name for an evidence file used when its
// upload fails: the recorded filename, else the on-disk fallback name.
func evidenceDisplayName(ev domain.SmokeEvidence) string {
	if n := strings.TrimSpace(ev.Filename); n != "" {
		return n
	}
	return evidenceFallbackName(ev)
}

func evidenceFallbackName(ev domain.SmokeEvidence) string {
	ext := ".bin"
	switch ev.Mime {
	case "image/png":
		ext = ".png"
	case "image/jpeg":
		ext = ".jpg"
	case "image/gif":
		ext = ".gif"
	case "image/webp":
		ext = ".webp"
	case "video/mp4":
		ext = ".mp4"
	case "video/webm":
		ext = ".webm"
	case "video/quicktime":
		ext = ".mov"
	}
	return ev.ID + ext
}

func sortBySeq(checks []domain.SmokeCheck) {
	for i := 1; i < len(checks); i++ {
		for j := i; j > 0 && checks[j-1].Seq > checks[j].Seq; j-- {
			checks[j-1], checks[j] = checks[j], checks[j-1]
		}
	}
}
