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

// UpsertScriptsStoreWorktree writes a workspace's store worktree row, creating
// it on first write. CreatedAt is kept from the first write.
func (s *Store) UpsertScriptsStoreWorktree(ctx context.Context, w domain.ScriptsStoreWorktree) error {
	held, err := json.Marshal(nonNilStrings(w.HeldFiles))
	if err != nil {
		return fmt.Errorf("encode held files for store worktree %s: %w", w.SessionID, err)
	}
	uncommitted, err := json.Marshal(nonNilStrings(w.Uncommitted))
	if err != nil {
		return fmt.Errorf("encode uncommitted files for store worktree %s: %w", w.SessionID, err)
	}
	var published sql.NullTime
	if !w.PublishedAt.IsZero() {
		published = sql.NullTime{Time: w.PublishedAt, Valid: true}
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	err = s.qr.UpsertScriptsStoreWorktree(ctx, gen.UpsertScriptsStoreWorktreeParams{
		SessionID:   w.SessionID,
		ProjectID:   w.ProjectID,
		Store:       w.Store,
		Path:        w.Path,
		Branch:      w.Branch,
		BaseBranch:  w.BaseBranch,
		State:       w.State,
		HeldReason:  w.HeldReason,
		HeldFiles:   string(held),
		Uncommitted: string(uncommitted),
		Unpublished: int64(w.Unpublished),
		CreatedAt:   w.CreatedAt,
		UpdatedAt:   w.UpdatedAt,
		PublishedAt: published,
	})
	if err != nil {
		return fmt.Errorf("upsert store worktree %s: %w", w.SessionID, err)
	}
	return nil
}

// GetScriptsStoreWorktree reads a workspace owner's row, ok=false if it has
// never had a store worktree.
func (s *Store) GetScriptsStoreWorktree(ctx context.Context, owner domain.SessionID) (domain.ScriptsStoreWorktree, bool, error) {
	row, err := s.qr.GetScriptsStoreWorktree(ctx, owner)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ScriptsStoreWorktree{}, false, nil
	}
	if err != nil {
		return domain.ScriptsStoreWorktree{}, false, fmt.Errorf("get store worktree %s: %w", owner, err)
	}
	w, err := scriptsStoreWorktreeFromRow(row)
	return w, err == nil, err
}

// ListScriptsStoreWorktreesInStates returns every row in one of the states,
// oldest first.
func (s *Store) ListScriptsStoreWorktreesInStates(ctx context.Context, states ...domain.ScriptsStoreState) ([]domain.ScriptsStoreWorktree, error) {
	rows, err := s.qr.ListScriptsStoreWorktreesInStates(ctx, states)
	if err != nil {
		return nil, fmt.Errorf("list store worktrees in states %v: %w", states, err)
	}
	out := make([]domain.ScriptsStoreWorktree, 0, len(rows))
	for _, row := range rows {
		w, err := scriptsStoreWorktreeFromRow(row)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, nil
}

func scriptsStoreWorktreeFromRow(row gen.ScriptsStoreWorktree) (domain.ScriptsStoreWorktree, error) {
	var held, uncommitted []string
	if err := json.Unmarshal([]byte(row.HeldFiles), &held); err != nil {
		return domain.ScriptsStoreWorktree{}, fmt.Errorf("decode held files for store worktree %s: %w", row.SessionID, err)
	}
	if err := json.Unmarshal([]byte(row.Uncommitted), &uncommitted); err != nil {
		return domain.ScriptsStoreWorktree{}, fmt.Errorf("decode uncommitted files for store worktree %s: %w", row.SessionID, err)
	}
	w := domain.ScriptsStoreWorktree{
		SessionID:   row.SessionID,
		ProjectID:   row.ProjectID,
		Store:       row.Store,
		Path:        row.Path,
		Branch:      row.Branch,
		BaseBranch:  row.BaseBranch,
		State:       row.State,
		HeldReason:  row.HeldReason,
		HeldFiles:   held,
		Uncommitted: uncommitted,
		Unpublished: int(row.Unpublished),
		CreatedAt:   row.CreatedAt,
		UpdatedAt:   row.UpdatedAt,
	}
	if row.PublishedAt.Valid {
		w.PublishedAt = row.PublishedAt.Time
	}
	return w, nil
}
