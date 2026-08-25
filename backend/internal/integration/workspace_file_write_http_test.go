package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
)

// The workspace write route, end to end: the REAL router, the REAL session
// service, a REAL sqlite store and a REAL shared git worktree with a REAL crew
// in it. Nothing here is a fake.
//
// That matters because the route's whole reason to exist is a worktree that TWO
// agents are writing in (#239), and the failures it has to rule out are failures
// of bytes on disk - a file written outside the tree, a tail destroyed, an
// agent's edit clobbered. A fake workspace cannot fail any of those ways.

// writeRouterFor puts the real HTTP router in front of the real session service.
func writeRouterFor(t *testing.T, s *crewStack) *httptest.Server {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(
		config.Config{}, log, nil, httpd.APIDeps{Sessions: s.svc}, httpd.ControlDeps{},
	))
	t.Cleanup(srv.Close)
	return srv
}

type wsFileRead struct {
	Available       bool   `json:"available"`
	Path            string `json:"path"`
	Truncated       bool   `json:"truncated"`
	ContentHash     string `json:"contentHash"`
	TrailingNewline bool   `json:"trailingNewline"`
	Lines           []struct {
		Text string `json:"text"`
	} `json:"lines"`
}

// getWorkspaceFile reads a file over the wire and returns the response plus the
// exact bytes a client would reconstruct from it.
func getWorkspaceFile(t *testing.T, srv *httptest.Server, id domain.SessionID, path string) (wsFileRead, string) {
	t.Helper()
	url := fmt.Sprintf("%s/api/v1/sessions/%s/workspace/file?path=%s", srv.URL, id, path)
	resp, err := srv.Client().Get(url) //nolint:noctx // test client
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: status %d: %s", path, resp.StatusCode, body)
	}
	var out wsFileRead
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	texts := make([]string, 0, len(out.Lines))
	for _, l := range out.Lines {
		texts = append(texts, l.Text)
	}
	rebuilt := strings.Join(texts, "\n")
	if out.TrailingNewline {
		rebuilt += "\n"
	}
	return out, rebuilt
}

// putWorkspaceFile saves a file over the wire and returns the status and body.
func putWorkspaceFile(t *testing.T, srv *httptest.Server, id domain.SessionID, path, content, baseHash string) (int, []byte) {
	t.Helper()
	payload, err := json.Marshal(map[string]string{"path": path, "content": content, "baseHash": baseHash})
	if err != nil {
		t.Fatal(err)
	}
	url := fmt.Sprintf("%s/api/v1/sessions/%s/workspace/file", srv.URL, id)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPut, url, bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, body
}

func errCode(t *testing.T, body []byte) string {
	t.Helper()
	var env struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode error body %s: %v", body, err)
	}
	return env.Code
}

func gitPorcelain(t *testing.T, dir string) string {
	t.Helper()
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git status: %v\n%s", err, out)
	}
	return strings.TrimSpace(string(out))
}

