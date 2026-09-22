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

// What the file list was measured FROM. A session owns a branch, so the branch
// is normally the subject; HEAD is the fallback for a worktree whose branch we
// cannot name or cannot find.
const (
	ChangesSubjectBranch = "branch"
	ChangesSubjectHead   = "head"
)

// Where the worktree's HEAD stands relative to the session's branch. This is
// reported on EVERY payload, because "the diff is empty" and "the worktree is
// parked somewhere else" are different answers and the reader has to be able to
// tell them apart.
const (
	HeadOnBranch      = "on_branch"
	HeadDetached      = "detached"
	HeadOnOtherBranch = "other_branch"
)

// Change statuses, mirroring git's name-status letters.
const (
	ChangeAdded    = "added"
	ChangeModified = "modified"
	ChangeDeleted  = "deleted"
	ChangeRenamed  = "renamed"
)

// Entry kinds. A changed entry is a plain file unless it says otherwise.
const (
	EntryFile      = "file"
	EntryDirectory = "directory"
)

// maxUntrackedPerDir bounds how many files ONE untracked directory may
// contribute to the list before it is shown as a single directory row instead.
//
// Untracked directories are the reason this cap exists at all. git's default
// `-unormal` collapses a directory holding nothing tracked into one record and
// never names the files inside it, so a session's brand-new source directory
// arrived here as a phantom "file" and the real files were invisible. Asking
// git for every untracked file (`ls-files --others`) fixes that, but an iOS
// project carrying an uncommitted DerivedData tree would then push thousands of
// build artefacts through the 2000-file cap and bury the three .swift files the
// reviewer came for. So a directory is expanded when expanding it is useful and
// stands in for its own contents when it is not.
const maxUntrackedPerDir = 50

// TargetSource records HOW the target branch was determined, so the UI can say
// "vs main" when it is certain and "vs main (project default)" when inferred.
const (
	TargetFromPR              = "pr"
	TargetFromSessionPRTarget = "session_pr_target"
	TargetFromSessionBase     = "session_base"
	TargetFromProject         = "project"
	TargetFromGitOriginHead   = "git_origin_head"
)

// targetPR is the only thing target resolution needs to know about a pull
// request. It exists so the one chain below can serve callers holding either
// domain.PullRequest or domain.PRFacts without either type leaking into it.
type targetPR struct {
	Branch string
	Open   bool
}

// resolveTargetChain is THE target-branch resolution, shared by the session read
// model (toSession) and the Files panel's Changes mode. Keeping it in one place
// is the point of the feature: a session whose target is resolved differently
// depending on who is asking is a session with no target at all.
//
// Precedence, most authoritative first:
//
//  1. an OPEN PR's real target — the forge is ground truth, and a PR retargeted
//     directly on GitHub/GitLab must beat AO's stored intent;
//  2. any other PR's target — weaker, but still an observed fact;
//  3. the session's stored PRTarget — recorded at spawn, editable by the human;
//  4. the session's stored BaseBranch — for sessions predating a stored target;
//  5. the project's default branch.
//
// It deliberately does NOT fall back to a hardcoded "main": a wrong target
// produces a confidently wrong diff, which is worse than admitting we do not
// know. Callers with a worktree in hand may extend it with real repo knowledge
// (see resolveTargetBranch's origin/HEAD step); nobody may extend it with a guess.
func resolveTargetChain(prs []targetPR, prTarget, baseBranch, projectDefault string) (string, string) {
	for _, p := range prs {
		if p.Open {
			if b := strings.TrimSpace(p.Branch); b != "" {
				return b, TargetFromPR
			}
		}
	}
	for _, p := range prs {
		if b := strings.TrimSpace(p.Branch); b != "" {
			return b, TargetFromPR
		}
	}
	if b := strings.TrimSpace(prTarget); b != "" {
		return b, TargetFromSessionPRTarget
	}
	if b := strings.TrimSpace(baseBranch); b != "" {
		return b, TargetFromSessionBase
	}
	if b := strings.TrimSpace(projectDefault); b != "" {
		return b, TargetFromProject
	}
	return "", ""
}

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
	// Kind is EntryFile or EntryDirectory. A directory row is NOT a file the
	// viewer can open: it stands in for an untracked directory too large to
	// list (see maxUntrackedPerDir), and EntryCount says how many files it
	// hides. Naming the kind on the wire is what stops the renderer from
	// routing a directory to the file viewer, which is how a directory came to
	// answer "File not found" for a path that plainly exists.
	Kind string
	// EntryCount is how many untracked files an EntryDirectory row stands for.
	// Zero for a file.
	EntryCount int
}

