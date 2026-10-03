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

// RecordTranscriptRef notes that a session's hook reported this transcript
// file. Repeated reports only move last_seen_at, and an empty native id never
// erases one already known.
func (s *Store) RecordTranscriptRef(ctx context.Context, ref domain.TranscriptRef) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	err := s.qw.UpsertSessionTranscript(ctx, gen.UpsertSessionTranscriptParams{
		SessionID:       string(ref.SessionID),
		TranscriptPath:  ref.Path,
		ClaudeSessionID: ref.ClaudeSessionID,
		FirstSeenAt:     ref.FirstSeenAt,
		LastSeenAt:      ref.LastSeenAt,
	})
	if err != nil {
		return fmt.Errorf("record transcript ref for %s: %w", ref.SessionID, err)
	}
	return nil
}

// ListTranscriptRefs returns every transcript file the hooks of the project's
// sessions have reported.
func (s *Store) ListTranscriptRefs(ctx context.Context, projectID domain.ProjectID) ([]domain.TranscriptRef, error) {
	rows, err := s.qr.ListSessionTranscriptsByProject(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("list transcript refs for %s: %w", projectID, err)
	}
	refs := make([]domain.TranscriptRef, 0, len(rows))
	for _, r := range rows {
		refs = append(refs, domain.TranscriptRef{
			SessionID:       domain.SessionID(r.SessionID),
			Path:            r.TranscriptPath,
			ClaudeSessionID: r.ClaudeSessionID,
			FirstSeenAt:     r.FirstSeenAt,
			LastSeenAt:      r.LastSeenAt,
		})
	}
	return refs, nil
}

// RecordPromptFingerprint stores one submitted prompt as a hash and a length.
func (s *Store) RecordPromptFingerprint(ctx context.Context, fp domain.PromptFingerprint) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	err := s.qw.InsertPromptFingerprint(ctx, gen.InsertPromptFingerprintParams{
		SessionID:       string(fp.SessionID),
		ProjectID:       string(fp.ProjectID),
		ClaudeSessionID: fp.ClaudeSessionID,
		Sha256:          fp.SHA256,
		Bytes:           int64(fp.Bytes),
		SubmittedAt:     fp.SubmittedAt,
	})
	if err != nil {
		return fmt.Errorf("record prompt fingerprint for %s: %w", fp.SessionID, err)
	}
	return nil
}

// RecordDeliveredFingerprint stores one message AO delivered, as a hash and
// who wrote it.
func (s *Store) RecordDeliveredFingerprint(ctx context.Context, fp domain.DeliveredFingerprint) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	err := s.qw.InsertDeliveredFingerprint(ctx, gen.InsertDeliveredFingerprintParams{
		SessionID:   string(fp.SessionID),
		ProjectID:   string(fp.ProjectID),
		Sha256:      fp.SHA256,
		Bytes:       int64(fp.Bytes),
		Trigger:     fp.Trigger,
		Author:      string(fp.Author),
		DeliveredAt: fp.DeliveredAt,
	})
	if err != nil {
		return fmt.Errorf("record delivered fingerprint for %s: %w", fp.SessionID, err)
	}
	return nil
}

// ListDeliveredFingerprints returns what AO recorded delivering into the
// session: each body's fingerprint, author and trigger.
func (s *Store) ListDeliveredFingerprints(ctx context.Context, sessionID domain.SessionID) ([]domain.DeliveredFingerprint, error) {
	rows, err := s.qr.ListDeliveredFingerprintsBySession(ctx, string(sessionID))
	if err != nil {
		return nil, fmt.Errorf("list delivered fingerprints for %s: %w", sessionID, err)
	}
	out := make([]domain.DeliveredFingerprint, 0, len(rows))
	for _, r := range rows {
		out = append(out, domain.DeliveredFingerprint{
			SessionID: sessionID,
			SHA256:    r.Sha256,
			Author:    domain.DeliveryAuthor(r.Author),
			Trigger:   r.Trigger,
		})
	}
	return out, nil
}

