package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Claude Code sets CLAUDE_CODE_SESSION_KIND=bg in a background job, and the job inherits
// the per-user daemon's environment: that of whichever AO session first started the daemon.
const claudeBackgroundKind = "bg"

type sessionEnvProblem struct {
	session string
	reason  string
	refuse  bool
}

func (p sessionEnvProblem) Error() string {
	return fmt.Sprintf("AO_SESSION_ID says this is AO session %s, but %s. Its messages, leases and claims would "+
		"be made in that session's name; run ao from that session's own terminal.", p.session, p.reason)
}

func checkSessionEnv() *sessionEnvProblem {
	session := strings.TrimSpace(os.Getenv("AO_SESSION_ID"))
	if session == "" {
		return nil
	}
	if os.Getenv("CLAUDE_CODE_SESSION_KIND") == claudeBackgroundKind {
		return &sessionEnvProblem{session: session, refuse: true,
			reason: "this is a Claude Code background session, whose environment comes from Claude's daemon, not from that session"}
	}
	cwd, ok := foreignWorktree(os.Getenv("AO_WORKSPACE"), os.Getenv("AO_DATA_DIR"))
	if !ok {
		return nil
	}
	return &sessionEnvProblem{session: session, refuse: keptToItsOwnWorktree(os.Getenv("AO_SESSION_KIND")),
		reason: fmt.Sprintf("the working directory %s is in another session's worktree, not in %s", cwd, os.Getenv("AO_WORKSPACE"))}
}

func keptToItsOwnWorktree(kind string) bool {
	return kind == string(domain.KindWorker)
}

func foreignWorktree(workspace, dataDir string) (string, bool) {
	if workspace == "" || dataDir == "" {
		return "", false
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", false
	}
	if !within(cwd, filepath.Join(dataDir, "worktrees")) || within(cwd, workspace) {
		return "", false
	}
	return cwd, true
}

func within(path, dir string) bool {
	path, dir = resolved(path), resolved(dir)
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func resolved(path string) string {
	if target, err := filepath.EvalSymlinks(path); err == nil {
		return target
	}
	return filepath.Clean(path)
}
