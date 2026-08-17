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
