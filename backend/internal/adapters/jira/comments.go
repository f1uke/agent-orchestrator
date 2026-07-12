package jira

// Comment + attachment writes — the SECOND sanctioned Jira write (after the
// status move). The Tests tab posts a session's smoke-test results as an ADF
// table comment on the linked issue, with each screenshot/clip uploaded as an
// attachment and referenced inline. Both go through the same Jira Cloud REST v3
// auth seam as the other calls (transitions.go): base URL + login from
// env/jira-cli config, API token from AO_JIRA_TOKEN → JIRA_API_TOKEN. Unlike the
// read paths, this needs the token to have WRITE scope; a rejected credential
// surfaces as ErrAuthFailed for the UI to show inline.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
)

// ErrBadRequest is a 400 from a comment/attachment write — usually a malformed
// ADF body (e.g. an inline media node the instance can't resolve). The smoke
// service uses it to retry a comment without the inline media. The underlying
// sentinel lives in client.go with the others.
var ErrBadRequest = errBadRequest

// Attachment is one uploaded evidence file as Jira recorded it. ContentURL is the
// authenticated download link the comment references inline; ID is the attachment
// id an ADF media node embeds.
type Attachment struct {
	ID         string
	Filename   string
	MimeType   string
	ContentURL string
}

// Comment is a posted issue comment. URL deep-links to it on the issue so the UI
// can open exactly the comment it created.
type Comment struct {
	ID  string
	URL string
}

// AddAttachment uploads one file to the issue via
// POST /rest/api/3/issue/{key}/attachments as multipart/form-data (field "file"),
// with the mandatory X-Atlassian-Token: no-check header Jira requires for a
// programmatic upload. Returns the attachment metadata (id + content URL) the
// caller references in the comment.
func (c *Client) AddAttachment(ctx context.Context, key, filename, mimeType string, r io.Reader) (Attachment, error) {
	key = strings.TrimSpace(key)
	if !keyPattern.MatchString(key) {
		return Attachment{}, fmt.Errorf("%w: %q", ErrBadKey, key)
	}
	cfg, err := c.config()
	if err != nil {
		return Attachment{}, err
	}
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreatePart(attachmentPartHeader(filename, mimeType))
	if err != nil {
		return Attachment{}, fmt.Errorf("%w: build attachment part: %w", ErrUnavailable, err)
	}
	if _, err := io.Copy(part, r); err != nil {
		return Attachment{}, fmt.Errorf("%w: read attachment %q: %w", ErrUnavailable, filename, err)
	}
	if err := mw.Close(); err != nil {
		return Attachment{}, fmt.Errorf("%w: finalize attachment: %w", ErrUnavailable, err)
	}
	url := cfg.baseURL + "/rest/api/3/issue/" + key + "/attachments"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &buf)
	if err != nil {
		return Attachment{}, fmt.Errorf("%w: build request: %w", ErrUnavailable, err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("X-Atlassian-Token", "no-check")
	req.Header.Set("Authorization", basicAuth(cfg))
	resp, err := c.httpDo(req)
	if err != nil {
		return Attachment{}, fmt.Errorf("%w: upload attachment %s: %w", ErrUnavailable, key, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := writeStatusError(resp, key); err != nil {
		return Attachment{}, err
	}
	// The endpoint returns an array — one element per uploaded file (we send one).
	var payload []struct {
		ID       string `json:"id"`
		Filename string `json:"filename"`
		MimeType string `json:"mimeType"`
		Content  string `json:"content"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return Attachment{}, fmt.Errorf("%w: decode attachment %s: %w", ErrUnavailable, key, err)
	}
	if len(payload) == 0 {
		return Attachment{}, fmt.Errorf("%w: attachment upload for %s returned no metadata", ErrUnavailable, key)
	}
	a := payload[0]
	return Attachment{ID: a.ID, Filename: a.Filename, MimeType: a.MimeType, ContentURL: a.Content}, nil
}

// AddComment posts an ADF comment to the issue via
// POST /rest/api/3/issue/{key}/comment. body is the ADF document node, marshaled
// under "body". Returns the created comment plus a deep link to it.
func (c *Client) AddComment(ctx context.Context, key string, body any) (Comment, error) {
	key = strings.TrimSpace(key)
	if !keyPattern.MatchString(key) {
		return Comment{}, fmt.Errorf("%w: %q", ErrBadKey, key)
	}
	cfg, err := c.config()
	if err != nil {
		return Comment{}, err
	}
	payload, err := json.Marshal(map[string]any{"body": body})
	if err != nil {
		return Comment{}, fmt.Errorf("%w: encode comment %s: %w", ErrUnavailable, key, err)
	}
	url := cfg.baseURL + "/rest/api/3/issue/" + key + "/comment"
	req, err := newJiraRequest(ctx, cfg, http.MethodPost, url, payload)
	if err != nil {
		return Comment{}, err
	}
	resp, err := c.httpDo(req)
	if err != nil {
		return Comment{}, fmt.Errorf("%w: post comment %s: %w", ErrUnavailable, key, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := writeStatusError(resp, key); err != nil {
		return Comment{}, err
	}
	var out struct {
		ID   string `json:"id"`
		Self string `json:"self"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return Comment{}, fmt.Errorf("%w: decode comment %s: %w", ErrUnavailable, key, err)
	}
	return Comment{ID: out.ID, URL: commentURL(out.Self, key, out.ID)}, nil
}

// writeStatusError maps a comment/attachment write status onto a sentinel. A 400
// is ErrBadRequest (distinct from a status move's ErrBadTransition) so the caller
// can retry a comment without inline media; 401/403 → auth, 404 → not found, else
// unavailable. It surfaces Jira's error snippet like the read-path mappers.
func writeStatusError(resp *http.Response, key string) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	snippet := errorSnippet(resp.Body)
	switch resp.StatusCode {
	case http.StatusBadRequest:
		return fmt.Errorf("%w: %s%s", ErrBadRequest, key, suffix(snippet))
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("%w: %s%s", ErrAuthFailed, key, suffix(snippet))
	case http.StatusNotFound:
		return fmt.Errorf("%w: %s", ErrNotFound, key)
	default:
		return fmt.Errorf("%w: %s: HTTP %d%s", ErrUnavailable, key, resp.StatusCode, suffix(snippet))
	}
}

// attachmentPartHeader builds the multipart "file" part header carrying the
// display filename and declared content type (Jira reads both). The filename is
// sanitized so it can't break the header.
func attachmentPartHeader(filename, mimeType string) textproto.MIMEHeader {
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename=%q`, sanitizePartFilename(filename)))
	if strings.TrimSpace(mimeType) != "" {
		h.Set("Content-Type", mimeType)
	}
	return h
}

func sanitizePartFilename(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "evidence"
	}
	return strings.Map(func(r rune) rune {
		if r == '"' || r == '\\' || r == '\n' || r == '\r' {
			return '_'
		}
		return r
	}, name)
}

// commentURL deep-links to a posted comment
// ({base}/browse/{KEY}?focusedCommentId={id}), reusing browseURL to derive the
// site base from the comment's self link. Falls back to the plain browse URL (or
// "") when self has no recognizable host.
func commentURL(self, key, id string) string {
	base := browseURL(self, key)
	if base == "" || id == "" {
		return base
	}
	return base + "?focusedCommentId=" + id
}
