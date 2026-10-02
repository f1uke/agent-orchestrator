package simbridge

import (
	"fmt"
	"math"
	"unicode"
)

// The screen as AO's XCTest runner reads it (internal/simrunner).
//
// The addon above reads the frontmost app's own process, which is blind to
// everything another process draws into it: an ASWebAuthenticationSession's
// web page (SafariViewService), SpringBoard's alerts, the text-edit callout,
// the keyboard. XCTest reads all of them. Its tree arrives here as one root per
// application on screen and leaves as the same Snapshot the addon produces, so
// nothing above - tap by name, settling, the Maestro selectors - has to know
// which reader answered.

// Reader sources, as reported on a Snapshot.
const (
	// SourceXCTest is the AO-owned XCTest runner: every process on screen.
	SourceXCTest = "xctest"
	// SourceAccessibility is the in-app accessibility bridge: the frontmost
	// app's own process only.
	SourceAccessibility = "accessibility"
)

// Reader says which mechanism read a screen, and - when it is the narrower
// one - why, because "the web page is not in this tree" is the first thing a
// caller would otherwise conclude about the app.
type Reader struct {
	Source string `json:"source"`
	// Note is set when the XCTest runner was wanted and did not answer.
	Note string `json:"note,omitempty"`
}

// XCTestHierarchy is the runner's GET /hierarchy answer.
type XCTestHierarchy struct {
	Version string      `json:"version"`
	Screen  Size        `json:"screen"`
	Apps    []XCTestApp `json:"apps"`
	Errors  []string    `json:"errors,omitempty"`
	// ElapsedMs is how long the snapshot took inside the runner.
	ElapsedMs int `json:"elapsedMs"`
	// ForegroundSource is which XCTest route named the foreground app.
	ForegroundSource string `json:"foregroundSource,omitempty"`
}

// XCTestApp is one application's tree. RemoteView marks a process that draws
// into another app's screen (a web sign-in sheet) rather than being the app
// on screen itself.
type XCTestApp struct {
	BundleID   string     `json:"bundleId"`
	PID        int        `json:"pid,omitempty"`
	RemoteView bool       `json:"remoteView,omitempty"`
	Tree       XCTestNode `json:"tree"`
}

// XCTestNode is one element as XCTest snapshots it. Type is the
// XCUIElement.ElementType's name ("Button", "SecureTextField", "MenuItem").
type XCTestNode struct {
	Type        string       `json:"type"`
	ID          string       `json:"id,omitempty"`
	Label       string       `json:"label,omitempty"`
	Value       string       `json:"value,omitempty"`
	Placeholder string       `json:"placeholder,omitempty"`
	Frame       Rect         `json:"frame"`
	Enabled     bool         `json:"enabled"`
	Selected    bool         `json:"selected,omitempty"`
	Focused     bool         `json:"focused,omitempty"`
	Children    []XCTestNode `json:"children,omitempty"`
}

// SnapshotFromXCTest converts the runner's tree.
//
// The application on screen is reported as Frontmost, never a remote-view host
// drawn over it: a web sign-in sheet belongs to the app that presented it, and
// "which app is this" is what the caller is asking. The host's tree still comes
// first, because it is on top.
//
// XCTest's tree is far deeper than the addon's: most of it is unnamed layout
// containers, nested twenty deep around a web page. They are dropped and their
// children kept, so the tree an agent reads is the controls and the text, in
// their real order. An element repeated exactly - the keyboard, which both an
// app and the sheet over it report, or a scroll indicator XCTest lists twice -
// is kept once.
func SnapshotFromXCTest(h XCTestHierarchy) Snapshot {
	snap := Snapshot{Screen: h.Screen, Elements: []Element{}, Reader: &Reader{Source: SourceXCTest}}
	for _, app := range h.Apps {
		if !app.RemoteView && snap.Frontmost.BundleID == "" {
			snap.Frontmost = Frontmost{BundleID: app.BundleID, PID: app.PID}
		}
	}
	if snap.Frontmost.BundleID == "" && len(h.Apps) > 0 {
		snap.Frontmost = Frontmost{BundleID: h.Apps[0].BundleID, PID: h.Apps[0].PID}
	}
	if snap.Screen.Width <= 0 || snap.Screen.Height <= 0 {
		return snap
	}

	c := xctestConverter{screen: snap.Screen, seen: map[string]bool{}}
	for _, app := range h.Apps {
		root := app.Tree
		// The application element is the one container always kept: it is
		// the screen's root, and its id says whose tree follows - the label
		// alone reads "Safari" for a sign-in sheet that is nothing of the sort.
		if root.ID == "" {
			root.ID = app.BundleID
		}
		c.seen[xctestKey(root)] = true
		el := c.element(root, indexPath("", len(snap.Elements)))
		snap.Elements = append(snap.Elements, el)
	}
	snap.NodeCount, snap.TotalNodeCount = c.count, c.count
	snap.OnScreenCount, snap.OffScreenCount = reach(snap.Elements)
	snap.OnlyStatusBar = onlyStatusBar(snap.Elements, snap.Screen)
	snap.Keyboard = keyboardOf(snap.Elements)
	return snap
}

