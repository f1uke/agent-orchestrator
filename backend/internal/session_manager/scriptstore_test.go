package sessionmanager

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/prompts"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/scriptstore"
)

type settleCall struct {
	owner  domain.SessionID
	policy scriptstore.Policy
}

// fakeScripts stands in for the store worktree service. Ensure lays a row out
// under root and returns it; Settle answers with blocked, recording each call.
// HasFile answers from files, keyed "<base>:<path>".
type fakeScripts struct {
	root      string
	probe     ports.ScriptsStoreProbe
	files     map[string]bool
	rows      map[domain.SessionID]domain.ScriptsStoreWorktree
	ensureErr error
	ensured   []domain.SessionID
	blocked   bool
	settled   []settleCall
}

func newFakeScripts(t *testing.T) *fakeScripts {
	return &fakeScripts{root: t.TempDir(), probe: ports.ScriptsStoreProbe{Base: "main", OK: true}, rows: map[domain.SessionID]domain.ScriptsStoreWorktree{}}
}

func (f *fakeScripts) Probe(context.Context, string) (ports.ScriptsStoreProbe, error) {
	return f.probe, nil
}

func (f *fakeScripts) Get(_ context.Context, owner domain.SessionID) (domain.ScriptsStoreWorktree, bool, error) {
	w, ok := f.rows[owner]
	return w, ok && w.State.Live(), nil
}

func (f *fakeScripts) Ensure(_ context.Context, project domain.ProjectID, owner domain.SessionID, root, base string) (domain.ScriptsStoreWorktree, error) {
	f.ensured = append(f.ensured, owner)
	if f.ensureErr != nil {
		return domain.ScriptsStoreWorktree{}, f.ensureErr
	}
	path := filepath.Join(f.root, string(owner))
	if err := os.MkdirAll(filepath.Join(path, "projects", "nter", "verify"), 0o755); err != nil {
		return domain.ScriptsStoreWorktree{}, err
	}
	if err := os.WriteFile(filepath.Join(path, "projects", "nter", "verify", "SKILL.md"), []byte("verify\n"), 0o600); err != nil {
		return domain.ScriptsStoreWorktree{}, err
	}
	w := domain.ScriptsStoreWorktree{SessionID: owner, ProjectID: project, Store: root, Path: path, Branch: "ao/" + string(owner), BaseBranch: base, State: domain.ScriptsStoreActive}
	f.rows[owner] = w
	return w, nil
}

func (f *fakeScripts) HasFile(_ context.Context, _, base, path string) (bool, error) {
	return f.files[base+":"+path], nil
}

func (f *fakeScripts) Settle(_ context.Context, owner domain.SessionID, policy scriptstore.Policy) (scriptstore.Settlement, error) {
	f.settled = append(f.settled, settleCall{owner, policy})
	w, ok := f.rows[owner]
	if !ok {
		return scriptstore.Settlement{}, nil
	}
	out := scriptstore.Settlement{Present: true, Worktree: w, Blocked: f.blocked}
	if f.blocked {
		out.Worktree.Uncommitted = []string{"projects/nter/draft.yaml"}
	}
	if policy == scriptstore.SettleDiscard || (policy == scriptstore.SettleKeep && !f.blocked) {
		out.Removed = true
		w.State = domain.ScriptsStoreRemoved
		f.rows[owner] = w
	}
	return out, nil
}

func mobileProject(skill string) domain.ProjectRecord {
	cfg := testRoleAgents()
	cfg.MobileScripts = &domain.MobileScriptsConfig{Product: "nter", Platform: domain.MobilePlatformIOS, Store: "/scripts/store", VerifySkill: skill}
	return domain.ProjectRecord{ID: "mer", Config: cfg}
}

// newScriptsManager is a manager on a mobileScripts project whose workspace is
// a real git repository, so the verify skill link and its exclude are real.
func newScriptsManager(t *testing.T, skill string) (*Manager, *fakeStore, *fakeRuntime, *fakeWorkspace, *fakeScripts) {
	t.Helper()
	m, st, rt, ws := newManager()
	m.dataDir = t.TempDir()
	st.projects["mer"] = mobileProject(skill)
	ws.path = filepath.Join(t.TempDir(), "ws")
	if out, err := exec.Command("git", "init", "-q", ws.path).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	scripts := newFakeScripts(t)
	m.SetScriptsStore(scripts)
	return m, st, rt, ws, scripts
}

