package session

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/diffhunk"
)

// gitRepo builds a temp git repo with an initial commit of the given files and
// returns its dir. Helpers run git and fail the test on error.
func gitRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	runGit := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	runGit("init", "-q")
	runGit("config", "user.email", "t@t")
	runGit("config", "user.name", "t")
	for rel, content := range files {
		writeRepoFile(t, dir, rel, content)
	}
	runGit("add", "-A")
	runGit("commit", "-q", "-m", "base")
	return dir
}

func writeRepoFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	full := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func serviceForRepo(t *testing.T, dir string) *Service {
	t.Helper()
	fake := newFakeStore()
	fake.putSessionWithWorkspace("s1", dir)
	return newServiceWithStore(t, fake)
}

func TestResolveWorkspaceRef_AbsoluteInsideWorkspace(t *testing.T) {
	dir := gitRepo(t, map[string]string{"pkg/a.go": "x\n"})
	svc := serviceForRepo(t, dir)
	abs := filepath.Join(dir, "pkg", "a.go")

	got, err := svc.ResolveWorkspaceRef(context.Background(), "s1", abs)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"pkg/a.go"}; !equalStrings(got, want) {
		t.Fatalf("candidates = %v, want %v", got, want)
	}
}

func TestResolveWorkspaceRef_RelativeWithSlash(t *testing.T) {
	dir := gitRepo(t, map[string]string{"pkg/a.go": "x\n"})
	svc := serviceForRepo(t, dir)

	got, err := svc.ResolveWorkspaceRef(context.Background(), "s1", "pkg/a.go")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"pkg/a.go"}; !equalStrings(got, want) {
		t.Fatalf("candidates = %v, want %v", got, want)
	}
}

func TestResolveWorkspaceRef_BareFilenameUnique(t *testing.T) {
	dir := gitRepo(t, map[string]string{"pkg/deep/UsableThing.swift": "x\n", "other/b.go": "y\n"})
	svc := serviceForRepo(t, dir)

	got, err := svc.ResolveWorkspaceRef(context.Background(), "s1", "UsableThing.swift")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"pkg/deep/UsableThing.swift"}; !equalStrings(got, want) {
		t.Fatalf("candidates = %v, want %v", got, want)
	}
}

func TestResolveWorkspaceRef_BareFilenameMultiple(t *testing.T) {
	dir := gitRepo(t, map[string]string{"a/config.go": "x\n", "b/config.go": "y\n"})
	svc := serviceForRepo(t, dir)

	got, err := svc.ResolveWorkspaceRef(context.Background(), "s1", "config.go")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"a/config.go", "b/config.go"}; !equalStrings(got, want) {
		t.Fatalf("candidates = %v, want %v", got, want)
	}
}

func TestResolveWorkspaceRef_NoMatch(t *testing.T) {
	dir := gitRepo(t, map[string]string{"a.go": "x\n"})
	svc := serviceForRepo(t, dir)

	got, err := svc.ResolveWorkspaceRef(context.Background(), "s1", "nope.swift")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("candidates = %v, want empty", got)
	}
}

// An absolute path outside the workspace resolves to ITSELF — the workspace
// confinement is intentionally not applied to absolute refs (approved product
// decision). It must not be rewritten to a same-basename file inside the
// workspace, which would open the wrong file.
func TestResolveWorkspaceRef_AbsoluteOutsideWorkspaceResolvesToItself(t *testing.T) {
	dir := gitRepo(t, map[string]string{"pkg/a.go": "x\n"})
	svc := serviceForRepo(t, dir)
	outside := filepath.Join(t.TempDir(), "notes.md")
	if err := os.WriteFile(outside, []byte("# hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := svc.ResolveWorkspaceRef(context.Background(), "s1", outside)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{mustEvalSymlinks(t, outside)}; !equalStrings(got, want) {
		t.Fatalf("candidates = %v, want %v", got, want)
	}
}

func TestResolveWorkspaceRef_AbsoluteMissingHasNoBasenameFallback(t *testing.T) {
	dir := gitRepo(t, map[string]string{"pkg/a.go": "x\n"})
	svc := serviceForRepo(t, dir)
	missing := filepath.Join(t.TempDir(), "some", "elsewhere", "a.go")

	got, err := svc.ResolveWorkspaceRef(context.Background(), "s1", missing)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("candidates = %v, want empty (no basename fallback for an absolute ref)", got)
	}
}

