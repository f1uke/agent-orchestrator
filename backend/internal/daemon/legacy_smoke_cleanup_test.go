package daemon

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
)

func runLegacySmokeCleanup(t *testing.T, dataDir string) string {
	t.Helper()
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	<-startLegacySmokeCleanup(config.Config{DataDir: dataDir}, log)
	return buf.String()
}

func seedSmokeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func assertGone(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("%s still exists (err=%v)", path, err)
	}
}

func TestLegacySmokeCleanupRemovesEvidenceAndRetentionSettings(t *testing.T) {
	dataDir := t.TempDir()
	evidence := filepath.Join(dataDir, "evidence")
	settings := filepath.Join(dataDir, "evidence-retention-settings.json")
	seedSmokeFile(t, filepath.Join(evidence, "sess-1", "case-a", "shot.png"), "png")
	seedSmokeFile(t, filepath.Join(evidence, "sess-2", "case-b", "clip.mp4"), "mp4")
	seedSmokeFile(t, settings, `{"retentionDays":30}`)
	keep := filepath.Join(dataDir, "ao.db")
	seedSmokeFile(t, keep, "db")

	out := runLegacySmokeCleanup(t, dataDir)

	assertGone(t, evidence)
	assertGone(t, settings)
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("cleanup touched an unrelated file: %v", err)
	}
	if !strings.Contains(out, "smoke cleanup:") {
		t.Fatalf("log has no smoke cleanup line:\n%s", out)
	}

	second := runLegacySmokeCleanup(t, dataDir)
	if strings.Contains(second, "level=WARN") || strings.Contains(second, "level=ERROR") {
		t.Fatalf("second run should be a quiet no-op, logged:\n%s", second)
	}
	if strings.Contains(second, "removed") {
		t.Fatalf("second run claims it removed something:\n%s", second)
	}
}

func TestLegacySmokeCleanupRefusesEvidenceSymlinkOutsideDataDir(t *testing.T) {
	dataDir := t.TempDir()
	outside := t.TempDir()
	target := filepath.Join(outside, "precious.txt")
	seedSmokeFile(t, target, "keep me")
	link := filepath.Join(dataDir, "evidence")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}

	out := runLegacySmokeCleanup(t, dataDir)

	if _, err := os.Stat(target); err != nil {
		t.Fatalf("target outside dataDir was touched: %v", err)
	}
	if _, err := os.Lstat(link); err != nil {
		t.Fatalf("refused symlink should stay in place: %v", err)
	}
	if !strings.Contains(out, "smoke cleanup:") || !strings.Contains(out, "outside") {
		t.Fatalf("refusal not logged:\n%s", out)
	}
}

func TestLegacySmokeCleanupSkipsWithoutDataDir(t *testing.T) {
	if out := runLegacySmokeCleanup(t, ""); out != "" {
		t.Fatalf("no dataDir should do nothing, logged:\n%s", out)
	}
}
