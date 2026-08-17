package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// StartSimRecording opens a recording of the gestures a session performs on
// one device, or reports why it could not.
//
// Like AcquireSimLease and AcquireSimHold, the exclusion is the database's:
// StartSimRecording is a single conditional upsert whose predicate carries
// every rule (the caller's lease is live, and no recording is already open on
// this device), so simultaneous callers resolve to exactly one winner without
// this function holding a lock. The transaction exists only so a loser can
// read WHY it lost - no lease, someone else's lease, or a recording already
// open - because those need different advice.
func (s *Store) StartSimRecording(ctx context.Context, rec domain.SimRecording, now time.Time) (domain.SimRecordingOutcome, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var outcome domain.SimRecordingOutcome
	err := s.inTx(ctx, "start sim recording", func(q *gen.Queries) error {
		rows, err := q.StartSimRecording(ctx, gen.StartSimRecordingParams{
			Udid:      rec.UDID,
			SessionID: string(rec.SessionID),
			Name:      rec.Name,
			StartedAt: rec.StartedAt,
			Now:       now,
		})
		if err != nil {
			return err
		}
		if rows > 0 {
			outcome = domain.SimRecordingOutcome{
				Granted:   true,
				Recording: rec,
				Lease:     domain.SimLease{UDID: rec.UDID, SessionID: rec.SessionID},
				Leased:    true,
			}
			return nil
		}
		leaseRow, err := q.GetSimLease(ctx, rec.UDID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil {
			if lease := simLeaseFromRow(leaseRow); lease.Live(now) {
				outcome.Lease, outcome.Leased = lease, true
			}
		}
		recRow, err := q.GetSimRecording(ctx, rec.UDID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil && !recRow.StoppedAt.Valid {
			outcome.Busy = true
		}
		return nil
	})
	if err != nil {
		return domain.SimRecordingOutcome{}, fmt.Errorf("start sim recording on %s for %s: %w", rec.UDID, rec.SessionID, err)
	}
	return outcome, nil
}

// StopSimRecording closes the caller's open recording on a device while
// keeping its row and every step it already captured - a flow is emitted from
// them after the fact. ok=false means this caller did not hold an open
// recording on this device (never started, already stopped, or someone
// else's) and nothing changed.
func (s *Store) StopSimRecording(ctx context.Context, udid string, sessionID domain.SessionID, now time.Time) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	rows, err := s.qw.StopSimRecording(ctx, gen.StopSimRecordingParams{
		Now:       sql.NullTime{Time: now, Valid: true},
		Udid:      udid,
		SessionID: string(sessionID),
	})
	if err != nil {
		return false, fmt.Errorf("stop sim recording on %s for %s: %w", udid, sessionID, err)
	}
	return rows > 0, nil
}

// GetSimRecording returns the recording row for a device, open or stopped.
// ok=false means no recording has ever been started on it.
func (s *Store) GetSimRecording(ctx context.Context, udid string) (domain.SimRecording, bool, error) {
	row, err := s.qr.GetSimRecording(ctx, udid)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.SimRecording{}, false, nil
	}
	if err != nil {
		return domain.SimRecording{}, false, fmt.Errorf("get sim recording on %s: %w", udid, err)
	}
	return simRecordingFromRow(row), true, nil
}

// AppendSimRecordingStep appends one captured step to the open recording on a
// device, numbered from 1 in the order steps were appended. The numbering and
// the openness check both live in one statement (queries/sim_recording.sql),
// so a step cannot land after a concurrent StopSimRecording closed the
// recording out from under it. ok=false means no recording is open on this
// device and nothing was appended.
func (s *Store) AppendSimRecordingStep(ctx context.Context, udid string, step domain.SimRecordingStep) (int64, bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	row, err := s.qw.AppendSimRecordingStep(ctx, gen.AppendSimRecordingStepParams{
		Udid:         udid,
		At:           step.At,
		Kind:         step.Kind,
		Selector:     step.Selector,
		SelectorRung: step.SelectorRung,
		Ambiguity:    step.Ambiguity,
		OffScreen:    boolInt(step.OffScreen),
		ScreenChange: boolInt(step.ScreenChange),
		X:            step.X,
		Y:            step.Y,
		ToX:          step.ToX,
		ToY:          step.ToY,
		DurationMs:   step.DurationMS,
		Text:         step.Text,
		Detail:       step.Detail,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("append sim recording step on %s: %w", udid, err)
	}
	return row.Seq, true, nil
}

// ListSimRecordingSteps returns every step captured for a device's recording,
// oldest first.
func (s *Store) ListSimRecordingSteps(ctx context.Context, udid string) ([]domain.SimRecordingStep, error) {
	rows, err := s.qr.ListSimRecordingSteps(ctx, udid)
	if err != nil {
		return nil, fmt.Errorf("list sim recording steps on %s: %w", udid, err)
	}
	steps := make([]domain.SimRecordingStep, 0, len(rows))
	for _, row := range rows {
		steps = append(steps, simRecordingStepFromRow(row))
	}
	return steps, nil
}

func simRecordingFromRow(row gen.SimRecording) domain.SimRecording {
	rec := domain.SimRecording{
		UDID:      row.Udid,
		SessionID: domain.SessionID(row.SessionID),
		Name:      row.Name,
		StartedAt: row.StartedAt.UTC(),
		UpdatedAt: row.UpdatedAt.UTC(),
	}
	if row.StoppedAt.Valid {
		stoppedAt := row.StoppedAt.Time.UTC()
		rec.StoppedAt = &stoppedAt
	}
	return rec
}

func simRecordingStepFromRow(row gen.SimRecordingStep) domain.SimRecordingStep {
	return domain.SimRecordingStep{
		Seq:          row.Seq,
		At:           row.At.UTC(),
		Kind:         row.Kind,
		Selector:     row.Selector,
		SelectorRung: row.SelectorRung,
		Ambiguity:    row.Ambiguity,
		OffScreen:    row.OffScreen != 0,
		ScreenChange: row.ScreenChange != 0,
		X:            row.X,
		Y:            row.Y,
		ToX:          row.ToX,
		ToY:          row.ToY,
		DurationMS:   row.DurationMs,
		Text:         row.Text,
		Detail:       row.Detail,
	}
}
