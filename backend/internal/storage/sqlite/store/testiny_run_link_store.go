package store

import (
	"context"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// InsertTestinyRunLink links a run to a task. Linking a run that is already
// linked changes nothing.
func (s *Store) InsertTestinyRunLink(ctx context.Context, l domain.TestinyRunLink) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.qr.InsertTestinyRunLink(ctx, gen.InsertTestinyRunLinkParams{
		SessionID:   l.SessionID,
		RunID:       l.RunID,
		ProjectID:   l.Project.ID,
		ProjectKey:  l.Project.Key,
		ProjectName: l.Project.Name,
		LinkedBy:    l.LinkedBy,
		CreatedAt:   l.CreatedAt,
	}); err != nil {
		return fmt.Errorf("link %s to %s: %w", l.RunID, l.SessionID, err)
	}
	return nil
}

// DeleteTestinyRunLink unlinks a run from a task. A run that is not linked is
// not an error.
func (s *Store) DeleteTestinyRunLink(ctx context.Context, sessionID domain.SessionID, runID domain.TestinyRunID) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.qr.DeleteTestinyRunLink(ctx, gen.DeleteTestinyRunLinkParams{SessionID: sessionID, RunID: runID}); err != nil {
		return fmt.Errorf("unlink %s from %s: %w", runID, sessionID, err)
	}
	return nil
}

// ListTestinyRunLinks returns a task's runs in the order they were linked.
func (s *Store) ListTestinyRunLinks(ctx context.Context, sessionID domain.SessionID) ([]domain.TestinyRunLink, error) {
	rows, err := s.qr.ListTestinyRunLinks(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("list testiny runs of %s: %w", sessionID, err)
	}
	links := make([]domain.TestinyRunLink, len(rows))
	for i, r := range rows {
		links[i] = domain.TestinyRunLink{
			SessionID: r.SessionID,
			RunID:     r.RunID,
			Project:   domain.TestinyProject{ID: r.ProjectID, Key: r.ProjectKey, Name: r.ProjectName},
			LinkedBy:  r.LinkedBy,
			CreatedAt: r.CreatedAt.UTC(),
		}
	}
	return links, nil
}

// FillTestinyRunLinkProject records the run's Testiny project on every link of
// the run made before AO stored it. A link that has its project keeps it.
func (s *Store) FillTestinyRunLinkProject(ctx context.Context, runID domain.TestinyRunID, p domain.TestinyProject) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.qr.FillTestinyRunLinkProject(ctx, gen.FillTestinyRunLinkProjectParams{
		ProjectID: p.ID, ProjectKey: p.Key, ProjectName: p.Name, RunID: runID,
	}); err != nil {
		return fmt.Errorf("fill the project of %s: %w", runID, err)
	}
	return nil
}
