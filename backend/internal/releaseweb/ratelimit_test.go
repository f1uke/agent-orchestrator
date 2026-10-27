package releaseweb

import (
	"testing"
	"time"
)

func fail(l *signinLimiter, client string, now time.Time) {
	l.attempt(client, now, func() bool { return false })
}

func lockedFor(l *signinLimiter, client string, now time.Time) time.Duration {
	_, wait := l.attempt(client, now, func() bool { return true })
	return wait
}

func TestClientLockoutDoublesUpToTheCap(t *testing.T) {
	l := newSigninLimiter()
	now := t0
	var got []time.Duration
	want := []time.Duration{5 * time.Minute, 10 * time.Minute, 20 * time.Minute, 40 * time.Minute, 80 * time.Minute,
		160 * time.Minute, 320 * time.Minute, 640 * time.Minute, 1280 * time.Minute, 24 * time.Hour, 24 * time.Hour}
	for range want {
		for range SigninFailuresPerClient {
			fail(l, "203.0.113.9", now)
		}
		wait := lockedFor(l, "203.0.113.9", now)
		got = append(got, wait)
		now = now.Add(wait)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("lockout %d = %v, want %v", i+1, got[i], want[i])
		}
	}

	now = now.Add(SigninMaxClientLockout)
	for range SigninFailuresPerClient {
		fail(l, "203.0.113.9", now)
	}
	if wait := lockedFor(l, "203.0.113.9", now); wait != SigninClientLockout {
		t.Errorf("after a quiet day the lockout is %v, want it back at %v", wait, SigninClientLockout)
	}
}

func TestClientTableIsBounded(t *testing.T) {
	l := newSigninLimiter()
	locked := "198.51.100.1"
	for range SigninFailuresPerClient {
		fail(l, locked, t0)
	}
	for i := range maxTrackedClients + 50 {
		fail(l, string(rune(0x4e00+i)), t0.Add(time.Second))
	}
	if n := len(l.clients); n > maxTrackedClients {
		t.Errorf("client table holds %d entries, cap %d", n, maxTrackedClients)
	}
	if lockedFor(l, locked, t0.Add(time.Minute)) == 0 {
		t.Error("eviction dropped a locked-out client while unlocked ones were available")
	}
}
