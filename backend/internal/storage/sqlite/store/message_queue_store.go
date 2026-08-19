package store

import (
	"context"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// EnqueueSessionMessage appends one message to a session's inbox and enforces
// the per-session cap in the SAME write transaction, so a queue can never be
// observed over its bound. It returns the stored row and how many pending
// messages the session now holds (the caller reports that back to the sender).
//
// The cap evicts the OLDEST pending rows, never the one just written: a queue
// that reached its bound belongs to a session nobody has opened in a long time,
// where the newest message is the one still worth reading. Eviction preserves
// order among what survives, because it only ever removes a prefix.
//
// limit <= 0 means unbounded; callers pass a real bound.
func (s *Store) EnqueueSessionMessage(ctx context.Context, msg domain.QueuedMessage, limit int) (domain.QueuedMessage, int, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var (
		stored  domain.QueuedMessage
		pending int
	)
	err := s.inTx(ctx, "enqueue session message", func(q *gen.Queries) error {
		row, err := q.InsertQueuedMessage(ctx, gen.InsertQueuedMessageParams{
			SessionID: msg.SessionID,
			Body:      msg.Body,
			QueuedAt:  msg.QueuedAt,
			ExpiresAt: msg.ExpiresAt,
			UpdatedAt: msg.QueuedAt,
		})
		if err != nil {
			return err
		}
		stored = queuedMessageFromRow(row)
		n, err := q.CountPendingQueuedMessages(ctx, msg.SessionID)
		if err != nil {
			return err
		}
		if limit > 0 && n > int64(limit) {
			if _, err := q.DeleteOldestPendingQueuedMessages(ctx, gen.DeleteOldestPendingQueuedMessagesParams{
				SessionID: msg.SessionID,
				Limit:     n - int64(limit),
			}); err != nil {
				return err
			}
			n = int64(limit)
		}
		pending = int(n)
		return nil
	})
	if err != nil {
		return domain.QueuedMessage{}, 0, err
	}
	return stored, pending, nil
}

// ListQueuedMessages returns a session's whole inbox, oldest first.
func (s *Store) ListQueuedMessages(ctx context.Context, id domain.SessionID) ([]domain.QueuedMessage, error) {
	rows, err := s.qr.ListQueuedMessagesBySession(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("list queued messages for session %s: %w", id, err)
	}
	out := make([]domain.QueuedMessage, 0, len(rows))
	for _, row := range rows {
		out = append(out, queuedMessageFromRow(row))
	}
	return out, nil
}

// ListPendingQueuedMessages returns the messages still waiting for a session,
// oldest first - the exact order a deliverer must replay them in.
func (s *Store) ListPendingQueuedMessages(ctx context.Context, id domain.SessionID) ([]domain.QueuedMessage, error) {
	rows, err := s.qr.ListPendingQueuedMessages(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("list pending queued messages for session %s: %w", id, err)
	}
	out := make([]domain.QueuedMessage, 0, len(rows))
	for _, row := range rows {
		out = append(out, queuedMessageFromRow(row))
	}
	return out, nil
}

// SessionsWithPendingMessages lists the sessions the delivery sweep has work
// for. Usually empty, so the sweep costs one indexed query per tick.
func (s *Store) SessionsWithPendingMessages(ctx context.Context) ([]domain.SessionID, error) {
	ids, err := s.qr.ListSessionsWithPendingMessages(ctx)
	if err != nil {
		return nil, fmt.Errorf("list sessions with pending messages: %w", err)
	}
	return ids, nil
}

// QueuedMessageCounts returns, per session holding any message, how many are
// pending and how many failed. Sessions with an empty inbox are absent.
func (s *Store) QueuedMessageCounts(ctx context.Context) (map[domain.SessionID]domain.QueuedMessageCounts, error) {
	rows, err := s.qr.CountQueuedMessagesBySession(ctx)
	if err != nil {
		return nil, fmt.Errorf("count queued messages: %w", err)
	}
	out := make(map[domain.SessionID]domain.QueuedMessageCounts, len(rows))
	for _, row := range rows {
		out[row.SessionID] = domain.QueuedMessageCounts{Pending: int(row.Pending), Failed: int(row.Failed)}
	}
	return out, nil
}

// SessionQueuedMessageCounts returns one session's pending/failed totals in a
// single indexed lookup, for the sessions read model.
func (s *Store) SessionQueuedMessageCounts(ctx context.Context, id domain.SessionID) (domain.QueuedMessageCounts, error) {
	row, err := s.qr.CountQueuedMessagesForSession(ctx, id)
	if err != nil {
		return domain.QueuedMessageCounts{}, fmt.Errorf("count queued messages for session %s: %w", id, err)
	}
	return domain.QueuedMessageCounts{Pending: int(row.Pending), Failed: int(row.Failed)}, nil
}

// ClaimQueuedMessage moves one row pending -> delivering and reports whether
// THIS caller won it. A row already claimed (or delivered, or expired away)
// yields false with no error: that is the delivered-once guard, not a failure.
func (s *Store) ClaimQueuedMessage(ctx context.Context, id int64, now time.Time) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	n, err := s.qw.ClaimQueuedMessage(ctx, gen.ClaimQueuedMessageParams{UpdatedAt: now, ID: id})
	if err != nil {
		return false, fmt.Errorf("claim queued message %d: %w", id, err)
	}
	return n > 0, nil
}

