package smoke

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	jiraadapter "github.com/aoagents/agent-orchestrator/backend/internal/adapters/jira"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// TestLive_PostToJiraInlineMedia posts a real ADF results comment (table + inline
// media) to a Jira issue, exercising the actual shipped path: AddAttachment →
// ResolveMediaID → buildResultsADF → AddComment. Gated behind AO_JIRA_LIVE=1 so it
// never runs in CI. Point it at a SCRATCH issue (e.g. project PTB), never a real
// ticket. After it runs, open the issue and confirm the screenshot previews
// inline (an image, not a link).
//
//	AO_JIRA_LIVE=1 AO_JIRA_LIVE_KEY=PTB-123 \
//	  go test -run TestLive_PostToJiraInlineMedia ./internal/service/smoke/ -v
//
// Optionally attach a real video too by pointing AO_JIRA_LIVE_VIDEO at an .mp4:
//
//	AO_JIRA_LIVE_VIDEO=/path/clip.mp4 (adds a second case whose evidence is the clip)
func TestLive_PostToJiraInlineMedia(t *testing.T) {
	if os.Getenv("AO_JIRA_LIVE") != "1" {
		t.Skip("set AO_JIRA_LIVE=1 to run the live Jira inline-media test")
	}
	key := os.Getenv("AO_JIRA_LIVE_KEY")
	if key == "" {
		t.Skip("set AO_JIRA_LIVE_KEY=<SCRATCH-KEY> (e.g. PTB-123) to pick the target issue")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	client := jiraadapter.NewClient()

	// A real screenshot when AO_JIRA_LIVE_IMAGE points at one, else a visibly
	// colored PNG so the preview is unmistakable when it renders.
	imgName, imgMime := "smoke-evidence.png", "image/png"
	var pngBytes []byte
	if imgPath := os.Getenv("AO_JIRA_LIVE_IMAGE"); imgPath != "" {
		b, err := os.ReadFile(imgPath)
		if err != nil {
			t.Fatalf("read image %s: %v", imgPath, err)
		}
		pngBytes = b
		imgName = filepath.Base(imgPath)
		imgMime = mimeByExt(imgPath)
	} else {
		pngBytes = makePNG(t, 240, 120, color.RGBA{R: 0x3b, G: 0x82, B: 0xf6, A: 0xff})
	}
	att, err := client.AddAttachment(ctx, key, imgName, imgMime, bytes.NewReader(pngBytes))
	if err != nil {
		t.Fatalf("AddAttachment: %v", err)
	}
	mediaID, err := client.ResolveMediaID(ctx, att.ID)
	if err != nil {
		t.Fatalf("ResolveMediaID(%s): %v", att.ID, err)
	}
	t.Logf("attachment id=%s -> media id=%s", att.ID, mediaID)

	run := []domain.SmokeCheck{{
		ID: "c1", Seq: 1, Name: "Inline image evidence previews",
		Why:      "Confirms the attachment renders as an image, not a download link",
		Steps:    []string{"Post the results comment", "Open the issue in the Jira web UI"},
		Expected: "The screenshot shows as an inline preview",
		Verdict:  domain.SmokePass,
		Note:     "posted by the AO live inline-media test",
	}}
	uploads := map[string][]uploadedEvidence{"c1": {{att: att, mediaID: mediaID}}}

	if videoPath := os.Getenv("AO_JIRA_LIVE_VIDEO"); videoPath != "" {
		vb, err := os.ReadFile(videoPath)
		if err != nil {
			t.Fatalf("read video %s: %v", videoPath, err)
		}
		vatt, err := client.AddAttachment(ctx, key, filepath.Base(videoPath), mimeByExt(videoPath), bytes.NewReader(vb))
		if err != nil {
			t.Fatalf("AddAttachment(video): %v", err)
		}
		vmedia, err := client.ResolveMediaID(ctx, vatt.ID)
		if err != nil {
			t.Fatalf("ResolveMediaID(video): %v", err)
		}
		t.Logf("video attachment id=%s -> media id=%s", vatt.ID, vmedia)
		run = append(run, domain.SmokeCheck{
			ID: "c2", Seq: 2, Name: "Inline video evidence previews",
			Why: "Confirms a clip renders as a video player", Verdict: domain.SmokeFail,
		})
		uploads["c2"] = []uploadedEvidence{{att: vatt, mediaID: vmedia}}
	}

	doc := buildResultsADF(run, uploads, true)
	comment, err := client.AddComment(ctx, key, doc)
	if err != nil {
		t.Fatalf("AddComment: %v", err)
	}
	t.Logf("POSTED comment on %s -> %s", key, comment.URL)
	t.Log("Now open the issue and confirm the image (and video, if attached) preview INLINE, not as links.")
}

func mimeByExt(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".mp4":
		return "video/mp4"
	case ".webm":
		return "video/webm"
	case ".mov":
		return "video/quicktime"
	default:
		return "image/png"
	}
}

func makePNG(t *testing.T, w, h int, c color.Color) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}
