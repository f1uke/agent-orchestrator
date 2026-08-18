package simflow_test

import (
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/simflow"
)

// tapText is a step that resolves cleanly: a unique label, nothing guessed.
func tapText(seq int64, label string) simflow.Step {
	return simflow.Step{
		Seq: seq, Kind: simflow.StepTap, Plain: label,
		Choice: simflow.Choice{Rung: simflow.RungText, Text: label, Ambiguity: 1},
	}
}

// tapGuessed is a step that fell through to an index - the shape the whole
// review count exists to make visible.
func tapGuessed(seq int64, label string) simflow.Step {
	return simflow.Step{
		Seq: seq, Kind: simflow.StepTap, Plain: label,
		Choice: simflow.Choice{Rung: simflow.RungTextIndex, Text: label, Index: 1, Ambiguity: 3},
	}
}

// The one property everything downstream rests on: what a flow says about
// itself is what Emit was actually given. A list that reads these numbers back
// is only as honest as this round trip.
func TestCounts_RoundTripThroughAnEmittedFlow(t *testing.T) {
	cases := []struct {
		name  string
		steps []simflow.Step
		want  simflow.Counts
	}{
		{"empty recording", nil, simflow.Counts{Steps: 0, Review: 0}},
		{"nothing guessed", []simflow.Step{tapText(1, "Home"), tapText(2, "Next")}, simflow.Counts{Steps: 2, Review: 0}},
		{"one guess among three", []simflow.Step{
			tapText(1, "Home"), tapGuessed(2, "Buy"), tapText(3, "Done"),
		}, simflow.Counts{Steps: 3, Review: 1}},
		{"every step guessed", []simflow.Step{tapGuessed(1, "Buy"), tapGuessed(2, "Buy")}, simflow.Counts{Steps: 2, Review: 2}},
		{"typing is a step but never a guess", []simflow.Step{
			{Seq: 1, Kind: simflow.StepType, Text: "hello"}, tapGuessed(2, "Buy"),
		}, simflow.Counts{Steps: 2, Review: 1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			flow, err := simflow.Emit(tc.steps, simflow.EmitOptions{Device: "d", Runtime: "r", RecordedAt: "t"})
			if err != nil {
				t.Fatalf("Emit: %v", err)
			}
			got, ok := simflow.ParseCounts(flow)
			if !ok {
				t.Fatalf("an emitted flow must always state its own counts:\n%s", flow)
			}
			if got != tc.want {
				t.Errorf("ParseCounts = %+v, want %+v, from:\n%s", got, tc.want, flow)
			}
		})
	}
}

// The number a reader is asked to act on is the number of markers actually in
// the file. If these could drift, a list would send a human looking for
// warnings that are not there - or, worse, let them skip a flow that has some.
func TestCounts_MatchTheMarkersAndBannerInTheFlow(t *testing.T) {
	steps := []simflow.Step{tapText(1, "Home"), tapGuessed(2, "Buy"), tapGuessed(3, "Buy"), tapText(4, "Done")}

	flow, err := simflow.Emit(steps, simflow.EmitOptions{Device: "d", Runtime: "r", RecordedAt: "t"})
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	counts, ok := simflow.ParseCounts(flow)
	if !ok {
		t.Fatalf("no counts line:\n%s", flow)
	}
	// Lines that BEGIN with the marker: the banner quotes the marker when
	// explaining it, and a substring count would read that mention as a step.
	markers := 0
	for _, line := range strings.Split(flow, "\n") {
		if strings.HasPrefix(line, "# REVIEW:") {
			markers++
		}
	}
	if markers != counts.Review {
		t.Errorf("counts line says %d needing review, but the flow carries %d %q markers:\n%s",
			counts.Review, markers, "# REVIEW:", flow)
	}
	if !strings.Contains(flow, "REVIEW REQUIRED: 2 of 4 steps") {
		t.Errorf("the banner must agree with the counts line:\n%s", flow)
	}
}

// A clean flow must stay greppable. The counts line names the same subject as
// the marker, so the temptation to quote it is real - and quoting it would put
// a false positive in every flow that has nothing to review.
func TestCounts_CleanFlowContainsNoReviewMarker(t *testing.T) {
	flow, err := simflow.Emit([]simflow.Step{tapText(1, "Home")}, simflow.EmitOptions{Device: "d", Runtime: "r", RecordedAt: "t"})
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if strings.Contains(flow, "# REVIEW:") {
		t.Errorf("a clean flow must carry no review marker, so `grep '# REVIEW:'` means what it says:\n%s", flow)
	}
	if counts, ok := simflow.ParseCounts(flow); !ok || counts.Review != 0 {
		t.Errorf("ParseCounts = %+v ok=%v, want 0 needing review", counts, ok)
	}
}

// A flow written before flows stated their counts must read as unmeasured, not
// as measured-and-empty. "-" in a list is true; "0 steps" for a flow with
// twelve of them is not.
func TestParseCounts_FlowWithoutTheLineIsUnknownNotZero(t *testing.T) {
	legacy := "appId: ${APP_ID}\n---\n# recorded by ao sim at t, device d (r)\n- tapOn: \"Home\"\n- tapOn: \"Next\"\n"
	if counts, ok := simflow.ParseCounts(legacy); ok {
		t.Errorf("ParseCounts said it knew %+v about a flow that states nothing", counts)
	}
}

