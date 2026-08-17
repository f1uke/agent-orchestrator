package lifecycle

import (
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// A worker that vanishes mid-task must leave behind WHO ended it and WHY. The
// harness hands AO a SessionEnd reason; before this it was parsed only to decide
// whether to report "exited" at all, then dropped — so a session that stopped by
// itself was indistinguishable from one AO tore down.
func TestApplyActivitySignal_ExitedRecordsAgentReasonAndLastState(t *testing.T) {
	m, st, _ := newManager()
	rec := working("mer-1")
	rec.FirstSignalAt = time.Now().Add(-time.Hour)
	st.sessions["mer-1"] = rec

	sig := ports.ActivitySignal{Valid: true, State: domain.ActivityExited, End: &ports.SessionEnd{Reason: "prompt_input_exit"}}
	if err := m.ApplyActivitySignal(ctx, "mer-1", sig); err != nil {
		t.Fatal(err)
	}

	got := st.sessions["mer-1"].Termination
	if got.Source != domain.TerminationSourceAgent {
		t.Errorf("source = %q, want %q — AO did not initiate this end", got.Source, domain.TerminationSourceAgent)
	}
	if got.Reason != "prompt_input_exit" {
		t.Errorf("reason = %q, want the harness's own reason", got.Reason)
	}
	if got.LastState != domain.ActivityActive {
		t.Errorf("lastState = %q, want %q — the session was working when it stopped", got.LastState, domain.ActivityActive)
	}
	if got.At.IsZero() {
		t.Error("termination time must be stamped")
	}
}

// A harness that reports no reason (or one AO does not recognise) still records
// that the AGENT ended it. "unknown" is a real answer; silence is not.
func TestApplyActivitySignal_ExitedWithoutReasonStillNamesTheAgent(t *testing.T) {
	m, st, _ := newManager()
	rec := working("mer-1")
	rec.FirstSignalAt = time.Now().Add(-time.Hour)
	st.sessions["mer-1"] = rec

	if err := m.ApplyActivitySignal(ctx, "mer-1", ports.ActivitySignal{Valid: true, State: domain.ActivityExited}); err != nil {
		t.Fatal(err)
	}

	got := st.sessions["mer-1"].Termination
	if got.Source != domain.TerminationSourceAgent {
		t.Errorf("source = %q, want %q", got.Source, domain.TerminationSourceAgent)
	}
	if got.Reason != domain.TerminationReasonUnknown {
		t.Errorf("reason = %q, want %q", got.Reason, domain.TerminationReasonUnknown)
	}
}

// A non-terminal signal carries no termination facts: only the end of a session
// writes them, so an ordinary idle/active hook cannot stamp a phantom ending.
func TestApplyActivitySignal_NonTerminalSignalRecordsNoTermination(t *testing.T) {
	m, st, _ := newManager()
	rec := working("mer-1")
	rec.FirstSignalAt = time.Now().Add(-time.Hour)
	st.sessions["mer-1"] = rec

	if err := m.ApplyActivitySignal(ctx, "mer-1", ports.ActivitySignal{Valid: true, State: domain.ActivityIdle}); err != nil {
		t.Fatal(err)
	}

	if got := st.sessions["mer-1"].Termination; !got.IsZero() {
		t.Errorf("termination = %+v, want zero for a live session", got)
	}
}

// When AO tears a session down, the record names the AO cause — so "was it the
// auto-reclaim loop?" is answerable from the row rather than by cross-reading
// four log files.
func TestMarkTerminated_RecordsTheAOCause(t *testing.T) {
	m, st, _ := newManager()
	rec := working("mer-1")
	rec.Activity = domain.Activity{State: domain.ActivityWaitingInput, LastActivityAt: time.Now()}
	st.sessions["mer-1"] = rec

	if err := m.MarkTerminated(ctx, "mer-1", domain.TerminationCauseAutoReclaim); err != nil {
		t.Fatal(err)
	}

	got := st.sessions["mer-1"].Termination
	if got.Source != domain.TerminationSourceAO {
		t.Errorf("source = %q, want %q", got.Source, domain.TerminationSourceAO)
	}
	if got.Reason != domain.TerminationCauseAutoReclaim {
		t.Errorf("reason = %q, want %q", got.Reason, domain.TerminationCauseAutoReclaim)
	}
	if got.LastState != domain.ActivityWaitingInput {
		t.Errorf("lastState = %q, want the state it was in before teardown", got.LastState)
	}
}

// A termination the REAPER infers from a vanished runtime is a third thing: AO
// did not ask for it and the agent never said goodbye. Recording it as either of
// the other two would misattribute the ending.
func TestApplyRuntimeObservation_RecordsAnInferredTermination(t *testing.T) {
	m, st, _ := newManager()
	rec := working("mer-1")
	rec.Activity = domain.Activity{State: domain.ActivityActive, LastActivityAt: time.Now().Add(-100 * time.Hour)}
	st.sessions["mer-1"] = rec

	if err := m.ApplyRuntimeObservation(ctx, "mer-1", ports.RuntimeFacts{Probe: ports.ProbeDead}); err != nil {
		t.Fatal(err)
	}

	got := st.sessions["mer-1"].Termination
	if got.Source != domain.TerminationSourceRuntimeGone {
		t.Errorf("source = %q, want %q", got.Source, domain.TerminationSourceRuntimeGone)
	}
	if got.LastState != domain.ActivityActive {
		t.Errorf("lastState = %q, want the state the session was last seen in", got.LastState)
	}
}

// The transcript is the only surviving account of what the agent was doing, so
// its path is snapshotted AT termination — the worktree it is derived from may
// be reclaimed later, which would make the answer underivable.
func TestTermination_SnapshotsTheTranscriptPointer(t *testing.T) {
	st := newFakeStore()
	m := New(st, &fakeMessenger{}, WithTranscriptLocator(func(rec domain.SessionRecord) string {
		return "/transcripts/" + string(rec.ID) + ".jsonl"
	}))
	rec := working("mer-1")
	rec.FirstSignalAt = time.Now().Add(-time.Hour)
	st.sessions["mer-1"] = rec

	if err := m.ApplyActivitySignal(ctx, "mer-1", ports.ActivitySignal{Valid: true, State: domain.ActivityExited}); err != nil {
		t.Fatal(err)
	}

	if got := st.sessions["mer-1"].Termination.TranscriptPath; got != "/transcripts/mer-1.jsonl" {
		t.Errorf("transcriptPath = %q, want the located transcript", got)
	}
}

// A restored session is live again, so it must not keep wearing the account of
// how it ended last time — that record would read as current and mislead.
func TestMarkSpawned_ClearsAPreviousTermination(t *testing.T) {
	m, st, _ := newManager()
	rec := working("mer-1")
	rec.IsTerminated = true
	rec.Termination = domain.Termination{
		Source: domain.TerminationSourceAgent, Reason: "other",
		LastState: domain.ActivityActive, At: time.Now().Add(-time.Hour),
	}
	st.sessions["mer-1"] = rec

	if err := m.MarkSpawned(ctx, "mer-1", domain.SessionMetadata{}); err != nil {
		t.Fatal(err)
	}

	if got := st.sessions["mer-1"].Termination; !got.IsZero() {
		t.Errorf("termination = %+v, want cleared on respawn", got)
	}
}
