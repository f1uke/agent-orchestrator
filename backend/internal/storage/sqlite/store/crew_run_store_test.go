package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func openRun(id string, rec domain.SessionRecord, at time.Time, attempt int) domain.CrewRun {
	return domain.CrewRun{
		ID: id, SessionID: rec.ID, ProjectID: rec.ProjectID,
		CrewID: rec.ID, Role: domain.CrewRoleQA,
		WorktreePath: "/tmp/worktree", Kind: domain.CrewRunTest, Label: "go test ./...",
		Attempt: attempt, Detector: domain.CrewRunDetectorLive, GenAtStart: 41,
		StartedAt: at, CreatedAt: at, UpdatedAt: at,
	}
}

func closeRun(run domain.CrewRun, at time.Time, outcome domain.CrewRunOutcome, result domain.CrewRunResult, paths []string) domain.CrewRun {
	ended := at
	run.EndedAt = &ended
	run.UpdatedAt = at
	run.Outcome = outcome
	run.Result = result
	run.ChangedPaths = paths
	run.GenAtEnd = run.GenAtStart
	if outcome == domain.CrewRunDiscarded {
		run.GenAtEnd = run.GenAtStart + uint64(len(paths)) + 1
	}
	return run
}

func TestCrewRunRoundTripsAndAnswersWhatIsRunningNow(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "crw")
	rec, err := s.CreateSession(ctx, sampleRecord("crw"))
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)

	// Nothing bracketed yet: the read model must say "not running", not "unknown".
	if _, ok, err := s.OpenCrewRunForSession(ctx, rec.ID); err != nil || ok {
		t.Fatalf("open run before any bracket: ok=%v err=%v", ok, err)
	}

	run := openRun("r1", rec, now, 1)
	if err := s.InsertCrewRun(ctx, run); err != nil {
		t.Fatalf("insert: %v", err)
	}
	got, ok, err := s.OpenCrewRunForSession(ctx, rec.ID)
	if err != nil || !ok {
		t.Fatalf("open run: ok=%v err=%v", ok, err)
	}
	if got.Kind != domain.CrewRunTest || got.Label != "go test ./..." || got.GenAtStart != 41 {
		t.Fatalf("open run round-tripped wrong: %+v", got)
	}
	if got.State() != domain.CrewRunStateRunning {
		t.Fatalf("state = %q, want running", got.State())
	}

	ended, ok, err := s.EndCrewRun(ctx, closeRun(run, now.Add(time.Minute), domain.CrewRunDiscarded, domain.CrewRunResultPass, []string{"a.go", "b.go"}))
	if err != nil || !ok {
		t.Fatalf("end: ok=%v err=%v", ok, err)
	}
	if ended.Outcome != domain.CrewRunDiscarded || len(ended.ChangedPaths) != 2 {
		t.Fatalf("ended run round-tripped wrong: %+v", ended)
	}
	// The pass it reported is kept as audit, and the run STILL reads discarded.
	if ended.Result != domain.CrewRunResultPass {
		t.Fatalf("the reported result was lost: %+v", ended)
	}
	if ended.State() != domain.CrewRunStateDiscarded {
		t.Fatalf("state = %q, want discarded", ended.State())
	}

	// A closed run is no longer "running now".
	if _, ok, err := s.OpenCrewRunForSession(ctx, rec.ID); err != nil || ok {
		t.Fatalf("open run after end: ok=%v err=%v", ok, err)
	}
	// And a second end is refused rather than rewriting a verdict already read.
	if _, ok, err := s.EndCrewRun(ctx, closeRun(run, now.Add(2*time.Minute), domain.CrewRunTrusted, domain.CrewRunResultPass, nil)); err != nil || ok {
		t.Fatalf("double end: ok=%v err=%v", ok, err)
	}
}

