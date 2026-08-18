// Package simflow turns what `ao sim ax` read off a screen into Maestro
// selectors.
//
// It is deliberately pure: a function of a Snapshot and nothing else. That is
// what lets every rule in here be table-tested on Linux CI with no Xcode, no
// node, no simulator and no `maestro` binary - the same property that makes
// simbridge's own rules testable.
package simflow

import (
	"math"
	"regexp"
	"sort"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/simbridge"
)

// Rung is which step of the selector ladder produced a Choice.
//
// It travels with the Choice because "a unique label" and "we could only find a
// coordinate" need very different treatment by whoever reads the flow: the
// first is stable, the last breaks on any layout change.
type Rung int

const (
	// RungNone means nothing on this element can address it.
	RungNone Rung = iota
	// RungText is a unique label - the house style of a Maestro suite.
	RungText
	// RungTextIndex is a label that repeats, so an index picks one of them.
	RungTextIndex
	// RungID is no label, but the app set an accessibility identifier.
	RungID
	// RungPoint is neither - a screen coordinate, which is brittle.
	RungPoint
	// RungTextAnchor is a label that repeats, narrowed by a nearby unique
	// label instead of by an index.
	//
	// It is numerically LAST rather than in ladder order on purpose: a Rung is
	// persisted on every recorded step (sim_recording_step.selector_rung), so
	// renumbering the existing constants would silently reinterpret every
	// recording already on disk. Ladder order lives in For, which is the only
	// thing that decides it; the numbers are storage.
	RungTextAnchor
)

// Relation is one of Maestro's relative-position selectors.
//
// The predicates are VERIFIED by decompiling the installed maestro-client
// 2.8.0 (Filters.below/above/leftOf/rightOf), not assumed from prose, and two
// details matter enough to write down because a reasonable guess gets both
// wrong:
//
//   - they compare the element's TOP/LEFT EDGE (Bounds.y / Bounds.x), not its
//     centre;
//   - there is NO overlap requirement on the other axis. `below` means "starts
//     lower down the screen", not "in the same column".
//
// anchorFor's own rule is built to survive that looseness: see For.
type Relation string

// The four relative selectors, spelled exactly as the YAML key Maestro takes.
const (
	RelBelow   Relation = "below"
	RelAbove   Relation = "above"
	RelLeftOf  Relation = "leftOf"
	RelRightOf Relation = "rightOf"
)

// relations is the order anchorFor tries them in. Fixed, so the same screen
// always produces the same flow - a generator whose output depends on map
// iteration order produces diffs nobody can review.
var relations = []Relation{RelBelow, RelAbove, RelLeftOf, RelRightOf}

// edgeMargin is how far apart two edges must be before this package will call
// one of them clearly past the other.
//
// It exists because the two sides measure in different types. Our frames are
// float points straight off the accessibility API; Maestro's Bounds are ints.
// Two edges 0.3 points apart are "below" here and exactly equal there, and the
// direction that breaks is the silent one: a relation we believed singled out
// one candidate can, after Maestro rounds, admit two - and Maestro then takes
// the nearest, which is the wrong-element failure this whole rung exists to
// remove.
//
// One whole point is the smallest gap that survives either rounding or
// truncation on Maestro's side, so the verdict cannot change no matter which
// it does - and a gap of exactly one point qualifies, because two ints one
// apart are strictly ordered however the fractions fell. Measured against the real screens this rung was designed on, it
// costs nothing at all: the same 22 of 29 ambiguous elements get an anchor
// with it as without it.
const edgeMargin = 1.0

// satisfies reports whether candidate stands clearly in rel to anchor.
//
// The axis and direction are Maestro's own (VERIFIED from Filters, see
// Relation): the TOP edge for below/above, the LEFT edge for leftOf/rightOf.
// Only the margin is ours.
func satisfies(rel Relation, candidate, anchor simbridge.Rect) bool {
	switch rel {
	case RelBelow:
		return candidate.Y-anchor.Y >= edgeMargin
	case RelAbove:
		return anchor.Y-candidate.Y >= edgeMargin
	case RelLeftOf:
		return anchor.X-candidate.X >= edgeMargin
	case RelRightOf:
		return candidate.X-anchor.X >= edgeMargin
	}
	return false
}

// ScrollDirection is which way scrollUntilVisible should search for an
// off-screen element.
type ScrollDirection string

