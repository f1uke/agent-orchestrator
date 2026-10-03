package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// ListAllLearnDrafts returns every draft of every project, oldest first.
func (s *Store) ListAllLearnDrafts(ctx context.Context) ([]domain.LearnDraft, error) {
	rows, err := s.qr.ListAllLearnDrafts(ctx)
	if err != nil {
		return nil, fmt.Errorf("list all learn drafts: %w", err)
	}
	out := make([]domain.LearnDraft, 0, len(rows))
	for _, r := range rows {
		out = append(out, draftFromRow(gen.ListLearnDraftsByProjectRow(r)))
	}
	return out, nil
}

// ListDecidedTasks returns every task decide has run on, by key.
func (s *Store) ListDecidedTasks(ctx context.Context) (map[string]domain.LearnDecidedTask, error) {
	rows, err := s.qr.ListDecidedTasks(ctx)
	if err != nil {
		return nil, fmt.Errorf("list decided tasks: %w", err)
	}
	out := make(map[string]domain.LearnDecidedTask, len(rows))
	for _, r := range rows {
		out[r.TaskKey] = domain.LearnDecidedTask{TaskKey: r.TaskKey, ProjectID: domain.ProjectID(r.ProjectID),
			Outcome: domain.LearnOutcome(r.Outcome), Proposals: int(r.Proposals), DecidedAt: r.DecidedAt}
	}
	return out, nil
}

// ListSkillProposals returns every proposal with its evidence, oldest first.
func (s *Store) ListSkillProposals(ctx context.Context) ([]domain.LearnProposal, error) {
	rows, err := s.qr.ListSkillProposals(ctx)
	if err != nil {
		return nil, fmt.Errorf("list skill proposals: %w", err)
	}
	ev, err := s.qr.ListSkillProposalEvidence(ctx)
	if err != nil {
		return nil, fmt.Errorf("list skill proposal evidence: %w", err)
	}
	byProposal := map[int64][]int64{}
	for _, e := range ev {
		byProposal[e.ProposalID] = append(byProposal[e.ProposalID], e.DraftID)
	}
	out := make([]domain.LearnProposal, 0, len(rows))
	for _, r := range rows {
		p := domain.LearnProposal{
			ID: r.ID, ProjectID: domain.ProjectID(r.ProjectID), TaskKey: r.TaskKey, Action: domain.LearnProposalAction(r.Action),
			TargetPath: r.TargetPath, Scope: r.Scope, Title: r.Title, Rationale: r.Rationale, BaseSHA256: r.BaseSha256,
			NewContent: r.NewContent, IndexLine: r.IndexLine, Diff: r.Diff, Confidence: r.Confidence, Outcome: domain.LearnOutcome(r.Outcome),
			Status: domain.LearnProposalStatus(r.Status), DropReason: r.DropReason, EvidenceIDs: byProposal[r.ID],
			CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt, RejectReason: r.RejectReason, AppliedSHA256: r.AppliedSha256,
			Resolution: domain.LearnResolution(r.Resolution),
		}
		if r.SnoozedUntil.Valid {
			p.SnoozedUntil = r.SnoozedUntil.Time
		}
		if r.DecidedAt.Valid {
			p.DecidedAt = r.DecidedAt.Time
		}
		if err := json.Unmarshal([]byte(r.RuleVerdictsJson), &p.RuleVerdicts); err != nil {
			return nil, fmt.Errorf("proposal %d verdicts: %w", r.ID, err)
		}
		if err := json.Unmarshal([]byte(r.VerifierJson), &p.Verifier); err != nil {
			return nil, fmt.Errorf("proposal %d verifier: %w", r.ID, err)
		}
		out = append(out, p)
	}
	return out, nil
}

