package learning

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/observe/learndecide"
)

// DecideStore is the persistence proposals are read from.
type DecideStore interface {
	ListSkillProposals(ctx context.Context) ([]domain.LearnProposal, error)
	ListAllLearnDrafts(ctx context.Context) ([]domain.LearnDraft, error)
}

// Decider is the decide loop, as the service drives it.
type Decider interface {
	RunNow(ctx context.Context, opts learndecide.RunOpts) error
	Progress() learndecide.Progress
}

// ErrDecideUnavailable is a decide request on a daemon without the stage.
var ErrDecideUnavailable = errors.New("decide is not available")

// ErrUnknownProposal is a proposal id the store does not have.
var ErrUnknownProposal = errors.New("unknown proposal")

// WithDecide wires the decide stage in.
func (s *Service) WithDecide(runCtx context.Context, st DecideStore, d Decider) *Service {
	s.runCtx, s.decideStore, s.decider = runCtx, st, d
	return s
}

// StartDecide starts a decide run now: every ready task of one project (or of
// every learning project), or one task whether it is ready or not.
func (s *Service) StartDecide(ctx context.Context, project, task string, budgetUSD float64) error {
	if s.decider == nil {
		return ErrDecideUnavailable
	}
	if budgetUSD <= 0 || budgetUSD > MaxManualBudgetUSD {
		return fmt.Errorf("%w: budget must be above 0 and at most %.0f", ErrInvalidBudget, MaxManualBudgetUSD)
	}
	if project != "" {
		p, ok, err := s.store.GetProject(ctx, project)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("%w: %s", ErrUnknownProject, project)
		}
		if !p.Config.LearnFromSessions {
			return fmt.Errorf("%w: %s", ErrNotLearning, project)
		}
	}
	return s.decider.RunNow(s.runCtx, learndecide.RunOpts{Project: project, Task: task, BudgetUSD: budgetUSD})
}

// DecideProgress is the decide loop's current or last pass.
func (s *Service) DecideProgress() (learndecide.Progress, error) {
	if s.decider == nil {
		return learndecide.Progress{}, ErrDecideUnavailable
	}
	return s.decider.Progress(), nil
}

// Proposals lists a project's proposals, newest first; pending ones only
// unless all is set. An empty project lists every project's.
func (s *Service) Proposals(ctx context.Context, project domain.ProjectID, all bool) ([]domain.LearnProposal, error) {
	if s.decideStore == nil {
		return nil, ErrDecideUnavailable
	}
	if project != "" {
		if err := s.requireProject(ctx, project); err != nil {
			return nil, err
		}
	}
	rows, err := s.decideStore.ListSkillProposals(ctx)
	if err != nil {
		return nil, err
	}
	var out []domain.LearnProposal
	for _, p := range rows {
		if (project == "" || p.ProjectID == project) && (all || p.Status == domain.LearnProposalPending) {
			out = append(out, p)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out, nil
}

// Proposal returns one proposal and the drafts it rests on.
func (s *Service) Proposal(ctx context.Context, id int64) (domain.LearnProposal, []domain.LearnDraft, error) {
	if s.decideStore == nil {
		return domain.LearnProposal{}, nil, ErrDecideUnavailable
	}
	rows, err := s.decideStore.ListSkillProposals(ctx)
	if err != nil {
		return domain.LearnProposal{}, nil, err
	}
	for _, p := range rows {
		if p.ID != id {
			continue
		}
		drafts, err := s.decideStore.ListAllLearnDrafts(ctx)
		if err != nil {
			return p, nil, err
		}
		want := map[int64]bool{}
		for _, d := range p.EvidenceIDs {
			want[d] = true
		}
		var ev []domain.LearnDraft
		for _, d := range drafts {
			if want[d.ID] {
				ev = append(ev, d)
			}
		}
		return p, ev, nil
	}
	return domain.LearnProposal{}, nil, fmt.Errorf("%w: %d", ErrUnknownProposal, id)
}
