package simbridge

import (
	"errors"
	"fmt"
	"strings"
)

// Finding an element by the name it shows on screen.
//
// `ao sim ax` prints a tap point for every element and `ao sim tap` takes one,
// which is a loop with no coordinate arithmetic in it - but it still asks a
// caller to carry a number from one command to the other, and a number is easy
// to copy from the wrong line. Naming the thing is what the caller was reading
// anyway.
//
// The rules here exist because a real screen is not a list of unique names:
//
//   - the same word usually appears twice, on a control and on the text inside
//     it, and refusing that as ambiguous would make the whole flag useless;
//   - two genuinely different controls with one name is a real ambiguity, and
//     guessing between them taps something nobody asked for;
//   - "not found" and "found, but not on the screen" need opposite advice.

// SelectorKind is which name is being matched. They are separate namespaces on
// purpose: an app's accessibility identifiers are set for automation and are
// stable, while labels are what a person reads and change with the copy.
type SelectorKind string

// The two names an element can be picked by.
const (
	SelectByLabel SelectorKind = "label"
	SelectByID    SelectorKind = "id"
)

// Selector is what the caller asked for.
type Selector struct {
	Kind SelectorKind
	Text string
}

// String is the selector as a caller would say it back, for a message.
func (s Selector) String() string {
	return fmt.Sprintf("%s %q", s.Kind, strings.TrimSpace(s.Text))
}

// MatchKind is how the name matched, which the caller must be able to report:
// a contains-match is an inference, not the name that was given.
type MatchKind string

// The two ways a name can match.
const (
	MatchExact    MatchKind = "exact"
	MatchContains MatchKind = "contains"
)

// Match is the one element a selector resolved to.
type Match struct {
	Element Element
	How     MatchKind
}

// ErrEmptySelector is a name with nothing in it, which matches everything.
var ErrEmptySelector = errors.New("simbridge: empty selector")

// AmbiguousMatchError is several different elements answering to one name. It
// carries them so the caller can list them and let a person choose, the same
// way an ambiguous device listing does.
type AmbiguousMatchError struct {
	Selector Selector
	Matches  []Element
}

func (e *AmbiguousMatchError) Error() string {
	return fmt.Sprintf("%d elements match %s", len(e.Matches), e.Selector)
}

// NoMatchError is a name nothing on this screen answers to. It carries what
// CAN be tapped, because the answer is almost always a name that is close to
// one of those.
type NoMatchError struct {
	Selector Selector
	OnScreen []Element
}

func (e *NoMatchError) Error() string {
	return fmt.Sprintf("nothing on screen matches %s", e.Selector)
}

// Select resolves a selector against one snapshot.
func Select(snapshot Snapshot, selector Selector) (Match, error) {
	want := strings.TrimSpace(selector.Text)
	if want == "" {
		return Match{}, ErrEmptySelector
	}
	folded := strings.ToLower(want)

	var exact, contains []Element
	walk(snapshot.Elements, func(e Element) {
		name := strings.ToLower(strings.TrimSpace(selectorName(e, selector.Kind)))
		if name == "" {
			return
		}
		switch {
		case name == folded:
			exact = append(exact, e)
		case strings.Contains(name, folded):
			contains = append(contains, e)
		}
	})

	// Exact first, always: "Continue" must not resolve to "Continue later"
	// while a control called exactly "Continue" is on the same screen.
	if match, found, err := resolve(exact, selector, MatchExact); found || err != nil {
		return match, err
	}
	if match, found, err := resolve(contains, selector, MatchContains); found || err != nil {
		return match, err
	}
	return Match{}, &NoMatchError{Selector: selector, OnScreen: tappable(snapshot)}
}

// resolve reduces the candidates of one round to a single target. The middle
// return says whether this round answered at all: when it did not, the caller
// tries the next round rather than reporting a miss.
func resolve(found []Element, selector Selector, how MatchKind) (Match, bool, error) {
	found = outermost(found)
	switch len(found) {
	case 0:
		return Match{}, false, nil
	case 1:
		return Match{Element: found[0], How: how}, true, nil
	default:
		return Match{}, true, &AmbiguousMatchError{Selector: selector, Matches: found}
	}
}

// outermost drops a candidate that sits inside another candidate.
//
// A button and the text drawn inside it both carry the label, and they are one
// thing on the screen: the button is what a person means, and its tap point is
// the same place anyway. Reporting those two as an ambiguity would fire on
// nearly every real screen.
func outermost(found []Element) []Element {
	out := make([]Element, 0, len(found))
	for _, candidate := range found {
		nested := false
		for _, other := range found {
			if other.Path != candidate.Path && strings.HasPrefix(candidate.Path, other.Path+".") {
				nested = true
				break
			}
		}
		if !nested {
			out = append(out, candidate)
		}
	}
	return out
}

// selectorName is the text this selector matches against. For a label it is
// exactly what `ao sim ax` prints as the element's name - its label, or its
// value when it has no label - so the caller matches what they read.
func selectorName(e Element, kind SelectorKind) string {
	if kind == SelectByID {
		return e.ID
	}
	if e.Label != "" {
		return e.Label
	}
	return e.Value
}

// tappable is every element that has somewhere to touch, for a caller that has
// to say what IS on the screen. An element that cannot be reached is not an
// alternative and is left out.
func tappable(snapshot Snapshot) []Element {
	out := []Element{}
	walk(snapshot.Elements, func(e Element) {
		if e.Tap == nil {
			return
		}
		if selectorName(e, SelectByLabel) == "" && e.ID == "" {
			return
		}
		out = append(out, e)
	})
	return out
}

func walk(elements []Element, visit func(Element)) {
	for _, e := range elements {
		visit(e)
		walk(e.Children, visit)
	}
}
