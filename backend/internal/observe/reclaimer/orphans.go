package reclaimer

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/aoagents/agent-orchestrator/backend/internal/reclaimlog"
)

// Orphan worktrees: directories under the managed worktree root that NO session
// record claims. They accumulate from spawns that failed part-way and from
// records removed without their directory.
//
// "No record" is also exactly what a bug in the ownership lookup looks like,
// so this sweep is deliberately the most timid part of the feature:
//
//   - It removes only EMPTY directories. Those are provably lossless, and they
//     are the leftover-directory case actually observed.
//   - A non-empty orphan is never touched. It is logged once, so it shows up in
//     the same audit trail as everything else and the user can decide. Deleting
//     an unowned directory whose provenance we cannot establish is the one
//     deletion in this feature that has no recovery route: with no session
//     record there is no branch name to recover from.
//   - If the orphan count exceeds maxOrphansPerSweep the whole sweep aborts and
//     says so. A sudden crop of orphans means the lookup is broken, not that
//     the user created dozens of stray directories.

// maxOrphansPerSweep is the circuit breaker. Above this the sweep refuses to
// act at all — the measured machine had 7, so a couple of dozen is already
// evidence that ownership resolution has gone wrong rather than evidence of
// mess.
const maxOrphansPerSweep = 24

// orphanDepth is how deep a worktree sits under the managed root:
// <root>/<project>/<kind>/<name>.
const orphanDepth = 3

// Orphan skip/abort reasons.
const (
	reasonOrphanNotEmpty = "orphan_not_empty"
	reasonOrphanFlood    = "orphan_count_over_limit"
)

// sweepOrphans removes empty unowned worktree directories and reports the rest.
// It is a no-op when no worktree root is configured.
func (r *Reclaimer) sweepOrphans(ctx context.Context) {
	if r.worktreeRoot == "" || r.paths == nil {
		return
	}
	known, err := r.paths.ListKnownWorkspacePaths(ctx)
	if err != nil {
		r.logger.Warn("reclaimer: orphan sweep skipped, could not read known workspaces", "err", err)
		return
	}
	owned := make(map[string]bool, len(known))
	for _, p := range known {
		owned[resolvePath(p)] = true
	}

	dirs, err := worktreeDirs(r.worktreeRoot)
	if err != nil {
		r.logger.Warn("reclaimer: orphan sweep skipped, could not scan worktree root", "err", err)
		return
	}
	orphans := make([]string, 0)
	for _, d := range dirs {
		if owned[resolvePath(d)] {
			continue
		}
		if pathContains(d, r.selfPath) {
			continue // never the ground we stand on
		}
		orphans = append(orphans, d)
	}
	if len(orphans) == 0 {
		return
	}
	if len(orphans) > maxOrphansPerSweep {
		// Refuse to act. An unexpectedly large orphan set is a signal to stop,
		// not a licence to delete.
		r.logger.Error("reclaimer: orphan sweep aborted, too many unowned worktrees",
			"count", len(orphans), "limit", maxOrphansPerSweep)
		r.writeOrphanOnce("", reclaimlog.Entry{
			Action:    reclaimlog.ActionAborted,
			Qualified: "orphan",
			Reason:    reasonOrphanFlood,
		})
		return
	}
	for _, d := range orphans {
		empty, err := dirIsEmpty(d)
		if err != nil || !empty {
			r.writeOrphanOnce(d, reclaimlog.Entry{
				Action:        reclaimlog.ActionSkipped,
				WorkspacePath: d,
				Qualified:     "orphan",
				Reason:        reasonOrphanNotEmpty,
			})
			continue
		}
		if err := os.Remove(d); err != nil {
			continue // a non-empty-by-now dir or a permission problem: leave it
		}
		r.logger.Info("reclaimer: removed empty orphan worktree directory", "path", d)
		r.writeOrphanOnce(d, reclaimlog.Entry{
			Action:        reclaimlog.ActionReclaimed,
			WorkspacePath: d,
			Qualified:     "orphan",
			Reason:        "empty_directory",
		})
	}
}

// writeOrphanOnce logs an orphan decision at most once per path per process, so
// a non-empty orphan that will never be removed does not append a line on every
// tick for the rest of the daemon's life.
func (r *Reclaimer) writeOrphanOnce(path string, e reclaimlog.Entry) {
	key := "orphan:" + path + ":" + e.Reason
	if r.orphanSeen[key] {
		return
	}
	r.orphanSeen[key] = true
	e.At = r.clock().UTC()
	r.write(e)
}

// worktreeDirs lists the directories exactly orphanDepth levels under root.
func worktreeDirs(root string) ([]string, error) {
	var out []string
	var walk func(dir string, depth int) error
	walk = func(dir string, depth int) error {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			child := filepath.Join(dir, e.Name())
			if depth+1 == orphanDepth {
				out = append(out, child)
				continue
			}
			if err := walk(child, depth+1); err != nil {
				continue // an unreadable project dir must not sink the scan
			}
		}
		return nil
	}
	if err := walk(root, 0); err != nil {
		return nil, err
	}
	return out, nil
}

// dirIsEmpty reports whether dir holds no entries at all. Reading a single name
// is enough to answer it without listing a directory that may be huge.
func dirIsEmpty(dir string) (bool, error) {
	f, err := os.Open(dir)
	if err != nil {
		return false, err
	}
	defer func() { _ = f.Close() }()
	names, err := f.Readdirnames(1)
	if errors.Is(err, io.EOF) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return len(names) == 0, nil
}
