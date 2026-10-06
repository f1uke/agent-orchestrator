// Package children owns a worker's child worktrees: the folders AO creates for
// subagents Claude Code launches with `isolation: "worktree"`, the merge of
// their commits back into the worker's branch when they stop, and the cleanup
// afterwards.
//
// Claude Code hands worktree creation to AO through its WorktreeCreate hook and
// never removes a worktree a hook created, so every child's lifecycle runs
// through here: Create (WorktreeCreate), Brief (SubagentStart), Stop
// (SubagentStop), Notes (the worker's own UserPromptSubmit and PostToolUse),
// and the teardown, restore and daemon-start paths that settle children whose
// subagent can no longer stop by itself.
//
// The rule every path keeps: a child's work is never dropped. It either reaches
// the worker's branch, or stays committed on the child's kept branch.
package children

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Errors a caller maps to a response.
var (
	ErrSessionNotFound = errors.New("children: session not found")
	ErrChildNotFound   = errors.New("children: child not found")
	ErrNotWorker       = errors.New("children: only a live worker session can have child worktrees")
	ErrBadName         = errors.New("children: not a subagent worktree name")
	ErrNested          = errors.New("children: a child cannot have child worktrees of its own; launch it without isolation")
	ErrForeignCwd      = errors.New("children: the request did not come from the worker's worktree")
)

// BranchPrefix namespaces child branches. It is deliberately outside the
// worker's own branch (git refuses feature/x/child while feature/x exists) and
// outside ao/<id>/, which AO attributes to pull requests.
const BranchPrefix = "ao-child"

// Store is the persistence the service needs.
type Store interface {
	GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error)
	GetProject(ctx context.Context, id string) (domain.ProjectRecord, bool, error)
	InsertSessionChild(ctx context.Context, c domain.SessionChild) error
	GetSessionChild(ctx context.Context, sessionID domain.SessionID, agentID string) (domain.SessionChild, bool, error)
	ListSessionChildren(ctx context.Context, sessionID domain.SessionID) ([]domain.SessionChild, error)
	ListSessionChildrenInStates(ctx context.Context, states ...domain.ChildState) ([]domain.SessionChild, error)
	UpdateSessionChild(ctx context.Context, c domain.SessionChild) (bool, error)
}

// Options configures a Service.
type Options struct {
	Store Store
	Trees ports.ChildTrees
	// Root is where child folders go: <Root>/<project>/<session>/<agent>.
	Root string
	// Provision prepares a new child folder the way a worker's is prepared
	// (project symlinks and postCreate commands). Nil skips it.
	Provision func(ctx context.Context, project domain.ProjectRecord, path string) error
	Now       func() time.Time
	Logger    *slog.Logger
}

// Service implements the child lifecycle.
type Service struct {
	store     Store
	trees     ports.ChildTrees
	root      string
	provision func(ctx context.Context, project domain.ProjectRecord, path string) error
	now       func() time.Time
	log       *slog.Logger

	mu    sync.Mutex
	locks map[domain.SessionID]*sync.Mutex
}

// New builds a Service.
func New(opts Options) *Service {
	now := opts.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	log := opts.Logger
	if log == nil {
		log = slog.Default()
	}
	return &Service{
		store: opts.Store, trees: opts.Trees, root: opts.Root, provision: opts.Provision,
		now: now, log: log, locks: map[domain.SessionID]*sync.Mutex{},
	}
}

// lock serializes everything touching one worker's children, so two children
// stopping together merge one after the other into the worker's tree.
func (s *Service) lock(id domain.SessionID) func() {
	s.mu.Lock()
	l, ok := s.locks[id]
	if !ok {
		l = &sync.Mutex{}
		s.locks[id] = l
	}
	s.mu.Unlock()
	l.Lock()
	return l.Unlock
}

// CreateInput is what Claude Code's WorktreeCreate hook reports.
type CreateInput struct {
	// Name is "agent-<id>" for a subagent.
	Name string
	// Cwd is the session's working directory: the worker's worktree for a
	// direct child.
	Cwd string
}

