package session

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	previewutil "github.com/aoagents/agent-orchestrator/backend/internal/preview"
)

// Reason values explaining an unavailable WorkspaceChangesResult. Each is a
// state the UI renders as its own empty view rather than an error.
const (
	ChangesNoWorkspace    = "no_workspace"     // worktree path unset or gone from disk
	ChangesNotARepo       = "not_a_repo"       // workspace exists but is not a git repo
	ChangesNoTargetBranch = "no_target_branch" // nothing to compare against
)

// Change statuses, mirroring git's name-status letters.
const (
	ChangeAdded    = "added"
	ChangeModified = "modified"
	ChangeDeleted  = "deleted"
	ChangeRenamed  = "renamed"
)

// TargetSource records HOW the target branch was determined, so the UI can say
// "vs main" when it is certain and "vs main (project default)" when inferred.
const (
	TargetFromPR              = "pr"
	TargetFromSessionPRTarget = "session_pr_target"
	TargetFromSessionBase     = "session_base"
	TargetFromProject         = "project"
	TargetFromGitOriginHead   = "git_origin_head"
)

// maxChangedFiles bounds the returned list so a branch that rewrites a huge tree
// cannot return an unbounded payload to the rail.
const maxChangedFiles = 2000

// ChangedFile is one file differing between the session branch and its target.
type ChangedFile struct {
	// Path is repo-relative and slash-separated. For a rename it is the NEW path.
	Path string
	// OldPath is set only for a rename.
	OldPath string
	// Status is added | modified | deleted | renamed.
	Status    string
	Additions int
	Deletions int
	// Binary reports that git emitted "-" counts, so Additions/Deletions are
	// meaningless and the viewer must not render them arithmetically.
	Binary bool
	// Committed is false when the file also has working-tree changes that are
	// not yet committed. A worker mid-task is the common case, so hiding these
	// would make the panel under-report its own session.
	Committed bool
}

// WorkspaceChangesResult is the Changes-mode payload: the files differing
// between the session's branch (including its working tree) and the resolved
// target branch.
type WorkspaceChangesResult struct {
	Available bool
	// Reason explains Available=false (one of the Changes* constants).
	Reason string
	// TargetBranch is the resolved comparison branch; TargetSource says how it
	// was resolved. Both may be set even when Available is false, so the UI can
	// name the branch it failed to resolve.
	TargetBranch string
	TargetSource string
	MergeBase    string
	Files        []ChangedFile
	Truncated    bool
}

