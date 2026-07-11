package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// ListSmokeChecksBySession returns a session's checklist ordered by seq, each
// case with its attached evidence loaded (checklists are small — 3–6 cases — so
// evidence is fetched per case rather than with a join).
func (s *Store) ListSmokeChecksBySession(ctx context.Context, id domain.SessionID) ([]domain.SmokeCheck, error) {
	rows, err := s.qr.ListSmokeChecksBySession(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("list smoke checks for session %s: %w", id, err)
	}
	checks := make([]domain.SmokeCheck, 0, len(rows))
	for _, row := range rows {
		check, err := smokeCheckFromRow(row)
		if err != nil {
			return nil, err
		}
		evidence, err := s.listSmokeEvidence(ctx, s.qr, check.ID)
		if err != nil {
			return nil, err
		}
		check.Evidence = evidence
		checks = append(checks, check)
	}
	return checks, nil
}

// GetSmokeCheck returns one case with its evidence, ok=false if absent.
func (s *Store) GetSmokeCheck(ctx context.Context, id string) (domain.SmokeCheck, bool, error) {
	row, err := s.qr.GetSmokeCheck(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.SmokeCheck{}, false, nil
	}
	if err != nil {
		return domain.SmokeCheck{}, false, fmt.Errorf("get smoke check %s: %w", id, err)
	}
	check, err := smokeCheckFromRow(row)
	if err != nil {
		return domain.SmokeCheck{}, false, err
	}
	evidence, err := s.listSmokeEvidence(ctx, s.qr, id)
	if err != nil {
		return domain.SmokeCheck{}, false, err
	}
	check.Evidence = evidence
	return check, true, nil
}