type xctestConverter struct {
	screen Size
	seen   map[string]bool
	count  int
}

// children converts a sibling list into *into. A dropped element's children
// take its place, so paths stay a plain index into what is reported.
func (c *xctestConverter) children(nodes []XCTestNode, prefix string, into *[]Element) {
	for _, node := range nodes {
		key := xctestKey(node)
		// A zero-sized element (keyboard padding, a collapsed wrapper) cannot
		// be seen or touched, but a wrapper of zero size still holds real
		// controls: a web view's toolbar buttons sit three of them deep.
		zero := node.Frame.Width <= 0 || node.Frame.Height <= 0
		if zero || c.seen[key] || isLayoutContainer(node) {
			// Not reported itself, but whatever it holds is still on screen.
			c.children(node.Children, prefix, into)
			continue
		}
		c.seen[key] = true
		*into = append(*into, c.element(node, indexPath(prefix, len(*into))))
	}
}

func (c *xctestConverter) element(node XCTestNode, path string) Element {
	c.count++
	value := node.Value
	if value == node.Label {
		// XCTest reports a web page's text as both; the addon never did.
		value = ""
	}
	tap := tapPoint(node.Frame, c.screen)
	el := Element{
		Path:        path,
		ID:          node.ID,
		Type:        node.Type,
		Label:       node.Label,
		Value:       value,
		Placeholder: node.Placeholder,
		Enabled:     node.Enabled,
		Focused:     node.Focused,
		Selected:    node.Selected,
		Frame:       node.Frame,
		Tap:         tap,
		Box:         box(node.Frame, c.screen),
		OffScreen:   tap == nil,
	}
	c.children(node.Children, path, &el.Children)
	return el
}

// isLayoutContainer is an element that says nothing about the screen: no
// name, no value, no identifier, and a type that only groups things.
func isLayoutContainer(node XCTestNode) bool {
	if node.Label != "" || node.Value != "" || node.ID != "" || node.Focused {
		return false
	}
	switch node.Type {
	case "Other", "Group", "Window", "ScrollView", "WebView", "Cell", "Table", "CollectionView", "Any":
		return true
	}
	return false
}

// xctestKey is an element's identity for repetition: the same kind of thing,
// with the same name, in the same place.
func xctestKey(node XCTestNode) string {
	r := func(v float64) int64 { return int64(math.Round(v)) }
	return fmt.Sprintf("%s|%s|%s|%s|%d,%d,%d,%d", node.Type, node.ID, node.Label, node.Value,
		r(node.Frame.X), r(node.Frame.Y), r(node.Frame.Width), r(node.Frame.Height))
}

// KeyboardState is the software keyboard as the screen shows it. The addon
// cannot see it at all; XCTest can, and which letters are on it is what
// decides whether typing arrives as typed.
type KeyboardState struct {
	// Keys is how many character keys are showing.
	Keys int `json:"keys"`
	// Latin says every letter key is an a-z letter. A Thai (or any other
	// non-Latin) layout is false, and is the input mode that turns
	// "finno123" into three Thai characters.
	Latin bool `json:"latin"`
	// NextInputMode is what the globe key switches TO - iOS reports the next
	// mode as the key's value, not the current one (measured: a Thai layout
	// on screen, value "English (US)"; tapped, a Latin layout, value
	// "ภาษาไทย"). Empty when the device has a single keyboard.
	NextInputMode string `json:"nextInputMode,omitempty"`
}

// nextKeyboardLabel is the globe key. iOS names it this in English whatever
// the device's language.
const nextKeyboardLabel = "Next keyboard"

// keyboardOf reads the keyboard that is ON SCREEN. A focused field with the
// software keyboard hidden - a hardware keyboard attached, or the simulator
// believing one is after HID key presses - still lists the keyboard, below
// the bottom edge (seen at y 1.09-1.36 on iOS 26.3), and reading that one as
// "up" says the opposite of what the screen shows.
func keyboardOf(elements []Element) *KeyboardState {
	var state *KeyboardState
	walk(elements, func(e Element) {
		if e.Type == "Keyboard" && !e.OffScreen && state == nil {
			state = &KeyboardState{}
		}
	})
	if state == nil {
		return nil
	}
	letters, latin := 0, 0
	walk(elements, func(e Element) {
		switch {
		case e.OffScreen:
		case e.Type == "Key":
			state.Keys++
			if r := []rune(e.Label); len(r) == 1 && unicode.IsLetter(r[0]) {
				letters++
				if r[0] < unicode.MaxASCII {
					latin++
				}
			}
		case e.Label == nextKeyboardLabel && state.NextInputMode == "":
			state.NextInputMode = e.Value
		}
	})
	state.Latin = letters > 0 && latin == letters
	return state
}
