package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	rt "github.com/aoagents/agent-orchestrator/backend/internal/releasetrain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// RecordReleaseFix stores f with its first event, unless the hit already has
// a live fix for the same plan fingerprint: then it returns that one and
// created is false, so a double confirm records nothing twice.
func (s *Store) RecordReleaseFix(ctx context.Context, f rt.Fix, ev rt.FixEvent) (rt.Fix, bool, error) {
	plan, err := json.Marshal(f.Plan)
	if err != nil {
		return rt.Fix{}, false, fmt.Errorf("encode fix plan: %w", err)
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var id string
	created := false
	err = s.inTx(ctx, "record release fix", func(q *gen.Queries) error {
		live, err := q.GetLiveReleaseFix(ctx, gen.GetLiveReleaseFixParams{HitID: string(f.Hit), PlanFingerprint: f.Plan.Fingerprint})
		switch {
		case err == nil:
			id = live.ID
			return nil
		case !errors.Is(err, sql.ErrNoRows):
			return err
		}
		if err := q.InsertReleaseFix(ctx, gen.InsertReleaseFixParams{
			ID: f.ID, HitID: string(f.Hit), RuleID: string(f.Rule), Choice: f.Choice, Plan: string(plan),
			PlanFingerprint: f.Plan.Fingerprint, State: string(f.State), CreatedAt: f.CreatedAt.UTC(), UpdatedAt: f.UpdatedAt.UTC(),
		}); err != nil {
			return err
		}
		id, created = f.ID, true
		return insertFixEvent(ctx, q, f.ID, ev)
	})
	if err != nil {
		return rt.Fix{}, false, err
	}
	got, err := s.GetReleaseFix(ctx, id)
	return got, created, err
}

// TransitionReleaseFix moves fix id from one state to another and appends ev,
// in one transaction. A fix no longer in from is rt.ErrFixChanged.
func (s *Store) TransitionReleaseFix(ctx context.Context, id string, from, to rt.FixState, ev rt.FixEvent) (rt.Fix, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	err := s.inTx(ctx, "transition release fix", func(q *gen.Queries) error {
		n, err := q.SetReleaseFixState(ctx, gen.SetReleaseFixStateParams{State: string(to), UpdatedAt: ev.At.UTC(), ID: id, State_2: string(from)})
		if err != nil {
			return err
		}
		if n == 0 {
			if _, err := q.GetReleaseFix(ctx, id); errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("%w: %q", rt.ErrNoSuchFix, id)
			}
			return fmt.Errorf("%w: %q is no longer %s", rt.ErrFixChanged, id, from)
		}
		return insertFixEvent(ctx, q, id, ev)
	})
	if err != nil {
		return rt.Fix{}, err
	}
	return s.GetReleaseFix(ctx, id)
}

func insertFixEvent(ctx context.Context, q *gen.Queries, id string, ev rt.FixEvent) error {
	detail := ev.Detail
	if len(detail) == 0 {
		detail = json.RawMessage("{}")
	}
	return q.InsertReleaseFixEvent(ctx, gen.InsertReleaseFixEventParams{
		FixID: id, At: ev.At.UTC(), Actor: ev.Actor, Kind: string(ev.Kind), Detail: string(detail),
	})
}

// LiveReleaseFix finds the hit's live fix for one plan fingerprint.
func (s *Store) LiveReleaseFix(ctx context.Context, hit rt.HitID, fingerprint string) (rt.Fix, bool, error) {
	row, err := s.qr.GetLiveReleaseFix(ctx, gen.GetLiveReleaseFixParams{HitID: string(hit), PlanFingerprint: fingerprint})
	if errors.Is(err, sql.ErrNoRows) {
		return rt.Fix{}, false, nil
	}
	if err != nil {
		return rt.Fix{}, false, fmt.Errorf("get live release fix: %w", err)
	}
	f, err := s.GetReleaseFix(ctx, row.ID)
	return f, err == nil, err
}

// GetReleaseFix reads one fix with its events, or rt.ErrNoSuchFix.
func (s *Store) GetReleaseFix(ctx context.Context, id string) (rt.Fix, error) {
	row, err := s.qr.GetReleaseFix(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return rt.Fix{}, fmt.Errorf("%w: %q", rt.ErrNoSuchFix, id)
	}
	if err != nil {
		return rt.Fix{}, fmt.Errorf("get release fix %s: %w", id, err)
	}
	fixes, err := s.withEvents(ctx, []gen.ReleaseFix{row})
	if err != nil {
		return rt.Fix{}, err
	}
	return fixes[0], nil
}

// ListReleaseFixes reads every fix with its events, oldest first.
func (s *Store) ListReleaseFixes(ctx context.Context) ([]rt.Fix, error) {
	rows, err := s.qr.ListReleaseFixes(ctx)
	if err != nil {
		return nil, fmt.Errorf("list release fixes: %w", err)
	}
	return s.withEvents(ctx, rows)
}

func (s *Store) withEvents(ctx context.Context, rows []gen.ReleaseFix) ([]rt.Fix, error) {
	events, err := s.qr.ListReleaseFixEvents(ctx)
	if err != nil {
		return nil, fmt.Errorf("list release fix events: %w", err)
	}
	byFix := map[string][]rt.FixEvent{}
	for _, e := range events {
		byFix[e.FixID] = append(byFix[e.FixID], rt.FixEvent{
			Seq: e.Seq, At: e.At.UTC(), Actor: e.Actor, Kind: rt.FixEventKind(e.Kind), Detail: json.RawMessage(e.Detail),
		})
	}
	out := make([]rt.Fix, 0, len(rows))
	for _, r := range rows {
		var plan rt.FixPlan
		if err := json.Unmarshal([]byte(r.Plan), &plan); err != nil {
			return nil, fmt.Errorf("fix %s: decode plan: %w", r.ID, err)
		}
		evs := byFix[r.ID]
		if evs == nil {
			evs = []rt.FixEvent{}
		}
		out = append(out, rt.Fix{
			ID: r.ID, Hit: rt.HitID(r.HitID), Rule: rt.RuleID(r.RuleID), Choice: r.Choice, Plan: plan,
			State: rt.FixState(r.State), CreatedAt: r.CreatedAt.UTC(), UpdatedAt: r.UpdatedAt.UTC(), Events: evs,
		})
	}
	return out, nil
}

func openFixes(rows []gen.ReleaseFix) map[rt.HitID]rt.FixRef {
	out := map[rt.HitID]rt.FixRef{}
	for _, r := range rows {
		if rt.FixState(r.State).Live() {
			out[rt.HitID(r.HitID)] = rt.FixRef{ID: r.ID, State: rt.FixState(r.State)}
		}
	}
	return out
}
