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

func TestResolveWorkspaceRef_AbsoluteOutsideFallsBackToBasename(t *testing.T) {
	dir := gitRepo(t, map[string]string{"pkg/a.go": "x\n"})
	svc := serviceForRepo(t, dir)
	// An absolute path that points OUTSIDE the workspace must never be read;
	// resolution falls back to a basename match inside the workspace.
	outside := filepath.Join(t.TempDir(), "some", "elsewhere", "a.go")

	got, err := svc.ResolveWorkspaceRef(context.Background(), "s1", outside)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"pkg/a.go"}; !equalStrings(got, want) {
		t.Fatalf("candidates = %v, want %v", got, want)
	}
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
