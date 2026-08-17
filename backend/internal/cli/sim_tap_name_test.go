package cli

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/simbridge"
)

// namedScreen is a screen with the shapes that make naming a target
// interesting: a control whose label is repeated by the text inside it, two
// labels where one contains the other, a field whose text is a value rather
// than a label, a disabled control, and a row below the fold.
func namedScreen() simbridge.Snapshot {
	return simbridge.Snapshot{
		Screen: simbridge.Size{Width: 440, Height: 956},
		Elements: []simbridge.Element{{
			Path: "0", Type: "Application", Enabled: true,
			Frame: simbridge.Rect{Width: 440, Height: 956},
			Tap:   &simbridge.Point{X: 0.5, Y: 0.5},
			Children: []simbridge.Element{
				{
					Path: "0.0", Type: "TextField", ID: "email-field", Value: "hello@example.com",
					Enabled: true, Tap: &simbridge.Point{X: 0.5, Y: 0.12},
				},
				{
					Path: "0.1", Type: "Button", Label: "Continue", Enabled: true,
					Tap: &simbridge.Point{X: 0.5, Y: 0.86},
					Children: []simbridge.Element{{
						Path: "0.1.0", Type: "StaticText", Label: "Continue", Enabled: true,
						Tap: &simbridge.Point{X: 0.5, Y: 0.86},
					}},
				},
				{
					Path: "0.2", Type: "Button", Label: "Continue later", Enabled: true,
					Tap: &simbridge.Point{X: 0.5, Y: 0.5},
				},
				{
					Path: "0.3", Type: "Button", Label: "Save draft", ID: "save-button",
					Enabled: false, Tap: &simbridge.Point{X: 0.5, Y: 0.7},
				},
				{
					Path: "0.4", Type: "Button", Label: "See all", Enabled: true,
					OffScreen: true, Box: &simbridge.Box{X1: 0.8, Y1: 1.01, X2: 0.96, Y2: 1.04},
				},
			},
		}},
		NodeCount: 6, TotalNodeCount: 6, OnScreenCount: 5, OffScreenCount: 1,
	}
}

func namedScreenDeps(t *testing.T) (Deps, *fakeSimDriver, *simDaemon) {
	t.Helper()
	driver := &fakeSimDriver{snapshot: namedScreen()}
	deps, daemon := touchDeps(t, driver)
	return deps, driver, daemon
}

func TestSimTap_ByLabelTapsTheElementsOwnPoint(t *testing.T) {
	deps, driver, daemon := namedScreenDeps(t)

	out, errOut, err := executeCLI(t, deps, "sim", "tap", "--label", "Continue")
	if err != nil {
		t.Fatalf("sim tap --label failed: %v\nstderr=%s", err, errOut)
	}
	calls := driver.calls()
	if len(calls) != 1 {
		t.Fatalf("driver saw %d gestures, want 1", len(calls))
	}
	if calls[0][0].X != 0.5 || calls[0][0].Y != 0.86 {
		t.Fatalf("tapped %+v, want the element's own point", calls[0][0])
	}
	// What was tapped, not just that something was: a caller that named a thing
	// has to be able to check the tool agreed with them.
	for _, want := range []string{"Continue", "Button", "0.500", "0.860"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output must say what it matched (%q):\n%s", want, out)
		}
	}
	assertHoldTakenAndReleased(t, daemon)
}

func TestSimTap_ByLabelIsCaseInsensitive(t *testing.T) {
	deps, driver, _ := namedScreenDeps(t)

	if _, _, err := executeCLI(t, deps, "sim", "tap", "--label", "continue"); err != nil {
		t.Fatalf("sim tap: %v", err)
	}
	if got := driver.calls()[0][0]; got.Y != 0.86 {
		t.Fatalf("tapped %+v, want the button", got)
	}
}

func TestSimTap_AnExactNameBeatsALongerOneContainingIt(t *testing.T) {
	deps, driver, _ := namedScreenDeps(t)

	if _, _, err := executeCLI(t, deps, "sim", "tap", "--label", "Continue"); err != nil {
		t.Fatalf("sim tap: %v", err)
	}
	if got := driver.calls()[0][0]; got.Y == 0.5 {
		t.Fatal("tapped \"Continue later\" for the name \"Continue\"")
	}
}

func TestSimTap_FallsBackToAContainsMatchAndSaysThatIsWhatItDid(t *testing.T) {
	deps, driver, _ := namedScreenDeps(t)

	out, _, err := executeCLI(t, deps, "sim", "tap", "--label", "later")
	if err != nil {
		t.Fatalf("sim tap: %v", err)
	}
	if got := driver.calls()[0][0]; got.Y != 0.5 {
		t.Fatalf("tapped %+v, want the only element containing the text", got)
	}
	// A contains-match is the tool's inference, not the name given, and it says
	// so - otherwise "it tapped something" reads as "it found what I named".
	if !strings.Contains(out, "Continue later") || !strings.Contains(strings.ToLower(out), "contain") {
		t.Fatalf("output must say the name was not an exact match:\n%s", out)
	}
}

