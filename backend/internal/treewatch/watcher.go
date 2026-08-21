// Package treewatch answers one question exactly: DID ANYTHING WRITE TO THIS
// WORKTREE BETWEEN THESE TWO MOMENTS?
//
// It exists because a crew shares one checkout, so qa's build or test run can
// read a tree dev is halfway through writing. That is not data loss - it is an
// untrustworthy result, which is worse in one way: IT LOOKS FINE. The race
// cannot be prevented (there is no way to stop an agent writing a file), so it
// is detected and the result is invalidated.
//
// # Why a watcher and not sampling
//
// Sampling the tree at run start and run end was the obvious mechanism and it
// misses, twice, in the shapes that matter most:
//
//   - `git status --porcelain` is a SET, not content. Two saves to an
//     already-dirty file produce the identical line, so a hash of porcelain
//     output cannot see an agent iterating on ONE file - which is not an edge
//     case, it is the normal shape of dev's work.
//   - Start/end sampling misses write-then-revert inside the window: a broken
//     file saved at t+1 and fixed at t+3, compiled at t+2, with both samples
//     agreeing.
//
// Both produce a laundered clean result, and a detector that misses is worse
// than none. A monotonic write counter fed by a filesystem watcher has neither
// miss: it is exact over the whole interval and O(1) to read.
//
// # When it cannot certify
//
// If the watcher cannot be established, this package says so. It never degrades
// to sampling and never reports a clean tree it did not actually observe: the
// caller marks the run UNCERTIFIED instead. If a detector that misses is bad, an
// absent one has to be visible too.
package treewatch

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fsnotify/fsnotify"
)

const (
	// defaultMaxDirs bounds how many directories one worktree may cost. A tree
	// bigger than this is REFUSED rather than watched in part: fsnotify's kqueue
	// backend holds a descriptor per file under every watched directory, and a
	// partial watch is precisely the detector-that-misses this package rejects.
	// This repo's non-ignored tree is ~210 directories, so the cap is ~20x
	// headroom for a monorepo and still far under any process descriptor limit.
	defaultMaxDirs = 4096
	// eventBuffer keeps the kernel's events from backing up behind a `git
	// check-ignore` on a path the watcher has not classified yet.
	eventBuffer = 4096
	// changeLogSize is how many recent write records are kept for the "what
	// moved?" line on a discarded run. Advisory only - the COUNTER is the
	// verdict, this is just the explanation.
	changeLogSize = 64
	// changedSample bounds how many distinct paths one lease will name.
	changedSample = 10
	// defaultIdleTTL is how long a watcher stays up after its last lease goes.
	//
	// Establishing one is not free - on macOS each watched directory costs a
	// descriptor per file inside it, which measured ~2s on this repo's worktree -
	// and a member normally brackets several runs in a row. Holding the watch
	// briefly makes every run after the first attach instantly, and it makes the
	// counter CONTINUOUS across that gap, which is strictly more accurate than
	// re-walking. It stays a bounded hold, not a permanent one: a worktree nobody
	// runs anything in ends up with no watcher, as it must.
	defaultIdleTTL = 2 * time.Minute
)

// ErrDetectorDown is returned by Lease.Generation when the watcher is not
// certifying: it never started, it latched down on a watch error, or it was lost
// (a daemon restart between a run's start and its end).
var ErrDetectorDown = errors.New("treewatch: detector is down")

type changeRecord struct {
	gen uint64
	rel string
}

// watcher is one live filesystem watch over one worktree, shared by every lease
// on that path.
type watcher struct {
	root   string
	logger *slog.Logger

	fsw *fsnotify.Watcher
	ig  *ignorer

	gen atomic.Uint64

	mu      sync.Mutex
	refs    int
	down    string
	changes []changeRecord
	dirs    map[string]struct{}

	closeOnce sync.Once
	done      chan struct{}
	// idle is the pending close for a watcher whose last lease has gone. It is
	// cancelled if a new run brackets the same worktree first.
	idle *time.Timer
}

// Options configures a Registry.
type Options struct {
	// GitBinary is the git executable used to answer "is this path ignored?".
	// Defaults to "git".
	GitBinary string
	// MaxDirs overrides defaultMaxDirs. Zero uses the default.
	MaxDirs int
	// IdleTTL overrides defaultIdleTTL - how long a watcher outlives its last
	// lease so back-to-back runs reuse it. Negative closes immediately.
	IdleTTL time.Duration
	Logger  *slog.Logger
}

// Registry attaches at most one watcher per worktree path and refcounts it, so
// two overlapping runs in one checkout read ONE consistent counter.
//
// A watcher exists only while at least one run brackets that worktree. That is
// deliberate: a solo session, and a crew that never brackets a run, gets no
// watcher at all - no descriptors, no goroutine, no git subprocess - so nothing
// about them changes. It costs no accuracy, because the recursive walk completes
// and every watch is live BEFORE the caller stamps the run's starting
// generation: a write that lands during the walk is a pre-run write, and any
// later write to that same path is seen.
type Registry struct {
	opts Options

	mu       sync.Mutex
	watchers map[string]*watcher
}