// WorkspaceChanges lists the files differing between the session's branch and
// its target branch, folding in uncommitted working-tree work.
//
// Every degraded state (no worktree on disk, not a repo, no resolvable target
// branch) comes back Available=false with a Reason rather than an error, so the
// rail renders a specific empty state. Only an unknown session is an error —
// the same contract DiffContext follows.
func (s *Service) WorkspaceChanges(ctx context.Context, id domain.SessionID) (WorkspaceChangesResult, error) {
	rec, ok, err := s.store.GetSession(ctx, id)
	if err != nil {
		return WorkspaceChangesResult{}, fmt.Errorf("get %s: %w", id, err)
	}
	if !ok {
		return WorkspaceChangesResult{}, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}

	workspace := rec.Metadata.WorkspacePath
	if workspace == "" || !isDir(workspace) {
		// A merged/cleaned-up session keeps its board row after its worktree is
		// removed; that is a normal state, not a failure.
		return WorkspaceChangesResult{Reason: ChangesNoWorkspace}, nil
	}
	if _, err := gitOutput(ctx, workspace, "rev-parse", "--git-dir"); err != nil {
		return WorkspaceChangesResult{Reason: ChangesNotARepo}, nil //nolint:nilerr // intentional: degrade, don't error
	}

	branch, source := s.resolveTargetBranch(ctx, rec, workspace)
	if branch == "" {
		return WorkspaceChangesResult{Reason: ChangesNoTargetBranch}, nil
	}
	ref, ok := resolveBranchRef(ctx, workspace, branch)
	if !ok {
		// The branch is named but does not exist in this worktree (never fetched,
		// or renamed upstream). Naming it beats a bare "nothing to compare".
		return WorkspaceChangesResult{
			Reason: ChangesNoTargetBranch, TargetBranch: branch, TargetSource: source,
		}, nil
	}
	baseOut, err := gitOutput(ctx, workspace, "merge-base", ref, "HEAD")
	if err != nil {
		// No common ancestor (unrelated histories, or an unborn HEAD).
		return WorkspaceChangesResult{
			Reason: ChangesNoTargetBranch, TargetBranch: branch, TargetSource: source,
		}, nil //nolint:nilerr // intentional: degrade, don't error
	}
	mergeBase := strings.TrimSpace(string(baseOut))

	res := WorkspaceChangesResult{
		Available: true, TargetBranch: branch, TargetSource: source, MergeBase: mergeBase,
	}

	// Diffing mergeBase against the WORKING TREE (no second ref) is what makes
	// committed and uncommitted work appear in one list — the reviewer wants
	// "what has this session done", not "what has it committed".
	nameOut, err := gitOutput(ctx, workspace, "diff", "--name-status", "-M", "-z", mergeBase)
	if err != nil {
		return WorkspaceChangesResult{Reason: ChangesNotARepo}, nil //nolint:nilerr // intentional: degrade, don't error
	}
	files := parseNameStatusZ(string(nameOut))

	if numOut, err := gitOutput(ctx, workspace, "diff", "--numstat", "-M", "-z", mergeBase); err == nil {
		applyNumstatZ(files, string(numOut))
	}

	// git diff never reports untracked files, and a brand-new file a worker has
	// not staged yet is exactly what a reviewer is looking for.
	dirty := map[string]bool{}
	if stOut, err := gitOutput(ctx, workspace, "status", "--porcelain=v1", "-z"); err == nil {
		untracked := parsePorcelainZ(string(stOut), dirty)
		for _, p := range untracked {
			if _, seen := indexOfPath(files, p); seen {
				continue
			}
			cf := ChangedFile{Path: p, Status: ChangeAdded}
			countUntracked(filepath.Join(absRoot(workspace), filepath.FromSlash(p)), &cf)
			files = append(files, cf)
		}
	}
	for i := range files {
		// Committed = "this file has no pending working-tree work".
		files[i].Committed = !dirty[files[i].Path] && !dirty[files[i].OldPath]
	}

	sortChangedFiles(files)
	if len(files) > maxChangedFiles {
		files = files[:maxChangedFiles]
		res.Truncated = true
	}
	res.Files = files
	return res, nil
}

// resolveTargetBranch answers "what is this session's branch measured against",
// most-authoritative first. It deliberately does NOT fall back to a hardcoded
// "main": a wrong target produces a confidently wrong diff, which is worse than
// telling the user we do not know. ProjectConfig.DefaultBranch is read RAW (not
// via WithDefaults) for the same reason — WithDefaults would synthesise "main"
// for a project that never configured one.
func (s *Service) resolveTargetBranch(ctx context.Context, rec domain.SessionRecord, workspace string) (string, string) {
	if prs, err := s.store.ListPRsBySession(ctx, rec.ID); err == nil {
		// A still-open PR is the only source that reflects where this work is
		// actually going; prefer it over any spawn-time intent. A merged/closed
		// PR still beats the weaker fallbacks, so it is tried second.
		for _, p := range prs {
			if !p.Merged && !p.Closed {
				if b := strings.TrimSpace(p.TargetBranch); b != "" {
					return b, TargetFromPR
				}
			}
		}
		for _, p := range prs {
			if b := strings.TrimSpace(p.TargetBranch); b != "" {
				return b, TargetFromPR
			}
		}
	}
	// PRTarget/BaseBranch are only populated for TODO-spawned sessions
	// (domain/session.go: "Empty for normal spawns"), so they usually miss.
	if b := strings.TrimSpace(rec.PRTarget); b != "" {
		return b, TargetFromSessionPRTarget
	}
	if b := strings.TrimSpace(rec.BaseBranch); b != "" {
		return b, TargetFromSessionBase
	}
	if proj, ok, err := s.store.GetProject(ctx, string(rec.ProjectID)); err == nil && ok {
		// WithDefaults, not the raw field: session_manager creates the worktree
		// from `project.Config.WithDefaults().DefaultBranch` (manager.go:651), so
		// this reads back the SAME resolution that produced this branch rather
		// than guessing. That is what keeps it honest — and the caller still
		// requires the ref to exist (resolveBranchRef) before diffing against it,
		// so a synthesised default that is not really in the repo degrades to
		// "no target branch" instead of a confidently wrong diff.
		if b := strings.TrimSpace(proj.Config.WithDefaults().DefaultBranch); b != "" {
			return b, TargetFromProject
		}
	}
	// origin/HEAD is real knowledge read out of the repo, not an assumption.
	if out, err := gitOutput(ctx, workspace, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); err == nil {
		if b := strings.TrimSpace(string(out)); b != "" {
			return strings.TrimPrefix(b, "origin/"), TargetFromGitOriginHead
		}
	}
	return "", ""
}

