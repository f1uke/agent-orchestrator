package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	rt "github.com/aoagents/agent-orchestrator/backend/internal/releasetrain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// LoadReleaseHumanState reads the rule settings, hides and plans Reconcile applies.
func (s *Store) LoadReleaseHumanState(ctx context.Context) (rt.HumanState, error) {
	plans, err := s.ListReleaseLlbPlans(ctx)
	if err != nil {
		return rt.HumanState{}, err
	}
	settings, err := s.qr.ListReleaseRuleSettings(ctx)
	if err != nil {
		return rt.HumanState{}, fmt.Errorf("list release rule settings: %w", err)
	}
	hides, err := s.qr.ListReleaseHitHides(ctx)
	if err != nil {
		return rt.HumanState{}, fmt.Errorf("list release hit hides: %w", err)
	}
	fixes, err := s.qr.ListReleaseFixes(ctx)
	if err != nil {
		return rt.HumanState{}, fmt.Errorf("list release fixes: %w", err)
	}
	human := rt.HumanState{RuleSettings: make(map[rt.RuleID]bool, len(settings)), Hides: make(map[rt.HitID]rt.Hide, len(hides)), Plans: plans,
		OpenFixes: openFixes(fixes)}
	for _, r := range settings {
		human.RuleSettings[rt.RuleID(r.RuleID)] = r.Enabled
	}
	for _, h := range hides {
		sev, ok := rt.ParseSeverity(h.Severity)
		if !ok {
			return rt.HumanState{}, fmt.Errorf("hide %s: unknown severity %q", h.HitID, h.Severity)
		}
		human.Hides[rt.HitID(h.HitID)] = rt.Hide{Fingerprint: h.Evidence, Severity: sev, Reason: h.Reason, HiddenAt: h.HiddenAt.UTC()}
	}
	return human, nil
}

// SetReleaseRuleSetting stores a rule's on/off; nil deletes the row so the
// rule's default applies.
func (s *Store) SetReleaseRuleSetting(ctx context.Context, id rt.RuleID, enabled *bool, at time.Time) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var err error
	if enabled == nil {
		err = s.qw.DeleteReleaseRuleSetting(ctx, string(id))
	} else {
		err = s.qw.UpsertReleaseRuleSetting(ctx, gen.UpsertReleaseRuleSettingParams{RuleID: string(id), Enabled: *enabled, UpdatedAt: at.UTC()})
	}
	if err != nil {
		return fmt.Errorf("set release rule %s: %w", id, err)
	}
	return nil
}

// HideReleaseHit stores or replaces a hide.
func (s *Store) HideReleaseHit(ctx context.Context, id rt.HitID, h rt.Hide) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.qw.UpsertReleaseHitHide(ctx, gen.UpsertReleaseHitHideParams{
		HitID: string(id), Evidence: h.Fingerprint, Severity: h.Severity.String(), Reason: h.Reason, HiddenAt: h.HiddenAt.UTC(),
	}); err != nil {
		return fmt.Errorf("hide release hit %s: %w", id, err)
	}
	return nil
}

// UnhideReleaseHits deletes the hides of ids and returns the ids that had one.
func (s *Store) UnhideReleaseHits(ctx context.Context, ids []rt.HitID) ([]rt.HitID, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	removed := []rt.HitID{}
	err := s.inTx(ctx, "unhide release hits", func(q *gen.Queries) error {
		for _, id := range ids {
			n, err := q.DeleteReleaseHitHide(ctx, string(id))
			if err != nil {
				return fmt.Errorf("%s: %w", id, err)
			}
			if n > 0 {
				removed = append(removed, id)
			}
		}
		return nil
	})
	return removed, err
}

