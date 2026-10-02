package simbridge

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// The fixtures are real runner reads of nter's web sign-in
// (ASWebAuthenticationSession, iOS 26.3), one with the email field focused and
// one with the Thai keyboard up. They are what the accessibility bridge could
// not see.
func loadXCTest(t *testing.T, name string) XCTestHierarchy {
	t.Helper()
	body, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	var h XCTestHierarchy
	if err := json.Unmarshal(body, &h); err != nil {
		t.Fatal(err)
	}
	return h
}

func findAll(elements []Element, match func(Element) bool) []Element {
	var out []Element
	walk(elements, func(e Element) {
		if match(e) {
			out = append(out, e)
		}
	})
	return out
}

func TestSnapshotFromXCTest_SeesTheWebSignInSheet(t *testing.T) {
	snap := SnapshotFromXCTest(loadXCTest(t, "xctest-web-login.json"))

	if snap.Reader == nil || snap.Reader.Source != SourceXCTest {
		t.Fatalf("reader = %+v, want xctest", snap.Reader)
	}
	// The sheet's host draws over the app; the app is still the one on screen.
	if snap.Frontmost.BundleID != "com.finnomena.app.finnomena" || snap.Frontmost.PID != 90017 {
		t.Fatalf("frontmost = %+v, want the app that presented the sheet", snap.Frontmost)
	}
	if len(snap.Elements) != 2 || snap.Elements[0].ID != "com.apple.SafariViewService" || snap.Elements[1].ID != "com.finnomena.app.finnomena" {
		t.Fatalf("roots = %v, want the sheet's host first, then the app, each named by bundle id", rootIDs(snap))
	}

	fields := findAll(snap.Elements, func(e Element) bool { return e.Type == "TextField" && e.Label == "อีเมล" })
	if len(fields) != 1 {
		t.Fatalf("found %d email fields, want 1", len(fields))
	}
	field := fields[0]
	if !field.Focused || field.Tap == nil || field.Value != "example@email.com" {
		t.Fatalf("email field = %+v, want it focused, tappable, reading its placeholder", field)
	}
	next := findAll(snap.Elements, func(e Element) bool { return e.Type == "Button" && e.Label == "ต่อไป" })
	if len(next) != 1 || next[0].Enabled {
		t.Fatalf("ต่อไป = %+v, want one, disabled until the field has content", next)
	}

	// Selecting by name works against it exactly as against the bridge's tree.
	match, err := Select(snap, Selector{Kind: SelectByLabel, Text: "ต่อไป"})
	if err != nil || match.Element.Path != next[0].Path {
		t.Fatalf("Select(ต่อไป) = %+v, %v", match, err)
	}
}

func TestSnapshotFromXCTest_DropsLayoutButKeepsWhatItHolds(t *testing.T) {
	snap := SnapshotFromXCTest(loadXCTest(t, "xctest-web-login.json"))

	// The web view's toolbar buttons sit under zero-sized wrappers; dropping a
	// wrapper must not drop what is inside it.
	for _, name := range []string{"Back", "Share", "Reload"} {
		if got := findAll(snap.Elements, func(e Element) bool { return e.Type == "Button" && e.Label == name }); len(got) != 1 {
			t.Errorf("toolbar button %q found %d times, want 1", name, len(got))
		}
	}
	walk(snap.Elements, func(e Element) {
		if e.Frame.Width <= 0 || e.Frame.Height <= 0 {
			t.Errorf("zero-sized element reported: %+v", e)
		}
		if isLayoutContainer(XCTestNode{Type: e.Type, Label: e.Label, Value: e.Value, ID: e.ID}) && e.Type != "Application" {
			t.Errorf("unnamed layout container reported: %s %q [%s]", e.Type, e.Label, e.Path)
		}
		// A web page's text arrives as label AND value; the value is dropped.
		if e.Type == "StaticText" && e.Value != "" && e.Value == e.Label {
			t.Errorf("static text repeats its label as its value: %+v", e)
		}
	})
	// Paths are plain index paths into what is reported.
	walkPaths(t, snap.Elements, "")
	if snap.NodeCount != snap.TotalNodeCount || snap.NodeCount != snap.OnScreenCount+snap.OffScreenCount {
		t.Fatalf("counts disagree: %d/%d, %d on + %d off", snap.NodeCount, snap.TotalNodeCount, snap.OnScreenCount, snap.OffScreenCount)
	}
}

func walkPaths(t *testing.T, elements []Element, prefix string) {
	t.Helper()
	for i, e := range elements {
		if want := indexPath(prefix, i); e.Path != want {
			t.Fatalf("path %q, want %q", e.Path, want)
		}
		walkPaths(t, e.Children, e.Path)
	}
}

func rootIDs(snap Snapshot) []string {
	var ids []string
	for _, e := range snap.Elements {
		ids = append(ids, e.ID)
	}
	return ids
}

