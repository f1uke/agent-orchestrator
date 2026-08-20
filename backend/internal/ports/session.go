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
	// PRTarget is the branch this session's PR merges INTO. Distinct from
	// BaseBranch: a hotfix can be cut from `release/2.1` and still target
	// `develop`, or vice versa. Empty falls back to the resolved BaseBranch, and
	// the resolved value is persisted on every session - deferred or immediate -
	// so the target is a durable fact rather than something each reader infers.
	PRTarget string
	// CreatedBy is the orchestrator session id that queued a deferred/TODO task,
	// so it can be pinged with the report-back when the worker finishes. Empty
	// for a normal interactive spawn.
	CreatedBy domain.SessionID
	// KeepWarmOnMerge marks a worker expected to open MORE PRs after the current
	// one merges: when set, a merge that would finish the session SUSPENDS it in
	// place (card stays on the board) instead of terminating it to Done
	// (feature/merge-suspend-in-place). Default false - an ordinary single-PR
	// worker still auto-archives on merge. Set by `ao spawn --keep-warm`.
	KeepWarmOnMerge bool
	// TaskSize is the ceremony level for a worker task (`ao spawn --task-size`):
	// mechanical / standard / deep. It drives only the worker system prompt (a
	// mechanical task is authorized to skip the process skills) and is persisted on
	// the session. Empty resolves to standard (full ceremony) via WithDefault.
	TaskSize domain.TaskSize
	// CrewOf and CrewRole spawn this session as an ADDITIONAL member of an
	// existing task rather than as a task of its own: it joins the named dev
	// session on its branch, in its worktree, as a second long-lived agent.
	//
	// This is the deliberate, non-default seam for the crew capability. It is
	// reachable only from Go - no CLI flag, no HTTP route, no DTO - so `ao spawn`
	// still creates exactly one session for every task size and nothing a human
	// touches can form a crew by accident. Leave both zero for every ordinary
	// spawn.
	//
	// CrewOf names the DEV member. CrewRole must be a non-dev role (dev is the
	// session being joined, and one-dev-per-crew is a database invariant).
	CrewOf   domain.SessionID
	CrewRole domain.CrewRole
}

// TodoSpecPatch carries the editable fields of a prepared TODO. A nil pointer
// leaves that field unchanged; a non-nil pointer sets it (including to empty).
type TodoSpecPatch struct {
	DisplayName    *string
	Harness        *domain.AgentHarness
	Branch         *string
	BaseBranch     *string
	PRTarget       *string
	Prompt         *string
	AutoNameBranch *bool
}