// WorkspaceChangesResult is the Changes-mode payload: the files differing
// between the session's branch (including its working tree, when the worktree is
// standing on that branch) and the resolved target branch. The scope fields at
// the bottom say WHICH comparison this is, because "no changes" and "I measured
// something else" must never read the same.
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
	// TargetFetch reports how fresh the target branch's remote-tracking ref is
	// (one of the TargetFetch* constants), and TargetFetchError carries the
	// reason when it is TargetFetchFailed. Empty means there is no remote to be
	// behind. Without this a diff measured against refs nobody refreshed looks
	// exactly like a correct one.
	TargetFetch      string
	TargetFetchError string
	// Branch is the session's OWN branch - what the board, the pull request and
	// the human all mean by "this task's changes". Empty only when the session
	// records no branch AND the worktree is not standing on one.
	Branch string
	// BranchMissing reports that Branch is named but has no ref in this worktree
	// (never created, renamed, or deleted). The diff then falls back to HEAD, and
	// saying so is the difference between "nothing changed" and "I could not find
	// the thing you asked about".
	BranchMissing bool
	// DiffSubject is what Files was measured FROM: ChangesSubjectBranch (the
	// branch's tip) or ChangesSubjectHead (the worktree's HEAD).
	DiffSubject string
	// IncludesWorktree reports whether uncommitted and untracked work is folded
	// into Files. It is false exactly when the worktree is NOT standing on the
	// subject: those edits are measured against a different baseline, so mixing
	// them into the branch's diff would invent changes neither side made.
	// PendingPaths then counts what was left out.
	IncludesWorktree bool
	// PendingPaths counts the worktree paths carrying uncommitted, staged or
	// untracked work that Files does NOT include. Zero when IncludesWorktree.
	PendingPaths int
	// HeadState is where the worktree's HEAD stands relative to Branch:
	// HeadOnBranch, HeadDetached or HeadOnOtherBranch. HeadLabel names it - the
	// short sha for a detached HEAD, the branch name for another branch, empty
	// when HEAD is on Branch.
	HeadState string
	HeadLabel string
}

// changesScope is the answer to "what exactly are we comparing, and what is the
// worktree doing while we do it". It exists because those two questions used to
// be answered by the same ref: the diff was computed from the worktree's HEAD, so
// a session that had legitimately checked out its base commit (to install a
// baseline build and test an upgrade path) reported that its branch matched its
// target - with 38 committed files sitting on the branch. A session OWNS A
// BRANCH; that is the subject, and where HEAD happens to be parked is a separate
// fact the reader is told rather than a silent input to the answer.
type changesScope struct {
	// Target is the resolved comparison branch, TargetSource how it was resolved,
	// and TargetRef the ref that actually exists for it.
	Target, TargetSource, TargetRef string
	// MergeBase is merge-base(TargetRef, SubjectRev).
	MergeBase string
	// SubjectRev is the rev Files is measured from, and Subject says which kind
	// of rev it is (ChangesSubject*).
	SubjectRev, Subject string
	// Branch/BranchMissing/HeadState/HeadLabel/IncludesWorktree are reported
	// verbatim on the payload; see WorkspaceChangesResult for what each means.
	Branch                          string
	BranchMissing, IncludesWorktree bool
	HeadState, HeadLabel            string
	// Reason is non-empty when nothing can be diffed; the caller returns it as an
	// Available=false payload rather than an error.
	Reason string
	// TargetFetch/TargetFetchError carry the freshness of the target's
	// remote-tracking ref.
	TargetFetch, TargetFetchError string
}

