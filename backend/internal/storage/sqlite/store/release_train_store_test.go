package store_test

import (
	"context"
	"testing"
	"time"

	rt "github.com/aoagents/agent-orchestrator/backend/internal/releasetrain"
)

func mrItem(t *testing.T, iid int, title string) rt.Item {
	t.Helper()
	it, err := rt.EncodeItem(rt.KindMRs, rt.MRFact{Project: 1001, IID: iid, Title: title, CreatedOn: rt.Day{Y: 2026, M: 10, D: 1}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return it
}

func tagItem(t *testing.T, name string) rt.Item {
	t.Helper()
	it, err := rt.EncodeItem(rt.KindTags, rt.TagFact{Project: 1001, Name: name, CommitOn: rt.Day{Y: 2026, M: 10, D: 1}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return it
}

func titles(s rt.Snapshot) []string {
	var out []string
	for _, mr := range rt.Items(s, rt.KindMRs, rt.ProjectScope(1001)) {
		out = append(out, mr.Title)
	}
	return out
}

func tagNames(s rt.Snapshot) []string {
	var out []string
	for _, tag := range rt.Items(s, rt.KindTags, rt.ProjectScope(1001)) {
		out = append(out, tag.Name)
	}
	return out
}

func resourceOf(t *testing.T, rs []rt.Resource, kind rt.KindName) rt.Resource {
	t.Helper()
	for _, r := range rs {
		if r.Kind == kind {
			return r
		}
	}
	t.Fatalf("no %s resource in %+v", kind, rs)
	return rt.Resource{}
}

func TestReleaseTrainObservationRoundTrips(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	t0 := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	mrs := rt.ResourceSpec{Kind: rt.KindMRs.Name, Scope: rt.ProjectScope(1001), App: "di", Mode: rt.Incremental}
	tags := rt.ResourceSpec{Kind: rt.KindTags.Name, Scope: rt.ProjectScope(1001), App: "di", Mode: rt.Snapshotted}
	if err := s.PlanReleaseResources(ctx, []rt.ResourceSpec{mrs, tags}, t0); err != nil {
		t.Fatal(err)
	}

	if err := s.WriteReleasePage(ctx, mrs, []rt.Item{mrItem(t, 1, "one"), mrItem(t, 2, "two")}, []byte(`{"page":2}`), nil, t0); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteReleasePage(ctx, mrs, []rt.Item{mrItem(t, 2, "two again")}, []byte(`{"page":3}`), &t0, t0.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteReleaseSnapshot(ctx, tags, []rt.Item{tagItem(t, "1.0.0"), tagItem(t, "1.1.0")}, t0); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteReleaseSnapshot(ctx, tags, []rt.Item{tagItem(t, "1.1.0")}, t0.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	snap, rows, err := s.LoadReleaseSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := titles(snap); len(got) != 2 || got[0] != "one" || got[1] != "two again" {
		t.Fatalf("an incremental page must upsert and never delete: %v", got)
	}
	if got := tagNames(snap); len(got) != 1 || got[0] != "1.1.0" {
		t.Fatalf("a complete snapshot must drop absent items: %v", got)
	}
	mr := resourceOf(t, rows, rt.KindMRs.Name)
	if string(mr.Cursor) != `{"page":3}` || mr.CompleteAt == nil || !mr.CompleteAt.Equal(t0) || mr.Generation != 2 || mr.App != "di" || mr.Mode != rt.Incremental {
		t.Fatalf("mrs row = %+v cursor %s", mr, mr.Cursor)
	}
	if st, ok := snap.Status(rt.KindMRs.Name, rt.ProjectScope(1001)); !ok || st.CompleteAt == nil {
		t.Fatalf("snapshot status = %+v %v", st, ok)
	}

	next := t0.Add(5 * time.Minute)
	if err := s.MarkReleaseFailure(ctx, tags, rt.Failure{At: t0.Add(2 * time.Minute), Failures: 2, Class: rt.ErrRateLimited, Message: "slow down", NextAttemptAt: next}); err != nil {
		t.Fatal(err)
	}
	snap, rows, err = s.LoadReleaseSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	tr := resourceOf(t, rows, rt.KindTags.Name)
	if tr.ErrorClass != rt.ErrRateLimited || tr.ErrorMessage != "slow down" || tr.Failures != 2 || tr.NextAttemptAt == nil || !tr.NextAttemptAt.Equal(next) ||
		tr.Generation != 3 || tr.CompleteAt == nil || !tr.CompleteAt.Equal(t0.Add(time.Minute)) {
		t.Fatalf("a failure must keep complete_at and bump generation: %+v", tr)
	}
	if got := tagNames(snap); len(got) != 1 {
		t.Fatalf("a failure must keep the items: %v", got)
	}

	if err := s.PlanReleaseResources(ctx, []rt.ResourceSpec{mrs}, t0.Add(25*time.Hour)); err != nil {
		t.Fatal(err)
	}
	n, err := s.DeleteUnplannedReleaseResources(ctx, t0.Add(time.Hour))
	if err != nil || n != 1 {
		t.Fatalf("deleted %d, %v; want the unplanned tags resource", n, err)
	}
	snap, rows, err = s.LoadReleaseSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || len(tagNames(snap)) != 0 || len(titles(snap)) != 2 {
		t.Fatalf("after delete rows=%d tags=%v mrs=%v; the cascade must drop the tags", len(rows), tagNames(snap), titles(snap))
	}
}

func TestReleaseTrainFactVersionBumpRestartsTheResource(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	t0 := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	mrs := rt.ResourceSpec{Kind: rt.KindMRs.Name, Scope: rt.ProjectScope(1001), Mode: rt.Incremental}
	if err := s.PlanReleaseResources(ctx, []rt.ResourceSpec{mrs}, t0); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteReleasePage(ctx, mrs, []rt.Item{mrItem(t, 1, "one")}, []byte(`{"page":2}`), &t0, t0); err != nil {
		t.Fatal(err)
	}
	mrs.Version = 2
	if err := s.PlanReleaseResources(ctx, []rt.ResourceSpec{mrs}, t0.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	_, rows, err := s.LoadReleaseSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if rows[0].Cursor != nil || rows[0].CompleteAt != nil || rows[0].FactVersion != 2 {
		t.Fatalf("after a version bump = %+v cursor %s, want no cursor and not complete", rows[0], rows[0].Cursor)
	}
}

func TestReleaseTrainWriteToAnUnplannedResourceFails(t *testing.T) {
	s := newTestStore(t)
	spec := rt.ResourceSpec{Kind: rt.KindTags.Name, Scope: rt.ProjectScope(9), Mode: rt.Snapshotted}
	err := s.WriteReleaseSnapshot(context.Background(), spec, []rt.Item{tagItem(t, "1.0.0")}, time.Now())
	if err == nil {
		t.Fatal("a write to a resource no plan created succeeded")
	}
}
