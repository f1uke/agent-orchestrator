// Package skillassets embeds the using-ao skill (the ao CLI catalog) and
// installs it into the AO data dir at daemon boot. Worker sessions run in a
// worktree of whatever project they were spawned in, so a repo-relative
// skills/ path only resolves when that project happens to be the AO repo
// itself. Installing under the data dir gives every session, in any project, a
// stable absolute path to read.
//
// The embedded copy is the single source of truth. Install clobbers the
// on-disk copy on every boot, so a new daemon build always refreshes it and the
// two can never drift; there is no version marker or hash to keep in sync
// because the daemon binary already is the version.
//
// # Two variants, one source
//
// Whether a project has a web UI is per-project (domain.ProjectConfig.HasWebUI),
// but the skill installs once per data dir — so the catalog cannot be filtered
// at read time. Install therefore writes BOTH variants and Manager points each
// project's prompt at the right one:
//
//   - using-ao      — the default. No `ao preview`: a project with no web UI has
//     nothing to preview, and an instruction its agents cannot follow is worse
//     than no instruction.
//   - using-ao-web  — the full catalog, for projects that do render in a browser.
//
// Web-UI-only content is annotated at the source rather than duplicated. A
// whole file is listed in webUIOnlyFiles; a single line carries webUIMarker,
// an HTML comment that renders as nothing and is stripped from the web variant
// so it never reaches an agent either way.
package skillassets

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"embed"
)

//go:embed using-ao
var files embed.FS

// SkillName is the default skill's directory name under <dataDir>/skills — the
// catalog a project with no web UI gets.
const SkillName = "using-ao"

// WebSkillName is the directory name of the variant for projects that do have a
// web UI: the same catalog plus the `ao preview` entries.
const WebSkillName = "using-ao-web"

// webUIMarker annotates a source line that only applies to a project with a web
// UI. It is an HTML comment so it renders as nothing, and Install strips it from
// the web variant, so no installed file ever contains it.
const webUIMarker = "<!-- web-ui -->"

// webUIOnlyFiles are embedded files installed only for projects with a web UI,
// keyed by their embed path. A whole file is listed here when marking it line by
// line would mean marking every line.
var webUIOnlyFiles = map[string]bool{
	SkillName + "/commands/preview.md": true,
}

// Dir returns the absolute directory the skill installs into for a given data
// dir and project. Callers building prompts use this so the path they cite
// always matches where Install writes.
func Dir(dataDir string, hasWebUI bool) string {
	name := SkillName
	if hasWebUI {
		name = WebSkillName
	}
	return filepath.Join(dataDir, "skills", name)
}

// Install writes both skill variants under <dataDir>/skills, replacing any
// existing copies. It runs once at daemon boot, before any session spawns, so a
// plain clobber-and-write needs no locking: there are no concurrent readers yet.
// A failure is returned but is non-fatal to boot (the skill enhances
// `ao --help`, it is not load-bearing).
func Install(dataDir string) error {
	for _, hasWebUI := range []bool{false, true} {
		if err := installVariant(dataDir, hasWebUI); err != nil {
			return err
		}
	}
	return nil
}

func installVariant(dataDir string, hasWebUI bool) error {
	dest := Dir(dataDir, hasWebUI)
	if err := os.RemoveAll(dest); err != nil {
		return fmt.Errorf("clear skill dir %q: %w", dest, err)
	}
	// embed.FS always uses forward-slash paths rooted at "using-ao"; map each
	// onto this variant's dest dir with the platform separator.
	return fs.WalkDir(files, SkillName, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !hasWebUI && webUIOnlyFiles[p] {
			return nil
		}
		target := filepath.Join(dest, filepath.FromSlash(strings.TrimPrefix(p, SkillName)))
		if d.IsDir() {
			return os.MkdirAll(target, 0o750)
		}
		b, err := files.ReadFile(p)
		if err != nil {
			return fmt.Errorf("read embedded %q: %w", p, err)
		}
		if err := os.WriteFile(target, applyWebUIMarkers(b, hasWebUI), 0o600); err != nil {
			return fmt.Errorf("write %q: %w", target, err)
		}
		return nil
	})
}

// applyWebUIMarkers resolves webUIMarker for one variant: a marked line is
// dropped entirely when the project has no web UI, and keeps its content with
// the marker itself removed when it does.
func applyWebUIMarkers(content []byte, hasWebUI bool) []byte {
	if !strings.Contains(string(content), webUIMarker) {
		return content
	}
	lines := strings.Split(string(content), "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		if !strings.Contains(line, webUIMarker) {
			kept = append(kept, line)
			continue
		}
		if hasWebUI {
			kept = append(kept, strings.TrimRight(strings.ReplaceAll(line, webUIMarker, ""), " \t"))
		}
	}
	return []byte(strings.Join(kept, "\n"))
}
