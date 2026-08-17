package cli

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/simbridge"
)

// fixtureSnapshot is a small screen with nesting, an enabled control and a
// disabled one - enough to exercise everything `ao sim ax` promises.
func fixtureSnapshot() simbridge.Snapshot {
	return simbridge.Snapshot{
		Screen:    simbridge.Size{Width: 440, Height: 956},
		Frontmost: simbridge.Frontmost{BundleID: "com.example.app", PID: 42},
		Elements: []simbridge.Element{{
			Path: "0", Type: "Application", Enabled: true,
			Frame: simbridge.Rect{Width: 440, Height: 956},
			Tap:   &simbridge.Point{X: 0.5, Y: 0.5},
			Box:   &simbridge.Box{X2: 1, Y2: 1},
			Children: []simbridge.Element{
				{
					Path: "0.0", Type: "TextField", Role: "text field", Label: "Search", ID: "search-field",
					Enabled: true,
					Frame:   simbridge.Rect{X: 20, Y: 100, Width: 400, Height: 40},
					Tap:     &simbridge.Point{X: 0.5, Y: 0.12552301255230125},
					Box:     &simbridge.Box{X1: 20.0 / 440, Y1: 100.0 / 956, X2: 420.0 / 440, Y2: 140.0 / 956},
				},
				{
					Path: "0.1", Type: "Button", Role: "button", Label: "Continue", Value: "disabled",
					Enabled: false,
					Frame:   simbridge.Rect{X: 20, Y: 800, Width: 400, Height: 50},
					Tap:     &simbridge.Point{X: 0.5, Y: 0.8629707112970712},
					Box:     &simbridge.Box{X1: 20.0 / 440, Y1: 800.0 / 956, X2: 420.0 / 440, Y2: 850.0 / 956},
				},
			},
		}},
		NodeCount:      3,
		TotalNodeCount: 3,
		OnScreenCount:  3,
	}
}

// scrolledSnapshot is the ordinary case on a real app: a row on screen and one
// below the fold, which has edges but nowhere to touch.
func scrolledSnapshot() simbridge.Snapshot {
	snap := fixtureSnapshot()
	snap.Elements[0].Children = append(snap.Elements[0].Children, simbridge.Element{
		Path: "0.2", Type: "Button", Role: "button", Label: "See all", Enabled: true,
		Frame:     simbridge.Rect{X: 340, Y: 1300, Width: 80, Height: 30},
		Box:       &simbridge.Box{X1: 340.0 / 440, Y1: 1300.0 / 956, X2: 420.0 / 440, Y2: 1330.0 / 956},
		OffScreen: true,
	})
	snap.NodeCount, snap.TotalNodeCount = 4, 4
	snap.OnScreenCount, snap.OffScreenCount = 3, 1
	return snap
}