// Prose near the counts line must not be mistaken for it. The pattern is
// anchored precisely so a comment a human wrote into their own flow cannot
// become the number a list reports.
func TestParseCounts_IgnoresLinesThatMerelyLookLikeIt(t *testing.T) {
	for _, line := range []string{
		"# 3 step(s), 2 needing review someday",
		"#  3 step(s), 2 needing review",
		"- text: \"# 3 step(s), 2 needing review\"",
		"# about 3 step(s), 2 needing review",
	} {
		if counts, ok := simflow.ParseCounts("appId: ${APP_ID}\n---\n" + line + "\n"); ok {
			t.Errorf("ParseCounts(%q) = %+v, want not recognized", line, counts)
		}
	}
}

// ReviewCount is the one definition of "how many steps are guesses". Every
// caller reads it rather than re-deriving it, so this pins the rule itself.
func TestReviewCount_CountsOnlyGuessesOnStepsThatTargetAnElement(t *testing.T) {
	steps := []simflow.Step{
		tapText(1, "Home"),
		tapGuessed(2, "Buy"),
		// A coordinate is always a guess.
		{Seq: 3, Kind: simflow.StepTap, Choice: simflow.Choice{Rung: simflow.RungPoint, PercentX: 50, PercentY: 50}},
		// Typing and buttons carry a Choice resolved from (0,0) - whatever sits
		// in the corner - so counting them would report guesses about elements
		// the steps never touched.
		{Seq: 4, Kind: simflow.StepType, Text: "hello", Choice: simflow.Choice{Rung: simflow.RungPoint}},
		{Seq: 5, Kind: simflow.StepButton, Detail: "home", Choice: simflow.Choice{Rung: simflow.RungNone}},
	}
	if got := simflow.ReviewCount(steps); got != 2 {
		t.Errorf("ReviewCount = %d, want 2 (the index guess and the coordinate)", got)
	}
}

// A named keyboard key is not typing, and the difference is not cosmetic: a
// flow that turned Enter into inputText "\n" would put a newline in the field
// and submit nothing.
func TestEmit_KeyStepsBecomePressKey(t *testing.T) {
	steps := []simflow.Step{
		{Seq: 1, Kind: simflow.StepType, Text: "hello"},
		{Seq: 2, Kind: simflow.StepKey, Detail: "backspace"},
		{Seq: 3, Kind: simflow.StepKey, Detail: "enter"},
		{Seq: 4, Kind: simflow.StepKey, Detail: "tab"},
		{Seq: 5, Kind: simflow.StepKey, Detail: "arrow-up"},
		{Seq: 6, Kind: simflow.StepKey, Detail: "arrow-down"},
		{Seq: 7, Kind: simflow.StepKey, Detail: "arrow-left"},
		{Seq: 8, Kind: simflow.StepKey, Detail: "arrow-right"},
	}
	flow, err := simflow.Emit(steps, simflow.EmitOptions{Device: "d", Runtime: "r", RecordedAt: "t"})
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	for _, want := range []string{
		`- inputText: "hello"`,
		"- pressKey: Backspace",
		"- pressKey: Enter",
		"- pressKey: Tab",
		"- pressKey: Arrow Up",
		"- pressKey: Arrow Down",
		"- pressKey: Arrow Left",
		"- pressKey: Arrow Right",
	} {
		if !strings.Contains(flow, want) {
			t.Errorf("flow missing %q:\n%s", want, flow)
		}
	}
	// Order is what makes an edit mean anything: typing, then the backspace
	// that removes part of it.
	if strings.Index(flow, `- inputText: "hello"`) > strings.Index(flow, "- pressKey: Backspace") {
		t.Errorf("steps came out in the wrong order:\n%s", flow)
	}
}

// A key with no Maestro spelling must fail Emit by name rather than be dropped.
// ⚠ `ao sim flow check` accepts ANY string after pressKey - it parses, it does
// not validate - so a name that slipped through here would produce a flow that
// runs and silently does nothing.
func TestEmit_UnknownKeyIsRefusedRatherThanGuessed(t *testing.T) {
	_, err := simflow.Emit([]simflow.Step{{Seq: 1, Kind: simflow.StepKey, Detail: "page-down"}},
		simflow.EmitOptions{Device: "d", Runtime: "r", RecordedAt: "t"})
	if err == nil {
		t.Fatal("an unknown key must refuse the flow, not be written as a guess")
	}
	if !strings.Contains(err.Error(), "page-down") {
		t.Errorf("the refusal must name the key: %v", err)
	}
}

// A key press is not a step a human is asked to check: it targets no element,
// so it can neither be a guess nor need review.
func TestReviewCount_IgnoresKeyPresses(t *testing.T) {
	steps := []simflow.Step{
		{Seq: 1, Kind: simflow.StepKey, Detail: "enter", Choice: simflow.Choice{Rung: simflow.RungNone}},
		{Seq: 2, Kind: simflow.StepKey, Detail: "backspace", Choice: simflow.Choice{Rung: simflow.RungPoint}},
	}
	if got := simflow.ReviewCount(steps); got != 0 {
		t.Errorf("ReviewCount = %d, want 0", got)
	}
}
