package store

import (
	"context"
	"encoding/json"
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
			steps := e.Steps
			if steps == nil {
				steps = []domain.TestinyStepResult{}
			}
			stepsJSON, err := json.Marshal(steps)
			if err != nil {
				return fmt.Errorf("TC-%d steps: %w", e.CaseID, err)
			}
			if err := q.InsertTestinyResultLog(ctx, gen.InsertTestinyResultLogParams{
				SessionID: e.SessionID,
				RunID:     e.RunID,
				CaseID:    e.CaseID,
				Status:    e.Status,
				Comment:   e.Comment,
				Steps:     string(stepsJSON),
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

// LatestTestinyResults returns, for each case in one run of a task, the latest
// logged result that set the case's status, by case id.
func (s *Store) LatestTestinyResults(ctx context.Context, sessionID domain.SessionID, runID domain.TestinyRunID) ([]domain.TestinyResultEntry, error) {
	rows, err := s.qr.ListLatestTestinyResults(ctx, gen.ListLatestTestinyResultsParams{SessionID: sessionID, RunID: runID})
	if err != nil {
		return nil, fmt.Errorf("latest testiny results of %s in %s: %w", runID, sessionID, err)
	}
	return testinyResultEntries(rows)
}

// TestinyStepResultLog returns every logged result in one run of a task that
// set step results, oldest first.
func (s *Store) TestinyStepResultLog(ctx context.Context, sessionID domain.SessionID, runID domain.TestinyRunID) ([]domain.TestinyResultEntry, error) {
	rows, err := s.qr.ListTestinyStepResultLog(ctx, gen.ListTestinyStepResultLogParams{SessionID: sessionID, RunID: runID})
	if err != nil {
		return nil, fmt.Errorf("testiny step results of %s in %s: %w", runID, sessionID, err)
	}
	return testinyResultEntries(rows)
}

func testinyResultEntries(rows []gen.TestinyResultLog) ([]domain.TestinyResultEntry, error) {
	entries := make([]domain.TestinyResultEntry, len(rows))
	for i, r := range rows {
		var steps []domain.TestinyStepResult
		if err := json.Unmarshal([]byte(r.Steps), &steps); err != nil {
			return nil, fmt.Errorf("testiny result log row %d: steps: %w", r.ID, err)
		}
		if len(steps) == 0 {
			steps = nil
		}
		entries[i] = domain.TestinyResultEntry{
			SessionID:     r.SessionID,
			RunID:         r.RunID,
			TestinyResult: domain.TestinyResult{CaseID: r.CaseID, Status: r.Status, Comment: r.Comment, Steps: steps},
			SetBy:         r.SetBy,
			SHA:           r.Sha,
			CreatedAt:     r.CreatedAt.UTC(),
		}
	}
	return entries, nil
}
