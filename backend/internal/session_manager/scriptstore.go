package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/scriptstore"
)

// verifySkillLink is where a worktree's agent loads the project's verify skill
// from.
const verifySkillLink = ".claude/skills/verify"

// ScriptsStore is what the manager needs from the scripts store worktree
// service.
type ScriptsStore interface {
	Probe(ctx context.Context, root string) (ports.ScriptsStoreProbe, error)
	Get(ctx context.Context, owner domain.SessionID) (domain.ScriptsStoreWorktree, bool, error)
	Ensure(ctx context.Context, project domain.ProjectID, owner domain.SessionID, root, base string) (domain.ScriptsStoreWorktree, error)
	Settle(ctx context.Context, owner domain.SessionID, policy scriptstore.Policy) (scriptstore.Settlement, error)
}

// SetScriptsStore wires the scripts store worktree service. Nil leaves every
// mobileScripts worker on the store's main checkout, as before it existed.
func (m *Manager) SetScriptsStore(s ScriptsStore) {
	m.scripts = s
}

// scriptsStorePlan is where a workspace's scripts go. Isolated means the
// workspace has (or, at spawn and restore, is about to have) its own worktree
// at Path on Branch, cut from Base. Otherwise the agent works in Root, the
// store's main checkout, and Reason says why; both are empty when the project
// has no mobileScripts at all.
type scriptsStorePlan struct {
	Root, Path, Branch, Base string
	Isolated                 bool
	Reason                   string
}

// planScriptsStore answers where the owner's scripts go without changing
// anything, so a system prompt can name the worktree before it exists. An
// existing worktree wins; otherwise the store is probed and the worktree laid
// out where Ensure will cut it.
func (m *Manager) planScriptsStore(ctx context.Context, project domain.ProjectRecord, owner domain.SessionID) scriptsStorePlan {
	ms := project.Config.MobileScripts
	if ms == nil {
		return scriptsStorePlan{}
	}
	plan := scriptsStorePlan{Root: scriptstore.Root(*ms)}
	if m.scripts == nil {
		plan.Reason = "this daemon gives no session its own scripts store worktree"
		return plan
	}
	w, ok, err := m.scripts.Get(ctx, owner)
	if err != nil {
		m.logger.Warn("scripts store: read the workspace's worktree", "owner", owner, "error", err)
	} else if ok {
		return scriptsStorePlan{Root: w.Store, Path: w.Path, Branch: w.Branch, Base: w.BaseBranch, Isolated: true}
	}
	probe, err := m.scripts.Probe(ctx, plan.Root)
	switch {
	case err != nil:
		plan.Reason = err.Error()
	case !probe.OK:
		plan.Reason = probe.Reason
	default:
		plan.Path, plan.Branch = scriptstore.Layout(m.dataDir, plan.Root, owner)
		plan.Base, plan.Isolated = probe.Base, true
	}
	return plan
}

// ensureScriptsStore makes the owner's worktree exist when its plan says it
// should, and returns the plan it acted on. A plan that is not isolated is
// logged, never an error: the agent then works in the main checkout.
func (m *Manager) ensureScriptsStore(ctx context.Context, project domain.ProjectRecord, owner domain.SessionID) (scriptsStorePlan, error) {
	plan := m.planScriptsStore(ctx, project, owner)
	if !plan.Isolated {
		if plan.Reason != "" {
			m.logger.Warn("scripts store: this workspace writes into the store's main checkout",
				"owner", owner, "store", plan.Root, "reason", plan.Reason)
		}
		return plan, nil
	}
	w, err := m.scripts.Ensure(ctx, domain.ProjectID(project.ID), owner, plan.Root, plan.Base)
	if err != nil {
		return plan, err
	}
	return scriptsStorePlan{Root: w.Store, Path: w.Path, Branch: w.Branch, Base: w.BaseBranch, Isolated: true}, nil
}

// scriptsOwner is the session whose store worktree a session uses: its crew's
// dev for a crew member, itself otherwise.
func scriptsOwner(rec domain.SessionRecord) domain.SessionID {
	if rec.InCrew() {
		return rec.CrewID
	}
	return rec.ID
}

// scriptsStoreEnv is the AO_SCRIPTS_STORE value for a worker of the owner's
// workspace: the worktree when it exists, the main checkout otherwise, and
// nothing for a project without mobileScripts.
func (m *Manager) scriptsStoreEnv(ctx context.Context, project domain.ProjectRecord, kind domain.SessionKind, owner domain.SessionID) string {
	if kind != domain.KindWorker || project.Config.MobileScripts == nil {
		return ""
	}
	if m.scripts != nil {
		if w, ok, err := m.scripts.Get(ctx, owner); err == nil && ok {
			return w.Path
		}
	}
	return scriptstore.Root(*project.Config.MobileScripts)
}

