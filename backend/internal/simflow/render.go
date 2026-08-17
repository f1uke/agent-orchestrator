package simflow

import (
	"fmt"
	"strings"
)

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
		if c.Ambiguity > 1 {
			fmt.Fprintf(&b, "# %d elements share this text - index picks one, verify it is the one you mean\n", c.Ambiguity)
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
			fmt.Fprintf(&b, "# %d elements share this text - index picks one, verify it is the one you mean\n", c.Ambiguity)
			b.WriteString("# which one was tapped could not be determined - verify this selector, or add your own index\n")
		}
		if c.Escaped {
			b.WriteString("# escaped: the label contains regex characters, and Maestro matches text as a regex\n")
		}
		fmt.Fprintf(&b, "- tapOn: %q\n", c.Text)
	case RungTextIndex:
		// Unlike RungText above, an index WAS picked - at record time, from the
		// same tree this Choice was resolved against - so the warning here is
		// "verify the index is still the one you mean", not "no index exists".
		fmt.Fprintf(&b, "# %d elements share this text - index picks one, verify it is the one you mean\n", c.Ambiguity)
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
		b.WriteString("# no label and no id: only a point works here, and a point breaks on any layout change\n")
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
