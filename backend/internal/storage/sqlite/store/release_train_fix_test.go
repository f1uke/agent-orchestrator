package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	rt "github.com/aoagents/agent-orchestrator/backend/internal/releasetrain"
)

func demoFix(id string) rt.Fix {
	due := rt.Day{Y: 2026, M: 10, D: 8}
	e := rt.EffJiraVersionUpdate{Site: "https://jira.example.com", VersionID: "5001",
		After: rt.VersionFields{Released: true, ReleaseDate: &due}}
	plan := rt.FixPlan{Hit: "E1~v/20-2026/di", Rule: "E1", Title: "Mark iOS 3.4.0 released", Mode: rt.ModeQueue,
		Steps: []rt.FixStep{{Label: "Jira release", Before: "unreleased", After: "released", Effect: e, Request: e.Request()}}, Fingerprint: "fp-1"}
	return rt.Fix{ID: id, Hit: plan.Hit, Rule: plan.Rule, Plan: plan, State: rt.FixDryRun, CreatedAt: humanAt, UpdatedAt: humanAt}
}

func demoEvent(kind rt.FixEventKind) rt.FixEvent {
	return rt.FixEvent{At: humanAt, Actor: "cli", Kind: kind, Detail: json.RawMessage(`{"request":{"method":"PUT"}}`)}
}

func TestReleaseFixRoundTripsWithItsEvents(t *testing.T) {
	s, ctx := newTestStore(t), context.Background()
	want := demoFix("fx-1")
	got, created, err := s.RecordReleaseFix(ctx, want, demoEvent(rt.EventDryRunRecorded))
	if err != nil || !created {
		t.Fatalf("record = %v, created %v", err, created)
	}
	if !reflect.DeepEqual(got.Plan, want.Plan) || got.State != rt.FixDryRun || len(got.Events) != 1 ||
		got.Events[0].Kind != rt.EventDryRunRecorded || string(got.Events[0].Detail) != `{"request":{"method":"PUT"}}` {
		t.Fatalf("stored fix = %+v", got)
	}
	human, err := s.LoadReleaseHumanState(ctx)
	if err != nil || human.OpenFixes["E1~v/20-2026/di"] != (rt.FixRef{ID: "fx-1", State: rt.FixDryRun}) {
		t.Fatalf("open fixes = %+v, %v", human.OpenFixes, err)
	}
}

func TestReleaseFixSecondConfirmReturnsTheLiveOne(t *testing.T) {
	s, ctx := newTestStore(t), context.Background()
	if _, _, err := s.RecordReleaseFix(ctx, demoFix("fx-1"), demoEvent(rt.EventDryRunRecorded)); err != nil {
		t.Fatal(err)
	}
	again, created, err := s.RecordReleaseFix(ctx, demoFix("fx-2"), demoEvent(rt.EventDryRunRecorded))
	if err != nil || created || again.ID != "fx-1" || len(again.Events) != 1 {
		t.Fatalf("second confirm = %+v created %v err %v; want fx-1 back with one event", again, created, err)
	}

	if _, err := s.TransitionReleaseFix(ctx, "fx-1", rt.FixDryRun, rt.FixWithdrawn, demoEvent(rt.EventWithdrawn)); err != nil {
		t.Fatal(err)
	}
	third, created, err := s.RecordReleaseFix(ctx, demoFix("fx-3"), demoEvent(rt.EventDryRunRecorded))
	if err != nil || !created || third.ID != "fx-3" {
		t.Fatalf("confirm after withdraw = %+v created %v err %v; want a new fix", third, created, err)
	}
	all, _ := s.ListReleaseFixes(ctx)
	if len(all) != 2 || all[0].State != rt.FixWithdrawn || len(all[0].Events) != 2 {
		t.Fatalf("fixes = %+v; want the withdrawn one kept with both events", all)
	}
	human, _ := s.LoadReleaseHumanState(ctx)
	if human.OpenFixes["E1~v/20-2026/di"].ID != "fx-3" {
		t.Fatalf("open fix = %+v, want fx-3", human.OpenFixes)
	}
}

func TestReleaseFixTransitionFromALeftStateIsRefused(t *testing.T) {
	s, ctx := newTestStore(t), context.Background()
	if _, _, err := s.RecordReleaseFix(ctx, demoFix("fx-1"), demoEvent(rt.EventDryRunRecorded)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.TransitionReleaseFix(ctx, "fx-1", rt.FixDryRun, rt.FixSuperseded, demoEvent(rt.EventSuperseded)); err != nil {
		t.Fatal(err)
	}
	_, err := s.TransitionReleaseFix(ctx, "fx-1", rt.FixDryRun, rt.FixWithdrawn, demoEvent(rt.EventWithdrawn))
	if !errors.Is(err, rt.ErrFixChanged) {
		t.Fatalf("withdraw of a superseded fix err = %v, want ErrFixChanged", err)
	}
	if _, err := s.TransitionReleaseFix(ctx, "nope", rt.FixDryRun, rt.FixWithdrawn, demoEvent(rt.EventWithdrawn)); !errors.Is(err, rt.ErrNoSuchFix) {
		t.Fatalf("unknown fix err = %v, want ErrNoSuchFix", err)
	}
	got, _ := s.GetReleaseFix(ctx, "fx-1")
	if got.State != rt.FixSuperseded || len(got.Events) != 2 {
		t.Fatalf("fix = %+v; the refused withdraw must append nothing", got)
	}
}
