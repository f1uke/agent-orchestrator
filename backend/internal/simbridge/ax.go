package simbridge

import (
	"fmt"
	"strconv"
)

// The accessibility tree, as an agent should receive it.
//
// Two design choices here are the difference between a tree an agent can act on
// and one it has to guess from:
//
//  1. The tree keeps its shape. A flat list loses which label belongs to which
//     row, which is exactly what "check this screen" turns on.
//  2. Every element carries a precomputed tap point, normalized 0..1, which is
//     the coordinate space the touch commands take. An agent never does
//     coordinate arithmetic and never estimates a position from a screenshot -
//     the two ways a tap lands somewhere nobody intended.

// rawAXNode is the addon's own shape (an idb-derived "axe" node). Frames are in
// points, in screen space.
type rawAXNode struct {
	AXUniqueId      *string     `json:"AXUniqueId"`
	AXLabel         *string     `json:"AXLabel"`
	AXValue         *string     `json:"AXValue"`
	Enabled         bool        `json:"enabled"`
	Frame           Rect        `json:"frame"`
	RoleDescription string      `json:"role_description"`
	Type            string      `json:"type"`
	Children        []rawAXNode `json:"children"`
}

// Rect is a frame in device points.
type Rect struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

// Size is the screen, in device points.
type Size struct {
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

// Point is a normalized 0..1 screen coordinate - what `ao sim tap` takes.
type Point struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// Box is an element's edges in the same 0..1 space as Point: left, top, right,
// bottom. An element is a rectangle, and a centre alone does not say whether it
// is a whole card or the chevron at the end of one, how big a target is, or -
// for something below the fold - how far away it is. Values are deliberately
// not clipped to the screen: 1.4 means "four tenths of a screen further down",
// which is what a caller needs to know how far to scroll.
type Box struct {
	X1 float64 `json:"x1"`
	Y1 float64 `json:"y1"`
	X2 float64 `json:"x2"`
	Y2 float64 `json:"y2"`
}

// Frontmost is the app actually on screen. It is reported with every tree
// because "no elements" and "you are looking at the home screen, not your app"
// need very different responses.
type Frontmost struct {
	BundleID string `json:"bundleId,omitempty"`
	PID      int    `json:"pid,omitempty"`
}

// Element is one accessibility element.
type Element struct {
	// Path is the index path from the roots ("0.1.2"). It is the handle that
	// always exists; ID often does not.
	Path string `json:"path"`
	// ID is AXUniqueId when the app sets one. Apps frequently do not, so it is
	// reported when present and never depended on.
	ID      string `json:"id,omitempty"`
	Role    string `json:"role,omitempty"`
	Type    string `json:"type,omitempty"`
	Label   string `json:"label,omitempty"`
	Value   string `json:"value,omitempty"`
	Enabled bool   `json:"enabled"`
	Frame   Rect   `json:"frame"`
	// Tap is where to touch this element, and is absent when there is nowhere
	// to touch: an element scrolled off the screen has no point that reaches it.
	// It used to be clamped onto the screen's edge instead, which is how a tap
	// meant for a row below the fold landed on the tab bar.
	Tap *Point `json:"tap,omitempty"`
	// Box is the element's edges, normalized like Tap. It is present even when
	// Tap is not: where an element is remains useful once it is known that it
	// cannot be touched from here.
	Box *Box `json:"box,omitempty"`
	// OffScreen says the element is on the page but not on the screen. It is
	// reported rather than dropped: knowing a control exists further down is
	// what tells an agent to scroll rather than to give up.
	OffScreen bool      `json:"offScreen,omitempty"`
	Children  []Element `json:"children,omitempty"`
}

// Snapshot is one read of a screen.
type Snapshot struct {
	Screen    Size      `json:"screen"`
	Frontmost Frontmost `json:"frontmost"`
	Elements  []Element `json:"elements"`
	// NodeCount is how many elements this snapshot contains, TotalNodeCount how
	// many the device reported. They differ only when Truncated.
	NodeCount      int  `json:"nodeCount"`
	TotalNodeCount int  `json:"totalNodeCount"`
	Truncated      bool `json:"truncated"`
	// OnScreenCount and OffScreenCount split the tree into what can be touched
	// now and what needs scrolling to first. On a real app screen most of the
	// tree is often the second kind, and nothing else says so.
	OnScreenCount  int `json:"onScreenCount"`
	OffScreenCount int `json:"offScreenCount"`
	// OnlyStatusBar is a tree that is the clock, the battery and nothing else.
	// It happens for a second after an app comes to the front, before it
	// publishes its screen - and read as an ordinary result it says the app is
	// blank, which is the wrong thing to act on.
	OnlyStatusBar bool `json:"onlyStatusBar,omitempty"`
}

// newSnapshot converts the addon's tree, computing a tap point per element.
// The screen is the first root's frame, which is how the device reports the
// display; every tap point is normalized against it.
func newSnapshot(roots []rawAXNode, front Frontmost) Snapshot {
	snap := Snapshot{Frontmost: front, Elements: []Element{}}
	if len(roots) == 0 {
		return snap
	}
	snap.Screen = Size{Width: roots[0].Frame.Width, Height: roots[0].Frame.Height}
	count := 0
	snap.Elements = convertNodes(dedupe(roots), "", snap.Screen, &count)
	snap.NodeCount, snap.TotalNodeCount = count, count
	snap.OnScreenCount, snap.OffScreenCount = reach(snap.Elements)
	snap.OnlyStatusBar = onlyStatusBar(snap.Elements, snap.Screen)
	return snap
}

// dedupe drops a node that repeats one already reported beside it.
//
// The addon finds elements twice on purpose: it walks the accessibility tree,
// then hit-tests a grid of points to catch containers that hide their children
// from the walk. Anything found both ways arrives twice, and two identical rows
// read as two rows - so the same element, in the same place, is kept once.
func dedupe(nodes []rawAXNode) []rawAXNode {
	seen := make(map[string]bool, len(nodes))
	out := make([]rawAXNode, 0, len(nodes))
	for _, node := range nodes {
		key := fmt.Sprintf("%s|%s|%s|%s|%v", node.Type, node.RoleDescription,
			deref(node.AXLabel), deref(node.AXValue), node.Frame)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, node)
	}
	return out
}

