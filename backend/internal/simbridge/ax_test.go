package simbridge

import (
	"encoding/json"
	"testing"
)

// A trimmed but real-shaped axDescribe payload: one screen-sized root with
// nested children, the way the addon returns it.
const rawTree = `[{
  "AXLabel": null, "AXValue": null, "AXUniqueId": null, "enabled": true,
  "role_description": "", "type": "Application",
  "frame": {"x": 0, "y": 0, "width": 440, "height": 956},
  "children": [
    {"AXLabel": "Search", "AXValue": null, "AXUniqueId": "search-field", "enabled": true,
     "role_description": "text field", "type": "TextField",
     "frame": {"x": 20, "y": 100, "width": 400, "height": 40}, "children": []},
    {"AXLabel": "Settings", "AXValue": "2 updates", "AXUniqueId": null, "enabled": false,
     "role_description": "button", "type": "Icon",
     "frame": {"x": 0, "y": 478, "width": 220, "height": 478},
     "children": [
       {"AXLabel": "badge", "AXValue": "2", "AXUniqueId": null, "enabled": true,
        "role_description": "", "type": "StaticText",
        "frame": {"x": 180, "y": 478, "width": 40, "height": 40}, "children": []}
     ]}
  ]
}]`

func parseRaw(t *testing.T) []rawAXNode {
	t.Helper()
	var nodes []rawAXNode
	if err := json.Unmarshal([]byte(rawTree), &nodes); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	return nodes
}

func TestSnapshot_KeepsTheTreeShape(t *testing.T) {
	snap := newSnapshot(parseRaw(t), Frontmost{BundleID: "com.example.app", PID: 42})

	if snap.Screen.Width != 440 || snap.Screen.Height != 956 {
		t.Fatalf("screen = %+v, want the root frame", snap.Screen)
	}
	if len(snap.Elements) != 1 || len(snap.Elements[0].Children) != 2 {
		t.Fatalf("nesting was flattened: %+v", snap.Elements)
	}
	badge := snap.Elements[0].Children[1].Children
	if len(badge) != 1 || badge[0].Value != "2" {
		t.Fatalf("grandchild lost: %+v", badge)
	}
	if snap.Frontmost.BundleID != "com.example.app" {
		t.Fatalf("frontmost = %+v", snap.Frontmost)
	}
}

func TestSnapshot_TapPointIsPrecomputedAndNormalized(t *testing.T) {
	// The whole point: an agent copies `tap` straight into `ao sim tap` without
	// doing coordinate maths or guessing from a picture.
	snap := newSnapshot(parseRaw(t), Frontmost{})
	search := snap.Elements[0].Children[0]

	wantX := (20.0 + 400.0/2) / 440.0
	wantY := (100.0 + 40.0/2) / 956.0
	if !closeEnough(search.Tap.X, wantX) || !closeEnough(search.Tap.Y, wantY) {
		t.Fatalf("tap = %+v, want the element's centre normalized (%.4f, %.4f)", search.Tap, wantX, wantY)
	}
	if search.Tap.X < 0 || search.Tap.X > 1 || search.Tap.Y < 0 || search.Tap.Y > 1 {
		t.Fatalf("tap %+v is outside the screen", search.Tap)
	}
}

func TestSnapshot_CarriesWhatAnAgentDecidesOn(t *testing.T) {
	snap := newSnapshot(parseRaw(t), Frontmost{})
	settings := snap.Elements[0].Children[1]

	if settings.Label != "Settings" || settings.Value != "2 updates" || settings.Role != "button" || settings.Type != "Icon" {
		t.Fatalf("element = %+v", settings)
	}
	if settings.Enabled {
		t.Fatal("a disabled element must not read as enabled: tapping it does nothing")
	}
	if settings.Path != "0.1" {
		t.Fatalf("path = %q, want the index path 0.1", settings.Path)
	}
	if snap.Elements[0].Children[0].ID != "search-field" {
		t.Fatal("AXUniqueId is the only stable handle an app can give us; keep it when it exists")
	}
}

func TestSnapshot_CountsEveryNode(t *testing.T) {
	snap := newSnapshot(parseRaw(t), Frontmost{})
	if snap.NodeCount != 4 {
		t.Fatalf("nodeCount = %d, want 4 (root + 2 + 1 grandchild)", snap.NodeCount)
	}
	if snap.Truncated {
		t.Fatal("nothing was dropped")
	}
}

func TestSnapshot_TruncateKeepsAPrefixAndSaysSo(t *testing.T) {
	snap := newSnapshot(parseRaw(t), Frontmost{}).Truncate(2)

	if !snap.Truncated {
		t.Fatal("a truncated tree that does not say so is a lie an agent will act on")
	}
	if snap.NodeCount != 2 {
		t.Fatalf("nodeCount = %d, want the cap of 2", snap.NodeCount)
	}
	if snap.TotalNodeCount != 4 {
		t.Fatalf("totalNodeCount = %d, want the real size 4 so the human can raise the cap", snap.TotalNodeCount)
	}
	if len(snap.Elements) != 1 || len(snap.Elements[0].Children) != 1 {
		t.Fatalf("truncation must keep a walkable prefix, got %+v", snap.Elements)
	}
}

