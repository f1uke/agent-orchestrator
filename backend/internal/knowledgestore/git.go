package knowledgestore

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// gitTimeout bounds every git call this package makes. The store is a safety
// net on the teardown path, so a wedged git must cost seconds, not the teardown.
const gitTimeout = 60 * time.Second

// committedBlobs returns, for every path git has ever recorded in a commit
// reachable from ANY ref of the repository at repoDir (every branch, tag and
// remote-tracking ref, plus HEAD), the set of blob ids that path has held.
// With pathspecs it is limited to those literal repo-relative paths; with none
// it covers the whole history.
//
// "Tracked" in this package means exactly this: a file whose content is
// already in a commit on some branch is safe in git and needs no rescue. Both
// sides of every change are recorded, so a version a later commit replaced or
// deleted still counts as committed.
func committedBlobs(ctx context.Context, repoDir string, pathspecs []string) (map[string]map[string]bool, error) {
	args := []string{"--literal-pathspecs", "log", "--all", "--format=", "--raw", "--no-abbrev", "--no-renames", "-z"}
	if len(pathspecs) > 0 {
		args = append(args, "--")
		args = append(args, pathspecs...)
	}
	out, err := runGit(ctx, repoDir, nil, args...)
	if err != nil {
		return nil, err
	}
	blobs := map[string]map[string]bool{}
	// With -z each change is ":<modeA> <modeB> <blobA> <blobB> <status>" NUL
	// "<path>" NUL; --no-renames guarantees exactly one path per change.
	tokens := strings.Split(string(out), "\x00")
	for i := 0; i < len(tokens); i++ {
		meta := strings.TrimLeft(tokens[i], "\n")
		if !strings.HasPrefix(meta, ":") || i+1 >= len(tokens) {
			continue
		}
		i++
		path := tokens[i]
		fields := strings.Fields(meta)
		if len(fields) < 4 {
			continue
		}
		for _, id := range fields[2:4] {
			if isNullBlob(id) {
				continue
			}
			if blobs[path] == nil {
				blobs[path] = map[string]bool{}
			}
			blobs[path][id] = true
		}
	}
	return blobs, nil
}

// hashFiles returns the git blob id each file's raw bytes would have in the
// repository at repoDir (its object format decides SHA-1 or SHA-256), in the
// order given. Filters are skipped: the bytes are compared as they sit on disk.
func hashFiles(ctx context.Context, repoDir string, files []string) ([]string, error) {
	if len(files) == 0 {
		return nil, nil
	}
	stdin := strings.Join(files, "\n") + "\n"
	out, err := runGit(ctx, repoDir, strings.NewReader(stdin), "hash-object", "--no-filters", "--stdin-paths")
	if err != nil {
		return nil, err
	}
	ids := strings.Fields(string(out))
	if len(ids) != len(files) {
		return nil, fmt.Errorf("git hash-object returned %d ids for %d files", len(ids), len(files))
	}
	return ids, nil
}

// isGitRepo reports whether dir is inside a git work tree.
func isGitRepo(ctx context.Context, dir string) bool {
	out, err := runGit(ctx, dir, nil, "rev-parse", "--is-inside-work-tree")
	return err == nil && strings.TrimSpace(string(out)) == "true"
}

func isNullBlob(id string) bool {
	return strings.Trim(id, "0") == ""
}

func runGit(ctx context.Context, dir string, stdin *strings.Reader, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	if stdin != nil {
		cmd.Stdin = stdin
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", subcommand(args), err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

// subcommand names the git subcommand in args for an error message, skipping
// global options such as --literal-pathspecs.
func subcommand(args []string) string {
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			return a
		}
	}
	return strings.Join(args, " ")
}
