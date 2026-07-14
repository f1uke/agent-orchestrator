package smoke

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	jiraadapter "github.com/aoagents/agent-orchestrator/backend/internal/adapters/jira"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type attachCall struct {
	key, filename, mime, body string
}

type fakePoster struct {
	attachErr   error
	resolveErr  error   // when set, ResolveMediaID fails (→ link fallback)
	commentErr  []error // per-call error, indexed by AddComment call count
	attachments []attachCall
	comments    []map[string]any
	resolved    []string // attachment ids ResolveMediaID was called with
}

// ResolveMediaID mimics the real media-id lookup: a distinct id derived from the
// attachment id, so tests can assert the media node carries the RESOLVED id, not
// the attachment id (the whole point — the attachment id never previews).
func (p *fakePoster) ResolveMediaID(_ context.Context, attachmentID string) (string, error) {
	p.resolved = append(p.resolved, attachmentID)
	if p.resolveErr != nil {
		return "", p.resolveErr
	}
	return "media-" + attachmentID, nil
}

func (p *fakePoster) AddAttachment(_ context.Context, key, filename, mime string, r io.Reader) (jiraadapter.Attachment, error) {
	if p.attachErr != nil {
		return jiraadapter.Attachment{}, p.attachErr
	}
	b, _ := io.ReadAll(r)
	p.attachments = append(p.attachments, attachCall{key: key, filename: filename, mime: mime, body: string(b)})
	n := len(p.attachments)
	return jiraadapter.Attachment{
		ID:         fmt.Sprintf("att-%d", n),
		Filename:   filename,
		MimeType:   mime,
		ContentURL: fmt.Sprintf("https://acme.atlassian.net/secure/attachment/%d/%s", n, filename),
	}, nil
}

func (p *fakePoster) AddComment(_ context.Context, key string, body any) (jiraadapter.Comment, error) {
	idx := len(p.comments)
	doc, _ := body.(map[string]any)
	p.comments = append(p.comments, doc)
	if idx < len(p.commentErr) && p.commentErr[idx] != nil {
		return jiraadapter.Comment{}, p.commentErr[idx]
	}
	return jiraadapter.Comment{ID: "10101", URL: "https://acme.atlassian.net/browse/" + key + "?focusedCommentId=10101"}, nil
}

func newJiraTestService(t *testing.T, store Store, poster JiraPoster) *Service {
	t.Helper()
	return New(store, t.TempDir(), nil, WithClock(func() time.Time { return time.Unix(1_700_000_000, 0).UTC() }), WithJiraPoster(poster))
}

// seedRunCheck creates a check, attaches one evidence blob to disk, marks the
// verdict, and wires the evidence onto the fake row (the fake store does not join
// evidence the way the real store does).
func seedRunCheck(t *testing.T, svc *Service, store *fakeStore, id string, seq int, verdict domain.SmokeVerdict, note, mime, data string) {
	t.Helper()
	store.checks[id] = domain.SmokeCheck{
		ID: id, SessionID: "w1", Seq: seq, Name: "Check " + id,
		Why:      "why-" + id,
		Steps:    []string{"first " + id, "second " + id},
		Expected: "expected-" + id,
		FileRef:  "path/to/" + id + ".swift:42",
		PRNum:    7,
		Verdict:  domain.SmokePending, Evidence: []domain.SmokeEvidence{},
	}
	var evs []domain.SmokeEvidence
	if data != "" {
		ev, err := svc.AttachEvidence(context.Background(), "w1", id, EvidenceUpload{Filename: id + "-shot", Mime: mime, Reader: strings.NewReader(data)})
		if err != nil {
			t.Fatalf("attach evidence for %s: %v", id, err)
		}
		evs = []domain.SmokeEvidence{ev}
	}
	c := store.checks[id]
	c.Verdict, c.Note, c.Evidence = verdict, note, evs
	store.checks[id] = c
}

