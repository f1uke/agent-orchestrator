// Package simrunner owns AO's XCTest screen reader: one warm runner per
// simulator that a session holds.
//
// Why it exists: the accessibility bridge (internal/simbridge) reads the
// frontmost app's own process and nothing else, so a web sign-in sheet
// (SafariViewService), SpringBoard's alerts, the text-edit callout and the
// keyboard come back missing - and an agent that cannot read them guesses
// coordinates off a screenshot. XCTest reads every process on screen. Started
// per read it costs 14-26 s (an xcodebuild launch each time); kept warm and
// asked over HTTP it answers in 0.05-0.3 s.
//
// The runner READS, and it TYPES (Type) - which the daemon asks of it only
// inside the device's gesture hold (internal/simtype). Every other touch still
// goes through `ao sim tap` and friends, under the same lease and hold.
//
// Lifecycle, and the ways it ends:
//
//   - A runner is wanted exactly while some session holds the device's lease.
//     The lease table is the one source of truth, asked on every tick: a claim,
//     a release, a take-over, a lapsed TTL and a session that ended (the
//     sim_lease trigger) all land there, so no path that ends a lease can be
//     missed by a hook somebody forgot to add. Kick makes a claim or release
//     take effect now rather than on the next tick.
//   - It is stopped by the pid AO captured when it started it: POST /stop
//     first (xcodebuild then exits in about a second), then SIGTERM, then
//     SIGKILL to its process group, then `simctl terminate` of the runner app
//     on that device. Never by a name pattern.
//   - Shutdown stops every runner when the daemon exits.
//   - A daemon that is SIGKILLed runs none of that. Two things cover it: a
//     handle file per runner, which the next daemon sweeps at startup, and the
//     runner's own idle timeout - it ends itself when nobody has asked it
//     anything for a minute, and the daemon's health check is what keeps
//     asking.
//
// AO never boots a simulator, and `xcodebuild test` on a shut-down device
// boots it, so a runner is only ever started on a device that is booted.
package simrunner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/simbridge"
)

// State is where a device's runner is.
type State string

// The states a runner reports.
const (
	// StateOff: no session holds the device, so no runner is wanted.
	StateOff State = "off"
	// StateStarting: building (first use only), launching, or waiting for it
	// to answer.
	StateStarting State = "starting"
	// StateReady: answering reads.
	StateReady State = "ready"
	// StateFailed: wanted, but the last attempt failed. It is retried with a
	// growing delay, and Reason says what happened.
	StateFailed State = "failed"
)

// Status is a device's runner as a caller sees it.
type Status struct {
	State  State  `json:"state"`
	Reason string `json:"reason,omitempty"`
}

// ErrNotReady is a read the runner cannot answer right now. The Status that
// comes with it says why, which is what a caller falling back has to report.
var ErrNotReady = errors.New("simrunner: the XCTest runner is not ready")

// LeaseSource names the devices some session holds right now. An error is not
// "none": a lease table that cannot be read stops nothing.
type LeaseSource func(ctx context.Context) ([]string, error)

// BootSource says whether a device is booted.
type BootSource func(ctx context.Context, udid string) (bool, error)

// Timing. Each is a measured number or a bound around one.
const (
	// DefaultInterval is how often the lease table is reconciled and every
	// ready runner is health-checked. The check doubles as the heartbeat that
	// keeps the runner's idle timeout from firing.
	DefaultInterval = 2 * time.Second
	// DefaultIdle is the runner's own idle timeout: how long a runner whose
	// daemon died stays alive.
	DefaultIdle = 60 * time.Second
	// defaultReadyTimeout bounds a launch. A warm launch answers in 3-6 s;
	// the first on a device also installs the runner app.
	defaultReadyTimeout = 90 * time.Second
	// readTimeout bounds one hierarchy read. A read is 0.05-0.3 s; an app
	// whose main thread is blocked makes XCTest wait for its accessibility
	// timeout instead, and the caller's fallback then names the hang.
	readTimeout = 15 * time.Second
	// defaultStopGrace is how long each step of a stop is given before the
	// next, stronger one. A runner told to stop exits in about a second; an
	// xcodebuild that is only signalled takes 15-25 s to write its result
	// bundle, which is why the polite step comes first.
	defaultStopGrace = 5 * time.Second
	// buildTimeout bounds the boot check and the build. The build is about
	// six seconds on a warm machine and runs once per AO build and Xcode.
	buildTimeout = 10 * time.Minute
	// healthMisses is how many failed health checks in a row restart a runner.
	healthMisses = 3
	// stableFor is how long a runner must stay up before its failures are
	// forgotten, so a runner that crashes right after answering still backs off.
	stableFor = time.Minute
)

