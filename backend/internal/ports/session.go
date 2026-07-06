package ports

import (
	"errors"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// ErrSessionNotFound reports an observation for an unknown session id.
var ErrSessionNotFound = errors.New("session not found")

// SpawnConfig is the request to start a new session: which project/issue, which
// agent harness, and the branch/prompt the agent launches with.
type SpawnConfig struct {
	ProjectID domain.ProjectID
	IssueID   domain.IssueID
	Kind      domain.SessionKind
	Harness   domain.AgentHarness
	Branch    string
	// AutoNameBranch asks the manager to generate a gitflow branch name via the
	// session's agent (one-shot) when Branch is empty. Non-dialog callers leave
	// it false to keep the ao/<id>/root default. Best-effort: any failure falls
	// back to the default name.
	AutoNameBranch bool
	// BaseBranch is the branch the new worktree is created from. Empty falls
	// back to the project's configured default branch.
	BaseBranch string
	Prompt     string
	// DisplayName is the user-facing sidebar label. Empty falls back to the
	// session id in the read model (e.g. orchestrator sessions).
	DisplayName string
}
