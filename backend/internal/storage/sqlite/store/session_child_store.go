package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// InsertSessionChild records a child worktree AO has just created.
func (s *Store) InsertSessionChild(ctx context.Context, c domain.SessionChild) error {
	dirty, err := json.Marshal(nonNilStrings(c.BaseDirty))
	if err != nil {
		return fmt.Errorf("encode base dirty for child %s/%s: %w", c.SessionID, c.AgentID, err)
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	err = s.qr.InsertSessionChild(ctx, gen.InsertSessionChildParams{
		SessionID:     c.SessionID,
		ProjectID:     c.ProjectID,
		AgentID:       c.AgentID,
		ParentAgentID: c.ParentAgentID,
		AgentType:     c.AgentType,
		Description:   c.Description,
		Branch:        c.Branch,
		TargetBranch:  c.TargetBranch,
		BaseSha:       c.BaseSHA,
		BaseDirty:     string(dirty),
		WorktreePath:  c.WorktreePath,
		State:         c.State,
		CreatedAt:     c.CreatedAt,
		UpdatedAt:     c.UpdatedAt,
	})
	if err != nil {
		return fmt.Errorf("insert child %s/%s: %w", c.SessionID, c.AgentID, err)
	}
	return nil
}

// GetSessionChild reads one child, ok=false if absent.
func (s *Store) GetSessionChild(ctx context.Context, sessionID domain.SessionID, agentID string) (domain.SessionChild, bool, error) {
	row, err := s.qr.GetSessionChild(ctx, gen.GetSessionChildParams{SessionID: sessionID, AgentID: agentID})
	if errors.Is(err, sql.ErrNoRows) {
		return domain.SessionChild{}, false, nil
	}
	if err != nil {
		return domain.SessionChild{}, false, fmt.Errorf("get child %s/%s: %w", sessionID, agentID, err)
	}
	c, err := sessionChildFromRow(row)
	return c, err == nil, err
}

// ListSessionChildren returns a worker's children, oldest first.
func (s *Store) ListSessionChildren(ctx context.Context, sessionID domain.SessionID) ([]domain.SessionChild, error) {
	rows, err := s.qr.ListSessionChildren(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("list children of %s: %w", sessionID, err)
	}
	return sessionChildrenFromRows(rows)
}

// ListSessionChildrenInStates returns every child, across sessions, in one of
// the given states.
func (s *Store) ListSessionChildrenInStates(ctx context.Context, states ...domain.ChildState) ([]domain.SessionChild, error) {
	rows, err := s.qr.ListSessionChildrenInStates(ctx, states)
	if err != nil {
		return nil, fmt.Errorf("list children in states %v: %w", states, err)
	}
	return sessionChildrenFromRows(rows)
}

// ListBoardSessionChildren returns the children the board draws: those of live
// workers, and preserved ones of any worker.
func (s *Store) ListBoardSessionChildren(ctx context.Context) ([]domain.SessionChild, error) {
	rows, err := s.qr.ListBoardSessionChildren(ctx)
	if err != nil {
		return nil, fmt.Errorf("list board children: %w", err)
	}
	return sessionChildrenFromRows(rows)
}

// UpdateSessionChild writes a child's mutable fields. ok=false means the row
// no longer exists (its worker was purged).
func (s *Store) UpdateSessionChild(ctx context.Context, c domain.SessionChild) (bool, error) {
	var finished sql.NullTime
	if c.FinishedAt != nil {
		finished = sql.NullTime{Time: *c.FinishedAt, Valid: true}
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	n, err := s.qr.UpdateSessionChild(ctx, gen.UpdateSessionChildParams{
		AgentType:       c.AgentType,
		Description:     c.Description,
		State:           c.State,
		MergeHeadBefore: c.MergeHeadBefore,
		MergedSha:       c.MergedSHA,
		Commits:         int64(c.Commits),
		FilesChanged:    int64(c.FilesChanged),
		Detail:          c.Detail,
		StopBlocks:      int64(c.StopBlocks),
		NotifiedState:   c.NotifiedState,
		UpdatedAt:       c.UpdatedAt,
		FinishedAt:      finished,
		SessionID:       c.SessionID,
		AgentID:         c.AgentID,
	})
	if err != nil {
		return false, fmt.Errorf("update child %s/%s: %w", c.SessionID, c.AgentID, err)
	}
	return n > 0, nil
}

func sessionChildrenFromRows(rows []gen.SessionChild) ([]domain.SessionChild, error) {
	out := make([]domain.SessionChild, 0, len(rows))
	for _, row := range rows {
		c, err := sessionChildFromRow(row)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

func sessionChildFromRow(row gen.SessionChild) (domain.SessionChild, error) {
	var dirty []string
	if err := json.Unmarshal([]byte(row.BaseDirty), &dirty); err != nil {
		return domain.SessionChild{}, fmt.Errorf("decode base dirty for child %s/%s: %w", row.SessionID, row.AgentID, err)
	}
	c := domain.SessionChild{
		SessionID:       row.SessionID,
		ProjectID:       row.ProjectID,
		AgentID:         row.AgentID,
		ParentAgentID:   row.ParentAgentID,
		AgentType:       row.AgentType,
		Description:     row.Description,
		Branch:          row.Branch,
		TargetBranch:    row.TargetBranch,
		BaseSHA:         row.BaseSha,
		BaseDirty:       dirty,
		WorktreePath:    row.WorktreePath,
		State:           row.State,
		MergeHeadBefore: row.MergeHeadBefore,
		MergedSHA:       row.MergedSha,
		Commits:         int(row.Commits),
		FilesChanged:    int(row.FilesChanged),
		Detail:          row.Detail,
		StopBlocks:      int(row.StopBlocks),
		NotifiedState:   row.NotifiedState,
		CreatedAt:       row.CreatedAt,
		UpdatedAt:       row.UpdatedAt,
	}
	if row.FinishedAt.Valid {
		t := row.FinishedAt.Time
		c.FinishedAt = &t
	}
	return c, nil
}

func nonNilStrings(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}