// Create cuts a child worktree from the worker's HEAD and records it. Calling
// it again for the same subagent returns the existing child.
func (s *Service) Create(ctx context.Context, id domain.SessionID, in CreateInput) (domain.SessionChild, error) {
	agentID, ok := domain.ParseChildAgentName(in.Name)
	if !ok {
		return domain.SessionChild{}, fmt.Errorf("%w: %q", ErrBadName, in.Name)
	}
	unlock := s.lock(id)
	defer unlock()
	rec, err := s.liveWorker(ctx, id)
	if err != nil {
		return domain.SessionChild{}, err
	}
	if existing, ok, err := s.store.GetSessionChild(ctx, id, agentID); err != nil {
		return domain.SessionChild{}, err
	} else if ok {
		return existing, nil
	}
	if err := s.checkCwd(rec, in.Cwd); err != nil {
		return domain.SessionChild{}, err
	}
	project, ok, err := s.store.GetProject(ctx, string(rec.ProjectID))
	if err != nil {
		return domain.SessionChild{}, err
	}
	if !ok {
		return domain.SessionChild{}, fmt.Errorf("children: project %s not found", rec.ProjectID)
	}
	path := filepath.Join(s.root, string(rec.ProjectID), string(id), agentID)
	branch := BranchPrefix + "/" + string(id) + "/" + agentID
	created, err := s.trees.Create(ctx, ports.ChildTreeSpec{WorkerPath: rec.Metadata.WorkspacePath, Path: path, Branch: branch})
	if err != nil {
		return domain.SessionChild{}, err
	}
	if s.provision != nil {
		if err := s.provision(ctx, project, path); err != nil {
			if rmErr := s.trees.Remove(ctx, rec.Metadata.WorkspacePath, path, branch, true); rmErr != nil {
				s.log.Warn("children: remove a child whose provisioning failed", "session", id, "agent", agentID, "error", rmErr)
			}
			return domain.SessionChild{}, fmt.Errorf("children: provision %s: %w", path, err)
		}
	}
	now := s.now()
	child := domain.SessionChild{
		SessionID: id, ProjectID: rec.ProjectID, AgentID: agentID,
		Branch: branch, TargetBranch: created.TargetBranch, BaseSHA: created.BaseSHA, BaseDirty: created.WorkerDirty,
		WorktreePath: path, State: domain.ChildRunning, NotifiedState: domain.ChildRunning,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := s.store.InsertSessionChild(ctx, child); err != nil {
		return domain.SessionChild{}, err
	}
	return child, nil
}

// checkCwd confines creation to the worker's own worktree. A cwd inside one of
// the worker's children is a grandchild, which v1 refuses; anything else (the
// primary checkout, which is where EnterWorktree reports from) is not a child of
// this worker at all.
func (s *Service) checkCwd(rec domain.SessionRecord, cwd string) error {
	if within(cwd, s.childDir(rec)) {
		return ErrNested
	}
	if !within(cwd, rec.Metadata.WorkspacePath) {
		return fmt.Errorf("%w: %s is outside %s", ErrForeignCwd, cwd, rec.Metadata.WorkspacePath)
	}
	return nil
}

func (s *Service) childDir(rec domain.SessionRecord) string {
	return filepath.Join(s.root, string(rec.ProjectID), string(rec.ID))
}

// within reports whether path is root or inside it, comparing physical paths so
// a symlinked temp dir (macOS /var -> /private/var) still matches.
func within(path, root string) bool {
	if path == "" || root == "" {
		return false
	}
	p, r := physical(path), physical(root)
	rel, err := filepath.Rel(r, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func physical(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	// Resolve the longest existing prefix: a child folder may not exist yet.
	dir, rest := abs, ""
	for {
		if resolved, err := filepath.EvalSymlinks(dir); err == nil {
			return filepath.Join(resolved, rest)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return abs
		}
		rest = filepath.Join(filepath.Base(dir), rest)
		dir = parent
	}
}

func (s *Service) liveWorker(ctx context.Context, id domain.SessionID) (domain.SessionRecord, error) {
	rec, ok, err := s.store.GetSession(ctx, id)
	if err != nil {
		return domain.SessionRecord{}, err
	}
	if !ok {
		return domain.SessionRecord{}, ErrSessionNotFound
	}
	if rec.Kind != domain.KindWorker || rec.IsTerminated || rec.Metadata.WorkspacePath == "" {
		return domain.SessionRecord{}, ErrNotWorker
	}
	return rec, nil
}

// Brief is the standing context a child gets through SubagentStart: subagents
// do not inherit the worker's system prompt, so this is the only way AO's rules
// reach them. ok=false when the subagent is not one of this worker's children
// (it runs in the worker's own tree and needs no brief).
func (s *Service) Brief(ctx context.Context, id domain.SessionID, agentID string) (string, bool, error) {
	child, ok, err := s.store.GetSessionChild(ctx, id, agentID)
	if err != nil || !ok {
		return "", false, err
	}
	ios := false
	if project, ok, err := s.store.GetProject(ctx, string(child.ProjectID)); err == nil && ok {
		ios = project.Config.HasIOSSimulator
	}
	return renderBrief(child, ios), true, nil
}

// DerivedDataPath is where an iOS child's xcodebuild output goes. It sits beside
// the child's folder, outside the repository, and is deleted with the child.
func DerivedDataPath(child domain.SessionChild) string {
	return child.WorktreePath + ".derived"
}

// StopOutcome is the answer to a SubagentStop.
type StopOutcome struct {
	// Known is false when the subagent is not a child (no isolated worktree):
	// its stop is none of AO's business.
	Known bool
	// Block asks Claude Code to keep the subagent going, with Reason as its
	// next instruction.
	Block  bool
	Reason string
	Child  domain.SessionChild
}

// Stop settles a child whose subagent is stopping: it asks the child to commit
// or to resolve a conflict while the child can still do it with full context,
// then merges its commits into the worker's branch.
func (s *Service) Stop(ctx context.Context, id domain.SessionID, agentID string) (StopOutcome, error) {
	unlock := s.lock(id)
	defer unlock()
	child, ok, err := s.store.GetSessionChild(ctx, id, agentID)
	if err != nil {
		return StopOutcome{}, err
	}
	if !ok {
		return StopOutcome{}, nil
	}
	if child.State != domain.ChildRunning {
		return StopOutcome{Known: true, Child: child}, nil
	}
	rec, ok, err := s.store.GetSession(ctx, id)
	if err != nil {
		return StopOutcome{}, err
	}
	if !ok {
		return StopOutcome{}, ErrSessionNotFound
	}
	out, err := s.finish(ctx, rec, child, true)
	out.Known = true
	return out, err
}

// finish runs the stop decision until it reaches a state that needs nothing
// more from AO right now. mayBlock is false when no subagent is left to ask.
func (s *Service) finish(ctx context.Context, rec domain.SessionRecord, child domain.SessionChild, mayBlock bool) (StopOutcome, error) {
	worker := rec.Metadata.WorkspacePath
	for range 2 {
		facts, err := s.trees.Inspect(ctx, child.WorktreePath, child.BaseSHA)
		if err != nil {
			return s.hold(ctx, child, "AO could not read the child's worktree: "+err.Error())
		}
		child.Commits, child.FilesChanged = facts.Commits, facts.FilesChanged
		var conflicts []string
		if !facts.Dirty && facts.Commits > 0 {
			conflicts, err = s.trees.Conflicts(ctx, worker, child.TargetBranch, child.Branch)
			if err != nil {
				return s.hold(ctx, child, "AO could not check the merge for conflicts: "+err.Error())
			}
		}
		action := domain.DecideChildStop(domain.ChildStopFacts{
			Dirty: facts.Dirty, Commits: facts.Commits, Conflicts: conflicts, Blocks: child.StopBlocks,
		}, mayBlock)
		switch action {
		case domain.ChildStopBlockCommit:
			return s.block(ctx, child, fmt.Sprintf(
				"AO: your worktree %s has uncommitted changes. Commit them on your branch (git add -A && git commit -m \"<what you did>\"), then finish. When you stop, AO merges your commits into %s.",
				child.WorktreePath, child.TargetBranch))
		case domain.ChildStopBlockRebase:
			return s.block(ctx, child, fmt.Sprintf(
				"AO: your commits conflict with %s in %s. In your worktree run `git rebase %s`, resolve the conflicts, finish the rebase, then stop again.",
				child.TargetBranch, strings.Join(conflicts, ", "), child.TargetBranch))
		case domain.ChildStopAutoCommit:
			if err := s.trees.CommitAll(ctx, child.WorktreePath, "AO: commit work subagent "+child.AgentID+" left uncommitted"); err != nil {
				return s.hold(ctx, child, "AO could not commit the work the child left uncommitted: "+err.Error())
			}
			continue
		case domain.ChildStopRemove:
			if err := s.trees.Remove(ctx, worker, child.WorktreePath, child.Branch, true); err != nil {
				return s.hold(ctx, child, "the child finished with no commits, but AO could not remove its worktree: "+err.Error())
			}
			s.removeDerivedData(child)
			child.State, child.Detail = domain.ChildRemoved, ""
			return s.settle(ctx, child)
		case domain.ChildStopPark:
			child.State = domain.ChildConflict
			child.Detail = fmt.Sprintf("conflicts with %s in %s", child.TargetBranch, strings.Join(conflicts, ", "))
			return s.settle(ctx, child)
		case domain.ChildStopMerge:
			return s.merge(ctx, rec, child)
		}
	}
	return s.hold(ctx, child, "the child's worktree still has uncommitted changes after AO committed them")
}

func (s *Service) block(ctx context.Context, child domain.SessionChild, reason string) (StopOutcome, error) {
	child.StopBlocks++
	child.UpdatedAt = s.now()
	if _, err := s.store.UpdateSessionChild(ctx, child); err != nil {
		return StopOutcome{}, err
	}
	return StopOutcome{Block: true, Reason: reason, Child: child}, nil
}

func (s *Service) hold(ctx context.Context, child domain.SessionChild, detail string) (StopOutcome, error) {
	child.State, child.Detail = domain.ChildHeld, detail
	return s.settle(ctx, child)
}

// settle records a state the child rests in until something else happens.
func (s *Service) settle(ctx context.Context, child domain.SessionChild) (StopOutcome, error) {
	now := s.now()
	child.UpdatedAt = now
	if child.State != domain.ChildRunning && child.State != domain.ChildMerging {
		child.FinishedAt = &now
	}
	if _, err := s.store.UpdateSessionChild(ctx, child); err != nil {
		return StopOutcome{}, err
	}
	return StopOutcome{Child: child}, nil
}

func (s *Service) merge(ctx context.Context, rec domain.SessionRecord, child domain.SessionChild) (StopOutcome, error) {
	worker := rec.Metadata.WorkspacePath
	head, err := s.trees.Head(ctx, worker)
	if err != nil {
		return s.hold(ctx, child, "AO could not read the worker's HEAD: "+err.Error())
	}
	child.State, child.MergeHeadBefore, child.UpdatedAt = domain.ChildMerging, head, s.now()
	if _, err := s.store.UpdateSessionChild(ctx, child); err != nil {
		return StopOutcome{}, err
	}
	res, err := s.trees.Merge(ctx, worker, child.TargetBranch, child.Branch, mergeMessage(child))
	if err != nil {
		return s.hold(ctx, child, "git merge could not run: "+err.Error())
	}
	if !res.Merged {
		return s.hold(ctx, child, res.Held)
	}
	return s.merged(ctx, worker, child, res.SHA)
}

// merged records a child whose commits are on the worker's branch and removes
// what is left of it. A failed cleanup is logged and named, never a reason to
// report the merge as anything but done.
func (s *Service) merged(ctx context.Context, worker string, child domain.SessionChild, sha string) (StopOutcome, error) {
	child.State, child.MergedSHA, child.Detail = domain.ChildMerged, sha, ""
	if err := s.trees.Remove(ctx, worker, child.WorktreePath, child.Branch, true); err != nil {
		s.log.Warn("children: remove a merged child", "session", child.SessionID, "agent", child.AgentID, "error", err)
		child.Detail = "merged, but AO could not remove its worktree: " + err.Error()
	}
	s.removeDerivedData(child)
	return s.settle(ctx, child)
}

func (s *Service) removeDerivedData(child domain.SessionChild) {
	dd := DerivedDataPath(child)
	if !within(dd, s.root) {
		return
	}
	if err := os.RemoveAll(dd); err != nil {
		s.log.Warn("children: remove a child's DerivedData", "path", dd, "error", err)
	}
}

func mergeMessage(child domain.SessionChild) string {
	label := child.AgentType
	if label == "" {
		label = child.AgentID
	}
	if child.Description != "" {
		return fmt.Sprintf("Merge subagent %s: %s", label, child.Description)
	}
	return "Merge subagent " + label
}

// Describe records what the worker called the subagent, read from its Agent
// tool call, so the board and the merge commit can say what the child did.
func (s *Service) Describe(ctx context.Context, id domain.SessionID, agentID, agentType, description string) error {
	unlock := s.lock(id)
	defer unlock()
	child, ok, err := s.store.GetSessionChild(ctx, id, agentID)
	if err != nil || !ok {
		return err
	}
	if child.AgentType == agentType && child.Description == description {
		return nil
	}
	if agentType != "" {
		child.AgentType = agentType
	}
	if description != "" {
		child.Description = description
	}
	child.UpdatedAt = s.now()
	_, err = s.store.UpdateSessionChild(ctx, child)
	return err
}

// Notes retries held merges and returns what the worker has not yet been told
// about its children, marking it told. It runs on the worker's own
// UserPromptSubmit and PostToolUse, which is how a held merge unblocks once the
// worker commits the overlapping files.
func (s *Service) Notes(ctx context.Context, id domain.SessionID) ([]string, error) {
	unlock := s.lock(id)
	defer unlock()
	children, err := s.store.ListSessionChildren(ctx, id)
	if err != nil || len(children) == 0 {
		return nil, err
	}
	rec, ok, err := s.store.GetSession(ctx, id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrSessionNotFound
	}
	var notes []string
	for _, child := range children {
		if child.State == domain.ChildHeld && !rec.IsTerminated {
			if out, err := s.retryHeld(ctx, rec, child); err != nil {
				s.log.Warn("children: retry a held merge", "session", id, "agent", child.AgentID, "error", err)
			} else {
				child = out.Child
			}
		}
		if child.State == child.NotifiedState || child.State == domain.ChildMerging {
			continue
		}
		notes = append(notes, renderNote(child))
		child.NotifiedState = child.State
		child.UpdatedAt = s.now()
		if _, err := s.store.UpdateSessionChild(ctx, child); err != nil {
			return nil, err
		}
	}
	return notes, nil
}

func (s *Service) retryHeld(ctx context.Context, rec domain.SessionRecord, child domain.SessionChild) (StopOutcome, error) {
	conflicts, err := s.trees.Conflicts(ctx, rec.Metadata.WorkspacePath, child.TargetBranch, child.Branch)
	if err != nil {
		return StopOutcome{Child: child}, err
	}
	if len(conflicts) > 0 {
		child.State = domain.ChildConflict
		child.Detail = fmt.Sprintf("conflicts with %s in %s", child.TargetBranch, strings.Join(conflicts, ", "))
		return s.settle(ctx, child)
	}
	return s.merge(ctx, rec, child)
}

// List returns a worker's children.
func (s *Service) List(ctx context.Context, id domain.SessionID) ([]domain.SessionChild, error) {
	return s.store.ListSessionChildren(ctx, id)
}

// Undelivered lists a worker's children whose work is not yet on its branch
// nor set aside on a kept branch. A teardown that asks first refuses on them.
func (s *Service) Undelivered(ctx context.Context, id domain.SessionID) ([]domain.SessionChild, error) {
	children, err := s.store.ListSessionChildren(ctx, id)
	if err != nil {
		return nil, err
	}
	var out []domain.SessionChild
	for _, c := range children {
		if c.State.Undelivered() {
			out = append(out, c)
		}
	}
	return out, nil
}

// SettleForTeardown sets aside every undelivered child of a worker that is
// being torn down: its leftovers are committed onto its branch, the branch is
// kept, and the folder is removed. Nothing is merged into a worker that is going
// away and nothing is deleted. A child that cannot be preserved is left exactly
// as it is and reported.
func (s *Service) SettleForTeardown(ctx context.Context, id domain.SessionID) error {
	unlock := s.lock(id)
	defer unlock()
	rec, ok, err := s.store.GetSession(ctx, id)
	if err != nil || !ok {
		return err
	}
	children, err := s.store.ListSessionChildren(ctx, id)
	if err != nil {
		return err
	}
	var errs []error
	for _, child := range children {
		if !child.State.Undelivered() {
			continue
		}
		if err := s.preserve(ctx, rec.Metadata.WorkspacePath, child); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (s *Service) preserve(ctx context.Context, worker string, child domain.SessionChild) error {
	facts, inspectErr := s.trees.Inspect(ctx, child.WorktreePath, child.BaseSHA)
	if inspectErr == nil && !facts.Dirty && facts.Commits == 0 {
		if err := s.trees.Remove(ctx, worker, child.WorktreePath, child.Branch, true); err == nil {
			s.removeDerivedData(child)
			child.State, child.Detail = domain.ChildRemoved, ""
			_, err = s.settle(ctx, child)
			return err
		}
	}
	committed, err := s.trees.Preserve(ctx, worker, child.WorktreePath, child.Branch, "AO: preserve work of subagent "+child.AgentID+" when its worker ended")
	if err != nil {
		return fmt.Errorf("children: preserve %s/%s: %w", child.SessionID, child.AgentID, err)
	}
	s.removeDerivedData(child)
	child.State = domain.ChildPreserved
	child.Detail = "kept on branch " + child.Branch
	if committed {
		child.Detail += " (AO committed the changes it left uncommitted)"
	}
	_, err = s.settle(ctx, child)
	return err
}

// SettleOrphans finishes the children of a worker whose Claude Code process is
// being relaunched: a subagent lives inside that process, so none of them can
// stop by itself any more. They go through the stop path without being asked.
func (s *Service) SettleOrphans(ctx context.Context, id domain.SessionID) error {
	unlock := s.lock(id)
	defer unlock()
	rec, ok, err := s.store.GetSession(ctx, id)
	if err != nil || !ok {
		return err
	}
	children, err := s.store.ListSessionChildren(ctx, id)
	if err != nil {
		return err
	}
	var errs []error
	for _, child := range children {
		if child.State != domain.ChildRunning {
			continue
		}
		if _, err := s.finish(ctx, rec, child, false); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Reconcile runs at daemon start. A child left `merging` was interrupted by a
// crash, so its merge is settled from what git shows; an undelivered child of a
// worker that has since ended is preserved.
func (s *Service) Reconcile(ctx context.Context) error {
	children, err := s.store.ListSessionChildrenInStates(ctx, domain.ChildRunning, domain.ChildMerging, domain.ChildHeld, domain.ChildConflict)
	if err != nil {
		return err
	}
	var errs []error
	for _, child := range children {
		if err := s.reconcileOne(ctx, child); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (s *Service) reconcileOne(ctx context.Context, child domain.SessionChild) error {
	unlock := s.lock(child.SessionID)
	defer unlock()
	rec, ok, err := s.store.GetSession(ctx, child.SessionID)
	if err != nil || !ok {
		return err
	}
	worker := rec.Metadata.WorkspacePath
	if child.State == domain.ChildMerging {
		res, err := s.trees.RecoverMerge(ctx, worker, child.Branch, child.MergeHeadBefore)
		var out StopOutcome
		switch {
		case err != nil:
			out, err = s.hold(ctx, child, "AO's merge was interrupted and could not be checked: "+err.Error())
		case res.Merged:
			out, err = s.merged(ctx, worker, child, res.SHA)
		default:
			out, err = s.hold(ctx, child, res.Held)
		}
		if err != nil {
			return err
		}
		child = out.Child
	}
	if rec.IsTerminated && child.State.Undelivered() {
		return s.preserve(ctx, worker, child)
	}
	return nil
}
