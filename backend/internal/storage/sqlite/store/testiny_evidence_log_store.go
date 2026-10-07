package store

import (
	"context"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// AppendTestinyEvidence logs evidence AO uploaded and linked, all or none.
// Each entry's run must be linked to its task.
func (s *Store) AppendTestinyEvidence(ctx context.Context, entries []domain.TestinyEvidenceEntry) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.inTx(ctx, "log testiny evidence", func(q *gen.Queries) error {
		for _, e := range entries {
			if err := q.InsertTestinyEvidenceLog(ctx, gen.InsertTestinyEvidenceLogParams{
				SessionID: e.SessionID,
				RunID:     e.RunID,
				Event:     e.Event,
				File:      e.File,
				CaseID:    e.CaseID,
				DriveID:   e.DriveID,
				CommentID: e.CommentID,
				SetBy:     e.SetBy,
				CreatedAt: e.CreatedAt,
			}); err != nil {
				return fmt.Errorf("%s %q in %s of %s: %w", e.Event, e.File, e.RunID, e.SessionID, err)
			}
		}
		return nil
	})
}

// TestinyEvidenceLinked returns every link AO posted in a run, from any task
// the run is linked to, oldest first.
func (s *Store) TestinyEvidenceLinked(ctx context.Context, runID domain.TestinyRunID) ([]domain.TestinyEvidenceEntry, error) {
	rows, err := s.qr.ListTestinyEvidenceLinked(ctx, runID)
	if err != nil {
		return nil, fmt.Errorf("testiny evidence linked in %s: %w", runID, err)
	}
	entries := make([]domain.TestinyEvidenceEntry, len(rows))
	for i, r := range rows {
		entries[i] = domain.TestinyEvidenceEntry{
			SessionID: r.SessionID,
			RunID:     r.RunID,
			Event:     r.Event,
			File:      r.File,
			CaseID:    r.CaseID,
			DriveID:   r.DriveID,
			CommentID: r.CommentID,
			SetBy:     r.SetBy,
			CreatedAt: r.CreatedAt.UTC(),
		}
	}
	return entries, nil
}
