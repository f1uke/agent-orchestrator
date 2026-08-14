package cli

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/simbridge"
)

// fakeSimDriver stands in for the whole mechanism - node, the vendored addon,
// the private frameworks and the device. Faking it here is what lets every test
// in this file run on a Linux CI box with no Xcode and no simulator, and it is
// the same seam a future Apple-supported driver would slot into.
type fakeSimDriver struct {
	mu         sync.Mutex
	snapshot   simbridge.Snapshot
	axErr      error
	performErr error
	// failEvery makes every Perform fail, including the recovery lift. Without
	// it only the first fails, which is the ordinary "the gesture died but the
	// device could still be rescued" case.
	failEvery bool
	result    simbridge.PerformResult
	// gestures records each Perform call, in order.
	gestures [][]simbridge.Event
	// onPerform runs inside Perform, for tests that need to observe the world
	// while a gesture is in flight.
	onPerform func()
	// snapshotQueue is handed out one per AX call before snapshot is used.
	snapshotQueue []simbridge.Snapshot
	axReads       int
}

// Hold is the desktop pane's drag, which the CLI has no command for: `ao sim`
// composes whole gestures, so a touch that outlives one call never happens here.
func (f *fakeSimDriver) Hold(context.Context, string, []simbridge.Event) error {
	return errors.New("the CLI composes whole gestures; a held touch is the desktop pane's drag")
}

func (f *fakeSimDriver) AX(context.Context, string) (simbridge.Snapshot, error) {
	if f.axErr != nil {
		return simbridge.Snapshot{}, f.axErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.axReads++
	// snapshotQueue lets a test hand out a different tree per read, which is how
	// "the app had not published its screen yet, so read again" is exercised.
	if len(f.snapshotQueue) > 0 {
		next := f.snapshotQueue[0]
		f.snapshotQueue = f.snapshotQueue[1:]
		return next, nil
	}
	return f.snapshot, nil
}

func (f *fakeSimDriver) reads() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.axReads
}

func (f *fakeSimDriver) Perform(_ context.Context, _ string, events []simbridge.Event) (simbridge.PerformResult, error) {
	f.mu.Lock()
	f.gestures = append(f.gestures, events)
	attempt := len(f.gestures)
	f.mu.Unlock()
	if f.onPerform != nil {
		f.onPerform()
	}
	if f.performErr != nil && (f.failEvery || attempt == 1) {
		return simbridge.PerformResult{}, f.performErr
	}
	return f.result, nil
}

func (f *fakeSimDriver) calls() [][]simbridge.Event {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([][]simbridge.Event(nil), f.gestures...)
}

// touchDeps is a booted device, a live daemon and a fake driver.
func touchDeps(t *testing.T, driver simbridge.Driver) (Deps, *simDaemon) {
	t.Helper()
	cfg := setConfigEnv(t)
	deps := simLeaseDeps(t, bootedProMaxOnly(t), fakePNG)
	deps.SimDriver = func(string) (simbridge.Driver, error) { return driver, nil }
	daemon := newSimDaemon(t, cfg)
	t.Setenv("AO_SESSION_ID", "mer-9")
	return deps, daemon
}

func touchTypesOf(events []simbridge.Event) string {
	var parts []string
	for _, e := range events {
		if e.Kind == "touch" {
			parts = append(parts, e.Type)
		}
	}
	return strings.Join(parts, ",")
}

// --- the happy paths -------------------------------------------------------

func TestSimTap_SendsAMatchedDownAndUpAtTheGivenPoint(t *testing.T) {
	driver := &fakeSimDriver{}
	deps, daemon := touchDeps(t, driver)

	out, errOut, err := executeCLI(t, deps, "sim", "tap", "0.5", "0.934")
	if err != nil {
		t.Fatalf("sim tap failed: %v\nstderr=%s", err, errOut)
	}
	calls := driver.calls()
	if len(calls) != 1 {
		t.Fatalf("driver saw %d gestures, want 1", len(calls))
	}
	if got := touchTypesOf(calls[0]); got != "begin,end" {
		t.Fatalf("touches = %q, want begin,end", got)
	}
	if calls[0][0].X != 0.5 || calls[0][0].Y != 0.934 {
		t.Fatalf("tapped %+v, want the point asked for", calls[0][0])
	}
	if !strings.Contains(out, "0.500") || !strings.Contains(out, simUDIDProMax) {
		t.Fatalf("output must say what was tapped and where: %s", out)
	}
	assertHoldTakenAndReleased(t, daemon)
}