// GetLearnCursor returns the capture cursor for one transcript file.
func (s *Store) GetLearnCursor(ctx context.Context, path string) (domain.LearnCursor, bool, error) {
	row, err := s.qr.GetLearnCursor(ctx, path)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.LearnCursor{}, false, nil
	}
	if err != nil {
		return domain.LearnCursor{}, false, fmt.Errorf("get learn cursor: %w", err)
	}
	return learnCursorFromRow(row), true, nil
}

// ListLearnCursors returns every capture cursor of the project.
func (s *Store) ListLearnCursors(ctx context.Context, projectID domain.ProjectID) ([]domain.LearnCursor, error) {
	rows, err := s.qr.ListLearnCursorsByProject(ctx, string(projectID))
	if err != nil {
		return nil, fmt.Errorf("list learn cursors for %s: %w", projectID, err)
	}
	out := make([]domain.LearnCursor, 0, len(rows))
	for _, r := range rows {
		out = append(out, learnCursorFromRow(r))
	}
	return out, nil
}

func learnCursorFromRow(r gen.LearnCursor) domain.LearnCursor {
	c := domain.LearnCursor{
		TranscriptPath: r.TranscriptPath,
		ProjectID:      domain.ProjectID(r.ProjectID),
		SessionID:      domain.SessionID(r.SessionID),
		Attribution:    domain.LearnAttribution(r.Attribution),
		ByteOffset:     r.ByteOffset,
		FileSize:       r.FileSize,
		PendingCarry:   r.PendingCarry,
		HumanTurns:     int(r.HumanTurns),
		MachineTurns:   int(r.MachineTurns),
		LastError:      r.LastError,
		UpdatedAt:      r.UpdatedAt,
	}
	if r.FileMtime.Valid {
		c.FileMTime = r.FileMtime.Time
	}
	return c
}

// CommitLearnPass lands one capture pass over one transcript atomically: the
// excerpts it finalized, the prompt fingerprints it accounted for, and the
// cursor it advanced to. A crash anywhere leaves the cursor where it was, so
// the next pass re-reads the same turns, and the (transcript, turn) key makes
// that re-read a no-op for turns already stored. It returns how many excerpts
// were new.
func (s *Store) CommitLearnPass(ctx context.Context, cursor domain.LearnCursor, excerpts []domain.LearnExcerpt, matches []domain.PromptMatch, now time.Time) (int, error) {
	params := make([]gen.InsertLearnExcerptParams, 0, len(excerpts))
	for _, e := range excerpts {
		p, err := learnExcerptParams(e)
		if err != nil {
			return 0, err
		}
		params = append(params, p)
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	inserted := 0
	err := s.inTx(ctx, "commit learn pass", func(q *gen.Queries) error {
		for _, p := range params {
			n, err := q.InsertLearnExcerpt(ctx, p)
			if err != nil {
				return fmt.Errorf("insert excerpt %s: %w", p.TurnUuid, err)
			}
			inserted += int(n)
		}
		for _, m := range matches {
			if _, err := q.MarkPromptFingerprintMatched(ctx, gen.MarkPromptFingerprintMatchedParams{
				MatchedAt: sql.NullTime{Time: now, Valid: true},
				SessionID: string(m.SessionID),
				Sha256:    m.SHA256,
			}); err != nil {
				return fmt.Errorf("match prompt fingerprint: %w", err)
			}
		}
		return q.UpsertLearnCursor(ctx, gen.UpsertLearnCursorParams{
			TranscriptPath: cursor.TranscriptPath,
			ProjectID:      string(cursor.ProjectID),
			SessionID:      string(cursor.SessionID),
			Attribution:    string(cursor.Attribution),
			ByteOffset:     cursor.ByteOffset,
			FileSize:       cursor.FileSize,
			FileMtime:      sql.NullTime{Time: cursor.FileMTime, Valid: !cursor.FileMTime.IsZero()},
			PendingCarry:   cursor.PendingCarry,
			HumanTurns:     int64(cursor.HumanTurns),
			MachineTurns:   int64(cursor.MachineTurns),
			LastError:      "",
			UpdatedAt:      now,
		})
	})
	if err != nil {
		return 0, err
	}
	return inserted, nil
}

// SetLearnCursorError records that a pass over this file failed, leaving its
// position untouched.
func (s *Store) SetLearnCursorError(ctx context.Context, cursor domain.LearnCursor, msg string, now time.Time) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	err := s.qw.SetLearnCursorError(ctx, gen.SetLearnCursorErrorParams{
		TranscriptPath: cursor.TranscriptPath,
		ProjectID:      string(cursor.ProjectID),
		SessionID:      string(cursor.SessionID),
		Attribution:    string(cursor.Attribution),
		LastError:      msg,
		UpdatedAt:      now,
	})
	if err != nil {
		return fmt.Errorf("set learn cursor error: %w", err)
	}
	return nil
}

