package simflow

import (
	"fmt"
	"strings"
)

// reviewMarker prefixes the comment on any step the generator could not
// resolve with confidence.
//
// It is one fixed string rather than prose per rung so that a human, a grep
// and Emit's header all recognise the same thing. The point is the one the
// real-app measurement made: a step that was guessed and a step that is
// certain must not look alike in the emitted YAML, because a suite that
// passes while tapping the wrong element teaches people to trust it.
const reviewMarker = "# REVIEW:"

// NeedsReview reports whether this Choice was a guess rather than a resolved
// selector - the same condition that makes Render write a reviewMarker.
//
// It exists so Emit's header and Render's per-step comment cannot disagree
// about which steps are guesses; there is one rule and both read it.
func (c Choice) NeedsReview() bool {
	switch c.Rung {
	case RungTextIndex, RungPoint, RungNone:
		return true
	case RungText:
		// A by-name tap that matched several candidates is stored as RungText
		// with no Index - see ForAmbiguousText. It is a guess too.
		return c.Ambiguity > 1
	default:
		return false
	}
}

// Render writes the Maestro YAML for acting on one element, with the comment
// that says how far to trust it.
//
// The comments are not decoration. A selector that turns out to match the wrong
// node is the failure mode of this whole idea, and the difference between a
// diagnosable failure and a mysterious one is whether the flow records what we
// believed when we wrote it.
//
// plain is the unescaped label; it is what a human reads in a comment and what
// scrollUntilVisible matches on.
func Render(c Choice, plain string) string {
	var b strings.Builder

	if c.OffScreen {
		if c.Rung == RungNone {
			b.WriteString("# no label, no id and no reachable point - this element cannot be addressed\n")
			return b.String()
		}
		// Off screen is exactly the case where the caller cannot look at the
		// screen to sanity-check a match, so the ambiguity warning matters more
		// here than anywhere else, not less.
		if c.NeedsReview() && c.Ambiguity > 1 {
			fmt.Fprintf(&b, "%s %d elements share this text - scrolling finds whichever comes first. Check it.\n", reviewMarker, c.Ambiguity)
		}
		// An off-screen element has no point to touch, so the only honest
		// command is the one that brings it on screen first.
		b.WriteString("# off screen - scroll to it first\n")
		b.WriteString("- scrollUntilVisible:\n")
		fmt.Fprintf(&b, "    element: %q\n", scrollTarget(c, plain))
		fmt.Fprintf(&b, "    direction: %s\n", c.ScrollDirection)
		return b.String()
	}

	switch c.Rung {
	case RungText:
		// RungText otherwise means "this text is unique" - but a by-name tap
		// recorded against several candidates (see selectorChoice in
		// internal/service/sim/recording.go) is stored this way too, on
		// purpose, with no Index: there was no way to tell which candidate was
		// actually tapped, and guessing one would be worse than not knowing.
		// The warning only fires when that is true (Ambiguity > 1); a genuinely
		// unique label still gets none, so this stays a comment worth reading
		// rather than one every step carries.
		if c.Ambiguity > 1 {
			fmt.Fprintf(&b, "%s %d elements share this text, and which one was tapped could not be\n", reviewMarker, c.Ambiguity)
			b.WriteString("#   determined - this selector takes whichever Maestro finds first. Check it.\n")
		}
		if c.Escaped {
			b.WriteString("# escaped: the label contains regex characters, and Maestro matches text as a regex\n")
		}
		fmt.Fprintf(&b, "- tapOn: %q\n", c.Text)
	case RungTextAnchor:
		// Narrowed, not guessed. The anchor is resolved by Maestro inside its
		// own hierarchy, so unlike an index it does not depend on our tree and
		// Maestro's counting the same elements. Say which anchor and why, so a
		// reader editing the flow knows what the step is leaning on.
		fmt.Fprintf(&b, "# %d elements share this text - pinned by the unique label %q rather than an index\n", c.Ambiguity, c.Anchor)
		if c.Escaped || c.AnchorEscaped {
			b.WriteString("# escaped: a label contains regex characters, and Maestro matches text as a regex\n")
		}
		b.WriteString("- tapOn:\n")
		fmt.Fprintf(&b, "    text: %q\n", c.Text)
		fmt.Fprintf(&b, "    %s:\n", c.Relation)
		fmt.Fprintf(&b, "      text: %q\n", c.Anchor)
	case RungTextIndex:
		// Unlike RungText above, an index WAS picked - at record time, from the
		// same tree this Choice was resolved against - so the warning here is
		// "verify the index is still the one you mean", not "no index exists".
		//
		// This is the rung the real-app measurement caught landing on a
		// DIFFERENT element 14% of the time, silently, because the index is
		// counted in our tree and replayed against Maestro's. Nothing here can
		// fix that (anchorFor already tried and found nothing unique to lean
		// on), so the only honest thing left is to refuse to look like the
		// rungs that ARE trustworthy - hence the marker, which Emit also
		// collects into the flow's header.
		fmt.Fprintf(&b, "%s %d elements share this text and no unique nearby label pins this one down.\n", reviewMarker, c.Ambiguity)
		b.WriteString("#   The index below is counted in the accessibility tree we recorded from, and\n")
		b.WriteString("#   Maestro counts its own - measured on a real app, that lands on a different\n")
		b.WriteString("#   element 14% of the time, WITHOUT failing. Check this step before trusting it.\n")
		if c.Escaped {
			b.WriteString("# escaped: the label contains regex characters, and Maestro matches text as a regex\n")
		}
		b.WriteString("- tapOn:\n")
		fmt.Fprintf(&b, "    text: %q\n", c.Text)
		fmt.Fprintf(&b, "    index: %d\n", c.Index)
	case RungID:
		b.WriteString("# no label; matched on the accessibility id\n")
		b.WriteString("- tapOn:\n")
		fmt.Fprintf(&b, "    id: %q\n", c.ID)
	case RungPoint:
		// ⚠ The marker, not a bare comment. NeedsReview counts this rung, so the
		// banner at the top of the flow already tells a reader that a step below
		// is marked - and until this line carried the marker, there was nothing
		// there to find. A header that promises markers it did not write is the
		// same untruth as a header that claims a flow is clean.
		fmt.Fprintf(&b, "%s no label and no id, so this replays as a coordinate: it is where the finger went,\n", reviewMarker)
		b.WriteString("#   not what was touched, and it breaks on any layout change. Check it.\n")
		b.WriteString("- tapOn:\n")
		fmt.Fprintf(&b, "    point: \"%d%%,%d%%\"\n", c.PercentX, c.PercentY)
	default:
		b.WriteString("# no label, no id and no reachable point - this element cannot be addressed\n")
	}
	return b.String()
}

// scrollTarget is what scrollUntilVisible matches on. It prefers the plain
// label: scrolling to an element is a search, and an escaped pattern reads as
// noise in a flow a human will edit.
func scrollTarget(c Choice, plain string) string {
	if trimmed := strings.TrimSpace(plain); trimmed != "" {
		return trimmed
	}
	return c.ID
}
