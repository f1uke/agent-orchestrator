package simbridge

import (
	"errors"
	"strings"
	"testing"
)

// A screen with the shapes that make finding an element by name interesting: a
// control whose own label is repeated by a child, two labels where one contains
// the other, a field that carries its text as a value rather than a label, a
// disabled control, and a row below the fold.
func findFixture() Snapshot {
	screen := Size{Width: 440, Height: 956}
	return Snapshot{
		Screen: screen,
		Elements: []Element{{
			Path: "0", Type: "Application", Enabled: true,
			Tap: &Point{X: 0.5, Y: 0.5},
			Children: []Element{
				{
					Path: "0.0", Type: "TextField", ID: "email-field", Value: "hello@example.com",
					Enabled: true, Tap: &Point{X: 0.5, Y: 0.12},
				},
				{
					Path: "0.1", Type: "Button", Label: "Continue", Enabled: true,
					Tap: &Point{X: 0.5, Y: 0.86},
					// The same word again, one level down: one target, not two.
					Children: []Element{{
						Path: "0.1.0", Type: "StaticText", Label: "Continue", Enabled: true,
						Tap: &Point{X: 0.5, Y: 0.86},
					}},
				},
				{
					Path: "0.2", Type: "Button", Label: "Continue later", Enabled: true,
					Tap: &Point{X: 0.5, Y: 0.5},
				},
				{
					Path: "0.3", Type: "Button", Label: "Save draft", ID: "save-button",
					Enabled: false, Tap: &Point{X: 0.5, Y: 0.7},
				},
				{
					Path: "0.4", Type: "Button", Label: "See all", Enabled: true,
					OffScreen: true, Box: &Box{X1: 0.8, Y1: 1.01, X2: 0.96, Y2: 1.04},
				},
			},
		}},
	}
}

func TestSelect_ExactLabel(t *testing.T) {
	got, err := Select(findFixture(), Selector{Kind: SelectByLabel, Text: "Continue"})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if got.Element.Path != "0.1" {
		t.Fatalf("matched %s, want the button itself", got.Element.Path)
	}
	if got.How != MatchExact {
		t.Fatalf("how = %q, want an exact match", got.How)
	}
}

func TestSelect_ExactBeatsContains(t *testing.T) {
	// "Continue" is also inside "Continue later". Matching the longer one would
	// tap a different control than the caller named.
	got, err := Select(findFixture(), Selector{Kind: SelectByLabel, Text: "Continue"})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if got.Element.Label != "Continue" {
		t.Fatalf("matched %q, want the exact label", got.Element.Label)
	}
}

func TestSelect_IsCaseInsensitiveAndTrimsSpace(t *testing.T) {
	for _, text := range []string{"continue", "  Continue  ", "CONTINUE"} {
		got, err := Select(findFixture(), Selector{Kind: SelectByLabel, Text: text})
		if err != nil {
			t.Fatalf("Select(%q): %v", text, err)
		}
		if got.Element.Path != "0.1" {
			t.Fatalf("Select(%q) matched %s", text, got.Element.Path)
		}
	}
}

func TestSelect_FallsBackToContains(t *testing.T) {
	got, err := Select(findFixture(), Selector{Kind: SelectByLabel, Text: "later"})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if got.Element.Path != "0.2" {
		t.Fatalf("matched %s, want the only element containing the text", got.Element.Path)
	}
	// The caller has to be able to say which kind of match it acted on: a
	// contains-match is a guess the caller made, not the name they gave.
	if got.How != MatchContains {
		t.Fatalf("how = %q, want a contains match", got.How)
	}
}

func TestSelect_ANestedRepeatOfTheSameLabelIsOneTarget(t *testing.T) {
	// A button and the text inside it both carry the label. They are the same
	// thing on screen, and refusing as "ambiguous" would make the flag useless
	// on any real app.
	got, err := Select(findFixture(), Selector{Kind: SelectByLabel, Text: "Continue"})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if got.Element.Path != "0.1" {
		t.Fatalf("matched %s, want the outermost of the two", got.Element.Path)
	}
}

func TestSelect_TwoDifferentElementsAreAmbiguous(t *testing.T) {
	// Two separate controls with the same name is a real ambiguity: there is no
	// way to know which was meant, and tapping either could be wrong.
	snapshot := findFixture()
	snapshot.Elements[0].Children = append(snapshot.Elements[0].Children, Element{
		Path: "0.5", Type: "Button", Label: "Continue", Enabled: true,
		Tap: &Point{X: 0.5, Y: 0.2},
	})

	_, err := Select(snapshot, Selector{Kind: SelectByLabel, Text: "Continue"})
	var ambiguous *AmbiguousMatchError
	if !errors.As(err, &ambiguous) {
		t.Fatalf("err = %v, want an ambiguity rather than a guess", err)
	}
	if len(ambiguous.Matches) != 2 {
		t.Fatalf("matches = %d, want both candidates carried for the caller to list", len(ambiguous.Matches))
	}
}

func TestSelect_ByAccessibilityID(t *testing.T) {
	got, err := Select(findFixture(), Selector{Kind: SelectByID, Text: "email-field"})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if got.Element.Path != "0.0" {
		t.Fatalf("matched %s, want the field with that identifier", got.Element.Path)
	}
	// An id is not a label: the two namespaces stay apart.
	if _, err := Select(findFixture(), Selector{Kind: SelectByID, Text: "Continue"}); err == nil {
		t.Fatal("a label must not be matched as an identifier")
	}
}

func TestSelect_LabelFallsBackToTheValueWhenThereIsNoLabel(t *testing.T) {
	// This is what `ao sim ax` prints for such an element, so it is the name the
	// caller read off the screen.
	got, err := Select(findFixture(), Selector{Kind: SelectByLabel, Text: "hello@example.com"})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if got.Element.Path != "0.0" {
		t.Fatalf("matched %s, want the field whose value is that text", got.Element.Path)
	}
}

func TestSelect_NoMatchCarriesWhatIsTappable(t *testing.T) {
	_, err := Select(findFixture(), Selector{Kind: SelectByLabel, Text: "Ghost"})
	var missing *NoMatchError
	if !errors.As(err, &missing) {
		t.Fatalf("err = %v, want a no-match", err)
	}
	// The point of carrying them: the caller can fix the name in one round
	// instead of reading the tree again.
	var names []string
	for _, e := range missing.OnScreen {
		names = append(names, e.Label)
	}
	joined := strings.Join(names, ",")
	for _, want := range []string{"Continue", "Save draft"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("on-screen elements = %q, want %q listed", joined, want)
		}
	}
	if strings.Contains(joined, "See all") {
		t.Fatalf("an element that cannot be tapped must not be offered as an alternative: %q", joined)
	}
}

func TestSelect_FindsAnElementThatCannotBeTapped(t *testing.T) {
	// Found but unreachable is a different answer from not found, and the two
	// need different advice: scroll to it, versus you named the wrong thing.
	got, err := Select(findFixture(), Selector{Kind: SelectByLabel, Text: "See all"})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if !got.Element.OffScreen || got.Element.Tap != nil {
		t.Fatalf("element = %+v, want the off-screen one, reported as it is", got.Element)
	}
}

func TestSelect_EmptyTextIsRefused(t *testing.T) {
	if _, err := Select(findFixture(), Selector{Kind: SelectByLabel, Text: "  "}); err == nil {
		t.Fatal("an empty name matches everything; it must be refused")
	}
}