func TestSpawn_ScriptsStoreCutsAWorktreeExportsItAndLinksTheVerifySkill(t *testing.T) {
	m, _, rt, ws, scripts := newScriptsManager(t, "projects/nter/verify")
	rec, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker})
	if err != nil {
		t.Fatal(err)
	}
	if len(scripts.ensured) != 1 || scripts.ensured[0] != rec.ID {
		t.Fatalf("ensured = %v, want the new worker's own worktree", scripts.ensured)
	}
	row := scripts.rows[rec.ID]
	if got := rt.lastCfg.Env[EnvScriptsStore]; got != row.Path {
		t.Fatalf("%s = %q, want the worktree %q", EnvScriptsStore, got, row.Path)
	}
	link := filepath.Join(ws.path, ".claude", "skills", "verify")
	if dest, err := os.Readlink(link); err != nil || dest != filepath.Join(row.Path, "projects", "nter", "verify") {
		t.Fatalf("verify link = %q, %v; want it to point into the worktree", dest, err)
	}
	m.linkVerifySkill(ctx, mobileProject("projects/nter/verify"), rec.ID, ws.path)
	exclude, err := os.ReadFile(filepath.Join(ws.path, ".git", "info", "exclude"))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(exclude), "\n.claude/skills/verify\n"); n != 1 {
		t.Fatalf("info/exclude names the link %d times, want once:\n%s", n, exclude)
	}
	if out, _ := exec.Command("git", "-C", ws.path, "status", "--porcelain").Output(); len(out) != 0 {
		t.Fatalf("the link shows in git status: %s", out)
	}
}

func TestSpawn_ScriptsStoreThatCannotHaveWorktreesLeavesTheMainCheckout(t *testing.T) {
	m, _, rt, ws, scripts := newScriptsManager(t, "projects/nter/verify")
	scripts.probe = ports.ScriptsStoreProbe{Reason: "the scripts store /scripts/store is not a git repository"}
	if _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker}); err != nil {
		t.Fatalf("spawn: %v; a store without worktrees must not fail it", err)
	}
	if len(scripts.ensured) != 0 {
		t.Fatalf("ensured = %v, want none", scripts.ensured)
	}
	if got := rt.lastCfg.Env[EnvScriptsStore]; got != "/scripts/store" {
		t.Fatalf("%s = %q, want the main checkout", EnvScriptsStore, got)
	}
	if _, err := os.Lstat(filepath.Join(ws.path, ".claude", "skills", "verify")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a verify link was made with no worktree: %v", err)
	}
}

func TestSpawn_ScriptsStoreWorktreeFailureFailsTheSpawn(t *testing.T) {
	m, st, _, ws, scripts := newScriptsManager(t, "")
	scripts.ensureErr = errors.New("branch ao/mer-1 is checked out elsewhere")
	if _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker}); err == nil || !strings.Contains(err.Error(), "scripts store worktree") {
		t.Fatalf("spawn = %v, want it failed over the store worktree", err)
	}
	if ws.destroyed != 1 {
		t.Fatalf("workspace destroyed %d time(s), want the failed spawn's tree removed", ws.destroyed)
	}
	if len(st.sessions) != 0 {
		t.Fatalf("sessions = %v, want the seed row rolled back", st.sessions)
	}
}

func TestSpawn_LaterFailureRemovesTheWorktreeItCut(t *testing.T) {
	m, _, rt, _, scripts := newScriptsManager(t, "")
	rt.createErr = errors.New("tmux refused")
	if _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker}); err == nil {
		t.Fatal("spawn succeeded with a failing runtime")
	}
	if len(scripts.settled) != 1 || scripts.settled[0].policy != scriptstore.SettleKeep {
		t.Fatalf("settled = %v, want the fresh worktree settled away once", scripts.settled)
	}
}

