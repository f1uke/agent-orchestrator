package releaseweb

import (
	"os"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)

var keyA, keyB = idOf("key a"), idOf("key b")

func TestSessionsExpireOnIdleAndAbsoluteTTL(t *testing.T) {
	s := newSessionStore()
	idle, _ := s.create(keyA, "203.0.113.9", t0)
	busy, _ := s.create(keyA, "203.0.113.9", t0)

	now := t0
	for now.Add(6 * time.Hour).Before(t0.Add(SessionAbsoluteTTL)) {
		now = now.Add(6 * time.Hour)
		if got := s.lookup(busy, keyA, now); got != sessionLive {
			t.Fatalf("active session dropped after %v", now.Sub(t0))
		}
	}
	if got := s.lookup(idle, keyA, now); got != sessionMissing {
		t.Errorf("idle session still live after %v", now.Sub(t0))
	}
	if got := s.lookup(busy, keyA, t0.Add(SessionAbsoluteTTL)); got != sessionMissing {
		t.Error("active session outlived the absolute TTL")
	}
}

func TestSessionFromAnotherKeyIsRotated(t *testing.T) {
	s := newSessionStore()
	tok, _ := s.create(keyA, "203.0.113.9", t0)
	if got := s.lookup(tok, keyB, t0); got != sessionRotated {
		t.Fatalf("lookup under another key = %v, want rotated", got)
	}
	if got := s.lookup(tok, keyA, t0); got != sessionMissing {
		t.Errorf("rotated session came back: %v", got)
	}

	tok, _ = s.create(keyB, "203.0.113.9", t0)
	s.rotate()
	if got := s.lookup(tok, keyB, t0); got != sessionRotated {
		t.Errorf("after rotate = %v, want rotated", got)
	}
	if s.count(t0) != 0 {
		t.Error("rotate left sessions behind")
	}
}

func TestSignInThatRacedARotationIsNotLiveUnderTheNewKey(t *testing.T) {
	p := Paths{Dir: t.TempDir()}
	if err := RotateKey(p); err != nil {
		t.Fatal(err)
	}
	old, err := ReadKey(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(p.KeyFile()); err != nil {
		t.Fatal(err)
	}
	if err := RotateKey(p); err != nil {
		t.Fatal(err)
	}
	next, err := ReadKey(p)
	if err != nil {
		t.Fatal(err)
	}
	if next.Generation != old.Generation {
		t.Fatalf("generations %d and %d; the case under test is a repeated generation", old.Generation, next.Generation)
	}

	s := newSessionStore()
	s.rotate()
	tok, err := s.create(old.id, "203.0.113.9", t0)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.lookup(tok, next.id, t0); got == sessionLive {
		t.Fatal("a session minted under the old key is live under the new one")
	}
}

func TestAFullStoreEvictsTheSigningInClientsOwnSessionsFirst(t *testing.T) {
	s := newSessionStore()
	teammate, err := s.create(keyA, "192.0.2.1", t0)
	if err != nil {
		t.Fatal(err)
	}
	const churner = "203.0.113.9"
	for i := 1; i < MaxSessions; i++ {
		if _, err := s.create(keyA, churner, t0.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	later := t0.Add(time.Hour)
	for range 50 {
		if _, err := s.create(keyA, churner, later); err != nil {
			t.Fatal(err)
		}
	}
	if got := s.count(later); got != MaxSessions {
		t.Errorf("count = %d, want %d", got, MaxSessions)
	}
	if s.lookup(teammate, keyA, later) != sessionLive {
		t.Error("one client signing in over and over evicted another client's session")
	}
	if _, err := s.create(keyA, "198.51.100.3", later); err != nil {
		t.Fatal(err)
	}
	if s.lookup(teammate, keyA, later) != sessionLive {
		t.Error("a new client evicted the most recently used session instead of the least recently seen")
	}
}

func TestSessionStoreIsBoundedAndEvictsTheLeastRecentlySeen(t *testing.T) {
	s := newSessionStore()
	first, _ := s.create(keyA, "203.0.113.9", t0)
	for i := 1; i < MaxSessions; i++ {
		if _, err := s.create(keyA, "203.0.113.9", t0.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	later := t0.Add(time.Hour)
	if s.lookup(first, keyA, later) != sessionLive {
		t.Fatal("first session not live before the store filled")
	}
	if _, err := s.create(keyA, "203.0.113.9", later); err != nil {
		t.Fatal(err)
	}
	if got := s.count(later); got != MaxSessions {
		t.Errorf("count = %d, want %d", got, MaxSessions)
	}
	if s.lookup(first, keyA, later) != sessionLive {
		t.Error("evicted the recently used session instead of the least recently seen")
	}
}