// resolveChangesScope decides what a session's changes are measured from, and
// records where the worktree's HEAD is while they are.
//
// It is shared by the file LIST and the per-file diff on purpose: a row that the
// list offers must open on the same comparison the list counted, or a file listed
// as +38 opens on "no diff to show".
func (s *Service) resolveChangesScope(
	ctx context.Context, rec domain.SessionRecord, workspace string,
) changesScope {
	sc := changesScope{}
	sc.Target, sc.TargetSource = s.resolveTargetBranch(ctx, rec, workspace)
	if sc.Target == "" {
		sc.Reason = ChangesNoTargetBranch
		return sc
	}
	// Refresh the target's remote-tracking ref before reading it. This does not
	// block: it returns immediately with the freshness of what is on disk, and
	// the diff below is computed from whatever refs exist right now. A branch
	// that moved on the forge lands on the next poll rather than stalling this
	// render behind the network.
	sc.TargetFetch, sc.TargetFetchError = s.refreshTarget(ctx, workspace, sc.Target)

	ref, ok := resolveBranchRef(ctx, workspace, sc.Target)
	if !ok {
		// The branch is named but does not exist in this worktree (never fetched,
		// or renamed upstream). Naming it beats a bare "nothing to compare".
		sc.Reason = ChangesNoTargetBranch
		return sc
	}
	sc.TargetRef = ref

	headBranch, headSHA := readHead(ctx, workspace)
	sc.Branch = strings.TrimSpace(rec.Metadata.Branch)
	if sc.Branch == "" {
		// A session that recorded no branch (an early row, or one spawned into an
		// existing checkout) still owns whatever the worktree is standing on. This
		// is read knowledge, not a guess - and it keeps HeadState quiet for every
		// session that is simply working normally.
		sc.Branch = headBranch
	}
	switch headBranch {
	case "":
		// Nothing is adopted from a detached HEAD, so Branch here is either the
		// recorded one or empty; either way the worktree is parked on a commit.
		sc.HeadState, sc.HeadLabel = HeadDetached, headSHA
	case sc.Branch:
		sc.HeadState = HeadOnBranch
	default:
		sc.HeadState, sc.HeadLabel = HeadOnOtherBranch, headBranch
	}

	// The subject: the session's branch when it has a ref, HEAD when it does not.
	sc.Subject, sc.SubjectRev, sc.IncludesWorktree = ChangesSubjectHead, "HEAD", true
	if sc.Branch != "" {
		if branchRef, ok := resolveLocalBranchRef(ctx, workspace, sc.Branch); ok {
			sc.Subject, sc.SubjectRev = ChangesSubjectBranch, branchRef
			// Uncommitted work belongs to the diff only while the worktree is
			// standing on the subject. Parked elsewhere, its edits are measured
			// against another baseline; folding them in would report changes the
			// branch does not carry and hide the ones it does.
			sc.IncludesWorktree = sc.HeadState == HeadOnBranch
		} else {
			sc.BranchMissing = true
		}
	}

	baseOut, err := gitOutput(ctx, workspace, "merge-base", sc.TargetRef, sc.SubjectRev)
	if err != nil {
		// No common ancestor (unrelated histories, or an unborn HEAD). Degrading
		// to an empty state rather than erroring is the same contract DiffContext
		// follows.
		sc.Reason = ChangesNoTargetBranch
		return sc
	}
	sc.MergeBase = strings.TrimSpace(string(baseOut))
	return sc
}

// readHead reports the branch the worktree is on ("" when HEAD is detached) and
// HEAD's short sha ("" on an unborn HEAD).
func readHead(ctx context.Context, workspace string) (branch, shortSHA string) {
	if out, err := gitOutput(ctx, workspace, "symbolic-ref", "--quiet", "--short", "HEAD"); err == nil {
		branch = strings.TrimSpace(string(out))
	}
	if out, err := gitOutput(ctx, workspace, "rev-parse", "--short", "HEAD"); err == nil {
		shortSHA = strings.TrimSpace(string(out))
	}
	return branch, shortSHA
}

