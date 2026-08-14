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
			Tap:   simbridge.Point{X: 0.5, Y: 0.5},
			Children: []simbridge.Element{
				{
					Path: "0.0", Type: "TextField", Role: "text field", Label: "Search", ID: "search-field",
					Enabled: true,
					Frame:   simbridge.Rect{X: 20, Y: 100, Width: 400, Height: 40},
					Tap:     simbridge.Point{X: 0.5, Y: 0.12552301255230125},
				},
				{
					Path: "0.1", Type: "Button", Role: "button", Label: "Continue", Value: "disabled",
					Enabled: false,
					Frame:   simbridge.Rect{X: 20, Y: 800, Width: 400, Height: 50},
					Tap:     simbridge.Point{X: 0.5, Y: 0.8629707112970712},
				},
			},
		}},
		NodeCount:      3,
		TotalNodeCount: 3,
	}
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