// NewRegistry builds a Registry. The zero Options are usable.
func NewRegistry(opts Options) *Registry {
	if opts.GitBinary == "" {
		opts.GitBinary = "git"
	}
	if opts.MaxDirs <= 0 {
		opts.MaxDirs = defaultMaxDirs
	}
	if opts.IdleTTL == 0 {
		opts.IdleTTL = defaultIdleTTL
	}
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}
	return &Registry{opts: opts, watchers: map[string]*watcher{}}
}

// Lease is one run's hold on a worktree's watcher. Generation is read at the
// run's start and again at its end; equal means nothing moved.
type Lease struct {
	r        *Registry
	w        *watcher
	startGen uint64
	released atomic.Bool
}

// Attach takes a lease on the watcher for worktree root, starting one if this is
// the first lease. It returns a lease even when the watcher could NOT be
// established: the lease then reports the detector as down, which is how the
// caller learns to mark the run uncertified instead of quietly approximating.
func (r *Registry) Attach(ctx context.Context, root string) (*Lease, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("treewatch: resolve %q: %w", root, err)
	}
	if info, statErr := os.Stat(abs); statErr != nil || !info.IsDir() {
		return nil, fmt.Errorf("treewatch: %q is not a directory", root)
	}

	r.mu.Lock()
	w, ok := r.watchers[abs]
	if !ok {
		w = r.start(ctx, abs)
		r.watchers[abs] = w
	}
	w.mu.Lock()
	// A watcher waiting out its idle hold is CLAIMED, not replaced: its counter
	// has been running the whole time, so reusing it is both faster and more
	// accurate than starting a fresh one.
	if w.idle != nil {
		w.idle.Stop()
		w.idle = nil
	}
	w.refs++
	w.mu.Unlock()
	r.mu.Unlock()

	return &Lease{r: r, w: w, startGen: w.gen.Load()}, nil
}

// start builds the watcher for abs. It never returns nil: a failure produces a
// watcher latched DOWN with the reason, so the caller gets "uncertified" rather
// than an error it might be tempted to ignore.
func (r *Registry) start(ctx context.Context, abs string) *watcher {
	w := &watcher{
		root:   abs,
		logger: r.opts.Logger,
		dirs:   map[string]struct{}{},
		done:   make(chan struct{}),
	}
	fsw, err := fsnotify.NewBufferedWatcher(eventBuffer)
	if err != nil {
		w.down = fmt.Sprintf("filesystem watcher unavailable: %v", err)
		return w
	}
	w.fsw = fsw
	w.ig = newIgnorer(ctx, r.opts.GitBinary, abs)
	if err := w.addTree(abs, r.opts.MaxDirs); err != nil {
		w.down = err.Error()
		_ = fsw.Close()
		w.fsw = nil
		return w
	}
	go w.loop()
	return w
}

// addTree walks root and watches every non-ignored directory. Directories only:
// both backends report writes to the FILES inside a watched directory, so a
// per-file watch would buy nothing and cost a descriptor each.
func (w *watcher) addTree(root string, maxDirs int) error {
	added := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// A directory that vanished mid-walk is not a reason to refuse the
			// whole tree; anything else is.
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if !d.IsDir() {
			return nil
		}
		if path != root && w.ig.ignored(path) {
			return fs.SkipDir
		}
		if added >= maxDirs {
			return fmt.Errorf("worktree has more than %d non-ignored directories; refusing a partial watch", maxDirs)
		}
		if addErr := w.fsw.Add(path); addErr != nil {
			return fmt.Errorf("watch %s: %w", path, addErr)
		}
		w.dirs[path] = struct{}{}
		added++
		return nil
	})
	if err != nil {
		return err
	}
	w.logger.Debug("treewatch: watching worktree", "root", root, "directories", added)
	return nil
}

// loop counts writes. Every non-ignored Create/Write/Remove/Rename bumps the
// generation.
//
// Chmod is deliberately NOT counted: kqueue reports attribute touches that are
// not content changes, and counting them would throw away good runs for no
// detection gain.
func (w *watcher) loop() {
	defer close(w.done)
	for {
		select {
		case ev, ok := <-w.fsw.Events:
			if !ok {
				return
			}
			w.onEvent(ev)
		case err, ok := <-w.fsw.Errors:
			if !ok {
				return
			}
			// An overflow means the kernel dropped events we will never see, so
			// this watcher can no longer certify anything. Latch it down rather
			// than keep counting a number that is now a guess.
			w.markDown(fmt.Sprintf("filesystem watch failed: %v", err))
			return
		}
	}
}

