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

// InsertCrewMessage records one crew-to-crew message attempt, delivered or
// refused. Both are stored: the refusal is the escalation signal, not a
// non-event (see migration 0050 and domain/crewmessage.go).
func (s *Store) InsertCrewMessage(ctx context.Context, msg domain.CrewMessage) error {
	return s.qw.InsertCrewMessage(ctx, gen.InsertCrewMessageParams{
		ID:            msg.ID,
		CrewID:        string(msg.CrewID),
		ProjectID:     string(msg.ProjectID),
		FromSession:   string(msg.From),
		ToSession:     string(msg.To),
		Subject:       msg.Subject,
		RefusedReason: msg.RefusedReason,
		CreatedAt:     msg.CreatedAt,
	})
}

// CrewMessagesOnSubject counts what one member has already DELIVERED about one
// subject IN THE CURRENT ROUND - the per-subject cap's counter.
//
// `since` is when that round began: the zero time counts every row, which is
// every crew that has never had a member restored. Earlier rounds' rows are
// never deleted, they simply stop being counted (see migration 0060).
func (s *Store) CrewMessagesOnSubject(ctx context.Context, crewID domain.SessionID, subject string, from domain.SessionID, since time.Time) (int, error) {
	n, err := s.qw.CountCrewMessagesOnSubject(ctx, gen.CountCrewMessagesOnSubjectParams{
		CrewID:      string(crewID),
		Subject:     subject,
		FromSession: string(from),
		CreatedAt:   since,
	})
	if err != nil {
		return 0, fmt.Errorf("count crew messages on subject: %w", err)
	}
	return int(n), nil
}

// CrewMessagesSince counts every delivered message inside one crew since a
// cutoff - the per-hour budget's counter.
func (s *Store) CrewMessagesSince(ctx context.Context, crewID domain.SessionID, since time.Time) (int, error) {
	n, err := s.qw.CountCrewMessagesSince(ctx, gen.CountCrewMessagesSinceParams{
		CrewID:    string(crewID),
		CreatedAt: since,
	})
	if err != nil {
		return 0, fmt.Errorf("count crew messages since: %w", err)
	}
	return int(n), nil
}

// LatestCrewMessageFrom returns this session's most recent message attempt. The
// status derivation reads it and asks one question: was it refused?
func (s *Store) LatestCrewMessageFrom(ctx context.Context, from domain.SessionID) (domain.CrewMessage, bool, error) {
	row, err := s.qw.GetLatestCrewMessageBySender(ctx, string(from))
	return crewMessageRow(row, err, "latest crew message")
}

// LatestDeliveredCrewMessageFrom returns the latest message this session
// DELIVERED at or after `since`, the start of its current round (the zero time
// for a member that was never restored). It is how the board tells that qa has
// handed back this round.
func (s *Store) LatestDeliveredCrewMessageFrom(ctx context.Context, from domain.SessionID, since time.Time) (domain.CrewMessage, bool, error) {
	row, err := s.qw.GetLatestDeliveredCrewMessageBySenderSince(ctx, gen.GetLatestDeliveredCrewMessageBySenderSinceParams{
		FromSession: string(from),
		CreatedAt:   since,
	})
	return crewMessageRow(row, err, "latest delivered crew message")
}

func crewMessageRow(row gen.CrewMessage, err error, what string) (domain.CrewMessage, bool, error) {
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.CrewMessage{}, false, nil
		}
		return domain.CrewMessage{}, false, fmt.Errorf("%s: %w", what, err)
	}
	return domain.CrewMessage{
		ID:            row.ID,
		CrewID:        domain.SessionID(row.CrewID),
		ProjectID:     domain.ProjectID(row.ProjectID),
		From:          domain.SessionID(row.FromSession),
		To:            domain.SessionID(row.ToSession),
		Subject:       row.Subject,
		RefusedReason: row.RefusedReason,
		CreatedAt:     row.CreatedAt,
	}, true, nil
}
