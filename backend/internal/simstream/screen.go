package simstream

import (
	"context"
	"os/exec"
	"sync"

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

	mu       sync.Mutex
	hub      *Hub
	hubErr   error
	driver   simbridge.Driver
	drvErr   error
	newCap   func(dataDir string, lookPath func(string) (string, error)) (simbridge.Capturer, error)
	newDrive func(dataDir string, lookPath func(string) (string, error)) (simbridge.Driver, error)
}

// NewScreen builds the surface. Nothing is started here.
func NewScreen(dataDir string) *Screen {
	return &Screen{
		dataDir:  dataDir,
		lookPath: exec.LookPath,
		run:      commandOutput,
		newCap: func(dir string, lookPath func(string) (string, error)) (simbridge.Capturer, error) {
			return simbridge.NewNodeCapturer(dir, lookPath, nil)
		},
		newDrive: func(dir string, lookPath func(string) (string, error)) (simbridge.Driver, error) {
			return simbridge.NewNodeDriver(dir, lookPath, nil)
		},
	}
}

// Devices lists this machine's simulators with the default an unqualified
// request resolves to - or the reason there is none.
func (s *Screen) Devices(ctx context.Context) (simctl.Listing, error) {
	devices, err := simctl.List(ctx, s.lookPath, s.run)
	if err != nil {
		return simctl.Listing{}, err
	}
	return simctl.Summarize(devices), nil
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

// Shutdown stops every running capture. The daemon calls it on the way out: a
// capture process that outlived the daemon would have nobody left to stop it.
func (s *Screen) Shutdown() {
	s.mu.Lock()
	hub := s.hub
	s.mu.Unlock()
	if hub != nil {
		hub.Shutdown()
	}
}

// commandOutput runs a command and returns its combined output, which is what
// simctl diagnostics need: it reports failures on stderr and data on stdout.
func commandOutput(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}
