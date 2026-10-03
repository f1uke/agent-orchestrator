package store

import (
	"context"
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
			NewContent: r.NewContent, Diff: r.Diff, Confidence: r.Confidence, Outcome: domain.LearnOutcome(r.Outcome),
			Status: domain.LearnProposalStatus(r.Status), DropReason: r.DropReason, EvidenceIDs: byProposal[r.ID],
			CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
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
					Diff: p.Diff, Confidence: p.Confidence, Outcome: string(p.Outcome), RuleVerdictsJson: string(verdicts),
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
					BaseSha256: p.BaseSHA256, NewContent: p.NewContent, Diff: p.Diff, Confidence: p.Confidence,
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
