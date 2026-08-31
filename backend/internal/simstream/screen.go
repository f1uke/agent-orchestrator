package simstream

import (
	"context"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/simbridge"
	"github.com/aoagents/agent-orchestrator/backend/internal/simchrome"
	"github.com/aoagents/agent-orchestrator/backend/internal/simctl"
	"github.com/aoagents/agent-orchestrator/backend/internal/simkeyboard"
	"github.com/aoagents/agent-orchestrator/backend/internal/simpaste"
	"github.com/aoagents/agent-orchestrator/backend/internal/simpower"
	"github.com/aoagents/agent-orchestrator/backend/internal/simslim"
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
	listMu     sync.Mutex
	listing    simctl.Listing
	listedAt   time.Time
	refreshing bool

	// The guest's keyboard input mode, per device. Kept beside the listing
	// cache because it is the same problem in the same place: a subprocess that
	// costs about a second, on a path a human is waiting on.
	kbMu sync.Mutex
	kb   map[string]keyboardEntry

	// power is the only thing in the daemon that can change a device's power
	// state. It lives here because this is already the machine-local surface -
	// the same object that lists devices and drives them - and because a boot
	// has to invalidate the listing cache below the moment it lands.
	power *simpower.Power

	mu       sync.Mutex
	hub      *Hub
	hubErr   error
	driver   simbridge.Driver
	drvErr   error
	newCap   func(dataDir string, lookPath func(string) (string, error)) (simbridge.Capturer, error)
	newDrive func(dataDir string, lookPath func(string) (string, error)) (simbridge.Driver, error)
}

const (
	// DevicesTTL is how long a device listing is reused without being refreshed
	// at all. Short enough that a simulator booted from Xcode shows up while the
	// human is still reaching for the mouse.
	DevicesTTL = 2 * time.Second
	// DevicesMaxAge is how old a listing may be and still be served while a
	// fresh one is fetched behind it. Past this, a caller waits.
	//
	// The two exist because `xcrun simctl list` takes most of a second and this
	// is on an interactive path. A plain expiry made every caller unlucky enough
	// to arrive just after it pay that second - which during a drag meant a
	// visible stall roughly every two seconds, measured at 0.43-0.71 s each.
	// Serving the recent answer and refreshing behind it means nobody waits
	// except the first caller of all.
	DevicesMaxAge = 30 * time.Second

	// KeyboardTTL is how long a guest's input mode is reused without asking
	// again. It is sized by what it protects: long enough that a burst of
	// typing costs one probe rather than one per keystroke, and short enough
	// that somebody who switches their Mac's input source is followed within a
	// few characters rather than a sentence.
	KeyboardTTL = 5 * time.Second
	// KeyboardMaxAge is how old a mode may be and still be served while a fresh
	// one is fetched behind the caller. Past this it is established before
	// anything is typed: a minute of not typing is long enough that the machine
	// may have become a different machine to type on, and correctness is worth
	// the second there in a way it is not worth mid-sentence. The same bound,
	// for the same reason, as the screen a recording remembers.
	KeyboardMaxAge = time.Minute
)

// keyboardEntry is one device's last known input mode, and whether a refresh
// for it is already on its way.
type keyboardEntry struct {
	mode       simkeyboard.Mode
	at         time.Time
	refreshing bool
}

// NewScreen builds the surface. Nothing is started here.
func NewScreen(dataDir string) *Screen {
	s := &Screen{
		dataDir:  dataDir,
		lookPath: exec.LookPath,
		run:      commandOutput,
		now:      time.Now,
		newCap: func(dir string, lookPath func(string) (string, error)) (simbridge.Capturer, error) {
			return simbridge.NewNodeCapturer(dir, lookPath, nil)
		},
	}
	// Set here rather than in the literal above: the driver has to be able to
	// ask this screen which boot of a device it is about to touch, and there is
	// no screen to close over until the literal exists.
	s.newDrive = func(dir string, lookPath func(string) (string, error)) (simbridge.Driver, error) {
		return simbridge.NewNodeDriver(dir, lookPath, s.Boot, nil)
	}
	s.newPower()
	return s
}

// newPower rebuilds the power surface over whatever runner the screen is
// currently using, and wires its completion back into the listing cache.
//
// The wiring is the point. The listing is reused for a couple of seconds, so
// without it a device that has just finished booting keeps reading as "still
// booting" in the pane for a beat after it is up - which is exactly the moment
// somebody is staring at the control waiting for it to change.
func (s *Screen) newPower() {
	s.power = simpower.New(s.lookPath, s.run)
	s.power.OnSettled(s.forgetListing)
}