func TestSimTap_TakesThePointAoSimAxPrints(t *testing.T) {
	// The contract that makes the pair usable: the number `ao sim ax` prints for
	// an element is the number `ao sim tap` accepts, with no maths in between.
	driver := &fakeSimDriver{snapshot: fixtureSnapshot()}
	deps, _ := touchDeps(t, driver)

	out, _, err := executeCLI(t, deps, "sim", "ax", "--json")
	if err != nil {
		t.Fatalf("sim ax: %v", err)
	}
	var snap simbridge.Snapshot
	if err := json.Unmarshal([]byte(out), &snap); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	target := snap.Elements[0].Children[0]

	x := strconv.FormatFloat(target.Tap.X, 'f', -1, 64)
	y := strconv.FormatFloat(target.Tap.Y, 'f', -1, 64)
	if _, _, err := executeCLI(t, deps, "sim", "tap", x, y); err != nil {
		t.Fatalf("tapping the point ax printed must work: %v", err)
	}
	calls := driver.calls()
	last := calls[len(calls)-1]
	if last[0].X != target.Tap.X || last[0].Y != target.Tap.Y {
		t.Fatalf("tap landed at %+v, want the element's own tap point %+v", last[0], target.Tap)
	}
}

func TestSimType_SendsKeysAndNoTouches(t *testing.T) {
	driver := &fakeSimDriver{}
	deps, daemon := touchDeps(t, driver)

	if _, errOut, err := executeCLI(t, deps, "sim", "type", "Hi 42"); err != nil {
		t.Fatalf("sim type failed: %v\nstderr=%s", err, errOut)
	}
	events := driver.calls()[0]
	var keys int
	for _, e := range events {
		if e.Kind == "touch" {
			t.Fatalf("typing must not touch the screen: %+v", events)
		}
		if e.Kind == "key" {
			keys++
		}
	}
	if keys == 0 {
		t.Fatal("no key events were sent")
	}
	assertHoldTakenAndReleased(t, daemon)
}

func TestSimSwipe_MovesAndAlwaysLifts(t *testing.T) {
	driver := &fakeSimDriver{}
	deps, _ := touchDeps(t, driver)

	if _, errOut, err := executeCLI(t, deps, "sim", "swipe", "0.5", "0.8", "0.5", "0.2"); err != nil {
		t.Fatalf("sim swipe failed: %v\nstderr=%s", err, errOut)
	}
	types := touchTypesOf(driver.calls()[0])
	if !strings.HasPrefix(types, "begin,move") || !strings.HasSuffix(types, "end") {
		t.Fatalf("swipe = %q, want begin, moves and a final end", types)
	}
}

func TestSimButton_OnlyKnownNamesReachTheDevice(t *testing.T) {
	driver := &fakeSimDriver{}
	deps, _ := touchDeps(t, driver)

	if _, _, err := executeCLI(t, deps, "sim", "button", "home"); err != nil {
		t.Fatalf("sim button home: %v", err)
	}
	if driver.calls()[0][0].Name != "swipe_home" {
		t.Fatalf("events = %+v", driver.calls()[0])
	}

	_, _, err := executeCLI(t, deps, "sim", "button", "hoem")
	if err == nil {
		t.Fatal("an unknown button must be refused: the device silently ignores it, which would look like success")
	}
	if len(driver.calls()) != 1 {
		t.Fatalf("a refused button must never reach the device: %+v", driver.calls())
	}
}

// --- the lease and the gesture hold ----------------------------------------

func TestSimTouch_RefusedWhenAnotherSessionHoldsTheDevice(t *testing.T) {
	driver := &fakeSimDriver{}
	deps, daemon := touchDeps(t, driver)
	daemon.holdStatus = http.StatusConflict
	daemon.holdBody = `{"error":"conflict","code":"SIM_DEVICE_BUSY","message":"leased elsewhere",` +
		`"details":{"reason":"leased_by_other","holder":"mer-3","expiresAt":"2026-08-13T07:48:02Z"}}`

	_, errOut, err := executeCLI(t, deps, "sim", "tap", "0.5", "0.5")
	if err == nil {
		t.Fatal("touching a device another session holds must fail")
	}
	if len(driver.calls()) != 0 {
		t.Fatalf("nothing may reach the device without the hold: %+v", driver.calls())
	}
	msg := err.Error() + errOut
	for _, want := range []string{"mer-3", "ao sim release", "shot"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("refusal %q must mention %q", msg, want)
		}
	}
}

