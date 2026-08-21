package treewatch

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// settle waits for the counter to reach at least want, or fails. Filesystem
// events are asynchronous on every backend, so a test that reads the generation
// immediately after a write is testing the scheduler, not the detector.
func settle(t *testing.T, l *Lease, want uint64) uint64 {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		got, err := l.Generation()
		if err != nil {
			t.Fatalf("Generation: %v", err)
		}
		if got >= want {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("generation stayed at %d, wanted at least %d", got, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// quiet asserts the counter does not move for a short settling window. Used for
// the "this write must NOT count" cases, where the failure mode is a late event.
func quiet(t *testing.T, l *Lease, want uint64) {
	t.Helper()
	deadline := time.Now().Add(600 * time.Millisecond)
	for time.Now().Before(deadline) {
		got, err := l.Generation()
		if err != nil {
			t.Fatalf("Generation: %v", err)
		}
		if got != want {
			t.Fatalf("generation moved to %d; expected it to stay at %d", got, want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func gitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	root := t.TempDir()
	// macOS hands out /var/folders/... which is a symlink to /private/var/...;
	// fsnotify reports the resolved path, so resolve here or every Rel fails.
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	root = resolved
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "t@example.com"},
		{"config", "user.name", "t"},
	} {
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write(t, filepath.Join(root, ".gitignore"), "node_modules/\ndist/\n*.log\n")
	mkdir(t, filepath.Join(root, "src"))
	write(t, filepath.Join(root, "src", "main.go"), "package main\n")
	mkdir(t, filepath.Join(root, "node_modules", "left-pad"))
	write(t, filepath.Join(root, "node_modules", "left-pad", "index.js"), "module.exports=1\n")
	commit(t, root)
	return root
}

func commit(t *testing.T, root string) {
	t.Helper()
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-qm", "seed"}} {
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func mkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}

func attach(t *testing.T, root string) (*Registry, *Lease) {
	t.Helper()
	// IdleTTL -1: tests assert the watcher's lifecycle directly, so the idle hold
	// (which exists to make back-to-back runs cheap) is turned off rather than
	// waited out.
	reg := NewRegistry(Options{IdleTTL: -1})
	t.Cleanup(reg.Close)
	lease, err := reg.Attach(context.Background(), root)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	t.Cleanup(lease.Release)
	if reason, down := lease.Down(); down {
		t.Fatalf("detector down on a plain git worktree: %s", reason)
	}
	return reg, lease
}

// A run nobody touched is trusted: the generation at the end equals the one at
// the start.
func TestUntouchedRunIsTrusted(t *testing.T) {
	root := gitRepo(t)
	_, lease := attach(t, root)

	start, err := lease.Generation()
	if err != nil {
		t.Fatalf("Generation: %v", err)
	}
	quiet(t, lease, start)

	end, err := lease.Generation()
	if err != nil {
		t.Fatalf("Generation: %v", err)
	}
	if end != start {
		t.Fatalf("an untouched tree moved: start %d end %d (changed=%v)", start, end, lease.Changed())
	}
}

// THE ONE THAT MATTERS: a write that lands mid-run is caught, and the run is
// therefore not trustworthy.
func TestMidRunWriteIsCaught(t *testing.T) {
	root := gitRepo(t)
	_, lease := attach(t, root)
	start := lease.StartGeneration()

	write(t, filepath.Join(root, "src", "main.go"), "package main // dev is typing\n")

	end := settle(t, lease, start+1)
	if end == start {
		t.Fatal("a write to a tracked file did not move the generation")
	}
	changed := lease.Changed()
	if len(changed) == 0 || changed[0] != "src/main.go" {
		t.Fatalf("changed paths did not name the file that moved: %v", changed)
	}
}

// The failure `git status --porcelain` cannot see: the SAME already-dirty file
// saved twice. Porcelain prints an identical line both times; the counter moves
// both times.
func TestRepeatedWriteToOneDirtyFileIsCaught(t *testing.T) {
	root := gitRepo(t)
	write(t, filepath.Join(root, "src", "main.go"), "package main // already dirty\n")
	_, lease := attach(t, root)
	start := lease.StartGeneration()

	write(t, filepath.Join(root, "src", "main.go"), "package main // save 1\n")
	afterFirst := settle(t, lease, start+1)
	write(t, filepath.Join(root, "src", "main.go"), "package main // save 2\n")
	settle(t, lease, afterFirst+1)
}

// The failure start/end sampling cannot see: written at t+1, reverted at t+3.
// Both samples agree; the counter does not.
func TestWriteThenRevertIsCaught(t *testing.T) {
	root := gitRepo(t)
	original := "package main\n"
	_, lease := attach(t, root)
	start := lease.StartGeneration()

	path := filepath.Join(root, "src", "main.go")
	write(t, path, "package main // broken\n")
	settle(t, lease, start+1)
	write(t, path, original)

	end := settle(t, lease, start+2)
	if end == start {
		t.Fatal("write-then-revert inside the window was not counted")
	}
}

// Ignored trees are not watched and their writes never count - this is the bound
// that keeps a worktree with node_modules affordable.
func TestIgnoredPathsDoNotCount(t *testing.T) {
	root := gitRepo(t)
	_, lease := attach(t, root)
	start := lease.StartGeneration()

	write(t, filepath.Join(root, "node_modules", "left-pad", "index.js"), "module.exports=2\n")
	write(t, filepath.Join(root, "build.log"), "noise\n")
	mkdir(t, filepath.Join(root, "dist"))
	write(t, filepath.Join(root, "dist", "bundle.js"), "console.log(1)\n")

	quiet(t, lease, start)
}

// A build product matching an ignore pattern, created DURING the run inside a
// watched directory, is classified per-path and does not move the counter. The
// attach-time snapshot cannot know about it, so this is the second ignore layer.
func TestIgnoredFileCreatedMidRunDoesNotCount(t *testing.T) {
	root := gitRepo(t)
	_, lease := attach(t, root)
	start := lease.StartGeneration()

	write(t, filepath.Join(root, "src", "compile.log"), "built\n")

	quiet(t, lease, start)
}

// A directory created mid-run is watched, so writes inside it count. Without
// this the detector would miss an agent creating a new package and filling it.
func TestNewDirectoryIsWatched(t *testing.T) {
	root := gitRepo(t)
	_, lease := attach(t, root)
	start := lease.StartGeneration()

	mkdir(t, filepath.Join(root, "src", "fresh"))
	afterMkdir := settle(t, lease, start+1)
	write(t, filepath.Join(root, "src", "fresh", "file.go"), "package fresh\n")
	settle(t, lease, afterMkdir+1)
}

// Two overlapping runs in one worktree share one watcher and one counter, and
// each reads the move relative to its OWN start.
func TestOverlappingLeasesShareOneCounter(t *testing.T) {
	root := gitRepo(t)
	reg, first := attach(t, root)

	write(t, filepath.Join(root, "src", "main.go"), "package main // before the second run\n")
	settle(t, first, first.StartGeneration()+1)

	second, err := reg.Attach(context.Background(), root)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	defer second.Release()
	if second.w != first.w {
		t.Fatal("two leases on one worktree got two watchers")
	}
	if second.StartGeneration() == first.StartGeneration() {
		t.Fatal("the second lease started from a stale generation")
	}

	write(t, filepath.Join(root, "src", "main.go"), "package main // during both\n")
	settle(t, second, second.StartGeneration()+1)
	if got := second.Changed(); len(got) != 1 || got[0] != "src/main.go" {
		t.Fatalf("second lease reported %v; wanted only the write since it attached", got)
	}
}

// The last lease going closes the watcher; a fresh attach starts a new one.
func TestWatcherStopsWithTheLastLease(t *testing.T) {
	root := gitRepo(t)
	reg := NewRegistry(Options{IdleTTL: -1})
	t.Cleanup(reg.Close)

	lease, err := reg.Attach(context.Background(), root)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	lease.Release()
	lease.Release() // idempotent

	reg.mu.Lock()
	live := len(reg.watchers)
	reg.mu.Unlock()
	if live != 0 {
		t.Fatalf("watcher survived its last lease: %d still live", live)
	}
}

// A tree over the directory cap is REFUSED, not partially watched: the lease
// reports the detector down so the run reads uncertified.
func TestOversizedTreeRefusesRatherThanWatchingPart(t *testing.T) {
	root := gitRepo(t)
	for i := range 8 {
		mkdir(t, filepath.Join(root, "src", "pkg"+string(rune('a'+i))))
	}
	reg := NewRegistry(Options{MaxDirs: 3, IdleTTL: -1})
	t.Cleanup(reg.Close)

	lease, err := reg.Attach(context.Background(), root)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	defer lease.Release()

	reason, down := lease.Down()
	if !down {
		t.Fatal("an oversized tree was watched in part instead of refused")
	}
	if _, err := lease.Generation(); err == nil {
		t.Fatal("a down detector returned a generation")
	}
	t.Logf("refused with: %s", reason)
}

// A path that is not a directory is a caller error, not a silent no-op detector.
func TestAttachRefusesANonDirectory(t *testing.T) {
	reg := NewRegistry(Options{IdleTTL: -1})
	t.Cleanup(reg.Close)
	if _, err := reg.Attach(context.Background(), filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Fatal("attaching to a missing path succeeded")
	}
}

// A run that follows another in the same worktree REUSES the watch rather than
// re-walking the tree. Establishing one costs a descriptor per file on macOS -
// measured around two seconds on this repo's own worktree - and a member
// normally brackets several runs in a row.
//
// The reuse is also more accurate than a fresh walk: the counter never stops, so
// a write that lands in the gap between two runs is still counted against the
// second one's start.
func TestBackToBackRunsReuseTheWarmWatcher(t *testing.T) {
	root := gitRepo(t)
	reg := NewRegistry(Options{IdleTTL: time.Minute})
	t.Cleanup(reg.Close)

	first, err := reg.Attach(context.Background(), root)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	warm := first.w
	first.Release()

	reg.mu.Lock()
	held := reg.watchers[root] == warm
	reg.mu.Unlock()
	if !held {
		t.Fatal("the watcher was dropped instead of being held for the next run")
	}

	// A write in the gap between the two runs. It has to be OBSERVED before the
	// second run attaches, or this asserts on the scheduler rather than on the
	// watcher having stayed up.
	write(t, filepath.Join(root, "src", "main.go"), "package main // between runs\n")
	deadline := time.Now().Add(5 * time.Second)
	for warm.gen.Load() == first.StartGeneration() {
		if time.Now().After(deadline) {
			t.Fatal("the held watcher stopped counting after its last lease went")
		}
		time.Sleep(10 * time.Millisecond)
	}

	second, err := reg.Attach(context.Background(), root)
	if err != nil {
		t.Fatalf("Attach 2: %v", err)
	}
	defer second.Release()
	if second.w != warm {
		t.Fatal("the second run started a fresh watcher instead of claiming the warm one")
	}
	if second.StartGeneration() == first.StartGeneration() {
		t.Fatal("the gap write was not counted, so the counter stopped between runs")
	}
}

// The hold is bounded: a worktree nobody runs anything in ends up with no
// watcher at all, which is the whole point of attaching per bracket.
func TestTheIdleHoldExpires(t *testing.T) {
	root := gitRepo(t)
	reg := NewRegistry(Options{IdleTTL: 20 * time.Millisecond})
	t.Cleanup(reg.Close)

	lease, err := reg.Attach(context.Background(), root)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	lease.Release()

	deadline := time.Now().Add(3 * time.Second)
	for {
		reg.mu.Lock()
		live := len(reg.watchers)
		reg.mu.Unlock()
		if live == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the idle hold never expired")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
