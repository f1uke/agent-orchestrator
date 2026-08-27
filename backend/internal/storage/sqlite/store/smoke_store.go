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
// case with its attached evidence loaded (checklists are small - 3-6 cases - so
// evidence is fetched per case rather than with a join). Retired cases sort
// after the active ones: they are still part of the record, just not part of
// what the user is asked to play.
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
		if err := s.loadSmokeEvidence(ctx, s.qr, &check); err != nil {
			return nil, err
		}
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
	if err := s.loadSmokeEvidence(ctx, s.qr, &check); err != nil {
		return domain.SmokeCheck{}, false, err
	}
	return check, true, nil
}

// ReplaceSmokeChecks upserts a whole checklist by stable case id in one write
// transaction: an id already present has only its authored fields rewritten
// (verdict/note/decided_at/reported_at + evidence rows are preserved), a new id
// is inserted fresh, and an existing id absent from cases is deleted (its
// evidence rows cascade). Returns the removed check ids so the caller can delete
// their on-disk evidence blobs (the store owns rows, not files).
//
// RETIRED cases are invisible to the replace: they are never deleted for being
// absent from the payload (that absence is exactly what retiring one means) and
// never rewritten. A retired case is frozen, so an agent that re-sends its whole
// checklist every round can neither drop nor revive what it retired last round.
func (s *Store) ReplaceSmokeChecks(ctx context.Context, sessionID domain.SessionID, projectID domain.ProjectID, cases []domain.SmokeAuthoredCase, author domain.SmokeAuthor, now time.Time) ([]domain.SmokeCheck, []string, error) {
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
			// Still recorded as present so a same-id insert can never collide with
			// the frozen row, but skipped by the delete sweep below.
			present[row.ID] = struct{}{}
			if row.RetiredAt.Valid {
				continue
			}
			if _, ok := keep[row.ID]; !ok {
				if _, err := q.DeleteSmokeCheck(ctx, row.ID); err != nil {
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
				if _, err := q.UpdateSmokeCheckAuthored(ctx, smokeUpdateAuthoredParams(c, string(steps), c.Seq, author, now)); err != nil {
					return err
				}
				continue
			}
			if err := q.InsertSmokeCheck(ctx, smokeInsertParams(c, sessionID, projectID, string(steps), c.Seq, author, now)); err != nil {
				return err
			}
		}
		// A checklist with cases on it is not a checklist that stood down.
		return q.DeleteSmokeChecklistState(ctx, sessionID)
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

// ResetSmokeCheck clears a case's verdict/note/decided_at and deletes the
// evidence rows the USER attached (one tx), ok=false if the case does not exist.
// Reset is the user re-playing a case, so it clears the user's result only: a
// machine's recorded result and artifacts are not theirs to drop, and wiping
// them here would make the two results one again by the back door.
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
		return q.DeleteUserSmokeEvidenceByCheck(ctx, id)
	})
	if err != nil {
		return false, fmt.Errorf("reset smoke check %s: %w", id, err)
	}
	return reset, nil
}

// SetSmokeAgentResult writes the MACHINE's result for a case - verdict, note,
// when it ran and the commit it ran against - leaving every user-runtime field
// alone. ok=false when the case does not exist OR is retired: a frozen case is
// not one anything is asked to run.
func (s *Store) SetSmokeAgentResult(ctx context.Context, id string, res domain.SmokeAgentResult, ranAt, now time.Time) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	n, err := s.qw.SetSmokeAgentResult(ctx, gen.SetSmokeAgentResultParams{
		AgentVerdict: res.Verdict,
		AgentNote:    res.Note,
		AgentRanAt:   sql.NullTime{Time: ranAt, Valid: true},
		AgentSha:     res.SHA,
		UpdatedAt:    now,
		ID:           id,
	})
	if err != nil {
		return false, fmt.Errorf("set smoke agent result %s: %w", id, err)
	}
	return n > 0, nil
}

// RetireSmokeCheck freezes a case with the reason it went, clearing nothing.
// ok=false when the case does not exist or is already retired (the statement is
// guarded on retired_at IS NULL, so the first reason and date always stand).
func (s *Store) RetireSmokeCheck(ctx context.Context, id, reason string, retiredAt, now time.Time) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	n, err := s.qw.RetireSmokeCheck(ctx, gen.RetireSmokeCheckParams{
		RetiredAt:     sql.NullTime{Time: retiredAt, Valid: true},
		RetiredReason: reason,
		UpdatedAt:     now,
		ID:            id,
	})
	if err != nil {
		return false, fmt.Errorf("retire smoke check %s: %w", id, err)
	}
	return n > 0, nil
}

