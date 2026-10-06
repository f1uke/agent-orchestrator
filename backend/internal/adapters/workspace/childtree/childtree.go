// Package childtree implements ports.ChildTrees with the git CLI: the worktrees
// AO creates for a worker's subagents, outside the worker's folder, and the
// merge of their commits back into the worker's branch.
package childtree

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

// fallbackIdentity signs AO's own commits (merge commits, leftover-work
// commits) in a repository that has no user configured, where git would
// otherwise refuse to commit at all.
var fallbackIdentity = []string{"-c", "user.name=Agent Orchestrator", "-c", "user.email=agent-orchestrator@localhost"}

// inProgressOps are the git-path entries that mean an operation is paused in a
// worktree. Merging on top of one would tangle AO's merge into the worker's.
var inProgressOps = []string{"MERGE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD", "rebase-merge", "rebase-apply", "BISECT_LOG"}

// indexLockRetries bounds how often a merge is retried while another git
// process (usually the worker's own) holds the index lock.
const indexLockRetries = 3

// Trees implements ports.ChildTrees.
type Trees struct {
	binary string
	sleep  func(time.Duration)
}

var _ ports.ChildTrees = (*Trees)(nil)

// New returns a Trees that runs the git on PATH.
func New() *Trees {
	return &Trees{binary: "git", sleep: time.Sleep}
}

type result struct {
	stdout string
	stderr string
	code   int
}

func (r result) output() string {
	return strings.TrimSpace(strings.TrimSpace(r.stdout) + "\n" + strings.TrimSpace(r.stderr))
}

// run executes git in dir. A non-zero exit is reported in result.code, not as
// an error; err is only for git not running at all.
func (t *Trees) run(ctx context.Context, dir string, env []string, args ...string) (result, error) {
	cmd := aoprocess.CommandContext(ctx, t.binary, append([]string{"-C", dir}, args...)...)
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
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
		return res, fmt.Errorf("childtree: git %s: %w", strings.Join(args, " "), err)
	}
	return res, nil
}

// must runs git and turns a non-zero exit into an error carrying git's output.
func (t *Trees) must(ctx context.Context, dir string, args ...string) (string, error) {
	res, err := t.run(ctx, dir, nil, args...)
	if err != nil {
		return "", err
	}
	if res.code != 0 {
		return "", fmt.Errorf("childtree: git %s in %s: exit %d: %s", strings.Join(args, " "), dir, res.code, res.output())
	}
	return strings.TrimSpace(res.stdout), nil
}

// identity returns the -c flags a commit in dir needs: none when the repository
// has an author configured, AO's fallback otherwise.
func (t *Trees) identity(ctx context.Context, dir string) []string {
	if res, err := t.run(ctx, dir, nil, "config", "--get", "user.email"); err == nil && res.code == 0 && strings.TrimSpace(res.stdout) != "" {
		return nil
	}
	return fallbackIdentity
}

// Create implements ports.ChildTrees.
func (t *Trees) Create(ctx context.Context, spec ports.ChildTreeSpec) (ports.ChildTreeCreated, error) {
	head, err := t.Head(ctx, spec.WorkerPath)
	if err != nil {
		return ports.ChildTreeCreated{}, err
	}
	target, err := t.currentBranch(ctx, spec.WorkerPath)
	if err != nil {
		return ports.ChildTreeCreated{}, err
	}
	if target == "" {
		return ports.ChildTreeCreated{}, fmt.Errorf("childtree: the worker at %s is on a detached HEAD, so there is no branch to merge a child back into", spec.WorkerPath)
	}
	dirty, err := t.dirtyPaths(ctx, spec.WorkerPath)
	if err != nil {
		return ports.ChildTreeCreated{}, err
	}
	created := ports.ChildTreeCreated{BaseSHA: head, TargetBranch: target, WorkerDirty: dirty}

	if existing, err := t.currentBranch(ctx, spec.Path); err == nil && existing == spec.Branch {
		base, err := t.must(ctx, spec.WorkerPath, "merge-base", "HEAD", spec.Branch)
		if err != nil {
			return ports.ChildTreeCreated{}, err
		}
		created.BaseSHA = base
		return created, nil
	}
	if err := os.MkdirAll(filepath.Dir(spec.Path), 0o750); err != nil {
		return ports.ChildTreeCreated{}, fmt.Errorf("childtree: create parent of %s: %w", spec.Path, err)
	}
	if _, err := t.must(ctx, spec.WorkerPath, "worktree", "add", "-q", "-b", spec.Branch, spec.Path, head); err != nil {
		return ports.ChildTreeCreated{}, err
	}
	if err := t.copyWorktreeIncludes(ctx, spec.WorkerPath, spec.Path); err != nil {
		return ports.ChildTreeCreated{}, err
	}
	return created, nil
}