func TestPostToJira_OnlyRunRowsAndUploadsEvidence(t *testing.T) {
	store := newFakeStore()
	store.sessions["w1"] = domain.SessionRecord{ID: "w1", ProjectID: "proj", IssueID: "jira:DEMO-101"}
	poster := &fakePoster{}
	svc := newJiraTestService(t, store, poster)

	seedRunCheck(t, svc, store, "c1", 1, domain.SmokePass, "looked good", "image/png", "PNG")
	seedRunCheck(t, svc, store, "c2", 2, domain.SmokeFail, "flashed unknown", "video/mp4", "MP4")
	seedRunCheck(t, svc, store, "c3", 3, domain.SmokeSkip, "", "", "")
	// A pending row must be omitted from the table entirely.
	store.checks["c4"] = domain.SmokeCheck{ID: "c4", SessionID: "w1", Seq: 4, Name: "pending one", Verdict: domain.SmokePending, Evidence: []domain.SmokeEvidence{}}

	out, err := svc.PostToJira(context.Background(), "w1")
	if err != nil {
		t.Fatalf("PostToJira: %v", err)
	}
	if out.Key != "DEMO-101" || out.RowsPosted != 3 || out.AttachmentsUploaded != 2 {
		t.Fatalf("outcome = %+v, want key DEMO-101, 3 rows, 2 attachments", out)
	}
	if !out.EmbeddedMedia {
		t.Errorf("EmbeddedMedia = false, want true (an image was attached)")
	}
	if out.CommentURL == "" {
		t.Errorf("CommentURL empty")
	}
	if len(poster.comments) != 1 {
		t.Fatalf("AddComment calls = %d, want 1", len(poster.comments))
	}
	doc := mustJSON(t, poster.comments[0])
	// The results are laid out as a per-case table: one header row + one row per
	// run row (3), so 4 table rows in total.
	if !strings.Contains(doc, `"table"`) {
		t.Errorf("doc must contain a results table: %s", doc)
	}
	if got := strings.Count(doc, `"tableRow"`); got != 4 {
		t.Errorf("tableRow count = %d, want 4 (header + 3 run rows)", got)
	}
	// Each case title is present; the pending row is not.
	for _, name := range []string{"Check c1", "Check c2", "Check c3"} {
		if !strings.Contains(doc, name) {
			t.Errorf("doc missing case title %q", name)
		}
	}
	if strings.Contains(doc, "pending one") {
		t.Errorf("doc must not contain the pending row")
	}
	// The file/PR reference must NOT leak into the post.
	if strings.Contains(doc, "path/to/") || strings.Contains(doc, ".swift:42") || strings.Contains(doc, "PR #7") {
		t.Errorf("doc must not contain the file/PR reference: %s", doc)
	}
	// Authored-context columns + their cell content are present.
	for _, want := range []string{"Why this matters", "why-c1", "Steps", "first c1", "Expected result", "expected-c1"} {
		if !strings.Contains(doc, want) {
			t.Errorf("doc missing authored-context content %q", want)
		}
	}
	// Both the image AND the video embed inline as media, below the table, in an
	// Evidence section (Jira renders a preview or a video player).
	if !strings.Contains(doc, labelEvidence) {
		t.Errorf("doc missing the Evidence section: %s", doc)
	}
	if got := strings.Count(doc, `"mediaSingle"`); got != 2 {
		t.Errorf("mediaSingle count = %d, want 2 (image + video)", got)
	}
	// The media node references the RESOLVED media-services id, never the raw
	// attachment id (the attachment id renders only a link, not a preview).
	if !strings.Contains(doc, `"media-att-1"`) || !strings.Contains(doc, `"media-att-2"`) {
		t.Errorf("doc missing resolved media id in a media node: %s", doc)
	}
	if strings.Contains(doc, `"id":"att-1"`) || strings.Contains(doc, `"id":"att-2"`) {
		t.Errorf("media node must not use the raw attachment id: %s", doc)
	}
	if !strings.Contains(doc, `"collection":""`) {
		t.Errorf("media node missing the required empty collection attr: %s", doc)
	}
	// The rich comment embeds media, so the bare attachment link is not used here.
	if strings.Contains(doc, "/secure/attachment/") {
		t.Errorf("rich comment should embed media, not link attachments: %s", doc)
	}
	// ResolveMediaID was called for each uploaded attachment.
	if len(poster.resolved) != 2 {
		t.Errorf("ResolveMediaID calls = %d, want 2", len(poster.resolved))
	}
	// Status renders as Jira's native status lozenge (colored pill), one per row.
	if got := strings.Count(doc, `"type":"status"`); got != 3 {
		t.Errorf("status node count = %d, want 3", got)
	}
	for _, want := range []string{`"text":"PASS"`, `"color":"green"`, `"text":"FAIL"`, `"color":"red"`, `"text":"SKIP"`, `"color":"neutral"`} {
		if !strings.Contains(doc, want) {
			t.Errorf("doc missing status lozenge attr %q", want)
		}
	}
	// Attachments carried the right key + bytes (upload invoked within the post).
	if len(poster.attachments) != 2 || poster.attachments[0].key != "DEMO-101" || poster.attachments[0].body != "PNG" {
		t.Errorf("attachments = %+v", poster.attachments)
	}
}