func TestSpawn_ScriptsStoreOnlyForWorkersOfMobileProjects(t *testing.T) {
	m, st, rt, _, scripts := newScriptsManager(t, "")
	if _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindOrchestrator}); err != nil {
		t.Fatal(err)
	}
	if _, ok := rt.lastCfg.Env[EnvScriptsStore]; ok || len(scripts.ensured) != 0 {
		t.Fatalf("an orchestrator got a scripts store (env %v, ensured %v)", rt.lastCfg.Env[EnvScriptsStore], scripts.ensured)
	}
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	if _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker}); err != nil {
		t.Fatal(err)
	}
	if _, ok := rt.lastCfg.Env[EnvScriptsStore]; ok || len(scripts.ensured) != 0 {
		t.Fatal("a worker of a project without mobileScripts got a scripts store")
	}
}

func TestRestore_OwnerReattachesAndMembersUseTheOwnersWorktree(t *testing.T) {
	m, st, rt, ws, scripts := newScriptsManager(t, "projects/nter/verify")
	dev, qa := seedCrew(st)
	for _, rec := range []domain.SessionRecord{dev, qa} {
		rec.IsTerminated = true
		rec.Metadata.WorkspacePath = ws.path
		st.sessions[rec.ID] = rec
	}

	if _, err := m.Restore(ctx, dev.ID); err != nil {
		t.Fatalf("restore dev: %v", err)
	}
	row := scripts.rows[dev.ID]
	if len(scripts.ensured) != 1 || scripts.ensured[0] != dev.ID || rt.lastCfg.Env[EnvScriptsStore] != row.Path {
		t.Fatalf("dev restore: ensured %v, env %q; want dev's worktree re-attached and exported", scripts.ensured, rt.lastCfg.Env[EnvScriptsStore])
	}
	if _, err := os.Readlink(filepath.Join(ws.path, ".claude", "skills", "verify")); err != nil {
		t.Fatalf("restore laid no verify link: %v", err)
	}

	if _, err := m.Restore(ctx, qa.ID); err != nil {
		t.Fatalf("restore qa: %v", err)
	}
	if len(scripts.ensured) != 1 {
		t.Fatalf("ensured = %v; a crew member must not touch the owner's worktree", scripts.ensured)
	}
	if got := rt.lastCfg.Env[EnvScriptsStore]; got != row.Path {
		t.Fatalf("qa %s = %q, want dev's worktree %q", EnvScriptsStore, got, row.Path)
	}
}

func TestVerifySkillLinkLeavesOthersAndReplacesOnlyStaleStoreLinks(t *testing.T) {
	m, _, _, ws, scripts := newScriptsManager(t, "projects/nter/verify")
	project := mobileProject("projects/nter/verify")
	w, err := scripts.Ensure(ctx, "mer", "mer-1", "/scripts/store", "main")
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(ws.path, ".claude", "skills", "verify")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink("/somewhere/a/person/chose", link); err != nil {
		t.Fatal(err)
	}
	m.linkVerifySkill(ctx, project, "mer-1", ws.path)
	if dest, _ := os.Readlink(link); dest != "/somewhere/a/person/chose" {
		t.Fatalf("a link AO did not make was replaced with %q", dest)
	}

	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(m.dataDir, "store-worktrees", "store", "mer-0", "projects", "nter", "verify")
	if err := os.Symlink(stale, link); err != nil {
		t.Fatal(err)
	}
	m.linkVerifySkill(ctx, project, "mer-1", ws.path)
	if dest, _ := os.Readlink(link); dest != filepath.Join(w.Path, "projects", "nter", "verify") {
		t.Fatalf("stale store link = %q, want it replaced by the live worktree's skill", dest)
	}
}

