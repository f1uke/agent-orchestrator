package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/simbridge"
)

// A runner read of a web sign-in sheet over an app, with the Paste callout
// and a Thai keyboard up: the screen the accessibility bridge could not see.
const runnerSignInSheet = `{"runner":{"state":"ready"},"hierarchy":{"version":"1",
"screen":{"width":440,"height":956},
"apps":[
 {"bundleId":"com.apple.SafariViewService","pid":71,"remoteView":true,"tree":{"type":"Application","label":"Safari","enabled":true,
  "frame":{"x":0,"y":0,"width":440,"height":956},"children":[
   {"type":"Other","enabled":true,"frame":{"x":0,"y":80,"width":440,"height":800},"children":[
    {"type":"TextField","label":"อีเมล","value":"","placeholder":"example@email.com","focused":true,"enabled":true,
     "frame":{"x":80,"y":600,"width":280,"height":56}},
    {"type":"Button","label":"ต่อไป","enabled":false,"frame":{"x":80,"y":680,"width":280,"height":52}}]},
   {"type":"MenuItem","label":"Paste","enabled":true,"frame":{"x":20,"y":540,"width":80,"height":44}},
   {"type":"Keyboard","enabled":true,"frame":{"x":0,"y":700,"width":440,"height":256},"children":[
    {"type":"Key","label":"ก","enabled":true,"frame":{"x":4,"y":710,"width":40,"height":54}},
    {"type":"Key","label":"ด","enabled":true,"frame":{"x":44,"y":710,"width":40,"height":54}},
    {"type":"Key","label":" ","id":"space","enabled":true,"frame":{"x":100,"y":880,"width":200,"height":54}}]},
   {"type":"Button","label":"Next keyboard","value":"English (US)","enabled":true,"frame":{"x":8,"y":900,"width":60,"height":50}}]}},
 {"bundleId":"com.example.app","pid":42,"tree":{"type":"Application","label":"Example","enabled":true,
  "frame":{"x":0,"y":0,"width":440,"height":956}}}]}}`

func TestSimAX_ReadsThroughTheRunnerWhenOneIsUp(t *testing.T) {
	driver := &fakeSimDriver{snapshot: fixtureSnapshot()}
	deps, daemon := touchDeps(t, driver)
	daemon.hierarchy = runnerSignInSheet

	out, _, err := executeCLI(t, deps, "sim", "ax")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Reader: XCTest (every process on screen)",
		`Keyboard: up, 3 keys, NOT Latin letters (a non-English input mode); the globe key switches to "English (US)"`,
		// The app that presented the sheet, not the sheet's host.
		"Foreground app: com.example.app (pid 42)",
		`Application "Safari" id "com.apple.SafariViewService"`,
		`TextField "อีเมล" placeholder "example@email.com" (focused)`,
		`Button "ต่อไป" (disabled)`,
		`MenuItem "Paste"  tap`,
		`keys: ก ด space`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, `Key "ก"`) {
		t.Errorf("keys are folded onto one line, not one per key:\n%s", out)
	}
	if strings.Contains(out, `"Search"`) {
		t.Fatalf("read through the bridge although the runner answered:\n%s", out)
	}
	if !strings.Contains(daemon.callLog(), "/api/v1/sim/devices/"+simUDIDProMax+"/hierarchy") {
		t.Fatalf("never asked the daemon for the runner's read: %s", daemon.callLog())
	}
	// `ao sim ax` is the one read that waits for a runner that is starting.
	if daemon.hierarchyQuery != "hitTest=true&waitMs=15000" {
		t.Fatalf("query = %q, want the ax wait", daemon.hierarchyQuery)
	}
}