func TestResolveWorkspaceRef_TildeExpandsToHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	notes := filepath.Join(home, "some", "dir", "notes.md")
	if err := os.MkdirAll(filepath.Dir(notes), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(notes, []byte("# hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	svc := serviceForRepo(t, gitRepo(t, map[string]string{"pkg/a.go": "x\n"}))

	got, err := svc.ResolveWorkspaceRef(context.Background(), "s1", "~/some/dir/notes.md")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{mustEvalSymlinks(t, notes)}; !equalStrings(got, want) {
		t.Fatalf("candidates = %v, want %v", got, want)
	}
}

func TestResolveWorkspaceRef_AbsoluteSymlinkResolvesToTarget(t *testing.T) {
	svc := serviceForRepo(t, gitRepo(t, map[string]string{"pkg/a.go": "x\n"}))
	outside := t.TempDir()
	target := filepath.Join(outside, "real.md")
	if err := os.WriteFile(target, []byte("# real\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(outside, "link.md")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	got, err := svc.ResolveWorkspaceRef(context.Background(), "s1", link)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{mustEvalSymlinks(t, target)}; !equalStrings(got, want) {
		t.Fatalf("candidates = %v, want %v", got, want)
	}
}

// An absolute ref needs no workspace at all — a session without one (a reviewer
// terminal) can still open a path on disk.
func TestResolveWorkspaceRef_AbsoluteWorksWithoutWorkspace(t *testing.T) {
	fake := newFakeStore()
	fake.putSessionWithWorkspace("s1", "")
	svc := newServiceWithStore(t, fake)
	notes := filepath.Join(t.TempDir(), "notes.md")
	if err := os.WriteFile(notes, []byte("# hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := svc.ResolveWorkspaceRef(context.Background(), "s1", notes)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{mustEvalSymlinks(t, notes)}; !equalStrings(got, want) {
		t.Fatalf("candidates = %v, want %v", got, want)
	}
}

func TestResolveWorkspaceRef_AbsoluteDirectoryIsNotACandidate(t *testing.T) {
	svc := serviceForRepo(t, gitRepo(t, map[string]string{"pkg/a.go": "x\n"}))

	got, err := svc.ResolveWorkspaceRef(context.Background(), "s1", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("candidates = %v, want empty for a directory", got)
	}
}

func TestReadWorkspaceFile_AbsoluteOutsideWorkspaceNotInAnyRepo(t *testing.T) {
	svc := serviceForRepo(t, gitRepo(t, map[string]string{"pkg/a.go": "x\n"}))
	notes := filepath.Join(t.TempDir(), "notes.md")
	if err := os.WriteFile(notes, []byte("# title\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := svc.ReadWorkspaceFile(context.Background(), "s1", notes)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Available || len(res.Lines) != 2 || res.Lines[0].Text != "# title" {
		t.Fatalf("res = %+v", res)
	}
	if len(res.ChangedLines) != 0 {
		t.Fatalf("changedLines = %+v, want none outside a git repo", res.ChangedLines)
	}
}

// A file living in a DIFFERENT git repo (e.g. another session's worktree) gets
// best-effort uncommitted markers from that repo.
func TestReadWorkspaceFile_AbsoluteInAnotherRepoHasMarkers(t *testing.T) {
	svc := serviceForRepo(t, gitRepo(t, map[string]string{"pkg/a.go": "x\n"}))
	other := gitRepo(t, map[string]string{"b.go": "l1\nl2\nl3\n"})
	writeRepoFile(t, other, "b.go", "l1\nCHANGED\nl3\n")

	res, err := svc.ReadWorkspaceFile(context.Background(), "s1", filepath.Join(other, "b.go"))
	if err != nil {
		t.Fatal(err)
	}
	want := []diffhunk.LineChange{{Start: 2, End: 2, Kind: diffhunk.ChangeModified}}
	if !equalChanges(res.ChangedLines, want) {
		t.Fatalf("changedLines = %+v, want %+v", res.ChangedLines, want)
	}
}

func TestReadWorkspaceFile_TildePath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	notes := filepath.Join(home, "notes.md")
	if err := os.WriteFile(notes, []byte("# hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	svc := serviceForRepo(t, gitRepo(t, map[string]string{"pkg/a.go": "x\n"}))

	res, err := svc.ReadWorkspaceFile(context.Background(), "s1", "~/notes.md")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Available || len(res.Lines) != 1 || res.Lines[0].Text != "# hello" {
		t.Fatalf("res = %+v", res)
	}
}

func TestReadWorkspaceFile_OverSizeCapIsReportedNotRead(t *testing.T) {
	svc := serviceForRepo(t, gitRepo(t, map[string]string{"pkg/a.go": "x\n"}))
	big := filepath.Join(t.TempDir(), "big.txt")
	if err := os.WriteFile(big, make([]byte, maxWorkspaceFileBytes+1), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := svc.ReadWorkspaceFile(context.Background(), "s1", big)
	if err != nil {
		t.Fatal(err)
	}
	if res.Available || res.Reason != UnavailableTooLarge || len(res.Lines) != 0 {
		t.Fatalf("res = %+v, want unavailable/too_large with no lines", res)
	}
}

func TestReadWorkspaceFile_BinaryIsReported(t *testing.T) {
	svc := serviceForRepo(t, gitRepo(t, map[string]string{"pkg/a.go": "x\n"}))
	bin := filepath.Join(t.TempDir(), "blob.txt")
	if err := os.WriteFile(bin, []byte{0x7f, 0x45, 0x00, 0x01, 0x02}, 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := svc.ReadWorkspaceFile(context.Background(), "s1", bin)
	if err != nil {
		t.Fatal(err)
	}
	if res.Available || res.Reason != UnavailableBinary {
		t.Fatalf("res = %+v, want unavailable/binary", res)
	}
}

func TestReadWorkspaceFile_AbsoluteMissingIsNotFound(t *testing.T) {
	svc := serviceForRepo(t, gitRepo(t, map[string]string{"pkg/a.go": "x\n"}))

	if _, err := svc.ReadWorkspaceFile(context.Background(), "s1", filepath.Join(t.TempDir(), "nope.md")); err == nil {
		t.Fatal("expected error for a missing absolute path")
	}
}

func TestRefTarget(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// Built rather than hardcoded so the "absolute" case is absolute on every
	// platform (a leading slash is not absolute on Windows).
	someAbs := filepath.Join(t.TempDir(), "x", "a.go")
	for _, tc := range []struct {
		ref      string
		wantAbs  string
		wantIsAb bool
	}{
		{"~/some/dir/notes.md", filepath.Join(home, "some", "dir", "notes.md"), true},
		{"~", home, true},
		{"~/", home, true},
		{someAbs, someAbs, true},
		{"pkg/a.go", "", false},
		{"./pkg/a.go", "", false},
		{"a.go", "", false},
		{"dir/~backup.md", "", false},
		{"", "", false},
	} {
		abs, isAbs := refTarget(tc.ref)
		if isAbs != tc.wantIsAb || abs != tc.wantAbs {
			t.Fatalf("refTarget(%q) = (%q, %v), want (%q, %v)", tc.ref, abs, isAbs, tc.wantAbs, tc.wantIsAb)
		}
	}
}

func mustEvalSymlinks(t *testing.T, p string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

func TestResolveWorkspaceRef_UnknownSession(t *testing.T) {
	svc := newServiceWithStore(t, newFakeStore())
	if _, err := svc.ResolveWorkspaceRef(context.Background(), "ghost", "a.go"); err == nil {
		t.Fatal("expected error for unknown session")
	}
}

func TestReadWorkspaceFile_ModifiedUncommitted(t *testing.T) {
	dir := gitRepo(t, map[string]string{"a.go": "l1\nl2\nl3\n"})
	// Uncommitted modification of line 2.
	writeRepoFile(t, dir, "a.go", "l1\nCHANGED\nl3\n")
	svc := serviceForRepo(t, dir)

	res, err := svc.ReadWorkspaceFile(context.Background(), "s1", "a.go")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Available || len(res.Lines) != 3 || res.Lines[1].Text != "CHANGED" {
		t.Fatalf("lines = %+v", res.Lines)
	}
	want := []diffhunk.LineChange{{Start: 2, End: 2, Kind: diffhunk.ChangeModified}}
	if !equalChanges(res.ChangedLines, want) {
		t.Fatalf("changedLines = %+v, want %+v", res.ChangedLines, want)
	}
}

func TestReadWorkspaceFile_UntrackedIsAllAdded(t *testing.T) {
	dir := gitRepo(t, map[string]string{"a.go": "x\n"})
	writeRepoFile(t, dir, "new.go", "n1\nn2\n")
	svc := serviceForRepo(t, dir)

	res, err := svc.ReadWorkspaceFile(context.Background(), "s1", "new.go")
	if err != nil {
		t.Fatal(err)
	}
	want := []diffhunk.LineChange{{Start: 1, End: 2, Kind: diffhunk.ChangeAdded}}
	if !equalChanges(res.ChangedLines, want) {
		t.Fatalf("changedLines = %+v, want %+v", res.ChangedLines, want)
	}
}

func TestReadWorkspaceFile_UnchangedHasNoMarkers(t *testing.T) {
	dir := gitRepo(t, map[string]string{"a.go": "l1\nl2\n"})
	svc := serviceForRepo(t, dir)

	res, err := svc.ReadWorkspaceFile(context.Background(), "s1", "a.go")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.ChangedLines) != 0 {
		t.Fatalf("changedLines = %+v, want empty", res.ChangedLines)
	}
}

func TestReadWorkspaceFile_NonGitDir(t *testing.T) {
	dir := t.TempDir()
	writeRepoFile(t, dir, "a.go", "l1\n")
	svc := serviceForRepo(t, dir)

	res, err := svc.ReadWorkspaceFile(context.Background(), "s1", "a.go")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Available || len(res.Lines) != 1 || len(res.ChangedLines) != 0 {
		t.Fatalf("res = %+v", res)
	}
}

func TestReadWorkspaceFile_PathEscapeRejected(t *testing.T) {
	dir := gitRepo(t, map[string]string{"a.go": "x\n"})
	svc := serviceForRepo(t, dir)

	if _, err := svc.ReadWorkspaceFile(context.Background(), "s1", "../escape.go"); err == nil {
		t.Fatal("expected error for path escaping the workspace")
	}
}

// equalStrings compares resolve candidates against their expected paths. The
// InWorkspace verdict is asserted separately, by the TestResolveWorkspaceRef_*
// InWorkspace tests, so the path-shape cases below stay readable.
func equalStrings(a []ResolveCandidate, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Path != b[i] {
			return false
		}
	}
	return true
}

func equalChanges(a, b []diffhunk.LineChange) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestReadWorkspaceFile_RelativePathIsEchoedBack(t *testing.T) {
	dir := gitRepo(t, map[string]string{"pkg/a.go": "l1\n"})
	svc := serviceForRepo(t, dir)

	res, err := svc.ReadWorkspaceFile(context.Background(), "s1", "pkg/a.go")
	if err != nil {
		t.Fatal(err)
	}
	if res.Path != "pkg/a.go" {
		t.Fatalf("path = %q, want %q", res.Path, "pkg/a.go")
	}
}

// --- candidateWithinWorkspace: the reveal-in-tree gate (Files tab) -----------
//
// Clicking a terminal file ref reveals the file in the Files tab ONLY when it
// lives inside the session's workspace; a ref pointing outside keeps the
// standalone viewer. That decision must NOT be inferred from ResolveWorkspaceRef,
// which is INTENTIONALLY UNCONFINED for absolute/`~` refs (#132) and would
// happily hand back a path outside the worktree.
//
// Like TestConfinedWorkspacePath_*, these assert on the helper's ok flag rather
// than on any downstream output: a wrong verdict here shows up as "the tree
// didn't scroll", which no endpoint-level assertion can distinguish from a
// correctly-refused reveal. Each test below was mutation-checked by deleting the
// guard it covers and confirming it goes red.

// TestCandidateWithinWorkspace_RejectsOutsideAbsolute is the core guard: an
// absolute path that resolves outside the worktree must never be revealable.
// Mutation check: drop the relWithin test in the absolute branch -> red.
func TestCandidateWithinWorkspace_RejectsOutsideAbsolute(t *testing.T) {
	dir := gitRepo(t, map[string]string{"pkg/a.go": "package a\n"})
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("s\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{outside, "/etc/passwd", "/"} {
		if rel, ok := candidateWithinWorkspace(dir, p); ok {
			t.Errorf("%q must NOT be revealable, got ok with rel=%q", p, rel)
		}
	}
	// Positive control: the same shape of input, inside the workspace, resolves.
	inside := filepath.Join(dir, "pkg", "a.go")
	if rel, ok := candidateWithinWorkspace(dir, inside); !ok || rel != "pkg/a.go" {
		t.Fatalf("in-workspace absolute broke: ok=%v rel=%q, want true/%q", ok, rel, "pkg/a.go")
	}
}

// TestCandidateWithinWorkspace_RejectsTildeOutside covers the `~` shape, which
// refTarget widens exactly like an absolute path.
// Mutation check: route `~` down the relative branch -> red.
func TestCandidateWithinWorkspace_RejectsTildeOutside(t *testing.T) {
	dir := gitRepo(t, map[string]string{"pkg/a.go": "package a\n"})
	if home, err := os.UserHomeDir(); err != nil || home == "" {
		t.Skip("no home dir")
	}
	for _, p := range []string{"~/.ssh/id_rsa", "~"} {
		if rel, ok := candidateWithinWorkspace(dir, p); ok {
			t.Errorf("%q must NOT be revealable, got ok with rel=%q", p, rel)
		}
	}
}

// TestCandidateWithinWorkspace_SymlinkEscape pins the sharp edge that
// previewutil.ConfinedPath is purely lexical: a link inside the worktree
// pointing outside it must not be revealable, by either shape of input.
// Mutation check: drop the EvalSymlinks re-check -> red.
func TestCandidateWithinWorkspace_SymlinkEscape(t *testing.T) {
	dir := gitRepo(t, map[string]string{"pkg/a.go": "package a\n"})
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("s\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "link.txt")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if rel, ok := candidateWithinWorkspace(dir, "link.txt"); ok {
		t.Errorf("relative symlink escaping the worktree must be rejected, got rel=%q", rel)
	}
	if rel, ok := candidateWithinWorkspace(dir, filepath.Join(dir, "link.txt")); ok {
		t.Errorf("absolute symlink escaping the worktree must be rejected, got rel=%q", rel)
	}
}

// TestCandidateWithinWorkspace_EmptyIsRejected pins the other ConfinedPath sharp
// edge: it rewrites an empty or "." path to "index.html", which would make the
// tree try to reveal a file nobody asked for.
//
// Mutation check, honestly reported: deleting the empty/workspace guard in
// candidateWithinWorkspace leaves this test GREEN, and that is by design rather
// than a weak test. Empty is rejected twice over — confinedWorkspacePath has its
// own empty guard, and relWithin/absRoot reject a "" root because filepath.Rel
// errors against a relative root. The guard is kept anyway so this function's
// contract does not silently depend on those two incidentals (note
// filepath.Abs("") returns the CWD, so the defence is subtler than it looks).
// What this test pins is the BEHAVIOUR, which no single deletion can break.
func TestCandidateWithinWorkspace_EmptyIsRejected(t *testing.T) {
	dir := gitRepo(t, map[string]string{"pkg/a.go": "package a\n"})
	for _, p := range []string{"", "   ", "."} {
		if rel, ok := candidateWithinWorkspace(dir, p); ok {
			t.Errorf("%q must be rejected, not rewritten; got rel=%q", p, rel)
		}
	}
	if rel, ok := candidateWithinWorkspace("", "pkg/a.go"); ok {
		t.Errorf("an empty workspace must be rejected, got rel=%q", rel)
	}
}

// TestCandidateWithinWorkspace_RelativeStaysConfined guards the traversal shape.
// Mutation check: swap confinedWorkspacePath for a bare filepath.Join -> red.
func TestCandidateWithinWorkspace_RelativeStaysConfined(t *testing.T) {
	dir := gitRepo(t, map[string]string{"pkg/a.go": "package a\n"})
	for _, p := range []string{"../escape.go", "pkg/../../escape.go"} {
		if rel, ok := candidateWithinWorkspace(dir, p); ok {
			t.Errorf("%q must NOT escape the worktree, got rel=%q", p, rel)
		}
	}
	if rel, ok := candidateWithinWorkspace(dir, "pkg/a.go"); !ok || rel != "pkg/a.go" {
		t.Fatalf("plain relative broke: ok=%v rel=%q", ok, rel)
	}
}

// TestCandidateWithinWorkspace_MacOSPrivateVar guards the /var -> /private/var
// trap called out on resolvedRoot: a workspace under a symlinked temp dir must
// still recognise its own files, or reveal silently never fires on macOS.
// Mutation check: compare against absRoot instead of resolvedRoot -> red on macOS.
func TestCandidateWithinWorkspace_MacOSPrivateVar(t *testing.T) {
	dir := gitRepo(t, map[string]string{"pkg/a.go": "package a\n"})
	resolved := mustEvalSymlinks(t, dir)
	if resolved == dir {
		t.Skip("temp dir is not symlinked on this platform")
	}
	if rel, ok := candidateWithinWorkspace(dir, filepath.Join(resolved, "pkg", "a.go")); !ok || rel != "pkg/a.go" {
		t.Fatalf("symlink-resolved workspace path broke: ok=%v rel=%q", ok, rel)
	}
}

// TestResolveWorkspaceRef_InWorkspaceVerdict pins the flag the Files tab reveal
// depends on, across all four ref shapes. equalStrings deliberately ignores the
// verdict, so without this test the flag would be entirely unasserted.
func TestResolveWorkspaceRef_InWorkspaceVerdict(t *testing.T) {
	dir := gitRepo(t, map[string]string{"pkg/a.go": "x\n"})
	svc := serviceForRepo(t, dir)
	outsideDir := t.TempDir()
	outside := filepath.Join(outsideDir, "a.go")
	if err := os.WriteFile(outside, []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name        string
		ref         string
		wantPath    string
		wantInWorks bool
	}{
		{"relative inside", "pkg/a.go", "pkg/a.go", true},
		{"bare inside", "a.go", "pkg/a.go", true},
		{"absolute inside", filepath.Join(dir, "pkg", "a.go"), "pkg/a.go", true},
		{"absolute outside", outside, mustEvalSymlinks(t, outside), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := svc.ResolveWorkspaceRef(context.Background(), "s1", tc.ref)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 {
				t.Fatalf("candidates = %+v, want exactly 1", got)
			}
			if got[0].Path != tc.wantPath {
				t.Errorf("path = %q, want %q", got[0].Path, tc.wantPath)
			}
			if got[0].InWorkspace != tc.wantInWorks {
				t.Errorf("inWorkspace = %v, want %v", got[0].InWorkspace, tc.wantInWorks)
			}
		})
	}
}

// TestResolveWorkspaceRef_OutsideCandidateIsNotRevealable is the regression guard
// for the whole point of the feature: a ref outside the project must never come
// back marked revealable, however it is spelled.
func TestResolveWorkspaceRef_OutsideCandidateIsNotRevealable(t *testing.T) {
	dir := gitRepo(t, map[string]string{"pkg/a.go": "x\n"})
	svc := serviceForRepo(t, dir)
	for _, ref := range []string{"/etc/hosts", "~/.ssh/id_rsa"} {
		got, err := svc.ResolveWorkspaceRef(context.Background(), "s1", ref)
		if err != nil {
			t.Fatalf("%s: %v", ref, err)
		}
		for _, c := range got {
			if c.InWorkspace {
				t.Errorf("%s resolved to a revealable candidate %q", ref, c.Path)
			}
		}
	}
}
