package session

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// writeInput builds a write request whose baseHash is whatever the file holds
// right now, i.e. the request a client makes when nothing changed underneath it.
func writeInput(t *testing.T, svc *Service, path, content string) WriteWorkspaceFileInput {
	t.Helper()
	res, err := svc.ReadWorkspaceFile(context.Background(), "s1", path)
	if err != nil {
		t.Fatalf("read %s for baseHash: %v", path, err)
	}
	return WriteWorkspaceFileInput{Path: path, Content: content, BaseHash: res.ContentHash}
}

// wantAPIError asserts the error is an apierr with the given kind and code.
func wantAPIError(t *testing.T, err error, kind apierr.Kind, code string) *apierr.Error {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error %s, got nil", code)
	}
	var e *apierr.Error
	if !errors.As(err, &e) {
		t.Fatalf("error %v is not an *apierr.Error", err)
	}
	if e.Kind != kind || e.Code != code {
		t.Fatalf("error = kind %d / %s, want kind %d / %s", e.Kind, e.Code, kind, code)
	}
	return e
}

func readBack(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path) //nolint:gosec // test-controlled path
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestWriteWorkspaceFile_WritesContentVerbatim(t *testing.T) {
	dir := gitRepo(t, map[string]string{"pkg/a.go": "l1\nl2\n"})
	svc := serviceForRepo(t, dir)

	res, err := svc.WriteWorkspaceFile(context.Background(), "s1", writeInput(t, svc, "pkg/a.go", "l1\nCHANGED\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := readBack(t, filepath.Join(dir, "pkg", "a.go")); got != "l1\nCHANGED\n" {
		t.Fatalf("file = %q, want %q", got, "l1\nCHANGED\n")
	}
	if res.Path != "pkg/a.go" {
		t.Fatalf("path = %q, want pkg/a.go", res.Path)
	}
	if res.Size != len("l1\nCHANGED\n") {
		t.Fatalf("size = %d, want %d", res.Size, len("l1\nCHANGED\n"))
	}
	if !strings.HasPrefix(res.ContentHash, "sha256:") {
		t.Fatalf("contentHash = %q, want a sha256: prefix", res.ContentHash)
	}
	if len(res.ChangedLines) == 0 {
		t.Fatal("changedLines is empty, want the modified line marked")
	}
}

// The write route must not normalise: a file with no trailing newline keeps
// none, and one with a trailing newline keeps exactly one. The spike's save()
// silently dropped the final \n, which turned a one-line edit into two.
func TestWriteWorkspaceFile_DoesNotNormaliseTrailingNewline(t *testing.T) {
	for _, tc := range []struct{ name, content string }{
		{"without", "a\nb"},
		{"with", "a\nb\n"},
		{"empty", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := gitRepo(t, map[string]string{"a.txt": "seed\n"})
			svc := serviceForRepo(t, dir)

			if _, err := svc.WriteWorkspaceFile(context.Background(), "s1", writeInput(t, svc, "a.txt", tc.content)); err != nil {
				t.Fatal(err)
			}
			if got := readBack(t, filepath.Join(dir, "a.txt")); got != tc.content {
				t.Fatalf("file = %q, want %q", got, tc.content)
			}
		})
	}
}

// The read response has to carry the two things a byte-exact round trip needs:
// the precondition token, and whether the file ended in a newline (the lines
// array cannot express it).
func TestReadWorkspaceFile_ReportsContentHashAndTrailingNewline(t *testing.T) {
	dir := gitRepo(t, map[string]string{"a.txt": "x\n", "b.txt": "x"})
	svc := serviceForRepo(t, dir)

	withNL, err := svc.ReadWorkspaceFile(context.Background(), "s1", "a.txt")
	if err != nil {
		t.Fatal(err)
	}
	if !withNL.TrailingNewline {
		t.Fatal("a.txt: trailingNewline = false, want true")
	}
	if !strings.HasPrefix(withNL.ContentHash, "sha256:") {
		t.Fatalf("a.txt: contentHash = %q, want a sha256: prefix", withNL.ContentHash)
	}

	withoutNL, err := svc.ReadWorkspaceFile(context.Background(), "s1", "b.txt")
	if err != nil {
		t.Fatal(err)
	}
	if withoutNL.TrailingNewline {
		t.Fatal("b.txt: trailingNewline = true, want false")
	}
	if withoutNL.ContentHash == withNL.ContentHash {
		t.Fatal("different files hashed the same")
	}
}

