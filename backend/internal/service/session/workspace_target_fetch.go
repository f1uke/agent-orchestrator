package session

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"sync"
	"time"
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
}

// status describes what the caller is about to diff against.
//
// A known failure outranks an in-flight retry: while the retry runs, the refs
// on disk are still the ones nobody could refresh, and saying "refreshing"
// would hide the staleness behind a spinner for as long as the remote stays
// down — which is precisely when the user most needs to see it.
func (e *targetFetchEntry) status() (string, string) {
	switch {
	case e.settled && !e.ok:
		return TargetFetchFailed, e.lastErr
	case e.inFlight:
		return TargetFetchRefreshing, ""
	case e.settled:
		return TargetFetchCurrent, ""
	default:
		return TargetFetchRefreshing, ""
	}
}

// refreshTarget starts a background refresh of branch's remote-tracking ref when
// the last one has aged out, and reports the freshness of what the caller is
// about to diff against.
//
// It never blocks on the network: the caller always proceeds with the refs
// already on disk. A repository with no remote gets no fetch and no freshness
// signal — it has nothing to be behind.
func (s *Service) refreshTarget(ctx context.Context, workspace, branch string) (status, errMsg string) {
	branch = strings.TrimSpace(branch)
	if branch == "" || !hasOriginRemote(ctx, workspace) {
		return "", ""
	}
	// The common git dir is shared by every worktree of one repository, which
	// makes it the identity of the thing being fetched.
	repo := gitCommonDir(ctx, workspace)
	if repo == "" {
		repo = workspace
	}
	return s.targetFetch.refresh(ctx, repo, workspace, branch, time.Now())
}

func (f *targetFetcher) refresh(ctx context.Context, repo, workspace, branch string, now time.Time) (status, errMsg string) {
	key := repo + "\x00" + branch

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
	// and the previous attempt has aged out (that is the throttle).
	start := !entry.inFlight && (entry.lastAttempt.IsZero() || now.Sub(entry.lastAttempt) >= targetFetchTTL)
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
			_, err := gitOutput(fetchCtx, workspace, fetchTargetArgs(branch)...)

			f.mu.Lock()
			defer f.mu.Unlock()
			entry.inFlight, entry.settled = false, true
			entry.ok = err == nil
			if err != nil {
				entry.lastErr = fetchErrorMessage(err)
				return
			}
			entry.lastErr = ""
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
// write. The refspec is explicit so the fetch stays narrow (one ref, not every
// branch on the forge) and lands exactly where resolveBranchRef looks. Mirrors
// gitworktree's fetchBaseArgs, which is the same operation for the sync path.
func fetchTargetArgs(branch string) []string {
	return []string{
		"fetch", "--quiet", "--no-tags", "--no-write-fetch-head", "origin",
		"+refs/heads/" + branch + ":refs/remotes/origin/" + branch,
	}
}

// hasOriginRemote reports whether there is an origin to fetch from at all. A
// remoteless repo must not be labelled "could not refresh": there is nothing it
// could be behind.
func hasOriginRemote(ctx context.Context, workspace string) bool {
	out, err := gitOutput(ctx, workspace, "remote", "get-url", "origin")
	return err == nil && strings.TrimSpace(string(out)) != ""
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
