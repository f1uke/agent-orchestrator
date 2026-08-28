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

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/pngmeta"
)

// shotWithBuild is what `ao sim shot` writes: a real PNG carrying the build it
// saw. The file is the whole interface between the two - no flag, no sidecar -
// so the fixture has to be the real thing.
func shotWithBuild(t *testing.T, build string) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	img.Set(1, 1, color.RGBA{R: 9, G: 90, B: 200, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode: %v", err)
	}
	path := filepath.Join(t.TempDir(), "shot.png")
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	if build != "" {
		if err := pngmeta.Set(path, "ao-build", build); err != nil {
			t.Fatalf("annotate: %v", err)
		}
	}
	raw, err := os.ReadFile(path) //nolint:gosec // test fixture
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// The build reaches the evidence row without anybody passing it, on BOTH lanes:
// the agent recording a run, and the person dragging a screenshot into the
// Tests tab having been told nothing at all.
func TestAttachEvidence_TakesTheBuildFromThePicture(t *testing.T) {
	ctx := context.Background()
	svc, _ := seedPlayedCase(ctx, t)
	build := "com.example.MyApp 6.5.18 (708) cdhash:d114bed635ed2eb91f5c"

	for _, source := range []domain.SmokeEvidenceSource{domain.SmokeEvidenceUser, domain.SmokeEvidenceAgent} {
		ev, err := svc.AttachEvidence(ctx, "w1", "played", EvidenceUpload{
			Filename: "shot.png", Mime: "image/png",
			Reader: bytes.NewReader(shotWithBuild(t, build)),
			Source: source,
		})
		if err != nil {
			t.Fatalf("attach (%s): %v", source, err)
		}
		if ev.Build != build {
			t.Fatalf("source %s: build = %q, want %q", source, ev.Build, build)
		}
	}
}

// Two captures either side of a reinstall must stay distinguishable once they
// are evidence, or the whole fingerprint stops at the terminal.
func TestAttachEvidence_KeepsTwoBuildsApartOnOneCase(t *testing.T) {
	ctx := context.Background()
	svc, _ := seedPlayedCase(ctx, t)

	before, err := svc.AttachEvidence(ctx, "w1", "played", EvidenceUpload{
		Filename: "before.png", Mime: "image/png",
		Reader: bytes.NewReader(shotWithBuild(t, "com.example.MyApp 1.0 (1) sha256:aaaaaaaaaaaa")),
	})
	if err != nil {
		t.Fatal(err)
	}
	after, err := svc.AttachEvidence(ctx, "w1", "played", EvidenceUpload{
		Filename: "after.png", Mime: "image/png",
		Reader: bytes.NewReader(shotWithBuild(t, "com.example.MyApp 1.0 (1) sha256:bbbbbbbbbbbb")),
	})
	if err != nil {
		t.Fatal(err)
	}
	if before.Build == after.Build {
		t.Fatalf("two captures of different builds were filed as %q", before.Build)
	}
}

// Most evidence comes from somewhere else. An empty build is a legitimate "this
// capture could not say", never an error and never "the same as the one above".
func TestAttachEvidence_EvidenceFromElsewhereHasNoBuild(t *testing.T) {
	ctx := context.Background()
	svc, _ := seedPlayedCase(ctx, t)

	for name, upload := range map[string]EvidenceUpload{
		"a plain PNG": {Filename: "plain.png", Mime: "image/png", Reader: bytes.NewReader(shotWithBuild(t, ""))},
		"not a PNG":   {Filename: "clip.mp4", Mime: "video/mp4", Reader: strings.NewReader("not a png")},
	} {
		ev, err := svc.AttachEvidence(ctx, "w1", "played", upload)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if ev.Build != "" {
			t.Fatalf("%s: build = %q, want empty", name, ev.Build)
		}
	}
}