func learnExcerptParams(e domain.LearnExcerpt) (gen.InsertLearnExcerptParams, error) {
	before, err := json.Marshal(e.Before)
	if err != nil {
		return gen.InsertLearnExcerptParams{}, fmt.Errorf("encode excerpt window: %w", err)
	}
	after, err := json.Marshal(e.After)
	if err != nil {
		return gen.InsertLearnExcerptParams{}, fmt.Errorf("encode excerpt window: %w", err)
	}
	redactions := e.Redactions
	if redactions == nil {
		redactions = map[string]int{}
	}
	red, err := json.Marshal(redactions)
	if err != nil {
		return gen.InsertLearnExcerptParams{}, fmt.Errorf("encode excerpt redactions: %w", err)
	}
	return gen.InsertLearnExcerptParams{
		ProjectID:      string(e.ProjectID),
		SessionID:      string(e.SessionID),
		TranscriptPath: e.TranscriptPath,
		TurnUuid:       e.TurnUUID,
		TurnAt:         e.TurnAt,
		SourceClass:    string(e.SourceClass),
		Cwd:            e.CWD,
		GitBranch:      e.GitBranch,
		BeforeJson:     string(before),
		HumanText:      e.HumanText,
		AfterJson:      string(after),
		RedactionsJson: string(red),
		CreatedAt:      e.CreatedAt,
	}, nil
}

