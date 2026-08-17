package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// newRecordingStore is a store with a project to hang sessions off.
func newRecordingStore(t *testing.T) *sqlite.Store {
	t.Helper()
	s := newTestStore(t)
	seedProject(t, s, "mer")
	return s
}

func simRecording(udid string, owner domain.SessionID, name string, at time.Time) domain.SimRecording {
	return domain.SimRecording{
		UDID:      udid,
		SessionID: owner,
		Name:      name,
		StartedAt: at,
		UpdatedAt: at,
	}
}

func simRecordingStep(seq int64, at time.Time, kind string) domain.SimRecordingStep {
	return domain.SimRecordingStep{
		Seq:  seq,
		At:   at,
		Kind: kind,
	}
}

// A lease says who may drive the device. A recording is what turns whatever
// that session does into a Maestro flow afterwards. These tests are about
// starting, appending to, stopping and listing that recording.

func TestStartSimRecording_RequiresALiveLeaseHeldByTheCaller(t *testing.T) {
	ctx := context.Background()
	s := newRecordingStore(t)
	owner := seedSession(t, s, "mer")
	now := time.Now().UTC().Truncate(time.Second)

	// No lease at all.
	out, err := s.StartSimRecording(ctx, simRecording(testUDID, owner, "flow", now), now)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if out.Granted {
		t.Fatal("starting a recording without a live lease must be refused")
	}
	if out.Leased {
		t.Fatalf("there is no lease to report, got %+v", out)
	}

	// A lease held by someone else must not let this caller start a recording.
	other := seedSession(t, s, "mer")
	if _, _, err := s.AcquireSimLease(ctx, domain.SimLease{UDID: testUDID, SessionID: other, AcquiredAt: now, ExpiresAt: now.Add(10 * time.Minute)}); err != nil {
		t.Fatalf("acquire lease: %v", err)
	}
	out, err = s.StartSimRecording(ctx, simRecording(testUDID, owner, "flow", now), now)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if out.Granted {
		t.Fatal("starting a recording with someone else's lease must be refused")
	}
	if !out.Leased || out.Lease.SessionID != other {
		t.Fatalf("the refusal must name the real lease holder, got %+v", out)
	}
}

func TestStartSimRecording_SecondRecordingOnTheSameDeviceIsRefused(t *testing.T) {
	ctx := context.Background()
	s := newRecordingStore(t)
	owner := seedSession(t, s, "mer")
	now := time.Now().UTC().Truncate(time.Second)
	if _, _, err := s.AcquireSimLease(ctx, domain.SimLease{UDID: testUDID, SessionID: owner, AcquiredAt: now, ExpiresAt: now.Add(10 * time.Minute)}); err != nil {
		t.Fatalf("acquire lease: %v", err)
	}

	out, err := s.StartSimRecording(ctx, simRecording(testUDID, owner, "first", now), now)
	if err != nil {
		t.Fatalf("start first: %v", err)
	}
	if !out.Granted {
		t.Fatalf("first recording on a leased device must be granted, got %+v", out)
	}

	out, err = s.StartSimRecording(ctx, simRecording(testUDID, owner, "second", now), now)
	if err != nil {
		t.Fatalf("start second: %v", err)
	}
	if out.Granted {
		t.Fatal("a second recording on a device already recording must be refused")
	}
	if !out.Busy {
		t.Fatalf("the refusal must say a recording is already open, got %+v", out)
	}
}

