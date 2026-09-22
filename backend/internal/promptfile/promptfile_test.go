package promptfile

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestWriteIsPrivateAndUnderTheSessionDir(t *testing.T) {
	dataDir := t.TempDir()
	path, err := Write(dataDir, "ao-7", SystemPrompt, "stand by")
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if want := filepath.Join(dataDir, "prompts", "ao-7", SystemPrompt); path != want {
		t.Fatalf("path = %q, want %q", path, want)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "stand by" {
		t.Fatalf("content = %q, %v", data, err)
	}
	if runtime.GOOS == "windows" {
		return // no POSIX permission bits to check
	}
	for p, want := range map[string]os.FileMode{path: 0o600, filepath.Dir(path): 0o700} {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != want {
			t.Errorf("%s mode = %o, want %o", p, got, want)
		}
	}
}

func TestWriteReplacesAndTightensAnOldDirectory(t *testing.T) {
	dataDir := t.TempDir()
	dir := Dir(dataDir, "ao-7")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Write(dataDir, "ao-7", SystemPrompt, "old"); err != nil {
		t.Fatal(err)
	}
	path, err := Write(dataDir, "ao-7", SystemPrompt, "new")
	if err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); string(data) != "new" {
		t.Fatalf("content = %q, want new", data)
	}
	if info, _ := os.Stat(dir); runtime.GOOS != "windows" && info.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode = %o, want 700", info.Mode().Perm())
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("dir holds %d entries, want only the prompt (no temp files)", len(entries))
	}
}

func TestWriteEmptyTextWritesNothing(t *testing.T) {
	dataDir := t.TempDir()
	path, err := Write(dataDir, "ao-7", SystemPrompt, "")
	if err != nil || path != "" {
		t.Fatalf("Write(empty) = %q, %v; want no path, no error", path, err)
	}
	if _, err := os.Stat(Root(dataDir)); !os.IsNotExist(err) {
		t.Fatalf("prompts dir created for an empty prompt: %v", err)
	}
}

func TestWriteRefusesIDsThatEscape(t *testing.T) {
	dataDir := t.TempDir()
	for _, id := range []domain.SessionID{"", ".", "..", "../x", "a/b"} {
		if _, err := Write(dataDir, id, SystemPrompt, "x"); err == nil {
			t.Errorf("Write(%q) succeeded, want refusal", id)
		}
		if err := Remove(dataDir, id); err == nil {
			t.Errorf("Remove(%q) succeeded, want refusal", id)
		}
	}
	if _, err := Write(dataDir, "ao-7", "../x", "x"); err == nil {
		t.Error("Write with a path-like name succeeded, want refusal")
	}
	if _, err := Write("", "ao-7", SystemPrompt, "x"); err == nil {
		t.Error("Write with no data dir succeeded, want refusal")
	}
}

func TestRemoveAndOwners(t *testing.T) {
	dataDir := t.TempDir()
	if owners, err := Owners(dataDir); err != nil || len(owners) != 0 {
		t.Fatalf("Owners on a fresh dir = %v, %v", owners, err)
	}
	for _, id := range []domain.SessionID{"ao-1", "ao-2"} {
		if _, err := Write(dataDir, id, SystemPrompt, "x"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Write(dataDir, "ao-1", ReviewerSystemPrompt, "y"); err != nil {
		t.Fatal(err)
	}
	if err := Remove(dataDir, "ao-1"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if err := Remove(dataDir, "ao-1"); err != nil {
		t.Fatalf("Remove of a gone session: %v", err)
	}
	owners, err := Owners(dataDir)
	if err != nil || len(owners) != 1 || owners[0] != "ao-2" {
		t.Fatalf("Owners = %v, %v; want [ao-2]", owners, err)
	}
}