func TestSimAX_JSONCarriesTapPointsAndTheTree(t *testing.T) {
	driver := &fakeSimDriver{snapshot: fixtureSnapshot()}
	deps, _ := touchDeps(t, driver)

	out, errOut, err := executeCLI(t, deps, "sim", "ax", "--json")
	if err != nil {
		t.Fatalf("sim ax --json failed: %v\nstderr=%s", err, errOut)
	}
	var got struct {
		simbridge.Snapshot
		UDID  string       `json:"udid"`
		Name  string       `json:"name"`
		Lease simLeaseView `json:"lease"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	if got.UDID != simUDIDProMax || got.Name != "iPhone 17 Pro Max" {
		t.Fatalf("the tree must say which device it came from: %s", out)
	}
	if len(got.Elements) != 1 || len(got.Elements[0].Children) != 2 {
		t.Fatalf("the tree must keep its shape: %s", out)
	}
	search := got.Elements[0].Children[0]
	if search.Tap.X == 0 || search.Tap.Y == 0 {
		t.Fatalf("every element needs a precomputed tap point: %+v", search)
	}
	if got.Frontmost.BundleID != "com.example.app" {
		t.Fatalf("frontmost app missing: %s", out)
	}
	if got.Lease.State == "" {
		t.Fatalf("a read must still report who holds the device: %s", out)
	}
}

func TestSimAX_TextOutputIsReadableAndCarriesTheTapCommand(t *testing.T) {
	driver := &fakeSimDriver{snapshot: fixtureSnapshot()}
	deps, _ := touchDeps(t, driver)

	out, _, err := executeCLI(t, deps, "sim", "ax")
	if err != nil {
		t.Fatalf("sim ax: %v", err)
	}
	for _, want := range []string{"Search", "Continue", "com.example.app", "0.500", "disabled", "ao sim tap"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output must contain %q:\n%s", want, out)
		}
	}
	// Nesting has to be visible, or an agent cannot tell what belongs to what.
	if !strings.Contains(out, "\n  ") {
		t.Fatalf("the tree must be indented:\n%s", out)
	}
}

func TestSimAX_NeedsNoLeaseButSaysWhoHoldsTheDevice(t *testing.T) {
	// Reading is read-only against HID: it cannot corrupt anyone's gesture, and
	// a cheap unblocked read is the whole point. It still has to stop an agent
	// reading a screen and assuming the device is its to drive.
	driver := &fakeSimDriver{snapshot: fixtureSnapshot()}
	deps, daemon := touchDeps(t, driver)
	daemon.leases[simUDIDProMax] = simLeaseClient{
		UDID: simUDIDProMax, SessionID: "mer-3", AcquiredAt: simFixedNow, ExpiresAt: simFixedNow.Add(7 * 60 * 1e9),
	}

	out, _, err := executeCLI(t, deps, "sim", "ax")
	if err != nil {
		t.Fatalf("a read must not be blocked by someone else's lease: %v", err)
	}
	if !strings.Contains(out, "mer-3") || !strings.Contains(out, "do NOT drive") {
		t.Fatalf("the read must name the holder and warn: %s", out)
	}
	if strings.Contains(daemon.callLog(), "/hold") {
		t.Fatalf("reading must not take a gesture hold: %s", daemon.callLog())
	}
}

func TestSimAX_WorksWithoutADaemon(t *testing.T) {
	driver := &fakeSimDriver{snapshot: fixtureSnapshot()}
	deps, _ := touchDeps(t, driver)
	deps.ProcessAlive = func(int) bool { return false }

	out, _, err := executeCLI(t, deps, "sim", "ax")
	if err != nil {
		t.Fatalf("a read must survive a daemon that is not running: %v", err)
	}
	if !strings.Contains(out, "Search") {
		t.Fatalf("output = %s", out)
	}
}

func TestSimAX_CapsTheTreeAndSaysHowMuchWasDropped(t *testing.T) {
	driver := &fakeSimDriver{snapshot: fixtureSnapshot()}
	deps, _ := touchDeps(t, driver)

	out, _, err := executeCLI(t, deps, "sim", "ax", "--max-nodes", "2")
	if err != nil {
		t.Fatalf("sim ax: %v", err)
	}
	if !strings.Contains(out, "--max-nodes") || !strings.Contains(out, "3") {
		t.Fatalf("a truncated tree must say how big it really was and how to see the rest:\n%s", out)
	}
}

func TestSimAX_EmptyTreeExplainsItselfWithTheFrontmostApp(t *testing.T) {
	// "No elements" is never a finding to report: it means accessibility gave
	// nothing back, and the frontmost bundle usually says why.
	driver := &fakeSimDriver{snapshot: simbridge.Snapshot{
		Frontmost: simbridge.Frontmost{BundleID: "com.apple.springboard", PID: 1},
	}}
	deps, _ := touchDeps(t, driver)

	_, _, err := executeCLI(t, deps, "sim", "ax")
	if err == nil {
		t.Fatal("an empty tree must fail rather than read as an empty screen")
	}
	for _, want := range []string{"com.apple.springboard", "ao sim shot"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("err = %v, want it to mention %q", err, want)
		}
	}
}

func TestSimAX_BridgeFailureIsPassedThroughWithItsAdvice(t *testing.T) {
	driver := &fakeSimDriver{axErr: &simbridge.Error{
		Code:    "addon_load_failed",
		Message: "the simulator bridge could not load: symbol not found",
		Advice:  "This bridge calls private Apple frameworks, so an Xcode upgrade can break it.",
	}}
	deps, _ := touchDeps(t, driver)

	_, _, err := executeCLI(t, deps, "sim", "ax")
	if err == nil {
		t.Fatal("a broken bridge must fail the command")
	}
	if !strings.Contains(err.Error(), "private Apple frameworks") {
		t.Fatalf("err = %v, want the advice kept", err)
	}
}

func TestSimAX_RefusesADeviceThatIsNotBooted(t *testing.T) {
	driver := &fakeSimDriver{snapshot: fixtureSnapshot()}
	deps, _ := touchDeps(t, driver)

	_, _, err := executeCLI(t, deps, "sim", "ax", "--udid", simUDIDPro)
	if err == nil || !strings.Contains(err.Error(), "not booted") {
		t.Fatalf("err = %v, want a not-booted refusal", err)
	}
}

func TestSimAX_MaxNodesMustBePositive(t *testing.T) {
	driver := &fakeSimDriver{snapshot: fixtureSnapshot()}
	deps, _ := touchDeps(t, driver)

	_, _, err := executeCLI(t, deps, "sim", "ax", "--max-nodes", "0")
	if !errors.As(err, &usageError{}) {
		t.Fatalf("err = %v, want a usage error", err)
	}
}

// Measured on a real app screen: 60 of 103 elements were off screen and every
// one still printed a tap point, clamped onto the screen's edge. A tap meant
// for a row below the fold went to the bottom row of pixels instead. The line
// has to say there is nowhere to touch - and still say where the thing is, so
// the answer "scroll down" is available without a second read.
func TestSimAX_OffScreenElementsSayThereIsNowhereToTap(t *testing.T) {
	driver := &fakeSimDriver{snapshot: scrolledSnapshot()}
	deps, _ := touchDeps(t, driver)

	out, errOut, err := executeCLI(t, deps, "sim", "ax")
	if err != nil {
		t.Fatalf("sim ax failed: %v\nstderr=%s", err, errOut)
	}
	var line string
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "See all") {
			line = l
		}
	}
	if line == "" {
		t.Fatalf("the off-screen element must still be listed:\n%s", out)
	}
	if strings.Contains(line, "tap ") {
		t.Fatalf("an element that cannot be touched must not offer a tap point: %q", line)
	}
	if !strings.Contains(line, "off screen") {
		t.Fatalf("line = %q, want it to say the element is off screen", line)
	}
	if !strings.Contains(line, "box ") {
		t.Fatalf("line = %q, want the edges so the caller knows how far to scroll", line)
	}
}

// An element is a rectangle; its edges are what say whether a target is a whole
// card or a chevron at the end of one.
func TestSimAX_EveryLineCarriesTheElementsEdges(t *testing.T) {
	driver := &fakeSimDriver{snapshot: fixtureSnapshot()}
	deps, _ := touchDeps(t, driver)

	out, _, err := executeCLI(t, deps, "sim", "ax")
	if err != nil {
		t.Fatalf("sim ax failed: %v", err)
	}
	for _, l := range strings.Split(out, "\n") {
		if !strings.Contains(l, "Search") {
			continue
		}
		// left,top->right,bottom in the same 0..1 units as the tap point.
		if !strings.Contains(l, "box 0.045,0.105->0.955,0.146") {
			t.Fatalf("line = %q, want the element's four edges", l)
		}
		return
	}
	t.Fatalf("no line for the search field:\n%s", out)
}

// Without the split, nothing on a scrolling screen says there is more below.
func TestSimAX_SaysHowMuchOfTheScreenIsWithinReach(t *testing.T) {
	driver := &fakeSimDriver{snapshot: scrolledSnapshot()}
	deps, _ := touchDeps(t, driver)

	out, _, err := executeCLI(t, deps, "sim", "ax")
	if err != nil {
		t.Fatalf("sim ax failed: %v", err)
	}
	if !strings.Contains(out, "3 on screen") || !strings.Contains(out, "1 off screen") {
		t.Fatalf("output does not say what is reachable:\n%s", out)
	}
}

// Observed on a real device: for a second after an app is brought to the front
// the tree is the status bar and nothing else. Reported as an ordinary read, an
// agent concludes the app is blank. One more read is what it costs to find out.
func TestSimAX_ReadsAgainWhenOnlyTheStatusBarCameBack(t *testing.T) {
	settling := fixtureSnapshot()
	settling.OnlyStatusBar = true
	driver := &fakeSimDriver{snapshotQueue: []simbridge.Snapshot{settling}, snapshot: fixtureSnapshot()}
	deps, _ := touchDeps(t, driver)

	out, _, err := executeCLI(t, deps, "sim", "ax")
	if err != nil {
		t.Fatalf("sim ax failed: %v", err)
	}
	if driver.reads() != 2 {
		t.Fatalf("reads = %d, want a second read after a status-bar-only tree", driver.reads())
	}
	if strings.Contains(out, "status bar") {
		t.Fatalf("the second read settled, so nothing needs saying:\n%s", out)
	}
}

func TestSimAX_SaysSoWhenTheScreenNeverSettles(t *testing.T) {
	settling := fixtureSnapshot()
	settling.OnlyStatusBar = true
	driver := &fakeSimDriver{snapshot: settling}
	deps, _ := touchDeps(t, driver)

	out, _, err := executeCLI(t, deps, "sim", "ax")
	if err != nil {
		t.Fatalf("sim ax failed: %v", err)
	}
	if !strings.Contains(out, "status bar") || !strings.Contains(out, "ao sim shot") {
		t.Fatalf("a tree of nothing but furniture must say so, and what to do:\n%s", out)
	}
}

func TestSimAX_FormatMaestroEmitsSelectorsPerElement(t *testing.T) {
	driver := &fakeSimDriver{snapshot: scrolledSnapshot()}
	deps, _ := touchDeps(t, driver)

	out, _, err := executeCLI(t, deps, "sim", "ax", "--format", "maestro")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	// The header still says what screen this is, because a selector list with
	// no idea which app it came from is not actionable.
	if !strings.Contains(out, "com.example.app") {
		t.Errorf("missing the foreground app header:\n%s", out)
	}
	if !strings.Contains(out, `- tapOn: "Search"`) {
		t.Errorf("missing the unique-label selector:\n%s", out)
	}
	// "See all" is below the fold in scrolledSnapshot.
	if !strings.Contains(out, "- scrollUntilVisible:") {
		t.Errorf("missing the off-screen scroll stanza:\n%s", out)
	}
}

func TestSimAX_FormatMaestroAndJSONTogetherIsRefused(t *testing.T) {
	driver := &fakeSimDriver{snapshot: fixtureSnapshot()}
	deps, _ := touchDeps(t, driver)

	_, _, err := executeCLI(t, deps, "sim", "ax", "--json", "--format", "maestro")
	if err == nil {
		t.Fatal("want an error when --json and --format disagree")
	}
	if !strings.Contains(err.Error(), "--json") || !strings.Contains(err.Error(), "--format") {
		t.Errorf("error must name both flags, got %q", err)
	}
}

func TestSimAX_UnknownFormatIsRefusedAndListsTheValid(t *testing.T) {
	driver := &fakeSimDriver{snapshot: fixtureSnapshot()}
	deps, _ := touchDeps(t, driver)

	_, _, err := executeCLI(t, deps, "sim", "ax", "--format", "yaml")
	if err == nil {
		t.Fatal("want an error for an unknown --format")
	}
	for _, want := range []string{"text", "json", "maestro"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error must list %q, got %q", want, err)
		}
	}
}

func TestSimAX_JSONFlagStillWorksUnchanged(t *testing.T) {
	driver := &fakeSimDriver{snapshot: fixtureSnapshot()}
	deps, _ := touchDeps(t, driver)

	out, _, err := executeCLI(t, deps, "sim", "ax", "--json")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	var got simAXResult
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("output is not the JSON payload it was before: %v", err)
	}
}