func TestStartSimRecording_AfterStoppingTheFirstIsAllowed(t *testing.T) {
	ctx := context.Background()
	s := newRecordingStore(t)
	owner := seedSession(t, s, "mer")
	now := time.Now().UTC().Truncate(time.Second)
	if _, _, err := s.AcquireSimLease(ctx, domain.SimLease{UDID: testUDID, SessionID: owner, AcquiredAt: now, ExpiresAt: now.Add(10 * time.Minute)}); err != nil {
		t.Fatalf("acquire lease: %v", err)
	}
	if out, err := s.StartSimRecording(ctx, simRecording(testUDID, owner, "first", now), now); err != nil || !out.Granted {
		t.Fatalf("start first: out=%+v err=%v", out, err)
	}

	// The first recording captures something. Without this the test passed
	// while the second recording inherited whatever the first had captured:
	// the restart reuses this device's one recording row, so its steps outlive
	// it unless the restart clears them.
	for _, kind := range []string{"tap", "swipe"} {
		if _, ok, err := s.AppendSimRecordingStep(ctx, testUDID, simRecordingStep(0, now, kind)); err != nil || !ok {
			t.Fatalf("append %s to the first recording: ok=%v err=%v", kind, ok, err)
		}
	}

	stoppedAt := now.Add(time.Minute)
	if ok, err := s.StopSimRecording(ctx, testUDID, owner, stoppedAt); err != nil || !ok {
		t.Fatalf("stop first: ok=%v err=%v", ok, err)
	}

	out, err := s.StartSimRecording(ctx, simRecording(testUDID, owner, "second", stoppedAt), stoppedAt)
	if err != nil {
		t.Fatalf("start second: %v", err)
	}
	if !out.Granted {
		t.Fatalf("starting again after stopping must be allowed, got %+v", out)
	}

	rec, ok, err := s.GetSimRecording(ctx, testUDID)
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if rec.Name != "second" || rec.StoppedAt != nil {
		t.Fatalf("recording after restart = %+v, want the new open recording", rec)
	}

	// The whole point: a restarted recording starts empty. Inheriting the
	// previous one's steps would put gestures nobody performed during this
	// recording into the flow emitted from it - and re-recording is the
	// sanctioned repair path for a flow that came out wrong, so this is the
	// common case rather than an edge one.
	steps, err := s.ListSimRecordingSteps(ctx, testUDID)
	if err != nil {
		t.Fatalf("list steps: %v", err)
	}
	if len(steps) != 0 {
		t.Fatalf("steps after restarting = %+v, want none of the first recording's", steps)
	}

	// And the new recording numbers its own steps from one, rather than
	// continuing the dead recording's sequence.
	seq, ok, err := s.AppendSimRecordingStep(ctx, testUDID, simRecordingStep(0, stoppedAt, "tap"))
	if err != nil || !ok {
		t.Fatalf("append to the restarted recording: ok=%v err=%v", ok, err)
	}
	if seq != 1 {
		t.Fatalf("first step of the restarted recording = seq %d, want 1", seq)
	}
}

func TestAppendSimRecordingStep_NumbersStepsFromOne(t *testing.T) {
	ctx := context.Background()
	s := newRecordingStore(t)
	owner := seedSession(t, s, "mer")
	now := time.Now().UTC().Truncate(time.Second)
	if _, _, err := s.AcquireSimLease(ctx, domain.SimLease{UDID: testUDID, SessionID: owner, AcquiredAt: now, ExpiresAt: now.Add(10 * time.Minute)}); err != nil {
		t.Fatalf("acquire lease: %v", err)
	}
	if out, err := s.StartSimRecording(ctx, simRecording(testUDID, owner, "flow", now), now); err != nil || !out.Granted {
		t.Fatalf("start: out=%+v err=%v", out, err)
	}

	for i, kind := range []string{"tap", "swipe", "type"} {
		seq, ok, err := s.AppendSimRecordingStep(ctx, testUDID, domain.SimRecordingStep{
			At:   now.Add(time.Duration(i) * time.Second),
			Kind: kind,
		})
		if err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
		if !ok {
			t.Fatalf("append %d: refused, want granted", i)
		}
		if seq != int64(i+1) {
			t.Fatalf("append %d: seq = %d, want %d", i, seq, i+1)
		}
	}

	steps, err := s.ListSimRecordingSteps(ctx, testUDID)
	if err != nil {
		t.Fatalf("list steps: %v", err)
	}
	if len(steps) != 3 {
		t.Fatalf("steps = %d, want 3", len(steps))
	}
}