// ListUserSmokeEvidence returns the evidence rows the USER attached to a case -
// exactly the rows ResetSmokeCheck deletes, so the caller can remove those blobs
// and leave the machine's alone.
func (s *Store) ListUserSmokeEvidence(ctx context.Context, checkID string) ([]domain.SmokeEvidence, error) {
	rows, err := s.qr.ListUserSmokeEvidenceByCheck(ctx, checkID)
	if err != nil {
		return nil, fmt.Errorf("list user smoke evidence for check %s: %w", checkID, err)
	}
	out := make([]domain.SmokeEvidence, 0, len(rows))
	for _, row := range rows {
		out = append(out, smokeEvidenceFromRow(row))
	}
	return out, nil
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
		Source:    evidenceSourceOrUser(ev.Source),
	})
}

// DeleteSmokeEvidence removes one evidence row by id, returning ok=false when no
// row matched (already gone). The on-disk blob is removed by the service.
func (s *Store) DeleteSmokeEvidence(ctx context.Context, id string) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	n, err := s.qw.DeleteSmokeEvidence(ctx, id)
	if err != nil {
		return false, fmt.Errorf("delete smoke evidence %s: %w", id, err)
	}
	return n > 0, nil
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

// loadSmokeEvidence fills a case's TWO evidence lists, split by provenance: what
// the user attached while playing it, and what a machine attached while running
// it. The split happens here, at the only read path, so no consumer can mix them
// up even by ignoring the source field on the row.
func (s *Store) loadSmokeEvidence(ctx context.Context, q *gen.Queries, check *domain.SmokeCheck) error {
	rows, err := q.ListSmokeEvidenceByCheck(ctx, check.ID)
	if err != nil {
		return fmt.Errorf("list smoke evidence for check %s: %w", check.ID, err)
	}
	check.Evidence = []domain.SmokeEvidence{}
	check.AgentEvidence = []domain.SmokeEvidence{}
	for _, row := range rows {
		ev := smokeEvidenceFromRow(row)
		if ev.Source == domain.SmokeEvidenceAgent {
			check.AgentEvidence = append(check.AgentEvidence, ev)
			continue
		}
		check.Evidence = append(check.Evidence, ev)
	}
	return nil
}

// ListSmokeEvidenceCreatedBefore returns every evidence row whose created_at
// predates the cutoff, across all sessions — the age-based retention sweep's
// read side. Ordered oldest-first.
func (s *Store) ListSmokeEvidenceCreatedBefore(ctx context.Context, before time.Time) ([]domain.SmokeEvidence, error) {
	rows, err := s.qr.ListSmokeEvidenceCreatedBefore(ctx, before)
	if err != nil {
		return nil, fmt.Errorf("list smoke evidence before %s: %w", before.Format(time.RFC3339), err)
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
		ID:             r.ID,
		SessionID:      r.SessionID,
		ProjectID:      r.ProjectID,
		Seq:            int(r.Seq),
		Name:           r.Name,
		Why:            r.Why,
		Steps:          steps,
		Expected:       r.Expected,
		PRNum:          int(r.PRNum),
		FileRef:        r.FileRef,
		Verdict:        r.Verdict,
		Note:           r.Note,
		Evidence:       []domain.SmokeEvidence{},
		DecidedAt:      nullTimePtr(r.DecidedAt),
		AgentVerdict:   r.AgentVerdict,
		AgentNote:      r.AgentNote,
		AgentEvidence:  []domain.SmokeEvidence{},
		AgentRanAt:     nullTimePtr(r.AgentRanAt),
		AgentSHA:       r.AgentSha,
		RetiredAt:      nullTimePtr(r.RetiredAt),
		RetiredReason:  r.RetiredReason,
		AuthoredBy:     r.AuthoredBy,
		AuthoredByRole: r.AuthoredByRole,
		AuthoredAt:     nullTimePtr(r.AuthoredAt),
		ReportedAt:     nullTimePtr(r.ReportedAt),
		CreatedAt:      r.CreatedAt,
		UpdatedAt:      r.UpdatedAt,
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
		Source:    evidenceSourceOrUser(r.Source),
	}
}

