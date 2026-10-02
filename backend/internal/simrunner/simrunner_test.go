//go:build !windows

package simrunner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

const (
	udidA = "AAAAAAAA-0000-0000-0000-000000000000"
	udidB = "BBBBBBBB-0000-0000-0000-000000000000"
)

// fakeLauncher stands in for Xcode. Each Start serves a real runner over HTTP
// on the port the Manager chose, so the Manager is exercised through the same
// wire it uses against a simulator.
type fakeLauncher struct {
	mu         sync.Mutex
	buildErr   error
	ignoreStop bool
	// wrongUDID makes a runner answer /status as another device.
	wrongUDID bool
	starts    []StartSpec
	procs     []*fakeProc
	stopped   []string
	commands  map[int]string
	nextPID   int
}

type fakeProc struct {
	pid       int
	spec      StartSpec
	srv       *http.Server
	done      chan struct{}
	once      sync.Once
	stops     atomic.Int32
	terms     atomic.Int32
	kills     atomic.Int32
	unhealthy atomic.Bool
}

func (p *fakeProc) exit() {
	p.once.Do(func() {
		_ = p.srv.Close()
		close(p.done)
	})
}

func (p *fakeProc) Pid() int    { return p.pid }
func (p *fakeProc) Wait() error { <-p.done; return nil }
func (p *fakeProc) Terminate() error {
	p.terms.Add(1)
	p.exit()
	return nil
}
func (p *fakeProc) Kill() error {
	p.kills.Add(1)
	p.exit()
	return nil
}

func (f *fakeLauncher) Build(context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.buildErr != nil {
		return "", f.buildErr
	}
	return "/fake/AORunner.xctestrun", nil
}

func (f *fakeLauncher) Start(spec StartSpec) (Process, error) {
	f.mu.Lock()
	f.nextPID++
	p := &fakeProc{pid: 1000 + f.nextPID, spec: spec, done: make(chan struct{})}
	wrong, ignore := f.wrongUDID, f.ignoreStop
	f.starts = append(f.starts, spec)
	f.procs = append(f.procs, p)
	f.mu.Unlock()

	l, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(spec.Port)))
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/status", func(w http.ResponseWriter, _ *http.Request) {
		if p.unhealthy.Load() {
			http.Error(w, "wedged", http.StatusInternalServerError)
			return
		}
		udid := spec.UDID
		if wrong {
			udid = "SOMEBODY-ELSE"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"version": WireVersion, "udid": udid, "pid": p.pid})
	})
	mux.HandleFunc("/hierarchy", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"version": WireVersion,
			"screen":  map[string]any{"width": 400, "height": 874},
			"apps": []any{map[string]any{"bundleId": "app.for." + spec.UDID, "tree": map[string]any{
				"type": "Application", "enabled": true, "frame": map[string]any{"x": 0, "y": 0, "width": 400, "height": 874},
			}}},
		})
	})
	mux.HandleFunc("/stop", func(w http.ResponseWriter, _ *http.Request) {
		p.stops.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"stopping": true})
		if !ignore {
			go p.exit()
		}
	})
	p.srv = &http.Server{Handler: mux, ReadHeaderTimeout: time.Second}
	go func() { _ = p.srv.Serve(l) }()
	return p, nil
}

func (f *fakeLauncher) TerminateRunner(_ context.Context, udid string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopped = append(f.stopped, udid)
	return nil
}

func (f *fakeLauncher) ProcessCommand(_ context.Context, pid int) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	cmd := f.commands[pid]
	if cmd != "" && syscall.Kill(pid, 0) != nil {
		return ""
	}
	return cmd
}

func (f *fakeLauncher) proc(i int) *fakeProc {
	f.mu.Lock()
	defer f.mu.Unlock()
	if i >= len(f.procs) {
		return nil
	}
	return f.procs[i]
}

func (f *fakeLauncher) startCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.starts)
}

func (f *fakeLauncher) terminatedOn(udid string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, u := range f.stopped {
		if u == udid {
			return true
		}
	}
	return false
}

// world is the lease table and the device list the Manager reads.
type world struct {
	mu       sync.Mutex
	held     []string
	booted   map[string]bool
	leaseErr error
	now      time.Time
}

func (w *world) leases(context.Context) ([]string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.held...), w.leaseErr
}

func (w *world) isBooted(_ context.Context, udid string) (bool, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.booted[udid], nil
}

func (w *world) hold(udids ...string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.held = udids
}