func TestAppendSimRecordingStep_RefusedWhenNoRecordingIsOpen(t *testing.T) {
	ctx := context.Background()
	s := newRecordingStore(t)
	now := time.Now().UTC().Truncate(time.Second)

	// No recording ever existed on this device.
	seq, ok, err := s.AppendSimRecordingStep(ctx, testUDID, simRecordingStep(0, now, "tap"))
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	if ok {
		t.Fatalf("append with no recording must be refused, got seq %d", seq)
	}

	owner := seedSession(t, s, "mer")
	if _, _, err := s.AcquireSimLease(ctx, domain.SimLease{UDID: testUDID, SessionID: owner, AcquiredAt: now, ExpiresAt: now.Add(10 * time.Minute)}); err != nil {
		t.Fatalf("acquire lease: %v", err)
	}
	if out, err := s.StartSimRecording(ctx, simRecording(testUDID, owner, "flow", now), now); err != nil || !out.Granted {
		t.Fatalf("start: out=%+v err=%v", out, err)
	}
	stoppedAt := now.Add(time.Minute)
	if ok, err := s.StopSimRecording(ctx, testUDID, owner, stoppedAt); err != nil || !ok {
		t.Fatalf("stop: ok=%v err=%v", ok, err)
	}

	// A stopped recording is not open either.
	seq, ok, err = s.AppendSimRecordingStep(ctx, testUDID, simRecordingStep(0, stoppedAt, "tap"))
	if err != nil {
		t.Fatalf("append after stop: %v", err)
	}
	if ok {
		t.Fatalf("append to a stopped recording must be refused, got seq %d", seq)
	}
}

func TestStopSimRecording_KeepsTheRowsAndTheSteps(t *testing.T) {
	ctx := context.Background()
	s := newRecordingStore(t)
	owner := seedSession(t, s, "mer")
	now := time.Now().UTC().Truncate(time.Second)
	if _, _, err := s.AcquireSimLease(ctx, domain.SimLease{UDID: testUDID, SessionID: owner, AcquiredAt: now, ExpiresAt: now.Add(10 * time.Minute)}); err != nil {
		t.Fatalf("acquire lease: %v", err)
	}
	if out, err := s.StartSimRecording(ctx, simRecording(testUDID, owner, "flow", now), now); err != nil || !out.Granted {
		t.Fatalf("start: out=%+v err=%v", out, err)
	}
	if _, ok, err := s.AppendSimRecordingStep(ctx, testUDID, domain.SimRecordingStep{At: now, Kind: "tap"}); err != nil || !ok {
		t.Fatalf("append: ok=%v err=%v", ok, err)
	}

	// A non-holder cannot stop someone else's recording.
	other := seedSession(t, s, "mer")
	stoppedAt := now.Add(time.Minute)
	if ok, err := s.StopSimRecording(ctx, testUDID, other, stoppedAt); err != nil {
		t.Fatalf("stop by non-holder: %v", err)
	} else if ok {
		t.Fatal("a non-holder must not be able to stop someone else's recording")
	}

	if ok, err := s.StopSimRecording(ctx, testUDID, owner, stoppedAt); err != nil || !ok {
		t.Fatalf("stop: ok=%v err=%v", ok, err)
	}

	// Stopping twice is a no-op the caller can see.
	if ok, err := s.StopSimRecording(ctx, testUDID, owner, stoppedAt.Add(time.Minute)); err != nil {
		t.Fatalf("second stop: %v", err)
	} else if ok {
		t.Fatal("stopping an already-stopped recording must be a no-op")
	}

	rec, ok, err := s.GetSimRecording(ctx, testUDID)
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if rec.StoppedAt == nil || !rec.StoppedAt.Equal(stoppedAt) {
		t.Fatalf("stoppedAt = %v, want %v", rec.StoppedAt, stoppedAt)
	}

	steps, err := s.ListSimRecordingSteps(ctx, testUDID)
	if err != nil {
		t.Fatalf("list steps: %v", err)
	}
	if len(steps) != 1 {
		t.Fatalf("steps after stop = %d, want the row to survive: %+v", len(steps), steps)
	}
}