// CommitDecide writes a decided task in one transaction: proposals (new or
// amended), their evidence, the drafts' new statuses and the task's mark.
func (s *Store) CommitDecide(ctx context.Context, res domain.LearnDecideResult, now time.Time) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.inTx(ctx, "commit decide", func(q *gen.Queries) error {
		kept := 0
		for _, p := range res.Proposals {
			verdicts, err := json.Marshal(nonNilVerdicts(p.RuleVerdicts))
			if err != nil {
				return err
			}
			verifier, err := json.Marshal(p.Verifier)
			if err != nil {
				return err
			}
			id := p.ID
			if id == 0 {
				status := p.Status
				if status == "" {
					status = domain.LearnProposalPending
				}
				id, err = q.InsertSkillProposal(ctx, gen.InsertSkillProposalParams{
					ProjectID: string(p.ProjectID), TaskKey: p.TaskKey, Action: string(p.Action), TargetPath: p.TargetPath,
					Scope: p.Scope, Title: p.Title, Rationale: p.Rationale, BaseSha256: p.BaseSHA256, NewContent: p.NewContent,
					IndexLine: p.IndexLine, Diff: p.Diff, Confidence: p.Confidence, Outcome: string(p.Outcome), RuleVerdictsJson: string(verdicts),
					VerifierJson: string(verifier), Status: string(status), DropReason: p.DropReason, CreatedAt: now, UpdatedAt: now,
				})
				if err != nil {
					return fmt.Errorf("insert proposal for %s: %w", p.TargetPath, err)
				}
				if status == domain.LearnProposalPending {
					kept++
				}
			} else {
				if err := q.AmendSkillProposal(ctx, gen.AmendSkillProposalParams{
					TaskKey: p.TaskKey, Action: string(p.Action), Scope: p.Scope, Title: p.Title, Rationale: p.Rationale,
					BaseSha256: p.BaseSHA256, NewContent: p.NewContent, IndexLine: p.IndexLine, Diff: p.Diff, Confidence: p.Confidence,
					Outcome: string(p.Outcome), RuleVerdictsJson: string(verdicts), VerifierJson: string(verifier),
					UpdatedAt: now, ID: id,
				}); err != nil {
					return fmt.Errorf("amend proposal %d: %w", id, err)
				}
				kept++
			}
			for _, d := range p.EvidenceIDs {
				if err := q.InsertSkillProposalEvidence(ctx, gen.InsertSkillProposalEvidenceParams{ProposalID: id, DraftID: d}); err != nil {
					return fmt.Errorf("proposal %d evidence %d: %w", id, d, err)
				}
			}
		}
		for _, d := range res.Consumed {
			if err := q.SetLearnDraftStatusByID(ctx, gen.SetLearnDraftStatusByIDParams{Status: string(domain.LearnDraftConsumed), ID: d}); err != nil {
				return fmt.Errorf("consume draft %d: %w", d, err)
			}
		}
		for _, d := range res.Dropped {
			if err := q.SetLearnDraftStatusByID(ctx, gen.SetLearnDraftStatusByIDParams{Status: string(domain.LearnDraftDropped), ID: d}); err != nil {
				return fmt.Errorf("drop draft %d: %w", d, err)
			}
		}
		return q.UpsertDecidedTask(ctx, gen.UpsertDecidedTaskParams{
			TaskKey: res.TaskKey, ProjectID: string(res.ProjectID), Outcome: string(res.Outcome),
			Proposals: int64(kept), DecidedAt: now,
		})
	})
}

func nonNilVerdicts(v []domain.LearnRuleVerdict) []domain.LearnRuleVerdict {
	if v == nil {
		return []domain.LearnRuleVerdict{}
	}
	return v
}

// SettleLearnProposal records a decision. It reports false when the proposal
// is not pending any more (decided elsewhere, or a second click).
func (s *Store) SettleLearnProposal(ctx context.Context, d domain.LearnSettlement, now time.Time) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	n, err := s.qw.SettleLearnProposal(ctx, gen.SettleLearnProposalParams{
		Status: string(d.Status), RejectReason: d.RejectReason, Resolution: string(d.Resolution), AppliedSha256: d.AppliedSHA256,
		NewContent: d.NewContent, DecidedAt: sql.NullTime{Time: now, Valid: true}, UpdatedAt: now, ID: d.ID,
	})
	if err != nil {
		return false, fmt.Errorf("settle proposal %d: %w", d.ID, err)
	}
	return n > 0, nil
}

// SnoozeLearnProposal hides a pending proposal until a time.
func (s *Store) SnoozeLearnProposal(ctx context.Context, id int64, until, now time.Time) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	n, err := s.qw.SnoozeLearnProposal(ctx, gen.SnoozeLearnProposalParams{SnoozedUntil: sql.NullTime{Time: until, Valid: true}, UpdatedAt: now, ID: id})
	if err != nil {
		return false, fmt.Errorf("snooze proposal %d: %w", id, err)
	}
	return n > 0, nil
}

// StaleLearnProposal marks a proposal whose target changed after it was made,
// and reopens what it rested on so decide proposes again against the file as
// it is now - all at once.
func (s *Store) StaleLearnProposal(ctx context.Context, p domain.LearnProposal, now time.Time) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	changed := false
	err := s.inTx(ctx, "stale proposal", func(q *gen.Queries) error {
		n, err := q.SettleLearnProposal(ctx, gen.SettleLearnProposalParams{
			Status: string(domain.LearnProposalStale), NewContent: p.NewContent,
			DecidedAt: sql.NullTime{Time: now, Valid: true}, UpdatedAt: now, ID: p.ID,
		})
		if err != nil || n == 0 {
			return err
		}
		changed = true
		if err := q.ReopenDraftsOfProposal(ctx, p.ID); err != nil {
			return err
		}
		return q.DeleteDecidedTask(ctx, p.TaskKey)
	})
	if err != nil {
		return false, fmt.Errorf("stale proposal %d: %w", p.ID, err)
	}
	return changed, nil
}

// UpdateLearnProtectedRuleText replaces a pinned rule's text, keeping its
// patterns: the person's newer words won a conflict with it.
func (s *Store) UpdateLearnProtectedRuleText(ctx context.Context, id int64, text string, now time.Time) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	n, err := s.qw.UpdateLearnProtectedRuleText(ctx, gen.UpdateLearnProtectedRuleTextParams{Text: text, UpdatedAt: now, ID: id})
	if err != nil {
		return false, fmt.Errorf("update protected rule %d: %w", id, err)
	}
	return n > 0, nil
}
