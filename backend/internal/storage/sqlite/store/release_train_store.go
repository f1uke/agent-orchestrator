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

var errReleaseResourceMissing = errors.New("release resource is not planned")

// LoadReleaseSnapshot reads every release-train resource with its decoded items.
// A resource whose kind no longer exists is skipped; its row ages out unplanned.
func (s *Store) LoadReleaseSnapshot(ctx context.Context) (rt.Snapshot, []rt.Resource, error) {
	rows, err := s.qr.ListReleaseResources(ctx)
	if err != nil {
		return rt.Snapshot{}, nil, fmt.Errorf("list release resources: %w", err)
	}
	items, err := s.qr.ListReleaseItems(ctx)
	if err != nil {
		return rt.Snapshot{}, nil, fmt.Errorf("list release items: %w", err)
	}
	type key struct{ kind, scope string }
	bodies := map[key][][]byte{}
	for _, it := range items {
		k := key{it.Kind, it.Scope}
		bodies[k] = append(bodies[k], []byte(it.Body))
	}
	var snap rt.Snapshot
	resources := make([]rt.Resource, 0, len(rows))
	for _, r := range rows {
		res := releaseResource(r)
		resources = append(resources, res)
		err := snap.PutEncoded(res.ResourceStatus, bodies[key{r.Kind, r.Scope}])
		switch {
		case errors.Is(err, rt.ErrUnknownKind):
		case err != nil:
			return rt.Snapshot{}, nil, fmt.Errorf("release resource %s %s: %w", r.Kind, r.Scope, err)
		}
	}
	return snap, resources, nil
}

// LoadReleaseResources reads every release-train resource row without its items.
func (s *Store) LoadReleaseResources(ctx context.Context) ([]rt.Resource, error) {
	rows, err := s.qr.ListReleaseResources(ctx)
	if err != nil {
		return nil, fmt.Errorf("list release resources: %w", err)
	}
	out := make([]rt.Resource, 0, len(rows))
	for _, r := range rows {
		out = append(out, releaseResource(r))
	}
	return out, nil
}

func releaseResource(r gen.ReleaseResource) rt.Resource {
	res := rt.Resource{
		ResourceStatus: rt.ResourceStatus{
			Kind: rt.KindName(r.Kind), Scope: rt.Scope(r.Scope), App: rt.AppID(r.AppID.String),
			CompleteAt: timePtr(r.CompleteAt), LastAttemptAt: timePtr(r.LastAttemptAt),
			Failures: int(r.Failures), ErrorClass: rt.ErrorClass(r.ErrorClass.String),
		},
		Mode: rt.SyncMode(r.SyncMode), FactVersion: int(r.FactVersion),
		LastSuccessAt: timePtr(r.LastSuccessAt), ErrorMessage: r.ErrorMessage.String,
		NextAttemptAt: timePtr(r.NextAttemptAt), PlannedAt: r.PlannedAt.UTC(), Generation: r.Generation,
	}
	if r.Cursor.Valid {
		res.Cursor = []byte(r.Cursor.String)
	}
	return res
}

func timePtr(t sql.NullTime) *time.Time {
	if !t.Valid {
		return nil
	}
	u := t.Time.UTC()
	return &u
}

func someTime(t *time.Time) sql.NullTime {
	if t == nil {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: t.UTC(), Valid: true}
}

func someString(s string) sql.NullString { return sql.NullString{String: s, Valid: s != ""} }

// PlanReleaseResources records that a source planned each spec at now. A
// spec whose fact version changed loses its cursor and completeness.
func (s *Store) PlanReleaseResources(ctx context.Context, specs []rt.ResourceSpec, now time.Time) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.inTx(ctx, "plan release resources", func(q *gen.Queries) error {
		for _, sp := range specs {
			version := sp.Version
			if version == 0 {
				version = 1
			}
			if err := q.UpsertReleaseResourceSpec(ctx, gen.UpsertReleaseResourceSpecParams{
				Kind: string(sp.Kind), Scope: string(sp.Scope), AppID: someString(string(sp.App)),
				SyncMode: string(sp.Mode), FactVersion: int64(version), PlannedAt: now.UTC(),
			}); err != nil {
				return fmt.Errorf("%s %s: %w", sp.Kind, sp.Scope, err)
			}
		}
		return nil
	})
}

