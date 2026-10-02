package cli

import (
	"encoding/json"
	"strings"
	"testing"
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
	if daemon.hierarchyQuery != "waitMs=15000" {
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
