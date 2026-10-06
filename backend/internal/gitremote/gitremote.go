// Package gitremote decides which git remote holds a project's code, and
// where a branch of it is fetched from.
//
// "origin" is a convention, not a fact. A repository cloned under another name,
// or one carrying several projects' remotes side by side (advisor-ios-app has
// `Advisor` and `Nter`, no origin), has no origin at all, and every place that
// hardcoded it went wrong in its own quiet way: worktrees cut from the human's
// stale local base, a fetch failing every sync, an empty repo URL hiding the
// project's forge. This package is the one answer the whole backend uses, so a
// project is read the same way everywhere.
package gitremote

import (
	"context"
	"net/url"
	"slices"
	"strings"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

// Origin is the conventional remote name. It is one step of the resolution
// below, never an assumption on its own.
const Origin = "origin"

// Git runs one git command in dir and returns its standard output. Callers
// pass their own runner so their tests can observe or fake the commands; Exec
// is the plain one.
type Git func(ctx context.Context, dir string, args ...string) ([]byte, error)

// Exec runs `git -C dir args...`.
func Exec(ctx context.Context, dir string, args ...string) ([]byte, error) {
	return aoprocess.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...).Output()
}

// Remotes lists the repository's configured remotes; nil on any error.
func Remotes(ctx context.Context, git Git, dir string) []string {
	out, err := git(ctx, dir, "remote")
	if err != nil {
		return nil
	}
	return strings.Fields(string(out))
}

