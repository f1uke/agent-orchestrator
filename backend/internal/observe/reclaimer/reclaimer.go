// Package reclaimer is the OBSERVE-layer poll loop that auto-reclaims finished
// worker sessions (tear down tmux + worktree, keep branch) once they have sat
// in a merged/terminated state past the configured grace period.
//
// It deletes without asking, so every decision it makes is written to a durable
// log (internal/reclaimlog) — reclaims AND refusals. The log is what makes a
// silent policy recoverable: the branch is never deleted, so a log line naming
// the branch is a complete recovery instruction.
package reclaimer

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/observe"
	"github.com/aoagents/agent-orchestrator/backend/internal/reclaimlog"
	"github.com/aoagents/agent-orchestrator/backend/internal/reclaimsettings"
	sessionsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/session"
)

// DefaultTickInterval is the poll cadence. Grace is in minutes, so a slow tick
// is fine.
const DefaultTickInterval = time.Minute

type reclaimService interface {
	ListReclaimable(ctx context.Context) ([]sessionsvc.ReclaimCandidate, error)
	Reclaim(ctx context.Context, id domain.SessionID) (sessionsvc.ReclaimOutcome, error)
}

type settingsReader interface {
	Get() reclaimsettings.Settings
}

// auditLog is the durable record of what the loop did.
type auditLog interface {
	Append(e reclaimlog.Entry) error
}

// Config holds optional knobs; zero values fall back to safe defaults.
type Config struct {
	Tick   time.Duration
	Clock  func() time.Time
	Logger *slog.Logger
	// Audit receives every reclaim and every refusal. Nil disables the durable
	// log (tests); the daemon always supplies one.
	Audit auditLog
	// SelfPath is the directory this process is executing in. Any candidate
	// whose workspace contains it is never reclaimed — see selfGuard.
	SelfPath string
	// WorktreeRoot is the managed worktree directory scanned for orphans. Empty
	// disables the orphan sweep.
	WorktreeRoot string
	// Paths supplies the owned-workspace set for the orphan sweep. Nil disables it.
	Paths workspacePathLister
	// OnTick, when non-nil, fires once before each poll cycle; the daemon's
	// loop-timing seam (see internal/looptelemetry).
	OnTick func()
}

// Skip reasons the loop itself decides (teardown-level reasons come up from the
// session manager).
const (
	// reasonSelf: the sweep is running inside this very worktree.
	reasonSelf = "self_worktree"
)

// Reclaimer holds the grace clock and the set of sessions already reclaimed, so
// a stale candidate (ListReclaimable keeps listing it because teardown never
// clears WorkspacePath — and must not, Restore needs it) is not reclaimed again
// on every subsequent tick.
type Reclaimer struct {
	svc       reclaimService
	settings  settingsReader
	firstSeen map[domain.SessionID]time.Time
	reclaimed map[domain.SessionID]bool
	// lastSkip remembers the reason a candidate was last refused, so a
	// permanently dirty worktree produces one log line per reason rather than
	// one per tick forever.
	lastSkip map[domain.SessionID]string
	// orphanSeen dedupes orphan log lines by path+reason for the process's life.
	orphanSeen   map[string]bool
	tick         time.Duration
	clock        func() time.Time
	logger       *slog.Logger
	audit        auditLog
	selfPath     string
	worktreeRoot string
	paths        workspacePathLister
	onTick       func()
}

// workspacePathLister reports every workspace path a session record claims, so
// the orphan sweep can tell an unowned directory from an owned one.
type workspacePathLister interface {
	ListKnownWorkspacePaths(ctx context.Context) ([]string, error)
}

