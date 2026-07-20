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

func equalStrings(a, b []string) bool {
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
