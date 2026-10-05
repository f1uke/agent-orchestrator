package session

import (
	"context"
	"errors"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Freshness of the remote-tracking ref the Changes diff was measured against.
//
// The point of reporting it at all is that a diff computed from refs nobody has
// refreshed is not obviously different from a correct one — it is a confidently
// wrong answer. The user must be able to tell "this is current" from "this
// could not be refreshed".
const (
	// TargetFetchCurrent — the target branch was refreshed from the remote
	// within targetFetchTTL, so this diff reflects the branch's current state.
	TargetFetchCurrent = "current"
	// TargetFetchRefreshing — a refresh is in flight. The diff was computed from
	// the refs already on disk and may move when the fetch lands.
	TargetFetchRefreshing = "refreshing"
	// TargetFetchFailed — the last refresh failed (offline, auth, or the branch
	// is gone from the remote). The diff is the best answer available from known
	// refs, and may be out of date.
	TargetFetchFailed = "failed"
)

// targetFetchTTL is how long a successful refresh keeps the target branch
// "current". The Changes panel refetches on every mount and window focus, so
// without a throttle a user tabbing between windows would fire a fetch per
// keystroke-scale event. Thirty seconds is short enough that opening the panel
// to look at a diff almost always refreshes, and long enough that browsing
// files inside one sitting costs one fetch.
const targetFetchTTL = 30 * time.Second

// targetFetchTimeout bounds a single background fetch, so an unreachable remote
// that never answers cannot pin the entry in-flight forever.
const targetFetchTimeout = 30 * time.Second

// goFetch runs the background refresh. Overridable in tests, where a goroutine
// racing the assertion would make the outcome a coin flip.
var goFetch = func(fn func()) { go fn() }

// targetFetcher throttles and de-duplicates the read-only refresh of a session's
// target branch.
//
// It is keyed on the REPOSITORY, not the session: many sessions are worktrees of
// one underlying repo, and they all want the same `refs/remotes/origin/<branch>`
// refreshed. Keying per session would multiply one useful fetch by the number of
// open sessions, and let two of them race on the same ref.
//
// The zero value is ready to use.
type targetFetcher struct {
	mu    sync.Mutex
	state map[string]*targetFetchEntry
}

type targetFetchEntry struct {
	inFlight bool
	// lastAttempt is when the most recent attempt STARTED, whatever its outcome.
	// The throttle keys off this rather than off the last success on purpose: a
	// remote that is down fails fast, so throttling only successes would turn an
	// outage into a fetch on every single panel load — the storm this type
	// exists to prevent, arriving exactly when the network is least able to take
	// it.
	lastAttempt time.Time
	// settled records that at least one attempt has finished, so a first-ever
	// fetch still in flight is not mistaken for a completed one.
	settled bool
	ok      bool
	lastErr string
	// lastSuccess is when a fetch last SUCCEEDED, which is what "fetched 2m
	// ago" means to the reader. It survives a later failure on purpose: a
	// failed refresh leaves the ref exactly as fresh as that success made it.
	lastSuccess time.Time
}

// targetFreshness is what the caller reports about the ref it diffs against.
type targetFreshness struct {
	// Status is one of the TargetFetch* constants; empty when there is no
	// remote to fetch from. Error carries the reason for TargetFetchFailed.
	Status, Error string
	// FetchedAt is when the ref was last refreshed by this daemon; zero when it
	// has not been yet.
	FetchedAt time.Time
	// InFlight reports a fetch running right now, whatever Status says. A known
	// failure keeps Status at TargetFetchFailed while a retry runs (see
	// status), so without this the reader could not tell that an answer is on
	// its way - and would not look again until long after it landed.
	InFlight bool
}

// status describes what the caller is about to diff against.
//
// A known failure outranks an in-flight retry: while the retry runs, the refs
// on disk are still the ones nobody could refresh, and saying "refreshing"
// would hide the staleness behind a spinner for as long as the remote stays
// down — which is precisely when the user most needs to see it.
func (e *targetFetchEntry) status() targetFreshness {
	fr := targetFreshness{FetchedAt: e.lastSuccess, InFlight: e.inFlight}
	switch {
	case e.settled && !e.ok:
		fr.Status, fr.Error = TargetFetchFailed, e.lastErr
	case e.inFlight:
		fr.Status = TargetFetchRefreshing
	case e.settled:
		fr.Status = TargetFetchCurrent
	default:
		fr.Status = TargetFetchRefreshing
	}
	return fr
}

// refreshTarget starts a background refresh of the target's remote-tracking ref
// when the last one has aged out (or at once when force is set, which is the
// panel's refresh button), and reports the freshness of what the caller is
// about to diff against.
//
// It never blocks on the network: the caller always proceeds with the refs
// already on disk. A target with no remote gets no fetch and no freshness
// signal - it has nothing to be behind.
func (s *Service) refreshTarget(ctx context.Context, workspace string, loc targetLocation, force bool) targetFreshness {
	if loc.Remote == "" || loc.Branch == "" {
		return targetFreshness{}
	}
	// The common git dir is shared by every worktree of one repository, which
	// makes it the identity of the thing being fetched.
	repo := gitCommonDir(ctx, workspace)
	if repo == "" {
		repo = workspace
	}
	return s.targetFetch.refresh(ctx, repo, workspace, loc, force, time.Now())
}

func (f *targetFetcher) refresh(
	ctx context.Context, repo, workspace string, loc targetLocation, force bool, now time.Time,
) targetFreshness {
	key := repo + "\x00" + loc.Remote + "\x00" + loc.Branch

	f.mu.Lock()
	if f.state == nil {
		f.state = map[string]*targetFetchEntry{}
	}
	entry, ok := f.state[key]
	if !ok {
		entry = &targetFetchEntry{}
		f.state[key] = entry
	}
	// Start an attempt only when nothing is already running for this ref (that is
	// the single-flight: many sessions share one repo and all want the same ref)
	// and the previous attempt has aged out (that is the throttle). A forced
	// refresh skips only the throttle: the human asked for it, but two clicks
	// still share one fetch.
	aged := entry.lastAttempt.IsZero() || now.Sub(entry.lastAttempt) >= targetFetchTTL
	start := !entry.inFlight && (force || aged)
	if start {
		entry.inFlight, entry.lastAttempt = true, now
	}
	f.mu.Unlock()

	if start {
		// Deliberately detached from the request context: the HTTP handler returns
		// immediately (that is the point), and a fetch cancelled the moment the
		// response is written would never finish, leaving the panel permanently
		// "refreshing" and permanently stale.
		fetchCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), targetFetchTimeout)
		goFetch(func() {
			defer cancel()
			_, err := gitOutput(fetchCtx, workspace, fetchTargetArgs(loc)...)

			f.mu.Lock()
			defer f.mu.Unlock()
			entry.inFlight, entry.settled = false, true
			entry.ok = err == nil
			if err != nil {
				entry.lastErr = fetchErrorMessage(err)
				return
			}
			entry.lastErr = ""
			entry.lastSuccess = time.Now()
		})
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	return entry.status()
}

