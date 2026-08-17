package simflow_test

import (
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/simbridge"
	"github.com/aoagents/agent-orchestrator/backend/internal/simflow"
)

// tree builds a snapshot from flat elements, which is all the ladder needs:
// For counts matches across the whole tree and never walks parents.
func tree(els ...simbridge.Element) simbridge.Snapshot {
	return simbridge.Snapshot{
		Screen:   simbridge.Size{Width: 440, Height: 956},
		Elements: []simbridge.Element{{Path: "0", Type: "Application", Children: els}},
	}
}

func at(x, y float64) *simbridge.Point { return &simbridge.Point{X: x, Y: y} }

func TestFor_UniqueLabelIsRungText(t *testing.T) {
	el := simbridge.Element{Path: "0.0", Label: "Accessibility", Tap: at(0.5, 0.5)}
	got := simflow.For(tree(el), el)
	if got.Rung != simflow.RungText {
		t.Fatalf("rung = %v, want RungText", got.Rung)
	}
	if got.Text != "Accessibility" || got.Escaped {
		t.Errorf("text = %q escaped=%v, want %q unescaped", got.Text, got.Escaped, "Accessibility")
	}
	if got.Ambiguity != 1 {
		t.Errorf("ambiguity = %d, want 1", got.Ambiguity)
	}
}

func TestFor_RepeatedLabelIsRungTextIndex(t *testing.T) {
	a := simbridge.Element{Path: "0.0", Label: "Continue", Tap: at(0.5, 0.2)}
	b := simbridge.Element{Path: "0.1", Label: "Continue", Tap: at(0.5, 0.6)}
	snap := tree(a, b)

	first := simflow.For(snap, a)
	if first.Rung != simflow.RungTextIndex || first.Index != 0 || first.Ambiguity != 2 {
		t.Fatalf("first = %+v, want RungTextIndex index 0 ambiguity 2", first)
	}
	second := simflow.For(snap, b)
	if second.Index != 1 {
		t.Errorf("second index = %d, want 1", second.Index)
	}
}

// Maestro's text matcher is the union of text, hintText and accessibilityText,
// and its `text` attribute is title-or-value. We only hold Label and Value, so
// a Value equal to another element's Label is a collision we must count.
func TestFor_ValueCollidesWithLabel(t *testing.T) {
	a := simbridge.Element{Path: "0.0", Label: "Done", Tap: at(0.5, 0.2)}
	b := simbridge.Element{Path: "0.1", Value: "Done", Tap: at(0.5, 0.6)}
	got := simflow.For(tree(a, b), a)
	if got.Ambiguity != 2 || got.Rung != simflow.RungTextIndex {
		t.Fatalf("got %+v, want ambiguity 2 and RungTextIndex", got)
	}
}

func TestFor_MetacharacterLabelIsEscaped(t *testing.T) {
	el := simbridge.Element{Path: "0.0", Label: "See all (12)", Tap: at(0.5, 0.5)}
	got := simflow.For(tree(el), el)
	if !got.Escaped {
		t.Fatal("escaped = false, want true")
	}
	if got.Text != `See all \(12\)` {
		t.Errorf("text = %q, want %q", got.Text, `See all \(12\)`)
	}
}

func TestFor_NoLabelFallsBackToID(t *testing.T) {
	el := simbridge.Element{Path: "0.0", ID: "search-field", Tap: at(0.5, 0.5)}
	got := simflow.For(tree(el), el)
	if got.Rung != simflow.RungID || got.ID != "search-field" {
		t.Fatalf("got %+v, want RungID search-field", got)
	}
}

func TestFor_NoLabelNoIDFallsBackToPoint(t *testing.T) {
	el := simbridge.Element{Path: "0.0", Tap: at(0.5, 0.8629707112970712)}
	got := simflow.For(tree(el), el)
	if got.Rung != simflow.RungPoint {
		t.Fatalf("rung = %v, want RungPoint", got.Rung)
	}
	// Maestro parses percentages with toInt and rejects outside 0..100.
	if got.PercentX != 50 || got.PercentY != 86 {
		t.Errorf("percent = %d,%d want 50,86", got.PercentX, got.PercentY)
	}
}

// The coordinate must be rounded to the nearest percent, not truncated: 0.865
// is 86.5%, which truncation and rounding disagree on (86 vs 87). Every other
// case in this file happens to land below the .5 boundary, where the two
// agree - this is the one that would not catch a truncating regression.
func TestFor_PointRoundsHalfwayToNearestPercent(t *testing.T) {
	el := simbridge.Element{Path: "0.0", Tap: at(0.865, 0.995)}
	got := simflow.For(tree(el), el)
	if got.Rung != simflow.RungPoint {
		t.Fatalf("rung = %v, want RungPoint", got.Rung)
	}
	if got.PercentX != 87 || got.PercentY != 100 {
		t.Errorf("percent = %d,%d want 87,100 (rounded, not truncated)", got.PercentX, got.PercentY)
	}
}

