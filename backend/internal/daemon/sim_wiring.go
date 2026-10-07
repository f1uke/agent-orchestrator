package daemon

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/cdc"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	iosrunsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/iosrun"
	simsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/sim"
	"github.com/aoagents/agent-orchestrator/backend/internal/simbridge"
	"github.com/aoagents/agent-orchestrator/backend/internal/simctl"
	"github.com/aoagents/agent-orchestrator/backend/internal/simowner"
	"github.com/aoagents/agent-orchestrator/backend/internal/simrunner"
	"github.com/aoagents/agent-orchestrator/backend/internal/simstream"
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
//
// extra carries the rest of the daemon's wiring, such as the nudge that tells
// the XCTest runners a lease changed.
func newSimService(store simsvc.Store, screen simsvc.ScreenReader, crew simsvc.RuntimeWatcher, extra ...simsvc.Option) *simsvc.Service {
	opts := append([]simsvc.Option{simsvc.WithRecorder(screen), simsvc.WithRuntimeWatcher(crew)}, extra...)
	return simsvc.New(store, opts...)
}

// simRunnerLeases is what the runner manager reconciles against: the devices
// some session holds right now, read from the lease table itself.
type simRunnerLeases interface {
	ListSimLeases(ctx context.Context, now time.Time) ([]domain.SimLease, error)
}

// simRunnerDevices is the daemon's resident device listing.
type simRunnerDevices interface {
	Devices(ctx context.Context) (simctl.Listing, error)
}

