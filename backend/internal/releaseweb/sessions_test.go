package releaseweb

import (
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)

func TestSessionsExpireOnIdleAndAbsoluteTTL(t *testing.T) {
	s := newSessionStore()
	idle, _ := s.create(1, t0)
	busy, _ := s.create(1, t0)

	// Touch busy every 6h; idle never comes back.
	now := t0
	for now.Add(6 * time.Hour).Before(t0.Add(SessionAbsoluteTTL)) {
		now = now.Add(6 * time.Hour)
		if got := s.lookup(busy, 1, now); got != sessionLive {
			t.Fatalf("active session dropped after %v", now.Sub(t0))
		}
	}
	if got := s.lookup(idle, 1, now); got != sessionMissing {
		t.Errorf("idle session still live after %v", now.Sub(t0))
	}
	if got := s.lookup(busy, 1, t0.Add(SessionAbsoluteTTL)); got != sessionMissing {
		t.Error("active session outlived the absolute TTL")
	}
}

func TestSessionFromAnotherKeyGenerationIsRotated(t *testing.T) {
	s := newSessionStore()
	tok, _ := s.create(1, t0)
	if got := s.lookup(tok, 2, t0); got != sessionRotated {
		t.Fatalf("lookup under generation 2 = %v, want rotated", got)
	}
	if got := s.lookup(tok, 1, t0); got != sessionMissing {
		t.Errorf("rotated session came back: %v", got)
	}

	tok, _ = s.create(2, t0)
	s.rotate()
	if got := s.lookup(tok, 2, t0); got != sessionRotated {
		t.Errorf("after rotate = %v, want rotated", got)
	}
	if s.count(t0) != 0 {
		t.Error("rotate left sessions behind")
	}
}

func TestSessionStoreIsBoundedAndEvictsTheLeastRecentlySeen(t *testing.T) {
	s := newSessionStore()
	first, _ := s.create(1, t0)
	for i := 1; i < MaxSessions; i++ {
		if _, err := s.create(1, t0.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	later := t0.Add(time.Hour)
	if s.lookup(first, 1, later) != sessionLive {
		t.Fatal("first session not live before the store filled")
	}
	if _, err := s.create(1, later); err != nil {
		t.Fatal(err)
	}
	if got := s.count(later); got != MaxSessions {
		t.Errorf("count = %d, want %d", got, MaxSessions)
	}
	if s.lookup(first, 1, later) != sessionLive {
		t.Error("evicted the recently used session instead of the least recently seen")
	}
}
