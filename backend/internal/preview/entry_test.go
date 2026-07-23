package preview

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func writeEntryFile(t *testing.T, path, contents string, mod time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	if !mod.IsZero() {
		if err := os.Chtimes(path, mod, mod); err != nil {
			t.Fatalf("chtimes %s: %v", path, err)
		}
	}
}

func TestDiscoverEntryPrefersIndexOverNewerFile(t *testing.T) {
	ws := t.TempDir()
	base := time.Now()
	writeEntryFile(t, filepath.Join(ws, "index.html"), "<main>app</main>", base)
	// A newer report must not win against the conventional index.html anchor.
	writeEntryFile(t, filepath.Join(ws, "report.html"), "<main>report</main>", base.Add(time.Hour))

	entry, ok := DiscoverEntry(ws)
	if !ok {
		t.Fatal("DiscoverEntry: ok=false, want entry")
	}
	if entry.Path != "index.html" {
		t.Fatalf("entry.Path = %q, want index.html", entry.Path)
	}
}

func TestDiscoverEntryFallsBackToMostRecentPreviewable(t *testing.T) {
	ws := t.TempDir()
	base := time.Now()
	writeEntryFile(t, filepath.Join(ws, "old.html"), "<main>old</main>", base)
	writeEntryFile(t, filepath.Join(ws, "docs", "notes.md"), "# notes", base.Add(30*time.Minute))
	writeEntryFile(t, filepath.Join(ws, "fresh.html"), "<main>fresh</main>", base.Add(time.Hour))

	entry, ok := DiscoverEntry(ws)
	if !ok {
		t.Fatal("DiscoverEntry: ok=false, want fallback entry")
	}
	if entry.Path != "fresh.html" {
		t.Fatalf("entry.Path = %q, want fresh.html", entry.Path)
	}
}

func TestDiscoverEntryFallsBackToMarkdown(t *testing.T) {
	ws := t.TempDir()
	writeEntryFile(t, filepath.Join(ws, "REPORT.md"), "# report", time.Now())

	entry, ok := DiscoverEntry(ws)
	if !ok {
		t.Fatal("DiscoverEntry: ok=false, want markdown fallback")
	}
	if entry.Path != "REPORT.md" {
		t.Fatalf("entry.Path = %q, want REPORT.md", entry.Path)
	}
}

func TestDiscoverEntrySkipsHiddenAndNodeModules(t *testing.T) {
	ws := t.TempDir()
	base := time.Now()
	// Newest files live in skipped dirs; the visible one must win.
	writeEntryFile(t, filepath.Join(ws, "node_modules", "pkg", "index.html"), "x", base.Add(time.Hour))
	writeEntryFile(t, filepath.Join(ws, ".cache", "cached.html"), "x", base.Add(2*time.Hour))
	writeEntryFile(t, filepath.Join(ws, "visible.html"), "ok", base)

	entry, ok := DiscoverEntry(ws)
	if !ok {
		t.Fatal("DiscoverEntry: ok=false, want visible entry")
	}
	if entry.Path != "visible.html" {
		t.Fatalf("entry.Path = %q, want visible.html", entry.Path)
	}
}

func TestDiscoverEntryTieBreaksOnPath(t *testing.T) {
	ws := t.TempDir()
	mod := time.Now()
	writeEntryFile(t, filepath.Join(ws, "b.html"), "b", mod)
	writeEntryFile(t, filepath.Join(ws, "a.html"), "a", mod)

	entry, ok := DiscoverEntry(ws)
	if !ok {
		t.Fatal("DiscoverEntry: ok=false, want tie-break entry")
	}
	if entry.Path != "a.html" {
		t.Fatalf("entry.Path = %q, want a.html (lexical tie-break)", entry.Path)
	}
}

func TestDiscoverEntryEmptyWorkspace(t *testing.T) {
	if _, ok := DiscoverEntry(t.TempDir()); ok {
		t.Fatal("DiscoverEntry: ok=true for empty workspace, want false")
	}
}

// The poller reveals the Browser tab unprompted, so it may only act on a
// conventional entrypoint — never on a "newest file wins" guess.
func TestDiscoverEntrypointFindsConventionalEntries(t *testing.T) {
	for _, candidate := range entryCandidates {
		t.Run(candidate, func(t *testing.T) {
			ws := t.TempDir()
			writeEntryFile(t, filepath.Join(ws, filepath.FromSlash(candidate)), "<main>app</main>", time.Now())

			entry, ok := DiscoverEntrypoint(ws)
			if !ok {
				t.Fatalf("DiscoverEntrypoint: ok=false, want %s", candidate)
			}
			if entry.Path != candidate {
				t.Fatalf("entry.Path = %q, want %q", entry.Path, candidate)
			}
		})
	}
}