// WriteReleaseSnapshot replaces a resource's item set with a complete fetch,
// in one transaction with the row, and bumps its generation.
func (s *Store) WriteReleaseSnapshot(ctx context.Context, sp rt.ResourceSpec, items []rt.Item, at time.Time) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.inTx(ctx, "write release snapshot", func(q *gen.Queries) error {
		if err := markFetched(ctx, q, sp, nil, &at, at); err != nil {
			return err
		}
		keep := map[string]bool{}
		for _, it := range items {
			keep[it.Key] = true
		}
		old, err := q.ListReleaseItemKeys(ctx, gen.ListReleaseItemKeysParams{Kind: string(sp.Kind), Scope: string(sp.Scope)})
		if err != nil {
			return err
		}
		for _, k := range old {
			if !keep[k] {
				if err := q.DeleteReleaseItem(ctx, gen.DeleteReleaseItemParams{Kind: string(sp.Kind), Scope: string(sp.Scope), ItemKey: k}); err != nil {
					return err
				}
			}
		}
		return upsertItems(ctx, q, sp, items)
	})
}

// WriteReleasePage upserts one incremental page with its cursor and never
// deletes. complete is set when the page caught the resource up.
func (s *Store) WriteReleasePage(ctx context.Context, sp rt.ResourceSpec, items []rt.Item, cursor []byte, complete *time.Time, at time.Time) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.inTx(ctx, "write release page", func(q *gen.Queries) error {
		if err := markFetched(ctx, q, sp, cursor, complete, at); err != nil {
			return err
		}
		return upsertItems(ctx, q, sp, items)
	})
}

// MarkReleaseFailure records a failed fetch and its backoff; items and
// complete_at stay as they were.
func (s *Store) MarkReleaseFailure(ctx context.Context, sp rt.ResourceSpec, f rt.Failure) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	n, err := s.qw.MarkReleaseResourceFailed(ctx, gen.MarkReleaseResourceFailedParams{
		LastAttemptAt: someTime(&f.At), Failures: int64(f.Failures), ErrorClass: someString(string(f.Class)),
		ErrorMessage: someString(f.Message), NextAttemptAt: someTime(&f.NextAttemptAt),
		Kind: string(sp.Kind), Scope: string(sp.Scope),
	})
	if err != nil {
		return fmt.Errorf("mark %s %s failed: %w", sp.Kind, sp.Scope, err)
	}
	if n == 0 {
		return fmt.Errorf("mark %s %s failed: %w", sp.Kind, sp.Scope, errReleaseResourceMissing)
	}
	return nil
}

// DeleteUnplannedReleaseResources drops resources no source planned since
// before, with their items.
func (s *Store) DeleteUnplannedReleaseResources(ctx context.Context, before time.Time) (int64, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	n, err := s.qw.DeleteUnplannedReleaseResources(ctx, before.UTC())
	if err != nil {
		return 0, fmt.Errorf("delete unplanned release resources: %w", err)
	}
	return n, nil
}

func markFetched(ctx context.Context, q *gen.Queries, sp rt.ResourceSpec, cursor []byte, complete *time.Time, at time.Time) error {
	c := sql.NullString{}
	if cursor != nil {
		c = sql.NullString{String: string(cursor), Valid: true}
	}
	n, err := q.MarkReleaseResourceFetched(ctx, gen.MarkReleaseResourceFetchedParams{
		Cursor: c, CompleteAt: someTime(complete), At: someTime(&at), Kind: string(sp.Kind), Scope: string(sp.Scope),
	})
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("%s %s: %w", sp.Kind, sp.Scope, errReleaseResourceMissing)
	}
	return nil
}

func upsertItems(ctx context.Context, q *gen.Queries, sp rt.ResourceSpec, items []rt.Item) error {
	for _, it := range items {
		if err := q.UpsertReleaseItem(ctx, gen.UpsertReleaseItemParams{
			Kind: string(sp.Kind), Scope: string(sp.Scope), ItemKey: it.Key, Body: string(it.Body),
			SourceUpdatedAt: someTime(it.SourceUpdatedAt),
		}); err != nil {
			return fmt.Errorf("item %s: %w", it.Key, err)
		}
	}
	return nil
}