// An off-screen element has no Tap by construction, so the coordinate rung is
// unreachable for it: with no label and no id there is nothing to address.
func TestFor_OffScreenWithoutLabelOrIDIsRungNone(t *testing.T) {
	el := simbridge.Element{Path: "0.0", OffScreen: true}
	got := simflow.For(tree(el), el)
	if got.Rung != simflow.RungNone {
		t.Fatalf("rung = %v, want RungNone", got.Rung)
	}
	if !got.OffScreen {
		t.Error("OffScreen = false, want true")
	}
}

func TestFor_OffScreenWithLabelStillReportsTheLabel(t *testing.T) {
	el := simbridge.Element{Path: "0.0", Label: "See all", OffScreen: true}
	got := simflow.For(tree(el), el)
	if got.Rung != simflow.RungText || !got.OffScreen {
		t.Fatalf("got %+v, want RungText with OffScreen", got)
	}
}

// simbridge sets OffScreen whenever an element's centre is off the viewport in
// any direction, not only below it. Scrolling DOWN for something entirely
// above the top edge moves away from the element, not toward it.
func TestFor_ScrollDirectionFollowsTheBox(t *testing.T) {
	cases := []struct {
		name string
		box  *simbridge.Box
		want simflow.ScrollDirection
	}{
		{"entirely above the viewport", &simbridge.Box{Y1: -0.6, Y2: -0.1}, simflow.ScrollUp},
		{"entirely below the viewport", &simbridge.Box{Y1: 1.1, Y2: 1.5}, simflow.ScrollDown},
		{"no box at all", nil, simflow.ScrollDown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			el := simbridge.Element{Path: "0.0", Label: "See all", OffScreen: true, Box: tc.box}
			got := simflow.For(tree(el), el)
			if got.ScrollDirection != tc.want {
				t.Errorf("ScrollDirection = %v, want %v", got.ScrollDirection, tc.want)
			}
		})
	}
}

// A by-name tap that matched several elements gets no Index - which candidate
// was hit is genuinely unknown - but it must still be escaped exactly like a
// unique match: Maestro matches text as a regex, and this is the one path that
// already could not tell its candidates apart, so relaxing the escaping here
// widens a selector that is ambiguous to begin with.
func TestForAmbiguousText_EscapesLikeForDoes(t *testing.T) {
	got := simflow.ForAmbiguousText("  Continue.  ", 3)
	if got.Rung != simflow.RungText {
		t.Fatalf("Rung = %v, want RungText", got.Rung)
	}
	if got.Text != `Continue\.` {
		t.Fatalf("Text = %q, want the escaped label - an unescaped %q also matches %q", got.Text, "Continue.", "Continue!")
	}
	if !got.Escaped {
		t.Fatal("Escaped must be set, or the flow carries backslashes with no explanation of why")
	}
	if got.Ambiguity != 3 {
		t.Fatalf("Ambiguity = %d, want 3", got.Ambiguity)
	}
	if got.Index != 0 {
		t.Fatalf("Index = %d, want 0 - no index may be invented for a match nobody could tell apart", got.Index)
	}
}

func TestForAmbiguousText_LeavesAPlainLabelAlone(t *testing.T) {
	got := simflow.ForAmbiguousText("Continue", 2)
	if got.Text != "Continue" || got.Escaped {
		t.Fatalf("got %+v, want the label untouched and Escaped false", got)
	}
}

// Unescape is the inverse of the escaping every stored text selector went
// through, and is what recovers the label a human reads (scrollUntilVisible
// searches for that, not for a pattern) plus the fact that it was escaped.
func TestUnescape_InvertsTheEscaping(t *testing.T) {
	cases := []struct {
		name       string
		label      string
		wantEscape bool
	}{
		{"no metacharacters", "See all", false},
		{"parens", "See all (12)", true},
		{"a full stop", "Continue.", true},
		{"a backslash of its own", `C:\Users`, true},
		{"every metacharacter", `(){}[].+*?^$|\`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			el := simbridge.Element{Path: "0.0", Label: tc.label}
			stored := simflow.For(tree(el), el)
			if stored.Escaped != tc.wantEscape {
				t.Fatalf("For escaped = %v, want %v for %q", stored.Escaped, tc.wantEscape, tc.label)
			}
			plain, escaped := simflow.Unescape(stored.Text)
			if plain != tc.label {
				t.Fatalf("Unescape(%q) = %q, want the original label %q", stored.Text, plain, tc.label)
			}
			if escaped != tc.wantEscape {
				t.Fatalf("Unescape reported escaped = %v, want %v", escaped, tc.wantEscape)
			}
		})
	}
}

// A backslash that escape could not have produced is left exactly as it is
// rather than guessed at.
func TestUnescape_LeavesAnUnknownEscapeAlone(t *testing.T) {
	if plain, escaped := simflow.Unescape(`a\nb`); plain != `a\nb` || escaped {
		t.Fatalf("Unescape(%q) = %q escaped=%v, want it untouched", `a\nb`, plain, escaped)
	}
}
