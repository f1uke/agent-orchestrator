package store_test

import (
	"context"
	"maps"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestSessionTransportRoundTripsAndDefaultsToTerminal(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")

	plain, err := s.CreateSession(ctx, sampleRecord("mer"))
	if err != nil {
		t.Fatal(err)
	}
	chat := sampleRecord("mer")
	chat.Transport = domain.TransportChat
	chatRec, err := s.CreateSession(ctx, chat)
	if err != nil {
		t.Fatal(err)
	}

	got, _, _ := s.GetSession(ctx, plain.ID)
	if got.Transport != domain.TransportTerminal {
		t.Fatalf("unset transport stored as %q, want terminal", got.Transport)
	}
	got, _, _ = s.GetSession(ctx, chatRec.ID)
	if got.Transport != domain.TransportChat {
		t.Fatalf("chat transport stored as %q", got.Transport)
	}
	got.Activity.State = domain.ActivityIdle
	if err := s.UpdateSession(ctx, got); err != nil {
		t.Fatal(err)
	}
	all, _ := s.ListAllSessions(ctx)
	byID := map[domain.SessionID]domain.Transport{}
	for _, r := range all {
		byID[r.ID] = r.Transport
	}
	if byID[plain.ID] != domain.TransportTerminal || byID[chatRec.ID] != domain.TransportChat {
		t.Fatalf("ListAllSessions transports = %v", byID)
	}
}

func TestLiveSessionTransportByRuntimeHandle(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")

	old := sampleRecord("mer")
	old.Metadata.RuntimeHandleID = "mer-feat-x"
	old.IsTerminated = true
	if _, err := s.CreateSession(ctx, old); err != nil {
		t.Fatal(err)
	}
	if _, _, ok, err := s.LiveSessionTransportByRuntimeHandle(ctx, "mer-feat-x"); err != nil || ok {
		t.Fatalf("a terminated row's handle resolved: ok=%v err=%v", ok, err)
	}

	live := sampleRecord("mer")
	live.Metadata.RuntimeHandleID = "mer-feat-x"
	live.Transport = domain.TransportChat
	rec, err := s.CreateSession(ctx, live)
	if err != nil {
		t.Fatal(err)
	}
	id, transport, ok, err := s.LiveSessionTransportByRuntimeHandle(ctx, "mer-feat-x")
	if err != nil || !ok || id != rec.ID || transport != domain.TransportChat {
		t.Fatalf("lookup = %q, %q, %v, %v; want %q chat", id, transport, ok, err, rec.ID)
	}
	if _, _, ok, _ := s.LiveSessionTransportByRuntimeHandle(ctx, "review-mer-1"); ok {
		t.Fatal("an unknown handle resolved to a session")
	}
}

func TestLiveChatSessions(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	create := func(transport domain.Transport, state domain.ActivityState, terminated, suspended bool) domain.SessionID {
		t.Helper()
		r := sampleRecord("mer")
		r.Transport, r.Activity.State, r.IsTerminated, r.IsSuspended = transport, state, terminated, suspended
		rec, err := s.CreateSession(ctx, r)
		if err != nil {
			t.Fatal(err)
		}
		return rec.ID
	}
	waiting := create(domain.TransportChat, domain.ActivityWaitingInput, false, false)
	idle := create(domain.TransportChat, domain.ActivityIdle, false, false)
	create(domain.TransportChat, domain.ActivityIdle, true, false)
	create(domain.TransportChat, domain.ActivityIdle, false, true)
	create(domain.TransportTerminal, domain.ActivityIdle, false, false)

	live, err := s.LiveChatSessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := map[domain.SessionID]domain.ActivityState{waiting: domain.ActivityWaitingInput, idle: domain.ActivityIdle}
	if !maps.Equal(live, want) {
		t.Fatalf("LiveChatSessions = %v, want %v", live, want)
	}
}
