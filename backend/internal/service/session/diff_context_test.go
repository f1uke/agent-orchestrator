package session

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
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
