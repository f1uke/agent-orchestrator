package simrecord

import (
	"math"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/simflow"
)

// Steps turns a stored recording into the shape Emit reads.
//
// It lives here rather than in the CLI, where it used to, because the flow is
// now built once - by the daemon, for every caller - and a second copy of this
// mapping in a second process is exactly how two callers end up disagreeing
// about what a recording said.
//
// Each step, as storage returns it,
// into simflow.Step - Emit's own mechanism-neutral shape (Task 4).
//
// Choice.Index (which of several same-label matches this step resolved to)
// round-trips through SelectorIndex (0039_sim_recording_step_index.sql):
// without it, re-emitting a flow from a stored recording would always
// address the FIRST element sharing a label, silently substituting it for
// whichever one was actually tapped - a flow whose selector text reads
// correctly while touching the wrong element. That is a correctness bug, not
// a cosmetic one, which is why it has its own column rather than a
// documented gap.
//
// Choice.Anchor/Relation round-trip through their own columns
// (0041_sim_recording_step_anchor.sql) for the same reason: an anchor resolved
// at record time and then lost would fall back to the index it exists to
// replace, quietly reintroducing the failure it was added to remove.
//
// Choice.Escaped - whether the stored selector text needed regex escaping -
// has no column of its own and needs none: it is RECOVERED here rather than
// remembered, because a stored text selector is always the escaped form, so
// unescaping it recovers both the plain label and the fact that it was
// escaped in the first place. Both are used, and neither is cosmetic:
//
//   - Plain is what scrollUntilVisible matches on for an off-screen element,
//     and render.go's rule is that it is the label a human reads. Handing it
//     the stored (escaped) text emitted `element: "See all \\(12\\)"` for a
//     label that reads "See all (12)".
//   - Escaped drives the "# escaped: ..." comment that tells a reader why the
//     selector above it is full of backslashes.
func Steps(steps []domain.SimRecordingStep) []simflow.Step {
	out := make([]simflow.Step, 0, len(steps))
	for _, step := range steps {
		out = append(out, toFlowStep(step))
	}
	return out
}

func toFlowStep(step domain.SimRecordingStep) simflow.Step {
	choice := simflow.Choice{
		Rung:      simflow.Rung(step.SelectorRung),
		Index:     int(step.SelectorIndex),
		Ambiguity: int(step.Ambiguity),
		OffScreen: step.OffScreen,
	}
	// plain is the label as a human reads it; it stays empty for a rung whose
	// selector is not text, so Render falls back to the id the way it does for
	// a Choice that never had a label.
	var plain string
	switch choice.Rung {
	case simflow.RungText, simflow.RungTextIndex:
		choice.Text = step.Selector
		plain, choice.Escaped = simflow.Unescape(step.Selector)
	case simflow.RungTextAnchor:
		choice.Text = step.Selector
		plain, choice.Escaped = simflow.Unescape(step.Selector)
		choice.Anchor = step.SelectorAnchor
		_, choice.AnchorEscaped = simflow.Unescape(step.SelectorAnchor)
		choice.Relation = simflow.Relation(step.SelectorAnchorRel)
	case simflow.RungID:
		choice.ID = step.Selector
	case simflow.RungPoint:
		choice.PercentX = percent(step.X)
		choice.PercentY = percent(step.Y)
	}
	if choice.OffScreen {
		// The element's own box - what scrollDirectionFor (internal/simflow)
		// would otherwise decide this from - is never persisted either. DOWN
		// is that same function's fallback "when an element's edges are not
		// known", which is exactly this case.
		choice.ScrollDirection = simflow.ScrollDown
	}
	return simflow.Step{
		Seq:          step.Seq,
		Kind:         flowStepKind(step.Kind),
		Choice:       choice,
		Plain:        plain,
		ScreenChange: step.ScreenChange,
		X:            step.X, Y: step.Y, ToX: step.ToX, ToY: step.ToY,
		Text:   step.Text,
		Detail: step.Detail,
	}
}

// flowStepKind maps a recorded step's Kind onto simflow's coarser
// vocabulary (Task 4). A drag is emitted exactly like a swipe (spec §8), so
// both "swipe" and "drag" - along with the daemon's own "drag-begin"/
// "drag-move"/"drag-end" intent kinds, which the Device tab may still send
// under a single hold - map onto StepSwipe. Anything this switch does not
// recognize passes straight through: Emit refuses an untranslatable step by
// name rather than dropping it silently, and inventing a second "unknown
// kind" error here would just repeat that refusal worse.
func flowStepKind(kind string) simflow.StepKind {
	switch kind {
	case "tap":
		return simflow.StepTap
	case "type":
		return simflow.StepType
	case "swipe", "drag", "drag-begin", "drag-move", "drag-end":
		return simflow.StepSwipe
	case "button":
		return simflow.StepButton
	default:
		return simflow.StepKind(kind)
	}
}

// percent mirrors internal/simflow's own unexported percent(): a
// normalized 0..1 coordinate rounded to the whole percent Maestro takes,
// clamped to 0..100. Duplicated rather than exported because it is three
// lines and the alternative is widening simflow's API for a single caller.
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