func TestSimTouch_RefusedWhenThisSessionHasNotClaimed(t *testing.T) {
	driver := &fakeSimDriver{}
	deps, daemon := touchDeps(t, driver)
	daemon.holdStatus = http.StatusConflict
	daemon.holdBody = `{"error":"conflict","code":"SIM_DEVICE_BUSY","message":"not claimed",` +
		`"details":{"reason":"not_leased"}}`

	_, errOut, err := executeCLI(t, deps, "sim", "tap", "0.5", "0.5")
	if err == nil {
		t.Fatal("touching an unclaimed device must fail rather than claim it implicitly")
	}
	if !strings.Contains(err.Error()+errOut, "ao sim claim") {
		t.Fatalf("the refusal must say how to fix it: %v", err)
	}
	if len(driver.calls()) != 0 {
		t.Fatalf("nothing may reach the device: %+v", driver.calls())
	}
}

func TestSimTouch_RefusedWhileAnotherGestureIsInFlight(t *testing.T) {
	driver := &fakeSimDriver{}
	deps, daemon := touchDeps(t, driver)
	daemon.holdStatus = http.StatusConflict
	daemon.holdBody = `{"error":"conflict","code":"SIM_DEVICE_BUSY","message":"mid-gesture",` +
		`"details":{"reason":"busy"}}`

	_, errOut, err := executeCLI(t, deps, "sim", "tap", "0.5", "0.5")
	if err == nil {
		t.Fatal("a second gesture must be refused while one is in flight")
	}
	if !strings.Contains(err.Error()+errOut, "mid-gesture") {
		t.Fatalf("the refusal must say the device is mid-gesture: %v", err)
	}
}

func TestSimTouch_HoldIsTakenBeforeTheFirstEventAndHeldUntilTheLast(t *testing.T) {
	// The hold has to bracket the WHOLE gesture, not each event: a device has
	// one finger and no caller identity, so a second gesture that starts between
	// this one's begin and end merges into it.
	driver := &fakeSimDriver{}
	deps, daemon := touchDeps(t, driver)
	driver.onPerform = func() {
		if got := daemon.callLog(); !strings.Contains(got, "POST /api/v1/sessions/mer-9/sim-leases/"+simUDIDProMax+"/hold") {
			t.Errorf("the hold must already be taken when the gesture runs, calls: %s", got)
		}
		if strings.Contains(daemon.callLog(), "DELETE /api/v1/sessions/mer-9/sim-leases/"+simUDIDProMax+"/hold/") {
			t.Errorf("the hold must not be released before the gesture ends, calls: %s", daemon.callLog())
		}
	}

	if _, _, err := executeCLI(t, deps, "sim", "tap", "0.5", "0.5"); err != nil {
		t.Fatalf("sim tap: %v", err)
	}
	assertHoldTakenAndReleased(t, daemon)
}

func TestSimTouch_TheHoldIsSizedToOutliveItsOwnGesture(t *testing.T) {
	// A hold that lapsed mid-gesture would be exactly the window another command
	// needs to take the finger while this one is still touching the screen, so
	// the TTL is sized from the gesture rather than from a fixed guess.
	driver := &fakeSimDriver{}
	deps, daemon := touchDeps(t, driver)

	if _, _, err := executeCLI(t, deps, "sim", "swipe", "0.5", "0.8", "0.5", "0.2", "--duration", "4s"); err != nil {
		t.Fatalf("sim swipe: %v", err)
	}
	if got := daemon.requestedHoldSeconds(t); got < 4 {
		t.Fatalf("hold ttl = %ds, want it to cover the 4s swipe", got)
	}

	if _, _, err := executeCLI(t, deps, "sim", "type", strings.Repeat("a", 1500)); err != nil {
		t.Fatalf("sim type: %v", err)
	}
	if got := daemon.requestedHoldSeconds(t); got < 6 {
		t.Fatalf("hold ttl = %ds, want it to cover a long run of keystrokes", got)
	}
}

// --- a gesture that fails midway -------------------------------------------