// copyWorktreeIncludes copies the worker's gitignored files that its
// .worktreeinclude names into the child. Claude Code does this itself for the
// worktrees it creates and skips it when a WorktreeCreate hook takes over, so
// AO keeps the behaviour a project configured for.
func (t *Trees) copyWorktreeIncludes(ctx context.Context, worker, child string) error {
	include := filepath.Join(worker, ".worktreeinclude")
	if _, err := os.Stat(include); errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("childtree: read %s: %w", include, err)
	}
	listed, err := t.must(ctx, worker, "ls-files", "-z", "--others", "--ignored", "--exclude-from="+include)
	if err != nil {
		return err
	}
	candidates := splitZ(listed)
	if len(candidates) == 0 {
		return nil
	}
	cmd := aoprocess.CommandContext(ctx, t.binary, "-C", worker, "check-ignore", "-z", "--stdin")
	cmd.Stdin = strings.NewReader(strings.Join(candidates, "\x00") + "\x00")
	out, err := cmd.Output()
	var exitErr *exec.ExitError
	if err != nil && (!errors.As(err, &exitErr) || exitErr.ExitCode() != 1) {
		return fmt.Errorf("childtree: check-ignore in %s: %w", worker, err)
	}
	for _, rel := range splitZ(string(out)) {
		if err := copyFile(filepath.Join(worker, rel), filepath.Join(child, rel)); err != nil {
			return fmt.Errorf("childtree: copy .worktreeinclude entry %s: %w", rel, err)
		}
	}
	return nil
}

func copyFile(src, dst string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(src)
		if err != nil {
			return err
		}
		return os.Symlink(target, dst)
	}
	in, err := os.Open(src) // #nosec G304 -- src is a path git listed inside the worker's worktree.
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm()) // #nosec G304 -- dst mirrors src inside the child's worktree.
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// Inspect implements ports.ChildTrees.
func (t *Trees) Inspect(ctx context.Context, path, baseSHA string) (ports.ChildTreeFacts, error) {
	status, err := t.must(ctx, path, "status", "--porcelain")
	if err != nil {
		return ports.ChildTreeFacts{}, err
	}
	head, err := t.Head(ctx, path)
	if err != nil {
		return ports.ChildTreeFacts{}, err
	}
	count, err := t.must(ctx, path, "rev-list", "--count", baseSHA+"..HEAD")
	if err != nil {
		return ports.ChildTreeFacts{}, err
	}
	commits, err := strconv.Atoi(count)
	if err != nil {
		return ports.ChildTreeFacts{}, fmt.Errorf("childtree: commit count %q: %w", count, err)
	}
	names, err := t.must(ctx, path, "diff", "--name-only", baseSHA, "HEAD")
	if err != nil {
		return ports.ChildTreeFacts{}, err
	}
	return ports.ChildTreeFacts{Dirty: status != "", Commits: commits, FilesChanged: len(nonEmptyLines(names)), Head: head}, nil
}

// CommitAll implements ports.ChildTrees.
func (t *Trees) CommitAll(ctx context.Context, path, message string) error {
	if _, err := t.must(ctx, path, "add", "-A"); err != nil {
		return err
	}
	staged, err := t.run(ctx, path, nil, "diff", "--cached", "--quiet")
	if err != nil {
		return err
	}
	if staged.code == 0 {
		return nil
	}
	args := append(t.identity(ctx, path), "commit", "-q", "-m", message)
	_, err = t.must(ctx, path, args...)
	return err
}

// Conflicts implements ports.ChildTrees.
func (t *Trees) Conflicts(ctx context.Context, workerPath, targetBranch, branch string) ([]string, error) {
	res, err := t.run(ctx, workerPath, nil, "merge-tree", "--write-tree", "--name-only", "--no-messages", targetBranch, branch)
	if err != nil {
		return nil, err
	}
	switch res.code {
	case 0:
		return nil, nil
	case 1:
		lines := nonEmptyLines(res.stdout)
		if len(lines) <= 1 {
			return nil, fmt.Errorf("childtree: merge-tree reported a conflict without naming a file: %s", res.output())
		}
		return lines[1:], nil
	}
	return nil, fmt.Errorf("childtree: merge-tree %s %s: exit %d: %s", targetBranch, branch, res.code, res.output())
}

// Head implements ports.ChildTrees.
func (t *Trees) Head(ctx context.Context, path string) (string, error) {
	return t.must(ctx, path, "rev-parse", "HEAD")
}

