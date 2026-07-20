package session

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// changesTestRepo builds a real git repo with a main branch and a feature
// branch carrying one of every change shape the panel must render: modified,
// added, deleted, renamed, binary, plus uncommitted and untracked work.
//
// The parsers are written against REAL git output rather than a recollection of
// the -z wire formats, so these fixtures are the specification.
func changesTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runGit("init", "-q")
	runGit("config", "user.email", "t@t")
	runGit("config", "user.name", "t")

	write("keep.go", "l1\nl2\nl3\n")
	write("old-name.go", "old\n")
	write("gone.go", "x\n")
	if err := os.WriteFile(filepath.Join(dir, "img.bin"), []byte{0x00, 0x01, 'b', 'i', 'n'}, 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", "-A")
	runGit("commit", "-qm", "base")
	runGit("branch", "-M", "main")
	runGit("checkout", "-qb", "feature/x")

	write("keep.go", "l1\nCHANGED\nl3\nl4\n")
	runGit("mv", "old-name.go", "new-name.go")
	runGit("rm", "-q", "gone.go")
	write("added.go", "brand new\n")
	if err := os.WriteFile(filepath.Join(dir, "img.bin"), []byte{0x00, 0x02, 'B', 'I', 'N'}, 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", "-A")
	runGit("commit", "-qm", "work")

	// uncommitted + untracked, on top of the commits
	write("keep.go", "l1\nCHANGED\nl3\nl4\nuncommitted\n")
	write("untracked.go", "u1\nu2\n")
	return dir
}

func changesService(t *testing.T, dir string, prs []domain.PullRequest) *Service {
	t.Helper()
	fake := newFakeStore()
	fake.putSessionWithWorkspace("s1", dir)
	return newServiceWithStore(t, &multiPRFakeStore{fakeStore: fake, prs: prs})
}

func fileByPath(t *testing.T, res WorkspaceChangesResult, path string) ChangedFile {
	t.Helper()
	for _, f := range res.Files {
		if f.Path == path {
			return f
		}
	}
	t.Fatalf("no changed file %q in %+v", path, res.Files)
	return ChangedFile{}
}

func TestWorkspaceChanges_AllStatuses(t *testing.T) {
	dir := changesTestRepo(t)
	svc := changesService(t, dir, []domain.PullRequest{{URL: "pr1", TargetBranch: "main"}})

	res, err := svc.WorkspaceChanges(context.Background(), "s1")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Available {
		t.Fatalf("want available, got %+v", res)
	}
	if res.TargetBranch != "main" || res.TargetSource != TargetFromPR {
		t.Fatalf("target = %q via %q, want main via pr", res.TargetBranch, res.TargetSource)
	}

	if f := fileByPath(t, res, "added.go"); f.Status != ChangeAdded || f.Additions != 1 {
		t.Errorf("added.go = %+v", f)
	}
	if f := fileByPath(t, res, "gone.go"); f.Status != ChangeDeleted || f.Deletions != 1 {
		t.Errorf("gone.go = %+v", f)
	}
	// Rename detection (-M) must carry the OLD path, or the UI cannot render
	// "old → new" and the row looks like an unrelated add.
	f := fileByPath(t, res, "new-name.go")
	if f.Status != ChangeRenamed || f.OldPath != "old-name.go" {
		t.Errorf("rename = %+v, want renamed from old-name.go", f)
	}
	// A binary file must be MARKED, not counted: numstat emits "-" for both, and
	// rendering that arithmetically produces a nonsense "+- -".
	if b := fileByPath(t, res, "img.bin"); !b.Binary || b.Additions != 0 || b.Deletions != 0 {
		t.Errorf("img.bin = %+v, want binary with zeroed counts", b)
	}
}

func TestWorkspaceChanges_IncludesUncommittedAndUntracked(t *testing.T) {
	dir := changesTestRepo(t)
	svc := changesService(t, dir, []domain.PullRequest{{URL: "pr1", TargetBranch: "main"}})

	res, err := svc.WorkspaceChanges(context.Background(), "s1")
	if err != nil {
		t.Fatal(err)
	}

	// keep.go was committed AND then edited again — the extra working-tree line
	// must be counted and the row flagged uncommitted.
	keep := fileByPath(t, res, "keep.go")
	if keep.Committed {
		t.Errorf("keep.go should be flagged uncommitted: %+v", keep)
	}
	if keep.Additions != 3 {
		t.Errorf("keep.go additions = %d, want 3 (2 committed + 1 working tree)", keep.Additions)
	}

	// An untracked file is invisible to `git diff` entirely. Omitting it would
	// make the panel silently under-report a worker mid-task.
	un := fileByPath(t, res, "untracked.go")
	if un.Status != ChangeAdded || un.Committed || un.Additions != 2 {
		t.Errorf("untracked.go = %+v, want added/uncommitted/+2", un)
	}

	// A purely committed file stays flagged committed.
	if added := fileByPath(t, res, "added.go"); !added.Committed {
		t.Errorf("added.go should be committed: %+v", added)
	}
}

func TestWorkspaceChanges_TargetBranchResolutionOrder(t *testing.T) {
	dir := changesTestRepo(t)

	t.Run("prefers open PR target over session spec", func(t *testing.T) {
		fake := newFakeStore()
		fake.putSessionWithWorkspace("s1", dir)
		rec := fake.sessions["s1"]
		rec.PRTarget = "develop"
		fake.sessions["s1"] = rec
		svc := newServiceWithStore(t, &multiPRFakeStore{
			fakeStore: fake, prs: []domain.PullRequest{{URL: "pr1", TargetBranch: "main"}},
		})
		res, err := svc.WorkspaceChanges(context.Background(), "s1")
		if err != nil {
			t.Fatal(err)
		}
		if res.TargetSource != TargetFromPR || res.TargetBranch != "main" {
			t.Fatalf("got %q via %q, want main via pr", res.TargetBranch, res.TargetSource)
		}
	})

	t.Run("falls back to session PRTarget when no PR", func(t *testing.T) {
		fake := newFakeStore()
		fake.putSessionWithWorkspace("s1", dir)
		rec := fake.sessions["s1"]
		rec.PRTarget = "main"
		fake.sessions["s1"] = rec
		svc := newServiceWithStore(t, &multiPRFakeStore{fakeStore: fake})
		res, err := svc.WorkspaceChanges(context.Background(), "s1")
		if err != nil {
			t.Fatal(err)
		}
		if res.TargetSource != TargetFromSessionPRTarget {
			t.Fatalf("source = %q, want %q", res.TargetSource, TargetFromSessionPRTarget)
		}
	})
}

// TestWorkspaceChanges_NeverAssumesMain is the load-bearing guard on the
// product decision: with no PR, no session spec, no project default and no
// origin/HEAD, the panel must say it does not know rather than diff against a
// fabricated "main". A wrong target renders a confidently wrong diff.
func TestWorkspaceChanges_NeverAssumesMain(t *testing.T) {
	dir := changesTestRepo(t)
	fake := newFakeStore()
	fake.putSessionWithWorkspace("s1", dir)
	svc := newServiceWithStore(t, &multiPRFakeStore{fakeStore: fake})

	res, err := svc.WorkspaceChanges(context.Background(), "s1")
	if err != nil {
		t.Fatal(err)
	}
	if res.Available {
		t.Fatalf("must not resolve a target branch by assumption, got %+v", res)
	}
	if res.Reason != ChangesNoTargetBranch {
		t.Fatalf("reason = %q, want %q", res.Reason, ChangesNoTargetBranch)
	}
	if res.TargetBranch == "main" {
		t.Fatal("fabricated 'main' as the target branch")
	}
}

// TestWorkspaceChanges_UnresolvableTargetDegrades is the safety property that
// actually protects the user now that the project default is read through
// WithDefaults (which synthesises "main"): if the named branch does not exist in
// this repo, the panel must say so rather than diff against something else.
// It names the branch it failed to resolve so the empty state can be specific.
func TestWorkspaceChanges_UnresolvableTargetDegrades(t *testing.T) {
	dir := changesTestRepo(t)
	fake := newFakeStore()
	fake.putSessionWithWorkspace("s1", dir)
	rec := fake.sessions["s1"]
	rec.PRTarget = "release/does-not-exist"
	fake.sessions["s1"] = rec
	svc := newServiceWithStore(t, &multiPRFakeStore{fakeStore: fake})

	res, err := svc.WorkspaceChanges(context.Background(), "s1")
	if err != nil {
		t.Fatal(err)
	}
	if res.Available {
		t.Fatalf("must not diff against a branch that does not exist: %+v", res)
	}
	if res.Reason != ChangesNoTargetBranch || res.TargetBranch != "release/does-not-exist" {
		t.Fatalf("res = %+v, want %s naming the unresolvable branch", res, ChangesNoTargetBranch)
	}
}

func TestWorkspaceChanges_MissingWorktreeDegrades(t *testing.T) {
	fake := newFakeStore()
	fake.putSessionWithWorkspace("s1", filepath.Join(t.TempDir(), "was-cleaned-up"))
	svc := newServiceWithStore(t, &multiPRFakeStore{fakeStore: fake})

	// A merged session keeps its board row after its worktree is removed. That
	// must render an empty state, never a 500.
	res, err := svc.WorkspaceChanges(context.Background(), "s1")
	if err != nil {
		t.Fatalf("a missing worktree must degrade, not error: %v", err)
	}
	if res.Available || res.Reason != ChangesNoWorkspace {
		t.Fatalf("res = %+v, want unavailable/%s", res, ChangesNoWorkspace)
	}
}

func TestWorkspaceChanges_UnknownSessionErrors(t *testing.T) {
	svc := newServiceWithStore(t, &multiPRFakeStore{fakeStore: newFakeStore()})
	if _, err := svc.WorkspaceChanges(context.Background(), "nope"); err == nil {
		t.Fatal("want error for unknown session")
	}
}

func TestWorkspaceChanges_NotAGitRepoDegrades(t *testing.T) {
	fake := newFakeStore()
	fake.putSessionWithWorkspace("s1", t.TempDir()) // exists, but no .git
	svc := newServiceWithStore(t, &multiPRFakeStore{fakeStore: fake})

	res, err := svc.WorkspaceChanges(context.Background(), "s1")
	if err != nil {
		t.Fatal(err)
	}
	if res.Available || res.Reason != ChangesNotARepo {
		t.Fatalf("res = %+v, want unavailable/%s", res, ChangesNotARepo)
	}
}

// ── per-file diff ────────────────────────────────────────────────────────────

func TestWorkspaceFileDiff_NoPRRequired(t *testing.T) {
	dir := changesTestRepo(t)
	// Deliberately NO PullRequest: DiffContext would 404 here, which is the
	// whole reason this endpoint exists.
	fake := newFakeStore()
	fake.putSessionWithWorkspace("s1", dir)
	rec := fake.sessions["s1"]
	rec.PRTarget = "main"
	fake.sessions["s1"] = rec
	svc := newServiceWithStore(t, &multiPRFakeStore{fakeStore: fake})

	res, err := svc.WorkspaceFileDiff(context.Background(), "s1", "keep.go")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Available {
		t.Fatalf("want a diff without any PR, got %+v", res)
	}
	var sawAdd bool
	for _, l := range res.Lines {
		if l.Kind == "add" && l.Text == "CHANGED" {
			sawAdd = true
		}
	}
	if !sawAdd {
		t.Fatalf("missing the added line: %+v", res.Lines)
	}
}

// TestWorkspaceFileDiff_DeletedFile covers the trap called out in the design:
// a deleted file has no working-tree content, so routing its row to the file
// reader 404s. It must diff as an all-deletions patch instead.
func TestWorkspaceFileDiff_DeletedFile(t *testing.T) {
	dir := changesTestRepo(t)
	fake := newFakeStore()
	fake.putSessionWithWorkspace("s1", dir)
	rec := fake.sessions["s1"]
	rec.PRTarget = "main"
	fake.sessions["s1"] = rec
	svc := newServiceWithStore(t, &multiPRFakeStore{fakeStore: fake})

	res, err := svc.WorkspaceFileDiff(context.Background(), "s1", "gone.go")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Available {
		t.Fatalf("a deleted file must still diff, got %+v", res)
	}
	for _, l := range res.Lines {
		if l.Kind != "del" {
			t.Fatalf("want an all-deletions patch, saw %+v", l)
		}
	}
}

// TestWorkspaceFileDiff_UntrackedFile is a regression guard for a bug only live
// end-to-end use surfaced: `git diff` never reports an untracked file, so a
// brand-new file was LISTED in Changes (correctly) but opened on an empty
// viewer. It must render as the all-additions patch its content implies.
func TestWorkspaceFileDiff_UntrackedFile(t *testing.T) {
	dir := changesTestRepo(t)
	fake := newFakeStore()
	fake.putSessionWithWorkspace("s1", dir)
	rec := fake.sessions["s1"]
	rec.PRTarget = "main"
	fake.sessions["s1"] = rec
	svc := newServiceWithStore(t, &multiPRFakeStore{fakeStore: fake})

	res, err := svc.WorkspaceFileDiff(context.Background(), "s1", "untracked.go")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Available {
		t.Fatalf("an untracked file must still render a diff, got %+v", res)
	}
	if len(res.Lines) != 2 {
		t.Fatalf("want 2 added lines, got %d: %+v", len(res.Lines), res.Lines)
	}
	for _, l := range res.Lines {
		if l.Kind != "add" {
			t.Fatalf("want an all-additions patch, saw %+v", l)
		}
	}
}

// TestWorkspaceChangesPaths_StayConfined asserts the confinement gate at the
// git-args seam, the same way TestDiffContext_ConfinesPathBeforeGit does: a
// black-box check on Available cannot tell "we confined it" from "git rejected
// it on its own".
//
// This matters specifically because #132 deliberately removed workspace
// confinement from ResolveWorkspaceRef/ReadWorkspaceFile for absolute and `~/`
// paths. That widening is scoped to the terminal's click-to-open feature; the
// Changes-mode endpoints must NOT inherit it.
func TestWorkspaceFileDiff_ConfinesPathBeforeGit(t *testing.T) {
	dir := changesTestRepo(t)
	fake := newFakeStore()
	fake.putSessionWithWorkspace("s1", dir)
	rec := fake.sessions["s1"]
	rec.PRTarget = "main"
	fake.sessions["s1"] = rec
	svc := newServiceWithStore(t, &multiPRFakeStore{fakeStore: fake})

	orig := gitOutput
	defer func() { gitOutput = orig }()

	var pathspecs []string
	gitOutput = func(ctx context.Context, dir string, args ...string) ([]byte, error) {
		if len(args) >= 2 && args[0] == "diff" && args[len(args)-2] == "--" {
			pathspecs = append(pathspecs, args[len(args)-1])
		}
		// let ref resolution succeed so we reach the diff call
		if len(args) > 0 && (args[0] == "rev-parse" || args[0] == "merge-base") {
			return []byte("deadbeef\n"), nil
		}
		return nil, errors.New("stubbed")
	}

	if _, err := svc.WorkspaceFileDiff(context.Background(), "s1", "../../etc/passwd"); err != nil {
		t.Fatal(err)
	}
	for _, p := range pathspecs {
		if strings.Contains(p, "..") {
			t.Fatalf("unconfined traversal path reached git: %q", p)
		}
	}
}

// TestConfinedWorkspacePath_RejectsAbsoluteAndTilde is the direct regression
// guard against inheriting #132's INTENTIONALLY UNCONFINED behaviour.
// ReadWorkspaceFile resolves these anywhere on disk by design; the Changes
// endpoints must not.
//
// It asserts on confinedWorkspacePath rather than on the endpoint's Available
// flag, because that flag CANNOT distinguish the two outcomes: ConfinedPath does
// not reject an absolute path, it silently REINTERPRETS "/etc/passwd" as
// "<workspace>/etc/passwd", which then simply does not exist and yields an empty
// diff. A black-box test therefore passes even with the guard deleted — verified
// by removing it. Only the helper's ok flag distinguishes reject from rewrite.
func TestConfinedWorkspacePath_RejectsAbsoluteAndTilde(t *testing.T) {
	dir := changesTestRepo(t)
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("no home dir")
	}
	for _, p := range []string{"/etc/passwd", "/", "~/.ssh/id_rsa", "~"} {
		if abs, rel, ok := confinedWorkspacePath(dir, p); ok {
			t.Errorf("%q must be REJECTED, got ok with abs=%q rel=%q", p, abs, rel)
		}
	}
	// A genuine repo-relative path still resolves.
	if _, rel, ok := confinedWorkspacePath(dir, "keep.go"); !ok || rel != "keep.go" {
		t.Fatalf("relative path broke: ok=%v rel=%q", ok, rel)
	}
}

