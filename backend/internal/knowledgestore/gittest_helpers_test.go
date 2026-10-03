package knowledgestore

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// newRepo creates a git repository on branch main with one empty commit,
// isolated from the machine's global and system git config.
func newRepo(t *testing.T) string {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	git(t, dir, "commit", "-q", "--allow-empty", "-m", "root")
	return dir
}

// commitFiles writes files (repo-relative, forward slashes) and commits them.
func commitFiles(t *testing.T, repo string, files map[string]string) {
	t.Helper()
	paths := make([]string, 0, len(files))
	for rel, content := range files {
		writeFile(t, filepath.Join(repo, filepath.FromSlash(rel)), content)
		paths = append(paths, rel)
	}
	sort.Strings(paths)
	git(t, repo, append([]string{"add", "--"}, paths...)...)
	git(t, repo, "commit", "-q", "-m", "add "+strings.Join(paths, " "))
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	full := append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false"}, args...)
	if out, err := exec.Command("git", full...).CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}