// Merge implements ports.ChildTrees.
func (t *Trees) Merge(ctx context.Context, workerPath, targetBranch, branch, message string) (ports.ChildMergeResult, error) {
	current, err := t.currentBranch(ctx, workerPath)
	if err != nil {
		return ports.ChildMergeResult{}, err
	}
	if current != targetBranch {
		on := "a detached HEAD"
		if current != "" {
			on = current
		}
		return ports.ChildMergeResult{Held: fmt.Sprintf("the worker is on %s, not %s", on, targetBranch)}, nil
	}
	if op := t.inProgressOp(ctx, workerPath); op != "" {
		return ports.ChildMergeResult{Held: fmt.Sprintf("the worker has a git operation in progress (%s)", op)}, nil
	}
	args := append(t.identity(ctx, workerPath), "merge", "--no-ff", "--no-edit", "-m", message, branch)
	for attempt := 1; ; attempt++ {
		res, err := t.run(ctx, workerPath, nil, args...)
		if err != nil {
			return ports.ChildMergeResult{}, err
		}
		if res.code == 0 {
			sha, err := t.Head(ctx, workerPath)
			if err != nil {
				return ports.ChildMergeResult{}, err
			}
			return ports.ChildMergeResult{Merged: true, SHA: sha}, nil
		}
		out := res.output()
		if strings.Contains(out, "index.lock") && attempt < indexLockRetries {
			t.sleep(time.Duration(attempt) * 500 * time.Millisecond)
			continue
		}
		return t.heldMerge(ctx, workerPath, out)
	}
}

// heldMerge turns a failed `git merge` into a held result, first undoing any
// half-done merge so the worker's tree is exactly as it was.
func (t *Trees) heldMerge(ctx context.Context, workerPath, out string) (ports.ChildMergeResult, error) {
	if t.inProgressOp(ctx, workerPath) == "MERGE_HEAD" {
		if _, err := t.must(ctx, workerPath, "merge", "--abort"); err != nil {
			return ports.ChildMergeResult{}, err
		}
		return ports.ChildMergeResult{Held: "the merge conflicted in " + strings.Join(conflictedFiles(out), ", ") + " and was undone"}, nil
	}
	switch {
	case strings.Contains(out, "untracked working tree files would be overwritten"):
		return ports.ChildMergeResult{Held: "the worker has untracked files in the way: " + strings.Join(indentedLines(out), ", ")}, nil
	case strings.Contains(out, "would be overwritten by merge"):
		return ports.ChildMergeResult{Held: "the worker has uncommitted changes in " + strings.Join(indentedLines(out), ", ")}, nil
	case strings.Contains(out, "index.lock"):
		return ports.ChildMergeResult{Held: "another git process holds the worker's index lock"}, nil
	}
	return ports.ChildMergeResult{Held: "git merge failed: " + firstLine(out)}, nil
}

// RecoverMerge implements ports.ChildTrees.
func (t *Trees) RecoverMerge(ctx context.Context, workerPath, branch, headBefore string) (ports.ChildMergeResult, error) {
	tip, err := t.must(ctx, workerPath, "rev-parse", "--verify", branch)
	if err != nil {
		return ports.ChildMergeResult{}, err
	}
	head, err := t.Head(ctx, workerPath)
	if err != nil {
		return ports.ChildMergeResult{}, err
	}
	ancestor, err := t.run(ctx, workerPath, nil, "merge-base", "--is-ancestor", tip, head)
	if err != nil {
		return ports.ChildMergeResult{}, err
	}
	if ancestor.code == 0 {
		return ports.ChildMergeResult{Merged: true, SHA: head}, nil
	}
	if t.inProgressOp(ctx, workerPath) == "MERGE_HEAD" && head == headBefore {
		mergeHead, err := t.must(ctx, workerPath, "rev-parse", "--verify", "MERGE_HEAD")
		if err == nil && mergeHead == tip {
			if _, err := t.must(ctx, workerPath, "merge", "--abort"); err != nil {
				return ports.ChildMergeResult{}, err
			}
			return ports.ChildMergeResult{Held: "AO's merge was interrupted and has been undone"}, nil
		}
	}
	return ports.ChildMergeResult{Held: "AO's merge was interrupted and the worker's branch does not contain the child's commits"}, nil
}

