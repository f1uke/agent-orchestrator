package daemon

import (
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
)

// startLegacySmokeCleanup deletes what the removed smoke system left in the
// data dir: the evidence tree and the evidence-retention settings file. It
// removes its own input, so a later boot finds nothing and returns at once.
// The returned channel closes when it is done.
func startLegacySmokeCleanup(cfg config.Config, log *slog.Logger) <-chan struct{} {
	done := make(chan struct{})
	if cfg.DataDir == "" {
		close(done)
		return done
	}
	go func() {
		defer close(done)
		removeLegacySmokePath(cfg.DataDir, filepath.Join(cfg.DataDir, "evidence"), log)
		removeLegacySmokePath(cfg.DataDir, filepath.Join(cfg.DataDir, "evidence-retention-settings.json"), log)
	}()
	return done
}

func removeLegacySmokePath(dataDir, path string, log *slog.Logger) {
	if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) {
		return
	}
	// A symlink here could point anywhere; RemoveAll must only ever see a
	// path that resolves inside the data dir.
	if !physicallyWithin(path, dataDir) {
		log.Warn("smoke cleanup: refused a path that resolves outside the data dir", "path", path)
		return
	}
	if err := os.RemoveAll(path); err != nil {
		log.Warn("smoke cleanup: remove failed", "path", path, "err", err)
		return
	}
	log.Info("smoke cleanup: removed", "path", path)
}

// physicallyWithin reports whether path is inside root once both are resolved
// through symlinks (macOS /var -> /private/var included).
func physicallyWithin(path, root string) bool {
	p, r := resolvedPath(path), resolvedPath(root)
	rel, err := filepath.Rel(r, p)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func resolvedPath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved
	}
	return abs
}
