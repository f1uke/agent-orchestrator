package releasetrain

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	rt "github.com/aoagents/agent-orchestrator/backend/internal/releasetrain"
)

// Errors the fix commands return for a controller to map.
var (
	ErrNothingToRecord = errors.New("release trains: this plan has nothing to record")
	// ErrNoUndo is an undo of a fix whose state has none.
	ErrNoUndo = errors.New("release trains: this fix cannot be undone")
)

// PlanChangedError is a confirm whose fingerprint is not the current plan's.
type PlanChangedError struct{ Plan rt.FixPlan }

func (e *PlanChangedError) Error() string {
	return "the data behind this fix changed; review the new plan and confirm again"
}

// FixStore is what the fix commands need from storage.
type FixStore interface {
	RecordReleaseFix(ctx context.Context, f rt.Fix, ev rt.FixEvent) (rt.Fix, bool, error)
	TransitionReleaseFix(ctx context.Context, id string, from, to rt.FixState, ev rt.FixEvent) (rt.Fix, error)
	LiveReleaseFix(ctx context.Context, hit rt.HitID, fingerprint string) (rt.Fix, bool, error)
	GetReleaseFix(ctx context.Context, id string) (rt.Fix, error)
	ListReleaseFixes(ctx context.Context) ([]rt.Fix, error)
}

// Executor applies one effect kind and returns the effect that reverts it.
// Only AO-owned effects have one; every other kind is recorded as a dry run.
type Executor func(ctx context.Context, e rt.Effect, actor string) (undo rt.Effect, err error)

// Executors maps the effect kinds that really apply to how.
type Executors map[rt.EffectKind]Executor

func (x Executors) cover(steps []rt.FixStep) bool {
	for _, s := range steps {
		if x[s.Effect.Kind()] == nil {
			return false
		}
	}
	return len(steps) > 0
}

// UsePlanTargetExecutor makes L3 fixes apply to AO's own plans through PutPlan.
func (s *BoardService) UsePlanTargetExecutor() {
	if s.d.Executors == nil {
		s.d.Executors = Executors{}
	}
	s.d.Executors[rt.EffectAOPlanTarget] = PlanTargetExecutor(s.PutPlan, s.Plans)
}

// PlanTargetExecutor applies an L3 effect through PutPlan, refused when the
// plan is no longer at the effect's PlanVersion.
func PlanTargetExecutor(put func(ctx context.Context, p rt.LlbPlan, ifVersion int) (rt.LlbPlan, error), plans func(ctx context.Context) ([]rt.LlbPlan, error)) Executor {
	return func(ctx context.Context, eff rt.Effect, actor string) (rt.Effect, error) {
		e, ok := eff.(rt.EffAOPlanTarget)
		if !ok {
			return nil, fmt.Errorf("plan target executor given %s", eff.Kind())
		}
		all, err := plans(ctx)
		if err != nil {
			return nil, err
		}
		var p *rt.LlbPlan
		for i := range all {
			if all[i].ID == e.Plan {
				p = &all[i]
			}
		}
		if p == nil {
			return nil, &rt.PlanVersionError{Plan: e.Plan}
		}
		next := *p
		next.TargetKind, next.TargetTrain, next.UpdatedBy = e.After.Kind, e.After.Train, actor
		put, err := put(ctx, next, e.PlanVersion)
		if err != nil {
			return nil, err
		}
		return rt.EffAOPlanTarget{Plan: e.Plan, Before: e.After, After: e.Before, PlanVersion: put.Version}, nil
	}
}

// PlanFix plans hit's fix against the current Board; it writes nothing.
func (s *BoardService) PlanFix(ctx context.Context, hit rt.HitID, choice string) (rt.FixPlan, error) {
	c, err := s.current(ctx)
	if err != nil {
		return rt.FixPlan{}, err
	}
	if c.board == nil {
		return rt.FixPlan{}, ErrNoBoard
	}
	return rt.PlanFix(*c.board, hit, choice)
}

type stepDetail struct {
	Label   string          `json:"label"`
	Effect  rt.EffectKind   `json:"effect"`
	Request rt.ShownRequest `json:"request"`
}

func shown(steps []rt.FixStep) []stepDetail {
	out := make([]stepDetail, len(steps))
	for i, st := range steps {
		out[i] = stepDetail{Label: st.Label, Effect: st.Effect.Kind(), Request: st.Request}
	}
	return out
}

func detail(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return b
}

// ConfirmFix records hit's fix when fingerprint is still the plan's. A
// second confirm of the same plan returns the stored fix. A plan whose every
// step has an executor applies; any other is recorded as a dry run.
func (s *BoardService) ConfirmFix(ctx context.Context, hit rt.HitID, choice, fingerprint, actor string) (rt.Fix, error) {
	if f, ok, err := s.d.Fixes.LiveReleaseFix(ctx, hit, fingerprint); err != nil || ok {
		return f, err
	}
	plan, err := s.PlanFix(ctx, hit, choice)
	if err != nil {
		return rt.Fix{}, err
	}
	if plan.Fingerprint != fingerprint {
		return rt.Fix{}, &PlanChangedError{Plan: plan}
	}
	if !plan.Recordable() {
		return rt.Fix{}, fmt.Errorf("%w: a %s plan", ErrNothingToRecord, plan.Mode)
	}
	apply := s.d.Executors.cover(plan.Steps)
	act := rt.ActRecordDryRun
	if apply {
		act = rt.ActStartApply
	}
	state, kind, err := rt.Transition("", act)
	if err != nil {
		return rt.Fix{}, err
	}
	now := s.d.Clock().UTC()
	f := rt.Fix{ID: newFixID(), Hit: plan.Hit, Rule: plan.Rule, Choice: plan.Choice, Plan: plan, State: state, CreatedAt: now, UpdatedAt: now}
	ev := rt.FixEvent{At: now, Actor: actor, Kind: kind, Detail: detail(map[string]any{"dryRun": !apply, "steps": shown(plan.Steps)})}
	f, created, err := s.d.Fixes.RecordReleaseFix(ctx, f, ev)
	if err != nil || !created || !apply {
		return f, err
	}
	return s.apply(ctx, f, actor)
}