func TestDiscoverEntrypointIgnoresLooseFiles(t *testing.T) {
	ws := t.TempDir()
	base := time.Now()
	// A worker's scratch notes and a stray report are not an entrypoint: revealing
	// either would steal the Browser tab nobody asked for.
	writeEntryFile(t, filepath.Join(ws, "PLAN.md"), "# plan", base.Add(time.Hour))
	writeEntryFile(t, filepath.Join(ws, "docs", "report.html"), "<main>report</main>", base)
	writeEntryFile(t, filepath.Join(ws, "frontend", "index.html"), "<main>vite</main>", base)

	if entry, ok := DiscoverEntrypoint(ws); ok {
		t.Fatalf("DiscoverEntrypoint: ok=true (%q), want no unprompted entry", entry.Path)
	}
}

func TestDiscoverEntrypointEmptyWorkspacePath(t *testing.T) {
	if _, ok := DiscoverEntrypoint("  "); ok {
		t.Fatal("DiscoverEntrypoint: ok=true for blank workspace path, want false")
	}
}

// The explicit `ao preview` fallback still walks, so it must be bounded: an AO
// worktree can carry tens of thousands of vendored files.
func TestScanPreviewableSkipsVendoredDirs(t *testing.T) {
	ws := t.TempDir()
	base := time.Now()
	for _, dir := range []string{"node_modules", "vendor", "Pods", "Carthage", "DerivedData", "__pycache__", "venv", "target"} {
		writeEntryFile(t, filepath.Join(ws, dir, "dep.html"), "x", base.Add(time.Hour))
	}
	writeEntryFile(t, filepath.Join(ws, "visible.html"), "ok", base)

	entry, ok := scanPreviewable(ws, defaultWalkBounds)
	if !ok {
		t.Fatal("scanPreviewable: ok=false, want visible entry")
	}
	if entry.Path != "visible.html" {
		t.Fatalf("entry.Path = %q, want visible.html", entry.Path)
	}
}

func TestScanPreviewableStopsAtMaxDepth(t *testing.T) {
	ws := t.TempDir()
	base := time.Now()
	deep := filepath.Join(ws, "a", "b", "c", "d")
	writeEntryFile(t, filepath.Join(deep, "deep.html"), "deep", base.Add(time.Hour))
	writeEntryFile(t, filepath.Join(ws, "shallow.html"), "shallow", base)

	entry, ok := scanPreviewable(ws, walkBounds{maxDepth: 2, maxEntries: 1000, maxCandidates: 1000})
	if !ok {
		t.Fatal("scanPreviewable: ok=false, want shallow entry")
	}
	if entry.Path != "shallow.html" {
		t.Fatalf("entry.Path = %q, want shallow.html (deeper file is past maxDepth)", entry.Path)
	}
}

func TestScanPreviewableStopsAtMaxEntries(t *testing.T) {
	ws := t.TempDir()
	base := time.Now()
	writeEntryFile(t, filepath.Join(ws, "a.html"), "first", base)
	for i := range 20 {
		writeEntryFile(t, filepath.Join(ws, "filler", "f"+strconv.Itoa(i)+".txt"), "x", base)
	}
	// Newest file, but the entry budget is spent long before the walk reaches it.
	writeEntryFile(t, filepath.Join(ws, "z.html"), "newest", base.Add(time.Hour))

	entry, ok := scanPreviewable(ws, walkBounds{maxDepth: 8, maxEntries: 3, maxCandidates: 1000})
	if !ok {
		t.Fatal("scanPreviewable: ok=false, want the entry found before the budget ran out")
	}
	if entry.Path != "a.html" {
		t.Fatalf("entry.Path = %q, want a.html (walk must stop at maxEntries)", entry.Path)
	}
}

func TestIsMarkdownPath(t *testing.T) {
	cases := map[string]bool{
		"a.md":       true,
		"A.MARKDOWN": true,
		"dir/x.md":   true,
		"index.html": false,
		"notes.txt":  false,
		"noext":      false,
	}
	for in, want := range cases {
		if got := IsMarkdownPath(in); got != want {
			t.Errorf("IsMarkdownPath(%q) = %v, want %v", in, got, want)
		}
	}
}
