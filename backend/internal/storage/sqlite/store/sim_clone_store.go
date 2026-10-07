package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// AddSimClone records a device AO just cloned for a session. added=false means
// the session already has a device under that label, and the returned clone is
// that one: the caller made a device nobody will own and must delete it.
func (s *Store) AddSimClone(ctx context.Context, clone domain.SimClone) (domain.SimClone, bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var (
		added bool
		held  domain.SimClone
	)
	err := s.inTx(ctx, "add sim clone", func(q *gen.Queries) error {
		rows, err := q.InsertSimClone(ctx, gen.InsertSimCloneParams{
			Udid:      domain.NormalizeSimUDID(clone.UDID),
			SessionID: clone.SessionID,
			Label:     clone.Label,
			Base:      clone.Base,
			Name:      clone.Name,
			CreatedAt: clone.CreatedAt.UTC(),
		})
		if err != nil {
			return err
		}
		if rows > 0 {
			added, held = true, clone
			held.UDID = domain.NormalizeSimUDID(clone.UDID)
			return nil
		}
		row, err := q.GetSimClone(ctx, gen.GetSimCloneParams{SessionID: clone.SessionID, Label: clone.Label})
		if err != nil {
			return err
		}
		held = simCloneFromRow(row)
		return nil
	})
	if err != nil {
		return domain.SimClone{}, false, fmt.Errorf("add sim clone %s for %s: %w", clone.UDID, clone.SessionID, err)
	}
	return held, added, nil
}

// GetSimClone returns a session's device under a label, ok=false when it has
// none.
func (s *Store) GetSimClone(ctx context.Context, sessionID domain.SessionID, label string) (domain.SimClone, bool, error) {
	row, err := s.qr.GetSimClone(ctx, gen.GetSimCloneParams{SessionID: sessionID, Label: label})
	if errors.Is(err, sql.ErrNoRows) {
		return domain.SimClone{}, false, nil
	}
	if err != nil {
		return domain.SimClone{}, false, fmt.Errorf("get sim clone %s of %s: %w", label, sessionID, err)
	}
	return simCloneFromRow(row), true, nil
}

// ListSimClones returns every clone AO holds, by session then label.
func (s *Store) ListSimClones(ctx context.Context) ([]domain.SimClone, error) {
	rows, err := s.qr.ListSimClones(ctx)
	if err != nil {
		return nil, fmt.Errorf("list sim clones: %w", err)
	}
	out := make([]domain.SimClone, 0, len(rows))
	for _, row := range rows {
		out = append(out, simCloneFromRow(row))
	}
	return out, nil
}

// ListOrphanSimClones returns the clones whose session has ended or no longer
// exists: the devices the sweep deletes.
func (s *Store) ListOrphanSimClones(ctx context.Context) ([]domain.SimClone, error) {
	rows, err := s.qr.ListOrphanSimClones(ctx)
	if err != nil {
		return nil, fmt.Errorf("list orphan sim clones: %w", err)
	}
	out := make([]domain.SimClone, 0, len(rows))
	for _, row := range rows {
		out = append(out, simCloneFromRow(gen.SimClone(row)))
	}
	return out, nil
}

// DeleteSimClone forgets a clone. Call it only once the device itself is gone.
func (s *Store) DeleteSimClone(ctx context.Context, udid string) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	rows, err := s.qw.DeleteSimClone(ctx, domain.NormalizeSimUDID(udid))
	if err != nil {
		return false, fmt.Errorf("delete sim clone %s: %w", udid, err)
	}
	return rows > 0, nil
}

func simCloneFromRow(row gen.SimClone) domain.SimClone {
	return domain.SimClone{
		UDID:      row.Udid,
		SessionID: row.SessionID,
		Label:     row.Label,
		Base:      row.Base,
		Name:      row.Name,
		CreatedAt: row.CreatedAt,
	}
}
