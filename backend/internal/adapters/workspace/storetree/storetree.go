// Package storetree implements ports.ScriptsTrees with the git CLI: a
// workspace's own worktree of the mobile scripts store, cut from the store's
// main checkout, and the publish of its commits back into it.
package storetree

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

// fallbackIdentity signs AO's merge commit in a store with no user configured,
// where git would otherwise refuse to commit at all.
var fallbackIdentity = []string{"-c", "user.name=Agent Orchestrator", "-c", "user.email=agent-orchestrator@localhost"}

// inProgressOps are the git-path entries that mean an operation is paused in a
// checkout. Merging on top of one would tangle AO's merge into a person's.
var inProgressOps = []string{"MERGE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD", "rebase-merge", "rebase-apply", "BISECT_LOG"}

// Trees implements ports.ScriptsTrees.
type Trees struct {
	binary string
}

var _ ports.ScriptsTrees = (*Trees)(nil)

// New returns a Trees that runs the git on PATH.
func New() *Trees {
	return &Trees{binary: "git"}
}

type result struct {
	stdout string
	stderr string
	code   int
}

func (r result) output() string {
	return strings.TrimSpace(strings.TrimSpace(r.stdout) + "\n" + strings.TrimSpace(r.stderr))
}

// run executes git in dir. A non-zero exit is reported in result.code; err is
// only for git not running at all. --no-optional-locks keeps a read such as
// `git status` from taking the index lock a person's own git may need.
func (t *Trees) run(ctx context.Context, dir string, args ...string) (result, error) {
	cmd := aoprocess.CommandContext(ctx, t.binary, append([]string{"--no-optional-locks", "-C", dir}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	res := result{stdout: stdout.String(), stderr: stderr.String()}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		res.code = exitErr.ExitCode()
		return res, nil
	}
	if err != nil {
		return res, fmt.Errorf("storetree: git %s: %w", strings.Join(args, " "), err)
	}
	return res, nil
}

// must runs git and turns a non-zero exit into an error carrying git's output.
func (t *Trees) must(ctx context.Context, dir string, args ...string) (string, error) {
	res, err := t.run(ctx, dir, args...)
	if err != nil {
		return "", err
	}
	if res.code != 0 {
		return "", fmt.Errorf("storetree: git %s in %s: exit %d: %s", strings.Join(args, " "), dir, res.code, res.output())
	}
	return strings.TrimSpace(res.stdout), nil
}

// Probe implements ports.ScriptsTrees.
func (t *Trees) Probe(ctx context.Context, store string) (ports.ScriptsStoreProbe, error) {
	if !isDir(store) {
		return ports.ScriptsStoreProbe{Reason: fmt.Sprintf("the scripts store %s does not exist", store)}, nil
	}
	top, err := t.run(ctx, store, "rev-parse", "--show-toplevel")
	if err != nil {
		return ports.ScriptsStoreProbe{}, err
	}
	if top.code != 0 {
		return ports.ScriptsStoreProbe{Reason: fmt.Sprintf("the scripts store %s is not a git repository", store)}, nil
	}
	if !samePath(strings.TrimSpace(top.stdout), store) {
		return ports.ScriptsStoreProbe{Reason: fmt.Sprintf("the scripts store %s is inside the git repository %s, not its top level", store, strings.TrimSpace(top.stdout))}, nil
	}
	branch, err := t.currentBranch(ctx, store)
	if err != nil {
		return ports.ScriptsStoreProbe{}, err
	}
	if branch == "" {
		return ports.ScriptsStoreProbe{Reason: fmt.Sprintf("the scripts store %s has a detached HEAD", store)}, nil
	}
	head, err := t.run(ctx, store, "rev-parse", "--verify", "--quiet", "HEAD")
	if err != nil {
		return ports.ScriptsStoreProbe{}, err
	}
	if head.code != 0 {
		return ports.ScriptsStoreProbe{Reason: fmt.Sprintf("the scripts store %s has no commits on %s", store, branch)}, nil
	}
	return ports.ScriptsStoreProbe{Base: branch, OK: true}, nil
}

// Ensure implements ports.ScriptsTrees.
func (t *Trees) Ensure(ctx context.Context, store, path, branch, base string) error {
	registered, err := t.Registered(ctx, store)
	if err != nil {
		return err
	}
	known := containsPath(registered, path)
	if _, statErr := os.Stat(path); statErr == nil {
		if !known {
			if err := os.Remove(path); err != nil {
				return fmt.Errorf("storetree: %s exists and is not a worktree of %s", path, store)
			}
		} else {
			_, err := t.must(ctx, store, "worktree", "repair", path)
			return err
		}
	}
	if _, err := t.must(ctx, store, "worktree", "prune"); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("storetree: create %s: %w", filepath.Dir(path), err)
	}
	exists, err := t.branchExists(ctx, store, branch)
	if err != nil {
		return err
	}
	if exists {
		_, err = t.must(ctx, store, "worktree", "add", "-q", path, branch)
		return err
	}
	_, err = t.must(ctx, store, "worktree", "add", "-q", "-b", branch, path, base)
	return err
}