const (
	// ScrollDown is the common case - most of a scrolling screen sits below the
	// fold - and the safe default when an element's edges are not known.
	ScrollDown ScrollDirection = "DOWN"
	// ScrollUp is for an element entirely above the top of the viewport, which
	// simbridge also reports as OffScreen: scrolling down would move away from
	// it, not toward it.
	ScrollUp ScrollDirection = "UP"
)

// Choice is the selector for one element plus everything a reader needs in
// order to decide how much to trust it.
type Choice struct {
	Rung Rung
	// Text is the label to match on, already escaped if it needed escaping.
	Text    string
	Escaped bool
	// Index is which of the Ambiguity matches this element is, in tree order.
	Index int
	// Ambiguity is how many elements share this text. 1 means unique.
	//
	// It is a LOWER BOUND. We count against our own tree; Maestro walks the
	// XCUITest hierarchy, which on the same screen reported 176 nodes where we
	// reported 21, because one label commonly sits on both a container and its
	// child. A Choice we call unique may match several nodes for Maestro. That
	// is not fixable from here - it is what running the flow is for.
	Ambiguity int
	ID        string
	PercentX  int
	PercentY  int
	OffScreen bool
	// Anchor and Relation carry a RungTextAnchor selector: "the one element
	// with this text that lies <Relation> the element labelled <Anchor>".
	//
	// They exist because Index is the one part of a selector whose meaning
	// differs between the tree we author from and the tree that replays it -
	// measured at 14% landing on a DIFFERENT element, silently. A relative
	// selector carries no index at all: Maestro evaluates it wholly inside its
	// own hierarchy, so the two trees only have to agree about geometry, which
	// they do. Anchor is escaped exactly like Text.
	Anchor        string
	AnchorEscaped bool
	Relation      Relation
	// ScrollDirection is which way scrollUntilVisible should search when
	// OffScreen is set. It is decided from the element's own edges, not just
	// whether it is off screen, because simbridge sets OffScreen for an element
	// above the top of the viewport too - and scrolling down moves away from
	// that one, not toward it.
	ScrollDirection ScrollDirection
}