func TestListSimRecordingSteps_ReturnsThemInSequenceOrder(t *testing.T) {
	ctx := context.Background()
	s := newRecordingStore(t)
	owner := seedSession(t, s, "mer")
	now := time.Now().UTC().Truncate(time.Second)
	if _, _, err := s.AcquireSimLease(ctx, domain.SimLease{UDID: testUDID, SessionID: owner, AcquiredAt: now, ExpiresAt: now.Add(10 * time.Minute)}); err != nil {
		t.Fatalf("acquire lease: %v", err)
	}
	if out, err := s.StartSimRecording(ctx, simRecording(testUDID, owner, "flow", now), now); err != nil || !out.Granted {
		t.Fatalf("start: out=%+v err=%v", out, err)
	}

	kinds := []string{"tap", "swipe", "type", "wait"}
	for i, kind := range kinds {
		if _, ok, err := s.AppendSimRecordingStep(ctx, testUDID, domain.SimRecordingStep{
			At:   now.Add(time.Duration(i) * time.Second),
			Kind: kind,
		}); err != nil || !ok {
			t.Fatalf("append %d: ok=%v err=%v", i, ok, err)
		}
	}

	steps, err := s.ListSimRecordingSteps(ctx, testUDID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(steps) != len(kinds) {
		t.Fatalf("steps = %d, want %d", len(steps), len(kinds))
	}
	for i, step := range steps {
		if step.Seq != int64(i+1) {
			t.Fatalf("step %d has seq %d, want %d", i, step.Seq, i+1)
		}
		if step.Kind != kinds[i] {
			t.Fatalf("step %d kind = %q, want %q", i, step.Kind, kinds[i])
		}
	}
}

// The one that matters most: two independent *Store values racing to start a
// recording on the same device must produce exactly one winner. This is the
// same exclusion problem TestAcquireSimLease_ConcurrentAcquiresAcrossStoresHaveExactlyOneWinner
// and TestAcquireSimHold_ConcurrentHoldsInOneSessionHaveExactlyOneWinner
// caught in the lease and hold slices - separate *Store values over one
// database file share no Go mutex, so only the SQL predicate can decide the
// winner. Written as a SELECT followed by an INSERT this would reproduce
// exactly that race.
func TestStartSimRecording_RaceAcrossStoresProducesExactlyOneWinner(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	const racers = 4
	pool := make([]*sqlite.Store, racers)
	for i := range pool {
		s, err := sqlite.Open(dir)
		if err != nil {
			t.Fatalf("open store %d: %v", i, err)
		}
		t.Cleanup(func() { _ = s.Close() })
		pool[i] = s
	}
	seedProject(t, pool[0], "mer")
	owner := seedSession(t, pool[0], "mer")
	now := time.Now().UTC().Truncate(time.Second)
	if _, _, err := pool[0].AcquireSimLease(ctx, domain.SimLease{UDID: testUDID, SessionID: owner, AcquiredAt: now, ExpiresAt: now.Add(10 * time.Minute)}); err != nil {
		t.Fatalf("acquire lease: %v", err)
	}

	outcomes := make([]domain.SimRecordingOutcome, racers)
	errs := make([]error, racers)
	runConcurrently(racers, func(i int) {
		name := "flow-" + string(rune('a'+i))
		outcomes[i], errs[i] = pool[i].StartSimRecording(ctx, simRecording(testUDID, owner, name, now), now)
	})
	for i, err := range errs {
		if err != nil {
			t.Fatalf("racer %d: %v", i, err)
		}
	}

	winners := 0
	for _, out := range outcomes {
		if out.Granted {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("concurrent starts on one device produced %d winners, want exactly 1", winners)
	}
	for i, out := range outcomes {
		if !out.Granted && !out.Busy {
			t.Fatalf("racer %d lost but was not told a recording is already open: %+v", i, out)
		}
	}
}

// A recording that outlives its owning session blocks the device for ever:
// udid is the primary key and StartSimRecording refuses over a live
// recording, so the next session on that simulator can never start one. Ending
// the session must close it - enforced by the schema (a trigger on
// sessions.is_terminated, 0040), not by any caller remembering to do it.
//
// Unlike the lease, which vanishes with its owner, the rows are KEPT: a lease
// is permission and must not outlive the session, a recording is evidence and
// should, because the flow is emitted from it after the fact (spec 10).
func TestSimRecording_StoppedWhenOwningSessionTerminates(t *testing.T) {
	ctx := context.Background()
	s := newRecordingStore(t)
	owner := seedSession(t, s, "mer")
	next := seedSession(t, s, "mer")
	now := time.Now().UTC().Truncate(time.Second)
	if _, _, err := s.AcquireSimLease(ctx, domain.SimLease{UDID: testUDID, SessionID: owner, AcquiredAt: now, ExpiresAt: now.Add(10 * time.Minute)}); err != nil {
		t.Fatalf("acquire lease: %v", err)
	}
	if out, err := s.StartSimRecording(ctx, simRecording(testUDID, owner, "first", now), now); err != nil || !out.Granted {
		t.Fatalf("start: out=%+v err=%v", out, err)
	}
	if _, ok, err := s.AppendSimRecordingStep(ctx, testUDID, simRecordingStep(0, now, "tap")); err != nil || !ok {
		t.Fatalf("append: ok=%v err=%v", ok, err)
	}

	rec, ok, err := s.GetSession(ctx, owner)
	if err != nil || !ok {
		t.Fatalf("get session: ok=%v err=%v", ok, err)
	}
	endedAt := now.Add(time.Minute)
	rec.IsTerminated = true
	rec.UpdatedAt = endedAt
	if err := s.UpdateSession(ctx, rec); err != nil {
		t.Fatalf("terminate session: %v", err)
	}

	stored, ok, err := s.GetSimRecording(ctx, testUDID)
	if err != nil || !ok {
		t.Fatalf("the recording's rows must be kept when its session ends: ok=%v err=%v", ok, err)
	}
	if stored.StoppedAt == nil {
		t.Fatalf("ending the owning session must close its recording, still open: %+v", stored)
	}
	if !stored.StoppedAt.Equal(endedAt) {
		t.Fatalf("stoppedAt = %v, want the moment the session ended (%v)", stored.StoppedAt, endedAt)
	}
	if steps, err := s.ListSimRecordingSteps(ctx, testUDID); err != nil || len(steps) != 1 {
		t.Fatalf("the captured steps must survive the session ending, got %+v (err=%v)", steps, err)
	}

	// And the device is genuinely usable again, not merely reported as closed:
	// this is the failure the trigger exists to prevent.
	if _, _, err := s.AcquireSimLease(ctx, domain.SimLease{UDID: testUDID, SessionID: next, AcquiredAt: endedAt, ExpiresAt: endedAt.Add(10 * time.Minute)}); err != nil {
		t.Fatalf("acquire lease for the next session: %v", err)
	}
	out, err := s.StartSimRecording(ctx, simRecording(testUDID, next, "second", endedAt), endedAt)
	if err != nil {
		t.Fatalf("start second: %v", err)
	}
	if !out.Granted {
		t.Fatalf("the next session must be able to record this device, got %+v", out)
	}
}

// A recording only closes when the session it belongs to ends. Another
// session ending on the same machine must not touch it.
func TestSimRecording_AnotherSessionEndingLeavesItOpen(t *testing.T) {
	ctx := context.Background()
	s := newRecordingStore(t)
	owner := seedSession(t, s, "mer")
	bystander := seedSession(t, s, "mer")
	now := time.Now().UTC().Truncate(time.Second)
	if _, _, err := s.AcquireSimLease(ctx, domain.SimLease{UDID: testUDID, SessionID: owner, AcquiredAt: now, ExpiresAt: now.Add(10 * time.Minute)}); err != nil {
		t.Fatalf("acquire lease: %v", err)
	}
	if out, err := s.StartSimRecording(ctx, simRecording(testUDID, owner, "first", now), now); err != nil || !out.Granted {
		t.Fatalf("start: out=%+v err=%v", out, err)
	}

	rec, _, err := s.GetSession(ctx, bystander)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	rec.IsTerminated = true
	if err := s.UpdateSession(ctx, rec); err != nil {
		t.Fatalf("terminate bystander: %v", err)
	}

	stored, ok, err := s.GetSimRecording(ctx, testUDID)
	if err != nil || !ok {
		t.Fatalf("get recording: ok=%v err=%v", ok, err)
	}
	if stored.StoppedAt != nil {
		t.Fatalf("somebody else's session ending closed this recording: %+v", stored)
	}
}
