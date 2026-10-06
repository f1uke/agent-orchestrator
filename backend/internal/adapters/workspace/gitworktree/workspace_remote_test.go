package gitworktree

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// urlResolver is a RepoResolver that also knows the project's registered URL,
// the way the daemon's project-backed resolver does.
type urlResolver struct {
	path, url string
}

func (r urlResolver) RepoPath(domain.ProjectID) (string, error) { return r.path, nil }
func (r urlResolver) RepoURL(domain.ProjectID) string           { return r.url }

// remoteNotOriginFixture is the advisor-ios-app layout: the project's own
// remote is called "Advisor", a second remote "Nter" points at a DIFFERENT
// project, there is no origin, and the forge's main has moved on since the
// repository last fetched - so both the local main and refs/remotes/Advisor/main
// are stale.
type remoteNotOriginFixture struct {
	repo, forge, stale, fresh string
}

func newRemoteNotOriginFixture(t *testing.T, git, tmp string) remoteNotOriginFixture {
	t.Helper()
	repo := setupOriginClone(t, git, tmp)
	forge := filepath.Join(tmp, "origin.git")
	stale := revParse(t, git, repo, "HEAD")
	runGit(t, git, repo, "remote", "rename", "origin", "Advisor")

	other := filepath.Join(tmp, "other.git")
	run(t, git, "init", "--bare", other)
	runGit(t, git, repo, "remote", "add", "Nter", other)

	// A colleague lands work on the forge after this repo's last fetch.
	colleague := filepath.Join(tmp, "colleague")
	run(t, git, "clone", forge, colleague)
	runGit(t, git, colleague, "config", "user.email", "ao@example.com")
	runGit(t, git, colleague, "config", "user.name", "Ao Agents")
	writeCommit(t, git, colleague, "landed.txt", "landed\n", "merged after the last pull")
	runGit(t, git, colleague, "push", "origin", "main")
	fresh := revParse(t, git, colleague, "HEAD")
	return remoteNotOriginFixture{repo: repo, forge: forge, stale: stale, fresh: fresh}
}

func (f remoteNotOriginFixture) workspace(t *testing.T, git, tmp string) *Workspace {
	t.Helper()
	ws, err := New(Options{Binary: git, ManagedRoot: filepath.Join(tmp, "managed"), RepoResolver: urlResolver{path: f.repo, url: f.forge}})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	return ws
}

