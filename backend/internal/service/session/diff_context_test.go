package session

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// putSessionWithWorkspace seeds a session record whose Metadata.WorkspacePath
// points at a real git worktree, for DiffContext's integration tests.
func (f *fakeStore) putSessionWithWorkspace(id domain.SessionID, workspacePath string) {
	f.sessions[id] = domain.SessionRecord{
		ID:        id,
		ProjectID: "proj",
		Kind:      domain.KindWorker,
		Metadata:  domain.SessionMetadata{WorkspacePath: workspacePath},
	}
}

// gitRevParse resolves ref to a full SHA inside dir, failing the test on error.
func gitRevParse(t *testing.T, dir, ref string) string {
	t.Helper()
	cmd := exec.Command("git", "rev-parse", ref)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git rev-parse %s: %v", ref, err)
	}
	return strings.TrimSpace(string(out))
}

// newServiceWithStore builds a session Service over the given store for
// DiffContext tests.
func newServiceWithStore(t *testing.T, st Store) *Service {
	t.Helper()
	return &Service{store: st}
}

// diffContextTestRepo builds a temp git repo with two commits: base has a.go
// with "l1\nl2\nl3\n", head changes l2 to "CHANGED". Returns the repo dir and
// the two commit SHAs.
func diffContextTestRepo(t *testing.T) (dir, baseSHA, headSHA string) {
	t.Helper()
	dir = t.TempDir()
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
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("l1\nl2\nl3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", "a.go")
	runGit("commit", "-q", "-m", "base")
	baseSHA = gitRevParse(t, dir, "HEAD")
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("l1\nCHANGED\nl3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", "a.go")
	runGit("commit", "-q", "-m", "head")
	headSHA = gitRevParse(t, dir, "HEAD")
	return dir, baseSHA, headSHA
}