// ListReleaseLlbPlans reads every long-lived-branch plan with its branches and accepted MRs.
func (s *Store) ListReleaseLlbPlans(ctx context.Context) ([]rt.LlbPlan, error) {
	rows, err := s.qr.ListReleaseLlbPlans(ctx)
	if err != nil {
		return nil, fmt.Errorf("list release plans: %w", err)
	}
	branches, err := s.qr.ListReleaseLlbPlanBranches(ctx)
	if err != nil {
		return nil, fmt.Errorf("list release plan branches: %w", err)
	}
	accepted, err := s.qr.ListReleaseLlbPlanAccepted(ctx)
	if err != nil {
		return nil, fmt.Errorf("list release plan accepted MRs: %w", err)
	}
	out := make([]rt.LlbPlan, 0, len(rows))
	index := map[string]int{}
	for _, r := range rows {
		p := rt.LlbPlan{
			ID: rt.PlanID(r.ID), Name: r.Name, TargetKind: rt.TargetKind(r.TargetKind), Note: r.Note,
			Branches: []rt.PlanBranch{}, Accepted: []rt.PlanAccepted{},
			Version: int(r.Version), UpdatedBy: r.UpdatedBy, UpdatedAt: r.UpdatedAt.UTC(),
		}
		if r.TargetTrain.Valid {
			train, ok := rt.ParseTrainKey(r.TargetTrain.String)
			if !ok {
				return nil, fmt.Errorf("plan %s: bad target train %q", r.ID, r.TargetTrain.String)
			}
			p.TargetTrain = &train
		}
		index[r.ID] = len(out)
		out = append(out, p)
	}
	for _, b := range branches {
		p := &out[index[b.PlanID]]
		p.Branches = append(p.Branches, rt.PlanBranch{App: rt.AppID(b.AppID), Branch: b.Branch})
	}
	for _, a := range accepted {
		p := &out[index[a.PlanID]]
		p.Accepted = append(p.Accepted, rt.PlanAccepted{MRRef: a.MrRef, Reason: a.Reason})
	}
	return out, nil
}

// PutReleaseLlbPlan writes p when its stored version is ifVersion (0 creates
// one that does not exist yet) and returns it at the next version. A version
// mismatch is a *rt.PlanVersionError and a branch held by another plan a
// *rt.BranchTakenError; neither writes anything.
func (s *Store) PutReleaseLlbPlan(ctx context.Context, p rt.LlbPlan, ifVersion int) (rt.LlbPlan, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	err := s.inTx(ctx, "put release plan", func(q *gen.Queries) error {
		current, err := q.GetReleaseLlbPlanVersion(ctx, string(p.ID))
		switch {
		case errors.Is(err, sql.ErrNoRows):
			current = 0
		case err != nil:
			return err
		}
		if int(current) != ifVersion {
			return &rt.PlanVersionError{Plan: p.ID, Current: int(current)}
		}
		for _, b := range p.Branches {
			owner, err := q.ReleaseLlbBranchOwner(ctx, gen.ReleaseLlbBranchOwnerParams{AppID: string(b.App), Branch: b.Branch, PlanID: string(p.ID)})
			switch {
			case err == nil:
				return &rt.BranchTakenError{Branch: b, Owner: rt.PlanID(owner)}
			case !errors.Is(err, sql.ErrNoRows):
				return err
			}
		}
		p.Version = int(current) + 1
		train := sql.NullString{}
		if p.TargetTrain != nil {
			train = sql.NullString{String: p.TargetTrain.Key(), Valid: true}
		}
		if err := q.UpsertReleaseLlbPlan(ctx, gen.UpsertReleaseLlbPlanParams{
			ID: string(p.ID), Name: p.Name, TargetKind: string(p.TargetKind), TargetTrain: train, Note: p.Note,
			Version: int64(p.Version), UpdatedBy: p.UpdatedBy, UpdatedAt: p.UpdatedAt.UTC(),
		}); err != nil {
			return err
		}
		if err := q.DeleteReleaseLlbPlanBranches(ctx, string(p.ID)); err != nil {
			return err
		}
		for _, b := range p.Branches {
			if err := q.InsertReleaseLlbPlanBranch(ctx, gen.InsertReleaseLlbPlanBranchParams{PlanID: string(p.ID), AppID: string(b.App), Branch: b.Branch}); err != nil {
				return err
			}
		}
		if err := q.DeleteReleaseLlbPlanAccepted(ctx, string(p.ID)); err != nil {
			return err
		}
		for _, a := range p.Accepted {
			if err := q.InsertReleaseLlbPlanAccepted(ctx, gen.InsertReleaseLlbPlanAcceptedParams{PlanID: string(p.ID), MrRef: a.MRRef, Reason: a.Reason}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return rt.LlbPlan{}, err
	}
	return p, nil
}
