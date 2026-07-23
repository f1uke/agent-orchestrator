package preview

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// buildWorkspaceTree lays out a workspace shaped like a real worktree: a wide,
// nested source tree with no conventional entrypoint at the root.
func buildWorkspaceTree(tb testing.TB, dirs, filesPerDir int) string {
	tb.Helper()
	root := tb.TempDir()
	for d := range dirs {
		dir := filepath.Join(root, "pkg"+strconv.Itoa(d), "internal", "sub")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			tb.Fatalf("mkdir %s: %v", dir, err)
		}
		for f := range filesPerDir {
			name := "src" + strconv.Itoa(f) + ".go"
			if f%10 == 0 {
				name = "notes" + strconv.Itoa(f) + ".md"
			}
			if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
				tb.Fatalf("write %s: %v", name, err)
			}
		}
	}
	return root
}

// BenchmarkDiscoverEntrypoint is the poller's per-session, per-tick cost. It is
// a fixed handful of stats and must not grow with the size of the workspace —
// walking the tree here is what pinned a CPU core across a fleet of sessions.
func BenchmarkDiscoverEntrypoint(b *testing.B) {
	ws := buildWorkspaceTree(b, 60, 25)
	b.ReportAllocs()
	for b.Loop() {
		DiscoverEntrypoint(ws)
	}
}

// BenchmarkDiscoverEntry is the explicit `ao preview` cost, paid once per user
// command rather than on a timer.
func BenchmarkDiscoverEntry(b *testing.B) {
	ws := buildWorkspaceTree(b, 60, 25)
	b.ReportAllocs()
	for b.Loop() {
		DiscoverEntry(ws)
	}
}