// ListLearnExcerpts returns the project's newest captured turns first.
func (s *Store) ListLearnExcerpts(ctx context.Context, projectID domain.ProjectID, limit int) ([]domain.LearnExcerpt, error) {
	rows, err := s.qr.ListLearnExcerptsByProject(ctx, gen.ListLearnExcerptsByProjectParams{
		ProjectID: string(projectID),
		Limit:     int64(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("list learn excerpts for %s: %w", projectID, err)
	}
	out := make([]domain.LearnExcerpt, 0, len(rows))
	for _, r := range rows {
		out = append(out, excerptFromRow(gen.LearnExcerpt{
			ID: r.ID, ProjectID: r.ProjectID, SessionID: r.SessionID, TranscriptPath: r.TranscriptPath,
			TurnUuid: r.TurnUuid, TurnAt: r.TurnAt, SourceClass: r.SourceClass, Cwd: r.Cwd,
			GitBranch: r.GitBranch, BeforeJson: r.BeforeJson, HumanText: r.HumanText,
			AfterJson: r.AfterJson, RedactionsJson: r.RedactionsJson, CreatedAt: r.CreatedAt,
		}))
	}
	return out, nil
}

func excerptFromRow(r gen.LearnExcerpt) domain.LearnExcerpt {
	e := domain.LearnExcerpt{
		ID:             r.ID,
		ProjectID:      domain.ProjectID(r.ProjectID),
		SessionID:      domain.SessionID(r.SessionID),
		TranscriptPath: r.TranscriptPath,
		TurnUUID:       r.TurnUuid,
		TurnAt:         r.TurnAt,
		SourceClass:    domain.LearnSourceClass(r.SourceClass),
		CWD:            r.Cwd,
		GitBranch:      r.GitBranch,
		HumanText:      r.HumanText,
		CreatedAt:      r.CreatedAt,
	}
	// A stored window that no longer decodes is shown empty rather than
	// failing the whole list: the human text is the part that matters.
	_ = json.Unmarshal([]byte(r.BeforeJson), &e.Before)
	_ = json.Unmarshal([]byte(r.AfterJson), &e.After)
	_ = json.Unmarshal([]byte(r.RedactionsJson), &e.Redactions)
	return e
}

// LearnCounts tallies the project's captured turns, and the hook-seen prompts
// submitted before unmatchedBefore that capture never found in a transcript.
func (s *Store) LearnCounts(ctx context.Context, projectID domain.ProjectID, unmatchedBefore time.Time) (domain.LearnCounts, error) {
	out := domain.LearnCounts{BySourceClass: map[domain.LearnSourceClass]int{}}
	n, err := s.qr.CountLearnExcerptsByProject(ctx, string(projectID))
	if err != nil {
		return out, fmt.Errorf("count learn excerpts: %w", err)
	}
	out.Excerpts = int(n)
	rows, err := s.qr.CountLearnExcerptsBySourceClass(ctx, string(projectID))
	if err != nil {
		return out, fmt.Errorf("count learn excerpts by class: %w", err)
	}
	for _, r := range rows {
		out.BySourceClass[domain.LearnSourceClass(r.SourceClass)] = int(r.Turns)
	}
	p, err := s.qr.CountPromptFingerprints(ctx, string(projectID))
	if err != nil {
		return out, fmt.Errorf("count prompt fingerprints: %w", err)
	}
	out.Prompts = int(p)
	u, err := s.qr.CountUnmatchedPromptFingerprints(ctx, gen.CountUnmatchedPromptFingerprintsParams{
		ProjectID:   string(projectID),
		SubmittedAt: unmatchedBefore,
	})
	if err != nil {
		return out, fmt.Errorf("count unmatched prompt fingerprints: %w", err)
	}
	out.UnmatchedPrompts = int(u)
	return out, nil
}

// ForgetLearning deletes everything learning kept for a project - excerpts,
// drafts, proposals, model runs, cursors and both kinds of fingerprint - in one transaction, and returns how
// many turns were deleted. The transcript refs stay: they are paths, not
// content, and the session's own bookkeeping uses them.
func (s *Store) ForgetLearning(ctx context.Context, projectID domain.ProjectID) (int, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	turns := 0
	err := s.inTx(ctx, "forget learning", func(q *gen.Queries) error {
		n, err := q.DeleteLearnExcerptsByProject(ctx, string(projectID))
		if err != nil {
			return err
		}
		turns = int(n)
		if _, err := q.DeleteSkillProposalsByProject(ctx, string(projectID)); err != nil {
			return fmt.Errorf("delete skill proposals: %w", err)
		}
		if _, err := q.DeleteDecidedTasksByProject(ctx, string(projectID)); err != nil {
			return fmt.Errorf("delete decided tasks: %w", err)
		}
		if _, err := q.DeleteLearnDraftsByProject(ctx, string(projectID)); err != nil {
			return err
		}
		if _, err := q.DeleteLearnJobsByProject(ctx, string(projectID)); err != nil {
			return err
		}
		if _, err := q.DeleteLearnCursorsByProject(ctx, string(projectID)); err != nil {
			return err
		}
		if _, err := q.DeletePromptFingerprintsByProject(ctx, string(projectID)); err != nil {
			return err
		}
		_, err = q.DeleteDeliveredFingerprintsByProject(ctx, string(projectID))
		return err
	})
	if err != nil {
		return 0, err
	}
	return turns, nil
}
