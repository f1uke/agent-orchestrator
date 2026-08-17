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
	// RungNone: nothing on this element can address it.
	RungNone Rung = iota
	// RungText: the label is unique. The house style of a Maestro suite.
	RungText
	// RungTextIndex: the label repeats, so an index picks one of them.
	RungTextIndex
	// RungID: no label, but the app set an accessibility identifier.
	RungID
	// RungPoint: neither - a screen coordinate, which is brittle.
	RungPoint
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
}

// metacharacter is the set that makes a label behave as a pattern rather than a
// literal. Maestro compiles the string as a regex and also compares it
// literally, so an unescaped label still matches itself - but an unescaped
// "Continue." would ALSO match "Continue!", and tapping the wrong element is
// worse than an ugly string.
var metacharacter = regexp.MustCompile(`[(){}\[\].+*?^$|\\]`)

// For picks the best selector for el, using snap only to count collisions.
func For(snap simbridge.Snapshot, el simbridge.Element) Choice {
	c := Choice{Rung: RungNone, OffScreen: el.OffScreen}

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
