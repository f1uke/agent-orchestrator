package store

import (
	"context"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// AppendTestinyResults logs results AO wrote to Testiny, all or none. Each
// entry's run must be linked to its task.
func (s *Store) AppendTestinyResults(ctx context.Context, entries []domain.TestinyResultEntry) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.inTx(ctx, "log testiny results", func(q *gen.Queries) error {
		for _, e := range entries {
			if err := q.InsertTestinyResultLog(ctx, gen.InsertTestinyResultLogParams{
				SessionID: e.SessionID,
				RunID:     e.RunID,
				CaseID:    e.CaseID,
				Status:    e.Status,
				Comment:   e.Comment,
				SetBy:     e.SetBy,
				Sha:       e.SHA,
				CreatedAt: e.CreatedAt,
			}); err != nil {
				return fmt.Errorf("TC-%d in %s of %s: %w", e.CaseID, e.RunID, e.SessionID, err)
			}
		}
		return nil
	})
}

// LatestTestinyResults returns the latest logged result of each case in one
// run of a task, by case id.
func (s *Store) LatestTestinyResults(ctx context.Context, sessionID domain.SessionID, runID domain.TestinyRunID) ([]domain.TestinyResultEntry, error) {
	rows, err := s.qr.ListLatestTestinyResults(ctx, gen.ListLatestTestinyResultsParams{SessionID: sessionID, RunID: runID})
	if err != nil {
		return nil, fmt.Errorf("latest testiny results of %s in %s: %w", runID, sessionID, err)
	}
	entries := make([]domain.TestinyResultEntry, len(rows))
	for i, r := range rows {
		entries[i] = domain.TestinyResultEntry{
			SessionID:     r.SessionID,
			RunID:         r.RunID,
			TestinyResult: domain.TestinyResult{CaseID: r.CaseID, Status: r.Status, Comment: r.Comment},
			SetBy:         r.SetBy,
			SHA:           r.Sha,
			CreatedAt:     r.CreatedAt.UTC(),
		}
	}
	return entries, nil
}