// resolveLocalBranchRef verifies the session's OWN branch, which is strictly the
// local ref. Unlike the target (see resolveBranchRef, which prefers
// origin/<branch>), a session's branch is the local one by definition: its
// newest commits may not be pushed yet, and origin's copy of the same name would
// silently under-report them.
func resolveLocalBranchRef(ctx context.Context, workspace, branch string) (string, bool) {
	ref := "refs/heads/" + branch
	if _, err := gitOutput(ctx, workspace, "rev-parse", "--verify", "--quiet", ref+"^{commit}"); err != nil {
		return "", false
	}
	return ref, true
}

// WorkspaceChanges lists the files differing between the session's BRANCH and
// its target branch, folding in uncommitted working-tree work while the worktree
// is standing on that branch (see resolveChangesScope for what happens when it
// is not, and why the payload says which of the two it did).
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

	sc := s.resolveChangesScope(ctx, rec, workspace)
	res := WorkspaceChangesResult{
		TargetBranch: sc.Target, TargetSource: sc.TargetSource, MergeBase: sc.MergeBase,
		TargetFetch: sc.TargetFetch, TargetFetchError: sc.TargetFetchError,
		Branch: sc.Branch, BranchMissing: sc.BranchMissing, DiffSubject: sc.Subject,
		IncludesWorktree: sc.IncludesWorktree, HeadState: sc.HeadState, HeadLabel: sc.HeadLabel,
	}
	if sc.Reason != "" {
		res.Reason = sc.Reason
		return res, nil
	}
	res.Available = true

	// With the worktree standing on the subject, the diff runs against the
	// WORKING TREE (no second rev) so committed and uncommitted work appear in one
	// list — the reviewer wants "what has this session done", not "what has it
	// committed". Parked elsewhere, the subject's tip is named explicitly and the
	// worktree is reported separately instead (see countPendingPaths).
	args := []string{"diff", "--name-status", "-M", "-z", sc.MergeBase}
	numArgs := []string{"diff", "--numstat", "-M", "-z", sc.MergeBase}
	if !sc.IncludesWorktree {
		args = append(args, sc.SubjectRev)
		numArgs = append(numArgs, sc.SubjectRev)
	}
	nameOut, err := gitOutput(ctx, workspace, args...)
	if err != nil {
		return WorkspaceChangesResult{Reason: ChangesNotARepo}, nil //nolint:nilerr // intentional: degrade, don't error
	}
	files := parseNameStatusZ(string(nameOut))

	if numOut, err := gitOutput(ctx, workspace, numArgs...); err == nil {
		applyNumstatZ(files, string(numOut))
	}

	dirty := map[string]bool{}
	if !sc.IncludesWorktree {
		// The list is the branch's committed work, so every file in it is
		// committed by construction, and the worktree's own pending work is
		// declared as a count rather than silently dropped.
		res.PendingPaths = countPendingPaths(ctx, workspace)
	} else if stOut, err := gitOutput(ctx, workspace, "status", "--porcelain=v1", "-z"); err == nil {
		// git diff never reports untracked files, and a brand-new file a worker has
		// not staged yet is exactly what a reviewer is looking for.
		for _, u := range parsePorcelainZ(string(stOut), dirty) {
			files = appendUntracked(ctx, workspace, files, u, dirty)
		}
	}
	for i := range files {
		// Committed = "this file has no pending working-tree work".
		files[i].Committed = !dirty[files[i].Path] && !dirty[files[i].OldPath]
		if files[i].Kind == "" {
			files[i].Kind = EntryFile
		}
	}

	sortChangedFiles(files)
	if len(files) > maxChangedFiles {
		files = files[:maxChangedFiles]
		res.Truncated = true
	}
	res.Files = files
	return res, nil
}

// countPendingPaths counts the worktree paths carrying uncommitted, staged or
// untracked work. It is only asked when those paths are NOT in the file list: a
// count is what lets the panel say "the branch's commits are listed; the worktree
// also holds 3 paths of work measured against something else" instead of leaving
// the reader to assume one way or the other.
//
// An untracked DIRECTORY counts as the single path git reports it as, the same
// way the list would show it.
func countPendingPaths(ctx context.Context, workspace string) int {
	out, err := gitOutput(ctx, workspace, "status", "--porcelain=v1", "-z")
	if err != nil {
		return 0
	}
	dirty := map[string]bool{}
	untracked := parsePorcelainZ(string(out), dirty)
	n := len(dirty)
	for _, u := range untracked {
		// An untracked FILE is already counted in dirty; a collapsed directory is not.
		if u.Dir {
			n++
		}
	}
	return n
}