// newSimRunner builds the warm XCTest readers behind `ao sim ax`, or nil on a
// machine that cannot run one (not macOS, or no Xcode). Constructing it reaps
// any runner a previous daemon left, which is why it is built at startup.
//
// The booted check goes through the resident listing, so a runner start pays
// a cache read rather than a `simctl list`; a device the listing does not know
// is not booted as far as the runner is concerned, because starting
// `xcodebuild test` against it would boot it.
func newSimRunner(dataDir string, leases simRunnerLeases, devices simRunnerDevices, log *slog.Logger) *simrunner.Manager {
	if goruntime.GOOS != "darwin" {
		return nil
	}
	if _, err := exec.LookPath("xcodebuild"); err != nil {
		log.Info("simrunner: xcodebuild is not on PATH; `ao sim ax` reads through the accessibility bridge only")
		return nil
	}
	held := func(ctx context.Context) ([]string, error) {
		list, err := leases.ListSimLeases(ctx, time.Now().UTC())
		if err != nil {
			return nil, err
		}
		udids := make([]string, 0, len(list))
		for _, lease := range list {
			udids = append(udids, lease.UDID)
		}
		return udids, nil
	}
	booted := func(ctx context.Context, udid string) (bool, error) {
		listing, err := devices.Devices(ctx)
		if err != nil {
			return false, err
		}
		for _, d := range listing.Devices {
			if domain.NormalizeSimUDID(d.UDID) == domain.NormalizeSimUDID(udid) {
				return d.Booted(), nil
			}
		}
		return false, nil
	}
	return simrunner.New(dataDir, held, booted, simrunner.WithLogger(log))
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

// newIOSRunService builds the run bar's service: what a session can build, and
// the pane `ao sim run` runs in.
//
// The binary is resolved to THIS daemon's own executable rather than left to
// the pane's PATH, for the reason the session manager and the reviewer launcher
// both pin theirs: a machine can have more than one `ao`, and a run bar in one
// build of AO starting another build's CLI would take a lease through a
// different daemon than the one that rendered the button. An executable that
// cannot be resolved, or is not named `ao`, falls back to the bare name - the
// pane then behaves exactly as an agent typing the command would.
//
// gatedRuntime is deliberately NOT used: the input gate exists to record and
// pace messages TYPED INTO an agent, and a build pane has no agent in it.
// dataDir is where a run's record and its verdict are kept, so the bar can
// still say how a build went to somebody who pressed Run, walked away and came
// back - across a daemon restart included. Under ~/.ao like all app state.
func newIOSRunService(dataDir string, store iosrunsvc.Sessions, runtime iosrunsvc.Runtime) *iosrunsvc.Service {
	return iosrunsvc.New(store, runtime, aoBinaryPath(), iosrunsvc.WithStateDir(dataDir))
}

// aoBinaryPath is this daemon's own `ao`, or the bare name when it cannot be
// resolved or is not `ao` (a test binary, a renamed build).
func aoBinaryPath() string {
	exe, err := os.Executable()
	if err != nil {
		return "ao"
	}
	if name := filepath.Base(exe); name != "ao" && name != "ao.exe" {
		return "ao"
	}
	return exe
}

// simRunnerAXReader adapts the runners to the screen surface's reader: a
// ready runner's read, or false so the bridge answers. It never waits for a
// runner that is starting - these reads sit inside a gesture.
func simRunnerAXReader(runner *simrunner.Manager) simstream.AXReader {
	return func(ctx context.Context, udid string, at *simbridge.Point) (simbridge.Snapshot, bool) {
		h, _, err := runner.Read(ctx, udid, simrunner.ReadOptions{At: at})
		if err != nil {
			return simbridge.Snapshot{}, false
		}
		snap := simbridge.SnapshotFromXCTest(h)
		return snap, snap.Usable()
	}
}

// simOwnershipSyncInterval is how often this daemon re-records its leases in
// the machine-wide registry. It bounds how long a lease whose session ended
// (sim_lease's trigger, which the registry cannot see) still reads as held to
// the OTHER daemons on the machine; this daemon itself stops honouring it at
// once. The work per tick is two small indexed reads.
const simOwnershipSyncInterval = 5 * time.Second

// openSimOwnership opens the machine-wide simulator ownership registry
// (internal/simowner) on behalf of this daemon, or returns nil with a warning
// when it cannot - leases then stay this daemon's alone, which is how every
// daemon behaved before the registry existed. It is shared by every AO daemon
// on the machine whatever their AO_DATA_DIR, so a device a sandbox daemon's
// session drives is refused and listed by the human's daemon and vice versa.
func openSimOwnership(dataDir string, port int, log *slog.Logger) *simowner.Registry {
	path, err := simowner.DefaultPath()
	if err != nil {
		log.Warn("simowner: no machine-wide simulator registry; leases are visible to this daemon only", "err", err)
		return nil
	}
	reg, err := simowner.Open(path, simowner.Self{DataDir: dataDir, PID: os.Getpid(), Port: port})
	if err != nil {
		log.Warn("simowner: no machine-wide simulator registry; leases are visible to this daemon only", "path", path, "err", err)
		return nil
	}
	return reg
}

// simOwnershipOptions wires the registry into the lease service, or nothing
// when there is no registry. (A nil *simowner.Registry must never become a
// non-nil interface value.)
func simOwnershipOptions(reg *simowner.Registry) []simsvc.Option {
	if reg == nil {
		return nil
	}
	return []simsvc.Option{simsvc.WithOwnership(reg)}
}

// simCloneSweepInterval is the backstop for deleting ended sessions' clones.
// An ending is normally handled the moment it happens; this only catches what
// that missed, so it can be slow.
const simCloneSweepInterval = 10 * time.Minute

// sessionEnded reports whether a change event is a session becoming
// terminated.
func sessionEnded(e cdc.Event) bool {
	if e.Type != cdc.EventSessionUpdated || e.SessionID == "" {
		return false
	}
	var payload struct {
		IsTerminated bool `json:"isTerminated"`
	}
	return json.Unmarshal(e.Payload, &payload) == nil && payload.IsTerminated
}

func logSimSweep(log *slog.Logger, report simsvc.SweepReport) {
	for _, clone := range report.Deleted {
		log.Info("deleted an ended session's simulator", "session", clone.SessionID, "label", clone.Label, "udid", clone.UDID)
	}
	for _, clone := range report.Forgotten {
		log.Info("forgot a simulator that no longer exists", "session", clone.SessionID, "label", clone.Label, "udid", clone.UDID)
	}
	for _, clone := range report.Kept {
		log.Info("kept an ended session's simulator another session is driving", "session", clone.SessionID, "udid", clone.UDID)
	}
}
