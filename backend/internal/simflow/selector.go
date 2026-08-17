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
)

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
		if c.Ambiguity <= 1 {
			c.Rung = RungText
		} else {
			c.Rung = RungTextIndex
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
