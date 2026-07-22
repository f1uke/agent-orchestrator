package jira

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

// TestLive_RealREST exercises the real Jira Cloud REST v3 issue endpoint end to
// end (base URL + login + API token → GET /rest/api/3/issue/{key} → v3 parse).
// Gated behind AO_JIRA_LIVE=1 so it never runs in CI (no credential there).
// Requires the same auth as the app: AO_JIRA_URL/JIRA_SERVER, AO_JIRA_EMAIL/
// JIRA_LOGIN, and AO_JIRA_TOKEN/JIRA_API_TOKEN (or a jira-cli config file for the
// non-secret base URL + login). Supply a key from your own Jira — nothing is
// hardcoded. Run locally with:
//
//	AO_JIRA_LIVE=1 AO_JIRA_LIVE_KEY=<YOUR-KEY> go test -run TestLive_RealREST ./internal/adapters/jira/ -v
func TestLive_RealREST(t *testing.T) {
	if os.Getenv("AO_JIRA_LIVE") != "1" {
		t.Skip("set AO_JIRA_LIVE=1 to run the live Jira REST integration test")
	}
	key := os.Getenv("AO_JIRA_LIVE_KEY")
	if key == "" {
		t.Skip("set AO_JIRA_LIVE_KEY=<YOUR-KEY> to pick which issue to fetch")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	iss, err := NewClient().Get(ctx, key)
	if err != nil {
		t.Fatalf("live Get(%s): %v", key, err)
	}
	if iss.Key != key || iss.Title == "" || iss.Status == "" {
		t.Fatalf("live issue looks empty: %+v", iss)
	}
	t.Logf("LIVE %s: %q [%s/%s] desc-nodes=%d subtasks=%d sprint=%v url=%s",
		iss.Key, iss.Title, iss.Status, iss.StatusCategory, len(iss.Description), len(iss.Subtasks), iss.Sprint != nil, iss.URL)
}

// TestLive_DownloadAttachment exercises the real inline-media DOWNLOAD path end to
// end: GET /rest/api/3/issue/{key} → decode attachments → GET
// /rest/api/3/attachment/content/{id} → follow the 303 to media-services → stream
// bytes. This is the exact seam the Summary tab's inline previews use. Gated the
// same way as TestLive_RealREST; pick an issue whose description references an
// image attachment. Run locally with:
//
//	AO_JIRA_LIVE=1 AO_JIRA_LIVE_KEY=PROJ-2394 go test -run TestLive_DownloadAttachment ./internal/adapters/jira/ -v
func TestLive_DownloadAttachment(t *testing.T) {
	if os.Getenv("AO_JIRA_LIVE") != "1" {
		t.Skip("set AO_JIRA_LIVE=1 to run the live Jira attachment-download test")
	}
	key := os.Getenv("AO_JIRA_LIVE_KEY")
	if key == "" {
		t.Skip("set AO_JIRA_LIVE_KEY=<KEY-WITH-IMAGE> to pick the issue")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	client := NewClient()
	iss, err := client.Get(ctx, key)
	if err != nil {
		t.Fatalf("live Get(%s): %v", key, err)
	}
	t.Logf("LIVE %s has %d attachment(s)", key, len(iss.Attachments))
	if len(iss.Attachments) == 0 {
		t.Fatalf("issue %s has no attachments to download", key)
	}
	// Prefer an image attachment (what the Summary tab previews inline).
	target := iss.Attachments[0]
	for _, a := range iss.Attachments {
		if strings.HasPrefix(a.MimeType, "image/") {
			target = a
			break
		}
	}
	t.Logf("downloading attachment id=%s filename=%q mime=%s", target.ID, target.Filename, target.MimeType)
	rc, ctype, err := client.DownloadAttachment(ctx, target.ID)
	if err != nil {
		t.Fatalf("DownloadAttachment(%s): %v", target.ID, err)
	}
	defer func() { _ = rc.Close() }()
	body, err := io.ReadAll(io.LimitReader(rc, 8<<20))
	if err != nil {
		t.Fatalf("read attachment body: %v", err)
	}
	if len(body) < 64 {
		t.Fatalf("attachment body suspiciously small: %d bytes", len(body))
	}
	t.Logf("streamed %d bytes, content-type=%q", len(body), ctype)
	// Sanity: a PNG/JPEG/GIF magic header when the mime says image.
	if strings.HasPrefix(target.MimeType, "image/") {
		isPNG := len(body) >= 8 && body[0] == 0x89 && body[1] == 0x50
		isJPEG := len(body) >= 3 && body[0] == 0xFF && body[1] == 0xD8
		isGIF := len(body) >= 3 && body[0] == 'G' && body[1] == 'I' && body[2] == 'F'
		if !isPNG && !isJPEG && !isGIF {
			t.Fatalf("image bytes lack a known magic header: % x", body[:8])
		}
	}
}
