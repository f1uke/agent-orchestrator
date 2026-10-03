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
	// This capture's keyboard sits below the screen (y 952 of 874): the field
	// had focus and the software keyboard was hidden. It is listed, and it is
	// not "up".
	if snap.Keyboard != nil {
		t.Fatalf("keyboard = %+v, want none: the only one in this read is below the screen", snap.Keyboard)
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

	// A focused field with the software keyboard hidden (a hardware keyboard,
	// or the simulator believing in one after HID key presses) still lists the
	// keyboard, below the bottom edge.
	hidden := tree(key("q"), key("w"))
	board := &hidden.Apps[0].Tree.Children[0]
	board.Frame.Y = 952
	for i := range board.Children {
		board.Children[i].Frame.Y = 970
	}
	hidden.Apps[0].Tree.Children[1].Frame.Y = 1100
	if kb := SnapshotFromXCTest(hidden).Keyboard; kb != nil {
		t.Fatalf("a keyboard below the screen is not up, got %+v", kb)
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

// Real hit-tested reads of nter (iOS 26.3): Home scrolled so a Top Stories row
// is half under the floating tab bar, and the web sign-in with the keyboard up
// and ต่อไป drawn under its ^ v Done bar - the two places an agent's taps
// were eaten, six times in twelve runs.
func TestSnapshotFromXCTest_ARowUnderTheTabBarIsTappedWhereItShows(t *testing.T) {
	snap := SnapshotFromXCTest(loadXCTest(t, "xctest-nter-home-tab-bar.json"))
	if !snap.HitTested || snap.CoveredCount != 5 {
		t.Fatalf("hitTested %v, covered %d, want a hit-tested read with 5 covered", snap.HitTested, snap.CoveredCount)
	}
	rows := findAll(snap.Elements, func(e Element) bool { return e.Label == "[PROMO-FINNO] ADSV2 NO SLA - 02" })
	if len(rows) != 1 {
		t.Fatalf("found %d rows", len(rows))
	}
	row := rows[0]
	if row.Covered == nil || row.Covered.By != `TabBar "Tab Bar"` || !row.Covered.VisiblePart {
		t.Fatalf("covered = %+v, want the tab bar with a visible part", row.Covered)
	}
	if row.OffScreen {
		t.Fatal("a row on the screen under the tab bar is not off screen")
	}
	// Its centre (y 0.915) is under the tab bar (0.905 down); the point is in
	// the strip above it, inside the row.
	if row.Tap == nil || row.Tap.Y >= 791.0/874 || row.Tap.Y <= row.Box.Y1 {
		t.Fatalf("tap %+v, box %+v: want a point between the row's top and the tab bar", row.Tap, row.Box)
	}
	// The row's date line is wholly under the bar (an earlier row has the
	// same date, out in the open).
	date := findAll(snap.Elements, func(e Element) bool { return e.Label == "28 พ.ค. 69" && e.Frame.Y > 791 })
	if len(date) != 1 || date[0].Covered == nil || date[0].Tap != nil || date[0].OffScreen {
		t.Fatalf("the date line wholly under the tab bar: %+v", date)
	}
	// The tab bar's own buttons answer the hit-test with their neighbours on
	// iOS 26; they are one bar, not covering each other.
	for _, name := range []string{"Home", "Port", "Fund", "Markets", "Chats"} {
		tab := findAll(snap.Elements, func(e Element) bool { return e.Type == "Button" && e.Label == name })
		if len(tab) != 1 || tab[0].Covered != nil || tab[0].Tap == nil {
			t.Fatalf("tab %s: %+v", name, tab)
		}
	}
}

func TestSnapshotFromXCTest_AButtonUnderTheKeyboardBarHasNoTapPoint(t *testing.T) {
	snap := SnapshotFromXCTest(loadXCTest(t, "xctest-nter-login-keyboard-bar.json"))
	next := findAll(snap.Elements, func(e Element) bool { return e.Type == "Button" && e.Label == "ต่อไป" })
	if len(next) != 1 {
		t.Fatalf("found %d ต่อไป buttons", len(next))
	}
	if c := next[0].Covered; c == nil || c.By != `Toolbar "Toolbar"` || c.VisiblePart || next[0].Tap != nil {
		t.Fatalf("ต่อไป: covered %+v, tap %+v - want the keyboard's toolbar and no point", c, next[0].Tap)
	}
	// What is on top stays tappable: the field being typed in, and the bar's
	// own Done.
	for _, e := range findAll(snap.Elements, func(e Element) bool {
		return (e.Type == "TextField" && e.Focused) || (e.Type == "Button" && e.Label == "Done")
	}) {
		if e.Covered != nil || e.Tap == nil {
			t.Fatalf("%s %q is in the open: %+v", e.Type, e.Label, e.Covered)
		}
	}
	// The browser's own bottom bar is under the keyboard.
	if back := findAll(snap.Elements, func(e Element) bool { return e.Type == "Button" && e.Label == "Back" }); len(back) != 1 ||
		back[0].Covered == nil || back[0].Covered.By != "Keyboard" {
		t.Fatalf("Back: %+v", back)
	}
}

func TestSnapshotFromXCTest_CoverOnlyCountsWhenTheReadHitTested(t *testing.T) {
	h := XCTestHierarchy{
		Screen: Size{Width: 400, Height: 800},
		Apps: []XCTestApp{{BundleID: "a", Tree: XCTestNode{
			Type: "Application", Frame: Rect{Width: 400, Height: 800}, Enabled: true,
			Children: []XCTestNode{
				{Type: "Button", Label: "Under", Enabled: true, Frame: Rect{X: 0, Y: 700, Width: 400, Height: 80},
					Covered: &XCTestCover{By: XCTestCoverer{Type: "TabBar", Label: "Tab Bar"}, Point: &XCTestPoint{X: 200, Y: 705}}},
				// Below the fold already: no point to move, and off screen says it.
				{Type: "Button", Label: "Gone", Enabled: true, Frame: Rect{X: 0, Y: 900, Width: 400, Height: 80},
					Covered: &XCTestCover{By: XCTestCoverer{Type: "TabBar"}}},
			},
		}}},
	}
	plain := SnapshotFromXCTest(h)
	if plain.HitTested || plain.CoveredCount != 0 {
		t.Fatalf("a read with no hit-test summary claims to know: %+v", plain)
	}
	h.HitTest = &XCTestHitTest{Checked: 1, Covered: 1}
	snap := SnapshotFromXCTest(h)
	under := findAll(snap.Elements, func(e Element) bool { return e.Label == "Under" })[0]
	if under.Tap == nil || under.Tap.X != 0.5 || under.Tap.Y != 705.0/800 || !under.Covered.VisiblePart {
		t.Fatalf("under: tap %+v covered %+v", under.Tap, under.Covered)
	}
	gone := findAll(snap.Elements, func(e Element) bool { return e.Label == "Gone" })[0]
	if !gone.OffScreen || gone.Covered != nil {
		t.Fatalf("gone: %+v", gone)
	}
	if !snap.HitTested || snap.CoveredCount != 1 {
		t.Fatalf("hitTested %v covered %d", snap.HitTested, snap.CoveredCount)
	}
	h.HitTest = &XCTestHitTest{Error: "XCTest has no accessibility hit-test here"}
	if failed := SnapshotFromXCTest(h); failed.HitTested {
		t.Fatal("a hit-test that could not run is not a hit-tested read")
	}
}

// The element the runner marked as reached by a touch keeps its mark through
// the conversion, under the path `ao sim ax` prints for it - and a mark on a
// layout container the converter drops lands on the nearest element it keeps,
// because that is what a touch there was inside.
func TestSnapshotFromXCTest_ReachedSurvivesTheConversion(t *testing.T) {
	read := func(mark func(*XCTestNode)) Snapshot {
		// Built fresh per read: the trees share no slices, so one mark
		// cannot leak into the next read.
		button := XCTestNode{Type: "Button", Label: "Continue", Enabled: true,
			Frame: Rect{X: 200, Y: 490, Width: 140, Height: 48}}
		wrapper := XCTestNode{Type: "Other", Frame: Rect{X: 0, Y: 400, Width: 402, Height: 200},
			Children: []XCTestNode{button}}
		sheet := XCTestNode{Type: "Alert", Label: "Sign In", Frame: Rect{X: 40, Y: 340, Width: 320, Height: 210},
			Children: []XCTestNode{wrapper}}
		root := XCTestNode{Type: "Application", Label: " ", Frame: Rect{Width: 402, Height: 874},
			Children: []XCTestNode{sheet}}
		mark(&root)
		return SnapshotFromXCTest(XCTestHierarchy{Screen: Size{Width: 402, Height: 874}, At: &XCTestAt{Found: true},
			Apps: []XCTestApp{{BundleID: "com.apple.springboard", Tree: root}}})
	}

	snap := read(func(root *XCTestNode) { root.Children[0].Children[0].Children[0].Reached = true })
	if snap.Reached == nil || snap.Reached.Path != "0.0.0" || snap.Reached.Error != "" {
		t.Fatalf("reached = %+v, want the button at 0.0.0 (its wrapper is dropped)", snap.Reached)
	}

	snap = read(func(root *XCTestNode) { root.Children[0].Children[0].Reached = true })
	if snap.Reached == nil || snap.Reached.Path != "0.0" {
		t.Fatalf("reached = %+v, want the alert at 0.0, the nearest kept element holding the dropped wrapper", snap.Reached)
	}

	snap = read(func(*XCTestNode) {})
	if snap.Reached == nil || snap.Reached.Path != "" || snap.Reached.Error == "" {
		t.Fatalf("reached = %+v, want an answer that says nothing was marked", snap.Reached)
	}

	plain := SnapshotFromXCTest(XCTestHierarchy{Screen: Size{Width: 402, Height: 874},
		Apps: []XCTestApp{{BundleID: "a", Tree: XCTestNode{Type: "Application", Frame: Rect{Width: 402, Height: 874}}}}})
	if plain.Reached != nil {
		t.Fatalf("reached = %+v on a read that never asked; want nil", plain.Reached)
	}
}