// fetchTargetArgs refreshes ONE branch's remote-tracking ref and nothing else.
//
// This is the ceiling on what this feature may do to the user's repository:
// refs only, never the working tree, never a checkout, pull, merge or branch
// write - every worktree shares the repo's refs, and the human's own checkout
// may have the target checked out and dirty. The refspec is explicit so the
// fetch stays narrow (one ref, not every branch on the forge) and lands exactly
// where resolveBranchRef looks. Mirrors gitworktree's fetchBaseArgs, which is
// the same operation for the sync path.
func fetchTargetArgs(loc targetLocation) []string {
	return []string{
		"fetch", "--quiet", "--no-tags", "--no-write-fetch-head", loc.Remote,
		"+refs/heads/" + loc.Branch + ":" + remoteTrackingRef(loc),
	}
}

// targetLocation is where a target branch lives: which remote it is fetched
// from, and its name there.
type targetLocation struct {
	// Remote is the git remote the target is fetched from; "" when the
	// repository has no remote this target can be attributed to, in which case
	// only the local branch is compared and nothing is fetched.
	Remote string
	// Branch is the branch's name on Remote. A remote-qualified target
	// ("origin/feature/x") has its remote prefix stripped here.
	Branch string
}

// remoteTrackingRef is where loc's branch lands when fetched.
func remoteTrackingRef(loc targetLocation) string {
	return "refs/remotes/" + loc.Remote + "/" + loc.Branch
}