// New constructs a Reclaimer.
func New(svc reclaimService, settings settingsReader, cfg Config) *Reclaimer {
	r := &Reclaimer{
		svc:          svc,
		settings:     settings,
		firstSeen:    map[domain.SessionID]time.Time{},
		reclaimed:    map[domain.SessionID]bool{},
		lastSkip:     map[domain.SessionID]string{},
		tick:         cfg.Tick,
		clock:        cfg.Clock,
		logger:       cfg.Logger,
		audit:        cfg.Audit,
		selfPath:     cfg.SelfPath,
		worktreeRoot: cfg.WorktreeRoot,
		paths:        cfg.Paths,
		orphanSeen:   map[string]bool{},
		onTick:       cfg.OnTick,
	}
	if r.tick <= 0 {
		r.tick = DefaultTickInterval
	}
	if r.clock == nil {
		r.clock = time.Now
	}
	if r.logger == nil {
		r.logger = slog.Default()
	}
	if r.selfPath == "" {
		// Default to the process's own working directory. A sweep launched from
		// inside a worktree that otherwise qualifies must not delete the ground
		// it is standing on.
		if wd, err := os.Getwd(); err == nil {
			r.selfPath = wd
		}
	}
	return r
}

// Start runs the loop until ctx is cancelled; the returned channel closes when
// the loop exits. Mirrors the reaper's shutdown contract.
func (r *Reclaimer) Start(ctx context.Context) <-chan struct{} {
	return observe.StartPollLoop(ctx, r.tick, r.Tick, r.logger, "reclaimer", r.onTick)
}

// Tick runs one grace-clock pass. Disabled settings make it a no-op.
func (r *Reclaimer) Tick(ctx context.Context) error {
	set := r.settings.Get()
	if !set.Enabled {
		return nil
	}
	now := r.clock()
	grace := time.Duration(set.GraceMinutes) * time.Minute

	candidates, err := r.svc.ListReclaimable(ctx)
	if err != nil {
		return err
	}
	r.forgetDeparted(candidates)

	for _, c := range candidates {
		if r.reclaimed[c.ID] {
			continue
		}
		if r.selfGuard(c) {
			r.noteSkip(c, reasonSelf, now)
			continue
		}
		if !r.pastGrace(c, now, grace) {
			continue
		}
		r.attempt(ctx, c, now)
	}
	r.sweepOrphans(ctx)
	return nil
}

// forgetDeparted drops clock entries, reclaim marks and skip reasons for
// sessions that are no longer candidates, so one that leaves candidacy (e.g. is
// restored) and comes back starts its grace afresh and is reclaimable again.
func (r *Reclaimer) forgetDeparted(candidates []sessionsvc.ReclaimCandidate) {
	current := make(map[domain.SessionID]bool, len(candidates))
	for _, c := range candidates {
		current[c.ID] = true
	}
	for id := range r.firstSeen {
		if !current[id] {
			delete(r.firstSeen, id)
		}
	}
	for id := range r.reclaimed {
		if !current[id] {
			delete(r.reclaimed, id)
		}
	}
	for id := range r.lastSkip {
		if !current[id] {
			delete(r.lastSkip, id)
		}
	}
}

// pastGrace reports whether the candidate has waited out the grace period.
//
// Two clocks must BOTH elapse. The record's own Since timestamp is durable, so
// a machine whose daemon restarts more often than the grace period still
// reclaims eventually. The in-memory first-seen stamp is the debounce for a
// session that has only just become a candidate on this daemon, so a restart
// can never reclaim something the instant it boots.
func (r *Reclaimer) pastGrace(c sessionsvc.ReclaimCandidate, now time.Time, grace time.Duration) bool {
	if !c.Since.IsZero() && now.Sub(c.Since) < grace {
		return false
	}
	seen, ok := r.firstSeen[c.ID]
	if !ok {
		r.firstSeen[c.ID] = now
		return false
	}
	return now.Sub(seen) >= grace
}

// selfGuard reports whether reclaiming this candidate would delete the ground
// the running process is standing on.
//
// The sweep runs inside AO, so its own worktree can match its own criteria.
// The check is by containment, not by name: a name comparison is exactly the
// mistake this feature has to avoid, because a worktree directory is fixed at
// spawn and never renamed when its branch is.
func (r *Reclaimer) selfGuard(c sessionsvc.ReclaimCandidate) bool {
	return pathContains(c.WorkspacePath, r.selfPath)
}