// evidenceSourceOrUser reads a stored provenance value, defaulting an empty one
// to the user. Rows written before provenance existed carry ” only if the
// column default was bypassed; either way "the user attached it" is the truth
// for every row that predates `ao smoke record`.
func evidenceSourceOrUser(src domain.SmokeEvidenceSource) domain.SmokeEvidenceSource {
	if src == domain.SmokeEvidenceAgent {
		return domain.SmokeEvidenceAgent
	}
	return domain.SmokeEvidenceUser
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

// UpsertSmokeChecks writes 1..N cases into a session's checklist WITHOUT
// touching any case the payload does not name. It is the write path shared
// authorship rests on: `ReplaceSmokeChecks` sets the list, so two members
// writing it in turn erase each other's cases (and, but for the guard above it,
// the human's verdicts); this one sets only what its caller named, so two
// members adding different cases at the same moment both survive.
//
// A named id that already exists has its AUTHORED fields rewritten in place, so
// the user's verdict, note and evidence ride through untouched. A new id is
// appended after the highest seq in the checklist, retired cases included: a
// "CHECK N" the user has already been shown must not come back meaning a
// different case.
func (s *Store) UpsertSmokeChecks(ctx context.Context, sessionID domain.SessionID, projectID domain.ProjectID, cases []domain.SmokeAuthoredCase, author domain.SmokeAuthor, now time.Time) ([]domain.SmokeCheck, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	err := s.inTx(ctx, "upsert smoke checks", func(q *gen.Queries) error {
		existing, err := q.ListSmokeChecksBySession(ctx, sessionID)
		if err != nil {
			return err
		}
		present := make(map[string]struct{}, len(existing))
		nextSeq := 0
		for _, row := range existing {
			present[row.ID] = struct{}{}
			if int(row.Seq) > nextSeq {
				nextSeq = int(row.Seq)
			}
		}
		for _, c := range cases {
			steps, err := json.Marshal(stepsOrEmpty(c.Steps))
			if err != nil {
				return fmt.Errorf("encode steps for %s: %w", c.ID, err)
			}
			if _, ok := present[c.ID]; ok {
				// Keep the case where the user already saw it: re-sending a case is
				// editing it, not re-filing it under a new number.
				seq, err := existingSeq(existing, c.ID)
				if err != nil {
					return err
				}
				if _, err := q.UpdateSmokeCheckAuthored(ctx, smokeUpdateAuthoredParams(c, string(steps), seq, author, now)); err != nil {
					return err
				}
				continue
			}
			nextSeq++
			if err := q.InsertSmokeCheck(ctx, smokeInsertParams(c, sessionID, projectID, string(steps), nextSeq, author, now)); err != nil {
				return err
			}
			present[c.ID] = struct{}{}
		}
		// A checklist with cases on it is not a checklist that stood down.
		return q.DeleteSmokeChecklistState(ctx, sessionID)
	})
	if err != nil {
		return nil, fmt.Errorf("upsert smoke checks for session %s: %w", sessionID, err)
	}
	return s.ListSmokeChecksBySession(ctx, sessionID)
}

// PatchSmokeCheckAuthored rewrites only the authored fields the patch names,
// inside one transaction so a read-modify-write cannot lose a field to a
// concurrent edit of a DIFFERENT field on the same case. ok=false when the case
// does not exist.
func (s *Store) PatchSmokeCheckAuthored(ctx context.Context, id string, patch domain.SmokeCasePatch, author domain.SmokeAuthor, now time.Time) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var found bool
	err := s.inTx(ctx, "patch smoke check", func(q *gen.Queries) error {
		row, err := q.GetSmokeCheck(ctx, id)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		found = true
		check, err := smokeCheckFromRow(row)
		if err != nil {
			return err
		}
		merged := applySmokePatch(check, patch)
		steps, err := json.Marshal(stepsOrEmpty(merged.Steps))
		if err != nil {
			return fmt.Errorf("encode steps for %s: %w", id, err)
		}
		_, err = q.UpdateSmokeCheckAuthored(ctx, smokeUpdateAuthoredParams(merged, string(steps), check.Seq, author, now))
		return err
	})
	if err != nil {
		return false, fmt.Errorf("patch smoke check %s: %w", id, err)
	}
	return found, nil
}

// DeleteSmokeCheck removes ONE case outright, ok=false when no row matched. The
// caller is responsible for refusing to call it on a case the user has played -
// that one is retired, with a reason, never deleted.
func (s *Store) DeleteSmokeCheck(ctx context.Context, id string) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	n, err := s.qw.DeleteSmokeCheck(ctx, id)
	if err != nil {
		return false, fmt.Errorf("delete smoke check %s: %w", id, err)
	}
	return n > 0, nil
}

// GetSmokeChecklistStandDown returns a session's recorded "nothing here needs a
// human" claim, ok=false when none stands.
func (s *Store) GetSmokeChecklistStandDown(ctx context.Context, sessionID domain.SessionID) (domain.SmokeStandDown, bool, error) {
	row, err := s.qr.GetSmokeChecklistState(ctx, sessionID)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.SmokeStandDown{}, false, nil
	}
	if err != nil {
		return domain.SmokeStandDown{}, false, fmt.Errorf("get smoke checklist state for session %s: %w", sessionID, err)
	}
	return domain.SmokeStandDown{
		SessionID: row.SessionID,
		At:        row.StoodDownAt,
		By:        row.StoodDownBy,
		ByRole:    row.StoodDownByRole,
		Reason:    row.Reason,
		CreatedAt: row.CreatedAt,
		UpdatedAt: row.UpdatedAt,
	}, true, nil
}