// Status implements ports.ScriptsTrees.
func (t *Trees) Status(ctx context.Context, path, branch, base string) (ports.ScriptsTreeStatus, error) {
	files, err := t.Dirty(ctx, path)
	if err != nil {
		return ports.ScriptsTreeStatus{}, err
	}
	ahead, err := t.ahead(ctx, path, base, branch)
	if err != nil {
		return ports.ScriptsTreeStatus{}, err
	}
	return ports.ScriptsTreeStatus{Uncommitted: files, Unpublished: ahead}, nil
}

// Dirty implements ports.ScriptsTrees. Untracked files are listed one by one,
// not as their folder, because a new script is usually a new file in a new
// folder and the folder alone names nothing.
func (t *Trees) Dirty(ctx context.Context, dir string) ([]string, error) {
	// Raw stdout: a porcelain record starts with a status column that may be a
	// space, which trimming would shift into the path.
	res, err := t.run(ctx, dir, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return nil, err
	}
	if res.code != 0 {
		return nil, fmt.Errorf("storetree: status in %s: %s", dir, res.output())
	}
	var paths []string
	records := strings.Split(res.stdout, "\x00")
	for i := 0; i < len(records); i++ {
		rec := records[i]
		if len(rec) < 4 {
			continue
		}
		paths = append(paths, rec[3:])
		if rec[0] == 'R' || rec[0] == 'C' {
			i++
		}
	}
	return paths, nil
}

// Publish implements ports.ScriptsTrees.
func (t *Trees) Publish(ctx context.Context, store, branch, base, message string) (ports.ScriptsPublishResult, error) {
	current, err := t.currentBranch(ctx, store)
	if err != nil {
		return ports.ScriptsPublishResult{}, err
	}
	if current != base {
		on := "a detached HEAD"
		if current != "" {
			on = current
		}
		return refused(domain.HoldStoreOffBase, fmt.Sprintf("the store's main checkout is on %s, not %s", on, base), nil), nil
	}
	if op := t.inProgressOp(ctx, store); op != "" {
		return refused(domain.HoldPublishFailed, fmt.Sprintf("the store's main checkout has a git operation in progress (%s)", op), nil), nil
	}
	commits, err := t.ahead(ctx, store, base, branch)
	if err != nil {
		return ports.ScriptsPublishResult{}, err
	}
	if commits == 0 {
		return ports.ScriptsPublishResult{Outcome: ports.PublishNothing}, nil
	}
	ancestor, err := t.run(ctx, store, "merge-base", "--is-ancestor", base, branch)
	if err != nil {
		return ports.ScriptsPublishResult{}, err
	}
	outcome := ports.PublishFastForward
	args := []string{"merge", "--ff-only", "-q", branch}
	if ancestor.code != 0 {
		conflicts, err := t.conflicts(ctx, store, base, branch)
		if err != nil {
			return ports.ScriptsPublishResult{}, err
		}
		if len(conflicts) > 0 {
			return refused(domain.HoldPublishConflict, fmt.Sprintf("%s conflicts with %s", branch, base), conflicts), nil
		}
		outcome = ports.PublishMerged
		args = append(t.identity(ctx, store), "merge", "--no-ff", "--no-edit", "-q", "-m", message, branch)
	}
	res, err := t.run(ctx, store, args...)
	if err != nil {
		return ports.ScriptsPublishResult{}, err
	}
	if res.code != 0 {
		return t.refusedMerge(ctx, store, res.output())
	}
	sha, err := t.must(ctx, store, "rev-parse", "HEAD")
	if err != nil {
		return ports.ScriptsPublishResult{}, err
	}
	return ports.ScriptsPublishResult{Outcome: outcome, SHA: sha, Commits: commits}, nil
}

