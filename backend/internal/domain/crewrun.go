package domain

import "time"

// CappedRepeat is AO's one answer to "how many times may something be repeated
// automatically before a human has to decide?".
//
// It was settled once, for review nudges (lifecycle's reviewMaxNudge), and the
// discarded-run retry is the same question about a different subject: try, try,
// try, then stop and say so. Two constants with the same value and no shared
// meaning is how they drift apart, so there is one.
const CappedRepeat = 3

// CrewRunKind says what a bracketed run WAS. It is advisory - the detector does
// not care - but it is what makes "qa is running" a useful thing to read.
type CrewRunKind string

// Crew run kinds.
const (
	CrewRunBuild  CrewRunKind = "build"
	CrewRunTest   CrewRunKind = "test"
	CrewRunDevice CrewRunKind = "device"
)

// Valid reports whether k is a known kind.
func (k CrewRunKind) Valid() bool {
	switch k {
	case CrewRunBuild, CrewRunTest, CrewRunDevice:
		return true
	}
	return false
}

// CrewRunOutcome is what the DETECTOR decided about a finished run: can this
// result be trusted at all? It is orthogonal to what the build or test said.
type CrewRunOutcome string

// Crew run outcomes. The zero value means the run is still open.
const (
	// CrewRunOpen is a run that has started and not ended.
	CrewRunOpen CrewRunOutcome = ""
	// CrewRunTrusted means nothing wrote to a non-ignored path between the run's
	// start and its end, so what the run observed is what the tree actually is.
	CrewRunTrusted CrewRunOutcome = "trusted"
	// CrewRunDiscarded means the tree moved under the run. The result is thrown
	// away - NOT failed, which would blame the code, and NOT passed, which is the
	// laundering this whole mechanism exists to stop.
	CrewRunDiscarded CrewRunOutcome = "discarded"
	// CrewRunUncertified means there was no working detector: it could not start,
	// it latched down mid-run, or it was lost (a daemon restart between the two
	// brackets). The result is reported as-is and explicitly NOT certified. An
	// absent detector has to be visible, exactly like one that misses.
	CrewRunUncertified CrewRunOutcome = "uncertified"
)

// CrewRunResult is what the build or the test said, as reported by the member
// that ran it. Empty means it did not say.
type CrewRunResult string

// Crew run results.
const (
	CrewRunResultNone CrewRunResult = ""
	CrewRunResultPass CrewRunResult = "pass"
	CrewRunResultFail CrewRunResult = "fail"
)

// Valid reports whether a supplied result is one AO accepts. The empty string is
// accepted separately by callers that allow "ran it, did not judge it".
func (r CrewRunResult) Valid() bool {
	return r == CrewRunResultPass || r == CrewRunResultFail
}

// CrewRunDetector records whether the tree-write detector was actually watching
// this run. It is stored, not derived, because it is a fact about a moment that
// has passed: the run either had a live watcher or it did not.
type CrewRunDetector string

// Detector states.
const (
	CrewRunDetectorLive CrewRunDetector = "live"
	CrewRunDetectorDown CrewRunDetector = "down"
)

// CrewRunState is the ONE word a human reads for a run. It deliberately has a
// third and a fourth value next to passed/failed: a run whose tree moved, and a
// run nothing was watching, are not verdicts about the code and must never be
// rendered as one.
type CrewRunState string

// Crew run display states.
const (
	CrewRunStateRunning     CrewRunState = "running"
	CrewRunStatePassed      CrewRunState = "passed"
	CrewRunStateFailed      CrewRunState = "failed"
	CrewRunStateFinished    CrewRunState = "finished"
	CrewRunStateDiscarded   CrewRunState = "discarded"
	CrewRunStateUncertified CrewRunState = "uncertified"
)

// CrewRun is one bracketed build/test/device run in a worktree: what it was, who
// ran it, the write generation at each end, and what the detector concluded.
//
// The bracket has two consumers and one mechanism. It is what the detector needs
// (a start reading and an end reading), and while it is open it is also the only
// thing that can tell the board "qa is running a build" rather than the far
// weaker "qa is awake" - ActivityState cannot distinguish a build from reading a
// file. Building it for the detector gets the status for free, with no second
// mechanism to keep in step.
type CrewRun struct {
	ID        string    `json:"id"`
	SessionID SessionID `json:"sessionId"`
	ProjectID ProjectID `json:"projectId"`
	// CrewID is the task this run belongs to (dev's session id), or empty when
	// the member is solo. Stored so a run survives being read from either member.
	CrewID SessionID `json:"crewId,omitempty"`
	Role   CrewRole  `json:"role,omitempty"`
	// WorktreePath is the checkout that was watched.
	WorktreePath string      `json:"worktreePath,omitempty"`
	Kind         CrewRunKind `json:"kind"`
	Label        string      `json:"label,omitempty"`
	// Attempt counts this run within a streak of discards: 1 for the first try,
	// and it is what the CappedRepeat cap is measured against.
	Attempt  int             `json:"attempt"`
	Detector CrewRunDetector `json:"detector"`
	// DetectorReason says why the detector is down, and is empty when it is live.
	DetectorReason string    `json:"detectorReason,omitempty"`
	GenAtStart     uint64    `json:"genAtStart"`
	GenAtEnd       uint64    `json:"genAtEnd"`
	StartedAt      time.Time `json:"startedAt"`
	// EndedAt is nil while the run is open.
	EndedAt *time.Time     `json:"endedAt,omitempty"`
	Outcome CrewRunOutcome `json:"outcome,omitempty"`
	Result  CrewRunResult  `json:"result,omitempty"`
	// ChangedPaths names up to a handful of the paths that moved, so a discarded
	// run can say WHAT moved instead of only that something did.
	ChangedPaths []string `json:"changedPaths,omitempty"`
	// HeadSHA is the commit HEAD pointed at when the run ended. On its own it is
	// not the identity of a run - a dirty tree is not reproducible from a SHA -
	// but paired with a trusted outcome it says which commit plus which
	// verified-unchanged working tree was measured.
	HeadSHA   string    `json:"headSha,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// Open reports whether the run has started and not ended.
func (r CrewRun) Open() bool { return r.EndedAt == nil }

// State is the single word this run reads as. A discarded or uncertified run
// never borrows the reported pass/fail: that is exactly the substitution the
// detector exists to prevent.
func (r CrewRun) State() CrewRunState {
	if r.Open() {
		return CrewRunStateRunning
	}
	switch r.Outcome {
	case CrewRunDiscarded:
		return CrewRunStateDiscarded
	case CrewRunUncertified:
		return CrewRunStateUncertified
	}
	switch r.Result {
	case CrewRunResultPass:
		return CrewRunStatePassed
	case CrewRunResultFail:
		return CrewRunStateFailed
	}
	return CrewRunStateFinished
}

// Trustworthy reports whether this run's result may be taken at face value.
func (r CrewRun) Trustworthy() bool { return !r.Open() && r.Outcome == CrewRunTrusted }