func TestSnapshotFromXCTest_KeepsARepeatedKeyboardOnce(t *testing.T) {
	snap := SnapshotFromXCTest(loadXCTest(t, "xctest-web-login-thai-keyboard.json"))

	// Both the sheet and the app under it report the same keyboard.
	keyboards := findAll(snap.Elements, func(e Element) bool { return e.Type == "Keyboard" })
	if len(keyboards) != 1 {
		t.Fatalf("found %d keyboards, want the one on screen", len(keyboards))
	}
	if snap.Keyboard == nil || snap.Keyboard.Keys == 0 || snap.Keyboard.Latin {
		t.Fatalf("keyboard = %+v, want a non-Latin (Thai) layout", snap.Keyboard)
	}
	if got := findAll(snap.Elements, func(e Element) bool { return e.Type == "Key" && e.Label == "ก" }); len(got) != 1 {
		t.Fatalf("key ก found %d times, want 1", len(got))
	}
}

func TestKeyboardOf(t *testing.T) {
	key := func(label string) XCTestNode {
		return XCTestNode{Type: "Key", Label: label, Enabled: true, Frame: Rect{X: 10, Y: 600, Width: 30, Height: 40}}
	}
	tree := func(keys ...XCTestNode) XCTestHierarchy {
		board := XCTestNode{Type: "Keyboard", Enabled: true, Frame: Rect{Y: 580, Width: 400, Height: 240}, Children: keys}
		globe := XCTestNode{Type: "Button", Label: "Next keyboard", Value: "ภาษาไทย", Enabled: true, Frame: Rect{X: 8, Y: 820, Width: 60, Height: 50}}
		return XCTestHierarchy{
			Version: "1",
			Screen:  Size{Width: 400, Height: 874},
			Apps: []XCTestApp{{BundleID: "com.example.app", Tree: XCTestNode{
				Type: "Application", Enabled: true, Frame: Rect{Width: 400, Height: 874},
				Children: []XCTestNode{board, globe},
			}}},
		}
	}

	latin := SnapshotFromXCTest(tree(key("q"), key("w"), key("delete"), key(" "))).Keyboard
	if latin == nil || !latin.Latin || latin.Keys != 4 || latin.NextInputMode != "ภาษาไทย" {
		t.Fatalf("latin keyboard = %+v", latin)
	}
	thai := SnapshotFromXCTest(tree(key("ก"), key("q"))).Keyboard
	if thai == nil || thai.Latin {
		t.Fatalf("a keyboard with any non-Latin letter is not Latin: %+v", thai)
	}
	none := SnapshotFromXCTest(XCTestHierarchy{Screen: Size{Width: 400, Height: 874}, Apps: []XCTestApp{{
		BundleID: "com.example.app",
		Tree:     XCTestNode{Type: "Application", Enabled: true, Frame: Rect{Width: 400, Height: 874}},
	}}})
	if none.Keyboard != nil {
		t.Fatalf("no keyboard on screen, got %+v", none.Keyboard)
	}
}

func TestSnapshotFromXCTest_EmptyIsNotUsable(t *testing.T) {
	if snap := SnapshotFromXCTest(XCTestHierarchy{Version: "1", Errors: []string{"XCTest reported no foreground application"}}); snap.Usable() {
		t.Fatalf("an empty read must not be usable: %+v", snap)
	}
}

func TestSnapshotFromXCTest_FrontmostFallsBackToTheOnlyApp(t *testing.T) {
	// SpringBoard showing an alert is the foreground app itself, not a host.
	snap := SnapshotFromXCTest(XCTestHierarchy{Screen: Size{Width: 400, Height: 874}, Apps: []XCTestApp{{
		BundleID: "com.apple.springboard", PID: 7,
		Tree: XCTestNode{Type: "Application", Enabled: true, Frame: Rect{Width: 400, Height: 874}, Children: []XCTestNode{{
			Type: "Alert", Label: "“Finnomena” Wants to Use “finnomena.com” to Sign In", Enabled: true,
			Frame:    Rect{X: 40, Y: 350, Width: 320, Height: 200},
			Children: []XCTestNode{{Type: "Button", Label: "Continue", Enabled: true, Frame: Rect{X: 200, Y: 480, Width: 140, Height: 48}}},
		}}},
	}}})
	if snap.Frontmost.BundleID != "com.apple.springboard" || snap.Frontmost.PID != 7 {
		t.Fatalf("frontmost = %+v", snap.Frontmost)
	}
	match, err := Select(snap, Selector{Kind: SelectByLabel, Text: "Continue"})
	if err != nil || !strings.HasPrefix(match.Element.Path, "0.0.") {
		t.Fatalf("Select(Continue) = %+v, %v", match, err)
	}
}