func TestSnapshot_TruncateIsANoOpBelowTheCap(t *testing.T) {
	full := newSnapshot(parseRaw(t), Frontmost{})
	capped := full.Truncate(500)
	if capped.Truncated || capped.NodeCount != full.NodeCount {
		t.Fatalf("capped = %+v, want the whole tree untouched", capped)
	}
}

func TestSnapshot_EmptyTreeIsNotAScreen(t *testing.T) {
	snap := newSnapshot(nil, Frontmost{BundleID: "com.apple.springboard"})
	if snap.Usable() {
		t.Fatal("an empty tree must not read as a usable screen")
	}
	snap = newSnapshot(parseRaw(t), Frontmost{})
	if !snap.Usable() {
		t.Fatal("a real tree must read as usable")
	}
}

func closeEnough(got, want float64) bool {
	const epsilon = 1e-9
	d := got - want
	return d < epsilon && d > -epsilon
}

// A screen with more below the fold than on it, which is every scrolling app:
// a visible row, a row 400pt past the bottom, a card that starts on screen and
// runs off it, and the same row reported twice (the addon walks the tree and
// then hit-tests a grid, so anything found both ways arrives twice).
const rawScrolledTree = `[{
  "AXLabel": null, "AXValue": null, "AXUniqueId": null, "enabled": true,
  "role_description": "", "type": "Application",
  "frame": {"x": 0, "y": 0, "width": 440, "height": 956},
  "children": [
    {"AXLabel": "Visible row", "AXValue": null, "AXUniqueId": null, "enabled": true,
     "role_description": "button", "type": "Button",
     "frame": {"x": 20, "y": 200, "width": 400, "height": 44}, "children": []},
    {"AXLabel": "Visible row", "AXValue": null, "AXUniqueId": null, "enabled": true,
     "role_description": "button", "type": "Button",
     "frame": {"x": 20, "y": 200, "width": 400, "height": 44}, "children": []},
    {"AXLabel": "Below the fold", "AXValue": null, "AXUniqueId": null, "enabled": true,
     "role_description": "button", "type": "Button",
     "frame": {"x": 20, "y": 1300, "width": 400, "height": 44}, "children": []},
    {"AXLabel": "Off to the right", "AXValue": null, "AXUniqueId": null, "enabled": true,
     "role_description": "button", "type": "Button",
     "frame": {"x": 500, "y": 300, "width": 200, "height": 44}, "children": []},
    {"AXLabel": "Half on", "AXValue": null, "AXUniqueId": null, "enabled": true,
     "role_description": "button", "type": "Button",
     "frame": {"x": 20, "y": 900, "width": 400, "height": 80}, "children": []}
  ]
}]`

// Only the status bar: what a read returns for a second or two after an app is
// brought to the front, before it publishes its own screen.
const rawStatusBarOnlyTree = `[{
  "AXLabel": null, "AXValue": null, "AXUniqueId": null, "enabled": true,
  "role_description": "", "type": "Application",
  "frame": {"x": 0, "y": 0, "width": 440, "height": 956},
  "children": [
    {"AXLabel": "20:09", "AXValue": null, "AXUniqueId": null, "enabled": true,
     "role_description": "", "type": "StaticText",
     "frame": {"x": 60, "y": 22, "width": 60, "height": 22}, "children": []},
    {"AXLabel": "100% battery power", "AXValue": "Not charging", "AXUniqueId": null, "enabled": true,
     "role_description": "", "type": "GenericElement",
     "frame": {"x": 370, "y": 25, "width": 40, "height": 14}, "children": []}
  ]
}]`

func parse(t *testing.T, raw string) []rawAXNode {
	t.Helper()
	var nodes []rawAXNode
	if err := json.Unmarshal([]byte(raw), &nodes); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	return nodes
}

func find(t *testing.T, snap Snapshot, label string) Element {
	t.Helper()
	var found *Element
	var walk func([]Element)
	walk = func(elements []Element) {
		for i := range elements {
			if elements[i].Label == label && found == nil {
				found = &elements[i]
			}
			walk(elements[i].Children)
		}
	}
	walk(snap.Elements)
	if found == nil {
		t.Fatalf("no element labelled %q in %+v", label, snap.Elements)
	}
	return *found
}

