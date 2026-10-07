package lifecycle

import (
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const defaultRecentActivityWindow = 60 * time.Second

func hasRecentActivity(a domain.Activity, now time.Time, window time.Duration) bool {
	switch {
	case a.State == domain.ActivityExited:
		return false
	case a.State.IsSticky():
		return true
	case a.LastActivityAt.IsZero():
		return false
	default:
		return now.Sub(a.LastActivityAt) <= window
	}
}

func runtimeClearlyDead(f ports.RuntimeFacts, activity domain.Activity, now time.Time, window time.Duration) bool {
	observedAt := timeOr(f.ObservedAt, now)
	return f.Probe == ports.ProbeDead && !hasRecentActivity(activity, observedAt, window)
}

// backgroundWorkDied reports whether a session waiting on its own background
// work has lost the agent that would resume it, while its pane lives on behind
// the keep-alive shell (a dead pane is runtimeClearlyDead's to handle).
func backgroundWorkDied(f ports.RuntimeFacts, activity domain.Activity) bool {
	return activity.State == domain.ActivityBackground && f.Probe == ports.ProbeAlive && f.Agent == ports.ProbeDead
}

func timeOr(t, fallback time.Time) time.Time {
	if t.IsZero() {
		return fallback
	}
	return t
}
