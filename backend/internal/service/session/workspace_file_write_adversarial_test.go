package session

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// This file attacks the write route from the outside: paths that only escape
// after cleaning or after symlink resolution, baselines the reader could not
// have held in full, and REAL concurrent writers (separate processes and
// parallel goroutines) rather than a hand-rolled "and then the file changed".
//
// Every containment case asserts the file OUTSIDE the workspace still holds its
// original bytes. A refusal that returns the right code while having already
// written through is the failure this is looking for, and only the bytes can
// tell the two apart.

// advBytes reads a file's exact bytes, failing the test if it cannot.
func advBytes(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path) //nolint:gosec // test-controlled path
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

// advHash reads a file through the service and returns the precondition token
// the editor would have been handed.
func advHash(t *testing.T, svc *Service, path string) string {
	t.Helper()
	res, err := svc.ReadWorkspaceFile(context.Background(), "s1", path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return res.ContentHash
}

// advTempLeftovers lists the atomic-write temp files still sitting in dir. A
// leftover is not cosmetic: it lands in the user's repo, shows up in git status
// and follows an agent into its next commit.
func advTempLeftovers(t *testing.T, dir string) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, ".*.ao-*"))
	if err != nil {
		t.Fatal(err)
	}
	return matches
}

// runIn runs a command in dir as a REAL subprocess - the "another agent wrote
// the file underneath you" case, which a same-goroutine os.WriteFile only
// imitates.
func runIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command(args[0], args[1:]...) //nolint:gosec // test-controlled args
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v: %v\n%s", args, err, out)
	}
}

// ---------------------------------------------------------------------------
// 1. Traversal
// ---------------------------------------------------------------------------