// Boot names the boot session of the device a gesture is about to touch, so
// the bridge can tell one run of a device from the next. See
// simbridge.ErrNotSent for what the answer protects against, and simctl.Boot
// for what it is.
//
// It reads the same cached listing the gesture route resolved the device from,
// rather than shelling out to simctl: the listing costs most of a second and
// this sits in front of every touch. That cache is dropped the moment any power
// operation settles (see newPower), so a device AO itself booted or shut down
// is never named by a memory of the run before. A device rebooted from outside
// AO is caught when the listing next turns over.
func (s *Screen) Boot(ctx context.Context, udid string) (string, error) {
	listing, err := s.Devices(ctx)
	if err != nil {
		return "", err
	}
	for _, device := range listing.Devices {
		if strings.EqualFold(device.UDID, udid) {
			return device.Boot(), nil
		}
	}
	return "", nil
}

// forgetListing drops the cached device listing so the next caller reads the
// machine rather than a memory of it.
func (s *Screen) forgetListing() {
	s.listMu.Lock()
	s.listedAt = time.Time{}
	s.listMu.Unlock()
}

// StartPower boots or shuts down a device, returning as soon as the work is
// under way. See internal/simpower for why this exists in the daemon and
// nowhere else - in particular, why there is no `ao sim boot`.
//
// req is passed straight through and never inspected: a Screen is a
// device-level surface with no idea what a project is, and keeping it that way
// is what lets it be tested over a bare fake runner.
func (s *Screen) StartPower(ctx context.Context, udid string, op simpower.Op, req *simslim.Request, done func()) error {
	return s.power.Start(ctx, udid, op, req, done)
}

// PowerStatus is every device with a power operation in flight or a failure to
// report, keyed by normalized udid.
func (s *Screen) PowerStatus() map[string]simpower.Status { return s.power.All() }

// ClearPower drops a device's remembered failure. Used when the machine has
// since reached the state that failure was about, so a stale reason cannot
// outlive the thing it described.
func (s *Screen) ClearPower(udid string) { s.power.Clear(udid) }

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
	// After the runner and lookPath have been replaced, not before: power runs
	// the test's commands, not the machine's.
	s.newPower()
	return s
}

// Devices lists this machine's simulators with the default an unqualified
// request resolves to - or the reason there is none.
//
// A recent listing is served at once; one that is getting old is served at once
// too and refreshed behind the caller. Only a caller with nothing usable waits.
// A failure is never cached: a machine that could not answer is asked again
// rather than remembered as broken.
//
// The refresh is demand-driven, never a timer. Nothing here runs when nobody is
// asking, which is the rule this repo learned twice from pollers that burned a
// core.
func (s *Screen) Devices(ctx context.Context) (simctl.Listing, error) {
	s.listMu.Lock()
	age := s.now().Sub(s.listedAt)
	cached, have := s.listing, !s.listedAt.IsZero()
	switch {
	case have && age < DevicesTTL:
		s.listMu.Unlock()
		return cached, nil
	case have && age < DevicesMaxAge:
		start := !s.refreshing
		s.refreshing = true
		s.listMu.Unlock()
		if start {
			// Detached from this request: the caller is being answered now, and
			// a refresh that died with them would never land. A refresh that
			// fails leaves the previous listing in place and is retried by the
			// next caller, which is the same thing a failed read has always done.
			go func() { _, _ = s.refresh(context.WithoutCancel(ctx)) }()
		}
		return cached, nil
	}
	s.listMu.Unlock()

	return s.refresh(ctx)
}

// withFrames attaches each device's own frame, read from the artwork Xcode
// ships. Done here so it rides the same cache the listing does: it is a handful
// of small file reads, and doing them per request would put them back on the
// interactive path the cache exists to keep clear.
//
// A device with no frame on this machine simply has none; the pane draws the
// screen without a body rather than inventing one.
func withFrames(listing simctl.Listing) simctl.Listing {
	roots := simchrome.DefaultRoots()
	for i, device := range listing.Devices {
		frame, err := simchrome.Lookup(roots, device.DeviceTypeIdentifier)
		if err != nil {
			continue
		}
		listing.Devices[i].Frame = &frame
	}
	return listing
}

