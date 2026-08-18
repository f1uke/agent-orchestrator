package simflow_test

import (
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/simflow"
)

func TestRender_UniqueTextIsTheBareStringHouseStyle(t *testing.T) {
	got := simflow.Render(simflow.Choice{Rung: simflow.RungText, Text: "Accessibility", Ambiguity: 1}, "Accessibility")
	want := "- tapOn: \"Accessibility\"\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRender_RepeatedTextEmitsIndexAndSaysWhy(t *testing.T) {
	got := simflow.Render(simflow.Choice{
		Rung: simflow.RungTextIndex, Text: "Continue", Index: 1, Ambiguity: 3,
	}, "Continue")
	if !strings.Contains(got, "3 elements share this text") {
		t.Errorf("missing the ambiguity comment: %q", got)
	}
	// The index rung is the one measured landing on a different element 14% of
	// the time without failing, so it must not read like a rung that resolved.
	if !strings.Contains(got, "# REVIEW:") {
		t.Errorf("an indexed step is a guess and must be marked for review: %q", got)
	}
	for _, want := range []string{"- tapOn:", "    text: \"Continue\"", "    index: 1"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestRender_EscapedTextSaysItWasEscaped(t *testing.T) {
	got := simflow.Render(simflow.Choice{
		Rung: simflow.RungText, Text: `See all \(12\)`, Escaped: true, Ambiguity: 1,
	}, "See all (12)")
	if !strings.Contains(got, "# escaped: the label contains regex characters") {
		t.Errorf("missing the escaping comment: %q", got)
	}
	if !strings.Contains(got, `- tapOn: "See all \\(12\\)"`) {
		t.Errorf("missing the escaped selector: %q", got)
	}
}

func TestRender_IDRungSaysWhyItIsNotText(t *testing.T) {
	got := simflow.Render(simflow.Choice{Rung: simflow.RungID, ID: "search-field"}, "")
	if !strings.Contains(got, "# no label; matched on the accessibility id") {
		t.Errorf("missing the id comment: %q", got)
	}
	if !strings.Contains(got, "    id: \"search-field\"") {
		t.Errorf("missing the id selector: %q", got)
	}
}

func TestRender_PointRungIsMarkedBrittle(t *testing.T) {
	got := simflow.Render(simflow.Choice{Rung: simflow.RungPoint, PercentX: 50, PercentY: 86}, "")
	if !strings.Contains(got, "breaks on any layout change") {
		t.Errorf("missing the brittleness warning: %q", got)
	}
	if !strings.Contains(got, `    point: "50%,86%"`) {
		t.Errorf("missing the point selector: %q", got)
	}
}

func TestRender_OffScreenEmitsScrollNotTap(t *testing.T) {
	got := simflow.Render(simflow.Choice{
		Rung: simflow.RungText, Text: "See all", Ambiguity: 1, OffScreen: true, ScrollDirection: simflow.ScrollDown,
	}, "See all")
	if strings.Contains(got, "tapOn") {
		t.Errorf("off-screen element must not be tapped directly: %q", got)
	}
	for _, want := range []string{"# off screen - scroll to it first", "- scrollUntilVisible:", "    element: \"See all\"", "    direction: DOWN"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

// Render emits whatever ScrollDirection holds - the decision of which way to
// scroll belongs to For, not to Render.
func TestRender_OffScreenEmitsWhicheverDirectionTheChoiceHolds(t *testing.T) {
	got := simflow.Render(simflow.Choice{
		Rung: simflow.RungText, Text: "Header", Ambiguity: 1, OffScreen: true, ScrollDirection: simflow.ScrollUp,
	}, "Header")
	if !strings.Contains(got, "    direction: UP\n") {
		t.Errorf("want direction: UP for an element above the fold, got:\n%s", got)
	}
	if strings.Contains(got, "direction: DOWN") {
		t.Errorf("must not also say DOWN: %q", got)
	}
}

// Off screen is exactly the case where the caller cannot look at the screen to
// check a match, so the ambiguity warning matters more here, not less - it
// must not be dropped the way it used to be.
func TestRender_OffScreenWithAmbiguityStillWarns(t *testing.T) {
	got := simflow.Render(simflow.Choice{
		Rung: simflow.RungTextIndex, Text: "Continue", Index: 1, Ambiguity: 3, OffScreen: true, ScrollDirection: simflow.ScrollDown,
	}, "Continue")
	if !strings.Contains(got, "3 elements share this text") {
		t.Errorf("missing the ambiguity comment on the off-screen path: %q", got)
	}
	if !strings.Contains(got, "# REVIEW:") {
		t.Errorf("an ambiguous off-screen step is still a guess: %q", got)
	}
	if !strings.Contains(got, "- scrollUntilVisible:") {
		t.Errorf("missing the scroll stanza: %q", got)
	}
}

// scrollTarget must trim the label the same way For does when matching, or a
// label like "  Save  " ends up tapped as "Save" but scrolled to as
// "  Save  ".
func TestRender_ScrollTargetIsTrimmed(t *testing.T) {
	got := simflow.Render(simflow.Choice{
		Rung: simflow.RungText, Text: "Save", Ambiguity: 1, OffScreen: true, ScrollDirection: simflow.ScrollDown,
	}, "  Save  ")
	if !strings.Contains(got, `    element: "Save"`) {
		t.Errorf("want the trimmed label as the scroll target, got: %q", got)
	}
}

func TestRender_NoneRungSaysItCannotBeAddressed(t *testing.T) {
	got := simflow.Render(simflow.Choice{Rung: simflow.RungNone, OffScreen: true}, "")
	if !strings.Contains(got, "# no label, no id and no reachable point") {
		t.Errorf("missing the unaddressable comment: %q", got)
	}
	if strings.Contains(got, "- tapOn") || strings.Contains(got, "- scrollUntilVisible") {
		t.Errorf("must emit no command at all: %q", got)
	}
}

func TestRender_AlwaysEndsWithExactlyOneNewline(t *testing.T) {
	for name, c := range map[string]simflow.Choice{
		"text":  {Rung: simflow.RungText, Text: "A", Ambiguity: 1},
		"index": {Rung: simflow.RungTextIndex, Text: "A", Ambiguity: 2},
		"id":    {Rung: simflow.RungID, ID: "a"},
		"point": {Rung: simflow.RungPoint},
		"none":  {Rung: simflow.RungNone},
	} {
		got := simflow.Render(c, "A")
		if !strings.HasSuffix(got, "\n") || strings.HasSuffix(got, "\n\n") {
			t.Errorf("%s: bad trailing newline in %q", name, got)
		}
	}
}

// A YAML double-quoted scalar processes backslash escapes: `\(` is not a valid
// one and makes the whole document fail to parse. Verified against the real
// `maestro check-syntax`, which rejects the single-backslash form and accepts
// this one. %q is what keeps the two in step.
func TestRender_EscapedTextIsValidYAMLNotJustEscaped(t *testing.T) {
	got := simflow.Render(simflow.Choice{
		Rung: simflow.RungText, Text: `a\(b`, Escaped: true, Ambiguity: 1,
	}, "a(b")
	if strings.Contains(got, `"a\(b"`) {
		t.Fatalf("emitted a single-backslash escape, which YAML rejects: %q", got)
	}
	if !strings.Contains(got, `"a\\(b"`) {
		t.Errorf("want the backslash doubled for YAML, got %q", got)
	}
}

// The anchor rung must emit Maestro's nested relative selector, and must NOT
// emit an index - carrying one would defeat the entire point of the rung.
func TestRender_AnchorEmitsTheRelativeSelectorAndNoIndex(t *testing.T) {
	got := simflow.Render(simflow.Choice{
		Rung: simflow.RungTextAnchor, Text: "Buy", Ambiguity: 3,
		Anchor: "Second Section", Relation: simflow.RelBelow,
	}, "Buy")

	for _, want := range []string{"- tapOn:", "    text: \"Buy\"", "    below:", "      text: \"Second Section\""} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "index:") {
		t.Errorf("an anchored selector must carry no index:\n%s", got)
	}
	if strings.Contains(got, "# REVIEW:") {
		t.Errorf("an anchored step was narrowed, not guessed:\n%s", got)
	}
	if !strings.Contains(got, "3 elements share this text") {
		t.Errorf("the ambiguity count is still worth stating:\n%s", got)
	}
}

// Whichever of the two matchers needed escaping, the reader has to be told:
// both are compiled as regexes by Maestro.
func TestRender_AnchorEscapingIsAnnouncedEvenWhenOnlyTheAnchorNeededIt(t *testing.T) {
	got := simflow.Render(simflow.Choice{
		Rung: simflow.RungTextAnchor, Text: "Buy", Ambiguity: 2,
		Anchor: `Total \(THB\)`, AnchorEscaped: true, Relation: simflow.RelAbove,
	}, "Buy")

	if !strings.Contains(got, "# escaped:") {
		t.Errorf("missing the escaping comment: %q", got)
	}
	if !strings.Contains(got, `      text: "Total \\(THB\\)"`) {
		t.Errorf("missing the escaped anchor: %q", got)
	}
}

// A point is brittle and an unaddressable element is worse; both are guesses
// and must carry the same marker an index does, or a reader learns the marker
// only means "index".
func TestRender_PointAndUnaddressableAreGuessesToo(t *testing.T) {
	for _, c := range []simflow.Choice{
		{Rung: simflow.RungPoint, PercentX: 50, PercentY: 80},
		{Rung: simflow.RungNone},
	} {
		if !c.NeedsReview() {
			t.Errorf("%v should need review", c.Rung)
		}
	}
	if (simflow.Choice{Rung: simflow.RungID, ID: "x"}).NeedsReview() {
		t.Error("an accessibility id is the most stable thing on the screen and is not a guess")
	}
}
