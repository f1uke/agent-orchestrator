package simflow_test

import (
	"strings"
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

// row is an element with a real frame, which the anchor rung needs: a relative
// selector is decided from edges, and the existing helpers above deliberately
// carry only a tap point. The flat shape mirrors what a device actually
// produces - the measurement found every repeating element sitting directly
// under the application root, because the addon walks the tree AND hit-tests a
// grid.
func row(path, label string, x, y float64) simbridge.Element {
	return simbridge.Element{
		Path:  path,
		Label: label,
		Frame: simbridge.Rect{X: x, Y: y, Width: 100, Height: 20},
		Tap:   at((x+50)/440, (y+10)/956),
	}
}

// The whole point of the anchor rung: a repeated label must stop resolving to
// an index, because an index counted in our tree is replayed against Maestro's
// and was measured landing on a different element 14% of the time.
func TestFor_RepeatedLabelIsPinnedByAUniqueAnchorInsteadOfAnIndex(t *testing.T) {
	first := row("0.0", "Buy", 0, 100)
	heading := row("0.1", "Second Section", 0, 200)
	second := row("0.2", "Buy", 0, 300)
	snap := tree(first, heading, second)

	got := simflow.For(snap, second)

	if got.Rung != simflow.RungTextAnchor {
		t.Fatalf("rung = %v, want RungTextAnchor (an index is the failure this rung exists to avoid)", got.Rung)
	}
	if got.Anchor != "Second Section" {
		t.Errorf("anchor = %q, want the unique nearby label", got.Anchor)
	}
	if got.Relation != simflow.RelBelow {
		t.Errorf("relation = %q, want below", got.Relation)
	}
	if got.Ambiguity != 2 {
		t.Errorf("ambiguity = %d, want 2 - the count is still reported", got.Ambiguity)
	}
	if got.NeedsReview() {
		t.Error("an anchored step was narrowed, not guessed, and must not be flagged")
	}
}

// An anchor is only honest when exactly ONE candidate satisfies the relation.
// Maestro keeps every element that satisfies the predicate and returns the
// nearest, so accepting an anchor that leaves two candidates would re-create
// the cross-tree ordering assumption the index already got wrong.
func TestFor_AnchorIsRefusedWhenItLeavesMoreThanOneCandidate(t *testing.T) {
	// Both rows sit below, right of and level with the only unique label, so
	// no relation separates them.
	header := row("0.0", "Header", 0, 0)
	a := row("0.1", "Buy", 0, 100)
	b := row("0.2", "Buy", 0, 200)
	snap := tree(header, a, b)

	got := simflow.For(snap, a)

	if got.Rung != simflow.RungTextIndex {
		t.Fatalf("rung = %v, want RungTextIndex - no anchor separates the two", got.Rung)
	}
	if !got.NeedsReview() {
		t.Error("a step that fell through to an index is a guess and must say so")
	}
}

// The anchor must be unique itself. A label that repeats cannot pin anything
// down, and leaning on one would move the ambiguity one step sideways.
func TestFor_RepeatedLabelIsNeverUsedAsAnAnchor(t *testing.T) {
	snap := tree(
		row("0.0", "Buy", 0, 100),
		row("0.1", "Row", 0, 150),
		row("0.2", "Buy", 0, 300),
		row("0.3", "Row", 0, 350),
	)
	target := snap.Elements[0].Children[2]

	got := simflow.For(snap, target)

	if got.Rung == simflow.RungTextAnchor && got.Anchor == "Row" {
		t.Fatalf("anchored on a label that itself repeats: %+v", got)
	}
}

// Maestro's hierarchy only reports what XCUITest can see. The measurement
// found 0 of 24 off-screen labels present in Maestro's tree, so anchoring to
// one names something that will not be there at replay.
func TestFor_OffScreenElementIsNeverUsedAsAnAnchor(t *testing.T) {
	hidden := row("0.1", "Only Unique Label", 0, 900)
	hidden.OffScreen = true
	hidden.Tap = nil
	target := row("0.0", "Buy", 0, 100)
	snap := tree(target, hidden, row("0.2", "Buy", 0, 300))

	got := simflow.For(snap, target)

	if got.Rung == simflow.RungTextAnchor {
		t.Fatalf("anchored on an off-screen label Maestro will not report: %+v", got)
	}
}

// A unique label must stay the bare house-style selector. The anchor rung is
// for repeats only; widening it would put a relative selector on steps that
// never needed one.
func TestFor_UniqueLabelIsUntouchedByAnchoring(t *testing.T) {
	target := row("0.0", "Settings", 0, 100)
	snap := tree(target, row("0.1", "Profile", 0, 200))

	got := simflow.For(snap, target)

	if got.Rung != simflow.RungText {
		t.Fatalf("rung = %v, want RungText", got.Rung)
	}
	if got.Anchor != "" || got.Relation != "" {
		t.Errorf("a unique label must carry no anchor, got %+v", got)
	}
}

// The anchor goes through the same escaping as the target text: Maestro
// compiles both as regexes, so an unescaped "Total (THB)" anchor would
// over-match exactly the way an unescaped target would.
func TestFor_AnchorIsEscapedLikeAnyOtherMatcher(t *testing.T) {
	target := row("0.2", "Buy", 0, 300)
	snap := tree(row("0.0", "Buy", 0, 100), row("0.1", "Total (THB)", 0, 200), target)

	got := simflow.For(snap, target)

	if got.Rung != simflow.RungTextAnchor {
		t.Fatalf("rung = %v, want RungTextAnchor", got.Rung)
	}
	if !got.AnchorEscaped {
		t.Error("an anchor containing regex metacharacters must be escaped")
	}
	if plain, _ := simflow.Unescape(got.Anchor); plain != "Total (THB)" {
		t.Errorf("escaped anchor does not round-trip: %q -> %q", got.Anchor, plain)
	}
}

// Anchors are tried nearest-first, so the emitted flow leans on the landmark a
// human would have picked rather than one across the screen.
func TestFor_NearestUniqueAnchorWins(t *testing.T) {
	target := row("0.3", "Buy", 0, 300)
	snap := tree(
		row("0.0", "Far Away Heading", 0, 0),
		row("0.1", "Buy", 0, 100),
		row("0.2", "Just Above", 0, 280),
		target,
	)

	got := simflow.For(snap, target)

	if got.Anchor != "Just Above" {
		t.Errorf("anchor = %q, want the nearest unique label", got.Anchor)
	}
}

// Maestro compares the element's TOP edge, not its centre (VERIFIED by
// decompiling Filters.below in maestro-client 2.8.0). A centre-based rule
// would disagree with Maestro exactly where the boxes overlap, which is where
// list rows live.
func TestFor_RelationUsesTheTopEdgeNotTheCentre(t *testing.T) {
	// The anchor's top edge is above the target's, but its CENTRE is below,
	// because the anchor is tall. Maestro says "below"; a centre rule says the
	// opposite.
	anchor := simbridge.Element{
		Path: "0.1", Label: "Tall Anchor",
		Frame: simbridge.Rect{X: 0, Y: 90, Width: 100, Height: 200},
		Tap:   at(0.1, 0.2),
	}
	target := row("0.2", "Buy", 0, 150)
	snap := tree(row("0.0", "Buy", 0, 0), anchor, target)

	got := simflow.For(snap, target)

	if got.Rung != simflow.RungTextAnchor {
		t.Fatalf("rung = %v, want RungTextAnchor", got.Rung)
	}
	if got.Relation != simflow.RelBelow {
		t.Errorf("relation = %q, want below - top edge 150 > 90", got.Relation)
	}
}

// Our frames are floats; Maestro's Bounds are ints. An anchor decided on a
// fraction of a point is one Maestro may read as a tie, and a tie admits two
// candidates where we counted one - which is the silent wrong-element failure
// this rung exists to remove. A sub-point gap must therefore not be enough.
func TestFor_SubPointEdgeDifferenceIsNotEnoughToAnchor(t *testing.T) {
	// The anchor sits 0.4 points above one candidate: clearly "below" in
	// floating point, exactly equal once rounded to whole points.
	anchor := simbridge.Element{
		Path: "0.1", Label: "Heading",
		Frame: simbridge.Rect{X: 0, Y: 100.0, Width: 100, Height: 20},
		Tap:   at(0.1, 0.1),
	}
	near := simbridge.Element{
		Path: "0.0", Label: "Buy",
		Frame: simbridge.Rect{X: 0, Y: 100.4, Width: 100, Height: 20},
		Tap:   at(0.1, 0.2),
	}
	far := simbridge.Element{
		Path: "0.2", Label: "Buy",
		Frame: simbridge.Rect{X: 0, Y: 40, Width: 100, Height: 20},
		Tap:   at(0.1, 0.05),
	}
	snap := tree(near, anchor, far)

	if got := simflow.For(snap, near); got.Rung == simflow.RungTextAnchor && got.Relation == simflow.RelBelow {
		t.Fatalf("anchored on a 0.4-point difference Maestro cannot see: %+v", got)
	}
}

// The margin is "at least one whole point", and exactly one point qualifies:
// two edges a point apart are strictly ordered as ints however Maestro
// rounded them, so refusing that gap would throw away a usable anchor.
func TestFor_ExactlyOnePointApartIsEnoughToAnchor(t *testing.T) {
	anchor := simbridge.Element{
		Path: "0.1", Label: "Heading",
		Frame: simbridge.Rect{X: 0, Y: 100, Width: 100, Height: 5},
		Tap:   at(0.1, 0.1),
	}
	// Exactly one point below the anchor's top edge; the other candidate is
	// well above it, so only this one is "below".
	target := simbridge.Element{
		Path: "0.2", Label: "Buy",
		Frame: simbridge.Rect{X: 0, Y: 101, Width: 100, Height: 20},
		Tap:   at(0.1, 0.2),
	}
	above := simbridge.Element{
		Path: "0.0", Label: "Buy",
		Frame: simbridge.Rect{X: 0, Y: 20, Width: 100, Height: 20},
		Tap:   at(0.1, 0.03),
	}
	snap := tree(above, anchor, target)

	got := simflow.For(snap, target)

	if got.Rung != simflow.RungTextAnchor || got.Relation != simflow.RelBelow {
		t.Fatalf("a one-point gap is visible to Maestro and must anchor: %+v", got)
	}
}

// An off-screen anchor must be refused even when it is the ONLY thing that
// would disambiguate - otherwise the emitted step names an element Maestro's
// hierarchy does not contain and the whole selector fails at replay.
func TestFor_OffScreenAnchorIsRefusedEvenWhenItIsTheOnlyOneThatWouldWork(t *testing.T) {
	// The only unique label sits BETWEEN the two candidates, so "below" would
	// single out the lower one perfectly - if it were on screen.
	hidden := simbridge.Element{
		Path: "0.1", Label: "Only Unique Label",
		Frame:     simbridge.Rect{X: 0, Y: 200, Width: 100, Height: 20},
		OffScreen: true,
	}
	target := row("0.2", "Buy", 0, 300)
	snap := tree(row("0.0", "Buy", 0, 100), hidden, target)

	got := simflow.For(snap, target)

	if got.Rung == simflow.RungTextAnchor {
		t.Fatalf("anchored on an off-screen label Maestro will not report: %+v", got)
	}
	if got.Rung != simflow.RungTextIndex {
		t.Fatalf("rung = %v, want the honest fallback to an index", got.Rung)
	}
}

// The dangerous shape of the same rule: two candidates satisfy the relation
// and the one we want happens to be the LAST of them. A count that merely
// asks "did anything match" accepts this, emits an anchor that leaves Maestro
// two elements to choose between, and Maestro picks the nearest - which is
// precisely the silent wrong-element failure the anchor rung replaced the
// index to avoid.
func TestFor_AnchorIsRefusedWhenTheTargetIsMerelyTheLastOfSeveralMatches(t *testing.T) {
	header := row("0.0", "Header", 0, 0)
	first := row("0.1", "Buy", 0, 100)
	target := row("0.2", "Buy", 0, 200)
	snap := tree(header, first, target)

	got := simflow.For(snap, target)

	if got.Rung == simflow.RungTextAnchor {
		t.Fatalf("both candidates are below %q, so it pins neither: %+v", "Header", got)
	}
	if got.Rung != simflow.RungTextIndex {
		t.Fatalf("rung = %v, want the honest fallback to an index", got.Rung)
	}
}

// Maestro matches an element's whole text. A label the app wrote with a
// trailing space (nter: "เปิดบัญชี สะดวกกับ ") trimmed to "เปิดบัญชี สะดวกกับ"
// matched nothing in Maestro's tree, so the selector keeps the label exactly -
// while uniqueness is still counted on the trimmed text.
func TestFor_LabelKeepsItsWhitespaceForMaestrosWholeTextMatch(t *testing.T) {
	el := simbridge.Element{Path: "0.0", Label: "Open an account ", Tap: at(0.5, 0.2)}
	other := simbridge.Element{Path: "0.1", Label: "Open an account", Tap: at(0.5, 0.6)}

	got := simflow.For(tree(el), el)
	if got.Rung != simflow.RungText || got.Text != "Open an account " {
		t.Fatalf("got %+v, want RungText with the label as written", got)
	}
	if amb := simflow.For(tree(el, other), el).Ambiguity; amb != 2 {
		t.Errorf("ambiguity = %d, want 2: the two read the same to a person and collide", amb)
	}
	if !strings.Contains(simflow.Render(got, "Open an account "), `- tapOn: "Open an account "`) {
		t.Errorf("render dropped the trailing space: %q", simflow.Render(got, "Open an account "))
	}
}

// A scroll indicator's label is unique on a screen and still useless as an
// anchor: it changes with the content's length. Measured on nter, "Vertical
// scroll bar, 2 pages" was picked to pin a sign-in button.
func TestFor_ScrollIndicatorIsNeverUsedAsAnAnchor(t *testing.T) {
	first := row("0.0", "Buy", 0, 100)
	indicator := row("0.1", "Vertical scroll bar, 2 pages", 0, 200)
	indicator.Type = "Other"
	heading := row("0.2", "Second Section", 0, 250)
	second := row("0.3", "Buy", 0, 300)

	got := simflow.For(tree(first, indicator, heading, second), second)
	if got.Anchor != "Second Section" {
		t.Fatalf("anchor = %q, want the heading - the nearer scroll indicator is not a landmark", got.Anchor)
	}
	if !simflow.IsScrollIndicator(indicator) || simflow.IsScrollIndicator(heading) {
		t.Error("IsScrollIndicator must name the indicator and only it")
	}
}

// An id is not unique because it is an id: nter names the clock icon in every
// row "feature-content-clock". Maestro takes the first match, so a repeated id
// is pinned by an anchor, or by an index flagged for review.
func TestFor_RepeatedIDIsNarrowedLikeARepeatedLabel(t *testing.T) {
	icon := func(path string, y float64) simbridge.Element {
		return simbridge.Element{Path: path, ID: "clock", Frame: simbridge.Rect{X: 0, Y: y, Width: 20, Height: 20},
			Tap: at(10.0/440, (y+10)/956)}
	}
	first, second := icon("0.0", 100), icon("0.2", 300)
	heading := row("0.1", "Second Section", 0, 200)

	got := simflow.For(tree(first, heading, second), second)
	if got.Rung != simflow.RungID || got.Ambiguity != 2 || got.Anchor != "Second Section" || got.Relation != simflow.RelBelow {
		t.Fatalf("got %+v, want RungID pinned below the heading", got)
	}
	if got.NeedsReview() {
		t.Error("an anchored id was narrowed, not guessed")
	}
	rendered := simflow.Render(got, "")
	if !strings.Contains(rendered, "    id: \"clock\"\n    below:\n      text: \"Second Section\"\n") {
		t.Errorf("render = %q, want the id pinned below the heading", rendered)
	}

	unpinned := simflow.For(tree(first, second), second)
	if unpinned.Index != 1 || unpinned.Anchor != "" || !unpinned.NeedsReview() {
		t.Fatalf("got %+v, want index 1 and a review flag", unpinned)
	}
	if rendered := simflow.Render(unpinned, ""); !strings.Contains(rendered, "    index: 1\n") || !strings.Contains(rendered, "# REVIEW:") {
		t.Errorf("render = %q, want the index and the review marker", rendered)
	}

	if alone := simflow.For(tree(first), first); alone.Ambiguity != 1 || alone.NeedsReview() {
		t.Errorf("a unique id = %+v, want ambiguity 1 and no review", alone)
	}
}

func TestRenderAction_UsesTheVerbItIsGiven(t *testing.T) {
	got := simflow.RenderAction("longPressOn", simflow.Choice{Rung: simflow.RungText, Text: "Password", Ambiguity: 1}, "Password")
	if got != "- longPressOn: \"Password\"\n" {
		t.Errorf("got %q", got)
	}
}

// Maestro counts an index over the DEEPEST matches (Filters.
// deepestMatchingElement): a label wrapper and the text inside it are one
// match. nter's sign-in email field was index 2 of 3 in our tree and out of
// range in Maestro's - the recorded flow failed there, twice.
func TestFor_IndexIsCountedOverTheDeepestMatchesLikeMaestro(t *testing.T) {
	wrapper := simbridge.Element{Path: "0.0", Label: "Email", Frame: simbridge.Rect{X: 74, Y: 415, Width: 34, Height: 26},
		Children: []simbridge.Element{{Path: "0.0.0", Label: "Email", Frame: simbridge.Rect{X: 74, Y: 415, Width: 34, Height: 26}}}}
	field := simbridge.Element{Path: "0.1", Type: "TextField", Label: "Email", Frame: simbridge.Rect{X: 74, Y: 443, Width: 254, Height: 54},
		Tap: at(0.5, 0.55)}
	snap := tree(wrapper, field)

	got := simflow.For(snap, field)
	if got.Ambiguity != 2 || got.Index != 1 {
		t.Fatalf("got %+v, want ambiguity 2 and index 1 - the wrapper holds its text and counts as one", got)
	}
	// The wrapper itself is the match it holds.
	if w := simflow.For(snap, wrapper); w.Index != 0 {
		t.Errorf("wrapper index = %d, want 0 (the text inside it)", w.Index)
	}
}

// A field whose label repeats is still unique by its placeholder, which
// Maestro matches as hintText - better than any index.
func TestFor_ARepeatedFieldLabelFallsBackToAUniquePlaceholder(t *testing.T) {
	caption := row("0.0", "Email", 0, 100)
	field := row("0.1", "Email", 0, 130)
	field.Type, field.Placeholder = "TextField", "example@email.com"

	got := simflow.For(tree(caption, field), field)
	if got.Rung != simflow.RungText || got.Text != `example@email\.com` || !got.Escaped || got.NeedsReview() {
		t.Fatalf("got %+v, want the unique placeholder", got)
	}
}

// A button and the text inside it carry one label and are one control: no
// index, no review.
func TestFor_AButtonAndItsOwnTextAreOneMatch(t *testing.T) {
	button := simbridge.Element{Path: "0.0", Type: "Button", Label: "Sign in", Frame: simbridge.Rect{X: 0, Y: 0, Width: 100, Height: 40},
		Tap: at(0.1, 0.02), Children: []simbridge.Element{{Path: "0.0.0", Type: "StaticText", Label: "Sign in",
			Frame: simbridge.Rect{X: 10, Y: 10, Width: 60, Height: 20}}}}
	got := simflow.For(tree(button), button)
	if got.Rung != simflow.RungText || got.Ambiguity != 1 || got.NeedsReview() {
		t.Fatalf("got %+v, want a plain unique text selector", got)
	}
}

// mobile-ui-scripts rule 4: an id that names one control beats its label.
func TestFor_AUniqueIDBeatsTheLabel(t *testing.T) {
	el := simbridge.Element{Path: "0.0", Label: "Search", ID: "search-field", Tap: at(0.5, 0.1)}
	if got := simflow.For(tree(el), el); got.Rung != simflow.RungID || got.ID != "search-field" {
		t.Fatalf("got %+v, want the id", got)
	}
}

// A tab whose label repeats elsewhere is named by the icon inside it when
// that icon's id is unique: no anchor (the nearest unique label on nter was a
// price) and no index.
func TestFor_ARepeatedLabelIsNamedByAUniqueIDInsideIt(t *testing.T) {
	feed := row("0.0", "Markets", 0, 100)
	tab := row("0.1", "Markets", 200, 800)
	tab.Type = "Button"
	tab.Children = []simbridge.Element{{Path: "0.1.0", Type: "Image", ID: "icon-tabBar-graph",
		Frame: simbridge.Rect{X: 230, Y: 802, Width: 20, Height: 10}}}
	price := row("0.2", "6.24%", 100, 790)

	got := simflow.For(tree(feed, tab, price), tab)
	if got.Rung != simflow.RungID || got.ID != "icon-tabBar-graph" || got.NeedsReview() {
		t.Fatalf("got %+v, want the tab named by its icon's id", got)
	}
}

// A label holding a digit is a value - a price, a date - and pins only today's
// screen: a landmark is preferred even when the value sits nearer.
func TestFor_AnAnchorPrefersALandmarkOverANearerValue(t *testing.T) {
	first := row("0.0", "Buy", 0, 100)
	heading := row("0.1", "Second Section", 0, 150)
	price := row("0.2", "6.24%", 0, 280)
	second := row("0.3", "Buy", 0, 300)

	got := simflow.For(tree(first, heading, price, second), second)
	if got.Anchor != "Second Section" {
		t.Fatalf("anchor = %q, want the heading over the nearer price", got.Anchor)
	}
}
