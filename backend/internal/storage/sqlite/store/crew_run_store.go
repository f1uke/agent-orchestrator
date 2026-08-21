package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// crewRunHistoryDepth bounds how far back the discard streak is counted. The
// streak that matters is at the head of the list and is capped at
// domain.CappedRepeat, so a handful of rows is always enough.
const crewRunHistoryDepth = 20

// InsertCrewRun records a run that has just started. gen is the worktree's write
// generation at that instant; detector says whether anything was actually
// watching, which is stored rather than derived because it is a fact about a
// moment that has passed.
func (s *Store) InsertCrewRun(ctx context.Context, run domain.CrewRun) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	err := s.qr.InsertCrewRun(ctx, gen.InsertCrewRunParams{
		ID:             run.ID,
		SessionID:      run.SessionID,
		ProjectID:      run.ProjectID,
		CrewID:         run.CrewID,
		Role:           run.Role,
		WorktreePath:   run.WorktreePath,
		Kind:           run.Kind,
		Label:          run.Label,
		Attempt:        int64(run.Attempt),
		Detector:       run.Detector,
		DetectorReason: run.DetectorReason,
		GenAtStart:     int64(run.GenAtStart), //nolint:gosec // a write counter cannot realistically exceed int64
		StartedAt:      run.StartedAt,
		CreatedAt:      run.CreatedAt,
		UpdatedAt:      run.UpdatedAt,
	})
	if err != nil {
		return fmt.Errorf("insert crew run %s: %w", run.ID, err)
	}
	return nil
}

// EndCrewRun closes a run that is still open and returns the stored row. It
// reports ok=false when the id is unknown or the run has already ended, so a
// double `--end` cannot rewrite a verdict that has been read.
func (s *Store) EndCrewRun(ctx context.Context, run domain.CrewRun) (domain.CrewRun, bool, error) {
	paths, err := json.Marshal(run.ChangedPaths)
	if err != nil {
		return domain.CrewRun{}, false, fmt.Errorf("encode changed paths for crew run %s: %w", run.ID, err)
	}
	var ended sql.NullTime
	if run.EndedAt != nil {
		ended = sql.NullTime{Time: *run.EndedAt, Valid: true}
	}
	s.writeMu.Lock()
	rows, err := s.qr.EndCrewRun(ctx, gen.EndCrewRunParams{
		GenAtEnd:       int64(run.GenAtEnd), //nolint:gosec // a write counter cannot realistically exceed int64
		EndedAt:        ended,
		Outcome:        run.Outcome,
		Result:         run.Result,
		ChangedPaths:   string(paths),
		HeadSha:        run.HeadSHA,
		Detector:       run.Detector,
		DetectorReason: run.DetectorReason,
		UpdatedAt:      run.UpdatedAt,
		ID:             run.ID,
	})
	s.writeMu.Unlock()
	if err != nil {
		return domain.CrewRun{}, false, fmt.Errorf("end crew run %s: %w", run.ID, err)
	}
	if rows == 0 {
		return domain.CrewRun{}, false, nil
	}
	return s.GetCrewRun(ctx, run.ID)
}

// GetCrewRun reads one run, ok=false if absent.
func (s *Store) GetCrewRun(ctx context.Context, id string) (domain.CrewRun, bool, error) {
	row, err := s.qr.GetCrewRun(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.CrewRun{}, false, nil
	}
	if err != nil {
		return domain.CrewRun{}, false, fmt.Errorf("get crew run %s: %w", id, err)
	}
	run, err := crewRunFromRow(row)
	return run, err == nil, err
}

