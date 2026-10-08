// Package branchfollow keeps each live session's recorded branch on the branch
// its worktree is on after a rename there (`git branch -m`), so every feature
// keyed by the session's branch - PR discovery, Files, review, restore - uses
// the new name without a restart. The rules for when a move is a rename live in
// the session service (FollowWorktreeBranch); this is only the loop.
package branchfollow

import (
	"context"
	"log/slog"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/observe"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/session"
)

// DefaultTickInterval runs well inside the SCM observer's 30s tick, so a branch
// renamed between two PR polls is followed before the second one reads it.
const DefaultTickInterval = 10 * time.Second

type sessionSource interface {
	ListAllSessions(ctx context.Context) ([]domain.SessionRecord, error)
}

type follower interface {
	FollowWorktreeBranch(ctx context.Context, rec domain.SessionRecord) (session.BranchFollow, error)
}

// Config tunes the loop. Zero values use the defaults.
type Config struct {
	Tick   time.Duration
	Logger *slog.Logger
	OnTick func()
}

// Observer is the follow loop.
type Observer struct {
	sessions sessionSource
	follow   follower
	tick     time.Duration
	logger   *slog.Logger
	onTick   func()
	// reported is the last outcome logged per session, so a branch left alone
	// for a reason (another session owns it) is logged once, not every tick.
	reported map[domain.SessionID]session.BranchFollow
}

// New builds the loop over the session list and the service that follows.
func New(sessions sessionSource, follow follower, cfg Config) *Observer {
	o := &Observer{sessions: sessions, follow: follow, tick: cfg.Tick, logger: cfg.Logger, onTick: cfg.OnTick, reported: map[domain.SessionID]session.BranchFollow{}}
	if o.tick <= 0 {
		o.tick = DefaultTickInterval
	}
	if o.logger == nil {
		o.logger = slog.Default()
	}
	return o
}

// Start runs the loop until ctx is cancelled; the channel closes when it exits.
func (o *Observer) Start(ctx context.Context) <-chan struct{} {
	return observe.StartPollLoop(ctx, o.tick, o.Poll, o.logger, "branch-follow observer", o.onTick)
}

// Poll compares every live session's recorded branch with its worktree once.
func (o *Observer) Poll(ctx context.Context) error {
	recs, err := o.sessions.ListAllSessions(ctx)
	if err != nil {
		return err
	}
	for _, rec := range recs {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if rec.IsTerminated || rec.IsTodo {
			continue
		}
		res, err := o.follow.FollowWorktreeBranch(ctx, rec)
		if err != nil {
			o.logger.Warn("branch-follow observer: compare failed", "session", rec.ID, "err", err)
			continue
		}
		o.report(rec.ID, res)
	}
	return nil
}

func (o *Observer) report(id domain.SessionID, res session.BranchFollow) {
	switch res.Outcome {
	case session.BranchFollowed:
		o.logger.Info("branch-follow observer: session follows its renamed branch", "session", id, "from", res.Recorded, "to", res.Head)
		delete(o.reported, id)
	case session.BranchLeftOwned:
		if o.reported[id] == res {
			return
		}
		o.reported[id] = res
		o.logger.Warn("branch-follow observer: worktree is on another live session's branch; not following",
			"session", id, "recorded", res.Recorded, "head", res.Head, "owner", res.Owner)
	default:
		delete(o.reported, id)
	}
}
