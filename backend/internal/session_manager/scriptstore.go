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
	"github.com/aoagents/agent-orchestrator/backend/internal/prompts"
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
	HasFile(ctx context.Context, root, base, path string) (bool, error)
}

// SetScriptsStore wires the scripts store worktree service. Nil leaves every
// mobileScripts worker on the store's main checkout, as before it existed.
func (m *Manager) SetScriptsStore(s ScriptsStore) {
	m.scripts = s
}

// scriptsStorePlan is where a workspace's scripts go. Isolated means the
// workspace has (Cut) or, at spawn and restore, is about to have its own
// worktree at Path on Branch, cut from Base. Otherwise the agent works in Root,
// the store's main checkout, and Reason says why; both are empty when the
// project has no mobileScripts at all.
type scriptsStorePlan struct {
	Root, Path, Branch, Base string
	Isolated, Cut            bool
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
		return cutPlan(w)
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

// promptScripts is the scripts store a worker's prompt names. The prompt is
// built before a spawned session has an id, so an isolated store is named by
// $AO_SCRIPTS_STORE, which the runtime env points at the worktree, never by its
// path. owner is empty for a session spawned to own its workspace: its
// worktree is about to be cut, so the store's probe decides. A known owner
// (a crew member's dev, or a restored owner, re-attached before this runs)
// is isolated only when its worktree exists: the env falls back to the main
// checkout otherwise, and the prompt must say the same.
func (m *Manager) promptScripts(ctx context.Context, project domain.ProjectRecord, owner domain.SessionID) prompts.MobileScripts {
	ms := *project.Config.MobileScripts
	plan := scriptsStorePlan{Root: scriptstore.Root(ms)}
	switch {
	case owner == "":
		plan = m.planScriptsStore(ctx, project, owner)
	case m.scripts != nil:
		if w, ok, err := m.scripts.Get(ctx, owner); err == nil && ok {
			plan = cutPlan(w)
		}
	}
	out := prompts.MobileScripts{Product: ms.Product, IOS: ms.Platform == domain.MobilePlatformIOS, Store: plan.Root, Root: plan.Root}
	if plan.Isolated {
		out.Store, out.Isolated, out.Base = "$"+EnvScriptsStore, true, plan.Base
	}
	if ms.VerifySkill != "" && m.hasVerifySkill(ctx, ms.VerifySkill, plan) {
		out.Skill = out.Store + "/" + strings.TrimSuffix(filepath.ToSlash(ms.VerifySkill), "/")
	}
	return out
}

// cutPlan is the plan of a worktree that exists.
func cutPlan(w domain.ScriptsStoreWorktree) scriptsStorePlan {
	return scriptsStorePlan{Root: w.Store, Path: w.Path, Branch: w.Branch, Base: w.BaseBranch, Isolated: true, Cut: true}
}

// hasVerifySkill reports whether the checkout a plan names holds the verify
// skill's SKILL.md: a cut worktree or the main checkout on disk, a worktree
// about to be cut at the base it will be cut from. The prompt and the link
// both ask it, so a prompt never sends an agent to a skill its store lacks;
// without one the agent gets the full device guidance instead.
func (m *Manager) hasVerifySkill(ctx context.Context, skill string, plan scriptsStorePlan) bool {
	rel := filepath.Join(filepath.FromSlash(skill), "SKILL.md")
	switch {
	case plan.Cut:
		return isFile(filepath.Join(plan.Path, rel))
	case plan.Isolated:
		ok, err := m.scripts.HasFile(ctx, plan.Root, plan.Base, filepath.ToSlash(rel))
		if err != nil {
			m.logger.Warn("scripts store: read the verify skill at the store's base", "store", plan.Root, "base", plan.Base, "error", err)
		}
		return ok
	default:
		return isFile(filepath.Join(plan.Root, rel))
	}
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
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
	return cutPlan(w), nil
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
	if !m.hasVerifySkill(ctx, ms.VerifySkill, cutPlan(w)) {
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
	if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
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
	if err := os.MkdirAll(filepath.Dir(exclude), 0o750); err != nil {
		return err
	}
	f, err := os.OpenFile(exclude, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
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