func convertNodes(nodes []rawAXNode, prefix string, screen Size, count *int) []Element {
	out := make([]Element, 0, len(nodes))
	for i, node := range nodes {
		path := indexPath(prefix, i)
		*count++
		tap := tapPoint(node.Frame, screen)
		out = append(out, Element{
			Path:      path,
			ID:        deref(node.AXUniqueId),
			Role:      node.RoleDescription,
			Type:      node.Type,
			Label:     deref(node.AXLabel),
			Value:     deref(node.AXValue),
			Enabled:   node.Enabled,
			Frame:     node.Frame,
			Tap:       tap,
			Box:       box(node.Frame, screen),
			OffScreen: tap == nil,
			Children:  convertNodes(dedupe(node.Children), path, screen, count),
		})
	}
	return out
}

// tapPoint is the element's centre in the 0..1 space the HID layer takes, or
// nil when that centre is not on the screen at all.
//
// It used to clamp instead, which produced a point for everything: a row 400pt
// below the fold reported the bottom edge of the screen, and a tap there landed
// on whatever is really at the bottom edge. Reporting no point is the honest
// answer - the element is on the page, not on the screen, and the way to reach
// it is to scroll.
func tapPoint(frame Rect, screen Size) *Point {
	if screen.Width <= 0 || screen.Height <= 0 {
		return nil
	}
	x := (frame.X + frame.Width/2) / screen.Width
	y := (frame.Y + frame.Height/2) / screen.Height
	if x < 0 || x > 1 || y < 0 || y > 1 {
		return nil
	}
	return &Point{X: x, Y: y}
}

// clamp01 keeps a coordinate on the screen. It is for the recovery lift, where
// the point only has to be somewhere valid - never for a tap, where a clamped
// coordinate is a touch on whatever happens to be at the edge.
func clamp01(v float64) float64 {
	switch {
	case v < 0:
		return 0
	case v > 1:
		return 1
	default:
		return v
	}
}

// box is the element's edges in the tap point's own units. Unclipped on
// purpose - see Box.
func box(frame Rect, screen Size) *Box {
	if screen.Width <= 0 || screen.Height <= 0 {
		return nil
	}
	return &Box{
		X1: frame.X / screen.Width,
		Y1: frame.Y / screen.Height,
		X2: (frame.X + frame.Width) / screen.Width,
		Y2: (frame.Y + frame.Height) / screen.Height,
	}
}

// reach counts what can be touched now against what has to be scrolled to.
func reach(elements []Element) (onScreen, offScreen int) {
	for _, element := range elements {
		if element.OffScreen {
			offScreen++
		} else {
			onScreen++
		}
		on, off := reach(element.Children)
		onScreen += on
		offScreen += off
	}
	return onScreen, offScreen
}

// statusBarFraction is how much of the screen's height the status bar occupies.
// The items in it (clock, cellular, battery) end around 51pt on a 956pt screen,
// and the app's own navigation bar starts at 56pt, so 6% separates them on
// every size of device without either being a hard-coded point value.
const statusBarFraction = 0.06

// onlyStatusBar reports a tree that has nothing in it but the furniture at the
// top of the screen. A blank app screen looks the same from here, which is why
// the caller words it as a possibility rather than a fact.
func onlyStatusBar(elements []Element, screen Size) bool {
	if len(elements) == 0 || screen.Height <= 0 {
		return false
	}
	bottom := screen.Height * statusBarFraction
	found := false
	var walk func([]Element)
	walk = func(nodes []Element) {
		for _, node := range nodes {
			// The application element is the whole screen by definition; it says
			// nothing about what the app has drawn.
			if node.Frame.Height < screen.Height && node.Frame.Y+node.Frame.Height > bottom {
				found = true
				return
			}
			walk(node.Children)
		}
	}
	walk(elements)
	return !found
}

// Truncate caps the snapshot at limit elements, keeping a walkable prefix of the
// tree (parents before children) and recording how much was dropped.
//
// A cap exists because a real app screen can report far more elements than an
// agent can usefully read, and an unbounded dump crowds out the reasoning that
// is supposed to happen next. Dropping silently would be worse than the cap:
// the count and the flag are how the caller knows to raise it.
func (s Snapshot) Truncate(limit int) Snapshot {
	if limit <= 0 || s.NodeCount <= limit {
		return s
	}
	budget := limit
	s.Elements = truncateNodes(s.Elements, &budget)
	s.NodeCount = limit
	s.Truncated = true
	return s
}

func truncateNodes(nodes []Element, budget *int) []Element {
	out := make([]Element, 0, len(nodes))
	for _, node := range nodes {
		if *budget <= 0 {
			break
		}
		*budget--
		node.Children = truncateNodes(node.Children, budget)
		out = append(out, node)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// Usable reports whether this looks like a screen at all. An empty tree is not
// an empty screen: it means accessibility answered with nothing, and a caller
// must say so rather than report "no elements found" as a finding.
func (s Snapshot) Usable() bool {
	return s.NodeCount > 0 && s.Screen.Width > 0 && s.Screen.Height > 0
}

func indexPath(prefix string, i int) string {
	digits := strconv.Itoa(i)
	if prefix == "" {
		return digits
	}
	return prefix + "." + digits
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
