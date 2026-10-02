package session

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// changesTestRepo's keep.go is "l1\nl2\nl3\n" on main, "l1\nCHANGED\nl3\nl4\n"
// at the branch's HEAD, and carries one more uncommitted line on disk. So the
// two bases of the SAME file are three different texts, and a base that read
// the wrong one is caught.

func fileBase(t *testing.T, svc *Service, path string, base DiffBase) WorkspaceFileBaseResult {
	t.Helper()
	res, err := svc.WorkspaceFileBase(context.Background(), "s1", FileBaseQuery{Path: path, Base: base})
	if err != nil {
		t.Fatalf("%s %s: %v", base, path, err)
	}
	return res
}

func TestWorkspaceFileBase_HeadIsTheLastCommit(t *testing.T) {
	svc, _ := fileDiffService(t, "main")

	res := fileBase(t, svc, "keep.go", DiffBaseHead)
	if !res.Available || !res.Exists {
		t.Fatalf("want HEAD's keep.go, got %+v", res)
	}
	if res.Text != "l1\nCHANGED\nl3\nl4\n" {
		t.Fatalf("HEAD text = %q", res.Text)
	}
	if len(res.Revision) != 40 {
		t.Fatalf("revision should be the full commit sha, got %q", res.Revision)
	}
}

func TestWorkspaceFileBase_TargetIsTheMergeBase(t *testing.T) {
	svc, _ := fileDiffService(t, "main")

	res := fileBase(t, svc, "keep.go", DiffBaseTarget)
	if !res.Available || !res.Exists {
		t.Fatalf("want main's keep.go, got %+v", res)
	}
	if res.Text != "l1\nl2\nl3\n" {
		t.Fatalf("target text = %q", res.Text)
	}
}

// An empty base keeps the branch level, as the per-file diff does.
func TestWorkspaceFileBase_EmptyBaseDefaultsToTarget(t *testing.T) {
	svc, _ := fileDiffService(t, "main")

	if got := fileBase(t, svc, "keep.go", ""); got.Text != "l1\nl2\nl3\n" {
		t.Fatalf("empty base answered %q, want the target's text", got.Text)
	}
}

// A file the base does not have is not an error and not "unknown": every line
// of it is an addition, and the caller can only say so if it is told plainly.
func TestWorkspaceFileBase_NewAndUntrackedFilesDoNotExistAtTheBase(t *testing.T) {
	svc, _ := fileDiffService(t, "main")

	for _, tc := range []struct {
		path string
		base DiffBase
	}{
		{"added.go", DiffBaseTarget},   // committed on the branch, absent on main
		{"untracked.go", DiffBaseHead}, // never committed at all
		{"untracked.go", DiffBaseTarget},
	} {
		res := fileBase(t, svc, tc.path, tc.base)
		if !res.Available || res.Exists || res.Text != "" {
			t.Fatalf("%s at %s: want available and absent, got %+v", tc.path, tc.base, res)
		}
	}
	// And one that IS in HEAD reads, so the case above is not passing vacuously.
	if res := fileBase(t, svc, "added.go", DiffBaseHead); !res.Exists || res.Text != "brand new\n" {
		t.Fatalf("added.go at HEAD = %+v", res)
	}
}

// The HEAD base is a local question and must not depend on resolving a target.
func TestWorkspaceFileBase_HeadNeedsNoTargetBranch(t *testing.T) {
	svc, _ := fileDiffService(t, "no-such-branch-anywhere")

	if res := fileBase(t, svc, "keep.go", DiffBaseHead); !res.Available {
		t.Fatalf("HEAD base must answer with no resolvable target, got %+v", res)
	}
	res := fileBase(t, svc, "keep.go", DiffBaseTarget)
	if res.Available || res.Reason != BaseUnavailableNoTarget {
		t.Fatalf("target base has nothing to resolve; want %q, got %+v", BaseUnavailableNoTarget, res)
	}
}