// ReplaceSmokeChecks upserts a whole checklist by stable case id in one write
// transaction: an id already present has only its authored fields rewritten
// (verdict/note/decided_at/reported_at + evidence rows are preserved), a new id
// is inserted fresh, and an existing id absent from cases is deleted (its
// evidence rows cascade). Returns the removed check ids so the caller can delete
// their on-disk evidence blobs (the store owns rows, not files).
func (s *Store) ReplaceSmokeChecks(ctx context.Context, sessionID domain.SessionID, projectID domain.ProjectID, cases []domain.SmokeAuthoredCase, now time.Time) ([]domain.SmokeCheck, []string, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var removed []string
	err := s.inTx(ctx, "replace smoke checks", func(q *gen.Queries) error {
		existing, err := q.ListSmokeChecksBySession(ctx, sessionID)
		if err != nil {
			return err
		}
		keep := make(map[string]struct{}, len(cases))
		for _, c := range cases {
			keep[c.ID] = struct{}{}
		}
		present := make(map[string]struct{}, len(existing))
		for _, row := range existing {
			present[row.ID] = struct{}{}
			if _, ok := keep[row.ID]; !ok {
				if err := q.DeleteSmokeCheck(ctx, row.ID); err != nil {
					return err
				}
				removed = append(removed, row.ID)
			}
		}
		for _, c := range cases {
			steps, err := json.Marshal(stepsOrEmpty(c.Steps))
			if err != nil {
				return fmt.Errorf("encode steps for %s: %w", c.ID, err)
			}
			if _, ok := present[c.ID]; ok {
				if _, err := q.UpdateSmokeCheckAuthored(ctx, gen.UpdateSmokeCheckAuthoredParams{
					Seq:       int64(c.Seq),
					Name:      c.Name,
					Why:       c.Why,
					Steps:     string(steps),
					Expected:  c.Expected,
					PRNum:     int64(c.PRNum),
					FileRef:   c.FileRef,
					UpdatedAt: now,
					ID:        c.ID,
				}); err != nil {
					return err
				}
				continue
			}
			if err := q.InsertSmokeCheck(ctx, gen.InsertSmokeCheckParams{
				ID:        c.ID,
				SessionID: sessionID,
				ProjectID: projectID,
				Seq:       int64(c.Seq),
				Name:      c.Name,
				Why:       c.Why,
				Steps:     string(steps),
				Expected:  c.Expected,
				PRNum:     int64(c.PRNum),
				FileRef:   c.FileRef,
				CreatedAt: now,
				UpdatedAt: now,
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, nil, fmt.Errorf("replace smoke checks for session %s: %w", sessionID, err)
	}
	checks, err := s.ListSmokeChecksBySession(ctx, sessionID)
	if err != nil {
		return nil, nil, err
	}
	return checks, removed, nil
}

// SetSmokeVerdict records the user's verdict + note for a case, ok=false if the
// case does not exist.
func (s *Store) SetSmokeVerdict(ctx context.Context, id string, verdict domain.SmokeVerdict, note string, decidedAt, now time.Time) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	n, err := s.qw.SetSmokeVerdict(ctx, gen.SetSmokeVerdictParams{
		Verdict:   verdict,
		Note:      note,
		DecidedAt: sql.NullTime{Time: decidedAt, Valid: true},
		UpdatedAt: now,
		ID:        id,
	})
	if err != nil {
		return false, fmt.Errorf("set smoke verdict %s: %w", id, err)
	}
	return n > 0, nil
}

// ResetSmokeCheck clears a case's verdict/note/decided_at and deletes its
// evidence rows (one tx), ok=false if the case does not exist.
func (s *Store) ResetSmokeCheck(ctx context.Context, id string, now time.Time) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var reset bool
	err := s.inTx(ctx, "reset smoke check", func(q *gen.Queries) error {
		n, err := q.ResetSmokeCheck(ctx, gen.ResetSmokeCheckParams{UpdatedAt: now, ID: id})
		if err != nil {
			return err
		}
		reset = n > 0
		if !reset {
			return nil
		}
		return q.DeleteSmokeEvidenceByCheck(ctx, id)
	})
	if err != nil {
		return false, fmt.Errorf("reset smoke check %s: %w", id, err)
	}
	return reset, nil
}

// MarkSmokeReported stamps reported_at across all of a session's checks and
// returns how many rows were marked.
func (s *Store) MarkSmokeReported(ctx context.Context, id domain.SessionID, reportedAt, now time.Time) (int64, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	n, err := s.qw.MarkSmokeReported(ctx, gen.MarkSmokeReportedParams{
		ReportedAt: sql.NullTime{Time: reportedAt, Valid: true},
		UpdatedAt:  now,
		SessionID:  id,
	})
	if err != nil {
		return 0, fmt.Errorf("mark smoke reported for session %s: %w", id, err)
	}
	return n, nil
}

// InsertSmokeEvidence records one evidence blob's metadata.
func (s *Store) InsertSmokeEvidence(ctx context.Context, ev domain.SmokeEvidence) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.qw.InsertSmokeEvidence(ctx, gen.InsertSmokeEvidenceParams{
		ID:        ev.ID,
		CheckID:   ev.CheckID,
		SessionID: ev.SessionID,
		Kind:      ev.Kind,
		Filename:  ev.Filename,
		Mime:      ev.Mime,
		SizeBytes: ev.SizeBytes,
		CreatedAt: ev.CreatedAt,
	})
}

// GetSmokeEvidence returns one evidence row, ok=false if absent.
func (s *Store) GetSmokeEvidence(ctx context.Context, id string) (domain.SmokeEvidence, bool, error) {
	row, err := s.qr.GetSmokeEvidence(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.SmokeEvidence{}, false, nil
	}
	if err != nil {
		return domain.SmokeEvidence{}, false, fmt.Errorf("get smoke evidence %s: %w", id, err)
	}
	return smokeEvidenceFromRow(row), true, nil
}

func (s *Store) listSmokeEvidence(ctx context.Context, q *gen.Queries, checkID string) ([]domain.SmokeEvidence, error) {
	rows, err := q.ListSmokeEvidenceByCheck(ctx, checkID)
	if err != nil {
		return nil, fmt.Errorf("list smoke evidence for check %s: %w", checkID, err)
	}
	out := make([]domain.SmokeEvidence, 0, len(rows))
	for _, row := range rows {
		out = append(out, smokeEvidenceFromRow(row))
	}
	return out, nil
}

func smokeCheckFromRow(r gen.SmokeCheck) (domain.SmokeCheck, error) {
	steps := []string{}
	if r.Steps != "" {
		if err := json.Unmarshal([]byte(r.Steps), &steps); err != nil {
			return domain.SmokeCheck{}, fmt.Errorf("decode steps for smoke check %s: %w", r.ID, err)
		}
	}
	return domain.SmokeCheck{
		ID:         r.ID,
		SessionID:  r.SessionID,
		ProjectID:  r.ProjectID,
		Seq:        int(r.Seq),
		Name:       r.Name,
		Why:        r.Why,
		Steps:      steps,
		Expected:   r.Expected,
		PRNum:      int(r.PRNum),
		FileRef:    r.FileRef,
		Verdict:    r.Verdict,
		Note:       r.Note,
		Evidence:   []domain.SmokeEvidence{},
		DecidedAt:  nullTimePtr(r.DecidedAt),
		ReportedAt: nullTimePtr(r.ReportedAt),
		CreatedAt:  r.CreatedAt,
		UpdatedAt:  r.UpdatedAt,
	}, nil
}

func smokeEvidenceFromRow(r gen.SmokeEvidence) domain.SmokeEvidence {
	return domain.SmokeEvidence{
		ID:        r.ID,
		CheckID:   r.CheckID,
		SessionID: r.SessionID,
		Kind:      r.Kind,
		Filename:  r.Filename,
		Mime:      r.Mime,
		SizeBytes: r.SizeBytes,
		CreatedAt: r.CreatedAt,
	}
}

func nullTimePtr(t sql.NullTime) *time.Time {
	if !t.Valid {
		return nil
	}
	v := t.Time
	return &v
}

func stepsOrEmpty(steps []string) []string {
	if steps == nil {
		return []string{}
	}
	return steps
}