// attachScriptsStore is the restore half: the owner re-attaches its worktree
// (a crew member uses the owner's as it is), and the verify skill link is laid
// again. Nothing here fails a restore.
func (m *Manager) attachScriptsStore(ctx context.Context, project domain.ProjectRecord, rec domain.SessionRecord, workspacePath string) {
	if rec.Kind != domain.KindWorker || project.Config.MobileScripts == nil {
		return
	}
	owner := scriptsOwner(rec)
	if owner == rec.ID {
		if _, err := m.ensureScriptsStore(ctx, project, owner); err != nil {
			m.logger.Warn("restore: re-attach the scripts store worktree; the agent writes into the main checkout", "sessionID", rec.ID, "error", err)
		}
	}
	m.linkVerifySkill(ctx, project, owner, workspacePath)
}

// linkVerifySkill links the store worktree's copy of the project's verify
// skill into the workspace as .claude/skills/verify, so the skill an agent
// loads is its own task's copy: an edit to the skill and the scripts it names
// are one commit and one publish.
//
// It is idempotent. An existing entry is left alone (the project's own
// symlinks, a committed skill, a link a person made), except a link AO made
// into a store worktree that no longer exists, which is replaced. A missing
// skill folder is a warning: the agent then has no verify skill, which is
// what it had before.
func (m *Manager) linkVerifySkill(ctx context.Context, project domain.ProjectRecord, owner domain.SessionID, workspacePath string) {
	ms := project.Config.MobileScripts
	if ms == nil || ms.VerifySkill == "" || m.scripts == nil || workspacePath == "" {
		return
	}
	w, ok, err := m.scripts.Get(ctx, owner)
	if err != nil || !ok {
		return
	}
	source := filepath.Join(w.Path, filepath.FromSlash(ms.VerifySkill))
	if info, err := os.Stat(filepath.Join(source, "SKILL.md")); err != nil || info.IsDir() {
		m.logger.Warn("scripts store: the project's verify skill is missing from the store worktree; no verify skill is linked",
			"owner", owner, "skill", source)
		return
	}
	target := filepath.Join(workspacePath, filepath.FromSlash(verifySkillLink))
	if existing, err := os.Readlink(target); err == nil {
		if existing == source || !m.staleStoreLink(existing) {
			return
		}
		if err := os.Remove(target); err != nil {
			m.logger.Warn("scripts store: replace a stale verify skill link", "target", target, "error", err)
			return
		}
	} else if _, statErr := os.Lstat(target); !errors.Is(statErr, fs.ErrNotExist) {
		return
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		m.logger.Warn("scripts store: create the skills folder", "path", filepath.Dir(target), "error", err)
		return
	}
	if err := os.Symlink(source, target); err != nil {
		m.logger.Warn("scripts store: link the verify skill", "target", target, "error", err)
		return
	}
	if err := excludeFromGit(ctx, workspacePath, verifySkillLink); err != nil {
		m.logger.Warn("scripts store: keep the verify skill link out of git status", "workspace", workspacePath, "error", err)
	}
}

// staleStoreLink reports whether a link points into AO's store worktrees and
// its destination is gone: the only kind of existing link AO replaces.
func (m *Manager) staleStoreLink(dest string) bool {
	root := filepath.Join(m.dataDir, "store-worktrees") + string(filepath.Separator)
	if !strings.HasPrefix(filepath.Clean(dest), root) {
		return false
	}
	_, err := os.Stat(dest)
	return errors.Is(err, fs.ErrNotExist)
}

// excludeFromGit appends rel to the repository's info/exclude unless git
// already ignores it, so the link never shows up as an untracked file a
// commit could pick up.
func excludeFromGit(ctx context.Context, workspacePath, rel string) error {
	err := aoprocess.CommandContext(ctx, "git", "-C", workspacePath, "check-ignore", "-q", rel).Run()
	if err == nil {
		return nil
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
		return fmt.Errorf("git check-ignore %s: %w", rel, err)
	}
	out, err := aoprocess.CommandContext(ctx, "git", "-C", workspacePath, "rev-parse", "--path-format=absolute", "--git-common-dir").Output()
	if err != nil {
		return fmt.Errorf("git rev-parse --git-common-dir: %w", err)
	}
	exclude := filepath.Join(strings.TrimSpace(string(out)), "info", "exclude")
	body, err := os.ReadFile(exclude)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	for _, line := range strings.Split(string(body), "\n") {
		if strings.TrimSpace(line) == rel {
			return nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(exclude), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(exclude, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	prefix := ""
	if len(body) > 0 && !strings.HasSuffix(string(body), "\n") {
		prefix = "\n"
	}
	_, werr := f.WriteString(prefix + rel + "\n")
	return errors.Join(werr, f.Close())
}

// ownsScriptsStore reports whether tearing this session down settles its
// workspace's store worktree: a worker that owns its workspace does; a crew
// member never does, the owner's teardown does.
func (m *Manager) ownsScriptsStore(rec domain.SessionRecord) bool {
	return m.scripts != nil && rec.Kind == domain.KindWorker && rec.OwnsCrewWorkspace()
}