// TestWorkspaceFileDiff_AbsolutePathYieldsNothing is the endpoint-level
// companion: whatever the mechanism, an absolute path must never surface content
// from outside the worktree.
func TestWorkspaceFileDiff_AbsolutePathYieldsNothing(t *testing.T) {
	dir := changesTestRepo(t)
	fake := newFakeStore()
	fake.putSessionWithWorkspace("s1", dir)
	rec := fake.sessions["s1"]
	rec.PRTarget = "main"
	fake.sessions["s1"] = rec
	svc := newServiceWithStore(t, &multiPRFakeStore{fakeStore: fake})

	for _, p := range []string{"/etc/passwd", "~/.ssh/id_rsa", "~", ""} {
		res, err := svc.WorkspaceFileDiff(context.Background(), "s1", p)
		if err != nil {
			t.Fatalf("%q: %v", p, err)
		}
		if res.Available {
			t.Fatalf("%q resolved outside the workspace: %+v", p, res)
		}
	}
}

// TestConfinedWorkspacePath_EmptyIsRejected pins the ConfinedPath sharp edge
// noted in the design: it rewrites an empty or "." path to "index.html", which
// is right for the preview route it was written for and wrong here.
func TestConfinedWorkspacePath_EmptyIsRejected(t *testing.T) {
	dir := t.TempDir()
	for _, p := range []string{"", "   ", "."} {
		if _, _, ok := confinedWorkspacePath(dir, p); ok {
			t.Fatalf("%q must be rejected, not rewritten to index.html", p)
		}
	}
}

// TestConfinedWorkspacePath_SymlinkEscape covers the other sharp edge:
// ConfinedPath is purely lexical and never resolves symlinks, so a link inside
// the worktree pointing outside it would otherwise pass.
func TestConfinedWorkspacePath_SymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("s\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link.txt")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, _, ok := confinedWorkspacePath(root, "link.txt"); ok {
		t.Fatal("a symlink escaping the worktree must be rejected")
	}
	// A deleted file does not resolve at all; that must stay allowed, because
	// diffing a deleted file is exactly what Changes mode has to do.
	if _, rel, ok := confinedWorkspacePath(root, "deleted.go"); !ok || rel != "deleted.go" {
		t.Fatalf("a non-existent path must stay allowed, got ok=%v rel=%q", ok, rel)
	}
}
