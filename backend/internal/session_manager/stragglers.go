package sessionmanager

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// strayReaper best-effort terminates processes an agent left running in its
// worktree (e.g. a `npm run dev` server that reparented to init and now holds a
// port such as :5175). Destroying a session's tmux reaps the pane's own process
// tree, but a double-forked/detached child escapes it and leaks its port; this
// catches those on teardown.
//
// It is deliberately conservative: it only acts on AO-managed worktree paths
// (see isManagedWorktreePath), never targets pid<=1 or the daemon itself, and is
// wholly best-effort — every failure is logged and swallowed so it can never
// break teardown. The listing/kill/self seams are injected for tests.
type strayReaper struct {
	// listCwdPIDs returns the pids whose current working directory is at or under
	// dir. Production uses lsof; a nil reaper is a no-op.
	listCwdPIDs func(ctx context.Context, dir string) ([]int, error)
	kill        func(pid int) error
	self        int
	log         *slog.Logger
}

// newStrayReaper builds the production reaper (lsof + SIGTERM). Returns a reaper
// whose fields are nil-safe: reap() no-ops when listCwdPIDs is nil.
func newStrayReaper(log *slog.Logger) *strayReaper {
	if log == nil {
		log = slog.Default()
	}
	return &strayReaper{
		listCwdPIDs: lsofCwdPIDs,
		kill:        killStray,
		self:        os.Getpid(),
		log:         log,
	}
}

// reap terminates stray processes rooted in worktreePath. Best-effort and silent
// on the happy path; guarded so a non-AO path is never touched.
func (r *strayReaper) reap(ctx context.Context, worktreePath string) {
	if r == nil || r.listCwdPIDs == nil {
		return
	}
	if !isManagedWorktreePath(worktreePath) {
		return
	}
	pids, err := r.listCwdPIDs(ctx, worktreePath)
	if err != nil {
		r.log.Warn("straggler reap: list processes failed", "path", worktreePath, "error", err)
		return
	}
	for _, pid := range pids {
		if pid <= 1 || pid == r.self {
			continue
		}
		if err := r.kill(pid); err != nil {
			r.log.Warn("straggler reap: signal failed", "pid", pid, "path", worktreePath, "error", err)
			continue
		}
		r.log.Info("straggler reap: terminated leftover process", "pid", pid, "path", worktreePath)
	}
}

// isManagedWorktreePath reports whether p is an AO-managed worktree — an absolute
// path with a "worktrees" segment. This is the safety guard that keeps the reaper
// from ever ranging over an arbitrary directory.
func isManagedWorktreePath(p string) bool {
	if !filepath.IsAbs(p) {
		return false
	}
	sep := string(os.PathSeparator)
	return strings.Contains(p, sep+"worktrees"+sep)
}

// lsofCwdPIDs lists pids whose current working directory is at or under dir,
// using `lsof -a -d cwd -F pn` (one process/name record pair per process). A
// missing lsof, or lsof's exit-1-when-nothing-matches, yields an empty list.
func lsofCwdPIDs(ctx context.Context, dir string) ([]int, error) {
	resolved := dir
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		resolved = r
	}
	out, err := exec.CommandContext(ctx, "lsof", "-a", "-d", "cwd", "-F", "pn").Output()
	if err != nil {
		// lsof exits 1 when it finds nothing to report — not an error for us.
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return nil, nil
		}
		if errors.Is(err, exec.ErrNotFound) {
			return nil, nil // no lsof on host: nothing to reap
		}
		return nil, err
	}
	return parseLsofCwd(string(out), resolved), nil
}

// parseLsofCwd extracts pids from `lsof -F pn` output whose cwd (the n record)
// is at or under root.
func parseLsofCwd(out, root string) []int {
	var pids []int
	cur := 0
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		switch line[0] {
		case 'p':
			if pid, err := strconv.Atoi(line[1:]); err == nil {
				cur = pid
			} else {
				cur = 0
			}
		case 'n':
			if cur == 0 {
				continue
			}
			if pathAtOrUnder(line[1:], root) {
				pids = append(pids, cur)
			}
		}
	}
	return pids
}

// pathAtOrUnder reports whether path equals root or sits under it.
func pathAtOrUnder(path, root string) bool {
	if path == root {
		return true
	}
	return strings.HasPrefix(path, strings.TrimRight(root, string(os.PathSeparator))+string(os.PathSeparator))
}