func (w *watcher) onEvent(ev fsnotify.Event) {
	if ev.Op == fsnotify.Chmod {
		return
	}
	if w.ig.ignored(ev.Name) {
		return
	}
	if ev.Has(fsnotify.Create) {
		if info, err := os.Stat(ev.Name); err == nil && info.IsDir() {
			w.addNewDir(ev.Name)
		}
	}
	rel, err := filepath.Rel(w.root, ev.Name)
	if err != nil {
		rel = ev.Name
	}
	// The bump and its explanation are published under ONE lock, and the bump
	// happens inside it. A reader that has already seen the new generation blocks
	// here until the record lands, so "the counter moved" is never observable
	// before "this is what moved".
	w.mu.Lock()
	gen := w.gen.Add(1)
	w.changes = append(w.changes, changeRecord{gen: gen, rel: filepath.ToSlash(rel)})
	if len(w.changes) > changeLogSize {
		w.changes = w.changes[len(w.changes)-changeLogSize:]
	}
	w.mu.Unlock()
}

// addNewDir watches a directory created while the run is in flight, so a write
// inside it counts. Failing to watch it means writes there would be invisible,
// which is a miss - so the watcher latches down instead.
func (w *watcher) addNewDir(path string) {
	w.mu.Lock()
	if _, seen := w.dirs[path]; seen {
		w.mu.Unlock()
		return
	}
	w.mu.Unlock()
	if err := w.fsw.Add(path); err != nil {
		w.markDown(fmt.Sprintf("watch %s: %v", path, err))
		return
	}
	w.mu.Lock()
	w.dirs[path] = struct{}{}
	w.mu.Unlock()
}

func (w *watcher) markDown(reason string) {
	w.mu.Lock()
	if w.down == "" {
		w.down = reason
	}
	w.mu.Unlock()
	w.logger.Warn("treewatch: detector down", "root", w.root, "reason", reason)
}

func (w *watcher) downReason() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.down
}

func (w *watcher) close() {
	w.closeOnce.Do(func() {
		if w.fsw != nil {
			_ = w.fsw.Close()
			<-w.done
		}
	})
}

// Generation is the worktree's write count. Equal readings at a run's start and
// end mean nothing wrote to a non-ignored path in between.
func (l *Lease) Generation() (uint64, error) {
	if reason := l.w.downReason(); reason != "" {
		return 0, fmt.Errorf("%w: %s", ErrDetectorDown, reason)
	}
	return l.w.gen.Load(), nil
}

// StartGeneration is the reading taken when this lease was attached.
func (l *Lease) StartGeneration() uint64 { return l.startGen }

// Changed names up to changedSample distinct paths written since this lease
// attached. Advisory - it explains a discard, it does not decide one.
func (l *Lease) Changed() []string {
	l.w.mu.Lock()
	defer l.w.mu.Unlock()
	seen := map[string]struct{}{}
	out := []string{}
	for _, c := range l.w.changes {
		if c.gen <= l.startGen {
			continue
		}
		if _, dup := seen[c.rel]; dup {
			continue
		}
		seen[c.rel] = struct{}{}
		out = append(out, c.rel)
		if len(out) == changedSample {
			break
		}
	}
	return out
}

// Release drops this run's hold. The watcher stops once the last lease goes.
func (l *Lease) Release() {
	if !l.released.CompareAndSwap(false, true) {
		return
	}
	l.r.release(l.w)
}

// release drops one hold. The last one starts the idle hold rather than closing
// outright, so the next run in the same worktree reuses a warm watcher.
func (r *Registry) release(w *watcher) {
	r.mu.Lock()
	defer r.mu.Unlock()
	w.mu.Lock()
	w.refs--
	last := w.refs <= 0
	if !last {
		w.mu.Unlock()
		return
	}
	if r.opts.IdleTTL <= 0 {
		w.mu.Unlock()
		delete(r.watchers, w.root)
		w.close()
		return
	}
	w.idle = time.AfterFunc(r.opts.IdleTTL, func() { r.expire(w) })
	w.mu.Unlock()
}

// expire closes a watcher whose idle hold ran out with nobody having claimed it.
func (r *Registry) expire(w *watcher) {
	r.mu.Lock()
	w.mu.Lock()
	stillIdle := w.refs <= 0 && w.idle != nil
	if stillIdle {
		w.idle = nil
	}
	w.mu.Unlock()
	if stillIdle && r.watchers[w.root] == w {
		delete(r.watchers, w.root)
	}
	r.mu.Unlock()
	if stillIdle {
		w.close()
	}
}

// Close stops every watcher. Used on daemon shutdown and by tests.
func (r *Registry) Close() {
	r.mu.Lock()
	watchers := make([]*watcher, 0, len(r.watchers))
	for _, w := range r.watchers {
		w.mu.Lock()
		if w.idle != nil {
			w.idle.Stop()
			w.idle = nil
		}
		w.mu.Unlock()
		watchers = append(watchers, w)
	}
	r.watchers = map[string]*watcher{}
	r.mu.Unlock()
	for _, w := range watchers {
		w.close()
	}
}

// Down reports why the detector cannot certify, and whether that is the case. A
// caller records this at attach time so a run that was never watched reads
// UNCERTIFIED from the start rather than discovering it at the end.
func (l *Lease) Down() (string, bool) {
	reason := l.w.downReason()
	return reason, reason != ""
}

// Root is the worktree this lease watches.
func (l *Lease) Root() string { return l.w.root }