// The round trip an editor makes: open a file, save it back untouched. If the
// bytes are not identical then every save the editor makes carries a diff the
// user never wrote - into a worktree an agent is about to commit from.
func TestWorkspaceFileWriteHTTP_RoundTripLeavesTheTreeClean(t *testing.T) {
	s := newCrewStack(t)
	dev, _ := s.spawnCrew(t)
	tree := dev.Metadata.WorkspacePath
	srv := writeRouterFor(t, s)

	// Shapes an editor mangles: no trailing newline, CRLF, a blank run, unicode.
	const original = "package main\r\n\nfunc main() {\n\tprintln(\"héllo 🎉\")\n}\n\n\nvar x = 1"
	if err := os.WriteFile(filepath.Join(tree, "main.go"), []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	runGitIn(t, tree, "add", "main.go")
	runGitIn(t, tree, "commit", "-m", "add main.go")

	read, rebuilt := getWorkspaceFile(t, srv, dev.ID, "main.go")
	if !read.Available || read.Truncated {
		t.Fatalf("read available=%v truncated=%v", read.Available, read.Truncated)
	}
	if rebuilt != original {
		t.Fatalf("the client cannot reconstruct the file it was shown:\n got %q\nwant %q", rebuilt, original)
	}

	status, body := putWorkspaceFile(t, srv, dev.ID, "main.go", rebuilt, read.ContentHash)
	if status != http.StatusOK {
		t.Fatalf("save-unchanged: status %d: %s", status, body)
	}
	if got, err := os.ReadFile(filepath.Join(tree, "main.go")); err != nil || string(got) != original {
		t.Fatalf("file = %q, %v; want the original bytes", got, err)
	}
	if out := gitPorcelain(t, tree); out != "" {
		t.Fatalf("saving a file back unchanged dirtied the shared worktree:\n%s", out)
	}
}

// The case the whole design exists for: dev opens a file, its crewmate writes it,
// dev saves. The save must be refused with enough to resolve it, and the
// crewmate's work must still be on disk. Then the resolution has to actually
// work - re-read, re-save, and the second save lands.
func TestWorkspaceFileWriteHTTP_CrewmateEditIsNotClobbered(t *testing.T) {
	s := newCrewStack(t)
	dev, qa := s.spawnCrew(t)
	tree := dev.Metadata.WorkspacePath
	srv := writeRouterFor(t, s)

	target := filepath.Join(tree, "shared.txt")
	if err := os.WriteFile(target, []byte("original\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGitIn(t, tree, "add", "shared.txt")
	runGitIn(t, tree, "commit", "-m", "add shared.txt")

	// dev opens it.
	read, _ := getWorkspaceFile(t, srv, dev.ID, "shared.txt")

	// The crewmate writes it - a real, separate process, the way an agent's
	// Edit tool or a git checkout would.
	const crewmateWork = "the other agent's work\nsecond line\n"
	runShellIn(t, tree, fmt.Sprintf("printf %q > shared.txt", crewmateWork))

	// dev saves what it was holding.
	status, body := putWorkspaceFile(t, srv, dev.ID, "shared.txt", "dev's edit\n", read.ContentHash)
	if status != http.StatusConflict {
		t.Fatalf("a stale save must be a conflict, got %d: %s", status, body)
	}
	if code := errCode(t, body); code != "WORKSPACE_FILE_CONFLICT" {
		t.Fatalf("code = %q: %s", code, body)
	}
	got, err := os.ReadFile(target)
	if err != nil || string(got) != crewmateWork {
		t.Fatalf("file = %q, %v; the crewmate's work was clobbered", got, err)
	}

	// And the refusal has to be RESOLVABLE, not just correct: re-read, save, land.
	// The crewmate's session id must reach the same file - one worktree, two
	// members, either of whom may be the one with the editor open.
	reread, _ := getWorkspaceFile(t, srv, qa.ID, "shared.txt")
	if reread.ContentHash == read.ContentHash {
		t.Fatal("the re-read handed back the stale hash")
	}
	status, body = putWorkspaceFile(t, srv, qa.ID, "shared.txt", "merged by hand\n", reread.ContentHash)
	if status != http.StatusOK {
		t.Fatalf("the resolved save was refused: %d: %s", status, body)
	}
	if got, err := os.ReadFile(target); err != nil || string(got) != "merged by hand\n" {
		t.Fatalf("file = %q, %v; want the resolved save to have landed", got, err)
	}
}

// Containment, over the wire, against a real worktree. The read route is
// intentionally unconfined for absolute and ~ paths (#132); the write route is
// not, and the proof is that the file outside the tree still holds its bytes.
func TestWorkspaceFileWriteHTTP_CannotWriteOutsideTheWorktree(t *testing.T) {
	s := newCrewStack(t)
	dev, _ := s.spawnCrew(t)
	tree := dev.Metadata.WorkspacePath
	srv := writeRouterFor(t, s)

	// A file outside the worktree, in the ROOT repository the worktree came
	// from - the escape that would actually matter here.
	outside := filepath.Join(s.repo, "outside.txt")
	if err := os.WriteFile(outside, []byte("main checkout\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A file with the SAME BASENAME inside the tree. previewutil.ConfinedPath
	// CLAMPS traversal rather than rejecting it, so without a same-named target
	// in here "../outside.txt" would resolve to a path that does not exist and
	// the test would pass for the wrong reason - containment by accident. With
	// this file present, a clamped escape writes over the WRONG in-tree file,
	// which is the data-loss half of the bug and is what the assertions below
	// actually catch.
	inside := filepath.Join(tree, "outside.txt")
	if err := os.WriteFile(inside, []byte("in the worktree\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	read, _ := getWorkspaceFile(t, srv, dev.ID, "README.md")

	for _, path := range []string{
		"../outside.txt",
		"../../outside.txt",
		"sub/../../outside.txt",
		"%2e%2e/outside.txt",
		`..\outside.txt`,
		outside,
		filepath.Join(s.repo, "sub", "..", "outside.txt"),
		"/etc/ao-pwned",
		"~/ao-pwned",
		"/",
		"",
	} {
		t.Run(fmt.Sprintf("path=%q", path), func(t *testing.T) {
			for f, want := range map[string]string{
				outside:                          "main checkout\n",
				inside:                           "in the worktree\n",
				filepath.Join(tree, "README.md"): "seed\n",
			} {
				if err := os.WriteFile(f, []byte(want), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			status, body := putWorkspaceFile(t, srv, dev.ID, path, "pwned\n", read.ContentHash)
			if status < 400 {
				t.Fatalf("path %q was accepted: %d %s", path, status, body)
			}
			if got, err := os.ReadFile(outside); err != nil || string(got) != "main checkout\n" {
				t.Fatalf("path %q wrote outside the worktree: %q, %v", path, got, err)
			}
			// The clamp case: a traversal that is not REJECTED lands on the
			// same-named file INSIDE the tree. Quietly writing over a file the
			// caller did not name is data loss even though it never left the
			// worktree.
			if got, err := os.ReadFile(inside); err != nil || string(got) != "in the worktree\n" {
				t.Fatalf("path %q was clamped onto the in-tree outside.txt: %q, %v", path, got, err)
			}
			if got, err := os.ReadFile(filepath.Join(tree, "README.md")); err != nil || string(got) != "seed\n" {
				t.Fatalf("path %q was clamped onto README.md: %q, %v", path, got, err)
			}
		})
	}
}

// The clamp, with the hash precondition unable to save it.
//
// previewutil.ConfinedPath CLAMPS traversal instead of rejecting it, so an
// unrejected "../README.md" resolves to the worktree's OWN README.md. The hash
// precondition usually masks that - the two files differ, so the write is
// refused as a conflict for the wrong reason. It does NOT mask it when the two
// files hold the SAME bytes, which is the normal state of a fresh worktree and
// the checkout it came from. Then the hash matches, and a save aimed outside
// lands silently on the wrong file.
//
// The read route is intentionally unconfined for absolute paths (#132), so a
// client really can hold the outside file's hash. This is the whole attack.
func TestWorkspaceFileWriteHTTP_ClampedPathCannotWriteTheWrongFile(t *testing.T) {
	s := newCrewStack(t)
	dev, _ := s.spawnCrew(t)
	tree := dev.Metadata.WorkspacePath
	srv := writeRouterFor(t, s)

	// Identical bytes inside the worktree and outside it.
	const shared = "shared between the checkout and the worktree\n"
	outside := filepath.Join(s.repo, "NOTES.md")
	insideSameName := filepath.Join(tree, "NOTES.md")
	for _, p := range []string{outside, insideSameName} {
		if err := os.WriteFile(p, []byte(shared), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	// The client legitimately reads the OUTSIDE file - the read route allows it -
	// and so holds a hash that is also the in-tree file's hash.
	read, _ := getWorkspaceFile(t, srv, dev.ID, outside)
	if read.ContentHash == "" {
		t.Fatal("the read handed out no hash for a file outside the worktree")
	}

	for _, path := range []string{"../NOTES.md", "sub/../../NOTES.md", outside} {
		t.Run(fmt.Sprintf("path=%q", path), func(t *testing.T) {
			// Restore both fixtures so a subtest that DOES clobber cannot make
			// the next one fail for a reason it did not cause.
			for _, f := range []string{outside, insideSameName} {
				if err := os.WriteFile(f, []byte(shared), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			status, body := putWorkspaceFile(t, srv, dev.ID, path, "clobbered\n", read.ContentHash)
			if status < 400 {
				t.Fatalf("path %q was accepted: %d %s", path, status, body)
			}
			if got, err := os.ReadFile(outside); err != nil || string(got) != shared {
				t.Fatalf("path %q wrote OUTSIDE the worktree: %q, %v", path, got, err)
			}
			if got, err := os.ReadFile(insideSameName); err != nil || string(got) != shared {
				t.Fatalf("path %q was clamped onto the worktree's own NOTES.md: %q, %v", path, got, err)
			}
		})
	}
}

// A file the read TRUNCATED cannot be saved back, over the wire, and the tail is
// still there afterwards. This is the one refusal whose absence is silent data
// loss: the client is holding a complete-looking file that stops at line 2000.
func TestWorkspaceFileWriteHTTP_TruncatedReadCannotBeSavedBack(t *testing.T) {
	s := newCrewStack(t)
	dev, _ := s.spawnCrew(t)
	tree := dev.Metadata.WorkspacePath
	srv := writeRouterFor(t, s)

	var b strings.Builder
	for i := 1; i <= 2100; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	original := b.String()
	target := filepath.Join(tree, "long.txt")
	if err := os.WriteFile(target, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	read, rebuilt := getWorkspaceFile(t, srv, dev.ID, "long.txt")
	if !read.Truncated {
		t.Fatal("a 2100-line file did not read back truncated; the fixture is wrong")
	}

	// Exactly what a client that forgot the truncated flag would send.
	status, body := putWorkspaceFile(t, srv, dev.ID, "long.txt", rebuilt, read.ContentHash)
	if status != http.StatusConflict {
		t.Fatalf("saving a truncated read must be refused, got %d: %s", status, body)
	}
	if code := errCode(t, body); code != "WORKSPACE_FILE_NOT_EDITABLE" {
		t.Fatalf("code = %q: %s", code, body)
	}
	var env struct {
		Details struct {
			Reason string `json:"reason"`
		} `json:"details"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatal(err)
	}
	if env.Details.Reason != "truncated" {
		t.Fatalf("reason = %q, the UI cannot tell the user why: %s", env.Details.Reason, body)
	}
	got, err := os.ReadFile(target)
	if err != nil || string(got) != original {
		t.Fatalf("the file changed: the tail was destroyed (%d bytes, want %d)", len(got), len(original))
	}
}

// A save with no baseHash is a 400, not an implicit force. There is deliberately
// no way to spell "write regardless", and that is the only thing standing between
// a client bug and a silent clobber.
func TestWorkspaceFileWriteHTTP_NoBaseHashIsNotAForce(t *testing.T) {
	s := newCrewStack(t)
	dev, _ := s.spawnCrew(t)
	tree := dev.Metadata.WorkspacePath
	srv := writeRouterFor(t, s)

	target := filepath.Join(tree, "README.md")
	status, body := putWorkspaceFile(t, srv, dev.ID, "README.md", "pwned\n", "")
	if status != http.StatusBadRequest {
		t.Fatalf("an omitted baseHash must be refused, got %d: %s", status, body)
	}
	if got, err := os.ReadFile(target); err != nil || string(got) != "seed\n" {
		t.Fatalf("file = %q, %v; a hashless save wrote through", got, err)
	}
}

// An ABSENT content key must not read as "". A TypeScript caller stringifying
// an `undefined` while the editor is still initialising drops the key, and if
// the server took that for an empty string it would empty the user's file and
// answer 200 - with a baseHash that is entirely correct, so the precondition
// cannot catch it. Emptying a file has to be spelled out.
func TestWorkspaceFileWriteHTTP_AbsentContentIsNotAnEmptyFile(t *testing.T) {
	s := newCrewStack(t)
	dev, _ := s.spawnCrew(t)
	tree := dev.Metadata.WorkspacePath
	srv := writeRouterFor(t, s)
	read, _ := getWorkspaceFile(t, srv, dev.ID, "README.md")

	payload, err := json.Marshal(map[string]string{"path": "README.md", "baseHash": read.ContentHash})
	if err != nil {
		t.Fatal(err)
	}
	url := fmt.Sprintf("%s/api/v1/sessions/%s/workspace/file", srv.URL, dev.ID)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPut, url, bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("an absent content key must be refused, got %d: %s", resp.StatusCode, body)
	}
	if code := errCode(t, body); code != "WORKSPACE_FILE_CONTENT_REQUIRED" {
		t.Fatalf("code = %s, want WORKSPACE_FILE_CONTENT_REQUIRED", code)
	}
	if got, err := os.ReadFile(filepath.Join(tree, "README.md")); err != nil || string(got) != "seed\n" {
		t.Fatalf("file = %q, %v; the file was emptied", got, err)
	}
	if out := gitPorcelain(t, tree); out != "" {
		t.Fatalf("tree is dirty after a refused save:\n%s", out)
	}
}

// The other side of the same rule: an EXPLICIT empty string is a legitimate
// save. Refusing the key must not cost the ability to empty a file on purpose.
func TestWorkspaceFileWriteHTTP_ExplicitEmptyStringEmptiesTheFile(t *testing.T) {
	s := newCrewStack(t)
	dev, _ := s.spawnCrew(t)
	tree := dev.Metadata.WorkspacePath
	srv := writeRouterFor(t, s)
	read, _ := getWorkspaceFile(t, srv, dev.ID, "README.md")

	status, body := putWorkspaceFile(t, srv, dev.ID, "README.md", "", read.ContentHash)
	if status != http.StatusOK {
		t.Fatalf("status %d: %s", status, body)
	}
	if got, err := os.ReadFile(filepath.Join(tree, "README.md")); err != nil || string(got) != "" {
		t.Fatalf("file = %q, %v; want it emptied", got, err)
	}
}

func runGitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...) //nolint:gosec // test-controlled args
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=ao", "GIT_AUTHOR_EMAIL=ao@example.com",
		"GIT_COMMITTER_NAME=ao", "GIT_COMMITTER_EMAIL=ao@example.com")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// runShellIn runs a command in a real subprocess - the crewmate whose write the
// precondition has to notice.
func runShellIn(t *testing.T, dir, script string) {
	t.Helper()
	cmd := exec.Command("sh", "-c", script) //nolint:gosec // test-controlled script
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("sh -c %q: %v\n%s", script, err, out)
	}
}