// locateTarget decides which remote a target branch is fetched from.
//
// "origin" is a convention, not a fact: a repository cloned under another name,
// or one carrying several projects' remotes side by side, has no origin at all,
// and hardcoding it meant such a project was never fetched and silently
// compared against the human's local checkout of the target - which only moves
// when they pull, so every commit merged since was billed to the session. In
// order:
//
//  1. a remote-qualified target ("origin/feature/x", "upstream/main") names its
//     remote, unless a local branch has that exact name;
//  2. the remote whose URL is the project's own repository - the forge the PR
//     lives on, and the only safe pick when remotes point at different projects;
//  3. the remote the local target branch tracks (branch.<target>.remote);
//  4. origin;
//  5. the sole remote.
//
// Anything else is no remote: the local ref is compared and nothing is fetched,
// rather than guessing between remotes that point at different repositories.
func (s *Service) locateTarget(ctx context.Context, rec domain.SessionRecord, workspace, target string) targetLocation {
	target = strings.TrimSpace(target)
	remotes := gitRemoteNames(ctx, workspace)
	if target == "" || len(remotes) == 0 {
		return targetLocation{Branch: target}
	}
	if _, isLocal := resolveLocalBranchRef(ctx, workspace, target); !isLocal {
		for _, r := range remotes {
			if rest, ok := strings.CutPrefix(target, r+"/"); ok && rest != "" {
				return targetLocation{Remote: r, Branch: rest}
			}
		}
	}
	if repoURL := s.projectRepoURL(ctx, rec); repoURL != "" {
		for _, r := range remotes {
			if out, err := gitOutput(ctx, workspace, "remote", "get-url", r); err == nil &&
				sameRepoURL(strings.TrimSpace(string(out)), repoURL) {
				return targetLocation{Remote: r, Branch: target}
			}
		}
	}
	if out, err := gitOutput(ctx, workspace, "config", "--get", "branch."+target+".remote"); err == nil {
		if r := strings.TrimSpace(string(out)); slices.Contains(remotes, r) {
			return targetLocation{Remote: r, Branch: target}
		}
	}
	if slices.Contains(remotes, "origin") {
		return targetLocation{Remote: "origin", Branch: target}
	}
	if len(remotes) == 1 {
		return targetLocation{Remote: remotes[0], Branch: target}
	}
	return targetLocation{Branch: target}
}

// projectRepoURL is the repository URL the session's project was registered
// with, or "" when it has none on record.
func (s *Service) projectRepoURL(ctx context.Context, rec domain.SessionRecord) string {
	proj, ok, err := s.store.GetProject(ctx, string(rec.ProjectID))
	if err != nil || !ok {
		return ""
	}
	return strings.TrimSpace(proj.RepoOriginURL)
}

// gitRemoteNames lists the repository's configured remotes.
func gitRemoteNames(ctx context.Context, workspace string) []string {
	out, err := gitOutput(ctx, workspace, "remote")
	if err != nil {
		return nil
	}
	return strings.Fields(string(out))
}

// sameRepoURL reports whether two remote URLs name the same repository,
// whatever their spelling: SSH or HTTPS, with or without ".git", a trailing
// slash, or a different host case.
func sameRepoURL(a, b string) bool {
	ah, ap, aerr := gitlabRepoFromURL(a)
	bh, bp, berr := gitlabRepoFromURL(b)
	if aerr != nil || berr != nil {
		return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
	}
	return strings.EqualFold(ah, bh) && strings.EqualFold(ap, bp)
}

// gitCommonDir returns the directory shared by every worktree of a repository,
// which is what makes two sessions on the same repo collapse to one fetch.
func gitCommonDir(ctx context.Context, workspace string) string {
	out, err := gitOutput(ctx, workspace, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// fetchErrorMessage turns a git failure into one short line for the UI. git
// writes the useful part ("could not read Username", "Repository not found") to
// stderr, which ExitError carries; the bare "exit status 128" alone would tell
// the user nothing about whether they are offline or unauthenticated.
func fetchErrorMessage(err error) string {
	msg := ""
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		for _, line := range strings.Split(string(exitErr.Stderr), "\n") {
			if line = strings.TrimSpace(line); line != "" {
				msg = line
				break
			}
		}
	}
	if msg == "" {
		msg = strings.TrimSpace(err.Error())
	}
	if msg == "" {
		return "fetch failed"
	}
	const maxLen = 200
	if len(msg) > maxLen {
		msg = msg[:maxLen]
	}
	return msg
}
