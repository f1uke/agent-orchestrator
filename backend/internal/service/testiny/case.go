package testiny

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type caseRead struct {
	at     time.Time
	detail domain.TestinyCaseDetail
}

// Case reads one case in full: its test data, precondition and steps. The case
// must be in a run linked to the task, so this reads what the task is about
// and is not a way into the rest of Testiny. A case read in the last minute is
// served from memory.
func (s *Service) Case(ctx context.Context, task domain.SessionID, id int64) (domain.TestinyCaseDetail, error) {
	if _, err := s.config(ctx, task); err != nil {
		return domain.TestinyCaseDetail{}, err
	}
	if err := s.caseInTask(ctx, task, id); err != nil {
		return domain.TestinyCaseDetail{}, err
	}
	s.mu.Lock()
	c, ok := s.cases[id]
	s.mu.Unlock()
	if ok && s.now().Sub(c.at) < caseTTL {
		return c.detail, nil
	}
	detail, err := s.reader.Case(ctx, id)
	if err != nil {
		return domain.TestinyCaseDetail{}, err
	}
	s.mu.Lock()
	s.cases[id] = caseRead{at: s.now(), detail: detail}
	s.mu.Unlock()
	return detail, nil
}

// caseInTask looks for the case in the remembered read of each linked run,
// then reads again each run whose read is older than runTTL. A run Testiny
// cannot read leaves the answer open, so its error wins over "not in task".
func (s *Service) caseInTask(ctx context.Context, task domain.SessionID, id int64) error {
	links, err := s.links.ListTestinyRunLinks(ctx, task)
	if err != nil {
		return err
	}
	var old []domain.TestinyRunID
	for _, l := range links {
		if s.runHasCase(l.RunID, id) {
			return nil
		}
		if !s.fresh(l.RunID) {
			old = append(old, l.RunID)
		}
	}
	var readErr error
	for _, run := range old {
		fresh, err := s.fetchByID(ctx, run)
		s.remember(run, fresh, err)
		if err != nil {
			if readErr == nil {
				readErr = err
			}
			continue
		}
		if s.runHasCase(run, id) {
			return nil
		}
	}
	if readErr != nil {
		return readErr
	}
	return fmt.Errorf("%w: TC-%d is in none of the runs linked to %s (ao testiny runs %s)", ErrCaseNotInTask, id, task, task)
}

func (s *Service) runHasCase(run domain.TestinyRunID, id int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.ContainsFunc(s.reads[run].Cases, func(c domain.TestinyCaseResult) bool { return c.ID == id })
}
