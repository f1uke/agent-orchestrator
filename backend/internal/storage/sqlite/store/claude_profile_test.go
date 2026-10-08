package store_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestSessionClaudeProfileRoundTripsAndSurvivesAFullRowUpdate(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	rec := sampleRecord("mer")
	rec.ClaudeProfile = "OmniRoute"
	r, err := s.CreateSession(ctx, rec)
	if err != nil {
		t.Fatal(err)
	}
	got, _, _ := s.GetSession(ctx, r.ID)
	if got.ClaudeProfile != "OmniRoute" || got.RestartPending {
		t.Fatalf("inserted row = profile %q pending %v, want OmniRoute, false", got.ClaudeProfile, got.RestartPending)
	}

	if ok, err := s.SetSessionClaudeProfile(ctx, r.ID, "Work", time.Now()); err != nil || !ok {
		t.Fatalf("SetSessionClaudeProfile = %v, %v", ok, err)
	}
	if ok, err := s.SetSessionRestartPending(ctx, r.ID, true, time.Now()); err != nil || !ok {
		t.Fatalf("SetSessionRestartPending = %v, %v", ok, err)
	}
	stale := got
	stale.Activity.State = domain.ActivityIdle
	if err := s.UpdateSession(ctx, stale); err != nil {
		t.Fatal(err)
	}
	got, _, _ = s.GetSession(ctx, r.ID)
	if got.ClaudeProfile != "Work" || !got.RestartPending {
		t.Fatalf("after a full-row write from a stale read: profile %q pending %v, want Work, true", got.ClaudeProfile, got.RestartPending)
	}
	all, _ := s.ListAllSessions(ctx)
	if len(all) != 1 || all[0].ClaudeProfile != "Work" || !all[0].RestartPending {
		t.Fatalf("ListAllSessions = %+v, want the profile and the pending flag", all)
	}

	if ok, err := s.SetSessionClaudeProfile(ctx, "mer-404", "Work", time.Now()); err != nil || ok {
		t.Fatalf("unknown session: %v, %v; want not applied", ok, err)
	}
}

func TestSessionClaudeProfileAndRestartPendingEachFireCDC(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	r, _ := s.CreateSession(ctx, sampleRecord("mer"))

	lastUpdate := func(after int64) (int, map[string]any) {
		t.Helper()
		evs, err := s.EventsAfter(ctx, after, 100)
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		var payload map[string]any
		for _, e := range evs {
			if string(e.Type) != "session_updated" {
				continue
			}
			n++
			payload = nil
			if err := json.Unmarshal([]byte(e.Payload), &payload); err != nil {
				t.Fatal(err)
			}
		}
		return n, payload
	}

	base, _ := s.LatestSeq(ctx)
	_, _ = s.SetSessionClaudeProfile(ctx, r.ID, "OmniRoute", time.Now())
	n, payload := lastUpdate(base)
	if n != 1 || payload["claudeProfile"] != "OmniRoute" {
		t.Fatalf("profile change: %d events, payload %v; want 1 with claudeProfile OmniRoute", n, payload)
	}

	base, _ = s.LatestSeq(ctx)
	_, _ = s.SetSessionRestartPending(ctx, r.ID, true, time.Now())
	n, payload = lastUpdate(base)
	if n != 1 || payload["restartPending"] != true {
		t.Fatalf("pending flip: %d events, payload %v; want 1 with restartPending true", n, payload)
	}

	base, _ = s.LatestSeq(ctx)
	_, _ = s.SetSessionRestartPending(ctx, r.ID, true, time.Now())
	if n, _ := lastUpdate(base); n != 0 {
		t.Fatalf("an unchanged pending flag fired %d events, want 0", n)
	}
}
