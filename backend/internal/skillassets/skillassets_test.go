package skillassets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestInstall_WritesSkillAndIsIdempotent: Install must lay down the embedded
// skill (SKILL.md plus a commands file) under <dataDir>/skills/using-ao, and a
// second run must clobber cleanly, leaving no stale files. This is the whole
// contract the daemon boot hook relies on.
func TestInstall_WritesSkillAndIsIdempotent(t *testing.T) {
	dataDir := t.TempDir()

	if err := Install(dataDir); err != nil {
		t.Fatalf("Install: %v", err)
	}

	for _, hasWebUI := range []bool{false, true} {
		skillFile := filepath.Join(Dir(dataDir, hasWebUI), "SKILL.md")
		if b, err := os.ReadFile(skillFile); err != nil {
			t.Fatalf("read %s: %v", skillFile, err)
		} else if len(b) == 0 {
			t.Fatalf("%s is empty", skillFile)
		}
		if _, err := os.Stat(filepath.Join(Dir(dataDir, hasWebUI), "commands", "spawn.md")); err != nil {
			t.Fatalf("commands/spawn.md missing for hasWebUI=%v: %v", hasWebUI, err)
		}
	}

	// A stale file inside either skill dir must not survive a reinstall (clobber).
	for _, hasWebUI := range []bool{false, true} {
		stale := filepath.Join(Dir(dataDir, hasWebUI), "stale.md")
		if err := os.WriteFile(stale, []byte("old"), 0o644); err != nil {
			t.Fatalf("seed stale file: %v", err)
		}
		if err := Install(dataDir); err != nil {
			t.Fatalf("reinstall: %v", err)
		}
		if _, err := os.Stat(stale); !os.IsNotExist(err) {
			t.Fatalf("stale file survived reinstall for hasWebUI=%v (err=%v)", hasWebUI, err)
		}
	}
}

// TestInstall_NoWebUIVariantHasNoPreviewGuidance is the load-bearing assertion
// for the per-project web-UI toggle: a project with no web UI must never be
// handed `ao preview` guidance, because it is an instruction its agents cannot
// follow. Rather than naming the known injection points one by one (the next one
// added would slip through), this greps the WHOLE installed tree for the word.
func TestInstall_NoWebUIVariantHasNoPreviewGuidance(t *testing.T) {
	dataDir := t.TempDir()
	if err := Install(dataDir); err != nil {
		t.Fatalf("Install: %v", err)
	}

	root := Dir(dataDir, false)
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		b, readErr := os.ReadFile(path) //nolint:gosec // G304: path comes from walking a temp dir this test wrote
		if readErr != nil {
			return readErr
		}
		rel, _ := filepath.Rel(root, path)
		for i, line := range strings.Split(string(b), "\n") {
			if strings.Contains(strings.ToLower(line), "preview") {
				t.Errorf("no-web-UI skill still mentions preview at %s:%d: %s", rel, i+1, line)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}

	if _, err := os.Stat(filepath.Join(root, "commands", "preview.md")); !os.IsNotExist(err) {
		t.Errorf("commands/preview.md must not be installed for a project with no web UI (err=%v)", err)
	}
}

// TestInstall_WebUIVariantKeepsPreviewGuidance is the other half: a project that
// does have a web UI must still get the full catalog.
func TestInstall_WebUIVariantKeepsPreviewGuidance(t *testing.T) {
	dataDir := t.TempDir()
	if err := Install(dataDir); err != nil {
		t.Fatalf("Install: %v", err)
	}

	root := Dir(dataDir, true)
	if _, err := os.Stat(filepath.Join(root, "commands", "preview.md")); err != nil {
		t.Fatalf("commands/preview.md missing for a project with a web UI: %v", err)
	}
	for _, tc := range []struct{ file, want string }{
		{"SKILL.md", "commands/preview.md"},
		{"references.md", "ao preview"},
	} {
		b, err := os.ReadFile(filepath.Join(root, tc.file))
		if err != nil {
			t.Fatalf("read %s: %v", tc.file, err)
		}
		if !strings.Contains(string(b), tc.want) {
			t.Errorf("%s lost %q in the web-UI variant", tc.file, tc.want)
		}
	}
}

// TestInstall_MarkerNeverReachesDisk: the marker is a build-time annotation, not
// content. An agent reading either installed tree must never see it.
func TestInstall_MarkerNeverReachesDisk(t *testing.T) {
	dataDir := t.TempDir()
	if err := Install(dataDir); err != nil {
		t.Fatalf("Install: %v", err)
	}
	for _, hasWebUI := range []bool{false, true} {
		root := Dir(dataDir, hasWebUI)
		err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return err
			}
			b, readErr := os.ReadFile(path) //nolint:gosec // G304: path comes from walking a temp dir this test wrote
			if readErr != nil {
				return readErr
			}
			if strings.Contains(string(b), webUIMarker) {
				rel, _ := filepath.Rel(root, path)
				t.Errorf("marker leaked into installed file %s (hasWebUI=%v)", rel, hasWebUI)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", root, err)
		}
	}
}

// TestInstall_VariantsShareEverythingElse guards against the two trees drifting:
// only preview-marked content may differ, so a command file that has nothing to
// do with the web UI must be byte-identical in both.
func TestInstall_VariantsShareEverythingElse(t *testing.T) {
	dataDir := t.TempDir()
	if err := Install(dataDir); err != nil {
		t.Fatalf("Install: %v", err)
	}
	for _, name := range []string{"commands/spawn.md", "commands/session.md", "commands/project.md"} {
		base, err := os.ReadFile(filepath.Join(Dir(dataDir, false), filepath.FromSlash(name)))
		if err != nil {
			t.Fatalf("read base %s: %v", name, err)
		}
		web, err := os.ReadFile(filepath.Join(Dir(dataDir, true), filepath.FromSlash(name)))
		if err != nil {
			t.Fatalf("read web %s: %v", name, err)
		}
		if string(base) != string(web) {
			t.Errorf("%s differs between the two variants but has nothing to do with the web UI", name)
		}
	}
}
