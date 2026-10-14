package releasetrain

import (
	"context"
	"errors"
	"fmt"

	rt "github.com/aoagents/agent-orchestrator/backend/internal/releasetrain"
)

// Errors the human-state commands return for a controller to map.
var (
	ErrUnknownRule = errors.New("release trains: unknown rule")
	ErrUnknownHit  = errors.New("release trains: no such hit on the board")
	ErrNoBoard     = errors.New("release trains: no board until a valid config has loaded")
)

// SetRule stores a rule's on/off, or with nil goes back to the rule's default,
// and returns the rule's catalogue row as it now stands.
func (s *BoardService) SetRule(ctx context.Context, id rt.RuleID, enabled *bool) (rt.RuleInfo, error) {
	if _, ok := rt.LookupRule(id, nil); !ok {
		return rt.RuleInfo{}, fmt.Errorf("%w: %q", ErrUnknownRule, id)
	}
	if err := s.d.Store.SetReleaseRuleSetting(ctx, id, enabled, s.d.Clock()); err != nil {
		return rt.RuleInfo{}, err
	}
	human, err := s.d.Store.LoadReleaseHumanState(ctx)
	if err != nil {
		return rt.RuleInfo{}, err
	}
	info, _ := rt.LookupRule(id, human.RuleSettings)
	return info, nil
}

// Hide hides a hit on the current Board, keeping its evidence fingerprint and
// severity so the Board can tell when it changed or escalated. Hiding a hit
// again replaces the hide with the hit as it is now.
func (s *BoardService) Hide(ctx context.Context, id rt.HitID, reason string) (rt.Hide, error) {
	c, err := s.current(ctx)
	if err != nil {
		return rt.Hide{}, err
	}
	if c.board == nil {
		return rt.Hide{}, ErrNoBoard
	}
	for _, h := range c.board.Alerts.Hits {
		if h.ID == id {
			hide := rt.Hide{Fingerprint: h.Fingerprint(), Severity: h.Severity, Reason: reason, HiddenAt: s.d.Clock().UTC()}
			return hide, s.d.Store.HideReleaseHit(ctx, id, hide)
		}
	}
	return rt.Hide{}, fmt.Errorf("%w: %q", ErrUnknownHit, id)
}

// Unhide removes one hide; a hit that is not hidden is ErrUnknownHit.
func (s *BoardService) Unhide(ctx context.Context, id rt.HitID) error {
	removed, err := s.d.Store.UnhideReleaseHits(ctx, []rt.HitID{id})
	if err != nil {
		return err
	}
	if len(removed) == 0 {
		return fmt.Errorf("%w: %q is not hidden", ErrUnknownHit, id)
	}
	return nil
}

// UnhideStale removes every hide the current Board lists as no longer matching
// and returns their ids.
func (s *BoardService) UnhideStale(ctx context.Context) ([]rt.HitID, error) {
	c, err := s.current(ctx)
	if err != nil {
		return nil, err
	}
	if c.board == nil {
		return []rt.HitID{}, nil
	}
	ids := make([]rt.HitID, len(c.board.Alerts.StaleHides))
	for i, sh := range c.board.Alerts.StaleHides {
		ids[i] = sh.ID
	}
	return s.d.Store.UnhideReleaseHits(ctx, ids)
}

// Plans lists every long-lived-branch plan.
func (s *BoardService) Plans(ctx context.Context) ([]rt.LlbPlan, error) {
	return s.d.Store.ListReleaseLlbPlans(ctx)
}

// PutPlan validates p and writes it when its stored version is ifVersion (0
// creates); see the store for the conflict errors.
func (s *BoardService) PutPlan(ctx context.Context, p rt.LlbPlan, ifVersion int) (rt.LlbPlan, error) {
	if err := p.Validate(); err != nil {
		return rt.LlbPlan{}, err
	}
	p.UpdatedAt = s.d.Clock().UTC()
	return s.d.Store.PutReleaseLlbPlan(ctx, p, ifVersion)
}