func TestSimTap_ByAccessibilityID(t *testing.T) {
	deps, driver, _ := namedScreenDeps(t)

	out, _, err := executeCLI(t, deps, "sim", "tap", "--id", "email-field")
	if err != nil {
		t.Fatalf("sim tap --id: %v", err)
	}
	if got := driver.calls()[0][0]; got.Y != 0.12 {
		t.Fatalf("tapped %+v, want the field with that identifier", got)
	}
	if !strings.Contains(out, "email-field") {
		t.Fatalf("output must name the identifier it matched:\n%s", out)
	}
}

func TestSimTap_ALabelIsNotAnIdentifier(t *testing.T) {
	// The two namespaces stay apart: an app's identifiers are set for
	// automation, labels are copy that changes.
	deps, driver, _ := namedScreenDeps(t)

	_, _, err := executeCLI(t, deps, "sim", "tap", "--id", "Continue")
	if err == nil {
		t.Fatal("a label must not be matched as an identifier")
	}
	if len(driver.calls()) != 0 {
		t.Fatal("nothing may reach the device when the name resolved to nothing")
	}
}

func TestSimTap_ByLabelFindsAFieldByTheValueItShows(t *testing.T) {
	// It is what `ao sim ax` prints for an element with no label, so it is the
	// name the caller read off the screen.
	deps, driver, _ := namedScreenDeps(t)

	if _, _, err := executeCLI(t, deps, "sim", "tap", "--label", "hello@example.com"); err != nil {
		t.Fatalf("sim tap: %v", err)
	}
	if got := driver.calls()[0][0]; got.Y != 0.12 {
		t.Fatalf("tapped %+v, want the field whose value is that text", got)
	}
}

func TestSimTap_SeveralDifferentElementsAreRefusedAndListed(t *testing.T) {
	driver := &fakeSimDriver{snapshot: namedScreen()}
	driver.snapshot.Elements[0].Children = append(driver.snapshot.Elements[0].Children, simbridge.Element{
		Path: "0.5", Type: "Button", Label: "Continue", Enabled: true,
		Tap: &simbridge.Point{X: 0.5, Y: 0.2},
	})
	deps, _ := touchDeps(t, driver)

	_, _, err := executeCLI(t, deps, "sim", "tap", "--label", "Continue")
	if err == nil {
		t.Fatal("two different elements with one name must not be guessed between")
	}
	got := err.Error()
	// Both candidates, with the point that picks each: the caller resolves the
	// ambiguity in one more command rather than reading the tree again.
	for _, want := range []string{"0.860", "0.200", "ao sim tap"} {
		if !strings.Contains(got, want) {
			t.Fatalf("err must list the candidates and how to pick one (%q):\n%v", want, got)
		}
	}
	if len(driver.calls()) != 0 {
		t.Fatal("nothing may reach the device while the target is ambiguous")
	}
}

func TestSimTap_ANameNothingAnswersToListsWhatIsOnScreen(t *testing.T) {
	deps, driver, _ := namedScreenDeps(t)

	_, _, err := executeCLI(t, deps, "sim", "tap", "--label", "Ghost")
	if err == nil {
		t.Fatal("a name nothing answers to must fail")
	}
	got := err.Error()
	for _, want := range []string{"Ghost", "Continue", "Save draft"} {
		if !strings.Contains(got, want) {
			t.Fatalf("err must say what IS on screen (%q):\n%v", want, got)
		}
	}
	if len(driver.calls()) != 0 {
		t.Fatal("nothing may reach the device when the name matched nothing")
	}
}

func TestSimTap_AnElementBelowTheFoldSaysToScrollToItFirst(t *testing.T) {
	// Found but unreachable is a different answer from not found: the element
	// exists, and the way to it is a scroll, not a different name.
	deps, driver, _ := namedScreenDeps(t)

	_, _, err := executeCLI(t, deps, "sim", "tap", "--label", "See all")
	if err == nil {
		t.Fatal("an element with nowhere to touch must not be tapped")
	}
	got := err.Error()
	for _, want := range []string{"See all", "off screen", "ao sim drag", "1.01"} {
		if !strings.Contains(got, want) {
			t.Fatalf("err must explain that it is below the fold and how far (%q):\n%v", want, got)
		}
	}
	if len(driver.calls()) != 0 {
		t.Fatal("nothing may reach the device for an element that cannot be touched")
	}
}

func TestSimTap_ADisabledElementIsRefusedWithAWayToOverrideIt(t *testing.T) {
	deps, driver, _ := namedScreenDeps(t)

	_, _, err := executeCLI(t, deps, "sim", "tap", "--label", "Save draft")
	if err == nil {
		t.Fatal("tapping a disabled control reports success and does nothing; it must be refused")
	}
	got := err.Error()
	if !strings.Contains(got, "disabled") {
		t.Fatalf("err = %v, want it to say the control is disabled", got)
	}
	// Refusing must not be a dead end: the app may be wrong about the state.
	if !strings.Contains(got, "ao sim tap 0.500 0.700") {
		t.Fatalf("err = %v, want the coordinate that overrides the refusal", got)
	}
	if len(driver.calls()) != 0 {
		t.Fatal("nothing may reach the device for a disabled control")
	}
}