// backoff is the wait before each retry after a failure, the last repeating.
var backoff = []time.Duration{2 * time.Second, 10 * time.Second, 30 * time.Second, 2 * time.Minute}

// Manager owns every runner on this machine.
type Manager struct {
	dataDir  string
	leases   LeaseSource
	booted   BootSource
	launcher Launcher
	log      *slog.Logger
	now      func() time.Time
	interval time.Duration
	idle     time.Duration
	http     *http.Client
	// grace and readyTimeout are the two bounds above; fields so a test does
	// not have to wait them out.
	grace        time.Duration
	readyTimeout time.Duration

	reconcileMu sync.Mutex

	mu       sync.Mutex
	runners  map[string]*runner
	failures map[string]failure
	wanted   map[string]bool
	stops    sync.WaitGroup
	closed   bool

	kick chan struct{}
	stop chan struct{}
	done chan struct{}
	// life is cancelled by Shutdown, so a launch still building or waiting
	// for its runner does not hold the daemon's exit for minutes.
	life   context.Context
	cancel context.CancelFunc
}

type runner struct {
	udid      string
	dir       string
	port      int
	proc      Process
	state     State
	readyAt   time.Time
	stopping  bool
	unhealthy string
	misses    int
	pinging   bool
	// terminateOnce makes the stop escalation run once, whichever of a
	// release, a failed health check or a launch racing a release asks first.
	terminateOnce sync.Once
	// ready is closed when the runner leaves StateStarting, either way.
	ready chan struct{}
	// exited is closed once it is gone and cleaned up.
	exited chan struct{}
}

type failure struct {
	count   int
	reason  string
	retryAt time.Time
}

// Option configures a Manager.
type Option func(*Manager)

// WithLauncher replaces Xcode. Tests use it.
func WithLauncher(l Launcher) Option { return func(m *Manager) { m.launcher = l } }

// WithLogger sets the logger.
func WithLogger(l *slog.Logger) Option { return func(m *Manager) { m.log = l } }

// WithClock replaces time.Now.
func WithClock(now func() time.Time) Option { return func(m *Manager) { m.now = now } }

// WithInterval sets the reconcile interval; 0 turns the loop off, and only
// Kick and Read reconcile.
func WithInterval(d time.Duration) Option { return func(m *Manager) { m.interval = d } }

// WithIdle sets the runner's own idle timeout.
func WithIdle(d time.Duration) Option { return func(m *Manager) { m.idle = d } }

