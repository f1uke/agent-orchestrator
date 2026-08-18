package simflow

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// The counts a flow states about itself, and the one line both sides of that
// statement go through.
//
// A generated flow is read twice: once by the human who opens it, and once by
// AO listing what a session has recorded. The second reader has only the file
// - the recording it came from may be long gone, and on a machine where the
// flows were written by `ao sim record stop` from a terminal there was never a
// row to read in the first place. So the flow has to be able to answer "how
// many steps, and how many of them are guesses" from its own bytes.
//
// Writing that line and reading it back live in one file on purpose. A writer
// and a parser that drift apart would make a list confidently report numbers a
// flow does not contain, which is the same class of untruth as a header that
// claims a flow is clean while a step below it carries a marker.

// countsLine is the format Emit writes and ParseCounts reads. It is prose
// rather than a machine-shaped key because a person opening the flow is the
// primary reader of every line in this header; it is parseable because the
// package owns both ends of it and pins them with a round trip.
//
// It deliberately does NOT contain the reviewMarker string. The marker is
// documented as the one thing a human, a grep and Emit's banner all recognise
// alike, and a header line quoting it would put a `# REVIEW:` hit in every
// clean flow - breaking the grep for the sake of a prettier sentence.
const countsLine = "# %d step(s), %d needing review\n"

var countsPattern = regexp.MustCompile(`^# (\d+) step\(s\), (\d+) needing review$`)

// Counts is what a flow says about itself: how many steps it contains, and how
// many of those the generator could not resolve to one element with
// confidence.
type Counts struct {
	Steps  int
	Review int
}

// ReviewCount reports how many steps a reader must check before trusting the
// flow.
//
// It is the single definition of that number. Emit's banner, the self-
// describing counts line, and anything downstream that lists a flow all call
// this rather than re-deriving it, so there is no second rule that could
// disagree with Choice.NeedsReview about which steps are guesses.
func ReviewCount(steps []Step) int {
	n := 0
	for _, step := range steps {
		if actsOnAnElement(step.Kind) && step.Choice.NeedsReview() {
			n++
		}
	}
	return n
}

// writeCounts states the flow's own counts in its header.
func writeCounts(b *strings.Builder, steps []Step) {
	fmt.Fprintf(b, countsLine, len(steps), ReviewCount(steps))
}

// ParseCounts recovers what a flow says about itself.
//
// ok is false for a flow that does not state its counts - every flow written
// before this line existed. That is reported rather than guessed at: a list
// that shows "-" for a flow it cannot measure is telling the truth, and one
// that shows 0 because it found nothing to parse is not. Counting the
// "# REVIEW:" markers in the body would recover half the answer for those
// files, but not the step count, and a row where one number is measured and
// the other invented is worse than a row that says it does not know.
func ParseCounts(flow string) (Counts, bool) {
	for _, line := range strings.Split(flow, "\n") {
		m := countsPattern.FindStringSubmatch(strings.TrimRight(line, "\r"))
		if m == nil {
			continue
		}
		steps, err := strconv.Atoi(m[1])
		if err != nil {
			return Counts{}, false
		}
		review, err := strconv.Atoi(m[2])
		if err != nil {
			return Counts{}, false
		}
		return Counts{Steps: steps, Review: review}, true
	}
	return Counts{}, false
}