// resolveBranchRef finds a ref that actually exists for the named branch,
// preferring the local branch and falling back to its origin tracking ref.
func resolveBranchRef(ctx context.Context, workspace, branch string) (string, bool) {
	for _, cand := range []string{branch, "origin/" + branch, "refs/remotes/origin/" + branch} {
		if _, err := gitOutput(ctx, workspace, "rev-parse", "--verify", "--quiet", cand+"^{commit}"); err == nil {
			return cand, true
		}
	}
	return "", false
}

// parseNameStatusZ parses `git diff --name-status -M -z` output.
//
// Records are NUL-separated: a status token followed by one path, except
// rename/copy (R100 / C75) which is followed by TWO paths (old, then new).
// Verified against real git output, not inferred.
func parseNameStatusZ(out string) []ChangedFile {
	tok := splitNUL(out)
	var files []ChangedFile
	for i := 0; i < len(tok); {
		status := tok[i]
		if status == "" {
			i++
			continue
		}
		switch status[0] {
		case 'R', 'C':
			if i+2 >= len(tok) {
				return files
			}
			files = append(files, ChangedFile{
				OldPath: tok[i+1], Path: tok[i+2], Status: ChangeRenamed,
			})
			i += 3
		default:
			if i+1 >= len(tok) {
				return files
			}
			files = append(files, ChangedFile{Path: tok[i+1], Status: statusFromLetter(status[0])})
			i += 2
		}
	}
	return files
}

func statusFromLetter(c byte) string {
	switch c {
	case 'A':
		return ChangeAdded
	case 'D':
		return ChangeDeleted
	default:
		// M, T (typechange) and anything else read as a content change.
		return ChangeModified
	}
}

// applyNumstatZ overlays `git diff --numstat -M -z` counts onto files.
//
// A normal record is one token "adds\tdels\tpath". A rename record has an EMPTY
// path field ("adds\tdels\t") and is followed by two more tokens, old then new.
// A binary file reports "-" for both counts. Verified against real git output.
func applyNumstatZ(files []ChangedFile, out string) {
	tok := splitNUL(out)
	for i := 0; i < len(tok); {
		rec := tok[i]
		if rec == "" {
			i++
			continue
		}
		parts := strings.SplitN(rec, "\t", 3)
		if len(parts) < 3 {
			i++
			continue
		}
		addStr, delStr, pathField := parts[0], parts[1], parts[2]
		path := pathField
		consumed := 1
		if pathField == "" {
			// rename: the two paths follow as separate NUL-terminated tokens
			if i+2 >= len(tok) {
				return
			}
			path = tok[i+2] // new path
			consumed = 3
		}
		if idx, ok := indexOfPath(files, path); ok {
			binary := addStr == "-" || delStr == "-"
			files[idx].Binary = binary
			if !binary {
				files[idx].Additions = atoiSafe(addStr)
				files[idx].Deletions = atoiSafe(delStr)
			}
		}
		i += consumed
	}
}