func (s *BoardService) apply(ctx context.Context, f rt.Fix, actor string) (rt.Fix, error) {
	var undo []rt.Effect
	var failure error
	for _, st := range f.Plan.Steps {
		u, err := s.d.Executors[st.Effect.Kind()](ctx, st.Effect, actor)
		if err != nil {
			failure = err
			break
		}
		undo = append(undo, u)
	}
	act, body := rt.ActApplyOK, map[string]any{"undo": undo}
	if failure != nil {
		act, body = rt.ActApplyFail, map[string]any{"error": failure.Error()}
	}
	return s.move(ctx, f, act, actor, body)
}

func (s *BoardService) move(ctx context.Context, f rt.Fix, act rt.FixAction, actor string, body any) (rt.Fix, error) {
	to, kind, err := rt.Transition(f.State, act)
	if err != nil {
		return rt.Fix{}, err
	}
	return s.d.Fixes.TransitionReleaseFix(ctx, f.ID, f.State, to, rt.FixEvent{At: s.d.Clock().UTC(), Actor: actor, Kind: kind, Detail: detail(body)})
}

// Fixes lists every fix with its audit events.
func (s *BoardService) Fixes(ctx context.Context) ([]rt.Fix, error) {
	return s.d.Fixes.ListReleaseFixes(ctx)
}

// UndoFix withdraws a dry run, or reverts an applied fix through the effects
// its apply recorded. A revert refused because the data moved on keeps the
// fix applied with an undo_failed event, and returns the error.
func (s *BoardService) UndoFix(ctx context.Context, id, actor string) (rt.Fix, error) {
	f, err := s.d.Fixes.GetReleaseFix(ctx, id)
	if err != nil {
		return rt.Fix{}, err
	}
	act, ok := rt.UndoAction(f.State)
	if !ok {
		return rt.Fix{}, fmt.Errorf("%w: it is %s", ErrNoUndo, f.State)
	}
	if act == rt.ActWithdraw {
		return s.move(ctx, f, act, actor, map[string]any{})
	}
	undo, err := undoEffects(f)
	if err != nil {
		return rt.Fix{}, err
	}
	for i := len(undo) - 1; i >= 0; i-- {
		x := s.d.Executors[undo[i].Kind()]
		if x == nil {
			err = fmt.Errorf("no executor for %s", undo[i].Kind())
		} else {
			_, err = x(ctx, undo[i], actor)
		}
		if err != nil {
			if _, merr := s.move(ctx, f, rt.ActUndoFail, actor, map[string]any{"error": err.Error()}); merr != nil {
				return rt.Fix{}, merr
			}
			return rt.Fix{}, err
		}
	}
	return s.move(ctx, f, rt.ActUndoOK, actor, map[string]any{"steps": len(undo)})
}

func undoEffects(f rt.Fix) ([]rt.Effect, error) {
	for i := len(f.Events) - 1; i >= 0; i-- {
		if f.Events[i].Kind != rt.EventApplied {
			continue
		}
		var body struct {
			Undo []json.RawMessage `json:"undo"`
		}
		if err := json.Unmarshal(f.Events[i].Detail, &body); err != nil {
			return nil, fmt.Errorf("fix %s: read undo: %w", f.ID, err)
		}
		out := make([]rt.Effect, len(body.Undo))
		for j, raw := range body.Undo {
			e, err := rt.DecodeEffect(raw)
			if err != nil {
				return nil, err
			}
			out[j] = e
		}
		return out, nil
	}
	return nil, fmt.Errorf("fix %s has no applied event to undo", f.ID)
}

type ruleCell struct {
	rule rt.RuleID
	app  rt.AppID
}

// SupersedeFixes marks open dry runs superseded when their hit has left the
// Board: no fired outcome lists it and the rule's cell for the hit's app (or
// its one global cell) is not unknown, so a cold start supersedes nothing.
func (s *BoardService) SupersedeFixes(ctx context.Context) error {
	c, err := s.current(ctx)
	if err != nil || c.board == nil {
		return err
	}
	fired, unsure := map[rt.HitID]bool{}, map[ruleCell]bool{}
	for _, o := range c.board.Alerts.Rules {
		switch out := o.Outcome.(type) {
		case rt.Fired:
			for _, h := range out.Hits {
				fired[h.ID] = true
			}
		case rt.Unknown:
			unsure[ruleCell{o.Rule, o.App}] = true
		}
	}
	fixes, err := s.d.Fixes.ListReleaseFixes(ctx)
	if err != nil {
		return err
	}
	for _, f := range fixes {
		if f.State != rt.FixDryRun || fired[f.Hit] || unsure[ruleCell{f.Rule, f.Plan.App}] {
			continue
		}
		if _, err := s.move(ctx, f, rt.ActSupersede, "poller", map[string]any{"reason": "the hit left the Board"}); err != nil && !errors.Is(err, rt.ErrFixChanged) {
			return err
		}
	}
	return nil
}

func newFixID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "fx-" + hex.EncodeToString(b[:])
}