// refresh reads the machine and stores what it found. Deliberately not holding
// the lock across the subprocess: it takes most of a second, and holding it
// would serialize every caller behind the slowest one.
func (s *Screen) refresh(ctx context.Context) (simctl.Listing, error) {
	devices, err := simctl.List(ctx, s.lookPath, s.run)
	if err != nil {
		s.listMu.Lock()
		s.refreshing = false
		s.listMu.Unlock()
		return simctl.Listing{}, err
	}
	listing := withFrames(simctl.Summarize(devices))

	s.listMu.Lock()
	s.listing, s.listedAt, s.refreshing = listing, s.now(), false
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

// AX reads a device's accessibility tree through the same resident driver a
// gesture goes through.
//
// It exists so the gesture recorder (internal/service/sim) can see the screen
// without owning a bridge of its own: the recorder reads the tree at the
// moment a gesture takes its hold, which is the moment this driver is already
// being used to touch the same device. A second NodeDriver would be a second
// Node process, a second addon load and a second 370 ms attach on a device
// that has one finger anyway - and the daemon would then have two bridges to
// shut down instead of one.
func (s *Screen) AX(ctx context.Context, udid string) (simbridge.Snapshot, error) {
	driver, err := s.Driver(ctx)
	if err != nil {
		return simbridge.Snapshot{}, err
	}
	return driver.AX(ctx, udid)
}

// Keyboard asks a device which input mode it will read key presses through.
//
// 🗝 This used to be fetched fresh for every keystroke, and that is what made
// typing in the Device tab feel broken: the probe spawns a process INSIDE the
// guest, measured at 909-960 ms, and it sat in front of the character. A
// character took 1164-1181 ms to reach the device, of which ~935 ms was this
// question and 6 ms was the device itself. So it is now maintained rather than
// fetched - the same move that took the recorder's screen read off the gesture
// path, for the same reason: the cost is real and nobody has to be waiting on it.
//
// ⚠ The old comment here said caching this was "precisely the silent
// corruption" the probe exists to prevent, so why it is safe now has to be
// stated rather than assumed. What changed is the caller. The pane sends the
// CHARACTER the human meant, taken from `event.key`, which the browser has
// already resolved through the Mac's input source. So a human who switches
// their Mac to Thai immediately starts sending Thai runes, and PlanText routes
// those to the pasteboard from the TEXT alone - before the input mode is
// consulted at all. A stale "US" therefore cannot corrupt Thai, which is the
// case that made this worth asking about.
//
// What a stale mode could still get wrong is narrow and real: a switch to
// another LATIN layout (AZERTY, QWERTZ, UK) while typing ASCII, where the
// characters stay sendable but the guest maps the usages differently. That is
// what the windows below are sized for, and it is why the mode is still
// established from scratch rather than assumed when it has gone properly cold.
func (s *Screen) Keyboard(ctx context.Context, udid string) (simkeyboard.Mode, error) {
	s.kbMu.Lock()
	entry, have := s.kb[udid]
	age := s.now().Sub(entry.at)
	switch {
	case have && age < KeyboardTTL:
		s.kbMu.Unlock()
		return entry.mode, nil
	case have && age < KeyboardMaxAge:
		start := !entry.refreshing
		entry.refreshing = true
		s.kb[udid] = entry
		s.kbMu.Unlock()
		if start {
			// Detached from this request for the same reason the listing's
			// refresh is: the caller is being answered now, and a refresh that
			// died with them would never land. At most one runs per device -
			// two queued probes are two guest spawns nobody is reading.
			go func() { _, _ = s.probeKeyboard(context.WithoutCancel(ctx), udid) }()
		}
		return entry.mode, nil
	}
	s.kbMu.Unlock()

	return s.probeKeyboard(ctx, udid)
}

// probeKeyboard asks the guest and stores what it said. The subprocess is
// deliberately not run under the lock: it takes about a second, and holding it
// would put every other device's keystrokes behind this one.
func (s *Screen) probeKeyboard(ctx context.Context, udid string) (simkeyboard.Mode, error) {
	mode, err := simkeyboard.Probe(ctx, s.run, udid)

	s.kbMu.Lock()
	defer s.kbMu.Unlock()
	if s.kb == nil {
		s.kb = map[string]keyboardEntry{}
	}
	entry := s.kb[udid]
	entry.refreshing = false
	if err != nil {
		// Never cached, the same rule the listing keeps: a guest that would not
		// answer must be asked again rather than remembered as unreadable. A
		// failed refresh leaves whatever was already known in place, because a
		// device that answered a moment ago has not become a different device.
		s.kb[udid] = entry
		return simkeyboard.Mode{}, err
	}
	entry.mode, entry.at = mode, s.now()
	s.kb[udid] = entry
	return mode, nil
}

// Pasteboard is the guest clipboard, which is how `type` reaches a field whose
// keyboard would remap the key presses.
func (s *Screen) Pasteboard() simpaste.Pasteboard {
	return simpaste.Simctl{Run: s.run}
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
