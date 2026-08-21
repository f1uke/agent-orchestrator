package treewatch

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// maxClassifyCalls bounds how many times one watcher will ask git about a path
// it has never seen. Past it an unknown path is COUNTED rather than skipped:
// over-counting throws away a good run, under-counting certifies a mixed one,
// and only the second failure is the one this package exists to prevent.
const maxClassifyCalls = 512

// classifyTimeout bounds a single `git check-ignore`. A git that hangs must not
// wedge the event loop; a timeout resolves to "not ignored", the safe direction.
const classifyTimeout = 3 * time.Second

// ignorer answers "does git ignore this path?" for one worktree.
//
// It is two layers because they have different costs. The bulk answer comes from
// ONE `git ls-files --others --ignored --directory --exclude-standard` at attach
// time, which collapses whole ignored subtrees (`node_modules/`, `dist/`,
// `frontend/out/`) into single entries - that is what keeps the recursive walk
// from ever descending into the trees that made the preview poller burn a core.
// The per-path layer exists only for what that snapshot cannot know: a file
// matching an ignore pattern that is CREATED during a run (a `go build -o ao`
// landing in the source tree), which would otherwise read as the tree moving.
type ignorer struct {
	root   string
	binary string

	mu       sync.Mutex
	prefixes []string        // ignored paths from the attach-time snapshot, slash-relative
	cache    map[string]bool // rel path -> ignored
	calls    int
}

func newIgnorer(ctx context.Context, binary, root string) *ignorer {
	ig := &ignorer{root: root, binary: binary, cache: map[string]bool{}}
	ig.prefixes = ig.snapshot(ctx)
	return ig
}

// snapshot lists what git already considers ignored, with whole directories
// collapsed. A repo that cannot be listed (not a git worktree, git missing)
// yields no prefixes, which is safe: the walk then relies on the per-path layer
// and on the watched-directory cap.
func (ig *ignorer) snapshot(ctx context.Context) []string {
	cmd := exec.CommandContext(ctx, ig.binary, "-C", ig.root,
		"ls-files", "-z", "--others", "--ignored", "--directory", "--exclude-standard")
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	var prefixes []string
	for _, entry := range strings.Split(string(out), "\x00") {
		if entry = strings.TrimSpace(entry); entry != "" {
			prefixes = append(prefixes, entry)
		}
	}
	return prefixes
}

// ignoredRel reports whether the slash-separated repo-relative path is ignored.
func (ig *ignorer) ignoredRel(rel string) bool {
	if rel == "" || rel == "." {
		return false
	}
	// .git is never watched and never counted. It is not in `ls-files --ignored`
	// output (git does not report its own directory), so it is named here.
	if rel == ".git" || strings.HasPrefix(rel, ".git/") {
		return true
	}
	ig.mu.Lock()
	for _, p := range ig.prefixes {
		if matchesIgnoredEntry(rel, p) {
			ig.mu.Unlock()
			return true
		}
	}
	if hit, ok := ig.cache[rel]; ok {
		ig.mu.Unlock()
		return hit
	}
	if ig.calls >= maxClassifyCalls {
		ig.mu.Unlock()
		return false
	}
	ig.calls++
	ig.mu.Unlock()

	hit := ig.checkIgnore(rel)
	ig.mu.Lock()
	ig.cache[rel] = hit
	ig.mu.Unlock()
	return hit
}

// ignored reports whether an absolute path inside the worktree is ignored. A
// path outside the worktree is not this watcher's business and reads ignored.
func (ig *ignorer) ignored(abs string) bool {
	rel, err := filepath.Rel(ig.root, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return true
	}
	return ig.ignoredRel(filepath.ToSlash(rel))
}

func (ig *ignorer) checkIgnore(rel string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), classifyTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, ig.binary, "-C", ig.root, "check-ignore", "-q", "--", rel)
	return cmd.Run() == nil
}

// matchesIgnoredEntry matches rel against one `ls-files --directory` entry. git
// prints a collapsed directory with a trailing slash and a file without one.
func matchesIgnoredEntry(rel, entry string) bool {
	if entry == "" {
		return false
	}
	if strings.HasSuffix(entry, "/") {
		return rel == strings.TrimSuffix(entry, "/") || strings.HasPrefix(rel, entry)
	}
	return rel == entry
}