// TestCreateCutsANewBranchFromTheFreshlyFetchedProjectRemote is the reported
// bug: new advisor-ios-app workers were cut from the human's stale local
// develop, because every candidate named origin and none existed.
func TestCreateCutsANewBranchFromTheFreshlyFetchedProjectRemote(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	f := newRemoteNotOriginFixture(t, git, tmp)
	ws := f.workspace(t, git, tmp)

	info, err := ws.Create(context.Background(), ports.WorkspaceConfig{
		ProjectID: "proj", SessionID: "sess", Branch: "feature/new", BaseBranch: "main",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if got := revParse(t, git, info.Path, "HEAD"); got != f.fresh {
		t.Fatalf("new worktree cut from %s, want the forge's current main %s (stale local main is %s)", got, f.fresh, f.stale)
	}
	if got := revParse(t, git, f.repo, "refs/heads/main"); got != f.stale {
		t.Fatalf("the human's local main moved to %s; fetching must touch remote-tracking refs only", got)
	}
}

// TestCreateFetchesTheBaseEvenWhenTheRemoteIsOrigin: the stale-base half of the
// bug was never origin-specific - a new branch was cut from whatever
// refs/remotes/origin/<base> happened to be on disk.
func TestCreateFetchesTheBaseEvenWhenTheRemoteIsOrigin(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	f := newRemoteNotOriginFixture(t, git, tmp)
	runGit(t, git, f.repo, "remote", "remove", "Nter")
	runGit(t, git, f.repo, "remote", "rename", "Advisor", "origin")
	ws, err := New(Options{Binary: git, ManagedRoot: filepath.Join(tmp, "managed"), RepoResolver: StaticRepoResolver{"proj": f.repo}})
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	info, err := ws.Create(context.Background(), ports.WorkspaceConfig{
		ProjectID: "proj", SessionID: "sess", Branch: "feature/new", BaseBranch: "main",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if got := revParse(t, git, info.Path, "HEAD"); got != f.fresh {
		t.Fatalf("new worktree cut from %s, want the freshly fetched origin/main %s", got, f.fresh)
	}
}

// TestCreateFallsBackToKnownRefsWhenTheFetchFails keeps a spawn working
// offline: an unreachable forge costs freshness, never the session.
func TestCreateFallsBackToKnownRefsWhenTheFetchFails(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	f := newRemoteNotOriginFixture(t, git, tmp)
	runGit(t, git, f.repo, "remote", "set-url", "Advisor", filepath.Join(tmp, "gone.git"))
	ws, err := New(Options{Binary: git, ManagedRoot: filepath.Join(tmp, "managed"), RepoResolver: urlResolver{path: f.repo, url: filepath.Join(tmp, "gone.git")}})
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	info, err := ws.Create(context.Background(), ports.WorkspaceConfig{
		ProjectID: "proj", SessionID: "sess", Branch: "feature/new", BaseBranch: "main",
	})
	if err != nil {
		t.Fatalf("create with the forge unreachable: %v", err)
	}
	if got := revParse(t, git, info.Path, "HEAD"); got != f.stale {
		t.Fatalf("worktree at %s, want the newest known refs/remotes/Advisor/main %s", got, f.stale)
	}
}

// TestSyncToBaseFetchesFromTheProjectRemote: the orchestrator sweep logged
// "'origin' does not appear to be a git repository" every 15 minutes for
// advisor-ios-app and left its orchestrator on stale code.
func TestSyncToBaseFetchesFromTheProjectRemote(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	f := newRemoteNotOriginFixture(t, git, tmp)
	runGit(t, git, f.repo, "branch", "ao/proj-orchestrator", f.stale)
	ws := f.workspace(t, git, tmp)
	ctx := context.Background()
	info, err := ws.Create(ctx, ports.WorkspaceConfig{
		ProjectID: "proj", Kind: domain.KindOrchestrator, SessionPrefix: "proj",
		Branch: "ao/proj-orchestrator", BaseBranch: "main",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	res, err := ws.SyncToBase(ctx, info, "main")
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if res.FetchError != "" {
		t.Fatalf("fetch error = %q, want none", res.FetchError)
	}
	if res.Outcome != ports.WorkspaceSyncUpdated || res.BaseRef != "refs/remotes/Advisor/main" {
		t.Fatalf("sync = %+v, want updated onto refs/remotes/Advisor/main", res)
	}
	if got := revParse(t, git, info.Path, "HEAD"); got != f.fresh {
		t.Fatalf("orchestrator HEAD = %s, want %s", got, f.fresh)
	}
}

// TestSyncToBaseWithoutAnyRemoteIsNotAFetchFailure: desk-calendar and gchat-cli
// have no remote at all. Their local main IS the base; reporting a failed
// fetch every sweep was noise that buried real failures.
func TestSyncToBaseWithoutAnyRemoteIsNotAFetchFailure(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	repo := setupOriginClone(t, git, tmp)
	runGit(t, git, repo, "remote", "remove", "origin")
	runGit(t, git, repo, "branch", "ao/proj-orchestrator", "main")
	writeCommit(t, git, repo, "later.txt", "later\n", "local work on main")
	ws, cfg := newOrchestratorWorkspace(t, git, tmp, repo, "main")
	ctx := context.Background()
	info, err := ws.Create(ctx, cfg)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	res, err := ws.SyncToBase(ctx, info, "main")
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if res.FetchError != "" {
		t.Fatalf("fetch error = %q, want none for a repository with nothing to fetch", res.FetchError)
	}
	if res.Outcome != ports.WorkspaceSyncUpdated || res.BaseRef != "refs/heads/main" {
		t.Fatalf("sync = %+v, want updated onto refs/heads/main", res)
	}
}
