package preview

import (
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

var entryCandidates = []string{"index.html", "public/index.html", "dist/index.html", "build/index.html"}

// previewableExts are the file extensions the browser panel can render: HTML
// verbatim and Markdown converted to HTML by the preview/files route.
var previewableExts = map[string]struct{}{
	".html":     {},
	".htm":      {},
	".md":       {},
	".markdown": {},
}

// vendoredPreviewDirs are dependency and build caches that never hold a document
// an agent meant to show. They dominate a real worktree (an iOS checkout carries
// tens of thousands of such files), so pruning them keeps the fallback scan
// affordable. Hidden directories (.git, .build, .venv, …) are pruned as well.
var vendoredPreviewDirs = map[string]struct{}{
	"node_modules": {},
	"vendor":       {},
	"Pods":         {},
	"Carthage":     {},
	"DerivedData":  {},
	"__pycache__":  {},
	"venv":         {},
	"target":       {},
}

// walkBounds caps the fallback scan so a pathological workspace cannot stall the
// request that asked for it.
type walkBounds struct {
	// maxDepth is how many directory levels below the workspace root the scan
	// descends. A preview document an agent wants shown is not buried deeper.
	maxDepth int
	// maxEntries caps total directory entries visited, not just matches, so a
	// huge tree of non-previewable files is bounded too.
	maxEntries int
	// maxCandidates caps how many previewable files are compared.
	maxCandidates int
}

var defaultWalkBounds = walkBounds{maxDepth: 8, maxEntries: 50000, maxCandidates: 5000}

// Entry is a workspace-local static frontend entrypoint.
type Entry struct {
	Path    string
	AbsPath string
	ModTime time.Time
	Size    int64
}

// DiscoverEntrypoint returns the workspace's conventional static entrypoint —
// index.html or its public/dist/build variants — and nothing else. It costs a
// handful of stats and never walks the tree.
//
// This is the discovery the preview poller runs, because the poller reveals the
// browser panel *unprompted*. An unprompted reveal must fire on a fact ("this
// workspace has a servable frontend"), never on a heuristic ("this file was
// touched most recently"): a heuristic steals the tab the moment an agent writes
// its own scratch notes, and re-running it across every session on a ticker is
// what pinned a CPU core. Everything looser lives behind an explicit
// `ao preview`, in DiscoverEntry.
func DiscoverEntrypoint(workspacePath string) (Entry, bool) {
	if strings.TrimSpace(workspacePath) == "" {
		return Entry{}, false
	}
	for _, candidate := range entryCandidates {
		file, ok := ConfinedPath(workspacePath, candidate)
		if !ok {
			continue
		}
		info, err := os.Stat(file)
		if err == nil && !info.IsDir() {
			return Entry{Path: candidate, AbsPath: file, ModTime: info.ModTime(), Size: info.Size()}, true
		}
	}
	return Entry{}, false
}

// DiscoverEntry returns the entry the browser panel should preview for a
// workspace when someone asked for one. The conventional entrypoint always wins;
// when none exists it falls back to the most-recently-modified previewable file
// (.html/.htm/.md/.markdown) in the workspace, so a freshly generated report or
// document shows up for a bare `ao preview`.
//
// The fallback scans the filesystem, so it belongs on the request path only —
// see DiscoverEntrypoint for what runs on a timer.
func DiscoverEntry(workspacePath string) (Entry, bool) {
	// Guarded here as well as in DiscoverEntrypoint: a blank path would send the
	// fallback scan walking the daemon's working directory.
	if strings.TrimSpace(workspacePath) == "" {
		return Entry{}, false
	}
	if entry, ok := DiscoverEntrypoint(workspacePath); ok {
		return entry, true
	}
	return scanPreviewable(workspacePath, defaultWalkBounds)
}

// scanPreviewable walks the workspace and returns the newest previewable file.
// Ties (equal mod times) break on the slash path so the result is deterministic.
// Hidden and vendored directories are pruned, and the walk stops once it exceeds
// any of the supplied bounds, keeping the result whatever it found so far.
func scanPreviewable(workspacePath string, bounds walkBounds) (Entry, bool) {
	root, err := filepath.Abs(workspacePath)
	if err != nil {
		return Entry{}, false
	}
	var best Entry
	found := false
	candidates := 0
	entries := 0
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			//nolint:nilerr // skip unreadable entries rather than aborting the whole scan
			return nil
		}
		entries++
		if entries > bounds.maxEntries {
			return filepath.SkipAll
		}
		if d.IsDir() {
			if p == root {
				return nil
			}
			if skipPreviewDir(d.Name()) || walkDepth(root, p) > bounds.maxDepth {
				return filepath.SkipDir
			}
			return nil
		}
		if _, ok := previewableExts[strings.ToLower(filepath.Ext(d.Name()))]; !ok {
			return nil
		}
		candidates++
		if candidates > bounds.maxCandidates {
			return filepath.SkipAll
		}
		info, err := d.Info()
		if err != nil {
			//nolint:nilerr // skip this file, keep scanning the rest of the workspace
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			//nolint:nilerr // skip this file, keep scanning the rest of the workspace
			return nil
		}
		relSlash := filepath.ToSlash(rel)
		if !found || newerPreviewable(info, relSlash, best) {
			best = Entry{Path: relSlash, AbsPath: p, ModTime: info.ModTime(), Size: info.Size()}
			found = true
		}
		return nil
	})
	return best, found
}