func (w *world) clock() time.Time {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.now
}

func (w *world) advance(d time.Duration) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.now = w.now.Add(d)
}

func newTestManager(t *testing.T, f *fakeLauncher, w *world) *Manager {
	t.Helper()
	if w.booted == nil {
		w.booted = map[string]bool{udidA: true, udidB: true}
	}
	if w.now.IsZero() {
		w.now = time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	}
	m := New(t.TempDir(), w.leases, w.isBooted,
		WithLauncher(f), WithInterval(0), WithClock(w.clock),
		WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
		func(m *Manager) {
			m.grace = 200 * time.Millisecond
			m.readyTimeout = 2 * time.Second
		})
	t.Cleanup(m.Shutdown)
	return m
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func exited(p *fakeProc) bool {
	select {
	case <-p.done:
		return true
	default:
		return false
	}
}

func TestManager_ARunnerLivesExactlyAsLongAsTheLease(t *testing.T) {
	f, w := &fakeLauncher{}, &world{}
	m := newTestManager(t, f, w)

	if _, st, err := m.Read(context.Background(), udidA, 0); !errors.Is(err, ErrNotReady) || st.State != StateOff {
		t.Fatalf("unheld device: state %q err %v, want off", st.State, err)
	}
	if f.startCount() != 0 {
		t.Fatal("a runner was started for a device nobody holds")
	}

	w.hold(udidA)
	// No tick has happened: the read itself reconciles, then waits for ready.
	h, st, err := m.Read(context.Background(), udidA, 3*time.Second)
	if err != nil || st.State != StateReady {
		t.Fatalf("held device: state %+v err %v, want a ready read", st, err)
	}
	if len(h.Apps) != 1 || h.Apps[0].BundleID != "app.for."+udidA {
		t.Fatalf("read the wrong runner: %+v", h.Apps)
	}
	p := f.proc(0)
	if p.spec.Idle != DefaultIdle || p.spec.UDID != udidA {
		t.Fatalf("started with %+v", p.spec)
	}
	handlePath := filepath.Join(m.dataDir, "sim", "runner", "devices", udidA, "runner.json")
	var h0 handle
	body, err := os.ReadFile(handlePath)
	if err != nil || json.Unmarshal(body, &h0) != nil || h0.PID != p.pid || h0.Port != p.spec.Port {
		t.Fatalf("handle file = %s (%v), want pid %d port %d", body, err, p.pid, p.spec.Port)
	}

	w.hold()
	m.Reconcile(context.Background())
	waitFor(t, "the runner to exit after the lease went", func() bool { return exited(p) })
	waitFor(t, "the device's runner app to be terminated", func() bool { return f.terminatedOn(udidA) })
	if p.stops.Load() != 1 || p.terms.Load() != 0 || p.kills.Load() != 0 {
		t.Fatalf("a runner that obeys /stop got stops=%d terms=%d kills=%d", p.stops.Load(), p.terms.Load(), p.kills.Load())
	}
	waitFor(t, "the handle file to go", func() bool { _, err := os.Stat(handlePath); return os.IsNotExist(err) })
	if st := m.Status(udidA); st.State != StateOff {
		t.Fatalf("after release: %+v, want off", st)
	}
}

func TestManager_EscalatesToSignalsWhenTheRunnerIgnoresStop(t *testing.T) {
	f, w := &fakeLauncher{ignoreStop: true}, &world{}
	m := newTestManager(t, f, w)
	w.hold(udidA)
	if _, _, err := m.Read(context.Background(), udidA, 3*time.Second); err != nil {
		t.Fatal(err)
	}
	p := f.proc(0)
	w.hold()
	m.Reconcile(context.Background())
	waitFor(t, "the runner to be terminated", func() bool { return exited(p) })
	if p.stops.Load() != 1 || p.terms.Load() != 1 {
		t.Fatalf("stops=%d terms=%d, want /stop then SIGTERM", p.stops.Load(), p.terms.Load())
	}
	waitFor(t, "the device's runner app to be terminated", func() bool { return f.terminatedOn(udidA) })
}

func TestManager_NeverLaunchesOnADeviceThatIsNotBooted(t *testing.T) {
	// `xcodebuild test` boots a shut-down simulator, and AO never boots one.
	f, w := &fakeLauncher{}, &world{booted: map[string]bool{}}
	m := newTestManager(t, f, w)
	w.hold(udidA)
	_, st, err := m.Read(context.Background(), udidA, time.Second)
	if !errors.Is(err, ErrNotReady) || st.State != StateFailed || st.Reason != "the simulator is not booted" {
		t.Fatalf("state %+v err %v, want failed: not booted", st, err)
	}
	if f.startCount() != 0 {
		t.Fatal("a runner was launched on a device that is not booted")
	}
}

func TestManager_RetriesAFailedRunnerWithBackoff(t *testing.T) {
	f, w := &fakeLauncher{}, &world{}
	m := newTestManager(t, f, w)
	w.hold(udidA)
	if _, _, err := m.Read(context.Background(), udidA, 3*time.Second); err != nil {
		t.Fatal(err)
	}
	p := f.proc(0)
	p.exit() // it dies on its own
	waitFor(t, "the exit to be noticed", func() bool { return m.Status(udidA).State == StateFailed })
	if st := m.Status(udidA); st.Reason == "" {
		t.Fatalf("a failure with no reason: %+v", st)
	}

	m.Reconcile(context.Background())
	if f.startCount() != 1 {
		t.Fatalf("restarted at once (%d starts); a crashing runner must back off", f.startCount())
	}
	w.advance(backoff[0] + time.Second)
	m.Reconcile(context.Background())
	waitFor(t, "the restart", func() bool { return f.startCount() == 2 })
	waitFor(t, "the restarted runner to be ready", func() bool { return m.Status(udidA).State == StateReady })
}

func TestManager_RefusesARunnerThatAnswersAsAnotherDevice(t *testing.T) {
	f, w := &fakeLauncher{wrongUDID: true}, &world{}
	m := newTestManager(t, f, w)
	w.hold(udidA)
	m.Reconcile(context.Background())
	waitFor(t, "the launch to give up", func() bool { return m.Status(udidA).State == StateFailed })
	if p := f.proc(0); !exited(p) {
		t.Fatal("a runner that never answered as itself was left running")
	}
}

func TestManager_RestartsARunnerThatStopsAnswering(t *testing.T) {
	f, w := &fakeLauncher{}, &world{}
	m := newTestManager(t, f, w)
	w.hold(udidA)
	if _, _, err := m.Read(context.Background(), udidA, 3*time.Second); err != nil {
		t.Fatal(err)
	}
	p := f.proc(0)
	p.unhealthy.Store(true)
	for range healthMisses {
		m.Reconcile(context.Background())
		time.Sleep(50 * time.Millisecond)
	}
	waitFor(t, "the wedged runner to be stopped", func() bool { return exited(p) })
	waitFor(t, "the failure to be recorded", func() bool { return m.Status(udidA).State == StateFailed })
}

func TestManager_TwoDevicesRunSideBySide(t *testing.T) {
	f, w := &fakeLauncher{}, &world{}
	m := newTestManager(t, f, w)
	w.hold(udidA, udidB)
	ha, _, errA := m.Read(context.Background(), udidA, 3*time.Second)
	hb, _, errB := m.Read(context.Background(), udidB, 3*time.Second)
	if errA != nil || errB != nil {
		t.Fatal(errA, errB)
	}
	if ha.Apps[0].BundleID != "app.for."+udidA || hb.Apps[0].BundleID != "app.for."+udidB {
		t.Fatalf("crossed reads: %s / %s", ha.Apps[0].BundleID, hb.Apps[0].BundleID)
	}
	if a, b := f.proc(0), f.proc(1); a.spec.Port == b.spec.Port {
		t.Fatalf("both runners on port %d", a.spec.Port)
	}
	// Releasing one leaves the other alone.
	w.hold(udidB)
	m.Reconcile(context.Background())
	var stoppedA, keptB *fakeProc
	for _, p := range []*fakeProc{f.proc(0), f.proc(1)} {
		if p.spec.UDID == udidA {
			stoppedA = p
		} else {
			keptB = p
		}
	}
	waitFor(t, "A's runner to exit", func() bool { return exited(stoppedA) })
	if exited(keptB) || m.Status(udidB).State != StateReady {
		t.Fatal("releasing A stopped B's runner")
	}
}

func TestManager_AnUnreadableLeaseTableStopsNothing(t *testing.T) {
	f, w := &fakeLauncher{}, &world{}
	m := newTestManager(t, f, w)
	w.hold(udidA)
	if _, _, err := m.Read(context.Background(), udidA, 3*time.Second); err != nil {
		t.Fatal(err)
	}
	w.mu.Lock()
	w.leaseErr = errors.New("database is locked")
	w.mu.Unlock()
	m.Reconcile(context.Background())
	time.Sleep(100 * time.Millisecond)
	if exited(f.proc(0)) {
		t.Fatal("a failed lease read was taken as every lease released")
	}
}

func TestManager_ShutdownStopsEveryRunner(t *testing.T) {
	f, w := &fakeLauncher{}, &world{}
	m := newTestManager(t, f, w)
	w.hold(udidA, udidB)
	for _, u := range []string{udidA, udidB} {
		if _, _, err := m.Read(context.Background(), u, 3*time.Second); err != nil {
			t.Fatal(err)
		}
	}
	m.Shutdown()
	for i := range 2 {
		if !exited(f.proc(i)) {
			t.Fatalf("runner %d outlived Shutdown", i)
		}
	}
}

func TestManager_ABuildFailureIsReportedAndRetried(t *testing.T) {
	f, w := &fakeLauncher{buildErr: fmt.Errorf("%w", ErrUnavailable)}, &world{}
	m := newTestManager(t, f, w)
	w.hold(udidA)
	_, st, err := m.Read(context.Background(), udidA, time.Second)
	if !errors.Is(err, ErrNotReady) || st.State != StateFailed || st.Reason != ErrUnavailable.Error() {
		t.Fatalf("state %+v err %v", st, err)
	}
}

// The sweep is the only path that signals a pid AO did not start in this
// process, so it must only ever signal the runner it recorded.
func TestSweep_StopsOnlyTheRunnerItRecorded(t *testing.T) {
	dir := t.TempDir()
	f := &fakeLauncher{commands: map[int]string{}}

	ours := exec.Command("sleep", "60")
	ours.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stranger := exec.Command("sleep", "60")
	if err := ours.Start(); err != nil {
		t.Fatal(err)
	}
	if err := stranger.Start(); err != nil {
		t.Fatal(err)
	}
	reaped := make(chan struct{})
	go func() { _ = ours.Wait(); close(reaped) }()
	t.Cleanup(func() {
		_ = ours.Process.Kill()
		_ = stranger.Process.Kill()
		_ = stranger.Wait()
	})

	xctestrun := filepath.Join(dir, "AORunner.xctestrun")
	f.commands[ours.Process.Pid] = "/usr/bin/xcodebuild test-without-building -xctestrun " + xctestrun
	// The stranger's pid is in a handle too - a recycled pid - but its
	// command line is not our xcodebuild.
	f.commands[stranger.Process.Pid] = "sleep 60"
	for udid, pid := range map[string]int{udidA: ours.Process.Pid, udidB: stranger.Process.Pid} {
		d := filepath.Join(dir, "sim", "runner", "devices", udid)
		if err := os.MkdirAll(d, 0o750); err != nil {
			t.Fatal(err)
		}
		if err := writeHandle(d, handle{PID: pid, Port: 1, UDID: udid, XCTestRun: xctestrun}); err != nil {
			t.Fatal(err)
		}
	}

	m := &Manager{dataDir: dir, launcher: f, http: &http.Client{Timeout: time.Second}, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	m.Sweep(context.Background())

	select {
	case <-reaped:
	case <-time.After(5 * time.Second):
		t.Fatal("the recorded runner survived the sweep")
	}
	if err := stranger.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("the sweep signalled a process that was not its runner: %v", err)
	}
	for _, u := range []string{udidA, udidB} {
		if !f.terminatedOn(u) {
			t.Errorf("runner app on %s not terminated", u)
		}
		if _, err := os.Stat(handlePath(filepath.Join(dir, "sim", "runner", "devices", u))); !os.IsNotExist(err) {
			t.Errorf("handle for %s left behind", u)
		}
	}
}

func TestIsOurXcodebuild(t *testing.T) {
	run := "/x/AORunner.xctestrun"
	cases := map[string]bool{
		"/usr/bin/xcodebuild test-without-building -xctestrun /x/AORunner.xctestrun": true,
		"/usr/bin/xcodebuild test-without-building -xctestrun /y/Other.xctestrun":    false,
		"sleep 60": false,
		"":         false,
	}
	for cmd, want := range cases {
		if got := isOurXcodebuild(cmd, run); got != want {
			t.Errorf("isOurXcodebuild(%q) = %v, want %v", cmd, got, want)
		}
	}
	if isOurXcodebuild("xcodebuild", "") {
		t.Error("an empty xctestrun matched")
	}
}