// refusedMerge turns a failed `git merge` into a refusal, first undoing any
// half-done merge so the main checkout is exactly as it was.
func (t *Trees) refusedMerge(ctx context.Context, store, out string) (ports.ScriptsPublishResult, error) {
	if t.inProgressOp(ctx, store) == "MERGE_HEAD" {
		if _, err := t.must(ctx, store, "merge", "--abort"); err != nil {
			return ports.ScriptsPublishResult{}, err
		}
		return refused(domain.HoldPublishConflict, "the merge conflicted and was undone", conflictedFiles(out)), nil
	}
	switch {
	case strings.Contains(out, "untracked working tree files would be overwritten"):
		return refused(domain.HoldStoreDirtyOverlap, "the store's main checkout has untracked files the merge would overwrite", indentedLines(out)), nil
	case strings.Contains(out, "would be overwritten by merge"):
		return refused(domain.HoldStoreDirtyOverlap, "the store's main checkout has uncommitted changes in files the merge changes", indentedLines(out)), nil
	case strings.Contains(out, "index.lock"):
		return refused(domain.HoldPublishFailed, "another git process holds the store's index lock", nil), nil
	}
	return refused(domain.HoldPublishFailed, "git merge failed: "+firstLine(out), nil), nil
}

func refused(hold domain.ScriptsStoreHold, detail string, files []string) ports.ScriptsPublishResult {
	return ports.ScriptsPublishResult{Outcome: ports.PublishRefused, Hold: hold, Detail: detail, Files: files}
}

// Remove implements ports.ScriptsTrees.
func (t *Trees) Remove(ctx context.Context, store, path, branch string, force bool) error {
	if _, err := os.Stat(path); err == nil {
		args := []string{"worktree", "remove", path}
		if force {
			args = []string{"worktree", "remove", "--force", path}
		}
		if _, err := t.must(ctx, store, args...); err != nil {
			return err
		}
	}
	if _, err := t.must(ctx, store, "worktree", "prune"); err != nil {
		return err
	}
	exists, err := t.branchExists(ctx, store, branch)
	if err != nil {
		return err
	}
	if exists {
		flag := "-d"
		if force {
			flag = "-D"
		}
		if _, err := t.must(ctx, store, "branch", flag, branch); err != nil {
			return err
		}
	}
	// Drops the per-store folder once its last worktree is gone; os.Remove
	// refuses a non-empty directory, which is exactly the guard wanted.
	_ = os.Remove(filepath.Dir(path))
	return nil
}