func TestSimAX_JSONSaysWhichReaderAnswered(t *testing.T) {
	driver := &fakeSimDriver{snapshot: fixtureSnapshot()}
	deps, daemon := touchDeps(t, driver)
	daemon.hierarchy = runnerSignInSheet

	out, _, err := executeCLI(t, deps, "sim", "ax", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Reader   struct{ Source string }
		Keyboard struct {
			Keys          int
			Latin         bool
			NextInputMode string
		}
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if got.Reader.Source != "xctest" || got.Keyboard.Keys != 3 || got.Keyboard.Latin || got.Keyboard.NextInputMode != "English (US)" {
		t.Fatalf("got %+v", got)
	}
}

func TestSimAX_FallsBackToTheBridgeAndSaysWhy(t *testing.T) {
	cases := map[string]struct {
		hierarchy string
		want      string
	}{
		"nobody holds it": {"", "no session holds this simulator; claim it (`ao sim claim`)"},
		"starting": {`{"runner":{"state":"starting","reason":"the XCTest runner is starting"}}`,
			"the XCTest reader is still starting"},
		"failed": {`{"runner":{"state":"failed","reason":"the simulator is not booted\nmore"}}`,
			"the XCTest reader failed: the simulator is not booted"},
		"empty read": {`{"runner":{"state":"ready"},"hierarchy":{"version":"1","screen":{"width":0,"height":0},"apps":[],"errors":["XCTest reported no foreground application"]}}`,
			"the XCTest runner read an empty screen (XCTest reported no foreground application)"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			driver := &fakeSimDriver{snapshot: fixtureSnapshot()}
			deps, daemon := touchDeps(t, driver)
			daemon.hierarchy = tc.hierarchy
			out, _, err := executeCLI(t, deps, "sim", "ax")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out, `TextField "Search"`) {
				t.Fatalf("did not fall back to the bridge:\n%s", out)
			}
			if !strings.Contains(out, "Reader: accessibility bridge (frontmost app only) - "+tc.want) {
				t.Fatalf("the fallback does not say why (%q):\n%s", tc.want, out)
			}
		})
	}
}

func TestSimAX_FallsBackWhenTheDaemonIsNotRunning(t *testing.T) {
	driver := &fakeSimDriver{snapshot: fixtureSnapshot()}
	deps, _ := touchDeps(t, driver)
	deps.ProcessAlive = func(int) bool { return false }
	out, _, err := executeCLI(t, deps, "sim", "ax")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Reader: accessibility bridge (frontmost app only) - the daemon could not be asked for the XCTest reader") {
		t.Fatalf("output:\n%s", out)
	}
}

// Tapping by name resolves against the runner's tree, so out-of-process UI is
// tappable by what it says - and the tap itself still goes through the bridge
// under the hold.
func TestSimTap_ByLabelReachesTheRunnersTree(t *testing.T) {
	driver := &fakeSimDriver{snapshot: fixtureSnapshot()}
	deps, daemon := touchDeps(t, driver)
	daemon.hierarchy = runnerSignInSheet

	out, errOut, err := executeCLI(t, deps, "sim", "tap", "--label", "Paste")
	if err != nil {
		t.Fatalf("tap --label Paste: %v\n%s", err, errOut)
	}
	calls := driver.calls()
	if len(calls) != 1 {
		t.Fatalf("driver saw %d gestures, want 1", len(calls))
	}
	if x, y := calls[0][0].X, calls[0][0].Y; x != 60.0/440 || y != 562.0/956 {
		t.Fatalf("tapped (%v, %v), want the Paste item's centre", x, y)
	}
	if !strings.Contains(out, `MenuItem "Paste"`) {
		t.Fatalf("output:\n%s", out)
	}
	// Under the hold, a read never waits for a starting runner.
	if strings.Contains(daemon.callLog(), "waitMs") {
		t.Fatalf("a tap waited for the runner: %s", daemon.callLog())
	}
}

// runnerCoveredScreen is a hit-tested read: a button wholly under the
// keyboard's bar, a row half under the tab bar, and the bar's own Done.
const runnerCoveredScreen = `{"runner":{"state":"ready"},"hierarchy":{"version":"3","screen":{"width":400,"height":800},
"hitTest":{"checked":4,"covered":2,"elapsedMs":3},
"apps":[{"bundleId":"com.example.app","pid":42,"tree":{"type":"Application","label":"Example","enabled":true,
 "frame":{"x":0,"y":0,"width":400,"height":800},"children":[
  {"type":"Button","label":"Next","enabled":true,"frame":{"x":20,"y":420,"width":360,"height":48},
   "covered":{"by":{"type":"Toolbar","label":"Toolbar"}}},
  {"type":"StaticText","label":"Top story","enabled":true,"frame":{"x":20,"y":700,"width":360,"height":60},
   "covered":{"by":{"type":"TabBar","label":"Tab Bar"},"point":{"x":200,"y":712}}},
  {"type":"Button","label":"Done","enabled":true,"frame":{"x":340,"y":400,"width":44,"height":44}}]}}]}}`