func TestDiffContext_HunkMode(t *testing.T) {
	dir, baseSHA, headSHA := diffContextTestRepo(t)

	fake := newFakeStore()
	fake.putSessionWithWorkspace("s1", dir)
	stList := &multiPRFakeStore{fakeStore: fake, prs: []domain.PullRequest{{URL: "pr1", BaseSHA: baseSHA, HeadSHA: headSHA}}}
	svc := newServiceWithStore(t, stList)

	res, err := svc.DiffContext(context.Background(), "s1", DiffContextQuery{PRURL: "pr1", Path: "a.go", Line: 2, Mode: "hunk"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Available || res.Mode != "hunk" {
		t.Fatalf("res = %+v", res)
	}
	var sawAdd bool
	for _, l := range res.Lines {
		if l.Kind == "add" && l.NewLine == 2 && l.Text == "CHANGED" {
			sawAdd = true
		}
	}
	if !sawAdd {
		t.Fatalf("expected the CHANGED add line at new 2: %+v", res.Lines)
	}
}

func TestDiffContext_HunkModeWindowsLargeHunk(t *testing.T) {
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
	// Seed an unrelated file so head's big.go is a pure add (one 40-line hunk).
	if err := os.WriteFile(filepath.Join(dir, "seed.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", "seed.txt")
	runGit("commit", "-q", "-m", "base")
	baseSHA := gitRevParse(t, dir, "HEAD")
	var sb strings.Builder
	for i := 1; i <= 40; i++ {
		sb.WriteString("line ")
		sb.WriteString(strconv.Itoa(i))
		sb.WriteByte('\n')
	}
	if err := os.WriteFile(filepath.Join(dir, "big.go"), []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", "big.go")
	runGit("commit", "-q", "-m", "head")
	headSHA := gitRevParse(t, dir, "HEAD")

	fake := newFakeStore()
	fake.putSessionWithWorkspace("s1", dir)
	stList := &multiPRFakeStore{fakeStore: fake, prs: []domain.PullRequest{{URL: "pr1", BaseSHA: baseSHA, HeadSHA: headSHA}}}
	svc := newServiceWithStore(t, stList)

	res, err := svc.DiffContext(context.Background(), "s1", DiffContextQuery{PRURL: "pr1", Path: "big.go", Line: 20, Mode: "hunk"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Available || res.Mode != "hunk" {
		t.Fatalf("res = %+v", res)
	}
	// A 40-line hunk must be trimmed to a 15-line window centered on line 20.
	if len(res.Lines) != 15 {
		t.Fatalf("want a 15-line window, got %d lines", len(res.Lines))
	}
	if res.Lines[0].NewLine != 13 || res.Lines[14].NewLine != 27 {
		t.Fatalf("window not centered on 20: first=%d last=%d", res.Lines[0].NewLine, res.Lines[14].NewLine)
	}
	sawAnchor := false
	for _, l := range res.Lines {
		if l.NewLine == 20 && l.Text == "line 20" {
			sawAnchor = true
		}
	}
	if !sawAnchor {
		t.Fatalf("window must include the anchor line 20: %+v", res.Lines)
	}
}

func TestDiffContext_FileMode(t *testing.T) {
	dir, baseSHA, headSHA := diffContextTestRepo(t)

	fake := newFakeStore()
	fake.putSessionWithWorkspace("s1", dir)
	stList := &multiPRFakeStore{fakeStore: fake, prs: []domain.PullRequest{{URL: "pr1", BaseSHA: baseSHA, HeadSHA: headSHA}}}
	svc := newServiceWithStore(t, stList)

	res, err := svc.DiffContext(context.Background(), "s1", DiffContextQuery{PRURL: "pr1", Path: "a.go", Mode: "file"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Available || res.Mode != "file" || len(res.Lines) != 3 || res.Lines[1].Text != "CHANGED" || res.Lines[1].NewLine != 2 {
		t.Fatalf("file mode = %+v", res)
	}
}

func TestDiffContext_PathTraversalRejected(t *testing.T) {
	dir, baseSHA, headSHA := diffContextTestRepo(t)

	fake := newFakeStore()
	fake.putSessionWithWorkspace("s1", dir)
	stList := &multiPRFakeStore{fakeStore: fake, prs: []domain.PullRequest{{URL: "pr1", BaseSHA: baseSHA, HeadSHA: headSHA}}}
	svc := newServiceWithStore(t, stList)

	res, _ := svc.DiffContext(context.Background(), "s1", DiffContextQuery{PRURL: "pr1", Path: "../../etc/passwd", Mode: "file"})
	if res.Available {
		t.Fatal("traversal path must be rejected (Available=false)")
	}
}

// TestDiffContext_ConfinesPathBeforeGit asserts the application-level
// confinement gate directly: it captures the pathspec DiffContext actually
// hands to git (via the gitOutput seam) and checks it has been confined to
// the workspace root, independent of git's own refusal to touch out-of-tree
// pathspecs. A black-box assertion on the returned Available flag can't tell
// the pre-fix code (raw ".." reaching git, which git then rejects on its
// own) apart from the fix (the confined path reaching git) — both produce
// Available:false. This test fails against the pre-fix code because the raw,
// unconfined "../../etc/passwd" is what gets captured.
func TestDiffContext_ConfinesPathBeforeGit(t *testing.T) {
	dir, baseSHA, headSHA := diffContextTestRepo(t)

	fake := newFakeStore()
	fake.putSessionWithWorkspace("s1", dir)
	stList := &multiPRFakeStore{fakeStore: fake, prs: []domain.PullRequest{{URL: "pr1", BaseSHA: baseSHA, HeadSHA: headSHA}}}
	svc := newServiceWithStore(t, stList)

	origGitOutput := gitOutput
	defer func() { gitOutput = origGitOutput }()

	var capturedArgs []string
	gitOutput = func(ctx context.Context, dir string, args ...string) ([]byte, error) {
		capturedArgs = append([]string{}, args...)
		return nil, errors.New("stubbed: capturing args only, not running git")
	}

	// mode=file: git args are ["show", "<headRef>:<path>"].
	if _, err := svc.DiffContext(context.Background(), "s1", DiffContextQuery{PRURL: "pr1", Path: "../../etc/passwd", Mode: "file"}); err != nil {
		t.Fatal(err)
	}
	if len(capturedArgs) != 2 || capturedArgs[0] != "show" {
		t.Fatalf("file mode: unexpected git args: %v", capturedArgs)
	}
	pathspec := capturedArgs[1]
	if strings.Contains(pathspec, "..") {
		t.Fatalf("file mode: unconfined traversal path reached git: %q", pathspec)
	}
	if !strings.HasSuffix(pathspec, ":etc/passwd") {
		t.Fatalf("file mode: expected confined path suffix %q, got %q", ":etc/passwd", pathspec)
	}

	// mode=hunk: git args are ["diff", "<base>..<head>", "--", "<path>"].
	capturedArgs = nil
	if _, err := svc.DiffContext(context.Background(), "s1", DiffContextQuery{PRURL: "pr1", Path: "../../etc/passwd", Mode: "hunk"}); err != nil {
		t.Fatal(err)
	}
	if len(capturedArgs) != 4 || capturedArgs[0] != "diff" || capturedArgs[2] != "--" {
		t.Fatalf("hunk mode: unexpected git args: %v", capturedArgs)
	}
	pathspec = capturedArgs[3]
	if strings.Contains(pathspec, "..") {
		t.Fatalf("hunk mode: unconfined traversal path reached git: %q", pathspec)
	}
	if pathspec != "etc/passwd" {
		t.Fatalf("hunk mode: expected confined path %q, got %q", "etc/passwd", pathspec)
	}
}

func TestDiffContext_UnknownSession(t *testing.T) {
	st := newFakeStore()
	svc := &Service{store: st}

	_, err := svc.DiffContext(context.Background(), "nope", DiffContextQuery{PRURL: "pr1", Path: "a.go"})
	if err == nil {
		t.Fatal("want error for unknown session")
	}
}

func TestDiffContext_UnknownPR(t *testing.T) {
	dir, baseSHA, headSHA := diffContextTestRepo(t)

	fake := newFakeStore()
	fake.putSessionWithWorkspace("s1", dir)
	stList := &multiPRFakeStore{fakeStore: fake, prs: []domain.PullRequest{{URL: "pr1", BaseSHA: baseSHA, HeadSHA: headSHA}}}
	svc := newServiceWithStore(t, stList)

	_, err := svc.DiffContext(context.Background(), "s1", DiffContextQuery{PRURL: "not-this-pr", Path: "a.go"})
	if err == nil {
		t.Fatal("want error for PR not belonging to the session")
	}
}