// A hash handed out by the read must still be accepted by the write; if the two
// sides hashed different bytes, every save would conflict.
func TestWriteWorkspaceFile_AcceptsHashFromRead(t *testing.T) {
	dir := gitRepo(t, map[string]string{"a.txt": "one\ntwo\n"})
	svc := serviceForRepo(t, dir)

	read, err := svc.ReadWorkspaceFile(context.Background(), "s1", "a.txt")
	if err != nil {
		t.Fatal(err)
	}
	in := WriteWorkspaceFileInput{Path: "a.txt", Content: "one\ntwo\nthree\n", BaseHash: read.ContentHash}
	if _, err := svc.WriteWorkspaceFile(context.Background(), "s1", in); err != nil {
		t.Fatalf("write with the hash the read handed out: %v", err)
	}
}

func TestWriteWorkspaceFile_ConflictWhenFileChangedUnderneath(t *testing.T) {
	dir := gitRepo(t, map[string]string{"a.txt": "original\n"})
	svc := serviceForRepo(t, dir)
	in := writeInput(t, svc, "a.txt", "edited by the human\n")

	// An agent writes the same file between the read and the save.
	writeRepoFile(t, dir, "a.txt", "written by an agent\n")

	_, err := svc.WriteWorkspaceFile(context.Background(), "s1", in)
	e := wantAPIError(t, err, apierr.KindConflict, "WORKSPACE_FILE_CONFLICT")
	if e.Details["currentHash"] == nil || e.Details["currentSize"] == nil {
		t.Fatalf("details = %v, want currentHash and currentSize", e.Details)
	}
	if got := readBack(t, filepath.Join(dir, "a.txt")); got != "written by an agent\n" {
		t.Fatalf("file = %q, the agent's write was clobbered", got)
	}
}

// Two saves from the same baseline: the first wins, the second is refused. This
// is the "never silently clobber" requirement stated as a sequence.
func TestWriteWorkspaceFile_SecondWriteFromSameBaselineIsRefused(t *testing.T) {
	dir := gitRepo(t, map[string]string{"a.txt": "base\n"})
	svc := serviceForRepo(t, dir)
	first := writeInput(t, svc, "a.txt", "first\n")
	second := writeInput(t, svc, "a.txt", "second\n")

	if _, err := svc.WriteWorkspaceFile(context.Background(), "s1", first); err != nil {
		t.Fatal(err)
	}
	_, err := svc.WriteWorkspaceFile(context.Background(), "s1", second)
	wantAPIError(t, err, apierr.KindConflict, "WORKSPACE_FILE_CONFLICT")
	if got := readBack(t, filepath.Join(dir, "a.txt")); got != "first\n" {
		t.Fatalf("file = %q, want the first write to stand", got)
	}
}

// A rewrite with identical bytes is not a conflict: nothing the client holds is
// stale. This is why the precondition is a content hash and not an mtime.
func TestWriteWorkspaceFile_IdenticalRewriteUnderneathIsNotAConflict(t *testing.T) {
	dir := gitRepo(t, map[string]string{"a.txt": "same\n"})
	svc := serviceForRepo(t, dir)
	in := writeInput(t, svc, "a.txt", "edited\n")

	writeRepoFile(t, dir, "a.txt", "same\n") // same bytes, new mtime

	if _, err := svc.WriteWorkspaceFile(context.Background(), "s1", in); err != nil {
		t.Fatalf("identical rewrite should not conflict: %v", err)
	}
}

func TestWriteWorkspaceFile_MissingBaseHashIsRefused(t *testing.T) {
	dir := gitRepo(t, map[string]string{"a.txt": "x\n"})
	svc := serviceForRepo(t, dir)

	_, err := svc.WriteWorkspaceFile(context.Background(), "s1", WriteWorkspaceFileInput{Path: "a.txt", Content: "y\n"})
	wantAPIError(t, err, apierr.KindInvalid, "WORKSPACE_FILE_BASE_HASH_REQUIRED")
	if got := readBack(t, filepath.Join(dir, "a.txt")); got != "x\n" {
		t.Fatalf("file = %q, a hashless write must not land", got)
	}
}

// The tail-destroying case: the client was shown the first maxFileLines lines of
// a longer file, so saving what it holds would delete the rest. The server
// decides this from the file on disk; the client is never asked to remember.
func TestWriteWorkspaceFile_RefusesWhenTheReadWasTruncated(t *testing.T) {
	long := strings.Repeat("line\n", maxFileLines+5)
	dir := gitRepo(t, map[string]string{"long.txt": long})
	svc := serviceForRepo(t, dir)

	read, err := svc.ReadWorkspaceFile(context.Background(), "s1", "long.txt")
	if err != nil {
		t.Fatal(err)
	}
	if !read.Truncated {
		t.Fatal("fixture was not truncated by the read; the test proves nothing")
	}
	in := WriteWorkspaceFileInput{Path: "long.txt", Content: "line\n", BaseHash: read.ContentHash}

	_, werr := svc.WriteWorkspaceFile(context.Background(), "s1", in)
	e := wantAPIError(t, werr, apierr.KindConflict, "WORKSPACE_FILE_NOT_EDITABLE")
	if e.Details["reason"] != UnavailableTruncated {
		t.Fatalf("details = %v, want reason %q", e.Details, UnavailableTruncated)
	}
	if got := readBack(t, filepath.Join(dir, "long.txt")); got != long {
		t.Fatal("the truncated file was written anyway; its tail is gone")
	}
}

