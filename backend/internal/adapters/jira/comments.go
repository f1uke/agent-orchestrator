package jira

// Attachment reads. An issue's attachments arrive with the issue (client.go
// decodes them); DownloadAttachment streams one back for inline display. Both go
// through the same Jira Cloud REST v3 auth seam as the other calls
// (transitions.go): base URL + login from env/jira-cli config, API token from
// AO_JIRA_TOKEN -> JIRA_API_TOKEN.

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// ErrBadRequest is a 400 from Jira, or a request AO refuses before sending it
// (an empty attachment id). The underlying sentinel lives in client.go with the
// others.
var ErrBadRequest = errBadRequest

// Attachment is one file attached to an issue, as Jira recorded it. ContentURL is
// the authenticated download link; ID is what DownloadAttachment takes.
type Attachment struct {
	ID         string
	Filename   string
	MimeType   string
	ContentURL string
}

// DownloadAttachment streams an attachment's bytes for inline display in the
// Summary tab (an image thumbnail / video player) and the shared lightbox. It
// GETs GET /rest/api/3/attachment/content/{id}, which 303-redirects to the media
// binary; the HTTP client follows the redirect and we hand back the body stream
// plus its Content-Type. The caller MUST close the returned reader.
func (c *Client) DownloadAttachment(ctx context.Context, attachmentID string) (io.ReadCloser, string, error) {
	attachmentID = strings.TrimSpace(attachmentID)
	if attachmentID == "" {
		return nil, "", fmt.Errorf("%w: empty attachment id", ErrBadRequest)
	}
	cfg, err := c.config()
	if err != nil {
		return nil, "", err
	}
	url := cfg.baseURL + "/rest/api/3/attachment/content/" + attachmentID
	req, err := newJiraRequest(ctx, cfg, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := c.transferDo(req)
	if err != nil {
		return nil, "", fmt.Errorf("%w: download attachment %s: %w", ErrUnavailable, attachmentID, err)
	}
	if resp.StatusCode >= 400 {
		defer func() { _ = resp.Body.Close() }()
		return nil, "", writeStatusError(resp, attachmentID)
	}
	return resp.Body, resp.Header.Get("Content-Type"), nil
}

// writeStatusError maps an attachment transfer's status onto a sentinel: 400 ->
// ErrBadRequest (distinct from a status move's ErrBadTransition), 401/403 -> auth,
// 404 -> not found, else unavailable. It surfaces Jira's error snippet like the
// read-path mappers.
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