// Remove implements ports.ChildTrees.
func (t *Trees) Remove(ctx context.Context, workerPath, path, branch string, deleteBranch bool) error {
	if _, err := os.Stat(path); err == nil {
		if _, err := t.must(ctx, workerPath, "worktree", "remove", path); err != nil {
			return err
		}
	}
	if _, err := t.must(ctx, workerPath, "worktree", "prune"); err != nil {
		return err
	}
	if deleteBranch {
		exists, err := t.run(ctx, workerPath, nil, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
		if err != nil {
			return err
		}
		if exists.code == 0 {
			if _, err := t.must(ctx, workerPath, "branch", "-d", branch); err != nil {
				return err
			}
		}
	}
	removeEmptyParent(path)
	return nil
}

// Preserve implements ports.ChildTrees. The capture goes through a temporary
// index and commit-tree, as gitworktree's StashUncommitted does, so no hook can
// refuse it and the child's own index is never touched. The forced removal that
// follows is safe only because everything the folder held is now a commit on
// the kept branch.
func (t *Trees) Preserve(ctx context.Context, workerPath, path, branch, message string) (bool, error) {
	if _, err := os.Stat(path); err != nil {
		_, err := t.must(ctx, workerPath, "worktree", "prune")
		return false, err
	}
	status, err := t.must(ctx, path, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	committed := false
	if status != "" {
		if err := t.captureToBranch(ctx, path, branch, message); err != nil {
			return false, err
		}
		committed = true
	}
	if _, err := t.must(ctx, workerPath, "worktree", "remove", "--force", path); err != nil {
		return committed, err
	}
	if _, err := t.must(ctx, workerPath, "worktree", "prune"); err != nil {
		return committed, err
	}
	removeEmptyParent(path)
	return committed, nil
}

func (t *Trees) captureToBranch(ctx context.Context, path, branch, message string) error {
	index, err := os.CreateTemp("", "ao-child-index-*")
	if err != nil {
		return fmt.Errorf("childtree: temp index: %w", err)
	}
	indexPath := index.Name()
	_ = index.Close()
	_ = os.Remove(indexPath)
	defer func() { _ = os.Remove(indexPath) }()
	env := []string{"GIT_INDEX_FILE=" + indexPath}
	head, err := t.Head(ctx, path)
	if err != nil {
		return err
	}
	for _, args := range [][]string{{"read-tree", "HEAD"}, {"add", "-A"}} {
		res, err := t.run(ctx, path, env, args...)
		if err != nil {
			return err
		}
		if res.code != 0 {
			return fmt.Errorf("childtree: git %s with a temp index in %s: %s", strings.Join(args, " "), path, res.output())
		}
	}
	tree, err := t.run(ctx, path, env, "write-tree")
	if err != nil {
		return err
	}
	if tree.code != 0 {
		return fmt.Errorf("childtree: write-tree in %s: %s", path, tree.output())
	}
	commitArgs := append(t.identity(ctx, path), "commit-tree", strings.TrimSpace(tree.stdout), "-p", head, "-m", message)
	sha, err := t.must(ctx, path, commitArgs...)
	if err != nil {
		return err
	}
	_, err = t.must(ctx, path, "update-ref", "refs/heads/"+branch, sha, head)
	return err
}

func (t *Trees) currentBranch(ctx context.Context, dir string) (string, error) {
	res, err := t.run(ctx, dir, nil, "symbolic-ref", "--short", "-q", "HEAD")
	if err != nil {
		return "", err
	}
	switch res.code {
	case 0:
		return strings.TrimSpace(res.stdout), nil
	case 1:
		return "", nil
	}
	return "", fmt.Errorf("childtree: read the branch of %s: %s", dir, res.output())
}

func (t *Trees) dirtyPaths(ctx context.Context, dir string) ([]string, error) {
	// Raw stdout: a porcelain record starts with a status column that may be a
	// space, which trimming would shift into the path.
	res, err := t.run(ctx, dir, nil, "status", "--porcelain=v1", "-z")
	if err != nil {
		return nil, err
	}
	if res.code != 0 {
		return nil, fmt.Errorf("childtree: status in %s: %s", dir, res.output())
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

func (t *Trees) inProgressOp(ctx context.Context, worktree string) string {
	for _, name := range inProgressOps {
		res, err := t.run(ctx, worktree, nil, "rev-parse", "--git-path", name)
		if err != nil || res.code != 0 {
			continue
		}
		resolved := strings.TrimSpace(res.stdout)
		if resolved == "" {
			continue
		}
		if !filepath.IsAbs(resolved) {
			resolved = filepath.Join(worktree, resolved)
		}
		if _, err := os.Lstat(resolved); err == nil {
			return name
		}
	}
	return ""
}

// removeEmptyParent drops the per-worker folder once its last child is gone.
// os.Remove refuses a non-empty directory, which is exactly the guard wanted.
func removeEmptyParent(path string) {
	_ = os.Remove(filepath.Dir(path))
}

func splitZ(s string) []string {
	var out []string
	for _, part := range strings.Split(s, "\x00") {
		if part != "" {
			out = append(out, part)
		}
	}
	return out
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
	if len(files) == 0 {
		return []string{"the worker's tree"}
	}
	return files
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