func TestPostToJira_UploadFailureIsNonFatal(t *testing.T) {
	store := newFakeStore()
	store.sessions["w1"] = domain.SessionRecord{ID: "w1", ProjectID: "proj", IssueID: "jira:DEMO-101"}
	// A non-systemic per-file error (a 400 on one attachment) must not sink the
	// whole post — the comment is still posted, noting the failed file.
	poster := &fakePoster{attachErr: fmt.Errorf("%w: rejected", jiraadapter.ErrBadRequest)}
	svc := newJiraTestService(t, store, poster)
	seedRunCheck(t, svc, store, "c1", 1, domain.SmokePass, "ok", "image/png", "PNG")

	out, err := svc.PostToJira(context.Background(), "w1")
	if err != nil {
		t.Fatalf("PostToJira: %v", err)
	}
	if out.AttachmentsUploaded != 0 {
		t.Errorf("AttachmentsUploaded = %d, want 0 (upload failed)", out.AttachmentsUploaded)
	}
	if out.EmbeddedMedia {
		t.Errorf("EmbeddedMedia = true, want false (no attachment survived)")
	}
	if len(poster.comments) != 1 {
		t.Fatalf("AddComment calls = %d, want 1 (comment still posted)", len(poster.comments))
	}
	doc := mustJSON(t, poster.comments[0])
	if !strings.Contains(doc, "upload failed") {
		t.Errorf("doc missing the failed-upload note: %s", doc)
	}
	if strings.Contains(doc, `"mediaSingle"`) {
		t.Errorf("doc must not embed media for a failed upload")
	}
}

func TestPostToJira_MediaIDResolutionFailureFallsBackToLink(t *testing.T) {
	store := newFakeStore()
	store.sessions["w1"] = domain.SessionRecord{ID: "w1", ProjectID: "proj", IssueID: "jira:DEMO-101"}
	// Upload succeeds but the media id can't be resolved — the file must still
	// appear, as a link, and the comment must not claim embedded media.
	poster := &fakePoster{resolveErr: fmt.Errorf("%w: no media id", jiraadapter.ErrUnavailable)}
	svc := newJiraTestService(t, store, poster)
	seedRunCheck(t, svc, store, "c1", 1, domain.SmokePass, "ok", "image/png", "PNG")

	out, err := svc.PostToJira(context.Background(), "w1")
	if err != nil {
		t.Fatalf("PostToJira: %v", err)
	}
	if out.AttachmentsUploaded != 1 {
		t.Errorf("AttachmentsUploaded = %d, want 1 (upload still succeeded)", out.AttachmentsUploaded)
	}
	if out.EmbeddedMedia {
		t.Errorf("EmbeddedMedia = true, want false (no media id resolved)")
	}
	if len(poster.comments) != 1 {
		t.Fatalf("AddComment calls = %d, want 1 (posted media-free directly)", len(poster.comments))
	}
	doc := mustJSON(t, poster.comments[0])
	if strings.Contains(doc, `"mediaSingle"`) {
		t.Errorf("doc must not embed media when no media id resolved: %s", doc)
	}
	if !strings.Contains(doc, "/secure/attachment/1/") {
		t.Errorf("doc must link the attachment as a fallback: %s", doc)
	}
}