// The defect this exists for, measured on a real app: 60 of 103 elements had
// their centre off the screen, and every one still carried a tap point -
// clamped onto the screen's edge. Tapping "See all" (9pt below the fold) put a
// finger on the very bottom row of pixels instead, where the tab bar lives. An
// element that cannot be touched must not offer a point to touch it.
func TestSnapshot_OffScreenElementsOfferNoTapPoint(t *testing.T) {
	snap := newSnapshot(parse(t, rawScrolledTree), Frontmost{})

	for _, label := range []string{"Below the fold", "Off to the right"} {
		element := find(t, snap, label)
		if element.Tap != nil {
			t.Fatalf("%q is off screen but offers tap %+v - a finger there lands on something else", label, *element.Tap)
		}
		if !element.OffScreen {
			t.Fatalf("%q must say it is off screen so an agent knows to scroll", label)
		}
	}

	visible := find(t, snap, "Visible row")
	if visible.Tap == nil || visible.OffScreen {
		t.Fatalf("a visible element must keep its tap point: %+v", visible)
	}
	// Half on screen still has a centre on screen, and a touch there lands.
	half := find(t, snap, "Half on")
	if half.Tap == nil || half.OffScreen {
		t.Fatalf("an element whose centre is on screen is touchable: %+v", half)
	}
}

// An agent that cannot see the counts has no way to know the screen scrolls.
func TestSnapshot_SaysHowMuchIsWithinReach(t *testing.T) {
	snap := newSnapshot(parse(t, rawScrolledTree), Frontmost{})

	if snap.OffScreenCount != 2 {
		t.Fatalf("offScreenCount = %d, want the 2 elements past the edges", snap.OffScreenCount)
	}
	if snap.OnScreenCount != snap.NodeCount-snap.OffScreenCount {
		t.Fatalf("counts do not add up: %d on screen, %d off, %d total",
			snap.OnScreenCount, snap.OffScreenCount, snap.NodeCount)
	}
}

// The addon walks the tree and then hit-tests a grid, so an element found both
// ways is reported twice. Two identical rows read as two rows.
func TestSnapshot_DropsTheSameElementReportedTwice(t *testing.T) {
	snap := newSnapshot(parse(t, rawScrolledTree), Frontmost{})

	seen := 0
	for _, child := range snap.Elements[0].Children {
		if child.Label == "Visible row" {
			seen++
		}
	}
	if seen != 1 {
		t.Fatalf("the same element at the same place appears %d times", seen)
	}
	if snap.NodeCount != 5 {
		t.Fatalf("nodeCount = %d, want 5: the root and four distinct elements", snap.NodeCount)
	}
}

// Observed on a real device: for a second after an app comes to the front, the
// tree is the status bar and nothing else. Reported as an ordinary screen, an
// agent concludes the app is blank and acts on that.
func TestSnapshot_KnowsAStatusBarIsNotAScreen(t *testing.T) {
	settling := newSnapshot(parse(t, rawStatusBarOnlyTree), Frontmost{BundleID: "com.example.app"})
	if !settling.OnlyStatusBar {
		t.Fatal("a tree of nothing but status-bar furniture must say so")
	}
	if !settling.Usable() {
		t.Fatal("it is still a read that happened - the caller decides what to do about it")
	}

	settled := newSnapshot(parse(t, rawScrolledTree), Frontmost{})
	if settled.OnlyStatusBar {
		t.Fatal("a screen with content must not read as unsettled")
	}
	if newSnapshot(nil, Frontmost{}).OnlyStatusBar {
		t.Fatal("an empty tree is empty, not a status bar")
	}
}

// An element is a rectangle, and where its edges are is what tells an agent
// whether a row is a whole card or a chevron at the end of it, how big a target
// is, and - for something below the fold - how far away it is. The edges are in
// the same 0..1 space as the tap point, so any corner of the box can be handed
// straight to `ao sim tap` without arithmetic.
func TestSnapshot_EveryElementReportsItsEdges(t *testing.T) {
	snap := newSnapshot(parse(t, rawScrolledTree), Frontmost{})

	visible := find(t, snap, "Visible row")
	if visible.Box == nil {
		t.Fatal("an element with a frame has edges")
	}
	want := Box{X1: 20.0 / 440, Y1: 200.0 / 956, X2: 420.0 / 440, Y2: 244.0 / 956}
	if !closeEnough(visible.Box.X1, want.X1) || !closeEnough(visible.Box.Y1, want.Y1) ||
		!closeEnough(visible.Box.X2, want.X2) || !closeEnough(visible.Box.Y2, want.Y2) {
		t.Fatalf("box = %+v, want %+v", *visible.Box, want)
	}

	// An element below the fold keeps its real edges rather than being squashed
	// onto the screen: "0.4 of a screen further down" is how far to scroll.
	below := find(t, snap, "Below the fold")
	if below.Box == nil || below.Box.Y1 <= 1 {
		t.Fatalf("box = %+v, want edges past the bottom of the screen", below.Box)
	}
}
