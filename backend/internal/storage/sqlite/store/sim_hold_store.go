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

// AcquireSimHold takes the finger on one device for the length of one gesture.
//
// Like the lease, the exclusion is the database's: AcquireSimHold is a single
// conditional UPDATE whose predicate carries every rule (the caller's lease is
// live, and no gesture is already in flight), so simultaneous callers resolve to
// exactly one winner without this function holding a lock. The transaction
// exists only so a loser can read WHY it lost - held by another session, or the
// device is mid-gesture - because those two need different advice.
func (s *Store) AcquireSimHold(ctx context.Context, hold domain.SimHold, now time.Time) (domain.SimHoldOutcome, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var outcome domain.SimHoldOutcome
	err := s.inTx(ctx, "acquire sim hold", func(q *gen.Queries) error {
		rows, err := q.AcquireSimHold(ctx, gen.AcquireSimHoldParams{
			HoldToken:     sql.NullString{String: hold.Token, Valid: true},
			HoldExpiresAt: sql.NullTime{Time: hold.ExpiresAt, Valid: true},
			Now:           now,
			Udid:          hold.UDID,
			SessionID:     hold.SessionID,
		})
		if err != nil {
			return err
		}
		if rows > 0 {
			outcome = domain.SimHoldOutcome{
				Granted: true,
				Hold:    hold,
				Lease:   domain.SimLease{UDID: hold.UDID, SessionID: hold.SessionID},
				Leased:  true,
			}
			return nil
		}
		row, err := q.GetSimLease(ctx, hold.UDID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		lease := simLeaseFromRow(row)
		if lease.Live(now) {
			outcome.Lease, outcome.Leased = lease, true
		}
		if current, ok := simHoldFromRow(row); ok && current.Live(now) {
			outcome.Busy = true
		}
		return nil
	})
	if err != nil {
		return domain.SimHoldOutcome{}, fmt.Errorf("acquire sim hold on %s for %s: %w", hold.UDID, hold.SessionID, err)
	}
	return outcome, nil
}

// ReleaseSimHold gives the finger back while keeping the lease. released=false
// means this caller no longer owned the hold - it lapsed and someone else took
// it - and nothing was changed.
func (s *Store) ReleaseSimHold(ctx context.Context, udid, token string, now time.Time) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	rows, err := s.qw.ReleaseSimHold(ctx, gen.ReleaseSimHoldParams{
		Now:       now,
		Udid:      udid,
		HoldToken: sql.NullString{String: token, Valid: true},
	})
	if err != nil {
		return false, fmt.Errorf("release sim hold on %s: %w", udid, err)
	}
	return rows > 0, nil
}

// simHoldFromRow reads the hold half of a lease row. ok=false when no gesture
// has ever claimed this device.
func simHoldFromRow(row gen.SimLease) (domain.SimHold, bool) {
	if !row.HoldToken.Valid || !row.HoldExpiresAt.Valid {
		return domain.SimHold{}, false
	}
	return domain.SimHold{
		UDID:      row.Udid,
		SessionID: row.SessionID,
		Token:     row.HoldToken.String,
		ExpiresAt: row.HoldExpiresAt.Time.UTC(),
	}, true
}