// parsePorcelainZ reads `git status --porcelain=v1 -z`, filling dirty with every
// path that has working-tree or index changes, and returning the untracked ones.
//
// Each record is "XY<space>path". A rename record ("R  new") is followed by a
// separate token holding the original path.
func parsePorcelainZ(out string, dirty map[string]bool) []string {
	tok := splitNUL(out)
	var untracked []string
	for i := 0; i < len(tok); i++ {
		rec := tok[i]
		if len(rec) < 4 {
			continue
		}
		x, y, path := rec[0], rec[1], rec[3:]
		if x == '?' && y == '?' {
			untracked = append(untracked, path)
			dirty[path] = true
			continue
		}
		dirty[path] = true
		if x == 'R' || x == 'C' {
			// the original path rides in the next token
			if i+1 < len(tok) {
				dirty[tok[i+1]] = true
				i++
			}
		}
	}
	return untracked
}

// countUntracked fills in the line count for an untracked file, which git diff
// cannot report. A binary or oversized blob is marked rather than counted.
func countUntracked(abs string, cf *ChangedFile) {
	info, err := os.Stat(abs)
	if err != nil || !info.Mode().IsRegular() {
		return
	}
	if info.Size() > maxWorkspaceFileBytes {
		cf.Binary = true
		return
	}
	data, err := os.ReadFile(abs) //nolint:gosec // path is workspace-confined by the caller
	if err != nil {
		return
	}
	if isBinary(data) {
		cf.Binary = true
		return
	}
	if len(data) == 0 {
		return
	}
	cf.Additions = strings.Count(strings.TrimSuffix(string(data), "\n"), "\n") + 1
}

// confinedWorkspacePath resolves a REPO-RELATIVE path inside the session's
// workspace and returns both the absolute path and the slash-separated relative
// path safe to hand to git.
//
// It deliberately does NOT use the absolute/`~` handling that ResolveWorkspaceRef
// and ReadWorkspaceFile grew for the terminal's click-to-open feature. That
// widening is scoped to opening a file a user clicked in their own terminal;
// every path reaching the Changes-mode endpoints names a file inside the
// session's worktree, so it stays confined.
//
// Two sharp edges in ConfinedPath are handled here rather than inherited:
//   - it rewrites an empty or "." path to "index.html" (correct for the preview
//     route it was written for, wrong here), so empty is rejected up front;
//   - it is purely lexical and never resolves symlinks, so a symlink inside the
//     worktree pointing outside it would pass. The containment is re-checked
//     after EvalSymlinks. A path that does not resolve (a DELETED file, which is
//     exactly what Changes mode must still diff) keeps the lexical result.
func confinedWorkspacePath(workspace, rel string) (abs, safeRel string, ok bool) {
	rel = strings.TrimSpace(rel)
	if rel == "" || rel == "." {
		return "", "", false
	}
	if _, isAbs := refTarget(rel); isAbs {
		return "", "", false
	}
	confined, ok := previewutil.ConfinedPath(workspace, rel)
	if !ok {
		return "", "", false
	}
	r, err := filepath.Rel(absRoot(workspace), confined)
	if err != nil {
		return "", "", false
	}
	if resolved, err := filepath.EvalSymlinks(confined); err == nil {
		if _, within := relWithin(resolvedRoot(workspace), resolved); !within {
			return "", "", false
		}
	}
	return confined, filepath.ToSlash(r), true
}

func splitNUL(s string) []string {
	parts := strings.Split(s, "\x00")
	if n := len(parts); n > 0 && parts[n-1] == "" {
		parts = parts[:n-1]
	}
	return parts
}

func indexOfPath(files []ChangedFile, path string) (int, bool) {
	for i := range files {
		if files[i].Path == path {
			return i, true
		}
	}
	return 0, false
}

func atoiSafe(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0
	}
	return n
}

func isDir(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

// sortChangedFiles gives the list a stable, path-alphabetical order so the rail
// does not reshuffle between refreshes.
func sortChangedFiles(files []ChangedFile) {
	for i := 1; i < len(files); i++ {
		for j := i; j > 0 && files[j].Path < files[j-1].Path; j-- {
			files[j], files[j-1] = files[j-1], files[j]
		}
	}
}