// Paths that are not the plain "../secret" shape: ones that only escape after
// cleaning, ones carrying an encoding the server must NOT decode, and ones with
// bytes that terminate a C string. Each must fail, and the outside file must
// still hold its bytes afterwards.
func TestWriteWorkspaceFile_AdversarialPathsCannotEscape(t *testing.T) {
	outsideDir := t.TempDir()
	outside := filepath.Join(outsideDir, "secret.txt")
	if err := os.WriteFile(outside, []byte("secret\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	inHome := filepath.Join(home, "inhome.txt")
	if err := os.WriteFile(inHome, []byte("home\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cases := []struct{ name, path string }{
		{"dot_then_parent", "pkg/./../../secret.txt"},
		{"leading_dot_parent", "./../secret.txt"},
		{"parent_chain", "../../../../../../../../etc/ao-pwned"},
		{"percent_encoded_parent", "%2e%2e/secret.txt"},
		{"percent_encoded_slash", "..%2fsecret.txt"},
		{"quad_dot_double_slash", "....//secret.txt"},
		{"nul_byte", "pkg/a.go\x00/../../secret.txt"},
		{"newline_in_path", "pkg/a.go\n../../secret.txt"},
		{"double_slash_root", "//secret.txt"},
		{"absolute_with_parent", filepath.Join(outsideDir, "sub", "..", "secret.txt")},
		{"tilde_only", "~"},
		{"tilde_parent", "~/../secret.txt"},
		{"whitespace_only", "   "},
		{"trailing_slash_dir", "pkg/"},
		{"directory", "pkg"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", home)
			// Same-named files INSIDE the workspace, so a traversal that is
			// clamped rather than rejected lands on a real file here and is
			// caught. Without them a clamped escape resolves to a path that
			// does not exist and the case passes for the wrong reason.
			dir := gitRepo(t, map[string]string{
				"pkg/a.go":   "keep\n",
				"secret.txt": "in the workspace\n",
				"inhome.txt": "in the workspace\n",
			})
			svc := serviceForRepo(t, dir)

			_, err := svc.WriteWorkspaceFile(context.Background(), "s1", WriteWorkspaceFileInput{
				Path: tc.path, Content: "pwned\n", BaseHash: advHash(t, svc, "pkg/a.go"),
			})
			if err == nil {
				t.Fatalf("path %q was accepted", tc.path)
			}
			var e *apierr.Error
			if !errors.As(err, &e) || (e.Kind != apierr.KindInvalid && e.Kind != apierr.KindNotFound) {
				t.Fatalf("path %q gave %v, want an invalid/not-found apierr", tc.path, err)
			}
			if got := advBytes(t, outside); got != "secret\n" {
				t.Fatalf("outside file = %q, it was written through", got)
			}
			if got := advBytes(t, inHome); got != "home\n" {
				t.Fatalf("home file = %q, it was written through", got)
			}
			// The in-workspace sentinel matters too: ConfinedPath CLAMPS
			// traversal, so a path that escapes lexically can land on a
			// DIFFERENT in-workspace file instead of being refused.
			for _, sentinel := range []string{"pkg/a.go", "secret.txt", "inhome.txt"} {
				want := "in the workspace\n"
				if sentinel == "pkg/a.go" {
					want = "keep\n"
				}
				if got := advBytes(t, filepath.Join(dir, filepath.FromSlash(sentinel))); got != want {
					t.Fatalf("in-workspace %s = %q, the clamped path wrote over it", sentinel, got)
				}
			}
			if leftover := advTempLeftovers(t, filepath.Join(dir, "pkg")); len(leftover) > 0 {
				t.Fatalf("refused write left temp files behind: %v", leftover)
			}
		})
	}
}

// A single symlink hop out is covered upstream; these are the shapes that need
// the check to be applied to the FULLY resolved path: a link to a link, and a
// link nested under a real directory.
func TestWriteWorkspaceFile_SymlinkChainsCannotEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs privileges on Windows")
	}
	outsideDir := t.TempDir()
	outside := filepath.Join(outsideDir, "secret.txt")
	if err := os.WriteFile(outside, []byte("secret\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name, path string
		link       func(t *testing.T, dir string)
	}{
		{
			name: "symlink_to_symlink",
			path: "hop1.txt",
			link: func(t *testing.T, dir string) {
				if err := os.Symlink(outside, filepath.Join(dir, "hop2.txt")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(dir, "hop2.txt"), filepath.Join(dir, "hop1.txt")); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "nested_dir_symlink",
			path: "pkg/sub/linkdir/secret.txt",
			link: func(t *testing.T, dir string) {
				if err := os.MkdirAll(filepath.Join(dir, "pkg", "sub"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outsideDir, filepath.Join(dir, "pkg", "sub", "linkdir")); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "dir_symlink_to_dir_symlink",
			path: "outer/inner/secret.txt",
			link: func(t *testing.T, dir string) {
				if err := os.Symlink(outsideDir, filepath.Join(dir, "real-inner")); err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(filepath.Join(dir, "outer"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(dir, "real-inner"), filepath.Join(dir, "outer", "inner")); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := gitRepo(t, map[string]string{"pkg/a.go": "keep\n"})
			tc.link(t, dir)
			svc := serviceForRepo(t, dir)

			_, err := svc.WriteWorkspaceFile(context.Background(), "s1", WriteWorkspaceFileInput{
				Path: tc.path, Content: "pwned\n", BaseHash: advHash(t, svc, "pkg/a.go"),
			})
			if err == nil {
				t.Fatalf("symlink path %q was accepted", tc.path)
			}
			if got := advBytes(t, outside); got != "secret\n" {
				t.Fatalf("outside file = %q, the symlink chain was written through", got)
			}
			if leftover := advTempLeftovers(t, outsideDir); len(leftover) > 0 {
				t.Fatalf("a temp file was created outside the workspace: %v", leftover)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 2. The truncated-read refusal
// ---------------------------------------------------------------------------

// The exact boundary. maxFileLines lines read back in FULL, so that file must
// be savable; one line more reads back truncated, so it must not be.
func TestWriteWorkspaceFile_LineCapBoundaryOnDisk(t *testing.T) {
	for _, tc := range []struct {
		name      string
		lines     int
		truncated bool
	}{
		{"at_cap", maxFileLines, false},
		{"one_over_cap", maxFileLines + 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			content := strings.Repeat("line\n", tc.lines)
			dir := gitRepo(t, map[string]string{"big.txt": content})
			svc := serviceForRepo(t, dir)

			read, err := svc.ReadWorkspaceFile(context.Background(), "s1", "big.txt")
			if err != nil {
				t.Fatal(err)
			}
			if read.Truncated != tc.truncated {
				t.Fatalf("read truncated = %v, want %v (%d lines)", read.Truncated, tc.truncated, tc.lines)
			}

			_, err = svc.WriteWorkspaceFile(context.Background(), "s1", WriteWorkspaceFileInput{
				Path: "big.txt", Content: content, BaseHash: read.ContentHash,
			})
			if tc.truncated {
				e := wantAPIError(t, err, apierr.KindConflict, "WORKSPACE_FILE_NOT_EDITABLE")
				if e.Details["reason"] != UnavailableTruncated {
					t.Fatalf("details = %v, want reason %q", e.Details, UnavailableTruncated)
				}
				return
			}
			if err != nil {
				t.Fatalf("a file at exactly the cap must be savable, got %v", err)
			}
		})
	}
}

// The tail-destroying case, played the way the editor would play it: save back
// EXACTLY what the read handed over. The refusal is not the point - the point
// is that line 2005 is still on disk afterwards.
func TestWriteWorkspaceFile_TruncatedRefusalLeavesTheTailOnDisk(t *testing.T) {
	var b strings.Builder
	for i := 1; i <= maxFileLines+5; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	original := b.String()
	dir := gitRepo(t, map[string]string{"long.txt": original})
	svc := serviceForRepo(t, dir)
	target := filepath.Join(dir, "long.txt")

	read, err := svc.ReadWorkspaceFile(context.Background(), "s1", "long.txt")
	if err != nil {
		t.Fatal(err)
	}
	if !read.Truncated {
		t.Fatal("fixture did not read back truncated")
	}
	// What the client holds: only the lines it was given.
	held := make([]string, 0, len(read.Lines))
	for _, l := range read.Lines {
		held = append(held, l.Text)
	}
	clientContent := strings.Join(held, "\n")
	if read.TrailingNewline {
		clientContent += "\n"
	}

	_, err = svc.WriteWorkspaceFile(context.Background(), "s1", WriteWorkspaceFileInput{
		Path: "long.txt", Content: clientContent, BaseHash: read.ContentHash,
	})
	e := wantAPIError(t, err, apierr.KindConflict, "WORKSPACE_FILE_NOT_EDITABLE")
	if e.Details["reason"] != UnavailableTruncated {
		t.Fatalf("details = %v, want reason %q", e.Details, UnavailableTruncated)
	}
	if got := advBytes(t, target); got != original {
		t.Fatalf("the file changed: the tail was destroyed (len %d, want %d)", len(got), len(original))
	}
	if !strings.Contains(advBytes(t, target), fmt.Sprintf("line %d\n", maxFileLines+5)) {
		t.Fatal("the last line is gone from disk")
	}
}

// The state the refusal guards against can also arrive AFTER the read. A file
// that grows past the cap, or turns binary, underneath a held baseline must not
// be flattened back to what the caller was shown.
func TestWriteWorkspaceFile_BaselineDegradesUnderneath(t *testing.T) {
	for _, tc := range []struct {
		name     string
		mutate   func(t *testing.T, path string) string
		wantCode string
	}{
		{
			name: "grows_past_the_line_cap",
			mutate: func(t *testing.T, path string) string {
				t.Helper()
				grown := "short\n" + strings.Repeat("more\n", maxFileLines+10)
				if err := os.WriteFile(path, []byte(grown), 0o644); err != nil {
					t.Fatal(err)
				}
				return grown
			},
			// The hash check comes first, and "it changed underneath you" is
			// the more actionable answer when both are true.
			wantCode: "WORKSPACE_FILE_CONFLICT",
		},
		{
			name: "turns_binary",
			mutate: func(t *testing.T, path string) string {
				t.Helper()
				bin := "\x00\x01\x02binary\x00\n"
				if err := os.WriteFile(path, []byte(bin), 0o644); err != nil {
					t.Fatal(err)
				}
				return bin
			},
			// A binary file has no hash to compare, so this one precedes it.
			wantCode: "WORKSPACE_FILE_NOT_EDITABLE",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := gitRepo(t, map[string]string{"f.txt": "short\n"})
			svc := serviceForRepo(t, dir)
			target := filepath.Join(dir, "f.txt")
			base := advHash(t, svc, "f.txt")

			afterMutation := tc.mutate(t, target)

			_, err := svc.WriteWorkspaceFile(context.Background(), "s1", WriteWorkspaceFileInput{
				Path: "f.txt", Content: "short but edited\n", BaseHash: base,
			})
			wantAPIError(t, err, apierr.KindConflict, tc.wantCode)
			if got := advBytes(t, target); got != afterMutation {
				t.Fatalf("file = %q, the other writer's bytes were clobbered", got)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 3. Concurrency, with writers that are real
// ---------------------------------------------------------------------------

// A separate PROCESS writes the file between the read and the save - an agent's
// edit, a script, a checkout. The save must be refused and the other writer's
// bytes must survive byte for byte.
func TestWriteWorkspaceFile_ExternalProcessWriteIsAConflict(t *testing.T) {
	for _, tc := range []struct {
		name    string
		mutate  func(t *testing.T, dir, target string) string
		wantErr bool
	}{
		{
			name: "another_process_overwrites",
			mutate: func(t *testing.T, dir, target string) string {
				t.Helper()
				runIn(t, dir, "sh", "-c", "printf 'from the other agent\\n' > f.txt")
				return "from the other agent\n"
			},
			wantErr: true,
		},
		{
			name: "git_checkout_reverts_it",
			mutate: func(t *testing.T, dir, target string) string {
				t.Helper()
				runIn(t, dir, "git", "checkout", "--", "f.txt")
				return "committed\n"
			},
			wantErr: true,
		},
		{
			name: "identical_rewrite_is_not_a_conflict",
			mutate: func(t *testing.T, dir, target string) string {
				t.Helper()
				// A formatter that rewrote the same bytes must not cost the
				// user their save - the token is the CONTENT, not the mtime.
				runIn(t, dir, "sh", "-c", "cat f.txt > f.copy && mv f.copy f.txt")
				return advBytes(t, target)
			},
			wantErr: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if runtime.GOOS == "windows" {
				t.Skip("uses a POSIX shell")
			}
			dir := gitRepo(t, map[string]string{"f.txt": "committed\n"})
			svc := serviceForRepo(t, dir)
			target := filepath.Join(dir, "f.txt")
			// Diverge from HEAD so `git checkout --` is a real change.
			if err := os.WriteFile(target, []byte("working tree\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			base := advHash(t, svc, "f.txt")

			expected := tc.mutate(t, dir, target)

			_, err := svc.WriteWorkspaceFile(context.Background(), "s1", WriteWorkspaceFileInput{
				Path: "f.txt", Content: "my edit\n", BaseHash: base,
			})
			if !tc.wantErr {
				if err != nil {
					t.Fatalf("identical bytes must not conflict, got %v", err)
				}
				if got := advBytes(t, target); got != "my edit\n" {
					t.Fatalf("file = %q, want the save to have landed", got)
				}
				return
			}
			e := wantAPIError(t, err, apierr.KindConflict, "WORKSPACE_FILE_CONFLICT")
			if got := advBytes(t, target); got != expected {
				t.Fatalf("file = %q, want the other writer's bytes %q", got, expected)
			}
			// The refusal has to be resolvable: the caller needs the hash of
			// what is there now, and it must be the hash of what IS there now.
			if e.Details["currentHash"] != contentHash([]byte(expected)) {
				t.Fatalf("currentHash = %v, does not match the bytes on disk", e.Details["currentHash"])
			}
			if e.Details["currentSize"] != len(expected) {
				t.Fatalf("currentSize = %v, want %d", e.Details["currentSize"], len(expected))
			}
		})
	}
}

// Many saves racing from ONE baseline, started at the same instant.
//
// This deliberately does NOT assert that exactly one lands. The hash check and
// the rename are not one operation, and the window between them is the WHOLE
// atomic write - read, hash, create the temp file, write it, fsync it, rename -
// which measures in MILLISECONDS, not microseconds, because fsync dominates it.
// Two saves issued inside that window both pass the check and both land, and the
// later one silently loses. Closing that needs worktree locking, which the human
// took out of scope for this route; the precondition catches the case it was
// built for, which is a write that COMPLETED before the save started (covered by
// TestWriteWorkspaceFile_ExternalProcessWriteIsAConflict and the sequential
// same-baseline test).
//
// What must hold even under full contention, and is asserted here: the file ends
// up holding exactly one racer's bytes IN FULL - never a blend of two, never a
// half-written file - and every save that is refused is refused as a conflict
// rather than as some other error the editor cannot explain.
func TestWriteWorkspaceFile_ParallelSavesNeverBlend(t *testing.T) {
	const racers = 8
	dir := gitRepo(t, map[string]string{"f.txt": "start\n"})
	svc := serviceForRepo(t, dir)
	target := filepath.Join(dir, "f.txt")
	base := advHash(t, svc, "f.txt")

	// Padded so a torn or interleaved write cannot coincidentally look whole.
	body := strings.Repeat("payload\n", 500)
	candidates := map[string]bool{"start\n": true}
	content := func(i int) string { return fmt.Sprintf("racer %d\n", i) + body }
	for i := range racers {
		candidates[content(i)] = true
	}

	var (
		mu     sync.Mutex
		landed int
		wg     sync.WaitGroup
		start  = make(chan struct{})
	)
	for i := range racers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := svc.WriteWorkspaceFile(context.Background(), "s1", WriteWorkspaceFileInput{
				Path: "f.txt", Content: content(i), BaseHash: base,
			})
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				landed++
				return
			}
			var e *apierr.Error
			if !errors.As(err, &e) || e.Code != "WORKSPACE_FILE_CONFLICT" {
				t.Errorf("a refused save must be a conflict, got %v", err)
			}
		}()
	}
	close(start)
	wg.Wait()

	if landed == 0 {
		t.Fatal("no save landed at all; the route deadlocked rather than raced")
	}
	got := advBytes(t, target)
	if !candidates[got] {
		t.Fatalf("the file holds bytes no single save wrote (%d bytes) - a torn or blended write", len(got))
	}
	if got == "start\n" {
		t.Fatal("a save reported success but the file still holds the original bytes")
	}
	if leftover := advTempLeftovers(t, dir); len(leftover) > 0 {
		t.Fatalf("temp files left in the workspace: %v", leftover)
	}
}

// A concurrent reader - an agent, a build, a linter - must never observe a
// half-written file. The write claims to be atomic; this is what that claim
// means in practice.
func TestWriteWorkspaceFile_ConcurrentReaderNeverSeesATornFile(t *testing.T) {
	const rounds = 40
	body := strings.Repeat("padding to make a torn write visible\n", 400)
	dir := gitRepo(t, map[string]string{"f.txt": "v0\n" + body})
	svc := serviceForRepo(t, dir)
	target := filepath.Join(dir, "f.txt")

	known := map[string]bool{"v0\n" + body: true}
	for i := 1; i <= rounds; i++ {
		known[fmt.Sprintf("v%d\n", i)+body] = true
	}

	done := make(chan struct{})
	var reads int
	var torn string
	var readerWG sync.WaitGroup
	readerWG.Add(1)
	go func() {
		defer readerWG.Done()
		for {
			select {
			case <-done:
				return
			default:
			}
			data, err := os.ReadFile(target) //nolint:gosec // test-controlled path
			if err != nil {
				// A rename never unlinks the target, so the file is always
				// there; anything else is the failure being looked for.
				torn = "read failed: " + err.Error()
				return
			}
			reads++
			if !known[string(data)] {
				torn = fmt.Sprintf("observed a file that was never written in full (%d bytes)", len(data))
				return
			}
		}
	}()

	for i := 1; i <= rounds; i++ {
		in := WriteWorkspaceFileInput{
			Path:     "f.txt",
			Content:  fmt.Sprintf("v%d\n", i) + body,
			BaseHash: advHash(t, svc, "f.txt"),
		}
		if _, err := svc.WriteWorkspaceFile(context.Background(), "s1", in); err != nil {
			close(done)
			readerWG.Wait()
			t.Fatalf("round %d: %v", i, err)
		}
	}
	close(done)
	readerWG.Wait()

	if torn != "" {
		t.Fatal(torn)
	}
	if reads == 0 {
		t.Fatal("the reader never observed the file; the test proved nothing")
	}
}

// ---------------------------------------------------------------------------
// 4. Byte-exactness of the read -> edit -> write round trip
// ---------------------------------------------------------------------------

// The editor reconstructs a file from Lines plus TrailingNewline. If that
// reconstruction is not the file's exact bytes then every save silently rewrites
// the file, and the round trip has to be byte-exact for shapes that are easy to
// mangle: no trailing newline, CRLF, a lone CR, blank runs, an empty file.
//
// The proof is not a string comparison alone - it is that git sees NOTHING
// changed after saving the file back unmodified.
func TestWorkspaceFileRoundTrip_IsByteExact(t *testing.T) {
	for _, tc := range []struct{ name, content string }{
		{"plain", "alpha\nbeta\n"},
		{"no_trailing_newline", "alpha\nbeta"},
		{"empty", ""},
		{"single_newline", "\n"},
		{"blank_run", "a\n\n\n"},
		{"leading_blank", "\n\na\n"},
		{"crlf", "alpha\r\nbeta\r\n"},
		{"crlf_no_trailing", "alpha\r\nbeta"},
		{"lone_cr", "alpha\rbeta\n"},
		{"tabs_and_spaces", "\tindented   \n  trailing\t\n"},
		{"unicode", "héllo wörld 🎉 日本語\n"},
		{"long_line", strings.Repeat("x", 100_000) + "\n"},
		{"at_line_cap", strings.Repeat("l\n", maxFileLines)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := gitRepo(t, map[string]string{"f.txt": tc.content})
			svc := serviceForRepo(t, dir)
			target := filepath.Join(dir, "f.txt")

			read, err := svc.ReadWorkspaceFile(context.Background(), "s1", "f.txt")
			if err != nil {
				t.Fatal(err)
			}
			if !read.Available || read.Truncated {
				t.Fatalf("fixture did not read back in full: available=%v truncated=%v", read.Available, read.Truncated)
			}

			texts := make([]string, 0, len(read.Lines))
			for _, l := range read.Lines {
				texts = append(texts, l.Text)
			}
			rebuilt := strings.Join(texts, "\n")
			if read.TrailingNewline {
				rebuilt += "\n"
			}
			if rebuilt != tc.content {
				t.Fatalf("reconstruction = %q, want the file's bytes %q", rebuilt, tc.content)
			}
			if read.ContentHash != contentHash([]byte(tc.content)) {
				t.Fatalf("contentHash does not match the bytes on disk")
			}

			if _, err := svc.WriteWorkspaceFile(context.Background(), "s1", WriteWorkspaceFileInput{
				Path: "f.txt", Content: rebuilt, BaseHash: read.ContentHash,
			}); err != nil {
				t.Fatalf("saving the file back unchanged failed: %v", err)
			}
			if got := advBytes(t, target); got != tc.content {
				t.Fatalf("after the round trip the file = %q, want %q", got, tc.content)
			}

			// The end-user check: an untouched save must not show up as a
			// change in the worktree the agents are sharing.
			cmd := exec.Command("git", "status", "--porcelain", "--", "f.txt")
			cmd.Dir = dir
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("git status: %v\n%s", err, out)
			}
			if len(strings.TrimSpace(string(out))) != 0 {
				t.Fatalf("saving the file back unchanged made git see a change:\n%s", out)
			}
		})
	}
}
