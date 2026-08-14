package simstream

import (
	"context"
	"io"
	"os/exec"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/simbridge"
	"github.com/aoagents/agent-orchestrator/backend/internal/simctl"
)

// Screen is the daemon's machine-local simulator surface: what simulators exist,
// their live screens, and the driver that touches one.
//
// Everything expensive about it is built on first use, not at startup. Listing
// devices shells out to simctl only when somebody asks; the capture mechanism
// and the screen driver both need Node and a mac, and a daemon must start on a
// machine that has neither. Building them lazily is also what lets the failure
// be a sentence in the UI ("node was not found on PATH") instead of a daemon
// that refused to boot.
type Screen struct {
	dataDir  string
	lookPath simctl.LookPath
	run      simctl.Runner
	now      func() time.Time

	// The device list is read again for every gesture, to refuse a device this
	// machine has not booted before anything reaches it. That check cost more
	// than everything else in a touch put together: `xcrun simctl list` is a
	// subprocess that measures 0.7-1.0 s on the machine this was built for, and
	// paying it per click is what made driving the pane feel like sending a
	// request rather than touching a screen. It is cached for long enough to
	// cover a burst of gestures and not long enough for a human to notice a
	// simulator they just booted is missing.
	listMu   sync.Mutex
	listing  simctl.Listing
	listedAt time.Time

	mu       sync.Mutex
	hub      *Hub
	hubErr   error
	driver   simbridge.Driver
	drvErr   error
	newCap   func(dataDir string, lookPath func(string) (string, error)) (simbridge.Capturer, error)
	newDrive func(dataDir string, lookPath func(string) (string, error)) (simbridge.Driver, error)
}

// DevicesTTL is how long a device listing is reused. Short enough that a
// simulator booted from Xcode shows up while the human is still reaching for
// the mouse; long enough that a drag does not shell out per gesture.
const DevicesTTL = 2 * time.Second

// NewScreen builds the surface. Nothing is started here.
func NewScreen(dataDir string) *Screen {
	return &Screen{
		dataDir:  dataDir,
		lookPath: exec.LookPath,
		run:      commandOutput,
		now:      time.Now,
		newCap: func(dir string, lookPath func(string) (string, error)) (simbridge.Capturer, error) {
			return simbridge.NewNodeCapturer(dir, lookPath, nil)
		},
		newDrive: func(dir string, lookPath func(string) (string, error)) (simbridge.Driver, error) {
			return simbridge.NewNodeDriver(dir, lookPath, nil)
		},
	}
}

// NewScreenForTest builds a screen over a driver that is already made and a
// clock the test owns, so the lifetime and caching rules can be checked without
// Node, an addon, a mac or a wall clock.
func NewScreenForTest(driver simbridge.Driver, run simctl.Runner, now func() time.Time) *Screen {
	s := NewScreen("")
	if driver != nil {
		s.newDrive = func(string, func(string) (string, error)) (simbridge.Driver, error) { return driver, nil }
	}
	if run != nil {
		s.run = run
		s.lookPath = func(string) (string, error) { return "/usr/bin/xcrun", nil }
	}
	if now != nil {
		s.now = now
	}
	return s
}

// Devices lists this machine's simulators with the default an unqualified
// request resolves to - or the reason there is none.
//
// A listing this recent is reused. A failure is never cached: a machine that
// could not answer is asked again rather than remembered as broken.
func (s *Screen) Devices(ctx context.Context) (simctl.Listing, error) {
	s.listMu.Lock()
	if !s.listedAt.IsZero() && s.now().Sub(s.listedAt) < DevicesTTL {
		cached := s.listing
		s.listMu.Unlock()
		return cached, nil
	}
	s.listMu.Unlock()

	// Deliberately outside the lock: holding it across a subprocess that takes
	// most of a second would serialize every caller behind the slowest one.
	devices, err := simctl.List(ctx, s.lookPath, s.run)
	if err != nil {
		return simctl.Listing{}, err
	}
	listing := simctl.Summarize(devices)

	s.listMu.Lock()
	s.listing, s.listedAt = listing, s.now()
	s.listMu.Unlock()
	return listing, nil
}

// Subscribe attaches to a device's live screen. The capture mechanism is built
// on the first subscription and reused; a machine that cannot capture says so
// every time rather than being retried into a loop.
func (s *Screen) Subscribe(ctx context.Context, udid string) (<-chan Event, error) {
	s.mu.Lock()
	if s.hub == nil && s.hubErr == nil {
		capturer, err := s.newCap(s.dataDir, s.lookPath)
		if err != nil {
			s.hubErr = err
		} else {
			s.hub = New(capturer)
		}
	}
	hub, err := s.hub, s.hubErr
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return hub.Subscribe(ctx, udid)
}

// Driver returns the screen driver a gesture goes through, building it on first
// use.
func (s *Screen) Driver(context.Context) (simbridge.Driver, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.driver == nil && s.drvErr == nil {
		driver, err := s.newDrive(s.dataDir, s.lookPath)
		if err != nil {
			s.drvErr = err
		} else {
			s.driver = driver
		}
	}
	return s.driver, s.drvErr
}

// Shutdown stops every running capture and the resident gesture bridge. The
// daemon calls it on the way out: either process outliving the daemon would
// have nobody left to stop it - and the bridge is the one that could be holding
// a finger down on a device.
func (s *Screen) Shutdown() {
	s.mu.Lock()
	hub, driver := s.hub, s.driver
	s.mu.Unlock()
	if hub != nil {
		hub.Shutdown()
	}
	if closer, ok := driver.(io.Closer); ok {
		_ = closer.Close()
	}
}

// commandOutput runs a command and returns its combined output, which is what
// simctl diagnostics need: it reports failures on stderr and data on stdout.
func commandOutput(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}