// DeleteQueuedMessage removes a row once the runtime has accepted it. Deletion
// IS the delivered marker: nothing left means nothing owed.
func (s *Store) DeleteQueuedMessage(ctx context.Context, id int64) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if _, err := s.qw.DeleteQueuedMessage(ctx, id); err != nil {
		return fmt.Errorf("delete queued message %d: %w", id, err)
	}
	return nil
}

// ReleaseQueuedMessage returns a claimed row to pending after a retryable
// delivery failure, recording why.
func (s *Store) ReleaseQueuedMessage(ctx context.Context, id int64, cause string, now time.Time) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if _, err := s.qw.ReleaseQueuedMessage(ctx, gen.ReleaseQueuedMessageParams{LastError: cause, UpdatedAt: now, ID: id}); err != nil {
		return fmt.Errorf("release queued message %d: %w", id, err)
	}
	return nil
}

// FailQueuedMessage marks a row as one that will never be delivered, keeping it
// so the drop stays visible.
func (s *Store) FailQueuedMessage(ctx context.Context, id int64, cause string, now time.Time) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if _, err := s.qw.FailQueuedMessage(ctx, gen.FailQueuedMessageParams{LastError: cause, UpdatedAt: now, ID: id}); err != nil {
		return fmt.Errorf("fail queued message %d: %w", id, err)
	}
	return nil
}

// FailDeliveringQueuedMessages is boot recovery: every row still in flight when
// the previous daemon died is failed rather than re-queued, because the runtime
// may already have typed it into the pane. Returns how many were failed.
func (s *Store) FailDeliveringQueuedMessages(ctx context.Context, cause string, now time.Time) (int, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	n, err := s.qw.FailDeliveringQueuedMessages(ctx, gen.FailDeliveringQueuedMessagesParams{LastError: cause, UpdatedAt: now})
	if err != nil {
		return 0, fmt.Errorf("fail in-flight queued messages: %w", err)
	}
	return int(n), nil
}

// ExpirePendingQueuedMessages drops every pending message past its expiry and
// returns the rows it removed, so the caller can say what was dropped.
func (s *Store) ExpirePendingQueuedMessages(ctx context.Context, now time.Time) ([]domain.QueuedMessage, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var expired []domain.QueuedMessage
	err := s.inTx(ctx, "expire queued messages", func(q *gen.Queries) error {
		rows, err := q.ListExpiredQueuedMessages(ctx, now)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		if _, err := q.DeleteExpiredQueuedMessages(ctx, now); err != nil {
			return err
		}
		for _, row := range rows {
			expired = append(expired, queuedMessageFromRow(row))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return expired, nil
}

// PurgeSessionMessages drops a session's whole inbox. Used when the session is
// deleted outright, where the ON DELETE CASCADE would otherwise be the only
// cleanup and callers want an explicit, testable seam.
func (s *Store) PurgeSessionMessages(ctx context.Context, id domain.SessionID) (int, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	n, err := s.qw.DeleteQueuedMessagesBySession(ctx, id)
	if err != nil {
		return 0, fmt.Errorf("purge queued messages for session %s: %w", id, err)
	}
	return int(n), nil
}

func queuedMessageFromRow(row gen.SessionMessageQueue) domain.QueuedMessage {
	return domain.QueuedMessage{
		ID:        row.ID,
		SessionID: row.SessionID,
		Body:      row.Body,
		State:     domain.QueuedMessageState(row.State),
		Attempts:  int(row.Attempts),
		LastError: row.LastError,
		QueuedAt:  row.QueuedAt,
		ExpiresAt: row.ExpiresAt,
		UpdatedAt: row.UpdatedAt,
	}
}
