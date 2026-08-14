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

// AcquireSimLease claims one simulator for one session, or reports who already
// has it. granted=true means the caller now holds the device (a fresh claim, a
// renewal of its own lease, or a takeover of an expired one); granted=false
// returns the CURRENT holder so the refusal can name them.
func (s *Store) AcquireSimLease(ctx context.Context, lease domain.SimLease) (domain.SimLease, bool, error) {
	return s.claimSimLease(ctx, "acquire", lease, func(q *gen.Queries) (int64, error) {
		return q.AcquireSimLease(ctx, gen.AcquireSimLeaseParams{
			Udid:       lease.UDID,
			SessionID:  lease.SessionID,
			AcquiredAt: lease.AcquiredAt,
			ExpiresAt:  lease.ExpiresAt,
			UpdatedAt:  lease.AcquiredAt,
		})
	})
}

// TakeOverSimLease claims a simulator another session holds. granted=false
// means a gesture is in flight on it, and returns the holder so the refusal can
// say whose.
func (s *Store) TakeOverSimLease(ctx context.Context, lease domain.SimLease) (domain.SimLease, bool, error) {
	return s.claimSimLease(ctx, "take over", lease, func(q *gen.Queries) (int64, error) {
		return q.TakeOverSimLease(ctx, gen.TakeOverSimLeaseParams{
			Udid:       lease.UDID,
			SessionID:  lease.SessionID,
			AcquiredAt: lease.AcquiredAt,
			ExpiresAt:  lease.ExpiresAt,
			UpdatedAt:  lease.AcquiredAt,
		})
	})
}

// claimSimLease is the shape both claims share: run one conditional upsert and,
// if it changed nothing, read the row to learn who won.
//
// The exclusion is the database's, not this function's: udid is the primary key
// and each statement is a single conditional upsert, so simultaneous callers
// resolve to exactly one winner even though nothing here holds a lock. The
// transaction exists only so the losing caller reads the holder that beat it.
// What differs between the two is only WHICH condition decides, which is why
// that is the one thing passed in.
func (s *Store) claimSimLease(
	ctx context.Context, what string, lease domain.SimLease, upsert func(*gen.Queries) (int64, error),
) (domain.SimLease, bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var (
		granted bool
		holder  domain.SimLease
	)
	err := s.inTx(ctx, what+" sim lease", func(q *gen.Queries) error {
		rows, err := upsert(q)
		if err != nil {
			return err
		}
		if rows > 0 {
			granted, holder = true, lease
			return nil
		}
		row, err := q.GetSimLease(ctx, lease.UDID)
		if err != nil {
			return err
		}
		holder = simLeaseFromRow(row)
		return nil
	})
	if err != nil {
		return domain.SimLease{}, false, fmt.Errorf("%s sim lease on %s for %s: %w", what, lease.UDID, lease.SessionID, err)
	}
	return holder, granted, nil
}

// ReleaseSimLease drops a session's own lease. released=false means the session
// did not hold that device (someone else does, or nobody does) and nothing was
// changed.
func (s *Store) ReleaseSimLease(ctx context.Context, udid string, sessionID domain.SessionID) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	rows, err := s.qw.ReleaseSimLease(ctx, gen.ReleaseSimLeaseParams{Udid: udid, SessionID: sessionID})
	if err != nil {
		return false, fmt.Errorf("release sim lease on %s for %s: %w", udid, sessionID, err)
	}
	return rows > 0, nil
}

// GetSimLease returns the live lease on a device, ok=false when there is none
// (no row, or a row whose TTL has run out at now).
func (s *Store) GetSimLease(ctx context.Context, udid string, now time.Time) (domain.SimLease, bool, error) {
	row, err := s.qr.GetSimLease(ctx, udid)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.SimLease{}, false, nil
	}
	if err != nil {
		return domain.SimLease{}, false, fmt.Errorf("get sim lease on %s: %w", udid, err)
	}
	lease := simLeaseFromRow(row)
	if !lease.Live(now) {
		return domain.SimLease{}, false, nil
	}
	return lease, true, nil
}

// ListSimLeases returns every lease still live at now, oldest udid first.
func (s *Store) ListSimLeases(ctx context.Context, now time.Time) ([]domain.SimLease, error) {
	rows, err := s.qr.ListLiveSimLeases(ctx, now)
	if err != nil {
		return nil, fmt.Errorf("list sim leases: %w", err)
	}
	leases := make([]domain.SimLease, 0, len(rows))
	for _, row := range rows {
		leases = append(leases, simLeaseFromRow(row))
	}
	return leases, nil
}

func simLeaseFromRow(row gen.SimLease) domain.SimLease {
	return domain.SimLease{
		UDID:       row.Udid,
		SessionID:  row.SessionID,
		AcquiredAt: row.AcquiredAt.UTC(),
		ExpiresAt:  row.ExpiresAt.UTC(),
	}
}