func TestSimAX_SaysWhatIsCoveredAndWhereItCanStillBeTouched(t *testing.T) {
	driver := &fakeSimDriver{snapshot: fixtureSnapshot()}
	deps, daemon := touchDeps(t, driver)
	daemon.hierarchy = runnerCoveredScreen

	out, _, err := executeCLI(t, deps, "sim", "ax")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"4 elements (4 on screen (2 of them covered), 0 off screen)",
		`Button "Next"  covered by Toolbar "Toolbar", no part of it left to touch  box`,
		`StaticText "Top story"  tap 0.500 0.890 (the part still showing - its centre is under TabBar "Tab Bar")`,
		`Button "Done"  tap 0.905 0.527`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "did not hit-test") {
		t.Errorf("a hit-tested read says it was not:\n%s", out)
	}

	out, _, err = executeCLI(t, deps, "sim", "ax", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		HitTested    bool
		CoveredCount int
		Elements     []simbridge.Element
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	kids := got.Elements[0].Children
	if !got.HitTested || got.CoveredCount != 2 || kids[0].Tap != nil || kids[0].Covered == nil ||
		kids[0].Covered.VisiblePart || kids[1].Covered == nil || !kids[1].Covered.VisiblePart || kids[1].Tap == nil {
		t.Fatalf("json: %s", out)
	}
}

func TestSimAX_AnXCTestReadThatCouldNotHitTestSaysSo(t *testing.T) {
	driver := &fakeSimDriver{snapshot: fixtureSnapshot()}
	deps, daemon := touchDeps(t, driver)
	daemon.hierarchy = strings.Replace(runnerCoveredScreen, `"hitTest":{"checked":4,"covered":2,"elapsedMs":3}`,
		`"hitTest":{"checked":0,"covered":0,"elapsedMs":0,"error":"no hit-test"}`, 1)

	out, _, err := executeCLI(t, deps, "sim", "ax")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Note: this read did not hit-test") {
		t.Fatalf("an unchecked read looks checked:\n%s", out)
	}
}

func TestSimTap_ByLabelRefusesACoveredElementAndTapsNothing(t *testing.T) {
	driver := &fakeSimDriver{snapshot: fixtureSnapshot()}
	deps, daemon := touchDeps(t, driver)
	daemon.hierarchy = runnerCoveredScreen

	_, _, err := executeCLI(t, deps, "sim", "tap", "--label", "Next")
	if err == nil {
		t.Fatal("tapped an element the keyboard's bar covers")
	}
	for _, want := range []string{`Button "Next" is on the screen but covered by Toolbar "Toolbar"`, "Nothing was tapped", "ao sim drag", "keyboard"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal lacks %q: %v", want, err)
		}
	}
	if calls := driver.calls(); len(calls) != 0 {
		t.Fatalf("sent %d gestures for a refused tap", len(calls))
	}
	if !strings.Contains(daemon.hierarchyQuery, "hitTest=true") {
		t.Fatalf("a tap by name read without hit-testing: %q", daemon.hierarchyQuery)
	}
}

func TestSimTap_ByLabelTapsThePartOfACoveredRowThatShows(t *testing.T) {
	driver := &fakeSimDriver{snapshot: fixtureSnapshot()}
	deps, daemon := touchDeps(t, driver)
	daemon.hierarchy = runnerCoveredScreen

	out, _, err := executeCLI(t, deps, "sim", "tap", "--label", "Top story")
	if err != nil {
		t.Fatal(err)
	}
	calls := driver.calls()
	if len(calls) != 1 || calls[0][0].X != 0.5 || calls[0][0].Y != 712.0/800 {
		t.Fatalf("tapped %+v, want the visible part (0.5, 0.89)", calls)
	}
	if !strings.Contains(out, `Its centre is under TabBar "Tab Bar", so the tap went to the part of it still showing.`) {
		t.Fatalf("the output does not say the tap moved off the centre:\n%s", out)
	}

	out, _, err = executeCLI(t, deps, "sim", "tap", "--label", "Top story", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"coveredBy": "TabBar \"Tab Bar\""`) {
		t.Fatalf("json does not carry what covers it:\n%s", out)
	}
}