// metacharacters is the set that makes a label behave as a pattern rather than
// a literal. Maestro compiles the string as a regex and also compares it
// literally, so an unescaped label still matches itself - but an unescaped
// "Continue." would ALSO match "Continue!", and tapping the wrong element is
// worse than an ugly string.
//
// It is written once, as the set itself: escape tests a whole label with the
// character class built from it, and Unescape walks a byte at a time. Two
// copies of this list would be two things to keep in step, and the failure of
// disagreeing is silent.
const metacharacters = `(){}[].+*?^$|\`

var metacharacter = regexp.MustCompile(`[` + regexp.QuoteMeta(metacharacters) + `]`)

// For picks the best selector for el, using snap only to count collisions.
func For(snap simbridge.Snapshot, el simbridge.Element) Choice {
	c := Choice{Rung: RungNone, OffScreen: el.OffScreen, ScrollDirection: scrollDirectionFor(el.Box)}

	if label := strings.TrimSpace(el.Label); label != "" {
		matches := matchingPaths(snap, label)
		c.Ambiguity = len(matches)
		c.Index = indexOf(matches, el.Path)
		c.Text, c.Escaped = escape(label)
		switch {
		case c.Ambiguity <= 1:
			c.Rung = RungText
		default:
			// An index is the last resort, not the first: it is the only part
			// of a selector that means something different in Maestro's tree
			// than in ours. Try to name the element by where it sits relative
			// to a label that IS unique, which Maestro resolves entirely in
			// its own hierarchy.
			if anchor, rel, ok := anchorFor(snap, el, label); ok {
				c.Rung = RungTextAnchor
				c.Anchor, c.AnchorEscaped = escape(anchor)
				c.Relation = rel
			} else {
				c.Rung = RungTextIndex
			}
		}
		return c
	}

	if id := strings.TrimSpace(el.ID); id != "" {
		c.Rung, c.ID = RungID, id
		return c
	}

	if el.Tap != nil {
		c.Rung = RungPoint
		c.PercentX = percent(el.Tap.X)
		c.PercentY = percent(el.Tap.Y)
		return c
	}

	return c
}

// ForAmbiguousText is the Choice for a label that WAS searched for by name and
// matched several elements - the one case a caller cannot get from For,
// because there is no single element to pass it.
//
// It exists so that case cannot skip the escaping every other path gets by
// going through For. Maestro compiles a text matcher as a regex, so an
// unescaped "Continue." also matches "Continue!", and tapping the wrong
// element is precisely the failure escaping exists to prevent - a by-name tap
// that could not be told apart from its neighbours is the LAST place to relax
// it. No Index is set, deliberately: which candidate was actually hit is
// unknown, and inventing one would substitute an element nobody chose.
func ForAmbiguousText(label string, ambiguity int) Choice {
	text, escaped := escape(strings.TrimSpace(label))
	return Choice{Rung: RungText, Text: text, Escaped: escaped, Ambiguity: ambiguity}
}

// Unescape recovers the plain label from text that went through escape - the
// form every selector is STORED in (a recorded step keeps Choice.Text, which
// is already escaped), and the form nothing that reads a label back wants.
// scrollUntilVisible searches for the label a human reads, not a pattern.
//
// It is an exact inverse for anything escape produced: escape only ever
// inserts a backslash before one of the metacharacters below, and a label
// containing a backslash is itself escaped (a backslash IS one of them), so a
// stored selector either has escaped pairs or no backslashes at all. A
// backslash before anything else is left alone rather than guessed at.
// escaped reports whether anything was actually unescaped, which is the fact
// Render's "# escaped: ..." comment is written from.
func Unescape(text string) (plain string, escaped bool) {
	var b strings.Builder
	b.Grow(len(text))
	for i := 0; i < len(text); i++ {
		if text[i] == '\\' && i+1 < len(text) && strings.IndexByte(metacharacters, text[i+1]) >= 0 {
			b.WriteByte(text[i+1])
			escaped = true
			i++
			continue
		}
		b.WriteByte(text[i])
	}
	return b.String(), escaped
}

// anchorFor looks for a unique label that pins el down without an index.
//
// The rule is deliberately strict: an anchor is accepted only when EXACTLY ONE
// candidate satisfies the relation. Maestro's relative filters keep every
// element that satisfies the predicate and hand back the nearest, so a laxer
// rule would work only as long as both trees agree on which is nearest -
// exactly the kind of cross-tree assumption the index already got wrong. If
// two candidates qualify the anchor is refused and the caller falls to the
// index, which is at least honest about being a guess.
//
// Anchors are tried nearest-first so the emitted flow reads the way a human
// would have written it ("the price below THIS heading", not below something
// across the screen), with the element path as a tie-break so the same screen
// always yields the same flow.
func anchorFor(snap simbridge.Snapshot, el simbridge.Element, label string) (string, Relation, bool) {
	candidates := matchingElements(snap, label)
	if len(candidates) < 2 {
		return "", "", false
	}
	for _, anchor := range anchorsByDistance(snap, el, textCounts(snap)) {
		for _, rel := range relations {
			only, ok := lone(candidates, anchor.Frame, rel)
			if ok && only.Path == el.Path {
				return strings.TrimSpace(anchor.Label), rel, true
			}
		}
	}
	return "", "", false
}

// lone returns the single candidate standing in rel to anchor, if there is
// exactly one.
func lone(candidates []simbridge.Element, anchor simbridge.Rect, rel Relation) (simbridge.Element, bool) {
	var found simbridge.Element
	n := 0
	for _, c := range candidates {
		if satisfies(rel, c.Frame, anchor) {
			found, n = c, n+1
		}
	}
	return found, n == 1
}

// anchorsByDistance lists the elements that could serve as an anchor for el -
// on screen, labelled, and unique by the same union rule Maestro matches text
// with - nearest first.
//
// Off-screen elements are excluded: Maestro's hierarchy only reports what
// XCUITest can see, so anchoring to a row below the fold names something that
// is not there at replay. The measurement found 0 of 24 off-screen labels
// present in Maestro's tree, so this is not a precaution but a rule.
func anchorsByDistance(snap simbridge.Snapshot, el simbridge.Element, counts map[string]int) []simbridge.Element {
	var out []simbridge.Element
	var walk func(els []simbridge.Element)
	walk = func(els []simbridge.Element) {
		for _, cand := range els {
			label := strings.TrimSpace(cand.Label)
			if label != "" && !cand.OffScreen && cand.Path != el.Path && counts[label] == 1 {
				out = append(out, cand)
			}
			walk(cand.Children)
		}
	}
	walk(snap.Elements)
	sort.SliceStable(out, func(i, j int) bool {
		di, dj := distance(el.Frame, out[i].Frame), distance(el.Frame, out[j].Frame)
		if di != dj {
			return di < dj
		}
		return out[i].Path < out[j].Path
	})
	return out
}

// textCounts is how many elements Maestro's text matcher would consider for
// each string on the screen, in one walk.
//
// anchorsByDistance needs this for every element it looks at, and asking
// matchingPaths per element would make choosing an anchor quadratic inside a
// function that is itself called once per element. Same matching rule as
// matchingPaths, so the two cannot drift: a Value that equals another
// element's Label is a real collision.
func textCounts(snap simbridge.Snapshot) map[string]int {
	counts := make(map[string]int)
	var walk func(els []simbridge.Element)
	walk = func(els []simbridge.Element) {
		for _, el := range els {
			for text := range map[string]struct{}{
				strings.TrimSpace(el.Label): {},
				strings.TrimSpace(el.Value): {},
			} {
				if text != "" {
					counts[text]++
				}
			}
			walk(el.Children)
		}
	}
	walk(snap.Elements)
	return counts
}

// distance is the squared distance between two frames' centres. Squared
// because only the ordering is used, and squaring keeps it exact in float
// arithmetic where a square root would not be.
func distance(a, b simbridge.Rect) float64 {
	dx := (a.X + a.Width/2) - (b.X + b.Width/2)
	dy := (a.Y + a.Height/2) - (b.Y + b.Height/2)
	return dx*dx + dy*dy
}

// matchingElements is matchingPaths' sibling for the cases that need the
// element and not just its path. The two share one definition of "Maestro
// would consider this a match" via matchingPaths - see there for why Value is
// compared as well as Label.
func matchingElements(snap simbridge.Snapshot, text string) []simbridge.Element {
	var out []simbridge.Element
	var walk func(els []simbridge.Element)
	walk = func(els []simbridge.Element) {
		for _, el := range els {
			if strings.TrimSpace(el.Label) == text || strings.TrimSpace(el.Value) == text {
				out = append(out, el)
			}
			walk(el.Children)
		}
	}
	walk(snap.Elements)
	return out
}

// matchingPaths lists, in tree order, the elements Maestro's text matcher would
// consider for this string.
//
// Maestro matches the union of `text`, `hintText` and `accessibilityText`,
// where `text` is title-or-value and `accessibilityText` is the label. We hold
// Label and Value, so both are compared: a Value that equals another element's
// Label is a real collision and pretending otherwise would emit a selector that
// silently picks the wrong node.
func matchingPaths(snap simbridge.Snapshot, text string) []string {
	var paths []string
	var walk func(els []simbridge.Element)
	walk = func(els []simbridge.Element) {
		for _, el := range els {
			if strings.TrimSpace(el.Label) == text || strings.TrimSpace(el.Value) == text {
				paths = append(paths, el.Path)
			}
			walk(el.Children)
		}
	}
	walk(snap.Elements)
	return paths
}

func indexOf(paths []string, path string) int {
	for i, p := range paths {
		if p == path {
			return i
		}
	}
	return 0
}

func escape(label string) (string, bool) {
	if !metacharacter.MatchString(label) {
		return label, false
	}
	return regexp.QuoteMeta(label), true
}

// scrollDirectionFor decides which way scrollUntilVisible should search, from
// an element's normalized box.
//
// simbridge sets OffScreen whenever an element's centre falls outside the
// viewport in any direction, not just below it - a header pinned above the
// current scroll position is off screen the same way a row further down is.
// Scrolling DOWN for the first case moves away from the element, not toward
// it.
func scrollDirectionFor(box *simbridge.Box) ScrollDirection {
	if box == nil {
		return ScrollDown
	}
	if box.Y2 <= 0 {
		return ScrollUp
	}
	return ScrollDown
}

// percent converts a normalized coordinate to the whole percent Maestro takes.
// TapOnPointV2Command parses with toInt and rejects anything outside 0..100.
func percent(v float64) int {
	p := int(math.Round(v * 100))
	if p < 0 {
		return 0
	}
	if p > 100 {
		return 100
	}
	return p
}