func TestWriteWorkspaceFile_RefusesBinaryAndOversizeBaselines(t *testing.T) {
	for _, tc := range []struct {
		name, content, reason string
	}{
		{"binary", "head\x00tail", UnavailableBinary},
		{"too_large", strings.Repeat("x", maxWorkspaceFileBytes+1), UnavailableTooLarge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := gitRepo(t, map[string]string{"blob.bin": tc.content})
			svc := serviceForRepo(t, dir)

			in := WriteWorkspaceFileInput{Path: "blob.bin", Content: "replaced\n", BaseHash: "sha256:whatever"}
			_, err := svc.WriteWorkspaceFile(context.Background(), "s1", in)
			e := wantAPIError(t, err, apierr.KindConflict, "WORKSPACE_FILE_NOT_EDITABLE")
			if e.Details["reason"] != tc.reason {
				t.Fatalf("details = %v, want reason %q", e.Details, tc.reason)
			}
			if got := readBack(t, filepath.Join(dir, "blob.bin")); got != tc.content {
				t.Fatal("file was overwritten anyway")
			}
		})
	}
}

// Content that the viewer could not show back is refused up front, rather than
// leaving a file the editor can only display in part and can never save again.
func TestWriteWorkspaceFile_RefusesUnrenderableContent(t *testing.T) {
	for _, tc := range []struct {
		name, content, reason string
	}{
		{"too_many_lines", strings.Repeat("l\n", maxFileLines+1), ContentTooManyLines},
		{"too_large", strings.Repeat("x", maxWorkspaceFileBytes+1), ContentTooLarge},
		{"binary", "ok\x00nope", ContentBinary},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := gitRepo(t, map[string]string{"a.txt": "x\n"})
			svc := serviceForRepo(t, dir)

			_, err := svc.WriteWorkspaceFile(context.Background(), "s1", writeInput(t, svc, "a.txt", tc.content))
			e := wantAPIError(t, err, apierr.KindInvalid, "WORKSPACE_FILE_CONTENT_REJECTED")
			if e.Details["reason"] != tc.reason {
				t.Fatalf("details = %v, want reason %q", e.Details, tc.reason)
			}
			if got := readBack(t, filepath.Join(dir, "a.txt")); got != "x\n" {
				t.Fatalf("file = %q, want the original", got)
			}
		})
	}
}