// TestPromptScripts_PointsAtTheVerifySkillOnlyWhenTheStoreHoldsIt: a project
// may name a verify skill before anyone has written it. Until the checkout the
// session's scripts come from holds its SKILL.md, the prompt gives the full
// device guidance, exactly as if no skill were named.
func TestPromptScripts_PointsAtTheVerifySkillOnlyWhenTheStoreHoldsIt(t *testing.T) {
	const skill = "projects/nter/verify"
	type setup func(t *testing.T, m *Manager, scripts *fakeScripts, project *domain.ProjectRecord) domain.SessionID
	aboutToBeCut := func(holds bool) setup {
		return func(_ *testing.T, _ *Manager, scripts *fakeScripts, _ *domain.ProjectRecord) domain.SessionID {
			scripts.files = map[string]bool{"main:" + skill + "/SKILL.md": holds}
			return ""
		}
	}
	cut := func(holds bool) setup {
		return func(t *testing.T, _ *Manager, scripts *fakeScripts, _ *domain.ProjectRecord) domain.SessionID {
			// The store's base holding the skill must not stand in for the
			// worktree itself: that is what the link reads.
			scripts.files = map[string]bool{"main:" + skill + "/SKILL.md": !holds}
			w, err := scripts.Ensure(ctx, "mer", "mer-1", "/scripts/store", "main")
			if err != nil {
				t.Fatal(err)
			}
			if !holds {
				if err := os.Remove(filepath.Join(w.Path, skill, "SKILL.md")); err != nil {
					t.Fatal(err)
				}
			}
			return "mer-1"
		}
	}
	mainCheckout := func(holds bool) setup {
		return func(t *testing.T, _ *Manager, scripts *fakeScripts, project *domain.ProjectRecord) domain.SessionID {
			store := t.TempDir()
			project.Config.MobileScripts.Store = store
			scripts.probe = ports.ScriptsStoreProbe{Reason: "not a git repository"}
			if err := os.MkdirAll(filepath.Join(store, skill), 0o755); err != nil {
				t.Fatal(err)
			}
			if holds {
				if err := os.WriteFile(filepath.Join(store, skill, "SKILL.md"), []byte("verify\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			return ""
		}
	}
	for name, tc := range map[string]struct {
		setup     setup
		wantSkill string
	}{
		"worktree about to be cut, its base holds the skill": {aboutToBeCut(true), "$" + EnvScriptsStore + "/" + skill},
		"worktree about to be cut, its base lacks the skill": {aboutToBeCut(false), ""},
		"cut worktree holds the skill":                       {cut(true), "$" + EnvScriptsStore + "/" + skill},
		"cut worktree lacks the skill":                       {cut(false), ""},
		"main checkout holds the skill":                      {mainCheckout(true), "<store>/" + skill},
		"main checkout lacks the skill":                      {mainCheckout(false), ""},
	} {
		t.Run(name, func(t *testing.T) {
			m, _, _, _, scripts := newScriptsManager(t, skill)
			project := mobileProject(skill)
			owner := tc.setup(t, m, scripts, &project)
			got := m.promptScripts(ctx, project, owner)
			want := strings.ReplaceAll(tc.wantSkill, "<store>", project.Config.MobileScripts.Store)
			if got.Skill != want {
				t.Fatalf("Skill = %q, want %q", got.Skill, want)
			}
			if want != "" {
				return
			}
			unset := project
			unsetScripts := *project.Config.MobileScripts
			unsetScripts.VerifySkill = ""
			unset.Config.MobileScripts = &unsetScripts
			if g, w := prompts.MobileScriptGuidance(got, ""), prompts.MobileScriptGuidance(m.promptScripts(ctx, unset, owner), ""); g != w {
				t.Fatalf("device guidance with a missing skill differs from the guidance with none named:\n%s\n---\n%s", g, w)
			}
		})
	}
}

func TestSpawn_MissingVerifySkillGivesTheFullGuidanceAndNoLink(t *testing.T) {
	m, _, _, ws, scripts := newScriptsManager(t, "projects/advisor/verify-ios")
	agent := &recordingAgent{}
	m.agents = singleAgent{agent: agent}
	if _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker}); err != nil {
		t.Fatal(err)
	}
	if len(scripts.ensured) != 1 {
		t.Fatalf("ensured = %v, want the worker's own worktree", scripts.ensured)
	}
	got := agent.lastLaunch.SystemPrompt
	if strings.Contains(got, "the project's verify skill (AO)") || strings.Contains(got, "projects/advisor/verify-ios") {
		t.Fatalf("the prompt points at a verify skill the store does not hold:\n%s", got)
	}
	if !strings.Contains(got, "## Driving the iOS Simulator") {
		t.Fatalf("the prompt has no device guidance:\n%s", got)
	}
	if _, err := os.Lstat(filepath.Join(ws.path, ".claude", "skills", "verify")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a verify link was made to a missing skill: %v", err)
	}
}

func TestTeardown_ScriptsStore(t *testing.T) {
	seed := func(t *testing.T, blocked bool) (*Manager, *fakeStore, *fakeRuntime, *fakeWorkspace, *fakeScripts, domain.SessionRecord) {
		m, st, rt, ws, scripts := newScriptsManager(t, "")
		rec, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker})
		if err != nil {
			t.Fatal(err)
		}
		rt.created, ws.destroyed = 0, 0
		scripts.blocked = blocked
		return m, st, rt, ws, scripts, rec
	}
	t.Run("kill refuses over unpublishable work and touches nothing", func(t *testing.T) {
		m, st, rt, ws, scripts, rec := seed(t, true)
		res, err := m.Kill(ctx, rec.ID, KillOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if res.Reason != ReasonScriptsStoreDirty || res.Terminated || res.ScriptsStore == nil || len(res.ScriptsStore.Worktree.Uncommitted) != 1 {
			t.Fatalf("kill = %+v, want refused with the store's files", res)
		}
		if rt.destroyed != 0 || ws.destroyed != 0 || st.sessions[rec.ID].IsTerminated {
			t.Fatal("a refused kill destroyed something")
		}
		if len(scripts.settled) != 1 || scripts.settled[0].policy != scriptstore.SettleCheck {
			t.Fatalf("settled = %v, want one check", scripts.settled)
		}
	})
	t.Run("background teardown keeps a blocked worktree and the session", func(t *testing.T) {
		m, st, _, ws, scripts, rec := seed(t, true)
		res, err := m.Teardown(ctx, rec.ID, domain.TerminationCauseAutoReclaim)
		if err != nil {
			t.Fatal(err)
		}
		if res.Reason != ReasonScriptsStoreDirty || res.Terminated || res.Freed || ws.destroyed != 0 || st.sessions[rec.ID].IsTerminated {
			t.Fatalf("teardown = %+v (workspace destroyed %d), want the workspace kept and the row non-terminal", res, ws.destroyed)
		}
		if len(scripts.settled) != 1 || scripts.settled[0].policy != scriptstore.SettleKeep {
			t.Fatalf("settled = %v, want one keep", scripts.settled)
		}
	})
	t.Run("clean teardown removes it with the workspace", func(t *testing.T) {
		m, _, _, ws, scripts, rec := seed(t, false)
		res, err := m.Kill(ctx, rec.ID, KillOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if !res.Terminated || !res.Freed || ws.destroyed != 1 || scripts.rows[rec.ID].State != domain.ScriptsStoreRemoved {
			t.Fatalf("kill = %+v, row %+v; want everything removed", res, scripts.rows[rec.ID])
		}
	})
	t.Run("discard removes it whatever it holds", func(t *testing.T) {
		m, _, _, _, scripts, rec := seed(t, true)
		res, err := m.Kill(ctx, rec.ID, KillOptions{DiscardUncommitted: true})
		if err != nil {
			t.Fatal(err)
		}
		last := scripts.settled[len(scripts.settled)-1]
		if !res.Terminated || last.policy != scriptstore.SettleDiscard {
			t.Fatalf("discard = %+v, settled %v", res, scripts.settled)
		}
	})
	t.Run("a crew member never settles the owner's worktree", func(t *testing.T) {
		m, st, _, _, scripts := newScriptsManager(t, "")
		_, qa := seedCrew(st)
		if _, err := m.Kill(ctx, qa.ID, KillOptions{}); err != nil {
			t.Fatal(err)
		}
		if len(scripts.settled) != 0 {
			t.Fatalf("settled = %v, want none for a crew member", scripts.settled)
		}
	})
}
