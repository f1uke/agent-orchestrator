package simrecord

import (
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/simflow"
)

// The Device tab drives a pinch as three held-touch steps, exactly as it drives
// a drag. Maestro has no pinch, so a recording containing one has to be refused
// by name - and by the SAME name `ao sim pinch` is refused under, or a human
// reading the refusal goes looking for a "pinch-begin" that does not exist.
func TestSteps_APinchIsRefusedUnderTheOneWordThatNamesIt(t *testing.T) {
	for _, kind := range []string{"pinch-begin", "pinch-move", "pinch-end"} {
		got := Steps([]domain.SimRecordingStep{{Seq: 1, Kind: kind, X: 0.5, Y: 0.5}})
		if len(got) != 1 {
			t.Fatalf("%s: got %d steps, want one", kind, len(got))
		}
		if got[0].Kind != simflow.StepKind("pinch") {
			t.Fatalf("%s mapped to %q, want pinch", kind, got[0].Kind)
		}
		_, err := simflow.Emit(got, simflow.EmitOptions{Device: "iPhone 17 Pro Max", Runtime: "iOS 26.3"})
		if err == nil || !strings.Contains(err.Error(), `kind "pinch" has no Maestro translation`) {
			t.Fatalf("%s: Emit error = %v, want it refused as \"pinch\"", kind, err)
		}
	}
}

// And the drag steps the Device tab has always sent still emit as a swipe. This
// is the regression guard for the switch above them: the pinch arm was added to
// the same switch, and a mistake there would silently turn every recorded drag
// into an untranslatable step.
func TestSteps_ADragStillEmitsAsASwipe(t *testing.T) {
	for _, kind := range []string{"swipe", "drag", "drag-begin", "drag-move", "drag-end"} {
		got := Steps([]domain.SimRecordingStep{{Seq: 1, Kind: kind, X: 0.2, Y: 0.8, ToX: 0.2, ToY: 0.2}})
		if got[0].Kind != simflow.StepSwipe {
			t.Fatalf("%s mapped to %q, want swipe", kind, got[0].Kind)
		}
	}
}

// A drag that never moved is a long press: the gesture that raises a text
// field's Paste menu. Replayed as a zero-length swipe it raises nothing. A
// quick one, or one that moved, is still a swipe.
func TestSteps_AFingerHeldStillIsALongPress(t *testing.T) {
	still := domain.SimRecordingStep{Seq: 1, Kind: "drag", X: 0.5, Y: 0.52, ToX: 0.5, ToY: 0.52, DurationMS: 1000,
		Selector: "Email", SelectorRung: int64(simflow.RungText), Ambiguity: 1}
	got := Steps([]domain.SimRecordingStep{still})
	if got[0].Kind != simflow.StepLongPress || got[0].Choice.Text != "Email" {
		t.Fatalf("got %+v, want a long press on Email", got[0])
	}
	quick := still
	quick.DurationMS = 100
	moved := still
	moved.ToY = 0.8
	for _, step := range []domain.SimRecordingStep{quick, moved} {
		if got := Steps([]domain.SimRecordingStep{step}); got[0].Kind != simflow.StepSwipe {
			t.Errorf("%+v mapped to %q, want swipe", step, got[0].Kind)
		}
	}
}

// A secure step carries the flag and the field through, and a repeated id
// keeps the anchor that pins it.
func TestSteps_SecureAndAnchoredIDSurvive(t *testing.T) {
	got := Steps([]domain.SimRecordingStep{
		{Seq: 1, Kind: "type", Secure: true, Selector: "Password", SelectorRung: int64(simflow.RungText), Ambiguity: 1},
		{Seq: 2, Kind: "tap", Selector: "clock", SelectorRung: int64(simflow.RungID), Ambiguity: 3, SelectorIndex: 1,
			SelectorAnchor: "Second", SelectorAnchorRel: "below"},
	})
	if !got[0].Secure || got[0].Choice.Text != "Password" {
		t.Errorf("secure step = %+v", got[0])
	}
	if c := got[1].Choice; c.ID != "clock" || c.Anchor != "Second" || c.Relation != simflow.RelBelow || c.Index != 1 {
		t.Errorf("id step choice = %+v, want the id pinned below Second", c)
	}
}
