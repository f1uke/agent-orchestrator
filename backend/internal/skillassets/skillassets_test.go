package skillassets

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"unicode"
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
// only web-UI-marked content may differ, so every embedded file that carries no
// marker and is not web-only must be byte-identical in both.
func TestInstall_VariantsShareEverythingElse(t *testing.T) {
	dataDir := t.TempDir()
	if err := Install(dataDir); err != nil {
		t.Fatalf("Install: %v", err)
	}
	err := fs.WalkDir(files, SkillName, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || webUIOnlyFiles[p] {
			return err
		}
		src, err := files.ReadFile(p)
		if err != nil {
			return err
		}
		if strings.Contains(string(src), webUIMarker) {
			return nil
		}
		name := filepath.FromSlash(strings.TrimPrefix(p, SkillName+"/"))
		base, err := os.ReadFile(filepath.Join(Dir(dataDir, false), name))
		if err != nil {
			return err
		}
		web, err := os.ReadFile(filepath.Join(Dir(dataDir, true), name))
		if err != nil {
			return err
		}
		if string(base) != string(web) {
			t.Errorf("%s differs between the two variants but has nothing to do with the web UI", name)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

var mdLink = regexp.MustCompile(`\]\(([^)#\s]+\.md)?(#[^)\s]+)?\)`)

// installedPages returns every installed .md file of one variant, keyed by its
// slash path relative to the skill dir.
func installedPages(t *testing.T, hasWebUI bool) map[string]string {
	t.Helper()
	dataDir := t.TempDir()
	if err := Install(dataDir); err != nil {
		t.Fatalf("Install: %v", err)
	}
	root := Dir(dataDir, hasWebUI)
	pages := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".md") {
			return err
		}
		b, err := os.ReadFile(p)
		rel, _ := filepath.Rel(root, p)
		pages[filepath.ToSlash(rel)] = string(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return pages
}

// headingAnchors lists the GitHub-style anchors of a page's headings.
func headingAnchors(page string) map[string]bool {
	anchors := map[string]bool{}
	for _, line := range strings.Split(page, "\n") {
		if !strings.HasPrefix(line, "#") {
			continue
		}
		text := strings.ToLower(strings.TrimSpace(strings.TrimLeft(line, "#")))
		var b strings.Builder
		for _, r := range text {
			switch {
			case r == ' ':
				b.WriteRune('-')
			case r == '-' || r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r):
				b.WriteRune(r)
			}
		}
		anchors[b.String()] = true
	}
	return anchors
}

// The skill follows Anthropic's skill-authoring guidance, because an agent reads
// it the way that guidance assumes: SKILL.md is the only page it is pointed at,
// so every other page must be one link away from it (a page two links deep gets
// read partially, or not at all); a link that resolves nowhere is a dead end
// mid-task; and a page over 100 lines needs a table of contents, so a partial
// read still shows what the page holds.
func TestInstall_SkillPagesAreReachableAndNavigable(t *testing.T) {
	for _, hasWebUI := range []bool{false, true} {
		pages := installedPages(t, hasWebUI)
		linkedFromSkill := map[string]bool{}
		for _, m := range mdLink.FindAllStringSubmatch(pages["SKILL.md"], -1) {
			linkedFromSkill[m[1]] = true
		}
		for name, page := range pages {
			if name != "SKILL.md" && !linkedFromSkill[name] {
				t.Errorf("web=%v: %s is not linked from SKILL.md", hasWebUI, name)
			}
			if lines := strings.Count(page, "\n"); lines > 100 && !strings.Contains(page, "\n## Contents\n") {
				t.Errorf("web=%v: %s is %d lines and has no ## Contents", hasWebUI, name, lines)
			}
			for _, m := range mdLink.FindAllStringSubmatch(page, -1) {
				target := name
				if m[1] != "" {
					target = path.Join(path.Dir(name), m[1])
				}
				dest, ok := pages[target]
				if !ok {
					t.Errorf("web=%v: %s links %s, which does not exist", hasWebUI, name, m[0])
					continue
				}
				if m[2] != "" && !headingAnchors(dest)[strings.TrimPrefix(m[2], "#")] {
					t.Errorf("web=%v: %s links %s, which has no such heading", hasWebUI, name, m[0])
				}
			}
		}
	}
}