// RemoteURL returns one remote's fetch URL, or "" when it is not configured.
func RemoteURL(ctx context.Context, git Git, dir, remote string) string {
	if remote == "" {
		return ""
	}
	out, err := git(ctx, dir, "remote", "get-url", remote)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// Pick chooses the remote that holds the project's code, in order:
//
//  1. the remote whose URL is repoURL - the project's own repository as it was
//     registered, and the only safe pick when remotes point at different
//     projects;
//  2. origin;
//  3. the remote branch tracks (branch.<branch>.remote), when branch is set;
//  4. the sole remote.
//
// Anything else is "" - no remote, or several and nothing to choose by - rather
// than a guess between remotes that may hold different repositories.
func Pick(ctx context.Context, git Git, dir, repoURL, branch string) string {
	remotes := Remotes(ctx, git, dir)
	if len(remotes) == 0 {
		return ""
	}
	if repoURL = strings.TrimSpace(repoURL); repoURL != "" {
		for _, r := range remotes {
			if SameRepo(RemoteURL(ctx, git, dir, r), repoURL) {
				return r
			}
		}
	}
	if slices.Contains(remotes, Origin) {
		return Origin
	}
	if branch = strings.TrimSpace(branch); branch != "" {
		if out, err := git(ctx, dir, "config", "--get", "branch."+branch+".remote"); err == nil {
			if r := strings.TrimSpace(string(out)); slices.Contains(remotes, r) {
				return r
			}
		}
	}
	if len(remotes) == 1 {
		return remotes[0]
	}
	return ""
}

// ForRepo is Pick with the checked-out branch as the tracking hint: the remote
// of the repository at dir, when no particular branch is in question.
func ForRepo(ctx context.Context, git Git, dir, repoURL string) string {
	return Pick(ctx, git, dir, repoURL, currentBranch(ctx, git, dir))
}

// ProjectURL is the URL of the repository at dir, for registering it as a
// project (or backfilling one registered before its remote could be read): the
// remote ForRepo picks, with no URL on record to match yet. "" when there is no
// remote it can be attributed to - registration must not fail over that.
func ProjectURL(ctx context.Context, git Git, dir string) string {
	return RemoteURL(ctx, git, dir, ForRepo(ctx, git, dir, ""))
}

// DefaultBranch is the default branch the project's remote advertises
// (refs/remotes/<remote>/HEAD), or "" when that is not known locally.
func DefaultBranch(ctx context.Context, git Git, dir, repoURL string) string {
	remote := ForRepo(ctx, git, dir, repoURL)
	if remote == "" {
		return ""
	}
	out, err := git(ctx, dir, "symbolic-ref", "--quiet", "--short", "refs/remotes/"+remote+"/HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(strings.TrimSpace(string(out)), remote+"/")
}

// Location is where a branch lives: the remote it is fetched from, and its
// name there.
type Location struct {
	// Remote is "" when the repository has no remote the branch can be
	// attributed to; only the local branch is then used and nothing is fetched.
	Remote string
	// Branch is the branch's name on Remote. A remote-qualified target
	// ("origin/feature/x") has its remote prefix stripped here.
	Branch string
}

// Locate decides where target is fetched from. A remote-qualified target
// ("origin/feature/x", "upstream/main") names its own remote, unless a local
// branch has that exact name; anything else goes to Pick, with target as the
// tracking hint.
func Locate(ctx context.Context, git Git, dir, target, repoURL string) Location {
	target = strings.TrimSpace(target)
	if target == "" {
		return Location{}
	}
	if !refExists(ctx, git, dir, "refs/heads/"+target) {
		for _, r := range Remotes(ctx, git, dir) {
			if rest, ok := strings.CutPrefix(target, r+"/"); ok && rest != "" {
				return Location{Remote: r, Branch: rest}
			}
		}
	}
	return Location{Remote: Pick(ctx, git, dir, repoURL, target), Branch: target}
}

// TrackingRef is where the branch lands when fetched; "" without a remote.
func (l Location) TrackingRef() string {
	if l.Remote == "" || l.Branch == "" {
		return ""
	}
	return "refs/remotes/" + l.Remote + "/" + l.Branch
}

// Candidates lists where the branch may be found, remote-tracking first: a
// project repo's local branch is a human's checkout that only moves when they
// pull, so it must never win over the remote's copy. The local branch stays as
// the fallback for a remoteless repository or a branch never fetched, and the
// bare name last lets git's own resolution try tags.
func (l Location) Candidates() []string {
	var out []string
	if ref := l.TrackingRef(); ref != "" {
		out = append(out, ref)
	}
	if l.Branch != "" {
		out = append(out, "refs/heads/"+l.Branch, l.Branch)
	}
	return out
}

// FetchArgs refreshes this ONE branch's remote-tracking ref and nothing else -
// refs only, never a working tree, checkout, pull or local branch, since every
// worktree shares the repository's refs and the human's own checkout may have
// the branch checked out and dirty. The refspec is explicit so the fetch stays
// narrow and lands exactly at TrackingRef. nil without a remote: there is
// nothing to fetch, which is not a failure.
func (l Location) FetchArgs() []string {
	if l.TrackingRef() == "" {
		return nil
	}
	return []string{
		"fetch", "--quiet", "--no-tags", "--no-write-fetch-head", l.Remote,
		"+refs/heads/" + l.Branch + ":" + l.TrackingRef(),
	}
}

// SameRepo reports whether two remote URLs name the same repository, whatever
// their spelling: SSH or HTTPS, with or without ".git", a trailing slash, or a
// different host case.
func SameRepo(a, b string) bool {
	a, b = strings.TrimSpace(a), strings.TrimSpace(b)
	if a == "" || b == "" {
		return false
	}
	ah, ap, aok := splitRepoURL(a)
	bh, bp, bok := splitRepoURL(b)
	if !aok || !bok {
		return strings.EqualFold(a, b)
	}
	return strings.EqualFold(ah, bh) && strings.EqualFold(ap, bp)
}

// splitRepoURL reads a forge URL (scp-style git@host:path, ssh://, https://)
// into its host and repository path.
func splitRepoURL(raw string) (host, path string, ok bool) {
	if !strings.Contains(raw, "://") {
		_, rest, found := strings.Cut(raw, "@")
		if !found {
			return "", "", false
		}
		host, path, found = strings.Cut(rest, ":")
		if !found {
			return "", "", false
		}
	} else {
		u, err := url.Parse(raw)
		if err != nil {
			return "", "", false
		}
		host, path = u.Hostname(), u.Path
	}
	host = strings.ToLower(strings.TrimSpace(host))
	path = strings.TrimSuffix(strings.Trim(path, "/"), ".git")
	if host == "" || path == "" {
		return "", "", false
	}
	return host, path, true
}

func currentBranch(ctx context.Context, git Git, dir string) string {
	out, err := git(ctx, dir, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func refExists(ctx context.Context, git Git, dir, ref string) bool {
	_, err := git(ctx, dir, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	return err == nil
}
