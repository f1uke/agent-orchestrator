package apply

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

var now = time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)

func setup(t *testing.T) (Roots, string) {
	t.Helper()
	home := t.TempDir()
	mem := filepath.Join(home, ".claude", "projects", "-repo", "memory")
	if err := os.MkdirAll(mem, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mem, "MEMORY.md"), []byte("- [Old](feedback_old.md) - old"), 0o644); err != nil {
		t.Fatal(err)
	}
	return Roots{Home: home, History: filepath.Join(t.TempDir(), "history")}, mem
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestCreateMemory_WritesTheFileAndItsIndexLineOnce(t *testing.T) {
	r, mem := setup(t)
	p := domain.LearnProposal{Action: domain.LearnProposeCreateMemory, TargetPath: filepath.Join(mem, "feedback_new.md"),
		NewContent: "---\nname: feedback-new\n---\n\nBody.\n", IndexLine: "- [New](feedback_new.md) - new"}
	hash, err := Apply(r, p, "", now)
	if err != nil || hash == "" {
		t.Fatalf("apply: %v", err)
	}
	if read(t, p.TargetPath) != p.NewContent {
		t.Error("the memory file must be the proposed content")
	}
	if got := read(t, filepath.Join(mem, "MEMORY.md")); got != "- [Old](feedback_old.md) - old\n- [New](feedback_new.md) - new\n" {
		t.Errorf("MEMORY.md = %q", got)
	}
	if entries, _ := os.ReadDir(r.History); len(entries) != 1 {
		t.Errorf("MEMORY.md's previous version must be backed up, got %d entries", len(entries))
	}
	if _, err := Apply(r, p, "", now); !errors.Is(err, ErrStale) {
		t.Errorf("an existing memory file is never overwritten: %v", err)
	}
	leftovers, _ := filepath.Glob(filepath.Join(mem, ".*learn-*"))
	if len(leftovers) != 0 {
		t.Errorf("temp files left behind: %v", leftovers)
	}
}

func TestUpdate_GoesStaleWhenTheFileChangedAndBacksUpOtherwise(t *testing.T) {
	r, mem := setup(t)
	target := filepath.Join(mem, "feedback_old.md")
	if err := os.WriteFile(target, []byte("v1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := domain.LearnProposal{Action: domain.LearnProposeUpdateMemory, TargetPath: target, BaseSHA256: sha([]byte("v0\n")), NewContent: "v2\n"}
	if _, err := Apply(r, p, "", now); !errors.Is(err, ErrStale) {
		t.Fatalf("a changed file must not be overwritten: %v", err)
	}
	p.BaseSHA256 = sha([]byte("v1\n"))
	if _, err := Apply(r, p, "v2 edited\n", now); err != nil {
		t.Fatal(err)
	}
	if read(t, target) != "v2 edited\n" {
		t.Error("the person's edit is what is written")
	}
	if st, _ := os.Stat(target); st.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want the file's own 0600 kept", st.Mode().Perm())
	}
	var backups []string
	_ = filepath.Walk(r.History, func(p string, info os.FileInfo, _ error) error {
		if info != nil && !info.IsDir() {
			backups = append(backups, read(t, p))
		}
		return nil
	})
	if len(backups) != 1 || backups[0] != "v1\n" {
		t.Errorf("backups = %q", backups)
	}
}

func TestAllowed_OnlyMemorySkillsAndClaudeMD(t *testing.T) {
	r, mem := setup(t)
	outside := t.TempDir()
	link := filepath.Join(r.Home, ".claude", "skills", "linked")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	for target, ok := range map[string]bool{
		filepath.Join(r.Home, ".claude", "CLAUDE.md"):                     true,
		filepath.Join(mem, "feedback_x.md"):                               true,
		filepath.Join(r.Home, ".claude", "skills", "release", "SKILL.md"): true,
		filepath.Join(mem, "MEMORY.md"):                                   false,
		filepath.Join(r.Home, ".claude", "settings.json"):                 false,
		filepath.Join(r.Home, ".bashrc"):                                  false,
		filepath.Join(link, "SKILL.md"):                                   false,
		"relative/CLAUDE.md":                                              false,
	} {
		if err := r.allowed(target); (err == nil) != ok {
			t.Errorf("%s: allowed = %v, want %v", strings.TrimPrefix(target, r.Home), err == nil, ok)
		}
	}
}