// 🗝 Parked on another branch, the branch's changes are measured to its tip and
// not to the files on disk - so a base for the BUFFER of those files would be
// compared against the wrong thing. Refused, not answered.
func TestWorkspaceFileBase_TargetIsRefusedOffTheBranch(t *testing.T) {
	dir := changesTestRepo(t)
	fake := newFakeStore()
	fake.putSessionWithWorkspace("s1", dir)
	rec := fake.sessions["s1"]
	rec.PRTarget = "main"
	rec.Metadata.Branch = "feature/x"
	fake.sessions["s1"] = rec
	svc := newServiceWithStore(t, &multiPRFakeStore{fakeStore: fake})
	// The throwaway repo's own checkout, forced: its uncommitted keep.go edit is
	// fixture, not anyone's work.
	cmd := exec.Command("git", "checkout", "-q", "-f", "main")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git checkout: %v\n%s", err, out)
	}

	res := fileBase(t, svc, "keep.go", DiffBaseTarget)
	if res.Available || res.Reason != BaseUnavailableNotOnBranch {
		t.Fatalf("want %q off the branch, got %+v", BaseUnavailableNotOnBranch, res)
	}
	// HEAD is still a fine question to ask about whatever is checked out.
	if res := fileBase(t, svc, "keep.go", DiffBaseHead); res.Text != "l1\nl2\nl3\n" {
		t.Fatalf("HEAD on main = %+v", res)
	}
}

func TestWorkspaceFileBase_BinaryIsUnavailable(t *testing.T) {
	svc, _ := fileDiffService(t, "main")

	res := fileBase(t, svc, "img.bin", DiffBaseHead)
	if res.Available || res.Reason != UnavailableBinary {
		t.Fatalf("want %q, got %+v", UnavailableBinary, res)
	}
}

// eol conversion is applied, so a CRLF checkout compares equal to its blob
// instead of being "changed" on every line.
func TestWorkspaceFileBase_AppliesCheckoutFilters(t *testing.T) {
	svc, dir := fileDiffService(t, "main")
	if err := os.WriteFile(filepath.Join(dir, ".gitattributes"), []byte("*.txt eol=crlf\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "crlf.txt"), []byte("a\r\nb\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-qm", "crlf"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	res := fileBase(t, svc, "crlf.txt", DiffBaseHead)
	if res.Text != "a\r\nb\r\n" {
		t.Fatalf("want the working-tree form, got %q", res.Text)
	}
}

func TestWorkspaceFileBase_UnbornHeadHasEveryFileNew(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fake := newFakeStore()
	fake.putSessionWithWorkspace("s1", dir)
	svc := newServiceWithStore(t, &multiPRFakeStore{fakeStore: fake})

	res := fileBase(t, svc, "a.go", DiffBaseHead)
	if !res.Available || res.Exists {
		t.Fatalf("an unborn HEAD has no a.go yet; want available and absent, got %+v", res)
	}
}

// Git objects are read for a path inside the worktree only: an absolute path is
// refused, not resolved.
func TestWorkspaceFileBase_AbsolutePathIsNotFound(t *testing.T) {
	svc, dir := fileDiffService(t, "main")

	_, err := svc.WorkspaceFileBase(context.Background(), "s1", FileBaseQuery{Path: filepath.Join(dir, "keep.go"), Base: DiffBaseHead})
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) || apiErr.Kind != apierr.KindNotFound {
		t.Fatalf("want NotFound, got %v", err)
	}
}

func TestWorkspaceFileBase_UnknownBaseIsRefused(t *testing.T) {
	svc, _ := fileDiffService(t, "main")

	_, err := svc.WorkspaceFileBase(context.Background(), "s1", FileBaseQuery{Path: "keep.go", Base: "origin"})
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) || apiErr.Kind != apierr.KindInvalid {
		t.Fatalf("want Invalid, got %v", err)
	}
}
