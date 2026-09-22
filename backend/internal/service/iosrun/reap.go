package iosrun

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// ReapOrphanedRuns closes run panes whose owning session has ended, on daemon
// boot.
//
// A run pane has no database row, no worktree and no board card - that is what
// makes it safe to leave a build running past the click that started it, and it
// is also what made a missed reap PERMANENT: no session sweep can see it, so
// `iosrun-advisor-ios-app-13` and `iosrun-advisor-ios-app-14` were still
// building days after the sessions that started them had ended, and the only
// way anyone found out was by reading `tmux ls`.
//
// A session ending now takes its panes with it (the lifecycle reducer reaps at
// every terminal write), so this is the RECOVERY half: panes orphaned by a
// crash, by a kill while the daemon was down, or by a version of AO that did
// not reap at all.
//
// It enumerates the state dir rather than tmux, which scopes it exactly right
// without needing a way to list panes: <dataDir>/iosrun/<session id> exists
// only for runs THIS daemon started, so a second daemon on its own AO_DATA_DIR
// can never reap the first one's panes. A session that is TERMINATED or has no
// row at all (purged) is reaped; a live one is left strictly alone, because a
// build that survived a daemon restart is a build somebody is waiting for.
//
// Returns the number of panes reaped. Best-effort per the daemon's boot
// contract: a sweep that cannot read its own directory reports the error and
// reaps nothing.
func (s *Service) ReapOrphanedRuns(ctx context.Context) (int, error) {
	if s.stateDir == "" {
		return 0, nil
	}
	root := filepath.Join(s.stateDir, "iosrun")
	entries, err := os.ReadDir(root)
	if err != nil {
		// No runs have ever been started here. Not a failure.
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("ios run: read the run records: %w", err)
	}
	reaped := 0
	for _, entry := range entries {
		id, ok := sessionIDForRecord(entry)
		if !ok {
			continue
		}
		rec, found, err := s.sessions.GetSession(ctx, id)
		if err != nil {
			return reaped, fmt.Errorf("ios run: read session %s: %w", id, err)
		}
		if found && !rec.IsTerminated {
			continue // live session: its build may be one somebody is waiting for.
		}
		if err := s.runtime.Destroy(ctx, ports.RuntimeHandle{ID: HandleID(id)}); err != nil {
			return reaped, fmt.Errorf("ios run: close the run pane of %s: %w", id, err)
		}
		reaped++
	}
	return reaped, nil
}

// sessionIDForRecord reads a session id back out of a run-record directory
// name, refusing anything that is not one. runDir writes the name through
// filepath.Base, so the round trip has to agree or the entry is not ours.
func sessionIDForRecord(entry fs.DirEntry) (domain.SessionID, bool) {
	if !entry.IsDir() {
		return "", false
	}
	name := entry.Name()
	if name == "" || name != filepath.Base(filepath.Clean(name)) {
		return "", false
	}
	return domain.SessionID(name), true
}