func TestSimTouch_AFailedGestureLiftsTheFingerAndSaysSo(t *testing.T) {
	// The failure this whole slice is built around: a gesture that stops between
	// begin and end leaves the guest with a finger held down, wedging input until
	// the device is rebooted.
	driver := &fakeSimDriver{performErr: errors.New("bridge died mid-gesture")}
	deps, daemon := touchDeps(t, driver)

	_, errOut, err := executeCLI(t, deps, "sim", "swipe", "0.5", "0.8", "0.5", "0.2")
	if err == nil {
		t.Fatal("a failed gesture must fail the command")
	}
	calls := driver.calls()
	if len(calls) != 2 {
		t.Fatalf("a failed gesture must be followed by a recovery lift, got %d calls", len(calls))
	}
	lift := calls[1]
	if len(lift) != 1 || lift[0].Kind != "touch" || lift[0].Type != "end" {
		t.Fatalf("recovery = %+v, want a single touch end", lift)
	}
	if lift[0].X != 0.5 || lift[0].Y != 0.2 {
		t.Fatalf("the lift must land where the gesture was heading, got %+v", lift[0])
	}
	if !strings.Contains(err.Error()+errOut, "released") {
		t.Fatalf("the failure must say the finger was released: %v", err)
	}
	// And the device must not stay locked out by a hold nobody owns.
	assertHoldTakenAndReleased(t, daemon)
}

func TestSimTouch_WhenEvenTheRecoveryLiftFailsTheHumanIsWarnedLoudly(t *testing.T) {
	// The worst case: the gesture died AND the release could not be delivered.
	// The device may genuinely have a finger held down, and silence here is how
	// somebody else's simulator ends up wedged with no explanation.
	driver := &fakeSimDriver{performErr: errors.New("bridge died mid-gesture"), failEvery: true}
	deps, daemon := touchDeps(t, driver)

	_, errOut, err := executeCLI(t, deps, "sim", "swipe", "0.5", "0.8", "0.5", "0.2")
	if err == nil {
		t.Fatal("a failed gesture must fail the command")
	}
	msg := err.Error() + errOut
	for _, want := range []string{"finger held down", "reboot"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("warning %q must mention %q", msg, want)
		}
	}
	assertHoldTakenAndReleased(t, daemon)
}

func TestSimTouch_ReportsWhenTheBridgeHadToRescueTheFinger(t *testing.T) {
	driver := &fakeSimDriver{result: simbridge.PerformResult{Lifted: true, LiftReason: "gesture ended without a lift"}}
	deps, _ := touchDeps(t, driver)

	out, errOut, err := executeCLI(t, deps, "sim", "tap", "0.5", "0.5")
	if err != nil {
		t.Fatalf("sim tap: %v", err)
	}
	if !strings.Contains(out+errOut, "released a touch") {
		t.Fatalf("a rescued finger must be reported, not swallowed: %s%s", out, errOut)
	}
}

// --- the mechanism failing -------------------------------------------------

func TestSimTouch_BridgeUnavailableIsActionable(t *testing.T) {
	deps, _ := touchDeps(t, nil)
	deps.SimDriver = func(string) (simbridge.Driver, error) {
		return nil, &simbridge.Error{
			Message: "node was not found on PATH",
			Advice:  "Install Node.js 20 or newer. `ao sim shot` and `ao sim list` do not need it.",
		}
	}

	_, errOut, err := executeCLI(t, deps, "sim", "tap", "0.5", "0.5")
	if err == nil {
		t.Fatal("a missing bridge must fail the command")
	}
	if !strings.Contains(err.Error()+errOut, "Install Node.js") {
		t.Fatalf("err = %v, want the advice passed through", err)
	}
}

func TestSimTouch_RefusesADeviceThatIsNotBooted(t *testing.T) {
	driver := &fakeSimDriver{}
	deps, _ := touchDeps(t, driver)

	_, _, err := executeCLI(t, deps, "sim", "tap", "0.5", "0.5", "--udid", simUDIDPro)
	if err == nil || !strings.Contains(err.Error(), "not booted") {
		t.Fatalf("err = %v, want a not-booted refusal", err)
	}
	if len(driver.calls()) != 0 {
		t.Fatalf("nothing may be sent to a shut-down device: %+v", driver.calls())
	}
}