func TestConsecutiveCrewRunDiscardsCountsTheCurrentStreakOnly(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "crw")
	rec, err := s.CreateSession(ctx, sampleRecord("crw"))
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	base := time.Now().UTC().Truncate(time.Second)

	record := func(id string, minute int, outcome domain.CrewRunOutcome) {
		t.Helper()
		at := base.Add(time.Duration(minute) * time.Minute)
		run := openRun(id, rec, at, 1)
		if err := s.InsertCrewRun(ctx, run); err != nil {
			t.Fatalf("insert %s: %v", id, err)
		}
		if _, ok, err := s.EndCrewRun(ctx, closeRun(run, at.Add(30*time.Second), outcome, domain.CrewRunResultPass, []string{"x.go"})); err != nil || !ok {
			t.Fatalf("end %s: ok=%v err=%v", id, ok, err)
		}
	}

	record("r1", 1, domain.CrewRunDiscarded)
	record("r2", 2, domain.CrewRunDiscarded)
	if streak, err := s.ConsecutiveCrewRunDiscards(ctx, rec.ID); err != nil || streak != 2 {
		t.Fatalf("streak = %d err=%v, want 2", streak, err)
	}

	// One run that ends any other way clears it - which is what lets the card
	// come back from NEEDS YOU on its own.
	record("r3", 3, domain.CrewRunTrusted)
	if streak, err := s.ConsecutiveCrewRunDiscards(ctx, rec.ID); err != nil || streak != 0 {
		t.Fatalf("streak after a trusted run = %d err=%v, want 0", streak, err)
	}

	record("r4", 4, domain.CrewRunDiscarded)
	if streak, err := s.ConsecutiveCrewRunDiscards(ctx, rec.ID); err != nil || streak != 1 {
		t.Fatalf("streak = %d err=%v, want 1", streak, err)
	}

	// An UNCERTIFIED run is skipped, not treated as a clear. Nothing watched the
	// tree, so it is no evidence this member got a quiet window - and if it broke
	// the streak, a daemon restart would silently cancel the escalation.
	record("r5", 5, domain.CrewRunUncertified)
	if streak, err := s.ConsecutiveCrewRunDiscards(ctx, rec.ID); err != nil || streak != 1 {
		t.Fatalf("streak after an uncertified run = %d err=%v, want the discard streak kept at 1", streak, err)
	}
}

func TestAbandonOpenCrewRunsClosesThemAsUncertified(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "crw")
	rec, err := s.CreateSession(ctx, sampleRecord("crw"))
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	if err := s.InsertCrewRun(ctx, openRun("r1", rec, now, 1)); err != nil {
		t.Fatalf("insert: %v", err)
	}

	closed, err := s.AbandonOpenCrewRuns(ctx, now.Add(time.Minute))
	if err != nil || closed != 1 {
		t.Fatalf("abandon: closed=%d err=%v", closed, err)
	}
	got, ok, err := s.GetCrewRun(ctx, "r1")
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	// A run whose watcher died with the daemon can only be uncertified. Trusting
	// it would certify a tree nobody watched.
	if got.Outcome != domain.CrewRunUncertified || got.Open() {
		t.Fatalf("abandoned run = %+v, want a closed uncertified run", got)
	}
	if got.Detector != domain.CrewRunDetectorDown || got.DetectorReason == "" {
		t.Fatalf("abandoned run does not say why it cannot be certified: %+v", got)
	}
}

// A session that never brackets a run reads exactly as it always did: no open
// run, no streak, no history.
func TestCrewRunReadsAreEmptyForASessionThatNeverBrackets(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "crw")
	rec, err := s.CreateSession(ctx, sampleRecord("crw"))
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if _, ok, err := s.OpenCrewRunForSession(ctx, rec.ID); err != nil || ok {
		t.Fatalf("open run: ok=%v err=%v", ok, err)
	}
	if streak, err := s.ConsecutiveCrewRunDiscards(ctx, rec.ID); err != nil || streak != 0 {
		t.Fatalf("streak = %d err=%v", streak, err)
	}
	runs, err := s.ListCrewRunsForSession(ctx, rec.ID, 10)
	if err != nil || len(runs) != 0 {
		t.Fatalf("runs = %v err=%v", runs, err)
	}
}