// resolveTargetBranch answers "what is this session's branch measured against",
// most-authoritative first. It deliberately does NOT fall back to a hardcoded
// "main": a wrong target produces a confidently wrong diff, which is worse than
// telling the user we do not know. ProjectConfig.DefaultBranch is read RAW (not
// via WithDefaults) for the same reason — WithDefaults would synthesise "main"
// for a project that never configured one.
func (s *Service) resolveTargetBranch(ctx context.Context, rec domain.SessionRecord, workspace string) (string, string) {
	var prs []targetPR
	if rows, err := s.store.ListPRsBySession(ctx, rec.ID); err == nil {
		for _, p := range rows {
			prs = append(prs, targetPR{Branch: p.TargetBranch, Open: !p.Merged && !p.Closed})
		}
	}
	var projectDefault string
	if proj, ok, err := s.store.GetProject(ctx, string(rec.ProjectID)); err == nil && ok {
		// WithDefaults, not the raw field: session_manager creates the worktree
		// from `project.Config.WithDefaults().DefaultBranch` (manager.go:651), so
		// this reads back the SAME resolution that produced this branch rather
		// than guessing. That is what keeps it honest — and the caller still
		// requires the ref to exist (resolveBranchRef) before diffing against it,
		// so a synthesised default that is not really in the repo degrades to
		// "no target branch" instead of a confidently wrong diff.
		projectDefault = proj.Config.WithDefaults().DefaultBranch
	}
	if b, src := resolveTargetChain(prs, rec.PRTarget, rec.BaseBranch, projectDefault); b != "" {
		return b, src
	}
	// Below the shared chain, and only here: origin/HEAD is real knowledge read
	// out of the repo, not an assumption — but it needs a worktree, which the
	// read model does not have. This is the one step Changes mode can take that
	// toSession cannot, which is why it lives at this call site rather than in
	// resolveTargetChain.
	if out, err := gitOutput(ctx, workspace, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); err == nil {
		if b := strings.TrimSpace(string(out)); b != "" {
			return strings.TrimPrefix(b, "origin/"), TargetFromGitOriginHead
		}
	}
	return "", ""
}