func newerPreviewable(info fs.FileInfo, relSlash string, best Entry) bool {
	mod := info.ModTime()
	if mod.After(best.ModTime) {
		return true
	}
	if mod.Equal(best.ModTime) {
		return relSlash < best.Path
	}
	return false
}

func skipPreviewDir(name string) bool {
	if strings.HasPrefix(name, ".") {
		return true
	}
	_, vendored := vendoredPreviewDirs[name]
	return vendored
}

// walkDepth counts how many directory levels dir sits below root; the root
// itself is depth 0.
func walkDepth(root, dir string) int {
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		return 0
	}
	return len(strings.Split(filepath.ToSlash(rel), "/"))
}

// IsMarkdownPath reports whether p names a Markdown file the preview/files
// route should render to HTML rather than serve verbatim.
func IsMarkdownPath(p string) bool {
	switch strings.ToLower(filepath.Ext(p)) {
	case ".md", ".markdown":
		return true
	}
	return false
}

// ConfinedPath maps an asset path into workspacePath and rejects paths that
// escape the workspace root.
func ConfinedPath(workspacePath, assetPath string) (string, bool) {
	root, err := filepath.Abs(workspacePath)
	if err != nil || root == "" {
		return "", false
	}
	clean := strings.TrimPrefix(path.Clean("/"+strings.TrimSpace(assetPath)), "/")
	if clean == "" || clean == "." {
		clean = "index.html"
	}
	file := filepath.Join(root, filepath.FromSlash(clean))
	absFile, err := filepath.Abs(file)
	if err != nil {
		return "", false
	}
	rel, err := filepath.Rel(root, absFile)
	if err != nil || rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." {
		return "", false
	}
	return absFile, true
}

// FileURL builds the daemon preview/files URL for a workspace-local entry.
func FileURL(baseURL string, id domain.SessionID, entry string) string {
	u := normalizedBaseURL(baseURL)
	u.Path = "/api/v1/sessions/" + url.PathEscape(string(id)) + "/preview/files/" + escapePath(entry)
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

func normalizedBaseURL(raw string) url.URL {
	raw = strings.TrimRight(strings.TrimSpace(raw), "/")
	if raw == "" {
		raw = "http://127.0.0.1:3001"
	}
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return url.URL{Scheme: "http", Host: raw}
	}
	return *u
}

func escapePath(raw string) string {
	parts := strings.Split(raw, "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	return strings.Join(parts, "/")
}