// New builds a Manager, reaps whatever a previous daemon left running, and
// starts reconciling.
func New(dataDir string, leases LeaseSource, booted BootSource, opts ...Option) *Manager {
	m := &Manager{
		dataDir:      dataDir,
		leases:       leases,
		booted:       booted,
		log:          slog.Default(),
		now:          time.Now,
		interval:     DefaultInterval,
		idle:         DefaultIdle,
		grace:        defaultStopGrace,
		readyTimeout: defaultReadyTimeout,
		// No keep-alive: the runner closes every connection after one answer.
		http:     &http.Client{Transport: &http.Transport{DisableKeepAlives: true, Proxy: nil}},
		runners:  map[string]*runner{},
		failures: map[string]failure{},
		wanted:   map[string]bool{},
		kick:     make(chan struct{}, 1),
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
	for _, opt := range opts {
		opt(m)
	}
	m.life, m.cancel = context.WithCancel(context.Background())
	if m.launcher == nil {
		m.launcher = newXcodeLauncher(dataDir)
	}
	m.Sweep(context.Background())
	go m.loop()
	return m
}

// Kick reconciles now rather than on the next tick. It never blocks.
func (m *Manager) Kick() {
	select {
	case m.kick <- struct{}{}:
	default:
	}
}

func (m *Manager) loop() {
	defer close(m.done)
	var tick <-chan time.Time
	if m.interval > 0 {
		ticker := time.NewTicker(m.interval)
		defer ticker.Stop()
		tick = ticker.C
	}
	for {
		select {
		case <-m.stop:
			return
		case <-tick:
		case <-m.kick:
		}
		m.Reconcile(context.Background())
	}
}

// Reconcile brings the running set in line with the lease table, and
// health-checks every runner that is up.
func (m *Manager) Reconcile(ctx context.Context) {
	m.reconcileMu.Lock()
	defer m.reconcileMu.Unlock()

	held, err := m.leases(ctx)
	if err != nil {
		m.log.Warn("simrunner: could not read the simulator leases; leaving runners as they are", "error", err)
		return
	}
	want := make(map[string]bool, len(held))
	for _, udid := range held {
		if key := domain.NormalizeSimUDID(udid); key != "" {
			want[key] = true
		}
	}

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.wanted = want
	var stop, ping []*runner
	for udid, r := range m.runners {
		switch {
		case !want[udid] && !r.stopping:
			stop = append(stop, r)
		case r.state == StateReady && !r.stopping && !r.pinging:
			r.pinging = true
			ping = append(ping, r)
		}
	}
	for udid := range m.failures {
		if !want[udid] {
			delete(m.failures, udid)
		}
	}
	now := m.now()
	for udid := range want {
		if _, running := m.runners[udid]; running {
			continue
		}
		if f, failed := m.failures[udid]; failed && now.Before(f.retryAt) {
			continue
		}
		m.startLocked(udid)
	}
	m.mu.Unlock()

	for _, r := range stop {
		m.stopAsync(r, "no session holds the device any more")
	}
	// Detached: a reconcile run by a read must not cancel the checks it
	// started when that read's request ends.
	checkCtx := context.WithoutCancel(ctx)
	for _, r := range ping {
		go m.healthCheck(checkCtx, r)
	}
}

// startLocked registers a starting runner and launches it in the background.
func (m *Manager) startLocked(udid string) {
	r := &runner{
		udid:   udid,
		dir:    filepath.Join(m.dataDir, "sim", "runner", "devices", udid),
		state:  StateStarting,
		ready:  make(chan struct{}),
		exited: make(chan struct{}),
	}
	m.runners[udid] = r
	go m.launch(r)
}

func (m *Manager) launch(r *runner) {
	// Bounded: a hung `xcodebuild build-for-testing` must not hold the device
	// in "starting" for ever, and a stop is waiting on this to finish.
	ctx, cancel := context.WithTimeout(m.life, buildTimeout)
	defer cancel()
	booted, err := m.booted(ctx, r.udid)
	switch {
	case err != nil:
		m.failLaunch(r, fmt.Sprintf("could not tell whether the simulator is booted: %v", err))
		return
	case !booted:
		// `xcodebuild test` would boot it, and AO never boots a simulator.
		m.failLaunch(r, "the simulator is not booted")
		return
	}
	xctestrun, err := m.launcher.Build(ctx)
	if err != nil {
		m.failLaunch(r, err.Error())
		return
	}
	port, err := m.freePort()
	if err != nil {
		m.failLaunch(r, fmt.Sprintf("could not find a free port: %v", err))
		return
	}
	proc, err := m.launcher.Start(StartSpec{XCTestRun: xctestrun, UDID: r.udid, Port: port, Idle: m.idle, Dir: r.dir})
	if err != nil {
		m.failLaunch(r, err.Error())
		return
	}

	m.mu.Lock()
	r.proc, r.port = proc, port
	stopping := r.stopping
	m.mu.Unlock()
	if err := writeHandle(r.dir, handle{PID: proc.Pid(), Port: port, UDID: r.udid, XCTestRun: xctestrun, StartedAt: m.now().UTC()}); err != nil {
		m.log.Warn("simrunner: could not write the runner's handle; a daemon crash would leave it to its idle timeout",
			"udid", r.udid, "error", err)
	}
	go m.watch(r)
	m.log.Info("simrunner: started the XCTest runner", "udid", r.udid, "pid", proc.Pid(), "port", port)
	if stopping {
		// Released while it was building: the stop is already waiting on exited.
		m.terminate(r)
		return
	}
	m.awaitReady(r)
}

// awaitReady polls the runner until it answers as itself, exits, or runs out
// of time.
func (m *Manager) awaitReady(r *runner) {
	c := m.client(r.port)
	deadline := time.NewTimer(m.readyTimeout)
	defer deadline.Stop()
	poll := time.NewTicker(250 * time.Millisecond)
	defer poll.Stop()
	for {
		select {
		case <-r.exited:
			return
		case <-deadline.C:
			why := fmt.Sprintf("the runner did not answer within %s", m.readyTimeout)
			m.mu.Lock()
			r.unhealthy = why
			m.mu.Unlock()
			m.stopAsync(r, why)
			return
		case <-m.life.Done():
			// Shutdown stops it; there is nothing left to wait for.
			return
		case <-poll.C:
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		err := c.status(ctx, r.udid)
		cancel()
		if err != nil {
			continue
		}
		m.mu.Lock()
		if r.state == StateStarting {
			r.state = StateReady
			r.readyAt = m.now()
			close(r.ready)
		}
		m.mu.Unlock()
		m.log.Info("simrunner: the XCTest runner is ready", "udid", r.udid, "port", r.port)
		return
	}
}

// failLaunch ends a runner that never got a process.
func (m *Manager) failLaunch(r *runner, reason string) {
	m.mu.Lock()
	if m.runners[r.udid] == r {
		delete(m.runners, r.udid)
	}
	if !r.stopping {
		m.recordFailureLocked(r, reason)
	}
	if r.state == StateStarting {
		r.state = StateFailed
		close(r.ready)
	}
	m.mu.Unlock()
	close(r.exited)
	m.log.Warn("simrunner: could not start the XCTest runner", "udid", r.udid, "reason", reason)
}

// watch waits for the process to exit and cleans up after it.
func (m *Manager) watch(r *runner) {
	waitErr := r.proc.Wait()
	m.mu.Lock()
	if m.runners[r.udid] == r {
		delete(m.runners, r.udid)
	}
	switch {
	case r.unhealthy != "":
		m.recordFailureLocked(r, r.unhealthy)
	case !r.stopping:
		reason := "the runner exited unexpectedly"
		if waitErr != nil {
			reason += fmt.Sprintf(" (%v)", waitErr)
		}
		if tail := tailFile(filepath.Join(r.dir, "xcodebuild.log"), 6); tail != "" {
			reason += ":\n" + tail
		}
		m.recordFailureLocked(r, reason)
	}
	if r.state == StateStarting {
		r.state = StateFailed
		close(r.ready)
	}
	r.state = StateFailed
	m.mu.Unlock()
	removeHandle(r.dir)
	_ = os.RemoveAll(filepath.Join(r.dir, "result.xcresult"))
	close(r.exited)
	m.log.Info("simrunner: the XCTest runner exited", "udid", r.udid, "pid", r.proc.Pid(), "stopping", r.stopping)
}

func (m *Manager) recordFailureLocked(r *runner, reason string) {
	f := m.failures[r.udid]
	if !r.readyAt.IsZero() && m.now().Sub(r.readyAt) > stableFor {
		f.count = 0
	}
	delay := backoff[min(f.count, len(backoff)-1)]
	f.count++
	f.reason = reason
	f.retryAt = m.now().Add(delay)
	m.failures[r.udid] = f
}

// healthCheck asks a ready runner who it is. It is also the heartbeat that
// keeps the runner's idle timeout from ending it while the lease is held.
func (m *Manager) healthCheck(ctx context.Context, r *runner) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	err := m.client(r.port).status(ctx, r.udid)
	m.mu.Lock()
	r.pinging = false
	if err == nil {
		r.misses = 0
		m.mu.Unlock()
		return
	}
	r.misses++
	restart := r.misses >= healthMisses && !r.stopping
	why := fmt.Sprintf("the runner stopped answering: %v", err)
	if restart {
		r.unhealthy = why
	}
	m.mu.Unlock()
	if restart {
		m.stopAsync(r, why)
	}
}

// stopAsync stops a runner without blocking the caller; Shutdown waits for it.
func (m *Manager) stopAsync(r *runner, why string) {
	m.mu.Lock()
	if r.stopping {
		m.mu.Unlock()
		return
	}
	r.stopping = true
	m.stops.Add(1)
	m.mu.Unlock()
	m.log.Info("simrunner: stopping the XCTest runner", "udid", r.udid, "why", why)
	go func() {
		defer m.stops.Done()
		m.terminate(r)
	}()
}

// terminate escalates until the runner is gone: ask it, then SIGTERM, then
// SIGKILL its process group - the one AO started, by the pid it captured -
// then stop the runner app on the device itself. It runs once per runner
// however many paths ask for it.
func (m *Manager) terminate(r *runner) {
	m.mu.Lock()
	proc, port := r.proc, r.port
	m.mu.Unlock()
	if proc == nil {
		// Still booting-checked, building or launching: launch sees stopping
		// once it has a process, and terminates it itself.
		<-r.exited
		return
	}
	r.terminateOnce.Do(func() {
		gone := func() bool {
			select {
			case <-r.exited:
				return true
			case <-time.After(m.grace):
				return false
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = m.client(port).stop(ctx)
		cancel()
		if !gone() {
			m.log.Info("simrunner: the runner ignored /stop; sending SIGTERM", "udid", r.udid, "pid", proc.Pid())
			_ = proc.Terminate()
			if !gone() {
				m.log.Warn("simrunner: the runner ignored SIGTERM; sending SIGKILL", "udid", r.udid, "pid", proc.Pid())
				_ = proc.Kill()
				gone()
			}
		}
		ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		// Harmless when it is already gone; essential when xcodebuild died
		// without taking the app down with it, because that app keeps the port.
		_ = m.launcher.TerminateRunner(ctx, r.udid)
	})
	select {
	case <-r.exited:
	case <-time.After(m.grace):
		m.log.Warn("simrunner: the runner's xcodebuild has not exited", "udid", r.udid, "pid", proc.Pid())
	}
}

// ReadOptions shapes one read of a device's screen.
type ReadOptions struct {
	// Wait is how long to wait for a runner that is still starting.
	Wait time.Duration
	// HitTest asks the runner what a touch at each element's tap point would
	// land on, so an element drawn under something else - a row under the tab
	// bar, a button under the keyboard - comes back marked covered. It costs
	// about a millisecond per element, so only a read that hands out tap
	// points asks for it.
	HitTest bool
	// At asks which element a touch at this point (normalized 0..1, like
	// every AO coordinate) reaches; the answer is Snapshot.Reached. It is
	// what lets a recorder name what a coordinate tap touched. Outside 0..1
	// it is refused rather than sent.
	At *simbridge.Point
}

// Read returns the device's screen through its runner. When the runner is
// still starting it waits up to opts.Wait for it. Any other answer than a tree
// is ErrNotReady with a Status saying why, for the caller's fallback to report.
func (m *Manager) Read(ctx context.Context, udid string, opts ReadOptions) (simbridge.XCTestHierarchy, Status, error) {
	if at := opts.At; at != nil && !(at.X >= 0 && at.X <= 1 && at.Y >= 0 && at.Y <= 1) {
		return simbridge.XCTestHierarchy{}, Status{}, fmt.Errorf("the point (%v, %v) is not on the screen: give it as 0..1", at.X, at.Y)
	}
	port, status, err := m.await(ctx, udid, opts.Wait)
	if err != nil {
		return simbridge.XCTestHierarchy{}, status, err
	}
	readCtx, cancel := context.WithTimeout(ctx, readTimeout)
	defer cancel()
	h, err := m.client(port).hierarchy(readCtx, opts)
	if err != nil {
		return simbridge.XCTestHierarchy{}, Status{State: StateReady, Reason: "the read failed: " + err.Error()},
			fmt.Errorf("%w: %w", ErrNotReady, err)
	}
	return h, Status{State: StateReady}, nil
}

// Await waits up to wait for the device's runner to be ready. Anything but
// ready is ErrNotReady with a Status saying why.
func (m *Manager) Await(ctx context.Context, udid string, wait time.Duration) (Status, error) {
	_, status, err := m.await(ctx, udid, wait)
	return status, err
}

// await is Await with the ready runner's port.
func (m *Manager) await(ctx context.Context, udid string, wait time.Duration) (int, Status, error) {
	key := domain.NormalizeSimUDID(udid)
	m.mu.Lock()
	r := m.runners[key]
	m.mu.Unlock()
	if r == nil {
		// A claim a moment ago may not have reached a tick yet.
		m.Reconcile(ctx)
		m.mu.Lock()
		r = m.runners[key]
		m.mu.Unlock()
	}
	if r == nil {
		return 0, m.Status(key), ErrNotReady
	}
	if wait > 0 {
		timer := time.NewTimer(wait)
		select {
		case <-r.ready:
		case <-timer.C:
		case <-ctx.Done():
		}
		timer.Stop()
	}
	m.mu.Lock()
	state, port := r.state, r.port
	m.mu.Unlock()
	if state != StateReady {
		return 0, m.Status(key), ErrNotReady
	}
	return port, Status{State: StateReady}, nil
}

// Type types text into whatever has keyboard focus on the device, through its
// runner. It does not wait for a runner that is starting: the caller holds
// the device's gesture hold by now, and waiting belongs before that (Await).
//
// ErrNotReady means nothing was sent. Any other error means the request may
// have reached the runner, so some of the text may have arrived and the
// screen is the only way to tell.
func (m *Manager) Type(ctx context.Context, udid, text string, opts TypeOptions) (TypeAnswer, error) {
	port, _, err := m.await(ctx, udid, 0)
	if err != nil {
		return TypeAnswer{}, err
	}
	typeCtx, cancel := context.WithTimeout(ctx, TypeTimeout(text))
	defer cancel()
	return m.client(port).typeText(typeCtx, text, opts)
}

// TypeOptions shape one Type.
type TypeOptions struct {
	// Layout switches the software keyboard, by its globe key, to a layout of
	// this kind before typing, and back afterwards. Empty types on whatever
	// keyboard is up.
	Layout Layout
}

// Layout is a kind of keyboard layout, by its letters.
type Layout string

// LayoutLatin is a layout whose letter keys are a-z. The runner also knows
// "other" (any layout whose letters are not); nothing asks for it, since a
// secure field does not take every Thai character typed through one.
const LayoutLatin Layout = "latin"

// Focus says what a Type would go into right now - the focused element, the
// application holding it, and whether the keyboard is up - without typing.
func (m *Manager) Focus(ctx context.Context, udid string) (TypeAnswer, error) {
	port, _, err := m.await(ctx, udid, 0)
	if err != nil {
		return TypeAnswer{}, err
	}
	focusCtx, cancel := context.WithTimeout(ctx, readTimeout)
	defer cancel()
	return m.client(port).focus(focusCtx)
}

// TypeTimeout bounds one /type. XCTest types about 30 characters a second
// (measured 25-40 ms each, iOS 26.3) and waits for the app to settle before
// it starts; the allowance per character is several times that, so a long
// text on a busy app is not cut off halfway.
func TypeTimeout(text string) time.Duration {
	return typeSettle + time.Duration(utf8.RuneCountInString(text))*typePerRune
}

const (
	// typeSettle covers XCTest's wait for the app to idle, and the focus
	// lookup before it types (0.1-0.2 s).
	typeSettle = 10 * time.Second
	// typePerRune is the allowance per character, about three times the
	// measured rate.
	typePerRune = 100 * time.Millisecond
)

// Status is the device's runner now.
func (m *Manager) Status(udid string) Status {
	key := domain.NormalizeSimUDID(udid)
	m.mu.Lock()
	defer m.mu.Unlock()
	if r, ok := m.runners[key]; ok {
		switch r.state {
		case StateReady:
			return Status{State: StateReady}
		case StateStarting:
			return Status{State: StateStarting, Reason: "the XCTest runner is starting"}
		}
	}
	if f, ok := m.failures[key]; ok {
		return Status{State: StateFailed, Reason: f.reason}
	}
	if m.wanted[key] {
		return Status{State: StateStarting, Reason: "the XCTest runner is about to start"}
	}
	return Status{State: StateOff, Reason: "no AO session holds this simulator, and the XCTest reader runs only for a claimed one"}
}

// Shutdown stops every runner and waits for them to go.
func (m *Manager) Shutdown() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	all := make([]*runner, 0, len(m.runners))
	for _, r := range m.runners {
		all = append(all, r)
	}
	m.mu.Unlock()
	m.cancel()
	close(m.stop)
	<-m.done
	for _, r := range all {
		m.stopAsync(r, "the daemon is shutting down")
	}
	m.stops.Wait()
}

func (m *Manager) client(port int) client { return client{port: port, http: m.http} }

// freePort asks the OS for a loopback port nobody holds, skipping the ones
// this manager's own runners are using.
func (m *Manager) freePort() (int, error) {
	for range 10 {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return 0, err
		}
		addr, ok := l.Addr().(*net.TCPAddr)
		_ = l.Close()
		if !ok {
			return 0, fmt.Errorf("the OS offered a non-TCP address %s", l.Addr())
		}
		port := addr.Port
		m.mu.Lock()
		taken := false
		for _, r := range m.runners {
			if r.port == port {
				taken = true
			}
		}
		m.mu.Unlock()
		if !taken {
			return port, nil
		}
	}
	return 0, errors.New("every port the OS offered is in use by another runner")
}

// --- the handle file --------------------------------------------------------

// handle is what the next daemon needs to reap a runner this one started.
type handle struct {
	PID       int       `json:"pid"`
	Port      int       `json:"port"`
	UDID      string    `json:"udid"`
	XCTestRun string    `json:"xctestrun"`
	StartedAt time.Time `json:"startedAt"`
}

func handlePath(dir string) string { return filepath.Join(dir, "runner.json") }

func writeHandle(dir string, h handle) error {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	body, err := json.Marshal(h)
	if err != nil {
		return err
	}
	return os.WriteFile(handlePath(dir), body, 0o600)
}

func removeHandle(dir string) { _ = os.Remove(handlePath(dir)) }

// Sweep reaps runners a previous daemon left behind.
//
// A process is signalled only when its command line is still that runner's
// xcodebuild - the pid in a stale file may belong to anything by now. The
// runner app on the device is stopped either way: it is what holds the port.
func (m *Manager) Sweep(ctx context.Context) {
	matches, err := filepath.Glob(filepath.Join(m.dataDir, "sim", "runner", "devices", "*", "runner.json"))
	if err != nil {
		return
	}
	for _, path := range matches {
		body, err := os.ReadFile(path)
		var h handle
		if err != nil || json.Unmarshal(body, &h) != nil {
			_ = os.Remove(path)
			continue
		}
		if h.PID > 0 && isOurXcodebuild(m.launcher.ProcessCommand(ctx, h.PID), h.XCTestRun) {
			stopCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			_ = m.client(h.Port).stop(stopCtx)
			cancel()
			m.awaitGone(ctx, h)
			if isOurXcodebuild(m.launcher.ProcessCommand(ctx, h.PID), h.XCTestRun) {
				_ = signalGroup(h.PID, false)
				m.awaitGone(ctx, h)
			}
			if isOurXcodebuild(m.launcher.ProcessCommand(ctx, h.PID), h.XCTestRun) {
				_ = signalGroup(h.PID, true)
			}
			m.log.Info("simrunner: stopped a runner left by a previous daemon", "udid", h.UDID, "pid", h.PID)
		}
		termCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		_ = m.launcher.TerminateRunner(termCtx, h.UDID)
		cancel()
		_ = os.Remove(path)
		_ = os.RemoveAll(filepath.Join(filepath.Dir(path), "result.xcresult"))
	}
}

func (m *Manager) awaitGone(ctx context.Context, h handle) {
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if !isOurXcodebuild(m.launcher.ProcessCommand(ctx, h.PID), h.XCTestRun) {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func isOurXcodebuild(command, xctestrun string) bool {
	return command != "" && xctestrun != "" && strings.Contains(command, "xcodebuild") && strings.Contains(command, xctestrun)
}
