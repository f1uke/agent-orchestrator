package simbridge

import "strconv"

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
	ID       string    `json:"id,omitempty"`
	Role     string    `json:"role,omitempty"`
	Type     string    `json:"type,omitempty"`
	Label    string    `json:"label,omitempty"`
	Value    string    `json:"value,omitempty"`
	Enabled  bool      `json:"enabled"`
	Frame    Rect      `json:"frame"`
	Tap      Point     `json:"tap"`
	Children []Element `json:"children,omitempty"`
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
	snap.Elements = convertNodes(roots, "", snap.Screen, &count)
	snap.NodeCount, snap.TotalNodeCount = count, count
	return snap
}

func convertNodes(nodes []rawAXNode, prefix string, screen Size, count *int) []Element {
	out := make([]Element, 0, len(nodes))
	for i, node := range nodes {
		path := indexPath(prefix, i)
		*count++
		out = append(out, Element{
			Path:     path,
			ID:       deref(node.AXUniqueId),
			Role:     node.RoleDescription,
			Type:     node.Type,
			Label:    deref(node.AXLabel),
			Value:    deref(node.AXValue),
			Enabled:  node.Enabled,
			Frame:    node.Frame,
			Tap:      tapPoint(node.Frame, screen),
			Children: convertNodes(node.Children, path, screen, count),
		})
	}
	return out
}

// tapPoint is the element's centre in the 0..1 space the HID layer takes. It is
// clamped because an element can extend past the screen (a row scrolled halfway
// off), and a touch outside the screen simply does not land.
func tapPoint(frame Rect, screen Size) Point {
	if screen.Width <= 0 || screen.Height <= 0 {
		return Point{}
	}
	return Point{
		X: clamp01((frame.X + frame.Width/2) / screen.Width),
		Y: clamp01((frame.Y + frame.Height/2) / screen.Height),
	}
}

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