// resolveBranchRef finds a ref that actually exists for the named branch,
// preferring the REMOTE-TRACKING ref over the local branch.
//
// The order is the whole correctness of this view. `refs/heads/<branch>` in a
// project repo is a human's personal checkout of the integration branch, which
// only advances when they run `git pull`, while AO cuts every session worktree
// from `origin/<branch>` (see gitworktree's baseRefCandidates). Measuring
// against the local ref therefore bills every commit that landed in between to
// this session — other people's already-merged work, shown as though the worker
// wrote it. gitworktree's syncBaseRefCandidates already resolves the base this
// way, for this reason; this path simply did not follow it.
//
// The local branch remains the fallback so a repository with no remote (or a
// branch that has never been fetched) still diffs. The bare name comes last so
// a qualified target like "upstream/main" still resolves.
func resolveBranchRef(ctx context.Context, workspace, branch string) (string, bool) {
	for _, cand := range []string{"refs/remotes/origin/" + branch, "refs/heads/" + branch, branch} {
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

// untrackedEntry is one `??` record from git status.
//
// Dir reports that git collapsed a whole untracked DIRECTORY into this single
// record and never named the files inside it. That is git's default `-unormal`
// behaviour for any directory holding nothing tracked, and the trailing slash
// git puts on such a record is the ONLY thing distinguishing it from a file.
// Carrying that distinction in a field rather than in a string suffix is the
// point: the slash used to ride all the way to the renderer, where a directory
// was drawn as a file and clicking it asked the file viewer to open a folder.
type untrackedEntry struct {
	// Path is repo-relative and slash-separated, with NO trailing slash.
	Path string
	Dir  bool
}

// parsePorcelainZ reads `git status --porcelain=v1 -z`, filling dirty with every
// path that has working-tree or index changes, and returning the untracked ones.
//
// Each record is "XY<space>path". A rename record ("R  new") is followed by a
// separate token holding the original path.
func parsePorcelainZ(out string, dirty map[string]bool) []untrackedEntry {
	tok := splitNUL(out)
	var untracked []untrackedEntry
	for i := 0; i < len(tok); i++ {
		rec := tok[i]
		if len(rec) < 4 {
			continue
		}
		x, y, path := rec[0], rec[1], rec[3:]
		if x == '?' && y == '?' {
			if dir := strings.TrimSuffix(path, "/"); dir != path {
				// A collapsed directory. It is NOT marked dirty: no file has
				// that path, so the entry would never match anything, and the
				// files it stands for are marked as they are expanded.
				untracked = append(untracked, untrackedEntry{Path: dir, Dir: true})
				continue
			}
			untracked = append(untracked, untrackedEntry{Path: path})
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

// appendUntracked adds one `??` entry to the changed-file list, expanding a
// collapsed directory into the files inside it.
//
// A directory small enough to list becomes its files - that is the whole fix:
// the two .swift files a session just wrote inside a new directory are what the
// reviewer needs to see, and git never named them. A directory too large to
// list (maxUntrackedPerDir) becomes ONE directory-kind row carrying its file
// count, so an uncommitted build tree says how big it is instead of flooding
// the list with artefacts.
func appendUntracked(
	ctx context.Context, workspace string, files []ChangedFile, u untrackedEntry, dirty map[string]bool,
) []ChangedFile {
	if !u.Dir {
		return appendUntrackedFile(workspace, files, u.Path, dirty)
	}
	paths, total := listUntrackedDir(ctx, workspace, u.Path)
	if total == 0 || total > maxUntrackedPerDir {
		if _, seen := indexOfPath(files, u.Path); seen {
			return files
		}
		dirty[u.Path] = true
		return append(files, ChangedFile{
			Path: u.Path, Status: ChangeAdded, Kind: EntryDirectory, EntryCount: total,
		})
	}
	for _, p := range paths {
		if dir := strings.TrimSuffix(p, "/"); dir != p {
			// git reports an EMBEDDED git repository as a directory rather than
			// recursing into it, since its contents belong to another repo.
			// Nothing here can open it, so it keeps its own directory row.
			if _, seen := indexOfPath(files, dir); !seen {
				dirty[dir] = true
				files = append(files, ChangedFile{Path: dir, Status: ChangeAdded, Kind: EntryDirectory})
			}
			continue
		}
		files = appendUntrackedFile(workspace, files, p, dirty)
	}
	return files
}

// appendUntrackedFile adds one untracked FILE, skipping a path the diff already
// reported.
func appendUntrackedFile(workspace string, files []ChangedFile, path string, dirty map[string]bool) []ChangedFile {
	if _, seen := indexOfPath(files, path); seen {
		return files
	}
	dirty[path] = true
	cf := ChangedFile{Path: path, Status: ChangeAdded, Kind: EntryFile}
	countUntracked(filepath.Join(absRoot(workspace), filepath.FromSlash(path)), &cf)
	return append(files, cf)
}

// listUntrackedDir names the untracked files inside one directory, honouring
// .gitignore exactly as git status does, and returns them with the total count.
//
// `ls-files --others` is asked for the directory specifically rather than
// switching the whole status call to `-uall`: expanding only the directories
// git actually collapsed keeps the common case at one status call, and it is
// the per-directory total - not a global one - that decides whether expanding
// is useful (see maxUntrackedPerDir).
func listUntrackedDir(ctx context.Context, workspace, dir string) ([]string, int) {
	out, err := gitOutput(ctx, workspace, "ls-files", "--others", "--exclude-standard", "-z", "--", dir+"/")
	if err != nil {
		return nil, 0
	}
	paths := splitNUL(string(out))
	return paths, len(paths)
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
