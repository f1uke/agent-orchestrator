package daemon

import (
	simsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/sim"
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
// crew is the "this task has a runtime surface" observer: a granted lease is what
// creates a task's qa (design §1.12.1), and the daemon is the only place that
// knows both halves. Wired here rather than in the controller so a take-over
// counts exactly as a claim does.
func newSimService(store simsvc.Store, screen simsvc.ScreenReader, crew simsvc.CrewJoiner) *simsvc.Service {
	return simsvc.New(store, simsvc.WithRecorder(screen), simsvc.WithCrewJoiner(crew))
}