// pathContains reports whether inner is root or lives beneath it. Both sides
// are resolved through EvalSymlinks where possible so a symlinked temp dir (as
// macOS gives for /tmp) compares equal to its real path.
func pathContains(root, inner string) bool {
	if root == "" || inner == "" {
		return false
	}
	root = resolvePath(root)
	inner = resolvePath(inner)
	if root == inner {
		return true
	}
	rel, err := filepath.Rel(root, inner)
	if err != nil {
		return false
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return !filepath.IsAbs(rel)
}

func resolvePath(p string) string {
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return filepath.Clean(resolved)
	}
	return filepath.Clean(p)
}

// attempt runs one teardown and records the truth about what happened.
func (r *Reclaimer) attempt(ctx context.Context, c sessionsvc.ReclaimCandidate, now time.Time) {
	out, err := r.svc.Reclaim(ctx, c.ID)
	if err != nil {
		r.logger.Error("reclaimer: reclaim failed", "session", c.ID, "err", err)
		r.noteSkip(c, "error: "+err.Error(), now)
		// Re-arm the debounce so a failing session is retried on the next grace
		// period rather than on every tick.
		r.firstSeen[c.ID] = now
		return
	}
	if out.Reason == sessionsvc.ReasonAlreadyGone {
		// Nothing was there to reclaim (an earlier daemon already did it, and
		// the record keeps its WorkspacePath so Restore can work). Stop tracking
		// it and write nothing: the audit log records events, not non-events.
		delete(r.firstSeen, c.ID)
		r.reclaimed[c.ID] = true
		return
	}
	if !out.Freed {
		// The workspace survived — typically because it holds uncommitted work.
		// This is a REFUSAL, not a success: do not mark it reclaimed, so the
		// next pass tries again once the user has dealt with the changes.
		r.noteSkip(c, skipReason(out.Reason), now)
		r.firstSeen[c.ID] = now
		return
	}
	r.logger.Info("reclaimer: reclaimed finished session",
		"session", c.ID, "path", out.WorkspacePath, "bytes", out.BytesFreed)
	r.write(reclaimlog.Entry{
		At:            now.UTC(),
		Action:        reclaimlog.ActionReclaimed,
		SessionID:     string(c.ID),
		ProjectID:     c.ProjectID,
		Branch:        c.Branch,
		WorkspacePath: out.WorkspacePath,
		Qualified:     c.Status,
		AgeMinutes:    ageMinutes(c.Since, now),
		BytesFreed:    out.BytesFreed,
	})
	delete(r.firstSeen, c.ID)
	delete(r.lastSkip, c.ID)
	r.reclaimed[c.ID] = true
}

// noteSkip records a refusal once per distinct reason, so a permanently
// un-reclaimable worktree does not append a line every tick forever.
func (r *Reclaimer) noteSkip(c sessionsvc.ReclaimCandidate, reason string, now time.Time) {
	if r.lastSkip[c.ID] == reason {
		return
	}
	r.lastSkip[c.ID] = reason
	r.logger.Info("reclaimer: kept workspace", "session", c.ID, "reason", reason)
	r.write(reclaimlog.Entry{
		At:            now.UTC(),
		Action:        reclaimlog.ActionSkipped,
		SessionID:     string(c.ID),
		ProjectID:     c.ProjectID,
		Branch:        c.Branch,
		WorkspacePath: c.WorkspacePath,
		Qualified:     c.Status,
		AgeMinutes:    ageMinutes(c.Since, now),
		Reason:        reason,
	})
}

func (r *Reclaimer) write(e reclaimlog.Entry) {
	if r.audit == nil {
		return
	}
	if err := r.audit.Append(e); err != nil {
		r.logger.Warn("reclaimer: audit log write failed", "err", err)
	}
}

func skipReason(reason string) string {
	if reason == "" {
		return "workspace_preserved"
	}
	return reason
}

func ageMinutes(since, now time.Time) int64 {
	if since.IsZero() {
		return 0
	}
	return int64(now.Sub(since) / time.Minute)
}
