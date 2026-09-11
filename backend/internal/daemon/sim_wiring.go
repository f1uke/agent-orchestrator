package daemon

import (
	"context"
	"log/slog"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	simsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/sim"
	"github.com/aoagents/agent-orchestrator/backend/internal/simvideo"
)

// newSimService builds the simulator lease service the daemon mounts at
// httpd APIDeps.Sim, WITH gesture recording turned on.
//
// The recorder is not optional in the daemon, only in the package: `ao sim
// record` is nothing without it - `record start` succeeds, `status` reports
// zero steps for ever and `stop` writes a header-only flow - because
// AcquireHold/ReleaseHold only resolve and keep a step when a ScreenReader is
// wired (see internal/service/sim/hold.go). Every test in that package injects
// its own recorder, so nothing there can notice this missing; the wiring test
// next to this file is what does.
//
// screen is the daemon's one resident simulator screen (internal/simstream),
// the same object the Device tab's gestures already go through. Reusing it is
// deliberate: its bridge is built on first use and kept, so recording a
// gesture reads the tree through the process that is already touching the
// device rather than starting a second one.
//
// crew is the "this task drove the app" observer: a granted lease records that
// fact on the session's row, and the daemon is the only place that knows both
// halves. Wired here rather than in the controller so a take-over counts exactly
// as a claim does. It creates nobody - dev asks for its own qa with
// `ao crew review`; the fact is what the unreviewed-work warning reads.
func newSimService(store simsvc.Store, screen simsvc.ScreenReader, crew simsvc.RuntimeWatcher) *simsvc.Service {
	return simsvc.New(store, simsvc.WithRecorder(screen), simsvc.WithRuntimeWatcher(crew))
}

// newSimVideoRecorder builds the screen recorder behind `ao sim record`, and
// teaches it the one question it cannot answer for itself: whether the session
// that owns an open recording is still running.
//
// Asking the store for the session is what makes "a recording never outlives
// its session" true for EVERY path that ends one - terminate, purge, replace,
// restart, the crew fan-out - because all of them land on the same
// is_terminated bit. A set of hooks on those paths would have to be extended by
// whoever adds the next one, and would fail silently when they did not.
//
// A session that cannot be READ answers live: a failed probe is not proof a
// session is dead (the hard rule in AGENTS.md), and acting on one would delete
// the recording somebody asked for. The duration cap bounds the other side of
// that choice.
//
// Constructing it sweeps orphaned recorders left by a previous daemon, which is
// why it is built during startup rather than lazily.
func newSimVideoRecorder(dataDir string, store simsvc.Store, log *slog.Logger) *simvideo.Recorder {
	return simvideo.New(dataDir,
		simvideo.WithLogger(log),
		simvideo.WithSessionLiveness(func(ctx context.Context, id domain.SessionID) bool {
			rec, found, err := store.GetSession(ctx, id)
			if err != nil {
				return true
			}
			return found && !rec.IsTerminated
		}),
	)
}
