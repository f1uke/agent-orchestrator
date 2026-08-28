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