// Registered implements ports.ScriptsTrees. The store's own checkout is not
// listed.
func (t *Trees) Registered(ctx context.Context, store string) ([]string, error) {
	out, err := t.must(ctx, store, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, line := range strings.Split(out, "\n") {
		if p, ok := strings.CutPrefix(line, "worktree "); ok {
			paths = append(paths, p)
		}
	}
	if len(paths) > 0 {
		paths = paths[1:]
	}
	return paths, nil
}

func (t *Trees) ahead(ctx context.Context, dir, base, branch string) (int, error) {
	count, err := t.must(ctx, dir, "rev-list", "--count", base+".."+branch)
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(count)
	if err != nil {
		return 0, fmt.Errorf("storetree: commit count %q: %w", count, err)
	}
	return n, nil
}

// conflicts names the files a merge of branch into base would conflict on,
// computed without touching any checkout.
func (t *Trees) conflicts(ctx context.Context, store, base, branch string) ([]string, error) {
	res, err := t.run(ctx, store, "merge-tree", "--write-tree", "--name-only", "--no-messages", base, branch)
	if err != nil {
		return nil, err
	}
	switch res.code {
	case 0:
		return nil, nil
	case 1:
		lines := nonEmptyLines(res.stdout)
		if len(lines) <= 1 {
			return nil, fmt.Errorf("storetree: merge-tree reported a conflict without naming a file: %s", res.output())
		}
		return lines[1:], nil
	}
	return nil, fmt.Errorf("storetree: merge-tree %s %s: exit %d: %s", base, branch, res.code, res.output())
}

func (t *Trees) identity(ctx context.Context, dir string) []string {
	if res, err := t.run(ctx, dir, "config", "--get", "user.email"); err == nil && res.code == 0 && strings.TrimSpace(res.stdout) != "" {
		return nil
	}
	return fallbackIdentity
}

func (t *Trees) branchExists(ctx context.Context, dir, branch string) (bool, error) {
	res, err := t.run(ctx, dir, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	if err != nil {
		return false, err
	}
	return res.code == 0, nil
}

func (t *Trees) currentBranch(ctx context.Context, dir string) (string, error) {
	res, err := t.run(ctx, dir, "symbolic-ref", "--short", "-q", "HEAD")
	if err != nil {
		return "", err
	}
	switch res.code {
	case 0:
		return strings.TrimSpace(res.stdout), nil
	case 1:
		return "", nil
	}
	return "", fmt.Errorf("storetree: read the branch of %s: %s", dir, res.output())
}

func (t *Trees) inProgressOp(ctx context.Context, dir string) string {
	for _, name := range inProgressOps {
		res, err := t.run(ctx, dir, "rev-parse", "--git-path", name)
		if err != nil || res.code != 0 {
			continue
		}
		resolved := strings.TrimSpace(res.stdout)
		if resolved == "" {
			continue
		}
		if !filepath.IsAbs(resolved) {
			resolved = filepath.Join(dir, resolved)
		}
		if _, err := os.Lstat(resolved); err == nil {
			return name
		}
	}
	return ""
}

func isDir(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

// samePath compares two paths after resolving symlinks, so /var and
// /private/var (macOS temp folders) name one place.
func samePath(a, b string) bool {
	return canonical(a) == canonical(b)
}

func containsPath(paths []string, p string) bool {
	for _, candidate := range paths {
		if samePath(candidate, p) {
			return true
		}
	}
	return false
}

// canonical resolves symlinks in the longest prefix of p that exists, so a
// folder that is gone still compares equal to git's record of it.
func canonical(p string) string {
	p = filepath.Clean(p)
	rest := ""
	for dir := p; ; dir = filepath.Dir(dir) {
		if resolved, err := filepath.EvalSymlinks(dir); err == nil {
			return filepath.Join(resolved, rest)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return p
		}
		rest = filepath.Join(filepath.Base(dir), rest)
	}
}

func nonEmptyLines(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}
	return out
}

// indentedLines are the file names git lists, tab-indented, under an error.
func indentedLines(out string) []string {
	var files []string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "\t") {
			files = append(files, strings.TrimSpace(line))
		}
	}
	return files
}

// conflictedFiles reads "CONFLICT (...): Merge conflict in <file>" lines.
func conflictedFiles(out string) []string {
	var files []string
	for _, line := range strings.Split(out, "\n") {
		if i := strings.Index(line, "Merge conflict in "); i >= 0 {
			files = append(files, strings.TrimSpace(line[i+len("Merge conflict in "):]))
		}
	}
	return files
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