// ListCrewRunsForSession returns a session's runs, newest first, capped at limit.
func (s *Store) ListCrewRunsForSession(ctx context.Context, id domain.SessionID, limit int) ([]domain.CrewRun, error) {
	if limit <= 0 {
		limit = crewRunHistoryDepth
	}
	rows, err := s.qr.ListCrewRunsBySession(ctx, gen.ListCrewRunsBySessionParams{SessionID: id, Limit: int64(limit)})
	if err != nil {
		return nil, fmt.Errorf("list crew runs for session %s: %w", id, err)
	}
	runs := make([]domain.CrewRun, 0, len(rows))
	for _, row := range rows {
		run, err := crewRunFromRow(row)
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	return runs, nil
}

// OpenCrewRunForSession returns the session's still-open run, if any. An open
// row IS the "this member is running a build right now" signal.
func (s *Store) OpenCrewRunForSession(ctx context.Context, id domain.SessionID) (domain.CrewRun, bool, error) {
	row, err := s.qr.GetOpenCrewRunForSession(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.CrewRun{}, false, nil
	}
	if err != nil {
		return domain.CrewRun{}, false, fmt.Errorf("open crew run for session %s: %w", id, err)
	}
	run, err := crewRunFromRow(row)
	return run, err == nil, err
}

// OpenCrewRunsForCrewmates returns the runs the OTHER member of this session's
// crew has open right now. It is the advisory half of the bracket: a member
// about to start a build can see that its crewmate is already running one in the
// same worktree, which matters most where it is least verified - two concurrent
// `xcodebuild` runs against one shared DerivedData.
//
// Advisory means advisory: nothing here waits, queues or refuses. Whether an
// overlapping run's result survives is still the detector's call.
func (s *Store) OpenCrewRunsForCrewmates(ctx context.Context, crewID, self domain.SessionID) ([]domain.CrewRun, error) {
	if crewID == "" {
		return nil, nil
	}
	rows, err := s.qr.ListOpenCrewRunsForCrew(ctx, gen.ListOpenCrewRunsForCrewParams{CrewID: crewID, SessionID: self})
	if err != nil {
		return nil, fmt.Errorf("open crew runs for crew %s: %w", crewID, err)
	}
	runs := make([]domain.CrewRun, 0, len(rows))
	for _, row := range rows {
		run, err := crewRunFromRow(row)
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	return runs, nil
}

// ConsecutiveCrewRunDiscards counts the discarded runs at the HEAD of a
// session's finished history - the current streak, not a lifetime total.
//
// It is derived on every read and never stored: the escalation it feeds is a
// display status, and it has to end by itself the moment the situation does.
//
// Only a TRUSTED run ends it. An UNCERTIFIED one is skipped rather than counted
// or treated as a clear, because it is not evidence either way: nothing watched
// the tree, so it says nothing about whether this member can get a quiet window.
// Letting it clear the streak would mean a daemon restart quietly cancelled the
// escalation, which is the same laundering in a different coat.
func (s *Store) ConsecutiveCrewRunDiscards(ctx context.Context, id domain.SessionID) (int, error) {
	outcomes, err := s.qr.ListEndedCrewRunOutcomes(ctx, gen.ListEndedCrewRunOutcomesParams{
		SessionID: id,
		Limit:     crewRunHistoryDepth,
	})
	if err != nil {
		return 0, fmt.Errorf("crew run discard streak for session %s: %w", id, err)
	}
	streak := 0
	for _, outcome := range outcomes {
		switch outcome {
		case domain.CrewRunDiscarded:
			streak++
		case domain.CrewRunUncertified:
			continue
		default:
			return streak, nil
		}
	}
	return streak, nil
}

// AbandonOpenCrewRuns closes every run left open by a daemon that went away
// mid-bracket. Their watchers died with the process, so they end UNCERTIFIED:
// there is no reading that could make them trusted, and leaving them open would
// have the board claim a build is running that nothing is running.
func (s *Store) AbandonOpenCrewRuns(ctx context.Context, now time.Time) (int, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	rows, err := s.qr.AbandonOpenCrewRuns(ctx, gen.AbandonOpenCrewRunsParams{
		EndedAt:   sql.NullTime{Time: now, Valid: true},
		UpdatedAt: now,
	})
	if err != nil {
		return 0, fmt.Errorf("abandon open crew runs: %w", err)
	}
	return int(rows), nil
}

func crewRunFromRow(row gen.CrewRun) (domain.CrewRun, error) {
	run := domain.CrewRun{
		ID:             row.ID,
		SessionID:      row.SessionID,
		ProjectID:      row.ProjectID,
		CrewID:         row.CrewID,
		Role:           row.Role,
		WorktreePath:   row.WorktreePath,
		Kind:           row.Kind,
		Label:          row.Label,
		Attempt:        int(row.Attempt),
		Detector:       row.Detector,
		DetectorReason: row.DetectorReason,
		GenAtStart:     uint64(row.GenAtStart), //nolint:gosec // written from a uint64 counter
		GenAtEnd:       uint64(row.GenAtEnd),   //nolint:gosec // written from a uint64 counter
		StartedAt:      row.StartedAt,
		Outcome:        row.Outcome,
		Result:         row.Result,
		HeadSHA:        row.HeadSha,
		CreatedAt:      row.CreatedAt,
		UpdatedAt:      row.UpdatedAt,
	}
	if row.EndedAt.Valid {
		ended := row.EndedAt.Time
		run.EndedAt = &ended
	}
	if row.ChangedPaths != "" {
		if err := json.Unmarshal([]byte(row.ChangedPaths), &run.ChangedPaths); err != nil {
			return domain.CrewRun{}, fmt.Errorf("decode changed paths for crew run %s: %w", row.ID, err)
		}
	}
	return run, nil
}