func TestPostToJira_NotLinked(t *testing.T) {
	store := newFakeStore()
	store.sessions["w1"] = domain.SessionRecord{ID: "w1", ProjectID: "proj", IssueID: "gl fix note render"}
	poster := &fakePoster{}
	svc := newJiraTestService(t, store, poster)
	store.checks["c1"] = domain.SmokeCheck{ID: "c1", SessionID: "w1", Seq: 1, Verdict: domain.SmokePass, Evidence: []domain.SmokeEvidence{}}

	if _, err := svc.PostToJira(context.Background(), "w1"); !errors.Is(err, ErrNotLinked) {
		t.Fatalf("err = %v, want ErrNotLinked", err)
	}
	if len(poster.comments) != 0 || len(poster.attachments) != 0 {
		t.Errorf("poster must not be called when unlinked")
	}
}

func TestPostToJira_NoRunRows(t *testing.T) {
	store := newFakeStore()
	store.sessions["w1"] = domain.SessionRecord{ID: "w1", ProjectID: "proj", IssueID: "jira:DEMO-101"}
	poster := &fakePoster{}
	svc := newJiraTestService(t, store, poster)
	store.checks["c1"] = domain.SmokeCheck{ID: "c1", SessionID: "w1", Seq: 1, Verdict: domain.SmokePending, Evidence: []domain.SmokeEvidence{}}

	if _, err := svc.PostToJira(context.Background(), "w1"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
	if len(poster.comments) != 0 {
		t.Errorf("no comment should be posted with zero run rows")
	}
}

func TestPostToJira_MissingPosterIsUnavailable(t *testing.T) {
	store := newFakeStore()
	store.sessions["w1"] = domain.SessionRecord{ID: "w1", ProjectID: "proj", IssueID: "jira:DEMO-101"}
	svc := New(store, t.TempDir(), nil) // no WithJiraPoster
	store.checks["c1"] = domain.SmokeCheck{ID: "c1", SessionID: "w1", Seq: 1, Verdict: domain.SmokePass, Evidence: []domain.SmokeEvidence{}}

	if _, err := svc.PostToJira(context.Background(), "w1"); !errors.Is(err, jiraadapter.ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
}

func TestPostToJira_MediaFallbackOn400(t *testing.T) {
	store := newFakeStore()
	store.sessions["w1"] = domain.SessionRecord{ID: "w1", ProjectID: "proj", IssueID: "jira:DEMO-101"}
	// First AddComment (with media) 400s; the retry (media-free) succeeds.
	poster := &fakePoster{commentErr: []error{jiraadapter.ErrBadRequest, nil}}
	svc := newJiraTestService(t, store, poster)
	seedRunCheck(t, svc, store, "c1", 1, domain.SmokePass, "ok", "image/png", "PNG")

	out, err := svc.PostToJira(context.Background(), "w1")
	if err != nil {
		t.Fatalf("PostToJira: %v", err)
	}
	if out.EmbeddedMedia {
		t.Errorf("EmbeddedMedia = true, want false after the 400 fallback")
	}
	if len(poster.comments) != 2 {
		t.Fatalf("AddComment calls = %d, want 2 (rich then media-free)", len(poster.comments))
	}
	if !strings.Contains(mustJSON(t, poster.comments[0]), `"mediaSingle"`) {
		t.Errorf("first attempt should include inline media")
	}
	if strings.Contains(mustJSON(t, poster.comments[1]), `"mediaSingle"`) {
		t.Errorf("fallback comment must not include inline media")
	}
	// The evidence link is still present in the fallback comment.
	if !strings.Contains(mustJSON(t, poster.comments[1]), "/secure/attachment/1/") {
		t.Errorf("fallback comment must still link the attachment")
	}
}

func TestPostToJira_AttachmentErrorSurfaces(t *testing.T) {
	store := newFakeStore()
	store.sessions["w1"] = domain.SessionRecord{ID: "w1", ProjectID: "proj", IssueID: "jira:DEMO-101"}
	poster := &fakePoster{attachErr: fmt.Errorf("%w: no write scope", jiraadapter.ErrAuthFailed)}
	svc := newJiraTestService(t, store, poster)
	seedRunCheck(t, svc, store, "c1", 1, domain.SmokePass, "ok", "image/png", "PNG")

	_, err := svc.PostToJira(context.Background(), "w1")
	if !errors.Is(err, jiraadapter.ErrAuthFailed) {
		t.Fatalf("err = %v, want ErrAuthFailed", err)
	}
	if len(poster.comments) != 0 {
		t.Errorf("no comment should be posted when an attachment upload fails")
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}