// SetSmokeChecklistStandDown records (or re-states) the claim that this task's
// change needs no human verification.
func (s *Store) SetSmokeChecklistStandDown(ctx context.Context, sessionID domain.SessionID, reason string, author domain.SmokeAuthor, now time.Time) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.qw.UpsertSmokeChecklistState(ctx, gen.UpsertSmokeChecklistStateParams{
		SessionID:       sessionID,
		StoodDownAt:     now,
		StoodDownBy:     author.ID,
		StoodDownByRole: smokeAuthorRole(author),
		Reason:          reason,
		CreatedAt:       now,
		UpdatedAt:       now,
	}); err != nil {
		return fmt.Errorf("set smoke checklist stand-down for session %s: %w", sessionID, err)
	}
	return nil
}

// ClearSmokeChecklistStandDown retracts the claim.
func (s *Store) ClearSmokeChecklistStandDown(ctx context.Context, sessionID domain.SessionID) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.qw.DeleteSmokeChecklistState(ctx, sessionID); err != nil {
		return fmt.Errorf("clear smoke checklist stand-down for session %s: %w", sessionID, err)
	}
	return nil
}

// applySmokePatch overlays the fields a patch names onto a stored case; a nil
// field leaves the stored value exactly as it was.
func applySmokePatch(check domain.SmokeCheck, patch domain.SmokeCasePatch) domain.SmokeAuthoredCase {
	out := domain.SmokeAuthoredCase{
		ID:       check.ID,
		Seq:      check.Seq,
		Name:     check.Name,
		Why:      check.Why,
		Steps:    check.Steps,
		Expected: check.Expected,
		PRNum:    check.PRNum,
		FileRef:  check.FileRef,
	}
	if patch.Name != nil {
		out.Name = *patch.Name
	}
	if patch.Why != nil {
		out.Why = *patch.Why
	}
	if patch.Steps != nil {
		out.Steps = *patch.Steps
	}
	if patch.Expected != nil {
		out.Expected = *patch.Expected
	}
	if patch.PRNum != nil {
		out.PRNum = *patch.PRNum
	}
	if patch.FileRef != nil {
		out.FileRef = *patch.FileRef
	}
	return out
}

// existingSeq finds a case's stored position so an upsert keeps it.
func existingSeq(rows []gen.SmokeCheck, id string) (int, error) {
	for _, row := range rows {
		if row.ID == id {
			return int(row.Seq), nil
		}
	}
	return 0, fmt.Errorf("smoke check %s vanished mid-transaction", id)
}

func smokeInsertParams(c domain.SmokeAuthoredCase, sessionID domain.SessionID, projectID domain.ProjectID, steps string, seq int, author domain.SmokeAuthor, now time.Time) gen.InsertSmokeCheckParams {
	return gen.InsertSmokeCheckParams{
		ID:             c.ID,
		SessionID:      sessionID,
		ProjectID:      projectID,
		Seq:            int64(seq),
		Name:           c.Name,
		Why:            c.Why,
		Steps:          steps,
		Expected:       c.Expected,
		PRNum:          int64(c.PRNum),
		FileRef:        c.FileRef,
		CreatedAt:      now,
		UpdatedAt:      now,
		AuthoredBy:     author.ID,
		AuthoredByRole: smokeAuthorRole(author),
		AuthoredAt:     smokeAuthoredAt(author, now),
	}
}

func smokeUpdateAuthoredParams(c domain.SmokeAuthoredCase, steps string, seq int, author domain.SmokeAuthor, now time.Time) gen.UpdateSmokeCheckAuthoredParams {
	return gen.UpdateSmokeCheckAuthoredParams{
		Seq:            int64(seq),
		Name:           c.Name,
		Why:            c.Why,
		Steps:          steps,
		Expected:       c.Expected,
		PRNum:          int64(c.PRNum),
		FileRef:        c.FileRef,
		UpdatedAt:      now,
		ID:             c.ID,
		AuthoredBy:     author.ID,
		AuthoredByRole: smokeAuthorRole(author),
		AuthoredAt:     smokeAuthoredAt(author, now),
	}
}

// smokeAuthorRole stores only a role AO actually knows. A caller it cannot
// identify - the desktop app, a direct API call, an older `ao` - is written as
// no author at all, because a guessed one is worse than none on a list whose
// whole point is telling two authors apart.
func smokeAuthorRole(author domain.SmokeAuthor) domain.CrewRole {
	if !author.Role.Valid() {
		return ""
	}
	return author.Role
}

// smokeAuthoredAt stamps the write only when it can be attributed: an
// authored_at with no author beside it would read as a fact about a person.
func smokeAuthoredAt(author domain.SmokeAuthor, now time.Time) sql.NullTime {
	if author.ID == "" {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: now, Valid: true}
}