func TestSimTouch_CoordinatesOffTheScreenAreUsageErrors(t *testing.T) {
	driver := &fakeSimDriver{}
	deps, _ := touchDeps(t, driver)

	for _, args := range [][]string{
		{"sim", "tap", "1.5", "0.5"},
		{"sim", "tap", "half", "0.5"},
		{"sim", "swipe", "0.5", "0.5", "0.5", "-2"},
	} {
		_, _, err := executeCLI(t, deps, args...)
		if err == nil {
			t.Fatalf("%v must be refused", args)
		}
		if !errors.As(err, &usageError{}) {
			t.Fatalf("%v: err = %v, want a usage error (exit 2)", args, err)
		}
	}
	if len(driver.calls()) != 0 {
		t.Fatalf("a rejected argument must never reach the device: %+v", driver.calls())
	}
}

func TestSimTouch_RequiresASession(t *testing.T) {
	driver := &fakeSimDriver{}
	deps, _ := touchDeps(t, driver)
	t.Setenv("AO_SESSION_ID", "")

	_, _, err := executeCLI(t, deps, "sim", "tap", "0.5", "0.5")
	if err == nil || !strings.Contains(err.Error(), "AO_SESSION_ID") {
		t.Fatalf("err = %v, want a refusal naming AO_SESSION_ID", err)
	}
}

func TestSimTouch_UnreachableDaemonRefusesRatherThanTouching(t *testing.T) {
	// Read-only commands degrade when the daemon is down. Touching must not:
	// without the daemon there is no hold, and without a hold two commands can
	// merge into one finger.
	driver := &fakeSimDriver{}
	deps, _ := touchDeps(t, driver)
	deps.ProcessAlive = func(int) bool { return false }

	_, _, err := executeCLI(t, deps, "sim", "tap", "0.5", "0.5")
	if err == nil {
		t.Fatal("no daemon means no hold, which must mean no touch")
	}
	if len(driver.calls()) != 0 {
		t.Fatalf("nothing may reach the device: %+v", driver.calls())
	}
}

func assertHoldTakenAndReleased(t *testing.T, daemon *simDaemon) {
	t.Helper()
	log := daemon.callLog()
	if !strings.Contains(log, "POST /api/v1/sessions/mer-9/sim-leases/"+simUDIDProMax+"/hold") {
		t.Fatalf("no gesture hold was taken: %s", log)
	}
	if !strings.Contains(log, "DELETE /api/v1/sessions/mer-9/sim-leases/"+simUDIDProMax+"/hold/") {
		t.Fatalf("the gesture hold was never released: %s", log)
	}
}

// A drag through several points is the gesture the desktop pane sends when a
// human holds the pointer down and moves it. Before this the CLI could only
// send whole swipes, so an agent could not follow a route at all - and a route
// composed of separate swipes lifts between legs, which an app reads as several
// flicks rather than one drag.
func TestSimDrag_IsOneTouchThroughEveryPoint(t *testing.T) {
	driver := &fakeSimDriver{}
	deps, daemon := touchDeps(t, driver)

	if _, errOut, err := executeCLI(t, deps, "sim", "drag", "0.5", "0.8", "0.5", "0.4", "0.2", "0.4"); err != nil {
		t.Fatalf("sim drag failed: %v\nstderr=%s", err, errOut)
	}
	events := driver.calls()[0]
	types := touchTypesOf(events)
	if strings.Count(types, "begin") != 1 || strings.Count(types, "end") != 1 {
		t.Fatalf("drag = %q, want one begin and one end", types)
	}
	visited := false
	for _, e := range events {
		if e.Kind == "touch" && e.X == 0.5 && e.Y == 0.4 {
			visited = true
		}
	}
	if !visited {
		t.Fatalf("the middle waypoint was never reached: %+v", events)
	}
	assertHoldTakenAndReleased(t, daemon)
}

// The arguments are points, so an odd count is a point with no Y - refused as a
// usage error rather than sent as something the human did not mean.
func TestSimDrag_RefusesArgumentsThatAreNotPoints(t *testing.T) {
	driver := &fakeSimDriver{}
	deps, _ := touchDeps(t, driver)

	for _, args := range [][]string{
		{"sim", "drag", "0.5", "0.8", "0.5"},
		{"sim", "drag", "0.5", "0.8"},
		{"sim", "drag", "0.5", "0.8", "0.5", "1.4"},
	} {
		_, _, err := executeCLI(t, deps, args...)
		if err == nil {
			t.Fatalf("%v must be refused", args)
		}
		if !errors.As(err, &usageError{}) {
			t.Fatalf("%v: err = %v, want a usage error (exit 2)", args, err)
		}
	}
	if len(driver.calls()) != 0 {
		t.Fatalf("a rejected drag must never reach the device: %+v", driver.calls())
	}
}
