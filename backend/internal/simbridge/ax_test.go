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