// Traversal is the security boundary. Every shape that names something outside
// the workspace is refused, and nothing outside is touched.
func TestWriteWorkspaceFile_RefusesPathsOutsideTheWorkspace(t *testing.T) {
	outsideDir := t.TempDir()
	outside := filepath.Join(outsideDir, "secret.txt")
	if err := os.WriteFile(outside, []byte("secret\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "inhome.txt"), []byte("home\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct{ name, path string }{
		{"parent_segment", "../secret.txt"},
		{"parent_segment_deep", "pkg/../../secret.txt"},
		{"parent_segment_backslash", `..\secret.txt`},
		{"absolute", outside},
		{"tilde", "~/inhome.txt"},
		{"empty", ""},
		{"dot", "."},
		{"root", "/"},
		{"double_slash", "//"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", home)
			dir := gitRepo(t, map[string]string{"pkg/a.go": "x\n"})
			svc := serviceForRepo(t, dir)

			_, err := svc.WriteWorkspaceFile(context.Background(), "s1", WriteWorkspaceFileInput{
				Path: tc.path, Content: "pwned\n", BaseHash: "sha256:whatever",
			})
			wantAPIError(t, err, apierr.KindInvalid, "WORKSPACE_FILE_PATH_INVALID")
			if got := readBack(t, outside); got != "secret\n" {
				t.Fatalf("outside file = %q, it was written through", got)
			}
			if got := readBack(t, filepath.Join(home, "inhome.txt")); got != "home\n" {
				t.Fatalf("home file = %q, it was written through", got)
			}
		})
	}
}

// A path that only leaves the workspace AFTER its symlinks are followed: the
// lexical check passes and the write must still be refused.
func TestWriteWorkspaceFile_RefusesSymlinkEscapes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs privileges on Windows")
	}
	outsideDir := t.TempDir()
	outsideFile := filepath.Join(outsideDir, "secret.txt")
	if err := os.WriteFile(outsideFile, []byte("secret\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name, path string
		link       func(dir string)
	}{
		{
			name: "file_symlink_out",
			path: "link.txt",
			link: func(dir string) {
				if err := os.Symlink(outsideFile, filepath.Join(dir, "link.txt")); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "parent_dir_symlink_out",
			path: "linkdir/secret.txt",
			link: func(dir string) {
				if err := os.Symlink(outsideDir, filepath.Join(dir, "linkdir")); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := gitRepo(t, map[string]string{"pkg/a.go": "x\n"})
			tc.link(dir)
			svc := serviceForRepo(t, dir)

			_, err := svc.WriteWorkspaceFile(context.Background(), "s1", WriteWorkspaceFileInput{
				Path: tc.path, Content: "pwned\n", BaseHash: "sha256:whatever",
			})
			wantAPIError(t, err, apierr.KindInvalid, "WORKSPACE_FILE_PATH_INVALID")
			if got := readBack(t, outsideFile); got != "secret\n" {
				t.Fatalf("outside file = %q, the symlink was written through", got)
			}
		})
	}
}

// A symlink that stays inside the workspace is legitimate, and the write must go
// THROUGH it: replacing the link with a regular file would silently detach it.
func TestWriteWorkspaceFile_WritesThroughAnInWorkspaceSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs privileges on Windows")
	}
	dir := gitRepo(t, map[string]string{"real/a.txt": "old\n"})
	link := filepath.Join(dir, "link.txt")
	if err := os.Symlink(filepath.Join(dir, "real", "a.txt"), link); err != nil {
		t.Fatal(err)
	}
	svc := serviceForRepo(t, dir)

	if _, err := svc.WriteWorkspaceFile(context.Background(), "s1", writeInput(t, svc, "link.txt", "new\n")); err != nil {
		t.Fatal(err)
	}
	if got := readBack(t, filepath.Join(dir, "real", "a.txt")); got != "new\n" {
		t.Fatalf("target = %q, want the write to reach it through the link", got)
	}
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("the symlink was replaced by a regular file")
	}
}

func TestWriteWorkspaceFile_DoesNotCreateFiles(t *testing.T) {
	dir := gitRepo(t, map[string]string{"pkg/a.go": "x\n"})
	svc := serviceForRepo(t, dir)

	for _, path := range []string{"pkg/new.go", "brandnew/deep.go", "pkg"} {
		_, err := svc.WriteWorkspaceFile(context.Background(), "s1", WriteWorkspaceFileInput{
			Path: path, Content: "x\n", BaseHash: "sha256:whatever",
		})
		wantAPIError(t, err, apierr.KindNotFound, "WORKSPACE_FILE_NOT_FOUND")
	}
	if _, err := os.Stat(filepath.Join(dir, "pkg", "new.go")); !os.IsNotExist(err) {
		t.Fatal("the route created a file")
	}
}

func TestWriteWorkspaceFile_PreservesFileMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permission bits")
	}
	dir := gitRepo(t, map[string]string{"run.sh": "#!/bin/sh\n"})
	if err := os.Chmod(filepath.Join(dir, "run.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	svc := serviceForRepo(t, dir)

	if _, err := svc.WriteWorkspaceFile(context.Background(), "s1", writeInput(t, svc, "run.sh", "#!/bin/sh\necho hi\n")); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, "run.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("mode = %v, want 0755", info.Mode().Perm())
	}
}

func TestWriteWorkspaceFile_UnknownSession(t *testing.T) {
	svc := serviceForRepo(t, gitRepo(t, map[string]string{"a.txt": "x\n"}))

	_, err := svc.WriteWorkspaceFile(context.Background(), "nope", WriteWorkspaceFileInput{
		Path: "a.txt", Content: "y\n", BaseHash: "sha256:whatever",
	})
	wantAPIError(t, err, apierr.KindNotFound, "SESSION_NOT_FOUND")
}

func TestWriteWorkspaceFile_SessionWithoutWorkspace(t *testing.T) {
	fake := newFakeStore()
	fake.putSessionWithWorkspace("s1", "")
	svc := newServiceWithStore(t, fake)

	_, err := svc.WriteWorkspaceFile(context.Background(), "s1", WriteWorkspaceFileInput{
		Path: "a.txt", Content: "y\n", BaseHash: "sha256:whatever",
	})
	wantAPIError(t, err, apierr.KindNotFound, "WORKSPACE_FILE_NOT_FOUND")
}