func TestSimTap_CoordinatesNeverReadTheScreen(t *testing.T) {
	// The fast form stays fast: naming a target costs an accessibility read,
	// and a caller that already has the point must not pay for it.
	deps, driver, _ := namedScreenDeps(t)

	if _, _, err := executeCLI(t, deps, "sim", "tap", "0.5", "0.934"); err != nil {
		t.Fatalf("sim tap: %v", err)
	}
	if driver.reads() != 0 {
		t.Fatalf("the coordinate form read the screen %d time(s); it must not", driver.reads())
	}
}

func TestSimTap_TheScreenIsReadUnderTheHold(t *testing.T) {
	// Ordering that decides whether the tap lands where it was read: if the
	// screen is read before the hold is taken, another command can move it in
	// between, and the point this one touches belongs to a screen that is gone.
	driver := &fakeSimDriver{snapshot: namedScreen()}
	deps, daemon := touchDeps(t, driver)
	var whenRead string
	driver.onAX = func() { whenRead = daemon.callLog() }

	if _, _, err := executeCLI(t, deps, "sim", "tap", "--label", "Continue"); err != nil {
		t.Fatalf("sim tap: %v", err)
	}
	if !strings.Contains(whenRead, "/hold") {
		t.Fatalf("the screen was read before the hold was taken: daemon calls at that point were %q", whenRead)
	}
}

func TestSimTap_UsageErrors(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "a name and a point are two different ways to say the same thing",
			args: []string{"sim", "tap", "--label", "Continue", "0.5", "0.9"},
			want: "--label",
		},
		{name: "two names at once", args: []string{"sim", "tap", "--label", "Continue", "--id", "go"}, want: "--id"},
		{name: "an empty name matches everything", args: []string{"sim", "tap", "--label", "  "}, want: "--label"},
		{name: "half a coordinate", args: []string{"sim", "tap", "0.5"}, want: "tap"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			deps, driver, _ := namedScreenDeps(t)
			_, _, err := executeCLI(t, deps, tc.args...)
			if !errors.As(err, &usageError{}) {
				t.Fatalf("err = %v, want a usage error (exit 2)", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to name %q", err, tc.want)
			}
			if len(driver.calls()) != 0 {
				t.Fatal("nothing may reach the device on misuse")
			}
		})
	}
}

func TestSimTap_JSONCarriesWhatItMatched(t *testing.T) {
	deps, _, _ := namedScreenDeps(t)

	out, _, err := executeCLI(t, deps, "sim", "tap", "--label", "Continue", "--json")
	if err != nil {
		t.Fatalf("sim tap --json: %v", err)
	}
	var got struct {
		Action string `json:"action"`
		Detail string `json:"detail"`
		Target *struct {
			Selector  string  `json:"selector"`
			Kind      string  `json:"kind"`
			MatchedBy string  `json:"matchedBy"`
			Path      string  `json:"path"`
			Label     string  `json:"label"`
			Type      string  `json:"type"`
			X         float64 `json:"x"`
			Y         float64 `json:"y"`
		} `json:"target"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	if got.Action != "tap" {
		t.Fatalf("action = %q, want the shipped value", got.Action)
	}
	if got.Target == nil {
		t.Fatalf("a tap by name must report what it resolved to: %s", out)
	}
	if got.Target.Path != "0.1" || got.Target.Label != "Continue" || got.Target.MatchedBy != "exact" {
		t.Fatalf("target = %+v", *got.Target)
	}
	if got.Target.Y != 0.86 {
		t.Fatalf("target point = %v, want the element's own", got.Target.Y)
	}
}

func TestSimTap_CoordinatesCarryNoTargetInJSON(t *testing.T) {
	// The shipped shape is unchanged for the form that shipped with it.
	deps, _, _ := namedScreenDeps(t)

	out, _, err := executeCLI(t, deps, "sim", "tap", "0.5", "0.934", "--json")
	if err != nil {
		t.Fatalf("sim tap --json: %v", err)
	}
	if strings.Contains(out, "\"target\"") {
		t.Fatalf("a tap by coordinate has nothing to report about a name:\n%s", out)
	}
}

func TestSimTap_ByNameOnABlockedAppNamesTheBlockedMainThread(t *testing.T) {
	// Tapping by name reads the screen, so it meets the wedged app itself -
	// and "no element called Continue" would be the same wrong answer that
	// `ao sim ax` used to give.
	driver := &fakeSimDriver{snapshot: simbridge.Snapshot{
		Frontmost: simbridge.Frontmost{BundleID: "com.example.nimbus", PID: 4242},
	}}
	deps, _ := touchDeps(t, driver)
	deps, probes := withSampler(deps, blockedSampleReport, nil)

	_, _, err := executeCLI(t, deps, "sim", "tap", "--label", "Continue")
	if err == nil {
		t.Fatal("a screen that cannot be read must fail")
	}
	for _, want := range []string{"main thread", "com.example.nimbus", "ao sim log"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("err must name the real cause (%q):\n%v", want, err)
		}
	}
	if *probes == 0 {
		t.Fatal("the probe never ran")
	}
}
