package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// AssignSimDevice gives a session a device to call its own. taken=false means
// the insert changed nothing - the session already has a device, or this one
// already belongs to somebody else - and the returned assignment is whatever
// the session actually holds now (zero when it holds none).
//
// Both outcomes are decided by the database rather than by a check here: the
// statement is a single conditional insert against two unique keys, so two
// spawns racing for the last free device resolve to one winner and the loser
// reads the truth back inside the same transaction.
func (s *Store) AssignSimDevice(
	ctx context.Context, assignment domain.SimDeviceAssignment,
) (domain.SimDeviceAssignment, bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var (
		taken bool
		held  domain.SimDeviceAssignment
	)
	err := s.inTx(ctx, "assign sim device", func(q *gen.Queries) error {
		rows, err := q.AssignSimDevice(ctx, gen.AssignSimDeviceParams{
			SessionID:  assignment.SessionID,
			Udid:       assignment.UDID,
			AssignedAt: assignment.AssignedAt,
		})
		if err != nil {
			return err
		}
		if rows > 0 {
			taken, held = true, assignment
			return nil
		}
		row, err := q.GetSimDeviceAssignment(ctx, assignment.SessionID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		held = simDeviceAssignmentFromRow(row)
		return nil
	})
	if err != nil {
		return domain.SimDeviceAssignment{}, false, fmt.Errorf(
			"assign sim device %s to %s: %w", assignment.UDID, assignment.SessionID, err)
	}
	return held, taken, nil
}

// GetSimDeviceAssignment returns the device a session holds, ok=false when it
// holds none.
func (s *Store) GetSimDeviceAssignment(
	ctx context.Context, sessionID domain.SessionID,
) (domain.SimDeviceAssignment, bool, error) {
	row, err := s.qr.GetSimDeviceAssignment(ctx, sessionID)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.SimDeviceAssignment{}, false, nil
	}
	if err != nil {
		return domain.SimDeviceAssignment{}, false, fmt.Errorf("get sim device assignment for %s: %w", sessionID, err)
	}
	return simDeviceAssignmentFromRow(row), true, nil
}

// ListSimDeviceAssignments returns every assignment, udid first. Every row is
// live: the session-terminate trigger deletes them, so there is nothing to
// expire on read.
func (s *Store) ListSimDeviceAssignments(ctx context.Context) ([]domain.SimDeviceAssignment, error) {
	rows, err := s.qr.ListSimDeviceAssignments(ctx)
	if err != nil {
		return nil, fmt.Errorf("list sim device assignments: %w", err)
	}
	out := make([]domain.SimDeviceAssignment, 0, len(rows))
	for _, row := range rows {
		out = append(out, simDeviceAssignmentFromRow(row))
	}
	return out, nil
}

// ReleaseSimDeviceAssignment hands a session's device back to the pool.
// released=false means it held none.
func (s *Store) ReleaseSimDeviceAssignment(ctx context.Context, sessionID domain.SessionID) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	rows, err := s.qw.ReleaseSimDeviceAssignment(ctx, sessionID)
	if err != nil {
		return false, fmt.Errorf("release sim device assignment for %s: %w", sessionID, err)
	}
	return rows > 0, nil
}

func simDeviceAssignmentFromRow(row gen.SimDeviceAssignment) domain.SimDeviceAssignment {
	return domain.SimDeviceAssignment{
		SessionID:  row.SessionID,
		UDID:       row.Udid,
		AssignedAt: row.AssignedAt,
	}
}
